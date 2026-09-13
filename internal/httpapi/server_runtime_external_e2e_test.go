package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestExternalE2EConfigExportsControlPanelConfirmation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e stream")
	if err != nil {
		t.Fatal(err)
	}
	driveProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Drive OAuth",
		Enabled:      true,
		ClientID:     "drive-client-id",
		ClientSecret: "raw-drive-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   driveProvider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Account",
		Email:        "drive@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-drive-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveDestination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive Upload",
		AuthMode:       "oauth2",
		OAuthAccountID: driveAccount.ID,
		FolderID:       "0ARealFolderIdShouldNotLeak",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	discordProfile := createDiscordConfigForTest(t, profiles, "external e2e discord", "discord-e2e-primary", "123456789012345678", "234567890123456789", "345678901234567890")
	youtubeOutput, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "private test output", map[string]any{
		"mode":             "live_api_dry_run",
		"oauth_account_id": driveAccount.ID,
		"rtmp_url":         "rtmps://a.rtmps.youtube.com/live2",
		"complete_on_stop": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "external e2e encoder", map[string]any{
		"input_url": "srt://encoder-input.example.com:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "external e2e archive", map[string]any{
		"drive_destination_id": driveDestination.ID,
		"final_container":      "mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		EncoderProfileID: encoderProfile.ID,
		ArchiveProfileID: archiveProfile.ID,
		YouTubeOutputID:  youtubeOutput.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, service := range []struct {
		id          string
		serviceType string
		role        string
	}{
		{id: "discord-e2e-primary", serviceType: "discord_bot", role: "primary"},
		{id: "encoder-e2e-primary", serviceType: "encoder_recorder", role: "primary"},
		{id: "worker-e2e-primary", serviceType: "worker", role: "primary"},
		{id: "encoder-e2e-standby", serviceType: "encoder_recorder", role: "standby"},
		{id: "worker-e2e-standby", serviceType: "worker", role: "standby"},
	} {
		registerServiceInstanceWithCapabilities(t, auth, service.id, service.serviceType, map[string]any{"runtime_config": true})
		if _, err := auth.AssignServiceToStreamWithRole(t.Context(), service.id, stream.ID, "admin", service.role); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("external e2e config response must not be cached, got %q", got)
	}
	responseBody := res.Body.String()
	var body externalE2EConfigResponse
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != 1 || body.StreamID != stream.ID {
		t.Fatalf("unexpected external e2e config identity: %#v", body)
	}
	if body.RuntimeConfig.YouTubeOutputID != youtubeOutput.ID ||
		body.RuntimeConfig.DriveDestinationID != driveDestination.ID ||
		body.RuntimeConfig.DiscordConfigID != discordProfile.ID ||
		body.RuntimeConfig.EncoderProfileID != encoderProfile.ID ||
		body.RuntimeConfig.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected runtime config ids: %#v", body.RuntimeConfig)
	}
	if body.ServiceAssignments.DiscordBotServiceID != "discord-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderPrimaryServiceID != "encoder-e2e-primary" ||
		body.ServiceAssignments.WorkerPrimaryServiceID != "worker-e2e-primary" ||
		body.ServiceAssignments.EncoderRecorderStandbyServiceID != "encoder-e2e-standby" ||
		body.ServiceAssignments.WorkerStandbyServiceID != "worker-e2e-standby" {
		t.Fatalf("unexpected service assignments: %#v", body.ServiceAssignments)
	}
	if !body.Confirmations.YouTubeOutputSaved ||
		!body.Confirmations.DriveDestinationSaved ||
		!body.Confirmations.DiscordConfigSaved ||
		!body.Confirmations.PrimaryAssignmentsSaved ||
		!body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("expected all confirmations true: %#v", body.Confirmations)
	}
	if !body.Readiness.Ready ||
		len(body.Readiness.MissingConfirmations) != 0 ||
		len(body.Readiness.MissingRuntimeIDs) != 0 ||
		len(body.Readiness.MissingPrimaryServices) != 0 ||
		len(body.Readiness.MissingRuntimeConfigCapabilities) != 0 {
		t.Fatalf("expected ready secret-safe readiness summary: %#v", body.Readiness)
	}
	for _, raw := range []string{
		"raw-drive-client-secret",
		"raw-drive-refresh-token",
		"0ARealFolderIdShouldNotLeak",
		"123456789012345678",
		"234567890123456789",
		"345678901234567890",
		"rtmps://a.rtmps.youtube.com/live2",
		"client_secret",
		"refresh_token",
		"folder_id",
	} {
		if strings.Contains(responseBody, raw) {
			t.Fatalf("external e2e config leaked secret or provider runtime value %q: %s", raw, responseBody)
		}
	}
}

func TestExternalE2EConfigRequiresStreamsRead(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e forbidden stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without streams.read, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestExternalE2EConfigReportsMissingControlPanelPieces(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "external e2e incomplete stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/external-e2e-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("external e2e incomplete config status = %d body = %s", res.Code, res.Body.String())
	}
	var body externalE2EConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.StreamID != stream.ID || body.SchemaVersion != 1 {
		t.Fatalf("unexpected incomplete config identity: %#v", body)
	}
	if body.RuntimeConfig != (externalE2ERuntimeConfig{}) || body.ServiceAssignments != (externalE2EServiceAssignments{}) {
		t.Fatalf("incomplete config should report empty ids: runtime=%#v assignments=%#v", body.RuntimeConfig, body.ServiceAssignments)
	}
	if body.Confirmations.YouTubeOutputSaved ||
		body.Confirmations.DriveDestinationSaved ||
		body.Confirmations.DiscordConfigSaved ||
		body.Confirmations.PrimaryAssignmentsSaved ||
		body.Confirmations.RuntimeConfigDistributionEnabled {
		t.Fatalf("incomplete config should report false confirmations: %#v", body.Confirmations)
	}
	if body.Readiness.Ready {
		t.Fatalf("incomplete config should not be ready: %#v", body.Readiness)
	}
	for _, expected := range []string{"youtube_output_saved", "drive_destination_saved", "discord_config_saved", "primary_assignments_saved", "runtime_config_distribution_enabled"} {
		if !slices.Contains(body.Readiness.MissingConfirmations, expected) {
			t.Fatalf("missing confirmation %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"youtube_output_id", "drive_destination_id", "discord_config_id", "encoder_profile_id", "archive_profile_id"} {
		if !slices.Contains(body.Readiness.MissingRuntimeIDs, expected) {
			t.Fatalf("missing runtime id %q not reported: %#v", expected, body.Readiness)
		}
	}
	for _, expected := range []string{"discord_bot", "worker", "encoder_recorder"} {
		if !slices.Contains(body.Readiness.MissingPrimaryServices, expected) {
			t.Fatalf("missing primary service %q not reported: %#v", expected, body.Readiness)
		}
	}
}
