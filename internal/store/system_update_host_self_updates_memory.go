package store

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
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
			!isTerminalSystemUpdateStatus(job.Status) {
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

func validateSystemUpdateHostSelfUpdateReady(
	ownership SystemUpdateExecutionHost,
	policy UpdaterPolicy,
	agent RegisteredService,
	release SystemUpdateHostReleaseMetadata,
) (systemUpdateHostPreviousRuntime, error) {
	if ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch < 1 ||
		ownership.AgentServiceID == "" ||
		policy.UpdaterID != ownership.AgentServiceID ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != ownership.ExecutionHostID ||
		policy.Revision != ownership.PolicyRevision ||
		policy.Revision < 1 || policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!systemUpdateHostSelfUpdatePolicyDigestPattern.MatchString(
			policy.LocalExecutorPolicySHA256,
		) ||
		agent.ServiceID != ownership.AgentServiceID ||
		agent.ServiceType != "update_agent" ||
		agent.TransportMode != SystemUpdateTransportPullV2 ||
		agent.ExecutionHostID != ownership.ExecutionHostID ||
		agent.OwnershipEpoch != ownership.OwnershipEpoch {
		return systemUpdateHostPreviousRuntime{},
			ErrSystemUpdateOwnershipConflict
	}
	caps := agent.ReportedCapabilities
	if !updaterPolicyCapabilityBool(caps["self_update_ready"]) ||
		!updaterPolicyCapabilityBool(caps["mutation_enabled"]) ||
		updaterPolicyCapabilityBool(caps["recovery_pending"]) ||
		strings.ToLower(updaterPolicyCapabilityString(caps["self_update_phase"])) != "stable" ||
		strings.ToLower(strings.TrimSpace(agent.ReportedOS)) != "linux" ||
		strings.ToLower(strings.TrimSpace(agent.ReportedArch)) != release.Arch {
		return systemUpdateHostPreviousRuntime{},
			ErrSystemUpdateExecutionHostBusy
	}
	previous := systemUpdateHostPreviousRuntime{
		agentVersion:    strings.TrimSpace(agent.ReportedVersion),
		executorVersion: updaterPolicyCapabilityString(caps["executor_version"]),
	}
	if updaterPolicyCapabilityString(
		caps["self_update_active_agent_version"],
	) != previous.agentVersion ||
		updaterPolicyCapabilityString(
			caps["self_update_active_executor_version"],
		) != previous.executorVersion {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	var okProtocol bool
	previous.agentProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["agent_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.executorProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["executor_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.mutationProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["mutation_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.recoveryProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["recovery_protocol_version"],
	)
	if !okProtocol ||
		!systemUpdateHostSelfUpdateCurrentProtocolsAreCompatible(previous) ||
		!systemUpdateJobVersionPattern.MatchString(previous.agentVersion) ||
		!systemUpdateJobVersionPattern.MatchString(previous.executorVersion) ||
		!systemUpdateHostSelfUpdateTargetIsStrictlyNewer(
			release.Tag,
			previous.agentVersion,
			previous.executorVersion,
		) {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	return previous, nil
}

func hostSelfUpdateCapabilityProtocol(value any) (int, bool) {
	if parsed, ok := updaterPolicyCapabilityInt64(value); ok &&
		parsed >= 1 && parsed <= int64(^uint(0)>>1) {
		return int(parsed), true
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil && parsed >= 1 {
			return parsed, true
		}
	}
	return 0, false
}

func activeMemorySystemUpdateHostSelfUpdateForHostLocked(
	s *MemorySystemUpdateStore,
	executionHostID string,
) (SystemUpdateHostSelfUpdate, bool) {
	for _, update := range s.hostSelfUpdates {
		if update.ExecutionHostID == executionHostID &&
			!isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
			return update, true
		}
	}
	return SystemUpdateHostSelfUpdate{}, false
}

func (s *MemorySystemUpdateStore) CancelSystemUpdateHostSelfUpdate(
	ctx context.Context,
	id, actorUserID string,
	expectedRevision int64,
	_ bool,
	now time.Time,
) (SystemUpdateHostSelfUpdate, error) {
	id = strings.TrimSpace(id)
	actorUserID = strings.TrimSpace(actorUserID)
	now = now.UTC()
	if !serviceIDPattern.MatchString(id) ||
		!serviceIDPattern.MatchString(actorUserID) ||
		expectedRevision < 1 || now.IsZero() {
		return SystemUpdateHostSelfUpdate{},
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	update, ok := s.hostSelfUpdates[id]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	if update.Revision != expectedRevision {
		return SystemUpdateHostSelfUpdate{}, ErrSystemUpdateHostSelfUpdateStale
	}
	switch {
	case update.Status == SystemUpdateHostSelfUpdateQueued:
		update.Status = SystemUpdateHostSelfUpdateCanceled
		update.Code = "canceled_by_admin"
		update.CompletedAt = cloneTimePtr(&now)
	default:
		return SystemUpdateHostSelfUpdate{}, ErrSystemUpdateHostSelfUpdateCancel
	}
	update.Revision++
	update.UpdatedAt = now
	s.hostSelfUpdates[id] = update
	return publicSystemUpdateHostSelfUpdate(update), nil
}

func (s *MemorySystemUpdateStore) ObserveSystemUpdateHostSelfUpdate(
	ctx context.Context,
	observation SystemUpdateHostSelfUpdateObservation,
) (SystemUpdateHostSelfUpdate, bool, error) {
	observation.ExecutionHostID = strings.TrimSpace(observation.ExecutionHostID)
	observation.AgentServiceID = strings.TrimSpace(observation.AgentServiceID)
	observation.Now = observation.Now.UTC()
	observation.HeartbeatAt = observation.HeartbeatAt.UTC()
	if !executionHostIDPattern.MatchString(observation.ExecutionHostID) ||
		!serviceIDPattern.MatchString(observation.AgentServiceID) ||
		observation.ExpectedRevision < 1 || observation.Now.IsZero() {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	update, ok := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, observation.ExecutionHostID,
	)
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	if update.AgentServiceID != observation.AgentServiceID {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if update.Revision != observation.ExpectedRevision {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateStale
	}
	next, changed, err := reconcileSystemUpdateHostSelfUpdateObservation(
		update, observation,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	s.hostSelfUpdates[next.ID] = next
	return publicSystemUpdateHostSelfUpdate(next), changed, nil
}

func reconcileSystemUpdateHostSelfUpdateObservation(
	update SystemUpdateHostSelfUpdate,
	observation SystemUpdateHostSelfUpdateObservation,
) (SystemUpdateHostSelfUpdate, bool, error) {
	beforeStatus := update.Status
	beforeObservation := update.ObservationState
	beforePhase := update.ReportedPhase
	if observation.HeartbeatAt.IsZero() {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationUnknown
		update.ReportedPhase = ""
		update.StalledSince = nil
	} else if observation.Now.Sub(observation.HeartbeatAt) >
		systemUpdateHostSelfUpdateHeartbeatStallAfter {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationStalled
		if update.StalledSince == nil {
			stalled := observation.HeartbeatAt.Add(
				systemUpdateHostSelfUpdateHeartbeatStallAfter,
			)
			update.StalledSince = cloneTimePtr(&stalled)
		}
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
	} else if observation.RecoveryPending {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationUnknown
		update.ReportedPhase = ""
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
		update.StalledSince = nil
	} else {
		phase := strings.ToLower(strings.TrimSpace(observation.Phase))
		if phase != "" && phase != "stable" &&
			observation.PendingGeneration != update.AttemptGeneration {
			return SystemUpdateHostSelfUpdate{}, false,
				ErrSystemUpdateHostSelfUpdateStale
		}
		if phase == "stable" &&
			observation.HeartbeatGeneration != "" &&
			observation.HeartbeatGeneration != update.AttemptGeneration &&
			observation.FailedGeneration != update.AttemptGeneration {
			return SystemUpdateHostSelfUpdate{}, false,
				ErrSystemUpdateHostSelfUpdateStale
		}
		update.ObservationState = SystemUpdateHostSelfUpdateObservationKnown
		update.ReportedPhase = phase
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
		update.StalledSince = nil
		if err := advanceSystemUpdateHostSelfUpdateFromObservation(
			&update, observation,
		); err != nil {
			return SystemUpdateHostSelfUpdate{}, false, err
		}
	}
	changed := update.Status != beforeStatus ||
		update.ObservationState != beforeObservation ||
		update.ReportedPhase != beforePhase
	if changed {
		update.Revision++
		update.UpdatedAt = observation.Now
	}
	return update, changed, nil
}

func advanceSystemUpdateHostSelfUpdateFromObservation(
	update *SystemUpdateHostSelfUpdate,
	observation SystemUpdateHostSelfUpdateObservation,
) error {
	phase := strings.ToLower(strings.TrimSpace(observation.Phase))
	if phase != "stable" &&
		observation.PendingGeneration != update.AttemptGeneration {
		return nil
	}
	switch phase {
	case "staged":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateStaging, observation.Now,
		)
	case "activating":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateActivating, observation.Now,
		)
	case "verifying":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateVerifying, observation.Now,
		)
	case "rolling_back":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateRollingBack, observation.Now,
		)
	case "stable":
		if strictSystemUpdateHostSelfUpdateSuccess(update, observation) {
			setSystemUpdateHostSelfUpdateTerminal(
				update, SystemUpdateHostSelfUpdateSucceeded,
				"succeeded", observation.Now,
			)
		} else if strictSystemUpdateHostSelfUpdateRollback(update, observation) {
			setSystemUpdateHostSelfUpdateTerminal(
				update, SystemUpdateHostSelfUpdateRolledBack,
				"rolled_back", observation.Now,
			)
		}
	case "":
		return nil
	default:
		return nil
	}
	return nil
}

func setSystemUpdateHostSelfUpdateStatus(
	update *SystemUpdateHostSelfUpdate,
	status string,
	now time.Time,
) {
	rank := map[string]int{
		SystemUpdateHostSelfUpdateQueued:          0,
		SystemUpdateHostSelfUpdateStaging:         1,
		SystemUpdateHostSelfUpdateActivating:      2,
		SystemUpdateHostSelfUpdateVerifying:       3,
		SystemUpdateHostSelfUpdateRollingBack:     4,
		SystemUpdateHostSelfUpdateCancelRequested: 5,
	}
	if isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) ||
		rank[status] < rank[update.Status] {
		return
	}
	update.Status = status
	if update.StartedAt == nil && status != SystemUpdateHostSelfUpdateQueued {
		update.StartedAt = cloneTimePtr(&now)
	}
}

func setSystemUpdateHostSelfUpdateTerminal(
	update *SystemUpdateHostSelfUpdate,
	status, code string,
	now time.Time,
) {
	if isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
		return
	}
	update.Status = status
	update.Code = code
	update.CompletedAt = cloneTimePtr(&now)
}

func strictSystemUpdateHostSelfUpdateSuccess(
	update *SystemUpdateHostSelfUpdate,
	o SystemUpdateHostSelfUpdateObservation,
) bool {
	return update.Status != SystemUpdateHostSelfUpdateQueued &&
		o.HeartbeatGeneration == update.AttemptGeneration &&
		o.AgentVersion == update.TargetVersion &&
		o.ActiveAgentVersion == update.TargetVersion &&
		o.ExecutorVersion == update.TargetVersion &&
		o.ActiveExecutorVersion == update.TargetVersion &&
		o.AgentProtocolVersion == update.Release.AgentProtocolVersion &&
		o.ExecutorProtocolVersion == update.Release.ExecutorProtocolVersion &&
		o.MutationProtocolVersion == update.Release.MutationProtocolVersion &&
		o.RecoveryProtocolVersion == update.Release.RecoveryProtocolVersion
}

func strictSystemUpdateHostSelfUpdateRollback(
	update *SystemUpdateHostSelfUpdate,
	o SystemUpdateHostSelfUpdateObservation,
) bool {
	return o.FailedGeneration == update.AttemptGeneration &&
		o.AgentVersion == update.PreviousAgentVersion &&
		o.ActiveAgentVersion == update.PreviousAgentVersion &&
		o.ExecutorVersion == update.PreviousExecutorVersion &&
		o.ActiveExecutorVersion == update.PreviousExecutorVersion &&
		o.AgentProtocolVersion == update.PreviousAgentProtocolVersion &&
		o.ExecutorProtocolVersion == update.PreviousExecutorProtocolVersion &&
		o.MutationProtocolVersion == update.PreviousMutationProtocolVersion &&
		o.RecoveryProtocolVersion == update.PreviousRecoveryProtocolVersion
}

func (s *MemorySystemUpdateStore) IssueSystemUpdateHostSelfUpdateGrant(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params IssueSystemUpdateHostSelfUpdateGrantParams,
) (IssueSystemUpdateHostSelfUpdateGrantResult, error) {
	params = normalizeIssueSystemUpdateHostSelfUpdateGrantParams(params)
	if err := validateIssueSystemUpdateHostSelfUpdateGrantParams(params); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	update, ok := s.hostSelfUpdates[params.SelfUpdateID]
	if !ok {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, ErrNotFound
	}
	if err := validateMemorySystemUpdateHostSelfUpdateGrantStateLocked(
		s, registry, policyStore, update, params.ExecutionHostID,
		params.AgentServiceID, params.ExpectedRevision,
	); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if params.Operation == SystemUpdateHostSelfUpdateGrantStage &&
		update.Status != SystemUpdateHostSelfUpdateQueued {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateState
	}
	if params.Operation == SystemUpdateHostSelfUpdateGrantReconcile &&
		(update.Status == SystemUpdateHostSelfUpdateQueued ||
			isTerminalSystemUpdateHostSelfUpdateStatus(update.Status)) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateState
	}
	var existingID string
	for id, grant := range s.hostSelfUpdateGrants {
		if grant.SelfUpdateID == update.ID &&
			grant.Operation == params.Operation &&
			grant.SessionID == params.SessionID {
			existingID = id
			if grant.ConsumedAt != nil {
				return IssueSystemUpdateHostSelfUpdateGrantResult{},
					ErrSystemUpdateHostSelfUpdateConsumed
			}
			break
		}
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	raw = "ast_hsug_" + raw
	now := params.Now
	grant := systemUpdateHostSelfUpdateGrantFromUpdate(
		update, params, now,
	)
	if existingID != "" &&
		!sameSystemUpdateHostSelfUpdateGrantIssueIntent(
			s.hostSelfUpdateGrants[existingID],
			grant,
		) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	grant.tokenHash = security.HashToken(raw)
	if existingID != "" {
		grant.ID = existingID
		grant.Revision = s.hostSelfUpdateGrants[existingID].Revision + 1
		grant.CreatedAt = s.hostSelfUpdateGrants[existingID].CreatedAt
	}
	if s.hostSelfUpdateGrants == nil {
		s.hostSelfUpdateGrants =
			map[string]SystemUpdateHostSelfUpdateGrant{}
	}
	s.hostSelfUpdateGrants[grant.ID] = grant
	return IssueSystemUpdateHostSelfUpdateGrantResult{
		Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
		RawToken: raw,
		Issued:   true,
	}, nil
}

func normalizeIssueSystemUpdateHostSelfUpdateGrantParams(
	p IssueSystemUpdateHostSelfUpdateGrantParams,
) IssueSystemUpdateHostSelfUpdateGrantParams {
	p.SelfUpdateID = strings.TrimSpace(p.SelfUpdateID)
	p.ExecutionHostID = strings.TrimSpace(p.ExecutionHostID)
	p.AgentServiceID = strings.TrimSpace(p.AgentServiceID)
	p.Operation = strings.ToLower(strings.TrimSpace(p.Operation))
	p.PlanSHA256 = strings.TrimSpace(p.PlanSHA256)
	p.SessionID = strings.TrimSpace(p.SessionID)
	p.Now = p.Now.UTC()
	return p
}

func validateIssueSystemUpdateHostSelfUpdateGrantParams(
	p IssueSystemUpdateHostSelfUpdateGrantParams,
) error {
	if !serviceIDPattern.MatchString(p.SelfUpdateID) ||
		!executionHostIDPattern.MatchString(p.ExecutionHostID) ||
		!serviceIDPattern.MatchString(p.AgentServiceID) ||
		p.ExpectedRevision < 1 ||
		(p.Operation != SystemUpdateHostSelfUpdateGrantStage &&
			p.Operation != SystemUpdateHostSelfUpdateGrantReconcile) ||
		!systemUpdateHostSelfUpdateDigestPattern.MatchString(p.PlanSHA256) ||
		!serviceIDPattern.MatchString(p.SessionID) ||
		p.Now.IsZero() || p.Now.Location() != time.UTC ||
		p.TTL < 15*time.Second || p.TTL > 5*time.Minute {
		return ErrSystemUpdateHostSelfUpdateGrant
	}
	return nil
}

func systemUpdateHostSelfUpdateGrantFromUpdate(
	update SystemUpdateHostSelfUpdate,
	params IssueSystemUpdateHostSelfUpdateGrantParams,
	now time.Time,
) SystemUpdateHostSelfUpdateGrant {
	return SystemUpdateHostSelfUpdateGrant{
		ID:                                  newUUID(),
		SelfUpdateID:                        update.ID,
		AttemptGeneration:                   update.AttemptGeneration,
		Operation:                           params.Operation,
		ExecutionHostID:                     update.ExecutionHostID,
		AgentServiceID:                      update.AgentServiceID,
		ExpectedSelfUpdateRevision:          update.Revision,
		ExpectedOwnershipEpoch:              update.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        update.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          update.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: update.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   update.ExpectedLocalExecutorPolicySHA256,
		AgentVersion:                        update.TargetVersion,
		ExecutorVersion:                     update.TargetVersion,
		ReleaseCommit:                       update.Release.Commit,
		ArtifactSHA256:                      "sha256:" + update.Release.ArchiveSHA256,
		AgentProtocolVersion:                update.Release.AgentProtocolVersion,
		ExecutorProtocolVersion:             update.Release.ExecutorProtocolVersion,
		MutationProtocolVersion:             update.Release.MutationProtocolVersion,
		RecoveryProtocolVersion:             update.Release.RecoveryProtocolVersion,
		Release:                             systemUpdateHostReleaseBinding(update.Release),
		DirectiveIssuedAt:                   update.IssuedAt,
		PlanSHA256:                          params.PlanSHA256,
		SessionID:                           params.SessionID,
		Revision:                            1,
		IssuedAt:                            now,
		ExpiresAt:                           now.Add(params.TTL),
		CreatedAt:                           now,
		UpdatedAt:                           now,
	}
}

func validateMemorySystemUpdateHostSelfUpdateGrantStateLocked(
	s *MemorySystemUpdateStore,
	registry *MemoryAuthStore,
	policyStore *MemoryUpdaterPolicyStore,
	update SystemUpdateHostSelfUpdate,
	hostID, agentID string,
	expectedRevision int64,
) error {
	if update.ExecutionHostID != hostID ||
		update.AgentServiceID != agentID {
		return ErrSystemUpdateOwnershipConflict
	}
	if update.Revision != expectedRevision ||
		isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	ownership, ok := s.executionHosts[hostID]
	if !ok ||
		ownership.AgentServiceID != agentID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch != update.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != update.ExpectedSourcePolicyRevision {
		return ErrSystemUpdateOwnershipConflict
	}
	policy, ok := policyStore.policies[agentID]
	if !ok ||
		policy.ExecutionHostID != hostID ||
		policy.Revision != update.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != update.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision !=
			update.ExpectedLocalExecutorPolicyRevision ||
		policy.LocalExecutorPolicySHA256 !=
			update.ExpectedLocalExecutorPolicySHA256 {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	agent, ok := registry.services[agentID]
	if !ok ||
		agent.ExecutionHostID != hostID ||
		agent.OwnershipEpoch != update.ExpectedOwnershipEpoch ||
		hasStagedServiceNodeConfiguration(agent) {
		return ErrSystemUpdateOwnershipConflict
	}
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
		s, hostID,
	); found {
		return ErrSystemUpdateRuntimeTokenRotationBusy
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == hostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return ErrSystemUpdateExecutionHostBusy
		}
	}
	return nil
}

func (s *MemorySystemUpdateStore) ConsumeSystemUpdateHostSelfUpdateGrant(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ConsumeSystemUpdateHostSelfUpdateGrantParams,
) (ConsumeSystemUpdateHostSelfUpdateGrantResult, error) {
	params.RawToken = strings.TrimSpace(params.RawToken)
	params.Now = params.Now.UTC()
	if !strings.HasPrefix(params.RawToken, "ast_hsug_") ||
		len(params.RawToken) > 256 ||
		params.Now.IsZero() {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	hash := security.HashToken(params.RawToken)
	var grant SystemUpdateHostSelfUpdateGrant
	var found bool
	for _, candidate := range s.hostSelfUpdateGrants {
		if candidate.tokenHash == hash {
			grant = candidate
			found = true
			break
		}
	}
	if !found ||
		!sameSystemUpdateHostSelfUpdateGrantBinding(
			grant, params.Binding,
		) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if grant.ConsumedAt != nil {
		update, ok := s.hostSelfUpdates[grant.SelfUpdateID]
		if !ok {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
				ErrNotFound
		}
		if err := validateConsumedSystemUpdateHostSelfUpdateGrant(
			grant,
			update,
		); err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{
			Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
			Consumed: false,
		}, nil
	}
	if params.Now.After(grant.ExpiresAt) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateExpired
	}
	update, ok := s.hostSelfUpdates[grant.SelfUpdateID]
	if !ok {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, ErrNotFound
	}
	if err := validateMemorySystemUpdateHostSelfUpdateGrantStateLocked(
		s, registry, policyStore, update,
		grant.ExecutionHostID, grant.AgentServiceID,
		grant.ExpectedSelfUpdateRevision,
	); err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if grant.Operation == SystemUpdateHostSelfUpdateGrantStage {
		reserved, reserveErr := reserveSystemUpdateHostSelfUpdateStage(
			update,
			params.Now,
		)
		if reserveErr != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
				reserveErr
		}
		update = reserved
		s.hostSelfUpdates[update.ID] = update
		grant.StageClaimRevision = update.Revision
		grant.StageClaimedAt = cloneTimePtr(&params.Now)
	}
	grant.ConsumedAt = cloneTimePtr(&params.Now)
	grant.UpdatedAt = params.Now
	s.hostSelfUpdateGrants[grant.ID] = grant
	return ConsumeSystemUpdateHostSelfUpdateGrantResult{
		Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
		Consumed: true,
	}, nil
}

var _ SystemUpdateHostSelfUpdateStore = (*MemorySystemUpdateStore)(nil)
