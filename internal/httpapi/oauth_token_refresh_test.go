package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

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

type oauthTokenRefreshRoundTripper struct {
	calls int
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
