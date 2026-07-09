package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

var (
	errSMTPNotConfigured = errors.New("smtp_not_configured")
	errSMTPRequiresTLS   = errors.New("smtp_requires_tls")
)

type Mailer interface {
	Send(ctx context.Context, settings store.AppSettings, password string, message MailMessage) error
}

type MailMessage struct {
	To      string
	Subject string
	Text    string
}

type SMTPMailer struct{}

func (s *Server) sendUserWelcomeEmail(r *http.Request, user store.User) error {
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		return errSMTPNotConfigured
	}
	settings = s.appSettingsWithSecretStatus(r.Context(), settings)
	if !settings.SMTPEnabled || strings.TrimSpace(user.Email) == "" {
		return errSMTPNotConfigured
	}
	password := ""
	if settings.SMTPUsername != "" || settings.SMTPPasswordConfigured {
		password, err = s.secrets.GetSecretValue(r.Context(), store.AppSMTPPasswordSecretName)
		if err != nil {
			return errSMTPNotConfigured
		}
	}
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "/"
	}
	appName := strings.TrimSpace(settings.AppName)
	if appName == "" {
		appName = "AutoStream"
	}
	return s.mailer.Send(r.Context(), settings, password, MailMessage{
		To:      user.Email,
		Subject: appName + " アカウント作成のお知らせ",
		Text: appName + " のアカウントを作成しました。\n\n" +
			"ユーザー名: " + user.Username + "\n" +
			"ログインURL: " + panelURL + "\n\n" +
			"初期パスワードはこのメールには記載していません。管理者から別経路で受け取ってください。\n" +
			"初回ログイン後、画面の案内に従ってパスワードを変更してください。\n",
	})
}

func (s *Server) mailSettingsForRequest(ctx context.Context) (store.AppSettings, string, int, string) {
	settings, err := s.appSettings.GetAppSettings(ctx)
	if err != nil {
		return store.AppSettings{}, "", http.StatusInternalServerError, "app_settings_failed"
	}
	settings = s.appSettingsWithSecretStatus(ctx, settings)
	if !settings.SMTPEnabled || strings.TrimSpace(settings.SMTPHost) == "" || strings.TrimSpace(settings.SMTPFrom) == "" {
		return store.AppSettings{}, "", http.StatusConflict, "smtp_not_configured"
	}
	password := ""
	if settings.SMTPUsername != "" || settings.SMTPPasswordConfigured {
		password, err = s.secrets.GetSecretValue(ctx, store.AppSMTPPasswordSecretName)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			return store.AppSettings{}, "", http.StatusServiceUnavailable, "secret_encryption_key_required"
		}
		if err != nil {
			return store.AppSettings{}, "", http.StatusConflict, "smtp_not_configured"
		}
	}
	return settings, password, 0, ""
}

func (s *Server) sendEmailChangeConfirmation(r *http.Request, settings store.AppSettings, password string, user store.User, challenge store.EmailChangeChallenge) error {
	confirmURL := emailChangeConfirmURL(r, challenge.Token)
	if confirmURL == "" {
		return errors.New("email_change_url_unavailable")
	}
	appName := strings.TrimSpace(settings.AppName)
	if appName == "" {
		appName = "AutoStream"
	}
	return s.mailer.Send(r.Context(), settings, password, MailMessage{
		To:      challenge.Email,
		Subject: appName + " email change confirmation",
		Text: "Confirm the email address change for " + appName + " Control Panel.\n\n" +
			"User: " + user.Username + "\n" +
			"One-time URL: " + confirmURL + "\n" +
			"Expires at: " + challenge.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00") + "\n\n" +
			"If you did not request this change, ignore this email.\n",
	})
}

func emailChangeConfirmURL(r *http.Request, token string) string {
	base := panelBaseURL(r)
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = "/auth/email/confirm"
	parsed.RawQuery = ""
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (SMTPMailer) Send(ctx context.Context, settings store.AppSettings, password string, message MailMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !settings.SMTPEnabled || strings.TrimSpace(settings.SMTPHost) == "" || strings.TrimSpace(settings.SMTPFrom) == "" {
		return errSMTPNotConfigured
	}
	if !settings.SMTPStartTLS && !isLocalSMTPHost(settings.SMTPHost) {
		return errSMTPRequiresTLS
	}
	addr := net.JoinHostPort(settings.SMTPHost, fmt.Sprintf("%d", settings.SMTPPort))
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp_dial_failed: %w", err)
	}
	defer client.Close()
	if settings.SMTPStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp_starttls_failed: %w", err)
		}
	}
	if settings.SMTPUsername != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.SMTPUsername, password, settings.SMTPHost)); err != nil {
			return fmt.Errorf("smtp_auth_failed: %w", err)
		}
	}
	if err := client.Mail(settings.SMTPFrom); err != nil {
		return fmt.Errorf("smtp_from_rejected: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("smtp_recipient_rejected: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp_data_failed: %w", err)
	}
	if _, err := writer.Write([]byte(formatPlainTextEmail(settings.SMTPFrom, message))); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp_write_failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp_close_failed: %w", err)
	}
	_ = client.Quit()
	return nil
}

func formatPlainTextEmail(from string, message MailMessage) string {
	return "From: " + sanitizeHeaderValue(from) + "\r\n" +
		"To: " + sanitizeHeaderValue(message.To) + "\r\n" +
		"Subject: " + sanitizeHeaderValue(message.Subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" +
		message.Text + "\r\n"
}

func sanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.TrimSpace(value)
}

func isLocalSMTPHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1" {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func safeErrorCode(err error) string {
	switch {
	case errors.Is(err, errSMTPNotConfigured):
		return "smtp_not_configured"
	case errors.Is(err, errSMTPRequiresTLS):
		return "smtp_requires_tls"
	}
	for _, code := range smtpDeliveryErrorCodes {
		if hasSMTPErrorCode(err, code) {
			return code
		}
	}
	return "send_failed"
}

var smtpDeliveryErrorCodes = []string{
	"smtp_dial_failed",
	"smtp_starttls_failed",
	"smtp_auth_failed",
	"smtp_from_rejected",
	"smtp_recipient_rejected",
	"smtp_data_failed",
	"smtp_write_failed",
	"smtp_close_failed",
}

func hasSMTPErrorCode(err error, code string) bool {
	for err != nil {
		message := err.Error()
		if message == code || strings.HasPrefix(message, code+":") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
