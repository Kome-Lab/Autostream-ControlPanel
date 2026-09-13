package store

import (
	"context"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s *MemoryIntegrationStore) ListOAuthAccounts(ctx context.Context) ([]OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OAuthAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		out = append(out, s.publicOAuthAccountLocked(account))
	}
	sortOAuthAccounts(out)
	return out, nil
}

func (s *MemoryIntegrationStore) CreateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	account, err := normalizeOAuthAccount(account, true)
	if err != nil {
		return OAuthAccount{}, err
	}
	account.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	account.CreatedAt, account.UpdatedAt = now, now
	account.RefreshTokenConfigured = account.RefreshToken != ""
	account.TokenRevision = 1
	if account.RefreshToken != "" {
		account.TokenFingerprint = security.SecretFingerprint(account.RefreshToken)
		account.RefreshTokenUpdatedAt = now
	}
	s.mu.Lock()
	s.accounts[account.ID] = account
	response := s.publicOAuthAccountLocked(account)
	s.mu.Unlock()
	return response, nil
}

func (s *MemoryIntegrationStore) GetOAuthAccount(ctx context.Context, id string) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) GetOAuthAccountForDispatch(ctx context.Context, id string) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	return account, nil
}

func (s *MemoryIntegrationStore) UpdateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	account, err := normalizeOAuthAccount(account, false)
	if err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.accounts[account.ID]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if account.RefreshToken == "" {
		account.RefreshToken = existing.RefreshToken
		account.RefreshTokenConfigured = existing.RefreshTokenConfigured
		account.TokenFingerprint = existing.TokenFingerprint
		account.TokenRevision = existing.TokenRevision
		account.RefreshTokenUpdatedAt = existing.RefreshTokenUpdatedAt
		account.AccessTokenRefreshedAt = existing.AccessTokenRefreshedAt
		account.AccessTokenRefreshAttemptedAt = existing.AccessTokenRefreshAttemptedAt
		account.AccessTokenRefreshFailedAt = existing.AccessTokenRefreshFailedAt
		account.AccessTokenRefreshFailureCode = existing.AccessTokenRefreshFailureCode
		account.AccessTokenRefreshRelinkRequired = existing.AccessTokenRefreshRelinkRequired
	} else {
		account.RefreshTokenConfigured = true
		account.TokenFingerprint = security.SecretFingerprint(account.RefreshToken)
		account.RefreshTokenUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		// A successful re-link replaces the credentials that caused any prior
		// refresh failure. The next automatic refresh records fresh metadata.
		account.AccessTokenRefreshedAt = ""
		account.AccessTokenRefreshAttemptedAt = ""
		account.AccessTokenRefreshFailedAt = ""
		account.AccessTokenRefreshFailureCode = ""
		account.AccessTokenRefreshRelinkRequired = false
		account.TokenRevision = nextOAuthAccountTokenRevision(existing.TokenRevision)
	}
	account.CreatedAt = existing.CreatedAt
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefreshAttempt(ctx context.Context, id string, expectedTokenRevision uint64, attemptedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	attemptedAt = normalizedOAuthRefreshTime(attemptedAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[strings.TrimSpace(id)]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	account.AccessTokenRefreshAttemptedAt = attemptedAt.Format(time.RFC3339)
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefreshFailure(ctx context.Context, id string, expectedTokenRevision uint64, failureCode string, relinkRequired bool, failedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	failedAt = normalizedOAuthRefreshTime(failedAt)
	failureCode, relinkRequired = normalizedOAuthTokenRefreshFailure(failureCode, relinkRequired)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[strings.TrimSpace(id)]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	account.AccessTokenRefreshAttemptedAt = failedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailedAt = failedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailureCode = failureCode
	account.AccessTokenRefreshRelinkRequired = relinkRequired
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefresh(ctx context.Context, id string, expectedTokenRevision uint64, rotatedRefreshToken string, refreshedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	refreshedAt = normalizedOAuthRefreshTime(refreshedAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	if rotatedRefreshToken = strings.TrimSpace(rotatedRefreshToken); rotatedRefreshToken != "" && rotatedRefreshToken != account.RefreshToken {
		account.RefreshToken = rotatedRefreshToken
		account.RefreshTokenConfigured = true
		account.TokenFingerprint = security.SecretFingerprint(rotatedRefreshToken)
		account.TokenRevision = nextOAuthAccountTokenRevision(account.TokenRevision)
		account.RefreshTokenUpdatedAt = refreshedAt.Format(time.RFC3339)
	}
	account.AccessTokenRefreshAttemptedAt = refreshedAt.Format(time.RFC3339)
	account.AccessTokenRefreshedAt = refreshedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailedAt = ""
	account.AccessTokenRefreshFailureCode = ""
	account.AccessTokenRefreshRelinkRequired = false
	s.accounts[id] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) publicOAuthAccountLocked(account OAuthAccount) OAuthAccount {
	if provider, ok := s.providers[account.ProviderID]; ok {
		account.ProviderName = provider.Name
	}
	return publicOAuthAccount(account)
}

func (s *MemoryIntegrationStore) DeleteOAuthAccount(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(s.accounts, id)
	return nil
}
