package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *MariaDBSystemUpdateStore) CreateSystemUpdateJob(ctx context.Context, params CreateSystemUpdateJobParams) (SystemUpdateJob, bool, error) {
	params = normalizeSystemUpdateCreate(params)
	if err := validateSystemUpdateCreate(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if params.Operation != SystemUpdateOperationSoftwareUpdate {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortCoordinatorRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, params.ExecutionHostID)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := authorizeSystemUpdateExecutionHostAgent(ownership, params.AgentServiceID); err != nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateOwnershipConflict
	}
	existing, err := scanSystemUpdateJob(tx.QueryRowContext(
		ctx,
		systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ? FOR UPDATE`,
		params.RequestedByUserID,
		params.IdempotencyKey,
	))
	if err == nil {
		if sameSystemUpdateRequest(existing, params) {
			if err := tx.Commit(); err != nil {
				return SystemUpdateJob{}, false, err
			}
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}
	var portHold string
	err = tx.QueryRowContext(ctx, `SELECT j.id FROM system_update_jobs j
JOIN system_update_port_transactions p ON p.job_id=j.id
WHERE j.execution_host_id=? AND (j.status NOT IN ('succeeded','rolled_back','failed','canceled') OR p.recovery_required=1)
ORDER BY j.id LIMIT 1 FOR UPDATE`, params.ExecutionHostID).Scan(&portHold)
	if err == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}
	var activeRotationID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeRotationID)
	if err == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}
	now := time.Now().UTC()
	job := SystemUpdateJob{
		ID: newUUID(), TargetID: params.TargetID, TargetServiceType: params.TargetServiceType, Operation: params.Operation,
		AgentServiceID: params.AgentServiceID, ExecutionHostID: params.ExecutionHostID,
		TransportMode: ownership.TransportMode, OwnershipEpoch: ownership.OwnershipEpoch, PolicyRevision: ownership.PolicyRevision,
		DeploymentMode: params.DeploymentMode, CurrentVersion: params.CurrentVersion, TargetVersion: params.TargetVersion,
		Strategy: params.Strategy, Status: SystemUpdateStatusQueued, IdempotencyKey: params.IdempotencyKey,
		RequestedByUserID: params.RequestedByUserID, RequestedByUsername: params.RequestedByUsername,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO system_update_jobs
	(id, target_id, target_service_type, operation, agent_service_id, execution_host_id, transport_mode, ownership_epoch, policy_revision, deployment_mode, current_version, target_version, strategy, status, idempotency_key, requested_by_user_id, requested_by_username, sequence, progress, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		job.ID, job.TargetID, job.TargetServiceType, job.Operation, job.AgentServiceID, job.ExecutionHostID, job.TransportMode, job.OwnershipEpoch, job.PolicyRevision, job.DeploymentMode, job.CurrentVersion, job.TargetVersion,
		job.Strategy, job.Status, job.IdempotencyKey, job.RequestedByUserID, job.RequestedByUsername, now, now)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return SystemUpdateJob{}, false, err
		}
		return job, true, nil
	}
	if !isDuplicateKeyError(err) {
		return SystemUpdateJob{}, false, err
	}
	if existing, getErr := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ?`, params.RequestedByUserID, params.IdempotencyKey)); getErr == nil {
		if sameSystemUpdateRequest(existing, params) {
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	if _, getErr := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE active_target_id = ?`, params.TargetID)); getErr == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
	}
	return SystemUpdateJob{}, false, err
}

func (s *MariaDBSystemUpdateStore) CancelSystemUpdateJob(ctx context.Context, id, actorUserID string) (SystemUpdateJob, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(actorUserID) == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	hostID, err := s.systemUpdateJobExecutionHost(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateJob{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	defer tx.Rollback()
	if _, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, hostID); err != nil {
		return SystemUpdateJob{}, err
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateJob{}, err
	}
	if job.Status != SystemUpdateStatusQueued {
		return SystemUpdateJob{}, ErrSystemUpdateNotCancellable
	}
	now := time.Now().UTC()
	if job.Operation == SystemUpdateOperationPortReconfigure {
		if err := rollbackMariaDBQueuedSystemdPortJob(ctx, tx, job, now); err != nil {
			return SystemUpdateJob{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_update_jobs SET status = ?, code = ?, message = ?, cancelled_at = ?, completed_at = ?, updated_at = ? WHERE id = ?`, SystemUpdateStatusCancelled, "canceled_by_user", "Update canceled before it was claimed.", now, now, now, id); err != nil {
		return SystemUpdateJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateJob{}, err
	}
	job.Status = SystemUpdateStatusCancelled
	job.Code = "canceled_by_user"
	job.Message = "Update canceled before it was claimed."
	job.CancelledAt = &now
	job.CompletedAt = &now
	job.UpdatedAt = now
	if isSystemUpdatePortV2(job) {
		job.portTransaction.Phase = "canceled"
		projectSystemUpdatePortTransaction(&job)
	}
	return job, nil
}
