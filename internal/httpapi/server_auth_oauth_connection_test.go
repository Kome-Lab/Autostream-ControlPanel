package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthAccountConnectionStartUsesProviderRedirectURI(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Login and Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"openid", "email", "profile"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"account_purpose":"drive"}`, provider.ID)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		AuthorizationURL string `json:"authorization_url"`
		AccountPurpose   string `json:"account_purpose"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(body.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := authorizationURL.Query().Get("redirect_uri"); got != provider.RedirectURI {
		t.Fatalf("connected account redirect_uri = %q, want %q", got, provider.RedirectURI)
	}
	if scope := authorizationURL.Query().Get("scope"); !strings.Contains(scope, "https://www.googleapis.com/auth/drive.file") {
		t.Fatalf("connected account scope missing drive access: %q", scope)
	}
	if body.AccountPurpose != store.OAuthAccountPurposeDrive {
		t.Fatalf("connected account purpose = %q, want %q", body.AccountPurpose, store.OAuthAccountPurposeDrive)
	}
}

func TestOAuthAccountConnectionCallbackStoresRefreshTokenWithoutLeak(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create", "integrations.read", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := fakeOAuthConnector{account: oauthlogin.ConnectedAccount{
		Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "archive@example.com"},
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"openid", "email", "https://www.googleapis.com/auth/youtube"},
	}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	state, oauthCookie := startOAuthAccountForTest(t, handler, cookie, csrf, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code","account_label":"YouTube Owner"}`, provider.ID, state)))
	req.AddCookie(cookie)
	req.AddCookie(oauthCookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("oauth account callback status = %d body = %s", res.Code, res.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, res.Result().Header)
	body := res.Body.String()
	for _, raw := range []string{"raw-google-refresh-token", "raw-google-client-secret", "client_secret"} {
		if strings.Contains(body, raw) {
			t.Fatalf("oauth account callback leaked secret-like value %q: %s", raw, body)
		}
	}
	var account store.OAuthAccount
	if err := json.NewDecoder(res.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	if !account.RefreshTokenConfigured || account.TokenFingerprint == "" || account.RefreshTokenUpdatedAt == "" || account.Email != "archive@example.com" {
		t.Fatalf("unexpected public account response: %#v", account)
	}
	if account.AccountPurpose != store.OAuthAccountPurposeYouTube {
		t.Fatalf("connected account purpose = %q, want %q", account.AccountPurpose, store.OAuthAccountPurposeYouTube)
	}
	dispatchAccount, err := integrations.GetOAuthAccountForDispatch(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatchAccount.RefreshToken != "raw-google-refresh-token" {
		t.Fatal("stored dispatch account does not contain refresh token")
	}
}

func TestOAuthAccountRelinkPreservesAccountIDAndReferences(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create", "integrations.read", "integrations.update"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: "配信用YouTube",
		Subject:      "google-subject-01",
		Email:        "old@example.com",
		Scopes:       []string{"openid", "email", "profile", "https://www.googleapis.com/auth/youtube.force-ssl"},
		RefreshToken: "old-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := fakeOAuthConnector{account: oauthlogin.ConnectedAccount{
		Identity:     oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: existing.Subject, Email: "new@example.com"},
		RefreshToken: "new-refresh-token",
		Scopes:       []string{"openid", "email", "profile", "https://www.googleapis.com/auth/youtube.force-ssl"},
	}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthConnector(connector))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"oauth_account_id":%q,"account_purpose":"youtube","redirect_after":"/admin/integrations/"}`, provider.ID, existing.ID)))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("oauth relink start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		State  string `json:"state"`
		Relink bool   `json:"relink"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	if startBody.State == "" || !startBody.Relink {
		t.Fatalf("oauth relink start did not bind target account: %#v", startBody)
	}

	callbackReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, startBody.State)))
	callbackReq.AddCookie(cookie)
	callbackReq.AddCookie(findCookieForTest(t, startRes.Result().Cookies(), oauthStateCookieName))
	callbackReq.Header.Set("X-CSRF-Token", csrf)
	callbackRes := httptest.NewRecorder()
	handler.ServeHTTP(callbackRes, callbackReq)
	if callbackRes.Code != http.StatusOK {
		t.Fatalf("oauth relink callback status = %d body = %s", callbackRes.Code, callbackRes.Body.String())
	}
	var relinked store.OAuthAccount
	if err := json.NewDecoder(callbackRes.Body).Decode(&relinked); err != nil {
		t.Fatal(err)
	}
	if relinked.ID != existing.ID || relinked.AccountLabel != existing.AccountLabel || relinked.Email != "new@example.com" {
		t.Fatalf("oauth relink changed account identity or label: before=%#v after=%#v", existing, relinked)
	}
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != existing.ID {
		t.Fatalf("oauth relink should preserve one referenced account: %#v", accounts)
	}
	dispatch, err := integrations.GetOAuthAccountForDispatch(t.Context(), existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.RefreshToken != "new-refresh-token" {
		t.Fatalf("oauth relink did not replace encrypted refresh token: %#v", dispatch)
	}
	if !strings.Contains(toJSONForTest(t, auth.AuditEvents()), `"action":"integrations.oauth_account.relink"`) {
		t.Fatal("oauth relink audit event was not recorded")
	}
}

func TestOAuthAccountRedirectCallbackSuppressesCodeCachingAndReferrers(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/integrations/oauth-accounts/callback?state="+url.QueryEscape(state)+"&code=callback-code&account_label=Drive+Owner", nil)
	req.AddCookie(cookie)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("oauth account redirect callback status = %d body = %s", res.Code, res.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, res.Result().Header)
}

func TestOAuthAccountRedirectCallbackUsesStartedAccountLabel(t *testing.T) {
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

	startReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"account_label":"Drive Owner"}`, provider.ID)))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	if startBody.State == "" {
		t.Fatal("oauth account start did not return state")
	}

	req := httptest.NewRequest(http.MethodGet, "/integrations/oauth-accounts/callback?state="+url.QueryEscape(startBody.State)+"&code=callback-code", nil)
	req.AddCookie(cookie)
	req.AddCookie(findCookieForTest(t, startRes.Result().Cookies(), oauthStateCookieName))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("oauth account redirect callback status = %d body = %s", res.Code, res.Body.String())
	}
	assertOAuthCallbackNoStoreHeaders(t, res.Result().Header)
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].AccountLabel != "Drive Owner" || accounts[0].DisplayName != "Drive Owner" || accounts[0].Email != "archive@example.com" {
		t.Fatalf("connected account label was not restored from state: %#v", accounts)
	}
}

func TestOAuthAccountRedirectCallbackDoesNotUseEmailAsDefaultLabel(t *testing.T) {
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

	startReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"account_purpose":"drive"}`, provider.ID)))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/integrations/oauth-accounts/callback?state="+url.QueryEscape(startBody.State)+"&code=callback-code", nil)
	req.AddCookie(cookie)
	req.AddCookie(findCookieForTest(t, startRes.Result().Cookies(), oauthStateCookieName))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("oauth account redirect callback status = %d body = %s", res.Code, res.Body.String())
	}
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].AccountLabel == "archive@example.com" || accounts[0].DisplayName == "archive@example.com" {
		t.Fatalf("connected account should not use email as display label: %#v", accounts)
	}
	if accounts[0].AccountLabel != "Google Drive" || !strings.HasPrefix(accounts[0].DisplayName, "Google Drive (") || accounts[0].ProviderName != "Google Drive" {
		t.Fatalf("connected account label should fall back to a distinct provider label: %#v", accounts)
	}
}
