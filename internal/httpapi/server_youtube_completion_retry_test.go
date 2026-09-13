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
