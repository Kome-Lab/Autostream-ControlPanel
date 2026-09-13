package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamStartChecksReadinessBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code   string                       `json:"code"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "stream_start_not_ready" || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher.fakeServiceDispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness failure: %#v", unchanged)
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "streams.start" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected readiness failure audit event, got %#v", events)
	}
}

func TestStreamStartReadinessEndpointReportsMissingAssignmentsWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready               bool     `json:"ready"`
		MissingServiceTypes []string `json:"missing_service_types"`
		AssignedCount       int      `json:"assigned_service_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "encoder_recorder") || body.AssignedCount != 1 {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness check: %#v", unchanged)
	}
}

func TestStreamStartReadinessEndpointReportsServerReadinessIssues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness endpoint discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","encoder_input_url":"srt://source.example.com:9000"}`))
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
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher.fakeServiceDispatcher)
	}
}

func TestStreamStartReadinessRejectsUnsupportedNegotiatedWorkerVideoProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "unsupported scene")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "scene discord", "discord_bot-01", "guild", "voice", "")
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "unsupported", map[string]any{"width": 1024, "height": 576, "fps": 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, registration := range []store.ServiceRegistration{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", ServiceName: "encoder", PublicURL: "https://encoder.example.com", Capabilities: map[string]any{"output_relay_mode": "direct", "worker_frame_ingest_mjpeg_srt": true}},
		{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "worker", PublicURL: "https://worker.example.com", Capabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", ServiceName: "bot", PublicURL: "https://bot.example.com"},
	} {
		token, err := auth.CreateServiceToken(t.Context(), registration.ServiceType, []string{"service.register"})
		if err != nil {
			t.Fatal(err)
		}
		registerServiceWithTokenForTest(t, auth, token, registration)
		if _, err := auth.AssignServiceToStream(t.Context(), registration.ServiceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	body := fmt.Sprintf(`{"discord_config_id":%q,"encoder_profile_id":%q}`, discord.ID, encoderProfile.ID)
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "worker_video_encoder_profile_unsupported") || !strings.Contains(res.Body.String(), `"ready":false`) {
		t.Fatalf("readiness did not reject unsupported negotiated scene: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestStreamStartReadinessEndpointReportsMissingDriveDestination(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "missing-destination-archive", map[string]any{
		"drive_destination_id": "missing-drive-destination",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(store.NewMemoryIntegrationStore()), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
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
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_destination_not_found" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestStreamStartReadinessEndpointReportsOAuthDriveAccountIssueWithoutRawSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Readiness",
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
		AccountLabel: "archive account without token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth archive readiness destination",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-readiness", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
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
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_oauth_account_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("readiness response leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
}
