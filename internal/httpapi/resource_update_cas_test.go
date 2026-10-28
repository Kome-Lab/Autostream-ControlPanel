package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

// The hook runs after the real HTTP handler has read its input/profile and
// prepared its secret changes, immediately before the real store's CAS. Only
// this scheduling boundary is wrapped; validation, persistence and secrets use
// the same Memory/MariaDB implementations as the server.
type resourceCASRaceProfileStore struct {
	store.ProfileStore
	beforeUpdate func(context.Context, store.ProfileKind, string) error
}

var _ store.ProfileRevisionStore = (*resourceCASRaceProfileStore)(nil)

func (s *resourceCASRaceProfileStore) UpdateProfileRevision(ctx context.Context, kind store.ProfileKind, id string, input store.ProfileUpdate, secrets store.SecretStore) (store.Profile, error) {
	if hook := s.beforeUpdate; hook != nil {
		s.beforeUpdate = nil
		if err := hook(ctx, kind, id); err != nil {
			return store.Profile{}, err
		}
	}
	return store.UpdateProfileWithRevision(ctx, s.ProfileStore, secrets, kind, id, input)
}

type resourceCASHTTPFixture struct {
	stores       outputSecretStores
	profiles     *resourceCASRaceProfileStore
	integrations store.IntegrationStore
	handler      *Server
	cookie       *http.Cookie
	csrf         string
	rawValues    []string
}

func TestResourceUpdateCASMemoryHTTP(t *testing.T) {
	runResourceUpdateCASHTTP(t, false)
}

func TestMariaDBResourceUpdateCASHTTP(t *testing.T) {
	runResourceUpdateCASHTTP(t, true)
}

func runResourceUpdateCASHTTP(t *testing.T, maria bool) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	t.Setenv("AUTOSTREAM_ENV", "development")
	st := preparationClaimStores(t, maria)
	f := &resourceCASHTTPFixture{stores: st, profiles: &resourceCASRaceProfileStore{ProfileStore: st.profiles}}
	if st.db == nil {
		f.integrations = store.NewMemoryIntegrationStore()
		// The wrapper must not hide the memory store's normal relay claim fence.
		st.profiles.(*store.MemoryProfileStore).BindStreamYouTubeRelayBindingClaims(st.streams.(*store.MemoryStreamStore))
	} else {
		f.integrations = store.NewMariaDBIntegrationStore(st.db, outputSecretRandom(t))
	}
	password := outputSecretRandom(t)
	if _, err := st.auth.CreateFirstAdmin(t.Context(), "resource-cas-admin", password, security.DefaultPermissions); err != nil {
		t.Fatal(err)
	}
	f.handler = NewServer(st.streams, WithAuthStore(st.auth), WithAuditStore(st.audit), WithServiceRegistryStore(st.services), WithProfileStore(f.profiles), WithSecretStore(st.secrets), WithIntegrationStore(f.integrations), WithRuntimeSecretLeaseStore(st.leases))
	f.cookie, f.csrf = loginForTest(t, f.handler, "resource-cas-admin", password)
	botID := p2RegisterSQLService(t, st.services, "discord_bot")
	for _, tc := range []resourceCASProfileCase{
		{store.ProfileEncoder, "/profiles/encoder", "", "", ""},
		{store.ProfileArchive, "/profiles/archive", "", "", ""},
		{store.ProfileCaption, "/profiles/caption", "", "", ""},
		{store.ProfileOverlay, "/profiles/overlay", "", "", ""},
		{store.ProfileDiscordConfig, "/discord/configs", "bot_token", "bot_token_secret_name", "bot_token_fingerprint"},
		{store.ProfileYouTubeOutput, "/youtube/outputs", "stream_key", "stream_key_secret_name", "stream_key_fingerprint"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) { f.profileCase(t, tc, botID) })
	}
	t.Run("drive_destination", func(t *testing.T) { f.driveCase(t) })
}

type resourceCASProfileCase struct {
	kind              store.ProfileKind
	path              string
	secretField       string
	secretReference   string
	secretFingerprint string
}

func (c resourceCASProfileCase) body(name, secret, botID string, variant int) map[string]any {
	body := map[string]any{"name": name}
	switch c.kind {
	case store.ProfileEncoder:
		body["config"] = map[string]any{"width": 1920, "height": 1080, "fps": 30, "video_bitrate_kbps": 6000 + variant}
	case store.ProfileArchive:
		body["config"] = map[string]any{"retention_days": 7 + variant}
	case store.ProfileCaption:
		body["config"] = map[string]any{"language": "ja", "delay_ms": 800 + variant}
	case store.ProfileOverlay:
		body["config"] = map[string]any{"chat_enabled": true, "chat_max_messages": 5 + variant}
	case store.ProfileDiscordConfig:
		body["service_id"], body["reconnect_max_attempts"] = botID, 3+variant
	case store.ProfileYouTubeOutput:
		body["mode"], body["privacy_status"] = "stream_key", "unlisted"
		body["watch_url"] = "https://www.youtube.com/watch?v=output-test"
	}
	if c.secretField != "" {
		body[c.secretField] = secret
	}
	return body
}

func (f *resourceCASHTTPFixture) profileCase(t *testing.T, c resourceCASProfileCase, botID string) {
	values := f.values(t, 5)
	initial, competing, stale, accepted, legacy := values[0], values[1], values[2], values[3], values[4]
	created := f.request(t, http.MethodPost, c.path, c.body(string(c.kind)+" initial", initial, botID, 0), http.StatusCreated, "")
	identity := resourceCASDocument(t, created)
	path := c.path + "/" + identity.ID
	baseline := resourceCASDocument(t, f.request(t, http.MethodGet, path, nil, http.StatusOK, ""))
	prior, err := f.stores.profiles.GetProfile(t.Context(), c.kind, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resourceCASStamp(prior.UpdatedAt) != baseline.UpdatedAt {
		t.Fatal("HTTP baseline does not represent the stored profile revision")
	}
	secretName := configString(prior.Config, c.secretReference)
	if c.secretField != "" {
		if secretName == "" {
			t.Fatal("normal HTTP creation did not store the profile secret reference")
		}
		f.secretEquals(t, secretName, initial)
	}

	var winner store.Profile
	hookRan := false
	f.profiles.beforeUpdate = func(ctx context.Context, kind store.ProfileKind, id string) error {
		hookRan = true
		if kind != c.kind || id != identity.ID {
			return errors.New("unexpected resource CAS hook target")
		}
		config := map[string]any{}
		for key, value := range prior.Config {
			config[key] = value
		}
		changes := []store.SecretWrite{}
		if c.secretField != "" {
			config[c.secretFingerprint] = security.SecretFingerprint(competing)
			changes = append(changes, store.SecretWrite{Name: secretName, Value: competing})
		}
		var updateErr error
		winner, updateErr = store.UpdateProfileWithRevision(ctx, f.stores.profiles, f.stores.secrets, kind, id, store.ProfileUpdate{Name: string(kind) + " competing", Config: config, ExpectedUpdatedAt: &baseline.UpdatedAt, SecretChanges: changes})
		return updateErr
	}
	staleBody := c.body(string(c.kind)+" stale", stale, botID, 1)
	staleBody["expected_updated_at"] = baseline.UpdatedAt
	f.request(t, http.MethodPut, path, staleBody, http.StatusConflict, "revision_conflict")
	if !hookRan {
		t.Fatal("the HTTP PUT did not reach the real store CAS boundary")
	}
	current, err := f.stores.profiles.GetProfile(t.Context(), c.kind, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != winner.Name || !reflect.DeepEqual(current.Config, winner.Config) || !current.UpdatedAt.Equal(winner.UpdatedAt) || current.YouTubeRelayBindingRevision != winner.YouTubeRelayBindingRevision {
		t.Fatal("stale HTTP PUT partially changed the winning profile")
	}
	if c.secretField != "" {
		f.secretEquals(t, secretName, competing)
	}
	afterRace := resourceCASDocument(t, f.request(t, http.MethodGet, path, nil, http.StatusOK, ""))
	if afterRace.Name != winner.Name || afterRace.UpdatedAt != resourceCASStamp(winner.UpdatedAt) {
		t.Fatal("GET did not preserve the competing profile revision")
	}
	resourceCASAdvanced(t, baseline.UpdatedAt, afterRace.UpdatedAt)

	acceptedBody := c.body(string(c.kind)+" accepted", accepted, botID, 2)
	acceptedBody["expected_updated_at"] = afterRace.UpdatedAt
	saved := resourceCASDocument(t, f.request(t, http.MethodPut, path, acceptedBody, http.StatusOK, ""))
	if saved.Name != acceptedBody["name"] {
		t.Fatal("baseline-authorized profile update did not save the requested name")
	}
	resourceCASAdvanced(t, afterRace.UpdatedAt, saved.UpdatedAt)
	f.profileSaved(t, c.kind, identity.ID, saved, secretName, accepted)
	// Legacy callers omit the optional field. They still use the atomic store
	// path, including credential writes, and receive the advanced revision.
	legacyBody := c.body(string(c.kind)+" legacy", legacy, botID, 3)
	legacySaved := resourceCASDocument(t, f.request(t, http.MethodPut, path, legacyBody, http.StatusOK, ""))
	if legacySaved.Name != legacyBody["name"] {
		t.Fatal("legacy profile update did not save the requested name")
	}
	resourceCASAdvanced(t, saved.UpdatedAt, legacySaved.UpdatedAt)
	f.profileSaved(t, c.kind, identity.ID, legacySaved, secretName, legacy)
	rowBeforeInvalid, err := f.stores.profiles.GetProfile(t.Context(), c.kind, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	secretsBeforeInvalid := f.secretSnapshot(t)
	for _, invalid := range resourceCASInvalidRevisions(true) {
		t.Run("invalid_expected_"+invalid.name, func(t *testing.T) {
			body := c.body(string(c.kind)+" rejected revision", stale, botID, 4)
			body["expected_updated_at"] = invalid.value
			f.request(t, http.MethodPut, path, body, http.StatusBadRequest, "bad_request")
			row, err := f.stores.profiles.GetProfile(t.Context(), c.kind, identity.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(row, rowBeforeInvalid) {
				t.Fatal("invalid expected revision changed the full stored profile or revision")
			}
			f.secretsUnchanged(t, secretsBeforeInvalid)
		})
	}
}

func (f *resourceCASHTTPFixture) profileSaved(t *testing.T, kind store.ProfileKind, id string, expected resourceCASIdentity, secretName, secretValue string) {
	t.Helper()
	actual, err := f.stores.profiles.GetProfile(t.Context(), kind, id)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Name != expected.Name || resourceCASStamp(actual.UpdatedAt) != expected.UpdatedAt {
		t.Fatal("successful HTTP profile response does not match its stored row")
	}
	if secretName != "" {
		f.secretEquals(t, secretName, secretValue)
	}
}

func (f *resourceCASHTTPFixture) driveCase(t *testing.T) {
	values := f.values(t, 6)
	providerSecret, refresh, initialFolder, competingFolder, staleFolder, acceptedFolder := values[0], values[1], values[2], values[3], values[4], values[5]
	provider, err := f.integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{ProviderType: "google", Name: "CAS Drive provider", Enabled: true, ClientID: "cas-drive-client", ClientSecret: providerSecret, RedirectURI: "https://control.example.com/integrations/oauth-accounts/callback"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := f.integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{ProviderID: provider.ID, ProviderType: "google", AccountLabel: "CAS Drive account", Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, RefreshToken: refresh})
	if err != nil {
		t.Fatal(err)
	}
	body := func(name, folder string, shared bool) map[string]any {
		return map[string]any{"name": name, "auth_mode": "oauth2", "oauth_account_id": account.ID, "folder_id": folder, "shared_drive": shared}
	}
	created := resourceCASDocument(t, f.request(t, http.MethodPost, "/archive/destinations", body("Drive initial", initialFolder, false), http.StatusCreated, ""))
	path := "/archive/destinations/" + created.ID
	baseline := resourceCASDocument(t, f.request(t, http.MethodGet, path, nil, http.StatusOK, ""))
	competingBody := body("Drive competing", competingFolder, true)
	competingBody["expected_updated_at"] = baseline.UpdatedAt
	winner := resourceCASDocument(t, f.request(t, http.MethodPut, path, competingBody, http.StatusOK, ""))
	resourceCASAdvanced(t, baseline.UpdatedAt, winner.UpdatedAt)
	winnerRow, err := f.integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleBody := body("Drive stale", staleFolder, false)
	staleBody["expected_updated_at"] = baseline.UpdatedAt
	f.request(t, http.MethodPut, path, staleBody, http.StatusConflict, "revision_conflict")
	afterRace, err := f.integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterRace, winnerRow) || afterRace.FolderID != competingFolder {
		t.Fatal("stale HTTP PUT partially changed the Drive destination or folder")
	}
	current := resourceCASDocument(t, f.request(t, http.MethodGet, path, nil, http.StatusOK, ""))
	if current.Name != winner.Name || current.UpdatedAt != winner.UpdatedAt {
		t.Fatal("GET did not preserve the competing Drive revision")
	}
	acceptedBody := body("Drive accepted", acceptedFolder, false)
	acceptedBody["expected_updated_at"] = current.UpdatedAt
	saved := resourceCASDocument(t, f.request(t, http.MethodPut, path, acceptedBody, http.StatusOK, ""))
	if saved.Name != "Drive accepted" {
		t.Fatal("baseline-authorized Drive update did not save the requested name")
	}
	resourceCASAdvanced(t, current.UpdatedAt, saved.UpdatedAt)
	f.driveSaved(t, created.ID, saved, acceptedFolder)
	legacy := resourceCASDocument(t, f.request(t, http.MethodPut, path, body("Drive legacy", "", true), http.StatusOK, ""))
	if legacy.Name != "Drive legacy" {
		t.Fatal("legacy Drive update did not save the requested name")
	}
	resourceCASAdvanced(t, saved.UpdatedAt, legacy.UpdatedAt)
	f.driveSaved(t, created.ID, legacy, acceptedFolder)
	rowBeforeInvalid, err := f.integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	secretsBeforeInvalid := f.secretSnapshot(t)
	for _, invalid := range resourceCASInvalidRevisions(true) {
		t.Run("invalid_expected_"+invalid.name, func(t *testing.T) {
			invalidBody := body("Drive rejected revision", staleFolder, false)
			invalidBody["expected_updated_at"] = invalid.value
			f.request(t, http.MethodPut, path, invalidBody, http.StatusBadRequest, "bad_request")
			row, err := f.integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(row, rowBeforeInvalid) {
				t.Fatal("invalid expected revision changed the full Drive destination, folder or revision")
			}
			f.secretsUnchanged(t, secretsBeforeInvalid)
		})
	}
}

func (f *resourceCASHTTPFixture) driveSaved(t *testing.T, id string, expected resourceCASIdentity, folder string) {
	t.Helper()
	actual, err := f.integrations.GetDriveDestinationForDispatch(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Name != expected.Name || actual.UpdatedAt != expected.UpdatedAt || actual.FolderID != folder {
		t.Fatal("successful HTTP Drive response does not match its stored row/folder")
	}
}

func (f *resourceCASHTTPFixture) secretEquals(t *testing.T, name, expected string) {
	t.Helper()
	actual, err := f.stores.secrets.GetSecretValue(t.Context(), name)
	if err != nil {
		t.Fatal("stored credential lookup failed")
	}
	if actual != expected {
		t.Fatal("stored credential differs from the winning update")
	}
}

func (f *resourceCASHTTPFixture) values(t *testing.T, count int) []string {
	t.Helper()
	values := make([]string, count)
	for i := range values {
		values[i] = outputSecretRandom(t)
		f.rawValues = append(f.rawValues, values[i])
	}
	return values
}

func (f *resourceCASHTTPFixture) request(t *testing.T, method, path string, body map[string]any, status int, code string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal("request encoding failed")
		}
		encoded = string(data)
	}
	res := serveUserJSON(t, f.handler, method, path, encoded, f.cookie, f.csrf)
	for _, raw := range f.rawValues {
		if bytes.Contains(res.Body.Bytes(), []byte(raw)) {
			t.Fatal("HTTP resource response exposed a credential or raw folder identifier")
		}
	}
	var result struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatalf("HTTP resource response is not JSON: status=%d", res.Code)
	}
	if res.Code != status || result.Code != code {
		t.Fatalf("HTTP resource response status=%d code=%s; want status=%d code=%s", res.Code, result.Code, status, code)
	}
	return res
}

type resourceCASIdentity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt string `json:"updated_at"`
}

func resourceCASDocument(t *testing.T, response *httptest.ResponseRecorder) resourceCASIdentity {
	t.Helper()
	var document resourceCASIdentity
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal("resource document decoding failed")
	}
	updated, err := time.Parse(time.RFC3339Nano, document.UpdatedAt)
	if document.ID == "" || document.Name == "" || err != nil || resourceCASStamp(updated) != document.UpdatedAt {
		t.Fatal("resource response lacks a canonical microsecond revision")
	}
	return document
}

func resourceCASAdvanced(t *testing.T, previous, current string) {
	t.Helper()
	before, beforeErr := time.Parse(time.RFC3339Nano, previous)
	after, afterErr := time.Parse(time.RFC3339Nano, current)
	if beforeErr != nil || afterErr != nil || !after.After(before) {
		t.Fatal("successful resource update did not advance updated_at")
	}
}

func resourceCASStamp(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

type resourceCASInvalidRevision struct {
	name  string
	value any
}

func resourceCASInvalidRevisions(unconfiguredInvalid bool) []resourceCASInvalidRevision {
	values := []resourceCASInvalidRevision{
		{"null", nil},
		{"number", 42},
		{"boolean", true},
		{"array", []any{}},
		{"object", map[string]any{}},
		{"empty", ""},
		{"offset", "2026-10-28T00:00:00+00:00"},
		{"submicrosecond", "2026-10-28T00:00:00.123456789Z"},
	}
	if unconfiguredInvalid {
		values = append(values, resourceCASInvalidRevision{"unconfigured", "unconfigured"})
	}
	return values
}

type resourceCASSecretSnapshot struct {
	statuses []store.SecretStatus
	values   map[string]string
}

func (f *resourceCASHTTPFixture) secretSnapshot(t *testing.T) resourceCASSecretSnapshot {
	t.Helper()
	statuses, err := f.stores.secrets.ListSecretStatus(t.Context())
	if err != nil {
		t.Fatal("credential status snapshot failed")
	}
	values := map[string]string{}
	for _, status := range statuses {
		if !status.Configured {
			continue
		}
		value, err := f.stores.secrets.GetSecretValue(t.Context(), status.Name)
		if err != nil {
			t.Fatal("credential value snapshot failed")
		}
		values[status.Name] = value
	}
	return resourceCASSecretSnapshot{statuses: statuses, values: values}
}

func (f *resourceCASHTTPFixture) secretsUnchanged(t *testing.T, expected resourceCASSecretSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(f.secretSnapshot(t), expected) {
		t.Fatal("rejected expected revision changed credential values or metadata")
	}
}
