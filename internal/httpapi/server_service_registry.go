package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const serviceHeartbeatWarningDefault = 60 * time.Second

const serviceHeartbeatOfflineDefault = 180 * time.Second

func (s *Server) registerNodeServiceRoutes() {
	s.mux.HandleFunc("GET /api-tokens", s.requirePermission("api_tokens.read", s.listServiceTokens))
	s.mux.HandleFunc("POST /api-tokens", s.requirePermission("api_tokens.create", s.createServiceToken))
	s.mux.HandleFunc("POST /api-tokens/{id}/rotate", s.requirePermission("api_tokens.create", s.rotateServiceToken))
	s.mux.HandleFunc("DELETE /api-tokens/{id}", s.requirePermission("api_tokens.revoke", s.revokeServiceToken))
	s.mux.HandleFunc("GET /nodes", s.requirePermission("api_tokens.create", s.listNodes))
	s.mux.HandleFunc("GET /nodes/action-permissions", nodeActionProjectionBoundary(s.requirePermission("api_tokens.create", s.nodeActionPermissionProjection)))
	s.mux.HandleFunc("POST /nodes/registration-tokens", s.requirePermission("api_tokens.create", s.createNodeRegistrationToken))
	s.mux.HandleFunc("PUT /nodes/{id}", s.requirePermission("api_tokens.create", s.updateNode))
	s.mux.HandleFunc("GET /nodes/{id}/configuration", s.requireAnyPermission([]string{"service_health.read", "api_tokens.create"}, s.nodeConfiguration))
	s.mux.HandleFunc("POST /nodes/{id}/configure-token", s.requirePermission("api_tokens.create", s.regenerateNodeConfigureToken))
	s.mux.HandleFunc("POST /nodes/{id}/rotate-token", s.requirePermission("api_tokens.create", s.rotateNodeRuntimeToken))
	s.mux.HandleFunc("GET /service-health", s.requirePermission("service_health.read", s.listServices))
	s.mux.HandleFunc("GET /service-health/{id}/runtime-config", s.requirePermission("service_health.read", s.adminServiceRuntimeConfig))
	s.mux.HandleFunc("POST /services/{id}/assign", s.requirePermission("services.assign", s.assignService))
	s.mux.HandleFunc("DELETE /services/{id}/assignment", s.requirePermission("services.unassign", s.unassignService))
	s.mux.HandleFunc("DELETE /services/{id}", s.requirePermission("services.disable", s.deleteService))
	s.mux.HandleFunc("GET /workers", s.requirePermission("workers.read", s.listWorkers))
	s.mux.HandleFunc("GET /workers/{id}", s.requirePermission("workers.read", s.getWorker))
	s.mux.HandleFunc("POST /workers/{id}/assign", s.requirePermission("workers.assign", s.assignWorker))
	s.mux.HandleFunc("DELETE /workers/{id}/assignment", s.requirePermission("workers.unassign", s.unassignWorker))
	s.mux.HandleFunc("POST /workers/{id}/restart", s.requirePermission("workers.restart", s.restartWorker))
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	writeJSON(w, http.StatusOK, serviceHealthResponses(services, time.Now().UTC()))
}

func heartbeatWarningAfter() time.Duration {
	return durationEnv("AUTOSTREAM_NODE_HEARTBEAT_WARNING_AFTER", serviceHeartbeatWarningDefault)
}

func heartbeatOfflineAfter() time.Duration {
	return durationEnv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", serviceHeartbeatOfflineDefault)
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func heartbeatAge(at *time.Time, now time.Time) *int64 {
	if at == nil {
		return nil
	}
	age := int64(now.Sub(*at).Seconds())
	if age < 0 {
		age = 0
	}
	return &age
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.services.ListWorkers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_workers_failed"})
		return
	}
	writeJSON(w, http.StatusOK, workers)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if worker.ServiceType != "worker" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func serviceAssignmentHTTPError(err error, unassign bool) (code string, status int, handled bool) {
	switch {
	case errors.Is(err, store.ErrServiceAssignmentConflict):
		return "service_assignment_conflict", http.StatusConflict, true
	case errors.Is(err, store.ErrServiceAssignmentProtectedStream):
		return "service_assignment_protected_stream", http.StatusConflict, true
	case errors.Is(err, store.ErrServiceUnassignProtectedStream):
		if unassign {
			return "service_unassign_protected_stream", http.StatusConflict, true
		}
		return "service_assignment_protected_stream", http.StatusConflict, true
	case errors.Is(err, store.ErrServiceAssignmentGuardUnavailable):
		return "service_assignment_guard_unavailable", http.StatusServiceUnavailable, true
	default:
		return "", 0, false
	}
}

func (s *Server) assignService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StreamID       string `json:"stream_id"`
		AssignmentRole string `json:"assignment_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.AssignmentRole = normalizeAssignmentRole(body.AssignmentRole)
	if body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_id_required"})
		return
	}
	if _, err := s.streams.GetStream(r.Context(), body.StreamID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if existing.ServiceType == "update_agent" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_assignment_unsupported"})
		return
	}
	current := currentFromContext(r.Context())
	service, err := s.services.AssignServiceToStreamGuarded(r.Context(), store.ServiceAssignmentMutation{ServiceID: existing.ServiceID, StreamID: body.StreamID, ActorUserID: current.User.ID, AssignmentRole: body.AssignmentRole})
	if err != nil {
		if errors.Is(err, store.ErrInvalidServiceAssignment) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "service_assignment_unsupported"})
			return
		}
		if code, status, handled := serviceAssignmentHTTPError(err, false); handled {
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_service_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.assign", ResourceType: "service", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": body.StreamID, "service_type": service.ServiceType, "assignment_role": body.AssignmentRole}})
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) unassignService(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	previousStreamID := existing.CurrentStreamID
	current := currentFromContext(r.Context())
	service, err := s.services.UnassignServiceFromStreamGuarded(r.Context(), store.ServiceUnassignmentMutation{ServiceID: existing.ServiceID, ActorUserID: current.User.ID})
	if err != nil {
		if code, status, handled := serviceAssignmentHTTPError(err, true); handled {
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "unassign_service_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.unassign", ResourceType: "service", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"previous_stream_id": previousStreamID, "service_type": service.ServiceType}})
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	current := currentFromContext(r.Context())
	if existing.ServiceType == "update_agent" {
		if s.rejectUpdaterIdentityMutationDuringActiveWork(
			w, r, existing.ServiceID,
		) {
			return
		}
	} else {
		activeUpdate, activeErr := s.systemUpdates.HasActiveSystemUpdateReference(
			r.Context(), existing.ServiceID,
		)
		if activeErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "check_system_update_active_failed"})
			return
		}
		if activeUpdate {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.delete", ResourceType: "service", ResourceID: existing.ServiceID, Result: "failure", Metadata: map[string]any{"reason": "system_update_active", "service_type": existing.ServiceType}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_active"})
			return
		}
	}
	if err := s.services.DeleteService(r.Context(), existing.ServiceID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	} else if code, status, handled := serviceAssignmentHTTPError(err, true); handled {
		writeJSON(w, status, map[string]string{"code": code})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_service_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.delete", ResourceType: "service", ResourceID: existing.ServiceID, Result: "success", Metadata: map[string]any{"service_type": existing.ServiceType, "previous_stream_id": existing.CurrentStreamID}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) assignWorker(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StreamID       string `json:"stream_id"`
		AssignmentRole string `json:"assignment_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.AssignmentRole = normalizeAssignmentRole(body.AssignmentRole)
	if body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_id_required"})
		return
	}
	if _, err := s.streams.GetStream(r.Context(), body.StreamID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	current := currentFromContext(r.Context())
	worker, err := s.services.AssignServiceToStreamGuarded(r.Context(), store.ServiceAssignmentMutation{ServiceID: r.PathValue("id"), StreamID: body.StreamID, ActorUserID: current.User.ID, AssignmentRole: body.AssignmentRole})
	if err != nil {
		if code, status, handled := serviceAssignmentHTTPError(err, false); handled {
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_worker_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.assign", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": body.StreamID, "assignment_role": body.AssignmentRole}})
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) unassignWorker(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	previousStreamID := existing.CurrentStreamID
	current := currentFromContext(r.Context())
	worker, err := s.services.UnassignServiceFromStreamGuarded(r.Context(), store.ServiceUnassignmentMutation{ServiceID: existing.ServiceID, ActorUserID: current.User.ID})
	if err != nil {
		if code, status, handled := serviceAssignmentHTTPError(err, true); handled {
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "unassign_worker_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.unassign", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success", Metadata: map[string]any{"previous_stream_id": previousStreamID}})
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) restartWorker(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	worker, err := s.services.RequestServiceRestart(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "restart_worker_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.restart", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success"})
	writeJSON(w, http.StatusAccepted, worker)
}
