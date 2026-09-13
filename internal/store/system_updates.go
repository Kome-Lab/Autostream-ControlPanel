package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
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
	ErrInvalidSystemUpdate                  = errors.New("invalid system update")
	ErrSystemUpdateTargetActive             = errors.New("system update target already has an active job")
	ErrSystemUpdateLeaseInvalid             = errors.New("system update lease is invalid or expired")
	ErrSystemUpdateSequenceStale            = errors.New("system update report sequence is stale")
	ErrSystemUpdateTransition               = errors.New("invalid system update status transition")
	ErrSystemUpdateNotCancellable           = errors.New("system update job is not cancellable")
	ErrSystemUpdateTakeoverForbidden        = errors.New("system update takeover requires explicit administrator reassignment")
	ErrSystemUpdateActiveUnavailable        = errors.New("active system update target is no longer authorized for this updater")
	ErrSystemUpdateAuthorizationState       = errors.New("system update is not in a mutation-authorizable state")
	ErrSystemUpdateAuthorizationMismatch    = errors.New("system update authorization request does not match the job")
	ErrSystemUpdateOwnershipConflict        = errors.New("system update execution host ownership conflicts with the job snapshot")
	ErrSystemUpdatePortCoordinatorRequired  = errors.New("port reconfiguration requires the transactional coordinator")
	ErrSystemUpdateRecoveryProofUnavailable = errors.New("system update recovery terminal proof is unavailable")
)

type SystemUpdateJob struct {
	ID                      string                           `json:"id"`
	TargetID                string                           `json:"target_id"`
	TargetServiceType       string                           `json:"target_type"`
	Operation               string                           `json:"operation"`
	PortReconfigure         *SystemUpdatePortReconfiguration `json:"port_reconfigure,omitempty"`
	PortResult              *SystemUpdatePortResultV2        `json:"port_result,omitempty"`
	RecoveryRequired        bool                             `json:"recovery_required,omitempty"`
	LastRecoveryObservation *SystemUpdatePortResultV2        `json:"last_recovery_observation,omitempty"`
	portTransaction         *systemUpdatePortTransaction
	DeploymentMode          string     `json:"deployment_mode"`
	CurrentVersion          string     `json:"current_version"`
	TargetVersion           string     `json:"target_version"`
	Strategy                string     `json:"strategy"`
	Status                  string     `json:"status"`
	IdempotencyKey          string     `json:"idempotency_key"`
	RequestedByUserID       string     `json:"-"`
	RequestedByUsername     string     `json:"requested_by,omitempty"`
	AgentServiceID          string     `json:"updater_id,omitempty"`
	ExecutionHostID         string     `json:"host_id"`
	TransportMode           string     `json:"transport_mode"`
	OwnershipEpoch          int64      `json:"ownership_epoch"`
	PolicyRevision          int64      `json:"policy_revision"`
	LeaseGeneration         int64      `json:"lease_generation"`
	LeaseExpiresAt          *time.Time `json:"lease_expires_at,omitempty"`
	Sequence                int64      `json:"sequence"`
	Progress                int        `json:"progress"`
	Code                    string     `json:"code,omitempty"`
	Message                 string     `json:"message,omitempty"`
	ArtifactDigest          string     `json:"artifact_digest,omitempty"`
	PreviousDigest          string     `json:"previous_digest,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	ClaimedAt               *time.Time `json:"claimed_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	CancelledAt             *time.Time `json:"canceled_at,omitempty"`

	leaseTokenHash string
}

type CreateSystemUpdateJobParams struct {
	TargetID            string
	TargetServiceType   string
	Operation           string
	PortReconfigure     *SystemUpdatePortReconfiguration
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
	PortResult      *SystemUpdatePortResultV2
	ProtocolVersion int
	AgentServiceID  string
	ExecutionHostID string
	LeaseToken      string
	LeaseGeneration int64
	DesiredRevision int64
	Fence           int64
	Sequence        int64
	Status          string
	Progress        int
	Code            string
	Message         string
	ArtifactDigest  string
	PreviousDigest  string
	PortReconfigure *SystemUpdatePortReconfiguration
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
	InspectSystemUpdateActiveJob(ctx context.Context, agentServiceID, activeJobID string) (SystemUpdateJob, bool, error)
	CreateSystemUpdateJob(ctx context.Context, params CreateSystemUpdateJobParams) (job SystemUpdateJob, created bool, err error)
	CancelSystemUpdateJob(ctx context.Context, id, actorUserID string) (SystemUpdateJob, error)
	ClaimSystemUpdateJob(ctx context.Context, agentServiceID, executionHostID, activeJobID string, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (claim SystemUpdateClaim, clearActiveJob bool, err error)
	ReportSystemUpdateJob(ctx context.Context, id string, report SystemUpdateReport, now time.Time, leaseTTL time.Duration) (job SystemUpdateJob, applied bool, err error)
	AuthorizeSystemUpdateMutation(ctx context.Context, id string, authorization SystemUpdateAuthorization, now time.Time) error
	HasActiveSystemUpdateReference(ctx context.Context, serviceID string) (bool, error)
}

// SystemUpdateV2Store is the durable adapter seam for the independent
// Updater protocol. V2 report sequences are scoped to one lease generation,
// so a v2 claim atomically resets the persisted sequence before returning the
// new lease. The request's generation and ownership fence are checked inside
// the same transaction as the claim so a stale updater cannot advance either
// authority.
type SystemUpdateV2Store interface {
	GetSystemUpdateJob(ctx context.Context, id string) (SystemUpdateJob, error)
	ClaimSystemUpdateJobV2(ctx context.Context, agentServiceID, executionHostID, activeJobID string, expectedLeaseGeneration, expectedFence int64, eligibleTargets map[string]string, now time.Time, leaseTTL time.Duration) (claim SystemUpdateClaim, clearActiveJob bool, err error)
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdateJob(ctx context.Context, id string) (SystemUpdateJob, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 64 || containsControl(id) {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

type SystemUpdateIdentityMutationFenceStore interface {
	HasSystemUpdateIdentityMutationFence(
		ctx context.Context,
		services ServiceRegistryStore,
		serviceID string,
	) (bool, error)
	IsSystemUpdateEmergencyIdentityRecovery(
		ctx context.Context,
		services ServiceRegistryStore,
		serviceID string,
	) (bool, error)
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

func (s *MariaDBSystemUpdateStore) InspectSystemUpdateActiveJob(ctx context.Context, agentServiceID, activeJobID string) (SystemUpdateJob, bool, error) {
	agentServiceID = strings.TrimSpace(agentServiceID)
	activeJobID = strings.TrimSpace(activeJobID)
	if agentServiceID == "" || activeJobID == "" || len(activeJobID) > 64 || containsControl(activeJobID) {
		return SystemUpdateJob{}, false, ErrInvalidSystemUpdate
	}
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ?`, activeJobID))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, ErrSystemUpdateRecoveryProofUnavailable
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if job.AgentServiceID != agentServiceID {
		return SystemUpdateJob{}, false, ErrSystemUpdateOwnershipConflict
	}
	if isExecutingSystemUpdateStatus(job.Status) || systemUpdatePortV2Recoverable(job) {
		return job, false, nil
	}
	if !isTerminalSystemUpdateStatus(job.Status) {
		return SystemUpdateJob{}, false, ErrSystemUpdateRecoveryProofUnavailable
	}
	return job, true, nil
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

var _ SystemUpdateStore = (*MariaDBSystemUpdateStore)(nil)
