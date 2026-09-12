package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

func (s *Server) nodeConfiguration(w http.ResponseWriter, r *http.Request) {
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	response := map[string]any{"node": service}
	if !isPullV2HostAgent(service) {
		response["node_api_url"] = buildNodeAgentURL(service.Host, service.Port, service.SSLEnabled)
	}
	addNodeConfigurationMetadata(response, r, service, service.TokenID, "", "")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) regenerateNodeConfigureToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	if _, err := nodeRuntimeTokenEncryptionKey(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	if service.ServiceType == "update_agent" {
		s.systemUpdateOperationMu.Lock()
		defer s.systemUpdateOperationMu.Unlock()
		service, err = s.services.GetService(r.Context(), service.ServiceID)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
			return
		}
		if !s.requireNodeConfigureTokenScopePermissions(w, r, service) {
			return
		}
		if s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, service.ServiceID) {
			return
		}
	} else if !s.requireNodeConfigureTokenScopePermissions(w, r, service) {
		return
	}
	token, expiresAt, err := s.issueNodeConfigureToken(r.Context(), service.ServiceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_node_configure_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "nodes.configure_token.rotate", ResourceType: "node", ResourceID: service.ServiceID, Result: "success"})
	response := map[string]any{
		"node":                       service,
		"configure_token":            token,
		"configure_token_expires_at": expiresAt,
		"configure_command":          nodeConfigureCommand(r, service, token, ""),
	}
	if service.ServiceType == "update_agent" {
		response["configuration_path"] = nodeDefaultConfigPath(service)
	}
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func (s *Server) rotateNodeRuntimeToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if service.ServiceType == "update_agent" {
		s.systemUpdateOperationMu.Lock()
		defer s.systemUpdateOperationMu.Unlock()
		service, err = s.services.GetService(r.Context(), service.ServiceID)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
			return
		}
	}
	if !s.requireNodeTokenScopePermissions(w, r, service) {
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	if service.ServiceType == "update_agent" &&
		s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, service.ServiceID) {
		return
	}
	if isPullV2HostAgent(service) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "staged_runtime_token_rotation_required"})
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, updated, err := s.services.RotateServiceNodeToken(r.Context(), service.ServiceID, service.TokenID, seal)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_token_not_found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "staged_runtime_token_rotation_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rotate_node_runtime_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "nodes.runtime_token.rotate", ResourceType: "node", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"token_id": token.ID}})
	response := map[string]any{
		"node":             updated,
		"runtime_token_id": token.ID,
		"runtime_token":    token.RawToken,
	}
	addNodeConfigurationMetadata(response, r, updated, token.ID, token.RawToken, "")
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func (s *Server) runtimeIdentityConfiguration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID         string `json:"nodeId"`
		NodeIDSnake    string `json:"node_id"`
		ConfigureToken string `json:"configureToken"`
		Token          string `json:"configure_token"`
		Version        string `json:"version"`
		Commit         string `json:"commit"`
		BuildDate      string `json:"build_date"`
		Hostname       string `json:"hostname"`
		OS             string `json:"os"`
		Arch           string `json:"arch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	nodeID := strings.TrimSpace(body.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSpace(body.NodeIDSnake)
	}
	configureToken := strings.TrimSpace(body.ConfigureToken)
	if configureToken == "" {
		configureToken = strings.TrimSpace(body.Token)
	}
	service, err := s.services.GetService(r.Context(), nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if service.ServiceType == "update_agent" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "two_phase_configure_required"})
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, updated, err := s.services.ConfigureServiceNode(r.Context(), nodeID, configureToken, time.Now().UTC(), store.ServiceRuntimeReport{
		ServiceID: nodeID,
		Version:   body.Version,
		Commit:    body.Commit,
		BuildDate: body.BuildDate,
		Hostname:  body.Hostname,
		OS:        body.OS,
		Arch:      body.Arch,
	}, seal)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if errors.Is(err, store.ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_configure_token"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "configure_node_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUsername: "node-configure",
		Action:        "nodes.configure",
		ResourceType:  "node",
		ResourceID:    updated.ServiceID,
		Result:        "success",
		Metadata: map[string]any{
			"service_type": updated.ServiceType,
			"version":      updated.ReportedVersion,
			"commit":       updated.ReportedCommit,
			"build_date":   updated.ReportedBuildDate,
			"hostname":     updated.ReportedHostname,
			"os":           updated.ReportedOS,
			"arch":         updated.ReportedArch,
		},
	})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"config":             nodeAgentConfigResponse(r, updated, token.ID, token.RawToken),
		"config_yml":         nodeConfigurationYAML(r, updated, token.ID, token.RawToken),
		"configuration_yaml": nodeConfigurationYAML(r, updated, token.ID, token.RawToken),
	})
}

func (s *Server) hostAgentRuntimeIdentityStage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID          string `json:"nodeId"`
		ConfigureToken  string `json:"configureToken"`
		ProtocolVersion int    `json:"protocolVersion"`
		AgentUID        uint32 `json:"agentUid"`
		AgentGID        uint32 `json:"agentGid"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxControlRequestBytes+1))
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
	nodeID := strings.TrimSpace(body.NodeID)
	configureToken := strings.TrimSpace(body.ConfigureToken)
	if nodeID == "" || configureToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	service, err := s.services.GetService(r.Context(), nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if service.ServiceType != "update_agent" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "two_phase_configure_not_supported"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	service, err = s.services.GetService(r.Context(), nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if service.ServiceType != "update_agent" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "two_phase_configure_not_supported"})
		return
	}
	now := time.Now().UTC()
	validConfigureToken, err := s.services.ValidateServiceConfigureToken(r.Context(), service.ServiceID, configureToken, now)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "validate_configure_token_failed"})
		return
	}
	if validConfigureToken &&
		s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, service.ServiceID) {
		return
	}
	var configurePolicy *updateradapter.ConfigurePolicyProjection
	if validConfigureToken && isPullV2HostAgent(service) {
		if body.ProtocolVersion != updateradapter.HostAgentConfigureProtocolVersion ||
			body.AgentUID == 0 ||
			body.AgentGID == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_configure_protocol_required"})
			return
		}
		projection, projectionErr := s.hostAgentConfigurePolicyProjection(
			r.Context(),
			r,
			service,
			body.AgentUID,
			body.AgentGID,
		)
		if projectionErr != nil {
			writeHostAgentConfigurePolicyError(w, projectionErr)
			return
		}
		configurePolicy = &projection
	}
	configureBindingKey, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	staged, err := s.services.StageServiceNodeConfiguration(r.Context(), service.ServiceID, configureToken, now, seal)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	case errors.Is(err, store.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_configure_token"})
		return
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationSharedToken):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_shared_token"})
		return
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_conflict"})
		return
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "two_phase_configure_not_supported"})
		return
	case errors.Is(err, store.ErrInvalidServiceScope):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_service_scope"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stage_node_configuration_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUsername: "node-configure",
		Action:        "nodes.configure.stage",
		ResourceType:  "node",
		ResourceID:    staged.Service.ServiceID,
		Result:        "success",
		Metadata: map[string]any{
			"service_type":     staged.Service.ServiceType,
			"configuration_id": staged.Token.ID,
		},
	})
	externalConfigurationID := staged.Token.ID
	if configurePolicy != nil {
		externalConfigurationID = hostAgentConfigureBoundConfigurationID(
			staged.Token.ID,
			body.AgentUID,
			body.AgentGID,
			*configurePolicy,
			configureBindingKey,
		)
	}
	response := map[string]any{
		"configuration_id":      externalConfigurationID,
		"activation_token":      staged.ActivationToken,
		"activation_expires_at": staged.ActivationExpiresAt,
		"config":                updaterNodeConfigurationResponse(r, staged.Service, staged.Token.RawToken),
	}
	if configurePolicy != nil {
		response["configure_protocol_version"] = updateradapter.HostAgentConfigureProtocolVersion
		response["local_executor_policy"] = configurePolicy
	}
	writeOneTimeSecretJSON(w, http.StatusOK, response)
}

func (s *Server) hostAgentRuntimeIdentityActivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID                      string `json:"nodeId"`
		ConfigurationID             string `json:"configurationId"`
		ActivationToken             string `json:"activationToken"`
		Version                     string `json:"version"`
		Commit                      string `json:"commit"`
		BuildDate                   string `json:"build_date"`
		Hostname                    string `json:"hostname"`
		OS                          string `json:"os"`
		Arch                        string `json:"arch"`
		ConfigureProtocolVersion    int    `json:"configureProtocolVersion"`
		AgentUID                    uint32 `json:"agentUid"`
		AgentGID                    uint32 `json:"agentGid"`
		LocalExecutorPolicySHA256   string `json:"localExecutorPolicySha256"`
		SourcePolicyRevision        int64  `json:"sourcePolicyRevision"`
		ProjectionRevision          int64  `json:"projectionRevision"`
		LocalExecutorPolicyRevision int64  `json:"localExecutorPolicyRevision"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxControlRequestBytes+1))
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
	nodeID := strings.TrimSpace(body.NodeID)
	configurationID := strings.TrimSpace(body.ConfigurationID)
	activationToken := strings.TrimSpace(body.ActivationToken)
	if nodeID == "" || configurationID == "" || activationToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	stagedService, inspectErr := s.services.GetService(r.Context(), nodeID)
	if inspectErr != nil && !errors.Is(inspectErr, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	activationServiceID := nodeID
	if inspectErr == nil {
		activationServiceID = stagedService.ServiceID
	}
	if inspectErr == nil &&
		stagedService.ServiceType == "update_agent" &&
		stagedService.StagedNodeActivationTokenHash != "" &&
		security.VerifyTokenHash(activationToken, stagedService.StagedNodeActivationTokenHash) &&
		(isPullV2HostAgent(stagedService) ||
			stagedService.StagedNodeTokenID == configurationID) &&
		s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, stagedService.ServiceID) {
		return
	}
	var configurePolicy *updateradapter.ConfigurePolicyProjection
	activationConfigurationID := configurationID
	clearedHostAgentActivationReplay := inspectErr == nil &&
		isPullV2HostAgent(stagedService) &&
		stagedService.StagedNodeTokenID == ""
	if clearedHostAgentActivationReplay {
		activationConfigurationID = stagedService.TokenID
	}
	if inspectErr == nil &&
		isPullV2HostAgent(stagedService) &&
		stagedService.StagedNodeTokenID != "" &&
		stagedService.StagedNodeActivationTokenHash != "" &&
		security.VerifyTokenHash(activationToken, stagedService.StagedNodeActivationTokenHash) {
		if body.ConfigureProtocolVersion != updateradapter.HostAgentConfigureProtocolVersion ||
			body.AgentUID == 0 ||
			body.AgentGID == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_configure_protocol_required"})
			return
		}
		projection, projectionErr := s.hostAgentConfigurePolicyProjection(
			r.Context(),
			r,
			stagedService,
			body.AgentUID,
			body.AgentGID,
		)
		if projectionErr != nil {
			writeHostAgentConfigurePolicyError(w, projectionErr)
			return
		}
		configureBindingKey, keyErr := nodeRuntimeTokenEncryptionKey()
		if keyErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "host_agent_configure_binding_unavailable"})
			return
		}
		if !hostAgentConfigureConfigurationIDMatches(
			configurationID,
			stagedService.StagedNodeTokenID,
			body.AgentUID,
			body.AgentGID,
			projection,
			configureBindingKey,
		) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			return
		}
		if body.LocalExecutorPolicySHA256 != projection.SHA256 ||
			body.SourcePolicyRevision != projection.SourcePolicyRevision ||
			body.ProjectionRevision != projection.ProjectionRevision ||
			body.LocalExecutorPolicyRevision != projection.PolicyRevision {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			return
		}
		if _, bindErr := s.updaterPolicies.BindPullUpdaterConfigurePolicy(
			r.Context(),
			store.BindPullUpdaterConfigurePolicyParams{
				ServiceID:                           stagedService.ServiceID,
				ExpectedSourcePolicyRevision:        projection.SourcePolicyRevision,
				ExpectedProjectionRevision:          projection.ProjectionRevision,
				ExpectedLocalExecutorPolicyRevision: projection.PolicyRevision,
				LocalExecutorPolicySHA256:           projection.SHA256,
			},
		); bindErr != nil {
			if errors.Is(bindErr, store.ErrConflict) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "bind_local_executor_policy_failed"})
			}
			return
		}
		configurePolicy = &projection
		activationConfigurationID = stagedService.StagedNodeTokenID
	}
	_, updated, alreadyActivated, err := s.services.ActivateServiceNodeConfiguration(r.Context(), activationServiceID, activationConfigurationID, activationToken, time.Now().UTC(), store.ServiceRuntimeReport{
		ServiceID: activationServiceID,
		Version:   body.Version,
		Commit:    body.Commit,
		BuildDate: body.BuildDate,
		Hostname:  body.Hostname,
		OS:        body.OS,
		Arch:      body.Arch,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	case errors.Is(err, store.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_activation_token"})
		return
	case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationSharedToken):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_shared_token"})
		return
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_conflict"})
		return
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "two_phase_configure_not_supported"})
		return
	case errors.Is(err, store.ErrInvalidServiceScope):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_service_scope"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "activate_node_configuration_failed"})
		return
	}
	if alreadyActivated && clearedHostAgentActivationReplay && configurePolicy == nil {
		if s.rejectUpdaterIdentityMutationDuringActiveWork(w, r, updated.ServiceID) {
			return
		}
		if body.ConfigureProtocolVersion != updateradapter.HostAgentConfigureProtocolVersion ||
			body.AgentUID == 0 ||
			body.AgentGID == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_configure_protocol_required"})
			return
		}
		projection, projectionErr := s.hostAgentConfigurePolicyProjection(
			r.Context(),
			r,
			updated,
			body.AgentUID,
			body.AgentGID,
		)
		if projectionErr != nil {
			writeHostAgentConfigurePolicyError(w, projectionErr)
			return
		}
		configureBindingKey, keyErr := nodeRuntimeTokenEncryptionKey()
		if keyErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "host_agent_configure_binding_unavailable"})
			return
		}
		if !hostAgentConfigureConfigurationIDMatches(
			configurationID,
			updated.TokenID,
			body.AgentUID,
			body.AgentGID,
			projection,
			configureBindingKey,
		) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			return
		}
		if body.LocalExecutorPolicySHA256 != projection.SHA256 ||
			body.SourcePolicyRevision != projection.SourcePolicyRevision ||
			body.ProjectionRevision != projection.ProjectionRevision ||
			body.LocalExecutorPolicyRevision != projection.PolicyRevision {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			return
		}
		if _, bindErr := s.updaterPolicies.BindPullUpdaterConfigurePolicy(
			r.Context(),
			store.BindPullUpdaterConfigurePolicyParams{
				ServiceID:                           updated.ServiceID,
				ExpectedSourcePolicyRevision:        projection.SourcePolicyRevision,
				ExpectedProjectionRevision:          projection.ProjectionRevision,
				ExpectedLocalExecutorPolicyRevision: projection.PolicyRevision,
				LocalExecutorPolicySHA256:           projection.SHA256,
			},
		); bindErr != nil {
			if errors.Is(bindErr, store.ErrConflict) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_binding_mismatch"})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "bind_local_executor_policy_failed"})
			}
			return
		}
		configurePolicy = &projection
	}
	state := "activated"
	if alreadyActivated {
		state = "already_activated"
	} else {
		s.writeAudit(r, store.AuditEvent{
			ActorUsername: "node-configure",
			Action:        "nodes.configure",
			ResourceType:  "node",
			ResourceID:    updated.ServiceID,
			Result:        "success",
			Metadata: map[string]any{
				"service_type": updated.ServiceType,
				"version":      updated.ReportedVersion,
				"commit":       updated.ReportedCommit,
				"build_date":   updated.ReportedBuildDate,
				"hostname":     updated.ReportedHostname,
				"os":           updated.ReportedOS,
				"arch":         updated.ReportedArch,
			},
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	response := map[string]any{"state": state, "configuration_id": configurationID}
	if configurePolicy != nil {
		response["configure_protocol_version"] = updateradapter.HostAgentConfigureProtocolVersion
		response["local_executor_policy_sha256"] = configurePolicy.SHA256
		response["source_policy_revision"] = configurePolicy.SourcePolicyRevision
		response["projection_revision"] = configurePolicy.ProjectionRevision
		response["local_executor_policy_revision"] = configurePolicy.PolicyRevision
	}
	writeJSON(w, http.StatusOK, response)
}

func updaterNodeConfigurationResponse(r *http.Request, service store.RegisteredService, rawToken string) map[string]any {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	apiHost := service.Host
	apiPort := service.Port
	apiSSLEnabled := service.SSLEnabled
	if isPullV2HostAgent(service) {
		apiHost = ""
		apiPort = 0
		apiSSLEnabled = false
	}
	response := map[string]any{
		"panel_url":     panelURL,
		"node_id":       service.ServiceID,
		"runtime_token": rawToken,
		"service_name":  service.ServiceName,
		"service_type":  service.ServiceType,
		"api": map[string]any{
			"host":        apiHost,
			"port":        apiPort,
			"ssl_enabled": apiSSLEnabled,
		},
	}
	if isPullV2HostAgent(service) {
		response["transport_mode"] = store.SystemUpdateTransportPullV2
	}
	return response
}

func nodeAgentConfigResponse(r *http.Request, service store.RegisteredService, tokenID, rawToken string) map[string]any {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	response := map[string]any{
		"panel": map[string]any{"url": panelURL},
		"node":  map[string]any{"id": service.ServiceID, "name": service.ServiceName, "type": service.ServiceType},
		"api":   map[string]any{"host": service.Host, "port": service.Port, "ssl_enabled": service.SSLEnabled},
		"auth":  map[string]any{"token_id": tokenID, "token": rawToken},
		"agent": map[string]any{"data_dir": nodeAgentDataDir(service.ServiceType), "log_dir": nodeAgentLogDir(service.ServiceType)},
	}
	if signingKey := nodeStreamIngestSigningKey(service.ServiceType, rawToken != ""); signingKey != "" {
		response["stream_ingest"] = map[string]any{"signing_key": signingKey}
	}
	return response
}

func (s *Server) nodeAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.serviceHeartbeat(w, r)
}

func (s *Server) nodeAgentReport(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.heartbeat")
	if !ok {
		return
	}
	var body store.ServiceHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.Status == "" {
		body.Status = "online"
	}
	service, err := s.persistServiceHeartbeat(r.Context(), token, serviceBearerToken(r), body, panelBaseURL(r))
	if errors.Is(err, store.ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_service_token"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "node_report_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, service)
}
