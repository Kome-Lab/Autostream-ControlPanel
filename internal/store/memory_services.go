package store

import (
	"context"
	"github.com/example/autostream-control-panel/internal/security"
	"sort"
	"strings"
	"time"
)

func (s *MemoryAuthStore) CreateServiceToken(ctx context.Context, serviceType string, scopes []string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceType(serviceType); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceScopes(scopes); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	token := ServiceToken{ID: newUUID(), ServiceType: serviceType, Scopes: append([]string(nil), scopes...), RawToken: "ast_svc_" + raw, CreatedAt: time.Now().UTC()}
	token.TokenHash = security.HashToken(token.RawToken)
	s.mu.Lock()
	s.serviceTokens[token.ID] = token
	s.mu.Unlock()
	return token, nil
}

func (s *MemoryAuthStore) ListServiceTokens(ctx context.Context) ([]ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := make([]ServiceToken, 0, len(s.serviceTokens))
	for _, token := range s.serviceTokens {
		token.RawToken = ""
		token.TokenHash = ""
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

func (s *MemoryAuthStore) RevokeServiceToken(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.serviceTokens[id]
	if !ok || token.RevokedAt != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	token.RevokedAt = &now
	s.serviceTokens[id] = token
	for serviceID, service := range s.services {
		if service.TokenID != id {
			continue
		}
		service.LastHeartbeatAt = nil
		service.ReportedCapabilities = map[string]any{}
		service.UpdatedAt = now
		s.services[serviceID] = service
	}
	return nil
}

func (s *MemoryAuthStore) RotateServiceToken(ctx context.Context, id string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	oldToken, ok := s.serviceTokens[id]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, ErrNotFound
	}
	oldToken.RevokedAt = &now
	s.serviceTokens[id] = oldToken

	token := ServiceToken{
		ID:          newUUID(),
		ServiceType: oldToken.ServiceType,
		Scopes:      serviceTokenScopesForRotation(oldToken),
		RawToken:    "ast_svc_" + raw,
		CreatedAt:   now,
	}
	token.TokenHash = security.HashToken(token.RawToken)
	s.serviceTokens[token.ID] = token

	for serviceID, service := range s.services {
		if service.TokenID == id {
			service.TokenID = token.ID
			service.LastHeartbeatAt = nil
			service.ReportedCapabilities = map[string]any{}
			clearStagedNodeConfiguration(&service)
			service.NodeTokenRotatedAt = &now
			service.UpdatedAt = now
			s.services[serviceID] = service
		}
	}
	return token, nil
}

func (s *MemoryAuthStore) RotateServiceNodeToken(ctx context.Context, serviceID, expectedTokenID string, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	expectedTokenID = strings.TrimSpace(expectedTokenID)
	if serviceID == "" || expectedTokenID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	service, ok := s.services[serviceID]
	if !ok || service.TokenID != expectedTokenID {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if requiresStagedNodeTokenRotation(service) {
		return ServiceToken{}, RegisteredService{}, ErrConflict
	}
	oldToken, ok := s.serviceTokens[expectedTokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}

	now := time.Now().UTC()
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
	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenHash = ""
	service.ConfigureTokenExpiresAt = nil
	service.ConfigureTokenUsedAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	s.services[serviceID] = service
	return token, service, nil
}

func (s *MemoryAuthStore) AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error) {
	if err := ctx.Err(); err != nil {
		return ServiceToken{}, err
	}
	hash := security.HashToken(rawToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, token := range s.serviceTokens {
		if token.TokenHash == hash {
			if token.RevokedAt != nil {
				return ServiceToken{}, ErrUnauthorized
			}
			if requiredScope != "" && !hasString(token.Scopes, requiredScope) {
				return ServiceToken{}, ErrForbidden
			}
			return token, nil
		}
	}
	return ServiceToken{}, ErrUnauthorized
}
