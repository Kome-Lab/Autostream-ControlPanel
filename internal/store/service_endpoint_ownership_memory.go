package store

import (
	"context"
	"sort"
)

func (s *MemoryAuthStore) UpdateServiceMetadataWithEndpointOwnership(ctx context.Context, policies UpdaterPolicyStore, ownerships SystemUpdateExecutionHostStore, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error) {
	update, err := prepareServiceEndpointMetadataUpdate(serviceID, update)
	if err != nil {
		return RegisteredService{}, err
	}
	policyStore, policyOK := policies.(*MemoryUpdaterPolicyStore)
	updates, updatesOK := ownerships.(*MemorySystemUpdateStore)
	if s == nil || !policyOK || policyStore == nil || !updatesOK || updates == nil {
		return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
	}
	// Same order as policy save, activation, deactivation and port coordination.
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	updates.mu.Lock()
	defer updates.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	service, exists := s.services[serviceID]
	if !exists {
		return RegisteredService{}, ErrNotFound
	}
	rows := make([]UpdaterPolicy, 0, len(policyStore.policies))
	hosts := make(map[string]SystemUpdateExecutionHost)
	for _, policy := range policyStore.policies {
		rows = append(rows, policy)
		if _, relevant := pullSystemdPolicyTargetForService(policy, serviceID); relevant {
			if !executionHostIDPattern.MatchString(policy.ExecutionHostID) {
				return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
			}
			host, exists := updates.executionHosts[policy.ExecutionHostID]
			if !exists {
				host = syntheticSystemUpdateExecutionHost(policy.ExecutionHostID)
			}
			hosts[policy.ExecutionHostID] = host
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdaterID < rows[j].UpdaterID })
	// Do not call public registry/policy/ownership methods under these locks.
	managed, err := serviceEndpointPullManaged(service, rows, s.services, hosts)
	if err != nil {
		return RegisteredService{}, err
	}
	if managed {
		return RegisteredService{}, ErrServiceEndpointManagedByUpdater
	}
	return s.updateServiceMetadataLocked(serviceID, update)
}
