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
	AppName                string `json:"app_name"`
	Timezone               string `json:"timezone"`
	SMTPEnabled            bool   `json:"smtp_enabled"`
	SMTPHost               string `json:"smtp_host,omitempty"`
	SMTPPort               int    `json:"smtp_port,omitempty"`
	SMTPStartTLS           bool   `json:"smtp_starttls"`
	SMTPFrom               string `json:"smtp_from,omitempty"`
	SMTPUsername           string `json:"smtp_username,omitempty"`
	SMTPPasswordConfigured bool   `json:"smtp_password_configured,omitempty"`
	UpdatedAt              string `json:"updated_at,omitempty"`
}

type AppSettingsStore interface {
	GetAppSettings(ctx context.Context) (AppSettings, error)
	UpdateAppSettings(ctx context.Context, settings AppSettings) (AppSettings, error)
}

const AppSMTPPasswordSecretName = "app_smtp_password"

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
	if !settings.SMTPEnabled {
		return AppSettings{AppName: name, Timezone: timezone, SMTPPort: defaultAppSettings.SMTPPort, SMTPStartTLS: defaultAppSettings.SMTPStartTLS}, nil
	}
	if settings.SMTPPort == 0 {
		settings.SMTPPort = defaultAppSettings.SMTPPort
	}
	if err := validateSMTPSettings(settings); err != nil {
		return AppSettings{}, err
	}
	return AppSettings{
		AppName:                name,
		Timezone:               timezone,
		SMTPEnabled:            settings.SMTPEnabled,
		SMTPHost:               settings.SMTPHost,
		SMTPPort:               settings.SMTPPort,
		SMTPStartTLS:           settings.SMTPStartTLS,
		SMTPFrom:               settings.SMTPFrom,
		SMTPUsername:           settings.SMTPUsername,
		SMTPPasswordConfigured: settings.SMTPPasswordConfigured,
	}, nil
}

func NormalizeAppSettings(settings AppSettings) (AppSettings, error) {
	return normalizeAppSettings(settings)
}

func validateSMTPSettings(settings AppSettings) error {
	if !validSMTPHost(settings.SMTPHost) || settings.SMTPPort < 1 || settings.SMTPPort > 65535 {
		return ErrInvalidSettings
	}
	if settings.SMTPFrom == "" || strings.ContainsAny(settings.SMTPUsername, "\r\n\x00") || len(settings.SMTPUsername) > 255 {
		return ErrInvalidSettings
	}
	address, err := mail.ParseAddress(settings.SMTPFrom)
	if err != nil || address.Address == "" || !strings.EqualFold(address.Address, settings.SMTPFrom) {
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
