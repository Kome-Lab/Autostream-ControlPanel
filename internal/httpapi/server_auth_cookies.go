package httpapi

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func sessionCookieSecure() bool {
	override := strings.ToLower(strings.TrimSpace(os.Getenv("AUTOSTREAM_COOKIE_SECURE")))
	publicHTTPS := strings.HasPrefix(strings.ToLower(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL"))), "https://")
	if override == "true" || override == "1" || override == "yes" {
		return true
	}
	if override == "false" || override == "0" || override == "no" {
		return !productionEnvironment() && publicHTTPS
	}
	if productionEnvironment() {
		return true
	}
	return publicHTTPS
}

func setOAuthStateCookie(w http.ResponseWriter, state store.OAuthLoginState) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state.StateHash,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(),
		Expires:  state.ExpiresAt,
	})
}

func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func oauthStateCookieMatches(r *http.Request, state store.OAuthLoginState) bool {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cookie.Value) != "" && strings.TrimSpace(cookie.Value) == state.StateHash
}

func oauthStateTokenCookieMatches(r *http.Request, stateToken string) bool {
	stateToken = strings.TrimSpace(stateToken)
	if stateToken == "" {
		return false
	}
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cookie.Value) != "" && strings.TrimSpace(cookie.Value) == security.HashToken(stateToken)
}
