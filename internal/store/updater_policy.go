package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/ssh"
)

const (
	UpdaterGitHubReleaseTokenSecretName  = "updater_github_release_token"
	maxUpdaterReleaseTokenBytes          = 4096
	pullUpdaterActivationHeartbeatMaxAge = 180 * time.Second
	updaterPolicySnapshotReadMaxAttempts = 3
)

var (
	ErrConflict                      = errors.New("conflict")
	errUpdaterReleaseTokenIntegrity  = errors.New("updater release token integrity check failed")
	errUpdaterPolicySnapshotChanged  = errors.New("updater policy snapshot changed")
	updaterPolicyIdentifierPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	updaterPolicyHostIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	updaterPolicyLinuxUserPattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	updaterPolicyDatabaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type UpdaterPolicy struct {
	UpdaterID                   string                `json:"updater_id"`
	Revision                    int64                 `json:"revision"`
	ProjectionRevision          int64                 `json:"projection_revision,omitempty"`
	LocalExecutorPolicyRevision int64                 `json:"local_executor_policy_revision,omitempty"`
	TransportMode               string                `json:"transport_mode"`
	ExecutionHostID             string                `json:"execution_host_id,omitempty"`
	LocalExecutorPolicySHA256   string                `json:"local_executor_policy_sha256,omitempty"`
	API                         UpdaterPolicyAPI      `json:"api"`
	PollIntervalSeconds         int                   `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds    int                   `json:"heartbeat_interval_seconds"`
	Hosts                       []UpdaterPolicyHost   `json:"hosts"`
	Targets                     []UpdaterPolicyTarget `json:"targets"`
	UpdatedAt                   time.Time             `json:"updated_at"`
}

type UpdaterPolicyAPI struct {
	BindHost    string `json:"bind_host"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SSLEnabled  bool   `json:"ssl_enabled"`
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`
}

type UpdaterPolicyHost struct {
	HostID        string `json:"host_id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Arch          string `json:"arch"`
	HostPublicKey string `json:"host_public_key"`
}

type UpdaterPolicyTarget struct {
	TargetID       string `json:"target_id"`
	ServiceID      string `json:"service_id"`
	HostID         string `json:"host_id"`
	ServiceType    string `json:"service_type"`
	DeploymentMode string `json:"deployment_mode"`
	// DatabaseName is stored in update_agent_target_databases rather than
	// policy_json so an older strict decoder can still read the declarative
	// policy after a Control Panel rollback.
	DatabaseName string `json:"-"`
	// LocalListenPort is stored in update_agent_target_local_listeners rather
	// than policy_json so an older strict decoder can still read the
	// declarative policy after a Control Panel rollback. Zero preserves the
	// legacy contract that derives the systemd listener from AppliedEndpoint.
	LocalListenPort int `json:"-"`
}

type UpdaterPolicyStore interface {
	GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error)
	ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error)
	SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error)
}

type UpdaterPolicyAdminStore interface {
	UpdaterPolicyStore
	SaveUpdaterPolicyAndReleaseToken(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy, releaseToken *string) (UpdaterPolicy, SecretStatus, error)
	SavePullUpdaterPolicy(ctx context.Context, executionHosts SystemUpdateExecutionHostStore, serviceID string, expectedRevision, expectedOwnershipEpoch int64, input UpdaterPolicy) (UpdaterPolicy, error)
	BindPullUpdaterConfigurePolicy(ctx context.Context, params BindPullUpdaterConfigurePolicyParams) (UpdaterPolicy, error)
	ActivatePullUpdaterOwnership(ctx context.Context, services ServiceRegistryStore, executionHosts SystemUpdateExecutionHostStore, params ActivatePullUpdaterOwnershipParams) (ActivatePullUpdaterOwnershipResult, error)
	DeactivatePullUpdaterOwnership(ctx context.Context, services ServiceRegistryStore, executionHosts SystemUpdateExecutionHostStore, params DeactivatePullUpdaterOwnershipParams) (DeactivatePullUpdaterOwnershipResult, error)
	GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error)
	GetUpdaterReleaseTokenValue(ctx context.Context) (string, error)
}

type BindPullUpdaterConfigurePolicyParams struct {
	ServiceID                           string
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	LocalExecutorPolicySHA256           string
}

type ActivatePullUpdaterOwnershipParams struct {
	ServiceID                           string
	ExecutionHostID                     string
	ExpectedExecutionHostOwnershipEpoch int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	ExpectedLocalExecutorPolicySHA256   string
	ControlPanelTarget                  *PullUpdaterControlPanelTarget
}

// PullUpdaterControlPanelTarget is the server-owned runtime view of the
// Control Panel process. The Control Panel is not a registered Node service,
// so activation receives this narrowly validated synthetic target instead of
// accepting a caller-provided services row.
type PullUpdaterControlPanelTarget struct {
	ServiceID             string
	ServiceType           string
	EndpointRevision      int64
	AppliedConfigRevision int64
	AppliedConfigSHA256   string
	AppliedEndpoint       ServiceEndpoint
}

func (target PullUpdaterControlPanelTarget) registeredService() RegisteredService {
	endpoint := target.AppliedEndpoint
	return RegisteredService{
		ServiceID:             target.ServiceID,
		ServiceType:           target.ServiceType,
		ServiceName:           "Control Panel",
		Host:                  endpoint.Host,
		Port:                  endpoint.Port,
		SSLEnabled:            endpoint.SSLEnabled,
		PublicURL:             endpoint.PublicURL,
		DesiredEndpoint:       copyServiceEndpoint(&endpoint),
		AppliedEndpoint:       copyServiceEndpoint(&endpoint),
		EndpointRevision:      target.EndpointRevision,
		EndpointStatus:        "applied",
		AppliedConfigRevision: target.AppliedConfigRevision,
		AppliedConfigSHA256:   target.AppliedConfigSHA256,
		Status:                "online",
	}
}

type ActivatePullUpdaterOwnershipResult struct {
	Service   RegisteredService
	Ownership SystemUpdateExecutionHost
	Policy    UpdaterPolicy
}

type DeactivatePullUpdaterOwnershipParams struct {
	ServiceID                           string
	ExecutionHostID                     string
	ExpectedExecutionHostOwnershipEpoch int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	ExpectedLocalExecutorPolicySHA256   string
}

type DeactivatePullUpdaterOwnershipResult struct {
	Service   RegisteredService
	Ownership SystemUpdateExecutionHost
	Policy    UpdaterPolicy
}

type MemoryUpdaterPolicyStore struct {
	mu                 sync.Mutex
	policies           map[string]UpdaterPolicy
	releaseToken       string
	releaseTokenStatus SecretStatus
}

func NewMemoryUpdaterPolicyStore() *MemoryUpdaterPolicyStore {
	return &MemoryUpdaterPolicyStore{policies: map[string]UpdaterPolicy{}}
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	serviceID = strings.TrimSpace(serviceID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[serviceID]
	if !ok {
		return UpdaterPolicy{}, ErrNotFound
	}
	return cloneUpdaterPolicy(policy), nil
}

func (s *MemoryUpdaterPolicyStore) ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.policies))
	for id := range s.policies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	policies := make([]UpdaterPolicy, 0, len(ids))
	for _, id := range ids {
		policies = append(policies, cloneUpdaterPolicy(s.policies[id]))
	}
	return policies, nil
}

func (s *MemoryUpdaterPolicyStore) SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.policies[normalized.UpdaterID]
	if (!exists && expectedRevision != 0) || (exists && current.Revision != expectedRevision) {
		return UpdaterPolicy{}, ErrConflict
	}
	now := time.Now().UTC()
	if exists && !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = normalized.Revision
	normalized.LocalExecutorPolicyRevision = 0
	normalized.UpdatedAt = now
	s.policies[normalized.UpdaterID] = cloneUpdaterPolicy(normalized)
	return cloneUpdaterPolicy(normalized), nil
}

func (s *MemoryUpdaterPolicyStore) SaveUpdaterPolicyAndReleaseToken(
	ctx context.Context,
	serviceID string,
	expectedRevision int64,
	input UpdaterPolicy,
	releaseToken *string,
) (UpdaterPolicy, SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, SecretStatus{}, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, SecretStatus{}, ErrInvalidSettings
	}
	normalizedToken, err := normalizeUpdaterReleaseToken(releaseToken)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	current, exists := s.policies[normalized.UpdaterID]
	if (!exists && expectedRevision != 0) || (exists && current.Revision != expectedRevision) {
		return UpdaterPolicy{}, SecretStatus{}, ErrConflict
	}
	now := time.Now().UTC()
	if exists && !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = normalized.Revision
	normalized.LocalExecutorPolicyRevision = 0
	normalized.UpdatedAt = now

	tokenStatus := s.releaseTokenStatus
	tokenStatus.Name = UpdaterGitHubReleaseTokenSecretName
	if normalizedToken != nil {
		if *normalizedToken == "" {
			s.releaseToken = ""
			s.releaseTokenStatus = SecretStatus{}
			tokenStatus = SecretStatus{
				Name:      UpdaterGitHubReleaseTokenSecretName,
				UpdatedAt: now.Format(time.RFC3339),
			}
		} else {
			s.releaseToken = *normalizedToken
			s.releaseTokenStatus = SecretStatus{
				Name:        UpdaterGitHubReleaseTokenSecretName,
				Configured:  true,
				Fingerprint: security.SecretFingerprint(*normalizedToken),
				UpdatedAt:   now.Format(time.RFC3339),
			}
			tokenStatus = s.releaseTokenStatus
		}
	}
	s.policies[normalized.UpdaterID] = cloneUpdaterPolicy(normalized)
	return cloneUpdaterPolicy(normalized), tokenStatus, nil
}

func (s *MemoryUpdaterPolicyStore) SavePullUpdaterPolicy(
	ctx context.Context,
	executionHosts SystemUpdateExecutionHostStore,
	serviceID string,
	expectedRevision, expectedOwnershipEpoch int64,
	input UpdaterPolicy,
) (UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, ErrConflict
	}
	if expectedOwnershipEpoch < 0 {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if normalized.TransportMode != SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	updates, ok := executionHosts.(*MemorySystemUpdateStore)
	if !ok || updates == nil {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionStoreMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	updates.mu.Lock()
	defer updates.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}

	currentPolicy, exists := s.policies[normalized.UpdaterID]
	if (!exists && expectedRevision != 0) || (exists && currentPolicy.Revision != expectedRevision) {
		return UpdaterPolicy{}, ErrConflict
	}
	ownership, ownershipExists := updates.executionHosts[normalized.ExecutionHostID]
	if !ownershipExists {
		ownership = syntheticSystemUpdateExecutionHost(normalized.ExecutionHostID)
	}
	if ownership.OwnershipEpoch != expectedOwnershipEpoch {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	activePullOwner := ownership.TransportMode == SystemUpdateTransportPullV2
	switch ownership.TransportMode {
	case SystemUpdateTransportSSHV1:
		// A pull agent may observe an SSH-owned host without taking ownership.
		// Its policy revision is independent until an explicit ownership switch.
	case SystemUpdateTransportPullV2:
		if ownership.AgentServiceID != normalized.UpdaterID {
			return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
		}
		if ownership.OwnershipEpoch <= 0 {
			return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
		}
		currentProjectionRevision := currentPolicy.ProjectionRevision
		if currentProjectionRevision < 1 {
			currentProjectionRevision = currentPolicy.Revision
		}
		if ownership.PolicyRevision != currentProjectionRevision {
			return UpdaterPolicy{}, ErrConflict
		}
		for _, job := range updates.jobs {
			if job.ExecutionHostID == normalized.ExecutionHostID && !isTerminalSystemUpdateStatus(job.Status) {
				return UpdaterPolicy{}, ErrSystemUpdateExecutionHostBusy
			}
		}
		if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
			updates, normalized.ExecutionHostID,
		); found {
			return UpdaterPolicy{}, ErrSystemUpdateRuntimeTokenRotationBusy
		}
	default:
		return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
	}

	now := time.Now().UTC()
	if exists && !now.After(currentPolicy.UpdatedAt) {
		now = currentPolicy.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = normalized.Revision
	normalized.LocalExecutorPolicyRevision = normalized.Revision
	normalized.UpdatedAt = now

	s.policies[normalized.UpdaterID] = cloneUpdaterPolicy(normalized)
	if activePullOwner {
		ownership.PolicyRevision = normalized.ProjectionRevision
		ownership.UpdatedAt = now
		updates.executionHosts[normalized.ExecutionHostID] = ownership
	}
	return cloneUpdaterPolicy(normalized), nil
}

func (s *MemoryUpdaterPolicyStore) BindPullUpdaterConfigurePolicy(
	ctx context.Context,
	params BindPullUpdaterConfigurePolicyParams,
) (UpdaterPolicy, error) {
	params, err := normalizeBindPullUpdaterConfigurePolicyParams(params)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.policies[params.ServiceID]
	if !exists {
		return UpdaterPolicy{}, ErrNotFound
	}
	if current.TransportMode != SystemUpdateTransportPullV2 ||
		current.Revision != params.ExpectedSourcePolicyRevision ||
		current.ProjectionRevision != params.ExpectedProjectionRevision ||
		current.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision {
		return UpdaterPolicy{}, ErrConflict
	}
	if current.LocalExecutorPolicySHA256 == params.LocalExecutorPolicySHA256 {
		return cloneUpdaterPolicy(current), nil
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	current.LocalExecutorPolicySHA256 = params.LocalExecutorPolicySHA256
	current.UpdatedAt = now
	s.policies[params.ServiceID] = cloneUpdaterPolicy(current)
	return cloneUpdaterPolicy(current), nil
}

func (s *MemoryUpdaterPolicyStore) ActivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params ActivatePullUpdaterOwnershipParams,
) (ActivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeActivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MemorySystemUpdateStore)
	if !ok || updates == nil {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	updates.mu.Lock()
	defer updates.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}

	service, exists := registry.services[params.ServiceID]
	if !exists ||
		service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != 0 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	token, exists := registry.serviceTokens[service.TokenID]
	if !exists || token.ServiceType != "update_agent" || token.RevokedAt != nil {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	policy, exists := s.policies[params.ServiceID]
	if !exists ||
		policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	targetServices := make(map[string]RegisteredService, len(policy.Targets))
	controlPanelTargetUsed := false
	for _, target := range policy.Targets {
		if updaterPolicyControlPanelTarget(target) {
			if params.ControlPanelTarget == nil {
				return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
			}
			targetServices[target.ServiceID] = params.ControlPanelTarget.registeredService()
			controlPanelTargetUsed = true
			continue
		}
		targetService, exists := registry.services[target.ServiceID]
		if !exists {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
		}
		targetServices[target.ServiceID] = targetService
	}
	if params.ControlPanelTarget != nil && !controlPanelTargetUsed {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	if !PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	if !registeredPullObserverReadyForActivation(service, policy, targetServices, time.Now().UTC()) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	baselineReservations, err := pullActivationBaselineReservations(
		policy,
		targetServices,
		service,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	for _, reservation := range baselineReservations {
		key := servicePortKey(reservation)
		if existing, exists := updates.portReservations[key]; exists &&
			!sameServicePortReservationOwner(existing, reservation) {
			return ActivatePullUpdaterOwnershipResult{}, ErrServicePortReserved
		}
	}
	currentOwnership, ownershipExists := updates.executionHosts[params.ExecutionHostID]
	if !ownershipExists {
		currentOwnership = syntheticSystemUpdateExecutionHost(params.ExecutionHostID)
	}
	if currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if normalizedSystemUpdateTransportMode(currentOwnership.TransportMode) != SystemUpdateTransportSSHV1 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	for _, job := range updates.jobs {
		if job.ExecutionHostID == params.ExecutionHostID && !isTerminalSystemUpdateStatus(job.Status) {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
		}
	}

	now := time.Now().UTC()
	legacyAgentServiceID := nextSystemUpdateLegacyAgentServiceID(
		currentOwnership,
		SystemUpdateTransportPullV2,
		params.ServiceID,
	)
	if legacyAgentServiceID == "" && !ownershipExists {
		legacyAgentServiceID = uniqueActiveMemoryLegacyUpdaterForHostLocked(
			s,
			registry,
			params.ExecutionHostID,
			policy,
		)
	}
	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID:      params.ExecutionHostID,
		TransportMode:        SystemUpdateTransportPullV2,
		AgentServiceID:       params.ServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:       policy.ProjectionRevision,
		CreatedAt:            currentOwnership.CreatedAt,
		UpdatedAt:            now,
	}
	if nextOwnership.CreatedAt.IsZero() {
		nextOwnership.CreatedAt = now
	}
	service.OwnershipEpoch = nextOwnership.OwnershipEpoch
	service.UpdatedAt = now
	updates.executionHosts[params.ExecutionHostID] = nextOwnership
	registry.services[params.ServiceID] = service
	reportedConfigDigests := updaterPolicyCapabilityStringMap(
		service.ReportedCapabilities["reported_config_sha256"],
	)
	for serviceID, targetService := range targetServices {
		if targetService.AppliedConfigSHA256 != "" {
			continue
		}
		targetService.AppliedConfigSHA256 = reportedConfigDigests[serviceID]
		targetService.UpdatedAt = now
		registry.services[serviceID] = targetService
	}
	if updates.portReservations == nil {
		updates.portReservations = map[servicePortReservationKey]ServicePortReservation{}
	}
	for _, reservation := range baselineReservations {
		key := servicePortKey(reservation)
		if _, exists := updates.portReservations[key]; exists {
			continue
		}
		reservation.CreatedAt = now
		reservation.UpdatedAt = now
		updates.portReservations[key] = reservation
	}
	return ActivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    cloneUpdaterPolicy(policy),
	}, nil
}

func (s *MemoryUpdaterPolicyStore) DeactivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params DeactivatePullUpdaterOwnershipParams,
) (DeactivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeDeactivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MemorySystemUpdateStore)
	if !ok || updates == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	updates.mu.Lock()
	defer updates.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}

	currentOwnership, exists := updates.executionHosts[params.ExecutionHostID]
	if !exists ||
		currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		currentOwnership.AgentServiceID != params.ServiceID ||
		currentOwnership.OwnershipEpoch <= 0 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	legacyAgentServiceID := strings.TrimSpace(currentOwnership.LegacyAgentServiceID)
	if !updaterPolicyIdentifierPattern.MatchString(legacyAgentServiceID) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service, exists := registry.services[params.ServiceID]
	if !exists ||
		service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != currentOwnership.OwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	token, exists := registry.serviceTokens[service.TokenID]
	if !exists || token.ServiceType != "update_agent" || token.RevokedAt != nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	recoveryPending, recoveryStateKnown := service.ReportedCapabilities["recovery_pending"].(bool)
	if !recoveryStateKnown || recoveryPending {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	policy, exists := s.policies[params.ServiceID]
	if !exists ||
		policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 ||
		currentOwnership.PolicyRevision != policy.ProjectionRevision {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	legacyService, exists := registry.services[legacyAgentServiceID]
	if !exists ||
		legacyService.ServiceType != "update_agent" ||
		normalizedSystemUpdateTransportMode(legacyService.TransportMode) != SystemUpdateTransportSSHV1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	legacyToken, exists := registry.serviceTokens[legacyService.TokenID]
	if !exists ||
		legacyToken.ServiceType != "update_agent" ||
		legacyToken.RevokedAt != nil ||
		validateRequiredUpdateAgentScopes(
			legacyToken.ServiceType,
			legacyToken.Scopes,
		) != nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	legacyPolicy, exists := s.policies[legacyAgentServiceID]
	if !exists ||
		legacyPolicy.TransportMode != SystemUpdateTransportSSHV1 ||
		!legacyUpdaterPolicyCoversPullPolicy(
			legacyPolicy,
			policy,
			params.ExecutionHostID,
		) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	for _, job := range updates.jobs {
		if job.ExecutionHostID == params.ExecutionHostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		updates, params.ExecutionHostID,
	); found {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateHostSelfUpdateBusy
	}
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
		updates, params.ExecutionHostID,
	); found {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	now := time.Now().UTC()
	if unsettledMemorySystemUpdateMutationGrantForHostLocked(
		updates, params.ExecutionHostID, now,
	) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}

	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID:      params.ExecutionHostID,
		TransportMode:        SystemUpdateTransportSSHV1,
		AgentServiceID:       legacyAgentServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:       legacyPolicy.ProjectionRevision,
		CreatedAt:            currentOwnership.CreatedAt,
		UpdatedAt:            now,
	}
	service.OwnershipEpoch = 0
	service.UpdatedAt = now
	updates.executionHosts[params.ExecutionHostID] = nextOwnership
	registry.services[params.ServiceID] = service
	return DeactivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    cloneUpdaterPolicy(policy),
	}, nil
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return SecretStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.releaseTokenStatus
	status.Name = UpdaterGitHubReleaseTokenSecretName
	return status, nil
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterReleaseTokenValue(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releaseToken == "" {
		return "", ErrNotFound
	}
	return s.releaseToken, nil
}

type MariaDBUpdaterPolicyStore struct {
	db          *sql.DB
	keyMaterial string
}

func NewMariaDBUpdaterPolicyStore(db *sql.DB) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db}
}

func NewMariaDBUpdaterPolicyAdminStore(db *sql.DB, keyMaterial string) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db, keyMaterial: keyMaterial}
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	for attempt := 0; attempt < updaterPolicySnapshotReadMaxAttempts; attempt++ {
		policy, err := s.getUpdaterPolicyOnce(ctx, serviceID)
		if !errors.Is(err, errUpdaterPolicySnapshotChanged) {
			return policy, err
		}
	}
	return UpdaterPolicy{}, ErrConflict
}

func (s MariaDBUpdaterPolicyStore) getUpdaterPolicyOnce(
	ctx context.Context,
	serviceID string,
) (UpdaterPolicy, error) {
	var (
		revision                    int64
		projectionRevision          int64
		localExecutorPolicyRevision int64
		body                        []byte
		updatedAt                   time.Time
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?`,
		serviceID,
	).Scan(&revision, &projectionRevision, &localExecutorPolicyRevision, &body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	policy, err := decodeUpdaterPolicyRevisions(
		serviceID,
		revision,
		projectionRevision,
		localExecutorPolicyRevision,
		body,
		updatedAt,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetDatabases(ctx, s.db, &policy); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetLocalListeners(ctx, s.db, &policy); err != nil {
		return UpdaterPolicy{}, err
	}
	return policy, nil
}

func (s MariaDBUpdaterPolicyStore) ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error) {
	for attempt := 0; attempt < updaterPolicySnapshotReadMaxAttempts; attempt++ {
		policies, err := s.listUpdaterPoliciesOnce(ctx)
		if !errors.Is(err, errUpdaterPolicySnapshotChanged) {
			return policies, err
		}
	}
	return nil, ErrConflict
}

func (s MariaDBUpdaterPolicyStore) listUpdaterPoliciesOnce(
	ctx context.Context,
) ([]UpdaterPolicy, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT service_id, revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
ORDER BY service_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	policies := []UpdaterPolicy{}
	for rows.Next() {
		var (
			serviceID                   string
			revision                    int64
			projectionRevision          int64
			localExecutorPolicyRevision int64
			body                        []byte
			updatedAt                   time.Time
		)
		if err := rows.Scan(
			&serviceID,
			&revision,
			&projectionRevision,
			&localExecutorPolicyRevision,
			&body,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		policy, err := decodeUpdaterPolicyRevisions(
			serviceID,
			revision,
			projectionRevision,
			localExecutorPolicyRevision,
			body,
			updatedAt,
		)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range policies {
		if err := attachUpdaterTargetDatabases(ctx, s.db, &policies[index]); err != nil {
			return nil, err
		}
		if err := attachUpdaterTargetLocalListeners(ctx, s.db, &policies[index]); err != nil {
			return nil, err
		}
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].UpdaterID < policies[j].UpdaterID
	})
	return policies, nil
}

func (s MariaDBUpdaterPolicyStore) SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error) {
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if err := saveUpdaterPolicyCAS(ctx, s.db, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, err
	}
	return normalized, nil
}

func (s MariaDBUpdaterPolicyStore) SaveUpdaterPolicyAndReleaseToken(
	ctx context.Context,
	serviceID string,
	expectedRevision int64,
	input UpdaterPolicy,
	releaseToken *string,
) (UpdaterPolicy, SecretStatus, error) {
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, SecretStatus{}, ErrInvalidSettings
	}
	normalizedToken, err := normalizeUpdaterReleaseToken(releaseToken)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	var (
		tokenCiphertext string
		tokenNonce      string
	)
	if normalizedToken != nil && *normalizedToken != "" {
		if s.keyMaterial == "" {
			return UpdaterPolicy{}, SecretStatus{}, ErrSecretKeyRequired
		}
		tokenCiphertext, tokenNonce, err = security.EncryptSecret(*normalizedToken, s.keyMaterial)
		if err != nil {
			return UpdaterPolicy{}, SecretStatus{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	defer tx.Rollback()

	if err := saveUpdaterPolicyCAS(ctx, tx, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	var tokenStatus SecretStatus
	switch {
	case normalizedToken == nil:
		_, tokenStatus, err = readUpdaterReleaseToken(ctx, tx, s.keyMaterial)
		if errors.Is(err, ErrNotFound) {
			err = nil
			tokenStatus = SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}
		}
	case *normalizedToken == "":
		_, err = tx.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, UpdaterGitHubReleaseTokenSecretName)
		tokenStatus = SecretStatus{
			Name:      UpdaterGitHubReleaseTokenSecretName,
			UpdatedAt: normalized.UpdatedAt.Format(time.RFC3339),
		}
	default:
		tokenStatus = SecretStatus{
			Name:        UpdaterGitHubReleaseTokenSecretName,
			Configured:  true,
			Fingerprint: security.SecretFingerprint(*normalizedToken),
			UpdatedAt:   normalized.UpdatedAt.Format(time.RFC3339),
		}
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO secrets (name, ciphertext, nonce, value_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE ciphertext = VALUES(ciphertext), nonce = VALUES(nonce), value_hash = VALUES(value_hash), updated_at = VALUES(updated_at)`,
			UpdaterGitHubReleaseTokenSecretName,
			tokenCiphertext,
			tokenNonce,
			tokenStatus.Fingerprint,
			normalized.UpdatedAt,
		)
	}
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	return normalized, tokenStatus, nil
}

func (s MariaDBUpdaterPolicyStore) SavePullUpdaterPolicy(
	ctx context.Context,
	executionHosts SystemUpdateExecutionHostStore,
	serviceID string,
	expectedRevision, expectedOwnershipEpoch int64,
	input UpdaterPolicy,
) (UpdaterPolicy, error) {
	if expectedOwnershipEpoch < 0 {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if normalized.TransportMode != SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionStoreMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	defer tx.Rollback()

	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		normalized.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		ownership = syntheticSystemUpdateExecutionHost(normalized.ExecutionHostID)
		err = nil
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if ownership.OwnershipEpoch != expectedOwnershipEpoch {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	activePullOwner := ownership.TransportMode == SystemUpdateTransportPullV2
	switch ownership.TransportMode {
	case SystemUpdateTransportSSHV1:
		// Observer policy: preserve the SSH ownership row and its revision.
	case SystemUpdateTransportPullV2:
		if ownership.ExecutionHostID != normalized.ExecutionHostID ||
			ownership.AgentServiceID != normalized.UpdaterID {
			return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
		}
		if ownership.OwnershipEpoch <= 0 {
			return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
		}
		var activeJobID string
		err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, normalized.ExecutionHostID).Scan(&activeJobID)
		if err == nil {
			return UpdaterPolicy{}, ErrSystemUpdateExecutionHostBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return UpdaterPolicy{}, err
		}
		var activeRotationID string
		err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, normalized.ExecutionHostID).Scan(&activeRotationID)
		if err == nil {
			return UpdaterPolicy{}, ErrSystemUpdateRuntimeTokenRotationBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return UpdaterPolicy{}, err
		}
	default:
		return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
	}

	var (
		currentPolicyRevision                    int64
		currentPolicyProjectionRevision          int64
		currentPolicyLocalExecutorPolicyRevision int64
		currentPolicyBody                        []byte
		currentPolicyUpdatedAt                   time.Time
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		normalized.UpdaterID,
	).Scan(
		&currentPolicyRevision,
		&currentPolicyProjectionRevision,
		&currentPolicyLocalExecutorPolicyRevision,
		&currentPolicyBody,
		&currentPolicyUpdatedAt,
	)
	currentPolicyExists := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if (!currentPolicyExists && expectedRevision != 0) ||
		(currentPolicyExists && currentPolicyRevision != expectedRevision) {
		return UpdaterPolicy{}, ErrConflict
	}
	if currentPolicyExists {
		currentPolicy, decodeErr := decodeUpdaterPolicyRevisions(
			normalized.UpdaterID,
			currentPolicyRevision,
			currentPolicyProjectionRevision,
			currentPolicyLocalExecutorPolicyRevision,
			currentPolicyBody,
			currentPolicyUpdatedAt,
		)
		if decodeErr != nil {
			return UpdaterPolicy{}, decodeErr
		}
		if activePullOwner && ownership.PolicyRevision != currentPolicy.ProjectionRevision {
			return UpdaterPolicy{}, ErrConflict
		}
	} else if activePullOwner && ownership.PolicyRevision != 0 {
		return UpdaterPolicy{}, ErrConflict
	}

	if err := saveUpdaterPolicyCAS(ctx, tx, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := replaceUpdaterTargetDatabases(ctx, tx, normalized); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := replaceUpdaterTargetLocalListeners(ctx, tx, normalized); err != nil {
		return UpdaterPolicy{}, err
	}
	if !activePullOwner {
		if err := tx.Commit(); err != nil {
			return UpdaterPolicy{}, err
		}
		return normalized, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET policy_revision = ?, updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND agent_service_id = ?
  AND ownership_epoch = ?
  AND policy_revision = ?`,
		normalized.ProjectionRevision,
		normalized.UpdatedAt,
		normalized.ExecutionHostID,
		SystemUpdateTransportPullV2,
		normalized.UpdaterID,
		ownership.OwnershipEpoch,
		ownership.PolicyRevision,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if affected != 1 {
		return UpdaterPolicy{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, err
	}
	return normalized, nil
}

func (s MariaDBUpdaterPolicyStore) BindPullUpdaterConfigurePolicy(
	ctx context.Context,
	params BindPullUpdaterConfigurePolicyParams,
) (UpdaterPolicy, error) {
	params, err := normalizeBindPullUpdaterConfigurePolicyParams(params)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	defer tx.Rollback()
	var (
		revision                    int64
		projectionRevision          int64
		localExecutorPolicyRevision int64
		body                        []byte
		updatedAt                   time.Time
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		params.ServiceID,
	).Scan(
		&revision,
		&projectionRevision,
		&localExecutorPolicyRevision,
		&body,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	current, err := decodeUpdaterPolicyRevisions(
		params.ServiceID,
		revision,
		projectionRevision,
		localExecutorPolicyRevision,
		body,
		updatedAt,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetDatabases(ctx, tx, &current); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetLocalListeners(ctx, tx, &current); err != nil {
		return UpdaterPolicy{}, err
	}
	if current.TransportMode != SystemUpdateTransportPullV2 ||
		current.Revision != params.ExpectedSourcePolicyRevision ||
		current.ProjectionRevision != params.ExpectedProjectionRevision ||
		current.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision {
		return UpdaterPolicy{}, ErrConflict
	}
	if current.LocalExecutorPolicySHA256 == params.LocalExecutorPolicySHA256 {
		if err := tx.Commit(); err != nil {
			return UpdaterPolicy{}, err
		}
		return current, nil
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	current.LocalExecutorPolicySHA256 = params.LocalExecutorPolicySHA256
	current.UpdatedAt = now
	body, err = json.Marshal(current)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE update_agent_policies
SET policy_json = ?, updated_at = ?
WHERE service_id = ?
  AND revision = ?
  AND projection_revision = ?
  AND local_executor_policy_revision = ?`,
		body,
		now,
		params.ServiceID,
		params.ExpectedSourcePolicyRevision,
		params.ExpectedProjectionRevision,
		params.ExpectedLocalExecutorPolicyRevision,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if affected != 1 {
		return UpdaterPolicy{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, err
	}
	return current, nil
}

func (s MariaDBUpdaterPolicyStore) ActivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params ActivatePullUpdaterOwnershipParams,
) (ActivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeActivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	auth, ok := mariaDBAuthStoreForUpdaterPolicy(services)
	if !ok || auth.db == nil || auth.db != s.db {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	defer tx.Rollback()

	currentOwnership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	ownershipMissing := errors.Is(err, sql.ErrNoRows)
	if ownershipMissing {
		currentOwnership = syntheticSystemUpdateExecutionHost(params.ExecutionHostID)
		err = nil
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if normalizedSystemUpdateTransportMode(currentOwnership.TransportMode) != SystemUpdateTransportSSHV1 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}

	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		params.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != 0 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	token, err := selectActiveServiceTokenForUpdate(ctx, tx, service.TokenID)
	if errors.Is(err, ErrNotFound) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if token.ServiceType != "update_agent" {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}

	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeJobID)
	if err == nil {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ActivatePullUpdaterOwnershipResult{}, err
	}

	var (
		policyRevision                    int64
		policyProjectionRevision          int64
		policyLocalExecutorPolicyRevision int64
		policyBody                        []byte
		policyUpdatedAt                   time.Time
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		params.ServiceID,
	).Scan(
		&policyRevision,
		&policyProjectionRevision,
		&policyLocalExecutorPolicyRevision,
		&policyBody,
		&policyUpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	policy, err := decodeUpdaterPolicyRevisions(
		params.ServiceID,
		policyRevision,
		policyProjectionRevision,
		policyLocalExecutorPolicyRevision,
		policyBody,
		policyUpdatedAt,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if err := attachUpdaterTargetDatabases(ctx, tx, &policy); err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if err := attachUpdaterTargetLocalListeners(ctx, tx, &policy); err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if !PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	if policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 {
		return ActivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	targetIDs := make([]string, 0, len(policy.Targets))
	targetServices := make(map[string]RegisteredService, len(policy.Targets))
	controlPanelTargetUsed := false
	for _, target := range policy.Targets {
		if updaterPolicyControlPanelTarget(target) {
			if params.ControlPanelTarget == nil {
				return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
			}
			targetServices[target.ServiceID] = params.ControlPanelTarget.registeredService()
			controlPanelTargetUsed = true
			continue
		}
		targetIDs = append(targetIDs, target.ServiceID)
	}
	if params.ControlPanelTarget != nil && !controlPanelTargetUsed {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	sort.Strings(targetIDs)
	for _, targetID := range targetIDs {
		targetService, targetErr := scanService(tx.QueryRowContext(
			ctx,
			serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
			targetID,
		))
		if errors.Is(targetErr, sql.ErrNoRows) {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
		}
		if targetErr != nil {
			return ActivatePullUpdaterOwnershipResult{}, targetErr
		}
		targetServices[targetID] = targetService
	}
	if !registeredPullObserverReadyForActivation(service, policy, targetServices, time.Now().UTC()) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}
	baselineReservations, err := pullActivationBaselineReservations(
		policy,
		targetServices,
		service,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	missingBaselineReservations := make([]ServicePortReservation, 0, len(baselineReservations))
	for _, reservation := range baselineReservations {
		existing, reservationErr := scanServicePortReservation(tx.QueryRowContext(
			ctx,
			servicePortReservationSelect+`
WHERE execution_host_id = ?
  AND network_namespace = ?
  AND protocol = ?
  AND port = ?
FOR UPDATE`,
			reservation.ExecutionHostID,
			reservation.NetworkNamespace,
			reservation.Protocol,
			reservation.Port,
		))
		if errors.Is(reservationErr, sql.ErrNoRows) {
			missingBaselineReservations = append(missingBaselineReservations, reservation)
			continue
		}
		if reservationErr != nil {
			return ActivatePullUpdaterOwnershipResult{}, reservationErr
		}
		if !sameServicePortReservationOwner(existing, reservation) {
			return ActivatePullUpdaterOwnershipResult{}, ErrServicePortReserved
		}
	}

	now := time.Now().UTC()
	legacyAgentServiceID := nextSystemUpdateLegacyAgentServiceID(
		currentOwnership,
		SystemUpdateTransportPullV2,
		params.ServiceID,
	)
	if legacyAgentServiceID == "" && ownershipMissing {
		legacyAgentServiceID, err = uniqueActiveMariaDBLegacyUpdaterForHost(
			ctx,
			tx,
			params.ExecutionHostID,
			policy,
		)
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
	}
	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID:      params.ExecutionHostID,
		TransportMode:        SystemUpdateTransportPullV2,
		AgentServiceID:       params.ServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:       policy.ProjectionRevision,
		CreatedAt:            currentOwnership.CreatedAt,
		UpdatedAt:            now,
	}
	if ownershipMissing {
		nextOwnership.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO system_update_execution_hosts
(execution_host_id, transport_mode, agent_service_id, legacy_agent_service_id, ownership_epoch, policy_revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			nextOwnership.ExecutionHostID,
			nextOwnership.TransportMode,
			nextOwnership.AgentServiceID,
			nullString(nextOwnership.LegacyAgentServiceID),
			nextOwnership.OwnershipEpoch,
			nextOwnership.PolicyRevision,
			nextOwnership.CreatedAt,
			nextOwnership.UpdatedAt,
		)
		if isDuplicateKeyError(err) {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET transport_mode = ?, agent_service_id = ?, legacy_agent_service_id = ?, ownership_epoch = ?, policy_revision = ?, updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND ownership_epoch = ?`,
			nextOwnership.TransportMode,
			nextOwnership.AgentServiceID,
			nullString(nextOwnership.LegacyAgentServiceID),
			nextOwnership.OwnershipEpoch,
			nextOwnership.PolicyRevision,
			nextOwnership.UpdatedAt,
			nextOwnership.ExecutionHostID,
			SystemUpdateTransportSSHV1,
			params.ExpectedExecutionHostOwnershipEpoch,
		)
		if err == nil {
			var affected int64
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
			}
		}
	}
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}

	result, err := tx.ExecContext(ctx, `UPDATE services
SET ownership_epoch = ?, updated_at = ?
WHERE service_id = ?
  AND service_type = 'update_agent'
  AND transport_mode = ?
  AND execution_host_id = ?
  AND ownership_epoch = 0`,
		nextOwnership.OwnershipEpoch,
		now,
		params.ServiceID,
		SystemUpdateTransportPullV2,
		params.ExecutionHostID,
	)
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service.OwnershipEpoch = nextOwnership.OwnershipEpoch
	service.UpdatedAt = now
	reportedConfigDigests := updaterPolicyCapabilityStringMap(
		service.ReportedCapabilities["reported_config_sha256"],
	)
	for serviceID, targetService := range targetServices {
		if targetService.AppliedConfigSHA256 != "" {
			continue
		}
		result, err = tx.ExecContext(ctx, `UPDATE services
SET applied_config_sha256 = ?, updated_at = ?
WHERE service_id = ?
  AND applied_config_revision = ?
  AND applied_config_sha256 IS NULL`,
			reportedConfigDigests[serviceID],
			now,
			serviceID,
			targetService.AppliedConfigRevision,
		)
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
		if affected != 1 {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
		}
	}
	for _, reservation := range missingBaselineReservations {
		_, err = tx.ExecContext(ctx, `INSERT INTO service_port_reservations
(execution_host_id, network_namespace, protocol, port, service_id, service_role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			reservation.ExecutionHostID,
			reservation.NetworkNamespace,
			reservation.Protocol,
			reservation.Port,
			reservation.ServiceID,
			reservation.ServiceRole,
			now,
			now,
		)
		if isDuplicateKeyError(err) {
			return ActivatePullUpdaterOwnershipResult{}, ErrServicePortReserved
		}
		if err != nil {
			return ActivatePullUpdaterOwnershipResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return ActivatePullUpdaterOwnershipResult{}, err
	}
	return ActivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    policy,
	}, nil
}

func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership(
	ctx context.Context,
	services ServiceRegistryStore,
	executionHosts SystemUpdateExecutionHostStore,
	params DeactivatePullUpdaterOwnershipParams,
) (DeactivatePullUpdaterOwnershipResult, error) {
	params, err := normalizeDeactivatePullUpdaterOwnershipParams(params)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	auth, ok := mariaDBAuthStoreForUpdaterPolicy(services)
	if !ok || auth.db == nil || auth.db != s.db {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionStoreMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	defer tx.Rollback()

	currentOwnership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		params.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if currentOwnership.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	if currentOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		currentOwnership.AgentServiceID != params.ServiceID ||
		currentOwnership.OwnershipEpoch <= 0 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	legacyAgentServiceID := strings.TrimSpace(currentOwnership.LegacyAgentServiceID)
	if !updaterPolicyIdentifierPattern.MatchString(legacyAgentServiceID) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}

	service, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		params.ServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if service.ServiceType != "update_agent" ||
		service.TransportMode != SystemUpdateTransportPullV2 ||
		service.ExecutionHostID != params.ExecutionHostID ||
		service.OwnershipEpoch != currentOwnership.OwnershipEpoch {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	token, err := selectActiveServiceTokenForUpdate(ctx, tx, service.TokenID)
	if errors.Is(err, ErrNotFound) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if token.ServiceType != "update_agent" {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	recoveryPending, recoveryStateKnown := service.ReportedCapabilities["recovery_pending"].(bool)
	if !recoveryStateKnown || recoveryPending {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentNotReady
	}

	var activeID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_host_self_updates
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateHostSelfUpdateBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, params.ExecutionHostID).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	now := time.Now().UTC()
	err = tx.QueryRowContext(ctx, `SELECT grant_row.id
FROM system_update_mutation_grants AS grant_row
INNER JOIN system_update_jobs AS job_row ON job_row.id = grant_row.job_id
WHERE job_row.execution_host_id = ?
  AND grant_row.expires_at > ?
ORDER BY grant_row.created_at ASC
LIMIT 1
FOR UPDATE`, params.ExecutionHostID, now).Scan(&activeID)
	if err == nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}

	policy, err := mariaDBUpdaterPolicyForUpdate(ctx, tx, params.ServiceID)
	if errors.Is(err, ErrNotFound) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if policy.Revision != params.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != params.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != params.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 ||
		currentOwnership.PolicyRevision != policy.ProjectionRevision {
		return DeactivatePullUpdaterOwnershipResult{}, ErrConflict
	}

	legacyService, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
		legacyAgentServiceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if legacyService.ServiceType != "update_agent" ||
		normalizedSystemUpdateTransportMode(legacyService.TransportMode) != SystemUpdateTransportSSHV1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	legacyToken, err := selectActiveServiceTokenForUpdate(
		ctx,
		tx,
		legacyService.TokenID,
	)
	if errors.Is(err, ErrNotFound) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if legacyToken.ServiceType != "update_agent" ||
		validateRequiredUpdateAgentScopes(
			legacyToken.ServiceType,
			legacyToken.Scopes,
		) != nil {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentInactive
	}
	legacyPolicy, err := mariaDBUpdaterPolicyForUpdate(
		ctx,
		tx,
		legacyAgentServiceID,
	)
	if errors.Is(err, ErrNotFound) ||
		(err == nil && !legacyUpdaterPolicyCoversPullPolicy(
			legacyPolicy,
			policy,
			params.ExecutionHostID,
		)) {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}

	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID:      params.ExecutionHostID,
		TransportMode:        SystemUpdateTransportSSHV1,
		AgentServiceID:       legacyAgentServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:       legacyPolicy.ProjectionRevision,
		CreatedAt:            currentOwnership.CreatedAt,
		UpdatedAt:            now,
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET transport_mode = ?,
    agent_service_id = ?,
    legacy_agent_service_id = ?,
    ownership_epoch = ?,
    policy_revision = ?,
    updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND agent_service_id = ?
  AND legacy_agent_service_id = ?
  AND ownership_epoch = ?`,
		nextOwnership.TransportMode,
		nextOwnership.AgentServiceID,
		nextOwnership.LegacyAgentServiceID,
		nextOwnership.OwnershipEpoch,
		nextOwnership.PolicyRevision,
		nextOwnership.UpdatedAt,
		nextOwnership.ExecutionHostID,
		SystemUpdateTransportPullV2,
		params.ServiceID,
		legacyAgentServiceID,
		params.ExpectedExecutionHostOwnershipEpoch,
	)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostStale
	}
	result, err = tx.ExecContext(ctx, `UPDATE services
SET ownership_epoch = 0, updated_at = ?
WHERE service_id = ?
  AND service_type = 'update_agent'
  AND transport_mode = ?
  AND execution_host_id = ?
  AND ownership_epoch = ?`,
		now,
		params.ServiceID,
		SystemUpdateTransportPullV2,
		params.ExecutionHostID,
		params.ExpectedExecutionHostOwnershipEpoch,
	)
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	if affected != 1 {
		return DeactivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	service.OwnershipEpoch = 0
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return DeactivatePullUpdaterOwnershipResult{}, err
	}
	return DeactivatePullUpdaterOwnershipResult{
		Service:   service,
		Ownership: nextOwnership,
		Policy:    policy,
	}, nil
}

func mariaDBUpdaterPolicyForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	serviceID string,
) (UpdaterPolicy, error) {
	var (
		revision                    int64
		projectionRevision          int64
		localExecutorPolicyRevision int64
		body                        []byte
		updatedAt                   time.Time
	)
	err := tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		serviceID,
	).Scan(
		&revision,
		&projectionRevision,
		&localExecutorPolicyRevision,
		&body,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	return decodeUpdaterPolicyRevisions(
		serviceID,
		revision,
		projectionRevision,
		localExecutorPolicyRevision,
		body,
		updatedAt,
	)
}

func uniqueActiveMariaDBLegacyUpdaterForHost(
	ctx context.Context,
	tx *sql.Tx,
	executionHostID string,
	pullPolicy UpdaterPolicy,
) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT policy.service_id,
       token.scopes,
       policy.revision,
       policy.projection_revision,
       policy.local_executor_policy_revision,
       policy.policy_json,
       policy.updated_at
FROM update_agent_policies AS policy
INNER JOIN services AS service ON service.service_id = policy.service_id
INNER JOIN service_tokens AS token ON token.id = service.token_id
WHERE service.service_type = 'update_agent'
  AND service.transport_mode = ?
  AND token.service_type = 'update_agent'
  AND token.revoked_at IS NULL
ORDER BY policy.service_id
FOR UPDATE`, SystemUpdateTransportSSHV1)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	candidates := make([]string, 0, 2)
	for rows.Next() {
		var (
			serviceID                   string
			tokenScopesJSON             string
			revision                    int64
			projectionRevision          int64
			localExecutorPolicyRevision int64
			body                        []byte
			updatedAt                   time.Time
		)
		if err := rows.Scan(
			&serviceID,
			&tokenScopesJSON,
			&revision,
			&projectionRevision,
			&localExecutorPolicyRevision,
			&body,
			&updatedAt,
		); err != nil {
			return "", err
		}
		policy, err := decodeUpdaterPolicyRevisions(
			serviceID,
			revision,
			projectionRevision,
			localExecutorPolicyRevision,
			body,
			updatedAt,
		)
		if err != nil {
			return "", err
		}
		var tokenScopes []string
		if err := json.Unmarshal([]byte(tokenScopesJSON), &tokenScopes); err != nil {
			return "", err
		}
		if validateRequiredUpdateAgentScopes("update_agent", tokenScopes) == nil &&
			legacyUpdaterPolicyCoversPullPolicy(
				policy,
				pullPolicy,
				executionHostID,
			) {
			candidates = append(candidates, serviceID)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(candidates) != 1 {
		return "", nil
	}
	return candidates[0], nil
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error) {
	_, status, err := readUpdaterReleaseToken(ctx, s.db, s.keyMaterial)
	if errors.Is(err, ErrNotFound) {
		return SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}, nil
	}
	return status, err
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterReleaseTokenValue(ctx context.Context) (string, error) {
	value, _, err := readUpdaterReleaseToken(ctx, s.db, s.keyMaterial)
	return value, err
}

type updaterPolicyExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type updaterPolicyQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type updaterPolicyRowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func attachUpdaterTargetDatabases(
	ctx context.Context,
	queryer updaterPolicyRowsQueryer,
	policy *UpdaterPolicy,
) error {
	if policy == nil || queryer == nil {
		return ErrInvalidSettings
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT p.revision AS current_policy_revision,
       d.target_id,
       d.binding_policy_revision,
       d.database_name
FROM update_agent_policies AS p
LEFT JOIN update_agent_target_databases AS d
  ON d.updater_service_id = p.service_id
WHERE p.service_id = ?
ORDER BY d.target_id`,
		policy.UpdaterID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	targetIndexes := make(map[string]int, len(policy.Targets))
	for index := range policy.Targets {
		targetIndexes[policy.Targets[index].TargetID] = index
	}
	databases := make(map[int]string, len(policy.Targets))
	snapshotFound := false
	for rows.Next() {
		var (
			currentPolicyRevision int64
			targetID              sql.NullString
			bindingPolicyRevision sql.NullInt64
			databaseName          sql.NullString
		)
		if err := rows.Scan(
			&currentPolicyRevision,
			&targetID,
			&bindingPolicyRevision,
			&databaseName,
		); err != nil {
			return err
		}
		snapshotFound = true
		if currentPolicyRevision != policy.Revision {
			return errUpdaterPolicySnapshotChanged
		}
		if !targetID.Valid && !bindingPolicyRevision.Valid && !databaseName.Valid {
			continue
		}
		if !targetID.Valid || !bindingPolicyRevision.Valid || !databaseName.Valid {
			return ErrInvalidSettings
		}
		if bindingPolicyRevision.Int64 != policy.Revision {
			continue
		}
		index, exists := targetIndexes[targetID.String]
		if !exists ||
			!updaterPolicyTargetRequiresDatabase(policy.Targets[index]) ||
			!updaterPolicyDatabaseNamePattern.MatchString(databaseName.String) {
			continue
		}
		databases[index] = databaseName.String
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !snapshotFound {
		return errUpdaterPolicySnapshotChanged
	}
	candidate := cloneUpdaterPolicy(*policy)
	for index := range candidate.Targets {
		candidate.Targets[index].DatabaseName = databases[index]
	}
	*policy = candidate
	return nil
}

func replaceUpdaterTargetDatabases(
	ctx context.Context,
	execer updaterPolicyExecer,
	policy UpdaterPolicy,
) error {
	if execer == nil ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.Revision < 1 ||
		!PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return ErrInvalidSettings
	}
	if _, err := execer.ExecContext(
		ctx,
		`DELETE FROM update_agent_target_databases WHERE updater_service_id = ?`,
		policy.UpdaterID,
	); err != nil {
		return err
	}
	for _, target := range policy.Targets {
		if !updaterPolicyTargetRequiresDatabase(target) {
			continue
		}
		if _, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_target_databases
(updater_service_id, target_id, binding_policy_revision, database_name, updated_at)
VALUES (?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			target.TargetID,
			policy.Revision,
			target.DatabaseName,
			policy.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func attachUpdaterTargetLocalListeners(
	ctx context.Context,
	queryer updaterPolicyRowsQueryer,
	policy *UpdaterPolicy,
) error {
	if policy == nil || queryer == nil {
		return ErrInvalidSettings
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT p.revision AS current_policy_revision,
       listener.target_id,
       listener.binding_policy_revision,
       listener.local_listen_port
FROM update_agent_policies AS p
LEFT JOIN update_agent_target_local_listeners AS listener
  ON listener.updater_service_id = p.service_id
WHERE p.service_id = ?
ORDER BY listener.target_id`,
		policy.UpdaterID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	targetIndexes := make(map[string]int, len(policy.Targets))
	for index := range policy.Targets {
		targetIndexes[policy.Targets[index].TargetID] = index
	}
	listeners := make(map[int]int, len(policy.Targets))
	snapshotFound := false
	for rows.Next() {
		var (
			currentPolicyRevision int64
			targetID              sql.NullString
			bindingPolicyRevision sql.NullInt64
			localListenPort       sql.NullInt64
		)
		if err := rows.Scan(
			&currentPolicyRevision,
			&targetID,
			&bindingPolicyRevision,
			&localListenPort,
		); err != nil {
			return err
		}
		snapshotFound = true
		if currentPolicyRevision != policy.Revision {
			return errUpdaterPolicySnapshotChanged
		}
		if !targetID.Valid && !bindingPolicyRevision.Valid && !localListenPort.Valid {
			continue
		}
		if !targetID.Valid || !bindingPolicyRevision.Valid || !localListenPort.Valid {
			return ErrInvalidSettings
		}
		if bindingPolicyRevision.Int64 != policy.Revision {
			continue
		}
		index, exists := targetIndexes[targetID.String]
		if !exists ||
			localListenPort.Int64 < 1024 ||
			localListenPort.Int64 > 65535 ||
			!updaterPolicyTargetAllowsExplicitLocalListener(policy.Targets[index]) {
			return ErrInvalidSettings
		}
		listeners[index] = int(localListenPort.Int64)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !snapshotFound {
		return errUpdaterPolicySnapshotChanged
	}
	candidate := cloneUpdaterPolicy(*policy)
	for index := range candidate.Targets {
		candidate.Targets[index].LocalListenPort = listeners[index]
	}
	*policy = candidate
	return nil
}

func replaceUpdaterTargetLocalListeners(
	ctx context.Context,
	execer updaterPolicyExecer,
	policy UpdaterPolicy,
) error {
	if execer == nil ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.Revision < 1 {
		return ErrInvalidSettings
	}
	for _, target := range policy.Targets {
		if target.LocalListenPort == 0 {
			continue
		}
		if target.LocalListenPort < 1024 ||
			target.LocalListenPort > 65535 ||
			!updaterPolicyTargetAllowsExplicitLocalListener(target) {
			return ErrInvalidSettings
		}
	}
	if _, err := execer.ExecContext(
		ctx,
		`DELETE FROM update_agent_target_local_listeners WHERE updater_service_id = ?`,
		policy.UpdaterID,
	); err != nil {
		return err
	}
	for _, target := range policy.Targets {
		if target.LocalListenPort == 0 {
			continue
		}
		if _, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_target_local_listeners
(updater_service_id, target_id, binding_policy_revision, local_listen_port, updated_at)
VALUES (?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			target.TargetID,
			policy.Revision,
			target.LocalListenPort,
			policy.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func prepareUpdaterPolicySave(serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, []byte, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, nil, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = normalized.Revision
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		normalized.LocalExecutorPolicyRevision = normalized.Revision
	} else {
		normalized.LocalExecutorPolicyRevision = 0
	}
	normalized.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(normalized)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	return normalized, body, nil
}

func normalizeActivatePullUpdaterOwnershipParams(params ActivatePullUpdaterOwnershipParams) (ActivatePullUpdaterOwnershipParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(params.ExpectedLocalExecutorPolicySHA256))
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.ExpectedExecutionHostOwnershipEpoch < 0 ||
		params.ExpectedExecutionHostOwnershipEpoch == math.MaxInt64 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.ExpectedLocalExecutorPolicySHA256) {
		return ActivatePullUpdaterOwnershipParams{}, ErrInvalidSettings
	}
	controlPanelTarget, err := normalizePullUpdaterControlPanelTarget(params.ControlPanelTarget)
	if err != nil {
		return ActivatePullUpdaterOwnershipParams{}, err
	}
	params.ControlPanelTarget = controlPanelTarget
	return params, nil
}

func normalizePullUpdaterControlPanelTarget(
	input *PullUpdaterControlPanelTarget,
) (*PullUpdaterControlPanelTarget, error) {
	if input == nil {
		return nil, nil
	}
	target := *input
	target.ServiceID = strings.TrimSpace(target.ServiceID)
	target.ServiceType = strings.TrimSpace(target.ServiceType)
	target.AppliedConfigSHA256 = strings.ToLower(strings.TrimSpace(target.AppliedConfigSHA256))
	target.AppliedEndpoint.Host = strings.TrimSpace(target.AppliedEndpoint.Host)
	target.AppliedEndpoint.PublicURL = strings.TrimSpace(target.AppliedEndpoint.PublicURL)
	if target.ServiceID != "control-panel" ||
		target.ServiceType != "control_panel" ||
		target.EndpointRevision < 1 ||
		target.AppliedConfigRevision < 1 ||
		target.AppliedConfigSHA256 == "" ||
		!validSystemUpdateDigest(target.AppliedConfigSHA256) ||
		target.AppliedEndpoint.Host != "127.0.0.1" ||
		target.AppliedEndpoint.Port < 1024 ||
		target.AppliedEndpoint.Port > 65535 ||
		target.AppliedEndpoint.SSLEnabled {
		return nil, ErrInvalidSettings
	}
	canonicalPublicURL := buildServiceURL(
		target.AppliedEndpoint.Host,
		target.AppliedEndpoint.Port,
		false,
	)
	if target.AppliedEndpoint.PublicURL != "" &&
		target.AppliedEndpoint.PublicURL != canonicalPublicURL {
		return nil, ErrInvalidSettings
	}
	target.AppliedEndpoint.PublicURL = canonicalPublicURL
	return &target, nil
}

func updaterPolicyControlPanelTarget(target UpdaterPolicyTarget) bool {
	return target.TargetID == "control-panel" &&
		target.ServiceID == "control-panel" &&
		target.ServiceType == "control_panel" &&
		target.DeploymentMode == "systemd"
}

func updaterPolicyTargetAllowsExplicitLocalListener(target UpdaterPolicyTarget) bool {
	return target.DeploymentMode == "systemd" &&
		!updaterPolicyControlPanelTarget(target)
}

// PullUpdaterPolicyTargetLocalListenPort resolves the local host listener used
// by a systemd pull target. An explicit revision-bound listener wins. Zero
// preserves the legacy AppliedEndpoint fallback; a present but invalid
// override never silently falls back. The exact synthetic Control Panel target
// cannot be overridden because its loopback listener is server-owned.
func PullUpdaterPolicyTargetLocalListenPort(
	target UpdaterPolicyTarget,
	service RegisteredService,
) (int, bool) {
	if target.DeploymentMode != "systemd" {
		return 0, false
	}
	if updaterPolicyControlPanelTarget(target) {
		if target.LocalListenPort != 0 {
			return 0, false
		}
	} else if target.LocalListenPort != 0 {
		if target.LocalListenPort < 1024 || target.LocalListenPort > 65535 {
			return 0, false
		}
		return target.LocalListenPort, true
	}
	if service.AppliedEndpoint == nil ||
		service.AppliedEndpoint.Port < 1024 ||
		service.AppliedEndpoint.Port > 65535 {
		return 0, false
	}
	return service.AppliedEndpoint.Port, true
}

func normalizeDeactivatePullUpdaterOwnershipParams(
	params DeactivatePullUpdaterOwnershipParams,
) (DeactivatePullUpdaterOwnershipParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(params.ExpectedLocalExecutorPolicySHA256),
	)
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.ExpectedExecutionHostOwnershipEpoch < 1 ||
		params.ExpectedExecutionHostOwnershipEpoch == math.MaxInt64 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.ExpectedLocalExecutorPolicySHA256) {
		return DeactivatePullUpdaterOwnershipParams{}, ErrInvalidSettings
	}
	return params, nil
}

func updaterPolicyMapsExecutionHost(policy UpdaterPolicy, executionHostID string) bool {
	if policy.TransportMode != SystemUpdateTransportSSHV1 {
		return false
	}
	hostFound := false
	for _, host := range policy.Hosts {
		if host.HostID == executionHostID {
			hostFound = true
			break
		}
	}
	if !hostFound {
		return false
	}
	for _, target := range policy.Targets {
		if target.HostID == executionHostID {
			return true
		}
	}
	return false
}

func legacyUpdaterPolicyCoversPullPolicy(
	legacyPolicy UpdaterPolicy,
	pullPolicy UpdaterPolicy,
	executionHostID string,
) bool {
	if !updaterPolicyMapsExecutionHost(legacyPolicy, executionHostID) ||
		pullPolicy.TransportMode != SystemUpdateTransportPullV2 ||
		pullPolicy.ExecutionHostID != executionHostID ||
		len(pullPolicy.Targets) == 0 {
		return false
	}
	for _, pullTarget := range pullPolicy.Targets {
		covered := false
		for _, legacyTarget := range legacyPolicy.Targets {
			if legacyTarget.HostID == executionHostID &&
				legacyTarget.TargetID == pullTarget.TargetID &&
				legacyTarget.ServiceID == pullTarget.ServiceID &&
				legacyTarget.ServiceType == pullTarget.ServiceType &&
				legacyTarget.DeploymentMode == pullTarget.DeploymentMode {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func uniqueActiveMemoryLegacyUpdaterForHostLocked(
	policies *MemoryUpdaterPolicyStore,
	registry *MemoryAuthStore,
	executionHostID string,
	pullPolicy UpdaterPolicy,
) string {
	candidate := ""
	for serviceID, policy := range policies.policies {
		if !legacyUpdaterPolicyCoversPullPolicy(
			policy,
			pullPolicy,
			executionHostID,
		) {
			continue
		}
		service, exists := registry.services[serviceID]
		if !exists ||
			service.ServiceType != "update_agent" ||
			normalizedSystemUpdateTransportMode(service.TransportMode) != SystemUpdateTransportSSHV1 {
			continue
		}
		token, exists := registry.serviceTokens[service.TokenID]
		if !exists ||
			token.ServiceType != "update_agent" ||
			token.RevokedAt != nil ||
			validateRequiredUpdateAgentScopes(
				token.ServiceType,
				token.Scopes,
			) != nil {
			continue
		}
		if candidate != "" {
			return ""
		}
		candidate = serviceID
	}
	return candidate
}

func unsettledMemorySystemUpdateMutationGrantForHostLocked(
	updates *MemorySystemUpdateStore,
	executionHostID string,
	now time.Time,
) bool {
	for _, grant := range updates.mutationGrants {
		if !grant.ExpiresAt.After(now) {
			continue
		}
		job, exists := updates.jobs[grant.JobID]
		if exists && job.ExecutionHostID == executionHostID {
			return true
		}
	}
	return false
}

func normalizeBindPullUpdaterConfigurePolicyParams(
	params BindPullUpdaterConfigurePolicyParams,
) (BindPullUpdaterConfigurePolicyParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.LocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(params.LocalExecutorPolicySHA256),
	)
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.LocalExecutorPolicySHA256) {
		return BindPullUpdaterConfigurePolicyParams{}, ErrInvalidSettings
	}
	return params, nil
}

func registeredPullObserverReadyForActivation(
	service RegisteredService,
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	now time.Time,
) bool {
	if !PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(service.Status), "online") ||
		service.LastHeartbeatAt == nil ||
		service.LastHeartbeatAt.IsZero() {
		return false
	}
	heartbeatAge := now.Sub(service.LastHeartbeatAt.UTC())
	if heartbeatAge < 0 || heartbeatAge > pullUpdaterActivationHeartbeatMaxAge {
		return false
	}
	capabilities := service.ReportedCapabilities
	reportedEpoch, epochOK := updaterPolicyCapabilityInt64(capabilities["ownership_epoch"])
	reportedPolicyRevision, revisionOK := updaterPolicyCapabilityInt64(capabilities["policy_revision"])
	recoveryPending, recoveryPendingOK := capabilities["recovery_pending"].(bool)
	if !updaterPolicyCapabilityBool(capabilities["host_agent"]) ||
		!updaterPolicyCapabilityBool(capabilities["observe_only"]) ||
		!updaterPolicyCapabilityBool(capabilities["update_executor"]) ||
		updaterPolicyCapabilityBool(capabilities["mutation_enabled"]) ||
		!recoveryPendingOK ||
		recoveryPending ||
		updaterPolicyCapabilityString(capabilities["transport_mode"]) != SystemUpdateTransportPullV2 ||
		updaterPolicyCapabilityString(capabilities["agent_protocol_version"]) != "2" ||
		updaterPolicyCapabilityString(capabilities["execution_host_id"]) != service.ExecutionHostID ||
		!epochOK ||
		reportedEpoch != 0 ||
		!revisionOK ||
		reportedPolicyRevision != policy.ProjectionRevision ||
		!strings.EqualFold(updaterPolicyCapabilityString(capabilities["policy_status"]), "applied") {
		return false
	}

	availability := updaterPolicyCapabilityStringMap(capabilities["target_availability"])
	availabilityCodes := updaterPolicyCapabilityStringMap(capabilities["target_availability_codes"])
	reportedPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_ports"])
	reportedServiceTypes := updaterPolicyCapabilityStringMap(capabilities["reported_service_types"])
	reportedDeploymentModes := updaterPolicyCapabilityStringMap(capabilities["reported_deployment_modes"])
	reportedPolicyRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := updaterPolicyCapabilityStringMap(capabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_config_revisions"])
	reportedConfigDigests := updaterPolicyCapabilityStringMap(capabilities["reported_config_sha256"])
	reportedPortDrift := updaterPolicyCapabilityBoolMap(capabilities["port_drift"])
	for _, target := range policy.Targets {
		targetService, exists := servicesByID[target.ServiceID]
		if !exists || targetService.ServiceType != target.ServiceType || targetService.AppliedEndpoint == nil {
			return false
		}
		expectedReportedPort := targetService.AppliedEndpoint.Port
		if target.DeploymentMode == "systemd" {
			var listenerOK bool
			expectedReportedPort, listenerOK = PullUpdaterPolicyTargetLocalListenPort(
				target,
				targetService,
			)
			if !listenerOK {
				return false
			}
		}
		expectedConfigRevision := targetService.AppliedConfigRevision
		if expectedConfigRevision < 1 {
			return false
		}
		portDrift, portDriftReported := reportedPortDrift[target.ServiceID]
		reportedConfigDigest := reportedConfigDigests[target.ServiceID]
		if availability[target.ServiceID] != "available" ||
			availabilityCodes[target.ServiceID] != "executor_verified" ||
			reportedServiceTypes[target.ServiceID] != target.ServiceType ||
			strings.ToLower(reportedDeploymentModes[target.ServiceID]) != target.DeploymentMode ||
			reportedPolicyRevisions[target.ServiceID] != policy.LocalExecutorPolicyRevision ||
			reportedPolicyDigests[target.ServiceID] != policy.LocalExecutorPolicySHA256 ||
			reportedConfigRevisions[target.ServiceID] != expectedConfigRevision ||
			!validSystemUpdateDigest(reportedConfigDigest) ||
			(targetService.AppliedConfigSHA256 != "" &&
				reportedConfigDigest != targetService.AppliedConfigSHA256) ||
			reportedPorts[target.ServiceID] != int64(expectedReportedPort) ||
			!portDriftReported ||
			portDrift {
			return false
		}
		if target.DeploymentMode == "docker" {
			if _, complete := systemUpdateDockerPortBaselineFromAgent(
				service,
				policy,
				targetService,
			); !complete {
				return false
			}
		}
	}
	return len(policy.Targets) > 0
}

// PullUpdaterObserverReadyForActivation reports whether the latest registered
// observer heartbeat exactly matches the server-owned pull policy projection.
// It intentionally excludes execution-host ownership and active-job checks,
// which are fenced again inside ActivatePullUpdaterOwnership.
func PullUpdaterObserverReadyForActivation(
	service RegisteredService,
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	now time.Time,
) bool {
	return registeredPullObserverReadyForActivation(service, policy, servicesByID, now)
}

func pullActivationBaselineReservations(
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	observer RegisteredService,
) ([]ServicePortReservation, error) {
	reservations := make([]ServicePortReservation, 0, len(policy.Targets))
	seen := make(map[servicePortReservationKey]ServicePortReservation, len(policy.Targets))
	for _, target := range policy.Targets {
		// service_port_reservations is deliberately owned by rows in services.
		// Control Panel is a server-owned synthetic target and cannot satisfy
		// that foreign key. Its fixed listener is fenced against systemd and
		// Docker host-port changes at job creation and verified by Host Agent.
		if updaterPolicyControlPanelTarget(target) {
			continue
		}
		service, exists := servicesByID[target.ServiceID]
		if !exists || service.AppliedEndpoint == nil {
			return nil, ErrSystemUpdateAgentNotReady
		}
		port := service.AppliedEndpoint.Port
		switch target.DeploymentMode {
		case "systemd":
			var listenerOK bool
			port, listenerOK = PullUpdaterPolicyTargetLocalListenPort(target, service)
			if !listenerOK {
				return nil, ErrSystemUpdateAgentNotReady
			}
		case "docker":
			baseline, complete := systemUpdateDockerPortBaselineFromAgent(
				observer,
				policy,
				service,
			)
			if !complete {
				return nil, ErrSystemUpdateAgentNotReady
			}
			port = baseline.PublishedPort
		default:
			return nil, ErrSystemUpdateAgentNotReady
		}
		if port < 1024 || port > 65535 {
			return nil, ErrSystemUpdateAgentNotReady
		}
		reservation := ServicePortReservation{
			ExecutionHostID:  policy.ExecutionHostID,
			NetworkNamespace: "host",
			Protocol:         "tcp",
			Port:             port,
			ServiceID:        target.ServiceID,
			ServiceRole:      "api",
		}
		key := servicePortKey(reservation)
		if existing, duplicate := seen[key]; duplicate {
			if !sameServicePortReservationOwner(existing, reservation) {
				return nil, ErrServicePortReserved
			}
			continue
		}
		seen[key] = reservation
		reservations = append(reservations, reservation)
	}
	sortServicePortReservations(reservations)
	return reservations, nil
}

func updaterPolicyCapabilityBool(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func updaterPolicyCapabilityString(value any) string {
	parsed, _ := value.(string)
	return strings.TrimSpace(parsed)
}

func updaterPolicyCapabilityInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		if typed >= 0 {
			return int64(typed), true
		}
	case int64:
		if typed >= 0 {
			return typed, true
		}
	case float64:
		integer := int64(typed)
		if typed >= 0 && float64(integer) == typed {
			return integer, true
		}
	case json.Number:
		integer, err := typed.Int64()
		if err == nil && integer >= 0 {
			return integer, true
		}
	}
	return 0, false
}

func updaterPolicyCapabilityStringMap(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(string); ok {
				result[strings.TrimSpace(key)] = strings.TrimSpace(parsed)
			}
		}
	}
	return result
}

func updaterPolicyCapabilityInt64Map(value any) map[string]int64 {
	result := map[string]int64{}
	switch typed := value.(type) {
	case map[string]int:
		for key, item := range typed {
			if item >= 0 {
				result[strings.TrimSpace(key)] = int64(item)
			}
		}
	case map[string]int64:
		for key, item := range typed {
			if item >= 0 {
				result[strings.TrimSpace(key)] = item
			}
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := updaterPolicyCapabilityInt64(item); ok {
				result[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return result
}

func updaterPolicyCapabilityBoolMap(value any) map[string]bool {
	result := map[string]bool{}
	switch typed := value.(type) {
	case map[string]bool:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = item
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(bool); ok {
				result[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return result
}

func mariaDBAuthStoreForUpdaterPolicy(services ServiceRegistryStore) (MariaDBAuthStore, bool) {
	switch typed := services.(type) {
	case MariaDBAuthStore:
		return typed, true
	case *MariaDBAuthStore:
		if typed != nil {
			return *typed, true
		}
	}
	return MariaDBAuthStore{}, false
}

func saveUpdaterPolicyCAS(ctx context.Context, execer updaterPolicyExecer, expectedRevision int64, policy UpdaterPolicy, body []byte) error {
	if expectedRevision == 0 {
		_, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_policies
(service_id, revision, projection_revision, local_executor_policy_revision, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			policy.Revision,
			policy.ProjectionRevision,
			policy.LocalExecutorPolicyRevision,
			body,
			policy.UpdatedAt,
		)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrConflict
			}
			return err
		}
		return nil
	}
	result, err := execer.ExecContext(
		ctx,
		`UPDATE update_agent_policies
SET revision = ?, projection_revision = ?, local_executor_policy_revision = ?, policy_json = ?, updated_at = ?
WHERE service_id = ? AND revision = ?`,
		policy.Revision,
		policy.ProjectionRevision,
		policy.LocalExecutorPolicyRevision,
		body,
		policy.UpdatedAt,
		policy.UpdaterID,
		expectedRevision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func readUpdaterReleaseToken(ctx context.Context, queryer updaterPolicyQueryer, keyMaterial string) (string, SecretStatus, error) {
	var (
		ciphertext string
		nonce      string
		status     = SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}
		updatedAt  time.Time
	)
	err := queryer.QueryRowContext(
		ctx,
		`SELECT ciphertext, nonce, value_hash, updated_at FROM secrets WHERE name = ?`,
		UpdaterGitHubReleaseTokenSecretName,
	).Scan(&ciphertext, &nonce, &status.Fingerprint, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", status, ErrNotFound
	}
	if err != nil {
		return "", SecretStatus{}, err
	}
	if keyMaterial == "" {
		return "", SecretStatus{}, ErrSecretKeyRequired
	}
	value, err := security.DecryptSecret(ciphertext, nonce, keyMaterial)
	if err != nil {
		return "", SecretStatus{}, err
	}
	if !validStoredUpdaterReleaseToken(value) || security.SecretFingerprint(value) != status.Fingerprint {
		return "", SecretStatus{}, errUpdaterReleaseTokenIntegrity
	}
	status.Configured = true
	status.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return value, status, nil
}

func normalizeUpdaterReleaseToken(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if len(normalized) > maxUpdaterReleaseTokenBytes {
		return nil, ErrInvalidSettings
	}
	for _, char := range []byte(normalized) {
		if char < 0x21 || char > 0x7e {
			return nil, ErrInvalidSettings
		}
	}
	return &normalized, nil
}

func validStoredUpdaterReleaseToken(value string) bool {
	normalized, err := normalizeUpdaterReleaseToken(&value)
	return err == nil && normalized != nil && *normalized != "" && *normalized == value
}

func normalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicyWithDatabaseBindings(serviceID, input, true)
}

func normalizeStoredUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicyWithDatabaseBindings(serviceID, input, false)
}

func normalizeUpdaterPolicyWithDatabaseBindings(
	serviceID string,
	input UpdaterPolicy,
	requireDatabaseBindings bool,
) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	input.UpdaterID = strings.TrimSpace(input.UpdaterID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) ||
		(input.UpdaterID != "" && input.UpdaterID != serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	input.UpdaterID = serviceID
	input.TransportMode = strings.ToLower(strings.TrimSpace(input.TransportMode))
	if input.TransportMode == "" {
		input.TransportMode = SystemUpdateTransportSSHV1
	}
	input.ExecutionHostID = strings.TrimSpace(input.ExecutionHostID)
	input.LocalExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(input.LocalExecutorPolicySHA256))

	if input.PollIntervalSeconds == 0 {
		input.PollIntervalSeconds = 15
	}
	if input.HeartbeatIntervalSeconds == 0 {
		input.HeartbeatIntervalSeconds = 30
	}
	if input.PollIntervalSeconds < 5 || input.PollIntervalSeconds > 3600 ||
		input.HeartbeatIntervalSeconds < 5 || input.HeartbeatIntervalSeconds > 60 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if input.TransportMode == SystemUpdateTransportPullV2 {
		return normalizePullUpdaterPolicy(input, requireDatabaseBindings)
	}
	if input.TransportMode != SystemUpdateTransportSSHV1 ||
		input.ExecutionHostID != "" ||
		input.LocalExecutorPolicySHA256 != "" {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	input.API.BindHost = strings.Trim(strings.TrimSpace(input.API.BindHost), "[]")
	input.API.Host = strings.Trim(strings.TrimSpace(input.API.Host), "[]")
	input.API.TLSCertFile = strings.TrimSpace(input.API.TLSCertFile)
	input.API.TLSKeyFile = strings.TrimSpace(input.API.TLSKeyFile)
	if input.API.BindHost == "" {
		input.API.BindHost = "127.0.0.1"
	}
	if input.API.Host == "" {
		input.API.Host = "127.0.0.1"
	}
	if input.API.Port == 0 {
		input.API.Port = 8090
	}
	if !validUpdaterPolicyHost(input.API.BindHost) || !validUpdaterPolicyHost(input.API.Host) || input.API.Port < 1 || input.API.Port > 65535 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if input.API.SSLEnabled {
		if !safeUpdaterPolicyAbsolutePath(input.API.TLSCertFile) || !safeUpdaterPolicyAbsolutePath(input.API.TLSKeyFile) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
	} else {
		if !updaterPolicyLoopbackHost(input.API.BindHost) || !updaterPolicyLoopbackHost(input.API.Host) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		input.API.TLSCertFile = ""
		input.API.TLSKeyFile = ""
	}

	if len(input.Hosts) == 0 || len(input.Hosts) > 128 || len(input.Targets) == 0 || len(input.Targets) > 1024 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	hosts := make(map[string]bool, len(input.Hosts))
	hostReferences := make(map[string]int, len(input.Hosts))
	for i := range input.Hosts {
		host := &input.Hosts[i]
		host.HostID = strings.TrimSpace(host.HostID)
		host.Name = strings.TrimSpace(host.Name)
		host.Address = strings.Trim(strings.TrimSpace(host.Address), "[]")
		host.User = strings.TrimSpace(host.User)
		host.Arch = strings.TrimSpace(host.Arch)
		host.HostPublicKey = strings.TrimSpace(host.HostPublicKey)
		canonicalHostPublicKey, validHostPublicKey := normalizeUpdaterPolicyHostPublicKey(host.HostPublicKey)
		if !updaterPolicyHostIDPattern.MatchString(host.HostID) || hosts[host.HostID] ||
			host.Name == "" || len([]rune(host.Name)) > 128 || updaterPolicyContainsControl(host.Name) ||
			!validUpdaterPolicyHost(host.Address) || host.Port < 1 || host.Port > 65535 ||
			!updaterPolicyLinuxUserPattern.MatchString(host.User) || host.User == "root" ||
			(host.Arch != "amd64" && host.Arch != "arm64") ||
			!validHostPublicKey {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		host.HostPublicKey = canonicalHostPublicKey
		hosts[host.HostID] = true
	}

	targets := make(map[string]bool, len(input.Targets))
	for i := range input.Targets {
		target := &input.Targets[i]
		target.TargetID = strings.TrimSpace(target.TargetID)
		target.ServiceID = strings.TrimSpace(target.ServiceID)
		if target.ServiceID == "" {
			target.ServiceID = target.TargetID
		}
		target.HostID = strings.TrimSpace(target.HostID)
		target.ServiceType = strings.TrimSpace(target.ServiceType)
		target.DeploymentMode = strings.TrimSpace(target.DeploymentMode)
		if target.DatabaseName != "" || target.LocalListenPort != 0 {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if !updaterPolicyIdentifierPattern.MatchString(target.TargetID) || targets[target.TargetID] ||
			!updaterPolicyIdentifierPattern.MatchString(target.ServiceID) ||
			!updaterPolicyHostIDPattern.MatchString(target.HostID) || !hosts[target.HostID] ||
			!validUpdaterPolicyServiceType(target.ServiceType) ||
			(target.DeploymentMode != "systemd" && target.DeploymentMode != "docker") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		targets[target.TargetID] = true
		hostReferences[target.HostID]++
	}
	for hostID := range hosts {
		if hostReferences[hostID] == 0 {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
	}

	input.Revision = 0
	input.ProjectionRevision = 0
	input.LocalExecutorPolicyRevision = 0
	input.UpdatedAt = time.Time{}
	return cloneUpdaterPolicy(input), nil
}

func normalizePullUpdaterPolicy(
	input UpdaterPolicy,
	requireDatabaseBindings bool,
) (UpdaterPolicy, error) {
	if !executionHostIDPattern.MatchString(input.ExecutionHostID) ||
		!validSystemUpdateDigest(input.LocalExecutorPolicySHA256) ||
		input.API != (UpdaterPolicyAPI{}) ||
		len(input.Hosts) != 0 ||
		len(input.Targets) == 0 ||
		len(input.Targets) > 1024 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	targets := make(map[string]bool, len(input.Targets))
	serviceIDs := make(map[string]bool, len(input.Targets))
	for index := range input.Targets {
		target := &input.Targets[index]
		target.TargetID = strings.TrimSpace(target.TargetID)
		target.ServiceID = strings.TrimSpace(target.ServiceID)
		if target.ServiceID == "" {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if target.TargetID == "" {
			target.TargetID = target.ServiceID
		}
		target.HostID = strings.TrimSpace(target.HostID)
		if target.HostID == "" {
			target.HostID = input.ExecutionHostID
		}
		target.ServiceType = strings.TrimSpace(target.ServiceType)
		target.DeploymentMode = strings.TrimSpace(target.DeploymentMode)
		if !updaterPolicyIdentifierPattern.MatchString(target.TargetID) ||
			targets[target.TargetID] ||
			!updaterPolicyIdentifierPattern.MatchString(target.ServiceID) ||
			serviceIDs[target.ServiceID] ||
			target.HostID != input.ExecutionHostID ||
			!validUpdaterPolicyServiceType(target.ServiceType) ||
			(target.DeploymentMode != "systemd" && target.DeploymentMode != "docker") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		requiresDatabase := updaterPolicyTargetRequiresDatabase(*target)
		if target.ServiceType == "control_panel" &&
			(target.TargetID != "control-panel" ||
				target.ServiceID != "control-panel" ||
				target.DeploymentMode != "systemd") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if target.LocalListenPort != 0 &&
			(target.LocalListenPort < 1024 ||
				target.LocalListenPort > 65535 ||
				!updaterPolicyTargetAllowsExplicitLocalListener(*target)) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		switch {
		case requiresDatabase && requireDatabaseBindings &&
			!updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName):
			return UpdaterPolicy{}, ErrInvalidSettings
		case requiresDatabase && !requireDatabaseBindings &&
			target.DatabaseName != "" &&
			!updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName):
			return UpdaterPolicy{}, ErrInvalidSettings
		case !requiresDatabase && target.DatabaseName != "":
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		targets[target.TargetID] = true
		serviceIDs[target.ServiceID] = true
	}

	input.API = UpdaterPolicyAPI{}
	input.Hosts = nil
	input.Revision = 0
	input.ProjectionRevision = 0
	input.LocalExecutorPolicyRevision = 0
	input.UpdatedAt = time.Time{}
	return cloneUpdaterPolicy(input), nil
}

// NormalizeUpdaterPolicy validates and canonicalizes the declarative policy
// without assigning a database revision or update timestamp.
func NormalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicy(serviceID, input)
}

// PullUpdaterPolicyDatabaseBindingsReady reports whether every database-owning
// pull target has an exact, revision-bound database binding and no other target
// has one. It is safe to use on policies loaded for display: missing bindings
// remain visible as empty values so an administrator can repair them.
func PullUpdaterPolicyDatabaseBindingsReady(policy UpdaterPolicy) bool {
	if policy.TransportMode != SystemUpdateTransportPullV2 || len(policy.Targets) == 0 {
		return false
	}
	for _, target := range policy.Targets {
		if updaterPolicyTargetRequiresDatabase(target) {
			if !updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName) {
				return false
			}
			continue
		}
		if target.DatabaseName != "" {
			return false
		}
	}
	return true
}

func updaterPolicyTargetRequiresDatabase(target UpdaterPolicyTarget) bool {
	if target.DeploymentMode != "systemd" {
		return false
	}
	return target.ServiceType == "control_panel" || target.ServiceType == "observability"
}

func decodeUpdaterPolicy(serviceID string, revision int64, body []byte, updatedAt time.Time) (UpdaterPolicy, error) {
	return decodeUpdaterPolicyRevisions(serviceID, revision, revision, 0, body, updatedAt)
}

func decodeUpdaterPolicyRevisions(
	serviceID string,
	revision int64,
	projectionRevision int64,
	localExecutorPolicyRevision int64,
	body []byte,
	updatedAt time.Time,
) (UpdaterPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var policy UpdaterPolicy
	if err := decoder.Decode(&policy); err != nil {
		return UpdaterPolicy{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return UpdaterPolicy{}, errors.New("updater policy contains trailing data")
	}
	normalized, err := normalizeStoredUpdaterPolicy(serviceID, policy)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if revision < 1 ||
		projectionRevision < 1 ||
		localExecutorPolicyRevision < 0 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	normalized.Revision = revision
	normalized.ProjectionRevision = projectionRevision
	normalized.LocalExecutorPolicyRevision = localExecutorPolicyRevision
	normalized.UpdatedAt = updatedAt.UTC()
	return normalized, nil
}

func cloneUpdaterPolicy(policy UpdaterPolicy) UpdaterPolicy {
	policy.Hosts = append([]UpdaterPolicyHost(nil), policy.Hosts...)
	policy.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
	return policy
}

func validUpdaterPolicyHost(value string) bool {
	if value == "" || len(value) > 253 || updaterPolicyContainsControl(value) || strings.ContainsAny(value, " /\\@?#[]") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func updaterPolicyLoopbackHost(value string) bool {
	if strings.EqualFold(value, "localhost") {
		return true
	}
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func safeUpdaterPolicyAbsolutePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) != "/" && !updaterPolicyContainsControl(value) && !strings.ContainsRune(value, '\\')
}

func updaterPolicyContainsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func normalizeUpdaterPolicyHostPublicKey(value string) (string, bool) {
	if value == "" || len(value) > 16*1024 || updaterPolicyContainsControl(value) {
		return "", false
	}
	publicKey, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 || comment != "" || len(options) != 0 || len(rest) != 0 {
		return "", false
	}
	canonical := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(publicKey)), "\n")
	if value != canonical {
		return "", false
	}
	return canonical, true
}

func validUpdaterPolicyServiceType(value string) bool {
	switch value {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

var (
	_ UpdaterPolicyStore      = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyAdminStore = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyStore      = MariaDBUpdaterPolicyStore{}
	_ UpdaterPolicyAdminStore = MariaDBUpdaterPolicyStore{}
)
