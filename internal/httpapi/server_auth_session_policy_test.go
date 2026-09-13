package httpapi

import (
	"bytes"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
