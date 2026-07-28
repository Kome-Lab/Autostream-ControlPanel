package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	SystemUpdateHostSelfUpdateQueued          = "queued"
	SystemUpdateHostSelfUpdateStaging         = "staging"
	SystemUpdateHostSelfUpdateActivating      = "activating"
	SystemUpdateHostSelfUpdateVerifying       = "verifying"
	SystemUpdateHostSelfUpdateRollingBack     = "rolling_back"
	SystemUpdateHostSelfUpdateCancelRequested = "cancel_requested"
	SystemUpdateHostSelfUpdateSucceeded       = "succeeded"
	SystemUpdateHostSelfUpdateRolledBack      = "rolled_back"
	SystemUpdateHostSelfUpdateFailed          = "failed"
	SystemUpdateHostSelfUpdateCanceled        = "canceled"

	SystemUpdateHostSelfUpdateObservationKnown   = "known"
	SystemUpdateHostSelfUpdateObservationStalled = "stalled"
	SystemUpdateHostSelfUpdateObservationUnknown = "unknown"

	SystemUpdateHostSelfUpdateGrantStage     = "stage"
	SystemUpdateHostSelfUpdateGrantReconcile = "reconcile"

	// These are the minimum protocols the current self-update control plane
	// knows how to drive on the already-running host runtime. They are
	// intentionally independent from the target release protocols so a
	// compatible current runtime can install a future protocol generation.
	SystemUpdateHostSelfUpdateMinimumAgentProtocolVersion    = 2
	SystemUpdateHostSelfUpdateMinimumExecutorProtocolVersion = 2
	SystemUpdateHostSelfUpdateMinimumMutationProtocolVersion = 2
	SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion = 2
)

var (
	ErrInvalidSystemUpdateHostSelfUpdate  = errors.New("invalid host self-update")
	ErrSystemUpdateHostSelfUpdateBusy     = errors.New("host self-update is already active")
	ErrSystemUpdateHostSelfUpdateStale    = errors.New("host self-update revision or fence is stale")
	ErrSystemUpdateHostSelfUpdateState    = errors.New("invalid host self-update transition")
	ErrSystemUpdateHostSelfUpdateCancel   = errors.New("host self-update cannot be canceled")
	ErrSystemUpdateHostSelfUpdateStore    = errors.New("host self-update stores do not share persistence")
	ErrSystemUpdateHostSelfUpdateGrant    = errors.New("host self-update grant is invalid")
	ErrSystemUpdateHostSelfUpdateExpired  = errors.New("host self-update grant expired")
	ErrSystemUpdateHostSelfUpdateConsumed = errors.New("host self-update grant was already consumed")

	systemUpdateHostSelfUpdateCommitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	systemUpdateHostSelfUpdateDigestPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	systemUpdateHostSelfUpdatePolicyDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	systemUpdateHostSelfUpdateAssetPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,254}$`)
)

// SystemUpdateHostReleaseMetadata contains only immutable, public release
// identities. Download URLs and release credentials are intentionally absent.
type SystemUpdateHostReleaseMetadata struct {
	Tag                     string    `json:"tag"`
	Commit                  string    `json:"commit"`
	PublishedAt             time.Time `json:"published_at"`
	ManifestAssetID         int64     `json:"manifest_asset_id"`
	ManifestAssetName       string    `json:"manifest_asset_name"`
	ManifestSHA256          string    `json:"manifest_sha256"`
	ManifestChecksumAssetID int64     `json:"manifest_checksum_asset_id"`
	ManifestChecksumSHA256  string    `json:"manifest_checksum_sha256"`
	ArchiveAssetID          int64     `json:"archive_asset_id"`
	ArchiveAssetName        string    `json:"archive_asset_name"`
	ArchiveSize             int64     `json:"archive_size"`
	ArchiveSHA256           string    `json:"archive_sha256"`
	ArchiveChecksumAssetID  int64     `json:"archive_checksum_asset_id"`
	ArchiveChecksumSHA256   string    `json:"archive_checksum_sha256"`
	Arch                    string    `json:"arch"`
	AgentProtocolVersion    int       `json:"agent_protocol_version"`
	ExecutorProtocolVersion int       `json:"executor_protocol_version"`
	MutationProtocolVersion int       `json:"mutation_protocol_version"`
	RecoveryProtocolVersion int       `json:"recovery_protocol_version"`
	MinimumPanelVersion     string    `json:"minimum_panel_version"`
	AttestationVerifiedAt   time.Time `json:"attestation_verified_at"`
}

// SystemUpdateHostReleaseBinding is the immutable release identity carried
// through the Host Agent directive and one-time root mutation grant. The
// server-local attestation verification timestamp is deliberately excluded.
type SystemUpdateHostReleaseBinding struct {
	Tag                     string    `json:"tag"`
	Commit                  string    `json:"commit"`
	PublishedAt             time.Time `json:"published_at"`
	ManifestAssetID         int64     `json:"manifest_asset_id"`
	ManifestAssetName       string    `json:"manifest_asset_name"`
	ManifestSHA256          string    `json:"manifest_sha256"`
	ManifestChecksumAssetID int64     `json:"manifest_checksum_asset_id"`
	ManifestChecksumSHA256  string    `json:"manifest_checksum_sha256"`
	ArchiveAssetID          int64     `json:"archive_asset_id"`
	ArchiveAssetName        string    `json:"archive_asset_name"`
	ArchiveSize             int64     `json:"archive_size"`
	ArchiveSHA256           string    `json:"archive_sha256"`
	ArchiveChecksumAssetID  int64     `json:"archive_checksum_asset_id"`
	ArchiveChecksumSHA256   string    `json:"archive_checksum_sha256"`
	Arch                    string    `json:"arch"`
	AgentProtocolVersion    int       `json:"agent_protocol_version"`
	ExecutorProtocolVersion int       `json:"executor_protocol_version"`
	MutationProtocolVersion int       `json:"mutation_protocol_version"`
	RecoveryProtocolVersion int       `json:"recovery_protocol_version"`
	MinimumPanelVersion     string    `json:"minimum_panel_version"`
}

func systemUpdateHostReleaseBinding(
	release SystemUpdateHostReleaseMetadata,
) SystemUpdateHostReleaseBinding {
	return SystemUpdateHostReleaseBinding{
		Tag:                     release.Tag,
		Commit:                  release.Commit,
		PublishedAt:             release.PublishedAt,
		ManifestAssetID:         release.ManifestAssetID,
		ManifestAssetName:       release.ManifestAssetName,
		ManifestSHA256:          release.ManifestSHA256,
		ManifestChecksumAssetID: release.ManifestChecksumAssetID,
		ManifestChecksumSHA256:  release.ManifestChecksumSHA256,
		ArchiveAssetID:          release.ArchiveAssetID,
		ArchiveAssetName:        release.ArchiveAssetName,
		ArchiveSize:             release.ArchiveSize,
		ArchiveSHA256:           release.ArchiveSHA256,
		ArchiveChecksumAssetID:  release.ArchiveChecksumAssetID,
		ArchiveChecksumSHA256:   release.ArchiveChecksumSHA256,
		Arch:                    release.Arch,
		AgentProtocolVersion:    release.AgentProtocolVersion,
		ExecutorProtocolVersion: release.ExecutorProtocolVersion,
		MutationProtocolVersion: release.MutationProtocolVersion,
		RecoveryProtocolVersion: release.RecoveryProtocolVersion,
		MinimumPanelVersion:     release.MinimumPanelVersion,
	}
}

func sameSystemUpdateHostReleaseBinding(
	left, right SystemUpdateHostReleaseBinding,
) bool {
	return left.Tag == right.Tag &&
		left.Commit == right.Commit &&
		left.PublishedAt.Equal(right.PublishedAt) &&
		left.ManifestAssetID == right.ManifestAssetID &&
		left.ManifestAssetName == right.ManifestAssetName &&
		left.ManifestSHA256 == right.ManifestSHA256 &&
		left.ManifestChecksumAssetID == right.ManifestChecksumAssetID &&
		left.ManifestChecksumSHA256 == right.ManifestChecksumSHA256 &&
		left.ArchiveAssetID == right.ArchiveAssetID &&
		left.ArchiveAssetName == right.ArchiveAssetName &&
		left.ArchiveSize == right.ArchiveSize &&
		left.ArchiveSHA256 == right.ArchiveSHA256 &&
		left.ArchiveChecksumAssetID == right.ArchiveChecksumAssetID &&
		left.ArchiveChecksumSHA256 == right.ArchiveChecksumSHA256 &&
		left.Arch == right.Arch &&
		left.AgentProtocolVersion == right.AgentProtocolVersion &&
		left.ExecutorProtocolVersion == right.ExecutorProtocolVersion &&
		left.MutationProtocolVersion == right.MutationProtocolVersion &&
		left.RecoveryProtocolVersion == right.RecoveryProtocolVersion &&
		left.MinimumPanelVersion == right.MinimumPanelVersion
}

type SystemUpdateHostSelfUpdate struct {
	ID                                  string                          `json:"id"`
	ExecutionHostID                     string                          `json:"execution_host_id"`
	AgentServiceID                      string                          `json:"agent_service_id"`
	TargetVersion                       string                          `json:"target_version"`
	Status                              string                          `json:"status"`
	Revision                            int64                           `json:"revision"`
	IdempotencyKey                      string                          `json:"idempotency_key"`
	RequestedByUsername                 string                          `json:"requested_by,omitempty"`
	RetryOfID                           string                          `json:"retry_of_id,omitempty"`
	AttemptGeneration                   string                          `json:"attempt_generation"`
	ExpectedOwnershipEpoch              int64                           `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64                           `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64                           `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64                           `json:"expected_local_executor_policy_revision"`
	ExpectedLocalExecutorPolicySHA256   string                          `json:"expected_local_executor_policy_sha256"`
	PreviousAgentVersion                string                          `json:"previous_agent_version"`
	PreviousExecutorVersion             string                          `json:"previous_executor_version"`
	PreviousAgentProtocolVersion        int                             `json:"previous_agent_protocol_version"`
	PreviousExecutorProtocolVersion     int                             `json:"previous_executor_protocol_version"`
	PreviousMutationProtocolVersion     int                             `json:"previous_mutation_protocol_version"`
	PreviousRecoveryProtocolVersion     int                             `json:"previous_recovery_protocol_version"`
	Release                             SystemUpdateHostReleaseMetadata `json:"release"`
	IssuedAt                            time.Time                       `json:"issued_at"`
	ObservationState                    string                          `json:"observation_state"`
	ReportedPhase                       string                          `json:"reported_phase,omitempty"`
	LastHeartbeatAt                     *time.Time                      `json:"last_heartbeat_at,omitempty"`
	StalledSince                        *time.Time                      `json:"stalled_since,omitempty"`
	CancelRequestedAt                   *time.Time                      `json:"cancel_requested_at,omitempty"`
	StartedAt                           *time.Time                      `json:"started_at,omitempty"`
	CompletedAt                         *time.Time                      `json:"completed_at,omitempty"`
	Code                                string                          `json:"code,omitempty"`
	Message                             string                          `json:"message,omitempty"`
	CreatedAt                           time.Time                       `json:"created_at"`
	UpdatedAt                           time.Time                       `json:"updated_at"`

	requestedByUserID string
	intentSHA256      string
}

type CreateSystemUpdateHostSelfUpdateParams struct {
	ExecutionHostID     string
	TargetVersion       string
	IdempotencyKey      string
	RequestedByUserID   string
	RequestedByUsername string
	RetryOfID           string
	Release             SystemUpdateHostReleaseMetadata
	Now                 time.Time
}

type RetrySystemUpdateHostSelfUpdateParams struct {
	ID                  string
	IdempotencyKey      string
	RequestedByUserID   string
	RequestedByUsername string
	Now                 time.Time
}

type SystemUpdateHostSelfUpdateObservation struct {
	ExecutionHostID         string
	AgentServiceID          string
	ExpectedRevision        int64
	Now                     time.Time
	HeartbeatAt             time.Time
	AgentVersion            string
	AgentProtocolVersion    int
	ExecutorVersion         string
	ExecutorProtocolVersion int
	MutationProtocolVersion int
	RecoveryProtocolVersion int
	Phase                   string
	PendingGeneration       string
	FailedGeneration        string
	HeartbeatGeneration     string
	ActiveAgentVersion      string
	ActiveExecutorVersion   string
	RecoveryPending         bool
}

type SystemUpdateHostSelfUpdateGrant struct {
	ID                                  string                         `json:"id"`
	SelfUpdateID                        string                         `json:"self_update_id"`
	AttemptGeneration                   string                         `json:"attempt_generation"`
	Operation                           string                         `json:"operation"`
	ExecutionHostID                     string                         `json:"execution_host_id"`
	AgentServiceID                      string                         `json:"agent_service_id"`
	ExpectedSelfUpdateRevision          int64                          `json:"expected_self_update_revision"`
	ExpectedOwnershipEpoch              int64                          `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64                          `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64                          `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64                          `json:"expected_local_executor_policy_revision"`
	ExpectedLocalExecutorPolicySHA256   string                         `json:"expected_local_executor_policy_sha256"`
	AgentVersion                        string                         `json:"agent_version"`
	ExecutorVersion                     string                         `json:"executor_version"`
	ReleaseCommit                       string                         `json:"release_commit"`
	ArtifactSHA256                      string                         `json:"artifact_sha256"`
	AgentProtocolVersion                int                            `json:"agent_protocol_version"`
	ExecutorProtocolVersion             int                            `json:"executor_protocol_version"`
	MutationProtocolVersion             int                            `json:"mutation_protocol_version"`
	RecoveryProtocolVersion             int                            `json:"recovery_protocol_version"`
	Release                             SystemUpdateHostReleaseBinding `json:"release"`
	DirectiveIssuedAt                   time.Time                      `json:"directive_issued_at"`
	PlanSHA256                          string                         `json:"plan_sha256"`
	SessionID                           string                         `json:"session_id"`
	Revision                            int64                          `json:"revision"`
	IssuedAt                            time.Time                      `json:"issued_at"`
	ExpiresAt                           time.Time                      `json:"expires_at"`
	ConsumedAt                          *time.Time                     `json:"consumed_at,omitempty"`
	StageClaimRevision                  int64                          `json:"stage_claim_revision,omitempty"`
	StageClaimedAt                      *time.Time                     `json:"stage_claimed_at,omitempty"`
	CreatedAt                           time.Time                      `json:"created_at"`
	UpdatedAt                           time.Time                      `json:"updated_at"`

	tokenHash string
}

type IssueSystemUpdateHostSelfUpdateGrantParams struct {
	SelfUpdateID     string
	ExecutionHostID  string
	AgentServiceID   string
	ExpectedRevision int64
	Operation        string
	PlanSHA256       string
	SessionID        string
	Now              time.Time
	TTL              time.Duration
}

type IssueSystemUpdateHostSelfUpdateGrantResult struct {
	Grant    SystemUpdateHostSelfUpdateGrant `json:"grant"`
	RawToken string                          `json:"token,omitempty"`
	Issued   bool                            `json:"issued"`
}

type ConsumeSystemUpdateHostSelfUpdateGrantParams struct {
	RawToken string
	Binding  SystemUpdateHostSelfUpdateGrant
	Now      time.Time
}

type ConsumeSystemUpdateHostSelfUpdateGrantResult struct {
	Grant    SystemUpdateHostSelfUpdateGrant `json:"grant"`
	Consumed bool                            `json:"consumed"`
}

type SystemUpdateHostSelfUpdateStore interface {
	ListSystemUpdateHostSelfUpdates(context.Context, int) ([]SystemUpdateHostSelfUpdate, error)
	GetSystemUpdateHostSelfUpdate(context.Context, string) (SystemUpdateHostSelfUpdate, error)
	GetActiveSystemUpdateHostSelfUpdateByExecutionHost(context.Context, string) (SystemUpdateHostSelfUpdate, error)
	CreateSystemUpdateHostSelfUpdate(context.Context, ServiceRegistryStore, UpdaterPolicyStore, CreateSystemUpdateHostSelfUpdateParams) (SystemUpdateHostSelfUpdate, bool, error)
	RetrySystemUpdateHostSelfUpdate(context.Context, ServiceRegistryStore, UpdaterPolicyStore, RetrySystemUpdateHostSelfUpdateParams) (SystemUpdateHostSelfUpdate, bool, error)
	CancelSystemUpdateHostSelfUpdate(context.Context, string, string, int64, bool, time.Time) (SystemUpdateHostSelfUpdate, error)
	ObserveSystemUpdateHostSelfUpdate(context.Context, SystemUpdateHostSelfUpdateObservation) (SystemUpdateHostSelfUpdate, bool, error)
	IssueSystemUpdateHostSelfUpdateGrant(context.Context, ServiceRegistryStore, UpdaterPolicyStore, IssueSystemUpdateHostSelfUpdateGrantParams) (IssueSystemUpdateHostSelfUpdateGrantResult, error)
	ConsumeSystemUpdateHostSelfUpdateGrant(context.Context, ServiceRegistryStore, UpdaterPolicyStore, ConsumeSystemUpdateHostSelfUpdateGrantParams) (ConsumeSystemUpdateHostSelfUpdateGrantResult, error)
}

func normalizeCreateSystemUpdateHostSelfUpdateParams(
	p CreateSystemUpdateHostSelfUpdateParams,
) CreateSystemUpdateHostSelfUpdateParams {
	p.ExecutionHostID = strings.TrimSpace(p.ExecutionHostID)
	p.TargetVersion = strings.TrimSpace(p.TargetVersion)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	p.RequestedByUserID = strings.TrimSpace(p.RequestedByUserID)
	p.RequestedByUsername = strings.TrimSpace(p.RequestedByUsername)
	p.RetryOfID = strings.TrimSpace(p.RetryOfID)
	p.Release.Tag = strings.TrimSpace(p.Release.Tag)
	p.Release.Commit = strings.TrimSpace(p.Release.Commit)
	p.Release.ManifestAssetName = strings.TrimSpace(p.Release.ManifestAssetName)
	p.Release.ManifestSHA256 = strings.TrimSpace(p.Release.ManifestSHA256)
	p.Release.ManifestChecksumSHA256 = strings.TrimSpace(p.Release.ManifestChecksumSHA256)
	p.Release.ArchiveAssetName = strings.TrimSpace(p.Release.ArchiveAssetName)
	p.Release.ArchiveSHA256 = strings.TrimSpace(p.Release.ArchiveSHA256)
	p.Release.ArchiveChecksumSHA256 = strings.TrimSpace(p.Release.ArchiveChecksumSHA256)
	p.Release.Arch = strings.ToLower(strings.TrimSpace(p.Release.Arch))
	p.Release.MinimumPanelVersion = strings.TrimSpace(p.Release.MinimumPanelVersion)
	p.Now = p.Now.UTC()
	return p
}

func validateCreateSystemUpdateHostSelfUpdateParams(
	p CreateSystemUpdateHostSelfUpdateParams,
) error {
	if !executionHostIDPattern.MatchString(p.ExecutionHostID) ||
		!systemUpdateJobVersionPattern.MatchString(p.TargetVersion) ||
		!semver.IsValid(p.TargetVersion) ||
		p.Release.Tag != p.TargetVersion ||
		!systemUpdateJobVersionPattern.MatchString(p.Release.Tag) ||
		!systemUpdateHostSelfUpdateCommitPattern.MatchString(p.Release.Commit) ||
		p.IdempotencyKey == "" || len(p.IdempotencyKey) > 128 ||
		containsControl(p.IdempotencyKey) ||
		!serviceIDPattern.MatchString(p.RequestedByUserID) ||
		p.RequestedByUsername == "" || len(p.RequestedByUsername) > 255 ||
		containsControl(p.RequestedByUsername) ||
		(p.RetryOfID != "" && !serviceIDPattern.MatchString(p.RetryOfID)) ||
		p.Now.IsZero() || p.Now.Location() != time.UTC {
		return ErrInvalidSystemUpdateHostSelfUpdate
	}
	r := p.Release
	assetIDs := map[int64]struct{}{
		r.ManifestAssetID:         {},
		r.ManifestChecksumAssetID: {},
		r.ArchiveAssetID:          {},
		r.ArchiveChecksumAssetID:  {},
	}
	expectedArchiveName := "autostream-host-agent_" + r.Tag +
		"_linux_" + r.Arch + ".tar.gz"
	if r.PublishedAt.IsZero() || r.PublishedAt.Location() != time.UTC ||
		r.AttestationVerifiedAt.IsZero() ||
		r.AttestationVerifiedAt.Location() != time.UTC ||
		r.AttestationVerifiedAt.After(p.Now) ||
		r.ManifestAssetID <= 0 || r.ManifestChecksumAssetID <= 0 ||
		r.ArchiveAssetID <= 0 || r.ArchiveChecksumAssetID <= 0 ||
		len(assetIDs) != 4 ||
		r.ManifestAssetName != "host-agent-manifest.json" ||
		r.ArchiveAssetName != expectedArchiveName ||
		!systemUpdateHostSelfUpdateDigestPattern.MatchString(r.ManifestSHA256) ||
		!systemUpdateHostSelfUpdateDigestPattern.MatchString(r.ManifestChecksumSHA256) ||
		!systemUpdateHostSelfUpdateDigestPattern.MatchString(r.ArchiveSHA256) ||
		!systemUpdateHostSelfUpdateDigestPattern.MatchString(r.ArchiveChecksumSHA256) ||
		r.ArchiveSize <= 0 ||
		(r.Arch != "amd64" && r.Arch != "arm64") ||
		r.AgentProtocolVersion < 1 || r.ExecutorProtocolVersion < 1 ||
		r.MutationProtocolVersion < 1 || r.RecoveryProtocolVersion < 1 ||
		!systemUpdateJobVersionPattern.MatchString(r.MinimumPanelVersion) {
		return ErrInvalidSystemUpdateHostSelfUpdate
	}
	return nil
}

func systemUpdateHostSelfUpdateTargetIsStrictlyNewer(
	targetVersion, agentVersion, executorVersion string,
) bool {
	return semver.IsValid(targetVersion) &&
		semver.IsValid(agentVersion) &&
		semver.IsValid(executorVersion) &&
		semver.Compare(targetVersion, agentVersion) > 0 &&
		semver.Compare(targetVersion, executorVersion) > 0
}

func systemUpdateHostSelfUpdateCurrentProtocolsAreCompatible(
	current systemUpdateHostPreviousRuntime,
) bool {
	return current.agentProtocol >=
		SystemUpdateHostSelfUpdateMinimumAgentProtocolVersion &&
		current.executorProtocol >=
			SystemUpdateHostSelfUpdateMinimumExecutorProtocolVersion &&
		current.mutationProtocol >=
			SystemUpdateHostSelfUpdateMinimumMutationProtocolVersion &&
		current.recoveryProtocol >=
			SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion
}

// reserveSystemUpdateHostSelfUpdateStage is called in the same durable
// transaction that consumes the one-time root mutation grant. Once the root
// boundary has permission to stage a release, the job must no longer be
// terminal-cancelable as merely queued. This closes the consume/response-loss
// window without treating grant issuance alone as proof that root can mutate.
func reserveSystemUpdateHostSelfUpdateStage(
	update SystemUpdateHostSelfUpdate,
	now time.Time,
) (SystemUpdateHostSelfUpdate, error) {
	if update.Status != SystemUpdateHostSelfUpdateQueued ||
		now.IsZero() || now.Location() != time.UTC {
		return SystemUpdateHostSelfUpdate{},
			ErrSystemUpdateHostSelfUpdateState
	}
	update.Status = SystemUpdateHostSelfUpdateStaging
	update.Revision++
	update.StartedAt = cloneTimePtr(&now)
	update.UpdatedAt = now
	return update, nil
}

func validateConsumedSystemUpdateHostSelfUpdateGrant(
	grant SystemUpdateHostSelfUpdateGrant,
	update SystemUpdateHostSelfUpdate,
) error {
	if grant.ConsumedAt == nil {
		return ErrSystemUpdateHostSelfUpdateGrant
	}
	if update.ID != grant.SelfUpdateID ||
		update.AttemptGeneration != grant.AttemptGeneration ||
		!systemUpdateHostSelfUpdateAllowsConsumedGrantReplay(update.Status) {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	if grant.Operation != SystemUpdateHostSelfUpdateGrantStage {
		if grant.StageClaimRevision != 0 ||
			grant.StageClaimedAt != nil {
			return ErrSystemUpdateHostSelfUpdateGrant
		}
		return nil
	}
	if grant.StageClaimRevision !=
		grant.ExpectedSelfUpdateRevision+1 ||
		grant.StageClaimedAt == nil ||
		!grant.StageClaimedAt.Equal(*grant.ConsumedAt) ||
		update.Revision < grant.StageClaimRevision {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	return nil
}

func systemUpdateHostSelfUpdateAllowsConsumedGrantReplay(
	status string,
) bool {
	switch status {
	case SystemUpdateHostSelfUpdateStaging,
		SystemUpdateHostSelfUpdateActivating,
		SystemUpdateHostSelfUpdateVerifying,
		SystemUpdateHostSelfUpdateRollingBack:
		return true
	default:
		return false
	}
}

func systemUpdateHostSelfUpdateIntentSHA256(
	p CreateSystemUpdateHostSelfUpdateParams,
) (string, error) {
	body, err := json.Marshal(struct {
		ExecutionHostID string                         `json:"execution_host_id"`
		TargetVersion   string                         `json:"target_version"`
		RetryOfID       string                         `json:"retry_of_id,omitempty"`
		Release         SystemUpdateHostReleaseBinding `json:"release"`
	}{
		ExecutionHostID: p.ExecutionHostID,
		TargetVersion:   p.TargetVersion,
		RetryOfID:       p.RetryOfID,
		Release:         systemUpdateHostReleaseBinding(p.Release),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func isTerminalSystemUpdateHostSelfUpdateStatus(status string) bool {
	switch status {
	case SystemUpdateHostSelfUpdateSucceeded,
		SystemUpdateHostSelfUpdateRolledBack,
		SystemUpdateHostSelfUpdateFailed,
		SystemUpdateHostSelfUpdateCanceled:
		return true
	default:
		return false
	}
}

func publicSystemUpdateHostSelfUpdate(
	update SystemUpdateHostSelfUpdate,
) SystemUpdateHostSelfUpdate {
	update.requestedByUserID = ""
	update.intentSHA256 = ""
	return update
}

func publicSystemUpdateHostSelfUpdateGrant(
	grant SystemUpdateHostSelfUpdateGrant,
) SystemUpdateHostSelfUpdateGrant {
	grant.tokenHash = ""
	return grant
}

func sameSystemUpdateHostSelfUpdateGrantBinding(
	left, right SystemUpdateHostSelfUpdateGrant,
) bool {
	return left.ID == right.ID &&
		left.SelfUpdateID == right.SelfUpdateID &&
		left.AttemptGeneration == right.AttemptGeneration &&
		left.Operation == right.Operation &&
		left.ExecutionHostID == right.ExecutionHostID &&
		left.AgentServiceID == right.AgentServiceID &&
		left.ExpectedSelfUpdateRevision == right.ExpectedSelfUpdateRevision &&
		left.ExpectedOwnershipEpoch == right.ExpectedOwnershipEpoch &&
		left.ExpectedSourcePolicyRevision == right.ExpectedSourcePolicyRevision &&
		left.ExpectedProjectionRevision == right.ExpectedProjectionRevision &&
		left.ExpectedLocalExecutorPolicyRevision == right.ExpectedLocalExecutorPolicyRevision &&
		left.ExpectedLocalExecutorPolicySHA256 == right.ExpectedLocalExecutorPolicySHA256 &&
		left.AgentVersion == right.AgentVersion &&
		left.ExecutorVersion == right.ExecutorVersion &&
		left.ReleaseCommit == right.ReleaseCommit &&
		left.ArtifactSHA256 == right.ArtifactSHA256 &&
		left.AgentProtocolVersion == right.AgentProtocolVersion &&
		left.ExecutorProtocolVersion == right.ExecutorProtocolVersion &&
		left.MutationProtocolVersion == right.MutationProtocolVersion &&
		left.RecoveryProtocolVersion == right.RecoveryProtocolVersion &&
		sameSystemUpdateHostReleaseBinding(left.Release, right.Release) &&
		left.DirectiveIssuedAt.Equal(right.DirectiveIssuedAt) &&
		left.PlanSHA256 == right.PlanSHA256 &&
		left.SessionID == right.SessionID &&
		left.Revision == right.Revision &&
		left.IssuedAt.Equal(right.IssuedAt) &&
		left.ExpiresAt.Equal(right.ExpiresAt)
}

func sameSystemUpdateHostSelfUpdateGrantIssueIntent(
	left, right SystemUpdateHostSelfUpdateGrant,
) bool {
	return left.SelfUpdateID == right.SelfUpdateID &&
		left.AttemptGeneration == right.AttemptGeneration &&
		left.Operation == right.Operation &&
		left.ExecutionHostID == right.ExecutionHostID &&
		left.AgentServiceID == right.AgentServiceID &&
		left.ExpectedSelfUpdateRevision == right.ExpectedSelfUpdateRevision &&
		left.ExpectedOwnershipEpoch == right.ExpectedOwnershipEpoch &&
		left.ExpectedSourcePolicyRevision == right.ExpectedSourcePolicyRevision &&
		left.ExpectedProjectionRevision == right.ExpectedProjectionRevision &&
		left.ExpectedLocalExecutorPolicyRevision ==
			right.ExpectedLocalExecutorPolicyRevision &&
		left.ExpectedLocalExecutorPolicySHA256 ==
			right.ExpectedLocalExecutorPolicySHA256 &&
		left.AgentVersion == right.AgentVersion &&
		left.ExecutorVersion == right.ExecutorVersion &&
		left.ReleaseCommit == right.ReleaseCommit &&
		left.ArtifactSHA256 == right.ArtifactSHA256 &&
		left.AgentProtocolVersion == right.AgentProtocolVersion &&
		left.ExecutorProtocolVersion == right.ExecutorProtocolVersion &&
		left.MutationProtocolVersion == right.MutationProtocolVersion &&
		left.RecoveryProtocolVersion == right.RecoveryProtocolVersion &&
		sameSystemUpdateHostReleaseBinding(left.Release, right.Release) &&
		left.DirectiveIssuedAt.Equal(right.DirectiveIssuedAt) &&
		left.PlanSHA256 == right.PlanSHA256 &&
		left.SessionID == right.SessionID
}
