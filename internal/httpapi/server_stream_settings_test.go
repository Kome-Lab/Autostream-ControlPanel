package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestCreateStreamRejectsBlankName(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"   "}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCreateStreamPersistsSchedule(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"scheduled stream","scheduled_start_at":"2026-07-10T01:00:00Z","scheduled_end_at":"2026-07-10T02:00:00Z","visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ScheduledStartAt == nil || created.ScheduledStartAt.Format(time.RFC3339) != "2026-07-10T01:00:00Z" {
		t.Fatalf("scheduled start was not persisted: %#v", created.ScheduledStartAt)
	}
	if created.ScheduledEndAt == nil || created.ScheduledEndAt.Format(time.RFC3339) != "2026-07-10T02:00:00Z" {
		t.Fatalf("scheduled end was not persisted: %#v", created.ScheduledEndAt)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/streams", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var listed []store.Stream
	if err := json.NewDecoder(listRes.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ScheduledStartAt == nil || listed[0].ScheduledEndAt == nil {
		t.Fatalf("list did not return schedule: %#v", listed)
	}
}

func TestStreamScheduledStartIsOptionalClearableAndHonored(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"immediate stream","visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ScheduledStartAt != nil || !youtubeLiveAPIScheduledStart(created, nil).IsZero() {
		t.Fatalf("stream without an explicit schedule must use the immediate YouTube path: %#v", created)
	}

	future := "2030-07-10T01:00:00Z"
	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", bytes.NewBufferString(`{"scheduled_start_at":"`+future+`"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("schedule update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	var scheduled store.Stream
	if err := json.NewDecoder(updateRes.Body).Decode(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled.ScheduledStartAt == nil || scheduled.ScheduledStartAt.Format(time.RFC3339) != future || !youtubeLiveAPIScheduledStart(scheduled, nil).Equal(*scheduled.ScheduledStartAt) {
		t.Fatalf("explicit scheduled start was not retained for YouTube: %#v", scheduled)
	}

	if _, err := streams.UpdateStreamStatus(t.Context(), created.ID, "live"); err != nil {
		t.Fatal(err)
	}
	activeClearReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", bytes.NewBufferString(`{"scheduled_start_at":""}`))
	activeClearReq.AddCookie(cookie)
	activeClearReq.Header.Set("X-CSRF-Token", csrf)
	activeClearRes := httptest.NewRecorder()
	handler.ServeHTTP(activeClearRes, activeClearReq)
	if activeClearRes.Code != http.StatusConflict || !strings.Contains(activeClearRes.Body.String(), "stream_status_not_editable") {
		t.Fatalf("active stream schedule clear must remain blocked: status=%d body=%s", activeClearRes.Code, activeClearRes.Body.String())
	}
	stillScheduled, err := streams.GetStream(t.Context(), created.ID)
	if err != nil || stillScheduled.ScheduledStartAt == nil || stillScheduled.ScheduledStartAt.Format(time.RFC3339) != future {
		t.Fatalf("active stream schedule changed after rejected clear: stream=%#v err=%v", stillScheduled, err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), created.ID, "stopped"); err != nil {
		t.Fatal(err)
	}

	clearReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", bytes.NewBufferString(`{"scheduled_start_at":""}`))
	clearReq.AddCookie(cookie)
	clearReq.Header.Set("X-CSRF-Token", csrf)
	clearRes := httptest.NewRecorder()
	handler.ServeHTTP(clearRes, clearReq)
	if clearRes.Code != http.StatusOK {
		t.Fatalf("schedule clear status=%d body=%s", clearRes.Code, clearRes.Body.String())
	}
	var cleared store.Stream
	if err := json.NewDecoder(clearRes.Body).Decode(&cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.ScheduledStartAt != nil || strings.Contains(clearRes.Body.String(), `"scheduled_start_at"`) {
		t.Fatalf("cleared schedule must be omitted from the response: stream=%#v body=%s", cleared, clearRes.Body.String())
	}
	stored, err := streams.GetStream(t.Context(), created.ID)
	if err != nil || stored.ScheduledStartAt != nil {
		t.Fatalf("cleared schedule was not persisted as NULL: stream=%#v err=%v", stored, err)
	}
}

func TestCreateStreamRejectsInvalidSchedule(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{
			name: "invalid time",
			body: `{"name":"bad schedule","scheduled_start_at":"2026/07/10 10:00"}`,
			code: "schedule_time_invalid",
		},
		{
			name: "end before start",
			body: `{"name":"bad schedule","scheduled_start_at":"2026-07-10T02:00:00Z","scheduled_end_at":"2026-07-10T01:00:00Z"}`,
			code: "schedule_end_before_start",
		},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s status = %d body = %s", tc.name, res.Code, res.Body.String())
		}
	}
}

func TestStreamSettingsCanBeSavedAndReturned(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	discordOne, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{"guild_id": "guild-01", "voice_channel_id": "voice-01"})
	if err != nil {
		t.Fatal(err)
	}
	discordTwo, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord two", map[string]any{"guild_id": "guild-02", "voice_channel_id": "voice-02"})
	if err != nil {
		t.Fatal(err)
	}
	youtubeOutput, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "youtube output", map[string]any{"mode": "rtmp", "rtmp_url": "rtmps://a.rtmps.youtube.com/live2"})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive profile", map[string]any{"format": "mp4"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	createReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"configured stream","discord_config_id":"`+discordOne.ID+`","auto_start_trigger":"discord_voice_join","youtube_output_id":"`+youtubeOutput.ID+`","visual_settings":{"expected_revision":0,"discord_target":{"mode":"manual","guild_id":"1001","voice_channel_id":"1002","text_channel_id":"1003"}}}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.DiscordConfigID != discordOne.ID || created.AutoStartTrigger != "discord_voice_join" || created.YouTubeOutputID != youtubeOutput.ID {
		t.Fatalf("create did not persist stream settings: %#v", created)
	}
	visualReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/visual-settings", bytes.NewBufferString(`{"expected_revision":1,"discord_target":{"mode":"manual","guild_id":"2001","voice_channel_id":"2002","text_channel_id":"2003"}}`))
	visualReq.AddCookie(cookie)
	visualReq.Header.Set("X-CSRF-Token", csrf)
	visualRes := httptest.NewRecorder()
	handler.ServeHTTP(visualRes, visualReq)
	if visualRes.Code != http.StatusOK {
		t.Fatalf("update visual settings status = %d body = %s", visualRes.Code, visualRes.Body.String())
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", bytes.NewBufferString(`{"discord_config_id":"`+discordTwo.ID+`","auto_start_trigger":"discord_voice_join","archive_profile_id":"`+archiveProfile.ID+`","encoder_input_url":"srt://input.example.com:9000"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update settings status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	var updated store.Stream
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.DiscordConfigID != discordTwo.ID || updated.AutoStartTrigger != "discord_voice_join" || updated.ArchiveProfileID != archiveProfile.ID || updated.EncoderInputURL == "" || updated.YouTubeOutputID != "" {
		t.Fatalf("unexpected updated stream settings: %#v", updated)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID, nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), discordTwo.ID) || strings.Contains(getRes.Body.String(), "2001") || !strings.Contains(getRes.Body.String(), "discord_voice_join") || !strings.Contains(getRes.Body.String(), archiveProfile.ID) {
		t.Fatalf("get stream did not return settings: status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	visualGet := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID+"/visual-settings", nil)
	visualGet.AddCookie(cookie)
	visualGetRes := httptest.NewRecorder()
	handler.ServeHTTP(visualGetRes, visualGet)
	if visualGetRes.Code != http.StatusOK || !strings.Contains(visualGetRes.Body.String(), `"discord_guild_id":"2001"`) {
		t.Fatalf("get visual settings status=%d body=%s", visualGetRes.Code, visualGetRes.Body.String())
	}
}

func TestUpdateStreamSettingsAllowsInactiveStatusesAndRejectsActiveStatuses(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name       string
		status     string
		wantStatus int
		wantEdit   bool
	}{
		{name: "created", status: "created", wantStatus: http.StatusOK, wantEdit: true},
		{name: "draft", status: "draft", wantStatus: http.StatusOK, wantEdit: true},
		{name: "scheduled", status: "scheduled", wantStatus: http.StatusOK, wantEdit: true},
		{name: "ready", status: "ready", wantStatus: http.StatusOK, wantEdit: true},
		{name: "failed", status: "failed", wantStatus: http.StatusOK, wantEdit: true},
		{name: "completed", status: "completed", wantStatus: http.StatusOK, wantEdit: true},
		{name: "stopped", status: "stopped", wantStatus: http.StatusOK, wantEdit: true},
		{name: "starting", status: "starting", wantStatus: http.StatusConflict},
		{name: "live", status: "live", wantStatus: http.StatusConflict},
		{name: "stopping", status: "stopping", wantStatus: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := streams.CreateStream(t.Context(), "before "+tc.status+" edit")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, tc.status); err != nil {
				t.Fatal(err)
			}

			updatedName := "after " + tc.status + " edit"
			req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"name":"`+updatedName+`"}`))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != tc.wantStatus {
				t.Fatalf("update status=%d want=%d body=%s", res.Code, tc.wantStatus, res.Body.String())
			}
			stored, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantEdit {
				if stored.Name != updatedName || stored.Status != tc.status {
					t.Fatalf("inactive stream update did not persist name/status: %#v", stored)
				}
				return
			}
			if !strings.Contains(res.Body.String(), `"code":"stream_status_not_editable"`) {
				t.Fatalf("active stream rejection code missing: %s", res.Body.String())
			}
			if stored.Name != stream.Name || stored.Status != tc.status {
				t.Fatalf("active stream was modified: %#v", stored)
			}
		})
	}
}

func TestCreateStreamRejectsInvalidAutoStartSettings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord", map[string]any{"guild_id": "guild-01", "voice_channel_id": "voice-01"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown trigger",
			body: `{"name":"bad trigger","auto_start_trigger":"vc_join","visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`,
			code: "auto_start_trigger_invalid",
		},
		{
			name: "missing discord voice target",
			body: `{"name":"missing discord","discord_config_id":"` + discordProfile.ID + `","auto_start_trigger":"discord_voice_join","visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`,
			code: "invalid_visual_settings",
		},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s status = %d body = %s", tc.name, res.Code, res.Body.String())
		}
	}
}

func TestCreateStreamMaterializesDirectArchiveSettings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Direct",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Direct Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"direct archive stream","archive_oauth_account_id":"`+account.ID+`","archive_folder_id":"raw-drive-folder-id","archive_shared_drive":true,"archive_shared_drive_id":"shared-drive-01","archive_retention_days":45,"visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") || strings.Contains(res.Body.String(), "raw-google-refresh-token") || strings.Contains(res.Body.String(), "raw-google-client-secret") {
		t.Fatalf("direct archive response leaked raw secret: %s", res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ArchiveProfileID == "" || created.ArchiveDriveDestinationID == "" || created.ArchiveOAuthAccountID != account.ID || !created.ArchiveFolderIDConfigured || !created.ArchiveSharedDrive || created.ArchiveSharedDriveID != "shared-drive-01" {
		t.Fatalf("direct archive settings were not persisted on stream: %#v", created)
	}
	if !strings.HasPrefix(created.ArchiveFileName, "direct archive stream-") || !strings.HasSuffix(created.ArchiveFileName, ".mp4") {
		t.Fatalf("default archive file name was not generated from stream name/date: %#v", created)
	}
	destination, err := integrations.GetDriveDestinationForDispatch(t.Context(), created.ArchiveDriveDestinationID)
	if err != nil {
		t.Fatal(err)
	}
	if destination.AuthMode != "oauth2" || destination.OAuthAccountID != account.ID || destination.FolderID != "raw-drive-folder-id" || !destination.SharedDrive {
		t.Fatalf("drive destination was not materialized for dispatch: %#v", destination)
	}
	profile, err := profiles.GetProfile(t.Context(), store.ProfileArchive, created.ArchiveProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["drive_destination_id"] != destination.ID || profile.Config["stream_archive_direct"] != true || profile.Config["archive_file_name"] != created.ArchiveFileName || profile.Config["shared_drive_id"] != "shared-drive-01" || profile.Config["retention_days"] != 45 {
		t.Fatalf("archive profile was not materialized from stream settings: %#v", profile.Config)
	}
}

func TestUpdateStreamSettingsPreservesOmittedLegacyArchiveFields(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy archive stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "legacy archive profile", map[string]any{"format": "mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		ArchiveProfileID:          archiveProfile.ID,
		ArchiveDriveDestinationID: "legacy-destination",
		ArchiveOAuthAccountID:     "legacy-oauth-account",
		ArchiveSharedDrive:        true,
		ArchiveSharedDriveID:      "legacy-shared-drive",
		ArchiveFileName:           "legacy-recording.mp4",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	update := func(body string) store.Stream {
		req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("update status = %d body = %s", res.Code, res.Body.String())
		}
		var updated store.Stream
		if err := json.NewDecoder(res.Body).Decode(&updated); err != nil {
			t.Fatal(err)
		}
		return updated
	}

	updated := update(`{"name":"renamed legacy archive stream","archive_profile_id":"` + archiveProfile.ID + `"}`)
	if updated.ArchiveDriveDestinationID != "legacy-destination" || updated.ArchiveOAuthAccountID != "legacy-oauth-account" || !updated.ArchiveSharedDrive || updated.ArchiveSharedDriveID != "legacy-shared-drive" || updated.ArchiveFileName != "legacy-recording.mp4" {
		t.Fatalf("standard settings edit erased omitted legacy archive fields: %#v", updated)
	}

	cleared := update(`{"name":"renamed legacy archive stream","archive_profile_id":"` + archiveProfile.ID + `","archive_oauth_account_id":"","archive_folder_id":"","archive_shared_drive":false,"archive_shared_drive_id":"","archive_file_name":""}`)
	if cleared.ArchiveDriveDestinationID != "" || cleared.ArchiveOAuthAccountID != "" || cleared.ArchiveFolderIDConfigured || cleared.ArchiveSharedDrive || cleared.ArchiveSharedDriveID != "" || cleared.ArchiveFileName != "" {
		t.Fatalf("explicit legacy archive field clear was not applied: %#v", cleared)
	}
}

func TestUpdateStreamSettingsRollsBackExplicitAssignmentClearWhenLaterAssignmentFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "before assignment rollback")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-old", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-fail", "worker")
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-old", stream.ID, "bootstrap", "primary"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(failOnStreamAssignmentRegistry{ServiceRegistryStore: auth, failServiceID: "worker-fail"}),
	)
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"name":"must not persist","encoder_service_id":"","worker_service_id":"worker-fail"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "assign_service_failed") {
		t.Fatalf("update status = %d body = %s", res.Code, res.Body.String())
	}

	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "before assignment rollback" {
		t.Fatalf("settings persisted despite failed assignment: %#v", updated)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceID != "encoder-old" || assignments[0].AssignmentRole != "primary" {
		t.Fatalf("stream assignments did not converge after failure: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	for serviceID, expectedStreamID := range map[string]string{
		"encoder-old": stream.ID,
		"worker-fail": "",
	} {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if service.CurrentStreamID != expectedStreamID {
			t.Fatalf("%s current stream = %q, want %q", serviceID, service.CurrentStreamID, expectedStreamID)
		}
	}
}

func TestUpdateStreamSettingsExplicitEmptyServiceIDClearsOnlyRequestedAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "before explicit assignment clear")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-clear", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-preserve", "worker")
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-clear", stream.ID, "bootstrap", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "worker-preserve", stream.ID, "bootstrap", "primary"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"name":"after explicit assignment clear","encoder_service_id":""}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", res.Code, res.Body.String())
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "after explicit assignment clear" {
		t.Fatalf("updated name = %q", updated.Name)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceID != "worker-preserve" || assignments[0].AssignmentRole != "primary" {
		t.Fatalf("explicit encoder clear changed unexpected assignments: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	for serviceID, expectedStreamID := range map[string]string{
		"encoder-clear":   "",
		"worker-preserve": stream.ID,
	} {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if service.CurrentStreamID != expectedStreamID {
			t.Fatalf("%s current stream = %q, want %q", serviceID, service.CurrentStreamID, expectedStreamID)
		}
	}
}

func TestCreateStreamMaterializesLocalRetentionArchiveProfileWithoutDrive(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"local retained stream","archive_retention_days":7,"visual_settings":{"expected_revision":0,"discord_target":{"mode":"inherit"}}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ArchiveProfileID == "" || created.ArchiveDriveDestinationID != "" || created.ArchiveOAuthAccountID != "" || created.ArchiveFolderIDConfigured {
		t.Fatalf("local retention archive settings were not persisted as expected: %#v", created)
	}
	profile, err := profiles.GetProfile(t.Context(), store.ProfileArchive, created.ArchiveProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["stream_archive_direct"] != true || profile.Config["retention_days"] != 7 {
		t.Fatalf("local retention archive profile was not materialized: %#v", profile.Config)
	}
	if _, ok := profile.Config["drive_destination_id"]; ok {
		t.Fatalf("local retention profile should not include Drive destination: %#v", profile.Config)
	}
}

func TestStreamSettingsRejectUnknownDiscordConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "configured stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	createReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"bad stream","discord_config_id":"missing-discord-config"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusBadRequest || !strings.Contains(createRes.Body.String(), "discord_config_not_found") {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	overrideOnlyCreateReq := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"bad override stream","discord_guild_id":"guild-without-config","discord_voice_channel_id":"voice-without-config"}`))
	overrideOnlyCreateReq.AddCookie(cookie)
	overrideOnlyCreateReq.Header.Set("X-CSRF-Token", csrf)
	overrideOnlyCreateRes := httptest.NewRecorder()
	handler.ServeHTTP(overrideOnlyCreateRes, overrideOnlyCreateReq)
	if overrideOnlyCreateRes.Code != http.StatusBadRequest || !strings.Contains(overrideOnlyCreateRes.Body.String(), "bad_request") {
		t.Fatalf("override-only create status=%d body=%s", overrideOnlyCreateRes.Code, overrideOnlyCreateRes.Body.String())
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"discord_config_id":"missing-discord-config"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusBadRequest || !strings.Contains(updateRes.Body.String(), "discord_config_not_found") {
		t.Fatalf("update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	overrideOnlyReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"discord_guild_id":"guild-without-config","discord_voice_channel_id":"voice-without-config"}`))
	overrideOnlyReq.AddCookie(cookie)
	overrideOnlyReq.Header.Set("X-CSRF-Token", csrf)
	overrideOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(overrideOnlyRes, overrideOnlyReq)
	if overrideOnlyRes.Code != http.StatusBadRequest || !strings.Contains(overrideOnlyRes.Body.String(), "bad_request") {
		t.Fatalf("override-only update status=%d body=%s", overrideOnlyRes.Code, overrideOnlyRes.Body.String())
	}
}

func TestStreamSettingsRejectUnknownSelectableReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "configured stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{name: "youtube output", body: `{"name":"bad youtube","youtube_output_id":"missing-youtube"}`, code: "youtube_output_not_found"},
		{name: "encoder profile", body: `{"name":"bad encoder","encoder_profile_id":"missing-encoder"}`, code: "encoder_profile_not_found"},
		{name: "caption profile", body: `{"name":"bad caption","caption_profile_id":"missing-caption"}`, code: "caption_profile_not_found"},
		{name: "overlay profile", body: `{"name":"bad overlay","overlay_profile_id":"missing-overlay"}`, code: "overlay_profile_not_found"},
		{name: "archive profile", body: `{"name":"bad archive","archive_profile_id":"missing-archive"}`, code: "archive_profile_not_found"},
		{name: "archive oauth account", body: `{"name":"bad archive oauth","archive_oauth_account_id":"missing-account"}`, code: "drive_oauth_account_unavailable"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(tc.body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), tc.code) {
			t.Fatalf("%s create status=%d body=%s", tc.name, res.Code, res.Body.String())
		}
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+stream.ID+"/settings", bytes.NewBufferString(`{"youtube_output_id":"missing-youtube"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusBadRequest || !strings.Contains(updateRes.Body.String(), "youtube_output_not_found") {
		t.Fatalf("update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
}
