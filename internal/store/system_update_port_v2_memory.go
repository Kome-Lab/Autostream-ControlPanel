package store

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

// Discover without retaining updates.mu, then acquire the established
// policy -> updates -> registry order. Recheck the sole policy-store pointer
// because the first port creation may race this discovery.
func (s *MemorySystemUpdateStore) lockPortPolicyOrder() func() {
	for {
		s.mu.Lock()
		policy := s.portPolicyStore
		s.mu.Unlock()
		if policy != nil {
			policy.mu.Lock()
		}
		s.mu.Lock()
		if s.portPolicyStore == policy {
			return func() {
				s.mu.Unlock()
				if policy != nil {
					policy.mu.Unlock()
				}
			}
		}
		s.mu.Unlock()
		if policy != nil {
			policy.mu.Unlock()
		}
	}
}

func (s *MemorySystemUpdateStore) GetSystemUpdatePortPolicySnapshot(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params SystemUpdatePortSnapshotParams) (SystemUpdatePortPolicySnapshot, error) {
	registry, rok := services.(*MemoryAuthStore)
	policyStore, pok := policies.(*MemoryUpdaterPolicyStore)
	if !rok || !pok || registry == nil || policyStore == nil {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	policy, _, err := memoryPullPolicyForPortTargetLocked(policyStore, params.TargetID)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	all, err := sortedPortServices(registry.services, policy, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	return restoreSystemUpdatePortPolicySnapshot(policy, s.executionHosts[policy.ExecutionHostID], all, params)
}

func (s *MemorySystemUpdateStore) ConfirmSystemUpdatePortPolicyBaseline(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params ConfirmSystemUpdatePortPolicyBaselineParams) error {
	registry, rok := services.(*MemoryAuthStore)
	policyStore, pok := policies.(*MemoryUpdaterPolicyStore)
	if !rok || !pok || registry == nil || policyStore == nil {
		return ErrSystemUpdatePortStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	policy, ok := policyStore.policies[params.AgentServiceID]
	if !ok {
		return ErrNotFound
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == policy.ExecutionHostID && systemUpdateJobHoldsHost(job) {
			return ErrSystemUpdateExecutionHostBusy
		}
	}
	all, err := sortedPortServices(registry.services, policy, params.ControlPanelTarget)
	if err != nil {
		return err
	}
	updated, err := validateSystemUpdatePortBaseline(policy, s.executionHosts[policy.ExecutionHostID], all, params)
	if err != nil {
		return err
	}
	for id, revision := range updated {
		service := registry.services[id]
		if service.AppliedEndpointRevision == 0 {
			service.AppliedEndpointRevision = revision
			registry.services[id] = service
		}
	}
	return nil
}

func validateSystemUpdatePortBaseline(policy UpdaterPolicy, host SystemUpdateExecutionHost, services []RegisteredService, params ConfirmSystemUpdatePortPolicyBaselineParams) (map[string]int64, error) {
	if params.Baseline == nil || contracts.ValidateUpdaterPortPolicyBaseline(*params.Baseline) != nil || params.AgentServiceID != policy.UpdaterID ||
		params.ExpectedSourcePolicyRevision != policy.Revision || params.ExpectedProjectionRevision != policy.ProjectionRevision || params.ExpectedExecutorPolicyRevision != policy.LocalExecutorPolicyRevision || params.ExpectedExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 {
		return nil, ErrSystemUpdatePortSnapshotStale
	}
	baseline := params.Baseline
	if baseline.SourcePolicyRevision != policy.Revision || baseline.ProjectionRevision != policy.ProjectionRevision || baseline.ExecutorPolicyRevision != policy.LocalExecutorPolicyRevision || baseline.ExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 || len(baseline.Targets) != len(policy.Targets) {
		return nil, ErrSystemUpdatePortSnapshotStale
	}
	observed := map[string]contracts.UpdaterPortPolicyBaselineTarget{}
	for _, target := range baseline.Targets {
		observed[target.ServiceID] = target
	}
	bound := map[string]UpdaterPolicyTarget{}
	for _, target := range policy.Targets {
		bound[target.ServiceID] = target
	}
	updates := map[string]int64{}
	for i := range services {
		service := &services[i]
		target, ok := bound[service.ServiceID]
		if !ok {
			continue
		}
		state, ok := observed[service.ServiceID]
		if !ok || string(state.ServiceType) != target.ServiceType || string(state.DeploymentMode) != target.DeploymentMode || state.ConfigRevision != service.AppliedConfigRevision || state.ConfigSHA256 != service.AppliedConfigSHA256 || state.EndpointRevision < 1 || state.EndpointRevision > service.EndpointRevision || service.AppliedEndpointRevision != 0 && service.AppliedEndpointRevision != state.EndpointRevision || !sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) {
			return nil, ErrSystemUpdatePortSnapshotStale
		}
		if target.DeploymentMode == "systemd" {
			local, ok := PullUpdaterPolicyTargetLocalListenPort(target, *service)
			if !ok || state.LocalListenPort != local || state.Docker != nil {
				return nil, ErrSystemUpdatePortSnapshotStale
			}
		}
		if claimed, ok := params.AppliedEndpointRevisions[service.ServiceID]; !ok || claimed != state.EndpointRevision {
			return nil, ErrSystemUpdatePortSnapshotStale
		}
		service.AppliedEndpointRevision = state.EndpointRevision
		updates[service.ServiceID] = state.EndpointRevision
	}
	if len(policy.Targets) == 0 {
		return nil, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	restored, err := restoreSystemUpdatePortPolicySnapshot(policy, host, services, SystemUpdatePortSnapshotParams{TargetID: policy.Targets[0].ServiceID, ControlPanelTarget: params.ControlPanelTarget, BuildPolicySnapshot: params.BuildPolicySnapshot})
	if err != nil {
		return nil, err
	}
	for _, target := range restored.Snapshot.Targets {
		state := observed[target.ServiceID]
		if state.LocalListenPort != target.LocalListenPort || !reflect.DeepEqual(state.Docker, target.Docker) {
			return nil, ErrSystemUpdatePortSnapshotStale
		}
	}
	return updates, nil
}

func validateSystemUpdatePortV2Ready(policy UpdaterPolicy, target UpdaterPolicyTarget, service, agent RegisteredService, host SystemUpdateExecutionHost, params CreateSystemdPortReconfigurationJobParams, now time.Time) error {
	if target.ServiceID != params.TargetID || target.HostID != policy.ExecutionHostID || !supportedSystemdPortServiceType(target.ServiceType) || service.ServiceType != target.ServiceType ||
		policy.UpdaterID != agent.ServiceID || agent.ServiceType != "update_agent" || agent.ExecutionHostID != host.ExecutionHostID || agent.OwnershipEpoch != host.OwnershipEpoch || agent.TransportMode != SystemUpdateTransportPullV2 {
		return ErrSystemUpdateOwnershipConflict
	}
	if agent.Status != "online" || agent.LastHeartbeatAt == nil || now.Sub(*agent.LastHeartbeatAt) < 0 || now.Sub(*agent.LastHeartbeatAt) > pullUpdaterActivationHeartbeatMaxAge {
		return ErrSystemUpdateAgentNotReady
	}
	version, vok := updaterPolicyCapabilityInt64(agent.ReportedCapabilities["port_contract_version"])
	transition, tok := updaterPolicyCapabilityInt64(agent.ReportedCapabilities["policy_transition_version"])
	if !vok || version != 2 || !tok || transition != 1 {
		return ErrSystemUpdatePortContractRequired
	}
	if target.DeploymentMode == "systemd" {
		probe := service
		probe.AppliedEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
		if probe.AppliedEndpoint == nil {
			return ErrSystemUpdateEndpointStale
		}
		probe.AppliedEndpoint.Port = target.LocalListenPort
		if !pullAgentReadyForSystemdPortChange(agent, policy, probe, now) {
			return ErrSystemUpdateAgentNotReady
		}
	} else {
		frozen, err := systemUpdatePortDockerSnapshotFromAgent(agent, target.ServiceID)
		if err != nil {
			return err
		}
		probe := service
		probe.AppliedEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
		if probe.AppliedEndpoint == nil {
			return ErrSystemUpdateEndpointStale
		}
		probe.AppliedEndpoint.Port = frozen.PublishedPort
		baseline, ok := pullAgentReadyForDockerPortChange(agent, policy, probe, now)
		if !ok || baseline.PublishedPort != frozen.PublishedPort || baseline.ContainerPort != frozen.ContainerPort || baseline.HealthPort != frozen.HealthPort ||
			"sha256:"+baseline.ApprovedComposeConfigSHA256 != frozen.ComposePolicySHA256 || baseline.ApprovedComposeRevision != frozen.ComposeRevision || baseline.VersionEnvSHA256 != frozen.VersionEnvSHA256 || baseline.ImageID != frozen.ImageID || baseline.RepositoryDigest != frozen.RepositoryDigest {
			return ErrSystemUpdateAgentNotReady
		}
	}
	return nil
}

func (s *MemorySystemUpdateStore) createSystemUpdatePortV2(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params CreateSystemdPortReconfigurationJobParams) (SystemUpdateJob, bool, error) {
	params = normalizeCreateSystemdPortReconfigurationJobParams(params)
	if err := validateCreateSystemUpdatePortV2Params(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	registry, rok := services.(*MemoryAuthStore)
	policyStore, pok := policies.(*MemoryUpdaterPolicyStore)
	if !rok || !pok || registry == nil || policyStore == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if s.portPolicyStore != nil && s.portPolicyStore != policyStore {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	if existing, ok := memorySystemUpdateByIdempotencyLocked(s, params.RequestedByUserID, params.IdempotencyKey); ok {
		if existing.portTransaction != nil && existing.portTransaction.RequestSHA256 == systemUpdatePortV2RequestDigest(params) {
			return publicMemorySystemUpdateJob(existing), false, nil
		}
		return SystemUpdateJob{}, false, ErrSystemUpdatePortIdempotencyConflict
	}
	policy, target, err := memoryPullPolicyForPortTargetLocked(policyStore, params.TargetID)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	host := s.executionHosts[policy.ExecutionHostID]
	for _, existing := range s.jobs {
		if existing.ExecutionHostID == policy.ExecutionHostID && systemUpdateJobHoldsHost(existing) {
			return SystemUpdateJob{}, false, ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, ok := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(s, host.ExecutionHostID); ok {
		return SystemUpdateJob{}, false, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if _, ok := activeMemorySystemUpdateHostSelfUpdateForHostLocked(s, host.ExecutionHostID); ok {
		return SystemUpdateJob{}, false, ErrSystemUpdateExecutionHostBusy
	}
	all, err := sortedPortServices(registry.services, policy, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	service, agent := registry.services[target.ServiceID], registry.services[policy.UpdaterID]
	if token, ok := registry.serviceTokens[agent.TokenID]; !ok || token.RevokedAt != nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateAgentInactive
	}
	now := time.Now().UTC()
	if err := validateSystemUpdatePortV2Ready(policy, target, service, agent, host, params, now); err != nil {
		return SystemUpdateJob{}, false, err
	}
	before, err := restoreSystemUpdatePortPolicySnapshot(policy, host, all, SystemUpdatePortSnapshotParams{TargetID: params.TargetID, ControlPanelTarget: params.ControlPanelTarget, BuildPolicySnapshot: params.BuildPolicySnapshot})
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(policy, params.TargetID, params.NewLocalListenPort, params.ControlPanelTarget); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateMemoryPortReservationsLocked(s, host.ExecutionHostID, target.ServiceID, before.Ref.LocalListenPort, params.NewLocalListenPort); err != nil {
		return SystemUpdateJob{}, false, err
	}
	job, err := systemUpdatePortV2Job(params, before, service, agent, host, now)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if !systemUpdatePortV2NoOp(job) {
		if before.Ref.LocalListenPort != params.NewLocalListenPort {
			pending := ServicePortReservation{ExecutionHostID: host.ExecutionHostID, NetworkNamespace: "host", Protocol: "tcp", Port: params.NewLocalListenPort, ServiceID: target.ServiceID, ServiceRole: systemUpdatePortPendingRole, CreatedAt: now, UpdatedAt: now}
			s.portReservations[servicePortKey(pending)] = pending
		}
		for _, state := range job.portTransaction.Target.Snapshot.Targets {
			if state.ServiceID == target.ServiceID {
				service.DesiredEndpoint = portServiceEndpoint(state.DesiredEndpoint)
				service.EndpointRevision = state.EndpointRevision
				service.EndpointStatus = "pending"
				service.UpdatedAt = now
				registry.services[target.ServiceID] = service
			}
		}
	}
	s.portPolicyStore = policyStore
	s.jobs[job.ID] = job
	s.portJobRegistries[job.ID] = registry
	return publicMemorySystemUpdateJob(job), true, nil
}

func validateMemorySystemUpdatePortV2StateLocked(s *MemorySystemUpdateStore, registry *MemoryAuthStore, job SystemUpdateJob) error {
	if job.portTransaction == nil || s.portPolicyStore == nil {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	tx := job.portTransaction
	expected := tx.Before
	if tx.Phase == "consumed" || tx.Phase == "rollback_latched" {
		expected = tx.Target
	}
	policy, ok := s.portPolicyStore.policies[job.AgentServiceID]
	if !ok || !portPolicyMatchesSnapshot(policy, expected) {
		return ErrSystemUpdatePortSnapshotStale
	}
	agent, ok := registry.services[job.AgentServiceID]
	if !ok || agent.ServiceType != "update_agent" || agent.ExecutionHostID != job.ExecutionHostID || agent.OwnershipEpoch != job.OwnershipEpoch {
		return ErrSystemUpdateOwnershipConflict
	}
	if registry.serviceTokens != nil {
		if token, ok := registry.serviceTokens[agent.TokenID]; !ok || token.RevokedAt != nil {
			return ErrSystemUpdateAgentInactive
		}
	}
	if s.executionHosts != nil && !systemUpdatePortV2OwnershipMatches(job, s.executionHosts[job.ExecutionHostID]) {
		return ErrSystemUpdateOwnershipConflict
	}
	refs := map[string]bool{}
	if agent.TokenID != "" {
		refs[agent.TokenID] = true
	}
	for _, before := range tx.Before.Snapshot.Targets {
		service, ok := registry.services[before.ServiceID]
		if !ok && before.ServiceID == "control-panel" {
			continue
		}
		if !ok || service.ServiceType != string(before.ServiceType) {
			return ErrSystemUpdatePortSnapshotStale
		}
		if service.TokenID != "" {
			refs[service.TokenID] = true
		}
		wantDesired := before.DesiredEndpoint
		wantE := before.EndpointRevision
		if before.ServiceID == job.TargetID && !systemUpdatePortV2NoOp(job) {
			for _, target := range tx.Target.Snapshot.Targets {
				if target.ServiceID == job.TargetID {
					wantDesired = target.DesiredEndpoint
					wantE = target.EndpointRevision
				}
			}
		}
		if portSnapshotEndpoint(service.AppliedEndpoint) != before.AppliedEndpoint || portSnapshotEndpoint(service.DesiredEndpoint) != wantDesired || service.EndpointRevision != wantE || service.AppliedEndpointRevision != before.AppliedEndpointRevision || service.AppliedConfigRevision != before.ConfigRevision || service.AppliedConfigSHA256 != before.ConfigSHA256 {
			return ErrSystemUpdatePortSnapshotStale
		}
	}
	currentRefs := make([]string, 0, len(refs))
	for ref := range refs {
		currentRefs = append(currentRefs, ref)
	}
	expectedRefs := append([]string(nil), tx.Before.Snapshot.CredentialReferences...)
	sort.Strings(currentRefs)
	sort.Strings(expectedRefs)
	if strings.Join(currentRefs, "\x00") != strings.Join(expectedRefs, "\x00") {
		return ErrSystemUpdatePortSnapshotStale
	}
	// SQL uses the same state checks and separately acquires these rows in
	// numeric port order. Memory owns its complete reservation map here.
	if s.portReservations != nil {
		oldPort, newPort := tx.Before.Ref.LocalListenPort, tx.Target.Ref.LocalListenPort
		for _, port := range []int{oldPort, newPort} {
			role := systemUpdatePortCurrentRole
			if port == newPort && newPort != oldPort {
				role = systemUpdatePortPendingRole
			}
			reservation, ok := s.portReservations[servicePortReservationKey{executionHostID: job.ExecutionHostID, networkNamespace: "host", protocol: "tcp", port: port}]
			if !ok || reservation.ServiceID != job.TargetID || reservation.ServiceRole != role {
				return ErrSystemUpdatePortSnapshotStale
			}
		}
	}
	return nil
}

func consumeMemorySystemUpdatePortV2Locked(s *MemorySystemUpdateStore, job SystemUpdateJob, operation string, now time.Time) error {
	registry := s.portJobRegistries[job.ID]
	if registry == nil {
		return ErrSystemUpdatePortStoreMismatch
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := validateMemorySystemUpdatePortV2StateLocked(s, registry, job); err != nil {
		return err
	}
	if job.PortResult != nil || systemUpdatePortV2NoOp(job) {
		return ErrSystemUpdateAuthorizationState
	}
	tx := cloneSystemUpdatePortTransaction(job.portTransaction)
	if operation == SystemUpdateMutationOperationPortReconfigure {
		if tx.Phase != "created" || tx.RecoveryRequired {
			return ErrSystemUpdateAuthorizationState
		}
		policy, err := portSnapshotPolicy(tx.Target)
		if err != nil {
			return err
		}
		policy.UpdatedAt = now
		s.portPolicyStore.policies[job.AgentServiceID] = policy
		host := s.executionHosts[job.ExecutionHostID]
		host.PolicyRevision = policy.ProjectionRevision
		host.UpdatedAt = now
		s.executionHosts[job.ExecutionHostID] = host
		tx.Phase = "consumed"
	} else if operation != SystemUpdateMutationOperationPortReconfigureReconcile || (tx.Phase != "consumed" && tx.Phase != "rollback_latched") {
		return ErrSystemUpdateAuthorizationState
	}
	job.portTransaction = tx
	projectSystemUpdatePortTransaction(&job)
	s.jobs[job.ID] = job
	return nil
}

func finishMemorySystemUpdatePortV2Locked(s *MemorySystemUpdateStore, registry *MemoryAuthStore, job *SystemUpdateJob, report SystemUpdateReport, now time.Time) error {
	if err := validateMemorySystemUpdatePortV2StateLocked(s, registry, *job); err != nil {
		return err
	}
	tx := cloneSystemUpdatePortTransaction(job.portTransaction)
	if report.PortResult == nil {
		if tx.Phase != "created" {
			return ErrSystemUpdatePortRecoveryRequired
		}
		if err := cancelMemorySystemUpdatePortV2Locked(s, registry, *job, now); err != nil {
			return err
		}
		tx.Phase = "premutation_failed"
	} else if report.PortResult.Result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
		if tx.Phase != "consumed" && tx.Phase != "rollback_latched" {
			return ErrSystemUpdatePortResultMismatch
		}
		tx.Phase = "rollback_latched"
		tx.RecoveryRequired = true
		tx.LastRecoveryObservation = cloneSystemUpdatePortV2Result(report.PortResult)
		service := registry.services[job.TargetID]
		service.EndpointStatus = "rollback_failed"
		service.UpdatedAt = now
		registry.services[job.TargetID] = service
	} else {
		if tx.AcceptedResult != nil {
			return ErrSystemUpdatePortResultMismatch
		}
		final := tx.Target
		switch report.PortResult.Result {
		case contracts.SystemUpdatePortReconfigurationUnchanged:
			if !systemUpdatePortV2NoOp(*job) || tx.Phase != "created" {
				return ErrSystemUpdatePortResultMismatch
			}
			final = tx.Before
		case contracts.SystemUpdatePortReconfigurationApplied:
			if tx.Phase != "consumed" || tx.RecoveryRequired {
				return ErrSystemUpdatePortResultMismatch
			}
		case contracts.SystemUpdatePortReconfigurationRolledBack:
			if tx.Phase != "consumed" && tx.Phase != "rollback_latched" {
				return ErrSystemUpdatePortResultMismatch
			}
			final = tx.Rollback
		default:
			return ErrSystemUpdatePortResultMismatch
		}
		if !systemUpdatePortV2NoOp(*job) {
			policy, err := portSnapshotPolicy(final)
			if err != nil {
				return err
			}
			policy.UpdatedAt = now
			s.portPolicyStore.policies[job.AgentServiceID] = policy
			host := s.executionHosts[job.ExecutionHostID]
			host.PolicyRevision = policy.ProjectionRevision
			host.UpdatedAt = now
			s.executionHosts[job.ExecutionHostID] = host
			for _, state := range final.Snapshot.Targets {
				if state.ServiceID == job.TargetID {
					service := registry.services[job.TargetID]
					service.AppliedEndpoint = portServiceEndpoint(state.AppliedEndpoint)
					service.DesiredEndpoint = portServiceEndpoint(state.DesiredEndpoint)
					service.Host = state.AppliedEndpoint.Host
					service.Port = state.AppliedEndpoint.Port
					service.SSLEnabled = state.AppliedEndpoint.SSLEnabled
					service.PublicURL = state.AppliedEndpoint.PublicURL
					service.EndpointRevision = state.EndpointRevision
					service.AppliedEndpointRevision = state.AppliedEndpointRevision
					service.AppliedConfigRevision = state.ConfigRevision
					service.AppliedConfigSHA256 = state.ConfigSHA256
					service.EndpointStatus = "applied"
					if report.PortResult.Result == contracts.SystemUpdatePortReconfigurationRolledBack {
						service.EndpointStatus = "rolled_back"
					}
					service.UpdatedAt = now
					registry.services[job.TargetID] = service
				}
			}
			oldPort, newPort := tx.Before.Ref.LocalListenPort, tx.Target.Ref.LocalListenPort
			if oldPort != newPort {
				oldKey := servicePortReservationKey{executionHostID: job.ExecutionHostID, networkNamespace: "host", protocol: "tcp", port: oldPort}
				newKey := servicePortReservationKey{executionHostID: job.ExecutionHostID, networkNamespace: "host", protocol: "tcp", port: newPort}
				if report.PortResult.Result == contracts.SystemUpdatePortReconfigurationApplied {
					delete(s.portReservations, oldKey)
					reservation := s.portReservations[newKey]
					reservation.ServiceRole = systemUpdatePortCurrentRole
					reservation.UpdatedAt = now
					s.portReservations[newKey] = reservation
				} else {
					delete(s.portReservations, newKey)
				}
			}
		}
		tx.AcceptedResult = cloneSystemUpdatePortV2Result(report.PortResult)
		tx.RecoveryRequired = false
		tx.Phase = "accepted"
	}
	job.portTransaction = tx
	projectSystemUpdatePortTransaction(job)
	return nil
}

func cancelMemorySystemUpdatePortV2Locked(s *MemorySystemUpdateStore, registry *MemoryAuthStore, job SystemUpdateJob, now time.Time) error {
	if job.portTransaction == nil || job.portTransaction.Phase != "created" || job.PortResult != nil {
		return ErrSystemUpdateNotCancellable
	}
	if err := validateMemorySystemUpdatePortV2StateLocked(s, registry, job); err != nil {
		return err
	}
	if !systemUpdatePortV2NoOp(job) {
		service := registry.services[job.TargetID]
		service.DesiredEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
		service.EndpointRevision = job.portTransaction.CancelEndpointRevision
		service.EndpointStatus = "applied"
		service.UpdatedAt = now
		registry.services[job.TargetID] = service
		if job.PortReconfigure.Before.LocalListenPort != job.PortReconfigure.Target.LocalListenPort {
			delete(s.portReservations, servicePortReservationKey{executionHostID: job.ExecutionHostID, networkNamespace: "host", protocol: "tcp", port: job.PortReconfigure.Target.LocalListenPort})
		}
	}
	return nil
}
