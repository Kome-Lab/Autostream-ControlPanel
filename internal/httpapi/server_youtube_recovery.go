package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

func (s *Server) completeYouTubeStream(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata, err := s.completeYouTubeRuntime(r.Context(), stream.ID, true)
	if err != nil {
		code := "complete_youtube_runtime_failed"
		status := http.StatusBadGateway
		if errors.Is(err, errYouTubeRelayStaticCompletionRequiresCompleted) {
			code = errYouTubeRelayStaticCompletionRequiresCompleted.Error()
			status = http.StatusConflict
		} else if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
			code = errYouTubeLiveAPICompleteFailed.Error()
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "trigger": "manual_retry"}})
		writeJSON(w, status, map[string]any{"code": code})
		return
	}
	if len(metadata) == 0 {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"completed": false, "trigger": "manual_retry"}})
		writeJSON(w, http.StatusOK, map[string]any{"completed": false})
		return
	}
	metadata["completed"] = true
	metadata["trigger"] = "manual_retry"
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, map[string]any{"completed": true, "youtube_runtime": metadata})
}

type relayStaticRecoveryResolveRequest struct {
	// ConfirmExternalCleanup is required because a recovery claim exists only
	// after the Panel could no longer prove the remote Broadcast state. For a
	// known Broadcast the handler still retries provider completion; for an
	// unknown Broadcast this is the privileged operator attestation that the
	// external cleanup was verified out of band.
	ConfirmExternalCleanup bool `json:"confirm_external_cleanup"`
}

// resolveYouTubeRelayStaticRecovery is an intentionally explicit recovery
// escape hatch. It never runs automatically: an unknown provider result must
// continue fencing the fixed relay until an operator confirms external cleanup.
func (s *Server) resolveYouTubeRelayStaticRecovery(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()

	var body relayStaticRecoveryResolveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}

	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	writeFailure := func(status int, code string, metadata map[string]any) {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["reason"] = code
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        "streams.youtube_relay_static_recovery.resolve",
			ResourceType:  "stream",
			ResourceID:    stream.ID,
			Result:        "failure",
			Metadata:      metadata,
		})
		writeJSON(w, status, map[string]string{"code": code})
	}
	if !body.ConfirmExternalCleanup {
		writeFailure(http.StatusBadRequest, "youtube_relay_static_external_cleanup_confirmation_required", nil)
		return
	}
	if isActiveStreamStatus(stream.Status) {
		writeFailure(http.StatusConflict, "stream_relay_recovery_not_safe_while_active", map[string]any{"status": stream.Status})
		return
	}
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		writeFailure(http.StatusServiceUnavailable, errYouTubeRelayBindingStoreUnavailable.Error(), nil)
		return
	}
	claim, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(r.Context(), stream.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeFailure(http.StatusNotFound, "youtube_relay_static_recovery_not_found", nil)
		return
	}
	if err != nil {
		writeFailure(http.StatusInternalServerError, "get_youtube_relay_static_recovery_failed", nil)
		return
	}
	if claim.State == store.YouTubeRelayBindingClaimStateReserved {
		// A process can crash after Reserve or after the Prepare handoff marker
		// while the stream is already terminal. Reconcile under the exact
		// reservation fence: only reserved/not_attempted is provably provider-free
		// and releasable; possibly_prepared becomes the same explicit recovery
		// state used for all uncertain Broadcasts.
		claim.BroadcastID = youtubeRelayStaticUnknownBroadcastID
		if strings.TrimSpace(claim.LastError) == "" {
			claim.LastError = "youtube_relay_static_prepare_recovery_reconcile"
		}
		resolution, reconcileErr := claimStore.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(r.Context(), claim)
		if reconcileErr != nil {
			writeFailure(http.StatusServiceUnavailable, errYouTubeRelayBindingStoreUnavailable.Error(), map[string]any{"relay_binding_id": claim.RelayBindingID, "operation": "prepare_fence_reconcile"})
			return
		}
		if resolution.Released {
			s.writeAudit(r, store.AuditEvent{
				ActorUserID:   current.User.ID,
				ActorUsername: current.User.Username,
				Action:        "streams.youtube_relay_static_recovery.resolve",
				ResourceType:  "stream",
				ResourceID:    stream.ID,
				Result:        "success",
				Metadata: map[string]any{
					"confirm_external_cleanup": true,
					"cleanup":                  "operator_confirmed_unknown_broadcast",
					"prepare_state":            store.YouTubeRelayBindingClaimPrepareStateNotAttempted,
					"relay_binding_id":         claim.RelayBindingID,
					"youtube_output_id":        claim.YouTubeOutputID,
				},
			})
			writeJSON(w, http.StatusOK, map[string]any{"resolved": true, "cleanup": "operator_confirmed_unknown_broadcast", "relay_binding_id": claim.RelayBindingID})
			return
		}
		claim = resolution.Claim
	}
	if claim.State == store.YouTubeRelayBindingClaimStatePrepared {
		// A dispatch-marker write/read failure can leave an inactive prepared
		// runtime even though the Panel cannot prove whether the first Start was
		// observed by the Encoder. Normalize it under the same exact reservation
		// fence before recovery; never release or provider-clean up this state
		// directly.
		if strings.TrimSpace(claim.LastError) == "" {
			claim.LastError = "youtube_relay_static_dispatch_marker_recovery_reconcile"
		}
		var reconcileErr error
		claim, reconcileErr = claimStore.ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(r.Context(), claim)
		if reconcileErr != nil {
			writeFailure(http.StatusServiceUnavailable, errYouTubeRelayBindingStoreUnavailable.Error(), map[string]any{"relay_binding_id": claim.RelayBindingID, "operation": "dispatch_fence_reconcile"})
			return
		}
	}
	if claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired {
		writeFailure(http.StatusConflict, "youtube_relay_static_recovery_not_required", map[string]any{"claim_state": claim.State})
		return
	}
	// A stream status of failed is not proof an Encoder Start/Stop request was
	// observed by the Encoder. Before an operator confirmation or provider
	// cleanup can release this fixed binding, obtain a fresh Encoder-specific
	// Stop receipt. Other assigned services are still stopped and audited, but
	// only the Encoder owns the reusable fixed relay safety boundary.
	var stopResults []servicecall.DispatchResult
	var stopWarnings []servicecall.DispatchResult
	encoderStopped := !claim.EncoderStopConfirmedAt.IsZero()
	if !encoderStopped {
		assignments, err := s.streamAssignments(r.Context(), stream.ID)
		if err != nil {
			writeFailure(http.StatusInternalServerError, "list_stream_assignments_failed", nil)
			return
		}
		primaryAssignments := primaryStreamAssignments(assignments)
		if missing := missingServiceTypes(primaryAssignments, []string{"encoder_recorder"}); len(missing) > 0 {
			writeFailure(http.StatusConflict, "youtube_relay_static_recovery_encoder_stop_unavailable", map[string]any{"missing_service_types": missing})
			return
		}
		stopRequest, cancelStopLifecycle := detachedStopLifecycleRequest(r)
		defer cancelStopLifecycle()
		stopResults = sanitizeDispatchResults(s.dispatcher.Stop(stopRequest.Context(), stream, primaryAssignments))
		encoderStopped, stopWarnings = relayStaticEncoderStopConfirmed(primaryAssignments, stopResults, claim.DispatchState)
		if !encoderStopped {
			writeFailure(http.StatusBadGateway, "youtube_relay_static_recovery_encoder_stop_unconfirmed", map[string]any{"dispatch": stopResults, "dispatch_state": claim.DispatchState})
			return
		}
		if claim.DispatchState == store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
			marked, markerErr := markYouTubeRelayStaticClaimEncoderStopConfirmed(r.Context(), claimStore, claim)
			if markerErr != nil || marked.EncoderStopConfirmedAt.IsZero() {
				writeFailure(http.StatusBadGateway, "youtube_relay_static_recovery_encoder_stop_unconfirmed", map[string]any{"dispatch": stopResults, "dispatch_state": claim.DispatchState, "durable_receipt": false})
				return
			}
			claim = marked
		}
	}

	cleanup := "operator_confirmed_unknown_broadcast"
	operatorAttestedProviderCleanup := false
	switch claim.DispatchState {
	case store.YouTubeRelayBindingClaimDispatchStateNotDispatched:
		// The durable marker proves no Start request was issued. A known
		// pre-dispatch Broadcast can therefore use the client's confirmed
		// delete path; an unknown result still requires the operator's explicit
		// attestation from the request body.
		if claim.BroadcastID == youtubeRelayStaticUnknownBroadcastID {
			break
		}
		credentials, err := s.youtubeOAuthCredentials(r.Context(), claim.OAuthAccountID)
		if err != nil {
			code := "youtube_relay_static_recovery_credentials_failed"
			status := http.StatusInternalServerError
			if errors.Is(err, errYouTubeOAuthAccountUnavailable) {
				code = errYouTubeOAuthAccountUnavailable.Error()
				status = http.StatusConflict
			}
			writeFailure(status, code, map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		cleanupClient, ok := s.youtubeLive.(ytlive.RelayStaticBroadcastCleanupClient)
		if !ok {
			writeFailure(http.StatusServiceUnavailable, "youtube_relay_static_recovery_cleanup_unavailable", map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		if err := cleanupClient.DeleteRelayStaticBroadcast(r.Context(), ytlive.RelayStaticBroadcastCleanupRequest{Credentials: credentials, BroadcastID: claim.BroadcastID}); err != nil {
			writeFailure(http.StatusBadGateway, "youtube_relay_static_recovery_cleanup_failed", map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		cleanup = "provider_delete"
	case store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched:
		// Once Start was handed off, a timeout/partial response may conceal a
		// running ingest. Even after the fresh Encoder Stop receipt, do not use
		// Delete (which is only safe for an unstarted Broadcast). Complete is
		// the sole automatic provider cleanup; any error preserves the recovery
		// claim for explicit operator investigation.
		if claim.BroadcastID == youtubeRelayStaticUnknownBroadcastID {
			writeFailure(http.StatusConflict, "youtube_relay_static_recovery_broadcast_unknown", map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		credentials, err := s.youtubeOAuthCredentials(r.Context(), claim.OAuthAccountID)
		if err != nil {
			code := "youtube_relay_static_recovery_credentials_failed"
			status := http.StatusInternalServerError
			if errors.Is(err, errYouTubeOAuthAccountUnavailable) {
				code = errYouTubeOAuthAccountUnavailable.Error()
				status = http.StatusConflict
			}
			writeFailure(status, code, map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		completionClient, ok := s.youtubeLive.(ytlive.RelayStaticBroadcastCompletionClient)
		if !ok {
			writeFailure(http.StatusServiceUnavailable, "youtube_relay_static_recovery_cleanup_unavailable", map[string]any{"relay_binding_id": claim.RelayBindingID})
			return
		}
		if err := completionClient.CompleteRelayStaticBroadcast(r.Context(), ytlive.CompleteRequest{Credentials: credentials, BroadcastID: claim.BroadcastID}); err != nil {
			// This endpoint is explicitly permissioned and requires the operator's
			// confirm_external_cleanup attestation. It is the manual-only escape
			// hatch for a provider response loss or an externally completed
			// Broadcast, but never substitutes for the durable Encoder stop fence.
			// Automatic completion retries still retain this claim on every error.
			if claim.EncoderStopConfirmedAt.IsZero() {
				writeFailure(http.StatusBadGateway, "youtube_relay_static_recovery_complete_failed", map[string]any{"relay_binding_id": claim.RelayBindingID})
				return
			}
			cleanup = "operator_confirmed_provider_cleanup"
			operatorAttestedProviderCleanup = true
		} else {
			cleanup = "provider_complete"
		}
	default:
		writeFailure(http.StatusConflict, "youtube_relay_static_recovery_dispatch_state_invalid", map[string]any{"relay_binding_id": claim.RelayBindingID, "dispatch_state": claim.DispatchState})
		return
	}
	if err := claimStore.ResolveStreamYouTubeRelayBindingRecovery(r.Context(), claim); err != nil {
		code := "resolve_youtube_relay_static_recovery_failed"
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, store.ErrNotFound):
			code = "youtube_relay_static_recovery_not_found"
			status = http.StatusNotFound
		case errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict), errors.Is(err, store.ErrYouTubeRelayBindingClaimState), errors.Is(err, store.ErrInvalidYouTubeRelayBindingClaim):
			code = "youtube_relay_static_recovery_not_required"
			status = http.StatusConflict
		}
		writeFailure(status, code, map[string]any{"relay_binding_id": claim.RelayBindingID})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "streams.youtube_relay_static_recovery.resolve",
		ResourceType:  "stream",
		ResourceID:    stream.ID,
		Result:        "success",
		Metadata: map[string]any{
			"confirm_external_cleanup":           true,
			"cleanup":                            cleanup,
			"stop_dispatch":                      stopResults,
			"stop_dispatch_warnings":             stopWarnings,
			"dispatch_state":                     claim.DispatchState,
			"operator_attested_provider_cleanup": operatorAttestedProviderCleanup,
			"relay_binding_id":                   claim.RelayBindingID,
			"youtube_output_id":                  claim.YouTubeOutputID,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"resolved": true, "cleanup": cleanup, "relay_binding_id": claim.RelayBindingID})
}
