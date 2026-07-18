package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

const (
	SystemUpdateStrategyWhenIdle    = "when_idle"
	SystemUpdateStrategyMaintenance = "maintenance"

	SystemUpdateStatusQueued         = "queued"
	SystemUpdateStatusClaimed        = "claimed"
	SystemUpdateStatusDownloading    = "downloading"
	SystemUpdateStatusVerifying      = "verifying"
	SystemUpdateStatusStaging        = "staging"
	SystemUpdateStatusStopping       = "stopping"
	SystemUpdateStatusInstalling     = "installing"
	SystemUpdateStatusStarting       = "starting"
	SystemUpdateStatusHealthChecking = "health_checking"
	SystemUpdateStatusReconciling    = "reconciling"
	SystemUpdateStatusSucceeded      = "succeeded"
	SystemUpdateStatusRollingBack    = "rolling_back"
	SystemUpdateStatusRolledBack     = "rolled_back"
	SystemUpdateStatusFailed         = "failed"
	SystemUpdateStatusCancelled      = "canceled"
)

var systemUpdateJobVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

var (
	ErrInvalidSystemUpdate               = errors.New("invalid system update")
	ErrSystemUpdateTargetActive          = errors.New("system update target already has an active job")
	ErrSystemUpdateLeaseInvalid          = errors.New("system update lease is invalid or expired")
	ErrSystemUpdateSequenceStale         = errors.New("system update report sequence is stale")
	ErrSystemUpdateTransition            = errors.New("invalid system update status transition")
	ErrSystemUpdateNotCancellable        = errors.New("system update job is not cancellable")
	ErrSystemUpdateTakeoverForbidden     = errors.New("system update takeover requires explicit administrator reassignment")
	ErrSystemUpdateActiveUnavailable     = errors.New("active system update target is no longer authorized for this updater")
	ErrSystemUpdateAuthorizationState    = errors.New("system update is not in a mutation-authorizable state")
	ErrSystemUpdateAuthorizationMismatch = errors.New("system update authorization request does not match the job")
)

type SystemUpdateJob struct {
	ID                  string     `json:"id"`
	TargetID            string     `json:"target_id"`
	TargetServiceType   string     `json:"target_type"`
	DeploymentMode      string     `json:"deployment_mode"`
	CurrentVersion      string     `json:"current_version"`
	TargetVersion       string     `json:"target_version"`
	Strategy            string     `json:"strategy"`
	Status              string     `json:"status"`
	IdempotencyKey      string     `json:"idempotency_key"`
	RequestedByUserID   string     `json:"-"`
	RequestedByUsername string     `json:"requested_by,omitempty"`
	AgentServiceID      string     `json:"updater_id,omitempty"`
	ExecutionHostID     string     `json:"host_id"`
	LeaseGeneration     int64      `json:"lease_generation"`
	LeaseExpiresAt      *time.Time `json:"lease_expires_at,omitempty"`
	Sequence            int64      `json:"sequence"`
	Progress            int        `json:"progress"`
	Code                string     `json:"code,omitempty"`
	Message             string     `json:"message,omitempty"`
	ArtifactDigest      string     `json:"artifact_digest,omitempty"`
	PreviousDigest      string     `json:"previous_digest,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ClaimedAt           *time.Time `json:"claimed_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	CancelledAt         *time.Time `json:"canceled_at,omitempty"`

	leaseTokenHash string
}

type CreateSystemUpdateJobParams struct {
	TargetID            string
	TargetServiceType   string
	AgentServiceID      string
	ExecutionHostID     string
	DeploymentMode      string
	CurrentVersion      string
	TargetVersion       string
	Strategy            string
	IdempotencyKey      string
	RequestedByUserID   string
	RequestedByUsername string
}

type SystemUpdateClaim struct {
	Job              SystemUpdateJob `json:"job"`
	LeaseToken       string          `json:"lease_token"`
	LeaseExpiresAt   time.Time       `json:"lease_expires_at"`
	LeaseGeneration  int64           `json:"lease_generation"`
	ReportSequence   int64           `json:"report_sequence"`
	RecoveryRequired bool            `json:"recovery_required"`
	LastStatus       string          `json:"last_status"`
}

type SystemUpdateReport struct {
	AgentServiceID  string
	LeaseToken      string
	LeaseGeneration int64
	Sequence        int64
	Status          string
	Progress        int
	Code            string
	Message         string
	ArtifactDigest  string
	PreviousDigest  string
}

type SystemUpdateAuthorization struct {
	AgentServiceID  string
	ExecutionHostID string
	LeaseToken      string
	LeaseGeneration int64
	TargetID        string
	TargetVersion   string
	DeploymentMode  string
}

type SystemUpdateStore interface {
	ListSystemUpdateJobs(ctx context.Context, limit int) ([]SystemUpdateJob, error)
	GetSystemUpdateJobByIdempotency(ctx context.Context, requestedByUserID, idempotencyKey string) (SystemUpdateJob, error)
	GetActiveSystemUpdateJob(ctx context.Context, targetID string) (SystemUpdateJob, error)
	CreateSystemUpdateJob(ctx context.Context, params CreateSystemUpdateJobParams) (job SystemUpdateJob, created bool, err error)
	CancelSystemUpdateJob(ctx context.Context, id, actorUserID string) (SystemUpdateJob, error)
	ClaimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (claim SystemUpdateClaim, clearActiveJob bool, err error)
	ReportSystemUpdateJob(ctx context.Context, id string, report SystemUpdateReport, now time.Time, leaseTTL time.Duration) (job SystemUpdateJob, applied bool, err error)
	AuthorizeSystemUpdateMutation(ctx context.Context, id string, authorization SystemUpdateAuthorization, now time.Time) error
	HasActiveSystemUpdateReference(ctx context.Context, serviceID string) (bool, error)
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdateJobByIdempotency(ctx context.Context, requestedByUserID, idempotencyKey string) (SystemUpdateJob, error) {
	requestedByUserID = strings.TrimSpace(requestedByUserID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if requestedByUserID == "" || idempotencyKey == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	return s.getSystemUpdateByIdempotency(ctx, requestedByUserID, idempotencyKey)
}

func (s *MariaDBSystemUpdateStore) GetActiveSystemUpdateJob(ctx context.Context, targetID string) (SystemUpdateJob, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	return s.getActiveSystemUpdateForTarget(ctx, targetID)
}

type MariaDBSystemUpdateStore struct {
	db *sql.DB
}

func NewMariaDBSystemUpdateStore(db *sql.DB) *MariaDBSystemUpdateStore {
	return &MariaDBSystemUpdateStore{db: db}
}

func (s *MariaDBSystemUpdateStore) ListSystemUpdateJobs(ctx context.Context, limit int) ([]SystemUpdateJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, systemUpdateSelect+` ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]SystemUpdateJob, 0)
	for rows.Next() {
		job, err := scanSystemUpdateJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *MariaDBSystemUpdateStore) CreateSystemUpdateJob(ctx context.Context, params CreateSystemUpdateJobParams) (SystemUpdateJob, bool, error) {
	params = normalizeSystemUpdateCreate(params)
	if err := validateSystemUpdateCreate(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	now := time.Now().UTC()
	job := SystemUpdateJob{
		ID: newUUID(), TargetID: params.TargetID, TargetServiceType: params.TargetServiceType,
		AgentServiceID: params.AgentServiceID, ExecutionHostID: params.ExecutionHostID,
		DeploymentMode: params.DeploymentMode, CurrentVersion: params.CurrentVersion, TargetVersion: params.TargetVersion,
		Strategy: params.Strategy, Status: SystemUpdateStatusQueued, IdempotencyKey: params.IdempotencyKey,
		RequestedByUserID: params.RequestedByUserID, RequestedByUsername: params.RequestedByUsername,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO system_update_jobs
	(id, target_id, target_service_type, agent_service_id, execution_host_id, deployment_mode, current_version, target_version, strategy, status, idempotency_key, requested_by_user_id, requested_by_username, sequence, progress, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		job.ID, job.TargetID, job.TargetServiceType, job.AgentServiceID, job.ExecutionHostID, job.DeploymentMode, job.CurrentVersion, job.TargetVersion,
		job.Strategy, job.Status, job.IdempotencyKey, job.RequestedByUserID, job.RequestedByUsername, now, now)
	if err == nil {
		return job, true, nil
	}
	if !isDuplicateKeyError(err) {
		return SystemUpdateJob{}, false, err
	}
	if existing, getErr := s.getSystemUpdateByIdempotency(ctx, params.RequestedByUserID, params.IdempotencyKey); getErr == nil {
		if sameSystemUpdateRequest(existing, params) {
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	if _, getErr := s.getActiveSystemUpdateForTarget(ctx, params.TargetID); getErr == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
	}
	return SystemUpdateJob{}, false, err
}

func (s *MariaDBSystemUpdateStore) CancelSystemUpdateJob(ctx context.Context, id, actorUserID string) (SystemUpdateJob, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(actorUserID) == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	defer tx.Rollback()
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
	return job, nil
}

func (s *MariaDBSystemUpdateStore) ClaimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (SystemUpdateClaim, bool, error) {
	agentServiceID = strings.TrimSpace(agentServiceID)
	executionHostID = normalizeSystemUpdateExecutionHostID(agentServiceID, executionHostID)
	activeJobID = strings.TrimSpace(activeJobID)
	targets := normalizedEligibleTargets(eligibleTargets)
	if agentServiceID == "" || !validSystemUpdateExecutionHostID(executionHostID) || (len(targets) == 0 && activeJobID == "") || leaseTTL <= 0 || len(activeJobID) > 64 || containsControl(activeJobID) {
		return SystemUpdateClaim{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	defer tx.Rollback()

	var job SystemUpdateJob
	if activeJobID != "" {
		job, err = scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, activeJobID))
		if errors.Is(err, sql.ErrNoRows) {
			return SystemUpdateClaim{}, true, nil
		}
		if err != nil {
			return SystemUpdateClaim{}, false, err
		}
		if job.AgentServiceID != agentServiceID || !isExecutingSystemUpdateStatus(job.Status) {
			return SystemUpdateClaim{}, true, nil
		}
		if job.ExecutionHostID != executionHostID {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
		mode, authorized := targets[job.TargetID]
		if !authorized || mode != job.DeploymentMode {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
	} else {
		job, err = findExecutingSystemUpdateForAgentHost(ctx, tx, agentServiceID, executionHostID)
		if err == nil {
			mode, eligible := targets[job.TargetID]
			if job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) || !eligible || mode != job.DeploymentMode {
				return SystemUpdateClaim{}, false, ErrNotFound
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			job, err = findClaimableSystemUpdate(ctx, tx, agentServiceID, executionHostID, targets, now, false)
		}
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
	leaseToken, err := newSystemUpdateLeaseToken()
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	leaseExpiresAt := now.Add(leaseTTL)
	claimedAt := job.ClaimedAt
	lastStatus := job.Status
	recoveryRequired := lastStatus != SystemUpdateStatusQueued
	status := SystemUpdateStatusReconciling
	if !recoveryRequired {
		status = SystemUpdateStatusClaimed
		claimedAt = &now
	}
	leaseGeneration := job.LeaseGeneration + 1
	result, err := tx.ExecContext(ctx, `UPDATE system_update_jobs SET status = ?, agent_service_id = ?, lease_generation = ?, lease_token_hash = ?, lease_expires_at = ?, claimed_at = ?, updated_at = ? WHERE id = ?`, status, agentServiceID, leaseGeneration, security.HashToken(leaseToken), leaseExpiresAt, claimedAt, now, job.ID)
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
	job.LeaseExpiresAt = &leaseExpiresAt
	job.ClaimedAt = claimedAt
	job.UpdatedAt = now
	return SystemUpdateClaim{Job: job, LeaseToken: leaseToken, LeaseExpiresAt: leaseExpiresAt, LeaseGeneration: leaseGeneration, ReportSequence: job.Sequence + 1, RecoveryRequired: recoveryRequired, LastStatus: lastStatus}, false, nil
}

func (s *MariaDBSystemUpdateStore) ReportSystemUpdateJob(ctx context.Context, id string, report SystemUpdateReport, now time.Time, leaseTTL time.Duration) (SystemUpdateJob, bool, error) {
	id = strings.TrimSpace(id)
	report = normalizeSystemUpdateReport(report)
	if id == "" || leaseTTL <= 0 || validateSystemUpdateReport(report) != nil {
		return SystemUpdateJob{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	defer tx.Rollback()
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if isTerminalSystemUpdateStatus(job.Status) {
		if job.AgentServiceID != report.AgentServiceID || job.LeaseGeneration != report.LeaseGeneration || !security.VerifyTokenHash(report.LeaseToken, job.leaseTokenHash) {
			return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
		}
		if report.Sequence != job.Sequence || !sameSystemUpdateReport(job, report) {
			return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
		}
		return job, false, nil
	}
	if job.AgentServiceID != report.AgentServiceID || job.LeaseGeneration != report.LeaseGeneration || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || !security.VerifyTokenHash(report.LeaseToken, job.leaseTokenHash) {
		return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
	}
	if report.Sequence < job.Sequence || report.Sequence > job.Sequence+1 || (report.Sequence == job.Sequence && !sameSystemUpdateReport(job, report)) {
		return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
	}
	if report.Sequence == job.Sequence {
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
	if !allowedSystemUpdateTransition(job.Status, report.Status) || report.Progress < job.Progress {
		return SystemUpdateJob{}, false, ErrSystemUpdateTransition
	}
	terminal := isTerminalSystemUpdateStatus(report.Status)
	var leaseExpires any
	var completedAt any
	if terminal {
		leaseExpires = nil
		completedAt = now
	} else {
		leaseExpires = now.Add(leaseTTL)
	}
	_, err = tx.ExecContext(ctx, `UPDATE system_update_jobs SET status = ?, sequence = ?, progress = ?, code = ?, message = ?, artifact_digest = ?, previous_digest = ?, lease_expires_at = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		report.Status, report.Sequence, report.Progress, report.Code, report.Message, report.ArtifactDigest, report.PreviousDigest, leaseExpires, completedAt, now, id)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	job.Status = report.Status
	job.Sequence = report.Sequence
	job.Progress = report.Progress
	job.Code = report.Code
	job.Message = report.Message
	job.ArtifactDigest = report.ArtifactDigest
	job.PreviousDigest = report.PreviousDigest
	job.UpdatedAt = now
	if terminal {
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
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return authorizeSystemUpdateMutation(job, authorization, now.UTC())
}

func (s *MariaDBSystemUpdateStore) HasActiveSystemUpdateReference(ctx context.Context, serviceID string) (bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrInvalidSystemUpdate
	}
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM system_update_jobs
  WHERE active_target_id IS NOT NULL AND (target_id = ? OR agent_service_id = ?)
)`, serviceID, serviceID).Scan(&active)
	return active, err
}

const systemUpdateSelect = `SELECT id, target_id, target_service_type, deployment_mode, current_version, target_version, strategy, status, idempotency_key, requested_by_user_id, requested_by_username, COALESCE(agent_service_id, ''), execution_host_id, lease_generation, COALESCE(lease_token_hash, ''), lease_expires_at, sequence, progress, COALESCE(code, ''), COALESCE(message, ''), COALESCE(artifact_digest, ''), COALESCE(previous_digest, ''), created_at, updated_at, claimed_at, completed_at, cancelled_at FROM system_update_jobs`

type systemUpdateScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateJob(row systemUpdateScanner) (SystemUpdateJob, error) {
	var job SystemUpdateJob
	var leaseExpiresAt, claimedAt, completedAt, cancelledAt sql.NullTime
	err := row.Scan(&job.ID, &job.TargetID, &job.TargetServiceType, &job.DeploymentMode, &job.CurrentVersion, &job.TargetVersion, &job.Strategy, &job.Status, &job.IdempotencyKey, &job.RequestedByUserID, &job.RequestedByUsername, &job.AgentServiceID, &job.ExecutionHostID, &job.LeaseGeneration, &job.leaseTokenHash, &leaseExpiresAt, &job.Sequence, &job.Progress, &job.Code, &job.Message, &job.ArtifactDigest, &job.PreviousDigest, &job.CreatedAt, &job.UpdatedAt, &claimedAt, &completedAt, &cancelledAt)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if claimedAt.Valid {
		job.ClaimedAt = &claimedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if cancelledAt.Valid {
		job.CancelledAt = &cancelledAt.Time
	}
	return job, nil
}

func (s *MariaDBSystemUpdateStore) getSystemUpdateByIdempotency(ctx context.Context, userID, key string) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ?`, userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func (s *MariaDBSystemUpdateStore) getActiveSystemUpdateForTarget(ctx context.Context, targetID string) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE active_target_id = ?`, targetID))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func findClaimableSystemUpdate(ctx context.Context, tx *sql.Tx, agentID, executionHostID string, targets map[string]string, now time.Time, expired bool) (SystemUpdateJob, error) {
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2+5)
	query := systemUpdateSelect + ` WHERE `
	if expired {
		query += `status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') AND lease_expires_at <= ?`
		args = append(args, now)
		if executionHostID != "" {
			query += ` AND execution_host_id = ?`
			args = append(args, executionHostID)
		}
		query += ` AND target_id IN (` + placeholders + `)`
	} else {
		query += `status = 'queued' AND execution_host_id = ? AND target_id IN (` + placeholders + `)`
		args = append(args, executionHostID)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	query += ` AND (`
	for i, id := range ids {
		if i > 0 {
			query += ` OR `
		}
		query += `(target_id = ? AND deployment_mode = ?)`
		args = append(args, id, targets[id])
	}
	query += `)`
	if agentID != "" {
		query += ` AND agent_service_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY created_at ASC LIMIT 1 FOR UPDATE`
	return scanSystemUpdateJob(tx.QueryRowContext(ctx, query, args...))
}

func findExecutingSystemUpdateForAgentHost(ctx context.Context, tx *sql.Tx, agentID, executionHostID string) (SystemUpdateJob, error) {
	return scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') AND agent_service_id = ? AND execution_host_id = ? ORDER BY created_at ASC LIMIT 1 FOR UPDATE`, agentID, executionHostID))
}

func newSystemUpdateLeaseToken() (string, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", err
	}
	return "ast_update_" + raw, nil
}

func normalizeSystemUpdateCreate(params CreateSystemUpdateJobParams) CreateSystemUpdateJobParams {
	params.TargetID = strings.TrimSpace(params.TargetID)
	params.TargetServiceType = strings.TrimSpace(params.TargetServiceType)
	params.AgentServiceID = strings.TrimSpace(params.AgentServiceID)
	params.ExecutionHostID = normalizeSystemUpdateExecutionHostID(params.AgentServiceID, params.ExecutionHostID)
	params.DeploymentMode = strings.ToLower(strings.TrimSpace(params.DeploymentMode))
	params.CurrentVersion = strings.TrimSpace(params.CurrentVersion)
	params.TargetVersion = strings.TrimSpace(params.TargetVersion)
	params.Strategy = strings.ToLower(strings.TrimSpace(params.Strategy))
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	return params
}

func normalizeSystemUpdateAuthorization(authorization SystemUpdateAuthorization) SystemUpdateAuthorization {
	authorization.AgentServiceID = strings.TrimSpace(authorization.AgentServiceID)
	authorization.ExecutionHostID = normalizeSystemUpdateExecutionHostID(authorization.AgentServiceID, authorization.ExecutionHostID)
	authorization.LeaseToken = strings.TrimSpace(authorization.LeaseToken)
	authorization.TargetID = strings.TrimSpace(authorization.TargetID)
	authorization.TargetVersion = strings.TrimSpace(authorization.TargetVersion)
	authorization.DeploymentMode = strings.ToLower(strings.TrimSpace(authorization.DeploymentMode))
	return authorization
}

func validateSystemUpdateAuthorization(authorization SystemUpdateAuthorization) error {
	if authorization.AgentServiceID == "" || !validSystemUpdateExecutionHostID(authorization.ExecutionHostID) || authorization.LeaseToken == "" || authorization.LeaseGeneration <= 0 ||
		authorization.TargetID == "" || authorization.TargetVersion == "" || !validSystemUpdateDeploymentMode(authorization.DeploymentMode) {
		return ErrInvalidSystemUpdate
	}
	if len(authorization.AgentServiceID) > 191 || len(authorization.LeaseToken) > 256 || len(authorization.TargetID) > 191 || len(authorization.TargetVersion) > 128 ||
		containsControl(authorization.AgentServiceID) || containsControl(authorization.LeaseToken) || containsControl(authorization.TargetID) || containsControl(authorization.TargetVersion) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func authorizeSystemUpdateMutation(job SystemUpdateJob, authorization SystemUpdateAuthorization, now time.Time) error {
	if job.Status != SystemUpdateStatusInstalling && job.Status != SystemUpdateStatusReconciling {
		return ErrSystemUpdateAuthorizationState
	}
	if job.AgentServiceID != authorization.AgentServiceID || job.LeaseGeneration != authorization.LeaseGeneration ||
		job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || !security.VerifyTokenHash(authorization.LeaseToken, job.leaseTokenHash) {
		return ErrSystemUpdateLeaseInvalid
	}
	if job.ExecutionHostID != authorization.ExecutionHostID || job.TargetID != authorization.TargetID || job.TargetVersion != authorization.TargetVersion || job.DeploymentMode != authorization.DeploymentMode {
		return ErrSystemUpdateAuthorizationMismatch
	}
	return nil
}

func validateSystemUpdateCreate(params CreateSystemUpdateJobParams) error {
	if params.TargetID == "" || len(params.TargetID) > 191 || params.TargetServiceType == "" || len(params.TargetServiceType) > 64 || params.AgentServiceID == "" || len(params.AgentServiceID) > 191 || !validSystemUpdateExecutionHostID(params.ExecutionHostID) || params.CurrentVersion == "" || len(params.CurrentVersion) > 128 || params.TargetVersion == "" || len(params.TargetVersion) > 128 || params.RequestedByUserID == "" || len(params.RequestedByUserID) > 64 || params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 {
		return ErrInvalidSystemUpdate
	}
	if !systemUpdateJobVersionPattern.MatchString(params.CurrentVersion) || !systemUpdateJobVersionPattern.MatchString(params.TargetVersion) {
		return ErrInvalidSystemUpdate
	}
	if !validSystemUpdateDeploymentMode(params.DeploymentMode) {
		return ErrInvalidSystemUpdate
	}
	if params.Strategy != SystemUpdateStrategyWhenIdle && params.Strategy != SystemUpdateStrategyMaintenance {
		return ErrInvalidSystemUpdate
	}
	if containsControl(params.TargetID) || containsControl(params.IdempotencyKey) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func normalizeSystemUpdateExecutionHostID(agentServiceID, executionHostID string) string {
	executionHostID = strings.TrimSpace(executionHostID)
	if executionHostID == "" {
		return strings.TrimSpace(agentServiceID)
	}
	return executionHostID
}

func validSystemUpdateExecutionHostID(executionHostID string) bool {
	return executionHostID != "" && len(executionHostID) <= 191 && !containsControl(executionHostID)
}

func validSystemUpdateDeploymentMode(mode string) bool {
	return mode == "systemd" || mode == "docker"
}

func normalizeSystemUpdateReport(report SystemUpdateReport) SystemUpdateReport {
	report.AgentServiceID = strings.TrimSpace(report.AgentServiceID)
	report.LeaseToken = strings.TrimSpace(report.LeaseToken)
	report.Status = strings.ToLower(strings.TrimSpace(report.Status))
	report.Code = strings.TrimSpace(report.Code)
	report.Message = sanitizeSystemUpdateMessage(report.Message)
	report.ArtifactDigest = strings.TrimSpace(report.ArtifactDigest)
	report.PreviousDigest = strings.TrimSpace(report.PreviousDigest)
	return report
}

func validateSystemUpdateReport(report SystemUpdateReport) error {
	if report.AgentServiceID == "" || report.LeaseToken == "" || report.LeaseGeneration <= 0 || report.Sequence <= 0 || report.Progress < 0 || report.Progress > 100 || !validSystemUpdateCode(report.Code) || len(report.Message) > 500 || !validSystemUpdateDigest(report.ArtifactDigest) || !validSystemUpdateDigest(report.PreviousDigest) || !isReportableSystemUpdateStatus(report.Status) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func validSystemUpdateCode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func validSystemUpdateDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func normalizedEligibleTargets(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for id, mode := range input {
		id = strings.TrimSpace(id)
		mode = strings.ToLower(strings.TrimSpace(mode))
		if id != "" && len(id) <= 191 && (mode == "systemd" || mode == "docker") {
			out[id] = mode
		}
	}
	return out
}

func sameSystemUpdateRequest(job SystemUpdateJob, params CreateSystemUpdateJobParams) bool {
	return job.TargetID == params.TargetID && job.Strategy == params.Strategy
}

func sameSystemUpdateReport(job SystemUpdateJob, report SystemUpdateReport) bool {
	return job.Status == report.Status && job.Progress == report.Progress && job.Code == report.Code && job.Message == report.Message && job.ArtifactDigest == report.ArtifactDigest && job.PreviousDigest == report.PreviousDigest
}

func allowedSystemUpdateTransition(current, next string) bool {
	if current == next {
		return !isTerminalSystemUpdateStatus(current)
	}
	if isTerminalSystemUpdateStatus(current) {
		return false
	}
	if current == SystemUpdateStatusReconciling {
		return next == SystemUpdateStatusSucceeded || next == SystemUpdateStatusRolledBack || next == SystemUpdateStatusFailed
	}
	if next == SystemUpdateStatusReconciling {
		return current == SystemUpdateStatusInstalling || current == SystemUpdateStatusStarting || current == SystemUpdateStatusHealthChecking || current == SystemUpdateStatusRollingBack
	}
	if next == SystemUpdateStatusFailed {
		return true
	}
	if current == SystemUpdateStatusRollingBack {
		return next == SystemUpdateStatusRolledBack
	}
	if next == SystemUpdateStatusRollingBack {
		return systemUpdateForwardRank(current) >= systemUpdateForwardRank(SystemUpdateStatusStopping)
	}
	currentRank := systemUpdateForwardRank(current)
	nextRank := systemUpdateForwardRank(next)
	return currentRank >= 0 && nextRank > currentRank
}

func systemUpdateForwardRank(status string) int {
	order := map[string]int{
		SystemUpdateStatusClaimed: 0, SystemUpdateStatusDownloading: 1, SystemUpdateStatusVerifying: 2,
		SystemUpdateStatusStaging: 3, SystemUpdateStatusStopping: 4, SystemUpdateStatusInstalling: 5,
		SystemUpdateStatusStarting: 6, SystemUpdateStatusHealthChecking: 7, SystemUpdateStatusSucceeded: 8,
	}
	if rank, ok := order[status]; ok {
		return rank
	}
	return -1
}

func isReportableSystemUpdateStatus(status string) bool {
	switch status {
	case SystemUpdateStatusClaimed, SystemUpdateStatusDownloading, SystemUpdateStatusVerifying, SystemUpdateStatusStaging, SystemUpdateStatusStopping, SystemUpdateStatusInstalling, SystemUpdateStatusStarting, SystemUpdateStatusHealthChecking, SystemUpdateStatusReconciling, SystemUpdateStatusSucceeded, SystemUpdateStatusRollingBack, SystemUpdateStatusRolledBack, SystemUpdateStatusFailed:
		return true
	default:
		return false
	}
}

func isTerminalSystemUpdateStatus(status string) bool {
	return status == SystemUpdateStatusSucceeded || status == SystemUpdateStatusRolledBack || status == SystemUpdateStatusFailed || status == SystemUpdateStatusCancelled
}

func isExecutingSystemUpdateStatus(status string) bool {
	switch status {
	case SystemUpdateStatusClaimed, SystemUpdateStatusDownloading, SystemUpdateStatusVerifying, SystemUpdateStatusStaging, SystemUpdateStatusStopping, SystemUpdateStatusInstalling, SystemUpdateStatusStarting, SystemUpdateStatusHealthChecking, SystemUpdateStatusRollingBack, SystemUpdateStatusReconciling:
		return true
	default:
		return false
	}
}

func sanitizeSystemUpdateMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, message)
	fields := strings.Fields(message)
	redactNext := false
	for i, field := range fields {
		if redactNext {
			fields[i] = "[REDACTED]"
			redactNext = false
			continue
		}
		lower := strings.ToLower(field)
		label := strings.Trim(lower, "[](){}<>,;")
		if strings.EqualFold(label, "bearer") || label == "authorization:" || label == "token:" || label == "access_token:" || label == "secret:" || label == "password:" {
			redactNext = true
		}
		if strings.HasPrefix(field, "ast_svc_") || strings.HasPrefix(field, "ast_update_") || strings.HasPrefix(field, "ghp_") || strings.HasPrefix(lower, "github_pat_") || strings.Contains(lower, "authorization:") || strings.Contains(lower, "access_token=") || strings.Contains(lower, "token=") || strings.Contains(lower, "secret=") || strings.Contains(lower, "password=") || (strings.Contains(field, "://") && strings.Contains(field, "@")) {
			fields[i] = "[REDACTED]"
		}
	}
	message = strings.Join(fields, " ")
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

var _ SystemUpdateStore = (*MariaDBSystemUpdateStore)(nil)
