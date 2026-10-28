package store

import (
	"context"
	"time"
)

func (s *MemoryAppSettingsStore) UpdateAppSettingsRevision(ctx context.Context, input AppSettingsMutation, secrets SecretStore) (AppSettings, error) {
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
	var memorySecrets *MemorySecretStore
	if secrets != nil {
		var ok bool
		memorySecrets, ok = secrets.(*MemorySecretStore)
		if !ok || memorySecrets == nil {
			return AppSettings{}, ErrAtomicSystemSettingsUpdateUnavailable
		}
	} else if len(input.SecretChanges) != 0 {
		return AppSettings{}, ErrAtomicSystemSettingsUpdateUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = matchExpectedSystemSettingsRevision(input.ExpectedUpdatedAt, systemSettingsRevision(s.settings.UpdatedAt)); err != nil {
		return AppSettings{}, err
	}
	previous, err := systemSettingsRevisionTime(s.settings.UpdatedAt)
	if err != nil {
		return AppSettings{}, err
	}
	if memorySecrets != nil {
		memorySecrets.mu.Lock()
		defer memorySecrets.mu.Unlock()
	}
	if err = ctx.Err(); err != nil {
		return AppSettings{}, err
	}
	now := nextResourceUpdatedAt(previous, time.Now().UTC())
	for _, change := range input.SecretChanges {
		memorySecrets.updateSecretLocked(change.Name, change.Value, now)
	}
	normalized.UpdatedAt = resourceUpdatedAt(now)
	s.settings = normalized
	if memorySecrets != nil {
		normalized.SMTPPasswordConfigured = memorySecrets.secrets[AppSMTPPasswordSecretName].Configured
		normalized.TurnstileConfigured = memorySecrets.secrets[AppTurnstileSecretName].Configured
	}
	return normalized, nil
}
