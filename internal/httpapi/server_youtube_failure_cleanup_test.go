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
)

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
