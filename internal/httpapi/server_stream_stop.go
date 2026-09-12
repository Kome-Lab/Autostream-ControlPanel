package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) stopStream(w http.ResponseWriter, r *http.Request) {
	unlockLifecycle := s.lockStreamLifecycle(r.PathValue("id"))
	defer unlockLifecycle()

	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if !isManuallyStoppableStreamStatus(stream.Status) {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_status_not_stoppable", "current_status": stream.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_stoppable", "status": stream.Status})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredStopServiceTypes); len(missing) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	stopping, transitioned, err := s.streams.TransitionStreamStatus(r.Context(), stream.ID, stream.Status, "stopping")
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !transitioned {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_status_not_stoppable", "current_status": stopping.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_stoppable", "status": stopping.Status})
		return
	}
	stream = stopping
	r, cancelStopLifecycle := detachedStopLifecycleRequest(r)
	defer cancelStopLifecycle()
	results := s.dispatcher.Stop(r.Context(), stream, primaryAssignments)
	results = sanitizeDispatchResults(results)
	results = s.normalizeManualStopAlreadyStoppedResults(r.Context(), stream.ID, results)
	if hasDispatchFailure(results) {
		failed, transitioned, transitionErr := s.streams.TransitionStreamStatus(r.Context(), stream.ID, "stopping", "failed")
		if errors.Is(transitionErr, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if transitionErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
			return
		}
		if !transitioned {
			s.writeStopSupersededResponse(w, r, failed, results)
			return
		}
		current := currentFromContext(r.Context())
		metadata := map[string]any{"dispatch": results}
		staticRuntime, encoderStopConfirmed, receiptErr := s.markYouTubeRelayStaticEncoderStopConfirmed(r.Context(), stream.ID, primaryAssignments, results)
		if receiptErr != nil {
			metadata["youtube_relay_static_encoder_stop_receipt_warning"] = "durable encoder stop receipt could not be confirmed"
		} else if staticRuntime && encoderStopConfirmed {
			metadata["youtube_relay_static_encoder_stop_confirmed"] = true
		}
		abandonedStaticRuntime, recoveryErr := s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(r.Context(), stream.ID, "youtube_relay_static_stop_dispatch_unconfirmed")
		staticRuntime = staticRuntime || abandonedStaticRuntime
		if recoveryErr != nil {
			metadata["youtube_relay_static_recovery_warning"] = "recovery fence could not be confirmed"
		} else if staticRuntime {
			metadata["youtube_relay_static_recovery_required"] = true
		}
		// A normal Stop timeout is no stronger evidence than a force-stop timeout:
		// the Encoder may still be pushing the fixed relay. Preserve the binding
		// behind a recovery claim rather than leaving a prepared runtime that no
		// completion/recovery path can safely release.
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: metadata})
		response := map[string]any{"code": "service_dispatch_failed", "stream": failed, "dispatch": results}
		if staticRuntime {
			response["recovery_required"] = true
		}
		writeJSON(w, http.StatusBadGateway, response)
		return
	}
	// Persist a positive Encoder Stop receipt before the completed transition and
	// provider completion. A later provider response-loss must never make this
	// acknowledgement depend on the Encoder's short-lived request cache.
	staticRuntime, encoderStopConfirmed, encoderStopReceiptErr := s.markYouTubeRelayStaticEncoderStopConfirmed(r.Context(), stream.ID, primaryAssignments, results)
	if encoderStopReceiptErr != nil {
		log.Printf("youtube relay static encoder stop receipt marker failed after confirmed stop: stream_id=%s error_type=%T", stream.ID, encoderStopReceiptErr)
	}
	completed, transitioned, transitionErr := s.streams.TransitionStreamStatus(r.Context(), stream.ID, "stopping", "completed")
	if errors.Is(transitionErr, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if transitionErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !transitioned {
		s.writeStopSupersededResponse(w, r, completed, results)
		return
	}
	youtubeCompleteWarning := ""
	if metadata, err := s.completeYouTubeRuntime(r.Context(), stream.ID, false); err != nil {
		current := currentFromContext(r.Context())
		code := "complete_youtube_runtime_failed"
		if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
			code = errYouTubeLiveAPICompleteFailed.Error()
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		// The assigned services have already stopped successfully. Keep the
		// persisted runtime for the existing completion retry worker, but do
		// not turn a completed physical stop into a 502 or block the next VC.
		youtubeCompleteWarning = code
	} else if len(metadata) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	}
	rearmed, rearmErr := s.ensureAutoStartWaitingStream(r, completed)
	if rearmErr != nil {
		log.Printf("stream VC rearm failed: stream_id=%s error=%v", completed.ID, rearmErr)
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.rearm", ResourceType: "stream", ResourceID: completed.ID, Result: "failure", Metadata: map[string]any{"reason": "waiting_stream_rearm_failed", "trigger": completed.AutoStartTrigger}})
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"status": "completed", "dispatch": results}
	if staticRuntime && encoderStopConfirmed {
		metadata["youtube_relay_static_encoder_stop_confirmed"] = true
	}
	if encoderStopReceiptErr != nil && staticRuntime {
		metadata["youtube_relay_static_encoder_stop_receipt_warning"] = "durable encoder stop receipt could not be confirmed"
	}
	if youtubeCompleteWarning != "" {
		metadata["youtube_complete_warning"] = youtubeCompleteWarning
	}
	if rearmErr != nil {
		metadata["rearm_warning"] = "waiting_stream_rearm_failed"
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: completed.ID, Result: "success", Metadata: metadata})
	response := map[string]any{"stream": completed, "dispatch": results}
	if youtubeCompleteWarning != "" {
		response["youtube_complete_warning"] = youtubeCompleteWarning
	}
	if rearmErr != nil {
		response["rearm_warning"] = "waiting_stream_rearm_failed"
	}
	if rearmed.ID != "" {
		response["waiting_stream"] = rearmed
	}
	writeJSON(w, http.StatusOK, response)
}

// forceStopStream is the escape hatch for a stream whose normal stop
// lifecycle is stuck. It deliberately converges the Panel state to failed
// after a best-effort dispatch so operators can recover the next waiting VC
// stream without leaving an eternal starting/live row.
func (s *Server) forceStopStream(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()

	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if !isForceStoppableStreamStatus(stream.Status) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_force_stoppable", "status": stream.Status})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	// Claim the force-stop terminal state before issuing the best-effort
	// downstream request. Force stop is deliberately terminal even when a
	// service does not acknowledge it, so claiming first prevents two
	// concurrent force-stop requests (including requests on different Panel
	// processes) from sending duplicate stop dispatches.
	var results []servicecall.DispatchResult
	updated, transitioned, err := s.streams.TransitionStreamStatus(r.Context(), stream.ID, stream.Status, "failed")
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if !transitioned {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.force_stop", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"dispatch": results, "previous_status": stream.Status, "status": updated.Status, "force_stop_superseded": true}})
		writeJSON(w, http.StatusAccepted, map[string]any{"stream": updated, "dispatch": results, "forced": true, "force_stop_superseded": true})
		return
	}
	r, cancelStopLifecycle := detachedStopLifecycleRequest(r)
	defer cancelStopLifecycle()
	results = sanitizeDispatchResults(s.dispatcher.Stop(r.Context(), updated, primaryStreamAssignments(assignments)))
	metadata := map[string]any{"dispatch": results, "previous_status": stream.Status}
	if hasDispatchFailure(results) {
		metadata["dispatch_warning"] = "one or more services did not acknowledge force stop"
		staticRuntime, encoderStopConfirmed, receiptErr := s.markYouTubeRelayStaticEncoderStopConfirmed(r.Context(), stream.ID, primaryStreamAssignments(assignments), results)
		if receiptErr != nil {
			metadata["youtube_relay_static_encoder_stop_receipt_warning"] = "durable encoder stop receipt could not be confirmed"
		} else if staticRuntime && encoderStopConfirmed {
			metadata["youtube_relay_static_encoder_stop_confirmed"] = true
		}
		abandonedStaticRuntime, recoveryErr := s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(r.Context(), stream.ID, "youtube_relay_static_force_stop_dispatch_unconfirmed")
		staticRuntime = staticRuntime || abandonedStaticRuntime
		if recoveryErr != nil {
			metadata["youtube_relay_static_recovery_warning"] = "recovery fence could not be confirmed"
		} else if staticRuntime {
			metadata["youtube_relay_static_recovery_required"] = true
		}
		// The Encoder might still be pushing despite the Panel state having been
		// force-terminalized. Do not Complete/release the fixed relay and do not
		// re-arm the next VC until the explicit recovery route receives a fresh
		// Stop acknowledgement.
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.force_stop", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
		writeJSON(w, http.StatusOK, map[string]any{"stream": updated, "dispatch": results, "forced": true, "recovery_required": staticRuntime})
		return
	}
	// The force-stop transition already put the stream in failed; a fully
	// acknowledged Encoder Stop is still durable evidence that permits the
	// subsequent failed->completed/provider-complete path to survive a restart.
	staticRuntime, encoderStopConfirmed, encoderStopReceiptErr := s.markYouTubeRelayStaticEncoderStopConfirmed(r.Context(), stream.ID, primaryStreamAssignments(assignments), results)
	if encoderStopReceiptErr != nil {
		log.Printf("youtube relay static encoder stop receipt marker failed after confirmed force stop: stream_id=%s error_type=%T", stream.ID, encoderStopReceiptErr)
	}

	completed, completedTransitioned, completionTransitionErr := s.streams.TransitionStreamStatus(r.Context(), stream.ID, "failed", "completed")
	if errors.Is(completionTransitionErr, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if completionTransitionErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	if !completedTransitioned {
		metadata["status"] = completed.Status
		metadata["force_stop_completion_superseded"] = true
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.force_stop", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
		writeJSON(w, http.StatusAccepted, map[string]any{"stream": completed, "dispatch": results, "forced": true, "force_stop_completion_superseded": true})
		return
	}
	metadata["status"] = "completed"
	if staticRuntime && encoderStopConfirmed {
		metadata["youtube_relay_static_encoder_stop_confirmed"] = true
	}
	if encoderStopReceiptErr != nil && staticRuntime {
		metadata["youtube_relay_static_encoder_stop_receipt_warning"] = "durable encoder stop receipt could not be confirmed"
	}
	if _, err := s.completeYouTubeRuntime(r.Context(), stream.ID, true); err != nil {
		metadata["youtube_complete_warning"] = "youtube runtime completion failed"
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "force_stop_completion_failed"}})
	}
	rearmed, rearmErr := s.ensureAutoStartWaitingStream(r, completed)
	if rearmErr != nil {
		log.Printf("forced stream VC rearm failed: stream_id=%s error=%v", completed.ID, rearmErr)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.rearm", ResourceType: "stream", ResourceID: completed.ID, Result: "failure", Metadata: map[string]any{"reason": "waiting_stream_rearm_failed", "trigger": completed.AutoStartTrigger}})
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.force_stop", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	response := map[string]any{"stream": completed, "dispatch": results, "forced": true}
	if rearmErr != nil {
		response["rearm_warning"] = "waiting_stream_rearm_failed"
	}
	if rearmed.ID != "" {
		response["waiting_stream"] = rearmed
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ensureAutoStartWaitingStream(r *http.Request, completed store.Stream) (store.Stream, error) {
	if !strings.EqualFold(strings.TrimSpace(completed.AutoStartTrigger), autoStartTriggerDiscordVoiceJoin) {
		return store.Stream{}, nil
	}
	waiting, transitioned, err := s.streams.TransitionStreamStatus(r.Context(), completed.ID, "completed", "ready")
	if err != nil {
		return store.Stream{}, err
	}
	if !transitioned {
		current, getErr := s.streams.GetStream(r.Context(), completed.ID)
		if getErr != nil {
			return store.Stream{}, getErr
		}
		if isAutoStartableStreamStatus(current.Status) {
			return current, nil
		}
		return store.Stream{}, fmt.Errorf("stream rearm superseded: status=%s", current.Status)
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.rearm", ResourceType: "stream", ResourceID: completed.ID, Result: "success", Metadata: map[string]any{"waiting_stream_id": waiting.ID, "reused_stream": true, "trigger": completed.AutoStartTrigger}})
	return waiting, nil
}
