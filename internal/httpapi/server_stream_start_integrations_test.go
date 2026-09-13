package httpapi

import (
	"bytes"
	"fmt"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartStreamQueuesDiscordNotificationOnlyAfterSuccessfulDispatch(t *testing.T) {
	tests := []struct {
		name                      string
		watchURL                  string
		failStart                 bool
		notificationResult        servicecall.DispatchResult
		snapshotTextChannel       string
		wantDispatchTextChannel   string
		wantHTTPStatus            int
		wantStreamStatus          string
		wantNotifyCalls           int
		wantResponseCode          string
		wantNotifiedURL           string
		wantNotificationState     string
		wantNotificationLastError string
	}{
		{name: "notification sent", watchURL: "https://youtu.be/video_01", snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_01", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "profile text channel fallback sends notification", watchURL: "https://youtu.be/video_profile_fallback", wantDispatchTextChannel: "3002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_profile_fallback", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "saved stream text channel override keeps priority", watchURL: "https://youtu.be/video_stream_override", snapshotTextChannel: "9002", wantDispatchTextChannel: "9002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_stream_override", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "ambiguous notification failure keeps live stream and fences recovery", watchURL: "https://www.youtube.com/watch?v=video_02", notificationResult: servicecall.DispatchResult{StatusCode: http.StatusBadGateway, Code: "discord_api_unavailable", Error: "service returned status 502"}, snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_02", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDeliveryUnknown, wantNotificationLastError: "discord_api_unavailable"},
		{name: "dispatch failure suppresses notification", watchURL: "https://www.youtube.com/watch?v=video_03", failStart: true, snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusBadGateway, wantStreamStatus: "failed", wantNotifyCalls: 0, wantResponseCode: "service_dispatch_failed"},
		{name: "profile text channel fallback requires watch URL", wantHTTPStatus: http.StatusConflict, wantStreamStatus: "created", wantNotifyCalls: 0, wantResponseCode: "youtube_output_invalid_config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "notification stream")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
			secrets := store.NewMemorySecretStore()
			if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_notification", "runtime-secret-stream-key"); err != nil {
				t.Fatal(err)
			}
			profiles := store.NewMemoryProfileStore()
			discord, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "notification discord", map[string]any{
				"service_id":           "discord_bot-01",
				"bot_token_configured": true,
				"guild_id":             "3001",
				"voice_channel_id":     "3003",
				"text_channel_id":      "3002",
			})
			if err != nil {
				t.Fatal(err)
			}
			youtubeConfig := map[string]any{
				"mode":                   "stream_key",
				"rtmp_url":               "rtmps://youtube.example.com/live2",
				"stream_key_secret_name": "youtube_stream_key_notification",
			}
			if tt.watchURL != "" {
				youtubeConfig["watch_url"] = tt.watchURL
			}
			youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "notification-output", youtubeConfig)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := &notificationFakeDispatcher{fakeServiceDispatcher: fakeServiceDispatcher{failStart: tt.failStart}, result: tt.notificationResult}
			options := []ServerOption{WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher)}
			if tt.snapshotTextChannel != "" {
				options = append(options, withManualDiscordTargetForTest(t, streams, stream.ID, "2001", tt.snapshotTextChannel, "2003"))
			}
			handler := NewServer(streams, options...)
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			body := fmt.Sprintf(`{"discord_config_id":%q,"youtube_output_id":%q}`, discord.ID, youtube.ID)
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantHTTPStatus {
				t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
			}
			updated, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != tt.wantStreamStatus || dispatcher.notifyCalls != 0 {
				t.Fatalf("start must only enqueue notification: stream=%s notify_calls=%d body=%s", updated.Status, dispatcher.notifyCalls, res.Body.String())
			}
			if tt.wantResponseCode != "" && !strings.Contains(res.Body.String(), tt.wantResponseCode) {
				t.Fatalf("expected response code %q: %s", tt.wantResponseCode, res.Body.String())
			}
			if tt.wantDispatchTextChannel != "" && dispatcher.startRequest.DiscordTextChannelID != tt.wantDispatchTextChannel {
				t.Fatalf("discord text channel was not resolved for dispatch: got=%q want=%q request=%#v", dispatcher.startRequest.DiscordTextChannelID, tt.wantDispatchTextChannel, dispatcher.startRequest)
			}
			if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "youtube.com/watch") || strings.Contains(toJSONForTest(t, auth.AuditEvents()), "youtu.be/") {
				t.Fatalf("YouTube watch URL leaked into audit events: %#v", auth.AuditEvents())
			}
			if tt.wantNotificationState != "" {
				queued, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				if queued.State != store.DiscordYouTubeLiveNotificationStateDispatchPending || queued.LifecycleStatus != "legacy_unverified" {
					t.Fatalf("start did not enqueue legacy notification: %#v", queued)
				}
				if _, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25); err != nil {
					t.Fatal(err)
				}
				notification, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				if notification.State != tt.wantNotificationState || notification.LastError != tt.wantNotificationLastError {
					t.Fatalf("notification state = %#v, want state=%q last_error=%q", notification, tt.wantNotificationState, tt.wantNotificationLastError)
				}
			}
			if dispatcher.notifyCalls != tt.wantNotifyCalls {
				t.Fatalf("notification calls = %d, want %d", dispatcher.notifyCalls, tt.wantNotifyCalls)
			}
			if dispatcher.notifyCalls == 1 {
				if dispatcher.notifiedStream.Status != "live" || dispatcher.notifiedURL != tt.wantNotifiedURL {
					t.Fatalf("unexpected notification request: stream=%#v url=%q", dispatcher.notifiedStream, dispatcher.notifiedURL)
				}
				if !strings.HasPrefix(dispatcher.notifiedEventID, "youtube-live-") {
					t.Fatalf("unexpected notification event id: %q", dispatcher.notifiedEventID)
				}
			}
		})
	}
}

func TestStartStreamResolvesArchiveDriveDestinationForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
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
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("drive folder ID leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.ArchiveConfig["folder_id"] == "raw-drive-folder-id" {
		t.Fatalf("archive config leaked raw folder ID to dispatch: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	secretName, _ := dispatcher.startRequest.ArchiveConfig["folder_id_secret_name"].(string)
	if secretName != driveDestinationFolderIDSecretName(destination.ID) || dispatcher.startRequest.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("archive config did not include scoped secret reference: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	if dispatcher.startRequest.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || dispatcher.startRequest.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("archive config did not include OAuth secret references: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	for _, leaked := range []string{"service_account_json", "service_account_credentials_secret_name", "client_secret", "refresh_token", "folder_id"} {
		if _, ok := dispatcher.startRequest.ArchiveConfig[leaked]; ok {
			t.Fatalf("archive config leaked raw or unsupported secret field %q: %#v", leaked, dispatcher.startRequest.ArchiveConfig)
		}
	}
}

func TestStartStreamResolvesOAuthDriveDestinationForDispatchWithoutResponseLeak(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
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
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-google-refresh-token", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("raw OAuth/Drive secret leaked in response: %s", res.Body.String())
		}
	}
	cfg := dispatcher.startRequest.ArchiveConfig
	if cfg["auth_mode"] != "oauth2" || cfg["client_secret"] == "raw-google-client-secret" || cfg["refresh_token"] == "raw-google-refresh-token" || cfg["folder_id"] == "raw-oauth-drive-folder-id" {
		t.Fatalf("OAuth archive config leaked raw secret values to dispatch: %#v", cfg)
	}
	if cfg["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("OAuth archive config did not include scoped secret references: %#v", cfg)
	}
}
