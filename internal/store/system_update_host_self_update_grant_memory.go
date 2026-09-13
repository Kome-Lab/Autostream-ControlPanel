package store

import (
	"context"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

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
		ownership.PolicyRevision != update.ExpectedProjectionRevision {
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
			systemUpdateJobHoldsHost(job) {
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
