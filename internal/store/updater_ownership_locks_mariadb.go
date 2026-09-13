package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"time"
)

type mariaDBPullUpdaterOwnershipLockPlan struct {
	Ownership               SystemUpdateExecutionHost
	OwnershipExists         bool
	Policies                []UpdaterPolicy
	PolicyIDs               []string
	PrimaryPolicyServiceIDs []string
	ServiceIDs              []string
	TokenIDs                []string
	References              []mariaDBServiceTokenReference
}

type mariaDBUpdaterPolicyLockPhase string

const (
	mariaDBUpdaterPolicyBeforeHostLock       mariaDBUpdaterPolicyLockPhase = "before_host_lock"
	mariaDBUpdaterPolicyHostLockHeld         mariaDBUpdaterPolicyLockPhase = "host_lock_held"
	mariaDBUpdaterPolicyLaneLocksHeld        mariaDBUpdaterPolicyLockPhase = "lane_locks_held"
	mariaDBUpdaterPolicyBeforePolicyLocks    mariaDBUpdaterPolicyLockPhase = "before_policy_locks"
	mariaDBUpdaterPolicyPolicyLocksHeld      mariaDBUpdaterPolicyLockPhase = "policy_locks_held"
	mariaDBUpdaterPolicyPolicySetRevalidated mariaDBUpdaterPolicyLockPhase = "policy_set_revalidated"
)

type mariaDBUpdaterPolicyLockObserver func(string, mariaDBUpdaterPolicyLockPhase)
type mariaDBUpdaterPolicyLockObserverContextKey struct{}

func observeMariaDBUpdaterPolicyLockPhase(
	ctx context.Context,
	operation string,
	phase mariaDBUpdaterPolicyLockPhase,
) {
	observer, _ := ctx.Value(mariaDBUpdaterPolicyLockObserverContextKey{}).(mariaDBUpdaterPolicyLockObserver)
	if observer != nil {
		observer(operation, phase)
	}
}

func mariaDBUpdaterPolicyPersistentServiceIDs(policy UpdaterPolicy) []string {
	serviceIDs := []string{policy.UpdaterID}
	for _, target := range policy.Targets {
		if !updaterPolicyControlPanelTarget(target) {
			serviceIDs = append(serviceIDs, target.ServiceID)
		}
	}
	return sortedUniqueStrings(serviceIDs)
}

func mariaDBServiceTokenIDs(services map[string]RegisteredService) []string {
	tokenIDs := make([]string, 0, len(services)*3)
	for _, service := range services {
		tokenIDs = append(
			tokenIDs,
			service.TokenID,
			service.StagedNodePreviousTokenID,
			service.StagedNodeTokenID,
		)
	}
	return sortedUniqueStrings(tokenIDs)
}

func discoverMariaDBUpdaterOwnershipServiceTokenSet(
	ctx context.Context,
	auth MariaDBAuthStore,
	seedServiceIDs []string,
) ([]string, []string, []mariaDBServiceTokenReference, error) {
	serviceIDs := sortedUniqueStrings(seedServiceIDs)
	services := make(map[string]RegisteredService, len(serviceIDs))
	for {
		for _, serviceID := range serviceIDs {
			if _, exists := services[serviceID]; exists {
				continue
			}
			service, err := auth.getService(ctx, serviceID)
			if err != nil {
				return nil, nil, nil, err
			}
			services[serviceID] = service
		}
		tokenIDs := mariaDBServiceTokenIDs(services)
		references, err := discoverMariaDBServiceTokenReferences(ctx, auth.db, tokenIDs)
		if err != nil {
			return nil, nil, nil, err
		}
		nextServiceIDs := append([]string(nil), serviceIDs...)
		for _, reference := range references {
			nextServiceIDs = append(nextServiceIDs, reference.ServiceID)
		}
		nextServiceIDs = sortedUniqueStrings(nextServiceIDs)
		if equalSortedStrings(serviceIDs, nextServiceIDs) {
			return serviceIDs, tokenIDs, references, nil
		}
		serviceIDs = nextServiceIDs
	}
}

func mariaDBUpdaterPolicyByServiceID(
	policies []UpdaterPolicy,
	serviceID string,
) (UpdaterPolicy, bool) {
	serviceID = strings.TrimSpace(serviceID)
	for _, policy := range policies {
		if policy.UpdaterID == serviceID {
			return policy, true
		}
	}
	return UpdaterPolicy{}, false
}

func expandMariaDBUpdaterPolicyServiceClosure(
	policies []UpdaterPolicy,
	seedServiceIDs []string,
) []string {
	serviceIDs := sortedUniqueStrings(seedServiceIDs)
	for {
		nextServiceIDs := append([]string(nil), serviceIDs...)
		for _, serviceID := range serviceIDs {
			policy, exists := mariaDBUpdaterPolicyByServiceID(policies, serviceID)
			if !exists {
				continue
			}
			nextServiceIDs = append(
				nextServiceIDs,
				mariaDBUpdaterPolicyPersistentServiceIDs(policy)...,
			)
		}
		nextServiceIDs = sortedUniqueStrings(nextServiceIDs)
		if equalSortedStrings(serviceIDs, nextServiceIDs) {
			return serviceIDs
		}
		serviceIDs = nextServiceIDs
	}
}

func discoverMariaDBUpdaterOwnershipReferenceClosure(
	ctx context.Context,
	auth MariaDBAuthStore,
	policies []UpdaterPolicy,
	seedServiceIDs []string,
) ([]string, []string, []mariaDBServiceTokenReference, error) {
	serviceIDs := expandMariaDBUpdaterPolicyServiceClosure(policies, seedServiceIDs)
	for {
		lockedServiceIDs, tokenIDs, references, err :=
			discoverMariaDBUpdaterOwnershipServiceTokenSet(ctx, auth, serviceIDs)
		if err != nil {
			return nil, nil, nil, err
		}
		nextServiceIDs := expandMariaDBUpdaterPolicyServiceClosure(policies, lockedServiceIDs)
		if equalSortedStrings(lockedServiceIDs, nextServiceIDs) {
			return lockedServiceIDs, tokenIDs, references, nil
		}
		serviceIDs = nextServiceIDs
	}
}

func lockMariaDBPullUpdaterOwnershipPolicies(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	discovered []UpdaterPolicy,
	discoveredPolicyIDs []string,
) (map[string]UpdaterPolicy, error) {
	observeMariaDBUpdaterPolicyLockPhase(ctx, operation, mariaDBUpdaterPolicyBeforePolicyLocks)
	rows, err := tx.QueryContext(ctx, `SELECT service_id,
       revision,
       projection_revision,
       local_executor_policy_revision,
       policy_json,
       updated_at
FROM update_agent_policies
ORDER BY service_id
FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	locked := make([]UpdaterPolicy, 0, len(discovered))
	for rows.Next() {
		var (
			serviceID                   string
			revision                    int64
			projectionRevision          int64
			localExecutorPolicyRevision int64
			body                        []byte
			updatedAt                   time.Time
		)
		if err := rows.Scan(
			&serviceID,
			&revision,
			&projectionRevision,
			&localExecutorPolicyRevision,
			&body,
			&updatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		policy, err := decodeUpdaterPolicyRevisions(
			serviceID,
			revision,
			projectionRevision,
			localExecutorPolicyRevision,
			body,
			updatedAt,
		)
		if err != nil {
			rows.Close()
			return nil, err
		}
		locked = append(locked, policy)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, operation, mariaDBUpdaterPolicyPolicyLocksHeld)

	lockedByServiceID := make(map[string]UpdaterPolicy, len(locked))
	lockedPolicyIDs := make([]string, 0, len(locked))
	for index := range locked {
		if err := attachUpdaterTargetDatabases(ctx, tx, &locked[index]); err != nil {
			return nil, err
		}
		if err := attachUpdaterTargetLocalListeners(ctx, tx, &locked[index]); err != nil {
			return nil, err
		}
		lockedByServiceID[locked[index].UpdaterID] = locked[index]
		lockedPolicyIDs = append(lockedPolicyIDs, locked[index].UpdaterID)
	}
	if !equalSortedStrings(discoveredPolicyIDs, sortedUniqueStrings(lockedPolicyIDs)) ||
		!reflect.DeepEqual(discovered, locked) {
		return nil, ErrConflict
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, operation, mariaDBUpdaterPolicyPolicySetRevalidated)
	return lockedByServiceID, nil
}

func (s MariaDBUpdaterPolicyStore) discoverMariaDBPullUpdaterOwnershipLockPlan(
	ctx context.Context,
	auth MariaDBAuthStore,
	serviceID, executionHostID string,
) (mariaDBPullUpdaterOwnershipLockPlan, error) {
	policies, err := s.ListUpdaterPolicies(ctx)
	if err != nil {
		return mariaDBPullUpdaterOwnershipLockPlan{}, err
	}
	policy, exists := mariaDBUpdaterPolicyByServiceID(policies, serviceID)
	if !exists {
		return mariaDBPullUpdaterOwnershipLockPlan{}, ErrNotFound
	}
	plan := mariaDBPullUpdaterOwnershipLockPlan{
		Policies:                policies,
		PrimaryPolicyServiceIDs: mariaDBUpdaterPolicyPersistentServiceIDs(policy),
	}
	for _, candidate := range policies {
		plan.PolicyIDs = append(plan.PolicyIDs, candidate.UpdaterID)
	}
	plan.PolicyIDs = sortedUniqueStrings(plan.PolicyIDs)
	plan.Ownership, err = scanSystemUpdateExecutionHost(s.db.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ?`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		plan.Ownership = syntheticSystemUpdateExecutionHost(executionHostID)
		err = nil
	} else if err == nil {
		plan.OwnershipExists = true
	}
	if err != nil {
		return mariaDBPullUpdaterOwnershipLockPlan{}, err
	}
	seedServiceIDs := append([]string(nil), plan.PrimaryPolicyServiceIDs...)
	seedServiceIDs = append(
		seedServiceIDs,
		plan.Ownership.AgentServiceID,
	)
	for _, candidate := range policies {
		if candidate.ExecutionHostID == executionHostID ||
			candidate.UpdaterID == plan.Ownership.AgentServiceID {
			seedServiceIDs = append(
				seedServiceIDs,
				mariaDBUpdaterPolicyPersistentServiceIDs(candidate)...,
			)
		}
	}
	var activeRuntimeRotationServiceID string
	runtimeRotationErr := s.db.QueryRowContext(ctx, `SELECT service_id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?`, executionHostID).Scan(&activeRuntimeRotationServiceID)
	if runtimeRotationErr == nil {
		seedServiceIDs = append(seedServiceIDs, activeRuntimeRotationServiceID)
		if runtimePolicy, found := mariaDBUpdaterPolicyByServiceID(
			policies,
			activeRuntimeRotationServiceID,
		); found {
			seedServiceIDs = append(
				seedServiceIDs,
				mariaDBUpdaterPolicyPersistentServiceIDs(runtimePolicy)...,
			)
		}
	} else if !errors.Is(runtimeRotationErr, sql.ErrNoRows) {
		return mariaDBPullUpdaterOwnershipLockPlan{}, runtimeRotationErr
	}
	plan.ServiceIDs, plan.TokenIDs, plan.References, err =
		discoverMariaDBUpdaterOwnershipReferenceClosure(ctx, auth, policies, seedServiceIDs)
	if err != nil {
		return mariaDBPullUpdaterOwnershipLockPlan{}, err
	}
	return plan, nil
}

func (plan mariaDBPullUpdaterOwnershipLockPlan) matchesOwnership(
	ownership SystemUpdateExecutionHost,
	exists bool,
) bool {
	return plan.OwnershipExists == exists &&
		plan.Ownership.ExecutionHostID == ownership.ExecutionHostID &&
		normalizedSystemUpdateTransportMode(plan.Ownership.TransportMode) ==
			normalizedSystemUpdateTransportMode(ownership.TransportMode) &&
		plan.Ownership.AgentServiceID == ownership.AgentServiceID &&
		plan.Ownership.OwnershipEpoch == ownership.OwnershipEpoch &&
		plan.Ownership.PolicyRevision == ownership.PolicyRevision
}

func (plan mariaDBPullUpdaterOwnershipLockPlan) matchesLockedServices(
	services map[string]RegisteredService,
) bool {
	if len(services) != len(plan.ServiceIDs) {
		return false
	}
	expectedByServiceID := make(map[string]mariaDBServiceTokenReference, len(plan.References))
	for _, reference := range plan.References {
		expectedByServiceID[reference.ServiceID] = reference
	}
	for _, serviceID := range plan.ServiceIDs {
		service, exists := services[serviceID]
		expected, expectedExists := expectedByServiceID[serviceID]
		if !exists || !expectedExists ||
			service.TokenID != expected.TokenID ||
			service.StagedNodePreviousTokenID != expected.StagedPreviousTokenID ||
			service.StagedNodeTokenID != expected.StagedTokenID {
			return false
		}
	}
	return true
}
