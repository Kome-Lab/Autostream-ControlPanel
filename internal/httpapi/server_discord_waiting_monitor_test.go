package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestDiscordWaitingMonitorSharedSelectionsHTTP(t *testing.T) {
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	profiles, secrets := store.NewMemoryProfileStore(), store.NewMemorySecretStore()
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth), WithRuntimeSecretLeaseStore(store.NewMemoryRuntimeSecretLeaseStore()))
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	serviceID := "waiting-monitor-bot"
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: serviceID, ServiceType: "discord_bot", ServiceName: serviceID,
		Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com",
		Capabilities: map[string]any{discordWaitingMonitorCapability: true},
	})
	secret, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_waiting_monitor", "synthetic-monitor-value")
	if err != nil {
		t.Fatal(err)
	}
	for i, voice := range []string{"100000000000000002", "100000000000000003"} {
		profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "waiting profile "+voice, map[string]any{
			"service_id": serviceID, "guild_id": "100000000000000001", "voice_channel_id": voice,
			"bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		stream, err := streams.CreateStream(t.Context(), "waiting "+voice)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
			DiscordConfigID: profile.ID, AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
			NodeSelections: &store.StreamNodeSelectionUpdate{DiscordServiceID: &serviceID},
		}); err != nil {
			t.Fatal(i, err)
		}
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, "https://panel.example.com"+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token.RawToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	read := request(http.MethodGet, "/services/runtime-config?service_id="+serviceID, nil)
	if read.Code != 200 || strings.Contains(read.Body.String(), "synthetic-monitor-value") {
		t.Fatalf("runtime read: %d %s", read.Code, read.Body.String())
	}
	var cfg serviceRuntimeConfigResponse
	if err := json.Unmarshal(read.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Assignments) != 0 || cfg.Service.CurrentStreamID != "" || cfg.CurrentDiscordBindings == nil || len(cfg.CurrentDiscordBindings.Bindings) != 0 {
		t.Fatal("waiting monitoring synthesized execution/current authority")
	}
	if cfg.DiscordWaitingMonitor == nil || cfg.DiscordWaitingMonitor.Version != 1 || len(cfg.DiscordWaitingMonitor.Bindings) != 2 {
		t.Fatal("shared waiting targets absent")
	}
	for _, binding := range cfg.DiscordWaitingMonitor.Bindings {
		if !binding.Ready || binding.TargetRevision == 0 || binding.VoiceChannelID == "" || binding.SelectionEpoch.IsZero() {
			t.Fatal("waiting target and its credential were not bound together")
		}
	}
	binding := cfg.DiscordWaitingMonitor.Bindings[0].DiscordWaitingCredentialBinding
	body := map[string]any{
		"service_id": serviceID, "secret_name": binding.SecretName,
		"discord_waiting_binding_version": 1, "discord_waiting_binding": binding,
	}
	body["discord_waiting_binding_version"] = 2
	if w := request(http.MethodPost, "/services/runtime-secrets/resolve", body); w.Code != 400 {
		t.Fatal("unsupported monitor secret version accepted", w.Code)
	}
	body["discord_waiting_binding_version"] = 1
	stale := binding
	stale.SelectionEpoch = stale.SelectionEpoch.Add(-time.Second)
	body["discord_waiting_binding"] = stale
	if w := request(http.MethodPost, "/services/runtime-secrets/resolve", body); w.Code != 409 || strings.Contains(w.Body.String(), "synthetic-monitor-value") {
		t.Fatal("stale selection epoch authorized secret", w.Code)
	}
	body["discord_waiting_binding"] = binding
	resolved := request(http.MethodPost, "/services/runtime-secrets/resolve", body)
	if resolved.Code != 200 || !strings.Contains(resolved.Body.String(), "synthetic-monitor-value") || resolved.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("fresh waiting credential did not resolve", resolved.Code)
	}
}

func TestDiscordWaitingMonitorPreservesActiveCredentialAndRejectsAmbiguousVC(t *testing.T) {
	credential := func(id, secret, voice string) serviceRuntimeDiscordWaitingBinding {
		return serviceRuntimeDiscordWaitingBinding{
			DiscordWaitingCredentialBinding: store.DiscordWaitingCredentialBinding{StreamID: id, SecretName: secret, Fingerprint: "fingerprint", Ready: true},
			GuildID:                         "100000000000000001", VoiceChannelID: voice,
		}
	}
	current := &serviceRuntimeCurrentDiscordBindings{Version: 1, Bindings: []store.CurrentDiscordCredentialBinding{{SecretName: "current", Fingerprint: "fingerprint", Ready: true}}}
	monitor := &serviceRuntimeDiscordWaitingMonitor{Version: 1, ExecutionActive: true, Bindings: []serviceRuntimeDiscordWaitingBinding{
		credential("same-credential", "current", "100000000000000002"),
		credential("different-credential", "other", "100000000000000003"),
	}}
	classifyDiscordWaitingMonitor(monitor, current)
	if !monitor.Bindings[0].Ready || monitor.Bindings[1].Ready || monitor.Bindings[1].ReadinessCode != "discord_waiting_credential_conflict" || monitor.AvailabilityCode != "" {
		t.Fatal("waiting B displaced current A credential authority")
	}
	monitor = &serviceRuntimeDiscordWaitingMonitor{Version: 1, Bindings: []serviceRuntimeDiscordWaitingBinding{
		credential("first", "same", "100000000000000002"),
		credential("second", "same", "100000000000000002"),
	}}
	classifyDiscordWaitingMonitor(monitor, nil)
	if monitor.Bindings[0].Ready || monitor.Bindings[1].Ready {
		t.Fatal("one ambiguous VC target was selected by list order")
	}
	monitor = &serviceRuntimeDiscordWaitingMonitor{Version: 1, Bindings: []serviceRuntimeDiscordWaitingBinding{
		credential("first", "one", "100000000000000002"),
		credential("second", "two", "100000000000000003"),
	}}
	classifyDiscordWaitingMonitor(monitor, nil)
	if monitor.AvailabilityCode != "discord_waiting_credentials_ambiguous" || monitor.Bindings[0].Ready || monitor.Bindings[1].Ready {
		t.Fatal("idle monitor silently selected incompatible credentials")
	}
}

func TestDiscordWaitingMonitorInactiveRetainedOwnerDoesNotPinCredential(t *testing.T) {
	current := &serviceRuntimeCurrentDiscordBindings{Version: 1, Bindings: []store.CurrentDiscordCredentialBinding{{
		StreamID: "retained-A", SecretName: "old", Fingerprint: "old-fingerprint", Ready: true,
	}}}
	monitor := &serviceRuntimeDiscordWaitingMonitor{Version: 1, ExecutionActive: false, Bindings: []serviceRuntimeDiscordWaitingBinding{{
		DiscordWaitingCredentialBinding: store.DiscordWaitingCredentialBinding{
			StreamID: "eligible-B", SecretName: "new", Fingerprint: "new-fingerprint", Ready: true,
		},
		GuildID: "100000000000000001", VoiceChannelID: "100000000000000003",
	}}}
	classifyDiscordWaitingMonitor(monitor, current)
	if !monitor.Bindings[0].Ready || monitor.AvailabilityCode != "" {
		t.Fatal("inactive retained execution assignment pinned the old Gateway credential")
	}
}
