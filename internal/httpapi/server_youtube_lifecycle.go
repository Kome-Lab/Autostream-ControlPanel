package httpapi

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

const youtubeCompleteRetryDefaultInterval = 60 * time.Second

// youtubeRelayStaticUnknownBroadcastID is an internal, non-provider identifier
// used only when a relay-static prepare attempt may have created a Broadcast
// but did not return its ID. It is never sent to YouTube. Keeping a durable
// recovery claim is safer than releasing the fixed LiveStream for reuse.
const youtubeRelayStaticUnknownBroadcastID = "unknown_external_broadcast"

func (s *Server) saveYouTubeRuntime(ctx context.Context, streamID string, runtime map[string]any) error {
	if len(runtime) == 0 {
		return nil
	}
	if mapString(runtime, "mode") == "live_api_relay_static" {
		// The relay-static path persisted the runtime atomically with its claim
		// before this generic start flow. Never downgrade that transaction to a
		// standalone runtime write.
		return nil
	}
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil
	}
	return storeWithRuntime.SaveStreamYouTubeRuntime(ctx, streamYouTubeRuntimeFromMap(streamID, runtime))
}

func (s *Server) deleteYouTubeRuntime(ctx context.Context, streamID string) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if err == nil {
		if runtime.Mode == "live_api_relay_static" {
			return
		}
		s.clearYouTubeRuntimeSecret(ctx, runtime)
	}
	_ = storeWithRuntime.DeleteStreamYouTubeRuntime(ctx, streamID)
}

// abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch is the only cleanup
// allowed after an unacknowledged Start or Stop. An HTTP timeout can race a
// successful Encoder action, so provider Delete/Complete and claim release are
// unsafe until the recovery route has obtained a fresh downstream Stop receipt.
// The store operation removes the runtime from automatic completion in the same
// transaction that creates the recovery fence.
func (s *Server) abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(ctx context.Context, streamID, reason string) (bool, error) {
	runtimeStore, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return false, errYouTubeRelayBindingStoreUnavailable
	}
	runtime, err := runtimeStore.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if runtime.Mode != "live_api_relay_static" {
		return false, nil
	}
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return true, errYouTubeRelayBindingStoreUnavailable
	}
	claim, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		return true, errYouTubeRelayStaticRecoveryRequired
	}
	claim.LastError = strings.TrimSpace(reason)
	if _, err := claimStore.AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx, claim); err != nil {
		log.Printf("youtube relay static unconfirmed dispatch recovery fence failed: stream_id=%s error_type=%T", streamID, err)
		return true, errYouTubeRelayStaticRecoveryRequired
	}
	return true, nil
}

// markYouTubeRelayStaticEncoderStopConfirmed preserves a positive primary
// Encoder Stop acknowledgement before a partially failed Stop is converted to
// recovery_required. The receipt is deliberately scoped to the Encoder: Worker
// and Discord failures are operational warnings, but neither owns the fixed
// relay's ingest process. A marker response-loss is safe only when the exact
// fenced claim can be re-read with its durable timestamp.
func (s *Server) markYouTubeRelayStaticEncoderStopConfirmed(ctx context.Context, streamID string, assignments []store.RegisteredService, results []servicecall.DispatchResult) (staticRuntime bool, encoderStopConfirmed bool, err error) {
	runtimeStore, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return false, false, errYouTubeRelayBindingStoreUnavailable
	}
	runtime, err := runtimeStore.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if runtime.Mode != "live_api_relay_static" {
		return false, false, nil
	}
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return true, false, errYouTubeRelayBindingStoreUnavailable
	}
	claim, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
	if err != nil {
		return true, false, err
	}
	if claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched ||
		(claim.State != store.YouTubeRelayBindingClaimStatePrepared && claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired) {
		return true, false, nil
	}
	if !claim.EncoderStopConfirmedAt.IsZero() {
		return true, true, nil
	}
	confirmed, _ := relayStaticEncoderStopConfirmed(assignments, results, store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if !confirmed {
		return true, false, nil
	}
	marked, markerErr := markYouTubeRelayStaticClaimEncoderStopConfirmed(ctx, claimStore, claim)
	if markerErr == nil {
		return true, !marked.EncoderStopConfirmedAt.IsZero(), nil
	}
	return true, false, markerErr
}

// markYouTubeRelayStaticClaimEncoderStopConfirmed is shared by the immediate
// Stop path (prepared runtime still present) and recovery (runtime atomically
// abandoned). Both use the same reservation/broadcast fence. A response loss
// is accepted only after an exact durable receipt re-read.
func markYouTubeRelayStaticClaimEncoderStopConfirmed(ctx context.Context, claimStore store.StreamYouTubeRelayBindingClaimStore, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	marked, markerErr := claimStore.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx, claim)
	if markerErr == nil {
		return marked, nil
	}
	// A write response can be lost after the receipt timestamp committed. Never
	// treat an arbitrary claim as proof; the durable result must still match this
	// reservation and Broadcast exactly.
	observed, observedErr := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, claim.StreamID)
	if observedErr == nil && observed.RelayBindingID == claim.RelayBindingID &&
		observed.ReservationToken == claim.ReservationToken && observed.CreatedAt.Equal(claim.CreatedAt) &&
		observed.BroadcastID == claim.BroadcastID && !observed.EncoderStopConfirmedAt.IsZero() {
		return observed, nil
	}
	return claim, markerErr
}

// markYouTubeRelayStaticPossiblyDispatched is the durable hand-off point from
// a prepared fixed relay to the external Start dispatcher. It must succeed
// before the first downstream request; a failed marker never permits dispatch.
// If its response was lost after commit, the exact prepared/possibly-dispatched
// claim is observed again while the stream is still starting and may continue.
func (s *Server) markYouTubeRelayStaticPossiblyDispatched(ctx context.Context, streamID string) error {
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return errYouTubeRelayBindingStoreUnavailable
	}
	claim, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		return errYouTubeRelayStaticRecoveryRequired
	}
	if _, err := claimStore.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, claim); err == nil {
		return nil
	}

	// A response-loss error can occur after the marker committed. It is safe to
	// continue only when the exact claim is now explicitly fenced as possibly
	// dispatched and the owning stream still belongs to this start lifecycle.
	observed, observedErr := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
	if observedErr == nil && observed.State == store.YouTubeRelayBindingClaimStatePrepared &&
		observed.DispatchState == store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched &&
		observed.ReservationToken == claim.ReservationToken && observed.CreatedAt.Equal(claim.CreatedAt) {
		stream, streamErr := s.streams.GetStream(ctx, streamID)
		if streamErr == nil && strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
			return nil
		}
	}

	// If the marker did not commit, this is still provably pre-dispatch. Convert
	// it into the store's not-dispatched recovery state, which atomically removes
	// the prepared runtime and keeps the binding fenced.
	if observedErr == nil {
		claim = observed
	}
	claim.LastError = "youtube_relay_static_dispatch_marker_failed"
	if _, recoveryErr := claimStore.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx, claim); recoveryErr != nil {
		log.Printf("youtube relay static dispatch marker recovery fence failed: stream_id=%s error_type=%T", streamID, recoveryErr)
	}
	return errYouTubeRelayStaticRecoveryRequired
}

func (s *Server) completeYouTubeRuntime(ctx context.Context, streamID string, force bool) (map[string]any, error) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil, nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if runtime.Mode == "live_api_relay_static" {
		if err := s.ensureYouTubeRelayStaticCompletionCompleted(ctx, streamID); err != nil {
			return nil, err
		}
	}
	shouldComplete := force || runtime.CompleteOnStop || runtime.Mode == "live_api_relay_static"
	if runtime.Mode == "live_api" && shouldComplete {
		credentials, err := s.youtubeOAuthCredentials(ctx, runtime.OAuthAccountID)
		if err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, err)
			return nil, err
		}
		if s.youtubeLive == nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeLiveAPIUnavailable)
			return nil, errYouTubeLiveAPIUnavailable
		}
		if err := s.youtubeLive.Complete(ctx, ytlive.CompleteRequest{Credentials: credentials, BroadcastID: runtime.BroadcastID}); err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeLiveAPICompleteFailed)
			return nil, errYouTubeLiveAPICompleteFailed
		}
	}
	if runtime.Mode == "live_api_relay_static" {
		// A fixed relay may already have observed a provider-side Complete even
		// when the transition response was lost. The generic LiveClient.Complete
		// cannot distinguish that response-loss case from a true failure, so it is
		// never an acceptable fallback for releasing a reusable relay claim.
		completionClient, ok := s.youtubeLive.(ytlive.RelayStaticBroadcastCompletionClient)
		if !ok {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeRelayStaticUnavailable)
			return nil, errYouTubeRelayStaticUnavailable
		}
		credentials, err := s.youtubeOAuthCredentials(ctx, runtime.OAuthAccountID)
		if err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, err)
			return nil, err
		}
		if err := completionClient.CompleteRelayStaticBroadcast(ctx, ytlive.CompleteRequest{Credentials: credentials, BroadcastID: runtime.BroadcastID}); err != nil {
			// Keep both the stable Panel error category and the client's typed
			// uncertain-completion cause so retries/audits remain actionable while
			// the claim and runtime stay durably fenced.
			completionErr := errors.Join(errYouTubeLiveAPICompleteFailed, err)
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, completionErr)
			return nil, completionErr
		}
	}
	if runtime.Mode == "live_api_relay_static" {
		// Recheck immediately before releasing the durable runtime/claim. The
		// store-side completed-only fence is the cross-process boundary; this
		// local read avoids issuing a release after a visible lifecycle change.
		if err := s.ensureYouTubeRelayStaticCompletionCompleted(ctx, streamID); err != nil {
			return nil, err
		}
		claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
		if !ok {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeRelayBindingStoreUnavailable)
			return nil, errYouTubeRelayBindingStoreUnavailable
		}
		claim, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
		if err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, err)
			return nil, err
		}
		if err := claimStore.CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx, claim); err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, err)
			return nil, err
		}
	} else if err := storeWithRuntime.DeleteStreamYouTubeRuntime(ctx, streamID); err != nil {
		return nil, err
	}
	s.clearYouTubeRuntimeSecret(ctx, runtime)
	return map[string]any{
		"mode":             runtime.Mode,
		"output_id":        runtime.YouTubeOutput,
		"oauth_account_id": runtime.OAuthAccountID,
		"broadcast_id":     runtime.BroadcastID,
		"live_stream_id":   runtime.LiveStreamID,
		"dry_run":          runtime.DryRun,
		"complete_on_stop": runtime.CompleteOnStop,
		"retry_count":      runtime.CompleteRetryCount,
		"complete_skipped": runtime.Mode == "live_api" && !shouldComplete,
	}, nil
}

func (s *Server) ensureYouTubeRelayStaticCompletionCompleted(ctx context.Context, streamID string) error {
	stream, err := s.streams.GetStream(ctx, strings.TrimSpace(streamID))
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "completed") {
		return errYouTubeRelayStaticCompletionRequiresCompleted
	}
	return nil
}

func (s *Server) recordYouTubeRuntimeCompleteFailure(ctx context.Context, storeWithRuntime store.StreamYouTubeRuntimeStore, runtime store.StreamYouTubeRuntime, err error) {
	retryCount := runtime.CompleteRetryCount + 1
	nextRetryAt := time.Now().UTC().Add(youtubeCompleteRetryDelay(retryCount))
	if _, updateErr := storeWithRuntime.RecordStreamYouTubeRuntimeCompleteFailure(ctx, runtime.StreamID, youtubeCompleteRetryErrorCode(err), nextRetryAt); updateErr != nil {
		log.Printf("youtube complete retry scheduling failed: stream_id=%s error=%v", runtime.StreamID, updateErr)
	}
}

func youtubeCompleteRetryDelay(retryCount int) time.Duration {
	switch {
	case retryCount <= 1:
		return time.Minute
	case retryCount == 2:
		return 2 * time.Minute
	case retryCount == 3:
		return 5 * time.Minute
	case retryCount == 4:
		return 10 * time.Minute
	case retryCount == 5:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

func youtubeCompleteRetryErrorCode(err error) string {
	switch {
	case errors.Is(err, ytlive.ErrRelayStaticBroadcastCompletionUncertain):
		return ytlive.ErrRelayStaticBroadcastCompletionUncertain.Error()
	case errors.Is(err, ytlive.ErrRelayStaticBroadcastCompletionFailed):
		return ytlive.ErrRelayStaticBroadcastCompletionFailed.Error()
	case errors.Is(err, errYouTubeRelayStaticUnavailable):
		return errYouTubeRelayStaticUnavailable.Error()
	case errors.Is(err, errYouTubeLiveAPIUnavailable):
		return errYouTubeLiveAPIUnavailable.Error()
	case errors.Is(err, errYouTubeOAuthAccountUnavailable):
		return errYouTubeOAuthAccountUnavailable.Error()
	case errors.Is(err, errYouTubeLiveAPICompleteFailed):
		return errYouTubeLiveAPICompleteFailed.Error()
	default:
		return "complete_youtube_runtime_failed"
	}
}

func (s *Server) RunYouTubeCompletionRetryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = youtubeCompleteRetryDefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.CompleteDueYouTubeRuntimes(ctx, 25); err != nil {
			log.Printf("youtube complete retry scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) CompleteDueYouTubeRuntimes(ctx context.Context, limit int) (map[string]any, error) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return map[string]any{"attempted": 0, "completed": 0, "failed": 0, "skipped": 0}, nil
	}
	runtimes, err := storeWithRuntime.ListDueStreamYouTubeRuntimes(ctx, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	attempted := 0
	completed := 0
	failed := 0
	skipped := 0
	for _, runtime := range runtimes {
		if runtime.Mode == "live_api_relay_static" {
			if err := s.ensureYouTubeRelayStaticCompletionCompleted(ctx, runtime.StreamID); err != nil {
				if errors.Is(err, errYouTubeRelayStaticCompletionRequiresCompleted) {
					skipped++
					continue
				}
				failed++
				s.writeSystemAudit(ctx, store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: runtime.StreamID, Result: "failure", Metadata: map[string]any{"reason": youtubeCompleteRetryErrorCode(err), "trigger": "auto_retry"}})
				continue
			}
		}
		attempted++
		metadata, err := s.completeYouTubeRuntime(ctx, runtime.StreamID, true)
		if err != nil {
			failed++
			s.writeSystemAudit(ctx, store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: runtime.StreamID, Result: "failure", Metadata: map[string]any{"reason": youtubeCompleteRetryErrorCode(err), "trigger": "auto_retry"}})
			continue
		}
		if len(metadata) == 0 {
			continue
		}
		completed++
		metadata["completed"] = true
		metadata["trigger"] = "auto_retry"
		s.writeSystemAudit(ctx, store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: runtime.StreamID, Result: "success", Metadata: metadata})
	}
	return map[string]any{"attempted": attempted, "completed": completed, "failed": failed, "skipped": skipped}, nil
}

func streamYouTubeRuntimeFromMap(streamID string, runtime map[string]any) store.StreamYouTubeRuntime {
	return store.StreamYouTubeRuntime{
		StreamID:            streamID,
		YouTubeOutput:       firstNonEmpty(mapString(runtime, "output_id"), mapString(runtime, "youtube_output")),
		OAuthAccountID:      mapString(runtime, "oauth_account_id"),
		Mode:                mapString(runtime, "mode"),
		BroadcastID:         mapString(runtime, "broadcast_id"),
		LiveStreamID:        mapString(runtime, "live_stream_id"),
		RTMPURL:             mapString(runtime, "rtmp_url"),
		StreamKeySecretName: mapString(runtime, "stream_key_secret_name"),
		DryRun:              mapBool(runtime, "dry_run"),
		CompleteOnStop:      mapBoolDefault(runtime, "complete_on_stop", true),
	}
}

func youtubeLiveAPIStreamKeySecretName(streamID, outputID, broadcastID string) string {
	seed := strings.TrimSpace(streamID) + ":" + strings.TrimSpace(outputID) + ":" + strings.TrimSpace(broadcastID)
	return "youtube_stream_key_runtime_" + security.SecretFingerprint(seed)
}

func (s *Server) clearYouTubeRuntimeSecretFromMap(ctx context.Context, runtime map[string]any) {
	if len(runtime) == 0 {
		return
	}
	s.clearYouTubeRuntimeSecret(ctx, streamYouTubeRuntimeFromMap("", runtime))
}

func (s *Server) clearYouTubeRuntimeSecret(ctx context.Context, runtime store.StreamYouTubeRuntime) {
	secretName := strings.TrimSpace(runtime.StreamKeySecretName)
	if secretName == "" {
		return
	}
	_, _ = s.secrets.UpdateSecret(ctx, secretName, "")
}
