package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

func TestApplyYouTubeLiveAPIOutputRejectsEncoderCDNFormatMismatchBeforeProviderPrepare(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-4k", LiveStreamID: "live-stream-4k",
	}}
	server := &Server{
		integrations: integrations,
		secrets:      store.NewMemorySecretStore(),
		youtubeLive:  youtubeClient,
	}
	req := &servicecall.StartRequest{EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-01", Name: "1080p source"}, store.Profile{
		ID: "youtube-output-01",
		Config: map[string]any{
			"oauth_account_id": account.ID,
			"resolution":       "2160p",
			"frame_rate":       "60fps",
		},
	}, req)
	if !errors.Is(err, errYouTubeOutputVideoFormatMismatch) {
		t.Fatalf("1080p Encoder with 2160p YouTube CDN config error = %v, want %v", err, errYouTubeOutputVideoFormatMismatch)
	}
	if youtubeClient.prepareCalls != 0 {
		t.Fatalf("provider prepare ran before video-format validation: calls=%d request=%#v", youtubeClient.prepareCalls, youtubeClient.prepareRequest)
	}
}

func TestApplyYouTubeLiveAPIOutputBindsFreshStreamToValidatedEncoderFormat(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		width      int
		height     int
		fps        int
		resolution string
		frameRate  string
	}{
		{name: "1080p60", width: 1920, height: 1080, fps: 60, resolution: "1080p", frameRate: "60fps"},
		{name: "720p30", width: 1280, height: 720, fps: 30, resolution: "720p", frameRate: "30fps"},
		{name: "480p60", width: 854, height: 480, fps: 60, resolution: "480p", frameRate: "60fps"},
	} {
		t.Run(test.name, func(t *testing.T) {
			youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
				RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-" + test.name, LiveStreamID: "live-stream-" + test.name,
			}}
			server := &Server{
				integrations: integrations,
				secrets:      store.NewMemorySecretStore(),
				youtubeLive:  youtubeClient,
			}
			req := &servicecall.StartRequest{EncoderVideoWidth: test.width, EncoderVideoHeight: test.height, EncoderVideoFPS: test.fps}
			err := server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-" + test.name, Name: test.name + " source"}, store.Profile{
				ID:     "youtube-output-01",
				Config: map[string]any{"oauth_account_id": account.ID},
			}, req)
			if err != nil {
				t.Fatal(err)
			}
			if youtubeClient.prepareCalls != 1 {
				t.Fatalf("provider prepare calls = %d, want 1", youtubeClient.prepareCalls)
			}
			if got := youtubeClient.prepareRequest.Resolution; got != test.resolution {
				t.Fatalf("provider CDN resolution = %q, want %s", got, test.resolution)
			}
			if got := youtubeClient.prepareRequest.FrameRate; got != test.frameRate {
				t.Fatalf("provider CDN frame rate = %q, want %s", got, test.frameRate)
			}
			if youtubeClient.prepareRequest.ReuseAccountStream {
				t.Fatal("stream-scoped provider ingest unexpectedly enabled account-wide reuse")
			}
		})
	}
}

func TestApplyYouTubeLiveAPIOutputUsesConfiguredReusableStreamKey(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client-id", ClientSecret: "youtube-client-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	const secretName = "youtube_stream_key_configured_reusable"
	const configuredKey = "operator-custom-key"
	if _, err := secrets.UpdateSecret(t.Context(), secretName, configuredKey); err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: configuredKey, BroadcastID: "broadcast-custom", LiveStreamID: "live-stream-custom",
	}}
	server := &Server{integrations: integrations, secrets: secrets, youtubeLive: youtubeClient}
	req := &servicecall.StartRequest{EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60}
	profile := store.Profile{ID: "youtube-output-custom", Config: map[string]any{
		"oauth_account_id":          account.ID,
		"stream_key_secret_name":    secretName,
		"use_configured_stream_key": true,
	}}
	if err := server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-custom", Name: "custom key source"}, profile, req); err != nil {
		t.Fatal(err)
	}
	if youtubeClient.prepareRequest.PreferredStreamKey != configuredKey {
		t.Fatal("configured reusable stream key was not passed to the provider client")
	}
	if youtubeClient.prepareRequest.ReuseAccountStream {
		t.Fatal("configured key selection must not use heuristic account-stream reuse")
	}
	if got := mapString(req.YouTubeRuntime, "ingest_selection"); got != "configured_reusable_stream" {
		t.Fatalf("ingest selection=%q runtime=%#v", got, req.YouTubeRuntime)
	}
	if strings.Contains(fmt.Sprintf("%#v", req.YouTubeRuntime), configuredKey) {
		t.Fatalf("configured stream key leaked into runtime metadata: %#v", req.YouTubeRuntime)
	}
}

func TestApplyYouTubeLiveAPIOutputResolvesEncoderProfileWithoutVideoCapabilities(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
		RedirectURI:  "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh-token", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "legacy-capability-720p30", map[string]any{
		"width": 1280, "height": 720, "fps": 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
		RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "runtime-key", BroadcastID: "broadcast-720p", LiveStreamID: "live-stream-720p",
	}}
	server := &Server{
		profiles:     profiles,
		integrations: integrations,
		secrets:      store.NewMemorySecretStore(),
		youtubeLive:  youtubeClient,
	}
	req := &servicecall.StartRequest{EncoderProfileID: encoderProfile.ID}
	err = server.applyYouTubeLiveAPIOutput(t.Context(), store.Stream{ID: "stream-legacy", Name: "legacy capability"}, store.Profile{
		ID:     "youtube-output-01",
		Config: map[string]any{"oauth_account_id": account.ID},
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if youtubeClient.prepareRequest.Resolution != "720p" || youtubeClient.prepareRequest.FrameRate != "30fps" {
		t.Fatalf("provider format did not resolve the selected Encoder profile: %#v", youtubeClient.prepareRequest)
	}
}

func TestServiceStartStreamUsesDiscordProfileChannelDefaultsForYouTubeNotification(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord profile defaults service start")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}

	profiles := store.NewMemoryProfileStore()
	discord, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "profile defaults discord", map[string]any{
		"service_id":           "discord-01",
		"bot_token_configured": true,
		"guild_id":             "guild-profile",
		"voice_channel_id":     "voice-profile",
		"text_channel_id":      "chat-profile",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "profile defaults output", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://youtube.example.com/live2",
		"stream_key_secret_name": "youtube_stream_key_profile_defaults",
		"watch_url":              "https://youtu.be/profile_defaults",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_profile_defaults", "runtime-secret-stream-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discord.ID,
		AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
		YouTubeOutputID:  youtube.ID,
	}); err != nil {
		t.Fatal(err)
	}

	dispatcher := &notificationFakeDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service start status=%d body=%s", res.Code, res.Body.String())
	}
	if dispatcher.startRequest.DiscordGuildID != "guild-profile" || dispatcher.startRequest.DiscordVoiceChannelID != "voice-profile" || dispatcher.startRequest.DiscordTextChannelID != "chat-profile" {
		t.Fatalf("service start did not apply Discord profile channel defaults: %#v", dispatcher.startRequest)
	}
	if dispatcher.notifyCalls != 0 {
		t.Fatalf("service start must only enqueue the configured text notification: calls=%d", dispatcher.notifyCalls)
	}
	queued, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.DiscordTextChannelID != "chat-profile" || queued.WatchURL != "https://www.youtube.com/watch?v=profile_defaults" || queued.State != store.DiscordYouTubeLiveNotificationStateDispatchPending {
		t.Fatalf("service start did not enqueue the configured text notification: %#v", queued)
	}
	result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 1)
	if err != nil || result["claimed"] != 1 || result["delivered"] != 1 {
		t.Fatalf("queued profile-default notification was not delivered: result=%#v err=%v", result, err)
	}
	if dispatcher.notifyCalls != 1 || dispatcher.notifiedURL != "https://www.youtube.com/watch?v=profile_defaults" {
		t.Fatalf("outbox did not notify the configured text channel: calls=%d url=%q", dispatcher.notifyCalls, dispatcher.notifiedURL)
	}
}

func TestStartStreamDirectRelayResolvesYouTubeOutputSecretForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	// Older Encoder releases report the ambiguous "static" capability whenever
	// a local fixed relay URL is configured. That must stay on the established
	// stream_key route until an operator explicitly moves it to live_api_static.
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{"output_relay_mode": "direct"})
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_main", "runtime-secret-stream-key"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "main-output", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://youtube.example.com/live2",
		"stream_key_secret_name": "youtube_stream_key_main",
		"watch_url":              "https://youtu.be/bundle8b-stream-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "runtime-secret-stream-key") {
		t.Fatalf("stream key leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" || dispatcher.startRequest.EncoderStreamKeySecretName != "youtube_stream_key_main" {
		t.Fatalf("youtube output was not dispatched as a runtime secret reference: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamRejectsYouTubeOutputWithoutProfileRTMPURL(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_legacy", "runtime-secret-stream-key"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "legacy youtube discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy-output", map[string]any{
		"mode":                   "stream_key",
		"stream_key_secret_name": "youtube_stream_key_legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`","encoder_rtmp_url":"rtmps://attacker.example.com/live2"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "youtube_output_invalid_config") {
		t.Fatalf("expected youtube_output_invalid_config: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called for youtube output without profile RTMP URL: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamPreparesYouTubeLiveAPIDryRunWithoutSecretLeak(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube dry-run discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "auto-output", map[string]any{
		"mode":     "live_api_dry_run",
		"rtmp_url": "rtmps://youtube.example.com/live2",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "yt-dry-run-") || strings.Contains(res.Body.String(), "dry-broadcast-") {
		t.Fatalf("youtube runtime secret/state leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" ||
		!strings.HasPrefix(dispatcher.startRequest.EncoderStreamKeySecretName, "youtube_stream_key_runtime_") {
		t.Fatalf("youtube live api dry-run was not prepared as a runtime secret reference: %#v", dispatcher.startRequest)
	}
	runtime := dispatcher.startRequest.YouTubeRuntime
	if runtime["mode"] != "live_api_dry_run" || runtime["output_id"] != youtube.ID || runtime["dry_run"] != true || runtime["complete_on_stop"] != true || runtime["rtmp_url"] != "rtmps://youtube.example.com/live2" || !strings.HasPrefix(stringValue(runtime["broadcast_id"]), "dry-broadcast-") || !strings.HasPrefix(stringValue(runtime["live_stream_id"]), "dry-live-stream-") || runtime["stream_key_secret_name"] != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("youtube runtime metadata was not prepared: %#v", runtime)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime was not stored: %v", err)
	}
	if stored.Mode != "live_api_dry_run" || stored.YouTubeOutput != youtube.ID || stored.RTMPURL != "rtmps://youtube.example.com/live2" || !stored.DryRun || !stored.CompleteOnStop || stored.StreamKeySecretName != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("unexpected stored youtube runtime: %#v", stored)
	}
	dryRunKey, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName)
	if err != nil || !strings.HasPrefix(dryRunKey, "yt-dry-run-") {
		t.Fatalf("dry-run stream key should be stored as a short-lived secret: value=%q err=%v", dryRunKey, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after stop, got err=%v", err)
	}
}

func TestStartStreamPreparesAndCompletesYouTubeLiveAPIWithOAuthAccount(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop", "audit_logs.read", "audit_logs.export"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	// This fixture exercises the immediate-start transition. Future schedules
	// intentionally remain under YouTube's scheduler and are covered by the
	// stream settings tests above.
	scheduledStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ScheduledStartAt: &scheduledStart})
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	secrets := store.NewMemorySecretStore()
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
	discord := createDiscordConfigForTest(t, profiles, "youtube live api discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":                     "live_api",
		"oauth_account_id":         account.ID,
		"privacy_status":           "private",
		"enable_auto_start":        false,
		"broadcast_title_template": "配信: {{program_title}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &transitioningYouTubeLiveClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-01", LiveStreamID: "live-stream-01"}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "runtime-youtube-live-api-key") || strings.Contains(res.Body.String(), "raw-youtube-refresh-token") || strings.Contains(res.Body.String(), "raw-youtube-client-secret") {
		t.Fatalf("youtube live api secret leaked in response: %s", res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.prepareRequest.Credentials.ClientID != "youtube-client-id" || youtubeLive.prepareRequest.Credentials.ClientSecret != "raw-youtube-client-secret" || youtubeLive.prepareRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" || youtubeLive.prepareRequest.EnableAutoStart {
		t.Fatalf("youtube live api was not called with OAuth credentials: %#v", youtubeLive.prepareRequest)
	}
	if youtubeLive.prepareRequest.ReuseAccountStream {
		t.Fatalf("youtube live api must create a stream-scoped ingest instead of reusing account-wide format state: %#v", youtubeLive.prepareRequest)
	}
	if youtubeLive.prepareRequest.Resolution != "1080p" || youtubeLive.prepareRequest.FrameRate != "60fps" {
		t.Fatalf("youtube live api must bind the fresh ingest to the validated Encoder format: %#v", youtubeLive.prepareRequest)
	}
	if youtubeLive.prepareRequest.Title != "配信: real youtube stream" {
		t.Fatalf("youtube live api title was not expanded from the stream name: %#v", youtubeLive.prepareRequest)
	}
	if youtubeLive.transitionCalls != 1 || youtubeLive.transitionRequest.BroadcastID != "broadcast-01" || youtubeLive.transitionRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("youtube live api broadcast was not explicitly transitioned to live: %#v", youtubeLive)
	}
	if !youtubeLive.prepareRequest.ScheduledStart.Equal(scheduledStart) {
		t.Fatalf("youtube live api did not receive the stream scheduled start: got=%s want=%s", youtubeLive.prepareRequest.ScheduledStart, scheduledStart)
	}
	if dispatcher.startRequest.EncoderRTMPURL != "rtmps://youtube.example.com/live2" || !strings.HasPrefix(dispatcher.startRequest.EncoderStreamKeySecretName, "youtube_stream_key_runtime_") {
		t.Fatalf("youtube live api output was not dispatched as a runtime secret reference: %#v", dispatcher.startRequest)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stored.Mode != "live_api" || stored.OAuthAccountID != account.ID || stored.BroadcastID != "broadcast-01" || stored.RTMPURL != "rtmps://youtube.example.com/live2" || !stored.CompleteOnStop {
		t.Fatalf("youtube live api runtime was not stored: %#v err=%v", stored, err)
	}
	if stored.StreamKeySecretName != dispatcher.startRequest.EncoderStreamKeySecretName {
		t.Fatalf("youtube live api runtime secret name mismatch: stored=%q dispatched=%q", stored.StreamKeySecretName, dispatcher.startRequest.EncoderStreamKeySecretName)
	}
	resolvedStreamKey, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName)
	if err != nil || resolvedStreamKey != "runtime-youtube-live-api-key" {
		t.Fatalf("youtube live api runtime stream key was not stored as a short-lived secret: value=%q err=%v", resolvedStreamKey, err)
	}
	auditListReq := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	auditListReq.AddCookie(cookie)
	auditListRes := httptest.NewRecorder()
	handler.ServeHTTP(auditListRes, auditListReq)
	if auditListRes.Code != http.StatusOK {
		t.Fatalf("audit list status = %d body = %s", auditListRes.Code, auditListRes.Body.String())
	}
	auditExportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export", nil)
	auditExportReq.AddCookie(cookie)
	auditExportRes := httptest.NewRecorder()
	handler.ServeHTTP(auditExportRes, auditExportReq)
	if auditExportRes.Code != http.StatusOK {
		t.Fatalf("audit export status = %d body = %s", auditExportRes.Code, auditExportRes.Body.String())
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(auditListRes.Body.String(), raw) || strings.Contains(auditExportRes.Body.String(), raw) {
			t.Fatalf("youtube live api audit leaked secret %q list=%s export=%s", raw, auditListRes.Body.String(), auditExportRes.Body.String())
		}
	}
	encoderService, err := auth.GetService(t.Context(), "encoder_recorder-01")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := (&Server{streams: streams, services: auth, profiles: profiles}).runtimeYouTubeStreamSecretAllowed(t.Context(), encoderService, stored.StreamKeySecretName, stream.ID)
	if err != nil || !allowed {
		t.Fatalf("primary encoder should be allowed to resolve live api runtime stream key: allowed=%v err=%v", allowed, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-01" || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("youtube live api complete was not called correctly: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after live api stop, got err=%v", err)
	}
	if _, err := secrets.GetSecretValue(t.Context(), stored.StreamKeySecretName); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube live api runtime stream key secret should be cleared after stop, got err=%v", err)
	}
}

func TestEnsureYouTubeBroadcastLiveLeavesFutureScheduleToYouTube(t *testing.T) {
	youtubeLive := &transitioningYouTubeLiveClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{}}
	server := &Server{youtubeLive: youtubeLive}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if err := server.ensureYouTubeBroadcastLive(t.Context(), map[string]any{
		"mode":               "live_api",
		"oauth_account_id":   "oauth-account-01",
		"broadcast_id":       "broadcast-01",
		"scheduled_start_at": future,
	}); err != nil {
		t.Fatalf("future scheduled broadcast should not be transitioned immediately: %v", err)
	}
	if youtubeLive.transitionCalls != 0 {
		t.Fatalf("future scheduled broadcast was transitioned immediately: %#v", youtubeLive)
	}
}

func TestEnsureYouTubeBroadcastLiveDefersWhenAutoStartEnabled(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "youtube-client-secret",
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
		RefreshToken: "youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &transitioningYouTubeLiveClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{}}
	server := &Server{integrations: integrations, youtubeLive: youtubeLive}

	if err := server.ensureYouTubeBroadcastLive(t.Context(), map[string]any{
		"mode":              "live_api",
		"oauth_account_id":  account.ID,
		"broadcast_id":      "broadcast-01",
		"enable_auto_start": true,
	}); err != nil {
		t.Fatalf("auto-start should defer the provider transition: %v", err)
	}
	if youtubeLive.transitionCalls != 0 {
		t.Fatalf("auto-start path attempted an explicit provider transition: %#v", youtubeLive)
	}
}

func TestStartStreamLegacyStaticRelayRejectsNonStreamKeyYouTubeOutputBeforePrepareOrDispatch(t *testing.T) {
	for _, tt := range []struct {
		name         string
		mode         string
		code         string
		capabilities map[string]any
	}{
		{name: "legacy static live api", mode: "live_api", code: "live_api_requires_managed_output_relay", capabilities: map[string]any{"output_relay_mode": "static"}},
		{name: "legacy static live api dry run", mode: "live_api_dry_run", code: "live_api_requires_managed_output_relay", capabilities: map[string]any{"output_relay_mode": "static"}},
		{name: "legacy static missing output", code: "youtube_output_invalid_config", capabilities: map[string]any{"output_relay_mode": "static"}},
		{name: "capability not reported live api", mode: "live_api", code: "live_api_requires_managed_output_relay", capabilities: map[string]any{}},
		{name: "capability not reported live api dry run", mode: "live_api_dry_run", code: "live_api_requires_managed_output_relay", capabilities: map[string]any{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "legacy static relay "+tt.name)
			if err != nil {
				t.Fatal(err)
			}
			registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", tt.capabilities)
			registerServiceInstance(t, auth, "worker-01", "worker")
			registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
			for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
				if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
					t.Fatal(err)
				}
			}

			profiles := store.NewMemoryProfileStore()
			discord := createDiscordConfigForTest(t, profiles, "legacy static relay discord", "discord_bot-01", "guild-static", "voice-static", "")
			outputID := ""
			if tt.mode != "" {
				youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy static relay output", map[string]any{"mode": tt.mode})
				if err != nil {
					t.Fatal(err)
				}
				outputID = youtube.ID
			}
			dispatcher := &fakeServiceDispatcher{}
			youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-static", LiveStreamID: "live-stream-static"}}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			body := `{"discord_config_id":"` + discord.ID + `"}`
			if outputID != "" {
				body = `{"discord_config_id":"` + discord.ID + `","youtube_output_id":"` + outputID + `"}`
			}
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), tt.code) {
				t.Fatalf("expected managed relay rejection %q, status=%d body=%s", tt.code, res.Code, res.Body.String())
			}
			if youtubeLive.prepareCalls != 0 || youtubeLive.relayStaticPrepareCalls != 0 {
				t.Fatalf("YouTube prepare must not run for rejected legacy static output: %#v", youtubeLive)
			}
			if dispatcher.startCalls != 0 {
				t.Fatalf("dispatcher must not run for rejected legacy static output: %#v", dispatcher.startRequest)
			}
			unchanged, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			if unchanged.Status != "created" {
				t.Fatalf("legacy static rejection must not transition the stream, got=%q", unchanged.Status)
			}
		})
	}
}

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

func TestStartStreamPreparesRelayStaticYouTubeAndReleasesClaimOnlyAfterCompletion(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "fixed relay stream")
	if err != nil {
		t.Fatal(err)
	}
	const relayBindingID = "relay-00000000-0000-4000-8000-000000000001"
	const reusableLiveStreamID = "youtube-live-stream-primary"
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{
		"output_relay_mode":       "live_api_relay_static",
		"output_relay_binding_id": relayBindingID,
	})
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
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
	discord := createDiscordConfigForTest(t, profiles, "fixed relay discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        relayBindingID,
		"reusable_live_stream_id": reusableLiveStreamID,
		"complete_on_stop":        false,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{
		BroadcastID:  "broadcast-static-primary",
		LiveStreamID: reusableLiveStreamID,
	}}
	handler := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithSecretStore(store.NewMemorySecretStore()),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(&relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`","encoder_rtmp_url":"rtmps://attacker.example.com/live2"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", startRes.Code, startRes.Body.String())
	}
	for _, secret := range []string{"raw-youtube-client-secret", "raw-youtube-refresh-token"} {
		if strings.Contains(startRes.Body.String(), secret) {
			t.Fatalf("start response leaked OAuth secret %q: %s", secret, startRes.Body.String())
		}
	}
	if youtubeLive.prepareCalls != 0 || youtubeLive.relayStaticPrepareCalls != 1 {
		t.Fatalf("unexpected prepare calls: live_api=%d relay_static=%d", youtubeLive.prepareCalls, youtubeLive.relayStaticPrepareCalls)
	}
	if youtubeLive.relayStaticRequest.ReusableLiveStreamID != reusableLiveStreamID ||
		youtubeLive.relayStaticRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" ||
		!youtubeLive.relayStaticRequest.EnableAutoStart {
		t.Fatalf("relay static prepare request did not use the bound reusable stream and OAuth account: %#v", youtubeLive.relayStaticRequest)
	}
	if youtubeLive.relayStaticRequest.Resolution != "1080p" || youtubeLive.relayStaticRequest.FrameRate != "60fps" {
		t.Fatalf("relay static prepare request did not use the resolved Encoder format: %#v", youtubeLive.relayStaticRequest)
	}
	if dispatcher.startCalls != 1 || dispatcher.startRequest.EncoderRTMPURL != "" || dispatcher.startRequest.EncoderStreamKeySecretName != "" {
		t.Fatalf("relay static dispatch leaked an ingest endpoint or key: calls=%d request=%#v", dispatcher.startCalls, dispatcher.startRequest)
	}
	runtimeConfig := dispatcher.startRequest.YouTubeRuntime
	if runtimeConfig["mode"] != "live_api_relay_static" || runtimeConfig["relay_binding_id"] != relayBindingID || runtimeConfig["reusable_live_stream_id"] != reusableLiveStreamID || runtimeConfig["watch_url"] != "https://www.youtube.com/watch?v=broadcast-static-primary" || runtimeConfig["complete_on_stop"] != true {
		t.Fatalf("relay static dispatch runtime missing specialized values: %#v", runtimeConfig)
	}
	for _, key := range []string{"rtmp_url", "stream_key", "stream_key_secret_name"} {
		if value, ok := runtimeConfig[key]; ok {
			t.Fatalf("relay static dispatch runtime leaked %s=%#v: %#v", key, value, runtimeConfig)
		}
	}

	runtime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("get relay static runtime: %v", err)
	}
	if runtime.Mode != "live_api_relay_static" || runtime.YouTubeOutput != youtube.ID || runtime.OAuthAccountID != account.ID || runtime.BroadcastID != "broadcast-static-primary" || runtime.LiveStreamID != reusableLiveStreamID || runtime.RTMPURL != "" || runtime.StreamKeySecretName != "" || !runtime.CompleteOnStop {
		t.Fatalf("unexpected relay static runtime: %#v", runtime)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID)
	if err != nil {
		t.Fatalf("get relay static claim: %v", err)
	}
	if claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.StreamID != stream.ID || claim.YouTubeOutputID != youtube.ID || claim.OAuthAccountID != account.ID || claim.ReusableLiveStreamID != reusableLiveStreamID || claim.BroadcastID != runtime.BroadcastID {
		t.Fatalf("unexpected prepared relay static claim: %#v", claim)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("relay static completion did not finish the prepared broadcast: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relay static runtime must be released only after provider completion, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relay static claim must be released only after provider completion, err=%v", err)
	}
}

func TestStartStreamRelayStaticRejectsNoncanonicalBindingBeforeClaimPrepareOrDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	const rawBindingID = "yt-stream-key-like-value"
	if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, fixture.youtube.Name, map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
		"relay_binding_id":        rawBindingID,
		"reusable_live_stream_id": configString(fixture.youtube.Config, "reusable_live_stream_id"),
	}); err != nil {
		t.Fatal(err)
	}
	res := fixture.start(t)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "youtube_output_invalid_config") {
		t.Fatalf("noncanonical relay binding start status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 {
		t.Fatalf("noncanonical relay binding called YouTube Prepare: %#v", fixture.youtubeLive)
	}
	if fixture.dispatcher.startCalls != 0 {
		t.Fatalf("noncanonical relay binding dispatched the Encoder: %#v", fixture.dispatcher.startRequest)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), rawBindingID); !errors.Is(err, store.ErrInvalidYouTubeRelayBindingClaim) {
		t.Fatalf("noncanonical relay binding lookup must fail strict validation: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("noncanonical relay binding created a claim: %v", err)
	}
	unchanged, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("noncanonical relay binding transitioned the stream: %q", unchanged.Status)
	}
}

func TestStartStreamRelayStaticRejectsOutputChangesBeforeClaimReservation(t *testing.T) {
	for _, tt := range []struct {
		name       string
		wantStatus string
		mutate     func(t *testing.T, fixture relayStaticStartFixtureForTest) error
	}{
		{
			name:       "output profile revision changes",
			wantStatus: "failed",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) error {
				_, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay dispatch output changed", map[string]any{
					"mode":                    "live_api_relay_static",
					"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
					"relay_binding_id":        fixture.relayBindingID,
					"reusable_live_stream_id": "youtube-live-stream-dispatch",
				})
				return err
			},
		},
		{
			name:       "persisted stream output changes",
			wantStatus: "starting",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) error {
				alternate, err := fixture.profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "alternate static relay output", map[string]any{
					"mode":                    "live_api_relay_static",
					"oauth_account_id":        configString(fixture.youtube.Config, "oauth_account_id"),
					"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000004",
					"reusable_live_stream_id": "youtube-live-stream-alternate",
				})
				if err != nil {
					return err
				}
				_, err = fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{YouTubeOutputID: alternate.ID})
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			barrier := &relayStaticReservationBarrierStreamStore{MemoryStreamStore: fixture.streams}
			fixture.server.streams = barrier
			var mutationErr error
			barrier.beforeReserve = func() {
				mutationErr = tt.mutate(t, fixture)
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("reservation barrier mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("stale static output must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("stale static output must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("stale static output must not reserve a recovery claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("stale static output must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != tt.wantStatus {
				t.Fatalf("claim-fenced compensation status=%q want=%q stream=%#v err=%v", stream.Status, tt.wantStatus, stream, err)
			}
		})
	}
}

func TestStartStreamRelayStaticRejectsModeChangesAfterSelectionBeforePrepareOrDispatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func(fixture relayStaticStartFixtureForTest) map[string]any
	}{
		{
			name: "live api",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":             "live_api",
					"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
				}
			},
		},
		{
			name: "stream key",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_mode_change",
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			barrier := &relayStaticSelectionBarrierStreamStore{MemoryStreamStore: fixture.streams}
			fixture.server.streams = barrier
			var mutationErr error
			barrier.afterStaticStartClaim = func() {
				_, mutationErr = fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay output mode changed", tt.config(fixture))
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("selection barrier mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("static mode change must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static mode change must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static mode change must not reserve a claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static mode change must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "failed" {
				t.Fatalf("static mode change must converge claimed lifecycle to failed: stream=%#v err=%v", stream, err)
			}
		})
	}
}

// TestStartStreamRelayStaticRejectsModeChangesAfterPrechecksBeforeStaticFence
// covers the earlier race where capability/readiness checks had accepted the
// selected static output but another writer changed it before the first static
// lifecycle fence. The snapshot must remain static and fail closed rather than
// rerunning a dynamic Prepare path under the old static authorization.
func TestStartStreamRelayStaticRejectsModeChangesAfterPrechecksBeforeStaticFence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func(fixture relayStaticStartFixtureForTest) map[string]any
	}{
		{
			name: "live api",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":             "live_api",
					"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
				}
			},
		},
		{
			name: "stream key",
			config: func(fixture relayStaticStartFixtureForTest) map[string]any {
				return map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_mode_change",
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			var mutationErr error
			fixture.server.dispatcher = &relayStaticPreFenceMutationDispatcher{
				fakeServiceDispatcher: fixture.dispatcher,
				mutate: func() {
					_, mutationErr = fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "static relay output mode changed during precheck", tt.config(fixture))
				},
			}

			response := fixture.start(t)
			if mutationErr != nil {
				t.Fatalf("pre-fence mutation: %v", mutationErr)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
				t.Fatalf("static precheck mode change must request reload before prepare: status=%d body=%s", response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static precheck mode change must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static precheck mode change must not reserve a claim: %v", err)
			}
			if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static precheck mode change must not create a runtime: %v", err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "failed" {
				t.Fatalf("static precheck mode change must terminalize the static lifecycle: stream=%#v err=%v", stream, err)
			}
		})
	}
}

// TestStartStreamRelayStaticRejectsDynamicOutputOverrideBeforePrepareOrDispatch
// closes the request-level variant of the static-to-dynamic race: a caller
// cannot supply a different dynamic output while the persisted stream still
// owns a fixed relay binding.
func TestStartStreamRelayStaticRejectsDynamicOutputOverrideBeforePrepareOrDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	alternate, err := fixture.profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "dynamic override output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": configString(fixture.youtube.Config, "oauth_account_id"),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+fixture.discord.ID+`","youtube_output_id":"`+alternate.ID+`"}`))
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticConfigChanged.Error()) {
		t.Fatalf("dynamic output override status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("dynamic output override must not prepare or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dynamic output override must not create runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dynamic output override must not claim the fixed binding: %v", err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "created" {
		t.Fatalf("dynamic output override must fail before lifecycle claim: stream=%#v err=%v", stream, err)
	}
}

func TestStartStreamRelayStaticReconcilesCommittedFinalizeResponseLossBeforeDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	streamStore := &relayStaticFinalizeResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}
	fixture.server.streams = streamStore

	response := fixture.start(t)
	if response.Code != http.StatusOK {
		t.Fatalf("finalizer response loss should reconcile the committed state before dispatch: status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("reconciled finalizer response loss must prepare and dispatch exactly once: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	runtime, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID)
	if err != nil || runtime.Mode != "live_api_relay_static" || runtime.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("finalizer response loss must retain exact committed runtime: runtime=%#v err=%v", runtime, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.BroadcastID != runtime.BroadcastID {
		t.Fatalf("finalizer response loss must retain exact prepared claim: claim=%#v err=%v", claim, err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "live" {
		t.Fatalf("reconciled finalizer response loss must complete normal start lifecycle: stream=%#v err=%v", stream, err)
	}
}

func TestStartStreamRelayStaticPrepareMarkerResponseLossRequiresRecoveryBeforeProviderCall(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.server.streams = &relayStaticPrepareMarkerResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}

	response := fixture.start(t)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("prepare marker response loss status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("prepare marker response loss must not call provider or dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepare marker response loss must not create runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched || claim.BroadcastID != youtubeRelayStaticUnknownBroadcastID {
		t.Fatalf("prepare marker response loss must retain a pre-dispatch recovery claim: claim=%#v err=%v", claim, err)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("prepare marker response loss must converge static lifecycle to failed: stream=%#v err=%v", stream, err)
	}
}

func TestResolveRelayStaticRecoveryReconcilesInactiveReservedPrepareFence(t *testing.T) {
	reserve := func(t *testing.T, fixture relayStaticStartFixtureForTest, possiblyPrepared bool) store.YouTubeRelayBindingClaim {
		t.Helper()
		starting, transitioned, err := fixture.streams.TransitionStreamStatus(t.Context(), fixture.stream.ID, fixture.stream.Status, "starting")
		if err != nil || !transitioned {
			t.Fatalf("claim static stream start: transitioned=%t err=%v", transitioned, err)
		}
		expectedRevision := fixture.youtube.YouTubeRelayBindingRevision
		claim, err := fixture.streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
			RelayBindingID:                fixture.relayBindingID,
			StreamID:                      starting.ID,
			YouTubeOutputID:               fixture.youtube.ID,
			ExpectedYouTubeOutputRevision: &expectedRevision,
			OAuthAccountID:                configString(fixture.youtube.Config, "oauth_account_id"),
			ReusableLiveStreamID:          configString(fixture.youtube.Config, "reusable_live_stream_id"),
		})
		if err != nil {
			t.Fatalf("reserve static prepare-fence claim: %v", err)
		}
		if possiblyPrepared {
			claim, err = fixture.streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), claim)
			if err != nil {
				t.Fatalf("mark static prepare-fence claim: %v", err)
			}
		}
		if _, transitioned, err := fixture.streams.TransitionStreamStatus(t.Context(), starting.ID, "starting", "failed"); err != nil || !transitioned {
			t.Fatalf("terminalize reserved prepare-fence stream: transitioned=%t err=%v", transitioned, err)
		}
		return claim
	}
	resolve := func(t *testing.T, fixture relayStaticStartFixtureForTest) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(`{"confirm_external_cleanup":true}`))
		req.AddCookie(fixture.cookie)
		req.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.server.ServeHTTP(response, req)
		return response
	}

	t.Run("not attempted reservation releases without provider or encoder call", func(t *testing.T) {
		fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
		claim := reserve(t, fixture, false)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resolved":true`) {
			t.Fatalf("reserved not-attempted recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.dispatcher.stopCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
			t.Fatalf("not-attempted reservation must not call Encoder or provider: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("not-attempted reserved claim must be released: %v", err)
		}
	})

	t.Run("possibly prepared reservation becomes explicit unknown-broadcast recovery", func(t *testing.T) {
		fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
		claim := reserve(t, fixture, true)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_unknown_broadcast"`) {
			t.Fatalf("reserved possibly-prepared recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
			t.Fatalf("possibly-prepared claim must require the no-process fence but no provider cleanup: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("operator-confirmed unknown-broadcast recovery must release claim: %v", err)
		}
	})
}

func TestResolveRelayStaticRecoveryRepairsInactivePreparedDispatchMarkerOutage(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.server.streams = &relayStaticDispatchMarkerRepairStreamStore{
		MemoryStreamStore:       fixture.streams,
		failInitialMarkRecovery: true,
	}

	start := fixture.start(t)
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("dispatch marker outage start status=%d body=%s", start.Code, start.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 0 {
		t.Fatalf("dispatch marker outage must not issue downstream Start: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("dispatch marker outage must terminalize the start: stream=%#v err=%v", stream, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		t.Fatalf("dispatch marker outage must retain exact prepared handoff fence: claim=%#v err=%v", claim, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); err != nil {
		t.Fatalf("dispatch marker outage must retain runtime until explicit repair: %v", err)
	}

	recoveryReq := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(`{"confirm_external_cleanup":true}`))
	recoveryReq.AddCookie(fixture.cookie)
	recoveryReq.Header.Set("X-CSRF-Token", fixture.csrf)
	recoveryRes := httptest.NewRecorder()
	fixture.server.ServeHTTP(recoveryRes, recoveryReq)
	if recoveryRes.Code != http.StatusOK || !strings.Contains(recoveryRes.Body.String(), `"cleanup":"provider_complete"`) {
		t.Fatalf("dispatch marker outage recovery status=%d body=%s", recoveryRes.Code, recoveryRes.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("prepared marker repair must Stop once then Complete, never Delete: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepared marker repair must atomically remove runtime before release: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prepared marker repair must release recovered claim: %v", err)
	}
}

func TestStartStreamRelayStaticObservedFinalizeCommitStillNotifiesDiscord(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if _, err := fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{
		YouTubeOutputID: fixture.youtube.ID,
	}); err != nil {
		t.Fatal(err)
	}
	notifier := &relayStaticNotificationDispatcher{fakeServiceDispatcher: fixture.dispatcher}
	fixture.server.dispatcher = notifier
	fixture.server.streams = &relayStaticFinalizeResponseLostStreamStore{MemoryStreamStore: fixture.streams, responseLost: true}

	response := fixture.start(t)
	if response.Code != http.StatusOK {
		t.Fatalf("observed finalizer commit start status=%d body=%s", response.Code, response.Body.String())
	}
	queued, err := fixture.streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), fixture.stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || queued.WatchURL != "https://www.youtube.com/watch?v=broadcast-static-dispatch" || notifier.notifyCalls != 0 {
		t.Fatalf("observed finalizer commit must queue the static watch URL before provider-live delivery: notification=%s notifier=%s", formatSafeHTTPSensitiveDiagnostic(queued), formatSafeHTTPSensitiveDiagnostic(notifier))
	}
	fixture.server.youtubeLive = &scriptedYouTubeLifecycleClient{fakeYouTubeLiveClient: fixture.youtubeLive, statuses: []string{"live"}}
	result, err := fixture.server.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 1)
	if err != nil || result["claimed"] != 1 || result["delivered"] != 1 {
		t.Fatalf("observed finalizer notification delivery result=%#v err=%v", result, err)
	}
	if notifier.notifyCalls != 1 || notifier.notifiedStream.Status != "live" || notifier.notifiedURL != "https://www.youtube.com/watch?v=broadcast-static-dispatch" {
		t.Fatalf("observed finalizer commit must retain static watch URL for Discord notification: notifier=%s", formatSafeHTTPSensitiveDiagnostic(notifier))
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("observed finalizer commit must still perform one static prepare and dispatch: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
}

func TestStartStreamRelayStaticPrepareStatusChangeRetainsRecoveryFence(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	fixture.youtubeLive.onRelayStaticPrepare = func(ctx context.Context, _ ytlive.RelayStaticPrepareRequest) {
		if _, transitioned, err := fixture.streams.TransitionStreamStatus(ctx, fixture.stream.ID, "starting", "failed"); err != nil || !transitioned {
			t.Fatalf("supersede static start after provider prepare: transitioned=%t err=%v", transitioned, err)
		}
	}

	res := fixture.start(t)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), errYouTubeRelayStaticRecoveryRequired.Error()) {
		t.Fatalf("prepare/status conflict status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 0 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("superseded static prepare must not dispatch or clean up as a live stream: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	updated, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || updated.Status != "failed" {
		t.Fatalf("provider/status conflict must preserve superseding lifecycle state: stream=%#v err=%v", updated, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("finalize conflict must not orphan a static runtime, err=%v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("provider/status conflict must preserve a recovery fence: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("finalize conflict left an unexpected completion retry: due=%#v err=%v", due, err)
	}
}

func TestStartStreamRelayStaticDispatchFailureRequiresRecoveryWithoutProviderCleanup(t *testing.T) {
	youtubeLive := &fakeYouTubeLiveClient{
		relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"},
	}
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStart: true}, youtubeLive)

	res := fixture.start(t)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("dispatch failure status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("unconfirmed static dispatch must never clean up or complete provider state: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("static dispatch failure must remove the automatic completion runtime, err=%v", err)
	}
	failed, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("static dispatch failure must terminalize before recovery fencing: stream=%#v err=%v", failed, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("static dispatch failure must leave no due completion retry: due=%#v err=%v", due, err)
	}
	result, err := fixture.server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil || result["attempted"] != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("completion retry must not touch an unconfirmed static dispatch: result=%#v complete_calls=%d err=%v", result, fixture.youtubeLive.completeCalls, err)
	}

	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_start_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed static start must retain recovery fence: claim=%#v err=%v", claim, err)
	}
}

func TestForceStopRelayStaticDispatchFailureRetainsRecoveryFenceWithoutProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStop: true}, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/force-stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("force stop static dispatch failure status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed force stop must not complete or delete provider state: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed force stop must remain failed: stream=%#v err=%v", stream, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unconfirmed force stop must remove automatic completion runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_force_stop_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed force stop must retain recovery claim: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("unconfirmed force stop must not leave a completion retry: due=%#v err=%v", due, err)
	}
}

func TestStopRelayStaticDispatchFailureRetainsRecoveryFenceWithoutProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStop: true}, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("normal stop static dispatch failure status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed normal stop must not complete or delete provider state: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed normal stop must remain failed: stream=%#v err=%v", stream, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unconfirmed normal stop must remove automatic completion runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_stop_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed normal stop must retain possible-dispatch recovery claim: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("unconfirmed normal stop must not leave a completion retry: due=%#v err=%v", due, err)
	}
}

func TestStopRelayStaticDoesNotNormalizeEncoderNoProcessAfterDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}
	fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
		{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("static no-process stop status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed static encoder stop must not complete or delete provider state: youtube=%#v", fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed static encoder stop must remain failed: stream=%#v err=%v", stream, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() {
		t.Fatalf("static no-process reply must retain the unconfirmed recovery claim: claim=%#v err=%v", claim, err)
	}
}

func TestPartialRelayStaticStopPersistsEncoderReceiptBeforeAbandon(t *testing.T) {
	for _, tt := range []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "normal stop", path: "stop", wantStatus: http.StatusBadGateway},
		{name: "force stop", path: "force-stop", wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			if response := fixture.start(t); response.Code != http.StatusOK {
				t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
			}
			fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
				{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			}
			req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/"+tt.path, nil)
			req.AddCookie(fixture.cookie)
			req.Header.Set("X-CSRF-Token", fixture.csrf)
			response := httptest.NewRecorder()
			fixture.server.ServeHTTP(response, req)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
				t.Fatalf("partial %s status=%d body=%s", tt.path, response.Code, response.Body.String())
			}
			claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
			if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || claim.EncoderStopConfirmedAt.IsZero() {
				t.Fatalf("partial %s must persist Encoder receipt before abandon: claim=%#v err=%v", tt.path, claim, err)
			}
			if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
				t.Fatalf("partial %s must not complete/delete before recovery: youtube=%#v", tt.path, fixture.youtubeLive)
			}
		})
	}
}

func TestForceStopRelayStaticAfterConfirmedStopsCompletesAndReleasesBinding(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	receipts := &relayStaticEncoderStopReceiptRecordingStreamStore{MemoryStreamStore: fixture.streams}
	fixture.server.streams = receipts
	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/force-stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("confirmed force stop status=%d body=%s", response.Code, response.Body.String())
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "completed" {
		t.Fatalf("confirmed force stop must transition to completed before static completion: stream=%#v err=%v", stream, err)
	}
	if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("confirmed force stop must use YouTube Complete exactly once: youtube=%#v", fixture.youtubeLive)
	}
	if receipts.encoderStopReceiptCalls != 1 || receipts.lastEncoderStopClaim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("confirmed force stop must persist Encoder Stop receipt before completion: receipts=%#v", receipts)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed force stop must release static runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed force stop must release static claim: %v", err)
	}
}

func TestStopRelayStaticTransitionsCompletedBeforeProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}
	var completionObservedStatus string
	fixture.youtubeLive.onComplete = func(ctx context.Context, _ ytlive.CompleteRequest) {
		stream, err := fixture.streams.GetStream(ctx, fixture.stream.ID)
		if err != nil {
			t.Fatalf("read static stream during completion: %v", err)
		}
		completionObservedStatus = stream.Status
	}
	receipts := &relayStaticEncoderStopReceiptRecordingStreamStore{MemoryStreamStore: fixture.streams}
	fixture.server.streams = receipts

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("static stop status=%d body=%s", response.Code, response.Body.String())
	}
	if completionObservedStatus != "completed" || fixture.youtubeLive.completeCalls != 1 {
		t.Fatalf("static stop must mark completed before provider completion: observed=%q youtube=%#v", completionObservedStatus, fixture.youtubeLive)
	}
	if receipts.encoderStopReceiptCalls != 1 || receipts.lastEncoderStopClaim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("normal stop must persist Encoder Stop receipt before completion: receipts=%#v", receipts)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed normal stop must release static claim: %v", err)
	}
}

func TestRelayStaticManualAndDueCompletionRequireCompletedStream(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	manual := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/complete", nil)
	manual.AddCookie(fixture.cookie)
	manual.Header.Set("X-CSRF-Token", fixture.csrf)
	manualResponse := httptest.NewRecorder()
	fixture.server.ServeHTTP(manualResponse, manual)
	if manualResponse.Code != http.StatusConflict || !strings.Contains(manualResponse.Body.String(), errYouTubeRelayStaticCompletionRequiresCompleted.Error()) {
		t.Fatalf("manual active static completion must be rejected: status=%d body=%s", manualResponse.Code, manualResponse.Body.String())
	}
	if fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("manual active static completion called provider: %#v", fixture.youtubeLive.completeRequest)
	}
	if _, err := fixture.streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), fixture.stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 0 || result["skipped"] != 1 || result["completed"] != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("due active static completion must skip provider and binding release: result=%#v youtube=%#v", result, fixture.youtubeLive)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); err != nil {
		t.Fatalf("active static completion must retain runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
		t.Fatalf("active static completion must retain claim: %v", err)
	}
}

func TestStartStreamRejectsRelayStaticBindingMismatchBeforeClaimReservePrepareOrDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "mismatched fixed relay stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{
		"output_relay_mode":       "live_api_relay_static",
		"output_relay_binding_id": "relay-00000000-0000-4000-8000-000000000002",
	})
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
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
	discord := createDiscordConfigForTest(t, profiles, "mismatched fixed relay discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "mismatched fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000001",
		"reusable_live_stream_id": "youtube-live-stream-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-primary", LiveStreamID: "youtube-live-stream-primary"}}
	handler := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(youtubeLive),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	request := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "youtube_relay_static_binding_unavailable") {
		t.Fatalf("expected fixed binding rejection, status=%d body=%s", response.Code, response.Body.String())
	}
	if youtubeLive.prepareCalls != 0 || youtubeLive.relayStaticPrepareCalls != 0 || dispatcher.startCalls != 0 {
		t.Fatalf("binding mismatch must reject before provider prepare or dispatch: live_api=%d relay_static=%d dispatch=%d", youtubeLive.prepareCalls, youtubeLive.relayStaticPrepareCalls, dispatcher.startCalls)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000001"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding mismatch must reject before reserving a claim, err=%v", err)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || unchanged.Status != "created" {
		t.Fatalf("binding mismatch changed stream state: stream=%#v err=%v", unchanged, err)
	}
}

func TestStartStreamStaticRelayEncoderRejectsNonStaticOrMissingYouTubeOutputBeforeProviderOrDispatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, fixture relayStaticStartFixtureForTest)
		body   func(fixture relayStaticStartFixtureForTest) string
	}{
		{
			name: "stream key output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "dynamic stream key output", map[string]any{
					"mode":                   "stream_key",
					"rtmp_url":               "rtmps://a.rtmps.youtube.com/live2",
					"stream_key_secret_name": "youtube_stream_key_static_guard",
				}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `","youtube_output_id":"` + fixture.youtube.ID + `"}`
			},
		},
		{
			name: "live api dry run output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.profiles.UpdateProfile(t.Context(), store.ProfileYouTubeOutput, fixture.youtube.ID, "dynamic dry run output", map[string]any{
					"mode": "live_api_dry_run",
				}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `","youtube_output_id":"` + fixture.youtube.ID + `"}`
			},
		},
		{
			name: "missing output",
			mutate: func(t *testing.T, fixture relayStaticStartFixtureForTest) {
				t.Helper()
				if _, err := fixture.streams.UpdateStreamSettings(t.Context(), fixture.stream.ID, store.StreamSettings{}); err != nil {
					t.Fatal(err)
				}
			},
			body: func(fixture relayStaticStartFixtureForTest) string {
				return `{"discord_config_id":"` + fixture.discord.ID + `"}`
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			tt.mutate(t, fixture)
			req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/start", bytes.NewBufferString(tt.body(fixture)))
			req.AddCookie(fixture.cookie)
			req.Header.Set("X-CSRF-Token", fixture.csrf)
			response := httptest.NewRecorder()
			fixture.server.ServeHTTP(response, req)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), errYouTubeRelayStaticBindingUnavailable.Error()) {
				t.Fatalf("static relay Encoder %s status=%d body=%s", tt.name, response.Code, response.Body.String())
			}
			if fixture.youtubeLive.prepareCalls != 0 || fixture.youtubeLive.relayStaticPrepareCalls != 0 || fixture.dispatcher.startCalls != 0 {
				t.Fatalf("static relay Encoder %s must reject before provider/dispatch: youtube=%#v dispatcher=%#v", tt.name, fixture.youtubeLive, fixture.dispatcher)
			}
			if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("static relay Encoder %s must not reserve a static claim: %v", tt.name, err)
			}
			stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
			if err != nil || stream.Status != "created" {
				t.Fatalf("static relay Encoder %s must leave stream unstarted: stream=%#v err=%v", tt.name, stream, err)
			}
		})
	}
}

func TestApplyRelayStaticYouTubePreservesRecoveryClaimWhenBindCleanupIsUncertain(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "uncertain fixed relay stream")
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
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "uncertain fixed relay output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000005",
		"reusable_live_stream_id": "youtube-live-stream-uncertain",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	youtubeLive := &fakeYouTubeLiveClient{relayStaticPrepareErr: &ytlive.RelayStaticBindError{
		BroadcastID:      "broadcast-static-uncertain",
		LiveStreamID:     "youtube-live-stream-uncertain",
		CleanupConfirmed: false,
	}}
	server := &Server{streams: streams, profiles: profiles, integrations: integrations, youtubeLive: youtubeLive}
	request := &servicecall.StartRequest{YouTubeOutputID: youtube.ID}
	err = server.applyYouTubeRelayStaticOutput(t.Context(), stream, youtube, store.StreamStartOwnershipClaim{}, request)
	if !errors.Is(err, errYouTubeRelayStaticRecoveryRequired) {
		t.Fatalf("uncertain bind error=%v, want recovery-required", err)
	}
	if youtubeLive.relayStaticPrepareCalls != 1 {
		t.Fatalf("relay-static provider was not called once: %d", youtubeLive.relayStaticPrepareCalls)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000005")
	if err != nil {
		t.Fatalf("uncertain bind claim was not preserved: %v", err)
	}
	if claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != "broadcast-static-uncertain" || claim.LastError != ytlive.ErrRelayStaticBindCleanupUncertain.Error() {
		t.Fatalf("unexpected recovery claim: %#v", claim)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("uncertain bind must not create a runtime, err=%v", err)
	}
}

func TestApplyRelayStaticYouTubeRetainsEveryPostPrepareFenceFailure(t *testing.T) {
	for _, tt := range []struct {
		name            string
		prepareErr      error
		wantBroadcastID string
	}{
		{
			name:            "unknown transport result remains fenced",
			prepareErr:      errors.New("youtube insert response lost"),
			wantBroadcastID: youtubeRelayStaticUnknownBroadcastID,
		},
		{
			name:            "known reusable stream validation remains fenced after marker",
			prepareErr:      ytlive.ErrReusableLiveStreamNotFound,
			wantBroadcastID: youtubeRelayStaticUnknownBroadcastID,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "relay static prepare classification")
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
			youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static output", map[string]any{
				"mode":                    "live_api_relay_static",
				"oauth_account_id":        account.ID,
				"relay_binding_id":        "relay-00000000-0000-4000-8000-000000000006",
				"reusable_live_stream_id": "youtube-live-stream-prepare-classification",
			})
			if err != nil {
				t.Fatal(err)
			}
			stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
			server := &Server{
				streams:      streams,
				profiles:     profiles,
				integrations: integrations,
				youtubeLive:  &fakeYouTubeLiveClient{relayStaticPrepareErr: tt.prepareErr},
			}
			err = server.applyYouTubeRelayStaticOutput(t.Context(), stream, youtube, store.StreamStartOwnershipClaim{}, &servicecall.StartRequest{YouTubeOutputID: youtube.ID})
			if !errors.Is(err, errYouTubeRelayStaticRecoveryRequired) {
				t.Fatalf("prepare error=%v, want recovery-required", err)
			}
			claim, claimErr := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), "relay-00000000-0000-4000-8000-000000000006")
			if claimErr != nil {
				t.Fatalf("post-marker prepare result must retain recovery claim: %v", claimErr)
			}
			if claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.BroadcastID != tt.wantBroadcastID || claim.LastError != "youtube_relay_static_prepare_uncertain" {
				t.Fatalf("unexpected post-marker recovery claim: %#v", claim)
			}
			if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("post-marker prepare result must not create a runtime, err=%v", err)
			}
		})
	}
}

func TestResolveRelayStaticYouTubeRecoveryRequiresConfirmationAndFencedCleanup(t *testing.T) {
	type recoveryFixture struct {
		handler     http.Handler
		streams     *store.MemoryStreamStore
		stream      store.Stream
		claim       store.YouTubeRelayBindingClaim
		youtubeLive *fakeYouTubeLiveClient
		dispatcher  *fakeServiceDispatcher
		cookie      *http.Cookie
		csrf        string
		auth        *store.MemoryAuthStore
	}
	newFixture := func(t *testing.T, relayBindingID, broadcastID string) recoveryFixture {
		t.Helper()
		auth := store.NewMemoryAuthStore()
		if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.stop"}); err != nil {
			t.Fatal(err)
		}
		streams := store.NewMemoryStreamStore()
		stream, err := streams.CreateStream(t.Context(), "relay static recovery")
		if err != nil {
			t.Fatal(err)
		}
		registerServiceInstance(t, auth, "encoder-recorder-01", "encoder_recorder")
		if _, err := auth.AssignServiceToStream(t.Context(), "encoder-recorder-01", stream.ID, "test-user"); err != nil {
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
		youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static recovery output", map[string]any{
			"mode":                    "live_api_relay_static",
			"oauth_account_id":        account.ID,
			"relay_binding_id":        relayBindingID,
			"reusable_live_stream_id": "youtube-live-stream-recovery",
		})
		if err != nil {
			t.Fatal(err)
		}
		stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
		expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
		reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
			RelayBindingID:                relayBindingID,
			StreamID:                      stream.ID,
			YouTubeOutputID:               youtube.ID,
			ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
			OAuthAccountID:                account.ID,
			ReusableLiveStreamID:          "youtube-live-stream-recovery",
		})
		if err != nil {
			t.Fatal(err)
		}
		reserved.BroadcastID = broadcastID
		reserved.LastError = "youtube_relay_static_prepare_uncertain"
		claim, err := streams.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(t.Context(), reserved)
		if err != nil {
			t.Fatal(err)
		}
		stream, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "failed")
		if err != nil || !transitioned {
			t.Fatalf("terminalize recovery fixture stream: transitioned=%t err=%v", transitioned, err)
		}
		youtubeLive := &fakeYouTubeLiveClient{}
		dispatcher := &fakeServiceDispatcher{}
		handler := NewServer(streams,
			WithAuthStore(auth),
			WithAuditStore(auth),
			WithServiceRegistryStore(auth),
			WithIntegrationStore(integrations),
			WithProfileStore(profiles),
			WithYouTubeLiveClient(youtubeLive),
			WithServiceDispatcher(dispatcher),
		)
		cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
		return recoveryFixture{handler: handler, streams: streams, stream: stream, claim: claim, youtubeLive: youtubeLive, dispatcher: dispatcher, cookie: cookie, csrf: csrf, auth: auth}
	}
	resolve := func(t *testing.T, fixture recoveryFixture, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(body))
		req.AddCookie(fixture.cookie)
		req.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, req)
		return response
	}

	t.Run("requires explicit confirmation", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000007", youtubeRelayStaticUnknownBroadcastID)
		response := resolve(t, fixture, `{}`)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "youtube_relay_static_external_cleanup_confirmation_required") {
			t.Fatalf("missing confirmation status=%d body=%s", response.Code, response.Body.String())
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); err != nil {
			t.Fatalf("missing confirmation must retain claim: %v", err)
		}
	})

	t.Run("known broadcast uses confirmed provider delete", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000008", "broadcast-static-recovery-known")
		// This claim was durably marked before any Start dispatch. The Encoder's
		// exact no-process reply is therefore a safe receipt, unlike it would be
		// after a possibly-dispatched hand-off.
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder-recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		response := resolve(t, fixture, `{"confirm_external_cleanup":true}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_delete"`) {
			t.Fatalf("known recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.relayStaticCleanupCalls != 1 || fixture.youtubeLive.relayStaticCleanupRequest.BroadcastID != fixture.claim.BroadcastID || fixture.youtubeLive.relayStaticCleanupRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
			t.Fatalf("known recovery did not perform the fixed-relay delete: %#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("confirmed provider delete must release recovery claim: %v", err)
		}
		if !hasAuditAction(fixture.auth.AuditEvents(), "streams.youtube_relay_static_recovery.resolve") {
			t.Fatalf("recovery resolution audit is missing: %#v", fixture.auth.AuditEvents())
		}
	})

	t.Run("unknown broadcast requires and records operator attestation", func(t *testing.T) {
		fixture := newFixture(t, "relay-00000000-0000-4000-8000-000000000009", youtubeRelayStaticUnknownBroadcastID)
		response := resolve(t, fixture, `{"confirm_external_cleanup":true}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_unknown_broadcast"`) {
			t.Fatalf("unknown recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("unknown broadcast sentinel must never be sent to provider delete: %#v", fixture.youtubeLive.relayStaticCleanupRequest)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.claim.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("operator-confirmed unknown recovery must release claim: %v", err)
		}
	})
}

// TestResolveRelayStaticYouTubeRecoveryPossiblyDispatchedRequiresEncoderStop
// exercises the durable hand-off fence set immediately before downstream Start.
// A failed Start response is never proof that the Encoder did not receive it:
// recovery must require an Encoder receipt and Complete (never Delete) before
// releasing the binding.
func TestResolveRelayStaticYouTubeRecoveryPossiblyDispatchedRequiresEncoderStop(t *testing.T) {
	newFixture := func(t *testing.T, youtubeLive *fakeYouTubeLiveClient) relayStaticStartFixtureForTest {
		t.Helper()
		fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStart: true}, youtubeLive)
		response := fixture.start(t)
		if response.Code != http.StatusBadGateway {
			t.Fatalf("create possibly-dispatched recovery fixture: status=%d body=%s", response.Code, response.Body.String())
		}
		claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
		if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
			t.Fatalf("start failure must retain a possibly-dispatched recovery claim: claim=%#v err=%v", claim, err)
		}
		return fixture
	}
	resolve := func(t *testing.T, fixture relayStaticStartFixtureForTest) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/relay-static/recovery/resolve", bytes.NewBufferString(`{"confirm_external_cleanup":true}`))
		req.AddCookie(fixture.cookie)
		req.Header.Set("X-CSRF-Token", fixture.csrf)
		response := httptest.NewRecorder()
		fixture.server.ServeHTTP(response, req)
		return response
	}

	t.Run("encoder receipt completes without delete", func(t *testing.T) {
		fixture := newFixture(t, nil)
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("possibly-dispatched recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("possibly-dispatched recovery must Complete and never Delete: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("confirmed encoder stop and Complete must release claim: %v", err)
		}
	})

	t.Run("encoder no-process response is insufficient after dispatch hand-off", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		response := resolve(t, fixture)
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "youtube_relay_static_recovery_encoder_stop_unconfirmed") {
			t.Fatalf("possibly-dispatched idle reply status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("unconfirmed encoder stop must not touch provider cleanup: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
			t.Fatalf("unconfirmed encoder stop must retain claim: %v", err)
		}
	})

	t.Run("encoder stop failure retains claim before provider cleanup", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.failStop = true
		response := resolve(t, fixture)
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "youtube_relay_static_recovery_encoder_stop_unconfirmed") {
			t.Fatalf("failed encoder stop status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("failed encoder stop must not touch provider cleanup: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
			t.Fatalf("failed encoder stop must retain claim: %v", err)
		}
	})

	t.Run("operator attestation releases known broadcast after reconciled completion failure", func(t *testing.T) {
		fixture := newFixture(t, &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"}, completeErr: errors.New("youtube completion unavailable")})
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"operator_confirmed_provider_cleanup"`) {
			t.Fatalf("operator-attested completion recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("operator-attested completion must never fall back to Delete: youtube=%#v", fixture.youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("explicit operator attestation with durable Encoder receipt must release claim: %v", err)
		}
		var attested bool
		for _, event := range fixture.server.audit.(*store.MemoryAuthStore).AuditEvents() {
			if event.Action == "streams.youtube_relay_static_recovery.resolve" && event.Metadata != nil {
				attested, _ = event.Metadata["operator_attested_provider_cleanup"].(bool)
			}
		}
		if !attested {
			t.Fatalf("operator-confirmed provider cleanup must be audited: %#v", fixture.server.audit.(*store.MemoryAuthStore).AuditEvents())
		}
	})

	t.Run("worker and bot stop warnings do not replace encoder receipt", func(t *testing.T) {
		fixture := newFixture(t, nil)
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "bot_stop_failed", Success: false},
		}
		response := resolve(t, fixture)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("non-encoder warning recovery status=%d body=%s", response.Code, response.Body.String())
		}
		if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("non-encoder warnings must preserve encoder-gated Complete recovery: youtube=%#v", fixture.youtubeLive)
		}
	})

	t.Run("durable encoder receipt survives recovery retry and suppresses stale encoder no-process", func(t *testing.T) {
		youtubeLive := &fakeYouTubeLiveClient{
			relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"},
		}
		fixture := newFixture(t, youtubeLive)
		initialStopCalls := fixture.dispatcher.stopCalls
		// First persist the positive Encoder receipt while the reconciled
		// completion client is unavailable. This retains the recovery claim
		// without using the explicit operator-cleanup attestation branch.
		fixture.server.youtubeLive = youtubeLive
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "bot_stop_failed", Success: false},
		}
		first := resolve(t, fixture)
		if first.Code != http.StatusServiceUnavailable || !strings.Contains(first.Body.String(), "youtube_relay_static_recovery_cleanup_unavailable") {
			t.Fatalf("receipt persistence recovery status=%d body=%s", first.Code, first.Body.String())
		}
		claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
		if err != nil || claim.EncoderStopConfirmedAt.IsZero() {
			t.Fatalf("positive encoder Stop must persist recovery receipt: claim=%#v err=%v", claim, err)
		}
		if fixture.dispatcher.stopCalls != initialStopCalls+1 || youtubeLive.completeCalls != 0 {
			t.Fatalf("first recovery must dispatch one additional stop and retain before completion: initial_stop_calls=%d dispatcher=%#v youtube=%#v", initialStopCalls, fixture.dispatcher, youtubeLive)
		}

		// Simulate a later retry after the Encoder has restarted and only reports
		// its normal no-process 404. The durable first receipt, not this stale
		// response, is what permits the reconciled provider completion.
		fixture.server.youtubeLive = &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}
		fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{{
			ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop",
			StatusCode: http.StatusNotFound, Code: "stream_not_running", Success: false,
		}}
		second := resolve(t, fixture)
		if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"cleanup":"provider_complete"`) {
			t.Fatalf("durable-receipt retry status=%d body=%s", second.Code, second.Body.String())
		}
		if fixture.dispatcher.stopCalls != initialStopCalls+1 || youtubeLive.completeCalls != 1 || youtubeLive.relayStaticCleanupCalls != 0 {
			t.Fatalf("durable receipt must skip stale stop and never Delete: dispatcher=%#v youtube=%#v", fixture.dispatcher, youtubeLive)
		}
		if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("reconciled completion must release the durable receipt claim: %v", err)
		}
	})
}

func TestCompleteRelayStaticYouTubeRetainsClaimUntilProviderCompletionSucceeds(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "relay static completion retry")
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
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "relay static completion output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000a",
		"reusable_live_stream_id": "youtube-live-stream-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000a",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                account.ID,
		ReusableLiveStreamID:          "youtube-live-stream-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err = streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark static completion fixture possibly prepared: %v", err)
	}
	reserved.BroadcastID = "broadcast-static-complete"
	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  reserved.YouTubeOutputID,
		OAuthAccountID: account.ID,
		Mode:           "live_api_relay_static",
		BroadcastID:    reserved.BroadcastID,
		LiveStreamID:   reserved.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), reserved, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), reserved); err != nil {
		t.Fatalf("mark static completion fixture possibly dispatched: %v", err)
	}
	if _, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "completed"); err != nil || !transitioned {
		t.Fatalf("mark static completion fixture completed: transitioned=%t err=%v", transitioned, err)
	}
	youtubeLive := &fakeYouTubeLiveClient{completeErr: errors.New("youtube transition unavailable")}
	withoutReconciler := &Server{streams: streams, integrations: integrations, youtubeLive: youtubeLive}
	if _, err := withoutReconciler.completeYouTubeRuntime(t.Context(), stream.ID, true); !errors.Is(err, errYouTubeRelayStaticUnavailable) {
		t.Fatalf("static completion without a reconciler err=%v, want fail-closed unavailable", err)
	}
	if youtubeLive.completeCalls != 0 {
		t.Fatalf("static completion must not fall back to generic Complete: calls=%d", youtubeLive.completeCalls)
	}
	stillFencedRuntime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stillFencedRuntime.CompleteRetryCount != 1 {
		t.Fatalf("missing reconciler must retain and retry static runtime: runtime=%#v err=%v", stillFencedRuntime, err)
	}
	if claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		t.Fatalf("missing reconciler must retain static claim: claim=%#v err=%v", claim, err)
	}
	server := &Server{streams: streams, integrations: integrations, youtubeLive: &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}}
	if _, err := server.completeYouTubeRuntime(t.Context(), stream.ID, true); !errors.Is(err, errYouTubeLiveAPICompleteFailed) {
		t.Fatalf("completion error=%v, want provider completion failure", err)
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID {
		t.Fatalf("provider completion was not attempted: %#v", youtubeLive.completeRequest)
	}
	storedRuntime, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("provider failure must retain runtime for retry: %v", err)
	}
	if storedRuntime.CompleteRetryCount != 2 || storedRuntime.CompleteNextRetryAt.IsZero() {
		t.Fatalf("provider failure did not schedule retry: %#v", storedRuntime)
	}
	claim, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStatePrepared {
		t.Fatalf("provider failure must retain prepared claim: claim=%#v err=%v", claim, err)
	}

	youtubeLive.completeErr = nil
	if _, err := server.completeYouTubeRuntime(t.Context(), stream.ID, true); err != nil {
		t.Fatalf("completion retry: %v", err)
	}
	if youtubeLive.completeCalls != 2 {
		t.Fatalf("completion retry did not call provider: %d", youtubeLive.completeCalls)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("successful completion must atomically release runtime, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("successful completion must atomically release claim, err=%v", err)
	}
}

func TestYouTubeOutputMutationAndDeleteRejectActiveRelayStaticClaim(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"youtube_outputs.update", "youtube_outputs.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "claimed output stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "claimed static output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        "8fd47de4-5aec-486f-99c7-10591076c408",
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000b",
		"reusable_live_stream_id": "youtube-live-stream-claimed-output",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	if _, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000b",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                "8fd47de4-5aec-486f-99c7-10591076c408",
		ReusableLiveStreamID:          "youtube-live-stream-claimed-output",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			request := httptest.NewRequest(method, "/youtube/outputs/"+youtube.ID, bytes.NewBufferString(`{}`))
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "youtube_relay_binding_release_pending") {
				t.Fatalf("active claim %s status=%d body=%s", method, response.Code, response.Body.String())
			}
		})
	}
	if _, err := profiles.GetProfile(t.Context(), store.ProfileYouTubeOutput, youtube.ID); err != nil {
		t.Fatalf("active relay claim must retain output profile: %v", err)
	}
}

func TestCompleteDueYouTubeRuntimesCompletesRelayStaticAndReleasesClaim(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "due relay static completion")
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
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "due relay static completion output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        "relay-00000000-0000-4000-8000-00000000000c",
		"reusable_live_stream_id": "youtube-live-stream-due-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream = setRelayStaticReservationPreconditionsForTest(t, streams, profiles, stream, youtube)
	expectedYouTubeOutputRevision := youtube.YouTubeRelayBindingRevision
	reserved, err := streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), store.YouTubeRelayBindingClaim{
		RelayBindingID:                "relay-00000000-0000-4000-8000-00000000000c",
		StreamID:                      stream.ID,
		YouTubeOutputID:               youtube.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                account.ID,
		ReusableLiveStreamID:          "youtube-live-stream-due-complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err = streams.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(t.Context(), reserved)
	if err != nil {
		t.Fatalf("mark due static completion fixture possibly prepared: %v", err)
	}
	reserved.BroadcastID = "broadcast-static-due-complete"
	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  reserved.YouTubeOutputID,
		OAuthAccountID: account.ID,
		Mode:           "live_api_relay_static",
		BroadcastID:    reserved.BroadcastID,
		LiveStreamID:   reserved.ReusableLiveStreamID,
		CompleteOnStop: true,
	}
	if err := streams.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(t.Context(), reserved, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(t.Context(), reserved); err != nil {
		t.Fatalf("mark due static completion fixture possibly dispatched: %v", err)
	}
	if _, transitioned, err := streams.TransitionStreamStatus(t.Context(), stream.ID, "starting", "completed"); err != nil || !transitioned {
		t.Fatalf("mark due static completion fixture completed: transitioned=%t err=%v", transitioned, err)
	}
	if _, err := streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{}
	server := &Server{streams: streams, integrations: integrations, youtubeLive: &relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 1 || result["failed"] != 0 || youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != runtime.BroadcastID {
		t.Fatalf("due relay-static completion result=%#v completion=%#v", result, youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("due relay-static completion must release runtime, err=%v", err)
	}
	if _, err := streams.GetStreamYouTubeRelayBindingClaim(t.Context(), reserved.RelayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("due relay-static completion must release claim, err=%v", err)
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

func TestStopStreamHonorsYouTubeCompleteOnStopFalseAndManualRetryForcesComplete(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "manual youtube complete stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
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
	discord := createDiscordConfigForTest(t, profiles, "youtube manual complete discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "manual-complete-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
		"complete_on_stop": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-manual", LiveStreamID: "live-stream-manual"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil || stored.CompleteOnStop {
		t.Fatalf("youtube runtime should store complete_on_stop=false: %#v err=%v", stored, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if youtubeLive.completeCalls != 0 {
		t.Fatalf("youtube complete should be skipped on normal stop when complete_on_stop=false: %#v", youtubeLive.completeRequest)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should still be cleared after skipped complete, got err=%v", err)
	}
	if !strings.Contains(toJSONForTest(t, auth.AuditEvents()), `"complete_skipped":true`) {
		t.Fatalf("audit should record skipped complete metadata: %s", toJSONForTest(t, auth.AuditEvents()))
	}

	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{StreamID: stream.ID, YouTubeOutput: youtube.ID, OAuthAccountID: account.ID, Mode: "live_api", BroadcastID: "broadcast-manual-retry", LiveStreamID: "live-stream-manual-retry", CompleteOnStop: false}); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube/complete", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK || !strings.Contains(retryRes.Body.String(), `"completed":true`) {
		t.Fatalf("manual complete retry status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-manual-retry" {
		t.Fatalf("manual complete retry should force YouTube complete: %#v", youtubeLive)
	}
}

func TestStopStreamKeepsYouTubeRuntimeWhenLiveAPICompleteFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
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
	discord := createDiscordConfigForTest(t, profiles, "youtube live api discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
		"enable_auto_stop": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	youtubeLive := &fakeYouTubeLiveClient{
		prepared:    ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-retry", LiveStreamID: "live-stream-retry"},
		completeErr: errors.New("youtube transition failed"),
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK || !strings.Contains(stopRes.Body.String(), "youtube_complete_warning") {
		t.Fatalf("expected physical stop with youtube completion warning, status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(stopRes.Body.String(), raw) {
			t.Fatalf("youtube stop failure leaked secret %q in response: %s", raw, stopRes.Body.String())
		}
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain for retry after complete failure: %v", err)
	}
	if stored.BroadcastID != "broadcast-retry" || stored.OAuthAccountID != account.ID || stored.YouTubeOutput != youtube.ID {
		t.Fatalf("unexpected retained youtube runtime: %#v", stored)
	}
	if stored.CompleteRetryCount != 1 || stored.CompleteNextRetryAt.IsZero() || stored.CompleteLastError != "youtube_live_api_complete_failed" {
		t.Fatalf("youtube complete failure should schedule retry metadata: %#v", stored)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "completed" {
		t.Fatalf("stream should complete even when YouTube completion is retried, got %s", updated.Status)
	}

	youtubeLive.completeErr = nil
	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube/complete", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK || !strings.Contains(retryRes.Body.String(), `"completed":true`) {
		t.Fatalf("expected youtube complete retry success, status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	if youtubeLive.completeCalls != 2 || youtubeLive.completeRequest.BroadcastID != "broadcast-retry" {
		t.Fatalf("youtube complete retry was not called correctly: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after manual complete retry, got err=%v", err)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(retryRes.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube complete retry leaked secret %q, response=%s audit=%s", raw, retryRes.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
}

func TestAutoRetryCompletesDueYouTubeLiveAPIRuntime(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "auto retry youtube stream")
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
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       "youtube-output-01",
		OAuthAccountID:      account.ID,
		Mode:                "live_api",
		BroadcastID:         "broadcast-auto-retry",
		LiveStreamID:        "live-stream-auto-retry",
		StreamKeySecretName: "youtube_stream_key_runtime_auto_retry",
		CompleteOnStop:      true,
		CompleteRetryCount:  1,
		CompleteNextRetryAt: time.Now().UTC().Add(-time.Minute),
		CompleteLastError:   "youtube_live_api_complete_failed",
	}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_runtime_auto_retry", "runtime-youtube-live-api-key"); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{}
	server := &Server{streams: streams, audit: auth, integrations: integrations, secrets: secrets, youtubeLive: youtubeLive}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 1 || result["failed"] != 0 {
		t.Fatalf("unexpected auto retry result: %#v", result)
	}
	if youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-auto-retry" || youtubeLive.completeRequest.Credentials.RefreshToken != "raw-youtube-refresh-token" {
		t.Fatalf("auto retry did not complete YouTube runtime with OAuth credentials: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after auto retry, got err=%v", err)
	}
	if _, err := secrets.GetSecretValue(t.Context(), "youtube_stream_key_runtime_auto_retry"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("runtime stream key secret should be cleared after auto retry, got err=%v", err)
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if !strings.Contains(auditJSON, `"trigger":"auto_retry"`) || strings.Contains(auditJSON, "runtime-youtube-live-api-key") || strings.Contains(auditJSON, "raw-youtube-refresh-token") || strings.Contains(auditJSON, "raw-youtube-client-secret") {
		t.Fatalf("auto retry audit missing or leaked secret: %s", auditJSON)
	}
}

func TestAutoRetryBacksOffDueYouTubeLiveAPIFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "auto retry failure youtube stream")
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
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:            stream.ID,
		YouTubeOutput:       "youtube-output-01",
		OAuthAccountID:      account.ID,
		Mode:                "live_api",
		BroadcastID:         "broadcast-auto-retry-failure",
		LiveStreamID:        "live-stream-auto-retry-failure",
		StreamKeySecretName: "youtube_stream_key_runtime_auto_retry_failure",
		CompleteOnStop:      true,
		CompleteRetryCount:  1,
		CompleteNextRetryAt: time.Now().UTC().Add(-time.Minute),
		CompleteLastError:   "youtube_live_api_complete_failed",
	}); err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{completeErr: errors.New("youtube transition failed")}
	server := &Server{streams: streams, audit: auth, integrations: integrations, secrets: store.NewMemorySecretStore(), youtubeLive: youtubeLive}
	result, err := server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 1 || result["completed"] != 0 || result["failed"] != 1 {
		t.Fatalf("unexpected auto retry failure result: %#v", result)
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain after failed auto retry: %v", err)
	}
	if stored.CompleteRetryCount != 2 || stored.CompleteNextRetryAt.IsZero() || !stored.CompleteNextRetryAt.After(time.Now().UTC()) || stored.CompleteLastError != "youtube_live_api_complete_failed" {
		t.Fatalf("failed auto retry should update retry backoff metadata: %#v", stored)
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if !strings.Contains(auditJSON, `"trigger":"auto_retry"`) || !strings.Contains(auditJSON, "youtube_live_api_complete_failed") || strings.Contains(auditJSON, "raw-youtube-refresh-token") || strings.Contains(auditJSON, "raw-youtube-client-secret") {
		t.Fatalf("auto retry failure audit missing or leaked secret: %s", auditJSON)
	}
}

func TestStartStreamCompletesYouTubeLiveAPIWhenDispatchFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dispatch failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
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
	discord := createDiscordConfigForTest(t, profiles, "youtube dispatch failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{failStart: true}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-cleanup", LiveStreamID: "live-stream-cleanup"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("expected dispatch failure, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.completeCalls != 1 || youtubeLive.completeRequest.BroadcastID != "broadcast-cleanup" {
		t.Fatalf("youtube live api cleanup was not called correctly: %#v", youtubeLive)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(res.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube dispatch failure leaked secret %q, response=%s audit=%s", raw, res.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should be cleared after successful start-failure cleanup, got err=%v", err)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("stream should be failed after dispatch failure, got %s", updated.Status)
	}
}

func TestStartStreamKeepsYouTubeRuntimeWhenDispatchFailureCleanupFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dispatch cleanup failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
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
	discord := createDiscordConfigForTest(t, profiles, "youtube dispatch cleanup failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
		"privacy_status":   "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{failStart: true}
	youtubeLive := &fakeYouTubeLiveClient{
		prepared:    ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-retained", LiveStreamID: "live-stream-retained"},
		completeErr: errors.New("youtube transition failed"),
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("expected dispatch failure, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 1 || youtubeLive.completeCalls != 1 {
		t.Fatalf("youtube cleanup should be attempted after dispatch failure: %#v", youtubeLive)
	}
	for _, raw := range []string{"runtime-youtube-live-api-key", "raw-youtube-refresh-token", "raw-youtube-client-secret"} {
		if strings.Contains(res.Body.String(), raw) || strings.Contains(toJSONForTest(t, auth.AuditEvents()), raw) {
			t.Fatalf("youtube cleanup failure leaked secret %q, response=%s audit=%s", raw, res.Body.String(), toJSONForTest(t, auth.AuditEvents()))
		}
	}
	stored, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("youtube runtime should remain for cleanup retry after dispatch failure cleanup error: %v", err)
	}
	if stored.BroadcastID != "broadcast-retained" || stored.OAuthAccountID != account.ID || stored.YouTubeOutput != youtube.ID {
		t.Fatalf("unexpected retained youtube runtime: %#v", stored)
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("stream should be failed after dispatch failure, got %s", updated.Status)
	}
}

func TestStartStreamRejectsYouTubeLiveAPIWithoutOAuthAccount(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "real youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube oauth unavailable discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": "oauth-account-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "youtube_oauth_account_unavailable") {
		t.Fatalf("expected youtube oauth account unavailable conflict, status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called when youtube oauth account is unavailable")
	}
}

func TestStartStreamDoesNotPrepareYouTubeLiveAPIWhenAssignmentsAreMissing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "missing assignment youtube stream")
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
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-must-not-exist"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "missing_stream_assignments") {
		t.Fatalf("expected missing assignments, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("youtube live api prepare must not run before assignment checks: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should not be saved when assignment checks fail, got err=%v", err)
	}
}

func TestStartStreamDoesNotPrepareYouTubeLiveAPIWhenArchiveReadinessFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive failure youtube stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
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
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "not ready oauth archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube archive failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "invalid-service-account-archive", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-must-not-exist"}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeLive))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "drive_oauth_account_unavailable") {
		t.Fatalf("expected archive profile invalid config, status = %d body = %s", res.Code, res.Body.String())
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("youtube live api prepare must not run before archive readiness checks: %#v", youtubeLive)
	}
	if _, err := streams.GetStreamYouTubeRuntime(t.Context(), stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("youtube runtime should not be saved when archive readiness fails, got err=%v", err)
	}
}

func TestStreamStartReadinessEndpointReportsMissingYouTubeStreamKeyWithoutReadingSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "youtube readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "missing-key-output", map[string]any{
		"mode":                   "stream_key",
		"rtmp_url":               "rtmps://youtube.example.com/live2",
		"stream_key_secret_name": "youtube_stream_key_missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := &trackingSecretStore{}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "youtube_stream_key_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if secrets.getCalls != 0 {
		t.Fatalf("readiness must not read raw youtube stream key, calls=%d", secrets.getCalls)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if strings.Contains(res.Body.String(), "<RAW_DISCORD_TOKEN>") || strings.Contains(res.Body.String(), "runtime-secret-stream-key") {
		t.Fatalf("readiness response leaked a raw secret: %s", res.Body.String())
	}
}

func TestStreamStartReadinessEndpointReportsYouTubeLiveAPIAccountIssueWithoutPrepare(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "youtube live readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "youtube live readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "live-api-output", map[string]any{
		"mode":             "live_api",
		"oauth_account_id": "missing-oauth-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeLive := &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{RTMPURL: "rtmps://youtube.example.com/live2", StreamKey: "runtime-youtube-live-api-key", BroadcastID: "broadcast-01"}}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(store.NewMemoryIntegrationStore()), WithYouTubeLiveClient(youtubeLive), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "youtube_oauth_account_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if youtubeLive.prepareCalls != 0 {
		t.Fatalf("readiness must not call YouTube Live API prepare, calls=%d", youtubeLive.prepareCalls)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if strings.Contains(res.Body.String(), "runtime-youtube-live-api-key") {
		t.Fatalf("readiness response leaked a runtime secret: %s", res.Body.String())
	}
}

func TestOAuthAccountDeleteRejectsDriveYouTubeAndRuntimeReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "live api stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	profiles := store.NewMemoryProfileStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Live API",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Live API Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "Live API Output", map[string]any{
		"mode":             "live_api",
		"rtmp_url":         "rtmps://a.rtmp.youtube.com/live2",
		"oauth_account_id": account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  output.ID,
		OAuthAccountID: account.ID,
		Mode:           "live_api",
		BroadcastID:    "broadcast-01",
		LiveStreamID:   "live-stream-01",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-accounts/"+account.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	body := deleteRes.Body.String()
	if deleteRes.Code != http.StatusConflict || !strings.Contains(body, "oauth_account_in_use") {
		t.Fatalf("expected account in-use conflict, status=%d body=%s", deleteRes.Code, body)
	}
	for _, expected := range []string{"drive_destinations", "youtube_outputs", "stream_youtube_runtimes"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("delete conflict missing reference count %q: %s", expected, body)
		}
	}
	for _, raw := range []string{"raw-refresh-token", "raw-google-client-secret", "raw-drive-folder-id"} {
		if strings.Contains(body, raw) {
			t.Fatalf("delete conflict leaked secret material %q: %s", raw, body)
		}
	}
	if _, err := integrations.GetOAuthAccount(t.Context(), account.ID); err != nil {
		t.Fatalf("account was deleted despite references: %v", err)
	}

	if err := integrations.DeleteDriveDestination(t.Context(), destination.ID); err != nil {
		t.Fatal(err)
	}
	if err := profiles.DeleteProfile(t.Context(), store.ProfileYouTubeOutput, output.ID); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStreamYouTubeRuntime(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-accounts/"+account.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("account delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
	if _, err := integrations.GetOAuthAccount(t.Context(), account.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected account deletion, err=%v", err)
	}
}
