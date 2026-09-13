package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s *MariaDBSystemUpdateStore) ClaimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (SystemUpdateClaim, bool, error) {
	return s.claimSystemUpdateJob(ctx, agentServiceID, executionHostID, activeJobID, 0, 0, eligibleTargets, now, leaseTTL, false)
}

func (s *MariaDBSystemUpdateStore) ClaimSystemUpdateJobV2(ctx context.Context, agentServiceID, executionHostID, activeJobID string, expectedLeaseGeneration, expectedFence int64, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (SystemUpdateClaim, bool, error) {
	return s.claimSystemUpdateJob(ctx, agentServiceID, executionHostID, activeJobID, expectedLeaseGeneration, expectedFence, eligibleTargets, now, leaseTTL, true)
}

func (s *MariaDBSystemUpdateStore) claimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, expectedLeaseGeneration, expectedFence int64, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration, v2 bool) (SystemUpdateClaim, bool, error) {
	agentServiceID = strings.TrimSpace(agentServiceID)
	executionHostID = normalizeSystemUpdateExecutionHostID(agentServiceID, executionHostID)
	activeJobID = strings.TrimSpace(activeJobID)
	targets := normalizedEligibleTargets(eligibleTargets)
	if agentServiceID == "" || !validSystemUpdateExecutionHostID(executionHostID) || (len(targets) == 0 && activeJobID == "") || leaseTTL <= 0 || len(activeJobID) > 64 || containsControl(activeJobID) ||
		(v2 && (expectedLeaseGeneration < 1 || expectedFence < 1)) {
		return SystemUpdateClaim{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, executionHostID)
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	if err := authorizeSystemUpdateExecutionHostAgent(ownership, agentServiceID); err != nil {
		return SystemUpdateClaim{}, false, ErrSystemUpdateOwnershipConflict
	}
	if v2 && ownership.OwnershipEpoch != expectedFence {
		return SystemUpdateClaim{}, false, ErrSystemUpdateOwnershipConflict
	}

	var job SystemUpdateJob
	if activeJobID != "" {
		job, err = scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, activeJobID))
		if errors.Is(err, sql.ErrNoRows) {
			return SystemUpdateClaim{}, false, ErrSystemUpdateRecoveryProofUnavailable
		}
		if err != nil {
			return SystemUpdateClaim{}, false, err
		}
		if job.AgentServiceID != agentServiceID {
			return SystemUpdateClaim{}, false, ErrSystemUpdateOwnershipConflict
		}
		if !isExecutingSystemUpdateStatus(job.Status) && !(v2 && systemUpdatePortV2Recoverable(job)) {
			return SystemUpdateClaim{}, false, ErrSystemUpdateRecoveryProofUnavailable
		}
		if job.ExecutionHostID != executionHostID {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
		if v2 && job.LeaseGeneration != expectedLeaseGeneration {
			return SystemUpdateClaim{}, false, ErrSystemUpdateLeaseInvalid
		}
		mode, authorized := targets[job.TargetID]
		if !authorized || mode != job.DeploymentMode {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
	} else if !v2 {
		job, err = findExecutingSystemUpdateForAgentHost(ctx, tx, agentServiceID, executionHostID)
		if err == nil {
			mode, eligible := targets[job.TargetID]
			if job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) || !eligible || mode != job.DeploymentMode {
				return SystemUpdateClaim{}, false, ErrNotFound
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			job, err = findClaimableSystemUpdate(ctx, tx, agentServiceID, executionHostID, targets, now, false)
		}
	} else if expectedLeaseGeneration != 1 {
		return SystemUpdateClaim{}, false, ErrSystemUpdateLeaseInvalid
	} else {
		job, err = findClaimableSystemUpdate(ctx, tx, agentServiceID, executionHostID, targets, now, false)
	}
	if errors.Is(err, sql.ErrNoRows) {
		foreign, foreignErr := findClaimableSystemUpdate(ctx, tx, "", "", targets, now, true)
		if foreignErr == nil && foreign.AgentServiceID != agentServiceID {
			return SystemUpdateClaim{}, false, ErrSystemUpdateTakeoverForbidden
		}
		if foreignErr != nil && !errors.Is(foreignErr, sql.ErrNoRows) {
			return SystemUpdateClaim{}, false, foreignErr
		}
		return SystemUpdateClaim{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	if err := authorizeSystemUpdateJobOwnership(job, ownership, agentServiceID, executionHostID); err != nil {
		return SystemUpdateClaim{}, false, err
	}
	if isSystemUpdatePortV2(job) {
		if _, _, err := lockMariaDBPortV2State(ctx, tx, job); err != nil {
			return SystemUpdateClaim{}, false, err
		}
	}
	leaseToken, err := newSystemUpdateLeaseToken()
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	leaseExpiresAt := now.Add(leaseTTL)
	if v2 {
		// The immutable v2 lease is compared with later DATETIME(6) reads.
		// Persist and return the same expiry without extending its lifetime.
		leaseExpiresAt = leaseExpiresAt.Truncate(time.Microsecond)
	}
	claimedAt := job.ClaimedAt
	lastStatus := job.Status
	recoveryRequired := lastStatus != SystemUpdateStatusQueued
	status := SystemUpdateStatusReconciling
	if !recoveryRequired {
		status = SystemUpdateStatusClaimed
		claimedAt = &now
	}
	leaseGeneration := job.LeaseGeneration + 1
	sequence := job.Sequence
	if v2 {
		sequence = 0
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_jobs SET status = ?, agent_service_id = ?, lease_generation = ?, lease_token_hash = ?, lease_expires_at = ?, claimed_at = ?, sequence = ?, updated_at = ? WHERE id = ?`, status, agentServiceID, leaseGeneration, security.HashToken(leaseToken), leaseExpiresAt, claimedAt, sequence, now, job.ID)
	if err != nil {
		if isDuplicateKeyError(err) {
			return SystemUpdateClaim{}, false, ErrNotFound
		}
		return SystemUpdateClaim{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return SystemUpdateClaim{}, false, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateClaim{}, false, err
	}
	job.Status = status
	job.AgentServiceID = agentServiceID
	job.LeaseGeneration = leaseGeneration
	job.Sequence = sequence
	job.LeaseExpiresAt = &leaseExpiresAt
	job.ClaimedAt = claimedAt
	job.UpdatedAt = now
	return SystemUpdateClaim{Job: job, LeaseToken: leaseToken, LeaseExpiresAt: leaseExpiresAt, LeaseGeneration: leaseGeneration, ReportSequence: sequence + 1, RecoveryRequired: recoveryRequired, LastStatus: lastStatus}, false, nil
}
