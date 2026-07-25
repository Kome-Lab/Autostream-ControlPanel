package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"golang.org/x/crypto/ssh"
)

type updaterPolicyUpdateRequest struct {
	ExpectedRevision         int64                       `json:"expected_revision"`
	API                      store.UpdaterPolicyAPI      `json:"api"`
	PollIntervalSeconds      int                         `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds int                         `json:"heartbeat_interval_seconds"`
	Hosts                    []store.UpdaterPolicyHost   `json:"hosts"`
	Targets                  []store.UpdaterPolicyTarget `json:"targets"`
	GitHubToken              *string                     `json:"github_token"`
}

type updaterPolicyHostResponse struct {
	HostID                   string `json:"host_id"`
	Name                     string `json:"name"`
	Address                  string `json:"address"`
	Port                     int    `json:"port"`
	User                     string `json:"user"`
	Arch                     string `json:"arch"`
	HostPublicKey            string `json:"host_public_key"`
	HostPublicKeyFingerprint string `json:"host_public_key_fingerprint"`
	SSHClientPublicKey       string `json:"ssh_client_public_key,omitempty"`
	SSHClientKeyFingerprint  string `json:"ssh_client_key_fingerprint,omitempty"`
}

type updaterPolicyResponse struct {
	UpdaterID                string                      `json:"updater_id"`
	Revision                 int64                       `json:"revision"`
	API                      store.UpdaterPolicyAPI      `json:"api"`
	PollIntervalSeconds      int                         `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds int                         `json:"heartbeat_interval_seconds"`
	Hosts                    []updaterPolicyHostResponse `json:"hosts"`
	Targets                  []store.UpdaterPolicyTarget `json:"targets"`
	UpdatedAt                time.Time                   `json:"updated_at"`
	GitHubTokenConfigured    *bool                       `json:"github_token_configured,omitempty"`
	GitHubTokenFingerprint   string                      `json:"github_token_fingerprint,omitempty"`
}

func (s *Server) getUpdaterPolicy(w http.ResponseWriter, r *http.Request) {
	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), updaterID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "updater_policy_not_configured"})
		return
	}
	if errors.Is(err, store.ErrInvalidSettings) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_update_agent"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_policy_failed"})
		return
	}
	tokenStatus, err := s.updaterReleaseTokenStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_status_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, makeUpdaterPolicyResponse(
		policy,
		&tokenStatus,
		&agent,
		canViewUpdaterReleaseTokenFingerprint(r.Context()),
	))
}

func (s *Server) updateUpdaterPolicy(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "secrets.update") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	var body updaterPolicyUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.ExpectedRevision < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_policy_revision"})
		return
	}
	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	policy, err := normalizedUpdaterPolicyRequest(updaterID, body)
	if err != nil {
		code := "invalid_updater_policy"
		if errors.Is(err, errInvalidUpdaterHostPublicKey) {
			code = "invalid_updater_host_public_key"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	saved, tokenStatus, err := s.updaterPolicies.SaveUpdaterPolicyAndReleaseToken(
		r.Context(),
		updaterID,
		body.ExpectedRevision,
		policy,
		body.GitHubToken,
	)
	if err != nil {
		writeUpdaterPolicySaveError(w, err)
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: current.User.ID, ActorUsername: current.User.Username,
		Action: "system_updates.updater_policy.save", ResourceType: "update_agent", ResourceID: updaterID, Result: "success",
		Metadata: map[string]any{
			"revision": saved.Revision, "host_count": len(saved.Hosts), "target_count": len(saved.Targets),
			"github_token_changed": body.GitHubToken != nil,
		},
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, makeUpdaterPolicyResponse(
		saved,
		&tokenStatus,
		&agent,
		canViewUpdaterReleaseTokenFingerprint(r.Context()),
	))
}

func (s *Server) serviceUpdaterPolicy(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "updates.claim")
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		ServiceID       string `json:"service_id"`
		CurrentRevision int64  `json:"current_revision"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || body.CurrentRevision < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), agent.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_not_configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_policy_failed"})
		return
	}
	if body.CurrentRevision == policy.Revision {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if body.CurrentRevision > policy.Revision {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_revision_ahead"})
		return
	}
	writeJSON(w, http.StatusOK, makeUpdaterPolicyResponse(policy, nil, nil, false))
}

var errInvalidUpdaterHostPublicKey = errors.New("invalid updater host public key")

func normalizedUpdaterPolicyRequest(updaterID string, body updaterPolicyUpdateRequest) (store.UpdaterPolicy, error) {
	policy := store.UpdaterPolicy{
		UpdaterID: updaterID, API: body.API,
		PollIntervalSeconds: body.PollIntervalSeconds, HeartbeatIntervalSeconds: body.HeartbeatIntervalSeconds,
		Hosts: append([]store.UpdaterPolicyHost(nil), body.Hosts...), Targets: append([]store.UpdaterPolicyTarget(nil), body.Targets...),
	}
	for index := range policy.Hosts {
		key, err := parseUpdaterED25519PublicKey(policy.Hosts[index].HostPublicKey)
		if err != nil {
			return store.UpdaterPolicy{}, errInvalidUpdaterHostPublicKey
		}
		policy.Hosts[index].HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	return store.NormalizeUpdaterPolicy(updaterID, policy)
}

func parseUpdaterED25519PublicKey(raw string) (ssh.PublicKey, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
	if err != nil || len(options) != 0 || len(rest) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return nil, errInvalidUpdaterHostPublicKey
	}
	return key, nil
}

func makeUpdaterPolicyResponse(policy store.UpdaterPolicy, tokenStatus *store.SecretStatus, agent *store.RegisteredService, includeTokenFingerprint bool) updaterPolicyResponse {
	clientKeys := map[string]string{}
	if agent != nil {
		clientKeys = capabilityStringMap(agent.ReportedCapabilities["ssh_client_public_keys"])
	}
	hosts := make([]updaterPolicyHostResponse, 0, len(policy.Hosts))
	for _, host := range policy.Hosts {
		responseHost := updaterPolicyHostResponse{
			HostID: host.HostID, Name: host.Name, Address: host.Address, Port: host.Port, User: host.User, Arch: host.Arch,
			HostPublicKey: host.HostPublicKey,
		}
		if publicKey, err := parseUpdaterED25519PublicKey(host.HostPublicKey); err == nil {
			responseHost.HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
			responseHost.HostPublicKeyFingerprint = ssh.FingerprintSHA256(publicKey)
		}
		if clientKey, err := parseUpdaterED25519PublicKey(clientKeys[host.HostID]); err == nil {
			responseHost.SSHClientPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(clientKey)))
			responseHost.SSHClientKeyFingerprint = ssh.FingerprintSHA256(clientKey)
		}
		hosts = append(hosts, responseHost)
	}
	response := updaterPolicyResponse{
		UpdaterID: policy.UpdaterID, Revision: policy.Revision, API: policy.API,
		PollIntervalSeconds: policy.PollIntervalSeconds, HeartbeatIntervalSeconds: policy.HeartbeatIntervalSeconds,
		Hosts: hosts, Targets: append([]store.UpdaterPolicyTarget(nil), policy.Targets...), UpdatedAt: policy.UpdatedAt,
	}
	if tokenStatus != nil {
		configured := tokenStatus.Configured
		response.GitHubTokenConfigured = &configured
		if includeTokenFingerprint {
			response.GitHubTokenFingerprint = tokenStatus.Fingerprint
		}
	}
	return response
}

func canViewUpdaterReleaseTokenFingerprint(ctx context.Context) bool {
	permissions := currentFromContext(ctx).Permissions
	return security.HasPermission(permissions, "secrets.read_status") ||
		security.HasPermission(permissions, "secrets.update")
}

func (s *Server) registeredUpdateAgent(w http.ResponseWriter, r *http.Request, updaterID string) (store.RegisteredService, bool) {
	if updaterID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_update_agent"})
		return store.RegisteredService{}, false
	}
	agent, err := s.services.GetService(r.Context(), updaterID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "update_agent_not_registered"})
		return store.RegisteredService{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_update_agent_failed"})
		return store.RegisteredService{}, false
	}
	if agent.ServiceType != "update_agent" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "update_agent_required"})
		return store.RegisteredService{}, false
	}
	return agent, true
}

func (s *Server) updaterReleaseTokenStatus(ctx context.Context) (store.SecretStatus, error) {
	return s.updaterPolicies.GetUpdaterReleaseTokenStatus(ctx)
}

func writeUpdaterPolicySaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_revision_conflict"})
	case errors.Is(err, store.ErrInvalidSettings):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_policy"})
	case errors.Is(err, store.ErrSecretKeyRequired):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "updater_release_token_encryption_key_required"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "save_updater_policy_failed"})
	}
}
