package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerLoginRoutes() {
	s.mux.HandleFunc("POST /auth/login", s.login)
	s.mux.HandleFunc("GET /auth/oauth/providers", s.listOAuthLoginProviders)
	s.mux.HandleFunc("POST /auth/oauth/{id}/start", s.startOAuthLogin)
	s.mux.HandleFunc("GET /auth/oauth/callback", s.oauthLoginRedirectCallback)
	s.mux.HandleFunc("POST /auth/oauth/callback", s.oauthLoginCallback)
}

type oauthStartRequest struct {
	RedirectAfter  string `json:"redirect_after"`
	TurnstileToken string `json:"turnstile_token"`
}

type oauthCallbackRequest struct {
	ProviderID string `json:"provider_id"`
	State      string `json:"state"`
	Code       string `json:"code"`
}

type oauthUserLinkRequest struct {
	ProviderID   string `json:"provider_id"`
	ProviderType string `json:"provider_type"`
	Subject      string `json:"subject"`
	Email        string `json:"email"`
}

func (s *Server) listOAuthLoginProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.integrations.ListOAuthProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_login_providers_failed"})
		return
	}
	out := []map[string]any{}
	for _, provider := range providers {
		if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) {
			continue
		}
		out = append(out, publicOAuthLoginProvider(provider))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) startOAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body oauthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "login"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.start", ResourceType: "oauth_provider", ResourceID: r.PathValue("id"), Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" || strings.TrimSpace(provider.RedirectURI) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	if !validOAuthRedirectURI(provider.RedirectURI, "/auth/oauth/callback") {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_redirect_uri_invalid"})
		return
	}
	oauthStartKey := loginFailureKey("oauth:start:"+provider.ID, clientIP(r))
	if !s.loginFailures.allow(oauthStartKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "oauth_start_rate_limited"})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:    provider.ID,
		ProviderType:  provider.ProviderType,
		Purpose:       "login",
		RedirectAfter: safeRedirectAfter(body.RedirectAfter),
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	s.loginFailures.record(oauthStartKey)
	authorizationURL, err := oauthAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
	})
}

func (s *Server) oauthLoginCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	var body oauthCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.finishOAuthLogin(w, r, body, false)
}

func (s *Server) oauthLoginRedirectCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	body := oauthCallbackRequest{
		ProviderID: r.URL.Query().Get("provider_id"),
		State:      r.URL.Query().Get("state"),
		Code:       r.URL.Query().Get("code"),
	}
	if s.isConnectedAccountOAuthRedirect(r, body.State) {
		s.requireAnyPermission([]string{"integrations.create", "integrations.update"}, s.oauthAccountRedirectCallback)(w, r)
		return
	}
	s.finishOAuthLogin(w, r, body, true)
}

func (s *Server) isConnectedAccountOAuthRedirect(r *http.Request, stateToken string) bool {
	if s.oauthLogin == nil || !oauthStateTokenCookieMatches(r, stateToken) {
		return false
	}
	state, err := s.oauthLogin.GetOAuthLoginState(r.Context(), stateToken)
	return err == nil && state.Purpose == "connected_account"
}

func setOAuthCallbackNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *Server) finishOAuthLogin(w http.ResponseWriter, r *http.Request, body oauthCallbackRequest, redirectOnSuccess bool) {
	if !oauthStateTokenCookieMatches(r, body.State) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	state, err := s.oauthLogin.ConsumeOAuthLoginState(r.Context(), body.State)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_state"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_consume_failed"})
		return
	}
	if !oauthStateCookieMatches(r, state) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	clearOAuthStateCookie(w)
	if strings.TrimSpace(body.ProviderID) != "" && strings.TrimSpace(body.ProviderID) != state.ProviderID {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: body.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_state_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if state.Purpose != "login" && state.Purpose != "account_link" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_purpose_mismatch", "purpose": state.Purpose}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(r.Context(), state.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientSecret) == "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: state.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_unavailable"}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_unavailable"})
		return
	}
	if err != nil {
		code := "oauth_provider_unavailable"
		if errors.Is(err, store.ErrSecretKeyRequired) {
			code = "secret_encryption_key_required"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return
	}
	identity, err := s.oauthVerifier.Verify(r.Context(), oauthlogin.VerifyRequest{Provider: provider, Code: body.Code, Nonce: state.Nonce})
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_verification_failed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "oauth_identity_verification_failed"})
		return
	}
	if identity.ProviderID != provider.ID || identity.Subject == "" || !identityAllowedForProvider(provider, identity) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_not_allowed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_identity_not_allowed"})
		return
	}
	if state.Purpose == "account_link" {
		s.finishCurrentUserOAuthLink(w, r, provider, state, identity, redirectOnSuccess)
		return
	}
	link, err := s.oauthLogin.FindOAuthUserLink(r.Context(), provider.ID, identity.Subject)
	if errors.Is(err, store.ErrNotFound) {
		user, provisioned, provisionErr := s.autoProvisionOAuthUser(r.Context(), provider, identity)
		if provisionErr != nil {
			reason := "account_not_linked"
			if provider.AutoProvision {
				reason = "auto_provision_failed"
			}
			s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": reason, "provider_type": provider.ProviderType}})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_account_not_linked"})
			return
		}
		if provisioned {
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.provision_user", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "default_role_count": len(provider.DefaultRoleIDs), "email_present": identity.Email != ""}})
		}
		s.continueOAuthLogin(w, r, user, provider, state, redirectOnSuccess)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_link_lookup_failed"})
		return
	}
	user, err := s.auth.GetUser(r.Context(), link.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_account_not_linked"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_user_lookup_failed"})
		return
	}
	if user.Status == "disabled" || user.Status == "locked" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "account_unavailable", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_unavailable"})
		return
	}
	s.continueOAuthLogin(w, r, user, provider, state, redirectOnSuccess)
}

func (s *Server) finishCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request, provider store.OAuthProvider, state store.OAuthLoginState, identity oauthlogin.Identity, redirectOnSuccess bool) {
	current, ok := s.authenticate(r)
	if !ok {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth_link.create", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "unauthorized", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
		return
	}
	if current.User.Status == "disabled" || current.User.Status == "locked" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "account_unavailable", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_unavailable"})
		return
	}
	existing, err := s.oauthLogin.FindOAuthUserLink(r.Context(), provider.ID, identity.Subject)
	if err == nil {
		if existing.UserID != current.User.ID {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "oauth_link", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_already_linked", "provider_type": provider.ProviderType}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_identity_already_linked"})
			return
		}
		s.finishOAuthLinkResponse(w, r, state, existing, redirectOnSuccess, http.StatusOK)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_link_lookup_failed"})
		return
	}
	link, err := s.oauthLogin.LinkOAuthUser(r.Context(), store.OAuthUserLink{
		UserID:       current.User.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		Subject:      identity.Subject,
		Email:        identity.Email,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_user_link_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "oauth_link", ResourceID: link.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "email_present": identity.Email != ""}})
	s.finishOAuthLinkResponse(w, r, state, link, redirectOnSuccess, http.StatusCreated)
}

func (s *Server) finishOAuthLinkResponse(w http.ResponseWriter, r *http.Request, state store.OAuthLoginState, link store.OAuthUserLink, redirectOnSuccess bool, status int) {
	if redirectOnSuccess {
		target := safeRedirectAfter(state.RedirectAfter)
		if target == "" {
			target = "/admin/account/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	writeJSON(w, status, link)
}

func (s *Server) continueOAuthLogin(w http.ResponseWriter, r *http.Request, user store.User, provider store.OAuthProvider, state store.OAuthLoginState, redirectOnSuccess bool) {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if passkeyRequiredForUser(settings, user) {
		s.rejectPasskeyRequiredLogin(w, r, user, "auth.oauth.login", map[string]any{"provider_type": provider.ProviderType})
		return
	}
	mfaConfig, mfaRequired, err := s.loginMFARequirement(r.Context(), settings, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if mfaRequired {
		if s.mfa == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
			return
		}
		if mfaConfig.Enabled {
			challenge, err := s.mfa.CreateMFAChallenge(r.Context(), user.ID, 10*time.Minute)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_challenge_failed"})
				return
			}
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "mfa_required", Metadata: map[string]any{"provider_type": provider.ProviderType}})
			if redirectOnSuccess {
				redirectOAuthMFAChallenge(w, r, challenge, state.RedirectAfter)
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_token": challenge.Token, "expires_at": challenge.ExpiresAt})
			return
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_enrollment_required", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "mfa_enrollment_required"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType}})
	if redirectOnSuccess {
		s.completeLoginRedirect(w, r, user, settings, state.RedirectAfter)
		return
	}
	s.completeLogin(w, r, user, settings)
}

func redirectOAuthMFAChallenge(w http.ResponseWriter, r *http.Request, challenge store.MFAChallenge, redirectAfter string) {
	fragment := url.Values{}
	fragment.Set("oauth_mfa_challenge", challenge.Token)
	fragment.Set("expires_at", challenge.ExpiresAt.UTC().Format(time.RFC3339Nano))
	target := "/login"
	if redirectAfter = safeRedirectAfter(redirectAfter); redirectAfter != "" {
		target += "?" + url.Values{"redirect_after": []string{redirectAfter}}.Encode()
	}
	http.Redirect(w, r, target+"#"+fragment.Encode(), http.StatusSeeOther)
}

func mfaRequiredForUser(settings store.SecuritySettings, user store.User) bool {
	return settings.MFAMode == "totp" && mfaPolicyAppliesToUser(settings, user)
}

func (s *Server) loginMFARequirement(ctx context.Context, settings store.SecuritySettings, user store.User) (store.MFAConfig, bool, error) {
	policyRequired := mfaRequiredForUser(settings, user)
	if s.mfa == nil {
		return store.MFAConfig{}, policyRequired, nil
	}
	cfg, err := s.mfa.GetMFAConfig(ctx, user.ID)
	if err != nil {
		return store.MFAConfig{}, false, err
	}
	return cfg, cfg.Enabled || policyRequired, nil
}

func passkeyRequiredForUser(settings store.SecuritySettings, user store.User) bool {
	return settings.MFAMode == "passkey" && mfaPolicyAppliesToUser(settings, user)
}

func mfaPolicyAppliesToUser(settings store.SecuritySettings, user store.User) bool {
	if settings.MFAMode == "disabled" {
		return false
	}
	requiredRoles := settings.MFARequiredRoles
	if len(requiredRoles) == 0 {
		return true
	}
	granted := map[string]bool{}
	for _, role := range user.Roles {
		granted[strings.TrimSpace(role)] = true
	}
	for _, role := range requiredRoles {
		if granted[strings.TrimSpace(role)] {
			return true
		}
	}
	return false
}

func (s *Server) rejectPasskeyRequiredLogin(w http.ResponseWriter, r *http.Request, user store.User, action string, metadata map[string]any) {
	if s.passkeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
		return
	}
	credentials, err := s.passkeys.ListPasskeyCredentialsForVerification(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_state_unavailable"})
		return
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	result := "passkey_required"
	code := "passkey_required"
	if len(credentials) == 0 {
		result = "failure"
		code = "passkey_enrollment_required"
		metadata["reason"] = code
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: action, ResourceType: "user", ResourceID: user.ID, Result: result, Metadata: metadata})
	writeJSON(w, http.StatusForbidden, map[string]string{"code": code})
}

func (s *Server) autoProvisionOAuthUser(ctx context.Context, provider store.OAuthProvider, identity oauthlogin.Identity) (store.User, bool, error) {
	if !provider.AutoProvision || len(provider.DefaultRoleIDs) == 0 || s.users == nil {
		return store.User{}, false, store.ErrNotFound
	}
	base := oauthProvisionUsername(provider.ProviderType, identity)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		username := base
		if attempt > 0 {
			username = base + "-" + strconv.Itoa(attempt+1)
		}
		user, err := s.users.CreateOAuthUser(ctx, username, identity.Email, provider.DefaultRoleIDs)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := s.oauthLogin.LinkOAuthUser(ctx, store.OAuthUserLink{
			UserID:       user.ID,
			ProviderID:   provider.ID,
			ProviderType: provider.ProviderType,
			Subject:      identity.Subject,
			Email:        identity.Email,
		}); err != nil {
			return store.User{}, false, err
		}
		return user, true, nil
	}
	if lastErr == nil {
		lastErr = store.ErrNotFound
	}
	return store.User{}, false, lastErr
}

func oauthProvisionUsername(providerType string, identity oauthlogin.Identity) string {
	if email := strings.TrimSpace(strings.ToLower(identity.Email)); email != "" {
		return safeOAuthUsername(email)
	}
	return safeOAuthUsername(providerType + "-" + identity.Subject)
}

func safeOAuthUsername(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '@' || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-_@")
	if out == "" {
		return "oauth-user"
	}
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}

func (s *Server) completeLoginRedirect(w http.ResponseWriter, r *http.Request, user store.User, settings store.SecuritySettings, redirectAfter string) {
	session, err := s.auth.CreateSession(r.Context(), user.ID, time.Duration(settings.SessionIdleTimeoutMin)*time.Minute, time.Duration(settings.SessionAbsoluteLifetimeH)*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_session_failed"})
		return
	}
	_ = s.auth.RecordLoginSuccess(r.Context(), user.ID, clientIP(r))
	s.loginFailures.clear(loginFailureKey(user.Username, clientIP(r)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: sessionCookieSecure(), Expires: session.AbsoluteExpiresAt,
	})
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	target := safeRedirectAfter(redirectAfter)
	if target == "" {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) startCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	var body oauthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" || strings.TrimSpace(provider.RedirectURI) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	if !validOAuthRedirectURI(provider.RedirectURI, "/auth/oauth/callback") {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_redirect_uri_invalid"})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:    provider.ID,
		ProviderType:  provider.ProviderType,
		Purpose:       "account_link",
		RedirectAfter: safeRedirectAfter(body.RedirectAfter),
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	authorizationURL, err := oauthAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
	})
}

func (s *Server) listUserOAuthLinks(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := s.auth.GetUser(r.Context(), userID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	links, err := s.oauthLogin.ListOAuthUserLinks(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_user_links_failed"})
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) createUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	user, err := s.auth.GetUser(r.Context(), userID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.oauth_link.create", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "manual_oauth_link_disabled"}})
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "manual_oauth_link_disabled"})
	return
}

func (s *Server) deleteUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if err := s.oauthLogin.DeleteOAuthUserLink(r.Context(), r.PathValue("link_id"), userID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_user_link_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.oauth_link.delete", ResourceType: "user", ResourceID: userID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) listCurrentUserOAuthLinks(w http.ResponseWriter, r *http.Request) {
	if s.oauthLogin == nil {
		writeJSON(w, http.StatusOK, []store.OAuthUserLink{})
		return
	}
	current := currentFromContext(r.Context())
	links, err := s.oauthLogin.ListOAuthUserLinks(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_user_links_failed"})
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) deleteCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	if s.oauthLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	current := currentFromContext(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_link_id_required"})
		return
	}
	if err := s.oauthLogin.DeleteOAuthUserLink(r.Context(), id, current.User.ID); errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.delete", ResourceType: "oauth_link", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_user_link_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.delete", ResourceType: "oauth_link", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
