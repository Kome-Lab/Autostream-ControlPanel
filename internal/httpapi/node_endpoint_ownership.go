package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

var errNodeEndpointOwnershipUnverifiable = errors.New("node endpoint ownership cannot be verified")

// activePullManagedSystemdTarget verifies the complete durable ownership
// binding before treating a target as pull-managed. A configured pull target
// with inconsistent or unavailable ownership state is returned as an error so
// callers cannot fall back to an unfenced direct endpoint mutation.
func (s *Server) activePullManagedSystemdTarget(
	ctx context.Context,
	service store.RegisteredService,
) (bool, error) {
	policies, err := s.updaterPolicies.ListUpdaterPolicies(ctx)
	if err != nil {
		return false, err
	}
	ownerships, ownershipsAvailable := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	for _, policy := range policies {
		target, relevant := pullSystemdPolicyTargetForService(policy, service.ServiceID)
		if !relevant {
			continue
		}
		if !ownershipsAvailable {
			return false, errNodeEndpointOwnershipUnverifiable
		}
		if target.ServiceType != service.ServiceType ||
			target.HostID != policy.ExecutionHostID ||
			policy.ExecutionHostID == "" ||
			policy.UpdaterID == "" {
			return false, errNodeEndpointOwnershipUnverifiable
		}
		agent, err := s.services.GetService(ctx, policy.UpdaterID)
		if err != nil {
			return false, err
		}
		ownership, err := ownerships.GetSystemUpdateExecutionHost(ctx, policy.ExecutionHostID)
		if err != nil {
			return false, err
		}
		if agent.ServiceType != "update_agent" ||
			agent.TransportMode != store.SystemUpdateTransportPullV2 ||
			agent.ExecutionHostID != policy.ExecutionHostID ||
			ownership.ExecutionHostID != policy.ExecutionHostID {
			return false, errNodeEndpointOwnershipUnverifiable
		}
		if ownership.TransportMode == store.SystemUpdateTransportPullV2 &&
			agent.OwnershipEpoch == 0 &&
			(ownership.AgentServiceID == "" || ownership.AgentServiceID == agent.ServiceID) {
			continue
		}
		if policy.ProjectionRevision < 1 ||
			agent.OwnershipEpoch < 1 ||
			ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
			ownership.AgentServiceID != agent.ServiceID ||
			ownership.OwnershipEpoch != agent.OwnershipEpoch ||
			ownership.PolicyRevision != policy.ProjectionRevision {
			return false, errNodeEndpointOwnershipUnverifiable
		}
		return true, nil
	}
	return false, nil
}

func pullSystemdPolicyTargetForService(
	policy store.UpdaterPolicy,
	serviceID string,
) (store.UpdaterPolicyTarget, bool) {
	if strings.ToLower(strings.TrimSpace(policy.TransportMode)) != store.SystemUpdateTransportPullV2 {
		return store.UpdaterPolicyTarget{}, false
	}
	serviceID = strings.TrimSpace(serviceID)
	for _, target := range policy.Targets {
		if strings.TrimSpace(target.ServiceID) == serviceID &&
			strings.ToLower(strings.TrimSpace(target.DeploymentMode)) == "systemd" {
			target.ServiceID = strings.TrimSpace(target.ServiceID)
			target.ServiceType = strings.TrimSpace(target.ServiceType)
			target.HostID = strings.TrimSpace(target.HostID)
			target.DeploymentMode = strings.ToLower(strings.TrimSpace(target.DeploymentMode))
			return target, true
		}
	}
	return store.UpdaterPolicyTarget{}, false
}

func sameNodeEndpoint(
	service store.RegisteredService,
	host string,
	port int,
	sslEnabled bool,
	publicURL string,
) bool {
	return service.Host == host &&
		service.Port == port &&
		service.SSLEnabled == sslEnabled &&
		service.PublicURL == publicURL
}
