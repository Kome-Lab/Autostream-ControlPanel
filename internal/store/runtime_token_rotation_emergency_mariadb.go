package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

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
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, id)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, _, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"emergency_revoke_system_update_runtime_token",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"emergency_revoke_system_update_runtime_token",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if !lockPlan.matches(rotation) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "emergency_revoke_system_update_runtime_token", lockPlan,
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
	previousToken, ok := lockedTokens[rotation.PreviousTokenID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	stagedToken, ok := lockedTokens[rotation.StagedTokenID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
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
	SystemUpdateExecutionHost,
	error,
) {
	var serviceID, storedHostID string
	err := tx.QueryRowContext(ctx, `SELECT service_id, execution_host_id
FROM system_update_runtime_token_rotations
WHERE id = ?`, rotationID).Scan(&serviceID, &storedHostID)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{}, err
	}
	if storedHostID != executionHostID {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{},
			ErrSystemUpdateOwnershipConflict
	}
	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{},
			ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{}, err
	}
	// Every host-scoped mutation takes the execution-host fence first and the
	// rotation next. Callers then lock the complete discovered service set in
	// sorted order before locking any service token row.
	rotation, err := scanSystemUpdateRuntimeTokenRotation(tx.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+` WHERE id = ? FOR UPDATE`,
		rotationID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{}, err
	}
	if rotation.ServiceID != serviceID ||
		rotation.ExecutionHostID != executionHostID {
		return SystemUpdateRuntimeTokenRotation{}, SystemUpdateExecutionHost{},
			ErrSystemUpdateOwnershipConflict
	}
	return rotation, ownership, nil
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
