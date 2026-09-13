package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *MariaDBSystemUpdateStore) MarkSystemUpdateRuntimeTokenRotationLocalStaged(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params MarkSystemUpdateRuntimeTokenRotationLocalStagedParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil || strings.TrimSpace(params.RawStagedToken) == "" {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyDB, ok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || policyDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, id)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		hostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"mark_system_update_runtime_token_rotation_local_staged",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	rotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+` WHERE id = ? FOR UPDATE`,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"mark_system_update_runtime_token_rotation_local_staged",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if rotation.ExecutionHostID != hostID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if !lockPlan.matches(rotation) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationCredential(
		rotation, params.RawStagedToken,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.EmergencyRevokedTokenID == rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	replay := rotation.Status == SystemUpdateRuntimeTokenRotationLocalStaged
	if replay {
		if rotation.LocalStageReceiptID != runtimeTokenRotationLocalStageReceiptID(rotation) ||
			!rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
	} else if rotation.Status != SystemUpdateRuntimeTokenRotationStaged {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	} else if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.CredentialClaimedAt == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	}
	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions port_hold WHERE port_hold.job_id = system_update_jobs.id AND port_hold.recovery_required = 1))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, rotation.ExecutionHostID).Scan(&activeJobID)
	if err == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if err := mariaDBRuntimeTokenRotationRejectActiveSelfUpdate(
		ctx, tx, rotation.ExecutionHostID,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"mark_system_update_runtime_token_rotation_local_staged",
		mariaDBRuntimeTokenRotationLaneLocksHeld,
	)
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"mark_system_update_runtime_token_rotation_local_staged",
		mariaDBRuntimeTokenRotationPolicyLocksHeld,
	)
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "mark_system_update_runtime_token_rotation_local_staged", lockPlan,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if hasStagedServiceNodeConfiguration(service) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	if err := validateRuntimeTokenRotationOwnership(
		service,
		policy,
		ownership,
		stageParamsForRuntimeTokenRotation(rotation, now),
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if service.TokenID != rotation.PreviousTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	oldToken, ok := lockedTokens[rotation.PreviousTokenID]
	if !ok || oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	stagedToken, ok := lockedTokens[rotation.StagedTokenID]
	if !ok || stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash ||
		stagedToken.RevokedAt == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if replay {
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	receiptID := runtimeTokenRotationLocalStageReceiptID(rotation)
	result, err := tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1, local_stage_receipt_id = ?,
    local_stage_acknowledged_at = ?, local_staged_at = ?, updated_at = ?
WHERE id = ? AND execution_host_id = ? AND status = ? AND revision = ?
  AND credential_claimed_at IS NOT NULL`,
		SystemUpdateRuntimeTokenRotationLocalStaged, receiptID,
		now, now, now, rotation.ID, hostID,
		SystemUpdateRuntimeTokenRotationStaged, expectedRevision,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationLocalStaged
	rotation.Revision++
	rotation.LocalStageReceiptID = receiptID
	rotation.LocalStageAcknowledgedAt = cloneTimePtr(&now)
	rotation.LocalStagedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MariaDBSystemUpdateStore) ProveSystemUpdateRuntimeTokenRotationHeartbeat(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	params = normalizeProveSystemUpdateRuntimeTokenRotationHeartbeatParams(params)
	if err := validateProveSystemUpdateRuntimeTokenRotationHeartbeatParams(params); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyDB, ok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || policyDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, params.RotationID)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"prove_system_update_runtime_token_rotation_heartbeat",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	rotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+` WHERE id = ? FOR UPDATE`,
		params.RotationID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"prove_system_update_runtime_token_rotation_heartbeat",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if !lockPlan.matches(rotation) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationCredential(rotation, params.RawStagedToken); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions port_hold WHERE port_hold.job_id = system_update_jobs.id AND port_hold.recovery_required = 1))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, rotation.ExecutionHostID).Scan(&activeJobID)
	if err == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if err := mariaDBRuntimeTokenRotationRejectActiveSelfUpdate(
		ctx, tx, rotation.ExecutionHostID,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"prove_system_update_runtime_token_rotation_heartbeat",
		mariaDBRuntimeTokenRotationLaneLocksHeld,
	)
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"prove_system_update_runtime_token_rotation_heartbeat",
		mariaDBRuntimeTokenRotationPolicyLocksHeld,
	)
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "prove_system_update_runtime_token_rotation_heartbeat", lockPlan,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if hasStagedServiceNodeConfiguration(service) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	oldToken, ok := lockedTokens[rotation.PreviousTokenID]
	if !ok || service.TokenID != rotation.PreviousTokenID ||
		oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	stagedToken, ok := lockedTokens[rotation.StagedTokenID]
	if !ok || stagedToken.RevokedAt == nil ||
		stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err := validateRuntimeTokenRotationHeartbeatProof(
		rotation, service, policy, ownership, params,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationHeartbeatProved {
		if !rotationRevisionAllowsReplay(rotation, params.ExpectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationLocalStaged {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != params.ExpectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1, heartbeat_proved_at = ?, updated_at = ?
WHERE id = ? AND execution_host_id = ? AND status = ? AND revision = ?`,
		SystemUpdateRuntimeTokenRotationHeartbeatProved, params.Now, params.Now,
		rotation.ID, params.ExecutionHostID,
		SystemUpdateRuntimeTokenRotationLocalStaged, params.ExpectedRevision,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationHeartbeatProved
	rotation.Revision++
	rotation.HeartbeatProvedAt = cloneTimePtr(&params.Now)
	rotation.UpdatedAt = params.Now
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}
