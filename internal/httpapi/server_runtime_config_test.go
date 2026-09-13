package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
