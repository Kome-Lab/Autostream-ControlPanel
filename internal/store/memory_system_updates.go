package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type MemorySystemUpdateStore struct {
	mu             sync.Mutex
	jobs           map[string]SystemUpdateJob
	mutationGrants map[string]SystemUpdateMutationGrant
}

func NewMemorySystemUpdateStore() *MemorySystemUpdateStore {
	return &MemorySystemUpdateStore{jobs: map[string]SystemUpdateJob{}, mutationGrants: map[string]SystemUpdateMutationGrant{}}
}

func (s *MemorySystemUpdateStore) ListSystemUpdateJobs(ctx context.Context, limit int) ([]SystemUpdateJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]SystemUpdateJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, publicMemorySystemUpdateJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID > jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func (s *MemorySystemUpdateStore) GetSystemUpdateJobByIdempotency(ctx context.Context, requestedByUserID, idempotencyKey string) (SystemUpdateJob, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, err
	}
	requestedByUserID = strings.TrimSpace(requestedByUserID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if requestedByUserID == "" || idempotencyKey == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		if job.RequestedByUserID == requestedByUserID && job.IdempotencyKey == idempotencyKey {
			return publicMemorySystemUpdateJob(job), nil
		}
	}
	return SystemUpdateJob{}, ErrNotFound
}

func (s *MemorySystemUpdateStore) GetActiveSystemUpdateJob(ctx context.Context, targetID string) (SystemUpdateJob, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		if job.TargetID == targetID && !isTerminalSystemUpdateStatus(job.Status) {
			return publicMemorySystemUpdateJob(job), nil
		}
	}
	return SystemUpdateJob{}, ErrNotFound
}

func (s *MemorySystemUpdateStore) CreateSystemUpdateJob(ctx context.Context, params CreateSystemUpdateJobParams) (SystemUpdateJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	params = normalizeSystemUpdateCreate(params)
	if err := validateSystemUpdateCreate(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.jobs {
		if existing.RequestedByUserID == params.RequestedByUserID && existing.IdempotencyKey == params.IdempotencyKey {
			if sameSystemUpdateRequest(existing, params) {
				return publicMemorySystemUpdateJob(existing), false, nil
			}
			return SystemUpdateJob{}, false, ErrAlreadyExists
		}
	}
	for _, existing := range s.jobs {
		if existing.TargetID == params.TargetID && !isTerminalSystemUpdateStatus(existing.Status) {
			return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
		}
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
	if s.jobs == nil {
		s.jobs = map[string]SystemUpdateJob{}
	}
	s.jobs[job.ID] = job
	return publicMemorySystemUpdateJob(job), true, nil
}

func (s *MemorySystemUpdateStore) CancelSystemUpdateJob(ctx context.Context, id, actorUserID string) (SystemUpdateJob, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(actorUserID) == "" {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return SystemUpdateJob{}, ErrNotFound
	}
	if job.Status != SystemUpdateStatusQueued {
		return SystemUpdateJob{}, ErrSystemUpdateNotCancellable
	}
	now := time.Now().UTC()
	job.Status = SystemUpdateStatusCancelled
	job.Code = "canceled_by_user"
	job.Message = "Update canceled before it was claimed."
	job.CancelledAt = &now
	job.CompletedAt = &now
	job.UpdatedAt = now
	s.jobs[id] = job
	return publicMemorySystemUpdateJob(job), nil
}

func (s *MemorySystemUpdateStore) ClaimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (SystemUpdateClaim, bool, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateClaim{}, false, err
	}
	agentServiceID = strings.TrimSpace(agentServiceID)
	executionHostID = normalizeSystemUpdateExecutionHostID(agentServiceID, executionHostID)
	activeJobID = strings.TrimSpace(activeJobID)
	targets := normalizedEligibleTargets(eligibleTargets)
	if agentServiceID == "" || !validSystemUpdateExecutionHostID(executionHostID) || (len(targets) == 0 && activeJobID == "") || leaseTTL <= 0 || len(activeJobID) > 64 || containsControl(activeJobID) {
		return SystemUpdateClaim{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected *SystemUpdateJob
	foreignExpired := false
	if activeJobID != "" {
		job, ok := s.jobs[activeJobID]
		if !ok || job.AgentServiceID != agentServiceID || !isExecutingSystemUpdateStatus(job.Status) {
			return SystemUpdateClaim{}, true, nil
		}
		if job.ExecutionHostID != executionHostID {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
		mode, authorized := targets[job.TargetID]
		if !authorized || mode != job.DeploymentMode {
			return SystemUpdateClaim{}, false, ErrSystemUpdateActiveUnavailable
		}
		selected = &job
	}
	executing := make([]SystemUpdateJob, 0)
	if selected == nil {
		for _, job := range s.jobs {
			if job.AgentServiceID == agentServiceID && job.ExecutionHostID == executionHostID && isExecutingSystemUpdateStatus(job.Status) {
				executing = append(executing, job)
			}
		}
		sortSystemUpdateJobsOldestFirst(executing)
		if len(executing) > 0 {
			job := executing[0]
			mode, eligible := targets[job.TargetID]
			if job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) || !eligible || mode != job.DeploymentMode {
				return SystemUpdateClaim{}, false, ErrNotFound
			}
			selected = &job
		} else {
			queued := make([]SystemUpdateJob, 0)
			for _, job := range s.jobs {
				mode, eligible := targets[job.TargetID]
				if eligible && mode == job.DeploymentMode && job.Status == SystemUpdateStatusQueued && job.AgentServiceID == agentServiceID && job.ExecutionHostID == executionHostID {
					queued = append(queued, job)
				}
			}
			sortSystemUpdateJobsOldestFirst(queued)
			if len(queued) > 0 {
				job := queued[0]
				selected = &job
			}
		}
	}
	if selected == nil {
		for _, job := range s.jobs {
			mode, eligible := targets[job.TargetID]
			if eligible && mode == job.DeploymentMode && isExecutingSystemUpdateStatus(job.Status) && job.AgentServiceID != agentServiceID && job.LeaseExpiresAt != nil && !job.LeaseExpiresAt.After(now) {
				foreignExpired = true
				break
			}
		}
		if foreignExpired {
			return SystemUpdateClaim{}, false, ErrSystemUpdateTakeoverForbidden
		}
		return SystemUpdateClaim{}, false, ErrNotFound
	}
	leaseToken, err := newSystemUpdateLeaseToken()
	if err != nil {
		return SystemUpdateClaim{}, false, err
	}
	job := *selected
	lastStatus := job.Status
	recoveryRequired := lastStatus != SystemUpdateStatusQueued
	if !recoveryRequired {
		job.Status = SystemUpdateStatusClaimed
		job.ClaimedAt = &now
	} else {
		job.Status = SystemUpdateStatusReconciling
	}
	expiresAt := now.Add(leaseTTL)
	job.AgentServiceID = agentServiceID
	job.LeaseGeneration++
	job.leaseTokenHash = security.HashToken(leaseToken)
	job.LeaseExpiresAt = &expiresAt
	job.UpdatedAt = now
	s.jobs[job.ID] = job
	return SystemUpdateClaim{Job: publicMemorySystemUpdateJob(job), LeaseToken: leaseToken, LeaseExpiresAt: expiresAt, LeaseGeneration: job.LeaseGeneration, ReportSequence: job.Sequence + 1, RecoveryRequired: recoveryRequired, LastStatus: lastStatus}, false, nil
}

func sortSystemUpdateJobsOldestFirst(jobs []SystemUpdateJob) {
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
}

func (s *MemorySystemUpdateStore) ReportSystemUpdateJob(ctx context.Context, id string, report SystemUpdateReport, now time.Time, leaseTTL time.Duration) (SystemUpdateJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	id = strings.TrimSpace(id)
	report = normalizeSystemUpdateReport(report)
	if id == "" || leaseTTL <= 0 || validateSystemUpdateReport(report) != nil {
		return SystemUpdateJob{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return SystemUpdateJob{}, false, ErrNotFound
	}
	if isTerminalSystemUpdateStatus(job.Status) {
		if job.AgentServiceID != report.AgentServiceID || job.LeaseGeneration != report.LeaseGeneration || !security.VerifyTokenHash(report.LeaseToken, job.leaseTokenHash) {
			return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
		}
		if report.Sequence != job.Sequence || !sameSystemUpdateReport(job, report) {
			return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
		}
		return publicMemorySystemUpdateJob(job), false, nil
	}
	if job.AgentServiceID != report.AgentServiceID || job.LeaseGeneration != report.LeaseGeneration || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || !security.VerifyTokenHash(report.LeaseToken, job.leaseTokenHash) {
		return SystemUpdateJob{}, false, ErrSystemUpdateLeaseInvalid
	}
	if report.Sequence < job.Sequence || report.Sequence > job.Sequence+1 || (report.Sequence == job.Sequence && !sameSystemUpdateReport(job, report)) {
		return SystemUpdateJob{}, false, ErrSystemUpdateSequenceStale
	}
	if report.Sequence == job.Sequence {
		expiresAt := now.Add(leaseTTL)
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		s.jobs[id] = job
		return publicMemorySystemUpdateJob(job), false, nil
	}
	if !allowedSystemUpdateTransition(job.Status, report.Status) || report.Progress < job.Progress {
		return SystemUpdateJob{}, false, ErrSystemUpdateTransition
	}
	job.Status = report.Status
	job.Sequence = report.Sequence
	job.Progress = report.Progress
	job.Code = report.Code
	job.Message = report.Message
	job.ArtifactDigest = report.ArtifactDigest
	job.PreviousDigest = report.PreviousDigest
	job.UpdatedAt = now
	if isTerminalSystemUpdateStatus(job.Status) {
		job.LeaseExpiresAt = nil
		job.CompletedAt = &now
	} else {
		expiresAt := now.Add(leaseTTL)
		job.LeaseExpiresAt = &expiresAt
	}
	s.jobs[id] = job
	return publicMemorySystemUpdateJob(job), true, nil
}

func (s *MemorySystemUpdateStore) AuthorizeSystemUpdateMutation(ctx context.Context, id string, authorization SystemUpdateAuthorization, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	authorization = normalizeSystemUpdateAuthorization(authorization)
	if id == "" || validateSystemUpdateAuthorization(authorization) != nil {
		return ErrInvalidSystemUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	return authorizeSystemUpdateMutation(job, authorization, now.UTC())
}

func (s *MemorySystemUpdateStore) HasActiveSystemUpdateReference(ctx context.Context, serviceID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrInvalidSystemUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		if !isTerminalSystemUpdateStatus(job.Status) && (job.TargetID == serviceID || job.AgentServiceID == serviceID) {
			return true, nil
		}
	}
	return false, nil
}

func publicMemorySystemUpdateJob(job SystemUpdateJob) SystemUpdateJob {
	job.leaseTokenHash = ""
	return job
}

var _ SystemUpdateStore = (*MemorySystemUpdateStore)(nil)
