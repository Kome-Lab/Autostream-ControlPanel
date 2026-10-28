package store

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMemorySystemSettingsAppCASAndSecretAuthority(t *testing.T) {
	settings, secrets := NewMemoryAppSettingsStore(), NewMemorySecretStore()
	initial, err := settings.GetAppSettings(t.Context())
	if err != nil || initial.UpdatedAt != UnconfiguredSystemSettingsRevision {
		t.Fatal("missing APP settings lack stable authority")
	}
	input := AppSettingsMutation{Settings: AppSettings{AppName: "Panel", Timezone: "Asia/Tokyo", SMTPEnabled: true, SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPStartTLS: true, SMTPFrom: "operator@example.com", SMTPUsername: "operator", SMTPPasswordConfigured: true, TurnstileEnabled: true, TurnstileSiteKey: "synthetic-site", TurnstileConfigured: true}, ExpectedUpdatedAt: &initial.UpdatedAt, SecretChanges: []SecretWrite{{AppSMTPPasswordSecretName, "synthetic-initial-smtp"}, {AppTurnstileSecretName, "synthetic-initial-turnstile"}}}
	saved, err := settings.UpdateAppSettingsRevision(t.Context(), input, secrets)
	if err != nil || saved.UpdatedAt == initial.UpdatedAt || !saved.SMTPPasswordConfigured || !saved.TurnstileConfigured {
		t.Fatal("APP first CAS did not publish settings and secret status")
	}
	assertUnchanged := func() {
		t.Helper()
		current, err := settings.GetAppSettings(t.Context())
		smtp, smtpErr := secrets.GetSecretValue(t.Context(), AppSMTPPasswordSecretName)
		turnstile, turnstileErr := secrets.GetSecretValue(t.Context(), AppTurnstileSecretName)
		if err != nil || smtpErr != nil || turnstileErr != nil || !reflect.DeepEqual(current, saved) || smtp != "synthetic-initial-smtp" || turnstile != "synthetic-initial-turnstile" {
			t.Fatal("rejected APP update partially changed settings or credentials")
		}
	}
	input.Settings.AppName = "stale"
	input.SecretChanges = []SecretWrite{{AppSMTPPasswordSecretName, "synthetic-stale-smtp"}, {AppTurnstileSecretName, "synthetic-stale-turnstile"}}
	if _, err = settings.UpdateAppSettingsRevision(t.Context(), input, secrets); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale initial authority accepted")
	}
	assertUnchanged()
	input.ExpectedUpdatedAt = &saved.UpdatedAt
	input.SecretChanges[1].Name = "unregistered-secret"
	if _, err = settings.UpdateAppSettingsRevision(t.Context(), input, secrets); err == nil {
		t.Fatal("invalid second secret passed preflight")
	}
	assertUnchanged()
	input.SecretChanges = []SecretWrite{{AppSMTPPasswordSecretName, "synthetic-other"}}
	if _, err = settings.UpdateAppSettingsRevision(t.Context(), input, struct{ SecretStore }{secrets}); !errors.Is(err, ErrAtomicSystemSettingsUpdateUnavailable) {
		t.Fatal("unsupported credential authority was accepted")
	}
	assertUnchanged()
	blank := ""
	input.ExpectedUpdatedAt = &blank
	if _, err = settings.UpdateAppSettingsRevision(t.Context(), input, secrets); !errors.Is(err, ErrInvalidResourceRevision) {
		t.Fatal("blank supplied baseline was treated as legacy omission")
	}
	assertUnchanged()
}

func TestMemorySystemSettingsSameBaselineHasOneWinner(t *testing.T) {
	app, security := NewMemoryAppSettingsStore(), NewMemorySecuritySettingsStore()
	for _, kind := range []string{"app", "security"} {
		t.Run(kind, func(t *testing.T) {
			initial := UnconfiguredSystemSettingsRevision
			start := make(chan struct{})
			outcomes := make(chan error, 2)
			var group sync.WaitGroup
			for range 2 {
				group.Add(1)
				go func() {
					defer group.Done()
					<-start
					var err error
					if kind == "app" {
						_, err = app.UpdateAppSettingsRevision(t.Context(), AppSettingsMutation{Settings: AppSettings{AppName: "Panel", Timezone: "Asia/Tokyo"}, ExpectedUpdatedAt: &initial}, nil)
					} else {
						_, err = security.UpdateSecuritySettingsRevision(t.Context(), defaultSecurityConfig, &initial)
					}
					outcomes <- err
				}()
			}
			close(start)
			group.Wait()
			passed, conflicted := 0, 0
			for range 2 {
				err := <-outcomes
				if err == nil {
					passed++
				} else if errors.Is(err, ErrRevisionConflict) {
					conflicted++
				} else {
					t.Fatal("unexpected settings CAS failure")
				}
			}
			if passed != 1 || conflicted != 1 {
				t.Fatal("a shared initial baseline did not have exactly one winner")
			}
		})
	}
	before, err := security.GetSecuritySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.UpdateAppSettings(t.Context(), AppSettings{AppName: "Legacy", Timezone: "Asia/Tokyo"}); err != nil {
		t.Fatal(err)
	}
	updated, err := security.UpdateSecuritySettingsRevision(t.Context(), defaultSecurityConfig, &before.UpdatedAt)
	if err != nil {
		t.Fatal("APP changes invalidated the independent Security authority")
	}
	previous, err := time.Parse(time.RFC3339Nano, before.UpdatedAt)
	current, parseErr := time.Parse(time.RFC3339Nano, updated.UpdatedAt)
	if err != nil || parseErr != nil || !current.After(previous) || resourceUpdatedAt(current) != updated.UpdatedAt {
		t.Fatal("Security CAS did not advance its canonical microsecond clock")
	}
	legacy, err := security.UpdateSecuritySettings(t.Context(), defaultSecurityConfig)
	if err != nil || legacy.UpdatedAt == updated.UpdatedAt {
		t.Fatal("legacy Security writer did not advance the same authority")
	}
}
