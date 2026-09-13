package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

const systemUpdateHostSelfUpdateHeartbeatStallAfter = 2 * time.Minute

func (s *MemorySystemUpdateStore) ListSystemUpdateHostSelfUpdates(
	ctx context.Context,
	limit int,
) ([]SystemUpdateHostSelfUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]SystemUpdateHostSelfUpdate, 0, len(s.hostSelfUpdates))
	for _, update := range s.hostSelfUpdates {
		result = append(result, publicSystemUpdateHostSelfUpdate(update))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemorySystemUpdateStore) GetSystemUpdateHostSelfUpdate(
	ctx context.Context,
	id string,
) (SystemUpdateHostSelfUpdate, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	id = strings.TrimSpace(id)
	if !serviceIDPattern.MatchString(id) {
		return SystemUpdateHostSelfUpdate{}, ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	update, ok := s.hostSelfUpdates[id]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	return publicSystemUpdateHostSelfUpdate(update), nil
}

func (s *MemorySystemUpdateStore) GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
	ctx context.Context,
	executionHostID string,
) (SystemUpdateHostSelfUpdate, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateHostSelfUpdate{}, ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	update, ok := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, executionHostID,
	)
	if !ok {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	return publicSystemUpdateHostSelfUpdate(update), nil
}

func (s *MemorySystemUpdateStore) CreateSystemUpdateHostSelfUpdate(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateSystemUpdateHostSelfUpdateParams,
) (SystemUpdateHostSelfUpdate, bool, error) {
	params = normalizeCreateSystemUpdateHostSelfUpdateParams(params)
	if err := validateCreateSystemUpdateHostSelfUpdateParams(params); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateStore
	}
	intent, err := systemUpdateHostSelfUpdateIntentSHA256(params)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}

	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	return createMemorySystemUpdateHostSelfUpdateLocked(
		s, registry, policyStore, params, intent,
	)
}

func (s *MemorySystemUpdateStore) RetrySystemUpdateHostSelfUpdate(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params RetrySystemUpdateHostSelfUpdateParams,
) (SystemUpdateHostSelfUpdate, bool, error) {
	params.ID = strings.TrimSpace(params.ID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	params.Now = params.Now.UTC()
	if !serviceIDPattern.MatchString(params.ID) ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 ||
		containsControl(params.IdempotencyKey) ||
		!serviceIDPattern.MatchString(params.RequestedByUserID) ||
		params.RequestedByUsername == "" ||
		params.Now.IsZero() || params.Now.Location() != time.UTC {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	previous, ok := s.hostSelfUpdates[params.ID]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	if !isTerminalSystemUpdateHostSelfUpdateStatus(previous.Status) {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateState
	}
	create := normalizeCreateSystemUpdateHostSelfUpdateParams(
		CreateSystemUpdateHostSelfUpdateParams{
			ExecutionHostID:     previous.ExecutionHostID,
			TargetVersion:       previous.TargetVersion,
			IdempotencyKey:      params.IdempotencyKey,
			RequestedByUserID:   params.RequestedByUserID,
			RequestedByUsername: params.RequestedByUsername,
			RetryOfID:           previous.ID,
			Release:             previous.Release,
			Now:                 params.Now,
		},
	)
	if err := validateCreateSystemUpdateHostSelfUpdateParams(create); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	intent, err := systemUpdateHostSelfUpdateIntentSHA256(create)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	return createMemorySystemUpdateHostSelfUpdateLocked(
		s, registry, policyStore, create, intent,
	)
}

func createMemorySystemUpdateHostSelfUpdateLocked(
	s *MemorySystemUpdateStore,
	registry *MemoryAuthStore,
	policyStore *MemoryUpdaterPolicyStore,
	params CreateSystemUpdateHostSelfUpdateParams,
	intent string,
) (SystemUpdateHostSelfUpdate, bool, error) {
	for _, existing := range s.hostSelfUpdates {
		if existing.requestedByUserID == params.RequestedByUserID &&
			existing.IdempotencyKey == params.IdempotencyKey {
			if existing.intentSHA256 != intent {
				return SystemUpdateHostSelfUpdate{}, false, ErrAlreadyExists
			}
			return publicSystemUpdateHostSelfUpdate(existing), false, nil
		}
	}
	if _, ok := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, params.ExecutionHostID,
	); ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrSystemUpdateHostSelfUpdateBusy
	}
	if _, ok := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
		s, params.ExecutionHostID,
	); ok {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateRuntimeTokenRotationBusy
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == params.ExecutionHostID &&
			systemUpdateJobHoldsHost(job) {
			return SystemUpdateHostSelfUpdate{}, false,
				ErrSystemUpdateExecutionHostBusy
		}
	}
	ownership, ok := s.executionHosts[params.ExecutionHostID]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	policy, ok := policyStore.policies[ownership.AgentServiceID]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	agent, ok := registry.services[ownership.AgentServiceID]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	previous, err := validateMemorySystemUpdateHostSelfUpdateReady(
		registry, ownership, policy, agent, params.Release,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if hasStagedServiceNodeConfiguration(agent) {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	now := params.Now
	update := SystemUpdateHostSelfUpdate{
		ID:                                  newUUID(),
		ExecutionHostID:                     params.ExecutionHostID,
		AgentServiceID:                      agent.ServiceID,
		TargetVersion:                       params.TargetVersion,
		Status:                              SystemUpdateHostSelfUpdateQueued,
		Revision:                            1,
		IdempotencyKey:                      params.IdempotencyKey,
		RequestedByUsername:                 params.RequestedByUsername,
		RetryOfID:                           params.RetryOfID,
		AttemptGeneration:                   newUUID(),
		ExpectedOwnershipEpoch:              ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        policy.Revision,
		ExpectedProjectionRevision:          policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		PreviousAgentVersion:                previous.agentVersion,
		PreviousExecutorVersion:             previous.executorVersion,
		PreviousAgentProtocolVersion:        previous.agentProtocol,
		PreviousExecutorProtocolVersion:     previous.executorProtocol,
		PreviousMutationProtocolVersion:     previous.mutationProtocol,
		PreviousRecoveryProtocolVersion:     previous.recoveryProtocol,
		Release:                             params.Release,
		IssuedAt:                            now,
		ObservationState:                    SystemUpdateHostSelfUpdateObservationUnknown,
		CreatedAt:                           now,
		UpdatedAt:                           now,
		requestedByUserID:                   params.RequestedByUserID,
		intentSHA256:                        intent,
	}
	if s.hostSelfUpdates == nil {
		s.hostSelfUpdates = map[string]SystemUpdateHostSelfUpdate{}
	}
	s.hostSelfUpdates[update.ID] = update
	return publicSystemUpdateHostSelfUpdate(update), true, nil
}

type systemUpdateHostPreviousRuntime struct {
	agentVersion     string
	executorVersion  string
	agentProtocol    int
	executorProtocol int
	mutationProtocol int
	recoveryProtocol int
}

func validateMemorySystemUpdateHostSelfUpdateReady(
	registry *MemoryAuthStore,
	ownership SystemUpdateExecutionHost,
	policy UpdaterPolicy,
	agent RegisteredService,
	release SystemUpdateHostReleaseMetadata,
) (systemUpdateHostPreviousRuntime, error) {
	previous, err := validateSystemUpdateHostSelfUpdateReady(
		ownership, policy, agent, release,
	)
	if err != nil {
		return systemUpdateHostPreviousRuntime{}, err
	}
	token, ok := registry.serviceTokens[agent.TokenID]
	if !ok || token.RevokedAt != nil || token.ServiceType != "update_agent" {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentInactive
	}
	return previous, nil
}

var _ SystemUpdateHostSelfUpdateStore = (*MemorySystemUpdateStore)(nil)
