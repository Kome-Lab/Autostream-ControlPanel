package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
)

func (s *Server) registerPublicRoutes() {
	s.mux.HandleFunc("GET /{$}", s.rootRedirect)
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /updater/version", s.updaterVersion)
	s.mux.HandleFunc("GET /setup/status", s.setupStatus)
	s.mux.HandleFunc("POST /setup/first-admin", s.setupFirstAdmin)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "control-panel"})
}

func (s *Server) updaterVersion(w http.ResponseWriter, _ *http.Request) {
	configRevision, err := controlPanelConfigRevision()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "invalid_service_config_revision"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         version.Current(),
		"service_id":      controlPanelSystemUpdateServiceID,
		"service_type":    "control_panel",
		"config_revision": configRevision,
	})
}

func controlPanelConfigRevision() (int64, error) {
	raw := os.Getenv("AUTOSTREAM_CONFIG_REVISION")
	if raw == "" {
		return 1, nil
	}
	if raw[0] < '1' || raw[0] > '9' {
		return 0, errors.New("config revision must be a positive decimal integer")
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, errors.New("config revision must be a positive decimal integer")
		}
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("config revision must fit in int64")
	}
	return revision, nil
}

func (s *Server) rootRedirect(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if s.auth != nil {
		count, err := s.auth.CountUsers(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
			return
		}
		if s.setupToken != "" && count == 0 {
			target = "/setup"
		} else if _, ok := s.authenticate(r); ok {
			target = "/admin"
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	setupEnabled := s.auth != nil && s.setupToken != ""
	if s.auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"setup_enabled": false, "setup_required": false})
		return
	}
	count, err := s.auth.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_enabled":  setupEnabled,
		"setup_required": setupEnabled && count == 0,
	})
}

func (s *Server) setupFirstAdmin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.setupToken == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "setup_disabled"})
		return
	}
	var body struct {
		SetupToken string `json:"setup_token"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	setupAttemptKey := loginFailureKey("setup:first-admin", clientIP(r))
	if !s.loginFailures.allow(setupAttemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "setup_rate_limited"})
		return
	}
	if !security.VerifyTokenHash(body.SetupToken, security.HashToken(s.setupToken)) {
		s.loginFailures.record(setupAttemptKey)
		s.writeAudit(r, store.AuditEvent{Action: "setup.first_admin", ResourceType: "user", Result: "failure", Metadata: map[string]any{"reason": "invalid_setup_token"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_setup_token"})
		return
	}
	count, err := s.auth.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
		return
	}
	if count > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "users_already_exist"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "username_required"})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.Password) {
		return
	}
	user, err := s.auth.CreateFirstAdmin(r.Context(), username, body.Password, security.DefaultPermissions)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_first_admin_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "setup.first_admin", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	s.loginFailures.clear(setupAttemptKey)
	writeJSON(w, http.StatusCreated, map[string]any{"user": publicUser(user)})
}
