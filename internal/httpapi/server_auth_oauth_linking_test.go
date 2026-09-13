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

func TestCurrentUserCanLinkOAuthProviderFromAccountSettings(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "self-user", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Login",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"openid", "email"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-link", Email: "operator@example.com", EmailVerified: true}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/auth/oauth-links/"+provider.ID+"/start", bytes.NewBufferString(`{"redirect_after":"/admin/account/"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("oauth link start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		State            string `json:"state"`
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	if startBody.State == "" || !strings.Contains(startBody.AuthorizationURL, "accounts.google.com") {
		t.Fatalf("oauth link start missing authorization details: %#v", startBody)
	}
	oauthCookie := findCookieForTest(t, startRes.Result().Cookies(), oauthStateCookieName)
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(startBody.State)+"&code=callback-code", nil)
	callbackReq.AddCookie(cookie)
	callbackReq.AddCookie(oauthCookie)
	callbackRes := httptest.NewRecorder()
	handler.ServeHTTP(callbackRes, callbackReq)
	if callbackRes.Code != http.StatusSeeOther || callbackRes.Header().Get("Location") != "/admin/account/" {
		t.Fatalf("oauth link callback status=%d location=%q body=%s", callbackRes.Code, callbackRes.Header().Get("Location"), callbackRes.Body.String())
	}
	link, err := oauthStore.FindOAuthUserLink(t.Context(), provider.ID, "google-subject-link")
	if err != nil {
		t.Fatalf("oauth user link was not created: %v", err)
	}
	if link.UserID != "self-user" || link.Email != "operator@example.com" {
		t.Fatalf("unexpected oauth link: %#v", link)
	}
	if strings.Contains(callbackRes.Body.String(), "raw-google-client-secret") {
		t.Fatalf("oauth link callback leaked client secret: %s", callbackRes.Body.String())
	}
}

func TestOAuthAccountConnectionRejectsLoginStatePurpose(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Login",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore))
	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/integrations/oauth-accounts/callback?state="+url.QueryEscape(state)+"&code=callback-code", nil)
	req.AddCookie(cookie)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "invalid_oauth_state") {
		t.Fatalf("connected account callback should reject login state purpose, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestOAuthLoginAutoProvisionedUserStillRequiresMFAWhenRoleScoped(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	adminRole, err := auth.CreateRole(t.Context(), "admin", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "totp",
		MFARequiredRoles:         []string{"admin"},
	}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType:   "google",
		Name:           "Google Login",
		Enabled:        true,
		ClientID:       "google-client-id",
		ClientSecret:   "raw-google-client-secret",
		Scopes:         []string{"openid", "email"},
		AllowedDomains: []string{"example.com"},
		AutoProvision:  true,
		DefaultRoleIDs: []string{adminRole.ID},
		RedirectURI:    "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-mfa", Email: "operator@example.com", EmailVerified: true}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier), WithSecuritySettingsStore(settings), WithMFAStore(auth))

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "mfa_enrollment_required") {
		t.Fatalf("auto-provisioned admin should require MFA enrollment, status=%d body=%s", res.Code, res.Body.String())
	}
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			t.Fatalf("OAuth auto-provision must not issue a session cookie before required MFA enrollment: %#v", cookie)
		}
	}
	if strings.Contains(res.Body.String(), "csrf_token") {
		t.Fatal("OAuth auto-provision must not issue a session cookie before required MFA enrollment")
	}
	user, err := auth.FindUserByUsername(t.Context(), "operator@example.com")
	if err != nil {
		t.Fatalf("auto-provisioned user not found: %v", err)
	}
	if !hasString(user.Roles, "admin") {
		t.Fatalf("expected auto-provisioned user to receive admin role, got %#v", user.Roles)
	}
	if _, err := oauthStore.FindOAuthUserLink(t.Context(), provider.ID, "google-subject-mfa"); err != nil {
		t.Fatalf("oauth user link was not created: %v", err)
	}
}

func TestOAuthLoginProviderScopesAreFixedForLogin(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create", "integrations.update"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(`{"provider_type":"google","name":"Google Login","enabled":true,"client_id":"client-id","client_secret":"client-secret","scopes":["openid","email","https://www.googleapis.com/auth/drive.file","https://www.googleapis.com/auth/youtube"],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create oauth provider status=%d body=%s", res.Code, res.Body.String())
	}
	var provider store.OAuthProvider
	if err := json.NewDecoder(res.Body).Decode(&provider); err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(provider.Scopes, []string{"openid", "email", "profile"}) {
		t.Fatalf("login provider scopes were not fixed: %#v", provider.Scopes)
	}
	authURL, err := oauthAuthorizationURL(store.OAuthProvider{
		ProviderType: "google",
		ClientID:     "client-id",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"},
	}, store.OAuthLoginState{StateToken: "state-token", Nonce: "nonce-value"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("scope"); got != "openid email profile" {
		t.Fatalf("login authorization URL used non-login scopes: %q", got)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/integrations/oauth-providers/"+provider.ID, bytes.NewBufferString(`{"provider_type":"google","name":"Google Login Updated","enabled":true,"client_id":"client-id","client_secret":"","scopes":["https://www.googleapis.com/auth/drive.file","https://www.googleapis.com/auth/youtube.force-ssl"],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update oauth provider status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	var updated store.OAuthProvider
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(updated.Scopes, []string{"openid", "email", "profile"}) {
		t.Fatalf("updated login provider scopes were not fixed: %#v", updated.Scopes)
	}
	if strings.Contains(updateRes.Body.String(), "drive.file") || strings.Contains(updateRes.Body.String(), "youtube.force-ssl") {
		t.Fatalf("provider update response exposed non-login scopes: %s", updateRes.Body.String())
	}
}

func TestOAuthProviderAPIRejectsUnsafeProviderConfig(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"integrations.create", "integrations.update"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	existing, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Existing Google Login",
		Enabled:      true,
		ClientID:     "existing-client-id",
		ClientSecret: "existing-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "invalid provider type",
			body: `{"provider_type":"microsoft","name":"Microsoft Login","enabled":true,"client_id":"client-id","client_secret":"client-secret","redirect_uri":"https://control.example.com/auth/oauth/callback"}`,
			code: "invalid_oauth_provider_type",
		},
		{
			name: "github cannot use connected account callback",
			body: `{"provider_type":"github","name":"GitHub Connected","enabled":true,"client_id":"client-id","client_secret":"client-secret","scopes":["read:user"],"redirect_uri":"https://control.example.com/integrations/oauth-accounts/callback"}`,
			code: "oauth_redirect_uri_invalid",
		},
		{
			name: "google cannot use connected account callback",
			body: `{"provider_type":"google","name":"Google Connected","enabled":true,"client_id":"client-id","client_secret":"client-secret","scopes":["openid","email"],"redirect_uri":"https://control.example.com/integrations/oauth-accounts/callback"}`,
			code: "oauth_redirect_uri_invalid",
		},
		{
			name: "connected account callback cannot auto provision users",
			body: `{"provider_type":"google","name":"Google Connected Auto","enabled":true,"client_id":"client-id","client_secret":"client-secret","scopes":["openid","email","https://www.googleapis.com/auth/drive.file"],"auto_provision":true,"redirect_uri":"https://control.example.com/integrations/oauth-accounts/callback"}`,
			code: "oauth_redirect_uri_invalid",
		},
	}

	for _, tc := range cases {
		t.Run("create "+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(tc.body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
				t.Fatalf("expected %s rejection, status=%d body=%s", tc.code, res.Code, res.Body.String())
			}
		})
		t.Run("update "+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/integrations/oauth-providers/"+existing.ID, bytes.NewBufferString(tc.body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
				t.Fatalf("expected %s rejection, status=%d body=%s", tc.code, res.Code, res.Body.String())
			}
		})
	}
}

func TestOAuthAccountConnectionStartReturnsOfflineConsentURLWithoutClientSecret(t *testing.T) {
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
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"account_label":"Archive Account","redirect_after":"/"}`, provider.ID)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, raw := range []string{"raw-google-client-secret", "client_secret"} {
		if strings.Contains(body, raw) {
			t.Fatalf("oauth account start leaked secret-like value %q: %s", raw, body)
		}
	}
	if !strings.Contains(body, "access_type=offline") || !strings.Contains(body, "prompt=consent") || !strings.Contains(body, "drive.file") {
		t.Fatalf("oauth account start response missing offline consent details: %s", body)
	}
}

func TestManualOAuthLinkCreationDisabledEvenWithManageMFAPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target", Roles: []string{"super_admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "mfa-admin", Roles: []string{"admin"}}, "correct horse battery", []string{"users.manage_mfa"}); err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithOAuthLoginStore(oauthStore), WithIntegrationStore(store.NewMemoryIntegrationStore()))
	cookie, csrf := loginForTest(t, handler, "mfa-admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users/target-id/oauth-links", bytes.NewBufferString(`{"provider_id":"provider-01","provider_type":"google","subject":"attacker-subject","email":"attacker@example.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "manual_oauth_link_disabled") {
		t.Fatalf("expected manual_oauth_link_disabled, body = %s", res.Body.String())
	}
	links, err := oauthStore.ListOAuthUserLinks(t.Context(), "target-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("manual oauth link was created: %#v", links)
	}
}

func TestCurrentUserOAuthLinksDoNotRequireUserReadPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "self-id", Username: "self-user"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "other-id", Username: "other-user"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	selfLink, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "self-id", ProviderID: "provider-google", ProviderType: "google", Subject: "self-subject", Email: "self@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	otherLink, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "other-id", ProviderID: "provider-google", ProviderType: "google", Subject: "other-subject", Email: "other@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithOAuthLoginStore(oauthStore))
	cookie, csrf := loginForTest(t, handler, "self-user", "correct horse battery")

	listReq := httptest.NewRequest(http.MethodGet, "/auth/oauth-links", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), "self@example.com") || strings.Contains(listRes.Body.String(), "other@example.com") {
		t.Fatalf("self oauth links status=%d body=%s", listRes.Code, listRes.Body.String())
	}

	crossDeleteReq := httptest.NewRequest(http.MethodDelete, "/auth/oauth-links/"+otherLink.ID, nil)
	crossDeleteReq.AddCookie(cookie)
	crossDeleteReq.Header.Set("X-CSRF-Token", csrf)
	crossDeleteRes := httptest.NewRecorder()
	handler.ServeHTTP(crossDeleteRes, crossDeleteReq)
	if crossDeleteRes.Code != http.StatusNotFound {
		t.Fatalf("cross-user oauth delete status=%d body=%s", crossDeleteRes.Code, crossDeleteRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/auth/oauth-links/"+selfLink.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("self oauth delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	remaining, err := oauthStore.ListOAuthUserLinks(t.Context(), "self-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("self oauth link was not deleted: %#v", remaining)
	}
	otherRemaining, err := oauthStore.ListOAuthUserLinks(t.Context(), "other-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherRemaining) != 1 {
		t.Fatalf("cross-user oauth link was modified: %#v", otherRemaining)
	}
}
