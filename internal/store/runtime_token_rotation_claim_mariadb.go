package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *MariaDBSystemUpdateStore) ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams,
	unseal NodeTokenUnsealer,
) (ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult, error) {
	params = normalizeClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(params)
	if err := validateClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(params); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if unseal == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, errNodeTokenSealerRequired
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyDB, ok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || policyDB != s.db {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, params.RotationID)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	defer tx.Rollback()

	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"claim_system_update_runtime_token_rotation_credential",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	rotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+` WHERE id = ? FOR UPDATE`,
		params.RotationID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"claim_system_update_runtime_token_rotation_credential",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	if !lockPlan.matches(rotation) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	claimIDHash := runtimeTokenRotationClaimIDHash(params.ClaimID)
	firstClaim, _, err := runtimeTokenRotationCredentialClaimMode(
		rotation, params.ExpectedRevision, claimIDHash,
	)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if params.AuthenticatedPreviousTokenID != rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationToken
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
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if err := mariaDBRuntimeTokenRotationRejectActiveSelfUpdate(
		ctx, tx, rotation.ExecutionHostID,
	); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"claim_system_update_runtime_token_rotation_credential",
		mariaDBRuntimeTokenRotationLaneLocksHeld,
	)
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"claim_system_update_runtime_token_rotation_credential",
		mariaDBRuntimeTokenRotationPolicyLocksHeld,
	)
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "claim_system_update_runtime_token_rotation_credential", lockPlan,
	)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if hasStagedServiceNodeConfiguration(service) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateExecutionHostBusy
	}
	if err := validateRuntimeTokenRotationOwnership(
		service,
		policy,
		ownership,
		stageParamsForRuntimeTokenRotation(rotation, params.Now),
	); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if service.TokenID != rotation.PreviousTokenID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	oldToken, ok := lockedTokens[rotation.PreviousTokenID]
	if !ok || oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateAgentInactive
	}
	stagedToken, ok := lockedTokens[rotation.StagedTokenID]
	if !ok || stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash ||
		stagedToken.RevokedAt == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	token, err := runtimeTokenRotationReplayToken(rotation, unseal)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if firstClaim {
		result, err := tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET revision = revision + 1, credential_claim_id_sha256 = ?,
    credential_claim_revision = ?, credential_claimed_at = ?, updated_at = ?
WHERE id = ? AND execution_host_id = ? AND service_id = ?
  AND status = ? AND revision = ?
  AND credential_claim_id_sha256 IS NULL
  AND credential_claim_revision IS NULL
  AND credential_claimed_at IS NULL`,
			claimIDHash, params.ExpectedRevision, params.Now, params.Now,
			rotation.ID, rotation.ExecutionHostID,
			rotation.ServiceID, SystemUpdateRuntimeTokenRotationStaged,
			params.ExpectedRevision,
		)
		if err != nil {
			return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
		}
		if affected != 1 {
			return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
				ErrSystemUpdateRuntimeTokenRotationStale
		}
		rotation.credentialClaimIDHash = claimIDHash
		rotation.credentialClaimRevision = params.ExpectedRevision
		rotation.Revision++
		rotation.CredentialClaimedAt = cloneTimePtr(&params.Now)
		rotation.UpdatedAt = params.Now
	}
	if err := tx.Commit(); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{
		Rotation: publicSystemUpdateRuntimeTokenRotation(rotation),
		Token:    token,
		Claimed:  firstClaim,
	}, nil
}

func mariaDBRuntimeTokenRotationPolicyForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	serviceID string,
) (UpdaterPolicy, error) {
	var (
		revision, projectionRevision, executorRevision int64
		body                                           []byte
		updatedAt                                      time.Time
	)
	err := tx.QueryRowContext(ctx, `SELECT revision, projection_revision,
local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`, serviceID).Scan(
		&revision, &projectionRevision, &executorRevision, &body, &updatedAt,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	return decodeUpdaterPolicyRevisions(
		serviceID, revision, projectionRevision, executorRevision, body, updatedAt,
	)
}

func mariaDBRuntimeTokenRotationRejectActiveSelfUpdate(
	ctx context.Context,
	tx *sql.Tx,
	executionHostID string,
) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id
FROM system_update_host_self_updates
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, executionHostID).Scan(&id)
	if err == nil {
		return ErrSystemUpdateHostSelfUpdateBusy
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
