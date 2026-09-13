package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params DeactivatePullUpdaterOwnershipParams,
) (DeactivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeDeactivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	auth, ok := mariaDBAuthStoreForUpdaterPolicy(services)
	if !ok || auth.db == nil || auth.db != s.db {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	lockPlan, err := s.discoverMariaDBPullUpdaterOwnershipLockPlan(
		ctx,
		auth,
		params.ServiceID,
		params.ExecutionHostID,
	)
	if errors.Is(err, ErrNotFound) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if !lockPlan.OwnershipExists {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	discoveredOwnership := lockPlan.Ownership
	if discoveredOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if discoveredOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		discoveredOwnership.AgentServiceID != params.ServiceID ||
		discoveredOwnership.OwnershipEpoch <= 0 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	defer tx.Rollback()

	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"deactivate_pull_updater_ownership",
		mariaDBUpdaterPolicyBeforeHostLock,
	)
	currentOwnership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"deactivate_pull_updater_ownership",
		mariaDBUpdaterPolicyHostLockHeld,
	)
	if !lockPlan.matchesOwnership(currentOwnership, true) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		currentOwnership.AgentServiceID != params.ServiceID ||
		currentOwnership.OwnershipEpoch <= 0 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	var activeID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions port_hold WHERE port_hold.job_id = system_update_jobs.id AND port_hold.recovery_required = 1))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_host_self_updates
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateHostSelfUpdateBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	now := time.Now().UTC()
	err = tx.QueryRowContext(ctx, `SELECT grant_row.id
FROM system_update_mutation_grants AS grant_row
INNER JOIN system_update_jobs AS job_row ON job_row.id = grant_row.job_id
WHERE job_row.execution_host_id = ?
  AND grant_row.expires_at > ?
ORDER BY grant_row.created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID, now).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"deactivate_pull_updater_ownership",
		mariaDBUpdaterPolicyLaneLocksHeld,
	)

	lockedPolicies, err := lockMariaDBPullUpdaterOwnershipPolicies(
		ctx,
		tx,
		"deactivate_pull_updater_ownership",
		lockPlan.Policies,
		lockPlan.PolicyIDs,
	)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	policy, exists := lockedPolicies[params.ServiceID]
	if !exists {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 ||
		currentOwnership.PolicyRevision != policy.ProjectionRevision {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if !equalSortedStrings(
		lockPlan.PrimaryPolicyServiceIDs,
		mariaDBUpdaterPolicyPersistentServiceIDs(policy),
	) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}

	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutation(
		ctx,
		tx,
		"deactivate_pull_updater_ownership",
		lockPlan.References,
		lockPlan.TokenIDs,
		lockPlan.ServiceIDs...,
	)
	if errors.Is(err, ErrNotFound) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if !lockPlan.matchesLockedServices(lockedServices) ||
		!mariaDBServiceTokenReferenceTypesMatch(
			lockPlan.References,
			lockedServices,
			lockedTokens,
		) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}

	service, exists := lockedServices[params.ServiceID]
	if !exists {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != currentOwnership.OwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	token, exists := lockedTokens[service.TokenID]
	if !exists || token.RevokedAt != nil || token.ServiceType != "update_agent" {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	recoveryPending, recoveryStateKnown := service.ReportedCapabilities["recovery_pending"].(bool)
	if !recoveryStateKnown || recoveryPending {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}

	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID: params.ExecutionHostID,
		TransportMode:   SystemUpdateTransportPullV2,
		AgentServiceID:  params.ServiceID,
		OwnershipEpoch:  currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:  policy.ProjectionRevision,
		CreatedAt:       currentOwnership.CreatedAt,
		UpdatedAt:       now,
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET transport_mode = ?,
    agent_service_id = ?,
    ownership_epoch = ?,
    policy_revision = ?,
    updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND agent_service_id = ?
  AND ownership_epoch = ?`,
		nextOwnership.TransportMode,
		nextOwnership.AgentServiceID,
		nextOwnership.OwnershipEpoch,
		nextOwnership.PolicyRevision,
		nextOwnership.UpdatedAt,
		nextOwnership.ExecutionHostID,
		SystemUpdateTransportPullV2,
		params.ServiceID,
		params.ExpectedExecutionHostOwnershipEpoch,
	)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	result, err = tx.ExecContext(ctx, `UPDATE services
SET ownership_epoch = 0, updated_at = ?
WHERE service_id = ?
  AND service_type = 'update_agent'
  AND transport_mode = ?
  AND execution_host_id = ?
  AND ownership_epoch = ?`,
		now,
		params.ServiceID,
		SystemUpdateTransportPullV2,
		params.ExecutionHostID,
		params.ExpectedExecutionHostOwnershipEpoch,
	)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service.OwnershipEpoch = 0
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	return DeactivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    policy,
	}, nil
}
