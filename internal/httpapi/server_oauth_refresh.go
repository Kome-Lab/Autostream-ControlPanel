package httpapi

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"golang.org/x/oauth2"
)

const oauthTokenRefreshDefaultInterval = 45 * time.Minute

// RunOAuthTokenRefreshLoop keeps short-lived provider access tokens warm. It
// never persists the access token; only the timestamp and any provider-issued
// refresh-token rotation are recorded.
func (s *Server) RunOAuthTokenRefreshLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = oauthTokenRefreshDefaultInterval
	}
	if _, err := s.RefreshOAuthTokensOnce(ctx, 100); err != nil {
		log.Printf("oauth token refresh scan failed: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.RefreshOAuthTokensOnce(ctx, 100); err != nil {
				log.Printf("oauth token refresh scan failed: %v", err)
			}
		}
	}
}

// RefreshOAuthTokensOnce refreshes configured Google OAuth accounts. The
// access token is intentionally discarded after the provider call; only a
// rotated refresh token and bounded, non-secret operational metadata are
// retained. Provider error bodies and descriptions are never persisted.
func (s *Server) RefreshOAuthTokensOnce(ctx context.Context, limit int) (map[string]any, error) {
	result := map[string]any{"attempted": 0, "refreshed": 0, "failed": 0, "skipped": 0}
	refresher, ok := s.youtubeLive.(oauthAccessTokenRefresher)
	if !ok {
		return result, nil
	}
	if limit <= 0 {
		limit = 100
	}
	accounts, err := s.integrations.ListOAuthAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, listed := range accounts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if result["attempted"].(int) >= limit {
			break
		}
		if !strings.EqualFold(strings.TrimSpace(listed.ProviderType), "google") || !listed.RefreshTokenConfigured {
			result["skipped"] = result["skipped"].(int) + 1
			continue
		}
		attemptedAt := time.Now().UTC()
		result["attempted"] = result["attempted"].(int) + 1
		account, err := s.integrations.GetOAuthAccountForDispatch(ctx, listed.ID)
		if err != nil || strings.TrimSpace(account.RefreshToken) == "" {
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh unavailable: account_id=%s error=%s", listed.ID, ytlive.RedactedError(err))
			continue
		}
		expectedTokenRevision := account.TokenRevision
		if _, err := s.integrations.RecordOAuthAccountTokenRefreshAttempt(ctx, listed.ID, expectedTokenRevision, attemptedAt); err != nil {
			if errors.Is(err, store.ErrConflict) {
				result["skipped"] = result["skipped"].(int) + 1
				log.Printf("oauth token refresh skipped after account credentials changed: account_id=%s", listed.ID)
				continue
			}
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh attempt state save failed: account_id=%s error=%s", listed.ID, ytlive.RedactedError(err))
			continue
		}
		provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, account.ProviderID)
		if err != nil || !provider.Enabled || !strings.EqualFold(strings.TrimSpace(provider.ProviderType), "google") || strings.TrimSpace(provider.ClientID) == "" || strings.TrimSpace(provider.ClientSecret) == "" {
			if persistErr := s.recordOAuthTokenRefreshFailure(ctx, listed.ID, expectedTokenRevision, store.OAuthTokenRefreshFailureProviderNotReady, false, attemptedAt); errors.Is(persistErr, store.ErrConflict) {
				result["skipped"] = result["skipped"].(int) + 1
				log.Printf("oauth token refresh failure discarded after account credentials changed: account_id=%s", listed.ID)
				continue
			} else if persistErr != nil {
				result["failed"] = result["failed"].(int) + 1
				log.Printf("oauth token refresh failure state save failed: account_id=%s error=%s", listed.ID, ytlive.RedactedError(persistErr))
				continue
			}
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh provider unavailable: account_id=%s provider_id=%s error=%s", listed.ID, account.ProviderID, ytlive.RedactedError(err))
			continue
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		token, refreshErr := refresher.RefreshAccessToken(refreshCtx, ytlive.OAuthCredentials{ClientID: provider.ClientID, ClientSecret: provider.ClientSecret, RefreshToken: account.RefreshToken})
		cancel()
		if refreshErr != nil {
			failureCode, relinkRequired := classifyOAuthTokenRefreshFailure(refreshErr)
			if persistErr := s.recordOAuthTokenRefreshFailure(ctx, listed.ID, expectedTokenRevision, failureCode, relinkRequired, attemptedAt); errors.Is(persistErr, store.ErrConflict) {
				result["skipped"] = result["skipped"].(int) + 1
				log.Printf("oauth token refresh failure discarded after account credentials changed: account_id=%s", listed.ID)
				continue
			} else if persistErr != nil {
				result["failed"] = result["failed"].(int) + 1
				log.Printf("oauth token refresh failure state save failed: account_id=%s error=%s", listed.ID, ytlive.RedactedError(persistErr))
				continue
			}
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh failed: account_id=%s provider=%s error=%s", account.ID, provider.ProviderType, ytlive.RedactedError(refreshErr))
			continue
		}
		if token == nil || strings.TrimSpace(token.AccessToken) == "" {
			if persistErr := s.recordOAuthTokenRefreshFailure(ctx, listed.ID, expectedTokenRevision, store.OAuthTokenRefreshFailureInvalidResponse, false, attemptedAt); errors.Is(persistErr, store.ErrConflict) {
				result["skipped"] = result["skipped"].(int) + 1
				log.Printf("oauth token refresh failure discarded after account credentials changed: account_id=%s", listed.ID)
				continue
			} else if persistErr != nil {
				result["failed"] = result["failed"].(int) + 1
				log.Printf("oauth token refresh failure state save failed: account_id=%s error=%s", listed.ID, ytlive.RedactedError(persistErr))
				continue
			}
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh failed: account_id=%s provider=%s error=empty_access_token", account.ID, provider.ProviderType)
			continue
		}
		rotatedRefreshToken := ""
		if token != nil && strings.TrimSpace(token.RefreshToken) != "" && token.RefreshToken != account.RefreshToken {
			rotatedRefreshToken = token.RefreshToken
		}
		if _, err := s.integrations.RecordOAuthAccountTokenRefresh(ctx, account.ID, expectedTokenRevision, rotatedRefreshToken, attemptedAt); err != nil {
			if errors.Is(err, store.ErrConflict) {
				result["skipped"] = result["skipped"].(int) + 1
				log.Printf("oauth token refresh result discarded after account credentials changed: account_id=%s", account.ID)
				continue
			}
			result["failed"] = result["failed"].(int) + 1
			log.Printf("oauth token refresh state save failed: account_id=%s error=%s", account.ID, ytlive.RedactedError(err))
			continue
		}
		result["refreshed"] = result["refreshed"].(int) + 1
	}
	return result, nil
}

func (s *Server) recordOAuthTokenRefreshFailure(ctx context.Context, accountID string, expectedTokenRevision uint64, failureCode string, relinkRequired bool, failedAt time.Time) error {
	_, err := s.integrations.RecordOAuthAccountTokenRefreshFailure(ctx, accountID, expectedTokenRevision, failureCode, relinkRequired, failedAt)
	return err
}

func classifyOAuthTokenRefreshFailure(err error) (string, bool) {
	if err == nil {
		return store.OAuthTokenRefreshFailureUnknown, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return store.OAuthTokenRefreshFailureTimedOut, false
	}
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		switch strings.TrimSpace(strings.ToLower(retrieveErr.ErrorCode)) {
		case "invalid_grant":
			return store.OAuthTokenRefreshFailureReauthorizationRequired, true
		case "invalid_client", "unauthorized_client":
			return store.OAuthTokenRefreshFailureProviderCredentialsInvalid, false
		case "temporarily_unavailable", "server_error":
			return store.OAuthTokenRefreshFailureProviderUnavailable, false
		}
		if retrieveErr.Response != nil && (retrieveErr.Response.StatusCode == http.StatusTooManyRequests || retrieveErr.Response.StatusCode >= http.StatusInternalServerError) {
			return store.OAuthTokenRefreshFailureProviderUnavailable, false
		}
		return store.OAuthTokenRefreshFailureUnknown, false
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return store.OAuthTokenRefreshFailureTimedOut, false
		}
		return store.OAuthTokenRefreshFailureProviderUnavailable, false
	}
	return store.OAuthTokenRefreshFailureUnknown, false
}
