package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerAuditSettingsRoutes() {
	s.mux.HandleFunc("GET /audit-logs", s.requirePermission("audit_logs.read", s.listAuditLogs))
	s.mux.HandleFunc("GET /audit-logs/export", s.requirePermission("audit_logs.export", s.exportAuditLogs))
	s.mux.HandleFunc("GET /security/settings", s.requirePermission("system_settings.read", s.securitySettings))
	s.mux.HandleFunc("PUT /security/settings", s.requirePermission("system_settings.update", s.updateSecuritySettings))
	s.mux.HandleFunc("GET /settings/app", s.appSettingsView)
	s.mux.HandleFunc("GET /settings/app/manage", s.requirePermission("system_settings.update", s.managedAppSettingsView))
	s.mux.HandleFunc("PUT /settings/app", s.requirePermission("system_settings.update", s.updateAppSettings))
	s.mux.HandleFunc("POST /settings/app/test-email", s.requirePermission("system_settings.update", s.sendAppSettingsTestEmail))
	s.mux.HandleFunc("GET /version", s.requirePermission("", s.versionInfo))
}

func (s *Server) passwordMeetsConfiguredPolicy(w http.ResponseWriter, r *http.Request, password string) bool {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return false
	}
	if err := security.ValidatePasswordWithMinLength(password, settings.PasswordMinLength); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":                "password_policy_failed",
			"password_min_length": settings.PasswordMinLength,
		})
		return false
	}
	return true
}

func (s *Server) securitySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSecuritySettings(w http.ResponseWriter, r *http.Request) {
	var body store.SecuritySettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	if productionEnvironment() && !productionMFASettingsAllowed(body) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "failure", Metadata: map[string]any{"reason": "production_mfa_required"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "production_mfa_required"})
		return
	}
	settings, err := s.settings.UpdateSecuritySettings(r.Context(), body)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_security_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "success"})
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) appSettingsView(w http.ResponseWriter, r *http.Request) {
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicAppSettings{
		AppName:                      settings.AppName,
		Timezone:                     settings.Timezone,
		TurnstileEnabled:             settings.TurnstileEnabled,
		TurnstileSiteKey:             settings.TurnstileSiteKey,
		TurnstileConfigured:          s.secretConfigured(r.Context(), store.AppTurnstileSecretName),
		GoogleAnalyticsEnabled:       settings.GoogleAnalyticsEnabled,
		GoogleAnalyticsMeasurementID: settings.GoogleAnalyticsMeasurementID,
		UpdatedAt:                    settings.UpdatedAt,
	})
}

type publicAppSettings struct {
	AppName                      string `json:"app_name"`
	Timezone                     string `json:"timezone"`
	TurnstileEnabled             bool   `json:"turnstile_enabled,omitempty"`
	TurnstileSiteKey             string `json:"turnstile_site_key,omitempty"`
	TurnstileConfigured          bool   `json:"turnstile_configured,omitempty"`
	GoogleAnalyticsEnabled       bool   `json:"google_analytics_enabled,omitempty"`
	GoogleAnalyticsMeasurementID string `json:"google_analytics_measurement_id,omitempty"`
	UpdatedAt                    string `json:"updated_at,omitempty"`
}

func (s *Server) managedAppSettingsView(w http.ResponseWriter, r *http.Request) {
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, s.appSettingsWithSecretStatus(r.Context(), settings))
}

func (s *Server) updateAppSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		store.AppSettings
		SMTPPassword    string `json:"smtp_password"`
		TurnstileSecret string `json:"turnstile_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	smtpPassword := strings.TrimSpace(body.SMTPPassword)
	turnstileSecret := strings.TrimSpace(body.TurnstileSecret)
	body.SMTPPasswordConfigured = s.secretConfigured(r.Context(), store.AppSMTPPasswordSecretName)
	if smtpPassword != "" {
		body.SMTPPasswordConfigured = true
	}
	if !body.SMTPEnabled {
		body.SMTPPasswordConfigured = false
	}
	body.TurnstileConfigured = s.secretConfigured(r.Context(), store.AppTurnstileSecretName)
	if turnstileSecret != "" {
		body.TurnstileConfigured = true
	}
	if !body.TurnstileEnabled {
		body.TurnstileConfigured = false
	}
	normalized, err := store.NormalizeAppSettings(body.AppSettings)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_update_failed"})
		return
	}
	if smtpPassword != "" || !normalized.SMTPEnabled {
		status, err := s.secrets.UpdateSecret(r.Context(), store.AppSMTPPasswordSecretName, smtpPassword)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "smtp_password_update_failed"})
			return
		}
		normalized.SMTPPasswordConfigured = status.Configured
	} else {
		normalized.SMTPPasswordConfigured = s.secretConfigured(r.Context(), store.AppSMTPPasswordSecretName)
	}
	if turnstileSecret != "" || !normalized.TurnstileEnabled {
		status, err := s.secrets.UpdateSecret(r.Context(), store.AppTurnstileSecretName, turnstileSecret)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "turnstile_secret_update_failed"})
			return
		}
		normalized.TurnstileConfigured = status.Configured
	} else {
		normalized.TurnstileConfigured = s.secretConfigured(r.Context(), store.AppTurnstileSecretName)
	}
	settings, err := s.appSettings.UpdateAppSettings(r.Context(), normalized)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "success"})
	writeJSON(w, http.StatusOK, s.appSettingsWithSecretStatus(r.Context(), settings))
}

func (s *Server) sendAppSettingsTestEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	to, ok := normalizeSMTPTestRecipient(body.To)
	if !ok {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_recipient"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_email_recipient"})
		return
	}
	maskedTo := maskEmailAddress(to)
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	settings = s.appSettingsWithSecretStatus(r.Context(), settings)
	if !settings.SMTPEnabled || strings.TrimSpace(settings.SMTPHost) == "" || strings.TrimSpace(settings.SMTPFrom) == "" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "smtp_not_configured", "target": maskedTo}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "smtp_not_configured", "target": maskedTo})
		return
	}
	password := ""
	if settings.SMTPUsername != "" || settings.SMTPPasswordConfigured {
		password, err = s.secrets.GetSecretValue(r.Context(), store.AppSMTPPasswordSecretName)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "secret_encryption_key_required", "target": maskedTo}})
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required", "target": maskedTo})
			return
		}
		if err != nil {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "smtp_not_configured", "target": maskedTo}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "smtp_not_configured", "target": maskedTo})
			return
		}
	}
	appName := strings.TrimSpace(settings.AppName)
	if appName == "" {
		appName = "AutoStream"
	}
	message := MailMessage{
		To:      to,
		Subject: appName + " SMTPテスト",
		Text: appName + " Control Panel からのテストメールです。\n\n" +
			"送信を実行したユーザー: " + current.User.Username + "\n" +
			"送信日時: " + formatMailTimestamp(time.Now(), settings.Timezone) + "\n",
	}
	if err := s.mailer.Send(r.Context(), settings, password, message); err != nil {
		code := safeErrorCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedTo}})
		writeJSON(w, smtpTestStatus(code), map[string]string{"code": code, "target": maskedTo})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "success", Metadata: map[string]any{"target": maskedTo}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "target": maskedTo})
}

func (s *Server) appSettingsWithSecretStatus(ctx context.Context, settings store.AppSettings) store.AppSettings {
	settings.SMTPPasswordConfigured = s.secretConfigured(ctx, store.AppSMTPPasswordSecretName)
	settings.TurnstileConfigured = s.secretConfigured(ctx, store.AppTurnstileSecretName)
	return settings
}

func (s *Server) turnstileFailure(ctx context.Context, r *http.Request, token, action string) (int, string) {
	settings, err := s.appSettings.GetAppSettings(ctx)
	if err != nil {
		return http.StatusInternalServerError, "app_settings_failed"
	}
	settings = s.appSettingsWithSecretStatus(ctx, settings)
	if !settings.TurnstileEnabled {
		return 0, ""
	}
	if strings.TrimSpace(settings.TurnstileSiteKey) == "" || !settings.TurnstileConfigured {
		return http.StatusServiceUnavailable, "turnstile_not_configured"
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 2048 {
		return http.StatusForbidden, "turnstile_token_required"
	}
	secret, err := s.secrets.GetSecretValue(ctx, store.AppTurnstileSecretName)
	if errors.Is(err, store.ErrSecretKeyRequired) {
		return http.StatusServiceUnavailable, "secret_encryption_key_required"
	}
	if err != nil {
		return http.StatusServiceUnavailable, "turnstile_not_configured"
	}
	result, err := s.turnstile.Verify(ctx, TurnstileVerifyRequest{Secret: secret, Token: token, RemoteIP: clientIP(r)})
	if err != nil {
		if errors.Is(err, errTurnstileFailed) {
			return http.StatusForbidden, "turnstile_failed"
		}
		return http.StatusServiceUnavailable, "turnstile_unavailable"
	}
	if !result.Success {
		return http.StatusForbidden, "turnstile_failed"
	}
	if result.Action != "" && strings.TrimSpace(action) != "" && result.Action != strings.TrimSpace(action) {
		return http.StatusForbidden, "turnstile_failed"
	}
	return 0, ""
}

func normalizeSMTPTestRecipient(value string) (string, bool) {
	recipient := strings.TrimSpace(value)
	if recipient == "" || strings.ContainsAny(recipient, "\r\n\t") {
		return "", false
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Name != "" || address.Address != recipient || strings.ContainsAny(address.Address, "\r\n\t") {
		return "", false
	}
	return address.Address, true
}

func maskEmailAddress(value string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(value), "@")
	if !ok || local == "" || domain == "" {
		return "masked"
	}
	runes := []rune(local)
	if len(runes) == 1 {
		return "*@" + domain
	}
	return string(runes[0]) + "***@" + domain
}

func smtpTestStatus(code string) int {
	switch code {
	case "smtp_not_configured", "smtp_requires_tls":
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func (s *Server) secretConfigured(ctx context.Context, name string) bool {
	statuses, err := s.secrets.ListSecretStatus(ctx)
	if err != nil {
		return false
	}
	for _, status := range statuses {
		if status.Name == name {
			return status.Configured
		}
	}
	return false
}
