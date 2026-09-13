package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *MariaDBSystemUpdateStore) AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	params = normalizeAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(params)
	if err := validateAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(params); err != nil {
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
		"acknowledge_system_update_runtime_token_rotation_cancel",
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
		"acknowledge_system_update_runtime_token_rotation_cancel",
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
	if params.AuthenticatedPreviousTokenID != rotation.PreviousTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		if rotation.CancelAcknowledgedAt == nil ||
			!rotationRevisionAllowsReplay(rotation, params.ExpectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationCancelRequested {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != params.ExpectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStale
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
		"acknowledge_system_update_runtime_token_rotation_cancel",
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
		"acknowledge_system_update_runtime_token_rotation_cancel",
		mariaDBRuntimeTokenRotationPolicyLocksHeld,
	)
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "acknowledge_system_update_runtime_token_rotation_cancel", lockPlan,
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
		stageParamsForRuntimeTokenRotation(rotation, params.Now),
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
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens
SET revoked_at = COALESCE(revoked_at, ?)
WHERE id = ?`, params.Now, rotation.StagedTokenID); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1,
    cancel_acknowledged_at = ?, canceled_at = ?,
    staged_token_ciphertext = NULL, staged_token_nonce = NULL,
    credential_claim_id_sha256 = NULL, credential_claim_revision = NULL,
    updated_at = ?
WHERE id = ? AND execution_host_id = ? AND service_id = ?
  AND status = ? AND revision = ? AND cancel_requested_at IS NOT NULL`,
		SystemUpdateRuntimeTokenRotationCanceled, params.Now, params.Now,
		params.Now, rotation.ID, rotation.ExecutionHostID,
		rotation.ServiceID, SystemUpdateRuntimeTokenRotationCancelRequested,
		params.ExpectedRevision,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
	rotation.Revision++
	rotation.CancelAcknowledgedAt = cloneTimePtr(&params.Now)
	rotation.CanceledAt = cloneTimePtr(&params.Now)
	rotation.UpdatedAt = params.Now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}
