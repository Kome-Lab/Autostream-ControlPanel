package httpapi

import (
	"bytes"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestYouTubeOutputConfigAcceptsRelayStaticWithoutIngestSecrets(t *testing.T) {
	completeOnStop := false
	config, err := youtubeOutputConfigFromRequest(youtubeOutputRequest{
		Name:                 "fixed relay",
		Mode:                 "live_api_relay_static",
		OAuthAccountID:       "youtube-oauth-account",
		RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
		ReusableLiveStreamID: "youtube-live-stream-primary",
		CompleteOnStop:       &completeOnStop,
	}, "youtube-output-static")
	if err != nil {
		t.Fatalf("relay static configuration error: %v", err)
	}
	if got := configString(config, "mode"); got != "live_api_relay_static" {
		t.Fatalf("mode=%q", got)
	}
	if got := configString(config, "relay_binding_id"); got != "relay-00000000-0000-4000-8000-000000000001" {
		t.Fatalf("relay_binding_id=%q", got)
	}
	if got := configString(config, "reusable_live_stream_id"); got != "youtube-live-stream-primary" {
		t.Fatalf("reusable_live_stream_id=%q", got)
	}
	if got := configString(config, "rtmp_url"); got != "" {
		t.Fatalf("relay static config leaked rtmp_url=%q", got)
	}
	if got := configString(config, "stream_key_secret_name"); got != "" {
		t.Fatalf("relay static config must not create a stream key secret=%q", got)
	}
	if !youtubeCompleteOnStop(config) {
		t.Fatal("relay static config must force complete_on_stop")
	}
}

func TestYouTubeOutputConfigScopesConfiguredStreamKeyReuseToLiveAPI(t *testing.T) {
	config, err := youtubeOutputConfigFromRequest(youtubeOutputRequest{
		Name:                   "managed custom key",
		Mode:                   "live_api",
		OAuthAccountID:         "youtube-oauth-account",
		UseConfiguredStreamKey: true,
	}, "youtube-output-custom")
	if err != nil {
		t.Fatal(err)
	}
	if !configBool(config, "use_configured_stream_key") {
		t.Fatalf("configured-key selection was not persisted: %#v", config)
	}
	for _, mode := range []string{"stream_key", "live_api_dry_run", "live_api_relay_static"} {
		request := youtubeOutputRequest{Name: "invalid configured key", Mode: mode, UseConfiguredStreamKey: true}
		if mode == "live_api_dry_run" {
			request.OAuthAccountID = "youtube-oauth-account"
		}
		if mode == "live_api_relay_static" {
			request.OAuthAccountID = "youtube-oauth-account"
			request.RelayBindingID = "relay-00000000-0000-4000-8000-000000000001"
			request.ReusableLiveStreamID = "youtube-live-stream-primary"
		}
		if _, err := youtubeOutputConfigFromRequest(request, "youtube-output-invalid"); err == nil {
			t.Fatalf("mode %q accepted configured-key reuse", mode)
		}
	}
}

func TestUpdateYouTubeOutputEnablesConfiguredStreamKey(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"youtube_outputs.update"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "test-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube Account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "test-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	output, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "Autostream_Develop", map[string]any{
		"mode":              "live_api",
		"oauth_account_id":  account.ID,
		"privacy_status":    "private",
		"enable_auto_start": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithProfileStore(profiles),
		WithIntegrationStore(integrations),
		WithSecretStore(secrets),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	const configuredKey = "test-configured-youtube-key"
	req := httptest.NewRequest(http.MethodPut, "/youtube/outputs/"+output.ID, bytes.NewBufferString(`{"name":"Autostream_Develop","mode":"live_api","rtmp_url":"rtmps://a.rtmps.youtube.com/live2","stream_key":"`+configuredKey+`","oauth_account_id":"`+account.ID+`","use_configured_stream_key":true,"privacy_status":"private","latency_preference":"low","enable_auto_start":true,"enable_auto_stop":true,"complete_on_stop":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("configured-key update status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), configuredKey) || strings.Contains(res.Body.String(), `"stream_key":"`) {
		t.Fatalf("configured key leaked in response: %s", res.Body.String())
	}
	updated, err := profiles.GetProfile(t.Context(), store.ProfileYouTubeOutput, output.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !configBool(updated.Config, "use_configured_stream_key") {
		t.Fatalf("configured-key selection was not persisted: %#v", updated.Config)
	}
	secretName := configString(updated.Config, "stream_key_secret_name")
	if secretName != youtubeOutputSecretName(output.ID) {
		t.Fatalf("stream key secret reference=%q", secretName)
	}
	storedKey, err := secrets.GetSecretValue(t.Context(), secretName)
	if err != nil {
		t.Fatal(err)
	}
	if storedKey != configuredKey {
		t.Fatal("configured key was not stored")
	}
}

func TestYouTubeOutputAutoStartDefaultsManagedLiveAPIOnly(t *testing.T) {
	disabled := false
	enabled := true
	for _, tc := range []struct {
		name          string
		request       youtubeOutputRequest
		want          bool
		wantPersisted bool
	}{
		{
			name:          "live api default",
			request:       youtubeOutputRequest{Name: "live api", Mode: "live_api", OAuthAccountID: "youtube-oauth-account"},
			want:          true,
			wantPersisted: true,
		},
		{
			name: "relay static default",
			request: youtubeOutputRequest{
				Name:                 "fixed relay",
				Mode:                 "live_api_relay_static",
				OAuthAccountID:       "youtube-oauth-account",
				RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
				ReusableLiveStreamID: "youtube-live-stream-primary",
			},
			want:          true,
			wantPersisted: true,
		},
		{
			name:          "live api explicit disable",
			request:       youtubeOutputRequest{Name: "live api disabled", Mode: "live_api", OAuthAccountID: "youtube-oauth-account", EnableAutoStart: &disabled},
			want:          false,
			wantPersisted: true,
		},
		{
			name: "relay static explicit disable",
			request: youtubeOutputRequest{
				Name:                 "fixed relay disabled",
				Mode:                 "live_api_relay_static",
				OAuthAccountID:       "youtube-oauth-account",
				RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
				ReusableLiveStreamID: "youtube-live-stream-primary",
				EnableAutoStart:      &disabled,
			},
			want:          false,
			wantPersisted: true,
		},
		{
			name:          "stream key remains unchanged when omitted",
			request:       youtubeOutputRequest{Name: "stream key", Mode: "stream_key"},
			want:          false,
			wantPersisted: false,
		},
		{
			name:          "stream key keeps explicit enable",
			request:       youtubeOutputRequest{Name: "stream key enabled", Mode: "stream_key", EnableAutoStart: &enabled},
			want:          true,
			wantPersisted: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := youtubeOutputConfigFromRequest(tc.request, "youtube-output")
			if err != nil {
				t.Fatal(err)
			}
			if got := youtubeOutputAutoStartEnabled(config); got != tc.want {
				t.Fatalf("auto-start=%t want=%t config=%#v", got, tc.want, config)
			}
			value, persisted := config["enable_auto_start"]
			if persisted != tc.wantPersisted {
				t.Fatalf("persisted=%t want=%t config=%#v", persisted, tc.wantPersisted, config)
			}
			if persisted && value != tc.want {
				t.Fatalf("persisted auto-start=%#v want=%t", value, tc.want)
			}
		})
	}
}

func TestYouTubeRelayStaticBindingIDRequiresCanonicalLowercaseUUID(t *testing.T) {
	valid := "relay-01234567-89ab-4cde-8f01-23456789abcd"
	if !validYouTubeRelayStaticBindingID(valid) {
		t.Fatalf("canonical relay binding id was rejected: %q", valid)
	}
	for _, value := range []string{
		"",
		"relay-static-primary",
		"yt-stream-key-like-value",
		" relay-01234567-89ab-4cde-8f01-23456789abcd ",
		"relay-01234567-89AB-4cde-8f01-23456789abcd",
		"01234567-89ab-4cde-8f01-23456789abcd",
		"relay-01234567-89ab-4cde-8f01-23456789abcde",
	} {
		if validYouTubeRelayStaticBindingID(value) {
			t.Fatalf("noncanonical relay binding id was accepted: %q", value)
		}
	}
}

func TestCreateYouTubeOutputRejectsNoncanonicalRelayBindingBeforePersistenceOrResponse(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"youtube_outputs.create"}); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	const rawBindingID = "yt-stream-key-like-value"
	req := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"unsafe-fixed-relay","mode":"live_api_relay_static","oauth_account_id":"unreachable-oauth-account","relay_binding_id":"`+rawBindingID+`","reusable_live_stream_id":"youtube-live-stream-primary"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_youtube_output") {
		t.Fatalf("noncanonical relay binding status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), rawBindingID) {
		t.Fatalf("noncanonical relay binding leaked in validation response: %s", res.Body.String())
	}
	stored, err := profiles.ListProfiles(t.Context(), store.ProfileYouTubeOutput)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("noncanonical relay binding was persisted: %#v", stored)
	}
}

func TestYouTubeOutputConfigRejectsRelayStaticIngestSettings(t *testing.T) {
	tests := []struct {
		name    string
		request youtubeOutputRequest
	}{
		{
			name: "rtmp url",
			request: youtubeOutputRequest{
				Name:                 "fixed relay",
				Mode:                 "live_api_relay_static",
				OAuthAccountID:       "youtube-oauth-account",
				RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
				ReusableLiveStreamID: "youtube-live-stream-primary",
				RTMPURL:              "rtmps://youtube.example.com/live2",
			},
		},
		{
			name: "stream key",
			request: youtubeOutputRequest{
				Name:                 "fixed relay",
				Mode:                 "live_api_relay_static",
				OAuthAccountID:       "youtube-oauth-account",
				RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
				ReusableLiveStreamID: "youtube-live-stream-primary",
				StreamKey:            "must-not-be-accepted",
			},
		},
		{
			name: "watch url",
			request: youtubeOutputRequest{
				Name:                 "fixed relay",
				Mode:                 "live_api_relay_static",
				OAuthAccountID:       "youtube-oauth-account",
				RelayBindingID:       "relay-00000000-0000-4000-8000-000000000001",
				ReusableLiveStreamID: "youtube-live-stream-primary",
				WatchURL:             "https://www.youtube.com/watch?v=video-static",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := youtubeOutputConfigFromRequest(tt.request, "youtube-output-static"); err == nil {
				t.Fatal("relay-static configuration unexpectedly accepted ingest data")
			}
		})
	}
}

func TestYouTubeLiveAPIOutputRejectsPlainRTMPFromClient(t *testing.T) {
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
	server := &Server{
		secrets:      store.NewMemorySecretStore(),
		integrations: integrations,
		youtubeLive:  &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmp://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-plain", LiveStreamID: "live-stream-plain"}},
	}
	req := &servicecall.StartRequest{}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-plain", Name: "plain rtmp"}, store.Profile{ID: "youtube-plain", Config: map[string]any{"oauth_account_id": account.ID}}, req)
	if !errors.Is(err, errYouTubeLiveAPIPrepareFailed) {
		t.Fatalf("expected plain RTMP to be rejected, got err=%v req=%#v", err, req)
	}
	if req.EncoderRTMPURL != "" || req.EncoderStreamKeySecretName != "" {
		t.Fatalf("plain RTMP client output must not populate dispatch request: %#v", req)
	}
}
