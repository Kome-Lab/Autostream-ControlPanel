package store

import (
	"context"
	"errors"
	"strings"
)

func (s *MemorySystemUpdateStore) GetSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	id string,
) (SystemUpdateRuntimeTokenRotation, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	id = strings.TrimSpace(id)
	if !serviceIDPattern.MatchString(id) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rotation, ok := s.runtimeTokenRotations[id]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

func (s *MemorySystemUpdateStore) GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
	ctx context.Context,
	executionHostID string,
) (SystemUpdateRuntimeTokenRotation, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rotation, ok := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
		s, executionHostID,
	)
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

func (s *MemorySystemUpdateStore) StageSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params StageSystemUpdateRuntimeTokenRotationParams,
	seal NodeTokenSealer,
) (StageSystemUpdateRuntimeTokenRotationResult, error) {
	params = normalizeStageSystemUpdateRuntimeTokenRotationParams(params)
	if err := validateStageSystemUpdateRuntimeTokenRotationParams(params); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if seal == nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, errNodeTokenSealerRequired
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	intentSHA256, err := runtimeTokenRotationIntentSHA256(params)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}

	// Keep the same global lock order used by the port/configuration
	// coordinators: policy -> execution host/job -> service/token.
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}

	if existing, found := memorySystemUpdateRuntimeTokenRotationByIdempotencyLocked(s, params.ServiceID, params.IdempotencyKey); found {
		if existing.intentSHA256 != intentSHA256 {
			return StageSystemUpdateRuntimeTokenRotationResult{}, ErrAlreadyExists
		}
		return StageSystemUpdateRuntimeTokenRotationResult{
			Rotation: publicSystemUpdateRuntimeTokenRotation(existing),
			Created:  false,
		}, nil
	}
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(s, params.ExecutionHostID); found {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == params.ExecutionHostID && !isTerminalSystemUpdateStatus(job.Status) {
			return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, params.ExecutionHostID,
	); found {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateHostSelfUpdateBusy
	}

	service, exists := registry.services[params.ServiceID]
	if !exists {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrNotFound
	}
	if hasStagedServiceNodeConfiguration(service) {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrConflict
	}
	policy, exists := policyStore.policies[params.ServiceID]
	if !exists {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrNotFound
	}
	ownership, exists := s.executionHosts[params.ExecutionHostID]
	if !exists {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationOwnership(service, policy, ownership, params); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	oldToken, exists := registry.serviceTokens[service.TokenID]
	if !exists || oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateAgentInactive
	}
	references := 0
	for _, candidate := range registry.services {
		if candidate.TokenID == oldToken.ID {
			references++
		}
	}
	if references != 1 {
		return StageSystemUpdateRuntimeTokenRotationResult{}, ErrSystemUpdateRuntimeTokenRotationSharedToken
	}

	stagedToken, _, err := newRotatedServiceToken(oldToken, params.Now)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	ciphertext, nonce, err := seal(stagedToken.RawToken)
	if err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}
	if strings.TrimSpace(ciphertext) == "" || strings.TrimSpace(nonce) == "" {
		return StageSystemUpdateRuntimeTokenRotationResult{}, errors.New("node token sealer returned an empty sealed value")
	}
	if err := ctx.Err(); err != nil {
		return StageSystemUpdateRuntimeTokenRotationResult{}, err
	}

	rotation := SystemUpdateRuntimeTokenRotation{
		ID:                                  newUUID(),
		ServiceID:                           params.ServiceID,
		ExecutionHostID:                     params.ExecutionHostID,
		Status:                              SystemUpdateRuntimeTokenRotationStaged,
		Revision:                            1,
		ExpectedOwnershipEpoch:              params.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        params.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          params.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: params.ExpectedLocalExecutorPolicyRevision,
		PreviousTokenID:                     oldToken.ID,
		StagedTokenID:                       stagedToken.ID,
		CreatedAt:                           params.Now,
		UpdatedAt:                           params.Now,
		idempotencyKey:                      params.IdempotencyKey,
		intentSHA256:                        intentSHA256,
		stagedTokenHash:                     stagedToken.TokenHash,
		stagedTokenScopes:                   append([]string(nil), stagedToken.Scopes...),
		stagedTokenCiphertext:               ciphertext,
		stagedTokenNonce:                    nonce,
	}
	durableToken := stagedToken
	durableToken.RawToken = ""
	revokedAt := params.Now
	durableToken.RevokedAt = &revokedAt
	if s.runtimeTokenRotations == nil {
		s.runtimeTokenRotations = map[string]SystemUpdateRuntimeTokenRotation{}
	}
	s.runtimeTokenRotations[rotation.ID] = rotation
	registry.serviceTokens[durableToken.ID] = durableToken
	return StageSystemUpdateRuntimeTokenRotationResult{
		Rotation: publicSystemUpdateRuntimeTokenRotation(rotation),
		Created:  true,
	}, nil
}

func (s *MemorySystemUpdateStore) ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams,
	unseal NodeTokenUnsealer,
) (ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult, error) {
	params = normalizeClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(params)
	if err := validateClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams(params); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if unseal == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, errNodeTokenSealerRequired
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}

	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}

	rotation, ok := s.runtimeTokenRotations[params.RotationID]
	if !ok {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	claimIDHash := runtimeTokenRotationClaimIDHash(params.ClaimID)
	firstClaim, _, err := runtimeTokenRotationCredentialClaimMode(
		rotation, params.ExpectedRevision, claimIDHash,
	)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if params.AuthenticatedPreviousTokenID != rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == rotation.ExecutionHostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
				ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, rotation.ExecutionHostID,
	); found {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateHostSelfUpdateBusy
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	if hasStagedServiceNodeConfiguration(service) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateExecutionHostBusy
	}
	policy, ok := policyStore.policies[rotation.ServiceID]
	if !ok {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, ErrNotFound
	}
	ownership, ok := s.executionHosts[rotation.ExecutionHostID]
	if !ok {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationOwnership(
		service,
		policy,
		ownership,
		stageParamsForRuntimeTokenRotation(rotation, params.Now),
	); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	oldToken, oldOK := registry.serviceTokens[rotation.PreviousTokenID]
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if service.TokenID != rotation.PreviousTokenID ||
		!oldOK || oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateAgentInactive
	}
	if !stagedOK ||
		stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash ||
		stagedToken.RevokedAt == nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	references := 0
	for _, candidate := range registry.services {
		if candidate.TokenID == rotation.PreviousTokenID {
			references++
		}
	}
	if references != 1 {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{},
			ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	token, err := runtimeTokenRotationReplayToken(rotation, unseal)
	if err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{}, err
	}
	if firstClaim {
		rotation.credentialClaimIDHash = claimIDHash
		rotation.credentialClaimRevision = params.ExpectedRevision
		rotation.Revision++
		rotation.CredentialClaimedAt = cloneTimePtr(&params.Now)
		rotation.UpdatedAt = params.Now
		s.runtimeTokenRotations[rotation.ID] = rotation
	}
	return ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult{
		Rotation: publicSystemUpdateRuntimeTokenRotation(rotation),
		Token:    token,
		Claimed:  firstClaim,
	}, nil
}

func (s *MemorySystemUpdateStore) MarkSystemUpdateRuntimeTokenRotationLocalStaged(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params MarkSystemUpdateRuntimeTokenRotationLocalStagedParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil || strings.TrimSpace(params.RawStagedToken) == "" {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[id]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ExecutionHostID != hostID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationCredential(rotation, params.RawStagedToken); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.EmergencyRevokedTokenID == rotation.PreviousTokenID ||
		rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	replay := rotation.Status == SystemUpdateRuntimeTokenRotationLocalStaged
	if replay {
		if rotation.LocalStageReceiptID != runtimeTokenRotationLocalStageReceiptID(rotation) ||
			!rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
	} else if rotation.Status != SystemUpdateRuntimeTokenRotationStaged {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	} else if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.CredentialClaimedAt == nil {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == rotation.ExecutionHostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, rotation.ExecutionHostID,
	); found {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateHostSelfUpdateBusy
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if hasStagedServiceNodeConfiguration(service) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateExecutionHostBusy
	}
	policy, ok := policyStore.policies[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	ownership, ok := s.executionHosts[rotation.ExecutionHostID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationOwnership(
		service,
		policy,
		ownership,
		stageParamsForRuntimeTokenRotation(rotation, now),
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	oldToken, oldOK := registry.serviceTokens[rotation.PreviousTokenID]
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if service.TokenID != rotation.PreviousTokenID ||
		!oldOK || oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateAgentInactive
	}
	if !stagedOK || stagedToken.RevokedAt == nil ||
		stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	references := 0
	for _, candidate := range registry.services {
		if candidate.TokenID == rotation.PreviousTokenID {
			references++
		}
	}
	if references != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	if replay {
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	receiptID := runtimeTokenRotationLocalStageReceiptID(rotation)
	rotation.Status = SystemUpdateRuntimeTokenRotationLocalStaged
	rotation.Revision++
	rotation.LocalStageReceiptID = receiptID
	rotation.LocalStageAcknowledgedAt = cloneTimePtr(&now)
	rotation.LocalStagedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	s.runtimeTokenRotations[id] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MemorySystemUpdateStore) ProveSystemUpdateRuntimeTokenRotationHeartbeat(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ProveSystemUpdateRuntimeTokenRotationHeartbeatParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	params = normalizeProveSystemUpdateRuntimeTokenRotationHeartbeatParams(params)
	if err := validateProveSystemUpdateRuntimeTokenRotationHeartbeatParams(params); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[params.RotationID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationCredential(rotation, params.RawStagedToken); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == rotation.ExecutionHostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, rotation.ExecutionHostID,
	); found {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateHostSelfUpdateBusy
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if hasStagedServiceNodeConfiguration(service) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	policy, ok := policyStore.policies[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	ownership, ok := s.executionHosts[rotation.ExecutionHostID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	oldToken, oldOK := registry.serviceTokens[rotation.PreviousTokenID]
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if !oldOK || service.TokenID != rotation.PreviousTokenID ||
		oldToken.RevokedAt != nil || oldToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	if !stagedOK || stagedToken.RevokedAt == nil ||
		stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err := validateRuntimeTokenRotationHeartbeatProof(
		rotation, service, policy, ownership, params,
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationHeartbeatProved {
		if !rotationRevisionAllowsReplay(rotation, params.ExpectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationLocalStaged {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != params.ExpectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationHeartbeatProved
	rotation.Revision++
	rotation.HeartbeatProvedAt = cloneTimePtr(&params.Now)
	rotation.UpdatedAt = params.Now
	s.runtimeTokenRotations[rotation.ID] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MemorySystemUpdateStore) ActivateSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	params ActivateSystemUpdateRuntimeTokenRotationParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[id]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ExecutionHostID != hostID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationCredential(rotation, params.RawStagedToken); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationActivated {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationHeartbeatProved {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.EmergencyRevokedTokenID == rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if err := validateMemoryRuntimeTokenRotationHostFenceLocked(s, rotation); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok || service.TokenID != rotation.PreviousTokenID ||
		service.ExecutionHostID != rotation.ExecutionHostID ||
		service.OwnershipEpoch != rotation.ExpectedOwnershipEpoch {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	references := 0
	for _, candidate := range registry.services {
		if candidate.TokenID == rotation.PreviousTokenID {
			references++
		}
	}
	if references != 1 {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationSharedToken
	}
	oldToken, oldOK := registry.serviceTokens[rotation.PreviousTokenID]
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if !oldOK || !stagedOK ||
		stagedToken.TokenHash != rotation.stagedTokenHash ||
		stagedToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}

	stagedToken.RevokedAt = nil
	oldRevokedAt := now
	oldToken.RevokedAt = &oldRevokedAt
	registry.serviceTokens[stagedToken.ID] = stagedToken
	registry.serviceTokens[oldToken.ID] = oldToken
	service.TokenID = stagedToken.ID
	service.NodeTokenCiphertext = rotation.stagedTokenCiphertext
	service.NodeTokenNonce = rotation.stagedTokenNonce
	service.NodeTokenRotatedAt = cloneTimePtr(&now)
	service.LastHeartbeatAt = cloneTimePtr(rotation.HeartbeatProvedAt)
	service.UpdatedAt = now
	registry.services[service.ServiceID] = service

	rotation.Status = SystemUpdateRuntimeTokenRotationActivated
	rotation.Revision++
	rotation.ActivatedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	s.runtimeTokenRotations[id] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MemorySystemUpdateStore) CancelSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	services ServiceRegistryStore,
	params CancelSystemUpdateRuntimeTokenRotationParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[id]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ExecutionHostID != hostID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationActivated {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCancelRequested {
		if !rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationStaged &&
		rotation.Status != SystemUpdateRuntimeTokenRotationLocalStaged &&
		rotation.Status != SystemUpdateRuntimeTokenRotationHeartbeatProved {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if err := validateMemoryRuntimeTokenRotationHostFenceLocked(s, rotation); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	stagedToken, ok := registry.serviceTokens[rotation.StagedTokenID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	immediate := rotation.Status == SystemUpdateRuntimeTokenRotationStaged &&
		rotation.CredentialClaimedAt == nil
	if immediate {
		if stagedToken.RevokedAt == nil {
			revokedAt := now
			stagedToken.RevokedAt = &revokedAt
			registry.serviceTokens[stagedToken.ID] = stagedToken
		}
		rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
		rotation.CanceledAt = cloneTimePtr(&now)
		scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	} else {
		rotation.Status = SystemUpdateRuntimeTokenRotationCancelRequested
		rotation.CancelRequestedAt = cloneTimePtr(&now)
	}
	rotation.Revision++
	rotation.UpdatedAt = now
	s.runtimeTokenRotations[id] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MemorySystemUpdateStore) AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	params = normalizeAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(params)
	if err := validateAcknowledgeSystemUpdateRuntimeTokenRotationCancelParams(params); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[params.RotationID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ServiceID != params.ServiceID ||
		rotation.ExecutionHostID != params.ExecutionHostID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if params.AuthenticatedPreviousTokenID != rotation.PreviousTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		if rotation.CancelAcknowledgedAt == nil ||
			!rotationRevisionAllowsReplay(rotation, params.ExpectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status != SystemUpdateRuntimeTokenRotationCancelRequested {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != params.ExpectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationStale
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == rotation.ExecutionHostID &&
			!isTerminalSystemUpdateStatus(job.Status) {
			return SystemUpdateRuntimeTokenRotation{}, false,
				ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, rotation.ExecutionHostID,
	); found {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateHostSelfUpdateBusy
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if hasStagedServiceNodeConfiguration(service) ||
		runtimeTokenRotationSelfUpdateBusy(service) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	policy, ok := policyStore.policies[rotation.ServiceID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	ownership, ok := s.executionHosts[rotation.ExecutionHostID]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if err := validateRuntimeTokenRotationOwnership(
		service,
		policy,
		ownership,
		stageParamsForRuntimeTokenRotation(rotation, params.Now),
	); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	oldToken, oldOK := registry.serviceTokens[rotation.PreviousTokenID]
	if service.TokenID != rotation.PreviousTokenID ||
		!oldOK || oldToken.RevokedAt != nil ||
		oldToken.ServiceType != "update_agent" {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateAgentInactive
	}
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if !stagedOK || stagedToken.ServiceType != "update_agent" ||
		stagedToken.TokenHash != rotation.stagedTokenHash {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationToken
	}
	if stagedToken.RevokedAt == nil {
		stagedToken.RevokedAt = cloneTimePtr(&params.Now)
		registry.serviceTokens[stagedToken.ID] = stagedToken
	}
	rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
	rotation.Revision++
	rotation.CancelAcknowledgedAt = cloneTimePtr(&params.Now)
	rotation.CanceledAt = cloneTimePtr(&params.Now)
	rotation.UpdatedAt = params.Now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	s.runtimeTokenRotations[rotation.ID] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func (s *MemorySystemUpdateStore) EmergencyRevokeSystemUpdateRuntimeToken(
	ctx context.Context,
	services ServiceRegistryStore,
	params EmergencyRevokeSystemUpdateRuntimeTokenParams,
) (SystemUpdateRuntimeTokenRotation, bool, error) {
	id, hostID, expectedRevision, now, err := normalizeRuntimeTokenRotationTransition(
		params.RotationID, params.ExecutionHostID, params.ExpectedRevision, params.Now,
	)
	params.TokenID = strings.TrimSpace(params.TokenID)
	if err != nil || !serviceIDPattern.MatchString(params.TokenID) {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, false, err
	}
	rotation, ok := s.runtimeTokenRotations[id]
	if !ok {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrNotFound
	}
	if rotation.ExecutionHostID != hostID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateOwnershipConflict
	}
	if params.TokenID != rotation.PreviousTokenID && params.TokenID != rotation.StagedTokenID {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	if rotation.EmergencyRevokedTokenID != "" {
		if rotation.EmergencyRevokedTokenID != params.TokenID ||
			!rotationRevisionAllowsReplay(rotation, expectedRevision) {
			return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
		}
		return publicSystemUpdateRuntimeTokenRotation(rotation), false, nil
	}
	if rotation.Status == SystemUpdateRuntimeTokenRotationCanceled {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateRuntimeTokenRotationTransition
	}
	if rotation.Revision != expectedRevision {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationStale
	}
	previousToken, previousOK := registry.serviceTokens[rotation.PreviousTokenID]
	stagedToken, stagedOK := registry.serviceTokens[rotation.StagedTokenID]
	if !previousOK || !stagedOK {
		return SystemUpdateRuntimeTokenRotation{}, false, ErrSystemUpdateRuntimeTokenRotationToken
	}
	for _, token := range []*ServiceToken{&previousToken, &stagedToken} {
		if token.RevokedAt == nil {
			token.RevokedAt = cloneTimePtr(&now)
		}
	}
	service, ok := registry.services[rotation.ServiceID]
	if !ok || (service.TokenID != rotation.PreviousTokenID &&
		service.TokenID != rotation.StagedTokenID) {
		return SystemUpdateRuntimeTokenRotation{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	registry.serviceTokens[previousToken.ID] = previousToken
	registry.serviceTokens[stagedToken.ID] = stagedToken
	service.Status = "offline"
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenCiphertext = ""
	service.NodeTokenNonce = ""
	service.ConfigureTokenHash = ""
	service.ConfigureTokenExpiresAt = nil
	service.ConfigureTokenUsedAt = nil
	clearStagedNodeConfiguration(&service)
	service.UpdatedAt = now
	registry.services[service.ServiceID] = service
	rotation.Status = SystemUpdateRuntimeTokenRotationCanceled
	rotation.Revision++
	rotation.CancelRequestedAt = nil
	rotation.CancelAcknowledgedAt = nil
	rotation.CanceledAt = cloneTimePtr(&now)
	rotation.EmergencyRevokedTokenID = params.TokenID
	rotation.EmergencyRevokedAt = cloneTimePtr(&now)
	rotation.UpdatedAt = now
	scrubSystemUpdateRuntimeTokenRotationReplaySecrets(&rotation)
	s.runtimeTokenRotations[id] = rotation
	return publicSystemUpdateRuntimeTokenRotation(rotation), true, nil
}

func validateMemoryRuntimeTokenRotationHostFenceLocked(
	store *MemorySystemUpdateStore,
	rotation SystemUpdateRuntimeTokenRotation,
) error {
	ownership, ok := store.executionHosts[rotation.ExecutionHostID]
	if !ok ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != rotation.ServiceID ||
		ownership.OwnershipEpoch != rotation.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != rotation.ExpectedProjectionRevision {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}
