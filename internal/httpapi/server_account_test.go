package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

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
