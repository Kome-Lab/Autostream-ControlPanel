package httpapi

import (
	"bytes"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
