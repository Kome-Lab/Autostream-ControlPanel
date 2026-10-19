package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

type serviceRuntimeCurrentDiscordBindings struct {
	Version          int                                     `json:"version"`
	Bindings         []store.CurrentDiscordCredentialBinding `json:"bindings"`
	AvailabilityCode string                                  `json:"availability_code,omitempty"`
}

func (s *Server) currentDiscordCredentialStores() store.CurrentDiscordCredentialStores {
	return store.CurrentDiscordCredentialStores{Services: s.services, Streams: s.streams, Profiles: s.profiles, Secrets: s.secrets, Leases: s.runtimeLeases}
}

func (s *Server) resolveCurrentDiscordBindingSecret(w http.ResponseWriter, r *http.Request, token store.ServiceToken, body serviceRuntimeSecretResolveRequest) {
	serviceID, secretName := strings.TrimSpace(body.ServiceID), strings.TrimSpace(body.SecretName)
	fail := func(status int, code string) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": code, "secret_name": secretName})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, map[string]string{"code": code})
	}
	if body.CurrentDiscordBindingVersion != 1 || len(body.CurrentDiscordBinding) == 0 ||
		bytes.Equal(bytes.TrimSpace(body.CurrentDiscordBinding), []byte("null")) ||
		strings.TrimSpace(body.StreamID) != "" || strings.TrimSpace(body.ArchiveProfileID) != "" {
		fail(http.StatusBadRequest, "invalid_current_discord_binding")
		return
	}
	var expected store.CurrentDiscordCredentialBinding
	if err := json.Unmarshal(body.CurrentDiscordBinding, &expected); err != nil || expected.ServiceID != serviceID || expected.SecretName != secretName {
		fail(http.StatusBadRequest, "invalid_current_discord_binding")
		return
	}
	value, lease, err := s.currentDiscordCredentialStores().Resolve(r.Context(), serviceID, token.ID, expected, runtimeSecretLeaseTTL)
	switch {
	case errors.Is(err, store.ErrRuntimeSecretLeaseActive):
		fail(http.StatusConflict, "runtime_secret_lease_active")
		return
	case errors.Is(err, store.ErrServiceAssignmentConflict):
		fail(http.StatusConflict, "current_discord_binding_changed")
		return
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrUnauthorized), errors.Is(err, store.ErrNotFound):
		fail(http.StatusForbidden, "current_discord_binding_unavailable")
		return
	case errors.Is(err, store.ErrCurrentDiscordBindingUnavailable):
		fail(http.StatusServiceUnavailable, "current_discord_binding_unavailable")
		return
	case err != nil:
		fail(http.StatusInternalServerError, "resolve_runtime_secret_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "success", map[string]any{"secret_name": secretName, "current_binding": true, "lease_expires_at": lease.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")})
	writeOneTimeSecretJSON(w, http.StatusOK, serviceRuntimeSecretResolveResponse{SecretName: secretName, Value: value, ExpiresInSec: int(runtimeSecretLeaseTTL.Seconds())})
}
