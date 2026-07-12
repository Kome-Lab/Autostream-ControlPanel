package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

func TestCurrentUserAvatarLifecycle(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-avatar", Username: "avatar-admin", Email: "avatar@example.jp", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	cookie, csrfToken := loginForTest(t, handler, "avatar-admin", "correct horse battery")
	avatarBody := testAvatarPNG(t, 96, 96)

	upload := httptest.NewRequest(http.MethodPut, "/auth/avatar", bytes.NewReader(avatarBody))
	upload.AddCookie(cookie)
	upload.Header.Set("Content-Type", "image/png")
	upload.Header.Set("X-CSRF-Token", csrfToken)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("avatar upload status = %d body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if strings.Contains(uploadResponse.Body.String(), "image_data") || strings.Contains(uploadResponse.Body.String(), "iVBOR") {
		t.Fatalf("avatar upload response leaked binary data: %s", uploadResponse.Body.String())
	}
	var uploaded struct {
		AvatarURL   string `json:"avatar_url"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uploaded.AvatarURL, "/auth/avatar?v=") || uploaded.ContentType != "image/png" || uploaded.SizeBytes != int64(len(avatarBody)) {
		t.Fatalf("unexpected avatar response: %#v", uploaded)
	}

	me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	me.AddCookie(cookie)
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK || !strings.Contains(meResponse.Body.String(), uploaded.AvatarURL) || !strings.Contains(meResponse.Body.String(), "avatar_updated_at") {
		t.Fatalf("auth me did not expose avatar metadata: status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}

	download := httptest.NewRequest(http.MethodGet, uploaded.AvatarURL, nil)
	download.AddCookie(cookie)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("avatar download status = %d body = %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if !bytes.Equal(downloadResponse.Body.Bytes(), avatarBody) {
		t.Fatal("avatar download bytes did not match upload")
	}
	if got := downloadResponse.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("avatar content type = %q", got)
	}
	if got := downloadResponse.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("avatar cache control = %q", got)
	}
	if got := downloadResponse.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("avatar nosniff header = %q", got)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/auth/avatar", nil)
	remove.AddCookie(cookie)
	remove.Header.Set("X-CSRF-Token", csrfToken)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("avatar delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/auth/avatar", nil)
	missing.AddCookie(cookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted avatar status = %d body = %s", missingResponse.Code, missingResponse.Body.String())
	}

	events := auth.AuditEvents()
	if len(events) < 3 || !hasAuditAction(events, "auth.avatar.update") || !hasAuditAction(events, "auth.avatar.delete") {
		t.Fatalf("avatar audit actions missing: %#v", events)
	}
}

func TestCurrentUserAvatarRejectsUnsupportedOversizedAndInvalidDimensions(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-avatar-validation", Username: "avatar-validator"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	cookie, csrfToken := loginForTest(t, handler, "avatar-validator", "correct horse battery")

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantCode   string
	}{
		{name: "unsupported", body: []byte("not an image"), wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_avatar_type"},
		{name: "corrupt png", body: append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...), wantStatus: http.StatusBadRequest, wantCode: "invalid_avatar_image"},
		{name: "too small", body: testAvatarPNG(t, 16, 16), wantStatus: http.StatusBadRequest, wantCode: "avatar_dimensions_out_of_range"},
		{name: "too large", body: bytes.Repeat([]byte{0}, maxUserAvatarBytes+1), wantStatus: http.StatusRequestEntityTooLarge, wantCode: "avatar_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/auth/avatar", bytes.NewReader(tt.body))
			req.AddCookie(cookie)
			req.Header.Set("Content-Type", "image/png")
			req.Header.Set("X-CSRF-Token", csrfToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantStatus || !strings.Contains(res.Body.String(), tt.wantCode) {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
		})
	}
}

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

func TestAdminAuditEventNotificationPolicy(t *testing.T) {
	cases := []struct {
		name  string
		event store.AuditEvent
		want  bool
	}{
		{name: "oauth account update", event: store.AuditEvent{Action: "oauth_accounts.update", ActorUsername: "ops"}, want: true},
		{name: "notification channel create", event: store.AuditEvent{Action: "notification_channels.create", ActorUsername: "ops"}, want: true},
		{name: "stream runtime events stay out", event: store.AuditEvent{Action: "streams.start", ActorUsername: "ops"}, want: false},
		{name: "service actor stays out", event: store.AuditEvent{Action: "nodes.update", ActorUsername: "service:worker"}, want: false},
		{name: "blank action stays out", event: store.AuditEvent{ActorUsername: "ops"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adminAuditEventNotificationAllowed(tc.event); got != tc.want {
				t.Fatalf("adminAuditEventNotificationAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdminAuditNotificationSummaryUsesRedactedEvent(t *testing.T) {
	event := store.RedactAuditEvent(store.AuditEvent{
		Action:       "secrets.update",
		ResourceType: "secret",
		ResourceID:   "raw-secret-token",
		Result:       "success",
		Metadata:     map[string]any{"webhook_url": "https://discord.com/api/webhooks/id/raw-secret-token"},
	})
	summary := adminAuditNotificationSummary(event)
	metadata := toJSONForTest(t, event.Metadata)
	if strings.Contains(summary, "raw-secret-token") || strings.Contains(metadata, "raw-secret-token") {
		t.Fatalf("admin audit notification leaked raw secret: summary=%q metadata=%s", summary, metadata)
	}
	if severity := adminAuditNotificationSeverity(event); severity != "warning" {
		t.Fatalf("security-related admin audit severity = %q, want warning", severity)
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

	state, oauthCookie := startOAuthForTest(t, handler, provider.ID)
	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/callback?state="+url.QueryEscape(state)+"&code=callback-code", nil)
	req.AddCookie(oauthCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	location := res.Header().Get("Location")
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(location, "/login#") {
		t.Fatalf("oauth MFA redirect status=%d location=%q body=%s", res.Code, location, res.Body.String())
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
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

func TestCreateOAuthProviderDefaultRolesRequireRoleAssignmentPermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	role, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(store.NewMemoryIntegrationStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	body := fmt.Sprintf(`{"provider_type":"google","name":"Google Login","enabled":true,"client_id":"client-id","client_secret":"client-secret","allowed_domains":["example.com"],"auto_provision":true,"default_role_ids":[%q],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`, role.ID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected roles.assign requirement, status=%d body=%s", res.Code, res.Body.String())
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
	if !account.RefreshTokenConfigured || account.TokenFingerprint == "" || account.Email != "archive@example.com" {
		t.Fatalf("unexpected public account response: %#v", account)
	}
	dispatchAccount, err := integrations.GetOAuthAccountForDispatch(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatchAccount.RefreshToken != "raw-google-refresh-token" {
		t.Fatal("stored dispatch account does not contain refresh token")
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
	if accounts[0].AccountLabel != "Google Drive 接続アカウント" || accounts[0].DisplayName != "Google Drive 接続アカウント" {
		t.Fatalf("connected account label should fall back to provider label: %#v", accounts)
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

func TestStreamLifecycleEndpoints(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.create", "streams.start", "streams.stop", "streams.update", "streams.retry_upload", "logs.read", "archives.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "lifecycle discord", "discord_bot-01", "guild-life", "voice-life", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createBody := bytes.NewBufferString(`{"name":"morning stream","discord_config_id":"` + config.ID + `","discord_guild_id":"guild-life","discord_voice_channel_id":"voice-life"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/streams", createBody)
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "created" || created.ID == "" {
		t.Fatalf("bad created stream: %#v", created)
	}
	registerAssignedServices(t, auth, created.ID, requiredStartServiceTypes...)

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/start", nil)
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		Stream   store.Stream                 `json:"stream"`
		Dispatch []servicecall.DispatchResult `json:"dispatch"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	if startBody.Stream.Status != "live" || len(startBody.Dispatch) != 3 {
		t.Fatalf("expected live status and dispatch, got %#v", startBody)
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected start dispatch, got %#v", dispatcher)
	}

	failReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/mark-failed", nil)
	failReq.AddCookie(cookie)
	failReq.Header.Set("X-CSRF-Token", csrf)
	failRes := httptest.NewRecorder()
	handler.ServeHTTP(failRes, failReq)
	if failRes.Code != http.StatusOK {
		t.Fatalf("mark failed status = %d body = %s", failRes.Code, failRes.Body.String())
	}
	var failed store.Stream
	if err := json.NewDecoder(failRes.Body).Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed status, got %#v", failed)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID, nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get stream status = %d body = %s", getRes.Code, getRes.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/retry-upload", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusAccepted {
		t.Fatalf("retry upload status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID+"/logs", nil)
	logsReq.AddCookie(cookie)
	logsRes := httptest.NewRecorder()
	handler.ServeHTTP(logsRes, logsReq)
	if logsRes.Code != http.StatusOK || !strings.Contains(logsRes.Body.String(), "archive upload retry requested") {
		t.Fatalf("logs status = %d body = %s", logsRes.Code, logsRes.Body.String())
	}

	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: created.ID, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + created.ID + "/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: created.ID, Kind: "archive", Name: "bad", RelativePath: "../secret", SizeBytes: 1}); err == nil {
		t.Fatal("unsafe artifact path was accepted")
	}
	artifactsReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID+"/artifacts", nil)
	artifactsReq.AddCookie(cookie)
	artifactsRes := httptest.NewRecorder()
	handler.ServeHTTP(artifactsRes, artifactsReq)
	if artifactsRes.Code != http.StatusOK || !strings.Contains(artifactsRes.Body.String(), "final/"+created.ID+"/final.mp4") || strings.Contains(artifactsRes.Body.String(), "secret") {
		t.Fatalf("artifacts status = %d body = %s", artifactsRes.Code, artifactsRes.Body.String())
	}
}

func TestStreamArchiveArtifactAdminRoutes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-admin", Roles: []string{"archive_admin"}}, "correct horse battery", []string{"streams.read", "archives.read", "archives.download", "archives.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive managed stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, Kind: "metadata", Name: "metadata.json", RelativePath: "final/" + stream.ID + "/metadata.json", SizeBytes: 12}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	var archiveArtifact store.StreamArtifact
	for _, artifact := range artifacts {
		if artifact.Name == "final.mp4" {
			archiveArtifact = artifact
		}
	}
	if archiveArtifact.ID == "" {
		t.Fatalf("archive artifact missing id: %#v", artifacts)
	}

	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "archive-admin", "correct horse battery")
	artifactPath := "/streams/" + stream.ID + "/artifacts/" + archiveArtifact.ID

	downloadReq := httptest.NewRequest(http.MethodGet, artifactPath+"/download", nil)
	downloadReq.AddCookie(cookie)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusOK || downloadRes.Body.String() != "archive-bytes" {
		t.Fatalf("download status=%d body=%q", downloadRes.Code, downloadRes.Body.String())
	}
	if got := downloadRes.Header().Get("Content-Disposition"); !strings.Contains(got, "final.mp4") {
		t.Fatalf("download content disposition = %q", got)
	}
	if dispatcher.archiveDownloadCalls != 1 || dispatcher.archiveArtifact.ID != archiveArtifact.ID {
		t.Fatalf("download did not dispatch expected artifact: %#v", dispatcher)
	}

	previewReq := httptest.NewRequest(http.MethodGet, artifactPath+"/download?inline=1", nil)
	previewReq.AddCookie(cookie)
	previewRes := httptest.NewRecorder()
	handler.ServeHTTP(previewRes, previewReq)
	if previewRes.Code != http.StatusOK || previewRes.Body.String() != "archive-bytes" {
		t.Fatalf("preview status=%d body=%q", previewRes.Code, previewRes.Body.String())
	}
	if got := previewRes.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") || !strings.Contains(got, "final.mp4") {
		t.Fatalf("preview content disposition = %q", got)
	}
	if dispatcher.archiveDownloadCalls != 2 || dispatcher.archiveArtifact.ID != archiveArtifact.ID {
		t.Fatalf("preview did not dispatch expected artifact: %#v", dispatcher)
	}

	invalidRenameReq := httptest.NewRequest(http.MethodPut, artifactPath, bytes.NewBufferString(`{"name":"../secret.mp4"}`))
	invalidRenameReq.AddCookie(cookie)
	invalidRenameReq.Header.Set("X-CSRF-Token", csrf)
	invalidRenameRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidRenameRes, invalidRenameReq)
	if invalidRenameRes.Code != http.StatusBadRequest || !strings.Contains(invalidRenameRes.Body.String(), "invalid_stream_artifact") {
		t.Fatalf("invalid rename status=%d body=%s", invalidRenameRes.Code, invalidRenameRes.Body.String())
	}
	if dispatcher.archiveRenameCalls != 0 {
		t.Fatalf("invalid rename must not dispatch: %#v", dispatcher)
	}

	renameReq := httptest.NewRequest(http.MethodPut, artifactPath, bytes.NewBufferString(`{"name":"renamed.mp4"}`))
	renameReq.AddCookie(cookie)
	renameReq.Header.Set("X-CSRF-Token", csrf)
	renameRes := httptest.NewRecorder()
	handler.ServeHTTP(renameRes, renameReq)
	if renameRes.Code != http.StatusOK || !strings.Contains(renameRes.Body.String(), "renamed.mp4") {
		t.Fatalf("rename status=%d body=%s", renameRes.Code, renameRes.Body.String())
	}
	if dispatcher.archiveRenameCalls != 1 || dispatcher.archiveRenameName != "renamed.mp4" {
		t.Fatalf("rename did not dispatch expected artifact: %#v", dispatcher)
	}
	renamedArtifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !artifactListContains(renamedArtifacts, "renamed.mp4", "final/"+stream.ID+"/renamed.mp4") || artifactListContains(renamedArtifacts, "final.mp4", "") {
		t.Fatalf("rename did not update artifact metadata safely: %#v", renamedArtifacts)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, artifactPath, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if dispatcher.archiveDeleteCalls != 1 {
		t.Fatalf("delete did not dispatch: %#v", dispatcher)
	}
	finalArtifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if artifactListContains(finalArtifacts, "renamed.mp4", "") {
		t.Fatalf("delete did not remove artifact metadata: %#v", finalArtifacts)
	}
}

func TestArchiveArtifactSharePublicPlaybackWithoutLogin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-admin", Roles: []string{"archive_admin"}}, "correct horse battery", []string{"streams.read", "archives.read", "archives.download", "archives.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "public share stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("unexpected artifacts: %#v", artifacts)
	}

	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "archive-admin", "correct horse battery")
	sharePath := "/streams/" + stream.ID + "/artifacts/" + artifacts[0].ID + "/shares"

	createReq := httptest.NewRequest(http.MethodPost, sharePath, bytes.NewBufferString(`{"expires_in_hours":2,"allow_download":false}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create share status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var createBody struct {
		ID     string `json:"id"`
		Token  string `json:"token"`
		APIURL string `json:"api_url"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.ID == "" || createBody.Token == "" || createBody.APIURL == "" || createBody.URL == "" {
		t.Fatalf("share response missing fields: %#v", createBody)
	}

	listReq := httptest.NewRequest(http.MethodGet, sharePath, nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list share status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), createBody.Token) || strings.Contains(listRes.Body.String(), "token_hash") {
		t.Fatalf("share list leaked token material: %s", listRes.Body.String())
	}

	publicReq := httptest.NewRequest(http.MethodGet, createBody.APIURL, nil)
	publicRes := httptest.NewRecorder()
	handler.ServeHTTP(publicRes, publicReq)
	if publicRes.Code != http.StatusOK || !strings.Contains(publicRes.Body.String(), "final.mp4") || strings.Contains(publicRes.Body.String(), "token_hash") {
		t.Fatalf("public share status=%d body=%s", publicRes.Code, publicRes.Body.String())
	}

	playbackReq := httptest.NewRequest(http.MethodGet, createBody.APIURL+"/download", nil)
	playbackRes := httptest.NewRecorder()
	handler.ServeHTTP(playbackRes, playbackReq)
	if playbackRes.Code != http.StatusOK || playbackRes.Body.String() != "archive-bytes" {
		t.Fatalf("playback status=%d body=%q", playbackRes.Code, playbackRes.Body.String())
	}
	if got := playbackRes.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("playback content disposition = %q", got)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, createBody.APIURL+"/download?download=1", nil)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusForbidden || !strings.Contains(downloadRes.Body.String(), "archive_share_download_disabled") {
		t.Fatalf("disabled download status=%d body=%s", downloadRes.Code, downloadRes.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, sharePath+"/"+createBody.ID, nil)
	revokeReq.AddCookie(cookie)
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeRes := httptest.NewRecorder()
	handler.ServeHTTP(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeRes.Code, revokeRes.Body.String())
	}
	revokedReq := httptest.NewRequest(http.MethodGet, createBody.APIURL, nil)
	revokedRes := httptest.NewRecorder()
	handler.ServeHTTP(revokedRes, revokedReq)
	if revokedRes.Code != http.StatusGone || !strings.Contains(revokedRes.Body.String(), "archive_share_revoked") {
		t.Fatalf("revoked share status=%d body=%s", revokedRes.Code, revokedRes.Body.String())
	}
}

func artifactListContains(artifacts []store.StreamArtifact, name, relativePath string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name && (relativePath == "" || artifact.RelativePath == relativePath) {
			return true
		}
	}
	return false
}

func TestCreateStreamRejectsBlankName(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"   "}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCreateStreamPersistsSchedule(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"scheduled stream","scheduled_start_at":"2026-07-10T01:00:00Z","scheduled_end_at":"2026-07-10T02:00:00Z"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ScheduledStartAt == nil || created.ScheduledStartAt.Format(time.RFC3339) != "2026-07-10T01:00:00Z" {
		t.Fatalf("scheduled start was not persisted: %#v", created.ScheduledStartAt)
	}
	if created.ScheduledEndAt == nil || created.ScheduledEndAt.Format(time.RFC3339) != "2026-07-10T02:00:00Z" {
		t.Fatalf("scheduled end was not persisted: %#v", created.ScheduledEndAt)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/streams", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var listed []store.Stream
	if err := json.NewDecoder(listRes.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ScheduledStartAt == nil || listed[0].ScheduledEndAt == nil {
		t.Fatalf("list did not return schedule: %#v", listed)
	}
}

func TestCreateStreamAssignsSelectedPrimaryNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"auto ready stream","encoder_service_id":"encoder-01","worker_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	primary := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primary, []string{"encoder_recorder", "worker"}); len(missing) > 0 {
		t.Fatalf("selected primary nodes were not assigned, missing=%#v assignments=%#v", missing, assignments)
	}
	events := auth.AuditEvents()
	assignAudits := 0
	for _, event := range events {
		if event.Action == "services.assign" && event.ResourceID != "" && event.Metadata["source"] == "stream_settings" {
			assignAudits++
		}
	}
	if assignAudits != 2 {
		t.Fatalf("expected assignment audit events for stream settings, got %d events=%#v", assignAudits, events)
	}
}

func TestCreateStreamRejectsPrimaryNodeAssignmentWithoutPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked stream","encoder_service_id":"encoder-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_denied") {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("stream should not be created when assignment permission is missing: %#v", items)
	}
}

func TestCreateStreamRejectsWrongPrimaryNodeType(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"wrong node stream","encoder_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "encoder_service_type_invalid") {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCreateStreamRejectsInvalidSchedule(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{
			name: "invalid time",
			body: `{"name":"bad schedule","scheduled_start_at":"2026/07/10 10:00"}`,
			code: "schedule_time_invalid",
		},
		{
			name: "end before start",
			body: `{"name":"bad schedule","scheduled_start_at":"2026-07-10T02:00:00Z","scheduled_end_at":"2026-07-10T01:00:00Z"}`,
			code: "schedule_end_before_start",
		},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s status = %d body = %s", tc.name, res.Code, res.Body.String())
		}
	}
}

func TestStreamSettingsCanBeSavedAndReturned(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	discordOne, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{"guild_id": "guild-01", "voice_channel_id": "voice-01"})
	if err != nil {
		t.Fatal(err)
	}
	discordTwo, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord two", map[string]any{"guild_id": "guild-02", "voice_channel_id": "voice-02"})
	if err != nil {
		t.Fatal(err)
	}
	youtubeOutput, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "youtube output", map[string]any{"mode": "rtmp", "rtmp_url": "rtmps://a.rtmps.youtube.com/live2"})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive profile", map[string]any{"format": "mp4"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	createReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"configured stream","discord_config_id":"`+discordOne.ID+`","discord_guild_id":"guild-create","discord_voice_channel_id":"voice-create","discord_text_channel_id":"text-create","auto_start_trigger":"discord_voice_join","youtube_output_id":"`+youtubeOutput.ID+`"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.DiscordConfigID != discordOne.ID || created.DiscordGuildID != "guild-create" || created.DiscordVoiceID != "voice-create" || created.DiscordTextID != "text-create" || created.AutoStartTrigger != "discord_voice_join" || created.YouTubeOutputID != youtubeOutput.ID {
		t.Fatalf("create did not persist stream settings: %#v", created)
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", bytes.NewBufferString(`{"discord_config_id":"`+discordTwo.ID+`","discord_guild_id":"guild-stream","discord_voice_channel_id":"voice-stream","discord_text_channel_id":"text-stream","auto_start_trigger":"discord_voice_join","archive_profile_id":"`+archiveProfile.ID+`","encoder_input_url":"srt://input.example.com:9000"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update settings status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	var updated store.Stream
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.DiscordConfigID != discordTwo.ID || updated.DiscordGuildID != "guild-stream" || updated.DiscordVoiceID != "voice-stream" || updated.DiscordTextID != "text-stream" || updated.AutoStartTrigger != "discord_voice_join" || updated.ArchiveProfileID != archiveProfile.ID || updated.EncoderInputURL == "" || updated.YouTubeOutputID != "" {
		t.Fatalf("unexpected updated stream settings: %#v", updated)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID, nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), discordTwo.ID) || !strings.Contains(getRes.Body.String(), "guild-stream") || !strings.Contains(getRes.Body.String(), "discord_voice_join") || !strings.Contains(getRes.Body.String(), archiveProfile.ID) {
		t.Fatalf("get stream did not return settings: status=%d body=%s", getRes.Code, getRes.Body.String())
	}
}

func TestCreateStreamRejectsInvalidAutoStartSettings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord", map[string]any{"guild_id": "guild-01", "voice_channel_id": "voice-01"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown trigger",
			body: `{"name":"bad trigger","auto_start_trigger":"vc_join"}`,
			code: "auto_start_trigger_invalid",
		},
		{
			name: "missing discord voice target",
			body: `{"name":"missing discord","discord_config_id":"` + discordProfile.ID + `","discord_guild_id":"guild-01","auto_start_trigger":"discord_voice_join"}`,
			code: "auto_start_discord_required",
		},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s status = %d body = %s", tc.name, res.Code, res.Body.String())
		}
	}
}

func TestCreateStreamMaterializesDirectArchiveSettings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Direct",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Direct Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"direct archive stream","archive_oauth_account_id":"`+account.ID+`","archive_folder_id":"raw-drive-folder-id","archive_shared_drive":true,"archive_shared_drive_id":"shared-drive-01","archive_retention_days":45}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") || strings.Contains(res.Body.String(), "raw-google-refresh-token") || strings.Contains(res.Body.String(), "raw-google-client-secret") {
		t.Fatalf("direct archive response leaked raw secret: %s", res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ArchiveProfileID == "" || created.ArchiveDriveDestinationID == "" || created.ArchiveOAuthAccountID != account.ID || !created.ArchiveFolderIDConfigured || !created.ArchiveSharedDrive || created.ArchiveSharedDriveID != "shared-drive-01" {
		t.Fatalf("direct archive settings were not persisted on stream: %#v", created)
	}
	if !strings.HasPrefix(created.ArchiveFileName, "direct archive stream-") || !strings.HasSuffix(created.ArchiveFileName, ".mp4") {
		t.Fatalf("default archive file name was not generated from stream name/date: %#v", created)
	}
	destination, err := integrations.GetDriveDestinationForDispatch(t.Context(), created.ArchiveDriveDestinationID)
	if err != nil {
		t.Fatal(err)
	}
	if destination.AuthMode != "oauth2" || destination.OAuthAccountID != account.ID || destination.FolderID != "raw-drive-folder-id" || !destination.SharedDrive {
		t.Fatalf("drive destination was not materialized for dispatch: %#v", destination)
	}
	profile, err := profiles.GetProfile(t.Context(), store.ProfileArchive, created.ArchiveProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["drive_destination_id"] != destination.ID || profile.Config["stream_archive_direct"] != true || profile.Config["archive_file_name"] != created.ArchiveFileName || profile.Config["shared_drive_id"] != "shared-drive-01" || profile.Config["retention_days"] != 45 {
		t.Fatalf("archive profile was not materialized from stream settings: %#v", profile.Config)
	}
}

func TestCreateStreamMaterializesLocalRetentionArchiveProfileWithoutDrive(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"local retained stream","archive_retention_days":7}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ArchiveProfileID == "" || created.ArchiveDriveDestinationID != "" || created.ArchiveOAuthAccountID != "" || created.ArchiveFolderIDConfigured {
		t.Fatalf("local retention archive settings were not persisted as expected: %#v", created)
	}
	profile, err := profiles.GetProfile(t.Context(), store.ProfileArchive, created.ArchiveProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["stream_archive_direct"] != true || profile.Config["retention_days"] != 7 {
		t.Fatalf("local retention archive profile was not materialized: %#v", profile.Config)
	}
	if _, ok := profile.Config["drive_destination_id"]; ok {
		t.Fatalf("local retention profile should not include Drive destination: %#v", profile.Config)
	}
}

func TestStreamSettingsRejectUnknownDiscordConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "configured stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	createReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"bad stream","discord_config_id":"missing-discord-config"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusBadRequest || !strings.Contains(createRes.Body.String(), "discord_config_not_found") {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	overrideOnlyCreateReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"bad override stream","discord_guild_id":"guild-without-config","discord_voice_channel_id":"voice-without-config"}`))
	overrideOnlyCreateReq.AddCookie(cookie)
	overrideOnlyCreateReq.Header.Set("X-CSRF-Token", csrf)
	overrideOnlyCreateRes := httptest.NewRecorder()
	handler.ServeHTTP(overrideOnlyCreateRes, overrideOnlyCreateReq)
	if overrideOnlyCreateRes.Code != http.StatusBadRequest || !strings.Contains(overrideOnlyCreateRes.Body.String(), "discord_config_required") {
		t.Fatalf("override-only create status=%d body=%s", overrideOnlyCreateRes.Code, overrideOnlyCreateRes.Body.String())
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"discord_config_id":"missing-discord-config"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusBadRequest || !strings.Contains(updateRes.Body.String(), "discord_config_not_found") {
		t.Fatalf("update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	overrideOnlyReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"discord_guild_id":"guild-without-config","discord_voice_channel_id":"voice-without-config"}`))
	overrideOnlyReq.AddCookie(cookie)
	overrideOnlyReq.Header.Set("X-CSRF-Token", csrf)
	overrideOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(overrideOnlyRes, overrideOnlyReq)
	if overrideOnlyRes.Code != http.StatusBadRequest || !strings.Contains(overrideOnlyRes.Body.String(), "discord_config_required") {
		t.Fatalf("override-only update status=%d body=%s", overrideOnlyRes.Code, overrideOnlyRes.Body.String())
	}
}

func TestStreamSettingsRejectUnknownSelectableReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "configured stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{name: "youtube output", body: `{"name":"bad youtube","youtube_output_id":"missing-youtube"}`, code: "youtube_output_not_found"},
		{name: "encoder profile", body: `{"name":"bad encoder","encoder_profile_id":"missing-encoder"}`, code: "encoder_profile_not_found"},
		{name: "caption profile", body: `{"name":"bad caption","caption_profile_id":"missing-caption"}`, code: "caption_profile_not_found"},
		{name: "overlay profile", body: `{"name":"bad overlay","overlay_profile_id":"missing-overlay"}`, code: "overlay_profile_not_found"},
		{name: "archive profile", body: `{"name":"bad archive","archive_profile_id":"missing-archive"}`, code: "archive_profile_not_found"},
		{name: "archive oauth account", body: `{"name":"bad archive oauth","archive_oauth_account_id":"missing-account"}`, code: "drive_oauth_account_unavailable"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s create status=%d body=%s", tc.name, res.Code, res.Body.String())
		}
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"youtube_output_id":"missing-youtube"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusBadRequest || !strings.Contains(updateRes.Body.String(), "youtube_output_not_found") {
		t.Fatalf("update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
}

func TestStreamStartStopDispatchesAssignedServices(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "dispatch discord", "discord_bot-01", "guild-01", "voice-01", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-01","discord_voice_channel_id":"voice-01","encoder_input_url":"srt://input.example.com:9000"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	if !strings.Contains(startRes.Body.String(), `"dispatch"`) || !strings.Contains(startRes.Body.String(), `"service_type":"encoder_recorder"`) {
		t.Fatalf("start response does not include dispatch results: %s", startRes.Body.String())
	}
	if dispatcher.startCalls != 1 || dispatcher.startedStream.ID != stream.ID || len(dispatcher.startedServices) != 3 {
		t.Fatalf("dispatcher was not called correctly: %#v", dispatcher)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if !strings.Contains(stopRes.Body.String(), `"dispatch"`) || !strings.Contains(stopRes.Body.String(), `"service_type":"worker"`) {
		t.Fatalf("stop response does not include dispatch results: %s", stopRes.Body.String())
	}
	if dispatcher.stopCalls != 1 || dispatcher.stoppedStream.ID != stream.ID || len(dispatcher.stoppedServices) != 3 {
		t.Fatalf("stop dispatcher was not called correctly: %#v", dispatcher)
	}
}

func TestStartStreamResolvesDiscordConfigForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord config stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "main discord", "discord_bot-01", "guild-from-config", "voice-from-config", "text-from-config")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"manual-guild","discord_voice_channel_id":"manual-voice","discord_text_channel_id":"manual-text"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "manual-guild" || dispatcher.startRequest.DiscordVoiceChannelID != "manual-voice" || dispatcher.startRequest.DiscordTextChannelID != "manual-text" {
		t.Fatalf("discord config was not resolved into start request: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamUsesSavedDiscordConfigSetting(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "saved discord config stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "saved discord", "discord_bot-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, DiscordGuildID: "guild-stream-override", DiscordVoiceID: "voice-stream-override", DiscordTextID: "text-stream-override", AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "guild-stream-override" || dispatcher.startRequest.DiscordVoiceChannelID != "voice-stream-override" || dispatcher.startRequest.DiscordTextChannelID != "text-stream-override" {
		t.Fatalf("saved discord config was not applied: %#v", dispatcher.startRequest)
	}
}

func TestServiceStartStreamUsesSavedSettingsForPrimaryDiscordBot(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service start stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, DiscordGuildID: "guild-stream-override", DiscordVoiceID: "voice-stream-override", DiscordTextID: "text-stream-override", AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"attacker-config","discord_guild_id":"attacker-guild","discord_voice_channel_id":"attacker-voice"}`))
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected one start dispatch, got %d", dispatcher.startCalls)
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "guild-stream-override" || dispatcher.startRequest.DiscordVoiceChannelID != "voice-stream-override" {
		t.Fatalf("service start must use saved settings and ignore request overrides: %#v", dispatcher.startRequest)
	}
	if dispatcher.startRequest.DiscordTextChannelID != "text-stream-override" {
		t.Fatalf("service start should use saved stream text channel: %#v", dispatcher.startRequest)
	}
	events := auth.AuditEvents()
	foundStartAudit := false
	for _, event := range events {
		if event.Action == "streams.start" && event.ResourceID == stream.ID && event.Result == "success" {
			foundStartAudit = event.ActorUserID == "service:discord-01" && event.ActorUsername == "discord-01"
		}
	}
	if !foundStartAudit {
		t.Fatalf("service start audit missing service actor: %#v", events)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	duplicateReq.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	duplicateRes := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusOK || !strings.Contains(duplicateRes.Body.String(), "already_active") {
		t.Fatalf("duplicate service start status = %d body = %s", duplicateRes.Code, duplicateRes.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("active stream must not be dispatched again, got %d calls", dispatcher.startCalls)
	}
}

func TestServiceStartStreamAllowsConfiguredDiscordBotWithoutPriorAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord configured service start stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "configured service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, DiscordGuildID: "guild-stream-override", DiscordVoiceID: "voice-stream-override", DiscordTextID: "text-stream-override", AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected one start dispatch, got %d", dispatcher.startCalls)
	}
	foundDiscord := false
	for _, service := range dispatcher.startedServices {
		if service.ServiceID == "discord-01" && service.ServiceType == "discord_bot" && service.AssignmentRole == "primary" {
			foundDiscord = true
		}
	}
	if !foundDiscord {
		t.Fatalf("configured discord bot was not assigned before dispatch: %#v", dispatcher.startedServices)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, service := range assignments {
		if service.ServiceID == "discord-01" && service.ServiceType == "discord_bot" && service.AssignmentRole == "primary" {
			persisted = true
		}
	}
	if !persisted {
		t.Fatalf("configured discord bot assignment was not persisted: %#v", assignments)
	}
}

func TestServiceStartStreamRequiresAutoStartTrigger(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service start disabled")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, DiscordGuildID: "guild-stream-override", DiscordVoiceID: "voice-stream-override", DiscordTextID: "text-stream-override"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_auto_start_not_enabled") {
		t.Fatalf("service start without trigger status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("disabled auto-start must not dispatch, got %d calls", dispatcher.startCalls)
	}
}

func TestServiceStartStreamRequiresPrimaryDiscordBotToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service forbidden stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "service_not_primary_assignment") {
		t.Fatalf("unassigned service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("unassigned discord token must not dispatch start")
	}
}

func TestStartStreamRejectsSavedDiscordChannelOverridesWithoutConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "saved discord channel stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordGuildID: "guild-direct", DiscordVoiceID: "voice-direct", DiscordTextID: "text-direct"}); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "discord_config_required") {
		t.Fatalf("expected discord_config_required: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called without a Discord Config")
	}
}

func TestStartStreamRejectsDiscordConfigForDifferentPrimaryBot(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord config mismatch stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "wrong discord", map[string]any{
		"service_id":       "discord-bot-other",
		"guild_id":         "guild-from-config",
		"voice_channel_id": "voice-from-config",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("expected discord_config_service_mismatch: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called for mismatched discord config")
	}
}

func TestStartStreamResolvesYouTubeOutputSecretForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_main", "runtime-secret-stream-key"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "main-output", map[string]any{
		"rtmp_url":               "rtmps://youtube.example.com/live2",
		"stream_key_secret_name": "youtube_stream_key_main",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`","encoder_rtmp_url":"rtmps://attacker.example.com/live2"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "runtime-secret-stream-key") {
		t.Fatalf("stream key leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" || dispatcher.startRequest.EncoderStreamKey != "" || dispatcher.startRequest.EncoderStreamKeySecretName != "youtube_stream_key_main" {
		t.Fatalf("youtube output was not dispatched as a runtime secret reference: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamRejectsYouTubeOutputWithoutProfileRTMPURL(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_legacy", "runtime-secret-stream-key"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "legacy youtube discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy-output", map[string]any{
		"mode":                   "stream_key",
		"stream_key_secret_name": "youtube_stream_key_legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`","encoder_rtmp_url":"rtmps://attacker.example.com/live2"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "youtube_output_invalid_config") {
		t.Fatalf("expected youtube_output_invalid_config: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called for youtube output without profile RTMP URL: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamPreparesYouTubeLiveAPIDryRunWithoutSecretLeak(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube dry-run discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "auto-output", map[string]any{
		"mode":     "live_api_dry_run",
		"rtmp_url": "rtmps://youtube.example.com/live2",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "yt-dry-run-") || strings.Contains(res.Body.String(), "dry-broadcast-") {
		t.Fatalf("youtube runtime secret/state leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" ||
		dispatcher.startRequest.EncoderStreamKey != "" ||
		!strings.HasPrefix(dispatcher.startRequest.EncoderStreamKeySecretName, "youtube_stream_key_runtime_") {
		t.Fatalf("youtube live api dry-run was not prepared as a runtime secret reference: %#v", dispatcher.startRequest)
	}
	runtime := dispatcher.startRequest.YouTubeRuntime
	if runtime["mode"] != "live_api_dry_run" || runtime["output_id"] != youtube.ID || runtime["dry_run"] != true || runtime["complete_on_stop"] != true || runtime["rtmp_url"] != "rtmps://youtube.example.com/live2" || !strings.HasPrefix(stringValue(runtime["broadcast_id"]), "dry-broadcast-") || !strings.HasPrefix(stringValue(runtime["live_stream_id"]), "dry-live-stream-") || runtime["stream_key_secret_name"] != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("youtube runtime metadata was not prepared: %#v", runtime)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime was not stored: %v", err)
	}
	if stored.Mode != "live_api_dry_run" || stored.YouTubeOutput != youtube.ID || stored.RTMPURL != "rtmps://youtube.example.com/live2" || !stored.DryRun || !stored.CompleteOnStop || stored.StreamKeySecretName != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("unexpected stored youtube runtime: %#v", stored)
	}
	dryRunKey, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName)
	if err != nil || !strings.HasPrefix(dryRunKey, "yt-dry-run-") {
		t.Fatalf("dry-run stream key should be stored as a short-lived secret: value=%q err=%v", dryRunKey, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after stop, got err=%v", err)
	}
}

func TestStartStreamPreparesAndCompletesYouTubeLiveAPIWithOAuthAccount(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop", "audit_logs.read", "audit_logs.export"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	secrets := store.NewMemorySecretStore()
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube live api discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-01", LiveStreamID: "live-stream-01"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "runtime-youtube-live-api-key") || strings.Contains(res.Body.String(), "raw-youtube-refresh-token") || strings.Contains(res.Body.String(), "raw-youtube-client-secret") {
		t.Fatalf("youtube live api secret leaked in response: %s", res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.prepareRequest.Credentials.ClientID != "youtube-client-id" || youtubeLive.prepareRequest.Credentials.ClientSecret != "raw-youtube-client-secret" || youtubeLive.prepareRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("youtube live api was not called with OAuth credentials: %#v", youtubeLive.prepareRequest)
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" || dispatcher.startRequest.EncoderStreamKey != "" || !strings.HasPrefix(dispatcher.startRequest.EncoderStreamKeySecretName, "youtube_stream_key_runtime_") {
		t.Fatalf("youtube live api output was not dispatched as a runtime secret reference: %#v", dispatcher.startRequest)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stored.Mode != "live_api" || stored.OAuthAccountID != account.ID || stored.BroadcastID != "broadcast-01" || stored.RTMPURL != "rtmps://youtube.example.com/live2" || !stored.CompleteOnStop {
		t.Fatalf("youtube live api runtime was not stored: %#v err=%v", stored, err)
	}
	if stored.StreamKeySecretName != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("youtube live api runtime secret name mismatch: stored=%q dispatched=%q", stored.StreamKeySecretName, dispatcher.startRequest.EncoderStreamKeySecretName)
	}
	resolvedStreamKey, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName)
	if err != nil || resolvedStreamKey != "runtime-youtube-live-api-key" {
		t.Fatalf("youtube live api runtime stream key was not stored as a short-lived secret: value=%q err=%v", resolvedStreamKey, err)
	}
	auditListReq := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	auditListReq.AddCookie(cookie)
	auditListRes := httptest.NewRecorder()
	handler.ServeHTTP(auditListRes, auditListReq)
	if auditListRes.Code != http.StatusOK {
		t.Fatalf("audit list status = %d body = %s", auditListRes.Code, auditListRes.Body.String())
	}
	auditExportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export", nil)
	auditExportReq.AddCookie(cookie)
	auditExportRes := httptest.NewRecorder()
	handler.ServeHTTP(auditExportRes, auditExportReq)
	if auditExportRes.Code != http.StatusOK {
		t.Fatalf("audit export status = %d body = %s", auditExportRes.Code, auditExportRes.Body.String())
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(auditListRes.Body.String(), raw) || strings.Contains(auditExportRes.Body.String(), raw) {
			t.Fatalf("youtube live api audit leaked secret %q list=%s export=%s", raw, auditListRes.Body.String(), auditExportRes.Body.String())
		}
	}
	encoderService, err := auth.GetService(t.Context(), "encoder_recorder-01")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := (&Server{streams: streams, services: auth, profiles: profiles}).runtimeYouTubeStreamSecretAllowed(t.Context(), encoderService, stored.StreamKeySecretName, stream.ID)
	if err != nil || !allowed {
		t.Fatalf("primary encoder should be allowed to resolve live api runtime stream key: allowed=%v err=%v", allowed, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-01" || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("youtube live api complete was not called correctly: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after live api stop, got err=%v", err)
	}
	if _, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube live api runtime stream key secret should be cleared after stop, got err=%v", err)
	}
}

func TestYouTubeLiveAPIOutputRejectsPlainRTMPFromClient(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		secrets:      store.NewMemorySecretStore(),
		integrations: integrations,
		youtubeLive:  &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmp://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-plain", LiveStreamID: "live-stream-plain"}},
	}
	req := &servicecall.StartRequest{}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-plain", Name: "plain rtmp"}, store.Profile{ID: "youtube-plain", Config: map[string]any{"oauth_account_id": account.ID}}, req)
	if !errors.Is(err, errYouTubeLiveAPIPrepareFailed) {
		t.Fatalf("expected plain RTMP to be rejected, got err=%v req=%#v", err, req)
	}
	if req.EncoderRTMPURL != "" || req.EncoderStreamKey != "" || req.EncoderStreamKeySecretName != "" {
		t.Fatalf("plain RTMP client output must not populate dispatch request: %#v", req)
	}
}

func TestStopStreamHonorsYouTubeCompleteOnStopFalseAndManualRetryForcesComplete(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "manual youtube complete stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube manual complete discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "manual-complete-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
		"complete_on_stop": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-manual", LiveStreamID: "live-stream-manual"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stored.CompleteOnStop {
		t.Fatalf("youtube runtime should store complete_on_stop=false: %#v err=%v", stored, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 0 {
		t.Fatalf("youtube complete should be skipped on normal stop when complete_on_stop=false: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should still be cleared after skipped complete, got err=%v", err)
	}
	if !strings.Contains(toJSONForTest(t, auth.AuditEvents()), `"complete_skipped":true`) {
		t.Fatalf("audit should record skipped complete metadata: %s", toJSONForTest(t, auth.AuditEvents()))
	}

	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{StreamID: stream.ID, YouTubeOutput: youtube.ID, OAuthAccountID: account.ID, Mode: "live_api", BroadcastID: "broadcast-manual-retry", LiveStreamID: "live-stream-manual-retry", CompleteOnStop: false}); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube/complete", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK || !strings.Contains(retryRes.Body.String(), `"completed":true`) {
		t.Fatalf("manual complete retry status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-manual-retry" {
		t.Fatalf("manual complete retry should force YouTube complete: %#v", youtubeLive)
	}
}

func TestStopStreamKeepsYouTubeRuntimeWhenLiveAPICompleteFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube live api discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
		"enable_auto_stop": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{
		prepared:    ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-retry", LiveStreamID: "live-stream-retry"},
		completeErr: errors.New("youtube transition failed"),
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusBadGateway || !strings.Contains(stopRes.Body.String(), "youtube_live_api_complete_failed") {
		t.Fatalf("expected youtube complete failure, status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(stopRes.Body.String(), raw) {
			t.Fatalf("youtube stop failure leaked secret %q in response: %s", raw, stopRes.Body.String())
		}
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain for retry after complete failure: %v", err)
	}
	if stored.BroadcastID != "broadcast-retry" || stored.OAuthAccountID != account.ID || stored.YouTubeOutput != youtube.ID {
		t.Fatalf("unexpected retained youtube runtime: %#v", stored)
	}
	if stored.CompleteRetryCount != 1 || stored.CompleteNextRetryAt.IsZero() || stored.CompleteLastError != "youtube_live_api_complete_failed" {
		t.Fatalf("youtube complete failure should schedule retry metadata: %#v", stored)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("stream should be marked failed when youtube complete fails, got %s", updated.Status)
	}

	youtubeLive.completeErr = nil
	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube/complete", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK || !strings.Contains(retryRes.Body.String(), `"completed":true`) {
		t.Fatalf("expected youtube complete retry success, status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	if youtubeLive.completeCalls != 2 || youtubeLive.completeRequest.BroadcastID != "broadcast-retry" {
		t.Fatalf("youtube complete retry was not called correctly: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after manual complete retry, got err=%v", err)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(retryRes.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube complete retry leaked secret %q, response=%s audit=%s", raw, retryRes.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
}

func TestAutoRetryCompletesDueYouTubeLiveAPIRuntime(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "auto retry youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       "youtube-output-01",
		OAuthAccountID:      account.ID,
		Mode:                "live_api",
		BroadcastID:         "broadcast-auto-retry",
		LiveStreamID:        "live-stream-auto-retry",
		StreamKeySecretName: "youtube_stream_key_runtime_auto_retry",
		CompleteOnStop:      true,
		CompleteRetryCount:  1,
		CompleteNextRetryAt: time.Now().UTC().Add(-time.Minute),
		CompleteLastError:   "youtube_live_api_complete_failed",
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_runtime_auto_retry", "runtime-youtube-live-api-key"); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{}
	server := &Server{streams: streams, audit: auth, integrations: integrations, secrets: secrets, youtubeLive: youtubeLive}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 1 || result["failed"] != 0 {
		t.Fatalf("unexpected auto retry result: %#v", result)
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-auto-retry" || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("auto retry did not complete YouTube runtime with OAuth credentials: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after auto retry, got err=%v", err)
	}
	if _, err := secrets.GetSecretValue(t.Context(), "youtube_stream_key_runtime_auto_retry"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("runtime stream key secret should be cleared after auto retry, got err=%v", err)
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if !strings.Contains(auditJSON, `"trigger":"auto_retry"`) || strings.Contains(auditJSON, "runtime-youtube-live-api-key") || strings.Contains(auditJSON, "raw-youtube-refresh-token") || strings.Contains(auditJSON, "raw-youtube-client-secret") {
		t.Fatalf("auto retry audit missing or leaked secret: %s", auditJSON)
	}
}

func TestAutoRetryBacksOffDueYouTubeLiveAPIFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "auto retry failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       "youtube-output-01",
		OAuthAccountID:      account.ID,
		Mode:                "live_api",
		BroadcastID:         "broadcast-auto-retry-failure",
		LiveStreamID:        "live-stream-auto-retry-failure",
		StreamKeySecretName: "youtube_stream_key_runtime_auto_retry_failure",
		CompleteOnStop:      true,
		CompleteRetryCount:  1,
		CompleteNextRetryAt: time.Now().UTC().Add(-time.Minute),
		CompleteLastError:   "youtube_live_api_complete_failed",
	}); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{completeErr: errors.New("youtube transition failed")}
	server := &Server{streams: streams, audit: auth, integrations: integrations, secrets: store.NewMemorySecretStore(), youtubeLive: youtubeLive}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 0 || result["failed"] != 1 {
		t.Fatalf("unexpected auto retry failure result: %#v", result)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain after failed auto retry: %v", err)
	}
	if stored.CompleteRetryCount != 2 || stored.CompleteNextRetryAt.IsZero() || !stored.CompleteNextRetryAt.After(time.Now().UTC()) || stored.CompleteLastError != "youtube_live_api_complete_failed" {
		t.Fatalf("failed auto retry should update retry backoff metadata: %#v", stored)
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if !strings.Contains(auditJSON, `"trigger":"auto_retry"`) || !strings.Contains(auditJSON, "youtube_live_api_complete_failed") || strings.Contains(auditJSON, "raw-youtube-refresh-token") || strings.Contains(auditJSON, "raw-youtube-client-secret") {
		t.Fatalf("auto retry failure audit missing or leaked secret: %s", auditJSON)
	}
}

func TestStartStreamCompletesYouTubeLiveAPIWhenDispatchFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dispatch failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube dispatch failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{failStart: true}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-cleanup", LiveStreamID: "live-stream-cleanup"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("expected dispatch failure, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-cleanup" {
		t.Fatalf("youtube live api cleanup was not called correctly: %#v", youtubeLive)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(res.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube dispatch failure leaked secret %q, response=%s audit=%s", raw, res.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after successful start-failure cleanup, got err=%v", err)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("stream should be failed after dispatch failure, got %s", updated.Status)
	}
}

func TestStartStreamKeepsYouTubeRuntimeWhenDispatchFailureCleanupFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dispatch cleanup failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube dispatch cleanup failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{failStart: true}
	youtubeLive := &fakeYouTubeLiveClient{
		prepared:    ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-retained", LiveStreamID: "live-stream-retained"},
		completeErr: errors.New("youtube transition failed"),
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("expected dispatch failure, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.completeCalls != 1 {
		t.Fatalf("youtube cleanup should be attempted after dispatch failure: %#v", youtubeLive)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(res.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube cleanup failure leaked secret %q, response=%s audit=%s", raw, res.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain for cleanup retry after dispatch failure cleanup error: %v", err)
	}
	if stored.BroadcastID != "broadcast-retained" || stored.OAuthAccountID != account.ID || stored.YouTubeOutput != youtube.ID {
		t.Fatalf("unexpected retained youtube runtime: %#v", stored)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("stream should be failed after dispatch failure, got %s", updated.Status)
	}
}

func TestStartStreamRejectsYouTubeLiveAPIWithoutOAuthAccount(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": "oauth-account-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "youtube_oauth_account_unavailable") {
		t.Fatalf("expected youtube oauth account unavailable conflict, status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called when youtube oauth account is unavailable")
	}
}

func TestStartStreamDoesNotPrepareYouTubeLiveAPIWhenAssignmentsAreMissing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "missing assignment youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-must-not-exist"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "missing_stream_assignments") {
		t.Fatalf("expected missing assignments, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("youtube live api prepare must not run before assignment checks: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should not be saved when assignment checks fail, got err=%v", err)
	}
}

func TestStartStreamDoesNotPrepareYouTubeLiveAPIWhenArchiveReadinessFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "not ready oauth archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube archive failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "invalid-service-account-archive", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-must-not-exist"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "drive_oauth_account_unavailable") {
		t.Fatalf("expected archive profile invalid config, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("youtube live api prepare must not run before archive readiness checks: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should not be saved when archive readiness fails, got err=%v", err)
	}
}

func TestStartStreamResolvesArchiveDriveDestinationForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("drive folder ID leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.ArchiveConfig["folder_id"] == "raw-drive-folder-id" {
		t.Fatalf("archive config leaked raw folder ID to dispatch: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	secretName, _ := dispatcher.startRequest.ArchiveConfig["folder_id_secret_name"].(string)
	if secretName != driveDestinationFolderIDSecretName(destination.ID) || dispatcher.startRequest.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("archive config did not include scoped secret reference: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	if dispatcher.startRequest.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || dispatcher.startRequest.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("archive config did not include OAuth secret references: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	for _, leaked := range []string{"service_account_json", "service_account_credentials_secret_name", "client_secret", "refresh_token", "folder_id"} {
		if _, ok := dispatcher.startRequest.ArchiveConfig[leaked]; ok {
			t.Fatalf("archive config leaked raw or unsupported secret field %q: %#v", leaked, dispatcher.startRequest.ArchiveConfig)
		}
	}
}

func TestStartStreamResolvesOAuthDriveDestinationForDispatchWithoutResponseLeak(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-google-refresh-token", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("raw OAuth/Drive secret leaked in response: %s", res.Body.String())
		}
	}
	cfg := dispatcher.startRequest.ArchiveConfig
	if cfg["auth_mode"] != "oauth2" || cfg["client_secret"] == "raw-google-client-secret" || cfg["refresh_token"] == "raw-google-refresh-token" || cfg["folder_id"] == "raw-oauth-drive-folder-id" {
		t.Fatalf("OAuth archive config leaked raw secret values to dispatch: %#v", cfg)
	}
	if cfg["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("OAuth archive config did not include scoped secret references: %#v", cfg)
	}
}

func TestStreamStartRequiresRequiredServiceAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "encoder_recorder") || hasString(body.MissingServiceTypes, "worker") {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on missing assignment: %#v", unchanged)
	}
}

func TestStreamStartChecksReadinessBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code   string                       `json:"code"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "stream_start_not_ready" || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher.fakeServiceDispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness failure: %#v", unchanged)
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "streams.start" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected readiness failure audit event, got %#v", events)
	}
}

func TestStreamStartReadinessEndpointReportsMissingAssignmentsWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready               bool     `json:"ready"`
		MissingServiceTypes []string `json:"missing_service_types"`
		AssignedCount       int      `json:"assigned_service_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "encoder_recorder") || body.AssignedCount != 1 {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness check: %#v", unchanged)
	}
}

func TestStreamStartReadinessEndpointReportsServerReadinessIssues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness endpoint discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","encoder_input_url":"srt://source.example.com:9000"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher.fakeServiceDispatcher)
	}
}

func TestStreamStartReadinessEndpointReportsMissingYouTubeStreamKeyWithoutReadingSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "youtube readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "missing-key-output", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://youtube.example.com/live2",
		"stream_key_secret_name": "youtube_stream_key_missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := &trackingSecretStore{}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "youtube_stream_key_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if secrets.getCalls != 0 {
		t.Fatalf("readiness must not read raw youtube stream key, calls=%d", secrets.getCalls)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher)
	}
	if strings.Contains(res.Body.String(), "<RAW_DISCORD_TOKEN>") || strings.Contains(res.Body.String(), "runtime-secret-stream-key") {
		t.Fatalf("readiness response leaked a raw secret: %s", res.Body.String())
	}
}

func TestStreamStartReadinessEndpointReportsYouTubeLiveAPIAccountIssueWithoutPrepare(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "youtube live readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube live readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": "missing-oauth-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-01"}}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(store.NewMemoryIntegrationStore()), WithYouTubeLiveClient(youtubeLive), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "youtube_oauth_account_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("readiness must not call YouTube Live API prepare, calls=%d", youtubeLive.prepareCalls)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher)
	}
	if strings.Contains(res.Body.String(), "runtime-youtube-live-api-key") {
		t.Fatalf("readiness response leaked a runtime secret: %s", res.Body.String())
	}
}

func TestStreamStartReadinessEndpointReportsMissingDriveDestination(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "missing-destination-archive", map[string]any{
		"drive_destination_id": "missing-drive-destination",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(store.NewMemoryIntegrationStore()), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_destination_not_found" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher)
	}
}

func TestStreamStartReadinessEndpointReportsOAuthDriveAccountIssueWithoutRawSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Readiness",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account without token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth archive readiness destination",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-readiness", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_oauth_account_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher)
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("readiness response leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
}

func TestStreamStopRequiresRequiredServiceAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "worker") || hasString(body.MissingServiceTypes, "encoder_recorder") {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.stopCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "live" {
		t.Fatalf("stream status changed on missing stop assignment: %#v", unchanged)
	}
}

func TestStreamStartRejectsActiveStatusWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "already live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_status_not_startable") {
		t.Fatalf("expected start state conflict, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("start must not be dispatched for an active stream: %#v", dispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "live" {
		t.Fatalf("active stream status changed: %#v", unchanged)
	}
	assertAuditFailureReason(t, auth.AuditEvents(), "streams.start", stream.ID, "stream_status_not_startable")
}

func TestStreamStopRejectsInactiveStatusWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "not started")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_status_not_stoppable") {
		t.Fatalf("expected stop state conflict, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopCalls != 0 {
		t.Fatalf("stop must not be dispatched for an inactive stream: %#v", dispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("inactive stream status changed: %#v", unchanged)
	}
	assertAuditFailureReason(t, auth.AuditEvents(), "streams.stop", stream.ID, "stream_status_not_stoppable")
}

func assertAuditFailureReason(t *testing.T, events []store.AuditEvent, action, resourceID, reason string) {
	t.Helper()
	for _, event := range events {
		if event.Action == action && event.ResourceID == resourceID && event.Result == "failure" && event.Metadata["reason"] == reason {
			return
		}
	}
	t.Fatalf("missing audit failure action=%q resource=%q reason=%q: %#v", action, resourceID, reason, events)
}

func TestStreamDispatchFailureMarksFailed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "dispatch failure discord", "discord_bot-01", "guild-dispatch", "voice-dispatch", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(&fakeServiceDispatcher{failStart: true}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	failed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed stream, got %#v", failed)
	}
}

func TestStreamDispatchFailureDoesNotLeakSecretError(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	dispatcher := &fakeServiceDispatcher{failStart: true, dispatchFailureError: `Post "https://encoder.example.com/jobs/start?token=secret-token": Authorization Bearer secret-token`}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "secret failure discord", "discord_bot-01", "guild-dispatch", "voice-dispatch", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") || strings.Contains(res.Body.String(), "encoder.example.com") || !strings.Contains(res.Body.String(), "service dispatch failed") {
		t.Fatalf("dispatch secret leaked or sanitized error missing: %s", res.Body.String())
	}
	if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "secret-token") {
		t.Fatalf("dispatch secret leaked in audit events: %#v", auth.AuditEvents())
	}
}

func TestRetryUploadDispatchesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 || len(dispatcher.retriedServices) != 1 || dispatcher.retriedServices[0].ServiceID != "enc-01" {
		t.Fatalf("retry dispatcher was not called correctly: %#v", dispatcher)
	}
}

func TestRetryUploadDispatchesStreamArchiveConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive retry stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("drive folder ID leaked in retry response: %s", res.Body.String())
	}
	if dispatcher.retriedArchiveConfig["folder_id"] == "raw-drive-folder-id" {
		t.Fatalf("retry archive config leaked raw folder ID: %#v", dispatcher.retriedArchiveConfig)
	}
	if dispatcher.retriedArchiveConfig["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || dispatcher.retriedArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || dispatcher.retriedArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) || dispatcher.retriedArchiveConfig["shared_drive"] != true {
		t.Fatalf("retry archive config missing scoped secret reference: %#v", dispatcher.retriedArchiveConfig)
	}
	for _, leaked := range []string{"service_account_json", "service_account_credentials_secret_name", "client_secret", "refresh_token", "folder_id"} {
		if _, ok := dispatcher.retriedArchiveConfig[leaked]; ok {
			t.Fatalf("retry archive config leaked raw or unsupported secret field %q: %#v", leaked, dispatcher.retriedArchiveConfig)
		}
	}
}

func TestRetryUploadDispatchesOAuthSharedDriveArchiveConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive retry stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Retry",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive retry account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth retry shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-retry", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-google-refresh-token", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("raw OAuth/Drive secret leaked in retry response: %s", res.Body.String())
		}
	}
	cfg := dispatcher.retriedArchiveConfig
	if cfg["auth_mode"] != "oauth2" || cfg["folder_id"] == "raw-oauth-drive-folder-id" || cfg["client_secret"] == "raw-google-client-secret" || cfg["refresh_token"] == "raw-google-refresh-token" {
		t.Fatalf("OAuth retry archive config leaked raw secret values: %#v", cfg)
	}
	if cfg["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) || cfg["shared_drive"] != true {
		t.Fatalf("OAuth retry archive config missing scoped secret references: %#v", cfg)
	}
}

func TestRetryUploadDispatchFailureDoesNotLeakSecretError(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{failRetry: true, dispatchFailureError: `Post "https://encoder.example.com/streams/package?token=secret-token": Authorization Bearer secret-token`}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") || strings.Contains(res.Body.String(), "encoder.example.com") || !strings.Contains(res.Body.String(), "service dispatch failed") {
		t.Fatalf("dispatch secret leaked or sanitized error missing: %s", res.Body.String())
	}
	if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "secret-token") {
		t.Fatalf("dispatch secret leaked in audit events: %#v", auth.AuditEvents())
	}
}

func TestRetryUploadRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || len(body.MissingServiceTypes) != 1 || body.MissingServiceTypes[0] != "encoder_recorder" {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher)
	}
	logs, err := streams.ListStreamLogs(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if strings.Contains(log.Message, "archive upload retry requested") {
			t.Fatalf("retry log should not be created on missing encoder assignment: %#v", logs)
		}
	}
}

func TestStreamAudioStatusRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/audio-status", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamAudioStatusProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{audioStatus: servicecall.AudioStatusResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/streams/" + stream.ID + "/audio-status",
		StatusCode:  http.StatusOK,
		Success:     true,
		AudioBridgeState: servicecall.AudioBridgeStatus{
			StreamID:         stream.ID,
			BridgeActive:     true,
			PacketsTotal:     2,
			RTPForwarded:     2,
			LastPacketAgeSec: 0,
		},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/audio-status", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.audioStatusCalls != 1 {
		t.Fatalf("audio status dispatcher was not called: %#v", dispatcher)
	}
	if !strings.Contains(res.Body.String(), `"packets_total":2`) || strings.Contains(res.Body.String(), "service-token") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
}

func TestStreamEncoderPreflightRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/encoder-preflight", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamEncoderPreflightProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{encoderPreflight: servicecall.ServicePreflightResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/preflight",
		StatusCode:  http.StatusOK,
		Success:     true,
		Ready:       false,
		CheckedAt:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Checks: []servicecall.ServicePreflightCheck{{
			ID:       "ffmpeg_binary",
			Status:   "ok",
			Severity: "critical",
			Message:  "ffmpeg is available.",
		}, {
			ID:       "youtube_stream_key",
			Status:   "missing",
			Severity: "critical",
			Message:  "YOUTUBE_STREAM_KEY is not configured.",
		}, {
			ID:       "auth_check",
			Status:   "warning",
			Severity: "warning",
			Message:  "Authorization Bearer service-token",
		}},
		Summary: map[string]any{
			"ffmpeg_bin":             "ffmpeg",
			"archive_root":           "/var/lib/autostream/archives",
			"stream_key":             "super-secret-stream-key",
			"google_drive_folder_id": "drive-folder-secret-id",
			"credential_url":         "rtsp://user:password@camera.example.com/live",
			"nested": map[string]any{
				"webhook_url": "https://discord.com/api/webhooks/id/upstream-secret-token",
			},
			"messages": []any{"ok", "Bearer nested-secret-token"},
		},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/encoder-preflight", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.encoderPreflightCalls != 1 {
		t.Fatalf("encoder preflight dispatcher was not called: %#v", dispatcher)
	}
	if !strings.Contains(res.Body.String(), `"id":"youtube_stream_key"`) || !strings.Contains(res.Body.String(), `"ffmpeg_bin":"ffmpeg"`) {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
	for _, raw := range []string{"service-token", "super-secret-stream-key", "drive-folder-secret-id", "password@camera", "upstream-secret-token", "nested-secret-token", "discord.com/api/webhooks"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("encoder preflight leaked upstream secret %q in response: %s", raw, res.Body.String())
		}
	}
}

func TestStreamWorkerEventsRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/worker-events", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamWorkerEventsProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{workerEvents: servicecall.WorkerEventsResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/streams/" + stream.ID + "/worker-events",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events: []servicecall.WorkerEvent{{
			ID:        "event-01",
			StreamID:  stream.ID,
			Type:      "caption.telop",
			Payload:   map[string]any{"text": "縺薙ｓ縺ｫ縺｡縺ｯ"},
			Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		}, {
			ID:       "event-02",
			StreamID: stream.ID,
			Type:     "overlay.custom",
			Payload: map[string]any{
				"text":        "safe",
				"target":      "https://example.com/callback?api_key=upstream-secret",
				"webhook_url": "https://discord.com/api/webhooks/id/upstream-secret-token",
				"nested":      map[string]any{"message": "Bearer upstream-secret-token"},
			},
			Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		}},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/worker-events", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.workerEventsCalls != 1 {
		t.Fatalf("worker events dispatcher was not called: %#v", dispatcher)
	}
	for _, raw := range []string{"service-token", "upstream-secret", "api_key=", "discord.com/api/webhooks", "Bearer"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("worker events leaked upstream secret-like payload %q in response: %s", raw, res.Body.String())
		}
	}
	if !strings.Contains(res.Body.String(), `"type":"caption.telop"`) || !strings.Contains(res.Body.String(), `"text":"safe"`) || !strings.Contains(res.Body.String(), "redacted") {
		t.Fatalf("unexpected response: %s", res.Body.String())
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

func TestSendWorkerTestEventDispatchesAssignedWorkerAndAudits(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/worker-events/test", bytes.NewBufferString(`{"event_type":"caption","text":"hello","speaker_user_id":"user-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.workerEventSendCalls != 1 || dispatcher.workerEventRequest.Text != "hello" {
		t.Fatalf("worker event dispatcher was not called: %#v", dispatcher)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"streams.worker_event_test"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Result != "success" || events[0].ResourceID != stream.ID {
		t.Fatalf("missing audit event: %#v", events)
	}
}

func TestServiceRemediationExecuteDispatchesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat", "observability.ingest", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	obsClient, closeObs := remediationValidationClient(t, obsToken.RawToken, map[string]observability.RemediationDispatchContext{
		"action-01": {ActionID: "action-01", Action: "retry_package_remux", IncidentID: "inc-01", StreamID: stream.ID, Executable: true},
	})
	defer closeObs()
	registerObservabilityNodeWithTokenForTest(t, auth, obsToken, obsClient.BaseURL)
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 || len(dispatcher.retriedServices) != 1 || dispatcher.retriedServices[0].ServiceType != "encoder_recorder" {
		t.Fatalf("expected encoder retry dispatch, got %#v", dispatcher)
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "remediation.execute" || events[len(events)-1].Result != "success" {
		t.Fatalf("expected remediation audit event, got %#v", events)
	}
	if events[len(events)-1].Metadata["action_id"] != "action-01" || events[len(events)-1].Metadata["incident_id"] != "inc-01" {
		t.Fatalf("expected remediation context in audit metadata, got %#v", events[len(events)-1])
	}
	if strings.Contains(res.Body.String(), obsToken.RawToken) {
		t.Fatalf("service token leaked in response: %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "remediation_action_replayed") {
		t.Fatalf("expected replay rejection, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 {
		t.Fatalf("replayed action should not dispatch again: %#v", dispatcher)
	}
}

func TestServiceRemediationExecuteRejectsUnverifiedObservabilityContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat", "observability.ingest", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	obsClient, closeObs := remediationValidationClient(t, obsToken.RawToken, map[string]observability.RemediationDispatchContext{
		"action-01": {ActionID: "action-01", Action: "retry_package_remux", IncidentID: "different-incident", StreamID: stream.ID, Executable: true},
	})
	defer closeObs()
	registerObservabilityNodeWithTokenForTest(t, auth, obsToken, obsClient.BaseURL)
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "remediation_context_not_verified") {
		t.Fatalf("expected context verification failure, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called for unverified context: %#v", dispatcher)
	}
}

func TestServiceRemediationExecuteRequiresActionAndIncidentContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, obsToken, store.ServiceRegistration{ServiceID: "observability-01", ServiceType: "observability", ServiceName: "Observability", PublicURL: "https://observability.example.com"})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	for name, body := range map[string]string{
		"missing_action_id":   `{"action":"retry_package_remux","incident_id":"inc-01","stream_id":"` + stream.ID + `"}`,
		"missing_incident_id": `{"action_id":"action-missing-incident","action":"retry_package_remux","stream_id":"` + stream.ID + `"}`,
		"missing_stream_id":   `{"action_id":"action-missing-stream","action":"retry_package_remux","incident_id":"inc-01"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "remediation_context_required") {
				t.Fatalf("expected remediation context rejection, got %d body = %s", res.Code, res.Body.String())
			}
		})
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called without complete context: %#v", dispatcher)
	}
}

func TestServiceRemediationExecuteRejectsInvalidServiceToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-worker","action":"retry_gdrive_upload","incident_id":"inc-worker","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-observability token, got %d body = %s", res.Code, res.Body.String())
	}

	noScopeToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-noscope","action":"retry_gdrive_upload","incident_id":"inc-noscope","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+noScopeToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing scope, got %d body = %s", res.Code, res.Body.String())
	}

	pendingToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), pendingToken, store.ServiceRegistration{ServiceID: "observability-pending", ServiceType: "observability", ServiceName: "Observability Pending", PublicURL: "https://observability-pending.example.com"}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-pending","action":"retry_gdrive_upload","incident_id":"inc-pending","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+pendingToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "service_token_not_registered") {
		t.Fatalf("expected pending observability token to be rejected, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestServiceRemediationExecuteRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, obsToken, store.ServiceRegistration{ServiceID: "observability-01", ServiceType: "observability", ServiceName: "Observability", PublicURL: "https://observability.example.com"})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-missing-assignment","action":"retry_gdrive_upload","incident_id":"inc-missing-assignment","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher)
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "remediation.execute" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected failed remediation audit event, got %#v", events)
	}
}

func TestAssignServiceEndpointAllowsNonWorkerServices(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/enc-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceType != "encoder_recorder" {
		t.Fatalf("unexpected assignments: %#v", assignments)
	}
}

func TestGenericServiceAssignRequiresServicePermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "restricted stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/enc-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("generic service assign with workers.assign should be forbidden: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAssignServiceEndpointReplacesSameTypeAndMovesService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	streamA, err := streams.CreateStream(t.Context(), "stream a")
	if err != nil {
		t.Fatal(err)
	}
	streamB, err := streams.CreateStream(t.Context(), "stream b")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	registerServiceInstance(t, auth, "enc-02", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	assignServiceForTest(t, handler, cookie, csrf, "enc-01", streamA.ID)
	assignServiceForTest(t, handler, cookie, csrf, "enc-01", streamB.ID)
	assignmentsA, err := auth.ListStreamAssignments(t.Context(), streamA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignmentsA) != 0 {
		t.Fatalf("moved service should be removed from previous stream: %#v", assignmentsA)
	}
	enc01, err := auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if enc01.CurrentStreamID != streamB.ID || enc01.Status != "assigned" {
		t.Fatalf("moved service has wrong state: %#v", enc01)
	}

	assignServiceForTest(t, handler, cookie, csrf, "enc-02", streamB.ID)
	assignmentsB, err := auth.ListStreamAssignments(t.Context(), streamB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignmentsB) != 1 || assignmentsB[0].ServiceID != "enc-02" {
		t.Fatalf("same type replacement did not keep a single assignment: %#v", assignmentsB)
	}
	enc01, err = auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if enc01.CurrentStreamID != "" || enc01.Status == "assigned" {
		t.Fatalf("replaced service should be cleared: %#v", enc01)
	}
}

func TestServiceAssignmentRoleAllowsStandbyWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign", "streams.start", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "standby stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "enc-primary", "encoder_recorder")
	registerServiceInstance(t, auth, "enc-standby", "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "standby discord", "discord-01", "guild-standby", "voice-standby", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	assignServiceForTest(t, handler, cookie, csrf, "discord-01", stream.ID)
	assignServiceForTest(t, handler, cookie, csrf, "worker-01", stream.ID)
	assignServiceForTest(t, handler, cookie, csrf, "enc-primary", stream.ID)
	assignServiceWithRoleForTest(t, handler, cookie, csrf, "enc-standby", stream.ID, "standby")

	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 4 {
		t.Fatalf("expected primary services plus standby encoder, got %#v", assignments)
	}
	var primaryEncoder, standbyEncoder bool
	for _, assignment := range assignments {
		if assignment.ServiceID == "enc-primary" && assignment.AssignmentRole == "primary" {
			primaryEncoder = true
		}
		if assignment.ServiceID == "enc-standby" && assignment.AssignmentRole == "standby" {
			standbyEncoder = true
		}
	}
	if !primaryEncoder || !standbyEncoder {
		t.Fatalf("missing expected encoder assignment roles: %#v", assignments)
	}
	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []store.RegisteredService
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, service := range health {
		roles[service.ServiceID] = service.AssignmentRole
	}
	if roles["discord-01"] != "primary" || roles["enc-primary"] != "primary" || roles["enc-standby"] != "standby" {
		t.Fatalf("service health did not expose assignment roles: %#v", roles)
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","discord_guild_id":"guild-test","discord_voice_channel_id":"voice-test"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected start dispatch, got %#v", dispatcher)
	}
	for _, service := range dispatcher.startedServices {
		if service.ServiceID == "enc-standby" {
			t.Fatalf("standby encoder must not receive start dispatch: %#v", dispatcher.startedServices)
		}
		if service.AssignmentRole != "primary" {
			t.Fatalf("dispatch should only include primary assignments: %#v", dispatcher.startedServices)
		}
	}
}

func TestServiceRuntimeConfigIsScopedToAuthenticatedService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "runtime config stream")
	if err != nil {
		t.Fatal(err)
	}
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	limitedToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	discordOne, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"text_channel_id":       "text-1",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord two", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-2",
		"voice_channel_id": "voice-2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordOne.ID,
		DiscordGuildID:   "guild-stream-override",
		DiscordVoiceID:   "voice-stream-override",
		DiscordTextID:    "text-stream-override",
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	limitedReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	limitedReq.Header.Set("Authorization", "Bearer "+limitedToken.RawToken)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden {
		t.Fatalf("limited runtime config status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-02", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime config status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime config response must not be cached, got %q", got)
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service.ServiceID != "discord-01" {
		t.Fatalf("wrong service in runtime config: %#v", body.Service)
	}
	if len(body.Assignments) != 1 || body.Assignments[0].StreamID != stream.ID || body.Assignments[0].AssignmentRole != "primary" {
		t.Fatalf("unexpected assignments: %#v", body.Assignments)
	}
	discordProfiles := body.Profiles[string(store.ProfileDiscordConfig)]
	if len(discordProfiles) != 1 || discordProfiles[0].Config["service_id"] != "discord-01" {
		t.Fatalf("runtime config leaked or omitted profiles: %#v", body.Profiles)
	}
	if _, ok := discordProfiles[0].Config["bot_token_secret_name"]; !ok {
		t.Fatalf("secret reference should remain visible: %#v", discordProfiles[0].Config)
	}
	if len(body.StreamDiscordConfigs) != 1 {
		t.Fatalf("expected one stream discord config, got %#v", body.StreamDiscordConfigs)
	}
	resolved := body.StreamDiscordConfigs[0]
	if resolved.StreamID != stream.ID || resolved.DiscordConfigID != discordOne.ID || resolved.AssignmentRole != "primary" {
		t.Fatalf("unexpected stream discord config identity: %#v", resolved)
	}
	if resolved.GuildID != "guild-stream-override" || resolved.VoiceChannelID != "voice-stream-override" {
		t.Fatalf("stream overrides were not applied: %#v", resolved)
	}
	if resolved.TextChannelID != "text-stream-override" {
		t.Fatalf("stream text channel override was not applied: %#v", resolved)
	}
	if resolved.AutoStartTrigger != "discord_voice_join" {
		t.Fatalf("stream auto-start trigger was not included: %#v", resolved)
	}
	if resolved.CaptionAudioURL != "https://caption.example.com/audio" {
		t.Fatalf("profile defaults were not preserved for non-overridden fields: %#v", resolved)
	}
	if strings.Contains(res.Body.String(), "guild-2") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord.com/api/webhooks") {
		t.Fatalf("runtime config response leaked another service or raw secret: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"token_id"`) || strings.Contains(res.Body.String(), tokenOne.ID) {
		t.Fatalf("runtime config response leaked service token binding: %s", res.Body.String())
	}
	sanitized := sanitizeRuntimeProfileConfig(map[string]any{"service_id": "discord-01", "nested": map[string]any{"webhook_url": "https://discord.com/api/webhooks/example/token"}})
	nested, ok := sanitized["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested config map: %#v", sanitized)
	}
	if _, ok := nested["webhook_url"]; ok {
		t.Fatalf("raw nested secret-like config should be removed: %#v", sanitized)
	}
}

func TestServiceRuntimeConfigIncludesConfiguredDiscordStreamsWithoutAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	streamA, err := streams.CreateStream(t.Context(), "runtime config waiting stream a")
	if err != nil {
		t.Fatal(err)
	}
	streamB, err := streams.CreateStream(t.Context(), "runtime config waiting stream b")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord assigned by settings", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-default",
		"voice_channel_id":      "voice-default",
		"text_channel_id":       "text-default",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "other discord", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-other",
		"voice_channel_id": "voice-other",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamA.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamB.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		DiscordGuildID:   "guild-stream-b",
		DiscordVoiceID:   "voice-stream-b",
		DiscordTextID:    "text-stream-b",
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assignments) != 0 {
		t.Fatalf("manual assignments should not be required for waiting discord streams: %#v", body.Assignments)
	}
	if len(body.StreamDiscordConfigs) != 2 {
		t.Fatalf("expected two configured stream discord configs, got %#v", body.StreamDiscordConfigs)
	}
	byStream := map[string]serviceRuntimeDiscordStreamConfig{}
	for _, item := range body.StreamDiscordConfigs {
		byStream[item.StreamID] = item
		if item.AssignmentRole != "primary" {
			t.Fatalf("configured discord stream should be presented as primary runtime target: %#v", item)
		}
	}
	if byStream[streamA.ID].GuildID != "guild-default" || byStream[streamA.ID].VoiceChannelID != "voice-default" || byStream[streamA.ID].TextChannelID != "text-default" {
		t.Fatalf("profile defaults were not applied for unassigned stream: %#v", byStream[streamA.ID])
	}
	if byStream[streamB.ID].GuildID != "guild-stream-b" || byStream[streamB.ID].VoiceChannelID != "voice-stream-b" || byStream[streamB.ID].TextChannelID != "text-stream-b" {
		t.Fatalf("stream overrides were not applied for unassigned stream: %#v", byStream[streamB.ID])
	}
	if byStream[streamA.ID].CaptionAudioURL != "https://caption.example.com/audio" {
		t.Fatalf("profile caption URL was not preserved: %#v", byStream[streamA.ID])
	}
	if strings.Contains(res.Body.String(), "guild-other") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord-bot-01") {
		t.Fatalf("runtime config leaked another service or raw secret: %s", res.Body.String())
	}
}

func TestServiceRuntimeConfigIncludesEncoderArchiveConfigWithoutRawSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "encoder runtime archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Runtime",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Runtime Account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Runtime Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "runtime archive", map[string]any{"drive_destination_id": destination.ID, "archive_file_name": "Council Meeting 20260708.mp4", "shared_drive_id": "shared-drive-01", "retention_days": 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-drive-folder-id", "raw-google-client-secret", "raw-google-refresh-token"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("runtime config leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamArchiveConfigs) != 1 {
		t.Fatalf("expected one stream archive config, got %#v", body.StreamArchiveConfigs)
	}
	cfg := body.StreamArchiveConfigs[0]
	if !cfg.Ready || cfg.StreamID != stream.ID || cfg.AssignmentRole != "primary" || cfg.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected stream archive config identity: %#v", cfg)
	}
	if cfg.ArchiveConfig["auth_mode"] != "oauth2" || cfg.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("runtime archive config omitted mode/shared drive: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["archive_file_name"] != "Council Meeting 20260708.mp4" || cfg.ArchiveConfig["shared_drive_id"] != "shared-drive-01" {
		t.Fatalf("runtime archive config omitted file/shared drive id settings: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["retention_days"] != float64(60) {
		t.Fatalf("runtime archive config omitted retention days: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("runtime archive config omitted scoped secret references: %#v", cfg.ArchiveConfig)
	}
	if _, leaked := cfg.ArchiveConfig["folder_id"]; leaked {
		t.Fatalf("runtime archive config exposed raw folder field: %#v", cfg.ArchiveConfig)
	}
}

func TestServiceRuntimeConfigIncludesEncoderYouTubeConfigWithoutRawStreamKey(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "encoder runtime youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-youtube-01", ServiceType: "encoder_recorder", ServiceName: "Encoder YouTube 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-youtube-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "runtime youtube", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
		"stream_key_secret_name": "youtube_stream_key_runtime_config",
		"enable_auto_stop":       true,
		"complete_on_stop":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_runtime_config", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       youtube.ID,
		Mode:                "stream_key",
		RTMPURL:             "rtmps://a.rtmps.youtube.com/live2",
		StreamKeySecretName: "youtube_stream_key_runtime_config",
		CompleteOnStop:      true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-youtube-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-youtube-stream-key") || strings.Contains(res.Body.String(), `"stream_key":`) {
		t.Fatalf("runtime config leaked raw youtube stream key material: %s", res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamYouTubeConfigs) != 1 {
		t.Fatalf("expected one stream youtube config, got %#v", body.StreamYouTubeConfigs)
	}
	cfg := body.StreamYouTubeConfigs[0]
	if !cfg.Ready || cfg.StreamID != stream.ID || cfg.AssignmentRole != "primary" || cfg.YouTubeOutputID != youtube.ID {
		t.Fatalf("unexpected stream youtube config identity: %#v", cfg)
	}
	if cfg.YouTubeConfig["mode"] != "stream_key" || cfg.YouTubeConfig["stream_key_secret_name"] != "youtube_stream_key_runtime_config" {
		t.Fatalf("runtime youtube config omitted non-secret mode/secret reference: %#v", cfg.YouTubeConfig)
	}
	if cfg.YouTubeConfig["rtmp_url"] != "rtmps://a.rtmps.youtube.com/live2" || cfg.YouTubeConfig["complete_on_stop"] != true {
		t.Fatalf("runtime youtube config omitted dispatch fields: %#v", cfg.YouTubeConfig)
	}
	if cfg.ActiveRuntime["rtmp_url"] != "rtmps://a.rtmps.youtube.com/live2" || cfg.ActiveRuntime["stream_key_secret_name"] != "youtube_stream_key_runtime_config" || cfg.ActiveRuntime["complete_on_stop"] != true {
		t.Fatalf("runtime youtube config omitted active runtime safe fields: %#v", cfg.ActiveRuntime)
	}
}

func TestAdminServiceRuntimeConfigPreviewIsSecretSafe(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "runtime preview stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-preview-01", ServiceType: "discord_bot", ServiceName: "Discord Preview 01", PublicURL: "https://discord-preview.example.com", Version: "0.1.0", Capabilities: map[string]any{"runtime_config": true}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-preview-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord preview", map[string]any{
		"service_id":            "discord-preview-01",
		"guild_id":              "guild-preview",
		"voice_channel_id":      "voice-preview",
		"bot_token_secret_name": "discord_bot_token_preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: discordProfile.ID, DiscordGuildID: "guild-preview", DiscordVoiceID: "voice-preview"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/service-health/discord-preview-01/runtime-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin runtime config preview status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("admin runtime config preview response must not be cached, got %q", got)
	}
	responseBody := res.Body.String()
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service.ServiceID != "discord-preview-01" || len(body.Assignments) != 1 {
		t.Fatalf("unexpected preview payload identity: %#v", body)
	}
	if len(body.StreamDiscordConfigs) != 1 || body.StreamDiscordConfigs[0].DiscordConfigID != discordProfile.ID {
		t.Fatalf("runtime preview omitted stream discord config: %#v", body.StreamDiscordConfigs)
	}
	for _, raw := range []string{"discord.com/api/webhooks", token.RawToken, token.ID, `"token_id"`} {
		if strings.Contains(responseBody, raw) {
			t.Fatalf("admin runtime config preview leaked raw secret or token binding %q: %s", raw, responseBody)
		}
	}
	if !strings.Contains(responseBody, "discord_bot_token_preview") {
		t.Fatalf("admin runtime config preview should keep scoped secret references visible: %s", responseBody)
	}
}

func TestExternalE2EConfigExportsControlPanelConfirmation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e stream")
	if err != nil {
		t.Fatal(err)
	}
	driveProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Drive OAuth",
		Enabled:      true,
		ClientID:     "drive-client-id",
		ClientSecret: "raw-drive-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   driveProvider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Account",
		Email:        "drive@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-drive-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveDestination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive Upload",
		AuthMode:       "oauth2",
		OAuthAccountID: driveAccount.ID,
		FolderID:       "0ARealFolderIdShouldNotLeak",
		SharedDrive:    true,
		BasePath:       "AutoStream/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	discordProfile := createDiscordConfigForTest(t, profiles, "external e2e discord", "discord-e2e-primary", "123456789012345678", "234567890123456789", "345678901234567890")
	youtubeOutput, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "private test output", map[string]any{
		"mode":             "live_api_dry_run",
		"oauth_account_id": driveAccount.ID,
		"rtmp_url":         "rtmps://a.rtmps.youtube.com/live2",
		"complete_on_stop": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "external e2e encoder", map[string]any{
		"input_url": "srt://encoder-input.example.com:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "external e2e archive", map[string]any{
		"drive_destination_id": driveDestination.ID,
		"final_container":      "mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		EncoderProfileID: encoderProfile.ID,
		ArchiveProfileID: archiveProfile.ID,
		YouTubeOutputID:  youtubeOutput.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, service := range []struct {
		id          string
		serviceType string
		role        string
	}{
		{id: "discord-e2e-primary", serviceType: "discord_bot", role: "primary"},
		{id: "encoder-e2e-primary", serviceType: "encoder_recorder", role: "primary"},
		{id: "worker-e2e-primary", serviceType: "worker", role: "primary"},
		{id: "encoder-e2e-standby", serviceType: "encoder_recorder", role: "standby"},
		{id: "worker-e2e-standby", serviceType: "worker", role: "standby"},
	} {
		registerServiceInstanceWithCapabilities(t, auth, service.id, service.serviceType, map[string]any{"runtime_config": true})
		if _, err := auth.AssignServiceToStreamWithRole(t.Context(), service.id, stream.ID, "admin", service.role); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("external e2e config response must not be cached, got %q", got)
	}
	responseBody := res.Body.String()
	var body externalE2EConfigResponse
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != 1 || body.StreamID != stream.ID {
		t.Fatalf("unexpected external e2e config identity: %#v", body)
	}
	if body.RuntimeConfig.YouTubeOutputID != youtubeOutput.ID ||
		body.RuntimeConfig.DriveDestinationID != driveDestination.ID ||
		body.RuntimeConfig.DiscordConfigID != discordProfile.ID ||
		body.RuntimeConfig.EncoderProfileID != encoderProfile.ID ||
		body.RuntimeConfig.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected runtime config ids: %#v", body.RuntimeConfig)
	}
	if body.ServiceAssignments.DiscordBotServiceID != "discord-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderPrimaryServiceID != "encoder-e2e-primary" ||
		body.ServiceAssignments.WorkerPrimaryServiceID != "worker-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderStandbyServiceID != "encoder-e2e-standby" ||
		body.ServiceAssignments.WorkerStandbyServiceID != "worker-e2e-standby" {
		t.Fatalf("unexpected service assignments: %#v", body.ServiceAssignments)
	}
	if !body.Confirmations.YouTubeOutputSaved ||
		!body.Confirmations.DriveDestinationSaved ||
		!body.Confirmations.DiscordConfigSaved ||
		!body.Confirmations.PrimaryAssignmentsSaved ||
		!body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("expected all confirmations true: %#v", body.Confirmations)
	}
	if !body.Readiness.Ready ||
		len(body.Readiness.MissingConfirmations) != 0 ||
		len(body.Readiness.MissingRuntimeIDs) != 0 ||
		len(body.Readiness.MissingPrimaryServices) != 0 ||
		len(body.Readiness.MissingRuntimeConfigCapabilities) != 0 {
		t.Fatalf("expected ready secret-safe readiness summary: %#v", body.Readiness)
	}
	for _, raw := range []string{
		"raw-drive-client-secret",
		"raw-drive-refresh-token",
		"0ARealFolderIdShouldNotLeak",
		"123456789012345678",
		"234567890123456789",
		"345678901234567890",
		"rtmps://a.rtmps.youtube.com/live2",
		"client_secret",
		"refresh_token",
		"folder_id",
	} {
		if strings.Contains(responseBody, raw) {
			t.Fatalf("external e2e config leaked secret or provider runtime value %q: %s", raw, responseBody)
		}
	}
}

func TestExternalE2EConfigRequiresStreamsRead(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e forbidden stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without streams.read, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestExternalE2EConfigReportsMissingControlPanelPieces(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e incomplete stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e incomplete config status = %d body = %s", res.Code, res.Body.String())
	}
	var body externalE2EConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.StreamID != stream.ID || body.SchemaVersion != 1 {
		t.Fatalf("unexpected incomplete config identity: %#v", body)
	}
	if body.RuntimeConfig != (externalE2ERuntimeConfig{}) || body.ServiceAssignments != (externalE2EServiceAssignments{}) {
		t.Fatalf("incomplete config should report empty ids: runtime=%#v assignments=%#v", body.RuntimeConfig, body.ServiceAssignments)
	}
	if body.Confirmations.YouTubeOutputSaved ||
		body.Confirmations.DriveDestinationSaved ||
		body.Confirmations.DiscordConfigSaved ||
		body.Confirmations.PrimaryAssignmentsSaved ||
		body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("incomplete config should report false confirmations: %#v", body.Confirmations)
	}
	if body.Readiness.Ready {
		t.Fatalf("incomplete config should not be ready: %#v", body.Readiness)
	}
	for _, expected := range []string{"youtube_output_saved", "drive_destination_saved", "discord_config_saved", "primary_assignments_saved", "runtime_config_distribution_enabled"} {
		if !slices.Contains(body.Readiness.MissingConfirmations, expected) {
			t.Fatalf("missing confirmation %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"youtube_output_id", "drive_destination_id", "discord_config_id", "encoder_profile_id", "archive_profile_id"} {
		if !slices.Contains(body.Readiness.MissingRuntimeIDs, expected) {
			t.Fatalf("missing runtime id %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"discord_bot", "worker", "encoder_recorder"} {
		if !slices.Contains(body.Readiness.MissingPrimaryServices, expected) {
			t.Fatalf("missing primary service %q not reported: %#v", expected, body.Readiness)
		}
	}
}

func TestServiceRuntimeSecretResolveIsScopedToRuntimeProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	configOnlyToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SecretName != "discord_bot_token_profile-01" || body.Value != "Bot <RAW_DISCORD_TOKEN>" || body.ExpiresInSec <= 0 {
		t.Fatalf("unexpected runtime secret response: %#v", body)
	}
	configOnlyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	configOnlyReq.Header.Set("Authorization", "Bearer "+configOnlyToken.RawToken)
	configOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(configOnlyRes, configOnlyReq)
	if configOnlyRes.Code != http.StatusForbidden || !strings.Contains(configOnlyRes.Body.String(), "missing_service_scope") || strings.Contains(configOnlyRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("config-only token must not resolve runtime secret, status = %d body = %s", configOnlyRes.Code, configOnlyRes.Body.String())
	}
	replayReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	replayReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusConflict || !strings.Contains(replayRes.Body.String(), "runtime_secret_lease_active") || strings.Contains(replayRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("active runtime secret lease replay status = %d body = %s", replayRes.Code, replayRes.Body.String())
	}

	crossReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	crossReq.Header.Set("Authorization", "Bearer "+tokenTwo.RawToken)
	crossRes := httptest.NewRecorder()
	handler.ServeHTTP(crossRes, crossReq)
	if crossRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime secret status = %d body = %s", crossRes.Code, crossRes.Body.String())
	}

	unreferencedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_unreferenced"}`))
	unreferencedReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	unreferencedRes := httptest.NewRecorder()
	handler.ServeHTTP(unreferencedRes, unreferencedReq)
	if unreferencedRes.Code != http.StatusForbidden {
		t.Fatalf("unreferenced runtime secret status = %d body = %s", unreferencedRes.Code, unreferencedRes.Body.String())
	}

	cfgReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	cfgReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	cfgRes := httptest.NewRecorder()
	handler.ServeHTTP(cfgRes, cfgReq)
	if cfgRes.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", cfgRes.Code, cfgRes.Body.String())
	}
	if strings.Contains(cfgRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime config leaked raw token: %s", cfgRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresSecureTransportInProduction(t *testing.T) {
	t.Setenv("AUTOSTREAM_ENV", "production")
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")

	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	requestBody := `{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`
	insecureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	insecureReq.RemoteAddr = "198.51.100.10:12345"
	insecureReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	insecureRes := httptest.NewRecorder()
	handler.ServeHTTP(insecureRes, insecureReq)
	if insecureRes.Code != http.StatusForbidden || !strings.Contains(insecureRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("production HTTP runtime secret resolve status = %d body = %s", insecureRes.Code, insecureRes.Body.String())
	}
	if insecureRes.Header().Get("Cache-Control") != "no-store" || strings.Contains(insecureRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("insecure runtime secret rejection must not be cached or leak raw value: headers=%v body=%s", insecureRes.Header(), insecureRes.Body.String())
	}

	spoofedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	spoofedReq.RemoteAddr = "198.51.100.10:12345"
	spoofedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	spoofedReq.Header.Set("X-Forwarded-Proto", "https")
	spoofedRes := httptest.NewRecorder()
	handler.ServeHTTP(spoofedRes, spoofedReq)
	if spoofedRes.Code != http.StatusForbidden || !strings.Contains(spoofedRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("untrusted forwarded proto runtime secret status = %d body = %s", spoofedRes.Code, spoofedRes.Body.String())
	}

	trustedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	trustedReq.RemoteAddr = "10.0.0.10:12345"
	trustedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	trustedReq.Header.Set("X-Forwarded-Proto", "https")
	trustedRes := httptest.NewRecorder()
	handler.ServeHTTP(trustedRes, trustedReq)
	if trustedRes.Code != http.StatusOK {
		t.Fatalf("trusted HTTPS forwarded runtime secret status = %d body = %s", trustedRes.Code, trustedRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(trustedRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected trusted forwarded runtime secret response: %#v", resolved)
	}
}

func TestServiceRuntimeSecretLeaseActiveIsCheckedBeforeSecretValue(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	secrets := &trackingSecretStore{}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithRuntimeSecretLeaseStore(activeRuntimeSecretLeaseStore{}), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "runtime_secret_lease_active") {
		t.Fatalf("active runtime secret lease status = %d body = %s", res.Code, res.Body.String())
	}
	if secrets.getCalls != 0 {
		t.Fatalf("runtime secret value was read before active lease rejection, calls=%d", secrets.getCalls)
	}
	if strings.Contains(res.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime secret replay leaked raw value: %s", res.Body.String())
	}
}

func TestServiceRuntimeSecretResolveReleasesLeaseWhenSecretIsMissing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	missingReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	missingReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound || !strings.Contains(missingRes.Body.String(), "runtime_secret_not_configured") {
		t.Fatalf("missing runtime secret status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}

	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	retryReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("runtime secret retry after configuration status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(retryRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected retry runtime secret response: %#v", body)
	}
}

func TestServiceRuntimeSecretResolveAllowsAssignedArchiveDestinationSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Standby", PublicURL: "https://encoder-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithSecretStore(secrets), WithAuditStore(auth))

	body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime archive secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-drive-folder-id" {
		t.Fatalf("unexpected resolved archive secret: %#v", resolved)
	}

	clientSecretBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthProviderClientSecretSecretName(provider.ID) + `"}`
	clientSecretReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(clientSecretBody))
	clientSecretReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	clientSecretRes := httptest.NewRecorder()
	handler.ServeHTTP(clientSecretRes, clientSecretReq)
	if clientSecretRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth client secret resolve status = %d body = %s", clientSecretRes.Code, clientSecretRes.Body.String())
	}
	var resolvedClientSecret serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(clientSecretRes.Body).Decode(&resolvedClientSecret); err != nil {
		t.Fatal(err)
	}
	if resolvedClientSecret.Value != "raw-google-client-secret" {
		t.Fatalf("unexpected resolved OAuth client secret: %#v", resolvedClientSecret)
	}

	refreshTokenBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthAccountRefreshTokenSecretName(account.ID) + `"}`
	refreshTokenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(refreshTokenBody))
	refreshTokenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	refreshTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(refreshTokenRes, refreshTokenReq)
	if refreshTokenRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth refresh token resolve status = %d body = %s", refreshTokenRes.Code, refreshTokenRes.Body.String())
	}
	var resolvedRefreshToken serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(refreshTokenRes.Body).Decode(&resolvedRefreshToken); err != nil {
		t.Fatal(err)
	}
	if resolvedRefreshToken.Value != "raw-google-refresh-token" {
		t.Fatalf("unexpected resolved OAuth refresh token: %#v", resolvedRefreshToken)
	}

	credentialsReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","stream_id":"`+stream.ID+`","archive_profile_id":"`+archiveProfile.ID+`","secret_name":"google_drive_credentials"}`))
	credentialsReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	credentialsRes := httptest.NewRecorder()
	handler.ServeHTTP(credentialsRes, credentialsReq)
	if credentialsRes.Code != http.StatusForbidden {
		t.Fatalf("service account credential secret must not resolve, status = %d body = %s", credentialsRes.Code, credentialsRes.Body.String())
	}

	standbyBody := `{"service_id":"encoder-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","secret_name":"`+driveDestinationFolderIDSecretName(destination.ID)+`"}`))
	forbiddenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("archive secret without stream/profile context status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresPrimaryAssignmentForGenericStreamSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "generic stream secret")
	if err != nil {
		t.Fatal(err)
	}
	primaryToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-generic-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Primary", PublicURL: "https://encoder-generic-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-generic-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Standby", PublicURL: "https://encoder-generic-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_generic", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "generic output", map[string]any{
		"service_ids":              []any{"encoder-generic-primary", "encoder-generic-standby"},
		"rtmp_url":                 "rtmps://youtube.example.com/live2",
		"stream_key_secret_name":   "youtube_stream_key_generic",
		"enable_auto_start":        true,
		"enable_auto_stop":         true,
		"broadcast_title_template": "{{stream_name}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-generic-primary","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder generic stream secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-youtube-stream-key" {
		t.Fatalf("unexpected generic stream secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-generic-standby","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve generic stream secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-generic-primary","secret_name":"youtube_stream_key_generic"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("generic stream secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRejectsSecretFromWrongProfileKind(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "wrong profile kind secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-wrong-kind", ServiceType: "encoder_recorder", ServiceName: "Encoder Wrong Kind", PublicURL: "https://encoder-wrong-kind.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-wrong-kind", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_wrong_kind", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	overlayProfile, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "bad overlay secret", map[string]any{
		"service_ids":             []any{"encoder-wrong-kind"},
		"stream_key_secret_name":  "youtube_stream_key_wrong_kind",
		"overlay_template_source": "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlayProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	reqBody := `{"service_id":"encoder-wrong-kind","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_wrong_kind"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "runtime_secret_not_allowed") {
		t.Fatalf("wrong profile kind secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-youtube-stream-key") {
		t.Fatalf("wrong profile kind rejection leaked secret: %s", res.Body.String())
	}
}

func TestEncoderRuntimeProfileSecretRequiresSelectedProfileAndPrimaryAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "custom encoder secret")
	if err != nil {
		t.Fatal(err)
	}
	primaryToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-custom-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Primary", PublicURL: "https://encoder-custom-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-custom-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Standby", PublicURL: "https://encoder-custom-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "encoder_runtime_secret_custom", "raw-encoder-runtime-secret"); err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "custom encoder", map[string]any{
		"service_ids":                []any{"encoder-custom-primary", "encoder-custom-standby"},
		"video_bitrate":              "9000k",
		"custom_runtime_secret_name": "encoder_runtime_secret_custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{EncoderProfileID: encoderProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-custom-primary","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder custom profile secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-encoder-runtime-secret" {
		t.Fatalf("unexpected custom encoder secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-custom-standby","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve custom profile secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-custom-primary","secret_name":"encoder_runtime_secret_custom"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}

	unselectedStream, err := streams.CreateStream(t.Context(), "unselected custom encoder secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", unselectedStream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	unselectedBody := `{"service_id":"encoder-custom-primary","stream_id":"` + unselectedStream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	unselectedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(unselectedBody))
	unselectedReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	unselectedRes := httptest.NewRecorder()
	handler.ServeHTTP(unselectedRes, unselectedReq)
	if unselectedRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret for unselected profile must be forbidden, status = %d body = %s", unselectedRes.Code, unselectedRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveAllowsAssignedOAuthArchiveSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-oauth-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Primary", PublicURL: "https://encoder-oauth-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-oauth-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Standby", PublicURL: "https://encoder-oauth-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-oauth-primary", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-oauth-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Upload",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Upload Account",
		Subject:      "google-subject-01",
		Email:        "uploader@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-shared-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-main", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	cases := []struct {
		name       string
		secretName string
		want       string
	}{
		{name: "folder id", secretName: driveDestinationFolderIDSecretName(destination.ID), want: "raw-shared-drive-folder-id"},
		{name: "oauth provider client secret", secretName: oauthProviderClientSecretSecretName(provider.ID), want: "raw-google-client-secret"},
		{name: "oauth account refresh token", secretName: oauthAccountRefreshTokenSecretName(account.ID), want: "raw-google-refresh-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"service_id":"encoder-oauth-primary","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("runtime oauth archive secret resolve status = %d body = %s", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("runtime secret response must not be cached, got %q", got)
			}
			var resolved serviceRuntimeSecretResolveResponse
			if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
				t.Fatal(err)
			}
			if resolved.SecretName != tc.secretName || resolved.Value != tc.want || resolved.ExpiresInSec <= 0 {
				t.Fatalf("unexpected resolved oauth archive secret: %#v", resolved)
			}

			standbyBody := `{"service_id":"encoder-oauth-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
			standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
			standbyRes := httptest.NewRecorder()
			handler.ServeHTTP(standbyRes, standbyReq)
			if standbyRes.Code != http.StatusForbidden {
				t.Fatalf("standby encoder must not resolve oauth archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
			}

			wrongProfileReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-oauth-primary","stream_id":"`+stream.ID+`","archive_profile_id":"different-profile","secret_name":"`+tc.secretName+`"}`))
			wrongProfileReq.Header.Set("Authorization", "Bearer "+token.RawToken)
			wrongProfileRes := httptest.NewRecorder()
			handler.ServeHTTP(wrongProfileRes, wrongProfileReq)
			if wrongProfileRes.Code != http.StatusForbidden {
				t.Fatalf("oauth archive secret for a different archive profile must be forbidden, status = %d body = %s", wrongProfileRes.Code, wrongProfileRes.Body.String())
			}
		})
	}
}

func TestUnassignServiceEndpointClearsAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/services/enc-01/assignment", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unassign status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected no assignments after unassign: %#v", assignments)
	}
	service, err := auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if service.CurrentStreamID != "" || service.Status == "assigned" {
		t.Fatalf("service was not cleared: %#v", service)
	}
	var auditEvents []store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.unassign" {
			auditEvents = append(auditEvents, event)
		}
	}
	if len(auditEvents) != 1 || auditEvents[0].Metadata["previous_stream_id"] != stream.ID {
		t.Fatalf("unassign audit not recorded correctly: %#v", auditEvents)
	}
}

func TestUnassignWorkerRequiresWorkerUnassignPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "assigner"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "unassigner"}, "correct horse battery", []string{"workers.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "worker stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "worker-01", "worker")
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))

	assignerCookie, assignerCSRF := loginForTest(t, handler, "assigner", "correct horse battery")
	forbiddenReq := httptest.NewRequest(http.MethodDelete, "/workers/worker-01/assignment", nil)
	forbiddenReq.AddCookie(assignerCookie)
	forbiddenReq.Header.Set("X-CSRF-Token", assignerCSRF)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("workers.assign-only unassign status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceID != "worker-01" {
		t.Fatalf("assignment changed after forbidden unassign: %#v", assignments)
	}

	unassignerCookie, unassignerCSRF := loginForTest(t, handler, "unassigner", "correct horse battery")
	allowedReq := httptest.NewRequest(http.MethodDelete, "/workers/worker-01/assignment", nil)
	allowedReq.AddCookie(unassignerCookie)
	allowedReq.Header.Set("X-CSRF-Token", unassignerCSRF)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("workers.unassign status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	assignments, err = auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected no assignments after allowed worker unassign: %#v", assignments)
	}
}

func TestAssignWorkerEndpointRejectsMissingStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/workers/worker-01/assign", bytes.NewBufferString(`{"stream_id":"missing-stream"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("assign worker to missing stream status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "stream_not_found" {
		t.Fatalf("unexpected response code: %#v", body)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), "missing-stream")
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("missing stream should not receive assignments: %#v", assignments)
	}
	worker, err := auth.GetService(t.Context(), "worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if worker.CurrentStreamID != "" || worker.Status == "assigned" {
		t.Fatalf("worker state changed after missing stream assignment: %#v", worker)
	}
}

func TestDeleteServiceEndpointRemovesRegistryAssignmentAndRevokesToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.disable", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/services/enc-01", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete service status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "enc-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("service should be removed, err = %v", err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("assignment should be removed: %#v", assignments)
	}
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"enc-01","status":"online"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusUnauthorized {
		t.Fatalf("deleted service token should be revoked, heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	if strings.Contains(healthRes.Body.String(), "enc-01") || strings.Contains(healthRes.Body.String(), token.RawToken) {
		t.Fatalf("deleted service or raw token leaked in health response: %s", healthRes.Body.String())
	}
	var auditEvents []store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.delete" {
			auditEvents = append(auditEvents, event)
		}
	}
	if len(auditEvents) != 1 || auditEvents[0].ResourceID != "enc-01" || auditEvents[0].Metadata["service_type"] != "encoder_recorder" {
		t.Fatalf("delete audit not recorded correctly: %#v", auditEvents)
	}
}

func TestServiceStreamEventsRequireDedicatedScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "event stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))

	limitedToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, limitedToken, store.ServiceRegistration{ServiceID: "worker-limited", ServiceType: "worker", ServiceName: "Worker Limited", PublicURL: "https://worker-limited.example.com"})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-limited", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	reqBody := `{"service_id":"worker-limited","stream_id":"` + stream.ID + `","event_type":"worker.overlay","payload":{"ok":true}}`
	req := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+limitedToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "missing_service_scope") {
		t.Fatalf("limited token should be rejected, status = %d body = %s", res.Code, res.Body.String())
	}

	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "worker.events.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{ServiceID: "worker-ok", ServiceType: "worker", ServiceName: "Worker OK", PublicURL: "https://worker-ok.example.com"})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-ok", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	reqBody = `{"service_id":"worker-ok","stream_id":"` + stream.ID + `","event_type":"worker.overlay","payload":{"ok":true}}`
	req = httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("worker.events.write token should be accepted, status = %d body = %s", res.Code, res.Body.String())
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

func TestClientIPOnlyTrustsForwardedForFromTrustedProxy(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "198.51.100.10:54321"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.99")
	if got := clientIP(untrusted); got != "198.51.100.10" {
		t.Fatalf("untrusted proxy X-Forwarded-For should be ignored, got %q", got)
	}

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.1.2.3:54321"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.99, 10.1.2.3")
	if got := clientIP(trusted); got != "203.0.113.99" {
		t.Fatalf("trusted proxy X-Forwarded-For should be used, got %q", got)
	}
}

func TestClientIPWalksTrustedProxyChainFromRight(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, 203.0.113.66, 10.0.0.20")
	if got := clientIP(req); got != "203.0.113.66" {
		t.Fatalf("expected first untrusted address from right, got %q", got)
	}
}

func TestClientIPRejectsMalformedForwardedChain(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, malformed")
	if got := clientIP(req); got != "10.0.0.10" {
		t.Fatalf("malformed chain must fall back to direct peer, got %q", got)
	}
}

func TestClientIPDoesNotTrustLoopbackImplicitly(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	if got := clientIP(req); got != "127.0.0.1" {
		t.Fatalf("loopback must require explicit trusted proxy configuration, got %q", got)
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

func TestChangePasswordInvalidatesSessionAndAudits(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewBufferString(`{"current_password":"correct horse battery","new_password":"new correct battery"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("change password status = %d body = %s", res.Code, res.Body.String())
	}
	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.AddCookie(cookie)
	meRes := httptest.NewRecorder()
	handler.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusUnauthorized {
		t.Fatalf("old session should be invalidated, status = %d body = %s", meRes.Code, meRes.Body.String())
	}
	_, newCSRF := loginForTest(t, handler, "operator", "new correct battery")
	if newCSRF == "" {
		t.Fatal("expected login with new password")
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "auth.login" {
		t.Fatalf("unexpected audit tail: %#v", events)
	}
	found := false
	for _, event := range events {
		if event.Action == "auth.change_password" && event.Result == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing successful change password audit event: %#v", events)
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

func TestAuditLogsListAndExport(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "auditor"}, "correct horse battery", []string{"audit_logs.read", "audit_logs.export"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "auditor", "correct horse battery")
	listReq := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), "auth.login") {
		t.Fatalf("audit list status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "services.assign", ResourceType: "service", ResourceID: "enc-01", Result: "success", Metadata: map[string]any{"stream_id": "stream-01", "service_type": "encoder_recorder"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "streams.start", ResourceType: "stream", ResourceID: "stream-01", Result: "failure", Metadata: map[string]any{"missing_service_types": []string{"worker"}}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "notification_channels.create", ResourceType: "notification_channel", ResourceID: "chn-01", Result: "success", Metadata: map[string]any{"has_webhook_url": true, "webhook_url": "https://discord.com/api/webhooks/id/raw-secret-token"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "secrets.update", ResourceType: "secret", ResourceID: "DISCORD_BOT_TOKEN", Result: "success", Metadata: map[string]any{"configured": true, "value": "super-raw-discord-token"}}); err != nil {
		t.Fatal(err)
	}
	redactedListReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=notifications", nil)
	redactedListReq.AddCookie(cookie)
	redactedListRes := httptest.NewRecorder()
	handler.ServeHTTP(redactedListRes, redactedListReq)
	if redactedListRes.Code != http.StatusOK || strings.Contains(redactedListRes.Body.String(), "raw-secret-token") || strings.Contains(redactedListRes.Body.String(), "discord.com/api/webhooks") {
		t.Fatalf("audit list leaked metadata secret: status=%d body=%s", redactedListRes.Code, redactedListRes.Body.String())
	}
	filterReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=service_assignment&result=success&q=enc-01", nil)
	filterReq.AddCookie(cookie)
	filterRes := httptest.NewRecorder()
	handler.ServeHTTP(filterRes, filterReq)
	if filterRes.Code != http.StatusOK {
		t.Fatalf("audit filter status = %d body = %s", filterRes.Code, filterRes.Body.String())
	}
	var filtered []store.AuditEvent
	if err := json.NewDecoder(filterRes.Body).Decode(&filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Action != "services.assign" || filtered[0].ResourceID != "enc-01" {
		t.Fatalf("unexpected filtered audit events: %#v", filtered)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Timestamp: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), Action: "streams.start", ResourceType: "stream", ResourceID: "dated-in", Result: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Timestamp: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), Action: "streams.start", ResourceType: "stream", ResourceID: "dated-out", Result: "success"}); err != nil {
		t.Fatal(err)
	}
	dateReq := httptest.NewRequest(http.MethodGet, "/audit-logs?from=2026-07-01&to=2026-07-01&q=dated", nil)
	dateReq.AddCookie(cookie)
	dateRes := httptest.NewRecorder()
	handler.ServeHTTP(dateRes, dateReq)
	if dateRes.Code != http.StatusOK {
		t.Fatalf("audit date filter status = %d body = %s", dateRes.Code, dateRes.Body.String())
	}
	var dated []store.AuditEvent
	if err := json.NewDecoder(dateRes.Body).Decode(&dated); err != nil {
		t.Fatal(err)
	}
	if len(dated) != 1 || dated[0].ResourceID != "dated-in" {
		t.Fatalf("unexpected date-filtered audit events: %#v", dated)
	}
	exportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export", nil)
	exportReq.AddCookie(cookie)
	exportRes := httptest.NewRecorder()
	handler.ServeHTTP(exportRes, exportReq)
	if exportRes.Code != http.StatusOK || !strings.Contains(exportRes.Body.String(), "auth.login") || !strings.Contains(exportRes.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("audit export status = %d headers = %#v body = %s", exportRes.Code, exportRes.Header(), exportRes.Body.String())
	}
	if !strings.Contains(exportRes.Body.String(), "user_agent") {
		t.Fatalf("audit export header missing user_agent: %s", exportRes.Body.String())
	}
	if strings.Contains(exportRes.Body.String(), "raw-secret-token") || strings.Contains(exportRes.Body.String(), "super-raw-discord-token") {
		t.Fatalf("audit export leaked metadata secret: %s", exportRes.Body.String())
	}
	notificationExportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export?action_group=notifications", nil)
	notificationExportReq.AddCookie(cookie)
	notificationExportRes := httptest.NewRecorder()
	handler.ServeHTTP(notificationExportRes, notificationExportReq)
	if notificationExportRes.Code != http.StatusOK || !strings.Contains(notificationExportRes.Body.String(), "notification_channels.create") || strings.Contains(notificationExportRes.Body.String(), "raw-secret-token") {
		t.Fatalf("notification audit export status = %d body = %s", notificationExportRes.Code, notificationExportRes.Body.String())
	}
}

func TestSecuritySettingsCanUpdateSafeValues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.read", "system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":["admin","super_admin","admin"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"password_min_length":14`) || !strings.Contains(res.Body.String(), `"mfa_mode":"totp"`) || !strings.Contains(res.Body.String(), `"mfa_required_roles":["admin","super_admin"]`) || !strings.Contains(res.Body.String(), `"mfa_supported_methods":["totp","passkey"]`) || !strings.Contains(res.Body.String(), `"passkey_status":"available"`) {
		t.Fatalf("update settings status = %d body = %s", res.Code, res.Body.String())
	}
	badReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":8,"password_hash":"argon2id","remember_me_enabled":true,"mfa_mode":"disabled"}`))
	badReq.AddCookie(cookie)
	badReq.Header.Set("X-CSRF-Token", csrf)
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusBadRequest {
		t.Fatalf("bad settings status = %d body = %s", badRes.Code, badRes.Body.String())
	}
	passkeyReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"passkey"}`))
	passkeyReq.AddCookie(cookie)
	passkeyReq.Header.Set("X-CSRF-Token", csrf)
	passkeyRes := httptest.NewRecorder()
	handler.ServeHTTP(passkeyRes, passkeyReq)
	if passkeyRes.Code != http.StatusOK || !strings.Contains(passkeyRes.Body.String(), `"mfa_mode":"passkey"`) {
		t.Fatalf("passkey mode update status = %d body = %s", passkeyRes.Code, passkeyRes.Body.String())
	}
}

func TestSecuritySettingsRejectsDisabledMFAInProduction(t *testing.T) {
	t.Setenv("AUTOSTREAM_ENV", "production")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"disabled"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "production_mfa_required") {
		t.Fatalf("production disabled MFA status = %d body = %s", res.Code, res.Body.String())
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"security.settings.update"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].Result != "failure" || events[len(events)-1].Metadata["reason"] != "production_mfa_required" {
		t.Fatalf("expected production MFA audit failure, got %#v", events)
	}
}

func TestSecuritySettingsProductionRequiresAdminRolesWhenScoped(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingAdminReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":["super_admin"]}`))
	missingAdminReq.AddCookie(cookie)
	missingAdminReq.Header.Set("X-CSRF-Token", csrf)
	missingAdminRes := httptest.NewRecorder()
	handler.ServeHTTP(missingAdminRes, missingAdminReq)
	if missingAdminRes.Code != http.StatusBadRequest || !strings.Contains(missingAdminRes.Body.String(), "production_mfa_required") {
		t.Fatalf("production scoped MFA missing admin status = %d body = %s", missingAdminRes.Code, missingAdminRes.Body.String())
	}

	okReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"passkey","mfa_required_roles":["super_admin","admin"]}`))
	okReq.AddCookie(cookie)
	okReq.Header.Set("X-CSRF-Token", csrf)
	okRes := httptest.NewRecorder()
	handler.ServeHTTP(okRes, okReq)
	if okRes.Code != http.StatusOK || !strings.Contains(okRes.Body.String(), `"mfa_mode":"passkey"`) {
		t.Fatalf("production scoped MFA ok status = %d body = %s", okRes.Code, okRes.Body.String())
	}

	allUsersReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":[]}`))
	allUsersReq.AddCookie(cookie)
	allUsersReq.Header.Set("X-CSRF-Token", csrf)
	allUsersRes := httptest.NewRecorder()
	handler.ServeHTTP(allUsersRes, allUsersReq)
	if allUsersRes.Code != http.StatusOK || !strings.Contains(allUsersRes.Body.String(), `"mfa_mode":"totp"`) {
		t.Fatalf("production all-user MFA status = %d body = %s", allUsersRes.Code, allUsersRes.Body.String())
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

func TestMFARequiredRolesLimitEnforcementToConfiguredRoles(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-01", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "viewer-01", Username: "viewer", Roles: []string{"viewer"}}, "correct horse battery", nil); err != nil {
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
		MFARequiredRoles:         []string{"super_admin", "admin"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))

	viewerReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"viewer","password":"correct horse battery"}`))
	viewerRes := httptest.NewRecorder()
	handler.ServeHTTP(viewerRes, viewerReq)
	if viewerRes.Code != http.StatusOK || strings.Contains(viewerRes.Body.String(), "mfa_required") {
		t.Fatalf("viewer should not require MFA under admin-only policy: %d %s", viewerRes.Code, viewerRes.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	adminRes := httptest.NewRecorder()
	handler.ServeHTTP(adminRes, adminReq)
	if adminRes.Code != http.StatusForbidden || !strings.Contains(adminRes.Body.String(), "mfa_enrollment_required") {
		t.Fatalf("admin should require MFA enrollment under admin-only policy: %d %s", adminRes.Code, adminRes.Body.String())
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

func TestIntegrationRegistryAPIDoesNotReturnRawSecrets(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.read", "integrations.create", "integrations.update", "integrations.delete", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	providerReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(`{"provider_type":"google","name":"Google Login","enabled":true,"client_id":"google-client-id.apps.exampleusercontent.com","client_secret":"raw-client-secret","scopes":["openid","email"],"allowed_domains":["example.com"],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`))
	providerReq.AddCookie(cookie)
	providerReq.Header.Set("X-CSRF-Token", csrf)
	providerRes := httptest.NewRecorder()
	handler.ServeHTTP(providerRes, providerReq)
	if providerRes.Code != http.StatusCreated {
		t.Fatalf("create provider failed: %d %s", providerRes.Code, providerRes.Body.String())
	}
	if strings.Contains(providerRes.Body.String(), "raw-client-secret") || strings.Contains(providerRes.Body.String(), "client_secret\":") {
		t.Fatalf("provider secret leaked: %s", providerRes.Body.String())
	}
	var provider store.OAuthProvider
	if err := json.Unmarshal(providerRes.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	if !provider.ClientSecretConfigured {
		t.Fatalf("expected provider secret configured status: %#v", provider)
	}

	accountReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts", bytes.NewBufferString(`{"provider_id":"`+provider.ID+`","provider_type":"google","account_label":"Archive Account","email":"archive@example.com","scopes":["https://www.googleapis.com/auth/drive.file"],"refresh_token":"raw-refresh-token"}`))
	accountReq.AddCookie(cookie)
	accountReq.Header.Set("X-CSRF-Token", csrf)
	accountRes := httptest.NewRecorder()
	handler.ServeHTTP(accountRes, accountReq)
	if accountRes.Code != http.StatusForbidden || !strings.Contains(accountRes.Body.String(), "manual_oauth_account_create_disabled") {
		t.Fatalf("manual account create should be disabled: %d %s", accountRes.Code, accountRes.Body.String())
	}
	if strings.Contains(accountRes.Body.String(), "raw-refresh-token") || strings.Contains(accountRes.Body.String(), "refresh_token\":") {
		t.Fatalf("manual account create rejection leaked token: %s", accountRes.Body.String())
	}

	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{ProviderID: provider.ID, ProviderType: "google", AccountLabel: "Archive Account", Email: "archive@example.com", Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, RefreshToken: "raw-refresh-token"})
	if err != nil {
		t.Fatal(err)
	}
	if !account.RefreshTokenConfigured || account.TokenFingerprint == "" {
		t.Fatalf("expected token status and fingerprint: %#v", account)
	}

	updateAccountReq := httptest.NewRequest(http.MethodPut, "/integrations/oauth-accounts/"+account.ID, bytes.NewBufferString(`{"account_label":"Archive Account 2","refresh_token":"raw-refresh-token-2"}`))
	updateAccountReq.AddCookie(cookie)
	updateAccountReq.Header.Set("X-CSRF-Token", csrf)
	updateAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(updateAccountRes, updateAccountReq)
	if updateAccountRes.Code != http.StatusForbidden || !strings.Contains(updateAccountRes.Body.String(), "manual_oauth_account_refresh_token_disabled") {
		t.Fatalf("manual account refresh token update should be disabled: %d %s", updateAccountRes.Code, updateAccountRes.Body.String())
	}
	if strings.Contains(updateAccountRes.Body.String(), "raw-refresh-token-2") || strings.Contains(updateAccountRes.Body.String(), `"refresh_token":`) {
		t.Fatalf("manual account update rejection leaked token context: %s", updateAccountRes.Body.String())
	}

	renameAccountReq := httptest.NewRequest(http.MethodPut, "/integrations/oauth-accounts/"+account.ID, bytes.NewBufferString(`{"account_label":"Archive Account 2"}`))
	renameAccountReq.AddCookie(cookie)
	renameAccountReq.Header.Set("X-CSRF-Token", csrf)
	renameAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(renameAccountRes, renameAccountReq)
	if renameAccountRes.Code != http.StatusOK {
		t.Fatalf("account label update failed: %d %s", renameAccountRes.Code, renameAccountRes.Body.String())
	}
	if strings.Contains(renameAccountRes.Body.String(), "raw-refresh-token") || strings.Contains(renameAccountRes.Body.String(), "refresh_token\":") {
		t.Fatalf("account label update leaked token: %s", renameAccountRes.Body.String())
	}
	var renamedAccount store.OAuthAccount
	if err := json.Unmarshal(renameAccountRes.Body.Bytes(), &renamedAccount); err != nil {
		t.Fatal(err)
	}
	if renamedAccount.AccountLabel != "Archive Account 2" || renamedAccount.DisplayName != "Archive Account 2" {
		t.Fatalf("account label update did not expose display name: %#v", renamedAccount)
	}

	driveReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"Shared Drive Archive","auth_mode":"oauth2","oauth_account_id":"`+account.ID+`","folder_id":"raw-drive-folder-id","shared_drive":true,"base_path":"AutoStream"}`))
	driveReq.AddCookie(cookie)
	driveReq.Header.Set("X-CSRF-Token", csrf)
	driveRes := httptest.NewRecorder()
	handler.ServeHTTP(driveRes, driveReq)
	if driveRes.Code != http.StatusCreated {
		t.Fatalf("create drive destination failed: %d %s", driveRes.Code, driveRes.Body.String())
	}
	if strings.Contains(driveRes.Body.String(), "raw-drive-folder-id") || strings.Contains(driveRes.Body.String(), `"folder_id":"`) {
		t.Fatalf("drive folder id leaked: %s", driveRes.Body.String())
	}
	var destination store.DriveDestination
	if err := json.Unmarshal(driveRes.Body.Bytes(), &destination); err != nil {
		t.Fatal(err)
	}
	if !destination.FolderIDConfigured || destination.FolderIDFingerprint == "" || !destination.SharedDrive {
		t.Fatalf("expected drive destination status: %#v", destination)
	}

	serviceAccountDriveReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"Legacy Service Account","auth_mode":"service_account","folder_id":"raw-drive-folder-id","shared_drive":true,"base_path":"AutoStream"}`))
	serviceAccountDriveReq.AddCookie(cookie)
	serviceAccountDriveReq.Header.Set("X-CSRF-Token", csrf)
	serviceAccountDriveRes := httptest.NewRecorder()
	handler.ServeHTTP(serviceAccountDriveRes, serviceAccountDriveReq)
	if serviceAccountDriveRes.Code != http.StatusBadRequest || !strings.Contains(serviceAccountDriveRes.Body.String(), "drive_destination_auth_mode_unsupported") {
		t.Fatalf("service account drive destination should be rejected: %d %s", serviceAccountDriveRes.Code, serviceAccountDriveRes.Body.String())
	}
	if strings.Contains(serviceAccountDriveRes.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("service account drive rejection leaked folder id: %s", serviceAccountDriveRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/archive/destinations", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || strings.Contains(listRes.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("list leaked drive secret or failed: %d %s", listRes.Code, listRes.Body.String())
	}
}

func TestIntegrationRegistryRejectsInvalidConnectedAccountReferences(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	permissions := []string{"integrations.create", "youtube_outputs.create"}
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	manualAccountReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts", bytes.NewBufferString(`{"provider_id":"missing-provider","provider_type":"google","account_label":"Missing Provider","scopes":["https://www.googleapis.com/auth/drive.file"],"refresh_token":"raw-refresh-token"}`))
	manualAccountReq.AddCookie(cookie)
	manualAccountReq.Header.Set("X-CSRF-Token", csrf)
	manualAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(manualAccountRes, manualAccountReq)
	if manualAccountRes.Code != http.StatusForbidden || !strings.Contains(manualAccountRes.Body.String(), "manual_oauth_account_create_disabled") {
		t.Fatalf("manual account create status = %d body = %s", manualAccountRes.Code, manualAccountRes.Body.String())
	}
	if strings.Contains(manualAccountRes.Body.String(), "raw-refresh-token") || strings.Contains(manualAccountRes.Body.String(), `"refresh_token":`) {
		t.Fatalf("manual account create response leaked refresh token: %s", manualAccountRes.Body.String())
	}

	driveProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-drive-client-id",
		ClientSecret: "raw-google-drive-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   driveProvider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-drive-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeWithDriveReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"bad-youtube-live","mode":"live_api","rtmp_url":"rtmps://example.youtube.com/live2","oauth_account_id":"`+driveAccount.ID+`"}`))
	youtubeWithDriveReq.AddCookie(cookie)
	youtubeWithDriveReq.Header.Set("X-CSRF-Token", csrf)
	youtubeWithDriveRes := httptest.NewRecorder()
	handler.ServeHTTP(youtubeWithDriveRes, youtubeWithDriveReq)
	if youtubeWithDriveRes.Code != http.StatusBadRequest || !strings.Contains(youtubeWithDriveRes.Body.String(), "youtube_output_youtube_scope_required") {
		t.Fatalf("youtube with drive account status = %d body = %s", youtubeWithDriveRes.Code, youtubeWithDriveRes.Body.String())
	}

	youtubeProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "google-youtube-client-id",
		ClientSecret: "raw-google-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   youtubeProvider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube Account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-youtube-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveWithYouTubeReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"bad-drive","auth_mode":"oauth2","oauth_account_id":"`+youtubeAccount.ID+`","folder_id":"raw-drive-folder-id","shared_drive":true,"base_path":"AutoStream"}`))
	driveWithYouTubeReq.AddCookie(cookie)
	driveWithYouTubeReq.Header.Set("X-CSRF-Token", csrf)
	driveWithYouTubeRes := httptest.NewRecorder()
	handler.ServeHTTP(driveWithYouTubeRes, driveWithYouTubeReq)
	if driveWithYouTubeRes.Code != http.StatusBadRequest || !strings.Contains(driveWithYouTubeRes.Body.String(), "drive_destination_drive_scope_required") {
		t.Fatalf("drive with youtube account status = %d body = %s", driveWithYouTubeRes.Code, driveWithYouTubeRes.Body.String())
	}
	for _, raw := range []string{"raw-github-refresh-token", "raw-drive-refresh-token", "raw-youtube-refresh-token", "raw-drive-folder-id"} {
		if strings.Contains(youtubeWithDriveRes.Body.String(), raw) || strings.Contains(driveWithYouTubeRes.Body.String(), raw) {
			t.Fatalf("validation response leaked raw secret %q", raw)
		}
	}
}

func TestOAuthProviderDeleteRejectsConnectedAccountReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Connected Accounts",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Archive Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-providers/"+provider.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusConflict || !strings.Contains(deleteRes.Body.String(), "oauth_provider_in_use") {
		t.Fatalf("expected provider in-use conflict, status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if strings.Contains(deleteRes.Body.String(), "raw-refresh-token") || strings.Contains(deleteRes.Body.String(), "raw-google-client-secret") {
		t.Fatalf("delete conflict leaked secret material: %s", deleteRes.Body.String())
	}
	if _, err := integrations.GetOAuthProvider(t.Context(), provider.ID); err != nil {
		t.Fatalf("provider was deleted despite connected account reference: %v", err)
	}

	if err := integrations.DeleteOAuthAccount(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-providers/"+provider.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("provider delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
}

func TestOAuthAccountDeleteRejectsDriveYouTubeAndRuntimeReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "live api stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	profiles := store.NewMemoryProfileStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Live API",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Live API Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "Live API Output", map[string]any{
		"mode":             "live_api",
		"rtmp_url":         "rtmps://a.rtmp.youtube.com/live2",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  output.ID,
		OAuthAccountID: account.ID,
		Mode:           "live_api",
		BroadcastID:    "broadcast-01",
		LiveStreamID:   "live-stream-01",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-accounts/"+account.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	body := deleteRes.Body.String()
	if deleteRes.Code != http.StatusConflict || !strings.Contains(body, "oauth_account_in_use") {
		t.Fatalf("expected account in-use conflict, status=%d body=%s", deleteRes.Code, body)
	}
	for _, expected := range []string{"drive_destinations", "youtube_outputs", "stream_youtube_runtimes"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("delete conflict missing reference count %q: %s", expected, body)
		}
	}
	for _, raw := range []string{"raw-refresh-token", "raw-google-client-secret", "raw-drive-folder-id"} {
		if strings.Contains(body, raw) {
			t.Fatalf("delete conflict leaked secret material %q: %s", raw, body)
		}
	}
	if _, err := integrations.GetOAuthAccount(t.Context(), account.ID); err != nil {
		t.Fatalf("account was deleted despite references: %v", err)
	}

	if err := integrations.DeleteDriveDestination(t.Context(), destination.ID); err != nil {
		t.Fatal(err)
	}
	if err := profiles.DeleteProfile(t.Context(), store.ProfileYouTubeOutput, output.ID); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStreamYouTubeRuntime(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-accounts/"+account.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("account delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
	if _, err := integrations.GetOAuthAccount(t.Context(), account.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected account deletion, err=%v", err)
	}
}

func TestDriveDestinationDeleteRejectsStreamAndArchiveProfileReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	profiles := store.NewMemoryProfileStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Archive Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
		BasePath:       "AutoStream",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveDriveDestinationID: destination.ID, ArchiveOAuthAccountID: account.ID}); err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "Shared Drive Archive", map[string]any{
		"format":               "mp4",
		"upload_enabled":       true,
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	body := deleteRes.Body.String()
	if deleteRes.Code != http.StatusConflict || !strings.Contains(body, "drive_destination_in_use") {
		t.Fatalf("expected destination in-use conflict, status=%d body=%s", deleteRes.Code, body)
	}
	for _, expected := range []string{"stream_archive_settings", "archive_profiles"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("delete conflict missing reference count %q: %s", expected, body)
		}
	}
	for _, raw := range []string{"raw-refresh-token", "raw-google-client-secret", "raw-drive-folder-id"} {
		if strings.Contains(body, raw) {
			t.Fatalf("delete conflict leaked secret material %q: %s", raw, body)
		}
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); err != nil {
		t.Fatalf("destination was deleted despite references: %v", err)
	}

	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.DeleteProfile(t.Context(), store.ProfileArchive, archiveProfile.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("destination delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected destination deletion, err=%v", err)
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

func TestLoginUsesSecuritySettingsForSessionTTLAndLockout(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    3,
		SessionIdleTimeoutMin:    5,
		SessionAbsoluteLifetimeH: 1,
		MFAMode:                  "disabled",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	before := time.Now().UTC()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	var cookie *http.Cookie
	for _, item := range res.Result().Cookies() {
		if item.Name == sessionCookieName {
			cookie = item
			break
		}
	}
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	session, err := auth.GetSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if session.IdleExpiresAt.Before(before.Add(4*time.Minute)) || session.IdleExpiresAt.After(before.Add(6*time.Minute)) {
		t.Fatalf("idle expiry did not use configured TTL: %s", session.IdleExpiresAt)
	}
	if session.AbsoluteExpiresAt.Before(before.Add(59*time.Minute)) || session.AbsoluteExpiresAt.After(before.Add(61*time.Minute)) {
		t.Fatalf("absolute expiry did not use configured TTL: %s", session.AbsoluteExpiresAt)
	}

	for i := 0; i < 3; i++ {
		failReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"wrong password"}`))
		failReq.RemoteAddr = fmt.Sprintf("198.51.100.%d:5000", i+1)
		failRes := httptest.NewRecorder()
		handler.ServeHTTP(failRes, failReq)
		if failRes.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d body = %s", i+1, failRes.Code, failRes.Body.String())
		}
	}
	locked, err := auth.FindUserByUsername(t.Context(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if locked.Status != "locked" {
		t.Fatalf("expected user to be locked at configured threshold, got %s", locked.Status)
	}
}

func TestLoginFailureAudited(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"wrong password"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	events := auth.AuditEvents()
	if len(events) != 1 || events[0].Action != "auth.login" || events[0].Result != "failure" {
		t.Fatalf("unexpected audit events: %#v", events)
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

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

type captureMailer struct {
	messages  []MailMessage
	settings  []store.AppSettings
	passwords []string
	err       error
}

func (m *captureMailer) Send(_ context.Context, settings store.AppSettings, password string, message MailMessage) error {
	m.settings = append(m.settings, settings)
	m.passwords = append(m.passwords, password)
	m.messages = append(m.messages, message)
	return m.err
}

type fakeTurnstileVerifier struct {
	requests []TurnstileVerifyRequest
	result   TurnstileVerifyResult
	err      error
}

func (f *fakeTurnstileVerifier) Verify(_ context.Context, req TurnstileVerifyRequest) (TurnstileVerifyResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return TurnstileVerifyResult{}, f.err
	}
	return f.result, nil
}

func remediationValidationClient(t *testing.T, expectedToken string, contexts map[string]observability.RemediationDispatchContext) (observability.Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Fatalf("unexpected observability auth header: %q", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/remediation-actions/") || !strings.HasSuffix(r.URL.Path, "/dispatch-context") {
			http.NotFound(w, r)
			return
		}
		actionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/remediation-actions/"), "/dispatch-context")
		context, ok := contexts[actionID]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, context)
	}))
	return observability.Client{BaseURL: server.URL, Token: expectedToken, Timeout: time.Second, HTTP: server.Client()}, server.Close
}

func TestUsersAndRolesAPI(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read", "users.create", "roles.read", "roles.create", "roles.assign", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	roleReq := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBufferString(`{"name":"viewer","permissions":["streams.read"]}`))
	roleReq.AddCookie(cookie)
	roleReq.Header.Set("X-CSRF-Token", csrf)
	roleRes := httptest.NewRecorder()
	handler.ServeHTTP(roleRes, roleReq)
	if roleRes.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body = %s", roleRes.Code, roleRes.Body.String())
	}
	var role store.Role
	if err := json.NewDecoder(roleRes.Body).Decode(&role); err != nil {
		t.Fatal(err)
	}

	userReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"viewer","email":"viewer@example.jp","temporary_password":"correct horse battery","role_ids":["`+role.ID+`"]}`))
	userReq.AddCookie(cookie)
	userReq.Header.Set("X-CSRF-Token", csrf)
	userRes := httptest.NewRecorder()
	handler.ServeHTTP(userRes, userReq)
	if userRes.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body = %s", userRes.Code, userRes.Body.String())
	}
	var user map[string]any
	if err := json.NewDecoder(userRes.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if _, hasHash := user["PasswordHash"]; hasHash {
		t.Fatal("password hash leaked")
	}
}

func TestDeleteUserRemovesUserSessionsAndBlocksSelf(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read", "users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	targetSession, err := auth.CreateSession(t.Context(), "target-id", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/users/target-id", nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete user status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "target-id"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user should not be retrievable, got %v", err)
	}
	if _, err := auth.GetSession(t.Context(), targetSession.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user sessions should be removed, got %v", err)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"users.delete"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ResourceID != "target-id" || events[0].Result != "success" {
		t.Fatalf("delete audit missing: %#v", events)
	}

	selfReq := httptest.NewRequest(http.MethodDelete, "/users/admin-id", nil)
	selfReq.AddCookie(cookie)
	selfReq.Header.Set("X-CSRF-Token", csrf)
	selfRes := httptest.NewRecorder()
	handler.ServeHTTP(selfRes, selfReq)
	if selfRes.Code != http.StatusConflict || !strings.Contains(selfRes.Body.String(), "cannot_delete_self") {
		t.Fatalf("self delete status = %d body = %s", selfRes.Code, selfRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "admin-id"); err != nil {
		t.Fatalf("self delete should not remove admin: %v", err)
	}
}

func TestDeleteUserRejectsSuperAdminAndPermissionEscalation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"operator"}}, "correct horse battery", []string{"users.delete", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "super-id", Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "elevated-id", Username: "elevated"}, "correct horse battery", []string{"streams.read", "system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	superReq := httptest.NewRequest(http.MethodDelete, "/users/super-id", nil)
	superReq.AddCookie(cookie)
	superReq.Header.Set("X-CSRF-Token", csrf)
	superRes := httptest.NewRecorder()
	handler.ServeHTTP(superRes, superReq)
	if superRes.Code != http.StatusForbidden || !strings.Contains(superRes.Body.String(), "cannot_delete_super_admin") {
		t.Fatalf("super_admin delete status = %d body = %s", superRes.Code, superRes.Body.String())
	}

	elevatedReq := httptest.NewRequest(http.MethodDelete, "/users/elevated-id", nil)
	elevatedReq.AddCookie(cookie)
	elevatedReq.Header.Set("X-CSRF-Token", csrf)
	elevatedRes := httptest.NewRecorder()
	handler.ServeHTTP(elevatedRes, elevatedReq)
	if elevatedRes.Code != http.StatusForbidden || !strings.Contains(elevatedRes.Body.String(), "permission_escalation") {
		t.Fatalf("permission escalation delete status = %d body = %s", elevatedRes.Code, elevatedRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "super-id"); err != nil {
		t.Fatalf("super_admin should remain: %v", err)
	}
	if _, err := auth.GetUser(t.Context(), "elevated-id"); err != nil {
		t.Fatalf("elevated user should remain: %v", err)
	}
}

func TestCreateUserCanSendWelcomeEmailWithoutLeakingTemporaryPassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.create"}); err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.example.jp",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "autostream",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(appSettings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"operator","email":"operator@example.jp","temporary_password":"correct horse battery","send_welcome_email":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body = %s", res.Code, res.Body.String())
	}
	var user map[string]any
	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user["email"] != "operator@example.jp" {
		t.Fatalf("created user email not returned: %#v", user)
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("expected one welcome email, got %#v", mailer.messages)
	}
	if mailer.messages[0].To != "operator@example.jp" || !strings.Contains(mailer.messages[0].Subject, "Kome Panel") {
		t.Fatalf("unexpected welcome email: %#v", mailer.messages[0])
	}
	if !strings.Contains(mailer.messages[0].Subject, "アカウント作成のお知らせ") ||
		!strings.Contains(mailer.messages[0].Text, "アカウントを作成しました") ||
		!strings.Contains(mailer.messages[0].Text, "ログインURL: ") ||
		!strings.Contains(mailer.messages[0].Text, "初期パスワードはこのメールには記載していません") ||
		strings.Contains(mailer.messages[0].Text, "Welcome") ||
		strings.Contains(mailer.messages[0].Text, "temporary password") {
		t.Fatalf("welcome email is not localized: %#v", mailer.messages[0])
	}
	if mailer.passwords[0] != "raw-smtp-password" {
		t.Fatalf("SMTP password was not resolved for mailer")
	}
	if strings.Contains(mailer.messages[0].Text, "correct horse battery") || strings.Contains(res.Body.String(), "correct horse battery") {
		t.Fatalf("temporary password leaked in welcome flow: body=%s mail=%s", res.Body.String(), mailer.messages[0].Text)
	}
}

func TestCurrentUserCanUpdateOwnEmail(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "self-id", Username: "operator", Email: "old@example.jp"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.example.jp",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "autostream",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(appSettings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/auth/email", bytes.NewBufferString(`{"email":"new@example.jp"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("request email change status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"confirmation_sent"`) || strings.Contains(res.Body.String(), "new@example.jp") {
		t.Fatalf("email change response is wrong: %s", res.Body.String())
	}
	user, err := auth.GetUser(t.Context(), "self-id")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "old@example.jp" {
		t.Fatalf("email should wait for confirmation: %#v", user)
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("expected one confirmation email, got %#v", mailer.messages)
	}
	if mailer.messages[0].To != "new@example.jp" || strings.Contains(mailer.messages[0].Text, "raw-smtp-password") || strings.Contains(mailer.messages[0].Text, "old@example.jp") {
		t.Fatalf("unexpected confirmation email: %#v", mailer.messages[0])
	}
	if !strings.Contains(mailer.messages[0].Subject, "メールアドレス変更確認") ||
		!strings.Contains(mailer.messages[0].Text, "メールアドレス変更を確認してください") ||
		!strings.Contains(mailer.messages[0].Text, "有効期限: ") ||
		strings.Contains(mailer.messages[0].Text, "Confirm the email address change") {
		t.Fatalf("confirmation email is not localized: %#v", mailer.messages[0])
	}
	if mailer.passwords[0] != "raw-smtp-password" {
		t.Fatalf("SMTP password was not resolved for confirmation email")
	}
	confirmURL := ""
	for _, line := range strings.Split(mailer.messages[0].Text, "\n") {
		if strings.HasPrefix(line, "ワンタイムURL: ") {
			confirmURL = strings.TrimSpace(strings.TrimPrefix(line, "ワンタイムURL: "))
		}
	}
	parsedConfirmURL, err := url.Parse(confirmURL)
	if err != nil {
		t.Fatal(err)
	}
	token := parsedConfirmURL.Query().Get("token")
	if token == "" || parsedConfirmURL.Path != "/auth/email/confirm" {
		t.Fatalf("confirmation URL is wrong: %q", confirmURL)
	}

	confirmReq := httptest.NewRequest(http.MethodPost, "/auth/email/confirm", bytes.NewBufferString(`{"token":"`+token+`"}`))
	confirmRes := httptest.NewRecorder()
	handler.ServeHTTP(confirmRes, confirmReq)
	if confirmRes.Code != http.StatusOK || !strings.Contains(confirmRes.Body.String(), `"status":"email_changed"`) {
		t.Fatalf("confirm email status = %d body = %s", confirmRes.Code, confirmRes.Body.String())
	}
	user, err = auth.GetUser(t.Context(), "self-id")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "new@example.jp" {
		t.Fatalf("email was not persisted after confirmation: %#v", user)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"auth.email.change_request", "auth.email.confirm"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("email change audit missing: %#v", events)
	}
}

func TestCurrentUserEmailUpdateRejectsInvalidEmail(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "self-id", Username: "operator", Email: "old@example.jp"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/auth/email", bytes.NewBufferString("{\"email\":\"new@example.jp\\nBcc: attacker@example.jp\"}"))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_email") {
		t.Fatalf("invalid email status = %d body = %s", res.Code, res.Body.String())
	}
	user, err := auth.GetUser(t.Context(), "self-id")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "old@example.jp" {
		t.Fatalf("invalid email should not persist: %#v", user)
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

func TestUserRoleAssignmentRequiresRolesAssign(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited", Roles: []string{"admin"}}, "correct horse battery", []string{"users.create", "users.update", "roles.read"}); err != nil {
		t.Fatal(err)
	}
	role, err := auth.CreateRole(t.Context(), "operator", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := auth.CreateUser(t.Context(), "existing", "", "correct horse battery", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "limited", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"created","email":"created@example.jp","temporary_password":"correct horse battery","role_ids":["`+role.ID+`"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden {
		t.Fatalf("create with role assignment should be forbidden, got %d body = %s", createRes.Code, createRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/users/"+existing.ID, bytes.NewBufferString(`{"role_ids":["`+role.ID+`"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden {
		t.Fatalf("update with role assignment should be forbidden, got %d body = %s", updateRes.Code, updateRes.Body.String())
	}
}

func TestNonSuperAdminCannotAssignSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "super-id", Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.create", "users.update", "roles.assign"}); err != nil {
		t.Fatal(err)
	}
	target, err := auth.CreateUser(t.Context(), "target", "", "correct horse battery", nil)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var superAdminRole store.Role
	for _, role := range roles {
		if role.Name == "super_admin" {
			superAdminRole = role
			break
		}
	}
	if superAdminRole.ID == "" {
		t.Fatal("expected super_admin role")
	}

	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"blocked","email":"blocked@example.jp","temporary_password":"correct horse battery","role_ids":["`+superAdminRole.ID+`"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden || !strings.Contains(createRes.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	if _, err := auth.FindUserByUsername(t.Context(), "blocked"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("blocked user should not be created, err = %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/users/"+target.ID, bytes.NewBufferString(`{"role_ids":["`+superAdminRole.ID+`"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden || !strings.Contains(updateRes.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("update status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	gotTarget, err := auth.GetUser(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if userHasRoleName(gotTarget, "super_admin") {
		t.Fatal("target received super_admin role")
	}
}

func TestRolePermissionsCannotExceedActorPermissions(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	actorPermissions := []string{"roles.create", "roles.update", "streams.read"}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", actorPermissions); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBufferString(`{"name":"escalated","permissions":["streams.start"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden || !strings.Contains(createRes.Body.String(), "permission_escalation") {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/roles/"+viewerRole.ID, bytes.NewBufferString(`{"name":"viewer","permissions":["streams.read","streams.start"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden || !strings.Contains(updateRes.Body.String(), "permission_escalation") {
		t.Fatalf("update status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	gotRole, err := auth.GetRole(t.Context(), viewerRole.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotRole.Permissions) != 1 || gotRole.Permissions[0] != "streams.read" {
		t.Fatalf("role permissions changed after denied update: %#v", gotRole.Permissions)
	}
}

func TestOnlySuperAdminCanResetSuperAdminPassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.reset_password"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.reset_password"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	operatorCookie, operatorCSRF := loginForTest(t, handler, "operator", "correct horse battery")
	deniedReq := httptest.NewRequest(http.MethodPost, "/users/target-id/reset-password", bytes.NewBufferString(`{"temporary_password":"replacement passphrase"}`))
	deniedReq.AddCookie(operatorCookie)
	deniedReq.Header.Set("X-CSRF-Token", operatorCSRF)
	deniedRes := httptest.NewRecorder()
	handler.ServeHTTP(deniedRes, deniedReq)
	if deniedRes.Code != http.StatusForbidden || !strings.Contains(deniedRes.Body.String(), "cannot_reset_super_admin_password") {
		t.Fatalf("denied status = %d body = %s", deniedRes.Code, deniedRes.Body.String())
	}
	target, err := auth.GetUser(t.Context(), "target-id")
	if err != nil {
		t.Fatal(err)
	}
	if !security.VerifyPassword("correct horse battery", target.PasswordHash) {
		t.Fatal("password changed after denied reset")
	}

	superCookie, superCSRF := loginForTest(t, handler, "super", "correct horse battery")
	allowedReq := httptest.NewRequest(http.MethodPost, "/users/target-id/reset-password", bytes.NewBufferString(`{"temporary_password":"replacement passphrase"}`))
	allowedReq.AddCookie(superCookie)
	allowedReq.Header.Set("X-CSRF-Token", superCSRF)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("allowed status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	target, err = auth.GetUser(t.Context(), "target-id")
	if err != nil {
		t.Fatal(err)
	}
	if !security.VerifyPassword("replacement passphrase", target.PasswordHash) {
		t.Fatal("super_admin reset did not update password")
	}
}

func TestProfileCRUDAPI(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{
		"encoder_profiles.read", "encoder_profiles.create", "encoder_profiles.update", "encoder_profiles.delete",
		"discord_configs.read", "discord_configs.create",
	}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"1080p60","config":{"width":1920,"height":1080,"fps":60,"video_bitrate_kbps":8000}}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create profile status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Profile
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != store.ProfileEncoder || created.Name != "1080p60" {
		t.Fatalf("unexpected profile: %#v", created)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/profiles/encoder/"+created.ID, bytes.NewBufferString(`{"name":"1080p60-high","config":{"width":1920,"height":1080,"fps":60,"video_bitrate_kbps":9000}}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK || !strings.Contains(updateRes.Body.String(), "1080p60-high") {
		t.Fatalf("update profile status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/profiles/encoder", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), "1080p60-high") {
		t.Fatalf("list profile status = %d body = %s", listRes.Code, listRes.Body.String())
	}

	discordReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"main-guild","service_id":"discord-01","audio_forward_enabled":true}`))
	discordReq.AddCookie(cookie)
	discordReq.Header.Set("X-CSRF-Token", csrf)
	discordRes := httptest.NewRecorder()
	handler.ServeHTTP(discordRes, discordReq)
	if discordRes.Code != http.StatusCreated {
		t.Fatalf("create discord config status = %d body = %s", discordRes.Code, discordRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/profiles/encoder/"+created.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete profile status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}

	events := auth.AuditEvents()
	if !strings.Contains(toJSONForTest(t, events), "encoder_profiles.create") || !strings.Contains(toJSONForTest(t, events), "encoder_profiles.delete") {
		t.Fatalf("profile audit events missing: %#v", events)
	}
}

func TestDeleteProfileRejectsStreamReferences(t *testing.T) {
	tests := []struct {
		name       string
		kind       store.ProfileKind
		path       string
		permission string
		settings   func(string) store.StreamSettings
		config     map[string]any
	}{
		{
			name:       "encoder",
			kind:       store.ProfileEncoder,
			path:       "/profiles/encoder/",
			permission: "encoder_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{EncoderProfileID: id} },
			config:     map[string]any{"width": 1920, "height": 1080, "fps": 60},
		},
		{
			name:       "archive",
			kind:       store.ProfileArchive,
			path:       "/profiles/archive/",
			permission: "archive_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{ArchiveProfileID: id} },
			config:     map[string]any{"format": "mp4", "upload_enabled": true},
		},
		{
			name:       "caption",
			kind:       store.ProfileCaption,
			path:       "/profiles/caption/",
			permission: "caption_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{CaptionProfileID: id} },
			config:     map[string]any{"language": "ja-JP", "provider": "manual"},
		},
		{
			name:       "overlay",
			kind:       store.ProfileOverlay,
			path:       "/profiles/overlay/",
			permission: "overlay_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{OverlayProfileID: id} },
			config:     map[string]any{"theme": "public", "watermark_enabled": true},
		},
		{
			name:       "discord config",
			kind:       store.ProfileDiscordConfig,
			path:       "/discord/configs/",
			permission: "discord_configs.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{DiscordConfigID: id} },
			config:     map[string]any{"service_id": "discord-01", "audio_forward_enabled": true},
		},
		{
			name:       "youtube output",
			kind:       store.ProfileYouTubeOutput,
			path:       "/youtube/outputs/",
			permission: "youtube_outputs.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{YouTubeOutputID: id} },
			config:     map[string]any{"mode": "stream_key", "rtmp_url": "rtmps://a.rtmps.youtube.com/live2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{tc.permission}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			profiles := store.NewMemoryProfileStore()
			profile, err := profiles.CreateProfile(t.Context(), tc.kind, tc.name, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := streams.CreateStream(t.Context(), "referencing stream")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, tc.settings(profile.ID)); err != nil {
				t.Fatal(err)
			}

			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
			cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
			req := httptest.NewRequest(http.MethodDelete, tc.path+profile.ID, nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "profile_in_use") || !strings.Contains(res.Body.String(), stream.ID) {
				t.Fatalf("delete referenced profile status = %d body = %s", res.Code, res.Body.String())
			}
			if _, err := profiles.GetProfile(t.Context(), tc.kind, profile.ID); err != nil {
				t.Fatalf("referenced profile was deleted: %v", err)
			}
		})
	}
}

func TestProfileAPIRejectsRawSecretConfigWithAllowedReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"archive_profiles.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/profiles/archive", bytes.NewBufferString(`{"name":"bad archive","config":{"refresh_token":"raw-refresh-token","upload_enabled":true}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusBadRequest || !strings.Contains(body, "profile_secret_reference_required") {
		t.Fatalf("expected raw secret config rejection, status=%d body=%s", res.Code, body)
	}
	var payload struct {
		Allowed []string `json:"allowed_secret_references"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode profile secret rejection response: %v body=%s", err, body)
	}
	for _, expected := range []string{"drive_destination:<id>:folder_id", "oauth_account:<id>:refresh_token"} {
		if !stringSliceContains(payload.Allowed, expected) {
			t.Fatalf("expected allowed reference hint %q in body: %s", expected, body)
		}
	}
	if strings.Contains(body, "raw-refresh-token") {
		t.Fatalf("raw secret value leaked in validation response: %s", body)
	}
}

func TestProfileAPIRejectsDisallowedSecretReferenceForKind(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"encoder_profiles.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"bad encoder","config":{"stream_key_secret_name":"youtube_stream_key","width":1920}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusBadRequest || !strings.Contains(body, "profile_secret_reference_not_allowed") {
		t.Fatalf("expected disallowed secret reference rejection, status=%d body=%s", res.Code, body)
	}
	var payload struct {
		Invalid []string `json:"invalid_secret_references"`
		Allowed []string `json:"allowed_secret_references"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode disallowed secret reference response: %v body=%s", err, body)
	}
	if !stringSliceContains(payload.Invalid, "youtube_stream_key") {
		t.Fatalf("expected invalid secret reference in body: %s", body)
	}
	if !stringSliceContains(payload.Allowed, "encoder_runtime_secret_<name>") {
		t.Fatalf("expected allowed encoder secret reference in body: %s", body)
	}
}

func TestProfileCRUDRejectsRawSecretConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{"youtube_outputs.create", "discord_configs.create"}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube Account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-youtube-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(store.NewMemoryProfileStore()), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	tests := []struct {
		name string
		body string
	}{
		{name: "raw stream key field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","stream_key":"super-secret-stream-key"}}`},
		{name: "camelCase stream key field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","streamKey":"super-secret-stream-key"}}`},
		{name: "mixed separator access token field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","access-Token":"super-secret-stream-key"}}`},
		{name: "raw google drive folder id field", body: `{"name":"bad-archive","config":{"google_drive_folder_id":"drive-folder-secret-id"}}`},
		{name: "raw camelCase drive folder id field", body: `{"name":"bad-archive-camel","config":{"googleDriveFolderId":"drive-folder-secret-id"}}`},
		{name: "credentialed URL value", body: `{"name":"bad-url","config":{"source_url":"rtsp://user:password@camera.example.com/live"}}`},
		{name: "raw webhook URL value", body: `{"name":"bad-webhook","config":{"notification":"https://discord.com/api/webhooks/id/raw-secret-token"}}`},
		{name: "plaintext rtmp youtube output", body: `{"name":"bad-rtmp","mode":"stream_key","rtmp_url":"rtmp://a.rtmp.youtube.com/live2","stream_key":"super-secret-stream-key"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(tc.body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			if strings.Contains(res.Body.String(), "super-secret") || strings.Contains(res.Body.String(), "drive-folder-secret-id") || strings.Contains(res.Body.String(), "raw-secret-token") || strings.Contains(res.Body.String(), "password@") {
				t.Fatalf("raw secret leaked in validation response: %s", res.Body.String())
			}
		})
	}

	allowedReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"good-youtube","mode":"stream_key","rtmp_url":"rtmps://example.youtube.com/live2","stream_key":"super-secret-stream-key"}`))
	allowedReq.AddCookie(cookie)
	allowedReq.Header.Set("X-CSRF-Token", csrf)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusCreated {
		t.Fatalf("allowed structured stream key status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	if strings.Contains(allowedRes.Body.String(), "super-secret-stream-key") || strings.Contains(allowedRes.Body.String(), `"stream_key":"`) {
		t.Fatalf("raw stream key leaked in youtube output response: %s", allowedRes.Body.String())
	}

	allowedCamelReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"good-youtube-live-dry","mode":"live_api_dry_run","rtmp_url":"rtmps://example.youtube.com/live2","oauth_account_id":"`+account.ID+`","privacy_status":"private","latency_preference":"low","enable_auto_start":true,"enable_auto_stop":true}`))
	allowedCamelReq.AddCookie(cookie)
	allowedCamelReq.Header.Set("X-CSRF-Token", csrf)
	allowedCamelRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedCamelRes, allowedCamelReq)
	if allowedCamelRes.Code != http.StatusCreated {
		t.Fatalf("allowed live api dry-run status = %d body = %s", allowedCamelRes.Code, allowedCamelRes.Body.String())
	}

	discordLegacyReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"bad-discord","config":{"guild_id":"guild-01","bot_token":"raw-discord-bot-token"}}`))
	discordLegacyReq.AddCookie(cookie)
	discordLegacyReq.Header.Set("X-CSRF-Token", csrf)
	discordLegacyRes := httptest.NewRecorder()
	handler.ServeHTTP(discordLegacyRes, discordLegacyReq)
	if discordLegacyRes.Code != http.StatusBadRequest {
		t.Fatalf("legacy discord config with raw secret status = %d body = %s", discordLegacyRes.Code, discordLegacyRes.Body.String())
	}
	if strings.Contains(discordLegacyRes.Body.String(), "raw-discord-bot-token") {
		t.Fatalf("raw discord token leaked in validation response: %s", discordLegacyRes.Body.String())
	}

	discordAllowedReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"good-discord","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","text_channel_id":"text-01","bot_token":"raw-discord-bot-token","audio_forward_enabled":true}`))
	discordAllowedReq.AddCookie(cookie)
	discordAllowedReq.Header.Set("X-CSRF-Token", csrf)
	discordAllowedRes := httptest.NewRecorder()
	handler.ServeHTTP(discordAllowedRes, discordAllowedReq)
	if discordAllowedRes.Code != http.StatusCreated {
		t.Fatalf("allowed discord config status = %d body = %s", discordAllowedRes.Code, discordAllowedRes.Body.String())
	}
	if strings.Contains(discordAllowedRes.Body.String(), "raw-discord-bot-token") || strings.Contains(discordAllowedRes.Body.String(), `"bot_token":"`) {
		t.Fatalf("raw discord token leaked in discord config response: %s", discordAllowedRes.Body.String())
	}
	if strings.Contains(discordAllowedRes.Body.String(), "guild-01") || strings.Contains(discordAllowedRes.Body.String(), "voice-01") || strings.Contains(discordAllowedRes.Body.String(), "text-01") {
		t.Fatalf("discord config response should not persist stream channel IDs: %s", discordAllowedRes.Body.String())
	}
}

func TestDiscordConfigRequiresRegisteredDiscordBotService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"discord_configs.create", "discord_configs.update"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"missing-service","service_id":"discord-missing"}`))
	missingReq.AddCookie(cookie)
	missingReq.Header.Set("X-CSRF-Token", csrf)
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusConflict || !strings.Contains(missingRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("missing service status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}

	wrongTypeReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"wrong-type","service_id":"encoder-01"}`))
	wrongTypeReq.AddCookie(cookie)
	wrongTypeReq.Header.Set("X-CSRF-Token", csrf)
	wrongTypeRes := httptest.NewRecorder()
	handler.ServeHTTP(wrongTypeRes, wrongTypeReq)
	if wrongTypeRes.Code != http.StatusConflict || !strings.Contains(wrongTypeRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("wrong service type status = %d body = %s", wrongTypeRes.Code, wrongTypeRes.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"valid","service_id":"discord-01"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created discordConfigResponse
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/discord/configs/"+created.ID, bytes.NewBufferString(`{"name":"invalid-update","service_id":"encoder-01"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusConflict || !strings.Contains(updateRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("update wrong service type status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
}

func TestDiscordConfigStoresReconnectPolicyFields(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"discord_configs.create", "discord_configs.read"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"reconnect","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","reconnect_enabled":false,"audio_forward_enabled":false,"reconnect_max_attempts":7,"reconnect_base_delay":"3s","reconnect_max_delay":"45s"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created discordConfigResponse
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.ReconnectEnabled || !created.AudioForwardEnabled || created.ReconnectMaxAttempts != 7 || created.ReconnectBaseDelay != "3s" || created.ReconnectMaxDelay != "45s" {
		t.Fatalf("unexpected reconnect response: %#v", created)
	}

	profile, err := profiles.GetProfile(t.Context(), store.ProfileDiscordConfig, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["reconnect_max_attempts"] != 7 || profile.Config["reconnect_base_delay"] != "3s" || profile.Config["reconnect_max_delay"] != "45s" {
		t.Fatalf("unexpected stored reconnect config: %#v", profile.Config)
	}
	if profile.Config["reconnect_enabled"] != true || profile.Config["audio_forward_enabled"] != true {
		t.Fatalf("discord transfer flags should be forced enabled: %#v", profile.Config)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"bad-reconnect","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","reconnect_base_delay":"soon"}`))
	badReq.AddCookie(cookie)
	badReq.Header.Set("X-CSRF-Token", csrf)
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusBadRequest || !strings.Contains(badRes.Body.String(), "invalid_discord_config") {
		t.Fatalf("bad duration status = %d body = %s", badRes.Code, badRes.Body.String())
	}
}

func TestProfileCRUDRequiresSpecificPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"encoder_profiles.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"blocked","config":{}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestViewerCannotManageUsers(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer", Roles: []string{"viewer"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"blocked","temporary_password":"correct horse battery"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestUsersUpdateCannotCreateManualOAuthLink(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target", Roles: []string{"super_admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update"}); err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithOAuthLoginStore(oauthStore), WithIntegrationStore(store.NewMemoryIntegrationStore()))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users/target-id/oauth-links", bytes.NewBufferString(`{"provider_id":"provider-01","provider_type":"google","subject":"attacker-subject","email":"attacker@example.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
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

func TestCannotDisableLastSuperAdmin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.disable"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users/admin-id/disable", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateOwnUserRoleAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update", "roles.assign"}); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/users/operator-id", bytes.NewBufferString(`{"role_ids":["`+viewerRole.ID+`"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "cannot_update_own_roles") {
		t.Fatalf("expected cannot_update_own_roles, body = %s", res.Body.String())
	}
}

func TestCannotRemoveLastSuperAdminRoleAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update", "roles.assign", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/users/admin-id", bytes.NewBufferString(`{"role_ids":["`+viewerRole.ID+`"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "last_super_admin") {
		t.Fatalf("expected last_super_admin, body = %s", res.Body.String())
	}
}

func TestCannotDeleteSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"roles.read", "roles.delete"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("expected super_admin role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/roles/"+roles[0].ID, nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"roles.read", "roles.update"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("expected super_admin role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/roles/"+roles[0].ID, bytes.NewBufferString(`{"name":"super_admin","permissions":["roles.read"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateOwnNonSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"operator"}}, "correct horse battery", []string{"roles.read", "roles.update"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var operatorRole store.Role
	for _, role := range roles {
		if role.Name == "operator" {
			operatorRole = role
			break
		}
	}
	if operatorRole.ID == "" {
		t.Fatal("expected operator role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/roles/"+operatorRole.ID, bytes.NewBufferString(`{"name":"operator","permissions":["roles.read","roles.update","users.create"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestServiceTokenRegisterHeartbeatAndRevoke(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.read", "api_tokens.create", "api_tokens.revoke", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register","service.heartbeat"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	if got := createTokenRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("one-time service token response should be no-store, got %q", got)
	}
	var token store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.RawToken == "" || token.TokenHash != "" {
		t.Fatalf("token response leaked hash or missed raw token: %#v", token)
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{"overlay":true,"webhook_url":"https://discord.com/api/webhooks/id/raw-secret-token","google_drive_folder_id":"drive-folder-secret-id","endpoint":"rtsp://user:password@camera.example.com/live","nested":{"access_token":"super-secret-token","drive_folder_id":"nested-folder-secret-id","safe":true}}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if strings.Contains(registerRes.Body.String(), `"token_id"`) || strings.Contains(registerRes.Body.String(), token.ID) {
		t.Fatalf("service registration response leaked token binding id: %s", registerRes.Body.String())
	}
	var registerAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.register" && event.ResourceID == "worker-01" {
			registerAudit = &event
		}
	}
	if registerAudit == nil || registerAudit.Result != "success" || registerAudit.ActorUsername != "worker" || registerAudit.ActorUserID != "service:worker" {
		t.Fatalf("service registration audit missing or unsafe: %#v", auth.AuditEvents())
	}
	if strings.Contains(registerAudit.ActorUserID, token.ID) {
		t.Fatalf("service registration audit leaked token binding id: %#v", registerAudit)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []struct {
		ServiceID       string         `json:"service_id"`
		HealthStatus    string         `json:"health_status"`
		HeartbeatStale  bool           `json:"heartbeat_stale"`
		HeartbeatAgeSec *int64         `json:"heartbeat_age_sec"`
		Metrics         map[string]any `json:"metrics"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].ServiceID != "worker-01" || health[0].HealthStatus != "unconfigured" || !health[0].HeartbeatStale || health[0].HeartbeatAgeSec != nil {
		t.Fatalf("unexpected pre-heartbeat health: %#v", health)
	}
	if strings.Contains(healthRes.Body.String(), "raw-secret-token") || strings.Contains(healthRes.Body.String(), "password@") || strings.Contains(healthRes.Body.String(), "super-secret-token") || strings.Contains(healthRes.Body.String(), "folder-secret-id") || strings.Contains(healthRes.Body.String(), "webhook_url") || strings.Contains(healthRes.Body.String(), "access_token") || strings.Contains(healthRes.Body.String(), "folder_id") || strings.Contains(healthRes.Body.String(), `"token_id"`) || strings.Contains(healthRes.Body.String(), token.ID) {
		t.Fatalf("service capabilities leaked secret-like values: %s", healthRes.Body.String())
	}
	if health[0].Capabilities["overlay"] != true {
		t.Fatalf("safe capability was not preserved: %#v", health[0].Capabilities)
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","metrics":{"discord.audio_forward_active":1,"discord.audio_forwarded_total":4,"last_forward_error":"secret should not persist"}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	if strings.Contains(heartbeatRes.Body.String(), `"token_id"`) || strings.Contains(heartbeatRes.Body.String(), token.ID) {
		t.Fatalf("heartbeat response leaked token binding id: %s", heartbeatRes.Body.String())
	}
	healthReq = httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes = httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health after heartbeat status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	health = nil
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].HealthStatus != "healthy" || health[0].HeartbeatStale || health[0].HeartbeatAgeSec == nil {
		t.Fatalf("unexpected post-heartbeat health: %#v", health)
	}
	if health[0].Metrics["discord.audio_forward_active"] == nil || health[0].Metrics["discord.audio_forwarded_total"] == nil {
		t.Fatalf("heartbeat metrics were not returned: %#v", health[0].Metrics)
	}
	if _, ok := health[0].Metrics["last_forward_error"]; ok || strings.Contains(healthRes.Body.String(), "secret should not persist") || strings.Contains(healthRes.Body.String(), `"token_id"`) || strings.Contains(healthRes.Body.String(), token.ID) {
		t.Fatalf("string heartbeat metric should not be persisted: %s", healthRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), token.RawToken) {
		t.Fatal("raw service token leaked in list response")
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api-tokens/"+token.ID, nil)
	revokeReq.AddCookie(cookie)
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeRes := httptest.NewRecorder()
	handler.ServeHTTP(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", revokeRes.Code, revokeRes.Body.String())
	}

	heartbeatReq = httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes = httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusUnauthorized {
		t.Fatalf("revoked heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
}

func TestCreateServiceTokenCanPrecreateService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register","service.heartbeat"],"service_id":"encoder-01","service_name":"Encoder 01","public_url":"https://encoder.example.com","version":"0.1.0","capabilities":{"rtmps":true,"stream_key":"must-not-persist"}}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.RawToken == "" {
		t.Fatal("expected one-time raw service token")
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []struct {
		ServiceID    string         `json:"service_id"`
		ServiceType  string         `json:"service_type"`
		ServiceName  string         `json:"service_name"`
		Status       string         `json:"status"`
		HealthStatus string         `json:"health_status"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].ServiceID != "encoder-01" || health[0].Status != "pending" || health[0].HealthStatus != "unconfigured" {
		t.Fatalf("unexpected precreated service health: %#v", health)
	}
	if health[0].Capabilities["rtmps"] != true || strings.Contains(healthRes.Body.String(), "must-not-persist") || strings.Contains(healthRes.Body.String(), "stream_key") {
		t.Fatalf("service capabilities leaked secret-like values: %s", healthRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-01","service_type":"encoder_recorder","service_name":"Encoder 01 Live","public_url":"https://encoder-live.example.com","version":"0.1.1","capabilities":{"rtmps":true}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var registered store.RegisteredService
	if err := json.NewDecoder(registerRes.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.Status != "registered" || registered.PublicURL != "https://encoder-live.example.com" {
		t.Fatalf("unexpected registered service: %#v", registered)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register"],"service_id":"encoder-01","service_name":"Duplicate","public_url":"https://duplicate.example.com"}`))
	duplicateReq.AddCookie(cookie)
	duplicateReq.Header.Set("X-CSRF-Token", csrf)
	duplicateRes := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusConflict {
		t.Fatalf("duplicate precreate status = %d body = %s", duplicateRes.Code, duplicateRes.Body.String())
	}
}

func TestRotateServiceTokenRebindsPrecreatedService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "api_tokens.read", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register","service.heartbeat"],"service_id":"encoder-rotate","service_name":"Encoder Rotate","public_url":"https://encoder.example.com","version":"0.1.0"}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	var oldToken store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&oldToken); err != nil {
		t.Fatal(err)
	}
	if oldToken.RawToken == "" {
		t.Fatal("expected one-time raw token")
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/api-tokens/"+oldToken.ID+"/rotate", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusCreated {
		t.Fatalf("rotate token status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	if got := rotateRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("rotated one-time token response should be no-store, got %q", got)
	}
	var newToken store.ServiceToken
	if err := json.NewDecoder(rotateRes.Body).Decode(&newToken); err != nil {
		t.Fatal(err)
	}
	if newToken.ID == oldToken.ID || newToken.RawToken == "" || newToken.RawToken == oldToken.RawToken || newToken.TokenHash != "" {
		t.Fatalf("unexpected rotated token response: old=%#v new=%#v", oldToken, newToken)
	}
	if strings.Contains(rotateRes.Body.String(), oldToken.RawToken) {
		t.Fatal("old raw token leaked in rotate response")
	}

	oldRegisterReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-rotate","service_type":"encoder_recorder","service_name":"Old Token","public_url":"https://old.example.com","version":"0.1.1","capabilities":{}}`))
	oldRegisterReq.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
	oldRegisterRes := httptest.NewRecorder()
	handler.ServeHTTP(oldRegisterRes, oldRegisterReq)
	if oldRegisterRes.Code != http.StatusUnauthorized {
		t.Fatalf("old token register status = %d body = %s", oldRegisterRes.Code, oldRegisterRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-rotate","service_type":"encoder_recorder","service_name":"Encoder Rotate Live","public_url":"https://encoder-live.example.com","version":"0.1.2","capabilities":{"rtmps":true}}`))
	registerReq.Header.Set("Authorization", "Bearer "+newToken.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("new token register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var registered store.RegisteredService
	if err := json.NewDecoder(registerRes.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(registerRes.Body.String(), `"token_id"`) || strings.Contains(registerRes.Body.String(), newToken.ID) {
		t.Fatalf("service registration response leaked rotated token binding id: %s", registerRes.Body.String())
	}
	storedService, err := auth.GetService(t.Context(), "encoder-rotate")
	if err != nil {
		t.Fatal(err)
	}
	if storedService.TokenID != newToken.ID || storedService.Status != "registered" {
		t.Fatalf("service was not rebound to rotated token: %#v", storedService)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), oldToken.RawToken) || strings.Contains(listRes.Body.String(), newToken.RawToken) {
		t.Fatalf("raw token leaked in list response: %s", listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	var oldRevoked, newActive bool
	for _, token := range tokens {
		if token.ID == oldToken.ID && token.RevokedAt != nil {
			oldRevoked = true
		}
		if token.ID == newToken.ID && token.RevokedAt == nil {
			newActive = true
		}
	}
	if !oldRevoked || !newActive {
		t.Fatalf("unexpected token rotation list state: %#v", tokens)
	}
}

func TestRotateServiceTokenRequiresRevokePermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-no-rotate", []string{"service.register"})

	rotateReq := httptest.NewRequest(http.MethodPost, "/api-tokens/"+token.ID+"/rotate", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusForbidden {
		t.Fatalf("rotate without revoke permission status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-no-rotate","service_type":"worker","service_name":"Worker","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("token should remain active after denied rotate, status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
}

func TestCreateServiceTokenPrecreateRequiresRegisterScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.heartbeat"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "service_register_scope_required") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("precreate without register scope should not create token: %#v", tokens)
	}
}

func TestCreateServiceTokenRejectsEmptyScopes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":[]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("empty scope request should not create token: %#v", tokens)
	}
}

func TestCreateNodeRegistrationTokenPrecreatesNode(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read", "service_health.read", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true,"description":"Studio worker node","version":"must-not-persist","capabilities":{"runtime_config":true,"token":"must-redact"}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create node token status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("node registration token response should be no-store, got %q", got)
	}
	var body struct {
		ID                string                  `json:"id"`
		Token             string                  `json:"token"`
		ConfigureToken    string                  `json:"configure_token"`
		RuntimeToken      string                  `json:"runtime_token"`
		NodeType          string                  `json:"node_type"`
		Scopes            []string                `json:"scopes"`
		ConfigureCommand  string                  `json:"configure_command"`
		ConfigurationYAML string                  `json:"configuration_yaml"`
		SystemdUnit       string                  `json:"systemd_unit"`
		Node              store.RegisteredService `json:"node"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" || body.ID == "" || body.Node.ServiceID != "studio-worker-01" || body.NodeType != "worker" {
		t.Fatalf("unexpected node registration response: %#v", body)
	}
	if body.ConfigureToken == "" || body.ConfigureToken != body.Token || body.RuntimeToken == "" {
		t.Fatalf("expected one-time configure token and runtime token: %#v", body)
	}
	if body.Node.PublicURL != "https://worker.example.com:8443" || body.Node.Host != "worker.example.com" || body.Node.Port != 8443 || !body.Node.SSLEnabled {
		t.Fatalf("node endpoint was not built from host/port/ssl: %#v", body.Node)
	}
	if body.Node.Version != "" || body.Node.ReportedVersion != "" || len(body.Node.Capabilities) != 0 || len(body.Node.ReportedCapabilities) != 0 {
		t.Fatalf("manual version/capabilities must not be stored during node creation: %#v", body.Node)
	}
	expectedConfigureCommand := `sudo autostream-worker configure --panel-url "http://example.com" --token ` + strconv.Quote(body.ConfigureToken) + ` --node "studio-worker-01" --config "/etc/autostream-worker/config.yml"`
	if body.ConfigureCommand != expectedConfigureCommand {
		t.Fatalf("missing configure command fields: %s", body.ConfigureCommand)
	}
	legacyNodeBinary := "sudo autostream-" + "node"
	legacyNodePath := "/usr/local/bin/autostream-" + "node"
	if strings.Contains(body.ConfigureCommand, "command -v") || strings.Contains(body.ConfigureCommand, "$bin") || strings.Contains(body.ConfigureCommand, "/usr/local/bin/worker") || strings.Contains(body.ConfigureCommand, legacyNodeBinary) || strings.Contains(body.ConfigureCommand, legacyNodePath) || strings.Contains(body.ConfigureCommand, "config_path=") || body.SystemdUnit != "" {
		t.Fatalf("node registration should not reference a non-existent shared node binary or systemd unit: command=%s unit=%s", body.ConfigureCommand, body.SystemdUnit)
	}
	if !strings.Contains(body.ConfigurationYAML, `ssl_enabled: true`) || !strings.Contains(body.ConfigurationYAML, body.RuntimeToken) || !strings.Contains(body.ConfigurationYAML, `host: "worker.example.com"`) {
		t.Fatalf("configuration yaml missing node agent settings: %s", body.ConfigurationYAML)
	}
	if !strings.Contains(body.ConfigurationYAML, `stream_ingest:`) || !strings.Contains(body.ConfigurationYAML, `signing_key: "test-stream-ingest-signing-key"`) {
		t.Fatalf("configuration yaml missing stream ingest signing key: %s", body.ConfigurationYAML)
	}
	legacyNodeName := "autostream-" + "node"
	if strings.Contains(body.ConfigureCommand, legacyNodeName) || strings.Contains(body.ConfigurationYAML, legacyNodeName) || !strings.Contains(body.ConfigurationYAML, `/var/lib/autostream/worker`) {
		t.Fatalf("node configuration should use service-specific paths: command=%s yaml=%s", body.ConfigureCommand, body.ConfigurationYAML)
	}
	if !stringSliceContains(body.Scopes, "service.register") || !stringSliceContains(body.Scopes, "worker.events.write") || stringSliceContains(body.Scopes, "service.secret.resolve") {
		t.Fatalf("unexpected default node scopes: %#v", body.Scopes)
	}
	if _, ok := body.Node.Capabilities["token"]; ok || strings.Contains(res.Body.String(), "must-redact") || strings.Contains(res.Body.String(), `"token_id"`) {
		t.Fatalf("node response leaked secret-like capability or token binding: %s", res.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK || !strings.Contains(healthRes.Body.String(), "studio-worker-01") {
		t.Fatalf("service health missing precreated node: status=%d body=%s", healthRes.Code, healthRes.Body.String())
	}
	if strings.Contains(healthRes.Body.String(), body.Token) || strings.Contains(healthRes.Body.String(), body.ID) {
		t.Fatalf("service health leaked node registration token material: %s", healthRes.Body.String())
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+body.ConfigureToken+`"}`))
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusOK {
		t.Fatalf("node configure status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		Config struct {
			Auth struct {
				TokenID string `json:"token_id"`
				Token   string `json:"token"`
			} `json:"auth"`
			StreamIngest struct {
				SigningKey string `json:"signing_key"`
			} `json:"stream_ingest"`
		} `json:"config"`
		ConfigYML string `json:"config_yml"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}
	if configureBody.Config.Auth.TokenID == "" || configureBody.Config.Auth.Token == "" || configureBody.Config.Auth.Token == body.RuntimeToken || !strings.Contains(configureBody.ConfigYML, configureBody.Config.Auth.Token) {
		t.Fatalf("configure endpoint did not rotate and return runtime token once: %#v", configureBody)
	}
	if configureBody.Config.StreamIngest.SigningKey != "test-stream-ingest-signing-key" || !strings.Contains(configureBody.ConfigYML, `signing_key: "test-stream-ingest-signing-key"`) {
		t.Fatalf("configure endpoint did not return the stream ingest signing key: %#v", configureBody)
	}
	configuredNode, err := auth.GetService(t.Context(), "studio-worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if configuredNode.Status != "registered" {
		t.Fatalf("configure should move pending node to registered, got %#v", configuredNode)
	}
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+body.ConfigureToken+`"}`))
	reuseRes := httptest.NewRecorder()
	handler.ServeHTTP(reuseRes, reuseReq)
	if reuseRes.Code != http.StatusUnauthorized {
		t.Fatalf("configure token reuse status = %d body = %s", reuseRes.Code, reuseRes.Body.String())
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/heartbeat", bytes.NewBufferString(`{"nodeId":"studio-worker-01","status":"online","version":"1.4.2","capabilities":{"streaming":true,"archive_upload":true},"hostname":"studio-worker-01","os":"linux","arch":"amd64","api":{"host":"worker.example.com","port":8443,"sslEnabled":true},"metrics":{"cpuUsage":12.5,"runningJobs":2}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+configureBody.Config.Auth.Token)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("node heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	var heartbeatService store.RegisteredService
	if err := json.NewDecoder(heartbeatRes.Body).Decode(&heartbeatService); err != nil {
		t.Fatal(err)
	}
	if heartbeatService.ReportedVersion != "1.4.2" || heartbeatService.ReportedOS != "linux" || heartbeatService.ReportedArch != "amd64" || heartbeatService.ReportedCapabilities["streaming"] != true {
		t.Fatalf("node reported fields were not saved: %#v", heartbeatService)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action=nodes.registration_token.create", nil)
	auditReq.AddCookie(cookie)
	auditRes := httptest.NewRecorder()
	handler.ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK {
		t.Fatalf("audit log status = %d body = %s", auditRes.Code, auditRes.Body.String())
	}
	auditBody := auditRes.Body.String()
	if !strings.Contains(auditBody, "nodes.registration_token.create") || !strings.Contains(auditBody, "studio-worker-01") {
		t.Fatalf("audit log missing node registration event: %s", auditBody)
	}
	if strings.Contains(auditBody, body.Token) || strings.Contains(auditBody, body.ID) {
		t.Fatalf("audit log leaked node registration token material: %s", auditBody)
	}
	if strings.Contains(auditBody, `"token_id"`) && !strings.Contains(auditBody, `"\u003credacted\u003e"`) {
		t.Fatalf("audit log token binding was not redacted: %s", auditBody)
	}
}

func TestNodeConfigurationYAMLScopesSigningKeyToOneTimeWorkerEncoderConfigs(t *testing.T) {
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key")
	request := httptest.NewRequest(http.MethodGet, "https://control.example.jp/nodes", nil)
	worker := store.RegisteredService{ServiceID: "worker-01", ServiceName: "Worker 01", ServiceType: "worker", Host: "worker.example.jp", Port: 8443, SSLEnabled: true}

	oneTime := nodeConfigurationYAML(request, worker, "token-id", "runtime-token")
	if !strings.Contains(oneTime, `stream_ingest:`) || !strings.Contains(oneTime, `signing_key: "test-stream-ingest-signing-key"`) {
		t.Fatalf("one-time worker config omitted signing key: %s", oneTime)
	}
	redacted := nodeConfigurationYAML(request, worker, "token-id", "")
	if strings.Contains(redacted, "test-stream-ingest-signing-key") || strings.Contains(redacted, "stream_ingest") {
		t.Fatalf("normal node config leaked signing key: %s", redacted)
	}
	discord := worker
	discord.ServiceType = "discord_bot"
	discordConfig := nodeConfigurationYAML(request, discord, "token-id", "runtime-token")
	if strings.Contains(discordConfig, "test-stream-ingest-signing-key") || strings.Contains(discordConfig, "stream_ingest") {
		t.Fatalf("discord config received an unrelated signing key: %s", discordConfig)
	}
}

func TestNodeAgentConfigurePersistsRuntimeReportBeforeRuntimeTokenSecret(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "")
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "studio-worker-01",
		ServiceType: "worker",
		ServiceName: "Studio Worker 01",
		Host:        "worker.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	configureToken := "ast_cfg_test_runtime_report"
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+configureToken+`","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"studio-worker-01","os":"linux","arch":"amd64"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "store_node_runtime_token_failed") {
		t.Fatalf("configure without encryption key status = %d body = %s", res.Code, res.Body.String())
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "registered" || got.ReportedVersion != "1.4.1" || got.ReportedCommit != "abc1234" || got.ReportedBuildDate != "2026-07-09T00:00:00Z" || got.ReportedHostname != "studio-worker-01" || got.ReportedOS != "linux" || got.ReportedArch != "amd64" {
		t.Fatalf("configure runtime report was not persisted before runtime token storage failure: %#v", got)
	}
	if got.TokenID != token.ID {
		t.Fatalf("runtime token should not rotate before encryption is available: old=%s got=%s", token.ID, got.TokenID)
	}
}

func TestNodeAgentConfigurePersistsRuntimeReportForSupportedNodeTypes(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	for _, serviceType := range []string{"worker", "encoder_recorder", "discord_bot", "observability"} {
		t.Run(serviceType, func(t *testing.T) {
			token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.config.read"})
			if err != nil {
				t.Fatal(err)
			}
			serviceID := strings.ReplaceAll(serviceType, "_", "-") + "-01"
			service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
				ServiceID:   serviceID,
				ServiceType: serviceType,
				ServiceName: serviceType + " Node 01",
				Host:        serviceID + ".example.com",
				Port:        8443,
				SSLEnabled:  true,
				PublicURL:   "https://" + serviceID + ".example.com:8443",
			})
			if err != nil {
				t.Fatal(err)
			}
			configureToken := "ast_cfg_test_" + strings.ReplaceAll(serviceType, "_", "-")
			if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			payload := `{"nodeId":"` + service.ServiceID + `","configureToken":"` + configureToken + `","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"` + service.ServiceID + `","os":"linux","arch":"amd64"}`
			req := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", bytes.NewBufferString(payload))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("configure node status = %d body = %s", res.Code, res.Body.String())
			}
			got, err := auth.GetService(t.Context(), service.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "registered" || got.ReportedVersion != "1.4.1" || got.ReportedCommit != "abc1234" || got.ReportedBuildDate != "2026-07-09T00:00:00Z" || got.ReportedHostname != service.ServiceID || got.ReportedOS != "linux" || got.ReportedArch != "amd64" || got.LastReportedAt == nil {
				t.Fatalf("configure runtime report was not persisted for %s: %#v", serviceType, got)
			}
			if got.ConfigureTokenUsedAt == nil {
				t.Fatalf("configure token was not marked used for %s: %#v", serviceType, got)
			}
		})
	}
}

func TestListNodesForRegistrationDoesNotRequireServiceHealthRead(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "node-admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "node-admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create node status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var createBody struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&createBody); err != nil {
		t.Fatal(err)
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+createBody.ConfigureToken+`","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"studio-worker-01","os":"linux","arch":"amd64"}`))
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusOK {
		t.Fatalf("configure node status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		Config struct {
			Auth struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"config"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}

	nodesBeforeHeartbeatReq := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	nodesBeforeHeartbeatReq.AddCookie(cookie)
	nodesBeforeHeartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(nodesBeforeHeartbeatRes, nodesBeforeHeartbeatReq)
	if nodesBeforeHeartbeatRes.Code != http.StatusOK {
		t.Fatalf("list nodes before heartbeat status = %d body = %s", nodesBeforeHeartbeatRes.Code, nodesBeforeHeartbeatRes.Body.String())
	}
	bodyBeforeHeartbeat := nodesBeforeHeartbeatRes.Body.String()
	for _, want := range []string{`"service_id":"studio-worker-01"`, `"status":"registered"`, `"health_status":"unconfigured"`, `"reported_version":"1.4.1"`, `"reported_commit":"abc1234"`, `"reported_build_date":"2026-07-09T00:00:00Z"`, `"reported_os":"linux"`, `"reported_arch":"amd64"`} {
		if !strings.Contains(bodyBeforeHeartbeat, want) {
			t.Fatalf("node list before heartbeat missing %s: %s", want, bodyBeforeHeartbeat)
		}
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/heartbeat", bytes.NewBufferString(`{"nodeId":"studio-worker-01","status":"online","version":"1.4.2","commit":"def5678","build_date":"2026-07-09T01:00:00Z","capabilities":{"streaming":true},"hostname":"studio-worker-01","os":"linux","arch":"amd64","api":{"host":"worker.example.com","port":8443,"sslEnabled":true},"metrics":{"cpuUsage":12.5,"runningJobs":2}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+configureBody.Config.Auth.Token)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("node heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusForbidden {
		t.Fatalf("service-health should still require service_health.read, got %d body = %s", healthRes.Code, healthRes.Body.String())
	}

	nodesReq := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	nodesReq.AddCookie(cookie)
	nodesRes := httptest.NewRecorder()
	handler.ServeHTTP(nodesRes, nodesReq)
	if nodesRes.Code != http.StatusOK {
		t.Fatalf("list nodes status = %d body = %s", nodesRes.Code, nodesRes.Body.String())
	}
	body := nodesRes.Body.String()
	for _, want := range []string{`"service_id":"studio-worker-01"`, `"health_status":"healthy"`, `"reported_version":"1.4.2"`, `"reported_commit":"def5678"`, `"reported_build_date":"2026-07-09T01:00:00Z"`, `"reported_os":"linux"`, `"reported_arch":"amd64"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("node list missing %s: %s", want, body)
		}
	}
	for _, want := range []string{`"capabilities":{"streaming":true}`, `"reported_capabilities":{"streaming":true}`, `"metrics":{"cpuUsage":12.5,"runningJobs":2}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("node list missing sanitized runtime field %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{`"token_id"`, createBody.ConfigureToken, configureBody.Config.Auth.Token} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("node list leaked %q: %s", forbidden, body)
		}
	}
}

func TestNodeManagementUpdateRotateAndDelete(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "node-admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "services.disable"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "studio-worker-01",
		ServiceType: "worker",
		ServiceName: "Studio Worker 01",
		Host:        "worker.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8443",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "node-admin", "correct horse battery")

	updateReq := httptest.NewRequest(http.MethodPut, "/nodes/studio-worker-01", bytes.NewBufferString(`{"service_name":"Studio Worker Edited","description":"編集済みNode","host":"worker-edited.example.com","port":9443,"ssl_enabled":true}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update node status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	if !strings.Contains(updateRes.Body.String(), `"service_name":"Studio Worker Edited"`) || !strings.Contains(updateRes.Body.String(), `"public_url":"https://worker-edited.example.com:9443"`) {
		t.Fatalf("update response missing edited node fields: %s", updateRes.Body.String())
	}
	updated, err := auth.GetService(t.Context(), "studio-worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ServiceName != "Studio Worker Edited" || updated.Description != "編集済みNode" || updated.Host != "worker-edited.example.com" || updated.Port != 9443 || updated.PublicURL != "https://worker-edited.example.com:9443" {
		t.Fatalf("node metadata was not updated: %#v", updated)
	}

	configReq := httptest.NewRequest(http.MethodGet, "/nodes/studio-worker-01/configuration", nil)
	configReq.AddCookie(cookie)
	configRes := httptest.NewRecorder()
	handler.ServeHTTP(configRes, configReq)
	if configRes.Code != http.StatusOK {
		t.Fatalf("node configuration status = %d body = %s", configRes.Code, configRes.Body.String())
	}
	if !strings.Contains(configRes.Body.String(), `"node_api_url":"https://worker-edited.example.com:9443"`) {
		t.Fatalf("node configuration should use edited endpoint: %s", configRes.Body.String())
	}
	if strings.Contains(configRes.Body.String(), "test-stream-ingest-signing-key") || strings.Contains(configRes.Body.String(), "stream_ingest") {
		t.Fatalf("normal node configuration response leaked one-time signing material: %s", configRes.Body.String())
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/nodes/studio-worker-01/configure-token", nil)
	configureReq.AddCookie(cookie)
	configureReq.Header.Set("X-CSRF-Token", csrf)
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusCreated {
		t.Fatalf("configure token rotate status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		ConfigureToken string `json:"configure_token"`
		Command        string `json:"configure_command"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}
	if configureBody.ConfigureToken == "" || !strings.Contains(configureBody.Command, configureBody.ConfigureToken) {
		t.Fatalf("configure token was not returned once: %#v", configureBody)
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/nodes/studio-worker-01/rotate-token", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusCreated {
		t.Fatalf("runtime token rotate status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	var rotateBody struct {
		RuntimeToken      string `json:"runtime_token"`
		RuntimeTokenID    string `json:"runtime_token_id"`
		ConfigurationYAML string `json:"configuration_yaml"`
	}
	if err := json.NewDecoder(rotateRes.Body).Decode(&rotateBody); err != nil {
		t.Fatal(err)
	}
	if rotateBody.RuntimeToken == "" || rotateBody.RuntimeTokenID == "" || !strings.Contains(rotateBody.ConfigurationYAML, rotateBody.RuntimeToken) {
		t.Fatalf("runtime token was not returned once in config: %#v", rotateBody)
	}
	if !strings.Contains(rotateBody.ConfigurationYAML, `signing_key: "test-stream-ingest-signing-key"`) {
		t.Fatalf("rotated one-time config omitted stream ingest signing key: %#v", rotateBody)
	}
	if rotateBody.RuntimeToken == token.RawToken || rotateBody.RuntimeTokenID == token.ID {
		t.Fatalf("runtime token was not rotated: %#v", rotateBody)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/services/studio-worker-01", nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete service status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "studio-worker-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted node should be gone, got %v", err)
	}
}

func TestCreateNodeRegistrationTokenReportsBlockedEndpoint(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.jp","port":8443,"ssl_enabled":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("blocked node endpoint status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "node_endpoint_blocked") {
		t.Fatalf("blocked endpoint should return actionable code: %s", res.Body.String())
	}
}

func TestCreateNodeRegistrationTokenRejectsSecretScopeEscalation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"encoder_recorder","node_id":"encoder-01","name":"Encoder 01","public_url":"https://encoder.example.com","version":"0.1.0","allow_runtime_secrets":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("secret scope escalation status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "permission_escalation") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("denied node registration should not create token: %#v", tokens)
	}
}

func TestCreateDiscordNodeRegistrationTokenRequiresStartPermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	limitedCookie, limitedCSRF := loginForTest(t, handler, "limited", "correct horse battery")

	limitedReq := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"discord_bot","node_id":"discord-01","name":"Discord 01","host":"discord.example.com","port":8443,"ssl_enabled":true}`))
	limitedReq.AddCookie(limitedCookie)
	limitedReq.Header.Set("X-CSRF-Token", limitedCSRF)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden || !strings.Contains(limitedRes.Body.String(), "permission_escalation") {
		t.Fatalf("limited discord node status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	adminCookie, adminCSRF := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"discord_bot","node_id":"discord-01","name":"Discord 01","host":"discord.example.com","port":8443,"ssl_enabled":true}`))
	req.AddCookie(adminCookie)
	req.Header.Set("X-CSRF-Token", adminCSRF)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("admin discord node status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(body.Scopes, "streams.start") || !stringSliceContains(body.Scopes, "discord.status.write") {
		t.Fatalf("discord node scopes missing auto-start permissions: %#v", body.Scopes)
	}
}

func TestCreateWorkerNodeRegistrationTokenIncludesObservabilityIngest(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"worker-01","name":"Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("worker node status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(body.Scopes, "observability.ingest") || !stringSliceContains(body.Scopes, "worker.events.write") {
		t.Fatalf("worker node scopes missing observability ingest: %#v", body.Scopes)
	}
}

func TestServiceObservabilitySignalProxiesWithRegisteredNodeIdentity(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	var gotAuth string
	var gotPayload map[string]any
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/signals" {
			t.Fatalf("unexpected observability path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"signal":{"id":"sig-01"}}`))
	}))
	defer obs.Close()
	registerObservabilityNodeForTest(t, auth, "admin-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-01", []string{"service.register", "service.heartbeat", "observability.ingest"})
	registerServiceForTest(t, handler, token.RawToken, "worker-01", "worker")

	req := httptest.NewRequest(http.MethodPost, "/services/observability/signals", bytes.NewBufferString(`{"type":"metric","name":"worker.event_send_failures_total","service_id":"attacker","service_type":"encoder_recorder","stream_id":"stream-01","value":1}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("proxy signal status = %d body = %s", res.Code, res.Body.String())
	}
	if gotAuth != "Bearer admin-token" {
		t.Fatalf("observability proxy used wrong auth: %q", gotAuth)
	}
	if gotPayload["service_id"] != "worker-01" || gotPayload["service_type"] != "worker" {
		t.Fatalf("service identity was not enforced by proxy: %#v", gotPayload)
	}
}

func TestServiceRegisterRejectsWrongServiceType(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", res.Code, res.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"discord-01","service_type":"discord_bot","service_name":"Discord","public_url":"https://discord.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusForbidden {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var failureAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.register" && event.Result == "failure" {
			failureAudit = &event
		}
	}
	if failureAudit == nil || failureAudit.ResourceID != "discord-01" || failureAudit.Metadata["reason"] != "service_token_scope_mismatch" || failureAudit.ActorUsername != "worker" {
		t.Fatalf("service register failure audit missing: %#v", auth.AuditEvents())
	}
}

func TestServiceRegisterRejectsNonHTTPPublicURL(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", res.Code, res.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker","public_url":"ftp://worker.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusBadRequest {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if !strings.Contains(registerRes.Body.String(), "invalid_service_registration") || strings.Contains(registerRes.Body.String(), "worker.example.com") {
		t.Fatalf("unexpected register error response: %s", registerRes.Body.String())
	}
}

func TestServiceRegisterRejectsPrivatePublicURLByDefault(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createServiceTokenForTest(t, handler, cookie, csrf, "worker", []string{"service.register"})

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker","public_url":"http://169.254.169.254/latest/meta-data","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusBadRequest {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if !strings.Contains(registerRes.Body.String(), "invalid_service_registration") || strings.Contains(registerRes.Body.String(), "169.254.169.254") {
		t.Fatalf("unexpected register error response: %s", registerRes.Body.String())
	}
}

func TestWorkerAssignmentAndStreamEventAuthorization(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "workers.read", "workers.assign", "workers.restart"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "worker event stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	token := createServiceTokenForTest(t, handler, cookie, csrf, "worker", []string{"service.register", "service.heartbeat", "service.status.write", "worker.events.write"})
	registerServiceForTest(t, handler, token.RawToken, "worker-01", "worker")

	listReq := httptest.NewRequest(http.MethodGet, "/workers", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list workers status = %d body = %s", listRes.Code, listRes.Body.String())
	}

	unassignedEventReq := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(`{"service_id":"worker-01","stream_id":"`+stream.ID+`","event_type":"worker.overlay","payload":{"ok":true}}`))
	unassignedEventReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedEventRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedEventRes, unassignedEventReq)
	if unassignedEventRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned event status = %d body = %s", unassignedEventRes.Code, unassignedEventRes.Body.String())
	}

	unassignedHeartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","current_stream_id":"`+stream.ID+`"}`))
	unassignedHeartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedHeartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedHeartbeatRes, unassignedHeartbeatReq)
	if unassignedHeartbeatRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned heartbeat status = %d body = %s", unassignedHeartbeatRes.Code, unassignedHeartbeatRes.Body.String())
	}
	var heartbeatFailureAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.heartbeat" && event.Result == "failure" && event.ResourceID == "worker-01" {
			heartbeatFailureAudit = &event
		}
	}
	if heartbeatFailureAudit == nil || heartbeatFailureAudit.ActorUsername != "worker" || heartbeatFailureAudit.Metadata["reason"] != "service_not_assigned_to_token" {
		t.Fatalf("heartbeat failure audit missing: %#v", auth.AuditEvents())
	}

	assignReq := httptest.NewRequest(http.MethodPost, "/workers/worker-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("assign worker status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","current_stream_id":"`+stream.ID+`"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("assigned heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}

	eventReq := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(`{"service_id":"worker-01","stream_id":"`+stream.ID+`","event_type":"worker.overlay","payload":{"ok":true}}`))
	eventReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	eventRes := httptest.NewRecorder()
	handler.ServeHTTP(eventRes, eventReq)
	if eventRes.Code != http.StatusAccepted {
		t.Fatalf("assigned event status = %d body = %s", eventRes.Code, eventRes.Body.String())
	}

	restartReq := httptest.NewRequest(http.MethodPost, "/workers/worker-01/restart", nil)
	restartReq.AddCookie(cookie)
	restartReq.Header.Set("X-CSRF-Token", csrf)
	restartRes := httptest.NewRecorder()
	handler.ServeHTTP(restartRes, restartReq)
	if restartRes.Code != http.StatusAccepted {
		t.Fatalf("restart worker status = %d body = %s", restartRes.Code, restartRes.Body.String())
	}
}

func TestEncoderArtifactReportRequiresScopeAssignmentAndSafePaths(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "services.assign", "archives.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Artifact report")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	limited := createBoundServiceTokenForTest(t, handler, cookie, csrf, "encoder_recorder", "encoder-limited", []string{"service.register"})
	registerServiceForTest(t, handler, limited.RawToken, "encoder-limited", "encoder_recorder")
	limitedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(`{"service_id":"encoder-limited","stream_id":"`+stream.ID+`","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/`+stream.ID+`/final.mp4","size_bytes":123}]}`))
	limitedReq.Header.Set("Authorization", "Bearer "+limited.RawToken)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden {
		t.Fatalf("limited artifact report status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	token := createServiceTokenForTest(t, handler, cookie, csrf, "encoder_recorder", []string{"service.register", "encoder.status.write"})
	registerServiceForTest(t, handler, token.RawToken, "encoder-01", "encoder_recorder")
	reportBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/final.mp4","size_bytes":123}]}`
	unassignedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(reportBody))
	unassignedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedRes, unassignedReq)
	if unassignedRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned artifact report status = %d body = %s", unassignedRes.Code, unassignedRes.Body.String())
	}
	var unassignedAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "failure" && event.ResourceID == stream.ID {
			unassignedAudit = &event
		}
	}
	if unassignedAudit == nil || unassignedAudit.Metadata["reason"] != "service_not_assigned_to_stream" || unassignedAudit.Metadata["artifact_count"] != 1 {
		t.Fatalf("unassigned artifact report audit missing: %#v", auth.AuditEvents())
	}

	assignReq := httptest.NewRequest(http.MethodPost, "/services/encoder-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("assign encoder status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}

	unsafeReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(`{"service_id":"encoder-01","stream_id":"`+stream.ID+`","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"../secret","size_bytes":123}]}`))
	unsafeReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unsafeRes := httptest.NewRecorder()
	handler.ServeHTTP(unsafeRes, unsafeReq)
	if unsafeRes.Code != http.StatusBadRequest || !strings.Contains(unsafeRes.Body.String(), "invalid_stream_artifact") {
		t.Fatalf("unsafe artifact report status = %d body = %s", unsafeRes.Code, unsafeRes.Body.String())
	}
	var invalidAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "failure" && event.Metadata["reason"] == "invalid_stream_artifact" {
			invalidAudit = &event
		}
	}
	if invalidAudit == nil || strings.Contains(toJSONForTest(t, invalidAudit), "../secret") {
		t.Fatalf("invalid artifact report audit missing or leaked path: %#v", auth.AuditEvents())
	}
	for _, unsafePath := range []string{
		`C:\var\lib\autostream\archives\final\` + stream.ID + `\final.mp4`,
		"final/another-stream/final.mp4",
		"final/" + stream.ID + "/metadata.json",
	} {
		body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"` + strings.ReplaceAll(unsafePath, `\`, `\\`) + `","size_bytes":123}]}`
		req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_stream_artifact") {
			t.Fatalf("mismatched artifact path should be rejected: path=%q status=%d body=%s", unsafePath, res.Code, res.Body.String())
		}
	}
	serverOwnedBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","artifacts":[{"id":"client-controlled","kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/final.mp4","size_bytes":123,"created_at":"2099-01-01T00:00:00Z"}]}`
	serverOwnedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(serverOwnedBody))
	serverOwnedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	serverOwnedRes := httptest.NewRecorder()
	handler.ServeHTTP(serverOwnedRes, serverOwnedReq)
	if serverOwnedRes.Code != http.StatusBadRequest {
		t.Fatalf("server-owned artifact fields should be rejected: status=%d body=%s", serverOwnedRes.Code, serverOwnedRes.Body.String())
	}

	for _, size := range []int{123, 456} {
		body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/final.mp4","size_bytes":` + strconv.Itoa(size) + `}]}`
		req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusAccepted {
			t.Fatalf("artifact report status = %d body = %s", res.Code, res.Body.String())
		}
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].SizeBytes != 456 || artifacts[0].RelativePath != "final/"+stream.ID+"/final.mp4" {
		t.Fatalf("artifact report was not upserted safely: %#v", artifacts)
	}
	var successAuditCount int
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "success" && event.ResourceID == stream.ID {
			successAuditCount++
			if event.ActorUsername != "encoder_recorder" || event.ActorUserID != "service:encoder_recorder" || strings.Contains(event.ActorUserID, token.ID) || event.Metadata["artifact_count"] != 1 {
				t.Fatalf("unsafe artifact success audit: %#v", event)
			}
		}
	}
	if successAuditCount != 2 {
		t.Fatalf("expected artifact success audit for each accepted report, got %d events=%#v", successAuditCount, auth.AuditEvents())
	}
}

func TestWorkerAssignRejectsNonWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "non-worker assignment stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createServiceTokenForTest(t, handler, cookie, csrf, "discord_bot", []string{"service.register"})
	registerServiceForTest(t, handler, token.RawToken, "discord-01", "discord_bot")

	assignReq := httptest.NewRequest(http.MethodPost, "/workers/discord-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusBadRequest {
		t.Fatalf("assign non-worker status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}
}

func TestObservabilityProxyEndpoints(t *testing.T) {
	var gotAuth string
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/incidents":
			_, _ = w.Write([]byte(`[{"id":"inc-1","severity":"critical","google_drive_folder_id":"drive-folder-secret-id","target":"https://example.com/callback?api_key=upstream-secret"}]`))
		case "/diagnostics":
			_, _ = w.Write([]byte(`[{"incident_id":"inc-1","rule":"encoder_process_exited","diagnostic_report":{"summary":"Encoder stopped"}}]`))
		case "/metrics":
			_, _ = w.Write([]byte(`[{"name":"encoder.output_fps","service_id":"enc-1","value":60},{"name":"discord.audio_receiving","service_id":"enc-1","value":1},{"name":"encoder.audio_silence_sec","service_id":"enc-1","value":0},{"name":"encoder.audio_clipping_total","service_id":"enc-1","value":0}]`))
		case "/remediation-actions":
			_, _ = w.Write([]byte(`[{"id":"rem-1","status":"suggested"}]`))
		case "/notification-deliveries":
			_, _ = w.Write([]byte(`[{"id":"ntf-1","status":"success","target":"https://discord.com/api/webhooks/id/upstream-secret-token","message":"bearer upstream-secret-token"}]`))
		case "/notification-channels":
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"chn-1","name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/upstream-slack-token","masked_webhook_url":"https://hooks.slack.com/<WEBHOOK_PATH>","smtp_password":"raw-smtp-password","smtp_password_configured":true,"masked_email_target":"o***s@example.com","smtp_server":"smtp-bypass.example.com","recipient_list":["bypass@example.com"]}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"chn-1","name":"discord","webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token","masked_webhook_url":"https://example.com/<WEBHOOK_PATH>"},{"id":"slack-1","name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/upstream-slack-token","masked_webhook_url":"https://hooks.slack.com/<WEBHOOK_PATH>"},{"id":"email-1","name":"email","type":"email","email_recipients":["ops@example.com"],"smtp_host":"smtp.example.com","smtp_port":587,"smtp_tls":true,"smtp_from":"autostream@example.com","smtp_username":"autostream","smtp_password":"raw-smtp-password","smtp_password_configured":true,"masked_email_target":"o***s@example.com","smtp_server":"smtp-bypass.example.com","recipient_list":["bypass@example.com"]}]`))
		case "/notification-channels/chn-1":
			if r.Method == http.MethodDelete {
				_, _ = w.Write([]byte(`{"status":"deleted"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"chn-1","name":"discord","webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token","masked_webhook_url":"https://example.com/<WEBHOOK_PATH>","recipient_list":["bypass@example.com"]}`))
		case "/notification-channels/chn-1/test":
			_, _ = w.Write([]byte(`[{"status":"success","target":"https://example.com/<WEBHOOK_PATH>"}]`))
		case "/notification-events":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`[{"event_type":"admin.audit","status":"success","channel":"generic","target":"https://example.com/<WEBHOOK_PATH>"}]`))
		case "/incidents/inc-1/acknowledge":
			_, _ = w.Write([]byte(`{"id":"inc-1","status":"acknowledged"}`))
		case "/incidents/inc-1/resolve":
			_, _ = w.Write([]byte(`{"id":"inc-1","status":"resolved"}`))
		case "/remediation-actions/rem-1/approve":
			_, _ = w.Write([]byte(`{"id":"rem-1","status":"approved"}`))
		default:
			t.Fatalf("unexpected observability path: %s", r.URL.Path)
		}
	}))
	defer obs.Close()

	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"incidents.read", "incidents.acknowledge", "incidents.resolve", "diagnostics.read", "metrics.read", "remediation.read", "remediation.approve", "notification_channels.read", "notification_channels.create", "notification_channels.update", "notification_channels.delete", "notification_channels.test", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	observabilityToken := registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	if _, err := auth.Heartbeat(t.Context(), observabilityToken, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online", Metrics: map[string]any{"observability.uptime_seconds": 42}}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	})
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Metrics: map[string]any{"worker.active_jobs": 2, "worker.last_error": "do not persist"}}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for _, path := range []string{"/observability/incidents", "/observability/diagnostics", "/observability/metrics", "/observability/remediation-actions", "/observability/notification-deliveries", "/observability/notification-channels"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, res.Code, res.Body.String())
		}
		if path == "/observability/metrics" && (!strings.Contains(res.Body.String(), "discord.audio_receiving") || !strings.Contains(res.Body.String(), "encoder.audio_clipping_total")) {
			t.Fatalf("audio metrics were not proxied: %s", res.Body.String())
		}
		if path == "/observability/metrics" && (!strings.Contains(res.Body.String(), "observability.uptime_seconds") || !strings.Contains(res.Body.String(), "worker.active_jobs") || strings.Contains(res.Body.String(), "do not persist")) {
			t.Fatalf("node heartbeat metrics were not safely merged: %s", res.Body.String())
		}
		if strings.Contains(res.Body.String(), "upstream-secret-token") || strings.Contains(res.Body.String(), "upstream-slack-token") || strings.Contains(res.Body.String(), "upstream-secret") || strings.Contains(res.Body.String(), "api_key=") || strings.Contains(res.Body.String(), "drive-folder-secret-id") || strings.Contains(res.Body.String(), `"webhook_url":"https://discord.com`) || strings.Contains(res.Body.String(), `"webhook_url":"https://hooks.slack.com`) || strings.Contains(res.Body.String(), "hooks.slack.com/services") || strings.Contains(res.Body.String(), "raw-smtp-password") || strings.Contains(res.Body.String(), "ops@example.com") || strings.Contains(res.Body.String(), "smtp.example.com") || strings.Contains(res.Body.String(), "autostream@example.com") || strings.Contains(res.Body.String(), "smtp-bypass.example.com") || strings.Contains(res.Body.String(), "bypass@example.com") || strings.Contains(res.Body.String(), `"smtp_host"`) || strings.Contains(res.Body.String(), `"email_recipients"`) || strings.Contains(res.Body.String(), `"smtp_from"`) || strings.Contains(res.Body.String(), `"smtp_username"`) || strings.Contains(res.Body.String(), `"smtp_server"`) || strings.Contains(res.Body.String(), `"recipient_list"`) {
			t.Fatalf("observability proxy leaked upstream notification secret: %s", res.Body.String())
		}
		if path == "/observability/notification-channels" && (!strings.Contains(res.Body.String(), `"smtp_password_configured":true`) || !strings.Contains(res.Body.String(), `"masked_email_target":"o***s@example.com"`)) {
			t.Fatalf("email notification public status was not preserved: %s", res.Body.String())
		}
		if path == "/observability/notification-channels" && !strings.Contains(res.Body.String(), `"masked_webhook_url":"https://hooks.slack.com/\u003cWEBHOOK_PATH\u003e"`) {
			t.Fatalf("slack notification public masked URL was not preserved: %s", res.Body.String())
		}
	}
	createReq := httptest.NewRequest(http.MethodPost, "/observability/notification-channels", bytes.NewBufferString(`{"name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/slack-secret-token","enabled":true}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated || strings.Contains(createRes.Body.String(), "slack-secret-token") || strings.Contains(createRes.Body.String(), "upstream-slack-token") || strings.Contains(createRes.Body.String(), `"webhook_url":"https://hooks.slack.com`) || strings.Contains(createRes.Body.String(), "hooks.slack.com/services") || strings.Contains(createRes.Body.String(), "raw-smtp-password") {
		t.Fatalf("create channel status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), `"type":"slack"`) || !strings.Contains(createRes.Body.String(), `"masked_webhook_url":"https://hooks.slack.com/\u003cWEBHOOK_PATH\u003e"`) {
		t.Fatalf("create channel response lost public slack status: %s", createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), `"smtp_password_configured":true`) || !strings.Contains(createRes.Body.String(), `"masked_email_target":"o***s@example.com"`) {
		t.Fatalf("create channel response lost public email status: %s", createRes.Body.String())
	}
	events := auth.AuditEvents()
	var createAudit *store.AuditEvent
	for i := range events {
		if events[i].Action == "notification_channels.create" {
			createAudit = &events[i]
			break
		}
	}
	if createAudit == nil {
		t.Fatalf("expected notification channel create audit event, got %#v", events)
	}
	metadata, _ := json.Marshal(createAudit.Metadata)
	if strings.Contains(string(metadata), "slack-secret-token") || strings.Contains(string(metadata), "upstream-slack-token") || strings.Contains(string(metadata), "hooks.slack.com/services") {
		t.Fatalf("raw webhook leaked in audit metadata: %s", string(metadata))
	}
	if createAudit.Metadata["has_webhook_url"] != true {
		t.Fatalf("expected has_webhook_url audit metadata, got %#v", createAudit.Metadata)
	}
	auditReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=notifications", nil)
	auditReq.AddCookie(cookie)
	auditRes := httptest.NewRecorder()
	handler.ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK || !strings.Contains(auditRes.Body.String(), "notification_channels.create") || strings.Contains(auditRes.Body.String(), "slack-secret-token") || strings.Contains(auditRes.Body.String(), "hooks.slack.com/services") {
		t.Fatalf("notification audit status = %d body = %s", auditRes.Code, auditRes.Body.String())
	}
	testReq := httptest.NewRequest(http.MethodPost, "/observability/notification-channels/chn-1/test", nil)
	testReq.AddCookie(cookie)
	testReq.Header.Set("X-CSRF-Token", csrf)
	testRes := httptest.NewRecorder()
	handler.ServeHTTP(testRes, testReq)
	if testRes.Code != http.StatusAccepted || !strings.Contains(testRes.Body.String(), "success") {
		t.Fatalf("test channel status = %d body = %s", testRes.Code, testRes.Body.String())
	}
	ackReq := httptest.NewRequest(http.MethodPost, "/observability/incidents/inc-1/acknowledge", nil)
	ackReq.AddCookie(cookie)
	ackReq.Header.Set("X-CSRF-Token", csrf)
	ackRes := httptest.NewRecorder()
	handler.ServeHTTP(ackRes, ackReq)
	if ackRes.Code != http.StatusOK || !strings.Contains(ackRes.Body.String(), "acknowledged") {
		t.Fatalf("ack incident status = %d body = %s", ackRes.Code, ackRes.Body.String())
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/observability/remediation-actions/rem-1/approve", nil)
	approveReq.AddCookie(cookie)
	approveReq.Header.Set("X-CSRF-Token", csrf)
	approveRes := httptest.NewRecorder()
	handler.ServeHTTP(approveRes, approveReq)
	if approveRes.Code != http.StatusOK || !strings.Contains(approveRes.Body.String(), "approved") {
		t.Fatalf("approve status = %d body = %s", approveRes.Code, approveRes.Body.String())
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("unexpected upstream auth: %s", gotAuth)
	}
	events = auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "remediation.approve" {
		t.Fatalf("expected remediation approve audit event, got %#v", events)
	}
}

func TestObservabilityProxyDoesNotLeakTokenOnUpstreamError(t *testing.T) {
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-token", http.StatusForbidden)
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"incidents.read"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/incidents", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") {
		t.Fatalf("token leaked in response: %s", res.Body.String())
	}
}

func TestObservabilityMetricsFallsBackToNodeHeartbeatMetrics(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"metrics.read"}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	})
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Metrics: map[string]any{"worker.active_jobs": 3}}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/observability/metrics", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("metrics fallback status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"name":"worker.active_jobs"`) || !strings.Contains(res.Body.String(), `"service_id":"worker-01"`) {
		t.Fatalf("node heartbeat metrics were not returned without observability upstream: %s", res.Body.String())
	}
}

func TestRedactRawJSONDoesNotEchoInvalidUpstreamBody(t *testing.T) {
	body := redactRawJSON(json.RawMessage(`raw upstream token https://discord.com/api/webhooks/id/upstream-secret-token`))
	if strings.Contains(string(body), "upstream-secret-token") || strings.Contains(string(body), "discord.com/api/webhooks") {
		t.Fatalf("invalid upstream body leaked raw content: %s", string(body))
	}
	if !strings.Contains(string(body), "invalid_upstream_json") {
		t.Fatalf("expected safe invalid JSON marker, got %s", string(body))
	}
}

func TestRedactRawJSONRedactsScalarSecretLikeString(t *testing.T) {
	for _, raw := range []string{
		`"Bearer upstream-secret-token"`,
		`"https://discord.com/api/webhooks/id/upstream-secret-token"`,
		`"https://example.com/callback?api_key=upstream-secret-token"`,
		`"ast_svc_upstream-secret-token"`,
		`"ast_ingest_v1.upstream-secret-token.signature"`,
		`"ya29.upstream-secret-token"`,
	} {
		body := redactRawJSON(json.RawMessage(raw))
		if strings.Contains(string(body), "upstream-secret-token") || strings.Contains(string(body), "discord.com/api/webhooks") || strings.Contains(string(body), "api_key=") || strings.Contains(string(body), "ast_svc_") || strings.Contains(string(body), "ast_ingest_v1.") || strings.Contains(string(body), "ya29.") {
			t.Fatalf("scalar upstream JSON leaked raw content: %s", string(body))
		}
		if !strings.Contains(string(body), "redacted") {
			t.Fatalf("expected scalar upstream JSON to be redacted, got %s", string(body))
		}
	}
}

func TestRedactRawJSONRedactsNestedServiceTokens(t *testing.T) {
	body := redactRawJSON(json.RawMessage(`{"message":"ast_svc_upstream-secret-token","items":[{"detail":"ast_ingest_v1.upstream-secret-token.signature"}]}`))
	out := string(body)
	if strings.Contains(out, "upstream-secret-token") || strings.Contains(out, "ast_svc_") || strings.Contains(out, "ast_ingest_v1.") {
		t.Fatalf("nested upstream JSON leaked token-like value: %s", out)
	}
	if strings.Count(out, "redacted") != 2 {
		t.Fatalf("expected nested token-like values to be redacted, got %s", out)
	}
}

func TestWriteOneTimeSecretJSONSetsStrictNoStoreHeaders(t *testing.T) {
	res := httptest.NewRecorder()
	writeOneTimeSecretJSON(res, http.StatusOK, map[string]string{"token": "one-time-value"})
	header := res.Result().Header
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if got := header.Get("Expires"); got != "0" {
		t.Fatalf("Expires = %q, want 0", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestObservabilityProxyRejectsEncodedSlashID(t *testing.T) {
	upstreamCalled := false
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		t.Fatalf("upstream should not be called for invalid path id: %s", r.URL.Path)
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"notification_channels.read"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/notification-channels/..%2Fmetrics", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if upstreamCalled {
		t.Fatalf("invalid observability id reached upstream")
	}
}

func TestObservabilityProxyRequiresPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/incidents", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSetupFirstAdmin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))
	req := httptest.NewRequest(http.MethodPost, "/setup/first-admin", bytes.NewBufferString(`{"setup_token":"setup-token","username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	_, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	if csrf == "" {
		t.Fatal("expected admin to be able to login")
	}
}

func TestRootRedirectsToSetupWhenFirstAdminRequired(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "/setup" {
		t.Fatalf("root redirect = %d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
}

func TestRootRedirectsToLoginWhenSetupCompleteAndUnauthenticated(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "/login" {
		t.Fatalf("root redirect = %d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
}

func TestRootRedirectsToAdminWhenAuthenticated(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "/admin" {
		t.Fatalf("root redirect = %d location=%q body=%s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
}

func TestSetupStatusReportsFirstAdminRequired(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))

	req := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"setup_required":true`) || !strings.Contains(res.Body.String(), `"setup_enabled":true`) {
		t.Fatalf("empty setup status = %d body = %s", res.Code, res.Body.String())
	}

	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"setup_required":false`) || !strings.Contains(res.Body.String(), `"setup_enabled":true`) {
		t.Fatalf("post-user setup status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSetupStatusRespectsDisabledSetupToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken(""))

	req := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"setup_required":false`) || !strings.Contains(res.Body.String(), `"setup_enabled":false`) {
		t.Fatalf("disabled setup status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAppSettingsCanBeReadWithoutSession(t *testing.T) {
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()), WithAppSettingsStore(store.NewMemoryAppSettingsStore()))
	req := httptest.NewRequest(http.MethodGet, "/settings/app", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"app_name":"AutoStream"`) || !strings.Contains(res.Body.String(), `"timezone":"Asia/Tokyo"`) {
		t.Fatalf("app settings status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAppSettingsUpdatePersistsWithPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"Kome Panel","timezone":"America/Los_Angeles"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"app_name":"Kome Panel"`) || !strings.Contains(res.Body.String(), `"timezone":"America/Los_Angeles"`) {
		t.Fatalf("update app settings status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/settings/app", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"app_name":"Kome Panel"`) || !strings.Contains(res.Body.String(), `"timezone":"America/Los_Angeles"`) {
		t.Fatalf("persisted app settings status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAppSettingsUpdateStoresSMTPPasswordAsSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	secrets := store.NewMemorySecretStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"Kome Panel","timezone":"Asia/Tokyo","smtp_enabled":true,"smtp_host":"smtp.example.jp","smtp_port":587,"smtp_starttls":true,"smtp_from":"noreply@example.jp","smtp_username":"autostream","smtp_password":"raw-smtp-password"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("update SMTP settings status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"smtp_password_configured":true`) {
		t.Fatalf("SMTP password status was not returned: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-smtp-password") || strings.Contains(res.Body.String(), `"smtp_password"`) {
		t.Fatalf("raw SMTP password leaked in settings response: %s", res.Body.String())
	}
	value, err := secrets.GetSecretValue(t.Context(), store.AppSMTPPasswordSecretName)
	if err != nil {
		t.Fatal(err)
	}
	if value != "raw-smtp-password" {
		t.Fatal("unexpected stored SMTP secret value")
	}
}

func TestAppSettingsAcceptsDisplayNameSMTPFrom(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(store.NewMemorySecretStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"Kome Panel","timezone":"Asia/Tokyo","smtp_enabled":true,"smtp_host":"smtp.example.jp","smtp_port":587,"smtp_starttls":true,"smtp_from":"AutoStream <no-reply@example.jp>"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	var response store.AppSettings
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode display name SMTP From response: %v body = %s", err, res.Body.String())
	}
	if res.Code != http.StatusOK || response.SMTPFrom != "AutoStream <no-reply@example.jp>" {
		t.Fatalf("display name SMTP From status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAppSettingsTestEmailSendsWithSavedSMTPSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.example.jp",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "autostream",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/settings/app/test-email", bytes.NewBufferString(`{"to":"ops@example.jp"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"sent"`) {
		t.Fatalf("test email status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-smtp-password") || strings.Contains(res.Body.String(), "ops@example.jp") || strings.Contains(res.Body.String(), "smtp.example.jp") {
		t.Fatalf("test email response leaked sensitive data: %s", res.Body.String())
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("expected one test email, got %#v", mailer.messages)
	}
	if mailer.messages[0].To != "ops@example.jp" || !strings.Contains(mailer.messages[0].Subject, "Kome Panel SMTPテスト") {
		t.Fatalf("unexpected test email message: %#v", mailer.messages[0])
	}
	if !strings.Contains(mailer.messages[0].Text, "Control Panel からのテストメールです。") ||
		!strings.Contains(mailer.messages[0].Text, "送信を実行したユーザー: admin") ||
		strings.Contains(mailer.messages[0].Text, "This is a test email") {
		t.Fatalf("test email is not localized: %#v", mailer.messages[0])
	}
	if mailer.passwords[0] != "raw-smtp-password" {
		t.Fatalf("SMTP password was not resolved for test email")
	}
}

func TestAppSettingsTestEmailRejectsInvalidRecipient(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(store.NewMemoryAppSettingsStore()), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/settings/app/test-email", bytes.NewBufferString(`{"to":"ops@example.jp\r\nBcc: bad@example.jp"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_email_recipient") {
		t.Fatalf("invalid recipient status = %d body = %s", res.Code, res.Body.String())
	}
	if len(mailer.messages) != 0 {
		t.Fatalf("invalid recipient should not invoke mailer: %#v", mailer.messages)
	}
}

func TestAppSettingsTestEmailSanitizesDeliveryFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.example.jp",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "autostream",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{err: errors.New("smtp failed with raw-smtp-password for ops@example.jp via smtp.example.jp")}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/settings/app/test-email", bytes.NewBufferString(`{"to":"ops@example.jp"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"send_failed"`) {
		t.Fatalf("delivery failure status = %d body = %s", res.Code, res.Body.String())
	}
	responseAndAudit := res.Body.String() + toJSONForTest(t, auth.AuditEvents())
	for _, raw := range []string{"raw-smtp-password", "ops@example.jp", "smtp.example.jp"} {
		if strings.Contains(responseAndAudit, raw) {
			t.Fatalf("delivery failure leaked %q: %s", raw, responseAndAudit)
		}
	}
}

func TestAppSettingsTestEmailReturnsSanitizedSMTPFailureCode(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.example.jp",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "autostream",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{err: fmt.Errorf("delivery failed: %w", errors.New("smtp_auth_failed: raw-smtp-password rejected for ops@example.jp via smtp.example.jp"))}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/settings/app/test-email", bytes.NewBufferString(`{"to":"ops@example.jp"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"smtp_auth_failed"`) {
		t.Fatalf("SMTP auth failure status = %d body = %s", res.Code, res.Body.String())
	}
	responseAndAudit := res.Body.String() + toJSONForTest(t, auth.AuditEvents())
	for _, raw := range []string{"raw-smtp-password", "ops@example.jp", "smtp.example.jp"} {
		if strings.Contains(responseAndAudit, raw) {
			t.Fatalf("SMTP auth failure leaked %q: %s", raw, responseAndAudit)
		}
	}
}

func TestAppSettingsRejectsInvalidSMTPWithoutStoringPassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	secrets := store.NewMemorySecretStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"Kome Panel","timezone":"Asia/Tokyo","smtp_enabled":true,"smtp_host":"bad host","smtp_port":587,"smtp_starttls":true,"smtp_from":"noreply@example.jp","smtp_username":"autostream","smtp_password":"raw-smtp-password"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_app_settings") {
		t.Fatalf("invalid SMTP settings status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := secrets.GetSecretValue(t.Context(), store.AppSMTPPasswordSecretName); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid SMTP settings should not store secret, err = %v", err)
	}
}

func TestAppSettingsRejectsInvalidTimezone(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(store.NewMemoryAppSettingsStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"Kome Panel","timezone":"../../etc/passwd"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_app_settings") {
		t.Fatalf("invalid timezone status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestVersionEndpointShowsBuildInfoAndUpdate(t *testing.T) {
	previousVersion, previousCommit, previousBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version, version.Commit, version.BuildDate = "v1.2.3", "abc123", "2026-07-07T00:00:00Z"
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildDate = previousVersion, previousCommit, previousBuildDate
	})
	t.Setenv("AUTOSTREAM_LATEST_VERSION", "v1.3.0")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("version status = %d body = %s", res.Code, res.Body.String())
	}
	for _, want := range []string{`"service":"control-panel"`, `"version":"v1.2.3"`, `"commit":"abc123"`, `"build_date":"2026-07-07T00:00:00Z"`, `"latest_version":"v1.3.0"`, `"update_available":true`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("version response missing %s: %s", want, res.Body.String())
		}
	}
}

func TestVersionEndpointChecksConfiguredUpdateURL(t *testing.T) {
	previousVersion, previousCommit, previousBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version, version.Commit, version.BuildDate = "v1.3.5", "abc123", "2026-07-07T00:00:00Z"
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildDate = previousVersion, previousCommit, previousBuildDate
	})
	updateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json, text/plain" {
			t.Fatalf("unexpected Accept header: %q", r.Header.Get("Accept"))
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "autostream-control-panel/") {
			t.Fatalf("unexpected User-Agent header: %q", r.Header.Get("User-Agent"))
		}
		writeJSON(w, http.StatusOK, map[string]string{"tag_name": "v1.3.6"})
	}))
	defer updateServer.Close()
	t.Setenv("AUTOSTREAM_UPDATE_CHECK_URL", updateServer.URL)
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("version status = %d body = %s", res.Code, res.Body.String())
	}
	for _, want := range []string{`"latest_version":"v1.3.6"`, `"update_available":true`, `"update_check_source":"url"`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("version response missing %s: %s", want, res.Body.String())
		}
	}
}

func TestVersionEndpointCanDisableUpdateCheck(t *testing.T) {
	t.Setenv("AUTOSTREAM_UPDATE_CHECK_URL", "off")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"update_check_source":"disabled"`) || strings.Contains(res.Body.String(), `"latest_version"`) {
		t.Fatalf("disabled update check response mismatch: status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSetupFirstAdminUsesConfiguredPasswordMinimum(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	settings := store.NewMemorySecuritySettingsStore()
	current, err := settings.GetSecuritySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.PasswordMinLength = 24
	if _, err := settings.UpdateSecuritySettings(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithSecuritySettingsStore(settings),
		WithSetupToken("setup-token"),
	)
	req := httptest.NewRequest(http.MethodPost, "/setup/first-admin", bytes.NewBufferString(`{"setup_token":"setup-token","username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"password_min_length":24`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSetupFirstAdminOnlyOnce(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))
	body := `{"setup_token":"setup-token","username":"admin","password":"correct horse battery"}`
	req := httptest.NewRequest(http.MethodPost, "/setup/first-admin", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("first setup status = %d body = %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/setup/first-admin", bytes.NewBufferString(body))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("second setup status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSetupFirstAdminRejectsBadToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSetupToken("setup-token"))
	req := httptest.NewRequest(http.MethodPost, "/setup/first-admin", bytes.NewBufferString(`{"setup_token":"wrong","username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestControlAPIRejectsOversizedRequestBody(t *testing.T) {
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()), WithSetupToken("setup-token"))
	body := `{"username":"admin","password":"` + strings.Repeat("a", maxControlRequestBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/setup/first-admin", strings.NewReader(body))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func createServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType string, scopes []string) store.ServiceToken {
	return createBoundServiceTokenForTest(t, handler, cookie, csrf, serviceType, defaultServiceIDForTest(serviceType), scopes)
}

func createBoundServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType, serviceID string, scopes []string) store.ServiceToken {
	t.Helper()
	payload := map[string]any{"service_type": serviceType, "scopes": scopes}
	if stringSliceContains(scopes, "service.register") {
		payload["service_id"] = serviceID
		payload["service_name"] = serviceID
		payload["public_url"] = "https://" + serviceID + ".example.com"
		payload["version"] = "0.1.0"
		payload["capabilities"] = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", res.Code, res.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func defaultServiceIDForTest(serviceType string) string {
	switch serviceType {
	case "worker":
		return "worker-01"
	case "encoder_recorder":
		return "encoder-01"
	case "discord_bot":
		return "discord-01"
	case "observability":
		return "observability-01"
	default:
		return serviceType + "-01"
	}
}

func registerServiceWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, registration store.ServiceRegistration) store.RegisteredService {
	t.Helper()
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	service, err := auth.RegisterService(t.Context(), token, registration)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerObservabilityNodeForTest(t *testing.T, auth *store.MemoryAuthStore, rawToken, publicURL string) store.ServiceToken {
	t.Helper()
	token := store.ServiceToken{
		ID:          "observability-node-token",
		ServiceType: "observability",
		RawToken:    rawToken,
		TokenHash:   security.HashToken(rawToken),
		Scopes:      []string{"service.register", "service.heartbeat", "observability.ingest", "remediation.execute"},
		CreatedAt:   time.Now().UTC(),
	}
	registerObservabilityNodeWithTokenForTest(t, auth, token, publicURL)
	return token
}

func registerObservabilityNodeWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, publicURL string) store.RegisteredService {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key")
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS", "127.0.0.1")
	service := registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "observability-01",
		ServiceType: "observability",
		ServiceName: "Observability",
		PublicURL:   publicURL,
	})
	ciphertext, nonce, err := security.EncryptSecret(token.RawToken, "test-secret-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	service, err = auth.SetServiceNodeTokenSecret(t.Context(), service.ServiceID, ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerServiceForTest(t *testing.T, handler http.Handler, rawToken, serviceID, serviceType string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"service_id": serviceID, "service_type": serviceType, "service_name": serviceID,
		"public_url": "https://" + serviceID + ".example.com", "version": "0.1.0", "capabilities": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("register service status = %d body = %s", res.Code, res.Body.String())
	}
}

type activeRuntimeSecretLeaseStore struct{}

func (activeRuntimeSecretLeaseStore) ClaimRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease, ttl time.Duration) (store.RuntimeSecretLease, error) {
	return store.RuntimeSecretLease{}, store.ErrRuntimeSecretLeaseActive
}

func (activeRuntimeSecretLeaseStore) ReleaseRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease) error {
	return nil
}

type trackingSecretStore struct {
	getCalls int
	statuses []store.SecretStatus
}

func (s *trackingSecretStore) ListSecretStatus(ctx context.Context) ([]store.SecretStatus, error) {
	return append([]store.SecretStatus(nil), s.statuses...), nil
}

func (s *trackingSecretStore) UpdateSecret(ctx context.Context, name, value string) (store.SecretStatus, error) {
	return store.SecretStatus{Name: name, Configured: value != ""}, nil
}

func (s *trackingSecretStore) GetSecretValue(ctx context.Context, name string) (string, error) {
	s.getCalls++
	return "Bot <RAW_DISCORD_TOKEN>", nil
}

type fakeServiceDispatcher struct {
	startCalls               int
	stopCalls                int
	retryCalls               int
	audioStatusCalls         int
	workerEventsCalls        int
	encoderPreflightCalls    int
	workerEventSendCalls     int
	archiveDownloadCalls     int
	archiveDeleteCalls       int
	archiveRenameCalls       int
	startedStream            store.Stream
	stoppedStream            store.Stream
	retriedStream            store.Stream
	audioStatusStream        store.Stream
	workerEventsStream       store.Stream
	encoderPreflightStream   store.Stream
	workerEventStream        store.Stream
	archiveStream            store.Stream
	archiveArtifact          store.StreamArtifact
	archiveRenameName        string
	startRequest             servicecall.StartRequest
	retriedArchiveConfig     map[string]any
	startedServices          []store.RegisteredService
	stoppedServices          []store.RegisteredService
	retriedServices          []store.RegisteredService
	audioStatusServices      []store.RegisteredService
	workerEventsServices     []store.RegisteredService
	encoderPreflightServices []store.RegisteredService
	workerEventServices      []store.RegisteredService
	audioStatus              servicecall.AudioStatusResult
	workerEvents             servicecall.WorkerEventsResult
	encoderPreflight         servicecall.ServicePreflightResult
	workerEventRequest       servicecall.WorkerEventRequest
	failStart                bool
	failStop                 bool
	failRetry                bool
	failAudioStatus          bool
	failWorkerEvents         bool
	failEncoderPreflight     bool
	failWorkerEventSend      bool
	failArchiveAction        bool
	dispatchFailureError     string
}

type fakeYouTubeLiveClient struct {
	prepareCalls    int
	completeCalls   int
	prepareRequest  ytlive.PrepareRequest
	completeRequest ytlive.CompleteRequest
	prepared        ytlive.PreparedOutput
	prepareErr      error
	completeErr     error
}

type fakeOAuthVerifier struct {
	identity oauthlogin.Identity
	err      error
}

type fakeOAuthConnector struct {
	account   oauthlogin.ConnectedAccount
	err       error
	onConnect func(oauthlogin.ConnectRequest)
}

func (f fakeOAuthVerifier) Verify(ctx context.Context, req oauthlogin.VerifyRequest) (oauthlogin.Identity, error) {
	if f.err != nil {
		return oauthlogin.Identity{}, f.err
	}
	return f.identity, nil
}

func (f fakeOAuthConnector) Connect(ctx context.Context, req oauthlogin.ConnectRequest) (oauthlogin.ConnectedAccount, error) {
	if f.onConnect != nil {
		f.onConnect(req)
	}
	if f.err != nil {
		return oauthlogin.ConnectedAccount{}, f.err
	}
	return f.account, nil
}

func startOAuthForTest(t *testing.T, handler http.Handler, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+providerID+"/start", bytes.NewBufferString(`{}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func assertOAuthCallbackNoStoreHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" {
		t.Fatalf("oauth callback Cache-Control = %q, want no-store", header.Get("Cache-Control"))
	}
	if header.Get("Pragma") != "no-cache" {
		t.Fatalf("oauth callback Pragma = %q, want no-cache", header.Get("Pragma"))
	}
	if header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("oauth callback Referrer-Policy = %q, want no-referrer", header.Get("Referrer-Policy"))
	}
}

func startOAuthAccountForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q}`, providerID)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth account start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func findCookieForTest(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %s in %#v", name, cookies)
	return nil
}

func (f *fakeYouTubeLiveClient) Prepare(ctx context.Context, req ytlive.PrepareRequest) (ytlive.PreparedOutput, error) {
	f.prepareCalls++
	f.prepareRequest = req
	if f.prepareErr != nil {
		return ytlive.PreparedOutput{}, f.prepareErr
	}
	return f.prepared, nil
}

func (f *fakeYouTubeLiveClient) Complete(ctx context.Context, req ytlive.CompleteRequest) error {
	f.completeCalls++
	f.completeRequest = req
	return f.completeErr
}

type readinessBlockDispatcher struct {
	fakeServiceDispatcher
	issues []servicecall.ReadinessIssue
}

func (f *readinessBlockDispatcher) StartReadinessIssues(services []store.RegisteredService, req servicecall.StartRequest, now time.Time) []servicecall.ReadinessIssue {
	return f.issues
}

func (f *fakeServiceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	f.startCalls++
	f.startedStream = stream
	f.startRequest = req
	f.startedServices = append([]store.RegisteredService(nil), services...)
	if f.failStart {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/jobs/start", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	for _, service := range services {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/start", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	f.stopCalls++
	f.stoppedStream = stream
	f.stoppedServices = append([]store.RegisteredService(nil), services...)
	if f.failStop {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/stop", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	for _, service := range services {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult {
	f.retryCalls++
	f.retriedStream = stream
	f.retriedArchiveConfig = archiveConfig
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			f.retriedServices = append(f.retriedServices, service)
		}
	}
	if f.failRetry && len(f.retriedServices) > 0 {
		return []servicecall.DispatchResult{{ServiceID: f.retriedServices[0].ServiceID, ServiceType: f.retriedServices[0].ServiceType, Endpoint: "/streams/package", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(f.retriedServices))
	for _, service := range f.retriedServices {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/streams/package", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.AudioStatusResult {
	f.audioStatusCalls++
	f.audioStatusStream = stream
	f.audioStatusServices = append([]store.RegisteredService(nil), services...)
	if f.failAudioStatus {
		return servicecall.AudioStatusResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/audio-status", Success: false, Error: "failed"}
	}
	if f.audioStatus.Success || f.audioStatus.Error != "" {
		return f.audioStatus
	}
	return servicecall.AudioStatusResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/audio-status",
		StatusCode:  http.StatusOK,
		Success:     true,
		AudioBridgeState: servicecall.AudioBridgeStatus{
			StreamID:     stream.ID,
			BridgeActive: true,
		},
	}
}

func (f *fakeServiceDispatcher) WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.WorkerEventsResult {
	f.workerEventsCalls++
	f.workerEventsStream = stream
	f.workerEventsServices = append([]store.RegisteredService(nil), services...)
	if f.failWorkerEvents {
		return servicecall.WorkerEventsResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/worker-events", Success: false, Error: "failed"}
	}
	if f.workerEvents.Success || f.workerEvents.Error != "" {
		return f.workerEvents
	}
	return servicecall.WorkerEventsResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/worker-events",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events:      []servicecall.WorkerEvent{},
	}
}

func (f *fakeServiceDispatcher) EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.ServicePreflightResult {
	f.encoderPreflightCalls++
	f.encoderPreflightStream = stream
	f.encoderPreflightServices = append([]store.RegisteredService(nil), services...)
	if f.failEncoderPreflight {
		return servicecall.ServicePreflightResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/preflight", Success: false, Error: "failed"}
	}
	if f.encoderPreflight.Success || f.encoderPreflight.Error != "" {
		return f.encoderPreflight
	}
	return servicecall.ServicePreflightResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/preflight",
		StatusCode:  http.StatusOK,
		Success:     true,
		Ready:       true,
		CheckedAt:   time.Now().UTC(),
		Checks:      []servicecall.ServicePreflightCheck{},
	}
}

func (f *fakeServiceDispatcher) SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.WorkerEventRequest) servicecall.DispatchResult {
	f.workerEventSendCalls++
	f.workerEventStream = stream
	f.workerEventServices = append([]store.RegisteredService(nil), services...)
	f.workerEventRequest = req
	if f.failWorkerEventSend {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/events/" + req.EventType, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/events/" + req.EventType,
		StatusCode:  http.StatusAccepted,
		Success:     true,
	}
}

func (f *fakeServiceDispatcher) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.ArchiveArtifactDownloadResult {
	f.archiveDownloadCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	if f.failArchiveAction {
		return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true, FileName: artifact.Name, ContentType: "video/mp4", SizeBytes: 13, Body: io.NopCloser(strings.NewReader("archive-bytes"))}
}

func (f *fakeServiceDispatcher) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.DispatchResult {
	f.archiveDeleteCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) servicecall.DispatchResult {
	f.archiveRenameCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	f.archiveRenameName = name
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) failureError() string {
	if f.dispatchFailureError != "" {
		return f.dispatchFailureError
	}
	return "failed"
}

func registerAssignedServices(t *testing.T, auth *store.MemoryAuthStore, streamID string, serviceTypes ...string) {
	t.Helper()
	for _, serviceType := range serviceTypes {
		serviceID := serviceType + "-01"
		registerServiceInstance(t, auth, serviceID, serviceType)
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, streamID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
}

func createDiscordConfigForTest(t *testing.T, profiles *store.MemoryProfileStore, name, serviceID, _, _, _ string) store.Profile {
	t.Helper()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, name, map[string]any{
		"service_id":           serviceID,
		"bot_token_configured": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func registerServiceInstance(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string) {
	t.Helper()
	registerServiceInstanceWithCapabilities(t, auth, serviceID, serviceType, map[string]any{})
}

func registerServiceInstanceWithCapabilities(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string, capabilities map[string]any) {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Version: "0.1.0", Capabilities: capabilities})
}

func assignServiceForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s status = %d body = %s", serviceID, streamID, res.Code, res.Body.String())
	}
}

func assignServiceWithRoleForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID, role string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`","assignment_role":"`+role+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s as %s status = %d body = %s", serviceID, streamID, role, res.Code, res.Body.String())
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasAuditAction(events []store.AuditEvent, action string) bool {
	for _, event := range events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func testAvatarPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(24 + x%80), G: uint8(100 + y%100), B: 180, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func loginForTest(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("missing session cookie")
	}
	var response struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.CSRFToken == "" {
		t.Fatal("missing csrf token")
	}
	return cookie, response.CSRFToken
}

func loginMFAChallengeForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("MFA login challenge status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ChallengeToken == "" {
		t.Fatal("missing MFA challenge token")
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("MFA challenge must not issue cookies")
	}
	return response.ChallengeToken
}
