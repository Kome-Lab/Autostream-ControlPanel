package store

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryUpdaterPolicyStore struct {
	mu       sync.Mutex
	policies map[string]UpdaterPolicy
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
	if ownership.TransportMode != SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
	}
	activePullOwner := ownership.AgentServiceID != ""
	if activePullOwner {
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
			if job.ExecutionHostID == normalized.ExecutionHostID && systemUpdateJobHoldsHost(job) {
				return UpdaterPolicy{}, ErrSystemUpdateExecutionHostBusy
			}
		}
		if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
			updates, normalized.ExecutionHostID,
		); found {
			return UpdaterPolicy{}, ErrSystemUpdateRuntimeTokenRotationBusy
		}
	} else if ownership.OwnershipEpoch != 0 || ownership.PolicyRevision != 0 {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}

	now := time.Now().UTC()
	if exists && !now.After(currentPolicy.UpdatedAt) {
		now = currentPolicy.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = 1
	normalized.LocalExecutorPolicyRevision = 1
	if exists {
		if currentPolicy.ProjectionRevision >= math.MaxInt64 || currentPolicy.LocalExecutorPolicyRevision >= math.MaxInt64 {
			return UpdaterPolicy{}, ErrConflict
		}
		normalized.ProjectionRevision = currentPolicy.ProjectionRevision + 1
		normalized.LocalExecutorPolicyRevision = currentPolicy.LocalExecutorPolicyRevision + 1
	}
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
	if currentOwnership.TransportMode != SystemUpdateTransportPullV2 ||
		(currentOwnership.AgentServiceID != "" &&
			currentOwnership.AgentServiceID != params.ServiceID) {
		return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateAgentBindingMismatch
	}
	for _, job := range updates.jobs {
		if job.ExecutionHostID == params.ExecutionHostID && systemUpdateJobHoldsHost(job) {
			return ActivatePullUpdaterOwnershipResult{}, ErrSystemUpdateExecutionHostBusy
		}
	}

	now := time.Now().UTC()
	nextOwnership := SystemUpdateExecutionHost{
		ExecutionHostID: params.ExecutionHostID,
		TransportMode:   SystemUpdateTransportPullV2,
		AgentServiceID:  params.ServiceID,
		OwnershipEpoch:  currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:  policy.ProjectionRevision,
		CreatedAt:       currentOwnership.CreatedAt,
		UpdatedAt:       now,
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
	for _, job := range updates.jobs {
		if job.ExecutionHostID == params.ExecutionHostID &&
			systemUpdateJobHoldsHost(job) {
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
		ExecutionHostID: params.ExecutionHostID,
		TransportMode:   SystemUpdateTransportPullV2,
		AgentServiceID:  params.ServiceID,
		OwnershipEpoch:  currentOwnership.OwnershipEpoch + 1,
		PolicyRevision:  policy.ProjectionRevision,
		CreatedAt:       currentOwnership.CreatedAt,
		UpdatedAt:       now,
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
