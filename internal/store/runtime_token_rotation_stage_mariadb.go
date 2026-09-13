package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"strings"
	"time"
)

func (s *MariaDBSystemUpdateStore) StageSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params StageSystemUpdateRuntimeTokenRotationParams,
	seal NodeTokenSealer,
) (StageSystemUpdateRuntimeTokenRotationResult, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		result, err := s.stageSystemUpdateRuntimeTokenRotationOnce(
			ctx, services, policies, params, seal,
		)
		if err == nil || !isMariaDBRuntimeTokenRotationDeadlock(err) {
			return result, err
		}
		lastErr = err
		if attempt+1 == maxAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return StageSystemUpdateRuntimeTokenRotationResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return StageSystemUpdateRuntimeTokenRotationResult{}, lastErr
}

func (s *MariaDBSystemUpdateStore) stageSystemUpdateRuntimeTokenRotationOnce(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params StageSystemUpdateRuntimeTokenRotationParams,
	seal NodeTokenSealer,
) (StageSystemUpdateRuntimeTokenRotationResult, error) {
	params = normalizeStageSystemUpdateRuntimeTokenRotationParams(params)
	if err := validateStageSystemUpdateRuntimeTokenRotationParams(params); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if seal == nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, errNodeTokenSealerRequired
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyDB, ok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || policyDB != s.db {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	intentSHA256, err := runtimeTokenRotationIntentSHA256(params)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	lockPlan, err := s.discoverMariaDBRuntimeTokenStageLockPlan(ctx, params.ServiceID)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	defer tx.Rollback()

	// The execution-host row is the first durable lock. Job creation, policy
	// mutation, ownership mutation, and every rotation transition use the same
	// lane fence so cross-connection races cannot split the host lifecycle.
	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"stage_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	existing, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+`
WHERE service_id = ? AND idempotency_key = ?
FOR UPDATE`,
		params.ServiceID, params.IdempotencyKey,
	))
	if err == nil {
		if existing.intentSHA256 != intentSHA256 {
			return StageSystemUpdateRuntimeTokenRotationResult{}, ErrAlreadyExists
		}
		if err := tx.Commit(); err != nil {
			return StageSystemUpdateRuntimeTokenRotationResult{}, err
		}
		return StageSystemUpdateRuntimeTokenRotationResult{
			Rotation: publicSystemUpdateRuntimeTokenRotation(existing),
			Created:  false,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	activeRotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+`
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID,
	))
	if err == nil {
		_ = activeRotation
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
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
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if err := mariaDBRuntimeTokenRotationRejectActiveSelfUpdate(
		ctx, tx, params.ExecutionHostID,
	); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"stage_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationLaneLocksHeld,
	)
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(ctx, tx, params.ServiceID)
	if errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrNotFound
	}
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"stage_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationPolicyLocksHeld,
	)
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "stage_system_update_runtime_token_rotation", lockPlan,
	)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if service.TokenID != lockPlan.PreviousTokenID {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateOwnershipConflict
	}
	if hasStagedServiceNodeConfiguration(service) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrConflict
	}
	oldToken, ok := lockedTokens[service.TokenID]
	if !ok || oldToken.RevokedAt != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateAgentInactive
	}
	if oldToken.ServiceType != "update_agent" {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateAgentInactive
	}
	if err := validateRuntimeTokenRotationOwnership(service, policy, ownership, params); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	stagedToken, scopesJSON, err := newRotatedServiceToken(oldToken, params.Now)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	ciphertext, nonce, err := seal(stagedToken.RawToken)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if strings.TrimSpace(ciphertext) == "" || strings.TrimSpace(nonce) == "" {
		return StageSystemUpdateRuntimeTokenRotationResult{}, errors.New("node token sealer returned an empty sealed value")
	}
	rotation := SystemUpdateRuntimeTokenRotation{
		ID:                                  newUUID(),
		ServiceID:                           params.ServiceID,
		ExecutionHostID:                     params.ExecutionHostID,
		Status:                              SystemUpdateRuntimeTokenRotationStaged,
		Revision:                            1,
		ExpectedOwnershipEpoch:              params.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        params.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          params.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: params.ExpectedLocalExecutorPolicyRevision,
		PreviousTokenID:                     oldToken.ID,
		StagedTokenID:                       stagedToken.ID,
		CreatedAt:                           params.Now,
		UpdatedAt:                           params.Now,
		idempotencyKey:                      params.IdempotencyKey,
		intentSHA256:                        intentSHA256,
		stagedTokenHash:                     stagedToken.TokenHash,
		stagedTokenScopes:                   append([]string(nil), stagedToken.Scopes...),
		stagedTokenCiphertext:               ciphertext,
		stagedTokenNonce:                    nonce,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens
(id, service_type, token_hash, scopes, revoked_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		stagedToken.ID, stagedToken.ServiceType, stagedToken.TokenHash,
		scopesJSON, params.Now, stagedToken.CreatedAt,
	); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO system_update_runtime_token_rotations
(id, service_id, execution_host_id, idempotency_key, intent_sha256, status,
 revision, expected_ownership_epoch, expected_source_policy_revision,
 expected_projection_revision, expected_local_executor_policy_revision,
 previous_token_id, staged_token_id, staged_token_hash, staged_token_scopes,
 staged_token_ciphertext, staged_token_nonce, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rotation.ID, rotation.ServiceID, rotation.ExecutionHostID,
		rotation.idempotencyKey, rotation.intentSHA256, rotation.Status,
		rotation.Revision, rotation.ExpectedOwnershipEpoch,
		rotation.ExpectedSourcePolicyRevision, rotation.ExpectedProjectionRevision,
		rotation.ExpectedLocalExecutorPolicyRevision, rotation.PreviousTokenID,
		rotation.StagedTokenID, rotation.stagedTokenHash, scopesJSON,
		rotation.stagedTokenCiphertext, rotation.stagedTokenNonce,
		rotation.CreatedAt, rotation.UpdatedAt,
	); err != nil {
		if isDuplicateKeyError(err) {
			return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
		}
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	return StageSystemUpdateRuntimeTokenRotationResult{
		Rotation: publicSystemUpdateRuntimeTokenRotation(rotation),
		Created:  true,
	}, nil
}

func isMariaDBRuntimeTokenRotationDeadlock(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1213
}
