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
