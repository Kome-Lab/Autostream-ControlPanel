package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
