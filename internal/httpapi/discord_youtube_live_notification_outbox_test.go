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

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

type scriptedYouTubeLifecycleClient struct {
	*fakeYouTubeLiveClient
	statuses          []string
	calls             int
	transitionCalls   int
	transitionRequest ytlive.BroadcastTransitionRequest
	transitionErr     error
}

func TestDiscordYouTubeLiveNotificationConfirmedRetryableIncludesMissingConfig(t *testing.T) {
	if !discordYouTubeLiveNotificationConfirmedRetryable(servicecall.DispatchResult{
		StatusCode: http.StatusConflict,
		Code:       "discord_config_not_found",
	}) {
		t.Fatal("a missing live runtime Discord config must be retried before suppressing the notification")
	}
}

func (f *scriptedYouTubeLifecycleClient) BroadcastLifecycle(_ context.Context, _ ytlive.BroadcastLifecycleRequest) (string, error) {
	index := f.calls
	f.calls++
	if len(f.statuses) == 0 {
		return "", nil
	}
	if index >= len(f.statuses) {
		index = len(f.statuses) - 1
	}
	return f.statuses[index], nil
}

func (f *scriptedYouTubeLifecycleClient) TransitionBroadcastLive(_ context.Context, req ytlive.BroadcastTransitionRequest) error {
	f.transitionCalls++
	f.transitionRequest = req
	return f.transitionErr
}

func TestDiscordYouTubeLiveNotificationReconcilesLiveStartingBeforeDispatch(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client", ClientSecret: "youtube-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &scriptedYouTubeLifecycleClient{
		fakeYouTubeLiveClient: &fakeYouTubeLiveClient{},
		statuses:              []string{"livestarting", "live"},
	}
	server := &Server{integrations: integrations, youtubeLive: youtubeClient}
	lifecycle, ready, terminal, code := server.discordYouTubeLiveNotificationLifecycle(t.Context(), store.DiscordYouTubeLiveNotification{
		YouTubeMode:           "live_api",
		YouTubeOAuthAccountID: account.ID,
		YouTubeBroadcastID:    "broadcast-01",
	})
	if lifecycle != "live" || !ready || terminal || code != "" {
		t.Fatalf("liveStarting reconciliation result = lifecycle=%q ready=%v terminal=%v code=%q", lifecycle, ready, terminal, code)
	}
	if youtubeClient.transitionCalls != 1 || youtubeClient.calls != 2 || youtubeClient.transitionRequest.BroadcastID != "broadcast-01" {
		t.Fatalf("liveStarting reconciliation did not transition then re-read exactly once: %#v", youtubeClient)
	}
}

func TestDiscordYouTubeLiveNotificationDoesNotRepeatUnknownLiveTransition(t *testing.T) {
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client", ClientSecret: "youtube-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeClient := &scriptedYouTubeLifecycleClient{
		fakeYouTubeLiveClient: &fakeYouTubeLiveClient{},
		statuses:              []string{"livestarting"},
		transitionErr:         errors.New("simulated response loss"),
	}
	server := &Server{integrations: integrations, youtubeLive: youtubeClient}
	notification := store.DiscordYouTubeLiveNotification{
		YouTubeMode:           "live_api",
		YouTubeOAuthAccountID: account.ID,
		YouTubeBroadcastID:    "broadcast-01",
	}
	lifecycle, ready, terminal, code := server.discordYouTubeLiveNotificationLifecycle(t.Context(), notification)
	if lifecycle != "livestarting" || ready || terminal || code != discordYouTubeLiveTransitionOutcomeUnknownCode {
		t.Fatalf("unknown transition result = lifecycle=%q ready=%v terminal=%v code=%q", lifecycle, ready, terminal, code)
	}
	notification.LastError = code
	_, ready, terminal, code = server.discordYouTubeLiveNotificationLifecycle(t.Context(), notification)
	if ready || terminal || code != discordYouTubeLiveTransitionOutcomeUnknownCode {
		t.Fatalf("unknown transition poll result = ready=%v terminal=%v code=%q", ready, terminal, code)
	}
	if youtubeClient.transitionCalls != 1 {
		t.Fatalf("uncertain live transition was repeated: calls=%d", youtubeClient.transitionCalls)
	}
}

func TestDiscordYouTubeLiveNotificationWaitsForTestingThenDispatchesOnce(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "lifecycle notification")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client", ClientSecret: "youtube-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh", Scopes: []string{"https://www.googleapis.com/auth/youtube"}})
	if err != nil {
		t.Fatal(err)
	}
	_, notification, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", store.DiscordYouTubeLiveNotification{
		WatchURL: "https://www.youtube.com/watch?v=lifecycle-test", DiscordServiceID: "discord_bot-01", DiscordTextChannelID: "text-01",
		YouTubeMode: "live_api", YouTubeOAuthAccountID: account.ID, YouTubeBroadcastID: "broadcast-01",
	})
	if err != nil || !transitioned {
		t.Fatalf("enqueue live-api notification: transitioned=%v err=%v", transitioned, err)
	}
	dispatcher := &notificationFakeDispatcher{}
	youtubeClient := &scriptedYouTubeLifecycleClient{fakeYouTubeLiveClient: &fakeYouTubeLiveClient{}, statuses: []string{"testing", "live"}}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithIntegrationStore(integrations), WithYouTubeLiveClient(youtubeClient), WithServiceDispatcher(dispatcher))

	result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25)
	if err != nil || result["claimed"] != 1 || result["retry_scheduled"] != 1 || dispatcher.notifyCalls != 0 {
		t.Fatalf("testing lifecycle must stay pending: result=%#v calls=%d err=%v", result, dispatcher.notifyCalls, err)
	}
	pending, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID != notification.ID || pending.State != store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || pending.LifecycleStatus != "testing" || pending.NextAttemptAt == nil {
		t.Fatalf("testing lifecycle was not durably polled: %#v", pending)
	}

	claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), pending.NextAttemptAt.Add(time.Second), 2*time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim after fixed lifecycle poll: claims=%#v err=%v", claims, err)
	}
	if outcome := handler.dispatchClaimedDiscordYouTubeLiveNotification(t.Context(), streams, claims[0]); outcome != "delivered" {
		t.Fatalf("live lifecycle dispatch outcome=%q", outcome)
	}
	delivered, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.State != store.DiscordYouTubeLiveNotificationStateDelivered || delivered.LifecycleStatus != "live" || dispatcher.notifyCalls != 1 || youtubeClient.calls != 2 {
		t.Fatalf("testing-to-live must send one receipt-backed notification: notification=%#v calls=%d lifecycle_calls=%d", delivered, dispatcher.notifyCalls, youtubeClient.calls)
	}
	if _, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25); err != nil || dispatcher.notifyCalls != 1 {
		t.Fatalf("delivered notification was re-dispatched: calls=%d err=%v", dispatcher.notifyCalls, err)
	}
}

func TestPrivateYouTubeLiveAPIStartQueuesWatchURLUntilProviderIsLive(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "private live api notification")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")

	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google", Name: "YouTube", Enabled: true, ClientID: "youtube-client", ClientSecret: "youtube-secret", RedirectURI: "https://control.example.test/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID: provider.ID, ProviderType: "google", AccountLabel: "youtube", RefreshToken: "youtube-refresh", Scopes: []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}

	profiles := store.NewMemoryProfileStore()
	discord, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "private notification discord", map[string]any{
		"service_id":           "discord_bot-01",
		"bot_token_configured": true,
		"guild_id":             "guild-private",
		"voice_channel_id":     "voice-private",
		"text_channel_id":      "text-private",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "private live api output", map[string]any{
		"mode":              "live_api",
		"oauth_account_id":  account.ID,
		"privacy_status":    "private",
		"enable_auto_start": true,
	})
	if err != nil {
		t.Fatal(err)
	}

	dispatcher := &notificationFakeDispatcher{}
	youtubeClient := &scriptedYouTubeLifecycleClient{
		fakeYouTubeLiveClient: &fakeYouTubeLiveClient{prepared: ytlive.PreparedOutput{
			RTMPURL: "rtmps://youtube.example.test/live2", StreamKey: "private-runtime-key", BroadcastID: "private-broadcast", LiveStreamID: "private-live-stream",
		}},
		statuses: []string{"testing", "live"},
	}
	handler := NewServer(
		streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithSecretStore(store.NewMemorySecretStore()),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(youtubeClient),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	body := fmt.Sprintf(`{"discord_config_id":%q,"youtube_output_id":%q}`, discord.ID, youtube.ID)
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("private live-api start status=%d body=%s", res.Code, res.Body.String())
	}
	if youtubeClient.prepareRequest.PrivacyStatus != "private" || !youtubeClient.prepareRequest.EnableAutoStart {
		t.Fatalf("private auto-start settings were not sent to YouTube: %#v", youtubeClient.prepareRequest)
	}
	if dispatcher.notifyCalls != 0 {
		t.Fatalf("start must enqueue instead of notifying before provider live: calls=%d", dispatcher.notifyCalls)
	}

	queued, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive ||
		queued.WatchURL != "https://www.youtube.com/watch?v=private-broadcast" ||
		queued.DiscordTextChannelID != "1002" ||
		queued.YouTubeMode != "live_api" ||
		queued.YouTubeOAuthAccountID != account.ID ||
		queued.YouTubeBroadcastID != "private-broadcast" {
		t.Fatalf("private live-api start did not durably enqueue its watch URL: %#v", queued)
	}

	result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 1)
	if err != nil || result["claimed"] != 1 || result["retry_scheduled"] != 1 || dispatcher.notifyCalls != 0 {
		t.Fatalf("testing lifecycle must keep private notification pending: result=%#v calls=%d err=%v", result, dispatcher.notifyCalls, err)
	}
	pending, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != store.DiscordYouTubeLiveNotificationStateAwaitingYouTubeLive || pending.LifecycleStatus != "testing" || pending.NextAttemptAt == nil {
		t.Fatalf("private notification was suppressed before provider live: %#v", pending)
	}

	claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), pending.NextAttemptAt.Add(time.Second), 2*time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim private notification after lifecycle poll: claims=%#v err=%v", claims, err)
	}
	if outcome := handler.dispatchClaimedDiscordYouTubeLiveNotification(t.Context(), streams, claims[0]); outcome != "delivered" {
		t.Fatalf("private live lifecycle dispatch outcome=%q", outcome)
	}
	delivered, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.State != store.DiscordYouTubeLiveNotificationStateDelivered || delivered.LifecycleStatus != "live" ||
		dispatcher.notifyCalls != 1 || dispatcher.notifiedURL != "https://www.youtube.com/watch?v=private-broadcast" {
		t.Fatalf("private YouTube URL was not delivered after provider live: notification=%#v calls=%d url=%q", delivered, dispatcher.notifyCalls, dispatcher.notifiedURL)
	}
}

func TestDiscordYouTubeLiveNotificationAcceptsLegacyStreamKeyMode(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy relay notification")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "discord_bot")
	if _, _, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", store.DiscordYouTubeLiveNotification{
		WatchURL: "https://www.youtube.com/watch?v=legacy-relay", DiscordServiceID: "discord_bot-01", DiscordTextChannelID: "text-01", YouTubeMode: "legacy_stream_key",
	}); err != nil || !transitioned {
		t.Fatalf("enqueue legacy notification: transitioned=%v err=%v", transitioned, err)
	}
	dispatcher := &notificationFakeDispatcher{}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 1)
	if err != nil || result["claimed"] != 1 || result["delivered"] != 1 || dispatcher.notifyCalls != 1 {
		t.Fatalf("legacy stream-key notification was not delivered: result=%#v calls=%d err=%v", result, dispatcher.notifyCalls, err)
	}
}

func TestDiscordYouTubeLiveNotificationBareRateLimitSchedulesOneDurableRetry(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS", "127.0.0.1")
	botCalls := 0
	botPath := ""
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botCalls++
		if botPath != "" && r.URL.Path != botPath {
			t.Fatalf("unexpected Bot path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer bot.Close()

	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord_bot-01", ServiceType: "discord_bot", ServiceName: "Discord", PublicURL: bot.URL, Version: "test"})
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "rate limit notification")
	if err != nil {
		t.Fatal(err)
	}
	botPath = "/streams/" + stream.ID + "/notifications/youtube-live"
	if _, err := auth.AssignServiceToStream(t.Context(), "discord_bot-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	_, _, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", store.DiscordYouTubeLiveNotification{
		WatchURL: "https://www.youtube.com/watch?v=rate-limit", DiscordServiceID: "discord_bot-01", DiscordTextChannelID: "text-01", YouTubeMode: "stream_key",
	})
	if err != nil || !transitioned {
		t.Fatalf("enqueue rate-limit notification: transitioned=%v err=%v", transitioned, err)
	}

	client := servicecall.Client{Config: servicecall.Config{Timeout: time.Second, URLPolicy: netpolicy.ServiceURLPolicy{AllowedHosts: map[string]struct{}{"127.0.0.1": {}}}}, RuntimeTokenResolver: func(store.RegisteredService) (string, error) { return "service-token", nil }}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithServiceDispatcher(client))
	result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25)
	if err != nil || result["claimed"] != 1 || result["retry_scheduled"] != 1 || botCalls != 1 {
		t.Fatalf("bare 429 must schedule exactly one retry: result=%#v calls=%d err=%v", result, botCalls, err)
	}
	notification, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if notification.State != store.DiscordYouTubeLiveNotificationStateDispatchPending || notification.LastError != "discord_rate_limited" || notification.DispatchAttemptCount != 1 || notification.NextAttemptAt == nil {
		t.Fatalf("bare 429 was not persisted as a safe retry: %#v", notification)
	}
	if _, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25); err != nil || botCalls != 1 {
		t.Fatalf("rate-limit retry ran before durable backoff elapsed: calls=%d err=%v", botCalls, err)
	}
}

func TestDiscordYouTubeLiveNotificationProcessesOnlyOneClaimPerScan(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	streamIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		stream, err := streams.CreateStream(t.Context(), "one-at-a-time")
		if err != nil {
			t.Fatal(err)
		}
		streamIDs = append(streamIDs, stream.ID)
		serviceID := fmt.Sprintf("discord_bot-%02d", i+1)
		registerServiceInstance(t, auth, serviceID, "discord_bot")
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
		if _, _, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", store.DiscordYouTubeLiveNotification{
			WatchURL: "https://www.youtube.com/watch?v=one-at-a-time", DiscordServiceID: serviceID, DiscordTextChannelID: "text-01", YouTubeMode: "stream_key",
		}); err != nil || !transitioned {
			t.Fatalf("enqueue %d: transitioned=%v err=%v", i, transitioned, err)
		}
	}
	dispatcher := &notificationFakeDispatcher{}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	for scan := 1; scan <= 3; scan++ {
		result, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25)
		if err != nil || result["claimed"] != 1 || result["delivered"] != 1 || dispatcher.notifyCalls != scan {
			notification, getErr := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), streamIDs[0])
			t.Fatalf("scan %d must process one protected lease: result=%#v calls=%d err=%v latest=%#v get_err=%v", scan, result, dispatcher.notifyCalls, err, notification, getErr)
		}
	}
}

func TestDiscordYouTubeLiveNotificationRecoveryRequiresAttestationAndCreatesNewEvent(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "recovery notification")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "discord_bot")
	_, notification, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", store.DiscordYouTubeLiveNotification{
		WatchURL: "https://www.youtube.com/watch?v=recovery", DiscordServiceID: "discord_bot-01", DiscordTextChannelID: "text-01", YouTubeMode: "stream_key",
	})
	if err != nil || !transitioned {
		t.Fatalf("enqueue recovery notification: transitioned=%v err=%v", transitioned, err)
	}
	claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), time.Now().UTC(), 2*time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim notification: claims=%#v err=%v", claims, err)
	}
	if _, err := streams.BeginDiscordYouTubeLiveNotificationBotDispatch(t.Context(), notification.ID, claims[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := streams.MarkDiscordYouTubeLiveNotificationDeliveryUnknown(t.Context(), notification.ID, claims[0].LeaseToken, "legacy_unverified", "discord_delivery_transport_unknown"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	get := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/youtube-live-notification", nil)
	get.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, get)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), `"state":"delivery_unknown"`) || strings.Contains(getRes.Body.String(), notification.EventID) {
		t.Fatalf("safe notification status response = %d %s", getRes.Code, getRes.Body.String())
	}

	missingAck := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube-live-notifications/"+notification.ID+"/recover", bytes.NewBufferString(`{}`))
	missingAck.AddCookie(cookie)
	missingAck.Header.Set("X-CSRF-Token", csrf)
	missingAckRes := httptest.NewRecorder()
	handler.ServeHTTP(missingAckRes, missingAck)
	if missingAckRes.Code != http.StatusBadRequest || !strings.Contains(missingAckRes.Body.String(), "duplicate_risk_acknowledgement_required") {
		t.Fatalf("missing attestation response = %d %s", missingAckRes.Code, missingAckRes.Body.String())
	}

	recoverRequest := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube-live-notifications/"+notification.ID+"/recover", bytes.NewBufferString(`{"acknowledge_possible_duplicate":true}`))
	recoverRequest.AddCookie(cookie)
	recoverRequest.Header.Set("X-CSRF-Token", csrf)
	recoverRes := httptest.NewRecorder()
	handler.ServeHTTP(recoverRes, recoverRequest)
	if recoverRes.Code != http.StatusAccepted || strings.Contains(recoverRes.Body.String(), notification.EventID) {
		t.Fatalf("recovery response = %d %s", recoverRes.Code, recoverRes.Body.String())
	}
	var body struct {
		Notification struct {
			ID           string `json:"id"`
			RecoveryOfID string `json:"recovery_of_id"`
			State        string `json:"state"`
		} `json:"notification"`
	}
	if err := json.NewDecoder(recoverRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Notification.ID == "" || body.Notification.ID == notification.ID || body.Notification.RecoveryOfID != notification.ID || body.Notification.State != store.DiscordYouTubeLiveNotificationStateDispatchPending {
		t.Fatalf("recovery did not create a distinct pending event: %#v", body.Notification)
	}

	repeat := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/youtube-live-notifications/"+notification.ID+"/recover", bytes.NewBufferString(`{"acknowledge_possible_duplicate":true}`))
	repeat.AddCookie(cookie)
	repeat.Header.Set("X-CSRF-Token", csrf)
	repeatRes := httptest.NewRecorder()
	handler.ServeHTTP(repeatRes, repeat)
	if repeatRes.Code != http.StatusConflict || !strings.Contains(repeatRes.Body.String(), "discord_youtube_notification_recovery_already_requested") {
		t.Fatalf("repeat recovery response = %d %s", repeatRes.Code, repeatRes.Body.String())
	}
}
