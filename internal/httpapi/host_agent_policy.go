package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
)

type hostAgentPolicyResponse struct {
	ServiceID                   string                                  `json:"service_id"`
	TransportMode               string                                  `json:"transport_mode"`
	ExecutionHostID             string                                  `json:"execution_host_id"`
	OwnershipEpoch              int64                                   `json:"ownership_epoch"`
	Revision                    int64                                   `json:"revision"`
	SourcePolicyRevision        int64                                   `json:"source_policy_revision"`
	LocalExecutorPolicyRevision int64                                   `json:"local_executor_policy_revision"`
	ObserveOnly                 bool                                    `json:"observe_only"`
	LocalExecutorPolicySHA256   string                                  `json:"local_executor_policy_sha256,omitempty"`
	RuntimeTokenRotation        *hostAgentRuntimeTokenRotationDirective `json:"runtime_token_rotation,omitempty"`
	SelfUpdateID                string                                  `json:"self_update_id,omitempty"`
	SelfUpdateRevision          int64                                   `json:"self_update_revision,omitempty"`
	SelfUpdateStatus            string                                  `json:"self_update_status,omitempty"`
	RuntimeRequirement          *hostAgentRuntimeRequirement            `json:"runtime_requirement,omitempty"`
	SelfUpdate                  *hostAgentSelfUpdateDirective           `json:"self_update,omitempty"`
	Targets                     []hostAgentPolicyTarget                 `json:"targets"`
}

type hostAgentRuntimeRequirement struct {
	MinimumAgentVersion     string `json:"minimum_agent_version"`
	MinimumExecutorVersion  string `json:"minimum_executor_version"`
	AgentProtocolVersion    int    `json:"agent_protocol_version"`
	ExecutorProtocolVersion int    `json:"executor_protocol_version"`
	MutationProtocolVersion int    `json:"mutation_protocol_version"`
	RecoveryProtocolVersion int    `json:"recovery_protocol_version"`
}

type hostAgentSelfUpdateDirective struct {
	updateagent.HostSelfUpdateRequest
	StagedAt time.Time `json:"staged_at"`
}

type hostAgentRuntimeTokenRotationDirective struct {
	ID                                  string     `json:"id"`
	ServiceID                           string     `json:"service_id"`
	ExecutionHostID                     string     `json:"execution_host_id"`
	Status                              string     `json:"status"`
	Revision                            int64      `json:"revision"`
	ExpectedOwnershipEpoch              int64      `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64      `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64      `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64      `json:"expected_local_executor_policy_revision"`
	PreviousTokenID                     string     `json:"previous_token_id"`
	StagedTokenID                       string     `json:"staged_token_id"`
	LocalStageReceiptID                 string     `json:"local_stage_receipt_id,omitempty"`
	CredentialClaimedAt                 *time.Time `json:"credential_claimed_at,omitempty"`
	LocalStageAcknowledgedAt            *time.Time `json:"local_stage_acknowledged_at,omitempty"`
	CancelRequestedAt                   *time.Time `json:"cancel_requested_at,omitempty"`
}

type hostAgentPolicyTarget struct {
	ServiceID             string                 `json:"service_id"`
	ServiceType           string                 `json:"service_type"`
	DeploymentMode        string                 `json:"deployment_mode"`
	AppliedConfigRevision int64                  `json:"applied_config_revision"`
	AppliedConfigSHA256   string                 `json:"applied_config_sha256,omitempty"`
	DesiredEndpoint       *store.ServiceEndpoint `json:"desired_endpoint,omitempty"`
	AppliedEndpoint       *store.ServiceEndpoint `json:"applied_endpoint,omitempty"`
	LocalListenEndpoint   *store.ServiceEndpoint `json:"local_listen_endpoint,omitempty"`
	LocalHealthEndpoint   *store.ServiceEndpoint `json:"local_health_endpoint,omitempty"`
}

func (s *Server) serviceHostAgentPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	var body struct {
		ServiceID       string `json:"service_id"`
		CurrentRevision int64  `json:"current_revision"`
	}
	if !decodeHostAgentControlJSON(w, r, &body) {
		return
	}
	body.ServiceID = strings.TrimSpace(body.ServiceID)
	if body.ServiceID == "" || body.CurrentRevision < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_host_agent_policy_request"})
		return
	}
	agent, err := s.systemUpdateAgentForToken(r.Context(), token, body.ServiceID)
	if err != nil {
		writeSystemUpdateAgentError(w, err)
		return
	}
	if agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		strings.TrimSpace(agent.ExecutionHostID) == "" ||
		agent.OwnershipEpoch < 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_transport_mismatch"})
		return
	}
	policy, err := s.updaterPolicies.GetUpdaterPolicy(r.Context(), agent.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_host_agent_policy_failed"})
		return
	}
	if policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != agent.ExecutionHostID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_policy_binding_mismatch"})
		return
	}
	if body.CurrentRevision > policy.ProjectionRevision {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "host_agent_policy_revision_ahead"})
		return
	}
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_host_agent_targets_failed"})
		return
	}
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	if err := addControlPanelSystemUpdateServiceForPolicy(servicesByID, policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "control_panel_update_target_unavailable",
		})
		return
	}
	targets := make([]hostAgentPolicyTarget, 0, len(policy.Targets))
	for _, target := range policy.Targets {
		if target.HostID != agent.ExecutionHostID {
			continue
		}
		item := hostAgentPolicyTarget{
			ServiceID:             target.ServiceID,
			ServiceType:           target.ServiceType,
			DeploymentMode:        target.DeploymentMode,
			AppliedConfigRevision: 1,
		}
		if service, exists := servicesByID[target.ServiceID]; exists && service.ServiceType == target.ServiceType {
			item.DesiredEndpoint = copyHostAgentEndpoint(service.DesiredEndpoint)
			item.AppliedEndpoint = copyHostAgentEndpoint(service.AppliedEndpoint)
			if service.AppliedConfigRevision > 0 {
				item.AppliedConfigRevision = service.AppliedConfigRevision
			}
			item.AppliedConfigSHA256 = service.AppliedConfigSHA256
			if target.DeploymentMode == "systemd" {
				if localListenPort, ok := store.PullUpdaterPolicyTargetLocalListenPort(target, service); ok {
					item.LocalListenEndpoint = localSystemdHostAgentEndpoint(localListenPort)
				}
				item.LocalHealthEndpoint = copyHostAgentEndpoint(item.LocalListenEndpoint)
			}
		}
		targets = append(targets, item)
	}
	localExecutorPolicySHA256 := policy.LocalExecutorPolicySHA256
	if policy.LocalExecutorPolicyRevision == 0 {
		localExecutorPolicySHA256 = ""
	}
	runtimeTokenRotation, err := s.hostAgentRuntimeTokenRotationDirective(
		r.Context(), agent, policy,
	)
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_token_rotation_binding_conflict"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_runtime_token_rotation_failed"})
		return
	}
	selfUpdateID, selfUpdateRevision, selfUpdateStatus,
		runtimeRequirement, selfUpdate, err :=
		s.hostAgentSelfUpdateDirective(r.Context(), agent, policy)
	if errors.Is(err, store.ErrSystemUpdateOwnershipConflict) ||
		errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_self_update_binding_conflict",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "get_host_self_update_failed",
		})
		return
	}
	if runtimeTokenRotation != nil && selfUpdate != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "host_lifecycle_conflict",
		})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, hostAgentPolicyResponse{
		ServiceID:                   agent.ServiceID,
		TransportMode:               agent.TransportMode,
		ExecutionHostID:             agent.ExecutionHostID,
		OwnershipEpoch:              agent.OwnershipEpoch,
		Revision:                    policy.ProjectionRevision,
		SourcePolicyRevision:        policy.Revision,
		LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ObserveOnly:                 agent.OwnershipEpoch == 0,
		LocalExecutorPolicySHA256:   localExecutorPolicySHA256,
		RuntimeTokenRotation:        runtimeTokenRotation,
		SelfUpdateID:                selfUpdateID,
		SelfUpdateRevision:          selfUpdateRevision,
		SelfUpdateStatus:            selfUpdateStatus,
		RuntimeRequirement:          runtimeRequirement,
		SelfUpdate:                  selfUpdate,
		Targets:                     targets,
	})
}

func (s *Server) hostAgentSelfUpdateDirective(
	ctx context.Context,
	agent store.RegisteredService,
	policy store.UpdaterPolicy,
) (
	string,
	int64,
	string,
	*hostAgentRuntimeRequirement,
	*hostAgentSelfUpdateDirective,
	error,
) {
	updates, ok := s.systemUpdates.(store.SystemUpdateHostSelfUpdateStore)
	if !ok {
		return "", 0, "", nil, nil, nil
	}
	update, err := updates.GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
		ctx, agent.ExecutionHostID,
	)
	if errors.Is(err, store.ErrNotFound) {
		return "", 0, "", nil, nil, nil
	}
	if err != nil {
		return "", 0, "", nil, nil, err
	}
	if update.AgentServiceID != agent.ServiceID ||
		update.ExpectedOwnershipEpoch != agent.OwnershipEpoch ||
		update.ExpectedSourcePolicyRevision != policy.Revision ||
		update.ExpectedProjectionRevision != policy.ProjectionRevision ||
		update.ExpectedLocalExecutorPolicyRevision !=
			policy.LocalExecutorPolicyRevision ||
		update.ExpectedLocalExecutorPolicySHA256 !=
			policy.LocalExecutorPolicySHA256 {
		return "", 0, "", nil, nil,
			store.ErrSystemUpdateOwnershipConflict
	}
	now := time.Now().UTC()
	heartbeat := time.Time{}
	if agent.LastHeartbeatAt != nil {
		heartbeat = agent.LastHeartbeatAt.UTC()
	}
	caps := agent.ReportedCapabilities
	observed, _, err := updates.ObserveSystemUpdateHostSelfUpdate(
		ctx,
		store.SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Now:              now,
			HeartbeatAt:      heartbeat,
			AgentVersion:     strings.TrimSpace(agent.ReportedVersion),
			AgentProtocolVersion: hostAgentCapabilityProtocol(
				caps["agent_protocol_version"],
			),
			ExecutorVersion: strings.TrimSpace(
				hostAgentCapabilityString(caps["executor_version"]),
			),
			ExecutorProtocolVersion: hostAgentCapabilityProtocol(
				caps["executor_protocol_version"],
			),
			MutationProtocolVersion: hostAgentCapabilityProtocol(
				caps["mutation_protocol_version"],
			),
			RecoveryProtocolVersion: hostAgentCapabilityProtocol(
				caps["recovery_protocol_version"],
			),
			Phase: hostAgentCapabilityString(caps["self_update_phase"]),
			PendingGeneration: hostAgentCapabilityString(
				caps["self_update_pending_generation"],
			),
			FailedGeneration: hostAgentCapabilityString(
				caps["self_update_failed_generation"],
			),
			HeartbeatGeneration: hostAgentCapabilityString(
				caps["self_update_heartbeat_generation"],
			),
			ActiveAgentVersion: hostAgentCapabilityString(
				caps["self_update_active_agent_version"],
			),
			ActiveExecutorVersion: hostAgentCapabilityString(
				caps["self_update_active_executor_version"],
			),
			RecoveryPending: capabilityBool(caps["recovery_pending"]),
		},
	)
	if errors.Is(err, store.ErrNotFound) {
		return "", 0, "", nil, nil, nil
	}
	if err != nil {
		return "", 0, "", nil, nil, err
	}
	if observed.Status == store.SystemUpdateHostSelfUpdateCancelRequested {
		return observed.ID, observed.Revision, observed.Status, nil, nil, nil
	}
	requirement := &hostAgentRuntimeRequirement{
		MinimumAgentVersion:     observed.TargetVersion,
		MinimumExecutorVersion:  observed.TargetVersion,
		AgentProtocolVersion:    observed.Release.AgentProtocolVersion,
		ExecutorProtocolVersion: observed.Release.ExecutorProtocolVersion,
		MutationProtocolVersion: observed.Release.MutationProtocolVersion,
		RecoveryProtocolVersion: observed.Release.RecoveryProtocolVersion,
	}
	directive := &hostAgentSelfUpdateDirective{
		HostSelfUpdateRequest: updateagent.HostSelfUpdateRequest{
			Generation:              observed.AttemptGeneration,
			AgentVersion:            observed.TargetVersion,
			ExecutorVersion:         observed.TargetVersion,
			Commit:                  observed.Release.Commit,
			ArtifactSHA256:          "sha256:" + observed.Release.ArchiveSHA256,
			AgentProtocolVersion:    observed.Release.AgentProtocolVersion,
			ExecutorProtocolVersion: observed.Release.ExecutorProtocolVersion,
			MutationProtocolVersion: observed.Release.MutationProtocolVersion,
			RecoveryProtocolVersion: observed.Release.RecoveryProtocolVersion,
			Release: updateagent.HostSelfUpdateReleaseIdentity{
				Tag:                     observed.Release.Tag,
				Commit:                  observed.Release.Commit,
				PublishedAt:             observed.Release.PublishedAt,
				ManifestAssetID:         observed.Release.ManifestAssetID,
				ManifestAssetName:       observed.Release.ManifestAssetName,
				ManifestSHA256:          observed.Release.ManifestSHA256,
				ManifestChecksumAssetID: observed.Release.ManifestChecksumAssetID,
				ManifestChecksumSHA256:  observed.Release.ManifestChecksumSHA256,
				ArchiveAssetID:          observed.Release.ArchiveAssetID,
				ArchiveAssetName:        observed.Release.ArchiveAssetName,
				ArchiveSize:             observed.Release.ArchiveSize,
				ArchiveSHA256:           observed.Release.ArchiveSHA256,
				ArchiveChecksumAssetID:  observed.Release.ArchiveChecksumAssetID,
				ArchiveChecksumSHA256:   observed.Release.ArchiveChecksumSHA256,
				Arch:                    observed.Release.Arch,
				AgentProtocolVersion:    observed.Release.AgentProtocolVersion,
				ExecutorProtocolVersion: observed.Release.ExecutorProtocolVersion,
				MutationProtocolVersion: observed.Release.MutationProtocolVersion,
				RecoveryProtocolVersion: observed.Release.RecoveryProtocolVersion,
				MinimumPanelVersion:     observed.Release.MinimumPanelVersion,
			},
		},
		StagedAt: observed.IssuedAt,
	}
	return observed.ID, observed.Revision, observed.Status,
		requirement, directive, nil
}

func hostAgentCapabilityString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func hostAgentCapabilityProtocol(value any) int {
	if parsed, ok := capabilityInt64(value); ok && parsed >= 1 {
		return int(parsed)
	}
	if text, ok := value.(string); ok {
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &parsed); err == nil &&
			parsed >= 1 {
			return parsed
		}
	}
	return 0
}

func (s *Server) hostAgentRuntimeTokenRotationDirective(
	ctx context.Context,
	agent store.RegisteredService,
	policy store.UpdaterPolicy,
) (*hostAgentRuntimeTokenRotationDirective, error) {
	rotations, ok := s.systemUpdates.(store.SystemUpdateRuntimeTokenRotationStore)
	if !ok {
		return nil, nil
	}
	rotation, err := rotations.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		ctx, agent.ExecutionHostID,
	)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rotation.ServiceID != agent.ServiceID ||
		rotation.ExecutionHostID != agent.ExecutionHostID ||
		rotation.ExpectedOwnershipEpoch != agent.OwnershipEpoch ||
		rotation.ExpectedSourcePolicyRevision != policy.Revision ||
		rotation.ExpectedProjectionRevision != policy.ProjectionRevision ||
		rotation.ExpectedLocalExecutorPolicyRevision != policy.LocalExecutorPolicyRevision {
		return nil, store.ErrSystemUpdateOwnershipConflict
	}
	return &hostAgentRuntimeTokenRotationDirective{
		ID:                                  rotation.ID,
		ServiceID:                           rotation.ServiceID,
		ExecutionHostID:                     rotation.ExecutionHostID,
		Status:                              rotation.Status,
		Revision:                            rotation.Revision,
		ExpectedOwnershipEpoch:              rotation.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        rotation.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          rotation.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: rotation.ExpectedLocalExecutorPolicyRevision,
		PreviousTokenID:                     rotation.PreviousTokenID,
		StagedTokenID:                       rotation.StagedTokenID,
		LocalStageReceiptID:                 rotation.LocalStageReceiptID,
		CredentialClaimedAt:                 rotation.CredentialClaimedAt,
		LocalStageAcknowledgedAt:            rotation.LocalStageAcknowledgedAt,
		CancelRequestedAt:                   rotation.CancelRequestedAt,
	}, nil
}

func copyHostAgentEndpoint(endpoint *store.ServiceEndpoint) *store.ServiceEndpoint {
	if endpoint == nil {
		return nil
	}
	copied := *endpoint
	return &copied
}

func localSystemdHostAgentEndpoint(port int) *store.ServiceEndpoint {
	if port < 1024 || port > 65535 {
		return nil
	}
	return &store.ServiceEndpoint{
		Host:       "127.0.0.1",
		Port:       port,
		SSLEnabled: false,
		PublicURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
	}
}
