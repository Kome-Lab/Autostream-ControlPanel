package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceRuntimeSecretResolveAllowsAssignedArchiveDestinationSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Standby", PublicURL: "https://encoder-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
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
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithSecretStore(secrets), WithAuditStore(auth))

	body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime archive secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-drive-folder-id" {
		t.Fatalf("unexpected resolved archive secret: %#v", resolved)
	}

	clientSecretBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthProviderClientSecretSecretName(provider.ID) + `"}`
	clientSecretReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(clientSecretBody))
	clientSecretReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	clientSecretRes := httptest.NewRecorder()
	handler.ServeHTTP(clientSecretRes, clientSecretReq)
	if clientSecretRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth client secret resolve status = %d body = %s", clientSecretRes.Code, clientSecretRes.Body.String())
	}
	var resolvedClientSecret serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(clientSecretRes.Body).Decode(&resolvedClientSecret); err != nil {
		t.Fatal(err)
	}
	if resolvedClientSecret.Value != "raw-google-client-secret" {
		t.Fatalf("unexpected resolved OAuth client secret: %#v", resolvedClientSecret)
	}

	refreshTokenBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + oauthAccountRefreshTokenSecretName(account.ID) + `"}`
	refreshTokenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(refreshTokenBody))
	refreshTokenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	refreshTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(refreshTokenRes, refreshTokenReq)
	if refreshTokenRes.Code != http.StatusOK {
		t.Fatalf("runtime OAuth refresh token resolve status = %d body = %s", refreshTokenRes.Code, refreshTokenRes.Body.String())
	}
	var resolvedRefreshToken serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(refreshTokenRes.Body).Decode(&resolvedRefreshToken); err != nil {
		t.Fatal(err)
	}
	if resolvedRefreshToken.Value != "raw-google-refresh-token" {
		t.Fatalf("unexpected resolved OAuth refresh token: %s", formatSafeHTTPSensitiveDiagnostic(resolvedRefreshToken))
	}

	credentialsReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","stream_id":"`+stream.ID+`","archive_profile_id":"`+archiveProfile.ID+`","secret_name":"google_drive_credentials"}`))
	credentialsReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	credentialsRes := httptest.NewRecorder()
	handler.ServeHTTP(credentialsRes, credentialsReq)
	if credentialsRes.Code != http.StatusForbidden {
		t.Fatalf("service account credential secret must not resolve, status = %d body = %s", credentialsRes.Code, credentialsRes.Body.String())
	}

	standbyBody := `{"service_id":"encoder-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + driveDestinationFolderIDSecretName(destination.ID) + `"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-01","secret_name":"`+driveDestinationFolderIDSecretName(destination.ID)+`"}`))
	forbiddenReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("archive secret without stream/profile context status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresPrimaryAssignmentForGenericStreamSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "generic stream secret")
	if err != nil {
		t.Fatal(err)
	}
	primaryToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	standbyToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-generic-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Primary", PublicURL: "https://encoder-generic-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-generic-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Generic Standby", PublicURL: "https://encoder-generic-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-generic-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_generic", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "generic output", map[string]any{
		"service_ids":              []any{"encoder-generic-primary", "encoder-generic-standby"},
		"rtmp_url":                 "rtmps://youtube.example.com/live2",
		"stream_key_secret_name":   "youtube_stream_key_generic",
		"enable_auto_start":        true,
		"enable_auto_stop":         true,
		"broadcast_title_template": "{{stream_name}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-generic-primary","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder generic stream secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-youtube-stream-key" {
		t.Fatalf("unexpected generic stream secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-generic-standby","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_generic"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve generic stream secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-generic-primary","secret_name":"youtube_stream_key_generic"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("generic stream secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveAllowsSelectedCaptionSecretForPrimaryWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "deepgram caption secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "worker-caption-primary", ServiceType: "worker", ServiceName: "Caption Worker",
		PublicURL: "https://worker-caption.example.com", Version: "0.1.0", Capabilities: map[string]any{},
	})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "worker-caption-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "deepgram_api_key", "raw-deepgram-api-key"); err != nil {
		t.Fatal(err)
	}
	caption, err := profiles.CreateProfile(t.Context(), store.ProfileCaption, "Deepgram Japanese", map[string]any{
		"provider": "deepgram", "model": "nova-3", "language": "ja", "api_key_secret_name": "deepgram_api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{CaptionProfileID: caption.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	body := `{"service_id":"worker-caption-primary","stream_id":"` + stream.ID + `","secret_name":"deepgram_api_key"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("caption runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-deepgram-api-key") {
		var resolved serviceRuntimeSecretResolveResponse
		if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
			t.Fatal(err)
		}
		if resolved.Value != "raw-deepgram-api-key" || resolved.SecretName != "deepgram_api_key" {
			t.Fatalf("unexpected caption runtime secret response: %#v", resolved)
		}
	} else {
		t.Fatalf("caption runtime secret was not returned to the selected primary worker: %s", res.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRejectsSecretFromWrongProfileKind(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "wrong profile kind secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-wrong-kind", ServiceType: "encoder_recorder", ServiceName: "Encoder Wrong Kind", PublicURL: "https://encoder-wrong-kind.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-wrong-kind", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_wrong_kind", "raw-youtube-stream-key"); err != nil {
		t.Fatal(err)
	}
	overlayProfile, err := profiles.CreateProfile(t.Context(), store.ProfileOverlay, "bad overlay secret", map[string]any{
		"service_ids":             []any{"encoder-wrong-kind"},
		"stream_key_secret_name":  "youtube_stream_key_wrong_kind",
		"overlay_template_source": "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{OverlayProfileID: overlayProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	reqBody := `{"service_id":"encoder-wrong-kind","stream_id":"` + stream.ID + `","secret_name":"youtube_stream_key_wrong_kind"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "runtime_secret_not_allowed") {
		t.Fatalf("wrong profile kind secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-youtube-stream-key") {
		t.Fatalf("wrong profile kind rejection leaked secret: %s", res.Body.String())
	}
}
