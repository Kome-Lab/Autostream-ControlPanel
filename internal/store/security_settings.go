package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type SecuritySettings struct {
	PasswordMinLength        int      `json:"password_min_length"`
	PasswordHash             string   `json:"password_hash"`
	LoginLockoutThreshold    int      `json:"login_lockout_threshold"`
	SessionIdleTimeoutMin    int      `json:"session_idle_timeout_min"`
	SessionAbsoluteLifetimeH int      `json:"session_absolute_lifetime_h"`
	RememberMeEnabled        bool     `json:"remember_me_enabled"`
	MFAMode                  string   `json:"mfa_mode"`
	MFARequiredRoles         []string `json:"mfa_required_roles,omitempty"`
	MFASupportedMethods      []string `json:"mfa_supported_methods"`
	PasskeyStatus            string   `json:"passkey_status"`
	UpdatedAt                string   `json:"updated_at,omitempty"`
}

type SecretStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Fingerprint string `json:"fingerprint,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type SecuritySettingsStore interface {
	GetSecuritySettings(ctx context.Context) (SecuritySettings, error)
	UpdateSecuritySettings(ctx context.Context, settings SecuritySettings) (SecuritySettings, error)
}

type SecretStore interface {
	ListSecretStatus(ctx context.Context) ([]SecretStatus, error)
	UpdateSecret(ctx context.Context, name, value string) (SecretStatus, error)
	GetSecretValue(ctx context.Context, name string) (string, error)
}

var (
	ErrUnknownSecret      = errors.New("unknown secret")
	ErrInvalidSettings    = errors.New("invalid security settings")
	ErrSecretKeyRequired  = errors.New("secret encryption key required")
	allowedSecretNames    = []string{AppSMTPPasswordSecretName, "deepgram_api_key", "discord_bot_token", "google_drive_folder_id", "observability_token", "youtube_stream_key"}
	allowedSecretNameSet  = map[string]bool{}
	defaultSecurityConfig = SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		RememberMeEnabled:        false,
		MFAMode:                  "disabled",
		MFARequiredRoles:         []string{},
		MFASupportedMethods:      []string{"totp", "passkey"},
		PasskeyStatus:            "available",
	}
)

func init() {
	for _, name := range allowedSecretNames {
		allowedSecretNameSet[name] = true
	}
}

type MemorySecuritySettingsStore struct {
	mu       sync.Mutex
	settings SecuritySettings
}

func NewMemorySecuritySettingsStore() *MemorySecuritySettingsStore {
	return &MemorySecuritySettingsStore{settings: defaultSecurityConfig}
}

func (s *MemorySecuritySettingsStore) GetSecuritySettings(ctx context.Context) (SecuritySettings, error) {
	if err := ctx.Err(); err != nil {
		return SecuritySettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings, nil
}

func (s *MemorySecuritySettingsStore) UpdateSecuritySettings(ctx context.Context, settings SecuritySettings) (SecuritySettings, error) {
	if err := ctx.Err(); err != nil {
		return SecuritySettings{}, err
	}
	normalized, err := normalizeSecuritySettings(settings)
	if err != nil {
		return SecuritySettings{}, err
	}
	normalized.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	s.settings = normalized
	s.mu.Unlock()
	return normalized, nil
}

type MemorySecretStore struct {
	mu      sync.Mutex
	secrets map[string]SecretStatus
	values  map[string]string
}

func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{secrets: map[string]SecretStatus{}, values: map[string]string{}}
}

func (s *MemorySecretStore) ListSecretStatus(ctx context.Context) ([]SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := map[string]SecretStatus{}
	for _, name := range allowedSecretNames {
		status := s.secrets[name]
		status.Name = name
		known[name] = status
	}
	for name, status := range s.secrets {
		status.Name = name
		known[name] = status
	}
	return sortedSecretStatuses(known), nil
}

func (s *MemorySecretStore) UpdateSecret(ctx context.Context, name, value string) (SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return SecretStatus{}, err
	}
	if !allowedSecretName(name) {
		return SecretStatus{}, ErrUnknownSecret
	}
	status := SecretStatus{Name: name, Configured: value != "", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if value != "" {
		status.Fingerprint = security.SecretFingerprint(value)
	}
	s.mu.Lock()
	if status.Configured {
		s.secrets[name] = status
		s.values[name] = value
	} else {
		delete(s.secrets, name)
		delete(s.values, name)
	}
	s.mu.Unlock()
	return status, nil
}

func (s *MemorySecretStore) GetSecretValue(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !allowedSecretName(name) {
		return "", ErrUnknownSecret
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[name]
	if !ok || value == "" {
		return "", ErrNotFound
	}
	return value, nil
}

type MariaDBSecuritySettingsStore struct {
	db *sql.DB
}

func NewMariaDBSecuritySettingsStore(db *sql.DB) MariaDBSecuritySettingsStore {
	return MariaDBSecuritySettingsStore{db: db}
}

func (s MariaDBSecuritySettingsStore) GetSecuritySettings(ctx context.Context) (SecuritySettings, error) {
	var body string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `SELECT value_json, updated_at FROM system_settings WHERE name = 'security'`).Scan(&body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSecurityConfig, nil
	}
	if err != nil {
		return SecuritySettings{}, err
	}
	settings := defaultSecurityConfig
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		return SecuritySettings{}, err
	}
	settings.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return settings, nil
}

func (s MariaDBSecuritySettingsStore) UpdateSecuritySettings(ctx context.Context, settings SecuritySettings) (SecuritySettings, error) {
	normalized, err := normalizeSecuritySettings(settings)
	if err != nil {
		return SecuritySettings{}, err
	}
	now := time.Now().UTC()
	body, err := json.Marshal(normalized)
	if err != nil {
		return SecuritySettings{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO system_settings (name, value_json, updated_at) VALUES ('security', ?, ?) ON DUPLICATE KEY UPDATE value_json = VALUES(value_json), updated_at = VALUES(updated_at)`, string(body), now)
	if err != nil {
		return SecuritySettings{}, err
	}
	normalized.UpdatedAt = now.Format(time.RFC3339)
	return normalized, nil
}

type MariaDBSecretStore struct {
	db          *sql.DB
	keyMaterial string
}

func NewMariaDBSecretStore(db *sql.DB, keyMaterial string) MariaDBSecretStore {
	return MariaDBSecretStore{db: db, keyMaterial: keyMaterial}
}

func (s MariaDBSecretStore) ListSecretStatus(ctx context.Context) ([]SecretStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, value_hash, updated_at FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]SecretStatus{}
	for rows.Next() {
		var status SecretStatus
		var updatedAt time.Time
		if err := rows.Scan(&status.Name, &status.Fingerprint, &updatedAt); err != nil {
			return nil, err
		}
		status.Configured = true
		status.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		known[status.Name] = status
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, name := range allowedSecretNames {
		status := known[name]
		status.Name = name
		known[name] = status
	}
	return sortedSecretStatuses(known), nil
}

func (s MariaDBSecretStore) UpdateSecret(ctx context.Context, name, value string) (SecretStatus, error) {
	if !allowedSecretName(name) {
		return SecretStatus{}, ErrUnknownSecret
	}
	if value == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
		return SecretStatus{Name: name, Configured: false, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, err
	}
	if s.keyMaterial == "" {
		return SecretStatus{}, ErrSecretKeyRequired
	}
	ciphertext, nonce, err := security.EncryptSecret(value, s.keyMaterial)
	if err != nil {
		return SecretStatus{}, err
	}
	fingerprint := security.SecretFingerprint(value)
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO secrets (name, ciphertext, nonce, value_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE ciphertext = VALUES(ciphertext), nonce = VALUES(nonce), value_hash = VALUES(value_hash), updated_at = VALUES(updated_at)`, name, ciphertext, nonce, fingerprint, now)
	if err != nil {
		return SecretStatus{}, err
	}
	return SecretStatus{Name: name, Configured: true, Fingerprint: fingerprint, UpdatedAt: now.Format(time.RFC3339)}, nil
}

func (s MariaDBSecretStore) GetSecretValue(ctx context.Context, name string) (string, error) {
	if !allowedSecretName(name) {
		return "", ErrUnknownSecret
	}
	if s.keyMaterial == "" {
		return "", ErrSecretKeyRequired
	}
	var ciphertext, nonce string
	err := s.db.QueryRowContext(ctx, `SELECT ciphertext, nonce FROM secrets WHERE name = ?`, name).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return security.DecryptSecret(ciphertext, nonce, s.keyMaterial)
}

func normalizeSecuritySettings(settings SecuritySettings) (SecuritySettings, error) {
	if settings.PasswordMinLength == 0 {
		settings.PasswordMinLength = defaultSecurityConfig.PasswordMinLength
	}
	if settings.PasswordHash == "" {
		settings.PasswordHash = defaultSecurityConfig.PasswordHash
	}
	if settings.LoginLockoutThreshold == 0 {
		settings.LoginLockoutThreshold = defaultSecurityConfig.LoginLockoutThreshold
	}
	if settings.SessionIdleTimeoutMin == 0 {
		settings.SessionIdleTimeoutMin = defaultSecurityConfig.SessionIdleTimeoutMin
	}
	if settings.SessionAbsoluteLifetimeH == 0 {
		settings.SessionAbsoluteLifetimeH = defaultSecurityConfig.SessionAbsoluteLifetimeH
	}
	if settings.MFAMode == "" {
		settings.MFAMode = defaultSecurityConfig.MFAMode
	}
	settings.MFARequiredRoles = cleanSecurityStringSlice(settings.MFARequiredRoles)
	if settings.PasswordMinLength < 12 ||
		settings.PasswordHash != "argon2id" ||
		settings.LoginLockoutThreshold < 3 ||
		settings.SessionIdleTimeoutMin < 5 ||
		settings.SessionAbsoluteLifetimeH < 1 ||
		settings.RememberMeEnabled ||
		(settings.MFAMode != "disabled" && settings.MFAMode != "totp" && settings.MFAMode != "passkey") {
		return SecuritySettings{}, ErrInvalidSettings
	}
	settings.MFASupportedMethods = append([]string(nil), defaultSecurityConfig.MFASupportedMethods...)
	settings.PasskeyStatus = defaultSecurityConfig.PasskeyStatus
	settings.UpdatedAt = ""
	return settings, nil
}

func cleanSecurityStringSlice(values []string) []string {
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
	sort.Strings(out)
	return out
}

func allowedSecretName(name string) bool {
	if allowedSecretNameSet[name] {
		return true
	}
	for _, prefix := range []string{
		"youtube_stream_key_",
		"discord_bot_token_",
		"encoder_runtime_secret_",
		"google_oauth_refresh_token_",
		"google_drive_folder_id_",
		"webhook_url_",
		"smtp_password_",
	} {
		if strings.HasPrefix(name, prefix) && safeDynamicSecretName(name) {
			return true
		}
	}
	return false
}

func safeDynamicSecretName(name string) bool {
	if len(name) > 128 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func sortedSecretStatuses(items map[string]SecretStatus) []SecretStatus {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SecretStatus, 0, len(names))
	for _, name := range names {
		status := items[name]
		status.Name = name
		out = append(out, status)
	}
	return out
}

func AllowedSecretNames() []string {
	out := append([]string(nil), allowedSecretNames...)
	sort.Strings(out)
	return out
}
