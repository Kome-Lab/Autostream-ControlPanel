package httpapi

import (
	"bytes"
	"fmt"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthLoginRedirectCallbackCompletesConnectedAccountState(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create", "integrations.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"openid", "email", "profile"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	var connectorRedirectURI string
	connector := fakeOAuthConnector{
		account: oauthlogin.ConnectedAccount{
			Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "archive@example.com"},
			RefreshToken: "raw-google-refresh-token",
			Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/drive.file"},
		},
		onConnect: func(req oauthlogin.ConnectRequest) {
			connectorRedirectURI = req.Provider.RedirectURI
		},
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, provider.ID)
	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(state)+"&code=callback-code&account_label=Drive+Owner", nil)
	req.AddCookie(cookie)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("oauth shared redirect callback status = %d body = %s", res.Code, res.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, res.Result().Header)
	if connectorRedirectURI != provider.RedirectURI {
		t.Fatalf("oauth connector redirect_uri = %q, want %q", connectorRedirectURI, provider.RedirectURI)
	}
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Email != "archive@example.com" || !accounts[0].RefreshTokenConfigured {
		t.Fatalf("connected account was not stored correctly: %#v", accounts)
	}
}

func TestOAuthLoginRedirectCallbackKeepsConnectedAccountStateWhenUnauthorized(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"openid", "email", "profile"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := fakeOAuthConnector{account: oauthlogin.ConnectedAccount{
		Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "archive@example.com"},
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/drive.file"},
	}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, provider.ID)
	missingSessionReq := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(state)+"&code=callback-code", nil)
	missingSessionReq.AddCookie(oauthCookie)
	missingSessionRes := httptest.NewRecorder()
	handler.ServeHTTP(missingSessionRes, missingSessionReq)
	if missingSessionRes.Code != http.StatusUnauthorized || !strings.Contains(missingSessionRes.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized shared callback, status=%d body=%s", missingSessionRes.Code, missingSessionRes.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, missingSessionRes.Result().Header)

	retryReq := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(state)+"&code=callback-code", nil)
	retryReq.AddCookie(cookie)
	retryReq.AddCookie(oauthCookie)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusSeeOther {
		t.Fatalf("connected account state was consumed before authorization, retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}
}

func TestOAuthAccountConnectionCallbackRejectsProviderMismatch(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	providerA, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{ProviderType: "google", Name: "Google A", Enabled: true, ClientID: "client-a", ClientSecret: "secret-a", RedirectURI: "https://control.example.com/integrations/oauth-accounts/callback"})
	if err != nil {
		t.Fatal(err)
	}
	providerB, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{ProviderType: "google", Name: "Google B", Enabled: true, ClientID: "client-b", ClientSecret: "secret-b", RedirectURI: "https://control.example.com/integrations/oauth-accounts/callback"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(fakeOAuthConnector{}))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, providerA.ID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, providerB.ID, state)))
	req.AddCookie(cookie)
	req.AddCookie(oauthCookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "invalid_oauth_state") {
		t.Fatalf("expected provider mismatch rejection, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestOAuthAccountConnectionCallbackRequiresStateCookieBeforeConsume(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "client-id",
		ClientSecret: "secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := fakeOAuthConnector{account: oauthlogin.ConnectedAccount{
		Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "archive@example.com"},
		RefreshToken: "raw-google-refresh-token",
	}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "invalid_oauth_state") {
		t.Fatalf("expected state cookie rejection, status=%d body=%s", res.Code, res.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	retryReq.AddCookie(cookie)
	retryReq.AddCookie(oauthCookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusCreated {
		t.Fatalf("state was consumed before cookie verification, retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}
}

func TestOAuthAccountConnectionPostCallbackRequiresCSRFBeforeConsume(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "client-id",
		ClientSecret: "secret",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := fakeOAuthConnector{account: oauthlogin.ConnectedAccount{
		Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "archive@example.com"},
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/drive.file"},
	}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, provider.ID)
	body := fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)
	missingCSRFReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(body))
	missingCSRFReq.AddCookie(cookie)
	missingCSRFReq.AddCookie(oauthCookie)
	missingCSRFRes := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRFRes, missingCSRFReq)
	if missingCSRFRes.Code != http.StatusForbidden || !strings.Contains(missingCSRFRes.Body.String(), "csrf_failed") {
		t.Fatalf("missing csrf callback status=%d body=%s", missingCSRFRes.Code, missingCSRFRes.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, missingCSRFRes.Header())

	retryReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(body))
	retryReq.AddCookie(cookie)
	retryReq.AddCookie(oauthCookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusCreated {
		t.Fatalf("state was consumed before CSRF verification, retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}
}
