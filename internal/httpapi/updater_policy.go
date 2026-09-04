package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"golang.org/x/crypto/ssh"
)

type updaterPolicyUpdateRequest struct {
	ExpectedRevision          int64                        `json:"expected_revision"`
	PollIntervalSeconds       int                          `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds  int                          `json:"heartbeat_interval_seconds"`
	Hosts                     []store.UpdaterPolicyHost    `json:"hosts"`
	Targets                   []updaterPolicyTargetRequest `json:"targets"`
	LocalExecutorPolicySHA256 string                       `json:"local_executor_policy_sha256,omitempty"`
	GitHubToken               *string                      `json:"github_token"`
}

type updaterPolicyTargetRequest struct {
	TargetID        string          `json:"target_id"`
	ServiceID       string          `json:"service_id"`
	HostID          string          `json:"host_id"`
	ServiceType     string          `json:"service_type"`
	DeploymentMode  string          `json:"deployment_mode"`
	DatabaseName    json.RawMessage `json:"database_name,omitempty"`
	LocalListenPort json.RawMessage `json:"local_listen_port,omitempty"`
}

type activatePullUpdaterOwnershipRequest struct {
	ExpectedExecutionHostID             string `json:"expected_execution_host_id"`
	ExpectedOwnershipEpoch              int64  `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64  `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64  `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64  `json:"expected_local_executor_policy_revision"`
	ExpectedLocalExecutorPolicySHA256   string `json:"expected_local_executor_policy_sha256"`
}

type activatePullUpdaterOwnershipResponse struct {
	UpdaterID                   string `json:"updater_id"`
	ExecutionHostID             string `json:"execution_host_id"`
	TransportMode               string `json:"transport_mode"`
	AgentServiceID              string `json:"agent_service_id"`
	OwnershipEpoch              int64  `json:"ownership_epoch"`
	SourcePolicyRevision        int64  `json:"source_policy_revision"`
	ProjectionRevision          int64  `json:"projection_revision"`
	LocalExecutorPolicyRevision int64  `json:"local_executor_policy_revision"`
	LocalExecutorPolicySHA256   string `json:"local_executor_policy_sha256"`
}

type deactivatePullUpdaterOwnershipRequest struct {
	ExpectedExecutionHostID             string `json:"expected_execution_host_id"`
	ExpectedOwnershipEpoch              int64  `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64  `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64  `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64  `json:"expected_local_executor_policy_revision"`
	ExpectedLocalExecutorPolicySHA256   string `json:"expected_local_executor_policy_sha256"`
}

type deactivatePullUpdaterOwnershipResponse struct {
	UpdaterID                   string `json:"updater_id"`
	ExecutionHostID             string `json:"execution_host_id"`
	TransportMode               string `json:"transport_mode"`
	AgentServiceID              string `json:"agent_service_id"`
	OwnershipEpoch              int64  `json:"ownership_epoch"`
	AgentOwnershipEpoch         int64  `json:"agent_ownership_epoch"`
	SourcePolicyRevision        int64  `json:"source_policy_revision"`
	ProjectionRevision          int64  `json:"projection_revision"`
	LocalExecutorPolicyRevision int64  `json:"local_executor_policy_revision"`
	LocalExecutorPolicySHA256   string `json:"local_executor_policy_sha256"`
}

type updaterPolicyTargetResponse struct {
	TargetID        string `json:"target_id"`
	ServiceID       string `json:"service_id"`
	HostID          string `json:"host_id"`
	ServiceType     string `json:"service_type"`
	DeploymentMode  string `json:"deployment_mode"`
	DatabaseName    string `json:"database_name,omitempty"`
	LocalListenPort int    `json:"local_listen_port,omitempty"`
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
	UpdaterID                   string                               `json:"updater_id"`
	Revision                    int64                                `json:"revision"`
	ProjectionRevision          int64                                `json:"projection_revision"`
	LocalExecutorPolicyRevision int64                                `json:"local_executor_policy_revision"`
	TransportMode               string                               `json:"transport_mode"`
	ExecutionHostID             string                               `json:"execution_host_id,omitempty"`
	LocalExecutorPolicySHA256   string                               `json:"local_executor_policy_sha256,omitempty"`
	PollIntervalSeconds         int                                  `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds    int                                  `json:"heartbeat_interval_seconds"`
	Hosts                       []updaterPolicyHostResponse          `json:"hosts"`
	Targets                     []updaterPolicyTargetResponse        `json:"targets"`
	UpdatedAt                   time.Time                            `json:"updated_at"`
	GitHubTokenConfigured       bool                                 `json:"github_token_configured"`
	GitHubTokenFingerprint      string                               `json:"github_token_fingerprint,omitempty"`
	ExecutionHostOwnership      *store.SystemUpdateExecutionHost     `json:"execution_host_ownership,omitempty"`
	PullActivation              *updaterPullActivationStatusResponse `json:"pull_activation,omitempty"`
}

type updaterPullActivationStatusResponse struct {
	Ready                      bool       `json:"ready"`
	BlockedReason              string     `json:"blocked_reason,omitempty"`
	Status                     string     `json:"status"`
	LastHeartbeatAt            *time.Time `json:"last_heartbeat_at,omitempty"`
	ObserveOnly                bool       `json:"observe_only"`
	UpdateExecutor             bool       `json:"update_executor"`
	MutationEnabled            bool       `json:"mutation_enabled"`
	RecoveryPending            bool       `json:"recovery_pending"`
	ReportedOwnershipEpoch     int64      `json:"reported_ownership_epoch"`
	ReportedProjectionRevision int64      `json:"reported_projection_revision"`
}

func (s *Server) getUpdaterPolicy(w http.ResponseWriter, r *http.Request) {
	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	updaterID = agent.ServiceID
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
	w.Header().Set("Cache-Control", "no-store")
	response := makeUpdaterPolicyResponse(policy, &agent)
	if err := s.enrichUpdaterReleaseTokenResponse(r.Context(), &response, canViewUpdaterReleaseTokenFingerprint(r.Context())); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_status_failed"})
		return
	}
	if err := s.enrichPullUpdaterPolicyResponse(r.Context(), &response, agent, policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_activation_status_failed"})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) updateUpdaterPolicy(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
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
	if _, err := normalizeUpdaterReleaseToken(body.GitHubToken); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_release_token"})
		return
	}
	if body.GitHubToken != nil && !runtimeSecretTransportAllowed(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secure_transport_required"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()

	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	updaterID = agent.ServiceID
	if body.GitHubToken != nil && !security.HasPermission(current.Permissions, "secrets.update") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, body)
	if err != nil {
		code := "invalid_updater_policy"
		if errors.Is(err, errInvalidUpdaterHostPublicKey) {
			code = "invalid_updater_host_public_key"
		} else if errors.Is(err, errInvalidUpdaterDatabaseName) {
			code = "invalid_updater_database_name"
		} else if errors.Is(err, errInvalidUpdaterLocalListenPort) {
			code = "invalid_updater_local_listen_port"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	bootstrapActive, err := s.updateHostBootstrapJobs.HasActiveJob(updaterID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "inspect_updater_host_bootstrap_failed"})
		return
	}
	if bootstrapActive {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_host_bootstrap_in_progress"})
		return
	}
	policy, err = s.canonicalizePullUpdaterLocalListenerBindings(r.Context(), policy)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "inspect_updater_target_endpoints_failed"})
		return
	}

	executionHosts, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "system_update_execution_host_store_unavailable"})
		return
	}
	pendingInitialPolicy := systemUpdateAgentPendingInitialPullPolicy(agent)
	expectedOwnershipEpoch := agent.OwnershipEpoch
	if agent.OwnershipEpoch == 0 &&
		(systemUpdateAgentObserveOnly(agent) || pendingInitialPolicy) {
		currentOwnership, ownershipErr := executionHosts.GetSystemUpdateExecutionHost(
			r.Context(),
			agent.ExecutionHostID,
		)
		if ownershipErr != nil {
			writeUpdaterPolicySaveError(w, ownershipErr)
			return
		}
		expectedOwnershipEpoch = currentOwnership.OwnershipEpoch
	}
	saved, err := s.updaterPolicies.SavePullUpdaterPolicy(
		r.Context(),
		executionHosts,
		updaterID,
		body.ExpectedRevision,
		expectedOwnershipEpoch,
		policy,
	)
	if err != nil {
		writeUpdaterPolicySaveError(w, err)
		return
	}
	if body.GitHubToken != nil {
		normalizedToken, _ := normalizeUpdaterReleaseToken(body.GitHubToken)
		if _, err := s.secrets.UpdateSecret(r.Context(), store.UpdaterGitHubReleaseTokenSecretName, *normalizedToken); err != nil {
			writeUpdaterPolicySaveError(w, err)
			return
		}
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
	response := makeUpdaterPolicyResponse(saved, &agent)
	if err := s.enrichUpdaterReleaseTokenResponse(r.Context(), &response, canViewUpdaterReleaseTokenFingerprint(r.Context())); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_release_token_status_failed"})
		return
	}
	if err := s.enrichPullUpdaterPolicyResponse(r.Context(), &response, agent, saved); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_updater_activation_status_failed"})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) activatePullUpdaterOwnership(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body activatePullUpdaterOwnershipRequest
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
	body.ExpectedExecutionHostID = strings.TrimSpace(body.ExpectedExecutionHostID)
	body.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(body.ExpectedLocalExecutorPolicySHA256))
	if body.ExpectedExecutionHostID == "" ||
		body.ExpectedOwnershipEpoch < 0 ||
		body.ExpectedSourcePolicyRevision < 1 ||
		body.ExpectedProjectionRevision < 1 ||
		body.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validUpdateManifestDigest(body.ExpectedLocalExecutorPolicySHA256) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_pull_activation_request"})
		return
	}

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()

	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	if agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		agent.OwnershipEpoch != 0 ||
		agent.ExecutionHostID != body.ExpectedExecutionHostID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	executionHosts, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "system_update_execution_host_store_unavailable"})
		return
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), agent.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		writePullUpdaterActivationError(w, store.ErrConflict)
		return
	}
	if err != nil {
		writePullUpdaterActivationError(w, err)
		return
	}
	controlPanelTarget, err := controlPanelPullUpdaterActivationTarget(policy)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "control_panel_update_target_unavailable",
		})
		return
	}
	result, err := s.updaterPolicies.ActivatePullUpdaterOwnership(
		r.Context(),
		s.services,
		executionHosts,
		store.ActivatePullUpdaterOwnershipParams{
			ServiceID:                           agent.ServiceID,
			ExecutionHostID:                     body.ExpectedExecutionHostID,
			ExpectedExecutionHostOwnershipEpoch: body.ExpectedOwnershipEpoch,
			ExpectedSourcePolicyRevision:        body.ExpectedSourcePolicyRevision,
			ExpectedProjectionRevision:          body.ExpectedProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: body.ExpectedLocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   body.ExpectedLocalExecutorPolicySHA256,
			ControlPanelTarget:                  controlPanelTarget,
		},
	)
	if err != nil {
		writePullUpdaterActivationError(w, err)
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: current.User.ID, ActorUsername: current.User.Username,
		Action: "system_updates.pull_ownership.activate", ResourceType: "update_agent", ResourceID: agent.ServiceID, Result: "success",
		Metadata: map[string]any{
			"execution_host_id":              result.Ownership.ExecutionHostID,
			"ownership_epoch":                result.Ownership.OwnershipEpoch,
			"source_policy_revision":         result.Policy.Revision,
			"projection_revision":            result.Policy.ProjectionRevision,
			"local_executor_policy_revision": result.Policy.LocalExecutorPolicyRevision,
		},
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, activatePullUpdaterOwnershipResponse{
		UpdaterID:                   result.Service.ServiceID,
		ExecutionHostID:             result.Ownership.ExecutionHostID,
		TransportMode:               result.Ownership.TransportMode,
		AgentServiceID:              result.Ownership.AgentServiceID,
		OwnershipEpoch:              result.Ownership.OwnershipEpoch,
		SourcePolicyRevision:        result.Policy.Revision,
		ProjectionRevision:          result.Policy.ProjectionRevision,
		LocalExecutorPolicyRevision: result.Policy.LocalExecutorPolicyRevision,
		LocalExecutorPolicySHA256:   result.Policy.LocalExecutorPolicySHA256,
	})
}

func writePullUpdaterActivationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidSettings):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_pull_activation_request"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_revision_conflict"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_execution_host_busy"})
	case errors.Is(err, store.ErrSystemUpdateAgentNotReady):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_not_ready"})
	case errors.Is(err, store.ErrSystemUpdateAgentInactive):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "update_agent_inactive"})
	case errors.Is(err, store.ErrServicePortReserved):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "service_port_reserved"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostStale),
		errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "activate_pull_ownership_failed"})
	}
}

func (s *Server) deactivatePullUpdaterOwnership(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body deactivatePullUpdaterOwnershipRequest
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
	body.ExpectedExecutionHostID = strings.TrimSpace(body.ExpectedExecutionHostID)
	body.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(body.ExpectedLocalExecutorPolicySHA256),
	)
	if body.ExpectedExecutionHostID == "" ||
		body.ExpectedOwnershipEpoch < 1 ||
		body.ExpectedSourcePolicyRevision < 1 ||
		body.ExpectedProjectionRevision < 1 ||
		body.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validUpdateManifestDigest(body.ExpectedLocalExecutorPolicySHA256) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_pull_deactivation_request"})
		return
	}

	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()

	updaterID := strings.TrimSpace(r.PathValue("id"))
	agent, ok := s.registeredUpdateAgent(w, r, updaterID)
	if !ok {
		return
	}
	if agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		agent.OwnershipEpoch != body.ExpectedOwnershipEpoch ||
		agent.ExecutionHostID != body.ExpectedExecutionHostID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
		return
	}
	executionHosts, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "system_update_execution_host_store_unavailable"})
		return
	}
	result, err := s.updaterPolicies.DeactivatePullUpdaterOwnership(
		r.Context(),
		s.services,
		executionHosts,
		store.DeactivatePullUpdaterOwnershipParams{
			ServiceID:                           agent.ServiceID,
			ExecutionHostID:                     body.ExpectedExecutionHostID,
			ExpectedExecutionHostOwnershipEpoch: body.ExpectedOwnershipEpoch,
			ExpectedSourcePolicyRevision:        body.ExpectedSourcePolicyRevision,
			ExpectedProjectionRevision:          body.ExpectedProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: body.ExpectedLocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   body.ExpectedLocalExecutorPolicySHA256,
		},
	)
	if err != nil {
		writePullUpdaterDeactivationError(w, err)
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: current.User.ID, ActorUsername: current.User.Username,
		Action: "system_updates.pull_ownership.deactivate", ResourceType: "update_agent", ResourceID: agent.ServiceID, Result: "success",
		Metadata: map[string]any{
			"execution_host_id":              result.Ownership.ExecutionHostID,
			"agent_service_id":               result.Ownership.AgentServiceID,
			"ownership_epoch":                result.Ownership.OwnershipEpoch,
			"agent_ownership_epoch":          result.Service.OwnershipEpoch,
			"source_policy_revision":         result.Policy.Revision,
			"projection_revision":            result.Policy.ProjectionRevision,
			"local_executor_policy_revision": result.Policy.LocalExecutorPolicyRevision,
		},
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deactivatePullUpdaterOwnershipResponse{
		UpdaterID:                   result.Service.ServiceID,
		ExecutionHostID:             result.Ownership.ExecutionHostID,
		TransportMode:               result.Ownership.TransportMode,
		AgentServiceID:              result.Ownership.AgentServiceID,
		OwnershipEpoch:              result.Ownership.OwnershipEpoch,
		AgentOwnershipEpoch:         result.Service.OwnershipEpoch,
		SourcePolicyRevision:        result.Policy.Revision,
		ProjectionRevision:          result.Policy.ProjectionRevision,
		LocalExecutorPolicyRevision: result.Policy.LocalExecutorPolicyRevision,
		LocalExecutorPolicySHA256:   result.Policy.LocalExecutorPolicySHA256,
	})
}

func writePullUpdaterDeactivationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidSettings):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_pull_deactivation_request"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_revision_conflict"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy),
		errors.Is(err, store.ErrSystemUpdateHostSelfUpdateBusy),
		errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_lifecycle_busy"})
	case errors.Is(err, store.ErrSystemUpdateAgentNotReady):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_not_ready"})
	case errors.Is(err, store.ErrSystemUpdateAgentInactive):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "update_agent_inactive"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostStale),
		errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "deactivate_pull_ownership_failed"})
	}
}

var (
	errInvalidUpdaterHostPublicKey   = errors.New("invalid updater host public key")
	errInvalidUpdaterDatabaseName    = errors.New("invalid updater database name")
	errInvalidUpdaterLocalListenPort = errors.New("invalid updater local listen port")
	updaterDatabaseNamePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

func normalizedUpdaterPolicyRequest(agent store.RegisteredService, body updaterPolicyUpdateRequest) (store.UpdaterPolicy, error) {
	transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
	if transportMode != store.SystemUpdateTransportPullV2 ||
		strings.TrimSpace(agent.ExecutionHostID) == "" ||
		(agent.OwnershipEpoch < 1 &&
			!systemUpdateAgentObserveOnly(agent) &&
			!systemUpdateAgentPendingInitialPullPolicy(agent)) {
		return store.UpdaterPolicy{}, store.ErrInvalidSettings
	}
	targets, err := normalizedUpdaterPolicyTargets(transportMode, body.Targets)
	if err != nil {
		return store.UpdaterPolicy{}, err
	}
	policy := store.UpdaterPolicy{
		UpdaterID:                 agent.ServiceID,
		TransportMode:             transportMode,
		ExecutionHostID:           strings.TrimSpace(agent.ExecutionHostID),
		LocalExecutorPolicySHA256: strings.TrimSpace(body.LocalExecutorPolicySHA256),
		PollIntervalSeconds:       body.PollIntervalSeconds, HeartbeatIntervalSeconds: body.HeartbeatIntervalSeconds,
		Hosts: append([]store.UpdaterPolicyHost(nil), body.Hosts...), Targets: targets,
	}
	for index := range policy.Hosts {
		key, err := parseUpdaterED25519PublicKey(policy.Hosts[index].HostPublicKey)
		if err != nil {
			return store.UpdaterPolicy{}, errInvalidUpdaterHostPublicKey
		}
		policy.Hosts[index].HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	return store.NormalizeUpdaterPolicy(agent.ServiceID, policy)
}

func normalizedUpdaterPolicyTargets(
	transportMode string,
	requests []updaterPolicyTargetRequest,
) ([]store.UpdaterPolicyTarget, error) {
	targets := make([]store.UpdaterPolicyTarget, 0, len(requests))
	for _, request := range requests {
		target := store.UpdaterPolicyTarget{
			TargetID:       request.TargetID,
			ServiceID:      request.ServiceID,
			HostID:         request.HostID,
			ServiceType:    request.ServiceType,
			DeploymentMode: request.DeploymentMode,
		}
		requiresLocalListenPort := transportMode == store.SystemUpdateTransportPullV2 &&
			strings.TrimSpace(request.DeploymentMode) == "systemd" &&
			strings.TrimSpace(request.ServiceType) != "control_panel"
		if !requiresLocalListenPort {
			if request.LocalListenPort != nil {
				return nil, errInvalidUpdaterLocalListenPort
			}
		} else {
			if request.LocalListenPort == nil {
				return nil, errInvalidUpdaterLocalListenPort
			}
			if err := json.Unmarshal(request.LocalListenPort, &target.LocalListenPort); err != nil ||
				target.LocalListenPort < 1024 ||
				target.LocalListenPort > 65535 {
				return nil, errInvalidUpdaterLocalListenPort
			}
		}
		requiresDatabase := transportMode == store.SystemUpdateTransportPullV2 &&
			strings.TrimSpace(request.DeploymentMode) == "systemd" &&
			(strings.TrimSpace(request.ServiceType) == "control_panel" ||
				strings.TrimSpace(request.ServiceType) == "observability")
		if !requiresDatabase {
			if request.DatabaseName != nil {
				return nil, errInvalidUpdaterDatabaseName
			}
			targets = append(targets, target)
			continue
		}
		if request.DatabaseName == nil {
			return nil, errInvalidUpdaterDatabaseName
		}
		var databaseName string
		if err := json.Unmarshal(request.DatabaseName, &databaseName); err != nil {
			return nil, errInvalidUpdaterDatabaseName
		}
		target.DatabaseName = strings.TrimSpace(databaseName)
		if !updaterDatabaseNamePattern.MatchString(target.DatabaseName) {
			return nil, errInvalidUpdaterDatabaseName
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func systemUpdateAgentObserveOnly(agent store.RegisteredService) bool {
	return capabilityBool(agent.Capabilities["observe_only"]) ||
		capabilityBool(agent.ReportedCapabilities["observe_only"])
}

func systemUpdateAgentPendingInitialPullPolicy(agent store.RegisteredService) bool {
	return systemUpdateAgentTransportMode(agent) == store.SystemUpdateTransportPullV2 &&
		strings.TrimSpace(agent.ExecutionHostID) != "" &&
		agent.OwnershipEpoch == 0 &&
		strings.TrimSpace(agent.Status) == "pending" &&
		agent.LastHeartbeatAt == nil &&
		agent.LastReportedAt == nil &&
		len(agent.ReportedCapabilities) == 0
}

func parseUpdaterED25519PublicKey(raw string) (ssh.PublicKey, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
	if err != nil || len(options) != 0 || len(rest) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return nil, errInvalidUpdaterHostPublicKey
	}
	return key, nil
}

func makeUpdaterPolicyResponse(policy store.UpdaterPolicy, agent *store.RegisteredService) updaterPolicyResponse {
	transportMode := strings.ToLower(strings.TrimSpace(policy.TransportMode))
	executionHostID := strings.TrimSpace(policy.ExecutionHostID)
	if agent != nil {
		transportMode = systemUpdateAgentTransportMode(*agent)
		executionHostID = strings.TrimSpace(agent.ExecutionHostID)
	}
	clientKeys := map[string]string{}
	if agent != nil {
		clientKeys = capabilityStringMap(agent.ReportedCapabilities["ssh_client_public_keys"])
	}
	hosts := make([]updaterPolicyHostResponse, 0, len(policy.Hosts))
	for _, host := range policy.Hosts {
		responseHost := updaterPolicyHostResponse{
			HostID: host.HostID, Name: host.Name, Address: host.Address, Port: host.Port,
			User: host.User, Arch: host.Arch, HostPublicKey: host.HostPublicKey,
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
	targets := make([]updaterPolicyTargetResponse, 0, len(policy.Targets))
	for _, target := range policy.Targets {
		targets = append(targets, updaterPolicyTargetResponse{
			TargetID: target.TargetID, ServiceID: target.ServiceID, HostID: executionHostID,
			ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode,
			DatabaseName: target.DatabaseName, LocalListenPort: target.LocalListenPort,
		})
	}
	response := updaterPolicyResponse{
		UpdaterID:                   policy.UpdaterID,
		Revision:                    policy.Revision,
		ProjectionRevision:          policy.ProjectionRevision,
		LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		TransportMode:               transportMode,
		ExecutionHostID:             executionHostID,
		LocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		PollIntervalSeconds:         policy.PollIntervalSeconds, HeartbeatIntervalSeconds: policy.HeartbeatIntervalSeconds,
		Hosts: hosts, Targets: targets, UpdatedAt: policy.UpdatedAt,
	}
	return response
}

func (s *Server) enrichPullUpdaterPolicyResponse(
	ctx context.Context,
	response *updaterPolicyResponse,
	agent store.RegisteredService,
	policy store.UpdaterPolicy,
) error {
	if response == nil || agent.TransportMode != store.SystemUpdateTransportPullV2 {
		return nil
	}
	executionHosts, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		return store.ErrSystemUpdateExecutionStoreMismatch
	}
	ownership, err := executionHosts.GetSystemUpdateExecutionHost(ctx, agent.ExecutionHostID)
	if err != nil {
		return err
	}
	response.ExecutionHostOwnership = &ownership
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return err
	}
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	if err := addControlPanelSystemUpdateServiceForPolicy(servicesByID, policy); err != nil {
		return err
	}
	targetsByID := make(map[string]store.UpdaterPolicyTarget, len(policy.Targets))
	for _, target := range policy.Targets {
		targetsByID[target.TargetID] = target
	}
	for index := range response.Targets {
		target, targetExists := targetsByID[response.Targets[index].TargetID]
		service, serviceExists := servicesByID[target.ServiceID]
		if !targetExists || !serviceExists || service.ServiceType != target.ServiceType {
			continue
		}
		if localListenPort, ok := store.PullUpdaterPolicyTargetLocalListenPort(target, service); ok {
			response.Targets[index].LocalListenPort = localListenPort
		}
	}
	reportedOwnershipEpoch, _ := capabilityInt64(agent.ReportedCapabilities["ownership_epoch"])
	reportedProjectionRevision, _ := capabilityInt64(agent.ReportedCapabilities["policy_revision"])
	ready := agent.OwnershipEpoch == 0 &&
		ownership.TransportMode == store.SystemUpdateTransportPullV2 &&
		(ownership.AgentServiceID == "" || ownership.AgentServiceID == agent.ServiceID) &&
		store.PullUpdaterObserverReadyForActivation(agent, policy, servicesByID, time.Now().UTC())
	blockedReason := ""
	switch {
	case agent.OwnershipEpoch > 0:
		blockedReason = "already_active"
	case ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		(ownership.AgentServiceID != "" && ownership.AgentServiceID != agent.ServiceID):
		blockedReason = "system_update_ownership_conflict"
	case !ready:
		blockedReason = "host_agent_not_ready"
	}
	response.PullActivation = &updaterPullActivationStatusResponse{
		Ready:                      ready,
		BlockedReason:              blockedReason,
		Status:                     strings.TrimSpace(agent.Status),
		LastHeartbeatAt:            agent.LastHeartbeatAt,
		ObserveOnly:                capabilityBool(agent.ReportedCapabilities["observe_only"]),
		UpdateExecutor:             capabilityBool(agent.ReportedCapabilities["update_executor"]),
		MutationEnabled:            capabilityBool(agent.ReportedCapabilities["mutation_enabled"]),
		RecoveryPending:            capabilityBool(agent.ReportedCapabilities["recovery_pending"]),
		ReportedOwnershipEpoch:     reportedOwnershipEpoch,
		ReportedProjectionRevision: reportedProjectionRevision,
	}
	return nil
}

func (s *Server) canonicalizePullUpdaterLocalListenerBindings(
	ctx context.Context,
	policy store.UpdaterPolicy,
) (store.UpdaterPolicy, error) {
	if policy.TransportMode != store.SystemUpdateTransportPullV2 {
		return policy, nil
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return store.UpdaterPolicy{}, err
	}
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	for index := range policy.Targets {
		target := &policy.Targets[index]
		if target.LocalListenPort == 0 {
			continue
		}
		service, exists := servicesByID[target.ServiceID]
		if !exists || service.ServiceType != target.ServiceType || service.AppliedEndpoint == nil {
			continue
		}
		if target.LocalListenPort == service.AppliedEndpoint.Port {
			// Matching advertised/local ports do not need a side-table override.
			target.LocalListenPort = 0
		}
	}
	return policy, nil
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

func normalizeUpdaterReleaseToken(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if len(normalized) > 4096 {
		return nil, store.ErrInvalidSettings
	}
	for _, char := range []byte(normalized) {
		if char < 0x21 || char > 0x7e {
			return nil, store.ErrInvalidSettings
		}
	}
	return &normalized, nil
}

func (s *Server) updaterReleaseTokenStatus(ctx context.Context) (store.SecretStatus, error) {
	statuses, err := s.secrets.ListSecretStatus(ctx)
	if err != nil {
		return store.SecretStatus{}, err
	}
	for _, status := range statuses {
		if status.Name == store.UpdaterGitHubReleaseTokenSecretName {
			return status, nil
		}
	}
	return store.SecretStatus{Name: store.UpdaterGitHubReleaseTokenSecretName}, nil
}

func (s *Server) enrichUpdaterReleaseTokenResponse(ctx context.Context, response *updaterPolicyResponse, includeFingerprint bool) error {
	status, err := s.updaterReleaseTokenStatus(ctx)
	if err != nil {
		return err
	}
	response.GitHubTokenConfigured = status.Configured
	if includeFingerprint {
		response.GitHubTokenFingerprint = status.Fingerprint
	}
	return nil
}

func canViewUpdaterReleaseTokenFingerprint(ctx context.Context) bool {
	permissions := currentFromContext(ctx).Permissions
	return security.HasPermission(permissions, "secrets.read_status") ||
		security.HasPermission(permissions, "secrets.update")
}

func writeUpdaterPolicySaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "updater_policy_revision_conflict"})
	case errors.Is(err, store.ErrInvalidSettings):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_updater_policy"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_execution_host_busy"})
	case errors.Is(err, store.ErrSystemUpdateExecutionHostStale),
		errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "system_update_ownership_conflict"})
	case errors.Is(err, store.ErrSecretKeyRequired):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "updater_release_token_encryption_key_required"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "save_updater_policy_failed"})
	}
}
