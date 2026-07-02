package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

type AppSettings struct {
	AppName   string `json:"app_name"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type AppSettingsStore interface {
	GetAppSettings(ctx context.Context) (AppSettings, error)
	UpdateAppSettings(ctx context.Context, settings AppSettings) (AppSettings, error)
}

var defaultAppSettings = AppSettings{AppName: "AutoStream"}

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
	return AppSettings{AppName: name}, nil
}
