package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const criticalHeartbeatMismatchGrace = 60 * time.Second

func (s *Server) serviceStreamEvent(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "")
	if !ok {
		return
	}
	var body store.ServiceStreamEvent
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	requiredScope := serviceStreamEventRequiredScope(token.ServiceType, body.EventType)
	if requiredScope == "" || !serviceTokenHasScope(token, requiredScope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_service_scope"})
		return
	}
	if err := s.services.WriteStreamEvent(r.Context(), token, body); errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
		return
	} else if errors.Is(err, store.ErrInvalidServiceStreamEvent) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_event_type"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_event_rejected"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

type serviceArtifactReport struct {
	ServiceID        string                          `json:"service_id"`
	StreamID         string                          `json:"stream_id"`
	ArchiveRunID     string                          `json:"archive_run_id,omitempty"`
	ArchiveStartedAt *time.Time                      `json:"archive_started_at,omitempty"`
	Artifacts        []serviceArtifactReportArtifact `json:"artifacts"`
}

type serviceArtifactReportArtifact struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
}

const maxServiceArtifactReportBodyBytes = 64 << 10

func (s *Server) serviceStreamArtifacts(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "encoder.status.write")
	if !ok {
		return
	}
	if token.ServiceType != "encoder_recorder" {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", "", "failure", map[string]any{"reason": "service_token_scope_mismatch"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_scope_mismatch"})
		return
	}
	var body serviceArtifactReport
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxServiceArtifactReportBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ServiceID = strings.TrimSpace(body.ServiceID)
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.ArchiveRunID = strings.TrimSpace(body.ArchiveRunID)
	if body.ServiceID == "" {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_service_id", "artifact_count": len(body.Artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_id"})
		return
	}
	artifacts := serviceArtifactReportArtifacts(body.Artifacts, body.ArchiveRunID, body.ArchiveStartedAt)
	if err := store.ValidateStreamArtifactReport(body.StreamID, artifacts); err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_stream_artifact", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	eventPayload := map[string]any{"artifact_count": len(artifacts)}
	if body.ArchiveRunID != "" {
		eventPayload["archive_run_id"] = body.ArchiveRunID
		if body.ArchiveStartedAt != nil {
			eventPayload["archive_started_at"] = body.ArchiveStartedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	event := store.ServiceStreamEvent{
		ServiceID: body.ServiceID,
		StreamID:  body.StreamID,
		EventType: "archive.artifacts.reported",
		Payload:   eventPayload,
	}
	if reporter, ok := s.streams.(store.StreamArtifactReportStore); ok {
		if err := reporter.WriteStreamArtifactReport(r.Context(), token, event, artifacts); errors.Is(err, store.ErrForbidden) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_assigned_to_stream", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
			return
		} else if errors.Is(err, store.ErrInvalidServiceStreamEvent) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_stream_event_type", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_event_type"})
			return
		} else if errors.Is(err, store.ErrNotFound) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_or_stream_not_found", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_or_stream_not_found"})
			return
		} else if err != nil {
			s.writeServiceArtifactStoreFailure(w, r, token, body, len(artifacts), err)
			return
		}
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "success", map[string]any{"service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "artifact_count": len(artifacts)})
		return
	}
	if err := s.services.WriteStreamEvent(r.Context(), token, event); errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_assigned_to_stream", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_registered", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	} else if errors.Is(err, store.ErrInvalidServiceStreamEvent) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_stream_event_type", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_event_type"})
		return
	} else if err != nil {
		s.writeServiceArtifactStoreFailure(w, r, token, body, len(artifacts), err)
		return
	}
	for index := range artifacts {
		artifacts[index].SourceServiceID = body.ServiceID
	}
	if err := s.streams.UpsertStreamArtifacts(r.Context(), body.StreamID, artifacts); errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "stream_not_found", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		s.writeServiceArtifactStoreFailure(w, r, token, body, len(artifacts), err)
		return
	}
	s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "success", map[string]any{"service_id": body.ServiceID, "artifact_count": len(artifacts)})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "artifact_count": len(artifacts)})
}

func (s *Server) writeServiceArtifactStoreFailure(w http.ResponseWriter, r *http.Request, token store.ServiceToken, body serviceArtifactReport, artifactCount int, err error) {
	status := http.StatusInternalServerError
	code := "stream_artifact_store_failed"
	if store.IsTransientDatabaseConnectionError(err) {
		status = http.StatusServiceUnavailable
		code = "stream_artifact_store_unavailable"
		w.Header().Set("Retry-After", "1")
	}
	s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{
		"reason":         code,
		"service_id":     body.ServiceID,
		"artifact_count": artifactCount,
	})
	writeJSON(w, status, map[string]string{"code": code})
}

func serviceArtifactReportArtifacts(input []serviceArtifactReportArtifact, archiveRunID string, archiveStartedAt *time.Time) []store.StreamArtifact {
	artifacts := make([]store.StreamArtifact, 0, len(input))
	for _, artifact := range input {
		artifacts = append(artifacts, store.StreamArtifact{
			ArchiveRunID:     archiveRunID,
			ArchiveStartedAt: archiveStartedAt,
			Kind:             artifact.Kind,
			Name:             artifact.Name,
			RelativePath:     artifact.RelativePath,
			SizeBytes:        artifact.SizeBytes,
		})
	}
	return artifacts
}

func serviceStreamEventRequiredScope(serviceType, eventType string) string {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		return ""
	}
	switch serviceType {
	case "worker":
		return "worker.events.write"
	case "encoder_recorder":
		return "encoder.status.write"
	case "discord_bot":
		return "discord.status.write"
	case "observability":
		return "observability.ingest"
	default:
		return ""
	}
}

func serviceTokenHasScope(token store.ServiceToken, scope string) bool {
	for _, value := range token.Scopes {
		if value == scope {
			return true
		}
	}
	return false
}

func (s *Server) serviceHeartbeat(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.heartbeat")
	if !ok {
		return
	}
	var body store.ServiceHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	service, err := s.persistServiceHeartbeat(r.Context(), token, serviceBearerToken(r), body, panelBaseURL(r))
	if errors.Is(err, store.ErrUnauthorized) {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "invalid_service_token", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_service_token"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "service_not_assigned_to_token", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "service_not_registered", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "heartbeat_failed", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "heartbeat_failed"})
		return
	}
	s.reconcileServiceHeartbeatStreamRuntime(r, token, service, body.CurrentStreamID)
	writeJSON(w, http.StatusAccepted, service)
}

func (s *Server) reconcileServiceHeartbeatStreamRuntime(r *http.Request, token store.ServiceToken, service store.RegisteredService, reportedCurrentStreamID string) {
	if service.ServiceType != "worker" && service.ServiceType != "encoder_recorder" {
		return
	}
	assignmentRows, err := s.services.ListServiceAssignmentsForService(r.Context(), service.ServiceID)
	if err != nil {
		s.writeServiceAudit(r, token, "services.heartbeat.reconcile", "service", service.ServiceID, "failure", map[string]any{
			"reason":       "list_service_assignments_failed",
			"service_type": service.ServiceType,
		})
		return
	}
	reportedStreamID := strings.TrimSpace(reportedCurrentStreamID)
	for _, assignment := range assignmentRows {
		streamID := strings.TrimSpace(assignment.StreamID)
		if streamID == "" || streamID == reportedStreamID || assignment.ServiceID != service.ServiceID || assignment.ServiceType != service.ServiceType || normalizeAssignmentRole(assignment.AssignmentRole) != "primary" {
			continue
		}
		stream, err := s.streams.GetStream(r.Context(), streamID)
		if err != nil || !heartbeatMismatchEligible(stream, time.Now().UTC()) {
			// Starting is deliberately excluded. An empty heartbeat can have been
			// sent immediately before an ordered Encoder -> Worker -> Bot start;
			// the lifecycle lock below protects convergence, but cannot make that
			// already-sent heartbeat describe the new runtime.
			continue
		}
		s.convergeServiceMediaFailure(r, token, service, "service_heartbeat_runtime_missing", "service.heartbeat.current_stream_mismatch", streamID, stream.UpdatedAt)
	}
}

func heartbeatMismatchEligible(stream store.Stream, now time.Time) bool {
	return stream.Status == "live" && !now.Before(stream.UpdatedAt.Add(criticalHeartbeatMismatchGrace))
}

func (s *Server) persistServiceHeartbeat(
	ctx context.Context,
	token store.ServiceToken,
	rawToken string,
	heartbeat store.ServiceHeartbeat,
	panelOrigins ...string,
) (store.RegisteredService, error) {
	if token.ServiceType != "update_agent" {
		return s.services.Heartbeat(ctx, token, heartbeat)
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	reauthenticated, err := s.reauthenticateServiceToken(ctx, rawToken, token, "service.heartbeat")
	if err != nil {
		return store.RegisteredService{}, err
	}
	service, err := s.services.Heartbeat(ctx, reauthenticated, heartbeat)
	if err != nil {
		return service, err
	}
	panelURL := panelBaseURL(nil)
	if len(panelOrigins) == 1 {
		panelURL = panelOrigins[0]
	}
	// Baseline confirmation is a separate, bounded metadata CAS. A missing or
	// mismatched baseline keeps ST-PORT unavailable without changing heartbeat
	// or stream reconciliation semantics.
	_ = s.confirmSystemUpdatePortBaseline(ctx, service, panelURL)
	return service, nil
}

func serviceBearerToken(r *http.Request) string {
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(raw)
}

func (s *Server) reauthenticateServiceToken(
	ctx context.Context,
	rawToken string,
	previous store.ServiceToken,
	requiredScope string,
) (store.ServiceToken, error) {
	token, err := s.services.AuthenticateServiceToken(ctx, rawToken, requiredScope)
	if err != nil {
		return store.ServiceToken{}, err
	}
	if token.ID != previous.ID {
		return store.ServiceToken{}, store.ErrUnauthorized
	}
	return token, nil
}

func (s *Server) reauthenticateService(
	w http.ResponseWriter,
	r *http.Request,
	previous store.ServiceToken,
	requiredScope string,
) (store.ServiceToken, bool) {
	token, err := s.reauthenticateServiceToken(
		r.Context(),
		serviceBearerToken(r),
		previous,
		requiredScope,
	)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_service_scope"})
		return store.ServiceToken{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_service_token"})
		return store.ServiceToken{}, false
	}
	return token, true
}

func (s *Server) rejectUpdaterIdentityMutationDuringActiveWork(
	w http.ResponseWriter,
	r *http.Request,
	updaterID string,
) bool {
	if s.rejectUpdaterIdentityMutationDuringBootstrap(w, r, updaterID) {
		return true
	}
	identityFenceStore, ok := s.systemUpdates.(store.SystemUpdateIdentityMutationFenceStore)
	if !ok {
		writeJSON(
			w,
			http.StatusServiceUnavailable,
			map[string]string{"code": "system_update_identity_fence_unavailable"},
		)
		return true
	}
	blocked, err := identityFenceStore.HasSystemUpdateIdentityMutationFence(
		r.Context(),
		s.services,
		strings.TrimSpace(updaterID),
	)
	if err != nil {
		writeJSON(
			w,
			http.StatusInternalServerError,
			map[string]string{"code": "check_system_update_identity_fence_failed"},
		)
		return true
	}
	if blocked {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_active"})
		return true
	}
	active, err := s.systemUpdates.HasActiveSystemUpdateReference(
		r.Context(),
		strings.TrimSpace(updaterID),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "check_system_update_active_failed"})
		return true
	}
	if !active {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_active"})
	return true
}

func (s *Server) rejectUpdaterIdentityMutationDuringBootstrap(
	w http.ResponseWriter,
	r *http.Request,
	updaterID string,
) bool {
	updaterID = strings.TrimSpace(updaterID)
	if !updateHostBootstrapIDPattern.MatchString(updaterID) {
		return false
	}
	active, err := s.updateHostBootstrapJobs.HasActiveJob(updaterID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "inspect_updater_host_bootstrap_failed"})
		return true
	}
	if !active {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_host_bootstrap_in_progress"})
	return true
}

type serviceRemediationExecuteRequest struct {
	ActionID   string `json:"action_id"`
	Action     string `json:"action"`
	IncidentID string `json:"incident_id"`
	StreamID   string `json:"stream_id"`
}

func (s *Server) serviceTokenRegisteredForType(ctx context.Context, token store.ServiceToken, serviceType string) (bool, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return false, err
	}
	for _, service := range services {
		if service.TokenID == token.ID && service.ServiceType == serviceType && strings.TrimSpace(service.Status) != "pending" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) registeredServiceForToken(ctx context.Context, token store.ServiceToken) (store.RegisteredService, bool, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, service := range services {
		if service.TokenID == token.ID && service.ServiceType == token.ServiceType && strings.TrimSpace(service.Status) != "pending" {
			return service, true, nil
		}
	}
	return store.RegisteredService{}, false, nil
}

func (s *Server) serviceObservabilitySignal(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "observability.ingest")
	if !ok {
		return
	}
	if token.ServiceType != "worker" && token.ServiceType != "encoder_recorder" {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", "", "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	service, registered, err := s.registeredServiceForToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	if !registered {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", "", "failure", map[string]any{"reason": "service_token_not_registered"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_not_registered"})
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	payload["service_id"] = service.ServiceID
	payload["service_type"] = service.ServiceType
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil || !configured {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "observability_not_configured"})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	body, err := obs.Post(r.Context(), "/signals", payload)
	if err != nil {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "observability_request_failed", "signal_name": stringMapValue(payload, "name"), "stream_id": stringMapValue(payload, "stream_id")})
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "observability_request_failed"})
		return
	}
	s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "success", map[string]any{"signal_name": stringMapValue(payload, "name"), "stream_id": stringMapValue(payload, "stream_id")})
	writeObservabilityJSON(w, http.StatusAccepted, "/signals", body)
}

func primaryAssignmentsContainService(assignments []store.RegisteredService, service store.RegisteredService) bool {
	for _, assignment := range assignments {
		if assignment.ServiceID == service.ServiceID && assignment.ServiceType == service.ServiceType && normalizeAssignmentRole(assignment.AssignmentRole) == "primary" {
			return true
		}
	}
	return false
}

func (s *Server) convergeServiceMediaFailure(r *http.Request, token store.ServiceToken, service store.RegisteredService, trigger, signalName, streamID string, expectedStreamUpdatedAt time.Time) {
	lifecycleRequest, cancel := detachedStopLifecycleRequest(r)
	defer cancel()
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()
	metadata := map[string]any{
		"trigger":             trigger,
		"signal_name":         signalName,
		"source_service_id":   service.ServiceID,
		"source_service_type": service.ServiceType,
	}
	// Re-read assignments while holding the lifecycle lock. The preflight
	// matched an authenticated heartbeat to an exact primary assignment; this
	// second check prevents a concurrent reassignment from turning that stale
	// observation into a Stop against services the reporter no longer owns.
	currentAssignments, assignmentErr := s.streamAssignments(lifecycleRequest.Context(), streamID)
	if assignmentErr != nil {
		metadata["lifecycle_outcome"] = "list_stream_assignments_failed"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "failure", metadata)
		return
	}
	assignments := primaryStreamAssignments(currentAssignments)
	if !primaryAssignmentsContainService(assignments, service) {
		metadata["lifecycle_outcome"] = "assignment_changed"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "failure", metadata)
		return
	}
	stream, err := s.streams.GetStream(lifecycleRequest.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		metadata["lifecycle_outcome"] = "stream_not_found"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "failure", metadata)
		return
	}
	if err != nil {
		metadata["lifecycle_outcome"] = "stream_read_failed"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "failure", metadata)
		return
	}
	metadata["previous_status"] = stream.Status
	if !stream.UpdatedAt.Equal(expectedStreamUpdatedAt) {
		metadata["status"] = stream.Status
		metadata["lifecycle_outcome"] = "heartbeat_observation_superseded"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "success", metadata)
		return
	}
	if !heartbeatMismatchEligible(stream, time.Now().UTC()) {
		metadata["status"] = stream.Status
		metadata["lifecycle_outcome"] = "ignored_stream_status"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "success", metadata)
		return
	}
	failed, transitioned, err := s.streams.TransitionStreamStatus(lifecycleRequest.Context(), streamID, stream.Status, "failed")
	if err != nil {
		metadata["lifecycle_outcome"] = "stream_transition_failed"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "failure", metadata)
		return
	}
	metadata["status"] = failed.Status
	if !transitioned {
		metadata["lifecycle_outcome"] = "transition_superseded"
		s.writeServiceAudit(lifecycleRequest, token, "streams.mark_failed", "stream", streamID, "success", metadata)
		return
	}
	stopResults := sanitizeDispatchResults(s.dispatcher.Stop(lifecycleRequest.Context(), failed, assignments))
	metadata["lifecycle_outcome"] = "failed"
	metadata["stop_dispatch"] = stopResults
	// Stop is allowed to consume its entire bounded lifecycle window. YouTube
	// cleanup and its retry persistence need a fresh detached deadline so a
	// timed-out service cannot also prevent durable provider recovery state.
	cleanupRequest, cleanupCancel := detachedStopLifecycleRequest(r)
	defer cleanupCancel()
	if hasDispatchFailure(stopResults) {
		metadata["stop_dispatch_warning"] = "one or more services did not acknowledge stop"
		staticRuntime, recoveryErr := s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(cleanupRequest.Context(), streamID, trigger+"_stop_unconfirmed")
		if recoveryErr != nil {
			metadata["youtube_cleanup"] = "recovery_fence_failed"
		} else if staticRuntime {
			metadata["youtube_cleanup"] = "recovery_required"
		} else if youtubeMetadata, err := s.completeYouTubeRuntime(cleanupRequest.Context(), streamID, true); err != nil {
			metadata["youtube_cleanup"] = "retry_scheduled"
			metadata["youtube_cleanup_error"] = youtubeCompleteRetryErrorCode(err)
		} else if len(youtubeMetadata) > 0 {
			metadata["youtube_cleanup"] = "completed"
			metadata["youtube_mode"] = youtubeMetadata["mode"]
		} else {
			metadata["youtube_cleanup"] = "not_configured"
		}
		s.writeServiceAudit(cleanupRequest, token, "streams.mark_failed", "stream", streamID, "success", metadata)
		return
	}
	staticRuntime, encoderStopConfirmed, receiptErr := s.markYouTubeRelayStaticEncoderStopConfirmed(cleanupRequest.Context(), streamID, assignments, stopResults)
	if receiptErr != nil {
		metadata["youtube_cleanup"] = "encoder_stop_receipt_failed"
	}
	if staticRuntime {
		// A fixed relay remains fenced until the explicit recovery path can
		// complete it from a confirmed terminal lifecycle. Abandoning the runtime
		// marks the durable claim recovery_required without releasing the binding.
		if _, recoveryErr := s.abandonYouTubeRelayStaticRuntimeForUnconfirmedDispatch(cleanupRequest.Context(), streamID, trigger); recoveryErr != nil {
			metadata["youtube_cleanup"] = "recovery_fence_failed"
		} else if encoderStopConfirmed {
			metadata["youtube_cleanup"] = "encoder_stop_confirmed_recovery_required"
		} else {
			metadata["youtube_cleanup"] = "recovery_required"
		}
	} else if youtubeMetadata, err := s.completeYouTubeRuntime(cleanupRequest.Context(), streamID, true); err != nil {
		metadata["youtube_cleanup"] = "retry_scheduled"
		metadata["youtube_cleanup_error"] = youtubeCompleteRetryErrorCode(err)
	} else if len(youtubeMetadata) > 0 {
		metadata["youtube_cleanup"] = "completed"
		metadata["youtube_mode"] = youtubeMetadata["mode"]
	} else {
		metadata["youtube_cleanup"] = "not_configured"
	}
	s.writeServiceAudit(cleanupRequest, token, "streams.mark_failed", "stream", streamID, "success", metadata)
}

func stringMapValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
