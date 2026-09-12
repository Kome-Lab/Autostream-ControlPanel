package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) serviceRemediationExecute(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "remediation.execute")
	if !ok {
		return
	}
	if token.ServiceType != "observability" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	registered, err := s.serviceTokenRegisteredForType(r.Context(), token, "observability")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	if !registered {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_not_registered"})
		return
	}
	var body serviceRemediationExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ActionID = strings.TrimSpace(body.ActionID)
	body.Action = strings.TrimSpace(body.Action)
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.IncidentID = strings.TrimSpace(body.IncidentID)
	if body.ActionID == "" || body.IncidentID == "" || body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "remediation_context_required"})
		return
	}
	if !isServiceRemediationActionAllowed(body.Action) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "unsupported_remediation_action"})
		return
	}
	stream, err := s.streams.GetStream(r.Context(), body.StreamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"missing_service_types": missing}))
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	archiveConfig, err := s.retryArchiveConfig(r.Context(), stream)
	if err != nil {
		code := archiveConfigCode(err)
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": code, "archive_profile_id": stream.ArchiveProfileID}))
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil || !configured {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "observability_not_configured"}))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	if err := obs.ValidateRemediationDispatchContext(r.Context(), body.ActionID, body.Action, body.IncidentID, stream.ID); err != nil {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "observability_context_invalid"}))
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "remediation_context_not_verified"})
		return
	}
	if err := s.remediation.ClaimRemediationExecution(r.Context(), body.ActionID, body.IncidentID, stream.ID, body.Action); errors.Is(err, store.ErrAlreadyExists) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "replay"}))
		writeJSON(w, http.StatusConflict, map[string]string{"code": "remediation_action_replayed"})
		return
	} else if errors.Is(err, store.ErrInvalidRemediationExecution) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "invalid_context"}))
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "remediation_context_required"})
		return
	} else if err != nil {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "claim_failed"}))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "remediation_claim_failed"})
		return
	}
	stream, err = s.beginStreamArchiveRetryGuarded(r.Context(), stream, primaryAssignments)
	if err != nil {
		code, status := archiveRetryGuardHTTPError(err)
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": code}))
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	results := s.dispatcher.RetryArchiveUpload(r.Context(), stream, primaryAssignments, archiveConfig)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"dispatch": results}))
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": results})
		return
	}
	logEntry, err := s.streams.RetryArchiveUpload(r.Context(), stream.ID, "service:"+token.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "retry_upload_failed"})
		return
	}
	s.writeAudit(r, serviceRemediationAudit(token, body, "success", map[string]any{"dispatch": results}))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":      "executed",
		"action_id":   body.ActionID,
		"action":      body.Action,
		"incident_id": body.IncidentID,
		"stream_id":   stream.ID,
		"log":         logEntry,
		"dispatch":    results,
	})
}

func isServiceRemediationActionAllowed(action string) bool {
	switch action {
	case "retry_gdrive_upload", "retry_package_remux":
		return true
	default:
		return false
	}
}

func (s *Server) beginStreamArchiveRetryGuarded(ctx context.Context, stream store.Stream, assignments []store.RegisteredService) (store.Stream, error) {
	if s.services == nil {
		return store.Stream{}, store.ErrServiceAssignmentGuardUnavailable
	}
	encoderServiceID := primaryServiceID(assignments, "encoder_recorder")
	if encoderServiceID == "" {
		return store.Stream{}, store.ErrServiceAssignmentConflict
	}
	return s.services.BeginStreamArchiveRetryGuarded(ctx, encoderServiceID, stream.ID)
}

func archiveRetryGuardHTTPError(err error) (string, int) {
	switch {
	case errors.Is(err, store.ErrServiceAssignmentConflict),
		errors.Is(err, store.ErrServiceAssignmentProtectedStream),
		errors.Is(err, store.ErrServiceUnassignProtectedStream):
		return "service_assignment_conflict", http.StatusConflict
	case errors.Is(err, store.ErrNotFound):
		return "not_found", http.StatusNotFound
	case errors.Is(err, store.ErrServiceAssignmentGuardUnavailable):
		return "service_assignment_guard_unavailable", http.StatusServiceUnavailable
	default:
		return "archive_retry_guard_failed", http.StatusInternalServerError
	}
}

func serviceRemediationAudit(token store.ServiceToken, req serviceRemediationExecuteRequest, result string, metadata map[string]any) store.AuditEvent {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["action"] = req.Action
	metadata["action_id"] = req.ActionID
	metadata["incident_id"] = req.IncidentID
	return store.AuditEvent{
		ActorUserID:   "service:" + token.ID,
		ActorUsername: token.ServiceType,
		Action:        "remediation.execute",
		ResourceType:  "stream",
		ResourceID:    req.StreamID,
		Result:        result,
		Metadata:      metadata,
	}
}
