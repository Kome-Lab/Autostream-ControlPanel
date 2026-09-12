package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerIntegrationRoutes() {
	s.mux.HandleFunc("GET /integrations/oauth-providers", s.requirePermission("integrations.read", s.listOAuthProviders))
	s.mux.HandleFunc("POST /integrations/oauth-providers", s.requirePermission("integrations.create", s.createOAuthProvider))
	s.mux.HandleFunc("GET /integrations/oauth-providers/{id}", s.requirePermission("integrations.read", s.getOAuthProvider))
	s.mux.HandleFunc("PUT /integrations/oauth-providers/{id}", s.requirePermission("integrations.update", s.updateOAuthProvider))
	s.mux.HandleFunc("DELETE /integrations/oauth-providers/{id}", s.requirePermission("integrations.delete", s.deleteOAuthProvider))
	s.mux.HandleFunc("GET /integrations/oauth-accounts", s.requirePermission("integrations.read", s.listOAuthAccounts))
	s.mux.HandleFunc("POST /integrations/oauth-accounts/start", s.requireAnyPermission([]string{"integrations.create", "integrations.update"}, s.startOAuthAccountConnection))
	s.mux.HandleFunc("GET /integrations/oauth-accounts/callback", s.requireAnyPermission([]string{"integrations.create", "integrations.update"}, s.oauthAccountRedirectCallback))
	s.mux.HandleFunc("POST /integrations/oauth-accounts/callback", s.requireAnyPermission([]string{"integrations.create", "integrations.update"}, s.oauthAccountCallback))
	s.mux.HandleFunc("GET /integrations/oauth-accounts/{id}", s.requirePermission("integrations.read", s.getOAuthAccount))
	s.mux.HandleFunc("PUT /integrations/oauth-accounts/{id}", s.requirePermission("integrations.update", s.updateOAuthAccount))
	s.mux.HandleFunc("DELETE /integrations/oauth-accounts/{id}", s.requirePermission("integrations.delete", s.deleteOAuthAccount))
	s.mux.HandleFunc("GET /archive/destinations", s.requirePermission("integrations.read", s.listDriveDestinations))
	s.mux.HandleFunc("POST /archive/destinations", s.requirePermission("integrations.create", s.createDriveDestination))
	s.mux.HandleFunc("GET /archive/destinations/{id}", s.requirePermission("integrations.read", s.getDriveDestination))
	s.mux.HandleFunc("PUT /archive/destinations/{id}", s.requirePermission("integrations.update", s.updateDriveDestination))
	s.mux.HandleFunc("DELETE /archive/destinations/{id}", s.requirePermission("integrations.delete", s.deleteDriveDestination))
}

type oauthProviderRequest struct {
	ProviderType   string   `json:"provider_type"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret"`
	Scopes         []string `json:"scopes"`
	AllowedDomains []string `json:"allowed_domains"`
	AutoProvision  bool     `json:"auto_provision"`
	DefaultRoleIDs []string `json:"default_role_ids"`
	RedirectURI    string   `json:"redirect_uri"`
}

type oauthAccountRequest struct {
	AccountLabel string `json:"account_label"`
}

type oauthAccountStartRequest struct {
	ProviderID     string `json:"provider_id"`
	OAuthAccountID string `json:"oauth_account_id"`
	AccountLabel   string `json:"account_label"`
	AccountPurpose string `json:"account_purpose"`
	RedirectAfter  string `json:"redirect_after"`
}

type oauthAccountCallbackRequest struct {
	ProviderID   string `json:"provider_id"`
	State        string `json:"state"`
	Code         string `json:"code"`
	AccountLabel string `json:"account_label"`
}

type driveDestinationRequest struct {
	Name           string `json:"name"`
	AuthMode       string `json:"auth_mode"`
	OAuthAccountID string `json:"oauth_account_id"`
	FolderID       string `json:"folder_id"`
	SharedDrive    bool   `json:"shared_drive"`
}

func (s *Server) listOAuthProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.integrations.ListOAuthProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_providers_failed"})
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (s *Server) createOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var body oauthProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Scopes = oauthProviderRequestScopes(body.ProviderType, body.RedirectURI, body.Scopes)
	if code := validateOAuthProviderRequest(body); code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.validateOAuthProviderRolePolicy(w, r, body.AutoProvision, body.DefaultRoleIDs) {
		return
	}
	provider, err := s.integrations.CreateOAuthProvider(r.Context(), store.OAuthProvider{
		ProviderType: body.ProviderType, Name: body.Name, Enabled: body.Enabled, ClientID: body.ClientID, ClientSecret: body.ClientSecret, Scopes: body.Scopes, AllowedDomains: body.AllowedDomains, AutoProvision: body.AutoProvision, DefaultRoleIDs: body.DefaultRoleIDs, RedirectURI: body.RedirectURI,
	})
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.create", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "client_secret_configured": provider.ClientSecretConfigured, "auto_provision": provider.AutoProvision, "default_role_count": len(provider.DefaultRoleIDs)}})
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) getOAuthProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) updateOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var body oauthProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Scopes = oauthProviderRequestScopes(body.ProviderType, body.RedirectURI, body.Scopes)
	if code := validateOAuthProviderRequest(body); code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.validateOAuthProviderRolePolicy(w, r, body.AutoProvision, body.DefaultRoleIDs) {
		return
	}
	provider, err := s.integrations.UpdateOAuthProvider(r.Context(), store.OAuthProvider{
		ID: r.PathValue("id"), ProviderType: body.ProviderType, Name: body.Name, Enabled: body.Enabled, ClientID: body.ClientID, ClientSecret: body.ClientSecret, Scopes: body.Scopes, AllowedDomains: body.AllowedDomains, AutoProvision: body.AutoProvision, DefaultRoleIDs: body.DefaultRoleIDs, RedirectURI: body.RedirectURI,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.update", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "client_secret_configured": provider.ClientSecretConfigured, "auto_provision": provider.AutoProvision, "default_role_count": len(provider.DefaultRoleIDs)}})
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) validateOAuthProviderRolePolicy(w http.ResponseWriter, r *http.Request, autoProvision bool, roleIDs []string) bool {
	if autoProvision && len(roleIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_default_role_required"})
		return false
	}
	if len(roleIDs) == 0 {
		return true
	}
	if !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_permission"})
		return false
	}
	if err := s.validateRoleAssignments(r.Context(), roleIDs); err != nil {
		writeRoleAssignmentError(w, err)
		return false
	}
	return true
}

func (s *Server) deleteOAuthProvider(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.oauthProviderDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_provider_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.delete", ResourceType: "oauth_provider", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "oauth_provider_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "oauth_provider_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteOAuthProvider(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.delete", ResourceType: "oauth_provider", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) oauthProviderDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"oauth_accounts": 0, "oauth_user_links": 0}
	accounts, err := s.integrations.ListOAuthAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.ProviderID) == id {
			refs["oauth_accounts"] = refs["oauth_accounts"].(int) + 1
		}
	}
	if s.users != nil && s.oauthLogin != nil {
		users, err := s.users.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			links, err := s.oauthLogin.ListOAuthUserLinks(ctx, user.ID)
			if err != nil {
				return nil, err
			}
			for _, link := range links {
				if strings.TrimSpace(link.ProviderID) == id {
					refs["oauth_user_links"] = refs["oauth_user_links"].(int) + 1
				}
			}
		}
	}
	return refs, nil
}

func (s *Server) startOAuthAccountConnection(w http.ResponseWriter, r *http.Request) {
	var body oauthAccountStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	targetAccountID := strings.TrimSpace(body.OAuthAccountID)
	if targetAccountID != "" {
		account, err := s.integrations.GetOAuthAccount(r.Context(), targetAccountID)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "oauth_account_not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
			return
		}
		if strings.TrimSpace(body.ProviderID) == "" {
			body.ProviderID = account.ProviderID
		} else if strings.TrimSpace(body.ProviderID) != account.ProviderID {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_account_provider_mismatch"})
			return
		}
		if strings.TrimSpace(body.AccountPurpose) == "" {
			body.AccountPurpose = account.AccountPurpose
		}
	}
	requiredPermission := "integrations.create"
	if targetAccountID != "" {
		requiredPermission = "integrations.update"
	}
	if !security.HasPermission(currentFromContext(r.Context()).Permissions, requiredPermission) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), body.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "oauth_provider_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedConnectedAccountProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	redirectURI, redirectCode := connectedAccountOAuthRedirectURI(r, provider)
	if redirectCode != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": redirectCode})
		return
	}
	requestedScopes, code := oauthAccountRequestedScopes(body.AccountPurpose)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:      provider.ID,
		ProviderType:    provider.ProviderType,
		Purpose:         "connected_account",
		RedirectAfter:   safeRedirectAfter(body.RedirectAfter),
		AccountLabel:    strings.TrimSpace(body.AccountLabel),
		TargetAccountID: targetAccountID,
		RequestedScopes: requestedScopes,
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	provider.RedirectURI = redirectURI
	authorizationURL, err := oauthConnectedAccountAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
		"account_label":     strings.TrimSpace(body.AccountLabel),
		"account_purpose":   store.OAuthAccountPurposeFromScopes(state.RequestedScopes),
		"relink":            targetAccountID != "",
		"scopes":            state.RequestedScopes,
	})
}

func (s *Server) oauthAccountCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	var body oauthAccountCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.finishOAuthAccountConnection(w, r, body, false)
}

func (s *Server) oauthAccountRedirectCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	body := oauthAccountCallbackRequest{
		ProviderID:   r.URL.Query().Get("provider_id"),
		State:        r.URL.Query().Get("state"),
		Code:         r.URL.Query().Get("code"),
		AccountLabel: r.URL.Query().Get("account_label"),
	}
	s.finishOAuthAccountConnection(w, r, body, true)
}

func (s *Server) finishOAuthAccountConnection(w http.ResponseWriter, r *http.Request, body oauthAccountCallbackRequest, redirectOnSuccess bool) {
	if !oauthStateTokenCookieMatches(r, body.State) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	state, err := s.oauthLogin.ConsumeOAuthLoginState(r.Context(), body.State)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_state"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_consume_failed"})
		return
	}
	if !oauthStateCookieMatches(r, state) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	clearOAuthStateCookie(w)
	if strings.TrimSpace(body.ProviderID) != "" && strings.TrimSpace(body.ProviderID) != state.ProviderID {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: body.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_state_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if state.Purpose != "connected_account" {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_purpose_mismatch", "purpose": state.Purpose}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	requiredPermission := "integrations.create"
	if strings.TrimSpace(state.TargetAccountID) != "" {
		requiredPermission = "integrations.update"
	}
	if !security.HasPermission(currentFromContext(r.Context()).Permissions, requiredPermission) {
		auditAction := "integrations.oauth_account.connect"
		if strings.TrimSpace(state.TargetAccountID) != "" {
			auditAction = "integrations.oauth_account.relink"
		}
		s.writeAudit(r, store.AuditEvent{Action: auditAction, ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "permission_denied", "target_account_id_bound": strings.TrimSpace(state.TargetAccountID) != ""}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(r.Context(), state.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientSecret) == "" {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: state.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_unavailable"}})
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
	if !supportedConnectedAccountProvider(provider.ProviderType) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	redirectURI, redirectCode := connectedAccountOAuthRedirectURI(r, provider)
	if redirectCode != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": redirectCode})
		return
	}
	provider.RedirectURI = redirectURI
	connected, err := s.oauthConnector.Connect(r.Context(), oauthlogin.ConnectRequest{Provider: provider, Code: body.Code, Nonce: state.Nonce})
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_connect_failed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "oauth_connect_failed"})
		return
	}
	if connected.Identity.ProviderID != provider.ID || connected.Identity.Subject == "" || !identityAllowedForProvider(provider, connected.Identity) {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_not_allowed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_identity_not_allowed"})
		return
	}
	scopes := cleanRequestStringSlice(connected.Scopes)
	if len(scopes) == 0 {
		scopes = state.RequestedScopes
	}
	if !oauthScopesContainConnectedAccountAccess(scopes) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_connected_account_scope_required"})
		return
	}
	targetAccountID := strings.TrimSpace(state.TargetAccountID)
	relinked := targetAccountID != ""
	var account store.OAuthAccount
	if relinked {
		existing, getErr := s.integrations.GetOAuthAccount(r.Context(), targetAccountID)
		if errors.Is(getErr, store.ErrNotFound) {
			s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.relink", ResourceType: "oauth_account", ResourceID: targetAccountID, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_not_found"}})
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "oauth_account_not_found"})
			return
		}
		if getErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
			return
		}
		if existing.ProviderID != provider.ID || !strings.EqualFold(existing.ProviderType, provider.ProviderType) {
			s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.relink", ResourceType: "oauth_account", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_provider_mismatch"}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_account_provider_mismatch"})
			return
		}
		if strings.TrimSpace(existing.Subject) != "" && existing.Subject != connected.Identity.Subject {
			s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.relink", ResourceType: "oauth_account", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_identity_mismatch", "provider_type": existing.ProviderType}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_account_identity_mismatch"})
			return
		}
		if strings.TrimSpace(connected.RefreshToken) == "" {
			s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.relink", ResourceType: "oauth_account", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_refresh_token_missing"}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_refresh_token_missing"})
			return
		}
		account, err = s.integrations.UpdateOAuthAccount(r.Context(), store.OAuthAccount{
			ID:           existing.ID,
			ProviderID:   existing.ProviderID,
			ProviderType: existing.ProviderType,
			AccountLabel: existing.AccountLabel,
			Subject:      connected.Identity.Subject,
			Email:        connected.Identity.Email,
			Scopes:       scopes,
			RefreshToken: connected.RefreshToken,
		})
	} else {
		label := strings.TrimSpace(body.AccountLabel)
		if label == "" {
			label = strings.TrimSpace(state.AccountLabel)
		}
		if label == "" || strings.EqualFold(label, strings.TrimSpace(connected.Identity.Email)) {
			label = defaultOAuthAccountLabel(provider)
		}
		account, err = s.integrations.CreateOAuthAccount(r.Context(), store.OAuthAccount{
			ProviderID:   provider.ID,
			ProviderType: provider.ProviderType,
			AccountLabel: label,
			Subject:      connected.Identity.Subject,
			Email:        connected.Identity.Email,
			Scopes:       scopes,
			RefreshToken: connected.RefreshToken,
		})
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_account_failed"})
		return
	}
	current := currentFromContext(r.Context())
	auditAction := "integrations.oauth_account.connect"
	if relinked {
		auditAction = "integrations.oauth_account.relink"
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: auditAction, ResourceType: "oauth_account", ResourceID: account.ID, Result: "success", Metadata: map[string]any{"provider_type": account.ProviderType, "account_purpose": account.AccountPurpose, "refresh_token_configured": account.RefreshTokenConfigured, "relinked": relinked}})
	if redirectOnSuccess {
		target := safeRedirectAfter(state.RedirectAfter)
		if target == "" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if relinked {
		writeJSON(w, http.StatusOK, account)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func defaultOAuthAccountLabel(provider store.OAuthProvider) string {
	if providerName := strings.TrimSpace(provider.Name); providerName != "" {
		return providerName
	}
	switch strings.TrimSpace(strings.ToLower(provider.ProviderType)) {
	case "google":
		return "Google接続アカウント"
	case "github":
		return "GitHub接続アカウント"
	case "discord":
		return "Discord接続アカウント"
	default:
		return "OAuth接続アカウント"
	}
}

func (s *Server) listOAuthAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.integrations.ListOAuthAccounts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_accounts_failed"})
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) getOAuthAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.integrations.GetOAuthAccount(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) updateOAuthAccount(w http.ResponseWriter, r *http.Request) {
	var body oauthAccountRequest
	if err := decodeSingleJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	existing, err := s.integrations.GetOAuthAccount(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
		return
	}
	label := strings.TrimSpace(body.AccountLabel)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_account_label_required"})
		return
	}
	account, err := s.integrations.UpdateOAuthAccount(r.Context(), store.OAuthAccount{
		ID: existing.ID, ProviderID: existing.ProviderID, ProviderType: existing.ProviderType, AccountLabel: label, Subject: existing.Subject, Email: existing.Email, Scopes: existing.Scopes,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_oauth_account_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.update", ResourceType: "oauth_account", ResourceID: account.ID, Result: "success", Metadata: map[string]any{"provider_type": account.ProviderType, "identity_locked": true, "refresh_token_configured": account.RefreshTokenConfigured}})
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) deleteOAuthAccount(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.oauthAccountDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_account_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.delete", ResourceType: "oauth_account", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "oauth_account_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteOAuthAccount(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_account_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.delete", ResourceType: "oauth_account", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) oauthAccountDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"drive_destinations": 0, "youtube_outputs": 0, "stream_archive_settings": 0, "stream_youtube_runtimes": 0}
	destinations, err := s.integrations.ListDriveDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for _, destination := range destinations {
		if strings.TrimSpace(destination.OAuthAccountID) == id {
			refs["drive_destinations"] = refs["drive_destinations"].(int) + 1
		}
	}
	outputs, err := s.profiles.ListProfiles(ctx, store.ProfileYouTubeOutput)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if firstNonEmpty(configString(output.Config, "oauth_account_id"), configString(output.Config, "youtube_oauth_account_id")) == id {
			refs["youtube_outputs"] = refs["youtube_outputs"].(int) + 1
		}
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, stream := range streams {
		if strings.TrimSpace(stream.ArchiveOAuthAccountID) == id {
			refs["stream_archive_settings"] = refs["stream_archive_settings"].(int) + 1
		}
	}
	if runtimes, ok := s.streams.(store.StreamYouTubeRuntimeStore); ok {
		items, err := runtimes.ListStreamYouTubeRuntimes(ctx)
		if err != nil {
			return nil, err
		}
		for _, runtime := range items {
			if strings.TrimSpace(runtime.OAuthAccountID) == id {
				refs["stream_youtube_runtimes"] = refs["stream_youtube_runtimes"].(int) + 1
			}
		}
	}
	return refs, nil
}

func oauthReferenceCount(refs map[string]any) int {
	total := 0
	for _, value := range refs {
		if count, ok := value.(int); ok {
			total += count
		}
	}
	return total
}

func (s *Server) listDriveDestinations(w http.ResponseWriter, r *http.Request) {
	destinations, err := s.integrations.ListDriveDestinations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_drive_destinations_failed"})
		return
	}
	writeJSON(w, http.StatusOK, destinations)
}

func (s *Server) createDriveDestination(w http.ResponseWriter, r *http.Request) {
	var body driveDestinationRequest
	if err := decodeSingleJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if code, status := normalizeDriveDestinationAPIRequest(&body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if code, status := s.validateDriveDestinationRequest(r.Context(), body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	destination, err := s.integrations.CreateDriveDestination(r.Context(), store.DriveDestination{
		Name: body.Name, AuthMode: body.AuthMode, OAuthAccountID: body.OAuthAccountID, FolderID: body.FolderID, SharedDrive: body.SharedDrive,
	})
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.create", ResourceType: "drive_destination", ResourceID: destination.ID, Result: "success", Metadata: map[string]any{"auth_mode": destination.AuthMode, "shared_drive": destination.SharedDrive, "folder_id_configured": destination.FolderIDConfigured}})
	writeJSON(w, http.StatusCreated, destination)
}

func (s *Server) getDriveDestination(w http.ResponseWriter, r *http.Request) {
	destination, err := s.integrations.GetDriveDestination(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_drive_destination_failed"})
		return
	}
	writeJSON(w, http.StatusOK, destination)
}

func (s *Server) updateDriveDestination(w http.ResponseWriter, r *http.Request) {
	var body driveDestinationRequest
	if err := decodeSingleJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if code, status := normalizeDriveDestinationAPIRequest(&body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if code, status := s.validateDriveDestinationRequest(r.Context(), body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	destination, err := s.integrations.UpdateDriveDestination(r.Context(), store.DriveDestination{
		ID: r.PathValue("id"), Name: body.Name, AuthMode: body.AuthMode, OAuthAccountID: body.OAuthAccountID, FolderID: body.FolderID, SharedDrive: body.SharedDrive,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.update", ResourceType: "drive_destination", ResourceID: destination.ID, Result: "success", Metadata: map[string]any{"auth_mode": destination.AuthMode, "shared_drive": destination.SharedDrive, "folder_id_configured": destination.FolderIDConfigured}})
	writeJSON(w, http.StatusOK, destination)
}

func (s *Server) deleteDriveDestination(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.driveDestinationDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_drive_destination_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.delete", ResourceType: "drive_destination", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "drive_destination_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "drive_destination_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteDriveDestination(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.delete", ResourceType: "drive_destination", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) driveDestinationDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"stream_archive_settings": 0, "archive_profiles": 0}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, stream := range streams {
		if strings.TrimSpace(stream.ArchiveDriveDestinationID) == id {
			refs["stream_archive_settings"] = refs["stream_archive_settings"].(int) + 1
		}
	}
	profiles, err := s.profiles.ListProfiles(ctx, store.ProfileArchive)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if strings.TrimSpace(configString(profile.Config, "drive_destination_id")) == id {
			refs["archive_profiles"] = refs["archive_profiles"].(int) + 1
		}
	}
	return refs, nil
}
