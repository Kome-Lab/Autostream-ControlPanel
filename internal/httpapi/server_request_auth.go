package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type currentUser struct {
	User        store.User
	Session     store.Session
	Permissions []string
}

type currentUserKey struct{}

func (s *Server) requirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isOAuthCallbackPath(r.URL.Path) {
			setOAuthCallbackNoStoreHeaders(w)
		}
		current, ok := s.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		if current.User.Status == "pending_password_change" && !isPasswordChangeAllowedPath(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "password_change_required"})
			return
		}
		if permission != "" && !security.HasPermission(current.Permissions, permission) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
			return
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) requireAnyPermission(permissions []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isOAuthCallbackPath(r.URL.Path) {
			setOAuthCallbackNoStoreHeaders(w)
		}
		current, ok := s.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		if current.User.Status == "pending_password_change" && !isPasswordChangeAllowedPath(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "password_change_required"})
			return
		}
		allowed := len(permissions) == 0
		for _, permission := range permissions {
			if permission != "" && security.HasPermission(current.Permissions, permission) {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
			return
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) authenticate(r *http.Request) (currentUser, bool) {
	if s.auth == nil {
		return currentUser{}, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return currentUser{}, false
	}
	session, err := s.auth.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return currentUser{}, false
	}
	user, err := s.auth.GetUser(r.Context(), session.UserID)
	if err != nil || user.Status == "disabled" || user.Status == "locked" {
		return currentUser{}, false
	}
	permissions, err := s.auth.GetUserPermissions(r.Context(), user.ID)
	if err != nil {
		return currentUser{}, false
	}
	return currentUser{User: user, Session: session, Permissions: permissions}, true
}

func (s *Server) authenticateService(w http.ResponseWriter, r *http.Request, requiredScope string) (store.ServiceToken, bool) {
	if s.services == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "service_registry_not_configured"})
		return store.ServiceToken{}, false
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(raw) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "missing_service_token"})
		return store.ServiceToken{}, false
	}
	token, err := s.services.AuthenticateServiceToken(r.Context(), strings.TrimSpace(raw), requiredScope)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_service_scope"})
		return store.ServiceToken{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_service_token"})
		return store.ServiceToken{}, false
	}
	return token, true
}

func currentFromContext(ctx context.Context) currentUser {
	current, _ := ctx.Value(currentUserKey{}).(currentUser)
	return current
}

func userHasRoleName(user store.User, roleName string) bool {
	for _, role := range user.Roles {
		if role == roleName {
			return true
		}
	}
	return false
}

func isUnsafeMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func isPasswordChangeAllowedPath(path string) bool {
	switch path {
	case "/auth/me", "/auth/change-password", "/auth/logout":
		return true
	default:
		return false
	}
}

func isOAuthCallbackPath(path string) bool {
	return path == "/auth/oauth/callback" || path == "/integrations/oauth-accounts/callback"
}
