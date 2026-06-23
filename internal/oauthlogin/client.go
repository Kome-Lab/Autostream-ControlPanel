package oauthlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"google.golang.org/api/idtoken"
)

type Identity struct {
	ProviderID   string
	ProviderType string
	Subject      string
	Email        string
}

type ConnectedAccount struct {
	Identity     Identity
	RefreshToken string
	Scopes       []string
}

type VerifyRequest struct {
	Provider store.OAuthProvider
	Code     string
	Nonce    string
}

type ConnectRequest struct {
	Provider store.OAuthProvider
	Code     string
	Nonce    string
}

type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) (Identity, error)
}

type Connector interface {
	Connect(ctx context.Context, req ConnectRequest) (ConnectedAccount, error)
}

type HTTPVerifier struct {
	Client *http.Client
}

func (v HTTPVerifier) Verify(ctx context.Context, req VerifyRequest) (Identity, error) {
	providerType := strings.ToLower(strings.TrimSpace(req.Provider.ProviderType))
	if strings.TrimSpace(req.Code) == "" {
		return Identity{}, errors.New("oauth code is required")
	}
	switch providerType {
	case "google":
		return v.verifyGoogle(ctx, req)
	case "github":
		return v.verifyGitHub(ctx, req)
	case "discord":
		return v.verifyDiscord(ctx, req)
	default:
		return Identity{}, errors.New("unsupported oauth provider")
	}
}

func (v HTTPVerifier) Connect(ctx context.Context, req ConnectRequest) (ConnectedAccount, error) {
	providerType := strings.ToLower(strings.TrimSpace(req.Provider.ProviderType))
	if strings.TrimSpace(req.Code) == "" {
		return ConnectedAccount{}, errors.New("oauth code is required")
	}
	switch providerType {
	case "google":
		return v.connectGoogle(ctx, req)
	default:
		return ConnectedAccount{}, errors.New("oauth provider does not support connected accounts")
	}
}

func (v HTTPVerifier) verifyGoogle(ctx context.Context, req VerifyRequest) (Identity, error) {
	token, err := v.exchange(ctx, "https://oauth2.googleapis.com/token", req.Provider, req.Code)
	if err != nil {
		return Identity{}, err
	}
	return v.googleIdentityFromIDToken(ctx, req.Provider, token.IDToken, req.Nonce, time.Now())
}

func (v HTTPVerifier) connectGoogle(ctx context.Context, req ConnectRequest) (ConnectedAccount, error) {
	token, err := v.exchange(ctx, "https://oauth2.googleapis.com/token", req.Provider, req.Code)
	if err != nil {
		return ConnectedAccount{}, err
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return ConnectedAccount{}, errors.New("oauth refresh token unavailable")
	}
	identity, err := v.googleIdentityFromIDToken(ctx, req.Provider, token.IDToken, req.Nonce, time.Now())
	if err != nil {
		if strings.TrimSpace(token.IDToken) != "" {
			return ConnectedAccount{}, err
		}
		var body struct {
			Sub   string `json:"sub"`
			Email string `json:"email"`
		}
		if err := v.getJSON(ctx, "https://openidconnect.googleapis.com/v1/userinfo", token.AccessToken, &body); err != nil {
			return ConnectedAccount{}, err
		}
		identity, err = normalizeIdentity(req.Provider, body.Sub, body.Email)
		if err != nil {
			return ConnectedAccount{}, err
		}
	}
	return ConnectedAccount{
		Identity:     identity,
		RefreshToken: token.RefreshToken,
		Scopes:       splitScope(token.Scope),
	}, nil
}

func (v HTTPVerifier) verifyGitHub(ctx context.Context, req VerifyRequest) (Identity, error) {
	token, err := v.exchange(ctx, "https://github.com/login/oauth/access_token", req.Provider, req.Code)
	if err != nil {
		return Identity{}, err
	}
	var body struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Email string `json:"email"`
	}
	if err := v.getJSON(ctx, "https://api.github.com/user", token.AccessToken, &body); err != nil {
		return Identity{}, err
	}
	subject := fmt.Sprintf("%d", body.ID)
	if body.ID == 0 {
		subject = body.Login
	}
	return normalizeIdentity(req.Provider, subject, body.Email)
}

func (v HTTPVerifier) verifyDiscord(ctx context.Context, req VerifyRequest) (Identity, error) {
	token, err := v.exchange(ctx, "https://discord.com/api/oauth2/token", req.Provider, req.Code)
	if err != nil {
		return Identity{}, err
	}
	var body struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := v.getJSON(ctx, "https://discord.com/api/users/@me", token.AccessToken, &body); err != nil {
		return Identity{}, err
	}
	return normalizeIdentity(req.Provider, body.ID, body.Email)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

func (v HTTPVerifier) exchange(ctx context.Context, endpoint string, provider store.OAuthProvider, code string) (tokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("client_id", provider.ClientID)
	values.Set("client_secret", provider.ClientSecret)
	values.Set("redirect_uri", provider.RedirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var body tokenResponse
	if err := v.doJSON(req, &body); err != nil {
		return tokenResponse{}, err
	}
	if strings.TrimSpace(body.AccessToken) == "" {
		return tokenResponse{}, errors.New("oauth access token unavailable")
	}
	return body, nil
}

func (v HTTPVerifier) getJSON(ctx context.Context, endpoint, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	return v.doJSON(req, out)
}

func (v HTTPVerifier) doJSON(req *http.Request, out any) error {
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("oauth upstream status %d", res.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

func normalizeIdentity(provider store.OAuthProvider, subject, email string) (Identity, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Identity{}, errors.New("oauth subject unavailable")
	}
	return Identity{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		Subject:      subject,
		Email:        strings.TrimSpace(email),
	}, nil
}

var validateGoogleIDToken = func(ctx context.Context, rawIDToken, audience string, client *http.Client) (*idtoken.Payload, error) {
	opts := []idtoken.ClientOption{}
	if client != nil {
		opts = append(opts, idtoken.WithHTTPClient(client))
	}
	validator, err := idtoken.NewValidator(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return validator.Validate(ctx, rawIDToken, audience)
}

func (v HTTPVerifier) googleIdentityFromIDToken(ctx context.Context, provider store.OAuthProvider, rawIDToken, expectedNonce string, now time.Time) (Identity, error) {
	rawIDToken = strings.TrimSpace(rawIDToken)
	if rawIDToken == "" {
		return Identity{}, errors.New("oauth id token unavailable")
	}
	payload, err := validateGoogleIDToken(ctx, rawIDToken, strings.TrimSpace(provider.ClientID), v.Client)
	if err != nil {
		return Identity{}, fmt.Errorf("oauth id token validation failed: %w", err)
	}
	return googleIdentityFromIDTokenPayload(provider, payload, expectedNonce, now)
}

func googleIdentityFromIDTokenPayload(provider store.OAuthProvider, payload *idtoken.Payload, expectedNonce string, now time.Time) (Identity, error) {
	if payload == nil {
		return Identity{}, errors.New("oauth id token payload unavailable")
	}
	if payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com" {
		return Identity{}, errors.New("oauth id token issuer invalid")
	}
	if strings.TrimSpace(payload.Audience) != strings.TrimSpace(provider.ClientID) {
		return Identity{}, errors.New("oauth id token audience invalid")
	}
	if payload.Expires <= now.Unix() {
		return Identity{}, errors.New("oauth id token expired")
	}
	nonce, _ := payload.Claims["nonce"].(string)
	if strings.TrimSpace(expectedNonce) != "" && strings.TrimSpace(nonce) != strings.TrimSpace(expectedNonce) {
		return Identity{}, errors.New("oauth id token nonce invalid")
	}
	email, _ := payload.Claims["email"].(string)
	return normalizeIdentity(provider, payload.Subject, email)
}

func splitScope(value string) []string {
	items := strings.Fields(strings.TrimSpace(value))
	if len(items) == 0 {
		return nil
	}
	return items
}
