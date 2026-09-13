package store

import (
	"context"
	"database/sql"
)

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
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, id)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, ownership, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"activate_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"activate_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if !lockPlan.matches(rotation) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	service, lockedTokens, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "activate_system_update_runtime_token_rotation", lockPlan,
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
	oldToken, ok := lockedTokens[rotation.PreviousTokenID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	stagedToken, ok := lockedTokens[rotation.StagedTokenID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
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
	lockPlan, err := s.discoverMariaDBRuntimeTokenRotationLockPlan(ctx, id)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	defer tx.Rollback()
	rotation, ownership, err := mariaDBRuntimeTokenRotationForTransition(
		ctx, tx, id, hostID,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"cancel_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationHostLocksHeld,
	)
	observeMariaDBRuntimeTokenRotationLockPhase(
		ctx,
		"cancel_system_update_runtime_token_rotation",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
	if !lockPlan.matches(rotation) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if _, _, err := lockMariaDBRuntimeTokenRotationPlan(
		ctx, tx, "cancel_system_update_runtime_token_rotation", lockPlan,
	); err != nil {
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
