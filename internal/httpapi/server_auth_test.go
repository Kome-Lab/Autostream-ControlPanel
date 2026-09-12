package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestOAuthLoginStartReturnsAuthorizationURLWithoutClientSecret(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
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
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()))

	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+provider.ID+"/start", bytes.NewBufferString(`{"redirect_after":"/streams"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start oauth status = %d body = %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, raw := range []string{"raw-google-client-secret", "client_secret"} {
		if strings.Contains(body, raw) {
			t.Fatalf("oauth start response leaked secret-like value %q: %s", raw, body)
		}
	}
	if !strings.Contains(body, "accounts.google.com") || !strings.Contains(body, "state") || !strings.Contains(body, "nonce") {
		t.Fatalf("oauth start response missing authorization details: %s", body)
	}
}

func TestSafeRedirectAfterRejectsExternalAndUnsafeTargets(t *testing.T) {
	if got := safeRedirectAfter("/admin/streams/?status=waiting#create-stream"); got != "/admin/streams/?status=waiting#create-stream" {
		t.Fatalf("valid local redirect was rejected: %q", got)
	}
	for _, raw := range []string{
		"https://evil.example/admin/",
		"//evil.example/admin/",
		"/admin\\@evil.example/",
		"/admin/%5cevil.example/",
		"/admin/%0d%0aLocation:%20https://evil.example/",
		"/admin/%c2%85Location:%20https://evil.example/",
		"/admin/%",
	} {
		if got := safeRedirectAfter(raw); got != "" {
			t.Fatalf("unsafe redirect %q was accepted as %q", raw, got)
		}
	}
}

func TestNormalizeOverlayProfileConfigUsesFixedWatermarkCanvas(t *testing.T) {
	config := normalizeProfileConfig(store.ProfileOverlay, map[string]any{
		"watermark_enabled":       true,
		"watermark_position":      "bottom_right",
		"watermark_opacity":       0.7,
		"watermark_width_percent": 14,
		"watermark_file_name":     "logo.png",
	})
	if _, ok := config["watermark_position"]; ok {
		t.Fatalf("legacy position key was not removed: %#v", config)
	}
	if _, ok := config["watermark_opacity"]; ok {
		t.Fatalf("legacy opacity key was not removed: %#v", config)
	}
	if _, ok := config["watermark_width_percent"]; ok {
		t.Fatalf("legacy width key was not removed: %#v", config)
	}
	if config["watermark_canvas_width"] != 1920 || config["watermark_canvas_height"] != 1080 || config["watermark_fit_mode"] != "scale_to_output" {
		t.Fatalf("fixed watermark canvas was not applied: %#v", config)
	}
	if config["watermark_file_name"] != "logo.png" {
		t.Fatalf("unrelated config was not preserved: %#v", config)
	}
}

func TestOAuthLoginCallbackCreatesSessionForLinkedUser(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-oauth-01", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "discord",
		Name:         "Discord Login",
		Enabled:      true,
		ClientID:     "discord-client-id",
		ClientSecret: "raw-discord-client-secret",
		Scopes:       []string{"identify", "email"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	if _, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "user-oauth-01", ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "discord-subject-01", Email: "operator@example.com"}); err != nil {
		t.Fatal(err)
	}
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "discord-subject-01", Email: "operator@example.com"}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier))

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth callback status = %d body = %s", res.Code, res.Body.String())
	}
	if len(res.Result().Cookies()) == 0 || !strings.Contains(res.Body.String(), "csrf_token") {
		t.Fatalf("oauth callback did not create a session: headers=%v body=%s", res.Result().Cookies(), res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-discord-client-secret") {
		t.Fatalf("oauth callback leaked client secret: %s", res.Body.String())
	}
}

func TestOAuthLoginCallbackRequiresStateCookie(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-oauth-cookie", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "discord",
		Name:         "Discord Login",
		Enabled:      true,
		ClientID:     "discord-client-id",
		ClientSecret: "raw-discord-client-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	if _, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "user-oauth-cookie", ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "discord-subject-01", Email: "operator@example.com"}); err != nil {
		t.Fatal(err)
	}
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "discord-subject-01", Email: "operator@example.com"}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier))

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "invalid_oauth_state") {
		t.Fatalf("expected state cookie rejection, status=%d body=%s", res.Code, res.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	retryReq.AddCookie(oauthCookie)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("state was consumed before cookie verification, retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}
}

func TestOAuthLoginStartRejectsUnexpectedRedirectURIPath(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Login",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/unexpected/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()))

	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+provider.ID+"/start", bytes.NewBufferString(`{}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "oauth_redirect_uri_invalid") {
		t.Fatalf("expected redirect uri rejection, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestOAuthRedirectURIRequiresPublicURLHostOrLocalhost(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "")

	if validOAuthRedirectURI("https://control.example.com/auth/oauth/callback", "/auth/oauth/callback") {
		t.Fatalf("external OAuth redirect must be rejected when AUTOSTREAM_PUBLIC_URL is not configured")
	}
	for _, raw := range []string{
		"http://localhost:8080/auth/oauth/callback",
		"http://127.0.0.1:8080/auth/oauth/callback",
		"http://[::1]:8080/auth/oauth/callback",
	} {
		if !validOAuthRedirectURI(raw, "/auth/oauth/callback") {
			t.Fatalf("local development redirect should be accepted: %s", raw)
		}
	}
}

func TestOAuthRedirectURIRequiresConfiguredPublicURLMatch(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")

	if !validOAuthRedirectURI("https://control.example.com/auth/oauth/callback", "/auth/oauth/callback") {
		t.Fatalf("configured public URL redirect should be accepted")
	}
	for _, raw := range []string{
		"http://control.example.com/auth/oauth/callback",
		"https://evil.example.com/auth/oauth/callback",
		"https://control.example.com.evil.test/auth/oauth/callback",
		"https://control.example.com/auth/oauth/callback?next=/",
	} {
		if validOAuthRedirectURI(raw, "/auth/oauth/callback") {
			t.Fatalf("unexpected OAuth redirect accepted: %s", raw)
		}
	}
}

func TestOAuthLoginRedirectCallbackCreatesSessionAndRedirects(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-oauth-redirect", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
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
	if _, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "user-oauth-redirect", ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "operator@example.com"}); err != nil {
		t.Fatal(err)
	}
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "operator@example.com", EmailVerified: true}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier))

	startReq := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+provider.ID+"/start", bytes.NewBufferString(`{"redirect_after":"/streams"}`))
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	oauthCookie := findCookieForTest(t, startRes.Result().Cookies(), oauthStateCookieName)
	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(startBody.State)+"&code=callback-code", nil)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/streams" {
		t.Fatalf("oauth redirect status=%d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("oauth redirect callback must suppress code caching/referrers, headers=%#v", res.Header())
	}
	if len(res.Result().Cookies()) == 0 {
		t.Fatalf("oauth redirect did not create session cookie")
	}
}

func TestOAuthLoginRedirectCallbackRedirectsMFAChallengeToLoginPage(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-oauth-mfa", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	if err := auth.StartTOTPEnrollment(t.Context(), "user-oauth-mfa", secret, []string{security.HashRecoveryCode("ABCD-EFGH-IJKL")}); err != nil {
		t.Fatal(err)
	}
	if err := auth.ConfirmTOTPEnrollment(t.Context(), "user-oauth-mfa"); err != nil {
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
	if _, err := oauthStore.LinkOAuthUser(t.Context(), store.OAuthUserLink{UserID: "user-oauth-mfa", ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-mfa-login", Email: "operator@example.com"}); err != nil {
		t.Fatal(err)
	}
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-mfa-login", Email: "operator@example.com", EmailVerified: true}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier), WithSecuritySettingsStore(settings), WithMFAStore(auth))

	redirectAfter := "/admin/streams/?status=waiting#create-stream"
	state, oauthCookie := startOAuthForTestWithRedirect(t, handler, provider.ID, redirectAfter)
	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(state)+"&code=callback-code", nil)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	location := res.Header().Get("Location")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("oauth MFA redirect status=%d location=%q body=%s", res.Code, location, res.Body.String())
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/login" || parsed.Query().Get("redirect_after") != redirectAfter {
		t.Fatalf("oauth MFA redirect lost post-login target: %q", location)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Get("oauth_mfa_challenge") == "" || fragment.Get("expires_at") == "" {
		t.Fatalf("oauth MFA redirect missing challenge fragment: %q", location)
	}
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			t.Fatalf("OAuth MFA redirect must not issue a session cookie before verification: %#v", cookie)
		}
	}
	if strings.Contains(res.Body.String(), "mfa_required") || strings.Contains(res.Body.String(), "challenge_token") {
		t.Fatalf("OAuth GET callback should not render JSON challenge body: %s", res.Body.String())
	}
}

func TestOAuthLoginCallbackRejectsUnlinkedIdentity(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "github",
		Name:         "GitHub Login",
		Enabled:      true,
		ClientID:     "github-client-id",
		ClientSecret: "raw-github-client-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "github-subject-01", Email: "operator@example.com"}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()), WithOAuthVerifier(verifier))

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"state":%q,"code":"callback-code"}`, state)))
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "oauth_account_not_linked") {
		t.Fatalf("expected unlinked identity rejection, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestOAuthLoginCallbackAutoProvisionsAllowedIdentity(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
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
		DefaultRoleIDs: []string{viewerRole.ID},
		RedirectURI:    "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	verifier := fakeOAuthVerifier{identity: oauthlogin.Identity{ProviderID: provider.ID, ProviderType: provider.ProviderType, Subject: "google-subject-01", Email: "operator@example.com", EmailVerified: true}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithOAuthLoginStore(oauthStore), WithOAuthVerifier(verifier))

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/callback", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q,"state":%q,"code":"callback-code"}`, provider.ID, state)))
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth callback status = %d body = %s", res.Code, res.Body.String())
	}
	user, err := auth.FindUserByUsername(t.Context(), "operator@example.com")
	if err != nil {
		t.Fatalf("auto-provisioned user not found: %v", err)
	}
	if user.Status != "active" || !hasString(user.Roles, "viewer") {
		t.Fatalf("unexpected auto-provisioned user: %#v", user)
	}
	if _, err := oauthStore.FindOAuthUserLink(t.Context(), provider.ID, "google-subject-01"); err != nil {
		t.Fatalf("oauth user link was not created: %v", err)
	}
	if strings.Contains(res.Body.String(), "raw-google-client-secret") {
		t.Fatalf("oauth auto-provision leaked client secret: %s", res.Body.String())
	}
}

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

func TestConcurrentForceStopsClaimOnceBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	baseStreams := store.NewMemoryStreamStore()
	transitionGate := &gatedLifecycleTransitionStore{
		StreamStore: baseStreams,
		expected:    "live",
		status:      "failed",
		entered:     make(chan struct{}, 2),
		release:     make(chan struct{}),
	}
	dispatcher := &synchronizedServiceDispatcher{}
	first := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	second := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, first, "operator", "correct horse battery")

	stream, err := baseStreams.CreateStream(t.Context(), "concurrent force stop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStreams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)

	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, handler := range []http.Handler{first, second} {
		go func(handler http.Handler) {
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/force-stop", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			responses <- res
		}(handler)
	}
	waitForLifecycleTransitionAttempts(t, transitionGate.entered, 2)
	close(transitionGate.release)

	codes := receiveLifecycleResponseCodes(t, responses, 2)
	if codes[http.StatusOK] != 1 || codes[http.StatusAccepted] != 1 {
		t.Fatalf("concurrent force-stop response codes = %#v, want one 200 and one 202", codes)
	}
	if got := dispatcher.StopCalls(); got != 1 {
		t.Fatalf("concurrent force stops dispatched %d times, want 1", got)
	}
	final, err := baseStreams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" {
		t.Fatalf("concurrent force stop final status = %q, want completed after confirmed downstream stop", final.Status)
	}
}

func TestSendWorkerTestEventRequiresAssignedWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/worker-events/test", bytes.NewBufferString(`{"event_type":"current_time"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamsRequireAuthentication(t *testing.T) {
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	req := httptest.NewRequest(http.MethodGet, "/streams", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestPermissionDenied(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCSRFFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked"}`))
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSessionCookieSecureFollowsPublicHTTPS(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	if !cookie.Secure {
		t.Fatalf("session cookie should be Secure when AUTOSTREAM_PUBLIC_URL is https")
	}
}

func TestSessionCookieSecureCannotBeDisabledForHTTPSPublicURL(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	t.Setenv("AUTOSTREAM_COOKIE_SECURE", "false")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	if !cookie.Secure {
		t.Fatalf("session cookie should remain Secure when AUTOSTREAM_PUBLIC_URL is https, even with Secure=false override")
	}
}

func TestSessionCookieSecureCanBeDisabledForLocalHTTP(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "http://127.0.0.1:8080")
	t.Setenv("AUTOSTREAM_COOKIE_SECURE", "false")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	if cookie.Secure {
		t.Fatalf("session cookie should allow explicit Secure=false override for local HTTP")
	}
}

func TestMFAEnrollmentCanStartWhenPolicyDisabled(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"method":"totp"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestMFAEnrollmentReportsTOTPUnavailableInPasskeyMode(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "passkey",
		MFARequiredRoles:         []string{"admin"},
		RememberMeEnabled:        false,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "totp_mfa_unavailable") {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestMFAStatusReturnsCurrentUserStateWithoutSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "mfa-user-id", Username: "mfa-user", Roles: []string{"admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.StartTOTPEnrollment(t.Context(), "mfa-user-id", "SECRET-TOTP-VALUE", []string{"hash-one", "hash-two"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.ConfirmTOTPEnrollment(t.Context(), "mfa-user-id"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	session, err := auth.CreateSession(t.Context(), "mfa-user-id", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: session.Token}

	req := httptest.NewRequest(http.MethodGet, "/auth/mfa/status", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mfa status = %d body = %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"available":true`, `"enabled":true`, `"method":"totp"`, `"recovery_code_count":2`} {
		if !strings.Contains(body, want) {
			t.Fatalf("mfa status missing %s: %s", want, body)
		}
	}
	for _, secret := range []string{"SECRET-TOTP-VALUE", "hash-one", "hash-two", "recovery_code_hash"} {
		if strings.Contains(body, secret) {
			t.Fatalf("mfa status leaked secret-like value %q: %s", secret, body)
		}
	}
}

func TestSecretUpdateDoesNotEchoSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"secrets.update", "secrets.read_status"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/secrets/youtube_stream_key", bytes.NewBufferString(`{"value":"super-secret-stream-key"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "super-secret-stream-key") {
		t.Fatalf("secret leaked in response: %s", res.Body.String())
	}
	statusReq := httptest.NewRequest(http.MethodGet, "/secrets/status", nil)
	statusReq.AddCookie(cookie)
	statusRes := httptest.NewRecorder()
	handler.ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK || !strings.Contains(statusRes.Body.String(), `"configured":true`) {
		t.Fatalf("secret status = %d body = %s", statusRes.Code, statusRes.Body.String())
	}
	if strings.Contains(statusRes.Body.String(), "super-secret-stream-key") {
		t.Fatalf("secret leaked in status response: %s", statusRes.Body.String())
	}
	events := auth.AuditEvents()
	if strings.Contains(toJSONForTest(t, events), "super-secret-stream-key") {
		t.Fatalf("secret leaked in audit events: %#v", events)
	}
}

func TestPendingPasswordChangeCanOnlyChangePassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Status: "pending_password_change"}, "temporary correct battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "temporary correct battery")

	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.AddCookie(cookie)
	meRes := httptest.NewRecorder()
	handler.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK || !strings.Contains(meRes.Body.String(), "pending_password_change") {
		t.Fatalf("me status = %d body = %s", meRes.Code, meRes.Body.String())
	}

	streamsReq := httptest.NewRequest(http.MethodGet, "/streams", nil)
	streamsReq.AddCookie(cookie)
	streamsRes := httptest.NewRecorder()
	handler.ServeHTTP(streamsRes, streamsReq)
	if streamsRes.Code != http.StatusForbidden || !strings.Contains(streamsRes.Body.String(), "password_change_required") {
		t.Fatalf("pending user streams status = %d body = %s", streamsRes.Code, streamsRes.Body.String())
	}

	changeReq := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewBufferString(`{"current_password":"temporary correct battery","new_password":"new correct battery"}`))
	changeReq.AddCookie(cookie)
	changeReq.Header.Set("X-CSRF-Token", csrf)
	changeRes := httptest.NewRecorder()
	handler.ServeHTTP(changeRes, changeReq)
	if changeRes.Code != http.StatusOK {
		t.Fatalf("change password status = %d body = %s", changeRes.Code, changeRes.Body.String())
	}

	newCookie, _ := loginForTest(t, handler, "operator", "new correct battery")
	streamsReq = httptest.NewRequest(http.MethodGet, "/streams", nil)
	streamsReq.AddCookie(newCookie)
	streamsRes = httptest.NewRecorder()
	handler.ServeHTTP(streamsRes, streamsReq)
	if streamsRes.Code != http.StatusOK {
		t.Fatalf("active user streams status = %d body = %s", streamsRes.Code, streamsRes.Body.String())
	}
}

func TestPasskeyModeRequiresPasskeyLoginForTargetedPasswordLogin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-01", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "viewer-01", Username: "viewer", Roles: []string{"viewer"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "admin-01",
		Name:          "Windows Hello",
		CredentialID:  []byte("admin-credential-id"),
		PublicKeyCBOR: []byte("admin-public-key"),
	}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "passkey",
		MFARequiredRoles:         []string{"super_admin", "admin"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithPasskeyStore(auth))

	viewerReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"viewer","password":"correct horse battery"}`))
	viewerRes := httptest.NewRecorder()
	handler.ServeHTTP(viewerRes, viewerReq)
	if viewerRes.Code != http.StatusOK || strings.Contains(viewerRes.Body.String(), "passkey_required") {
		t.Fatalf("viewer should not require passkey under admin-only policy: %d %s", viewerRes.Code, viewerRes.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	adminRes := httptest.NewRecorder()
	handler.ServeHTTP(adminRes, adminReq)
	if adminRes.Code != http.StatusForbidden || !strings.Contains(adminRes.Body.String(), "passkey_required") {
		t.Fatalf("admin password login must require passkey under passkey policy: %d %s", adminRes.Code, adminRes.Body.String())
	}
	if len(adminRes.Result().Cookies()) != 0 {
		t.Fatal("passkey-required password login must not issue a session cookie")
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "auth.login" || events[len(events)-1].Result != "passkey_required" {
		t.Fatalf("expected passkey-required audit event, got %#v", events)
	}
}

func TestPasskeyModeRejectsUnenrolledTargetedUser(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-01", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "passkey",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithPasskeyStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "passkey_enrollment_required") {
		t.Fatalf("unenrolled passkey policy login status = %d body = %s", res.Code, res.Body.String())
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("unenrolled passkey policy login must not issue a session cookie")
	}
}

func TestPasskeyCredentialManagementDoesNotExposeVerifierMaterial(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-01", Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	created, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "user-01",
		Name:          "Windows Hello",
		CredentialID:  []byte("raw-credential-id"),
		PublicKeyCBOR: []byte("raw-public-key-cbor"),
		SignCount:     7,
		Transports:    []string{"internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithPasskeyStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	listReq := httptest.NewRequest(http.MethodGet, "/auth/passkeys", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list passkeys status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	body := listRes.Body.String()
	if !strings.Contains(body, "Windows Hello") || !strings.Contains(body, "credential_id_hash") {
		t.Fatalf("passkey public fields missing: %s", body)
	}
	if strings.Contains(body, "raw-credential-id") || strings.Contains(body, "raw-public-key-cbor") {
		t.Fatalf("passkey list leaked verifier material: %s", body)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/auth/passkeys/"+created.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("delete passkey status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := auth.FindPasskeyCredentialByCredentialID(t.Context(), []byte("raw-credential-id")); err != store.ErrNotFound {
		t.Fatalf("expected passkey to be deleted, got %v", err)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Action == "passkeys.delete" && event.Result == "success" && event.ResourceID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing passkey delete audit event: %#v", events)
	}
}

func TestPasskeyRegistrationStartCreatesNoStoreChallenge(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	t.Setenv("AUTOSTREAM_WEBAUTHN_RP_NAME", "AutoStream Test")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-01", Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "user-01",
		Name:          "Existing Passkey",
		CredentialID:  []byte("raw-existing-credential-id"),
		PublicKeyCBOR: []byte("raw-existing-public-key"),
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithPasskeyStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingCSRFReq := httptest.NewRequest(http.MethodPost, "/auth/passkeys/register/start", bytes.NewBufferString(`{"display_name":"Admin User"}`))
	missingCSRFReq.AddCookie(cookie)
	missingCSRFRes := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRFRes, missingCSRFReq)
	if missingCSRFRes.Code != http.StatusForbidden || !strings.Contains(missingCSRFRes.Body.String(), "csrf_failed") {
		t.Fatalf("missing csrf start status = %d body = %s", missingCSRFRes.Code, missingCSRFRes.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/passkeys/register/start", bytes.NewBufferString(`{"display_name":"Admin User"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("registration start status = %d body = %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("registration start must be no-store, headers = %#v", res.Header())
	}
	body := res.Body.String()
	if strings.Contains(body, "raw-existing-credential-id") || strings.Contains(body, "raw-existing-public-key") {
		t.Fatalf("registration start leaked existing credential material: %s", body)
	}
	var payload struct {
		RegistrationToken string         `json:"registration_token"`
		ExpiresAt         time.Time      `json:"expires_at"`
		PublicKey         map[string]any `json:"public_key"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	rp, _ := payload.PublicKey["rp"].(map[string]any)
	user, _ := payload.PublicKey["user"].(map[string]any)
	challenge, _ := payload.PublicKey["challenge"].(string)
	if payload.RegistrationToken == "" || challenge == "" || rp["id"] != "control.example.com" || rp["name"] != "AutoStream Test" || user["displayName"] != "Admin User" {
		t.Fatalf("unexpected registration payload: %#v", payload)
	}
	stored, err := auth.GetPasskeyCeremonySession(t.Context(), payload.RegistrationToken, "registration")
	if err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == payload.RegistrationToken || stored.Ceremony != "registration" || !strings.Contains(string(stored.SessionJSON), challenge) {
		t.Fatalf("ceremony persistence must use token hash and same challenge: %#v", stored)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"passkeys.registration.start"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("missing passkey registration audit event")
	}
	auditBody, _ := json.Marshal(events)
	if strings.Contains(string(auditBody), payload.RegistrationToken) || strings.Contains(string(auditBody), challenge) {
		t.Fatalf("audit leaked registration token or challenge: %s", string(auditBody))
	}
}

func TestPasskeyLoginStartCreatesNoStoreChallengeAndInvalidFinishConsumesIt(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-01", Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "user-01",
		Name:          "Windows Hello",
		CredentialID:  []byte("raw-login-credential-id"),
		PublicKeyCBOR: []byte("raw-login-public-key"),
		Transports:    []string{"internal"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithPasskeyStore(auth))

	anonymousReq := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/start", bytes.NewBufferString(`{}`))
	anonymousRes := httptest.NewRecorder()
	handler.ServeHTTP(anonymousRes, anonymousReq)
	if anonymousRes.Code != http.StatusOK || strings.Contains(anonymousRes.Body.String(), "admin") {
		t.Fatalf("discoverable login start must not enumerate users: %d %s", anonymousRes.Code, anonymousRes.Body.String())
	}

	startReq := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/start", bytes.NewBufferString(`{"username":"admin"}`))
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("login start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	if startRes.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login start must be no-store, headers = %#v", startRes.Header())
	}
	if strings.Contains(startRes.Body.String(), "raw-login-public-key") || strings.Contains(startRes.Body.String(), "raw-login-credential-id") {
		t.Fatalf("login start leaked raw passkey material: %s", startRes.Body.String())
	}
	var payload struct {
		ChallengeToken string         `json:"challenge_token"`
		PublicKey      map[string]any `json:"public_key"`
	}
	if err := json.Unmarshal(startRes.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	challenge, _ := payload.PublicKey["challenge"].(string)
	if payload.ChallengeToken == "" || challenge == "" {
		t.Fatalf("unexpected login payload: %#v", payload)
	}
	stored, err := auth.GetPasskeyCeremonySession(t.Context(), payload.ChallengeToken, "login")
	if err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == payload.ChallengeToken || stored.UserID != "" || !strings.Contains(string(stored.SessionJSON), challenge) {
		t.Fatalf("login ceremony persistence must hash token and retain challenge: %#v", stored)
	}

	finishReq := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/finish", bytes.NewBufferString(`{"challenge_token":"`+payload.ChallengeToken+`","credential":{}}`))
	finishRes := httptest.NewRecorder()
	handler.ServeHTTP(finishRes, finishReq)
	if finishRes.Code != http.StatusUnauthorized {
		t.Fatalf("invalid finish status = %d body = %s", finishRes.Code, finishRes.Body.String())
	}
	if _, err := auth.GetPasskeyCeremonySession(t.Context(), payload.ChallengeToken, "login"); err != store.ErrNotFound {
		t.Fatalf("invalid finish must consume one-time passkey session, got %v", err)
	}
}

func TestPasskeyOriginFallbackIgnoresUntrustedOriginHeader(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "")
	t.Setenv("AUTOSTREAM_WEBAUTHN_RP_ORIGINS", "")
	req := httptest.NewRequest(http.MethodPost, "http://control.localhost/auth/passkeys/login/start", nil)
	req.Host = "control.localhost"
	req.Header.Set("Origin", "https://evil.example.com")

	origins, err := passkeyOrigins(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 1 || origins[0] != "http://control.localhost" {
		t.Fatalf("origin fallback must use request host, got %#v", origins)
	}
}

func TestPasskeyProductionRequiresConfiguredRelyingParty(t *testing.T) {
	t.Setenv("AUTOSTREAM_ENV", "production")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "")
	t.Setenv("AUTOSTREAM_WEBAUTHN_RP_ID", "")
	t.Setenv("AUTOSTREAM_WEBAUTHN_RP_ORIGINS", "")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-01", Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithPasskeyStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/start", bytes.NewBufferString(`{"username":"admin"}`))
	req.Host = "attacker-controlled.example.com"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "passkey_runtime_unavailable") {
		t.Fatalf("production passkey start must fail closed without configured RP, status = %d body = %s", res.Code, res.Body.String())
	}

	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	okReq := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/start", bytes.NewBufferString(`{"username":"admin"}`))
	okReq.Host = "attacker-controlled.example.com"
	okRes := httptest.NewRecorder()
	handler.ServeHTTP(okRes, okReq)
	if okRes.Code != http.StatusOK {
		t.Fatalf("production passkey start with configured public URL status = %d body = %s", okRes.Code, okRes.Body.String())
	}
	if strings.Contains(okRes.Body.String(), "attacker-controlled.example.com") {
		t.Fatalf("production passkey response must not use request host fallback: %s", okRes.Body.String())
	}
}

func TestPasskeyDeleteRequiresCSRFAndCurrentUserOwnership(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-01", Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "user-02", Username: "other"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	own, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "user-01",
		Name:          "Own Passkey",
		CredentialID:  []byte("own-credential-id"),
		PublicKeyCBOR: []byte("own-public-key-cbor"),
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.CreatePasskeyCredential(t.Context(), store.PasskeyCredential{
		UserID:        "user-02",
		Name:          "Other Passkey",
		CredentialID:  []byte("other-credential-id"),
		PublicKeyCBOR: []byte("other-public-key-cbor"),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithPasskeyStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingCSRFReq := httptest.NewRequest(http.MethodDelete, "/auth/passkeys/"+own.ID, nil)
	missingCSRFReq.AddCookie(cookie)
	missingCSRFRes := httptest.NewRecorder()
	handler.ServeHTTP(missingCSRFRes, missingCSRFReq)
	if missingCSRFRes.Code != http.StatusForbidden || !strings.Contains(missingCSRFRes.Body.String(), "csrf_failed") {
		t.Fatalf("missing csrf delete status = %d body = %s", missingCSRFRes.Code, missingCSRFRes.Body.String())
	}
	if _, err := auth.FindPasskeyCredentialByCredentialID(t.Context(), []byte("own-credential-id")); err != nil {
		t.Fatalf("missing csrf must not delete credential: %v", err)
	}

	crossUserReq := httptest.NewRequest(http.MethodDelete, "/auth/passkeys/"+other.ID, nil)
	crossUserReq.AddCookie(cookie)
	crossUserReq.Header.Set("X-CSRF-Token", csrf)
	crossUserRes := httptest.NewRecorder()
	handler.ServeHTTP(crossUserRes, crossUserReq)
	if crossUserRes.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete status = %d body = %s", crossUserRes.Code, crossUserRes.Body.String())
	}
	if _, err := auth.FindPasskeyCredentialByCredentialID(t.Context(), []byte("other-credential-id")); err != nil {
		t.Fatalf("cross-user delete must not remove credential: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/auth/passkeys", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || strings.Contains(listRes.Body.String(), "Other Passkey") {
		t.Fatalf("list must stay scoped to current user, status = %d body = %s", listRes.Code, listRes.Body.String())
	}
}

func TestTOTPEnrollmentAndLoginChallenge(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "totp",
	}); err != nil {
		t.Fatal(err)
	}

	enrollReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", nil)
	enrollReq.AddCookie(cookie)
	enrollReq.Header.Set("X-CSRF-Token", csrf)
	enrollRes := httptest.NewRecorder()
	handler.ServeHTTP(enrollRes, enrollReq)
	if enrollRes.Code != http.StatusOK {
		t.Fatalf("enroll status = %d body = %s", enrollRes.Code, enrollRes.Body.String())
	}
	var enrollBody struct {
		Secret        string   `json:"secret"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(enrollRes.Body).Decode(&enrollBody); err != nil {
		t.Fatal(err)
	}
	if enrollBody.Secret == "" || len(enrollBody.RecoveryCodes) != 10 {
		t.Fatalf("unexpected enroll body: %#v", enrollBody)
	}
	code, err := security.TOTPCode(enrollBody.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	verifyReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"code":"`+code+`"}`))
	verifyReq.AddCookie(cookie)
	verifyReq.Header.Set("X-CSRF-Token", csrf)
	verifyRes := httptest.NewRecorder()
	handler.ServeHTTP(verifyRes, verifyReq)
	if verifyRes.Code != http.StatusOK || !strings.Contains(verifyRes.Body.String(), "mfa_enabled") {
		t.Fatalf("verify status = %d body = %s", verifyRes.Code, verifyRes.Body.String())
	}

	reenrollWithoutCodeReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", bytes.NewBufferString(`{}`))
	reenrollWithoutCodeReq.AddCookie(cookie)
	reenrollWithoutCodeReq.Header.Set("X-CSRF-Token", csrf)
	reenrollWithoutCodeRes := httptest.NewRecorder()
	handler.ServeHTTP(reenrollWithoutCodeRes, reenrollWithoutCodeReq)
	if reenrollWithoutCodeRes.Code != http.StatusUnauthorized {
		t.Fatalf("reenroll without current code status = %d body = %s", reenrollWithoutCodeRes.Code, reenrollWithoutCodeRes.Body.String())
	}

	reenrollCode, err := security.TOTPCode(enrollBody.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reenrollReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", bytes.NewBufferString(`{"code":"`+reenrollCode+`"}`))
	reenrollReq.AddCookie(cookie)
	reenrollReq.Header.Set("X-CSRF-Token", csrf)
	reenrollRes := httptest.NewRecorder()
	handler.ServeHTTP(reenrollRes, reenrollReq)
	if reenrollRes.Code != http.StatusOK || reenrollRes.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reenroll status = %d cache = %q body = %s", reenrollRes.Code, reenrollRes.Header().Get("Cache-Control"), reenrollRes.Body.String())
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	loginRes := httptest.NewRecorder()
	handler.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusAccepted || !strings.Contains(loginRes.Body.String(), "challenge_token") {
		t.Fatalf("login challenge status = %d body = %s", loginRes.Code, loginRes.Body.String())
	}
	if len(loginRes.Result().Cookies()) != 0 {
		t.Fatal("MFA challenge must not issue a session cookie")
	}
	var loginBody struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(loginRes.Body).Decode(&loginBody); err != nil {
		t.Fatal(err)
	}
	invalidChallengeReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"challenge_token":"`+loginBody.ChallengeToken+`","code":"000000"}`))
	invalidChallengeRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidChallengeRes, invalidChallengeReq)
	if invalidChallengeRes.Code != http.StatusUnauthorized {
		t.Fatalf("invalid challenge status = %d body = %s", invalidChallengeRes.Code, invalidChallengeRes.Body.String())
	}
	challengeCode, err := security.TOTPCode(enrollBody.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	challengeReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"challenge_token":"`+loginBody.ChallengeToken+`","code":"`+challengeCode+`"}`))
	challengeRes := httptest.NewRecorder()
	handler.ServeHTTP(challengeRes, challengeReq)
	if challengeRes.Code != http.StatusUnauthorized {
		t.Fatalf("reused invalidated challenge status = %d body = %s", challengeRes.Code, challengeRes.Body.String())
	}

	loginBody.ChallengeToken = loginMFAChallengeForTest(t, handler)
	challengeReq = httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"challenge_token":"`+loginBody.ChallengeToken+`","code":"`+challengeCode+`"}`))
	// A stale authenticated session must not turn an explicit login challenge
	// into an enrollment request that requires the old session's CSRF token.
	challengeReq.AddCookie(cookie)
	challengeReq.Header.Set("X-CSRF-Token", "stale-session-csrf-token")
	challengeRes = httptest.NewRecorder()
	handler.ServeHTTP(challengeRes, challengeReq)
	if challengeRes.Code != http.StatusOK || !strings.Contains(challengeRes.Body.String(), "csrf_token") {
		t.Fatalf("challenge verify status = %d body = %s", challengeRes.Code, challengeRes.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, item := range challengeRes.Result().Cookies() {
		if item.Name == sessionCookieName {
			sessionCookie = item
		}
	}
	if sessionCookie == nil {
		t.Fatal("MFA verify did not issue session cookie")
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if strings.Contains(auditJSON, enrollBody.Secret) || strings.Contains(auditJSON, enrollBody.RecoveryCodes[0]) {
		t.Fatalf("MFA audit leaked one-time secret material: %s", auditJSON)
	}
}

func TestTOTPModeRejectsUnenrolledUserLogin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
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
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "mfa_enrollment_required") {
		t.Fatalf("unenrolled totp login status = %d body = %s", res.Code, res.Body.String())
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("unenrolled MFA login must not issue a session cookie")
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "auth.login" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected failed login audit, got %#v", events)
	}
}

func TestMFARecoveryCodeIsSingleUse(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "totp",
	}); err != nil {
		t.Fatal(err)
	}

	enrollReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", nil)
	enrollReq.AddCookie(cookie)
	enrollReq.Header.Set("X-CSRF-Token", csrf)
	enrollRes := httptest.NewRecorder()
	handler.ServeHTTP(enrollRes, enrollReq)
	var enrollBody struct {
		Secret        string   `json:"secret"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(enrollRes.Body).Decode(&enrollBody); err != nil {
		t.Fatal(err)
	}
	code, _ := security.TOTPCode(enrollBody.Secret, time.Now())
	verifyReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"code":"`+code+`"}`))
	verifyReq.AddCookie(cookie)
	verifyReq.Header.Set("X-CSRF-Token", csrf)
	verifyRes := httptest.NewRecorder()
	handler.ServeHTTP(verifyRes, verifyReq)
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("enroll verify status = %d body = %s", verifyRes.Code, verifyRes.Body.String())
	}

	challengeToken := loginMFAChallengeForTest(t, handler)
	firstRecoveryReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"challenge_token":"`+challengeToken+`","code":"`+enrollBody.RecoveryCodes[0]+`"}`))
	firstRecoveryRes := httptest.NewRecorder()
	handler.ServeHTTP(firstRecoveryRes, firstRecoveryReq)
	if firstRecoveryRes.Code != http.StatusOK {
		t.Fatalf("first recovery status = %d body = %s", firstRecoveryRes.Code, firstRecoveryRes.Body.String())
	}

	challengeToken = loginMFAChallengeForTest(t, handler)
	secondRecoveryReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"challenge_token":"`+challengeToken+`","code":"`+enrollBody.RecoveryCodes[0]+`"}`))
	secondRecoveryRes := httptest.NewRecorder()
	handler.ServeHTTP(secondRecoveryRes, secondRecoveryReq)
	if secondRecoveryRes.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery status = %d body = %s", secondRecoveryRes.Code, secondRecoveryRes.Body.String())
	}
}

func TestMFARecoveryCodeRegenerationRequiresCurrentCode(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "totp",
	}); err != nil {
		t.Fatal(err)
	}

	enrollReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll", nil)
	enrollReq.AddCookie(cookie)
	enrollReq.Header.Set("X-CSRF-Token", csrf)
	enrollRes := httptest.NewRecorder()
	handler.ServeHTTP(enrollRes, enrollReq)
	var enrollBody struct {
		Secret        string   `json:"secret"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(enrollRes.Body).Decode(&enrollBody); err != nil {
		t.Fatal(err)
	}
	code, _ := security.TOTPCode(enrollBody.Secret, time.Now())
	verifyReq := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewBufferString(`{"code":"`+code+`"}`))
	verifyReq.AddCookie(cookie)
	verifyReq.Header.Set("X-CSRF-Token", csrf)
	verifyRes := httptest.NewRecorder()
	handler.ServeHTTP(verifyRes, verifyReq)
	if verifyRes.Code != http.StatusOK {
		t.Fatalf("enroll verify status = %d body = %s", verifyRes.Code, verifyRes.Body.String())
	}

	withoutCodeReq := httptest.NewRequest(http.MethodPost, "/auth/recovery-codes/regenerate", bytes.NewBufferString(`{}`))
	withoutCodeReq.AddCookie(cookie)
	withoutCodeReq.Header.Set("X-CSRF-Token", csrf)
	withoutCodeRes := httptest.NewRecorder()
	handler.ServeHTTP(withoutCodeRes, withoutCodeReq)
	if withoutCodeRes.Code != http.StatusUnauthorized {
		t.Fatalf("regenerate without code status = %d body = %s", withoutCodeRes.Code, withoutCodeRes.Body.String())
	}

	currentCode, _ := security.TOTPCode(enrollBody.Secret, time.Now())
	regenReq := httptest.NewRequest(http.MethodPost, "/auth/recovery-codes/regenerate", bytes.NewBufferString(`{"code":"`+currentCode+`"}`))
	regenReq.AddCookie(cookie)
	regenReq.Header.Set("X-CSRF-Token", csrf)
	regenRes := httptest.NewRecorder()
	handler.ServeHTTP(regenRes, regenReq)
	if regenRes.Code != http.StatusOK || regenRes.Header().Get("Cache-Control") != "no-store" || !strings.Contains(regenRes.Body.String(), "recovery_codes") || strings.Contains(regenRes.Body.String(), enrollBody.RecoveryCodes[0]) {
		t.Fatalf("regenerate status = %d cache = %q body = %s", regenRes.Code, regenRes.Header().Get("Cache-Control"), regenRes.Body.String())
	}
}

func TestSessionRefreshSlidesIdleExpiryOnlyOnExplicitActivity(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "active-user", Username: "active-user"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(t.Context(), "active-user", time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	meRes := httptest.NewRecorder()
	handler.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK {
		t.Fatalf("auth/me status = %d body = %s", meRes.Code, meRes.Body.String())
	}
	afterRead, err := auth.GetSession(t.Context(), session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRead.IdleExpiresAt.Equal(session.IdleExpiresAt) {
		t.Fatalf("read-only auth check changed idle expiry: before=%s after=%s", session.IdleExpiresAt, afterRead.IdleExpiresAt)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/auth/session/refresh", nil)
	refreshReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	refreshReq.Header.Set("X-CSRF-Token", session.CSRFToken)
	refreshRes := httptest.NewRecorder()
	handler.ServeHTTP(refreshRes, refreshReq)
	if refreshRes.Code != http.StatusOK || !strings.Contains(refreshRes.Body.String(), `"status":"refreshed"`) {
		t.Fatalf("session refresh status = %d body = %s", refreshRes.Code, refreshRes.Body.String())
	}
	afterRefresh, err := auth.GetSession(t.Context(), session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRefresh.IdleExpiresAt.After(session.IdleExpiresAt) {
		t.Fatalf("activity refresh did not advance idle expiry: before=%s after=%s", session.IdleExpiresAt, afterRefresh.IdleExpiresAt)
	}
	if afterRefresh.IdleExpiresAt.After(session.AbsoluteExpiresAt) {
		t.Fatalf("activity refresh exceeded absolute expiry: idle=%s absolute=%s", afterRefresh.IdleExpiresAt, session.AbsoluteExpiresAt)
	}
}

func TestLoginFailuresAreRateLimitedBeforeImmediateAccountLockout(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	current, err := settings.GetSecuritySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.LoginLockoutThreshold = 3
	if _, err := settings.UpdateSecuritySettings(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"wrong password"}`))
		req.RemoteAddr = "198.51.100.10:5000"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d body = %s", i+1, res.Code, res.Body.String())
		}
	}
	limitedReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"wrong password"}`))
	limitedReq.RemoteAddr = "198.51.100.10:5000"
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusTooManyRequests || !strings.Contains(limitedRes.Body.String(), "login_rate_limited") {
		t.Fatalf("expected login rate limit, status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}
	user, err := auth.FindUserByUsername(t.Context(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if user.Status == "locked" {
		t.Fatal("short burst from one source must be rate limited before locking the account")
	}
}

func TestLoginRequiresTurnstileWhenConfigured(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "turnstile-user", Username: "operator"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{AppName: "AutoStream", Timezone: "Asia/Tokyo", TurnstileEnabled: true, TurnstileSiteKey: "site-key", TurnstileConfigured: true}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppTurnstileSecretName, "turnstile-secret"); err != nil {
		t.Fatal(err)
	}
	turnstile := &fakeTurnstileVerifier{result: TurnstileVerifyResult{Success: true, Action: "login"}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(appSettings), WithSecretStore(secrets), WithTurnstileVerifier(turnstile))

	missingReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"correct horse battery"}`))
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusForbidden || !strings.Contains(missingRes.Body.String(), "turnstile_token_required") {
		t.Fatalf("missing turnstile status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}
	if len(turnstile.requests) != 0 {
		t.Fatalf("missing token should not call verifier: %#v", turnstile.requests)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"correct horse battery","turnstile_token":"client-token"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "csrf_token") {
		t.Fatalf("turnstile login status = %d body = %s", res.Code, res.Body.String())
	}
	if len(turnstile.requests) != 1 || turnstile.requests[0].Secret != "turnstile-secret" || turnstile.requests[0].Token != "client-token" {
		t.Fatalf("turnstile verifier request mismatch: %#v", turnstile.requests)
	}
}

func TestAlternativeLoginStartsRequireTurnstileWhenConfigured(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Login",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "google-client-secret",
		Scopes:       []string{"openid", "email"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{AppName: "AutoStream", Timezone: "Asia/Tokyo", TurnstileEnabled: true, TurnstileSiteKey: "site-key", TurnstileConfigured: true}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppTurnstileSecretName, "turnstile-secret"); err != nil {
		t.Fatal(err)
	}
	turnstile := &fakeTurnstileVerifier{result: TurnstileVerifyResult{Success: true, Action: "login"}}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithPasskeyStore(auth),
		WithIntegrationStore(integrations),
		WithOAuthLoginStore(store.NewMemoryOAuthLoginStore()),
		WithAppSettingsStore(appSettings),
		WithSecretStore(secrets),
		WithTurnstileVerifier(turnstile),
	)

	missingCases := []struct {
		name string
		path string
	}{
		{name: "passkey", path: "/auth/passkeys/login/start"},
		{name: "oauth", path: "/auth/oauth/" + provider.ID + "/start"},
	}
	for _, testCase := range missingCases {
		t.Run(testCase.name+" missing token", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testCase.path, bytes.NewBufferString(`{}`))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "turnstile_token_required") {
				t.Fatalf("missing turnstile status = %d body = %s", res.Code, res.Body.String())
			}
		})
	}
	if len(turnstile.requests) != 0 {
		t.Fatalf("missing tokens must not call verifier: %#v", turnstile.requests)
	}

	validCases := []struct {
		name  string
		path  string
		token string
	}{
		{name: "passkey", path: "/auth/passkeys/login/start", token: "passkey-client-token"},
		{name: "oauth", path: "/auth/oauth/" + provider.ID + "/start", token: "oauth-client-token"},
	}
	for _, testCase := range validCases {
		t.Run(testCase.name+" valid token", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testCase.path, bytes.NewBufferString(`{"turnstile_token":"`+testCase.token+`"}`))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("valid turnstile status = %d body = %s", res.Code, res.Body.String())
			}
		})
	}
	if len(turnstile.requests) != 2 || turnstile.requests[0].Token != "passkey-client-token" || turnstile.requests[1].Token != "oauth-client-token" {
		t.Fatalf("alternative login verifier requests mismatch: %#v", turnstile.requests)
	}
}

func TestEmailConfirmationRequiresTurnstileBeforeConsumingToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "self-id", Username: "operator", Email: "old@example.jp"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	challenge, err := auth.CreateEmailChangeChallenge(t.Context(), "self-id", "new@example.jp", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{AppName: "AutoStream", Timezone: "Asia/Tokyo", TurnstileEnabled: true, TurnstileSiteKey: "site-key", TurnstileConfigured: true}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppTurnstileSecretName, "turnstile-secret"); err != nil {
		t.Fatal(err)
	}
	turnstile := &fakeTurnstileVerifier{result: TurnstileVerifyResult{Success: true, Action: "email_confirm"}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(appSettings), WithSecretStore(secrets), WithTurnstileVerifier(turnstile))

	missingReq := httptest.NewRequest(http.MethodPost, "/auth/email/confirm", bytes.NewBufferString(`{"token":"`+challenge.Token+`"}`))
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusForbidden || !strings.Contains(missingRes.Body.String(), "turnstile_token_required") {
		t.Fatalf("missing turnstile confirm status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}
	if _, err := auth.GetEmailChangeChallenge(t.Context(), challenge.Token); err != nil {
		t.Fatalf("failed turnstile must not consume email token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/email/confirm", bytes.NewBufferString(`{"token":"`+challenge.Token+`","turnstile_token":"client-token"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"email_changed"`) {
		t.Fatalf("email confirm status = %d body = %s", res.Code, res.Body.String())
	}
	user, err := auth.GetUser(t.Context(), "self-id")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "new@example.jp" {
		t.Fatalf("email was not confirmed: %#v", user)
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
