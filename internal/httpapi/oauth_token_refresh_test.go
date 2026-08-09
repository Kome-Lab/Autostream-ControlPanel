package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

func TestRefreshOAuthTokensOnceRefreshesGoogleAccessTokenAndRecordsOnlyMetadata(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := &oauthTokenRefreshRoundTripper{}
	srv := NewServer(store.NewMemoryStreamStore(),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(ytlive.LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}),
	)
	result, err := srv.RefreshOAuthTokensOnce(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("unexpected refresh result: %#v", result)
	}
	if transport.calls != 1 {
		t.Fatalf("expected one provider refresh request, got %d", transport.calls)
	}
	updated, err := integrations.GetOAuthAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessTokenRefreshedAt == "" {
		t.Fatal("access token refresh timestamp was not recorded")
	}
	if updated.RefreshToken != "" {
		t.Fatal("public account exposed refresh token")
	}
	dispatch, err := integrations.GetOAuthAccountForDispatch(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.RefreshToken != "google-refresh-token" {
		t.Fatalf("refresh token changed unexpectedly: %#v", dispatch)
	}
}

func TestRefreshOAuthTokensOnceRecordsSafeReauthorizationFailureAndClearsItOnSuccess(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := &oauthTokenRefreshFailureThenSuccessRoundTripper{}
	srv := NewServer(store.NewMemoryStreamStore(),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(ytlive.LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}),
	)
	result, err := srv.RefreshOAuthTokensOnce(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["refreshed"] != 0 || result["failed"] != 1 {
		t.Fatalf("unexpected failed refresh result: %#v", result)
	}
	failed, err := integrations.GetOAuthAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.AccessTokenRefreshAttemptedAt == "" || failed.AccessTokenRefreshFailedAt == "" {
		t.Fatalf("failed refresh did not record timestamps: %#v", failed)
	}
	if failed.AccessTokenRefreshFailureCode != store.OAuthTokenRefreshFailureReauthorizationRequired || !failed.AccessTokenRefreshRelinkRequired {
		t.Fatalf("failed refresh metadata = %#v", failed)
	}
	publicBody, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"google-refresh-token", "provider error description that must not persist"} {
		if strings.Contains(string(publicBody), forbidden) {
			t.Fatalf("public refresh metadata leaked %q: %s", forbidden, publicBody)
		}
	}

	result, err = srv.RefreshOAuthTokensOnce(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["refreshed"] != 1 || result["failed"] != 0 {
		t.Fatalf("unexpected successful refresh result: %#v", result)
	}
	updated, err := integrations.GetOAuthAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessTokenRefreshedAt == "" || updated.AccessTokenRefreshAttemptedAt == "" {
		t.Fatalf("successful refresh did not record timestamps: %#v", updated)
	}
	if updated.AccessTokenRefreshFailedAt != "" || updated.AccessTokenRefreshFailureCode != "" || updated.AccessTokenRefreshRelinkRequired {
		t.Fatalf("successful refresh did not clear failure metadata: %#v", updated)
	}
}

func TestRefreshOAuthTokensOnceDoesNotPersistStaleResultAfterRelink(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "old-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := &oauthTokenRefreshBlockingRoundTripper{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	srv := NewServer(store.NewMemoryStreamStore(),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(ytlive.LiveAPIClient{HTTPClient: &http.Client{Transport: transport}}),
	)
	type refreshOutcome struct {
		result map[string]any
		err    error
	}
	resultCh := make(chan refreshOutcome, 1)
	go func() {
		result, err := srv.RefreshOAuthTokensOnce(context.Background(), 10)
		resultCh <- refreshOutcome{result: result, err: err}
	}()
	select {
	case <-transport.started:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh token request did not start")
	}

	if _, err := integrations.UpdateOAuthAccount(t.Context(), store.OAuthAccount{
		ID:           account.ID,
		ProviderID:   account.ProviderID,
		ProviderType: account.ProviderType,
		AccountLabel: account.AccountLabel,
		Scopes:       account.Scopes,
		RefreshToken: "new-refresh-token",
	}); err != nil {
		t.Fatal(err)
	}
	close(transport.release)

	var outcome refreshOutcome
	select {
	case outcome = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh token run did not finish")
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.result["attempted"] != 1 || outcome.result["refreshed"] != 0 || outcome.result["failed"] != 0 || outcome.result["skipped"] != 1 {
		t.Fatalf("unexpected stale refresh result: %#v", outcome.result)
	}
	updated, err := integrations.GetOAuthAccountForDispatch(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RefreshToken != "new-refresh-token" {
		t.Fatalf("stale provider response overwrote re-linked refresh token: got %q", updated.RefreshToken)
	}
	if updated.AccessTokenRefreshedAt != "" || updated.AccessTokenRefreshAttemptedAt != "" || updated.AccessTokenRefreshFailedAt != "" || updated.AccessTokenRefreshFailureCode != "" || updated.AccessTokenRefreshRelinkRequired {
		t.Fatalf("stale provider response overwrote re-link metadata: %#v", updated)
	}
}

type oauthTokenRefreshRoundTripper struct {
	calls int
}

type oauthTokenRefreshFailureThenSuccessRoundTripper struct {
	calls int
}

type oauthTokenRefreshBlockingRoundTripper struct {
	started chan struct{}
	release chan struct{}
}

func (r *oauthTokenRefreshBlockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "oauth2.googleapis.com" || req.URL.Path != "/token" {
		return oauthRefreshResponse(req, http.StatusNotFound, `{"error":"unexpected request"}`), nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(body, []byte("grant_type=refresh_token")) || !bytes.Contains(body, []byte("refresh_token=old-refresh-token")) {
		return oauthRefreshResponse(req, http.StatusBadRequest, `{"error":"bad refresh request"}`), nil
	}
	r.started <- struct{}{}
	select {
	case <-r.release:
		return oauthRefreshResponse(req, http.StatusOK, `{"access_token":"short-lived-access-token","refresh_token":"stale-rotated-refresh-token","token_type":"Bearer","expires_in":3600}`), nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func (r *oauthTokenRefreshFailureThenSuccessRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "oauth2.googleapis.com" || req.URL.Path != "/token" {
		return oauthRefreshResponse(req, http.StatusNotFound, `{"error":"unexpected request"}`), nil
	}
	r.calls++
	if r.calls == 1 {
		return oauthRefreshResponse(req, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"provider error description that must not persist"}`), nil
	}
	return oauthRefreshResponse(req, http.StatusOK, `{"access_token":"short-lived-access-token","token_type":"Bearer","expires_in":3600}`), nil
}

func (r *oauthTokenRefreshRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "oauth2.googleapis.com" || req.URL.Path != "/token" {
		return oauthRefreshResponse(req, http.StatusNotFound, `{"error":"unexpected request"}`), nil
	}
	r.calls++
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(body, []byte("grant_type=refresh_token")) || !bytes.Contains(body, []byte("refresh_token=google-refresh-token")) {
		return oauthRefreshResponse(req, http.StatusBadRequest, `{"error":"bad refresh request"}`), nil
	}
	return oauthRefreshResponse(req, http.StatusOK, `{"access_token":"short-lived-access-token","token_type":"Bearer","expires_in":3600}`), nil
}

func oauthRefreshResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}
}
