package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"log"
	"net/http"
	"strings"
	"time"
)

func archiveRunIDForStart(startedAt time.Time) string {
	return store.StreamArchiveRunIDForStart(startedAt)
}

func (s *Server) applyCaptionDispatchConfig(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.CaptionProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileCaption, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errCaptionProfileNotFound
	}
	if err != nil {
		return err
	}
	req.CaptionAudioFlushMS = configInt(profile.Config, "caption_audio_flush_ms")
	if req.CaptionAudioFlushMS < 10 || req.CaptionAudioFlushMS > 1000 {
		req.CaptionAudioFlushMS = 100
	}
	req.CaptionAudioMaxBatchPackets = configInt(profile.Config, "caption_audio_max_batch_packets")
	if req.CaptionAudioMaxBatchPackets < 1 || req.CaptionAudioMaxBatchPackets > 100 {
		req.CaptionAudioMaxBatchPackets = 5
	}
	req.UnresolvedSSRCBufferMS = configInt(profile.Config, "unresolved_ssrc_buffer_ms")
	if req.UnresolvedSSRCBufferMS < 0 || req.UnresolvedSSRCBufferMS > 5000 {
		req.UnresolvedSSRCBufferMS = 1000
	}
	return nil
}

// ensureYouTubeBroadcastLive handles the provider transition after a
// successful Encoder dispatch. AutoStart-enabled immediate broadcasts are
// initially left for YouTube to move directly into live after it observes
// ingest; the durable notification outbox polls that lifecycle and performs
// one fenced reconciliation if the provider remains at liveStarting.
// Explicit auto-start=false retains the operator-controlled transition path.
// Scheduled broadcasts remain under YouTube's own schedule.
func (s *Server) ensureYouTubeBroadcastLive(ctx context.Context, runtime map[string]any) error {
	if strings.ToLower(strings.TrimSpace(mapString(runtime, "mode"))) != "live_api" {
		return nil
	}
	if scheduledStart, ok := youtubeRuntimeScheduledStart(runtime); ok && scheduledStart.After(time.Now().UTC()) {
		return nil
	}
	// With YouTube AutoStart enabled, the provider must observe the Encoder
	// ingest before it can move the Broadcast through testing/into live. Do not
	// race that provider transition synchronously from the start request. The
	// durable Discord notification outbox polls lifecycle and delivers only
	// after YouTube reports live. Explicit auto-start=false keeps the legacy
	// operator-controlled transition path below.
	if mapBoolDefault(runtime, "enable_auto_start", true) {
		return nil
	}
	transitionClient, ok := s.youtubeLive.(ytlive.BroadcastTransitionClient)
	if !ok {
		// Keep narrow test doubles and older integrations source-compatible. The
		// production LiveAPIClient implements this optional capability.
		return nil
	}
	broadcastID := strings.TrimSpace(mapString(runtime, "broadcast_id"))
	oauthAccountID := strings.TrimSpace(mapString(runtime, "oauth_account_id"))
	if broadcastID == "" || oauthAccountID == "" {
		return errYouTubeLiveAPIStartFailed
	}
	credentials, err := s.youtubeOAuthCredentials(ctx, oauthAccountID)
	if err != nil {
		return err
	}
	transitionCtx, cancel := context.WithTimeout(ctx, youtubeLiveTransitionTimeout)
	defer cancel()
	request := ytlive.BroadcastTransitionRequest{Credentials: credentials, BroadcastID: broadcastID}
	var lastErr error
	for attempt := 0; attempt < youtubeLiveTransitionAttempts; attempt++ {
		if err := transitionClient.TransitionBroadcastLive(transitionCtx, request); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if lifecycleClient, ok := s.youtubeLive.(ytlive.BroadcastLifecycleClient); ok {
			lifecycle, lifecycleErr := lifecycleClient.BroadcastLifecycle(transitionCtx, ytlive.BroadcastLifecycleRequest{Credentials: credentials, BroadcastID: broadcastID})
			if lifecycleErr == nil && strings.EqualFold(strings.TrimSpace(lifecycle), "live") {
				return nil
			}
		}
		if attempt+1 < youtubeLiveTransitionAttempts {
			delay := time.Duration(1<<attempt) * 500 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-transitionCtx.Done():
				timer.Stop()
				lastErr = transitionCtx.Err()
				attempt = youtubeLiveTransitionAttempts
			case <-timer.C:
			}
		}
	}
	log.Printf("youtube live api broadcast transition failed: broadcast_id=%s error_type=%T", broadcastID, lastErr)
	return errYouTubeLiveAPIStartFailed
}

// failYouTubeLiveAPIStart prevents a provider Broadcast from being left
// scheduled/live after the Encoder accepted the stream but YouTube could not
// be moved to live. Downstream stop and provider completion use a detached,
// bounded context so a cancelled Discord auto-start request cannot abandon
// cleanup.
func (s *Server) failYouTubeLiveAPIStart(w http.ResponseWriter, r *http.Request, stream store.Stream, assignments []store.RegisteredService, ownership store.StreamStartOwnershipClaim, dispatch []servicecall.DispatchResult, transitionErr error) {
	log.Printf("youtube live api start failed: stream_id=%s error_type=%T", stream.ID, transitionErr)
	claimStore, ok := s.streams.(store.StreamStartClaimStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_start_claim_unavailable"})
		return
	}
	failed, transitioned, err := claimStore.TransitionClaimedStreamStart(r.Context(), ownership, "failed")
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !transitioned {
		s.writeStartSupersededResponse(w, r, failed, dispatch, assignments)
		return
	}
	stopRequest, cancel := detachedStopLifecycleRequest(r)
	defer cancel()
	stopDispatch := sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), failed, assignments))
	metadata := map[string]any{
		"reason":          errYouTubeLiveAPIStartFailed.Error(),
		"dispatch":        dispatch,
		"stop_dispatch":   stopDispatch,
		"transition_code": errYouTubeLiveAPIStartFailed.Error(),
	}
	if completeMetadata, completeErr := s.completeYouTubeRuntime(stopRequest.Context(), stream.ID, true); completeErr != nil {
		metadata["youtube_complete_error"] = "youtube_live_api_complete_failed"
		current := currentFromContext(stopRequest.Context())
		s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "youtube_live_api_complete_failed", "trigger": "start_transition_failed"}})
	} else if len(completeMetadata) > 0 {
		metadata["youtube_complete"] = completeMetadata
		current := currentFromContext(stopRequest.Context())
		s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"trigger": "start_transition_failed", "complete": completeMetadata}})
	}
	current := currentFromContext(stopRequest.Context())
	s.writeAudit(stopRequest, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
	writeJSON(w, http.StatusBadGateway, map[string]any{"code": errYouTubeLiveAPIStartFailed.Error(), "stream": failed, "dispatch": dispatch, "stop_dispatch": stopDispatch})
}
