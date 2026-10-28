package store

import (
	"context"
	"errors"
	"strings"
)

var ErrServiceEndpointManagedByUpdater = errors.New("service endpoint is managed by updater")
var ErrServiceEndpointOwnershipUnavailable = errors.New("service endpoint ownership is unavailable")

// ServiceMetadataEndpointOwnershipStore is the stamped endpoint-edit boundary.
// Implementations fence updater ownership and metadata CAS in one transaction.
type ServiceMetadataEndpointOwnershipStore interface {
	UpdateServiceMetadataWithEndpointOwnership(context.Context, UpdaterPolicyStore, SystemUpdateExecutionHostStore, string, ServiceMetadataUpdate) (RegisteredService, error)
}

func pullSystemdPolicyTargetForService(policy UpdaterPolicy, serviceID string) (UpdaterPolicyTarget, bool) {
	if strings.ToLower(strings.TrimSpace(policy.TransportMode)) != SystemUpdateTransportPullV2 {
		return UpdaterPolicyTarget{}, false
	}
	serviceID = strings.TrimSpace(serviceID)
	for _, target := range policy.Targets {
		if strings.TrimSpace(target.ServiceID) == serviceID && strings.ToLower(strings.TrimSpace(target.DeploymentMode)) == "systemd" {
			target.ServiceID = strings.TrimSpace(target.ServiceID)
			target.ServiceType = strings.TrimSpace(target.ServiceType)
			target.HostID = strings.TrimSpace(target.HostID)
			target.DeploymentMode = strings.ToLower(strings.TrimSpace(target.DeploymentMode))
			return target, true
		}
	}
	return UpdaterPolicyTarget{}, false
}

// serviceEndpointPullManaged is shared by the preliminary HTTP read and the
// final fenced store check. The caller owns all snapshot consistency/locks.
func serviceEndpointPullManaged(service RegisteredService, policies []UpdaterPolicy, agents map[string]RegisteredService, hosts map[string]SystemUpdateExecutionHost) (bool, error) {
	for _, policy := range policies {
		target, relevant := pullSystemdPolicyTargetForService(policy, service.ServiceID)
		if !relevant {
			continue
		}
		agent, agentExists := agents[policy.UpdaterID]
		ownership, hostExists := hosts[policy.ExecutionHostID]
		if target.ServiceType != service.ServiceType || target.HostID != policy.ExecutionHostID || policy.ExecutionHostID == "" || policy.UpdaterID == "" ||
			!agentExists || !hostExists || agent.ServiceType != "update_agent" || agent.TransportMode != SystemUpdateTransportPullV2 ||
			agent.ExecutionHostID != policy.ExecutionHostID || ownership.ExecutionHostID != policy.ExecutionHostID {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
		// Deactivation retains a positive host epoch while the agent becomes an
		// observer again. Preserve that established inactive state exactly.
		if ownership.TransportMode == SystemUpdateTransportPullV2 && agent.OwnershipEpoch == 0 && (ownership.AgentServiceID == "" || ownership.AgentServiceID == agent.ServiceID) {
			continue
		}
		if policy.ProjectionRevision < 1 || agent.OwnershipEpoch < 1 || ownership.TransportMode != SystemUpdateTransportPullV2 ||
			ownership.AgentServiceID != agent.ServiceID || ownership.OwnershipEpoch != agent.OwnershipEpoch || ownership.PolicyRevision != policy.ProjectionRevision {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
		return true, nil
	}
	return false, nil
}

// ServiceEndpointPullManaged performs the existing preliminary ownership read.
// Stamped effective endpoint writes additionally use the fenced store method.
func ServiceEndpointPullManaged(ctx context.Context, service RegisteredService, services ServiceRegistryStore, policies UpdaterPolicyStore, ownerships SystemUpdateExecutionHostStore) (bool, error) {
	if services == nil || policies == nil {
		return false, ErrServiceEndpointOwnershipUnavailable
	}
	switch policy := policies.(type) {
	case *MemoryUpdaterPolicyStore:
		if policy == nil {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
	case *MariaDBUpdaterPolicyStore:
		if policy == nil {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
	}
	rows, err := policies.ListUpdaterPolicies(ctx)
	if err != nil {
		return false, ErrServiceEndpointOwnershipUnavailable
	}
	agents := map[string]RegisteredService{}
	hosts := map[string]SystemUpdateExecutionHost{}
	for _, policy := range rows {
		if _, relevant := pullSystemdPolicyTargetForService(policy, service.ServiceID); !relevant {
			continue
		}
		if ownerships == nil {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
		switch hostStore := ownerships.(type) {
		case *MemorySystemUpdateStore:
			if hostStore == nil {
				return false, ErrServiceEndpointOwnershipUnavailable
			}
		case *MariaDBSystemUpdateStore:
			if hostStore == nil {
				return false, ErrServiceEndpointOwnershipUnavailable
			}
		}
		agent, err := services.GetService(ctx, policy.UpdaterID)
		if err != nil {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
		host, err := ownerships.GetSystemUpdateExecutionHost(ctx, policy.ExecutionHostID)
		if err != nil {
			return false, ErrServiceEndpointOwnershipUnavailable
		}
		agents[policy.UpdaterID] = agent
		hosts[policy.ExecutionHostID] = host
		managed, err := serviceEndpointPullManaged(service, []UpdaterPolicy{policy}, agents, hosts)
		if err != nil || managed {
			return managed, err
		}
	}
	return false, nil
}

func prepareServiceEndpointMetadataUpdate(serviceID string, update ServiceMetadataUpdate) (ServiceMetadataUpdate, error) {
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(serviceID) == "" {
		return update, ErrNotFound
	}
	if err := validateServiceMetadataUpdate(update); err != nil {
		return update, err
	}
	if update.ExpectedMetadataRevision == nil || !ValidServiceMetadataRevision(*update.ExpectedMetadataRevision) || update.Endpointless || update.PreserveEndpoint {
		return update, ErrInvalidServiceRegistration
	}
	return update, nil
}
