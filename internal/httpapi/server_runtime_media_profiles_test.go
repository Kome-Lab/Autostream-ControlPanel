package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceRuntimeConfigIncludesEncoderArchiveConfigWithoutRawSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "encoder runtime archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Runtime",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Drive Runtime Account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Runtime Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "runtime archive", map[string]any{"drive_destination_id": destination.ID, "archive_file_name": "Council Meeting 20260708.mp4", "shared_drive_id": "shared-drive-01", "retention_days": 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-drive-folder-id", "raw-google-client-secret", "raw-google-refresh-token"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("runtime config leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamArchiveConfigs) != 1 {
		t.Fatalf("expected one stream archive config, got %#v", body.StreamArchiveConfigs)
	}
	cfg := body.StreamArchiveConfigs[0]
	if !cfg.Ready || cfg.StreamID != stream.ID || cfg.AssignmentRole != "primary" || cfg.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("unexpected stream archive config identity: %#v", cfg)
	}
	if cfg.ArchiveConfig["auth_mode"] != "oauth2" || cfg.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("runtime archive config omitted mode/shared drive: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["archive_file_name"] != "Council Meeting 20260708.mp4" || cfg.ArchiveConfig["shared_drive_id"] != "shared-drive-01" {
		t.Fatalf("runtime archive config omitted file/shared drive id settings: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["retention_days"] != float64(60) {
		t.Fatalf("runtime archive config omitted retention days: %#v", cfg.ArchiveConfig)
	}
	if cfg.ArchiveConfig["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("runtime archive config omitted scoped secret references: %#v", cfg.ArchiveConfig)
	}
	if _, leaked := cfg.ArchiveConfig["folder_id"]; leaked {
		t.Fatalf("runtime archive config exposed raw folder field: %#v", cfg.ArchiveConfig)
	}
}

func TestServiceRuntimeConfigIncludesSelectedUnscopedEncoderProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "encoder profile stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder 01",
		PublicURL:   "https://encoder.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	selected, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "selected encoder", map[string]any{
		"width": 1280, "height": 720, "fps": 30, "video_bitrate_kbps": 4500,
	})
	if err != nil {
		t.Fatal(err)
	}
	unselected, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "unselected encoder", map[string]any{
		"width": 1920, "height": 1080, "fps": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "other encoder", map[string]any{
		"service_id": "encoder-other-01", "width": 1920, "height": 1080, "fps": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{EncoderProfileID: selected.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	encoderProfiles := body.Profiles[string(store.ProfileEncoder)]
	if len(encoderProfiles) != 1 || encoderProfiles[0].ID != selected.ID {
		t.Fatalf("assigned encoder runtime config must contain only its selected unscoped profile: selected=%s unselected=%s restricted=%s profiles=%#v", selected.ID, unselected.ID, restricted.ID, encoderProfiles)
	}
	if encoderProfiles[0].Config["width"] != float64(1280) || encoderProfiles[0].Config["height"] != float64(720) || encoderProfiles[0].Config["fps"] != float64(30) {
		t.Fatalf("selected encoder profile config was not preserved: %#v", encoderProfiles[0].Config)
	}
}

func TestRuntimeUnscopedProfileMayFollowAssignedMediaServiceRejectsExplicitBindings(t *testing.T) {
	selected := map[string]struct{}{"profile-01": {}}
	tests := []struct {
		name    string
		profile store.Profile
		want    bool
	}{
		{name: "selected unscoped", profile: store.Profile{ID: "profile-01", Config: map[string]any{}}, want: true},
		{name: "not selected", profile: store.Profile{ID: "profile-02", Config: map[string]any{}}, want: false},
		{name: "bound to another service", profile: store.Profile{ID: "profile-01", Config: map[string]any{"service_id": "encoder-other-01"}}, want: false},
		{name: "bound to service list", profile: store.Profile{ID: "profile-01", Config: map[string]any{"service_ids": []any{"encoder-other-01"}}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeUnscopedProfileMayFollowAssignedMediaService(tt.profile, selected); got != tt.want {
				t.Fatalf("runtimeUnscopedProfileMayFollowAssignedMediaService() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestServiceRuntimeConfigIncludesStreamWatermarkForAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "encoder watermark stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-watermark-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder Watermark 01",
		PublicURL:   "https://encoder-watermark.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-watermark-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	overlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "Kome-Lab watermark", map[string]any{
		"watermark_enabled":        true,
		"watermark_image_data_url": "data:image/png;base64,AA==",
		"watermark_file_name":      "kome-lab.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlay.ID}); err != nil {
		t.Fatal(err)
	}
	otherStream, err := streams.CreateStream(t.Context(), "encoder watermark restricted stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "encoder-other-01",
		ServiceType: "encoder_recorder",
		ServiceName: "Encoder Other 01",
		PublicURL:   "https://encoder-other.example.com",
		Version:     "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-other-01", otherStream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	restrictedOverlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "Other encoder watermark", map[string]any{
		"service_id":               "encoder-other-01",
		"watermark_image_data_url": "data:image/png;base64,BB==",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), otherStream.ID, store.StreamSettings{OverlayProfileID: restrictedOverlay.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=encoder-watermark-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	overlayProfiles := body.Profiles[string(store.ProfileOverlay)]
	if len(overlayProfiles) != 1 || overlayProfiles[0].ID != overlay.ID {
		t.Fatalf("assigned encoder runtime config omitted stream watermark profile: %#v", overlayProfiles)
	}
	if overlayProfiles[0].Config["watermark_image_data_url"] != "data:image/png;base64,AA==" {
		t.Fatalf("runtime watermark image was not preserved: %#v", overlayProfiles[0].Config)
	}
}

func TestServiceRuntimeConfigIncludesSelectedOverlayForAssignedWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "Worker scene stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "worker-scene-01", ServiceType: "worker", ServiceName: "Worker Scene 01",
		PublicURL: "https://worker-scene.example.com", Version: "0.1.0",
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-scene-01", stream.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	overlay, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "scene overlay", map[string]any{
		"watermark_enabled": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlay.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))
	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=worker-scene-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status=%d body=%s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	items := body.Profiles[string(store.ProfileOverlay)]
	if len(items) != 1 || items[0].ID != overlay.ID {
		t.Fatalf("assigned Worker runtime config omitted selected overlay: %#v", items)
	}
}
