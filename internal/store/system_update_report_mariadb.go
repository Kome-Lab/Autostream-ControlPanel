package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *MariaDBSystemUpdateStore) ReportSystemUpdateJob(ctx context.Context, id string, report SystemUpdateReport, now time.Time, leaseTTL time.Duration) (SystemUpdateJob, bool, error) {
	id = strings.TrimSpace(id)
	report = normalizeSystemUpdateReport(report)
	if id == "" || leaseTTL <= 0 || validateSystemUpdateReport(report) != nil {
		return SystemUpdateJob{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	executionHostID, err := s.systemUpdateJobExecutionHost(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, executionHostID)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	report, err = canonicalizeSystemUpdatePortReport(job, report)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := authorizeSystemUpdateJobOwnership(job, ownership, report.AgentServiceID, authenticatedSystemUpdateExecutionHost(job, report.ExecutionHostID)); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if isTerminalSystemUpdateStatus(job.Status) {
		if !systemUpdateReportLeaseMatches(job, report, now, false) {
			return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
		}
		if isSystemUpdatePortV2(job) && job.PortResult != nil && report.Sequence >= job.Sequence && report.Sequence <= job.Sequence+1 && systemUpdatePortV2AcceptedReplay(job, report) {
			return job, false, nil
		}
		if report.Sequence != job.Sequence || !sameSystemUpdateReport(job, report) {
			return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
		}
		return job, false, nil
	}
	if !systemUpdateReportLeaseMatches(job, report, now, true) {
		return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
	}
	if report.Sequence < job.Sequence || report.Sequence > job.Sequence+1 || (report.Sequence == job.Sequence && !sameSystemUpdateReport(job, report)) {
		return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
	}
	if report.Sequence == job.Sequence {
		if report.ProtocolVersion == 2 {
			return job, false, nil
		}
		expires := now.Add(leaseTTL)
		if _, err := tx.ExecContext(ctx, `UPDATE system_update_jobs SET lease_expires_at = ?, updated_at = ? WHERE id = ?`, expires, now, id); err != nil {
			return SystemUpdateJob{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return SystemUpdateJob{}, false, err
		}
		job.LeaseExpiresAt = &expires
		job.UpdatedAt = now
		return job, false, nil
	}
	if !allowedSystemUpdateJobTransition(job, report.Status) || report.Progress < job.Progress {
		return SystemUpdateJob{}, false, ErrSystemUpdateTransition
	}
	terminal := isTerminalSystemUpdateStatus(report.Status)
	var leaseExpires any
	var completedAt any
	if terminal {
		leaseExpires = nil
		completedAt = now
		if isSystemUpdatePortV2(job) {
			if err := finishMariaDBSystemUpdatePortV2(ctx, tx, &job, report, now); err != nil {
				return SystemUpdateJob{}, false, err
			}
		} else if job.Operation == SystemUpdateOperationPortReconfigure {
			if err := applyMariaDBSystemdPortTerminalState(
				ctx, tx, job, report.PortReconfigure.Result, now,
			); err != nil {
				return SystemUpdateJob{}, false, err
			}
		}
	} else {
		if report.ProtocolVersion == 2 {
			leaseExpires = job.LeaseExpiresAt.UTC()
		} else {
			leaseExpires = now.Add(leaseTTL)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE system_update_jobs SET status = ?, sequence = ?, progress = ?, code = ?, message = ?, artifact_digest = ?, previous_digest = ?, lease_expires_at = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		report.Status, report.Sequence, report.Progress, report.Code, report.Message, report.ArtifactDigest, report.PreviousDigest, leaseExpires, completedAt, now, id)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeSystemUpdatePortCommit(ctx, job, report.Status, SystemUpdatePortBeforeCommit)
	if err := tx.Commit(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeSystemUpdatePortCommit(ctx, job, report.Status, SystemUpdatePortAfterCommit)
	job.Status = report.Status
	job.Sequence = report.Sequence
	job.Progress = report.Progress
	job.Code = report.Code
	job.Message = report.Message
	job.ArtifactDigest = report.ArtifactDigest
	job.PreviousDigest = report.PreviousDigest
	job.UpdatedAt = now
	if terminal {
		if job.Operation == SystemUpdateOperationPortReconfigure && !isSystemUpdatePortV2(job) {
			job.PortReconfigure.Result = report.PortReconfigure.Result
		}
		job.LeaseExpiresAt = nil
		job.CompletedAt = &now
	} else {
		expires := leaseExpires.(time.Time)
		job.LeaseExpiresAt = &expires
	}
	return job, true, nil
}

func (s *MariaDBSystemUpdateStore) AuthorizeSystemUpdateMutation(ctx context.Context, id string, authorization SystemUpdateAuthorization, now time.Time) error {
	id = strings.TrimSpace(id)
	authorization = normalizeSystemUpdateAuthorization(authorization)
	if id == "" || validateSystemUpdateAuthorization(authorization) != nil {
		return ErrInvalidSystemUpdate
	}
	executionHostID, err := s.systemUpdateJobExecutionHost(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, executionHostID)
	if err != nil {
		return err
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := authorizeSystemUpdateJobOwnership(job, ownership, authorization.AgentServiceID, job.ExecutionHostID); err != nil {
		return err
	}
	return authorizeSystemUpdateMutation(job, authorization, now.UTC())
}
