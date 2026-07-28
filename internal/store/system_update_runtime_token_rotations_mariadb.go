package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const systemUpdateRuntimeTokenRotationSelect = `SELECT id, service_id, execution_host_id,
idempotency_key, intent_sha256, status, revision,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
previous_token_id, staged_token_id, staged_token_hash, staged_token_scopes,
staged_token_ciphertext, staged_token_nonce, local_stage_receipt_id,
credential_claim_id_sha256, credential_claim_revision,
credential_claimed_at, local_stage_acknowledged_at, local_staged_at,
heartbeat_proved_at, activated_at,
cancel_requested_at, cancel_acknowledged_at, canceled_at,
emergency_revoked_token_id, emergency_revoked_at,
created_at, updated_at
FROM system_update_runtime_token_rotations`

func (s *MariaDBSystemUpdateStore) GetSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	id string,
) (SystemUpdateRuntimeTokenRotation, error) {
	id = strings.TrimSpace(id)
	if !serviceIDPattern.MatchString(id) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	rotation, err := scanSystemUpdateRuntimeTokenRotation(s.db.QueryRowContext(
		ctx, systemUpdateRuntimeTokenRotationSelect+` WHERE id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

func (s *MariaDBSystemUpdateStore) GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
	ctx context.Context,
	executionHostID string,
) (SystemUpdateRuntimeTokenRotation, error) {
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	rotation, err := scanSystemUpdateRuntimeTokenRotation(s.db.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+`
WHERE active_execution_host_id = ?`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

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
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
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
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(ctx, tx, params.ServiceID)
	if errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrNotFound
	}
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		params.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrNotFound
	}
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if hasStagedServiceNodeConfiguration(service) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrConflict
	}
	oldToken, err := selectActiveServiceTokenForUpdate(ctx, tx, service.TokenID)
	if errors.Is(err, ErrNotFound) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if oldToken.ServiceType != "update_agent" {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateAgentInactive
	}
	if err := validateRuntimeTokenRotationOwnership(service, policy, ownership, params); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	references, err := mariaDBRuntimeTokenServiceReferencesForUpdate(ctx, tx, oldToken.ID)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if len(references) != 1 || references[0] != service.ServiceID {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationSharedToken
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
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
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
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
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
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		rotation.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
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
	oldToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent")) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	references, err := mariaDBRuntimeTokenServiceReferencesForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if len(references) != 1 || references[0] != rotation.ServiceID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.StagedTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (stagedToken.ServiceType != "update_agent" ||
			stagedToken.TokenHash != rotation.stagedTokenHash ||
			stagedToken.RevokedAt == nil)) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
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

func mariaDBRuntimeTokenServiceReferencesForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	tokenID string,
) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT service_id
FROM services
WHERE token_id = ?
ORDER BY service_id
FOR UPDATE`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var serviceIDs []string
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, err
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	return serviceIDs, rows.Err()
}

type runtimeTokenRotationScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateRuntimeTokenRotation(
	scanner runtimeTokenRotationScanner,
) (SystemUpdateRuntimeTokenRotation, error) {
	var (
		rotation                                      SystemUpdateRuntimeTokenRotation
		scopesJSON                                    string
		localStageReceiptID, emergencyRevokedTokenID  sql.NullString
		stagedTokenCiphertext, stagedTokenNonce       sql.NullString
		credentialClaimIDHash                         sql.NullString
		credentialClaimRevision                       sql.NullInt64
		credentialClaimedAt, localStageAcknowledgedAt sql.NullTime
		localStagedAt                                 sql.NullTime
		heartbeatProvedAt, activatedAt                sql.NullTime
		cancelRequestedAt, cancelAcknowledgedAt       sql.NullTime
		canceledAt                                    sql.NullTime
		emergencyRevokedAt                            sql.NullTime
	)
	err := scanner.Scan(
		&rotation.ID, &rotation.ServiceID, &rotation.ExecutionHostID,
		&rotation.idempotencyKey, &rotation.intentSHA256, &rotation.Status,
		&rotation.Revision, &rotation.ExpectedOwnershipEpoch,
		&rotation.ExpectedSourcePolicyRevision, &rotation.ExpectedProjectionRevision,
		&rotation.ExpectedLocalExecutorPolicyRevision,
		&rotation.PreviousTokenID, &rotation.StagedTokenID,
		&rotation.stagedTokenHash, &scopesJSON,
		&stagedTokenCiphertext, &stagedTokenNonce,
		&localStageReceiptID, &credentialClaimIDHash, &credentialClaimRevision,
		&credentialClaimedAt,
		&localStageAcknowledgedAt, &localStagedAt,
		&heartbeatProvedAt, &activatedAt,
		&cancelRequestedAt, &cancelAcknowledgedAt, &canceledAt,
		&emergencyRevokedTokenID, &emergencyRevokedAt,
		&rotation.CreatedAt, &rotation.UpdatedAt,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &rotation.stagedTokenScopes); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	rotation.LocalStageReceiptID = localStageReceiptID.String
	rotation.stagedTokenCiphertext = stagedTokenCiphertext.String
	rotation.stagedTokenNonce = stagedTokenNonce.String
	rotation.credentialClaimIDHash = credentialClaimIDHash.String
	if credentialClaimRevision.Valid {
		rotation.credentialClaimRevision = credentialClaimRevision.Int64
	}
	rotation.EmergencyRevokedTokenID = emergencyRevokedTokenID.String
	if credentialClaimedAt.Valid {
		rotation.CredentialClaimedAt = cloneTimePtr(&credentialClaimedAt.Time)
	}
	if localStageAcknowledgedAt.Valid {
		rotation.LocalStageAcknowledgedAt = cloneTimePtr(&localStageAcknowledgedAt.Time)
	}
	if localStagedAt.Valid {
		rotation.LocalStagedAt = cloneTimePtr(&localStagedAt.Time)
	}
	if heartbeatProvedAt.Valid {
		rotation.HeartbeatProvedAt = cloneTimePtr(&heartbeatProvedAt.Time)
	}
	if activatedAt.Valid {
		rotation.ActivatedAt = cloneTimePtr(&activatedAt.Time)
	}
	if cancelRequestedAt.Valid {
		rotation.CancelRequestedAt = cloneTimePtr(&cancelRequestedAt.Time)
	}
	if cancelAcknowledgedAt.Valid {
		rotation.CancelAcknowledgedAt = cloneTimePtr(&cancelAcknowledgedAt.Time)
	}
	if canceledAt.Valid {
		rotation.CanceledAt = cloneTimePtr(&canceledAt.Time)
	}
	if emergencyRevokedAt.Valid {
		rotation.EmergencyRevokedAt = cloneTimePtr(&emergencyRevokedAt.Time)
	}
	return rotation, nil
}

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
	if rotation.ExecutionHostID != hostID {
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
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
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
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		rotation.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
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
	oldToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (oldToken.RevokedAt != nil ||
			oldToken.ServiceType != "update_agent")) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	references, err := mariaDBRuntimeTokenServiceReferencesForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if len(references) != 1 || references[0] != rotation.ServiceID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.StagedTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (stagedToken.ServiceType != "update_agent" ||
			stagedToken.TokenHash != rotation.stagedTokenHash ||
			stagedToken.RevokedAt == nil)) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
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
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
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
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
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
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		rotation.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if hasStagedServiceNodeConfiguration(service) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	oldToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (service.TokenID != rotation.PreviousTokenID ||
			oldToken.RevokedAt != nil ||
			oldToken.ServiceType != "update_agent")) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.StagedTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (stagedToken.RevokedAt == nil ||
			stagedToken.ServiceType != "update_agent" ||
			stagedToken.TokenHash != rotation.stagedTokenHash)) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
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

func (s *MariaDBSystemUpdateStore) ActivateSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	params ActivateSystemUpdateRuntimeTokenRotationParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, service, ownership, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if err := validateRuntimeTokenRotationCredential(rotation, params.RawStagedToken); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationActivated {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationHeartbeatProved {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err := validateMariaDBRuntimeTokenRotationHostFence(rotation, ownership); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if service.TokenID != rotation.PreviousTokenID ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != rotation.ExecutionHostID ||
		service.OwnershipEpoch != rotation.ExpectedOwnershipEpoch {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	references, err := mariaDBRuntimeTokenServiceReferencesForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if len(references) != 1 || references[0] != service.ServiceID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	oldToken, err := mariaDBRuntimeServiceTokenForUpdate(ctx, tx, rotation.PreviousTokenID)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(ctx, tx, rotation.StagedTokenID)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if oldToken.ServiceType != "update_agent" ||
		stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	result, err := tx.ExecContext(ctx, `UPDATE service_tokens
SET revoked_at = NULL
WHERE id = ? AND token_hash = ?`,
		rotation.StagedTokenID, rotation.stagedTokenHash,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens
SET revoked_at = COALESCE(revoked_at, ?)
WHERE id = ?`, now, rotation.PreviousTokenID); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE services
SET token_id = ?, node_token_ciphertext = ?, node_token_nonce = ?,
    last_heartbeat_at = ?, node_token_rotated_at = ?, updated_at = ?
WHERE service_id = ? AND token_id = ? AND transport_mode = ?
  AND execution_host_id = ? AND ownership_epoch = ?`,
		rotation.StagedTokenID, rotation.stagedTokenCiphertext,
		rotation.stagedTokenNonce, rotation.HeartbeatProvedAt, now, now,
		rotation.ServiceID, rotation.PreviousTokenID,
		SystemUpdateTransportPullV2, rotation.ExecutionHostID,
		rotation.ExpectedOwnershipEpoch,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1, activated_at = ?,
    staged_token_ciphertext = NULL, staged_token_nonce = NULL,
    credential_claim_id_sha256 = NULL, credential_claim_revision = NULL,
    updated_at = ?
WHERE id = ? AND execution_host_id = ? AND status = ? AND revision = ?`,
		SystemUpdateRuntimeTokenRotationActivated, now, now, rotation.ID,
		hostID, SystemUpdateRuntimeTokenRotationHeartbeatProved,
		expectedRevision,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationActivated
	rotation.Revision++
	rotation.ActivatedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MariaDBSystemUpdateStore) CancelSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	params CancelSystemUpdateRuntimeTokenRotationParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, _, ownership, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationActivated {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCancelRequested {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationStaged &&
		rotation.Status != SystemUpdateRuntimeTokenRotationLocalStaged &&
		rotation.Status != SystemUpdateRuntimeTokenRotationHeartbeatProved {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if err := validateMariaDBRuntimeTokenRotationHostFence(rotation, ownership); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	immediate := rotation.Status == SystemUpdateRuntimeTokenRotationStaged &&
		rotation.CredentialClaimedAt == nil
	var result sql.Result
	if immediate {
		if _, err := tx.ExecContext(ctx, `UPDATE service_tokens
SET revoked_at = COALESCE(revoked_at, ?)
WHERE id = ?`, now, rotation.StagedTokenID); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		result, err = tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1, canceled_at = ?,
    staged_token_ciphertext = NULL, staged_token_nonce = NULL,
    credential_claim_id_sha256 = NULL, credential_claim_revision = NULL,
    updated_at = ?
WHERE id = ? AND execution_host_id = ? AND status = ? AND revision = ?
  AND credential_claimed_at IS NULL`,
			SystemUpdateRuntimeTokenRotationCanceled, now, now, rotation.ID,
			hostID, SystemUpdateRuntimeTokenRotationStaged, expectedRevision,
		)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1, cancel_requested_at = ?, updated_at = ?
WHERE id = ? AND execution_host_id = ? AND status IN (?, ?, ?) AND revision = ?`,
			SystemUpdateRuntimeTokenRotationCancelRequested, now, now, rotation.ID,
			hostID, SystemUpdateRuntimeTokenRotationStaged,
			SystemUpdateRuntimeTokenRotationLocalStaged,
			SystemUpdateRuntimeTokenRotationHeartbeatProved, expectedRevision,
		)
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if immediate {
		rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
		rotation.CanceledAt = cloneTimePtr(&now)
		scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	} else {
		rotation.Status = SystemUpdateRuntimeTokenRotationCancelRequested
		rotation.CancelRequestedAt = cloneTimePtr(&now)
	}
	rotation.Revision++
	rotation.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

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
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
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
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
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
	policy, err := mariaDBRuntimeTokenRotationPolicyForUpdate(
		ctx, tx, rotation.ServiceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		rotation.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
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
	oldToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (oldToken.RevokedAt != nil ||
			oldToken.ServiceType != "update_agent")) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.StagedTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (stagedToken.ServiceType != "update_agent" ||
			stagedToken.TokenHash != rotation.stagedTokenHash)) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
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

func (s *MariaDBSystemUpdateStore) EmergencyRevokeSystemUpdateRuntimeToken(
	ctx context.Context,
	services ServiceRegistryStore,
	params EmergencyRevokeSystemUpdateRuntimeTokenParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	params.TokenID = strings.TrimSpace(params.TokenID)
	if err != nil || !serviceIDPattern.MatchString(params.TokenID) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, service, _, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if params.TokenID != rotation.PreviousTokenID &&
		params.TokenID != rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if rotation.EmergencyRevokedTokenID != "" {
		if rotation.EmergencyRevokedTokenID != params.TokenID ||
			!rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateRuntimeTokenRotation{}, false, err
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	previousToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.PreviousTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	stagedToken, err := mariaDBRuntimeServiceTokenForUpdate(
		ctx, tx, rotation.StagedTokenID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if previousToken.ServiceType != "update_agent" ||
		stagedToken.ServiceType != "update_agent" ||
		(service.TokenID != rotation.PreviousTokenID &&
			service.TokenID != rotation.StagedTokenID) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens
SET revoked_at = COALESCE(revoked_at, ?)
WHERE id IN (?, ?)`,
		now, rotation.PreviousTokenID, rotation.StagedTokenID,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE services
SET status = 'offline', last_heartbeat_at = NULL,
    reported_capabilities = '{}', node_token_ciphertext = NULL,
    node_token_nonce = NULL, configure_token_hash = NULL,
    configure_token_expires_at = NULL, configure_token_used_at = NULL,
    staged_node_previous_token_id = NULL, staged_node_token_id = NULL,
    staged_node_token_hash = NULL, staged_node_token_scopes = NULL,
    staged_node_token_ciphertext = NULL, staged_node_token_nonce = NULL,
    staged_node_activation_token_hash = NULL, staged_node_token_at = NULL,
    updated_at = ?
WHERE service_id = ? AND token_id IN (?, ?)`,
		now, rotation.ServiceID, rotation.PreviousTokenID,
		rotation.StagedTokenID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE system_update_runtime_token_rotations
SET status = ?, revision = revision + 1,
    cancel_requested_at = NULL, cancel_acknowledged_at = NULL,
    canceled_at = ?, emergency_revoked_token_id = ?,
    emergency_revoked_at = ?, staged_token_ciphertext = NULL,
    staged_token_nonce = NULL, credential_claim_id_sha256 = NULL,
    credential_claim_revision = NULL, updated_at = ?
WHERE id = ? AND execution_host_id = ? AND revision = ?
  AND status <> ? AND emergency_revoked_token_id IS NULL`,
		SystemUpdateRuntimeTokenRotationCanceled, now, params.TokenID,
		now, now, rotation.ID, hostID, expectedRevision,
		SystemUpdateRuntimeTokenRotationCanceled,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	} else if affected != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
	rotation.Revision++
	rotation.CancelRequestedAt = nil
	rotation.CancelAcknowledgedAt = nil
	rotation.CanceledAt = cloneTimePtr(&now)
	rotation.EmergencyRevokedTokenID = params.TokenID
	rotation.EmergencyRevokedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	if err := tx.Commit(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func mariaDBRuntimeTokenRotationForTransition(
	ctx context.Context,
	tx *sql.Tx,
	rotationID, executionHostID string,
) (
	SystemUpdateRuntimeTokenRotation,
	RegisteredService,
	SystemUpdateExecutionHost,
	error,
) {
	var serviceID, storedHostID string
	err := tx.QueryRowContext(ctx, `SELECT service_id, execution_host_id
FROM system_update_runtime_token_rotations
WHERE id = ?`, rotationID).Scan(&serviceID, &storedHostID)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, err
	}
	if storedHostID != executionHostID {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrSystemUpdateOwnershipConflict
	}
	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, err
	}
	// Every host-scoped mutation takes the execution-host fence first. Lock the
	// rotation next, then the service row, so transitions cannot deadlock with
	// stage, job creation, policy changes, or ownership changes.
	rotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+` WHERE id = ? FOR UPDATE`,
		rotationID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, err
	}
	if rotation.ServiceID != serviceID ||
		rotation.ExecutionHostID != executionHostID {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrSystemUpdateOwnershipConflict
	}
	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		rotation.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, err
	}
	if rotation.ServiceID != service.ServiceID {
		return SystemUpdateRuntimeTokenRotation{}, RegisteredService{},
			SystemUpdateExecutionHost{}, ErrSystemUpdateOwnershipConflict
	}
	return rotation, service, ownership, nil
}

func validateMariaDBRuntimeTokenRotationHostFence(
	rotation SystemUpdateRuntimeTokenRotation,
	ownership SystemUpdateExecutionHost,
) error {
	if ownership.ExecutionHostID != rotation.ExecutionHostID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != rotation.ServiceID ||
		ownership.OwnershipEpoch != rotation.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != rotation.ExpectedProjectionRevision {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func mariaDBRuntimeServiceTokenForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	id string,
) (ServiceToken, error) {
	var token ServiceToken
	var scopesJSON string
	var revokedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT id, service_type, token_hash,
scopes, revoked_at, created_at
FROM service_tokens
WHERE id = ?
FOR UPDATE`, id).Scan(
		&token.ID, &token.ServiceType, &token.TokenHash, &scopesJSON,
		&revokedAt, &token.CreatedAt,
	)
	if err != nil {
		return ServiceToken{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return ServiceToken{}, err
	}
	if revokedAt.Valid {
		token.RevokedAt = cloneTimePtr(&revokedAt.Time)
	}
	return token, nil
}

var _ SystemUpdateRuntimeTokenRotationStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateRuntimeTokenRotationStore = (*MariaDBSystemUpdateStore)(nil)
