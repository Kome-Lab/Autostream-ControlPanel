package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/store"
)

func publicOAuthLoginProvider(provider store.OAuthProvider) map[string]any {
	return map[string]any{
		"id":            provider.ID,
		"provider_type": provider.ProviderType,
		"name":          provider.Name,
		"scopes":        loginOAuthScopes(provider.ProviderType),
		"redirect_uri":  provider.RedirectURI,
	}
}

func supportedLoginOAuthProvider(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google", "github", "discord":
		return true
	default:
		return false
	}
}

func supportedConnectedAccountProvider(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google":
		return true
	default:
		return false
	}
}

func validateOAuthProviderRequest(body oauthProviderRequest) string {
	providerType := strings.ToLower(strings.TrimSpace(body.ProviderType))
	if !supportedLoginOAuthProvider(providerType) {
		return "invalid_oauth_provider_type"
	}
	if !validOAuthRedirectURI(body.RedirectURI, "/auth/oauth/callback") {
		return "oauth_redirect_uri_invalid"
	}
	return ""
}

func oauthProviderRequestScopes(providerType, redirectURI string, requested []string) []string {
	_ = redirectURI
	_ = requested
	return loginOAuthScopes(providerType)
}

func loginOAuthScopes(providerType string) []string {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google":
		return []string{"openid", "email", "profile"}
	case "github":
		return []string{"read:user", "user:email"}
	case "discord":
		return []string{"identify", "email"}
	default:
		return []string{}
	}
}

func oauthAccountRequestedScopes(purpose string) ([]string, string) {
	base := []string{"openid", "email", "profile"}
	switch normalizeOAuthAccountPurpose(purpose) {
	case "drive":
		return append(base, "https://www.googleapis.com/auth/drive.file"), ""
	case "youtube":
		return append(base, "https://www.googleapis.com/auth/youtube.force-ssl"), ""
	case "drive_youtube":
		return append(base, "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube.force-ssl"), ""
	default:
		return nil, "invalid_oauth_account_purpose"
	}
}

func normalizeOAuthAccountPurpose(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "drive_youtube", "both", "all", "stream_archive", "archive_youtube":
		return "drive_youtube"
	case "drive", "archive", "google_drive":
		return "drive"
	case "youtube", "youtube_live":
		return "youtube"
	default:
		return "invalid"
	}
}

func cleanRequestStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func oauthScopesContainConnectedAccountAccess(scopes []string) bool {
	return store.OAuthAccountPurposeFromScopes(scopes) != store.OAuthAccountPurposeUnknown
}

func sameStringSet(left, right []string) bool {
	seen := map[string]int{}
	for _, item := range left {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		seen[value]++
	}
	for _, item := range right {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		seen[value]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func (s *Server) validateDriveDestinationRequest(ctx context.Context, body driveDestinationRequest) (string, int) {
	if strings.EqualFold(strings.TrimSpace(body.AuthMode), "oauth2") {
		account, err := s.integrations.GetOAuthAccount(ctx, body.OAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return "oauth_account_not_found", http.StatusNotFound
		}
		if err != nil {
			return "get_oauth_account_failed", http.StatusInternalServerError
		}
		if !strings.EqualFold(account.ProviderType, "google") {
			return "drive_destination_oauth_account_not_google", http.StatusBadRequest
		}
		if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
			return "drive_destination_drive_scope_required", http.StatusBadRequest
		}
	}
	return "", 0
}

func normalizeDriveDestinationAPIRequest(body *driveDestinationRequest) (string, int) {
	authMode := strings.TrimSpace(body.AuthMode)
	if authMode != "" && !strings.EqualFold(authMode, "oauth2") {
		return "drive_destination_auth_mode_unsupported", http.StatusBadRequest
	}
	body.AuthMode = "oauth2"
	return "", 0
}

func (s *Server) validateYouTubeOutputOAuthAccount(ctx context.Context, config map[string]any) (string, int) {
	mode := normalizedYouTubeOutputMode(configString(config, "mode"))
	if mode != "live_api" && mode != "live_api_dry_run" && mode != "live_api_relay_static" {
		return "", 0
	}
	accountID := firstNonEmpty(configString(config, "oauth_account_id"), configString(config, "youtube_oauth_account_id"))
	account, err := s.integrations.GetOAuthAccount(ctx, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return "oauth_account_not_found", http.StatusNotFound
	}
	if err != nil {
		return "get_oauth_account_failed", http.StatusInternalServerError
	}
	if !strings.EqualFold(account.ProviderType, "google") {
		return "youtube_output_oauth_account_not_google", http.StatusBadRequest
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return "youtube_output_youtube_scope_required", http.StatusBadRequest
	}
	return "", 0
}

func oauthAuthorizationURL(provider store.OAuthProvider, state store.OAuthLoginState) (string, error) {
	var endpoint string
	values := url.Values{}
	values.Set("client_id", provider.ClientID)
	values.Set("redirect_uri", provider.RedirectURI)
	values.Set("response_type", "code")
	values.Set("state", state.StateToken)
	scopes := loginOAuthScopes(provider.ProviderType)
	switch strings.ToLower(strings.TrimSpace(provider.ProviderType)) {
	case "google":
		endpoint = "https://accounts.google.com/o/oauth2/v2/auth"
		values.Set("nonce", state.Nonce)
	case "github":
		endpoint = "https://github.com/login/oauth/authorize"
	case "discord":
		endpoint = "https://discord.com/oauth2/authorize"
	default:
		return "", errors.New("unsupported oauth provider")
	}
	values.Set("scope", strings.Join(scopes, " "))
	return endpoint + "?" + values.Encode(), nil
}

func oauthConnectedAccountAuthorizationURL(provider store.OAuthProvider, state store.OAuthLoginState) (string, error) {
	providerType := strings.ToLower(strings.TrimSpace(provider.ProviderType))
	if providerType != "google" {
		return "", errors.New("unsupported connected account provider")
	}
	values := url.Values{}
	values.Set("client_id", provider.ClientID)
	values.Set("redirect_uri", provider.RedirectURI)
	values.Set("response_type", "code")
	values.Set("state", state.StateToken)
	values.Set("nonce", state.Nonce)
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	scopes := state.RequestedScopes
	if len(scopes) == 0 {
		scopes, _ = oauthAccountRequestedScopes("")
	}
	values.Set("scope", strings.Join(scopes, " "))
	return "https://accounts.google.com/o/oauth2/v2/auth?" + values.Encode(), nil
}

func connectedOAuthRedirectURI(r *http.Request) string {
	base := strings.TrimRight(panelBaseURL(r), "/")
	if base == "" {
		return ""
	}
	return base + "/integrations/oauth-accounts/callback"
}

func connectedAccountOAuthRedirectURI(r *http.Request, provider store.OAuthProvider) (string, string) {
	redirectURI := strings.TrimSpace(provider.RedirectURI)
	if validOAuthRedirectURI(redirectURI, "/auth/oauth/callback") || validOAuthRedirectURI(redirectURI, "/integrations/oauth-accounts/callback") {
		return redirectURI, ""
	}
	if redirectURI == "" {
		legacyRedirectURI := connectedOAuthRedirectURI(r)
		if validOAuthRedirectURI(legacyRedirectURI, "/integrations/oauth-accounts/callback") {
			return legacyRedirectURI, ""
		}
	}
	return "", "oauth_connected_account_redirect_uri_unavailable"
}

func validOAuthRedirectURI(raw, expectedPath string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}
	if parsed.User != nil || parsed.Path != expectedPath || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL"))
	if publicURL == "" {
		return isLocalOAuthRedirectHost(parsed.Hostname())
	}
	publicParsed, err := url.Parse(publicURL)
	if err != nil || publicParsed.Scheme == "" || publicParsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, publicParsed.Scheme) && strings.EqualFold(parsed.Host, publicParsed.Host)
}

func isLocalOAuthRedirectHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func safeRedirectAfter(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || unsafeRedirectCharacters(value) || unsafeRedirectCharacters(decoded) {
		return ""
	}
	return value
}

func unsafeRedirectCharacters(value string) bool {
	for _, char := range value {
		if char < 0x20 || (char >= 0x7f && char <= 0x9f) || char == '\\' {
			return true
		}
	}
	return false
}

func emailAllowedForProvider(email string, allowedDomains []string) bool {
	if len(allowedDomains) == 0 {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	for _, allowed := range allowedDomains {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if domain == allowed {
			return true
		}
	}
	return false
}

func identityAllowedForProvider(provider store.OAuthProvider, identity oauthlogin.Identity) bool {
	if len(provider.AllowedDomains) == 0 {
		return true
	}
	if !identity.EmailVerified {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(provider.ProviderType), "google") && strings.TrimSpace(identity.HostedDomain) != "" {
		return domainAllowedForProvider(identity.HostedDomain, provider.AllowedDomains)
	}
	return emailAllowedForProvider(identity.Email, provider.AllowedDomains)
}

func domainAllowedForProvider(domain string, allowedDomains []string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}
	for _, allowed := range allowedDomains {
		if domain == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}
