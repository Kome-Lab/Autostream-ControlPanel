package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

const SystemUpdateMutationGrantMaxTTL = 90 * time.Second

const (
	SystemUpdateMutationOperationApply     = "apply"
	SystemUpdateMutationOperationReconcile = "reconcile"
)

var (
	ErrSystemUpdateMutationGrantConflict = errors.New("system update mutation grant is invalid, expired, or conflicts with the current job")

	systemUpdateMutationPlanPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	systemUpdateMutationSessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
	systemUpdateMutationTokenPattern   = regexp.MustCompile(`^ast_mutation_[A-Za-z0-9_-]{43}$`)
)

// SystemUpdateMutationGrantBinding is the immutable remote execution identity.
// It deliberately contains no path, command, URL, or credential selected by a
// job. Privileged target details remain in the remote host's root-owned config.
type SystemUpdateMutationGrantBinding struct {
	HostID         string
	TargetID       string
	TargetVersion  string
	DeploymentMode string
	Operation      string
	PlanSHA256     string
	SessionID      string
}

type IssueSystemUpdateMutationGrantParams struct {
	AgentServiceID  string
	LeaseToken      string
	LeaseGeneration int64
	Binding         SystemUpdateMutationGrantBinding
}

type SystemUpdateMutationGrant struct {
	ID              string
	JobID           string
	AgentServiceID  string
	LeaseGeneration int64
	Binding         SystemUpdateMutationGrantBinding
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time

	tokenHash string
}

type IssuedSystemUpdateMutationGrant struct {
	Grant      SystemUpdateMutationGrant
	GrantToken string
}

type SystemUpdateMutationGrantStore interface {
	IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error)
	ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (grant SystemUpdateMutationGrant, replayed bool, err error)
}

func (s *MemorySystemUpdateStore) IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error) {
	if err := ctx.Err(); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	jobID = strings.TrimSpace(jobID)
	params = normalizeSystemUpdateMutationGrantIssue(params)
	if jobID == "" || validateSystemUpdateMutationGrantIssue(params) != nil || ttl <= 0 {
		return IssuedSystemUpdateMutationGrant{}, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	ttl = boundedSystemUpdateMutationGrantTTL(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return IssuedSystemUpdateMutationGrant{}, ErrNotFound
	}
	if err := authorizeSystemUpdateMutationGrantIssue(job, params, now); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	rawToken, err := newSystemUpdateMutationGrantToken()
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	expiresAt := now.Add(ttl)
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = job.LeaseExpiresAt.UTC()
	}
	grant := SystemUpdateMutationGrant{
		ID: newUUID(), JobID: job.ID, AgentServiceID: params.AgentServiceID,
		LeaseGeneration: params.LeaseGeneration, Binding: params.Binding,
		ExpiresAt: expiresAt, CreatedAt: now, tokenHash: security.HashToken(rawToken),
	}
	if s.mutationGrants == nil {
		s.mutationGrants = map[string]SystemUpdateMutationGrant{}
	}
	if _, exists := s.mutationGrants[grant.tokenHash]; exists {
		return IssuedSystemUpdateMutationGrant{}, errors.New("generate unique system update mutation grant")
	}
	s.mutationGrants[grant.tokenHash] = grant
	return IssuedSystemUpdateMutationGrant{Grant: publicSystemUpdateMutationGrant(grant), GrantToken: rawToken}, nil
}

func (s *MemorySystemUpdateStore) ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (SystemUpdateMutationGrant, bool, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	jobID = strings.TrimSpace(jobID)
	grantToken = strings.TrimSpace(grantToken)
	binding = normalizeSystemUpdateMutationGrantBinding(binding)
	if jobID == "" || leaseGeneration <= 0 || !systemUpdateMutationTokenPattern.MatchString(grantToken) || validateSystemUpdateMutationGrantBinding(binding) != nil {
		return SystemUpdateMutationGrant{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.mutationGrants[security.HashToken(grantToken)]
	if !ok || grant.JobID != jobID || grant.LeaseGeneration != leaseGeneration || !sameSystemUpdateMutationGrantBinding(grant.Binding, binding) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if !grant.ExpiresAt.After(now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	job, ok := s.jobs[jobID]
	if !ok || !systemUpdateMutationGrantMatchesCurrentJob(grant, job, now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if grant.ConsumedAt != nil {
		return publicSystemUpdateMutationGrant(grant), true, nil
	}
	consumedAt := now
	grant.ConsumedAt = &consumedAt
	s.mutationGrants[grant.tokenHash] = grant
	return publicSystemUpdateMutationGrant(grant), false, nil
}

func (s *MariaDBSystemUpdateStore) IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error) {
	jobID = strings.TrimSpace(jobID)
	params = normalizeSystemUpdateMutationGrantIssue(params)
	if jobID == "" || validateSystemUpdateMutationGrantIssue(params) != nil || ttl <= 0 {
		return IssuedSystemUpdateMutationGrant{}, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	ttl = boundedSystemUpdateMutationGrantTTL(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	defer tx.Rollback()
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedSystemUpdateMutationGrant{}, ErrNotFound
	}
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	if err := authorizeSystemUpdateMutationGrantIssue(job, params, now); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	expiresAt := now.Add(ttl)
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = job.LeaseExpiresAt.UTC()
	}
	for attempt := 0; attempt < 3; attempt++ {
		rawToken, tokenErr := newSystemUpdateMutationGrantToken()
		if tokenErr != nil {
			return IssuedSystemUpdateMutationGrant{}, tokenErr
		}
		grant := SystemUpdateMutationGrant{
			ID: newUUID(), JobID: job.ID, AgentServiceID: params.AgentServiceID,
			LeaseGeneration: params.LeaseGeneration, Binding: params.Binding,
			ExpiresAt: expiresAt, CreatedAt: now, tokenHash: security.HashToken(rawToken),
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO system_update_mutation_grants
  (id, job_id, token_hash, agent_service_id, lease_generation, host_id, target_id, target_version, deployment_mode, operation, plan_sha256, session_id, expires_at, consumed_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
			grant.ID, grant.JobID, grant.tokenHash, grant.AgentServiceID, grant.LeaseGeneration,
			grant.Binding.HostID, grant.Binding.TargetID, grant.Binding.TargetVersion, grant.Binding.DeploymentMode,
			grant.Binding.Operation, grant.Binding.PlanSHA256, grant.Binding.SessionID, grant.ExpiresAt, grant.CreatedAt)
		if err == nil {
			if err := tx.Commit(); err != nil {
				return IssuedSystemUpdateMutationGrant{}, err
			}
			return IssuedSystemUpdateMutationGrant{Grant: publicSystemUpdateMutationGrant(grant), GrantToken: rawToken}, nil
		}
		if !isDuplicateKeyError(err) {
			return IssuedSystemUpdateMutationGrant{}, err
		}
	}
	return IssuedSystemUpdateMutationGrant{}, errors.New("generate unique system update mutation grant")
}

func (s *MariaDBSystemUpdateStore) ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (SystemUpdateMutationGrant, bool, error) {
	jobID = strings.TrimSpace(jobID)
	grantToken = strings.TrimSpace(grantToken)
	binding = normalizeSystemUpdateMutationGrantBinding(binding)
	if jobID == "" || leaseGeneration <= 0 || !systemUpdateMutationTokenPattern.MatchString(grantToken) || validateSystemUpdateMutationGrantBinding(binding) != nil {
		return SystemUpdateMutationGrant{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	defer tx.Rollback()
	grant, err := scanSystemUpdateMutationGrant(tx.QueryRowContext(ctx, systemUpdateMutationGrantSelect+` WHERE token_hash = ? FOR UPDATE`, security.HashToken(grantToken)))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	if grant.JobID != jobID || grant.LeaseGeneration != leaseGeneration || !sameSystemUpdateMutationGrantBinding(grant.Binding, binding) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if !grant.ExpiresAt.After(now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, jobID))
	if err != nil || !systemUpdateMutationGrantMatchesCurrentJob(grant, job, now) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SystemUpdateMutationGrant{}, false, err
		}
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if grant.ConsumedAt != nil {
		if err := tx.Commit(); err != nil {
			return SystemUpdateMutationGrant{}, false, err
		}
		return publicSystemUpdateMutationGrant(grant), true, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_mutation_grants SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, now, grant.ID)
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	consumedAt := now
	grant.ConsumedAt = &consumedAt
	return publicSystemUpdateMutationGrant(grant), false, nil
}

const systemUpdateMutationGrantSelect = `SELECT id, job_id, token_hash, agent_service_id, lease_generation, host_id, target_id, target_version, deployment_mode, operation, plan_sha256, session_id, expires_at, consumed_at, created_at FROM system_update_mutation_grants`

func scanSystemUpdateMutationGrant(row systemUpdateScanner) (SystemUpdateMutationGrant, error) {
	var grant SystemUpdateMutationGrant
	var consumedAt sql.NullTime
	err := row.Scan(&grant.ID, &grant.JobID, &grant.tokenHash, &grant.AgentServiceID, &grant.LeaseGeneration,
		&grant.Binding.HostID, &grant.Binding.TargetID, &grant.Binding.TargetVersion, &grant.Binding.DeploymentMode,
		&grant.Binding.Operation, &grant.Binding.PlanSHA256, &grant.Binding.SessionID, &grant.ExpiresAt, &consumedAt, &grant.CreatedAt)
	if err != nil {
		return SystemUpdateMutationGrant{}, err
	}
	if consumedAt.Valid {
		grant.ConsumedAt = &consumedAt.Time
	}
	return grant, nil
}

func normalizeSystemUpdateMutationGrantIssue(params IssueSystemUpdateMutationGrantParams) IssueSystemUpdateMutationGrantParams {
	params.AgentServiceID = strings.TrimSpace(params.AgentServiceID)
	params.LeaseToken = strings.TrimSpace(params.LeaseToken)
	params.Binding = normalizeSystemUpdateMutationGrantBinding(params.Binding)
	return params
}

func normalizeSystemUpdateMutationGrantBinding(binding SystemUpdateMutationGrantBinding) SystemUpdateMutationGrantBinding {
	binding.HostID = strings.TrimSpace(binding.HostID)
	binding.TargetID = strings.TrimSpace(binding.TargetID)
	binding.TargetVersion = strings.TrimSpace(binding.TargetVersion)
	binding.DeploymentMode = strings.ToLower(strings.TrimSpace(binding.DeploymentMode))
	binding.Operation = strings.ToLower(strings.TrimSpace(binding.Operation))
	binding.PlanSHA256 = strings.TrimSpace(binding.PlanSHA256)
	binding.SessionID = strings.TrimSpace(binding.SessionID)
	return binding
}

func validateSystemUpdateMutationGrantIssue(params IssueSystemUpdateMutationGrantParams) error {
	if params.AgentServiceID == "" || len(params.AgentServiceID) > 191 || containsControl(params.AgentServiceID) ||
		len(params.LeaseToken) < 32 || len(params.LeaseToken) > 256 || containsControl(params.LeaseToken) || params.LeaseGeneration <= 0 {
		return ErrInvalidSystemUpdate
	}
	return validateSystemUpdateMutationGrantBinding(params.Binding)
}

func validateSystemUpdateMutationGrantBinding(binding SystemUpdateMutationGrantBinding) error {
	if !validSystemUpdateExecutionHostID(binding.HostID) || binding.TargetID == "" || len(binding.TargetID) > 191 || containsControl(binding.TargetID) ||
		binding.TargetVersion == "" || len(binding.TargetVersion) > 128 || containsControl(binding.TargetVersion) ||
		!validSystemUpdateDeploymentMode(binding.DeploymentMode) ||
		(binding.Operation != SystemUpdateMutationOperationApply && binding.Operation != SystemUpdateMutationOperationReconcile) ||
		!systemUpdateMutationPlanPattern.MatchString(binding.PlanSHA256) || !systemUpdateMutationSessionPattern.MatchString(binding.SessionID) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func authorizeSystemUpdateMutationGrantIssue(job SystemUpdateJob, params IssueSystemUpdateMutationGrantParams, now time.Time) error {
	authorization := SystemUpdateAuthorization{
		AgentServiceID: params.AgentServiceID, ExecutionHostID: params.Binding.HostID,
		LeaseToken: params.LeaseToken, LeaseGeneration: params.LeaseGeneration,
		TargetID: params.Binding.TargetID, TargetVersion: params.Binding.TargetVersion,
		DeploymentMode: params.Binding.DeploymentMode,
	}
	if err := authorizeSystemUpdateMutation(job, authorization, now); err != nil {
		return err
	}
	if (params.Binding.Operation == SystemUpdateMutationOperationApply && job.Status != SystemUpdateStatusInstalling) ||
		(params.Binding.Operation == SystemUpdateMutationOperationReconcile && job.Status != SystemUpdateStatusReconciling) {
		return ErrSystemUpdateAuthorizationState
	}
	return nil
}

func systemUpdateMutationGrantMatchesCurrentJob(grant SystemUpdateMutationGrant, job SystemUpdateJob, now time.Time) bool {
	if job.ID != grant.JobID || job.AgentServiceID != grant.AgentServiceID || job.LeaseGeneration != grant.LeaseGeneration ||
		job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || job.ExecutionHostID != grant.Binding.HostID ||
		job.TargetID != grant.Binding.TargetID || job.TargetVersion != grant.Binding.TargetVersion || job.DeploymentMode != grant.Binding.DeploymentMode {
		return false
	}
	return (grant.Binding.Operation == SystemUpdateMutationOperationApply && job.Status == SystemUpdateStatusInstalling) ||
		(grant.Binding.Operation == SystemUpdateMutationOperationReconcile && job.Status == SystemUpdateStatusReconciling)
}

func sameSystemUpdateMutationGrantBinding(left, right SystemUpdateMutationGrantBinding) bool {
	return left == right
}

func boundedSystemUpdateMutationGrantTTL(ttl time.Duration) time.Duration {
	if ttl > SystemUpdateMutationGrantMaxTTL {
		return SystemUpdateMutationGrantMaxTTL
	}
	return ttl
}

func newSystemUpdateMutationGrantToken() (string, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", err
	}
	return "ast_mutation_" + raw, nil
}

func publicSystemUpdateMutationGrant(grant SystemUpdateMutationGrant) SystemUpdateMutationGrant {
	grant.tokenHash = ""
	return grant
}

var _ SystemUpdateMutationGrantStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateMutationGrantStore = (*MariaDBSystemUpdateStore)(nil)
