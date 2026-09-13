package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
