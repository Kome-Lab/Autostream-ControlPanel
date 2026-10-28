package store

import (
	"context"
	"encoding/json"
	"time"
)

func (s MariaDBAppSettingsStore) UpdateAppSettingsRevision(ctx context.Context, input AppSettingsMutation, secrets SecretStore) (AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return AppSettings{}, err
	}
	if err := validateExpectedSystemSettingsRevision(input.ExpectedUpdatedAt); err != nil {
		return AppSettings{}, err
	}
	normalized, err := normalizeAppSettings(input.Settings)
	if err != nil {
		return AppSettings{}, err
	}
	if err = validateAppSecretChanges(input.SecretChanges); err != nil {
		return AppSettings{}, err
	}
	var secretStore MariaDBSecretStore
	if secrets != nil {
		var ok bool
		secretStore, ok = secrets.(MariaDBSecretStore)
		if !ok || secretStore.db != s.db {
			return AppSettings{}, ErrAtomicSystemSettingsUpdateUnavailable
		}
	} else if len(input.SecretChanges) != 0 {
		return AppSettings{}, ErrAtomicSystemSettingsUpdateUnavailable
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return AppSettings{}, err
	}
	tx, previous, err := beginSystemSettingsMutation(ctx, s.db, "app", string(body), input.ExpectedUpdatedAt)
	if err != nil {
		return AppSettings{}, err
	}
	defer tx.Rollback()
	now := nextResourceUpdatedAt(previous, time.Now().UTC())
	for _, change := range input.SecretChanges {
		if _, err = secretStore.UpdateSecretTx(ctx, tx, change.Name, change.Value, now); err != nil {
			return AppSettings{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE system_settings SET value_json=?,updated_at=? WHERE name='app'`, string(body), now); err != nil {
		return AppSettings{}, err
	}
	var savedBody string
	var savedAt time.Time
	if err = tx.QueryRowContext(ctx, `SELECT value_json,updated_at FROM system_settings WHERE name='app'`).Scan(&savedBody, &savedAt); err != nil {
		return AppSettings{}, err
	}
	var saved AppSettings
	if err = json.Unmarshal([]byte(savedBody), &saved); err != nil {
		return AppSettings{}, err
	}
	saved.UpdatedAt = resourceUpdatedAt(savedAt)
	if secrets != nil {
		if err = appSecretStatusTx(ctx, tx, &saved); err != nil {
			return AppSettings{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return AppSettings{}, err
	}
	return saved, nil
}
