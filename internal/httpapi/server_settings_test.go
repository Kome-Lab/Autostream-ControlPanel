package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
)

func TestSecuritySettingsCanUpdateSafeValues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.read", "system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":8,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":["admin","super_admin","admin"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"password_min_length":8`) || !strings.Contains(res.Body.String(), `"mfa_mode":"totp"`) || !strings.Contains(res.Body.String(), `"mfa_required_roles":["admin","super_admin"]`) || !strings.Contains(res.Body.String(), `"mfa_supported_methods":["totp","passkey"]`) || !strings.Contains(res.Body.String(), `"passkey_status":"available"`) {
		t.Fatalf("update settings status = %d body = %s", res.Code, res.Body.String())
	}
	badReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":7,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"disabled"}`))
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

func TestPublicAppSettingsExcludeSMTPConfiguration(t *testing.T) {
	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "AutoStream",
		Timezone:               "Asia/Tokyo",
		SMTPEnabled:            true,
		SMTPHost:               "smtp.internal.example",
		SMTPPort:               587,
		SMTPStartTLS:           true,
		SMTPFrom:               "noreply@example.jp",
		SMTPUsername:           "smtp-user",
		SMTPPasswordConfigured: true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()), WithAppSettingsStore(settings))
	req := httptest.NewRequest(http.MethodGet, "/settings/app", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public app settings status = %d body = %s", res.Code, res.Body.String())
	}
	for _, privateValue := range []string{"smtp.internal.example", "smtp-user", "noreply@example.jp", `"smtp_host"`, `"smtp_username"`, `"smtp_from"`} {
		if strings.Contains(res.Body.String(), privateValue) {
			t.Fatalf("public app settings leaked %q: %s", privateValue, res.Body.String())
		}
	}
}

func TestManagedAppSettingsRequirePermissionAndExposeSMTPStatus(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{AppName: "AutoStream", Timezone: "Asia/Tokyo", SMTPEnabled: true, SMTPHost: "smtp.internal.example", SMTPPort: 587, SMTPStartTLS: true, SMTPFrom: "noreply@example.jp"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAppSettingsStore(settings))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/settings/app/manage", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("managed settings without session status = %d body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/settings/app/manage", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"smtp_host":"smtp.internal.example"`) {
		t.Fatalf("managed settings status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAppSettingsUpdatePersistsGoogleAnalytics(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(store.NewMemoryAppSettingsStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"AutoStream","timezone":"Asia/Tokyo","google_analytics_enabled":true,"google_analytics_measurement_id":"g-abcd1234"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"google_analytics_measurement_id":"G-ABCD1234"`) {
		t.Fatalf("analytics settings status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/settings/app", bytes.NewBufferString(`{"app_name":"AutoStream","timezone":"Asia/Tokyo","google_analytics_enabled":true,"google_analytics_measurement_id":"G-BAD<script>"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_app_settings") {
		t.Fatalf("invalid analytics settings status = %d body = %s", res.Code, res.Body.String())
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
	t.Setenv("AUTOSTREAM_WORKER_LATEST_VERSION", "v2.4.1")
	t.Setenv("AUTOSTREAM_ENCODER_RECORDER_LATEST_VERSION", "v3.0.2")
	t.Setenv("AUTOSTREAM_DISCORD_BOT_LATEST_VERSION", "v1.8.4")
	t.Setenv("AUTOSTREAM_OBSERVABILITY_LATEST_VERSION", "v4.2.0")
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
	var payload versionInfoResponse
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	wants := map[string]string{
		"worker":           "v2.4.1",
		"encoder_recorder": "v3.0.2",
		"discord_bot":      "v1.8.4",
		"observability":    "v4.2.0",
	}
	for serviceType, want := range wants {
		if got := payload.ServiceUpdates[serviceType].LatestVersion; got != want {
			t.Fatalf("%s latest version = %q, want %q; response = %s", serviceType, got, want, res.Body.String())
		}
	}
	if payload.ServiceUpdates["worker"].LatestVersion == payload.LatestVersion {
		t.Fatalf("worker latest version must not reuse the Control Panel latest version: %s", res.Body.String())
	}
}

func TestUpdaterVersionEndpointIsUnauthenticatedAndBoundedIdentity(t *testing.T) {
	previousVersion, previousCommit, previousBuildDate := version.Version, version.Commit, version.BuildDate
	version.Version, version.Commit, version.BuildDate = "v1.7.1", "forbidden-commit", "forbidden-build-date"
	t.Setenv("SERVICE_VERSION", "from-environment")
	t.Setenv("SERVICE_ID", "control-panel-primary")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")
	t.Setenv("AUTOSTREAM_SECRET", "must-not-be-exposed")
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildDate = previousVersion, previousCommit, previousBuildDate
	})

	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	req := httptest.NewRequest(http.MethodGet, "/updater/version", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("updater version status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("updater version content type = %q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("updater version cache control = %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode updater version response: %v", err)
	}
	if len(payload) != 4 ||
		payload["version"] != "v1.7.1" ||
		payload["service_id"] != "control-panel" ||
		payload["service_type"] != "control_panel" ||
		payload["config_revision"] != float64(7) {
		t.Fatalf("unexpected updater version response: %#v", payload)
	}
	for _, forbidden := range []string{
		"from-environment",
		"control-panel-primary",
		"must-not-be-exposed",
		version.Commit,
		version.BuildDate,
	} {
		if forbidden != "" && strings.Contains(res.Body.String(), forbidden) {
			t.Fatalf("updater version response exposed forbidden value %q: %s", forbidden, res.Body.String())
		}
	}

	methodReq := httptest.NewRequest(http.MethodPost, "/updater/version", nil)
	methodRes := httptest.NewRecorder()
	handler.ServeHTTP(methodRes, methodReq)
	if methodRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("updater version POST status = %d body = %s", methodRes.Code, methodRes.Body.String())
	}
	if got := methodRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("updater version POST cache control = %q", got)
	}
	if got := methodRes.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("updater version POST Allow = %q", got)
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/version", nil)
	protectedRes := httptest.NewRecorder()
	handler.ServeHTTP(protectedRes, protectedReq)
	if protectedRes.Code != http.StatusUnauthorized {
		t.Fatalf("authenticated application version status = %d body = %s", protectedRes.Code, protectedRes.Body.String())
	}
}

func TestUpdaterVersionEndpointDefaultsIdentityAndRejectsInvalidConfigRevision(t *testing.T) {
	previousVersion := version.Version
	version.Version = "v1.7.1"
	t.Cleanup(func() {
		version.Version = previousVersion
	})

	t.Run("defaults", func(t *testing.T) {
		t.Setenv("SERVICE_ID", "")
		t.Setenv("AUTOSTREAM_CONFIG_REVISION", "")
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
		req := httptest.NewRequest(http.MethodGet, "/updater/version", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("updater version status = %d body = %s", res.Code, res.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode updater version response: %v", err)
		}
		if len(payload) != 4 ||
			payload["service_id"] != "control-panel" ||
			payload["service_type"] != "control_panel" ||
			payload["config_revision"] != float64(1) {
			t.Fatalf("unexpected default updater identity: %#v", payload)
		}
	})

	for _, revision := range []string{"0", "-1", "+1", "01", "1.5", " 2 ", "9223372036854775808"} {
		t.Run("invalid_"+strings.NewReplacer("-", "minus", "+", "plus", ".", "_", " ", "_").Replace(revision), func(t *testing.T) {
			t.Setenv("SERVICE_ID", "control-panel")
			t.Setenv("AUTOSTREAM_CONFIG_REVISION", revision)
			handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
			req := httptest.NewRequest(http.MethodGet, "/updater/version", nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != http.StatusInternalServerError {
				t.Fatalf("invalid config revision %q status = %d body = %s", revision, res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("invalid config revision %q cache control = %q", revision, got)
			}
			var payload map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode invalid config revision response: %v", err)
			}
			if len(payload) != 1 || payload["code"] != "invalid_service_config_revision" {
				t.Fatalf("invalid config revision response exposed unbounded details: %#v", payload)
			}
			if strings.Contains(res.Body.String(), revision) {
				t.Fatalf("invalid config revision response echoed configuration value %q: %s", revision, res.Body.String())
			}
		})
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
	disableNodeVersionUpdateChecks(t)
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
	t.Setenv(dockerVersionUpdateTarget.latestVersionEnv, "v9.9.9")
	disableNodeVersionUpdateChecks(t)
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
