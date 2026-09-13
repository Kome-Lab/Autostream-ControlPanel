package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
