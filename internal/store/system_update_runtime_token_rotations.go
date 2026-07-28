package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

const (
	SystemUpdateRuntimeTokenRotationStaged          = "staged"
	SystemUpdateRuntimeTokenRotationLocalStaged     = "local_staged"
	SystemUpdateRuntimeTokenRotationHeartbeatProved = "heartbeat_proved"
	SystemUpdateRuntimeTokenRotationActivated       = "activated"
	SystemUpdateRuntimeTokenRotationCancelRequested = "cancel_requested"
	SystemUpdateRuntimeTokenRotationCanceled        = "canceled"

	systemUpdateRuntimeTokenRotationIntentSchema = 1

	SystemUpdateRuntimeTokenRotationHeartbeatProofPhase = "staged_token_active"
	SystemUpdateRuntimeTokenRotationEmergencyCode       = "emergency_revoked"
)

var (
	ErrInvalidSystemUpdateRuntimeTokenRotation           = errors.New("invalid system update runtime token rotation")
	ErrSystemUpdateRuntimeTokenRotationBusy              = errors.New("system update execution host has an active runtime token rotation")
	ErrSystemUpdateRuntimeTokenRotationStale             = errors.New("system update runtime token rotation revision is stale")
	ErrSystemUpdateRuntimeTokenRotationTransition        = errors.New("invalid system update runtime token rotation transition")
	ErrSystemUpdateRuntimeTokenRotationToken             = errors.New("runtime token rotation credential does not match")
	ErrSystemUpdateRuntimeTokenRotationCredentialClaimed = errors.New("runtime token rotation credential is already claimed")
	ErrSystemUpdateRuntimeTokenRotationHeartbeatProof    = errors.New("runtime token rotation heartbeat proof does not match current agent state")
	ErrSystemUpdateRuntimeTokenRotationSharedToken       = errors.New("runtime token is shared by multiple services")
	ErrSystemUpdateRuntimeTokenRotationStoreMismatch     = errors.New("runtime token rotation stores do not share one transaction boundary")
)

// NodeTokenUnsealer is used only by the authenticated staged-credential claim
// path. Implementations must authenticate the ciphertext before returning
// plaintext.
type NodeTokenUnsealer func(ciphertext, nonce string) (rawToken string, err error)

type SystemUpdateRuntimeTokenRotation struct {
	ID                                  string     `json:"id"`
	ServiceID                           string     `json:"service_id"`
	ExecutionHostID                     string     `json:"execution_host_id"`
	Status                              string     `json:"status"`
	Revision                            int64      `json:"revision"`
	ExpectedOwnershipEpoch              int64      `json:"expected_ownership_epoch"`
	ExpectedSourcePolicyRevision        int64      `json:"expected_source_policy_revision"`
	ExpectedProjectionRevision          int64      `json:"expected_projection_revision"`
	ExpectedLocalExecutorPolicyRevision int64      `json:"expected_local_executor_policy_revision"`
	PreviousTokenID                     string     `json:"previous_token_id"`
	StagedTokenID                       string     `json:"staged_token_id"`
	LocalStageReceiptID                 string     `json:"local_stage_receipt_id,omitempty"`
	CredentialClaimedAt                 *time.Time `json:"credential_claimed_at,omitempty"`
	LocalStageAcknowledgedAt            *time.Time `json:"local_stage_acknowledged_at,omitempty"`
	LocalStagedAt                       *time.Time `json:"local_staged_at,omitempty"`
	HeartbeatProvedAt                   *time.Time `json:"heartbeat_proved_at,omitempty"`
	ActivatedAt                         *time.Time `json:"activated_at,omitempty"`
	CancelRequestedAt                   *time.Time `json:"cancel_requested_at,omitempty"`
	CancelAcknowledgedAt                *time.Time `json:"cancel_acknowledged_at,omitempty"`
	CanceledAt                          *time.Time `json:"canceled_at,omitempty"`
	EmergencyRevokedTokenID             string     `json:"emergency_revoked_token_id,omitempty"`
	EmergencyRevokedAt                  *time.Time `json:"emergency_revoked_at,omitempty"`
	CreatedAt                           time.Time  `json:"created_at"`
	UpdatedAt                           time.Time  `json:"updated_at"`

	idempotencyKey          string
	intentSHA256            string
	stagedTokenHash         string
	stagedTokenScopes       []string
	stagedTokenCiphertext   string
	stagedTokenNonce        string
	credentialClaimIDHash   string
	credentialClaimRevision int64
}

type StageSystemUpdateRuntimeTokenRotationParams struct {
	ServiceID                           string
	ExecutionHostID                     string
	IdempotencyKey                      string
	ExpectedOwnershipEpoch              int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	Now                                 time.Time
}

type StageSystemUpdateRuntimeTokenRotationResult struct {
	Rotation SystemUpdateRuntimeTokenRotation `json:"rotation"`
	Created  bool                             `json:"created"`
}

type ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams struct {
	RotationID                   string
	ServiceID                    string
	ExecutionHostID              string
	AuthenticatedPreviousTokenID string
	ClaimID                      string
	ExpectedRevision             int64
	Now                          time.Time
}

type ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult struct {
	Rotation SystemUpdateRuntimeTokenRotation `json:"rotation"`
	Token    ServiceToken                     `json:"runtime_token"`
	Claimed  bool                             `json:"claimed"`
}

type MarkSystemUpdateRuntimeTokenRotationLocalStagedParams struct {
	RotationID       string
	ExecutionHostID  string
	ExpectedRevision int64
	RawStagedToken   string
	Now              time.Time
}

type ProveSystemUpdateRuntimeTokenRotationHeartbeatParams struct {
	RotationID                          string
	ServiceID                           string
	ExecutionHostID                     string
	ExpectedRevision                    int64
	RawStagedToken                      string
	Phase                               string
	AgentVersion                        string
	ExecutorVersion                     string
	AgentProtocolVersion                int
	ExecutorProtocolVersion             int
	MutationProtocolVersion             int
	ExpectedOwnershipEpoch              int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	ExpectedLocalExecutorPolicySHA256   string
	LocalStageReceiptID                 string
	Now                                 time.Time
}

type ActivateSystemUpdateRuntimeTokenRotationParams struct {
	RotationID       string
	ExecutionHostID  string
	ExpectedRevision int64
	RawStagedToken   string
	Now              time.Time
}

type CancelSystemUpdateRuntimeTokenRotationParams struct {
	RotationID       string
	ExecutionHostID  string
	ExpectedRevision int64
	Now              time.Time
}

type AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams struct {
	RotationID                   string
	ServiceID                    string
	ExecutionHostID              string
	AuthenticatedPreviousTokenID string
	ExpectedRevision             int64
	Now                          time.Time
}

type EmergencyRevokeSystemUpdateRuntimeTokenParams struct {
	RotationID       string
	ExecutionHostID  string
	ExpectedRevision int64
	TokenID          string
	Now              time.Time
}

type SystemUpdateRuntimeTokenRotationStore interface {
	GetSystemUpdateRuntimeTokenRotation(ctx context.Context, id string) (SystemUpdateRuntimeTokenRotation, error)
	GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		ctx context.Context,
		executionHostID string,
	) (SystemUpdateRuntimeTokenRotation, error)
	StageSystemUpdateRuntimeTokenRotation(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params StageSystemUpdateRuntimeTokenRotationParams,
		seal NodeTokenSealer,
	) (StageSystemUpdateRuntimeTokenRotationResult, error)
	ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams,
		unseal NodeTokenUnsealer,
	) (ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult, error)
	MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params MarkSystemUpdateRuntimeTokenRotationLocalStagedParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
	ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
	ActivateSystemUpdateRuntimeTokenRotation(
		ctx context.Context,
		services ServiceRegistryStore,
		params ActivateSystemUpdateRuntimeTokenRotationParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
	CancelSystemUpdateRuntimeTokenRotation(
		ctx context.Context,
		services ServiceRegistryStore,
		params CancelSystemUpdateRuntimeTokenRotationParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
	AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
	EmergencyRevokeSystemUpdateRuntimeToken(
		ctx context.Context,
		services ServiceRegistryStore,
		params EmergencyRevokeSystemUpdateRuntimeTokenParams,
	) (rotation SystemUpdateRuntimeTokenRotation, applied bool, err error)
}

func normalizeClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(
	params ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams,
) ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
	params.RotationID = strings.TrimSpace(params.RotationID)
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.AuthenticatedPreviousTokenID = strings.TrimSpace(params.AuthenticatedPreviousTokenID)
	params.ClaimID = strings.TrimSpace(params.ClaimID)
	params.Now = params.Now.UTC()
	return params
}

func validateClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(
	params ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams,
) error {
	if !serviceIDPattern.MatchString(params.RotationID) ||
		!serviceIDPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		!serviceIDPattern.MatchString(params.AuthenticatedPreviousTokenID) ||
		!serviceIDPattern.MatchString(params.ClaimID) ||
		params.ExpectedRevision < 1 ||
		params.ExpectedRevision == math.MaxInt64 ||
		params.Now.IsZero() {
		return ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	return nil
}

func normalizeAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(
	params AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams,
) AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams {
	params.RotationID = strings.TrimSpace(params.RotationID)
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.AuthenticatedPreviousTokenID = strings.TrimSpace(
		params.AuthenticatedPreviousTokenID,
	)
	params.Now = params.Now.UTC()
	return params
}

func normalizeProveSystemUpdateRuntimeTokenRotationHeartbeatParams(
	params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
) ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
	params.RotationID = strings.TrimSpace(params.RotationID)
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.Phase = strings.TrimSpace(params.Phase)
	params.AgentVersion = strings.TrimSpace(params.AgentVersion)
	params.ExecutorVersion = strings.TrimSpace(params.ExecutorVersion)
	params.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(params.ExpectedLocalExecutorPolicySHA256),
	)
	params.LocalStageReceiptID = strings.TrimSpace(params.LocalStageReceiptID)
	params.Now = params.Now.UTC()
	return params
}

func validateProveSystemUpdateRuntimeTokenRotationHeartbeatParams(
	params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
) error {
	if !serviceIDPattern.MatchString(params.RotationID) ||
		!serviceIDPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.ExpectedRevision < 1 ||
		params.ExpectedRevision == math.MaxInt64 ||
		strings.TrimSpace(params.RawStagedToken) == "" ||
		params.Phase != SystemUpdateRuntimeTokenRotationHeartbeatProofPhase ||
		!systemUpdateJobVersionPattern.MatchString(params.AgentVersion) ||
		!systemUpdateJobVersionPattern.MatchString(params.ExecutorVersion) ||
		params.AgentProtocolVersion < 1 ||
		params.ExecutorProtocolVersion < 1 ||
		params.MutationProtocolVersion < 1 ||
		params.ExpectedOwnershipEpoch < 1 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.ExpectedLocalExecutorPolicySHA256) ||
		params.LocalStageReceiptID == "" ||
		len(params.LocalStageReceiptID) > 128 ||
		params.Now.IsZero() {
		return ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	return nil
}

func validateRuntimeTokenRotationHeartbeatProof(
	rotation SystemUpdateRuntimeTokenRotation,
	service RegisteredService,
	policy UpdaterPolicy,
	ownership SystemUpdateExecutionHost,
	params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
) error {
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID ||
		rotation.ExpectedOwnershipEpoch != params.ExpectedOwnershipEpoch ||
		rotation.ExpectedSourcePolicyRevision != params.ExpectedSourcePolicyRevision ||
		rotation.ExpectedProjectionRevision != params.ExpectedProjectionRevision ||
		rotation.ExpectedLocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		rotation.LocalStageReceiptID != params.LocalStageReceiptID ||
		rotation.LocalStageAcknowledgedAt == nil ||
		service.ServiceID != rotation.ServiceID ||
		service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != rotation.ExecutionHostID ||
		service.OwnershipEpoch != rotation.ExpectedOwnershipEpoch ||
		service.TokenID != rotation.PreviousTokenID ||
		service.Status != "online" ||
		service.LastHeartbeatAt == nil ||
		!service.LastHeartbeatAt.After(rotation.LocalStageAcknowledgedAt.UTC()) ||
		params.Now.Before(service.LastHeartbeatAt.UTC()) ||
		params.Now.Sub(service.LastHeartbeatAt.UTC()) > pullUpdaterActivationHeartbeatMaxAge ||
		service.ReportedVersion != params.AgentVersion ||
		policy.UpdaterID != rotation.ServiceID ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != rotation.ExecutionHostID ||
		policy.Revision != rotation.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != rotation.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != rotation.ExpectedLocalExecutorPolicyRevision ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 ||
		ownership.ExecutionHostID != rotation.ExecutionHostID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != rotation.ServiceID ||
		ownership.OwnershipEpoch != rotation.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != rotation.ExpectedProjectionRevision {
		return ErrSystemUpdateRuntimeTokenRotationHeartbeatProof
	}
	capabilities := service.ReportedCapabilities
	if updaterPolicyCapabilityString(capabilities["agent_version"]) != params.AgentVersion ||
		updaterPolicyCapabilityString(capabilities["executor_version"]) != params.ExecutorVersion ||
		runtimeTokenRotationCapabilityProtocol(capabilities["agent_protocol_version"]) != params.AgentProtocolVersion ||
		runtimeTokenRotationCapabilityProtocol(capabilities["executor_protocol_version"]) != params.ExecutorProtocolVersion ||
		runtimeTokenRotationCapabilityProtocol(capabilities["mutation_protocol_version"]) != params.MutationProtocolVersion ||
		updaterPolicyCapabilityString(capabilities["execution_host_id"]) != params.ExecutionHostID ||
		runtimeTokenRotationCapabilityRevision(capabilities["ownership_epoch"]) != params.ExpectedOwnershipEpoch ||
		runtimeTokenRotationCapabilityRevision(capabilities["source_policy_revision"]) != params.ExpectedSourcePolicyRevision ||
		runtimeTokenRotationCapabilityRevision(capabilities["projection_revision"]) != params.ExpectedProjectionRevision ||
		runtimeTokenRotationCapabilityRevision(capabilities["local_executor_policy_revision"]) != params.ExpectedLocalExecutorPolicyRevision ||
		updaterPolicyCapabilityString(capabilities["local_executor_policy_sha256"]) != params.ExpectedLocalExecutorPolicySHA256 ||
		updaterPolicyCapabilityString(capabilities["local_stage_receipt_id"]) != params.LocalStageReceiptID ||
		updaterPolicyCapabilityString(capabilities["local_phase"]) != params.Phase ||
		!updaterPolicyCapabilityBool(capabilities["host_agent"]) ||
		!updaterPolicyCapabilityBool(capabilities["update_executor"]) ||
		!updaterPolicyCapabilityBool(capabilities["mutation_enabled"]) ||
		updaterPolicyCapabilityBool(capabilities["recovery_pending"]) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return ErrSystemUpdateRuntimeTokenRotationHeartbeatProof
	}
	return nil
}

func runtimeTokenRotationCapabilityProtocol(value any) int {
	if number, ok := updaterPolicyCapabilityInt64(value); ok && number <= math.MaxInt {
		return int(number)
	}
	if text, ok := value.(string); ok {
		number, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil && number > 0 {
			return number
		}
	}
	return 0
}

func runtimeTokenRotationCapabilityRevision(value any) int64 {
	number, _ := updaterPolicyCapabilityInt64(value)
	return number
}

func validateAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(
	params AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams,
) error {
	if !serviceIDPattern.MatchString(params.RotationID) ||
		!serviceIDPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		!serviceIDPattern.MatchString(params.AuthenticatedPreviousTokenID) ||
		params.ExpectedRevision < 1 ||
		params.ExpectedRevision == math.MaxInt64 ||
		params.Now.IsZero() {
		return ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	return nil
}

func runtimeTokenRotationCredentialClaimMode(
	rotation SystemUpdateRuntimeTokenRotation,
	expectedRevision int64,
	claimIDHash string,
) (firstClaim bool, replay bool, err error) {
	if rotation.Status != SystemUpdateRuntimeTokenRotationStaged {
		return false, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.CredentialClaimedAt == nil {
		if rotation.Revision != expectedRevision {
			return false, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		return true, false, nil
	}
	// Only the exact request revision used by the successful first claim may
	// replay the credential after an uncertain/lost response. Supplying the
	// returned current revision is a new retrieval attempt and fails closed.
	if rotation.credentialClaimIDHash != claimIDHash {
		return false, false, ErrSystemUpdateRuntimeTokenRotationCredentialClaimed
	}
	if expectedRevision != rotation.credentialClaimRevision ||
		rotation.credentialClaimRevision+1 != rotation.Revision {
		return false, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	return false, true, nil
}

func runtimeTokenRotationClaimIDHash(claimID string) string {
	digest := sha256.Sum256([]byte(claimID))
	return hex.EncodeToString(digest[:])
}

func normalizeStageSystemUpdateRuntimeTokenRotationParams(params StageSystemUpdateRuntimeTokenRotationParams) StageSystemUpdateRuntimeTokenRotationParams {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.Now = params.Now.UTC()
	return params
}

func validateStageSystemUpdateRuntimeTokenRotationParams(params StageSystemUpdateRuntimeTokenRotationParams) error {
	if !serviceIDPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 ||
		containsControl(params.IdempotencyKey) ||
		params.ExpectedOwnershipEpoch < 1 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		params.ExpectedOwnershipEpoch == math.MaxInt64 ||
		params.ExpectedSourcePolicyRevision == math.MaxInt64 ||
		params.ExpectedProjectionRevision == math.MaxInt64 ||
		params.ExpectedLocalExecutorPolicyRevision == math.MaxInt64 ||
		params.Now.IsZero() {
		return ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	return nil
}

func runtimeTokenRotationIntentSHA256(params StageSystemUpdateRuntimeTokenRotationParams) (string, error) {
	payload := struct {
		SchemaVersion                       int    `json:"schema_version"`
		ServiceID                           string `json:"service_id"`
		ExecutionHostID                     string `json:"execution_host_id"`
		ExpectedOwnershipEpoch              int64  `json:"expected_ownership_epoch"`
		ExpectedSourcePolicyRevision        int64  `json:"expected_source_policy_revision"`
		ExpectedProjectionRevision          int64  `json:"expected_projection_revision"`
		ExpectedLocalExecutorPolicyRevision int64  `json:"expected_local_executor_policy_revision"`
	}{
		SchemaVersion:                       systemUpdateRuntimeTokenRotationIntentSchema,
		ServiceID:                           params.ServiceID,
		ExecutionHostID:                     params.ExecutionHostID,
		ExpectedOwnershipEpoch:              params.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        params.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          params.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: params.ExpectedLocalExecutorPolicyRevision,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func publicSystemUpdateRuntimeTokenRotation(rotation SystemUpdateRuntimeTokenRotation) SystemUpdateRuntimeTokenRotation {
	rotation.idempotencyKey = ""
	rotation.intentSHA256 = ""
	rotation.stagedTokenHash = ""
	rotation.stagedTokenScopes = nil
	rotation.stagedTokenCiphertext = ""
	rotation.stagedTokenNonce = ""
	rotation.credentialClaimIDHash = ""
	rotation.credentialClaimRevision = 0
	return rotation
}

func scrubSystemUpdateRuntimeTokenRotationReplaySecrets(
	rotation *SystemUpdateRuntimeTokenRotation,
) {
	if rotation == nil {
		return
	}
	rotation.stagedTokenCiphertext = ""
	rotation.stagedTokenNonce = ""
	rotation.credentialClaimIDHash = ""
	rotation.credentialClaimRevision = 0
}

func isActiveSystemUpdateRuntimeTokenRotation(rotation SystemUpdateRuntimeTokenRotation) bool {
	switch rotation.Status {
	case SystemUpdateRuntimeTokenRotationStaged,
		SystemUpdateRuntimeTokenRotationLocalStaged,
		SystemUpdateRuntimeTokenRotationHeartbeatProved,
		SystemUpdateRuntimeTokenRotationCancelRequested:
		return true
	default:
		return false
	}
}

func activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(store *MemorySystemUpdateStore, executionHostID string) (SystemUpdateRuntimeTokenRotation, bool) {
	for _, rotation := range store.runtimeTokenRotations {
		if rotation.ExecutionHostID == executionHostID && isActiveSystemUpdateRuntimeTokenRotation(rotation) {
			return rotation, true
		}
	}
	return SystemUpdateRuntimeTokenRotation{}, false
}

func memorySystemUpdateRuntimeTokenRotationByIdempotencyLocked(store *MemorySystemUpdateStore, serviceID, key string) (SystemUpdateRuntimeTokenRotation, bool) {
	for _, rotation := range store.runtimeTokenRotations {
		if rotation.ServiceID == serviceID && rotation.idempotencyKey == key {
			return rotation, true
		}
	}
	return SystemUpdateRuntimeTokenRotation{}, false
}

func runtimeTokenRotationReplayToken(rotation SystemUpdateRuntimeTokenRotation, unseal NodeTokenUnsealer) (ServiceToken, error) {
	if unseal == nil {
		return ServiceToken{}, errNodeTokenSealerRequired
	}
	rawToken, err := unseal(rotation.stagedTokenCiphertext, rotation.stagedTokenNonce)
	if err != nil {
		return ServiceToken{}, err
	}
	if !runtimeTokenRotationRawTokenMatchesHash(rawToken, rotation.stagedTokenHash) {
		return ServiceToken{}, ErrSystemUpdateRuntimeTokenRotationToken
	}
	return ServiceToken{
		ID:          rotation.StagedTokenID,
		ServiceType: "update_agent",
		Scopes:      append([]string(nil), rotation.stagedTokenScopes...),
		RawToken:    rawToken,
		CreatedAt:   rotation.CreatedAt,
	}, nil
}

func securityHashToken(rawToken string) string {
	// Kept behind a small helper so every proof/replay path performs the same
	// exact hash operation and never formats the plaintext in an error.
	return security.HashToken(rawToken)
}

func runtimeTokenRotationRawTokenMatchesHash(rawToken, expectedHash string) bool {
	actualHash := securityHashToken(rawToken)
	if len(actualHash) != sha256.Size*2 || len(expectedHash) != sha256.Size*2 {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(actualHash),
		[]byte(expectedHash),
	) == 1
}

func normalizeRuntimeTokenRotationTransition(id, executionHostID string, expectedRevision int64, now time.Time) (string, string, int64, time.Time, error) {
	id = strings.TrimSpace(id)
	executionHostID = strings.TrimSpace(executionHostID)
	now = now.UTC()
	if !serviceIDPattern.MatchString(id) ||
		!executionHostIDPattern.MatchString(executionHostID) ||
		expectedRevision < 1 ||
		expectedRevision == math.MaxInt64 ||
		now.IsZero() {
		return "", "", 0, time.Time{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	return id, executionHostID, expectedRevision, now, nil
}

func rotationRevisionAllowsReplay(rotation SystemUpdateRuntimeTokenRotation, expectedRevision int64) bool {
	return expectedRevision+1 == rotation.Revision
}

func validateRuntimeTokenRotationCredential(rotation SystemUpdateRuntimeTokenRotation, rawToken string) error {
	if rawToken == "" ||
		!runtimeTokenRotationRawTokenMatchesHash(rawToken, rotation.stagedTokenHash) {
		return ErrSystemUpdateRuntimeTokenRotationToken
	}
	return nil
}

func hasStagedServiceNodeConfiguration(service RegisteredService) bool {
	return service.StagedNodePreviousTokenID != "" ||
		service.StagedNodeTokenID != "" ||
		service.StagedNodeTokenHash != "" ||
		len(service.StagedNodeTokenScopes) != 0 ||
		service.StagedNodeTokenCiphertext != "" ||
		service.StagedNodeTokenNonce != "" ||
		service.StagedNodeActivationTokenHash != "" ||
		service.StagedNodeTokenAt != nil
}

func validateRuntimeTokenRotationOwnership(
	service RegisteredService,
	policy UpdaterPolicy,
	ownership SystemUpdateExecutionHost,
	params StageSystemUpdateRuntimeTokenRotationParams,
) error {
	if service.ServiceID != params.ServiceID ||
		service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != params.ExpectedOwnershipEpoch ||
		policy.UpdaterID != params.ServiceID ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		ownership.ExecutionHostID != params.ExecutionHostID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != params.ServiceID ||
		ownership.OwnershipEpoch != params.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != params.ExpectedProjectionRevision ||
		policy.ProjectionRevision != ownership.PolicyRevision {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func stageParamsForRuntimeTokenRotation(
	rotation SystemUpdateRuntimeTokenRotation,
	now time.Time,
) StageSystemUpdateRuntimeTokenRotationParams {
	return StageSystemUpdateRuntimeTokenRotationParams{
		ServiceID:                           rotation.ServiceID,
		ExecutionHostID:                     rotation.ExecutionHostID,
		ExpectedOwnershipEpoch:              rotation.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        rotation.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          rotation.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: rotation.ExpectedLocalExecutorPolicyRevision,
		Now:                                 now,
	}
}

func runtimeTokenRotationLocalStageReceiptID(
	rotation SystemUpdateRuntimeTokenRotation,
) string {
	return "staged-token:" + rotation.StagedTokenID
}

func runtimeTokenRotationSelfUpdateBusy(service RegisteredService) bool {
	if updaterPolicyCapabilityBool(service.ReportedCapabilities["recovery_pending"]) {
		return true
	}
	phase := strings.ToLower(
		updaterPolicyCapabilityString(service.ReportedCapabilities["self_update_phase"]),
	)
	return phase != "" && phase != "stable"
}
