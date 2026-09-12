package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const sensitiveActionAttemptThreshold = 6

const emailChangeChallengeTTL = 30 * time.Minute

func (s *Server) updateCurrentUserEmail(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "users_not_configured"})
		return
	}
	if s.emailChanges == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "email_change_not_configured"})
		return
	}
	email, ok := normalizeSMTPTestRecipient(body.Email)
	if !ok {
		code := "invalid_email"
		if strings.TrimSpace(body.Email) == "" {
			code = "email_required"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	maskedEmail := maskEmailAddress(email)
	if strings.EqualFold(email, strings.TrimSpace(current.User.Email)) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"unchanged": true, "target": maskedEmail}})
		writeJSON(w, http.StatusOK, map[string]string{"status": "unchanged", "target": maskedEmail})
		return
	}
	settings, password, status, code := s.mailSettingsForRequest(r.Context())
	if code != "" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedEmail}})
		writeJSON(w, status, map[string]string{"code": code, "target": maskedEmail})
		return
	}
	challenge, err := s.emailChanges.CreateEmailChangeChallenge(r.Context(), current.User.ID, email, emailChangeChallengeTTL)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "not_found", "target": maskedEmail}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "target": maskedEmail})
		return
	}
	if err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "email_change_create_failed", "target": maskedEmail}})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "email_change_create_failed", "target": maskedEmail})
		return
	}
	if err := s.sendEmailChangeConfirmation(r, settings, password, current.User, challenge); err != nil {
		_, _ = s.emailChanges.ConsumeEmailChangeChallenge(r.Context(), challenge.Token)
		code := safeErrorCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedEmail}})
		writeJSON(w, smtpTestStatus(code), map[string]string{"code": code, "target": maskedEmail})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"target": maskedEmail}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmation_sent", "target": maskedEmail})
}

func (s *Server) confirmCurrentUserEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token          string `json:"token"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "email_confirm"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "email_change", Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if s.emailChanges == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "email_change_not_configured"})
		return
	}
	challenge, err := s.emailChanges.ConsumeEmailChangeChallenge(r.Context(), body.Token)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "email_change", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_token"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_email_change_token"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "email_change_confirm_failed"})
		return
	}
	user, err := s.users.UpdateUser(r.Context(), challenge.UserID, store.UserPatch{Email: &challenge.Email})
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "user", ResourceID: challenge.UserID, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "user", ResourceID: challenge.UserID, Result: "failure", Metadata: map[string]any{"reason": "invalid_email"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_email"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.email.confirm", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"target": maskEmailAddress(user.Email)}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "email_changed", "target": maskEmailAddress(user.Email)})
}
