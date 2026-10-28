package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

// These wrappers schedule one legitimate mutation after HTTP request
// preparation and immediately before the real store's atomic revision check.
// No store, authentication, secret persistence or response is replaced.
type systemCASAppRaceStore struct {
	store.AppSettingsStore
	beforeUpdate func(context.Context) error
}

func (s *systemCASAppRaceStore) UpdateAppSettingsRevision(ctx context.Context, input store.AppSettingsMutation, secrets store.SecretStore) (store.AppSettings, error) {
	if hook := s.beforeUpdate; hook != nil {
		s.beforeUpdate = nil
		if err := hook(ctx); err != nil {
			return store.AppSettings{}, err
		}
	}
	return store.UpdateAppSettingsWithRevision(ctx, s.AppSettingsStore, secrets, input)
}

type systemCASSecurityRaceStore struct {
	store.SecuritySettingsStore
	beforeUpdate func(context.Context) error
}

func (s *systemCASSecurityRaceStore) UpdateSecuritySettingsRevision(ctx context.Context, input store.SecuritySettings, expected *string) (store.SecuritySettings, error) {
	if hook := s.beforeUpdate; hook != nil {
		s.beforeUpdate = nil
		if err := hook(ctx); err != nil {
			return store.SecuritySettings{}, err
		}
	}
	return store.UpdateSecuritySettingsWithRevision(ctx, s.SecuritySettingsStore, input, expected)
}

var (
	_ store.AppSettingsRevisionStore      = (*systemCASAppRaceStore)(nil)
	_ store.SecuritySettingsRevisionStore = (*systemCASSecurityRaceStore)(nil)
)

type systemCASHTTPFixture struct {
	http     resourceCASHTTPFixture
	app      store.AppSettingsStore
	security store.SecuritySettingsStore
	appRace  *systemCASAppRaceStore
	secRace  *systemCASSecurityRaceStore
}

func TestSystemSettingsCASMemoryHTTP(t *testing.T) {
	runSystemSettingsCASHTTP(t, false)
}

func TestMariaDBSystemSettingsCASHTTP(t *testing.T) {
	runSystemSettingsCASHTTP(t, true)
}

func runSystemSettingsCASHTTP(t *testing.T, maria bool) {
	t.Setenv("AUTOSTREAM_ENV", "development")
	st := preparationClaimStores(t, maria)
	f := &systemCASHTTPFixture{http: resourceCASHTTPFixture{stores: st}}
	if maria {
		f.app = store.NewMariaDBAppSettingsStore(st.db)
		f.security = store.NewMariaDBSecuritySettingsStore(st.db)
	} else {
		f.app = store.NewMemoryAppSettingsStore()
		f.security = store.NewMemorySecuritySettingsStore()
	}
	f.appRace = &systemCASAppRaceStore{AppSettingsStore: f.app}
	f.secRace = &systemCASSecurityRaceStore{SecuritySettingsStore: f.security}
	password := outputSecretRandom(t)
	if _, err := st.auth.CreateFirstAdmin(t.Context(), "system-cas-admin", password, security.DefaultPermissions); err != nil {
		t.Fatal(err)
	}
	f.http.handler = f.server(st.secrets)
	f.http.cookie, f.http.csrf = loginForTest(t, f.http.handler, "system-cas-admin", password)
	t.Run("app_initial_race_fresh_legacy", f.appCase)
	t.Run("security_initial_race_fresh_legacy", f.securityCase)
	t.Run("secret_authority_preflight_rejection", f.secretAuthorityCase)
	if maria {
		t.Run("second_secret_failure_rolls_back", f.secondSecretFailureCase)
	}
}

func (f *systemCASHTTPFixture) server(secrets store.SecretStore) *Server {
	st := f.http.stores
	return NewServer(st.streams, WithAuthStore(st.auth), WithAuditStore(st.audit), WithServiceRegistryStore(st.services), WithAppSettingsStore(f.appRace), WithSecuritySettingsStore(f.secRace), WithSecretStore(secrets))
}

func systemCASAppSettings(name string) store.AppSettings {
	return store.AppSettings{
		AppName: name, Timezone: "Asia/Tokyo", SMTPEnabled: true,
		SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPStartTLS: true,
		SMTPFrom: "noreply@example.com", SMTPUsername: "cas-mailer", SMTPPasswordConfigured: true,
		TurnstileEnabled: true, TurnstileSiteKey: "cas-public-site-key", TurnstileConfigured: true,
	}
}

func systemCASAppBody(name, smtp, turnstile string, expected *string) map[string]any {
	body := map[string]any{
		"app_name": name, "timezone": "Asia/Tokyo", "smtp_enabled": true,
		"smtp_host": "smtp.example.com", "smtp_port": 587, "smtp_starttls": true,
		"smtp_from": "noreply@example.com", "smtp_username": "cas-mailer", "smtp_password": smtp,
		"turnstile_enabled": true, "turnstile_site_key": "cas-public-site-key", "turnstile_secret": turnstile,
	}
	if expected != nil {
		body["expected_updated_at"] = *expected
	}
	return body
}

func systemCASAppChanges(smtp, turnstile string) []store.SecretWrite {
	return []store.SecretWrite{{Name: store.AppSMTPPasswordSecretName, Value: smtp}, {Name: store.AppTurnstileSecretName, Value: turnstile}}
}

func (f *systemCASHTTPFixture) appGet(t *testing.T) store.AppSettings {
	t.Helper()
	return systemCASAppDocument(t, f.http.request(t, http.MethodGet, "/settings/app/manage", nil, http.StatusOK, ""))
}

func (f *systemCASHTTPFixture) appPut(t *testing.T, name, smtp, turnstile string, expected *string) store.AppSettings {
	t.Helper()
	return systemCASAppDocument(t, f.http.request(t, http.MethodPut, "/settings/app", systemCASAppBody(name, smtp, turnstile, expected), http.StatusOK, ""))
}

func (f *systemCASHTTPFixture) appCase(t *testing.T) {
	values := f.http.values(t, 10)
	initial := f.appGet(t)
	if initial.UpdatedAt != store.UnconfiguredSystemSettingsRevision || f.appGet(t).UpdatedAt != initial.UpdatedAt {
		t.Fatal("initial application GET lacks a stable unconfigured baseline")
	}
	first := f.appPut(t, "App initial", values[0], values[1], &initial.UpdatedAt)
	systemCASRevisionAdvanced(t, initial.UpdatedAt, first.UpdatedAt)
	f.appSaved(t, first, "App initial", values[0], values[1])
	f.http.request(t, http.MethodPut, "/settings/app", systemCASAppBody("App obsolete initial", values[4], values[5], &initial.UpdatedAt), http.StatusConflict, "revision_conflict")
	f.appSaved(t, first, "App initial", values[0], values[1])

	baseline := f.appGet(t)
	var winner store.AppSettings
	hookRan := false
	f.appRace.beforeUpdate = func(ctx context.Context) error {
		hookRan = true
		var err error
		winner, err = store.UpdateAppSettingsWithRevision(ctx, f.app, f.http.stores.secrets, store.AppSettingsMutation{Settings: systemCASAppSettings("App competing"), ExpectedUpdatedAt: &baseline.UpdatedAt, SecretChanges: systemCASAppChanges(values[2], values[3])})
		return err
	}
	f.http.request(t, http.MethodPut, "/settings/app", systemCASAppBody("App stale", values[4], values[5], &baseline.UpdatedAt), http.StatusConflict, "revision_conflict")
	if !hookRan {
		t.Fatal("application HTTP PUT did not reach the store CAS boundary")
	}
	f.appSaved(t, winner, "App competing", values[2], values[3])
	systemCASRevisionAdvanced(t, baseline.UpdatedAt, winner.UpdatedAt)

	current := f.appGet(t)
	saved := f.appPut(t, "App accepted", values[6], values[7], &current.UpdatedAt)
	systemCASRevisionAdvanced(t, current.UpdatedAt, saved.UpdatedAt)
	f.appSaved(t, saved, "App accepted", values[6], values[7])
	legacy := f.appPut(t, "App legacy", values[8], values[9], nil)
	systemCASRevisionAdvanced(t, saved.UpdatedAt, legacy.UpdatedAt)
	f.appSaved(t, legacy, "App legacy", values[8], values[9])

	// Enabled settings with blank write-only fields retain both secrets.
	kept := f.appPut(t, "App credentials retained", "", "", &legacy.UpdatedAt)
	systemCASRevisionAdvanced(t, legacy.UpdatedAt, kept.UpdatedAt)
	f.appSaved(t, kept, "App credentials retained", values[8], values[9])
	rowBeforeInvalid, err := f.app.GetAppSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secretsBeforeInvalid := f.http.secretSnapshot(t)
	for _, invalid := range resourceCASInvalidRevisions(false) {
		t.Run("invalid_expected_"+invalid.name, func(t *testing.T) {
			body := systemCASAppBody("App rejected revision", values[4], values[5], nil)
			body["expected_updated_at"] = invalid.value
			f.http.request(t, http.MethodPut, "/settings/app", body, http.StatusBadRequest, "bad_request")
			row, err := f.app.GetAppSettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !systemCASJSONEqual(t, row, rowBeforeInvalid) || !systemCASJSONEqual(t, f.appGet(t), kept) {
				t.Fatal("invalid expected revision changed the full application settings or revision")
			}
			f.http.secretsUnchanged(t, secretsBeforeInvalid)
		})
	}
}

func (f *systemCASHTTPFixture) appSaved(t *testing.T, receipt store.AppSettings, name, smtp, turnstile string) {
	t.Helper()
	if receipt.AppName != name || !receipt.SMTPPasswordConfigured || !receipt.TurnstileConfigured {
		t.Fatal("application mutation receipt lost the saved settings or credential status")
	}
	row, err := f.app.GetAppSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !systemCASJSONEqual(t, row, receipt) || !systemCASJSONEqual(t, f.appGet(t), receipt) {
		t.Fatal("application row or managed GET changed outside the winning mutation")
	}
	f.http.secretEquals(t, store.AppSMTPPasswordSecretName, smtp)
	f.http.secretEquals(t, store.AppTurnstileSecretName, turnstile)
}

func systemCASPolicy(length int) store.SecuritySettings {
	return store.SecuritySettings{PasswordMinLength: length, PasswordHash: "argon2id", LoginLockoutThreshold: 5, SessionIdleTimeoutMin: 30, SessionAbsoluteLifetimeH: 12, MFAMode: "disabled"}
}

func systemCASPolicyBody(length int, expected *string) map[string]any {
	body := map[string]any{"password_min_length": length, "password_hash": "argon2id", "login_lockout_threshold": 5, "session_idle_timeout_min": 30, "session_absolute_lifetime_h": 12, "remember_me_enabled": false, "mfa_mode": "disabled"}
	if expected != nil {
		body["expected_updated_at"] = *expected
	}
	return body
}

func (f *systemCASHTTPFixture) securityGet(t *testing.T) store.SecuritySettings {
	t.Helper()
	return systemCASSecurityDocument(t, f.http.request(t, http.MethodGet, "/security/settings", nil, http.StatusOK, ""))
}

func (f *systemCASHTTPFixture) securityPut(t *testing.T, length int, expected *string) store.SecuritySettings {
	t.Helper()
	return systemCASSecurityDocument(t, f.http.request(t, http.MethodPut, "/security/settings", systemCASPolicyBody(length, expected), http.StatusOK, ""))
}

func (f *systemCASHTTPFixture) securityCase(t *testing.T) {
	initial := f.securityGet(t)
	if initial.UpdatedAt != store.UnconfiguredSystemSettingsRevision || f.securityGet(t).UpdatedAt != initial.UpdatedAt {
		t.Fatal("initial security GET lacks a stable unconfigured baseline")
	}
	first := f.securityPut(t, 13, &initial.UpdatedAt)
	systemCASRevisionAdvanced(t, initial.UpdatedAt, first.UpdatedAt)
	f.securitySaved(t, first, 13)
	f.http.request(t, http.MethodPut, "/security/settings", systemCASPolicyBody(18, &initial.UpdatedAt), http.StatusConflict, "revision_conflict")
	f.securitySaved(t, first, 13)

	baseline := f.securityGet(t)
	var winner store.SecuritySettings
	hookRan := false
	f.secRace.beforeUpdate = func(ctx context.Context) error {
		hookRan = true
		var err error
		winner, err = store.UpdateSecuritySettingsWithRevision(ctx, f.security, systemCASPolicy(14), &baseline.UpdatedAt)
		return err
	}
	f.http.request(t, http.MethodPut, "/security/settings", systemCASPolicyBody(19, &baseline.UpdatedAt), http.StatusConflict, "revision_conflict")
	if !hookRan {
		t.Fatal("security HTTP PUT did not reach the store CAS boundary")
	}
	f.securitySaved(t, winner, 14)
	systemCASRevisionAdvanced(t, baseline.UpdatedAt, winner.UpdatedAt)
	current := f.securityGet(t)
	saved := f.securityPut(t, 15, &current.UpdatedAt)
	systemCASRevisionAdvanced(t, current.UpdatedAt, saved.UpdatedAt)
	f.securitySaved(t, saved, 15)
	legacy := f.securityPut(t, 16, nil)
	systemCASRevisionAdvanced(t, saved.UpdatedAt, legacy.UpdatedAt)
	f.securitySaved(t, legacy, 16)
	rowBeforeInvalid, err := f.security.GetSecuritySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secretsBeforeInvalid := f.http.secretSnapshot(t)
	for _, invalid := range resourceCASInvalidRevisions(false) {
		t.Run("invalid_expected_"+invalid.name, func(t *testing.T) {
			body := systemCASPolicyBody(20, nil)
			body["expected_updated_at"] = invalid.value
			f.http.request(t, http.MethodPut, "/security/settings", body, http.StatusBadRequest, "bad_request")
			row, err := f.security.GetSecuritySettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !systemCASJSONEqual(t, row, rowBeforeInvalid) || !systemCASJSONEqual(t, f.securityGet(t), legacy) {
				t.Fatal("invalid expected revision changed the full security policy or revision")
			}
			f.http.secretsUnchanged(t, secretsBeforeInvalid)
		})
	}
}

func (f *systemCASHTTPFixture) securitySaved(t *testing.T, receipt store.SecuritySettings, length int) {
	t.Helper()
	if receipt.PasswordMinLength != length || receipt.PasswordHash != "argon2id" || receipt.MFAMode != "disabled" {
		t.Fatal("security mutation did not return the winning policy")
	}
	row, err := f.security.GetSecuritySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !systemCASJSONEqual(t, row, receipt) || !systemCASJSONEqual(t, f.securityGet(t), receipt) {
		t.Fatal("security row or GET changed outside the winning mutation")
	}
}

// A different secret authority must fail before writing. This proves the
// Memory preflight boundary, independently of the SQL mid-write rollback case.
type systemCASUnsupportedSecretAuthority struct{ store.SecretStore }

func (f *systemCASHTTPFixture) secretAuthorityCase(t *testing.T) {
	before := f.appGet(t)
	prior := f.appSecrets(t)
	values := f.http.values(t, 2)
	original := f.http.handler
	f.http.handler = f.server(systemCASUnsupportedSecretAuthority{SecretStore: f.http.stores.secrets})
	defer func() { f.http.handler = original }()
	f.http.request(t, http.MethodPut, "/settings/app", systemCASAppBody("App unsupported authority", values[0], values[1], &before.UpdatedAt), http.StatusInternalServerError, "app_settings_update_failed")
	f.appSaved(t, before, before.AppName, prior[0], prior[1])
}

func (f *systemCASHTTPFixture) secondSecretFailureCase(t *testing.T) {
	before := f.appGet(t)
	prior := f.appSecrets(t)
	values := f.http.values(t, 2)
	trigger := "system_settings_cas_" + outputSecretRandom(t)[:16]
	// INSERT ... ON DUPLICATE KEY UPDATE still executes BEFORE INSERT. The
	// SMTP write succeeds inside the transaction before Turnstile is rejected.
	query := `CREATE TRIGGER ` + trigger + ` BEFORE INSERT ON secrets FOR EACH ROW BEGIN IF NEW.name='app_turnstile_secret' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='system settings second secret fixture'; END IF; END`
	if _, err := f.http.stores.db.ExecContext(t.Context(), query); err != nil {
		t.Fatal("could not install isolated-schema secret failure fixture")
	}
	t.Cleanup(func() {
		if _, err := f.http.stores.db.ExecContext(context.WithoutCancel(t.Context()), `DROP TRIGGER IF EXISTS `+trigger); err != nil {
			t.Error("could not remove isolated-schema secret failure fixture")
		}
	})
	f.http.request(t, http.MethodPut, "/settings/app", systemCASAppBody("App rolled back", values[0], values[1], &before.UpdatedAt), http.StatusInternalServerError, "app_settings_update_failed")
	f.appSaved(t, before, before.AppName, prior[0], prior[1])
}

func (f *systemCASHTTPFixture) appSecrets(t *testing.T) [2]string {
	t.Helper()
	var values [2]string
	for i, name := range []string{store.AppSMTPPasswordSecretName, store.AppTurnstileSecretName} {
		var err error
		values[i], err = f.http.stores.secrets.GetSecretValue(t.Context(), name)
		if err != nil {
			t.Fatal("stored application credential lookup failed")
		}
	}
	return values
}

func systemCASAppDocument(t *testing.T, response *httptest.ResponseRecorder) store.AppSettings {
	t.Helper()
	var settings store.AppSettings
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
		t.Fatal("application settings response decoding failed")
	}
	if settings.AppName == "" {
		t.Fatal("application response lacks its settings")
	}
	systemCASValidRevision(t, settings.UpdatedAt)
	return settings
}

func systemCASSecurityDocument(t *testing.T, response *httptest.ResponseRecorder) store.SecuritySettings {
	t.Helper()
	var settings store.SecuritySettings
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
		t.Fatal("security settings response decoding failed")
	}
	if settings.PasswordMinLength < 8 {
		t.Fatal("security response lacks its policy")
	}
	systemCASValidRevision(t, settings.UpdatedAt)
	return settings
}

func systemCASValidRevision(t *testing.T, revision string) {
	t.Helper()
	if revision == store.UnconfiguredSystemSettingsRevision {
		return
	}
	parsed, err := time.Parse(time.RFC3339Nano, revision)
	if err != nil || parsed.IsZero() || resourceCASStamp(parsed) != revision {
		t.Fatal("system settings response lacks its canonical revision")
	}
}

func systemCASRevisionAdvanced(t *testing.T, previous, current string) {
	t.Helper()
	systemCASValidRevision(t, current)
	if current == store.UnconfiguredSystemSettingsRevision {
		t.Fatal("successful system settings mutation stayed unconfigured")
	}
	if previous != store.UnconfiguredSystemSettingsRevision {
		resourceCASAdvanced(t, previous, current)
	}
}

func systemCASJSONEqual(t *testing.T, left, right any) bool {
	t.Helper()
	a, leftErr := json.Marshal(left)
	b, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		t.Fatal("system settings snapshot encoding failed")
	}
	return bytes.Equal(a, b)
}
