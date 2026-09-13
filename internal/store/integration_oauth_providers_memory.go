package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *MemoryIntegrationStore) ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OAuthProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		out = append(out, publicOAuthProvider(provider))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryIntegrationStore) CreateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	provider, err := normalizeOAuthProvider(provider, true)
	if err != nil {
		return OAuthProvider{}, err
	}
	provider.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	provider.CreatedAt, provider.UpdatedAt = now, now
	provider.ClientSecretConfigured = provider.ClientSecret != ""
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.providers {
		if strings.EqualFold(existing.Name, provider.Name) {
			return OAuthProvider{}, errors.New("oauth provider name already exists")
		}
	}
	s.providers[provider.ID] = provider
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) GetOAuthProvider(ctx context.Context, id string) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) GetOAuthProviderForDispatch(ctx context.Context, id string) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	return provider, nil
}

func (s *MemoryIntegrationStore) UpdateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	provider, err := normalizeOAuthProvider(provider, false)
	if err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.providers[provider.ID]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	for id, item := range s.providers {
		if id != provider.ID && strings.EqualFold(item.Name, provider.Name) {
			return OAuthProvider{}, errors.New("oauth provider name already exists")
		}
	}
	if provider.ClientSecret == "" {
		provider.ClientSecret = existing.ClientSecret
		provider.ClientSecretConfigured = existing.ClientSecretConfigured
	} else {
		provider.ClientSecretConfigured = true
	}
	provider.CreatedAt = existing.CreatedAt
	provider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.providers[provider.ID] = provider
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) DeleteOAuthProvider(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return ErrNotFound
	}
	delete(s.providers, id)
	return nil
}
