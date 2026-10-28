package store

import (
	"context"
	"database/sql"
	"errors"
)

type serviceEndpointMetadataObserver func(string, *sql.Tx)
type serviceEndpointMetadataObserverContextKey struct{}

func observeServiceEndpointMetadata(ctx context.Context, phase string, tx *sql.Tx) {
	if observer, ok := ctx.Value(serviceEndpointMetadataObserverContextKey{}).(serviceEndpointMetadataObserver); ok {
		observer(phase, tx)
	}
}

func (s MariaDBAuthStore) UpdateServiceMetadataWithEndpointOwnership(ctx context.Context, policies UpdaterPolicyStore, ownerships SystemUpdateExecutionHostStore, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error) {
	update, err := prepareServiceEndpointMetadataUpdate(serviceID, update)
	if err != nil {
		return RegisteredService{}, err
	}
	if policy, pointer := policies.(*MariaDBUpdaterPolicyStore); pointer && policy == nil {
		return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
	}
	policyDB, policyOK := mariaDBFromUpdaterPolicyStore(policies)
	updates, updatesOK := ownerships.(*MariaDBSystemUpdateStore)
	if s.db == nil || !policyOK || policyDB != s.db || !updatesOK || updates == nil || updates.db != s.db {
		return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
	}
	// A discovery conflict may be an unrelated policy save. Re-discover once
	// before any write; never retry a mutation or loop until a snapshot wins.
	for attempt := 0; attempt < 2; attempt++ {
		rows, err := policies.ListUpdaterPolicies(ctx)
		if err != nil {
			return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
		}
		hostIDs, agentIDs, policyIDs := []string{}, []string{serviceID}, []string{}
		for _, policy := range rows {
			policyIDs = append(policyIDs, policy.UpdaterID)
			if _, relevant := pullSystemdPolicyTargetForService(policy, serviceID); relevant {
				if !executionHostIDPattern.MatchString(policy.ExecutionHostID) || !serviceIDPattern.MatchString(policy.UpdaterID) {
					return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
				}
				hostIDs = append(hostIDs, policy.ExecutionHostID)
				agentIDs = append(agentIDs, policy.UpdaterID)
			}
		}
		observeServiceEndpointMetadata(ctx, "discovered", nil)
		updated, retry, err := s.updateServiceEndpointMetadataOnce(ctx, serviceID, update, rows, sortedUniqueStrings(policyIDs), sortedUniqueStrings(hostIDs), sortedUniqueStrings(agentIDs))
		if retry {
			continue
		}
		return updated, err
	}
	return RegisteredService{}, ErrServiceEndpointOwnershipUnavailable
}

func (s MariaDBAuthStore) updateServiceEndpointMetadataOnce(ctx context.Context, serviceID string, update ServiceMetadataUpdate, discovered []UpdaterPolicy, policyIDs, hostIDs, serviceIDs []string) (RegisteredService, bool, error) {
	// Full-policy FOR UPDATE also fences new policy rows. Keep this transaction
	// on repeatable read even when unrelated application transactions use RC.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return RegisteredService{}, false, ErrServiceEndpointOwnershipUnavailable
	}
	defer tx.Rollback()
	hosts := make(map[string]SystemUpdateExecutionHost, len(hostIDs))
	for _, hostID := range hostIDs {
		host, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, hostID)
		if err != nil {
			return RegisteredService{}, false, ErrServiceEndpointOwnershipUnavailable
		}
		hosts[hostID] = host
	}
	lockedPolicies, err := lockMariaDBPullUpdaterOwnershipPolicies(ctx, tx, "node_endpoint_metadata", discovered, policyIDs)
	if errors.Is(err, ErrConflict) {
		return RegisteredService{}, true, nil
	}
	if err != nil {
		return RegisteredService{}, false, ErrServiceEndpointOwnershipUnavailable
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return RegisteredService{}, false, ErrServiceEndpointOwnershipUnavailable
	}
	service, exists := lockedServices[serviceID]
	if !exists {
		return RegisteredService{}, false, ErrNotFound
	}
	rows := make([]UpdaterPolicy, 0, len(policyIDs))
	for _, id := range policyIDs {
		rows = append(rows, lockedPolicies[id])
	}
	observeServiceEndpointMetadata(ctx, "service_locks_held", tx)
	managed, err := serviceEndpointPullManaged(service, rows, lockedServices, hosts)
	if err != nil {
		return RegisteredService{}, false, err
	}
	if managed {
		return RegisteredService{}, false, ErrServiceEndpointManagedByUpdater
	}
	updated, err := updateMariaDBServiceMetadataLocked(ctx, tx, serviceID, update, service)
	if err != nil {
		return RegisteredService{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, false, err
	}
	return updated, false, nil
}
