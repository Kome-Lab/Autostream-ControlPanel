package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerSecretRoutes() {
	s.mux.HandleFunc("GET /secrets/status", s.requirePermission("secrets.read_status", s.secretStatus))
	s.mux.HandleFunc("PUT /secrets/{name}", s.requirePermission("secrets.update", s.updateSecret))
}

func (s *Server) secretStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.secrets.ListSecretStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "secret_status_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": status})
}

func (s *Server) updateSecret(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secret_name_required"})
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	status, err := s.secrets.UpdateSecret(r.Context(), name, body.Value)
	if errors.Is(err, store.ErrUnknownSecret) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "failure", Metadata: map[string]any{"reason": "unknown_secret"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "unknown_secret"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "failure", Metadata: map[string]any{"reason": "secret_encryption_key_required", "configured": body.Value != ""}})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "secret_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "success", Metadata: map[string]any{"configured": status.Configured}})
	writeJSON(w, http.StatusOK, status)
}
