package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AppSettings struct {
	AppName                      string `json:"app_name"`
	Timezone                     string `json:"timezone"`
	GoogleAnalyticsEnabled       bool   `json:"google_analytics_enabled,omitempty"`
	GoogleAnalyticsMeasurementID string `json:"google_analytics_measurement_id,omitempty"`
	SMTPEnabled                  bool   `json:"smtp_enabled"`
	SMTPHost                     string `json:"smtp_host,omitempty"`
	SMTPPort                     int    `json:"smtp_port,omitempty"`
	SMTPStartTLS                 bool   `json:"smtp_starttls"`
	SMTPFrom                     string `json:"smtp_from,omitempty"`
	SMTPUsername                 string `json:"smtp_username,omitempty"`
	SMTPPasswordConfigured       bool   `json:"smtp_password_configured,omitempty"`
	TurnstileEnabled             bool   `json:"turnstile_enabled,omitempty"`
	TurnstileSiteKey             string `json:"turnstile_site_key,omitempty"`
	TurnstileConfigured          bool   `json:"turnstile_configured,omitempty"`
	UpdatedAt                    string `json:"updated_at,omitempty"`
}

type AppSettingsStore interface {
	GetAppSettings(ctx context.Context) (AppSettings, error)
	UpdateAppSettings(ctx context.Context, settings AppSettings) (AppSettings, error)
}

const (
	AppSMTPPasswordSecretName = "app_smtp_password"
	AppTurnstileSecretName    = "app_turnstile_secret"
)

var defaultAppSettings = AppSettings{AppName: "AutoStream", Timezone: "Asia/Tokyo", SMTPPort: 587, SMTPStartTLS: true}

type MemoryAppSettingsStore struct {
	mu       sync.Mutex
	settings AppSettings
}

func NewMemoryAppSettingsStore() *MemoryAppSettingsStore {
	return &MemoryAppSettingsStore{settings: defaultAppSettings}
}

func (s *MemoryAppSettingsStore) GetAppSettings(ctx context.Context) (AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return AppSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings, nil
}

func (s *MemoryAppSettingsStore) UpdateAppSettings(ctx context.Context, settings AppSettings) (AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return AppSettings{}, err
	}
	normalized, err := normalizeAppSettings(settings)
	if err != nil {
		return AppSettings{}, err
	}
	normalized.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	s.settings = normalized
	s.mu.Unlock()
	return normalized, nil
}

type MariaDBAppSettingsStore struct {
	db *sql.DB
}

func NewMariaDBAppSettingsStore(db *sql.DB) MariaDBAppSettingsStore {
	return MariaDBAppSettingsStore{db: db}
}

func (s MariaDBAppSettingsStore) GetAppSettings(ctx context.Context) (AppSettings, error) {
	var body string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `SELECT value_json, updated_at FROM system_settings WHERE name = 'app'`).Scan(&body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultAppSettings, nil
	}
	if err != nil {
		return AppSettings{}, err
	}
	settings := defaultAppSettings
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		return AppSettings{}, err
	}
	normalized, err := normalizeAppSettings(settings)
	if err != nil {
		return AppSettings{}, err
	}
	normalized.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return normalized, nil
}

func (s MariaDBAppSettingsStore) UpdateAppSettings(ctx context.Context, settings AppSettings) (AppSettings, error) {
	normalized, err := normalizeAppSettings(settings)
	if err != nil {
		return AppSettings{}, err
	}
	now := time.Now().UTC()
	body, err := json.Marshal(normalized)
	if err != nil {
		return AppSettings{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO system_settings (name, value_json, updated_at) VALUES ('app', ?, ?) ON DUPLICATE KEY UPDATE value_json = VALUES(value_json), updated_at = VALUES(updated_at)`, string(body), now)
	if err != nil {
		return AppSettings{}, err
	}
	normalized.UpdatedAt = now.Format(time.RFC3339)
	return normalized, nil
}

func normalizeAppSettings(settings AppSettings) (AppSettings, error) {
	name := strings.TrimSpace(settings.AppName)
	if name == "" {
		name = defaultAppSettings.AppName
	}
	if len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n\t\x00") {
		return AppSettings{}, ErrInvalidSettings
	}
	timezone := strings.TrimSpace(settings.Timezone)
	if timezone == "" {
		timezone = defaultAppSettings.Timezone
	}
	if !validTimezoneName(timezone) {
		return AppSettings{}, ErrInvalidSettings
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return AppSettings{}, ErrInvalidSettings
	}
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPFrom = strings.TrimSpace(settings.SMTPFrom)
	settings.SMTPUsername = strings.TrimSpace(settings.SMTPUsername)
	settings.TurnstileSiteKey = strings.TrimSpace(settings.TurnstileSiteKey)
	settings.GoogleAnalyticsMeasurementID = strings.ToUpper(strings.TrimSpace(settings.GoogleAnalyticsMeasurementID))
	normalized := AppSettings{
		AppName:                      name,
		Timezone:                     timezone,
		GoogleAnalyticsEnabled:       settings.GoogleAnalyticsEnabled,
		GoogleAnalyticsMeasurementID: settings.GoogleAnalyticsMeasurementID,
		SMTPPort:                     defaultAppSettings.SMTPPort,
		SMTPStartTLS:                 defaultAppSettings.SMTPStartTLS,
		TurnstileEnabled:             settings.TurnstileEnabled,
		TurnstileSiteKey:             settings.TurnstileSiteKey,
		TurnstileConfigured:          settings.TurnstileConfigured,
	}
	if !settings.GoogleAnalyticsEnabled {
		normalized.GoogleAnalyticsMeasurementID = ""
	} else if !validGoogleAnalyticsMeasurementID(settings.GoogleAnalyticsMeasurementID) {
		return AppSettings{}, ErrInvalidSettings
	}
	if !settings.TurnstileEnabled {
		normalized.TurnstileSiteKey = ""
		normalized.TurnstileConfigured = false
	} else if !validTurnstileSiteKey(settings.TurnstileSiteKey) || !settings.TurnstileConfigured {
		return AppSettings{}, ErrInvalidSettings
	}
	if !settings.SMTPEnabled {
		return normalized, nil
	}
	if settings.SMTPPort == 0 {
		settings.SMTPPort = defaultAppSettings.SMTPPort
	}
	if err := validateSMTPSettings(settings); err != nil {
		return AppSettings{}, err
	}
	normalized.SMTPEnabled = settings.SMTPEnabled
	normalized.SMTPHost = settings.SMTPHost
	normalized.SMTPPort = settings.SMTPPort
	normalized.SMTPStartTLS = settings.SMTPStartTLS
	normalized.SMTPFrom = settings.SMTPFrom
	normalized.SMTPUsername = settings.SMTPUsername
	normalized.SMTPPasswordConfigured = settings.SMTPPasswordConfigured
	return normalized, nil
}

func NormalizeAppSettings(settings AppSettings) (AppSettings, error) {
	return normalizeAppSettings(settings)
}

func validateSMTPSettings(settings AppSettings) error {
	if !validSMTPHost(settings.SMTPHost) || settings.SMTPPort < 1 || settings.SMTPPort > 65535 {
		return ErrInvalidSettings
	}
	if !validSMTPFrom(settings.SMTPFrom) || strings.ContainsAny(settings.SMTPUsername, "\r\n\x00") || len(settings.SMTPUsername) > 255 {
		return ErrInvalidSettings
	}
	if _, err := strconv.Atoi(settings.SMTPHost); err == nil {
		return ErrInvalidSettings
	}
	if settings.SMTPUsername == "" && settings.SMTPPasswordConfigured {
		return ErrInvalidSettings
	}
	if settings.SMTPUsername != "" && !settings.SMTPPasswordConfigured {
		return ErrInvalidSettings
	}
	return nil
}

func validSMTPFrom(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && validSMTPMailbox(address.Address)
}

func validSMTPMailbox(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 320 && strings.Contains(value, "@") && !strings.ContainsAny(value, "\r\n\t\x00 <>")
}

func validSMTPHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 255 || strings.ContainsAny(host, "\r\n\t\x00/") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.ContainsAny(host, ":@") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validTimezoneName(value string) bool {
	if value == "" || len(value) > 64 || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "\\") {
		return false
	}
	for _, char := range value {
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= 'a' && char <= 'z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '/', '_', '-', '+':
			continue
		default:
			return false
		}
	}
	return true
}

func validTurnstileSiteKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n\t\x00") {
		return false
	}
	return true
}

func validGoogleAnalyticsMeasurementID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 6 || len(value) > 24 || !strings.HasPrefix(value, "G-") {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "G-") {
		if char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}
