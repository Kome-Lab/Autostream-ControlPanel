package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
)

var (
	errHostAgentConfigurePolicyNotConfigured = errors.New("Host Agent configure policy is not configured")
	errHostAgentConfigurePolicyUnavailable   = errors.New("Host Agent configure policy is unavailable")
)

func (s *Server) hostAgentConfigurePolicyProjection(
	ctx context.Context,
	r *http.Request,
	agent store.RegisteredService,
	agentUID, agentGID uint32,
) (updateagent.ConfigurePolicyProjection, error) {
	policy, err := s.updaterPolicies.GetUpdaterPolicy(ctx, agent.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		return updateagent.ConfigurePolicyProjection{}, errHostAgentConfigurePolicyNotConfigured
	}
	if err != nil {
		return updateagent.ConfigurePolicyProjection{}, err
	}
	if agent.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.UpdaterID != agent.ServiceID ||
		policy.ExecutionHostID != agent.ExecutionHostID ||
		policy.Revision < 1 ||
		policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 {
		return updateagent.ConfigurePolicyProjection{}, errHostAgentConfigurePolicyUnavailable
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return updateagent.ConfigurePolicyProjection{}, err
	}
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	if err := addControlPanelSystemUpdateServiceForPolicy(servicesByID, policy); err != nil {
		return updateagent.ConfigurePolicyProjection{}, errHostAgentConfigurePolicyUnavailable
	}
	targets := make([]updateagent.HostAgentConfigurePolicyTarget, 0, len(policy.Targets))
	for _, target := range policy.Targets {
		service, exists := servicesByID[target.ServiceID]
		if !exists ||
			target.HostID != policy.ExecutionHostID ||
			service.ServiceType != target.ServiceType ||
			service.AppliedEndpoint == nil {
			return updateagent.ConfigurePolicyProjection{}, errHostAgentConfigurePolicyUnavailable
		}
		targets = append(targets, updateagent.HostAgentConfigurePolicyTarget{
			ServiceID:             target.ServiceID,
			ServiceType:           target.ServiceType,
			DeploymentMode:        target.DeploymentMode,
			DatabaseName:          target.DatabaseName,
			EndpointRevision:      service.EndpointRevision,
			AppliedConfigRevision: service.AppliedConfigRevision,
			AppliedConfigSHA256:   service.AppliedConfigSHA256,
			AppliedEndpointPort:   service.AppliedEndpoint.Port,
		})
	}
	projection, err := updateagent.BuildHostAgentConfigurePolicy(
		updateagent.HostAgentConfigurePolicySource{
			PanelURL:                    panelBaseURL(r),
			ExecutionHostID:             policy.ExecutionHostID,
			AgentUID:                    agentUID,
			AgentGID:                    agentGID,
			SourcePolicyRevision:        policy.Revision,
			ProjectionRevision:          policy.ProjectionRevision,
			LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			Targets:                     targets,
		},
	)
	if err != nil {
		return updateagent.ConfigurePolicyProjection{}, errHostAgentConfigurePolicyUnavailable
	}
	return projection, nil
}

func writeHostAgentConfigurePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errHostAgentConfigurePolicyNotConfigured):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "pull_policy_not_configured"})
	case errors.Is(err, errHostAgentConfigurePolicyUnavailable):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "local_executor_policy_unavailable"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "build_local_executor_policy_failed"})
	}
}

// hostAgentConfigureBoundConfigurationID makes the externally visible
// configuration ID an authenticated commitment to the exact stage projection.
// The service store keeps its normal staged token ID; activation recomputes
// this commitment from that server-owned ID and rejects a client that changes
// the peer identity or policy binding between stage and activation.
func hostAgentConfigureBoundConfigurationID(
	internalConfigurationID string,
	agentUID, agentGID uint32,
	projection updateagent.ConfigurePolicyProjection,
	key string,
) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = fmt.Fprintf(
		mac,
		"autostream-host-agent-configure-v1\n%s\n%d\n%d\n%s\n%d\n%d\n%d\n",
		internalConfigurationID,
		agentUID,
		agentGID,
		projection.SHA256,
		projection.SourcePolicyRevision,
		projection.ProjectionRevision,
		projection.PolicyRevision,
	)
	// Do not expose or inherit the service-store identifier's length. The
	// fixed-size value stays within the public configure identifier contract
	// even if the backing store changes its internal identifier format.
	return "hac1-" + hex.EncodeToString(mac.Sum(nil))
}

func hostAgentConfigureConfigurationIDMatches(
	externalConfigurationID, internalConfigurationID string,
	agentUID, agentGID uint32,
	projection updateagent.ConfigurePolicyProjection,
	key string,
) bool {
	expected := hostAgentConfigureBoundConfigurationID(
		internalConfigurationID,
		agentUID,
		agentGID,
		projection,
		key,
	)
	return hmac.Equal([]byte(externalConfigurationID), []byte(expected))
}
