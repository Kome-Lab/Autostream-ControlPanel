package store

import (
	"context"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s *MemoryAuthStore) SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.ConfigureTokenExpiresAt = &expiresAt
	svc.ConfigureTokenUsedAt = nil
	svc.ConfigureTokenHash = tokenHash
	pendingStagedTokenID := ""
	if svc.StagedNodeTokenID != "" && svc.StagedNodeTokenID != svc.TokenID {
		pendingStagedTokenID = svc.StagedNodeTokenID
	}
	clearStagedNodeConfiguration(&svc)
	svc.StagedNodeTokenID = pendingStagedTokenID
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) ValidateServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrNotFound
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return false, ErrNotFound
	}
	return svc.ConfigureTokenHash != "" &&
		svc.ConfigureTokenExpiresAt != nil &&
		svc.ConfigureTokenUsedAt == nil &&
		now.Before(svc.ConfigureTokenExpiresAt.UTC()) &&
		security.VerifyTokenHash(rawToken, svc.ConfigureTokenHash), nil
}

func (s *MemoryAuthStore) ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if svc.ConfigureTokenHash == "" || svc.ConfigureTokenExpiresAt == nil || svc.ConfigureTokenUsedAt != nil || !now.Before(*svc.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawToken, svc.ConfigureTokenHash) {
		return RegisteredService{}, ErrUnauthorized
	}
	svc.ConfigureTokenUsedAt = &now
	svc.UpdatedAt = now
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) ConfigureServiceNode(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, report ServiceRuntimeReport, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType == "update_agent" {
		return ServiceToken{}, RegisteredService{}, ErrTwoPhaseConfigureRequired
	}
	if service.ConfigureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, service.ConfigureTokenHash) {
		return ServiceToken{}, RegisteredService{}, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}

	token, _, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	oldTokenStillReferenced := false
	for candidateID, candidate := range s.services {
		if candidateID != serviceID && candidate.TokenID == oldToken.ID {
			oldTokenStillReferenced = true
			break
		}
	}
	if !oldTokenStillReferenced {
		oldToken.RevokedAt = &now
	}
	s.serviceTokens[oldToken.ID] = oldToken
	s.serviceTokens[token.ID] = token
	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenUsedAt = &now
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return token, service, nil
}

func (s *MemoryAuthStore) StageServiceNodeConfiguration(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, seal NodeTokenSealer) (StagedServiceNodeConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if seal == nil {
		return StagedServiceNodeConfiguration{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	if service.ConfigureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, service.ConfigureTokenHash) {
		return StagedServiceNodeConfiguration{}, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.RevokedAt != nil &&
		(!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken) ||
			(hasStagedServiceNodeConfiguration(service) &&
				!hasOnlyStagedNodeConfigurationTombstone(service))) {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	for candidateID, candidate := range s.services {
		if !updateAgentStageTokenReferenceAllowed(
			candidateID,
			candidate.TokenID,
			candidate.StagedNodePreviousTokenID,
			candidate.StagedNodeTokenID,
			serviceID,
			oldToken.ID,
		) {
			return StagedServiceNodeConfiguration{}, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	token, _, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationRandom, err := security.RandomToken(32)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if err := ctx.Err(); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationToken := "ast_act_" + activationRandom
	service.StagedNodePreviousTokenID = oldToken.ID
	service.StagedNodeTokenID = token.ID
	service.StagedNodeTokenHash = token.TokenHash
	service.StagedNodeTokenScopes = append([]string(nil), token.Scopes...)
	service.StagedNodeTokenCiphertext = ciphertext
	service.StagedNodeTokenNonce = nonce
	service.StagedNodeActivationTokenHash = security.HashToken(activationToken)
	service.StagedNodeTokenAt = &now
	service.ConfigureTokenUsedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return StagedServiceNodeConfiguration{Token: token, Service: service, ActivationToken: activationToken, ActivationExpiresAt: *service.ConfigureTokenExpiresAt}, nil
}

func (s *MemoryAuthStore) ActivateServiceNodeConfiguration(ctx context.Context, serviceID, configurationID, rawActivationToken string, now time.Time, report ServiceRuntimeReport) (ServiceToken, RegisteredService, bool, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	serviceID = strings.TrimSpace(serviceID)
	configurationID = strings.TrimSpace(configurationID)
	rawActivationToken = strings.TrimSpace(rawActivationToken)
	if serviceID == "" || configurationID == "" || rawActivationToken == "" {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok {
		return ServiceToken{}, RegisteredService{}, false, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return ServiceToken{}, RegisteredService{}, false, ErrForbidden
	}
	if service.TokenID == configurationID && service.StagedNodeTokenID == "" {
		if service.ConfigureTokenUsedAt == nil ||
			service.ConfigureTokenExpiresAt != nil ||
			service.ConfigureTokenHash == "" ||
			!security.VerifyTokenHash(rawActivationToken, service.ConfigureTokenHash) {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		token, ok := s.serviceTokens[service.TokenID]
		if !ok || token.RevokedAt != nil || token.ServiceType != service.ServiceType {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		return token, service, true, nil
	}
	if service.StagedNodeTokenID != configurationID || service.StagedNodeTokenHash == "" || service.StagedNodeActivationTokenHash == "" || !security.VerifyTokenHash(rawActivationToken, service.StagedNodeActivationTokenHash) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if service.TokenID == service.StagedNodeTokenID {
		token, ok := s.serviceTokens[service.TokenID]
		if !ok || token.RevokedAt != nil || token.TokenHash != service.StagedNodeTokenHash {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		return token, service, true, nil
	}
	if service.ConfigureTokenExpiresAt == nil || !now.Before(*service.ConfigureTokenExpiresAt) || service.StagedNodeTokenAt == nil || service.TokenID != service.StagedNodePreviousTokenID {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	oldToken, ok := s.serviceTokens[service.TokenID]
	if !ok ||
		oldToken.ServiceType != "update_agent" ||
		(oldToken.RevokedAt != nil &&
			!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken)) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if err := validateRequiredUpdateAgentScopes(service.ServiceType, service.StagedNodeTokenScopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	for candidateID, candidate := range s.services {
		if !updateAgentActivationTokenReferenceAllowed(
			candidateID,
			candidate.TokenID,
			candidate.StagedNodePreviousTokenID,
			candidate.StagedNodeTokenID,
			serviceID,
			oldToken.ID,
			service.StagedNodeTokenID,
		) {
			return ServiceToken{}, RegisteredService{}, false, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	token := ServiceToken{ID: service.StagedNodeTokenID, ServiceType: "update_agent", Scopes: append([]string(nil), service.StagedNodeTokenScopes...), TokenHash: service.StagedNodeTokenHash, CreatedAt: service.StagedNodeTokenAt.UTC()}
	if err := validateServiceScopes(token.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	s.serviceTokens[token.ID] = token
	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = service.StagedNodeTokenCiphertext
	service.NodeTokenNonce = service.StagedNodeTokenNonce
	service.ConfigureTokenHash = service.StagedNodeActivationTokenHash
	service.ConfigureTokenExpiresAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	oldTokenStillReferenced := false
	for _, candidate := range s.services {
		if candidate.TokenID == oldToken.ID ||
			candidate.StagedNodePreviousTokenID == oldToken.ID ||
			candidate.StagedNodeTokenID == oldToken.ID {
			oldTokenStillReferenced = true
			break
		}
	}
	if !oldTokenStillReferenced && oldToken.RevokedAt == nil {
		oldToken.RevokedAt = &now
		s.serviceTokens[oldToken.ID] = oldToken
	}
	return token, service, false, nil
}

func (s *MemoryAuthStore) SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.NodeTokenCiphertext = ciphertext
	svc.NodeTokenNonce = nonce
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}
