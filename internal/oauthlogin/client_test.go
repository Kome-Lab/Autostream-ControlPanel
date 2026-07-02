package oauthlogin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"google.golang.org/api/idtoken"
)

func TestGoogleIdentityFromIDTokenUsesGoogleValidator(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)
	customClient := &http.Client{}
	original := validateGoogleIDToken
	t.Cleanup(func() { validateGoogleIDToken = original })
	validateGoogleIDToken = func(ctx context.Context, rawIDToken, audience string, client *http.Client) (*idtoken.Payload, error) {
		if rawIDToken != "signed-google-id-token" || audience != "google-client-id" || client != customClient {
			t.Fatalf("unexpected validator input: token=%q audience=%q client=%p", rawIDToken, audience, client)
		}
		return validGooglePayload(provider, now, "expected-nonce"), nil
	}

	identity, err := (HTTPVerifier{Client: customClient}).googleIdentityFromIDToken(t.Context(), provider, "signed-google-id-token", "expected-nonce", now)
	if err != nil {
		t.Fatalf("expected identity, got error: %v", err)
	}
	if identity.ProviderID != provider.ID || identity.Subject != "google-subject-01" || identity.Email != "operator@example.com" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestGoogleIdentityFromIDTokenRejectsValidatorError(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)
	original := validateGoogleIDToken
	t.Cleanup(func() { validateGoogleIDToken = original })
	validateGoogleIDToken = func(ctx context.Context, rawIDToken, audience string, client *http.Client) (*idtoken.Payload, error) {
		return nil, errors.New("signature invalid")
	}

	_, err := (HTTPVerifier{}).googleIdentityFromIDToken(t.Context(), provider, "tampered-google-id-token", "expected-nonce", now)
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validator rejection, got %v", err)
	}
}

func TestVerifyGitHubExchangesCodeAndReadsUserIdentity(t *testing.T) {
	provider := store.OAuthProvider{
		ID:           "github-provider-01",
		ProviderType: "github",
		ClientID:     "github-client-id",
		ClientSecret: "raw-github-client-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://github.com/login/oauth/access_token":
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected token method: %s", req.Method)
			}
			body, _ := io.ReadAll(req.Body)
			form := string(body)
			for _, want := range []string{"grant_type=authorization_code", "code=github-code", "client_id=github-client-id", "client_secret=raw-github-client-secret"} {
				if !strings.Contains(form, want) {
					t.Fatalf("github token request missing %q in %q", want, form)
				}
			}
			return jsonResponse(200, `{"access_token":"github-access-token","token_type":"bearer","scope":"read:user user:email"}`), nil
		case "https://api.github.com/user":
			if req.Header.Get("Authorization") != "Bearer github-access-token" {
				t.Fatalf("github userinfo authorization header mismatch: %q", req.Header.Get("Authorization"))
			}
			return jsonResponse(200, `{"id":9163,"login":"kome-lab","email":"operator@example.com"}`), nil
		case "https://api.github.com/user/emails":
			if req.Header.Get("Authorization") != "Bearer github-access-token" {
				t.Fatalf("github emails authorization header mismatch: %q", req.Header.Get("Authorization"))
			}
			return jsonResponse(200, `[{"email":"operator@example.com","primary":true,"verified":true}]`), nil
		default:
			t.Fatalf("unexpected GitHub OAuth URL: %s", req.URL.String())
			return jsonResponse(404, `{}`), nil
		}
	})}

	identity, err := (HTTPVerifier{Client: client}).Verify(t.Context(), VerifyRequest{Provider: provider, Code: "github-code"})
	if err != nil {
		t.Fatalf("expected GitHub identity, got error: %v", err)
	}
	if identity.ProviderID != provider.ID || identity.ProviderType != "github" || identity.Subject != "9163" || identity.Email != "operator@example.com" || !identity.EmailVerified {
		t.Fatalf("unexpected GitHub identity: %#v", identity)
	}
}

func TestVerifyDiscordExchangesCodeAndReadsUserIdentity(t *testing.T) {
	provider := store.OAuthProvider{
		ID:           "discord-provider-01",
		ProviderType: "discord",
		ClientID:     "discord-client-id",
		ClientSecret: "raw-discord-client-secret",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://discord.com/api/oauth2/token":
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected token method: %s", req.Method)
			}
			body, _ := io.ReadAll(req.Body)
			form := string(body)
			for _, want := range []string{"grant_type=authorization_code", "code=discord-code", "client_id=discord-client-id", "client_secret=raw-discord-client-secret"} {
				if !strings.Contains(form, want) {
					t.Fatalf("discord token request missing %q in %q", want, form)
				}
			}
			return jsonResponse(200, `{"access_token":"discord-access-token","token_type":"bearer","scope":"identify email"}`), nil
		case "https://discord.com/api/users/@me":
			if req.Header.Get("Authorization") != "Bearer discord-access-token" {
				t.Fatalf("discord userinfo authorization header mismatch: %q", req.Header.Get("Authorization"))
			}
			return jsonResponse(200, `{"id":"123456789012345678","email":"operator@example.com"}`), nil
		default:
			t.Fatalf("unexpected Discord OAuth URL: %s", req.URL.String())
			return jsonResponse(404, `{}`), nil
		}
	})}

	identity, err := (HTTPVerifier{Client: client}).Verify(t.Context(), VerifyRequest{Provider: provider, Code: "discord-code"})
	if err != nil {
		t.Fatalf("expected Discord identity, got error: %v", err)
	}
	if identity.ProviderID != provider.ID || identity.ProviderType != "discord" || identity.Subject != "123456789012345678" || identity.Email != "operator@example.com" {
		t.Fatalf("unexpected Discord identity: %#v", identity)
	}
}

func TestVerifyDiscordRejectsMissingSubject(t *testing.T) {
	provider := store.OAuthProvider{ID: "discord-provider-01", ProviderType: "discord", ClientID: "discord-client-id", ClientSecret: "raw-discord-client-secret", RedirectURI: "https://control.example.com/auth/oauth/callback"}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://discord.com/api/oauth2/token":
			return jsonResponse(200, `{"access_token":"discord-access-token","token_type":"bearer"}`), nil
		case "https://discord.com/api/users/@me":
			return jsonResponse(200, `{"email":"operator@example.com"}`), nil
		default:
			return jsonResponse(404, `{}`), nil
		}
	})}

	_, err := (HTTPVerifier{Client: client}).Verify(t.Context(), VerifyRequest{Provider: provider, Code: "discord-code"})
	if err == nil || !strings.Contains(err.Error(), "subject unavailable") {
		t.Fatalf("expected missing Discord subject rejection, got %v", err)
	}
}

func TestGoogleIdentityFromIDTokenPayloadAcceptsExpectedNonce(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)

	identity, err := googleIdentityFromIDTokenPayload(provider, validGooglePayload(provider, now, "expected-nonce"), "expected-nonce", now)
	if err != nil {
		t.Fatalf("expected identity, got error: %v", err)
	}
	if identity.ProviderID != provider.ID || identity.Subject != "google-subject-01" || identity.Email != "operator@example.com" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestGoogleIdentityFromIDTokenPayloadRejectsNonceMismatch(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)

	_, err := googleIdentityFromIDTokenPayload(provider, validGooglePayload(provider, now, "attacker-nonce"), "expected-nonce", now)
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected nonce rejection, got %v", err)
	}
}

func TestGoogleIdentityFromIDTokenPayloadRejectsAudienceMismatch(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)
	payload := validGooglePayload(provider, now, "expected-nonce")
	payload.Audience = "other-client-id"

	_, err := googleIdentityFromIDTokenPayload(provider, payload, "expected-nonce", now)
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("expected audience rejection, got %v", err)
	}
}

func TestGoogleIdentityFromIDTokenPayloadRejectsExpiredToken(t *testing.T) {
	provider := store.OAuthProvider{ID: "provider-1", ProviderType: "google", ClientID: "google-client-id"}
	now := time.Unix(1000, 0)
	payload := validGooglePayload(provider, now, "expected-nonce")
	payload.Expires = now.Add(-time.Second).Unix()

	_, err := googleIdentityFromIDTokenPayload(provider, payload, "expected-nonce", now)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiry rejection, got %v", err)
	}
}

func validGooglePayload(provider store.OAuthProvider, now time.Time, nonce string) *idtoken.Payload {
	return &idtoken.Payload{
		Issuer:   "https://accounts.google.com",
		Subject:  "google-subject-01",
		Audience: provider.ClientID,
		Expires:  now.Add(time.Hour).Unix(),
		Claims: map[string]interface{}{
			"email": "operator@example.com",
			"nonce": nonce,
		},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
