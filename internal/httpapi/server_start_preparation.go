package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"log"
	"net/http"
	"time"
)

func (s *Server) startPreparationClaimCurrent(ctx context.Context, claimed store.ClaimedStreamStart) bool {
	stream, err := s.streams.GetStream(ctx, claimed.Stream.ID)
	if err != nil || stream.Status != "starting" {
		return false
	}
	data, err := json.Marshal(stream)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != claimed.OwnershipClaim.StreamIdentity {
		return false
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return false
	}
	primary := primaryStreamAssignments(assignments)
	if len(primary) != len(claimed.PrimaryAssignments) {
		return false
	}
	for _, want := range claimed.PrimaryAssignments {
		found := false
		for _, got := range primary {
			if got.ServiceID == want.ServiceID && got.ServiceType == want.ServiceType && got.CurrentStreamID == stream.ID && got.PublicURL == want.PublicURL {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (s *Server) finishPreparedStreamStart(w http.ResponseWriter, r *http.Request, claimed store.ClaimedStreamStart, req servicecall.StartRequest, results []servicecall.DispatchResult, relayStatic bool) {
	p := req.StartPreparation
	fail := func(code string) { s.failPreparedStreamStart(w, r, claimed, req, results, relayStatic, code) }
	if hasDispatchFailure(results) {
		fail("service_dispatch_failed")
		return
	}
	if s.videoCovers == nil || req.VideoCoverStart == nil || p.CheckClaim == nil || !p.EncoderCommitted || p.WorkerGeneration == 0 || !p.BotStarted || !p.CheckClaim(r.Context()) {
		fail("start_preparation_final_witness_unknown")
		return
	}
	if err := s.ensureYouTubeBroadcastLive(r.Context(), req.YouTubeRuntime); err != nil {
		fail("youtube_live_start_failed")
		return
	}
	if !p.CheckClaim(r.Context()) {
		fail("start_preparation_claim_unknown")
		return
	}
	if _, err := s.videoCovers.RecordStartApplied(r.Context(), claimed.Stream.ID, req.VideoCoverStart.JobGeneration, req.VideoCoverStart.Active, req.VideoCoverStart.Revision); err != nil {
		fail("record_video_cover_start_failed")
		return
	}
	if media, ok := s.streams.(store.StreamMediaRuntimeStore); ok {
		if err := media.SetStreamVideoOverlayBurnIn(r.Context(), claimed.Stream.ID, true); err != nil {
			fail("save_stream_media_runtime_failed")
			return
		}
	}
	if !p.CheckClaim(r.Context()) {
		fail("start_preparation_claim_unknown")
		return
	}
	s.completeStreamStart(w, r, claimed.Stream, claimed.PrimaryAssignments, req, results)
}
func (s *Server) failPreparedStreamStart(w http.ResponseWriter, r *http.Request, claimed store.ClaimedStreamStart, req servicecall.StartRequest, results []servicecall.DispatchResult, relayStatic bool, code string) {
	p := req.StartPreparation
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	// No remote request was possible on a failed pre-dispatch validation.
	safe := p.Cleanup == nil && !p.EncoderPrepared && !p.WorkerAttempted
	if p.Cleanup != nil {
		safe = p.Cleanup(ctx)
	}
	stream := claimed.Stream
	safe = safe && p.CheckClaim != nil && p.CheckClaim(ctx)
	if safe {
		claimStore := s.streams.(store.StreamStartClaimStore)
		changed, transitioned, err := claimStore.TransitionClaimedStreamStart(ctx, claimed.OwnershipClaim, "failed")
		safe = err == nil && transitioned
		if safe {
			stream = changed
		}
	}
	if !safe {
		code = "start_preparation_ownership_unknown"
	} else if relayStatic {
		_, _ = s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(ctx, stream.ID, "youtube_relay_static_start_dispatch_unconfirmed")
	} else {
		_, _ = s.completeYouTubeRuntime(ctx, stream.ID, true)
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"reason": code, "start_id": p.StartID, "dispatch": sanitizeDispatchResults(results), "cleanup_confirmed": safe, "stop_dispatch": sanitizeDispatchResults(p.CleanupResults)}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
	log.Printf("cp start: event=abort_end stream_id=%s start_id=%s cleanup_confirmed=%t", stream.ID, p.StartID, safe)
	writeJSON(w, http.StatusBadGateway, map[string]any{"code": code, "stream": stream, "dispatch": sanitizeDispatchResults(results), "stop_dispatch": sanitizeDispatchResults(p.CleanupResults), "cleanup_confirmed": safe})
}
