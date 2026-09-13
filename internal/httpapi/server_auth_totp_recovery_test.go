package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
