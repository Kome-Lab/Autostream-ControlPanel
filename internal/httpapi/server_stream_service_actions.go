package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"strings"
)

func (s *Server) serviceStartStream(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.start")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		s.writeServiceAudit(r, token, "streams.start", "stream", r.PathValue("id"), "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	assignBeforeStart := false
	if !assigned {
		configuredService, configured, err := s.discordServiceTokenConfiguredForStream(r.Context(), token, stream)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		if configured {
			service = configuredService
			assigned = true
			assignBeforeStart = true
		}
	}
	if !assigned {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
		return
	}
	if isActiveStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_active"})
		writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "already_active": true})
		return
	}
	if strings.TrimSpace(stream.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_auto_start_not_enabled"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_auto_start_not_enabled"})
		return
	}
	if !isAutoStartableStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_not_waiting", "status": stream.Status})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_not_waiting"})
		return
	}
	var materialization *streamStartMaterialization
	if assignBeforeStart {
		materialization = &streamStartMaterialization{Service: service, ActorUserID: "service:" + service.ServiceID}
	}
	r.Body = http.NoBody
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, serviceCurrentUser(service)))
	s.startStreamWithMaterialization(w, r, materialization)
}

// serviceStopStream is deliberately narrower than the operator stop route:
// only the primary Discord Bot assigned to a VC-triggered stream may request
// it. The actual transition remains stopStream so that dispatch, YouTube
// completion, auditing, and waiting-stream rearm have one implementation.
func (s *Server) serviceStopStream(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.stop")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		s.writeServiceAudit(r, token, "streams.stop", "stream", r.PathValue("id"), "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	stream, err := s.streams.GetStream(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	if !assigned && strings.EqualFold(strings.TrimSpace(stream.Status), "completed") {
		configuredService, configured, configuredErr := s.discordServiceTokenConfiguredForStream(r.Context(), token, stream)
		if configuredErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		if configured {
			service = configuredService
			assigned = true
		}
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "completed":
		// Re-arming a VC stream reuses its existing row. A duplicate stop must
		// still be tied to that stream's configured Discord Bot; it is never an
		// authorization shortcut for an arbitrary registered bot.
		if !assigned {
			s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
			return
		}
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_stopped"})
		writeJSON(w, http.StatusOK, map[string]any{"already_stopped": true})
		return
	}
	if !assigned {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
		return
	}
	if strings.TrimSpace(stream.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_auto_start_not_enabled"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_auto_start_not_enabled"})
		return
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "stopping":
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_stopping"})
		writeJSON(w, http.StatusAccepted, map[string]any{"stream": stream, "already_stopping": true})
		return
	}
	if !isManuallyStoppableStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.stop", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_status_not_stoppable", "status": stream.Status})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_stoppable", "status": stream.Status})
		return
	}
	r.Body = http.NoBody
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, serviceCurrentUser(service)))
	s.stopStream(w, r)
}
