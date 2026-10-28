package store

import (
	"context"
	"errors"
	"time"
)

const UnconfiguredSystemSettingsRevision = "unconfigured"

var ErrAtomicSystemSettingsUpdateUnavailable = errors.New("atomic system settings update unavailable")

type AppSettingsMutation struct {
	Settings          AppSettings
	ExpectedUpdatedAt *string
	SecretChanges     []SecretWrite
}

type AppSettingsRevisionStore interface {
	UpdateAppSettingsRevision(context.Context, AppSettingsMutation, SecretStore) (AppSettings, error)
}

type SecuritySettingsRevisionStore interface {
	UpdateSecuritySettingsRevision(context.Context, SecuritySettings, *string) (SecuritySettings, error)
}

func UpdateAppSettingsWithRevision(ctx context.Context, settings AppSettingsStore, secrets SecretStore, input AppSettingsMutation) (AppSettings, error) {
	if atomic, ok := settings.(AppSettingsRevisionStore); ok {
		return atomic.UpdateAppSettingsRevision(ctx, input, secrets)
	}
	if input.ExpectedUpdatedAt != nil || len(input.SecretChanges) != 0 {
		return AppSettings{}, ErrAtomicSystemSettingsUpdateUnavailable
	}
	return settings.UpdateAppSettings(ctx, input.Settings)
}

func UpdateSecuritySettingsWithRevision(ctx context.Context, settings SecuritySettingsStore, input SecuritySettings, expected *string) (SecuritySettings, error) {
	if atomic, ok := settings.(SecuritySettingsRevisionStore); ok {
		return atomic.UpdateSecuritySettingsRevision(ctx, input, expected)
	}
	if expected != nil {
		return SecuritySettings{}, ErrAtomicSystemSettingsUpdateUnavailable
	}
	return settings.UpdateSecuritySettings(ctx, input)
}

func validateExpectedSystemSettingsRevision(expected *string) error {
	if expected == nil || *expected == UnconfiguredSystemSettingsRevision {
		return nil
	}
	return validateExpectedResourceUpdatedAt(expected)
}

func matchExpectedSystemSettingsRevision(expected *string, current string) error {
	if err := validateExpectedSystemSettingsRevision(expected); err != nil {
		return err
	}
	if expected != nil && *expected != current {
		return ErrRevisionConflict
	}
	return nil
}

func systemSettingsRevisionTime(value string) (time.Time, error) {
	if value == "" || value == UnconfiguredSystemSettingsRevision {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func systemSettingsRevision(value string) string {
	if value == "" {
		return UnconfiguredSystemSettingsRevision
	}
	return value
}

func validateAppSecretChanges(changes []SecretWrite) error {
	if err := validateProfileSecretWrites(changes); err != nil {
		return err
	}
	for _, change := range changes {
		if change.Name != AppSMTPPasswordSecretName && change.Name != AppTurnstileSecretName {
			return ErrInvalidProfileSecretMutation
		}
	}
	return nil
}
