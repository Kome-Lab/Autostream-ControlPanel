package store

import (
	"context"
	"encoding/json"
	"time"
)

func (s *MemorySecuritySettingsStore) UpdateSecuritySettingsRevision(ctx context.Context, settings SecuritySettings, expected *string) (SecuritySettings, error) {
	if err := ctx.Err(); err != nil {
		return SecuritySettings{}, err
	}
	if err := validateExpectedSystemSettingsRevision(expected); err != nil {
		return SecuritySettings{}, err
	}
	normalized, err := normalizeSecuritySettings(settings)
	if err != nil {
		return SecuritySettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = matchExpectedSystemSettingsRevision(expected, systemSettingsRevision(s.settings.UpdatedAt)); err != nil {
		return SecuritySettings{}, err
	}
	previous, err := systemSettingsRevisionTime(s.settings.UpdatedAt)
	if err != nil {
		return SecuritySettings{}, err
	}
	if err = ctx.Err(); err != nil {
		return SecuritySettings{}, err
	}
	normalized.UpdatedAt = resourceUpdatedAt(nextResourceUpdatedAt(previous, time.Now().UTC()))
	s.settings = normalized
	return normalized, nil
}

func (s MariaDBSecuritySettingsStore) UpdateSecuritySettingsRevision(ctx context.Context, settings SecuritySettings, expected *string) (SecuritySettings, error) {
	if err := validateExpectedSystemSettingsRevision(expected); err != nil {
		return SecuritySettings{}, err
	}
	normalized, err := normalizeSecuritySettings(settings)
	if err != nil {
		return SecuritySettings{}, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return SecuritySettings{}, err
	}
	tx, previous, err := beginSystemSettingsMutation(ctx, s.db, "security", string(body), expected)
	if err != nil {
		return SecuritySettings{}, err
	}
	defer tx.Rollback()
	now := nextResourceUpdatedAt(previous, time.Now().UTC())
	if _, err = tx.ExecContext(ctx, `UPDATE system_settings SET value_json=?,updated_at=? WHERE name='security'`, string(body), now); err != nil {
		return SecuritySettings{}, err
	}
	var savedBody string
	var savedAt time.Time
	if err = tx.QueryRowContext(ctx, `SELECT value_json,updated_at FROM system_settings WHERE name='security'`).Scan(&savedBody, &savedAt); err != nil {
		return SecuritySettings{}, err
	}
	var saved SecuritySettings
	if err = json.Unmarshal([]byte(savedBody), &saved); err != nil {
		return SecuritySettings{}, err
	}
	saved.UpdatedAt = resourceUpdatedAt(savedAt)
	if err = tx.Commit(); err != nil {
		return SecuritySettings{}, err
	}
	return saved, nil
}
