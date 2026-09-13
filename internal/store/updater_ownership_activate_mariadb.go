package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s MariaDBUpdaterPolicyStore) ActivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params ActivatePullUpdaterOwnershipParams,
) (ActivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeActivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	auth, ok := mariaDBAuthStoreForUpdaterPolicy(services)
	if !ok || auth.db == nil || auth.db != s.db {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	lockPlan, err := s.discoverMariaDBPullUpdaterOwnershipLockPlan(
		ctx,
		auth,
		params.ServiceID,
		params.ExecutionHostID,
	)
	if errors.Is(err, ErrNotFound) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	defer tx.Rollback()

	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"activate_pull_updater_ownership",
		mariaDBUpdaterPolicyBeforeHostLock,
	)
	currentOwnership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	ownershipMissing := errors.Is(err, sql.ErrNoRows)
	if ownershipMissing {
		currentOwnership = syntheticSystemUpdateExecutionHost(params.ExecutionHostID)
		err = nil
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"activate_pull_updater_ownership",
		mariaDBUpdaterPolicyHostLockHeld,
	)
	if !lockPlan.matchesOwnership(currentOwnership, !ownershipMissing) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		(currentOwnership.AgentServiceID != "" &&
			currentOwnership.AgentServiceID != params.ServiceID) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}

	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions port_hold WHERE port_hold.job_id = system_update_jobs.id AND port_hold.recovery_required = 1))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeJobID)
	if err == nil {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	observeMariaDBUpdaterPolicyLockPhase(
		ctx,
		"activate_pull_updater_ownership",
		mariaDBUpdaterPolicyLaneLocksHeld,
	)

	lockedPolicies, err := lockMariaDBPullUpdaterOwnershipPolicies(
		ctx,
		tx,
		"activate_pull_updater_ownership",
		lockPlan.Policies,
		lockPlan.PolicyIDs,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	policy, exists := lockedPolicies[params.ServiceID]
	if !exists {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if !PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	if policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if !equalSortedStrings(
		lockPlan.PrimaryPolicyServiceIDs,
		mariaDBUpdaterPolicyPersistentServiceIDs(policy),
	) {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutation(
		ctx,
		tx,
		"activate_pull_updater_ownership",
		lockPlan.References,
		lockPlan.TokenIDs,
		lockPlan.ServiceIDs...,
	)
	if errors.Is(err, ErrNotFound) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if !lockPlan.matchesLockedServices(lockedServices) ||
		!mariaDBServiceTokenReferenceTypesMatch(
			lockPlan.References,
			lockedServices,
			lockedTokens,
		) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service, exists := lockedServices[params.ServiceID]
	if !exists {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != 0 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	lockedToken, tokenExists := lockedTokens[service.TokenID]
	if !tokenExists || lockedToken.RevokedAt != nil ||
		lockedToken.ServiceType != "update_agent" {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	targetServices := make(map[string]RegisteredService, len(policy.Targets))
	controlPanelTargetUsed := false
	for _, target := range policy.Targets {
		if updaterPolicyControlPanelTarget(target) {
			if params.ControlPanelTarget == nil {
				return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
			}
			targetServices[target.ServiceID] = params.ControlPanelTarget.registeredService()
			controlPanelTargetUsed = true
			continue
		}
		targetService, targetExists := lockedServices[target.ServiceID]
		if !targetExists {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
		}
		targetServices[target.ServiceID] = targetService
	}
	if params.ControlPanelTarget != nil && !controlPanelTargetUsed {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	if !registeredPullObserverReadyForActivation(service, policy, targetServices, time.Now().UTC()) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	baselineReservations, err := pullActivationBaselineReservations(
		policy,
		targetServices,
		service,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	missingBaselineReservations := make([]ServicePortReservation, 0, len(baselineReservations))
	for _, reservation := range baselineReservations {
		existing, reservationErr := scanServicePortReservation(tx.QueryRowContext(
			ctx,
			servicePortReservationSelect+`
WHERE execution_host_id = ?
  AND network_namespace = ?
  AND protocol = ?
  AND port = ?
FOR UPDATE`,
			reservation.ExecutionHostID,
			reservation.NetworkNamespace,
			reservation.Protocol,
			reservation.Port,
		))
		if errors.Is(reservationErr, sql.ErrNoRows) {
			missingBaselineReservations = append(missingBaselineReservations, reservation)
			continue
		}
		if reservationErr != nil {
			return ActivatePullUpdaterOwnershipResult{}, reservationErr
		}
		if !sameServicePortReservationOwner(existing, reservation) {
			return ActivatePullUpdaterOwnershipResult{}, ErrServicePortReserved
		}
	}

	now := time.Now().UTC()
	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID: params.ExecutionHostID,
		TransportMode:   SystemUpdateTransportPullV2,
		AgentServiceID:  params.ServiceID,
		OwnershipEpoch:  currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:  policy.ProjectionRevision,
		CreatedAt:       currentOwnership.CreatedAt,
		UpdatedAt:       now,
	}
	if ownershipMissing {
		nextOwnership.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO system_update_execution_hosts
(execution_host_id, transport_mode, agent_service_id, ownership_epoch, policy_revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			nextOwnership.ExecutionHostID,
			nextOwnership.TransportMode,
			nextOwnership.AgentServiceID,
			nextOwnership.OwnershipEpoch,
			nextOwnership.PolicyRevision,
			nextOwnership.CreatedAt,
			nextOwnership.UpdatedAt,
		)
		if isDuplicateKeyError(err) {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET transport_mode = ?, agent_service_id = ?, ownership_epoch = ?, policy_revision = ?, updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND ownership_epoch = ?`,
			nextOwnership.TransportMode,
			nextOwnership.AgentServiceID,
			nextOwnership.OwnershipEpoch,
			nextOwnership.PolicyRevision,
			nextOwnership.UpdatedAt,
			nextOwnership.ExecutionHostID,
			SystemUpdateTransportPullV2,
			params.ExpectedExecutionHostOwnershipEpoch,
		)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
			}
		}
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}

	result, err := tx.ExecContext(ctx, `UPDATE services
SET ownership_epoch = ?, updated_at = ?
WHERE service_id = ?
  AND service_type = 'update_agent'
  AND transport_mode = ?
  AND execution_host_id = ?
  AND ownership_epoch = 0`,
		nextOwnership.OwnershipEpoch,
		now,
		params.ServiceID,
		SystemUpdateTransportPullV2,
		params.ExecutionHostID,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service.OwnershipEpoch = nextOwnership.OwnershipEpoch
	service.UpdatedAt = now
	reportedConfigDigests := updaterPolicyCapabilityStringMap(
		service.ReportedCapabilities["reported_config_sha256"],
	)
	for serviceID, targetService := range targetServices {
		if targetService.AppliedConfigSHA256 != "" {
			continue
		}
		result, err = tx.ExecContext(ctx, `UPDATE services
SET applied_config_sha256 = ?, updated_at = ?
WHERE service_id = ?
  AND applied_config_revision = ?
  AND applied_config_sha256 IS NULL`,
			reportedConfigDigests[serviceID],
			now,
			serviceID,
			targetService.AppliedConfigRevision,
		)
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
		if affected != 1 {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
		}
	}
	for _, reservation := range missingBaselineReservations {
		_, err = tx.ExecContext(ctx, `INSERT INTO service_port_reservations
(execution_host_id, network_namespace, protocol, port, service_id, service_role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			reservation.ExecutionHostID,
			reservation.NetworkNamespace,
			reservation.Protocol,
			reservation.Port,
			reservation.ServiceID,
			reservation.ServiceRole,
			now,
			now,
		)
		if isDuplicateKeyError(err) {
			return ActivatePullUpdaterOwnershipResult{}, ErrServicePortReserved
		}
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	return ActivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    policy,
	}, nil
}
