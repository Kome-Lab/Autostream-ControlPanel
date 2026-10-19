package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

// The same saved waiting candidate is deliberately retained throughout. The
// oracle checks the authenticated public projection, not a mocked DTO builder.
func TestUI188CB15CurrentBindingSurvivesOtherWaitingCandidate(t *testing.T) {
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	profiles, secrets := store.NewMemoryProfileStore(), store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "binding-bot", ServiceType: "discord_bot", ServiceName: "Binding Bot", Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	status, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_cb15", "synthetic-cb15-bot-credential")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "binding profile", map[string]any{
		"service_id": "binding-bot", "guild_id": "100000000000000001", "voice_channel_id": "100000000000000003",
		"bot_token_secret_name": status.Name, "bot_token_fingerprint": status.Fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := streams.CreateStream(t.Context(), "current")
	if err != nil {
		t.Fatal(err)
	}
	other, err := streams.CreateStream(t.Context(), "other retained waiting")
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []store.Stream{current, other} {
		_, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: profile.ID, AutoStartTrigger: "discord_voice_join"})
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))
	if _, err = auth.AssignServiceToStream(t.Context(), "binding-bot", current.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=binding-bot", nil)
		r.Header.Set("Authorization", "Bearer "+token.RawToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("runtime GET status %d", w.Code)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	waiting := read()
	var before []store.StreamServiceAssignment
	if err := json.Unmarshal(waiting["assignments"], &before); err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].StreamID != current.ID {
		t.Fatal("initial primary assignment not established")
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), current.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	starting := read()
	var after []store.StreamServiceAssignment
	if err := json.Unmarshal(starting["assignments"], &after); err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].StreamID != current.ID || !after[0].AssignedAt.Equal(before[0].AssignedAt) {
		t.Fatal("current assignment or epoch changed")
	}
	var candidates []serviceRuntimeDiscordStreamConfig
	if err := json.Unmarshal(starting["stream_discord_configs"], &candidates); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].StreamID != other.ID || candidates[0].DiscordConfigID != profile.ID {
		t.Fatal("other created candidate was hidden or active stream polluted waiting list")
	}
	var binding struct {
		Version  int `json:"version"`
		Bindings []struct {
			StreamID  string `json:"stream_id"`
			ProfileID string `json:"profile_id"`
			Ready     bool   `json:"ready"`
		} `json:"bindings"`
	}
	if len(starting["current_discord_bindings"]) == 0 {
		t.Fatal("current credential binding absent while another waiting candidate remains")
	}
	if err := json.Unmarshal(starting["current_discord_bindings"], &binding); err != nil {
		t.Fatal(err)
	}
	if binding.Version != 1 || len(binding.Bindings) != 1 || binding.Bindings[0].StreamID != current.ID || binding.Bindings[0].ProfileID != profile.ID || !binding.Bindings[0].Ready {
		t.Fatal("current credential binding does not authorize exact primary")
	}
}

func TestUI188CB15CurrentResolverMemoryHTTP(t *testing.T) { cb15CurrentResolverHTTP(t, nil) }
func TestUI188CB15CurrentResolverMariaDBHTTP(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("real MariaDB not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	db, err := database.OpenFromEnv(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	cb15CurrentResolverHTTP(t, db)
}

func cb15CurrentResolverHTTP(t *testing.T, db *sql.DB) {
	t.Helper()
	var services store.ServiceRegistryStore
	var auth store.AuthStore
	var audit store.AuditStore
	var streams store.StreamStore
	var profiles store.ProfileStore
	var secrets store.SecretStore
	var leases store.RuntimeSecretLeaseStore
	if db == nil {
		a := store.NewMemoryAuthStore()
		services, auth, audit = a, a, a
		streams, profiles, secrets, leases = store.NewMemoryStreamStore(), store.NewMemoryProfileStore(), store.NewMemorySecretStore(), store.NewMemoryRuntimeSecretLeaseStore()
	} else {
		a := store.NewMariaDBAuthStore(db)
		services, auth, audit = a, a, store.NewMariaDBAuditStore(db)
		streams, profiles, secrets, leases = store.NewMariaDBStreamStore(db), store.NewMariaDBProfileStore(db), store.NewMariaDBSecretStore(db, "cb15-http-test-only-key"), store.NewMariaDBRuntimeSecretLeaseStore(db)
	}
	id := "http-cb15-" + time.Now().UTC().Format("150405.000000000")
	token, err := services.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: id, ServiceType: "discord_bot", ServiceName: id, Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com", Capabilities: map[string]any{}}
	if _, err = services.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = services.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	status, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_"+id, "synthetic-cb15-http-current")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, id, map[string]any{"service_id": id, "bot_token_secret_name": status.Name, "bot_token_fingerprint": status.Fingerprint, "guild_id": "100000000000000001", "voice_channel_id": "100000000000000003"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := streams.CreateStream(t.Context(), "http current")
	if err != nil {
		t.Fatal(err)
	}
	other, err := streams.CreateStream(t.Context(), "http other waiting retained")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []store.Stream{current, other} {
		if _, err = streams.UpdateStreamSettings(t.Context(), s.ID, store.StreamSettings{DiscordConfigID: profile.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(services), WithAuditStore(audit), WithProfileStore(profiles), WithSecretStore(secrets), WithRuntimeSecretLeaseStore(leases))
	if _, err = services.AssignServiceToStreamGuarded(t.Context(), store.ServiceAssignmentMutation{ServiceID: id, StreamID: current.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			var e error
			raw, e = json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
		}
		r := httptest.NewRequest(method, "https://panel.example.com"+path, bytes.NewReader(raw))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	read := func() serviceRuntimeConfigResponse {
		t.Helper()
		w := request(http.MethodGet, "/services/runtime-config?service_id="+id, token.RawToken, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-cb15-http-current") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("current GET failed or exposed secret", w.Code)
		}
		var payload serviceRuntimeConfigResponse
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	before := read()
	if _, err = streams.UpdateStreamStatus(t.Context(), current.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	after := read()
	if len(after.StreamDiscordConfigs) != 1 || after.StreamDiscordConfigs[0].StreamID != other.ID || after.CurrentDiscordBindings == nil || len(after.CurrentDiscordBindings.Bindings) != 1 || !after.CurrentDiscordBindings.Bindings[0].Ready || !after.Assignments[0].AssignedAt.Equal(before.Assignments[0].AssignedAt) {
		t.Fatal("real HTTP starting counterexample not repaired")
	}
	binding := after.CurrentDiscordBindings.Bindings[0]
	body := map[string]any{"service_id": id, "secret_name": status.Name, "current_discord_binding_version": 1, "current_discord_binding": binding}
	for _, tc := range []struct {
		name   string
		body   map[string]any
		bearer string
		status int
		code   string
	}{
		{"unauthenticated", body, "", 401, ""},
		{"unknown bearer", body, "not-a-current-token", 401, ""},
		{"null binding", map[string]any{"service_id": id, "secret_name": status.Name, "current_discord_binding_version": 1, "current_discord_binding": nil}, token.RawToken, 400, "invalid_current_discord_binding"},
		{"unknown version", map[string]any{"service_id": id, "secret_name": status.Name, "current_discord_binding_version": 2, "current_discord_binding": binding}, token.RawToken, 400, "invalid_current_discord_binding"},
		{"lease scope injection", map[string]any{"service_id": id, "secret_name": status.Name, "stream_id": current.ID, "current_discord_binding_version": 1, "current_discord_binding": binding}, token.RawToken, 400, "invalid_current_discord_binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(http.MethodPost, "/services/runtime-secrets/resolve", tc.bearer, tc.body)
			if w.Code != tc.status || (tc.code != "" && !strings.Contains(w.Body.String(), tc.code)) || strings.Contains(w.Body.String(), "synthetic-cb15-http-current") {
				t.Fatal("negative did not reach intended rejection", w.Code)
			}
		})
	}
	stale := binding
	stale.AssignmentEpoch = stale.AssignmentEpoch.Add(-time.Second)
	body["current_discord_binding"] = stale
	w := request(http.MethodPost, "/services/runtime-secrets/resolve", token.RawToken, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "current_discord_binding_changed") {
		t.Fatal("stale epoch accepted", w.Code)
	}
	body["current_discord_binding"] = binding
	w = request(http.MethodPost, "/services/runtime-secrets/resolve", token.RawToken, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-cb15-http-current") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("cold current HTTP resolver failed", w.Code)
	}
	w = request(http.MethodPost, "/services/runtime-secrets/resolve", token.RawToken, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "runtime_secret_lease_active") || strings.Contains(w.Body.String(), "synthetic-cb15-http-current") {
		t.Fatal("actual current lease was bypassed", w.Code)
	}
	foreignKind, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key", "synthetic-not-a-discord-secret")
	if err != nil {
		t.Fatal(err)
	}
	profile.Config["bot_token_secret_name"], profile.Config["bot_token_fingerprint"] = foreignKind.Name, foreignKind.Fingerprint
	if _, err = profiles.UpdateProfile(t.Context(), store.ProfileDiscordConfig, profile.ID, profile.Name, profile.Config); err != nil {
		t.Fatal(err)
	}
	unavailable := read()
	if unavailable.CurrentDiscordBindings == nil || len(unavailable.CurrentDiscordBindings.Bindings) != 1 || unavailable.CurrentDiscordBindings.Bindings[0].Ready {
		t.Fatal("wrong secret kind became current authority")
	}
	body["secret_name"], body["current_discord_binding"] = foreignKind.Name, store.CurrentDiscordCredentialBinding{StreamID: current.ID, ServiceID: id, ServiceType: "discord_bot", AssignmentRole: "primary", AssignmentEpoch: binding.AssignmentEpoch, ProfileID: profile.ID, ProfileOwnerServiceID: id, SecretName: foreignKind.Name, Fingerprint: foreignKind.Fingerprint, Ready: true}
	w = request(http.MethodPost, "/services/runtime-secrets/resolve", token.RawToken, body)
	if w.Code != 403 || strings.Contains(w.Body.String(), "synthetic-not-a-discord-secret") {
		t.Fatal("wrong secret kind reached value resolver", w.Code)
	}
	body["secret_name"], body["current_discord_binding"] = status.Name, binding
	profile.Config["bot_token_secret_name"], profile.Config["bot_token_fingerprint"] = status.Name, status.Fingerprint
	profile.Config["service_id"] = "another-owner"
	if _, err = profiles.UpdateProfile(t.Context(), store.ProfileDiscordConfig, profile.ID, profile.Name, profile.Config); err != nil {
		t.Fatal(err)
	}
	w = request(http.MethodPost, "/services/runtime-secrets/resolve", token.RawToken, body)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "current_discord_binding_unavailable") {
		t.Fatal("owner change reached lease or secret instead of current denial", w.Code)
	}
	if err = services.RevokeServiceToken(t.Context(), token.ID); err != nil {
		t.Fatal(err)
	}
	w = request(http.MethodGet, "/services/runtime-config?service_id="+id, token.RawToken, nil)
	if w.Code != 401 {
		t.Fatal("revoked current bearer accepted", w.Code)
	}
	kept, err := streams.GetStream(t.Context(), other.ID)
	if err != nil || kept.Status != "created" || kept.DiscordConfigID != profile.ID {
		t.Fatal("other waiting was hidden or changed")
	}
}
