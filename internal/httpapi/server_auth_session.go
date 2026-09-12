package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerAccountRoutes() {
	s.mux.HandleFunc("POST /auth/logout", s.requirePermission("", s.logout))
	s.mux.HandleFunc("POST /auth/session/refresh", s.requirePermission("", s.refreshSession))
	s.mux.HandleFunc("GET /auth/me", s.requirePermission("", s.me))
	s.mux.HandleFunc("GET /account/preferences/ui", s.requirePermission("", s.getUIPreference))
	s.mux.HandleFunc("PUT /account/preferences/ui", s.requirePermission("", s.updateUIPreference))
	s.mux.HandleFunc("GET /auth/avatar", s.requirePermission("", s.getCurrentUserAvatar))
	s.mux.HandleFunc("PUT /auth/avatar", s.requirePermission("", s.updateCurrentUserAvatar))
	s.mux.HandleFunc("DELETE /auth/avatar", s.requirePermission("", s.deleteCurrentUserAvatar))
	s.mux.HandleFunc("PUT /auth/email", s.requirePermission("", s.updateCurrentUserEmail))
	s.mux.HandleFunc("POST /auth/email/confirm", s.confirmCurrentUserEmail)
	s.mux.HandleFunc("POST /auth/change-password", s.requirePermission("", s.changePassword))
	s.mux.HandleFunc("GET /auth/mfa/status", s.requirePermission("", s.mfaStatus))
	s.mux.HandleFunc("POST /auth/mfa/enroll", s.requirePermission("", s.mfaEnroll))
	s.mux.HandleFunc("POST /auth/mfa/verify", s.mfaVerify)
	s.mux.HandleFunc("POST /auth/mfa/disable", s.requirePermission("", s.mfaDisable))
	s.mux.HandleFunc("POST /auth/recovery-codes/regenerate", s.requirePermission("", s.mfaRegenerateRecoveryCodes))
	s.mux.HandleFunc("POST /auth/passkeys/register/start", s.requirePermission("", s.startPasskeyRegistration))
	s.mux.HandleFunc("POST /auth/passkeys/register/finish", s.requirePermission("", s.finishPasskeyRegistration))
	s.mux.HandleFunc("POST /auth/passkeys/login/start", s.startPasskeyLogin)
	s.mux.HandleFunc("POST /auth/passkeys/login/finish", s.finishPasskeyLogin)
	s.mux.HandleFunc("GET /auth/passkeys", s.requirePermission("", s.listPasskeys))
	s.mux.HandleFunc("DELETE /auth/passkeys/{id}", s.requirePermission("", s.deletePasskey))
	s.mux.HandleFunc("GET /auth/oauth-links", s.requirePermission("", s.listCurrentUserOAuthLinks))
	s.mux.HandleFunc("POST /auth/oauth-links/{id}/start", s.requirePermission("", s.startCurrentUserOAuthLink))
	s.mux.HandleFunc("DELETE /auth/oauth-links/{id}", s.requirePermission("", s.deleteCurrentUserOAuthLink))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "auth_store_not_configured"})
		return
	}
	var body struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "login"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	failureKey := loginFailureKey(body.Username, clientIP(r))
	if !s.loginFailures.allow(failureKey, settings.LoginLockoutThreshold) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": "rate_limited"}})
		w.Header().Set("Retry-After", strconv.Itoa(int(defaultLoginFailureWindow/time.Second)))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "login_rate_limited"})
		return
	}
	user, err := s.auth.FindUserByUsername(r.Context(), body.Username)
	if err != nil || user.Status == "disabled" || user.Status == "locked" || !security.VerifyPassword(body.Password, user.PasswordHash) {
		s.loginFailures.record(failureKey)
		if s.auth != nil {
			_ = s.auth.RecordLoginFailure(r.Context(), body.Username, settings.LoginLockoutThreshold)
		}
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": "invalid_credentials"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		return
	}
	if passkeyRequiredForUser(settings, user) {
		s.rejectPasskeyRequiredLogin(w, r, user, "auth.login", nil)
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
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "mfa_required"})
			writeJSON(w, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_token": challenge.Token, "expires_at": challenge.ExpiresAt})
			return
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_enrollment_required"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "mfa_enrollment_required"})
		return
	}
	s.completeLogin(w, r, user, settings)
}

func (s *Server) completeLogin(w http.ResponseWriter, r *http.Request, user store.User, settings store.SecuritySettings) {
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
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": session.CSRFToken, "user": publicUser(user)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.auth.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: sessionCookieSecure(), Expires: time.Unix(0, 0), MaxAge: -1})
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.logout", ResourceType: "session", Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) refreshSession(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	session, err := s.auth.RefreshSession(r.Context(), current.Session.Token, time.Duration(settings.SessionIdleTimeoutMin)*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "refreshed",
		"idle_expires_at":     session.IdleExpiresAt,
		"absolute_expires_at": session.AbsoluteExpiresAt,
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	user := publicUser(current.User)
	if s.avatars != nil {
		if info, err := s.avatars.GetUserAvatarInfo(r.Context(), current.User.ID); err == nil {
			user["avatar_url"] = userAvatarURL(info)
			user["avatar_updated_at"] = info.UpdatedAt
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "permissions": current.Permissions})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if !security.VerifyPassword(body.CurrentPassword, current.User.PasswordHash) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "current_password_invalid"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "current_password_invalid"})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.NewPassword) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "password_policy"}})
		return
	}
	if err := s.auth.ChangePassword(r.Context(), current.User.ID, body.NewPassword); err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "password_policy"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "password_change_failed"})
		return
	}
	_ = s.auth.DeleteUserSessions(r.Context(), current.User.ID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: sessionCookieSecure(), Expires: time.Unix(0, 0), MaxAge: -1})
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}
