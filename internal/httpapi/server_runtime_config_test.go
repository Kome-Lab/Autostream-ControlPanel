package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

func TestYouTubeRuntimeConfigFromRelayStaticProfileExcludesIngestSettings(t *testing.T) {
	config := youtubeRuntimeConfigFromProfile(store.Profile{
		ID: "youtube-output-static",
		Config: map[string]any{
			"mode":                    "live_api_relay_static",
			"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000001",
			"reusable_live_stream_id": "youtube-live-stream-primary",
			"oauth_account_id":        "youtube-oauth-account",
			"rtmp_url":                "rtmps://must-not-reach-encoder.example/live2",
			"stream_key_secret_name":  "must-not-reach-encoder",
			"watch_url":               "https://www.youtube.com/watch?v=must-not-reach-encoder",
		},
	})
	if config["mode"] != "live_api_relay_static" || config["relay_binding_id"] != "relay-00000000-0000-4000-8000-000000000001" || config["reusable_live_stream_id"] != "youtube-live-stream-primary" {
		t.Fatalf("relay-static runtime config did not preserve the specialized binding: %#v", config)
	}
	for _, key := range []string{"rtmp_url", "stream_key", "stream_key_secret_name", "watch_url"} {
		if value, ok := config[key]; ok {
			t.Fatalf("relay-static runtime config leaked %s=%#v: %#v", key, value, config)
		}
	}
}

func TestRuntimeYouTubeRelayStaticConfigRequiresExactEncoderBinding(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "fixed relay runtime config")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "fixed relay runtime", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000001",
		"reusable_live_stream_id": "youtube-live-stream-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	server := &Server{streams: streams, profiles: profiles, integrations: integrations, youtubeLive: &fakeYouTubeLiveClient{}}
	assignment := store.StreamServiceAssignment{StreamID: stream.ID, ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", AssignmentRole: "primary"}
	service := store.RegisteredService{
		ServiceID:   assignment.ServiceID,
		ServiceType: assignment.ServiceType,
		Capabilities: map[string]any{
			"output_relay_mode":       "live_api_relay_static",
			"output_relay_binding_id": "relay-00000000-0000-4000-8000-000000000001",
		},
	}
	configs, err := server.runtimeYouTubeStreamConfigs(t.Context(), service, []store.StreamServiceAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || !configs[0].Ready || configs[0].YouTubeConfig["mode"] != "live_api_relay_static" || configs[0].YouTubeConfig["relay_binding_id"] != "relay-00000000-0000-4000-8000-000000000001" {
		t.Fatalf("matching static relay config was not safely forwarded: %#v", configs)
	}
	for _, key := range []string{"rtmp_url", "stream_key", "stream_key_secret_name", "watch_url"} {
		if value, ok := configs[0].YouTubeConfig[key]; ok {
			t.Fatalf("matching static relay config leaked %s=%#v: %#v", key, value, configs[0].YouTubeConfig)
		}
	}

	service.Capabilities["output_relay_binding_id"] = "relay-00000000-0000-4000-8000-000000000002"
	configs, err = server.runtimeYouTubeStreamConfigs(t.Context(), service, []store.StreamServiceAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Ready || configs[0].ReadinessCode != "youtube_relay_static_binding_unavailable" {
		t.Fatalf("mismatched static relay binding must be not-ready: %#v", configs)
	}
}

func TestRuntimeYouTubeDirectRelayKeepsStreamKeyReadyWithoutRawKey(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy fixed relay runtime")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy fixed relay output", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
		"stream_key_secret_name": "youtube_stream_key_legacy_runtime",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_legacy_runtime", "raw-legacy-stream-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	server := &Server{streams: streams, profiles: profiles, secrets: secrets}
	assignment := store.StreamServiceAssignment{StreamID: stream.ID, ServiceID: "encoder_recorder-legacy", ServiceType: "encoder_recorder", AssignmentRole: "primary"}
	service := store.RegisteredService{
		ServiceID:   assignment.ServiceID,
		ServiceType: assignment.ServiceType,
		Capabilities: map[string]any{
			"output_relay_mode": "direct",
		},
	}
	configs, err := server.runtimeYouTubeStreamConfigs(t.Context(), service, []store.StreamServiceAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || !configs[0].Ready || configs[0].YouTubeConfig["mode"] != "stream_key" || configs[0].YouTubeConfig["stream_key_secret_name"] != "youtube_stream_key_legacy_runtime" {
		t.Fatalf("direct output did not preserve the stream-key runtime route: %#v", configs)
	}
	if _, leaked := configs[0].YouTubeConfig["stream_key"]; leaked {
		t.Fatalf("direct output runtime config leaked the raw stream key: %#v", configs[0].YouTubeConfig)
	}
}

func TestRuntimeYouTubeLegacyOrUnknownRelayRejectsDynamicOutputBeforeOAuthReadiness(t *testing.T) {
	for _, tt := range []struct {
		name         string
		capabilities map[string]any
	}{
		{name: "legacy static alias", capabilities: map[string]any{"output_relay_mode": "static"}},
		{name: "legacy stream key", capabilities: map[string]any{"output_relay_mode": "legacy_stream_key"}},
		{name: "capability not reported", capabilities: map[string]any{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "dynamic runtime "+tt.name)
			if err != nil {
				t.Fatal(err)
			}
			profiles := store.NewMemoryProfileStore()
			youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "dynamic output", map[string]any{
				"mode":             "live_api",
				"oauth_account_id": "unreachable-oauth-account",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
				t.Fatal(err)
			}
			server := &Server{streams: streams, profiles: profiles, integrations: store.NewMemoryIntegrationStore()}
			assignment := store.StreamServiceAssignment{StreamID: stream.ID, ServiceID: "encoder_recorder-runtime", ServiceType: "encoder_recorder", AssignmentRole: "primary"}
			service := store.RegisteredService{ServiceID: assignment.ServiceID, ServiceType: assignment.ServiceType, Capabilities: tt.capabilities}
			configs, err := server.runtimeYouTubeStreamConfigs(t.Context(), service, []store.StreamServiceAssignment{assignment})
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 1 || configs[0].Ready || configs[0].ReadinessCode != "live_api_requires_managed_output_relay" {
				t.Fatalf("dynamic output must report relay capability before OAuth readiness: %#v", configs)
			}
		})
	}
}

func TestServiceRuntimeConfigIsScopedToAuthenticatedService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "runtime config stream")
	if err != nil {
		t.Fatal(err)
	}
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	limitedToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	discordOne, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"text_channel_id":       "text-1",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord two", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-2",
		"voice_channel_id": "voice-2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordOne.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "100000000000000001", "100000000000000002", "100000000000000003"), WithAuditStore(auth))

	limitedReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	limitedReq.Header.Set("Authorization", "Bearer "+limitedToken.RawToken)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden {
		t.Fatalf("limited runtime config status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-02", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime config status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime config response must not be cached, got %q", got)
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service.ServiceID != "discord-01" {
		t.Fatalf("wrong service in runtime config: %#v", body.Service)
	}
	if len(body.Assignments) != 1 || body.Assignments[0].StreamID != stream.ID || body.Assignments[0].AssignmentRole != "primary" {
		t.Fatalf("unexpected assignments: %#v", body.Assignments)
	}
	discordProfiles := body.Profiles[string(store.ProfileDiscordConfig)]
	if len(discordProfiles) != 1 || discordProfiles[0].Config["service_id"] != "discord-01" {
		t.Fatalf("runtime config leaked or omitted profiles: %#v", body.Profiles)
	}
	if _, ok := discordProfiles[0].Config["bot_token_secret_name"]; !ok {
		t.Fatalf("secret reference should remain visible: %#v", discordProfiles[0].Config)
	}
	if len(body.StreamDiscordConfigs) != 1 {
		t.Fatalf("expected one stream discord config, got %#v", body.StreamDiscordConfigs)
	}
	resolved := body.StreamDiscordConfigs[0]
	if resolved.StreamID != stream.ID || resolved.DiscordConfigID != discordOne.ID || resolved.AssignmentRole != "primary" {
		t.Fatalf("unexpected stream discord config identity: %#v", resolved)
	}
	if resolved.GuildID != "100000000000000001" || resolved.VoiceChannelID != "100000000000000003" {
		t.Fatalf("stream overrides were not applied: %#v", resolved)
	}
	if resolved.TextChannelID != "100000000000000002" {
		t.Fatalf("stream text channel override was not applied: %#v", resolved)
	}
	if resolved.AutoStartTrigger != "discord_voice_join" {
		t.Fatalf("stream auto-start trigger was not included: %#v", resolved)
	}
	if strings.Contains(res.Body.String(), "caption.example.com") {
		t.Fatalf("legacy arbitrary caption URL must not be exposed: %#v", resolved)
	}
	if strings.Contains(res.Body.String(), "guild-2") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord.com/api/webhooks") {
		t.Fatalf("runtime config response leaked another service or raw secret: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"token_id"`) || strings.Contains(res.Body.String(), tokenOne.ID) {
		t.Fatalf("runtime config response leaked service token binding: %s", res.Body.String())
	}
	sanitized := sanitizeRuntimeProfileConfig(map[string]any{"service_id": "discord-01", "nested": map[string]any{"webhook_url": "https://discord.com/api/webhooks/example/token"}})
	nested, ok := sanitized["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested config map: %#v", sanitized)
	}
	if _, ok := nested["webhook_url"]; ok {
		t.Fatalf("raw nested secret-like config should be removed: %#v", sanitized)
	}
}

func TestServiceRuntimeConfigIncludesConfiguredDiscordStreamsWithoutAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	streamA, err := streams.CreateStream(t.Context(), "runtime config waiting stream a")
	if err != nil {
		t.Fatal(err)
	}
	streamB, err := streams.CreateStream(t.Context(), "runtime config waiting stream b")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord assigned by settings", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-default",
		"voice_channel_id":      "voice-default",
		"text_channel_id":       "text-default",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "other discord", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-other",
		"voice_channel_id": "voice-other",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamA.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamB.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	visuals := streamvisual.NewMemoryRepository(streams)
	for _, target := range []struct {
		streamID, guildID, textChannelID, voiceChannelID string
	}{
		{streamA.ID, "100000000000000001", "100000000000000002", "100000000000000003"},
		{streamB.ID, "200000000000000001", "200000000000000002", "200000000000000003"},
	} {
		if _, err := visuals.Update(t.Context(), target.streamID, "test", streamvisual.Update{
			ExpectedRevision: 1,
			DiscordTarget: streamvisual.OptionalDiscordTarget{Set: true, Value: streamvisual.DiscordTarget{
				Mode: "manual", GuildID: target.guildID, TextChannelID: target.textChannelID, VoiceChannelID: target.voiceChannelID,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithStreamVisualRepository(visuals), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assignments) != 0 {
		t.Fatalf("manual assignments should not be required for waiting discord streams: %#v", body.Assignments)
	}
	if len(body.StreamDiscordConfigs) != 2 {
		t.Fatalf("expected two configured stream discord configs, got %#v", body.StreamDiscordConfigs)
	}
	byStream := map[string]serviceRuntimeDiscordStreamConfig{}
	for _, item := range body.StreamDiscordConfigs {
		byStream[item.StreamID] = item
		if item.AssignmentRole != "primary" {
			t.Fatalf("configured discord stream should be presented as primary runtime target: %#v", item)
		}
	}
	if byStream[streamA.ID].GuildID != "100000000000000001" || byStream[streamA.ID].VoiceChannelID != "100000000000000003" || byStream[streamA.ID].TextChannelID != "100000000000000002" {
		t.Fatalf("stream visual target was not applied for unassigned stream: %#v", byStream[streamA.ID])
	}
	if byStream[streamB.ID].GuildID != "200000000000000001" || byStream[streamB.ID].VoiceChannelID != "200000000000000003" || byStream[streamB.ID].TextChannelID != "200000000000000002" {
		t.Fatalf("stream overrides were not applied for unassigned stream: %#v", byStream[streamB.ID])
	}
	if strings.Contains(res.Body.String(), "caption.example.com") {
		t.Fatalf("legacy arbitrary caption URL must not be exposed: %#v", byStream[streamA.ID])
	}
	if strings.Contains(res.Body.String(), "guild-other") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord-bot-01") {
		t.Fatalf("runtime config leaked another service or raw secret: %s", res.Body.String())
	}
}

func TestServiceRuntimeConfigExcludesCompletedDiscordSourceAfterRearm(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	source, err := streams.CreateStream(t.Context(), "completed VC source")
	if err != nil {
		t.Fatal(err)
	}
	successor, err := streams.CreateStream(t.Context(), "waiting VC successor")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "rearm discord", map[string]any{
		"service_id":       "discord-01",
		"guild_id":         "guild-rearm",
		"voice_channel_id": "voice-rearm",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), source.ID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), successor.ID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), source.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", successor.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 1 {
		t.Fatalf("runtime config retained terminal or ambiguous discord configs: %#v", body.StreamDiscordConfigs)
	}
	if got := body.StreamDiscordConfigs[0]; got.StreamID != successor.ID || got.AssignmentRole != "primary" {
		t.Fatalf("runtime config did not retain only the waiting successor: %#v", got)
	}
	contender, err := streams.CreateStream(t.Context(), "ambiguous waiting contender")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), contender.ID, settings); err != nil {
		t.Fatal(err)
	}
	// An explicit primary successor wins over a separately saved, unassigned
	// candidate for the same VC. This prevents a Bot refresh from replacing the
	// authoritative successor with a map-order-dependent implicit candidate.
	secondReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	secondReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	secondRes := httptest.NewRecorder()
	handler.ServeHTTP(secondRes, secondReq)
	if secondRes.Code != http.StatusOK {
		t.Fatalf("runtime config with explicit successor status = %d body = %s", secondRes.Code, secondRes.Body.String())
	}
	body = serviceRuntimeConfigResponse{}
	if err := json.NewDecoder(secondRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 1 || body.StreamDiscordConfigs[0].StreamID != successor.ID {
		t.Fatalf("explicit successor did not win same-VC candidate selection: %#v", body.StreamDiscordConfigs)
	}
	if _, err := auth.UnassignServiceFromStream(t.Context(), "discord-01", "operator"); err != nil {
		t.Fatal(err)
	}
	if assignments, err := auth.ListServiceAssignmentsForService(t.Context(), "discord-01"); err != nil {
		t.Fatal(err)
	} else if len(assignments) != 0 {
		t.Fatalf("discord assignment still present after unassign: %#v", assignments)
	}
	thirdReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	thirdReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	thirdRes := httptest.NewRecorder()
	handler.ServeHTTP(thirdRes, thirdReq)
	if thirdRes.Code != http.StatusOK {
		t.Fatalf("runtime config with ambiguous candidates status = %d body = %s", thirdRes.Code, thirdRes.Body.String())
	}
	body = serviceRuntimeConfigResponse{}
	if err := json.NewDecoder(thirdRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 0 {
		successorCurrent, _ := streams.GetStream(t.Context(), successor.ID)
		contenderCurrent, _ := streams.GetStream(t.Context(), contender.ID)
		t.Fatalf("ambiguous unassigned same-VC candidates must not choose an implicit primary: source=%s successor=%#v contender=%#v configs=%#v", source.ID, successorCurrent, contenderCurrent, body.StreamDiscordConfigs)
	}
}

func TestServiceRuntimeConfigIncludesEncoderArchiveConfigWithoutRawSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "encoder runtime archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Runtime",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Runtime Account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Runtime Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "runtime archive", map[string]any{"drive_destination_id": destination.ID, "archive_file_name": "Council Meeting 20260708.mp4", "shared_drive_id": "shared-drive-01", "retention_days": 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-drive-folder-id", "raw-google-client-secret", "raw-google-refresh-token"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("runtime config leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamArchiveConfigs) != 1 {
		t.Fatalf("expected one stream archive config, got %#v", body.StreamArchiveConfigs)
	}
	cfg := body.StreamArchiveConfigs[0]
	if !cfg.Ready || cfg.StreamID != stream.ID || cfg.AssignmentRole != "primary" || cfg.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected stream archive config identity: %#v", cfg)
	}
	if cfg.ArchiveConfig["auth_mode"] != "oauth2" || cfg.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("runtime archive config omitted mode/shared drive: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["archive_file_name"] != "Council Meeting 20260708.mp4" || cfg.ArchiveConfig["shared_drive_id"] != "shared-drive-01" {
		t.Fatalf("runtime archive config omitted file/shared drive id settings: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["retention_days"] != float64(60) {
		t.Fatalf("runtime archive config omitted retention days: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("runtime archive config omitted scoped secret references: %#v", cfg.ArchiveConfig)
	}
	if _, leaked := cfg.ArchiveConfig["folder_id"]; leaked {
		t.Fatalf("runtime archive config exposed raw folder field: %#v", cfg.ArchiveConfig)
	}
}

func TestServiceRuntimeConfigIncludesSelectedUnscopedEncoderProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "encoder profile stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder 01",
		PublicURL:   "https://encoder.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	selected, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "selected encoder", map[string]any{
		"width": 1280, "height": 720, "fps": 30, "video_bitrate_kbps": 4500,
	})
	if err != nil {
		t.Fatal(err)
	}
	unselected, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "unselected encoder", map[string]any{
		"width": 1920, "height": 1080, "fps": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "other encoder", map[string]any{
		"service_id": "encoder-other-01", "width": 1920, "height": 1080, "fps": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{EncoderProfileID: selected.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	encoderProfiles := body.Profiles[string(store.ProfileEncoder)]
	if len(encoderProfiles) != 1 || encoderProfiles[0].ID != selected.ID {
		t.Fatalf("assigned encoder runtime config must contain only its selected unscoped profile: selected=%s unselected=%s restricted=%s profiles=%#v", selected.ID, unselected.ID, restricted.ID, encoderProfiles)
	}
	if encoderProfiles[0].Config["width"] != float64(1280) || encoderProfiles[0].Config["height"] != float64(720) || encoderProfiles[0].Config["fps"] != float64(30) {
		t.Fatalf("selected encoder profile config was not preserved: %#v", encoderProfiles[0].Config)
	}
}

func TestRuntimeUnscopedProfileMayFollowAssignedMediaServiceRejectsExplicitBindings(t *testing.T) {
	selected := map[string]struct{}{"profile-01": {}}
	tests := []struct {
		name    string
		profile store.Profile
		want    bool
	}{
		{name: "selected unscoped", profile: store.Profile{ID: "profile-01", Config: map[string]any{}}, want: true},
		{name: "not selected", profile: store.Profile{ID: "profile-02", Config: map[string]any{}}, want: false},
		{name: "bound to another service", profile: store.Profile{ID: "profile-01", Config: map[string]any{"service_id": "encoder-other-01"}}, want: false},
		{name: "bound to service list", profile: store.Profile{ID: "profile-01", Config: map[string]any{"service_ids": []any{"encoder-other-01"}}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeUnscopedProfileMayFollowAssignedMediaService(tt.profile, selected); got != tt.want {
				t.Fatalf("runtimeUnscopedProfileMayFollowAssignedMediaService() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestServiceRuntimeConfigIncludesStreamWatermarkForAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "encoder watermark stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-watermark-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder Watermark 01",
		PublicURL:   "https://encoder-watermark.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-watermark-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	overlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "Kome-Lab watermark", map[string]any{
		"watermark_enabled":        true,
		"watermark_image_data_url": "data:image/png;base64,AA==",
		"watermark_file_name":      "kome-lab.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlay.ID}); err != nil {
		t.Fatal(err)
	}
	otherStream, err := streams.CreateStream(t.Context(), "encoder watermark restricted stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-other-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder Other 01",
		PublicURL:   "https://encoder-other.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-other-01", otherStream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	restrictedOverlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "Other encoder watermark", map[string]any{
		"service_id":               "encoder-other-01",
		"watermark_image_data_url": "data:image/png;base64,BB==",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), otherStream.ID, store.StreamSettings{OverlayProfileID: restrictedOverlay.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-watermark-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	overlayProfiles := body.Profiles[string(store.ProfileOverlay)]
	if len(overlayProfiles) != 1 || overlayProfiles[0].ID != overlay.ID {
		t.Fatalf("assigned encoder runtime config omitted stream watermark profile: %#v", overlayProfiles)
	}
	if overlayProfiles[0].Config["watermark_image_data_url"] != "data:image/png;base64,AA==" {
		t.Fatalf("runtime watermark image was not preserved: %#v", overlayProfiles[0].Config)
	}
}

func TestServiceRuntimeConfigIncludesSelectedOverlayForAssignedWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "Worker scene stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "worker-scene-01", ServiceType: "worker", ServiceName: "Worker Scene 01",
		PublicURL: "https://worker-scene.example.com", Version: "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-scene-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	overlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "scene overlay", map[string]any{
		"watermark_enabled": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlay.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))
	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=worker-scene-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status=%d body=%s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	items := body.Profiles[string(store.ProfileOverlay)]
	if len(items) != 1 || items[0].ID != overlay.ID {
		t.Fatalf("assigned Worker runtime config omitted selected overlay: %#v", items)
	}
}

func TestServiceRuntimeConfigIncludesEncoderYouTubeConfigWithoutRawStreamKey(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "encoder runtime youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-youtube-01", ServiceType: "encoder_recorder", ServiceName: "Encoder YouTube 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{"output_relay_mode": "direct"}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-youtube-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "runtime youtube", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
		"stream_key_secret_name": "youtube_stream_key_runtime_config",
		"enable_auto_stop":       true,
		"complete_on_stop":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_runtime_config", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       youtube.ID,
		Mode:                "stream_key",
		RTMPURL:             "rtmps://a.rtmps.youtube.com/live2",
		StreamKeySecretName: "youtube_stream_key_runtime_config",
		CompleteOnStop:      true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-youtube-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-youtube-stream-key") || strings.Contains(res.Body.String(), `"stream_key":`) {
		t.Fatalf("runtime config leaked raw youtube stream key material: %s", res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamYouTubeConfigs) != 1 {
		t.Fatalf("expected one stream youtube config, got %#v", body.StreamYouTubeConfigs)
	}
	cfg := body.StreamYouTubeConfigs[0]
	if !cfg.Ready || cfg.StreamID != stream.ID || cfg.AssignmentRole != "primary" || cfg.YouTubeOutputID != youtube.ID {
		t.Fatalf("unexpected stream youtube config identity: %#v", cfg)
	}
	if cfg.YouTubeConfig["mode"] != "stream_key" || cfg.YouTubeConfig["stream_key_secret_name"] != "youtube_stream_key_runtime_config" {
		t.Fatalf("runtime youtube config omitted non-secret mode/secret reference: %#v", cfg.YouTubeConfig)
	}
	if cfg.YouTubeConfig["rtmp_url"] != "rtmps://a.rtmps.youtube.com/live2" || cfg.YouTubeConfig["complete_on_stop"] != true {
		t.Fatalf("runtime youtube config omitted dispatch fields: %#v", cfg.YouTubeConfig)
	}
	if cfg.ActiveRuntime["rtmp_url"] != "rtmps://a.rtmps.youtube.com/live2" || cfg.ActiveRuntime["stream_key_secret_name"] != "youtube_stream_key_runtime_config" || cfg.ActiveRuntime["complete_on_stop"] != true {
		t.Fatalf("runtime youtube config omitted active runtime safe fields: %#v", cfg.ActiveRuntime)
	}
}

func TestExternalE2EConfigExportsControlPanelConfirmation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e stream")
	if err != nil {
		t.Fatal(err)
	}
	driveProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Drive OAuth",
		Enabled:      true,
		ClientID:     "drive-client-id",
		ClientSecret: "raw-drive-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   driveProvider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Account",
		Email:        "drive@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-drive-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveDestination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive Upload",
		AuthMode:       "oauth2",
		OAuthAccountID: driveAccount.ID,
		FolderID:       "0ARealFolderIdShouldNotLeak",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	discordProfile := createDiscordConfigForTest(t, profiles, "external e2e discord", "discord-e2e-primary", "123456789012345678", "234567890123456789", "345678901234567890")
	youtubeOutput, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "private test output", map[string]any{
		"mode":             "live_api_dry_run",
		"oauth_account_id": driveAccount.ID,
		"rtmp_url":         "rtmps://a.rtmps.youtube.com/live2",
		"complete_on_stop": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "external e2e encoder", map[string]any{
		"input_url": "srt://encoder-input.example.com:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "external e2e archive", map[string]any{
		"drive_destination_id": driveDestination.ID,
		"final_container":      "mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		EncoderProfileID: encoderProfile.ID,
		ArchiveProfileID: archiveProfile.ID,
		YouTubeOutputID:  youtubeOutput.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, service := range []struct {
		id          string
		serviceType string
		role        string
	}{
		{id: "discord-e2e-primary", serviceType: "discord_bot", role: "primary"},
		{id: "encoder-e2e-primary", serviceType: "encoder_recorder", role: "primary"},
		{id: "worker-e2e-primary", serviceType: "worker", role: "primary"},
		{id: "encoder-e2e-standby", serviceType: "encoder_recorder", role: "standby"},
		{id: "worker-e2e-standby", serviceType: "worker", role: "standby"},
	} {
		registerServiceInstanceWithCapabilities(t, auth, service.id, service.serviceType, map[string]any{"runtime_config": true})
		if _, err := auth.AssignServiceToStreamWithRole(t.Context(), service.id, stream.ID, "admin", service.role); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("external e2e config response must not be cached, got %q", got)
	}
	responseBody := res.Body.String()
	var body externalE2EConfigResponse
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != 1 || body.StreamID != stream.ID {
		t.Fatalf("unexpected external e2e config identity: %#v", body)
	}
	if body.RuntimeConfig.YouTubeOutputID != youtubeOutput.ID ||
		body.RuntimeConfig.DriveDestinationID != driveDestination.ID ||
		body.RuntimeConfig.DiscordConfigID != discordProfile.ID ||
		body.RuntimeConfig.EncoderProfileID != encoderProfile.ID ||
		body.RuntimeConfig.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected runtime config ids: %#v", body.RuntimeConfig)
	}
	if body.ServiceAssignments.DiscordBotServiceID != "discord-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderPrimaryServiceID != "encoder-e2e-primary" ||
		body.ServiceAssignments.WorkerPrimaryServiceID != "worker-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderStandbyServiceID != "encoder-e2e-standby" ||
		body.ServiceAssignments.WorkerStandbyServiceID != "worker-e2e-standby" {
		t.Fatalf("unexpected service assignments: %#v", body.ServiceAssignments)
	}
	if !body.Confirmations.YouTubeOutputSaved ||
		!body.Confirmations.DriveDestinationSaved ||
		!body.Confirmations.DiscordConfigSaved ||
		!body.Confirmations.PrimaryAssignmentsSaved ||
		!body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("expected all confirmations true: %#v", body.Confirmations)
	}
	if !body.Readiness.Ready ||
		len(body.Readiness.MissingConfirmations) != 0 ||
		len(body.Readiness.MissingRuntimeIDs) != 0 ||
		len(body.Readiness.MissingPrimaryServices) != 0 ||
		len(body.Readiness.MissingRuntimeConfigCapabilities) != 0 {
		t.Fatalf("expected ready secret-safe readiness summary: %#v", body.Readiness)
	}
	for _, raw := range []string{
		"raw-drive-client-secret",
		"raw-drive-refresh-token",
		"0ARealFolderIdShouldNotLeak",
		"123456789012345678",
		"234567890123456789",
		"345678901234567890",
		"rtmps://a.rtmps.youtube.com/live2",
		"client_secret",
		"refresh_token",
		"folder_id",
	} {
		if strings.Contains(responseBody, raw) {
			t.Fatalf("external e2e config leaked secret or provider runtime value %q: %s", raw, responseBody)
		}
	}
}

func TestExternalE2EConfigRequiresStreamsRead(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e forbidden stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without streams.read, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestExternalE2EConfigReportsMissingControlPanelPieces(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e incomplete stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e incomplete config status = %d body = %s", res.Code, res.Body.String())
	}
	var body externalE2EConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.StreamID != stream.ID || body.SchemaVersion != 1 {
		t.Fatalf("unexpected incomplete config identity: %#v", body)
	}
	if body.RuntimeConfig != (externalE2ERuntimeConfig{}) || body.ServiceAssignments != (externalE2EServiceAssignments{}) {
		t.Fatalf("incomplete config should report empty ids: runtime=%#v assignments=%#v", body.RuntimeConfig, body.ServiceAssignments)
	}
	if body.Confirmations.YouTubeOutputSaved ||
		body.Confirmations.DriveDestinationSaved ||
		body.Confirmations.DiscordConfigSaved ||
		body.Confirmations.PrimaryAssignmentsSaved ||
		body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("incomplete config should report false confirmations: %#v", body.Confirmations)
	}
	if body.Readiness.Ready {
		t.Fatalf("incomplete config should not be ready: %#v", body.Readiness)
	}
	for _, expected := range []string{"youtube_output_saved", "drive_destination_saved", "discord_config_saved", "primary_assignments_saved", "runtime_config_distribution_enabled"} {
		if !slices.Contains(body.Readiness.MissingConfirmations, expected) {
			t.Fatalf("missing confirmation %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"youtube_output_id", "drive_destination_id", "discord_config_id", "encoder_profile_id", "archive_profile_id"} {
		if !slices.Contains(body.Readiness.MissingRuntimeIDs, expected) {
			t.Fatalf("missing runtime id %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"discord_bot", "worker", "encoder_recorder"} {
		if !slices.Contains(body.Readiness.MissingPrimaryServices, expected) {
			t.Fatalf("missing primary service %q not reported: %#v", expected, body.Readiness)
		}
	}
}

func TestServiceRuntimeSecretResolveIsScopedToRuntimeProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	configOnlyToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SecretName != "discord_bot_token_profile-01" || body.Value != "Bot <RAW_DISCORD_TOKEN>" || body.ExpiresInSec <= 0 {
		t.Fatalf("unexpected runtime secret response: %#v", body)
	}
	configOnlyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	configOnlyReq.Header.Set("Authorization", "Bearer "+configOnlyToken.RawToken)
	configOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(configOnlyRes, configOnlyReq)
	if configOnlyRes.Code != http.StatusForbidden || !strings.Contains(configOnlyRes.Body.String(), "missing_service_scope") || strings.Contains(configOnlyRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("config-only token must not resolve runtime secret, status = %d body = %s", configOnlyRes.Code, configOnlyRes.Body.String())
	}
	replayReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	replayReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusConflict || !strings.Contains(replayRes.Body.String(), "runtime_secret_lease_active") || strings.Contains(replayRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("active runtime secret lease replay status = %d body = %s", replayRes.Code, replayRes.Body.String())
	}

	crossReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	crossReq.Header.Set("Authorization", "Bearer "+tokenTwo.RawToken)
	crossRes := httptest.NewRecorder()
	handler.ServeHTTP(crossRes, crossReq)
	if crossRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime secret status = %d body = %s", crossRes.Code, crossRes.Body.String())
	}

	unreferencedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_unreferenced"}`))
	unreferencedReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	unreferencedRes := httptest.NewRecorder()
	handler.ServeHTTP(unreferencedRes, unreferencedReq)
	if unreferencedRes.Code != http.StatusForbidden {
		t.Fatalf("unreferenced runtime secret status = %d body = %s", unreferencedRes.Code, unreferencedRes.Body.String())
	}

	cfgReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	cfgReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	cfgRes := httptest.NewRecorder()
	handler.ServeHTTP(cfgRes, cfgReq)
	if cfgRes.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", cfgRes.Code, cfgRes.Body.String())
	}
	if strings.Contains(cfgRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime config leaked raw token: %s", cfgRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresSecureTransportInProduction(t *testing.T) {
	t.Setenv("AUTOSTREAM_ENV", "production")
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")

	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	requestBody := `{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`
	insecureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	insecureReq.RemoteAddr = "198.51.100.10:12345"
	insecureReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	insecureRes := httptest.NewRecorder()
	handler.ServeHTTP(insecureRes, insecureReq)
	if insecureRes.Code != http.StatusForbidden || !strings.Contains(insecureRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("production HTTP runtime secret resolve status = %d body = %s", insecureRes.Code, insecureRes.Body.String())
	}
	if insecureRes.Header().Get("Cache-Control") != "no-store" || strings.Contains(insecureRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("insecure runtime secret rejection must not be cached or leak raw value: headers=%v body=%s", insecureRes.Header(), insecureRes.Body.String())
	}

	spoofedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	spoofedReq.RemoteAddr = "198.51.100.10:12345"
	spoofedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	spoofedReq.Header.Set("X-Forwarded-Proto", "https")
	spoofedRes := httptest.NewRecorder()
	handler.ServeHTTP(spoofedRes, spoofedReq)
	if spoofedRes.Code != http.StatusForbidden || !strings.Contains(spoofedRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("untrusted forwarded proto runtime secret status = %d body = %s", spoofedRes.Code, spoofedRes.Body.String())
	}

	trustedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	trustedReq.RemoteAddr = "10.0.0.10:12345"
	trustedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	trustedReq.Header.Set("X-Forwarded-Proto", "https")
	trustedRes := httptest.NewRecorder()
	handler.ServeHTTP(trustedRes, trustedReq)
	if trustedRes.Code != http.StatusOK {
		t.Fatalf("trusted HTTPS forwarded runtime secret status = %d body = %s", trustedRes.Code, trustedRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(trustedRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected trusted forwarded runtime secret response: %#v", resolved)
	}
}

func TestServiceRuntimeSecretLeaseActiveIsCheckedBeforeSecretValue(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	secrets := &trackingSecretStore{}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithRuntimeSecretLeaseStore(activeRuntimeSecretLeaseStore{}), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "runtime_secret_lease_active") {
		t.Fatalf("active runtime secret lease status = %d body = %s", res.Code, res.Body.String())
	}
	if secrets.getCalls != 0 {
		t.Fatalf("runtime secret value was read before active lease rejection, calls=%d", secrets.getCalls)
	}
	if strings.Contains(res.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime secret replay leaked raw value: %s", res.Body.String())
	}
}

func TestServiceRuntimeSecretResolveReleasesLeaseWhenSecretIsMissing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	missingReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	missingReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound || !strings.Contains(missingRes.Body.String(), "runtime_secret_not_configured") {
		t.Fatalf("missing runtime secret status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}

	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	retryReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("runtime secret retry after configuration status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(retryRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected retry runtime secret response: %#v", body)
	}
}

func TestServiceRuntimeSecretResolveAllowsLiveAPIDryRunStreamSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run runtime secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "encoder-dry-run", ServiceType: "encoder_recorder", ServiceName: "Encoder Dry Run",
		PublicURL: "https://encoder-dry-run.example.com", Version: "0.1.0", Capabilities: map[string]any{},
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-dry-run", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "dry-run output", map[string]any{
		"mode": "live_api_dry_run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	secretName := "youtube_stream_key_runtime_dry_run"
	if _, err := secrets.UpdateSecret(t.Context(), secretName, "raw-dry-run-stream-key"); err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID: stream.ID, YouTubeOutput: youtube.ID, Mode: "live_api_dry_run", StreamKeySecretName: secretName,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))
	body := `{"service_id":"encoder-dry-run","stream_id":"` + stream.ID + `","secret_name":"` + secretName + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("dry-run runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.SecretName != secretName || resolved.Value != "raw-dry-run-stream-key" || resolved.ExpiresInSec <= 0 {
		t.Fatalf("unexpected dry-run runtime secret: %#v", resolved)
	}
}

func TestServiceRuntimeSecretResolveAllowsAssignedArchiveDestinationSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Standby", PublicURL: "https://encoder-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithSecretStore(secrets), WithAuditStore(auth))

	body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime archive secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-drive-folder-id" {
		t.Fatalf("unexpected resolved archive secret: %#v", resolved)
	}

	clientSecretBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthProviderClientSecretSecretName(provider.ID) + `"}`
	clientSecretReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(clientSecretBody))
	clientSecretReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	clientSecretRes := httptest.NewRecorder()
	handler.ServeHTTP(clientSecretRes, clientSecretReq)
	if clientSecretRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth client secret resolve status = %d body = %s", clientSecretRes.Code, clientSecretRes.Body.String())
	}
	var resolvedClientSecret serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(clientSecretRes.Body).Decode(&resolvedClientSecret); err != nil {
		t.Fatal(err)
	}
	if resolvedClientSecret.Value != "raw-google-client-secret" {
		t.Fatalf("unexpected resolved OAuth client secret: %#v", resolvedClientSecret)
	}

	refreshTokenBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthAccountRefreshTokenSecretName(account.ID) + `"}`
	refreshTokenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(refreshTokenBody))
	refreshTokenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	refreshTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(refreshTokenRes, refreshTokenReq)
	if refreshTokenRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth refresh token resolve status = %d body = %s", refreshTokenRes.Code, refreshTokenRes.Body.String())
	}
	var resolvedRefreshToken serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(refreshTokenRes.Body).Decode(&resolvedRefreshToken); err != nil {
		t.Fatal(err)
	}
	if resolvedRefreshToken.Value != "raw-google-refresh-token" {
		t.Fatalf("unexpected resolved OAuth refresh token: %s", formatSafeHTTPSensitiveDiagnostic(resolvedRefreshToken))
	}

	credentialsReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","stream_id":"`+stream.ID+`","archive_profile_id":"`+archiveProfile.ID+`","secret_name":"google_drive_credentials"}`))
	credentialsReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	credentialsRes := httptest.NewRecorder()
	handler.ServeHTTP(credentialsRes, credentialsReq)
	if credentialsRes.Code != http.StatusForbidden {
		t.Fatalf("service account credential secret must not resolve, status = %d body = %s", credentialsRes.Code, credentialsRes.Body.String())
	}

	standbyBody := `{"service_id":"encoder-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","secret_name":"`+driveDestinationFolderIDSecretName(destination.ID)+`"}`))
	forbiddenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("archive secret without stream/profile context status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresPrimaryAssignmentForGenericStreamSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "generic stream secret")
	if err != nil {
		t.Fatal(err)
	}
	primaryToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-generic-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Primary", PublicURL: "https://encoder-generic-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-generic-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Standby", PublicURL: "https://encoder-generic-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_generic", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "generic output", map[string]any{
		"service_ids":              []any{"encoder-generic-primary", "encoder-generic-standby"},
		"rtmp_url":                 "rtmps://youtube.example.com/live2",
		"stream_key_secret_name":   "youtube_stream_key_generic",
		"enable_auto_start":        true,
		"enable_auto_stop":         true,
		"broadcast_title_template": "{{stream_name}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-generic-primary","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder generic stream secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-youtube-stream-key" {
		t.Fatalf("unexpected generic stream secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-generic-standby","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve generic stream secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-generic-primary","secret_name":"youtube_stream_key_generic"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("generic stream secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveAllowsSelectedCaptionSecretForPrimaryWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "deepgram caption secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "worker-caption-primary", ServiceType: "worker", ServiceName: "Caption Worker",
		PublicURL: "https://worker-caption.example.com", Version: "0.1.0", Capabilities: map[string]any{},
	})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "worker-caption-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "deepgram_api_key", "raw-deepgram-api-key"); err != nil {
		t.Fatal(err)
	}
	caption, err := profiles.CreateProfile(t.Context(), store.ProfileCaption, "Deepgram Japanese", map[string]any{
		"provider": "deepgram", "model": "nova-3", "language": "ja", "api_key_secret_name": "deepgram_api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{CaptionProfileID: caption.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	body := `{"service_id":"worker-caption-primary","stream_id":"` + stream.ID + `","secret_name":"deepgram_api_key"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("caption runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-deepgram-api-key") {
		var resolved serviceRuntimeSecretResolveResponse
		if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
			t.Fatal(err)
		}
		if resolved.Value != "raw-deepgram-api-key" || resolved.SecretName != "deepgram_api_key" {
			t.Fatalf("unexpected caption runtime secret response: %#v", resolved)
		}
	} else {
		t.Fatalf("caption runtime secret was not returned to the selected primary worker: %s", res.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRejectsSecretFromWrongProfileKind(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "wrong profile kind secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-wrong-kind", ServiceType: "encoder_recorder", ServiceName: "Encoder Wrong Kind", PublicURL: "https://encoder-wrong-kind.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-wrong-kind", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_wrong_kind", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	overlayProfile, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "bad overlay secret", map[string]any{
		"service_ids":             []any{"encoder-wrong-kind"},
		"stream_key_secret_name":  "youtube_stream_key_wrong_kind",
		"overlay_template_source": "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlayProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	reqBody := `{"service_id":"encoder-wrong-kind","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_wrong_kind"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "runtime_secret_not_allowed") {
		t.Fatalf("wrong profile kind secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-youtube-stream-key") {
		t.Fatalf("wrong profile kind rejection leaked secret: %s", res.Body.String())
	}
}

func TestEncoderRuntimeProfileSecretRequiresSelectedProfileAndPrimaryAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "custom encoder secret")
	if err != nil {
		t.Fatal(err)
	}
	primaryToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-custom-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Primary", PublicURL: "https://encoder-custom-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-custom-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Standby", PublicURL: "https://encoder-custom-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "encoder_runtime_secret_custom", "raw-encoder-runtime-secret"); err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "custom encoder", map[string]any{
		"service_ids":                []any{"encoder-custom-primary", "encoder-custom-standby"},
		"video_bitrate":              "9000k",
		"custom_runtime_secret_name": "encoder_runtime_secret_custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{EncoderProfileID: encoderProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-custom-primary","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder custom profile secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-encoder-runtime-secret" {
		t.Fatalf("unexpected custom encoder secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-custom-standby","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve custom profile secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-custom-primary","secret_name":"encoder_runtime_secret_custom"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}

	unselectedStream, err := streams.CreateStream(t.Context(), "unselected custom encoder secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", unselectedStream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	unselectedBody := `{"service_id":"encoder-custom-primary","stream_id":"` + unselectedStream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	unselectedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(unselectedBody))
	unselectedReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	unselectedRes := httptest.NewRecorder()
	handler.ServeHTTP(unselectedRes, unselectedReq)
	if unselectedRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret for unselected profile must be forbidden, status = %d body = %s", unselectedRes.Code, unselectedRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveAllowsAssignedOAuthArchiveSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-oauth-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Primary", PublicURL: "https://encoder-oauth-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-oauth-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Standby", PublicURL: "https://encoder-oauth-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-oauth-primary", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-oauth-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Upload",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Upload Account",
		Subject:      "google-subject-01",
		Email:        "uploader@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-shared-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-main", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	cases := []struct {
		name       string
		secretName string
		want       string
	}{
		{name: "folder id", secretName: driveDestinationFolderIDSecretName(destination.ID), want: "raw-shared-drive-folder-id"},
		{name: "oauth provider client secret", secretName: oauthProviderClientSecretSecretName(provider.ID), want: "raw-google-client-secret"},
		{name: "oauth account refresh token", secretName: oauthAccountRefreshTokenSecretName(account.ID), want: "raw-google-refresh-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"service_id":"encoder-oauth-primary","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("runtime oauth archive secret resolve status = %d body = %s", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("runtime secret response must not be cached, got %q", got)
			}
			var resolved serviceRuntimeSecretResolveResponse
			if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
				t.Fatal(err)
			}
			if resolved.SecretName != tc.secretName || resolved.Value != tc.want || resolved.ExpiresInSec <= 0 {
				t.Fatalf("unexpected resolved oauth archive secret: %#v", resolved)
			}

			standbyBody := `{"service_id":"encoder-oauth-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
			standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
			standbyRes := httptest.NewRecorder()
			handler.ServeHTTP(standbyRes, standbyReq)
			if standbyRes.Code != http.StatusForbidden {
				t.Fatalf("standby encoder must not resolve oauth archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
			}

			wrongProfileReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-oauth-primary","stream_id":"`+stream.ID+`","archive_profile_id":"different-profile","secret_name":"`+tc.secretName+`"}`))
			wrongProfileReq.Header.Set("Authorization", "Bearer "+token.RawToken)
			wrongProfileRes := httptest.NewRecorder()
			handler.ServeHTTP(wrongProfileRes, wrongProfileReq)
			if wrongProfileRes.Code != http.StatusForbidden {
				t.Fatalf("oauth archive secret for a different archive profile must be forbidden, status = %d body = %s", wrongProfileRes.Code, wrongProfileRes.Body.String())
			}
		})
	}
}

func TestNodeRegistrationScopesAutomaticallyIncludeEncoderRuntimeSecrets(t *testing.T) {
	scopes := nodeRegistrationScopes("encoder_recorder", false, false)
	if !stringSliceContains(scopes, "service.secret.resolve") || !stringSliceContains(scopes, "encoder.status.write") || !stringSliceContains(scopes, "observability.ingest") {
		t.Fatalf("encoder scopes should include required runtime access automatically: %#v", scopes)
	}
}
