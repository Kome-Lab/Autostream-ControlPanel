package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncoderRuntimeProfileSecretRequiresSelectedProfileAndPrimaryAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "custom encoder secret")
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
	registerServiceWithTokenForTest(t, auth, primaryToken, store.ServiceRegistration{ServiceID: "encoder-custom-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Primary", PublicURL: "https://encoder-custom-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-custom-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder Custom Standby", PublicURL: "https://encoder-custom-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", stream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "encoder_runtime_secret_custom", "raw-encoder-runtime-secret"); err != nil {
		t.Fatal(err)
	}
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "custom encoder", map[string]any{
		"service_ids":                []any{"encoder-custom-primary", "encoder-custom-standby"},
		"video_bitrate":              "9000k",
		"custom_runtime_secret_name": "encoder_runtime_secret_custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{EncoderProfileID: encoderProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	primaryBody := `{"service_id":"encoder-custom-primary","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	primaryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(primaryBody))
	primaryReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	primaryRes := httptest.NewRecorder()
	handler.ServeHTTP(primaryRes, primaryReq)
	if primaryRes.Code != http.StatusOK {
		t.Fatalf("primary encoder custom profile secret resolve status = %d body = %s", primaryRes.Code, primaryRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(primaryRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "raw-encoder-runtime-secret" {
		t.Fatalf("unexpected custom encoder secret response: %#v", resolved)
	}

	standbyBody := `{"service_id":"encoder-custom-standby","stream_id":"` + stream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
	standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
	standbyRes := httptest.NewRecorder()
	handler.ServeHTTP(standbyRes, standbyReq)
	if standbyRes.Code != http.StatusForbidden {
		t.Fatalf("standby encoder must not resolve custom profile secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
	}

	noStreamReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-custom-primary","secret_name":"encoder_runtime_secret_custom"}`))
	noStreamReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	noStreamRes := httptest.NewRecorder()
	handler.ServeHTTP(noStreamRes, noStreamReq)
	if noStreamRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret without stream context must be forbidden, status = %d body = %s", noStreamRes.Code, noStreamRes.Body.String())
	}

	unselectedStream, err := streams.CreateStream(t.Context(), "unselected custom encoder secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-custom-primary", unselectedStream.ID, "admin", "primary"); err != nil {
		t.Fatal(err)
	}
	unselectedBody := `{"service_id":"encoder-custom-primary","stream_id":"` + unselectedStream.ID + `","secret_name":"encoder_runtime_secret_custom"}`
	unselectedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(unselectedBody))
	unselectedReq.Header.Set("Authorization", "Bearer "+primaryToken.RawToken)
	unselectedRes := httptest.NewRecorder()
	handler.ServeHTTP(unselectedRes, unselectedReq)
	if unselectedRes.Code != http.StatusForbidden {
		t.Fatalf("custom encoder secret for unselected profile must be forbidden, status = %d body = %s", unselectedRes.Code, unselectedRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveAllowsAssignedOAuthArchiveSecrets(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	integrations := store.NewMemoryIntegrationStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
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
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "encoder-oauth-primary", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Primary", PublicURL: "https://encoder-oauth-primary.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, standbyToken, store.ServiceRegistration{ServiceID: "encoder-oauth-standby", ServiceType: "encoder_recorder", ServiceName: "Encoder OAuth Standby", PublicURL: "https://encoder-oauth-standby.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-oauth-primary", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamWithRole(t.Context(), "encoder-oauth-standby", stream.ID, "admin", "standby"); err != nil {
		t.Fatal(err)
	}
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Upload",
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
		AccountLabel: "Drive Upload Account",
		Subject:      "google-subject-01",
		Email:        "uploader@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-google-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-shared-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-main", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithAuditStore(auth))

	cases := []struct {
		name       string
		secretName string
		want       string
	}{
		{name: "folder id", secretName: driveDestinationFolderIDSecretName(destination.ID), want: "raw-shared-drive-folder-id"},
		{name: "oauth provider client secret", secretName: oauthProviderClientSecretSecretName(provider.ID), want: "raw-google-client-secret"},
		{name: "oauth account refresh token", secretName: oauthAccountRefreshTokenSecretName(account.ID), want: "raw-google-refresh-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"service_id":"encoder-oauth-primary","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("runtime oauth archive secret resolve status = %d body = %s", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("runtime secret response must not be cached, got %q", got)
			}
			var resolved serviceRuntimeSecretResolveResponse
			if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
				t.Fatal(err)
			}
			if resolved.SecretName != tc.secretName || resolved.Value != tc.want || resolved.ExpiresInSec <= 0 {
				t.Fatalf("unexpected resolved oauth archive secret: %#v", resolved)
			}

			standbyBody := `{"service_id":"encoder-oauth-standby","stream_id":"` + stream.ID + `","archive_profile_id":"` + archiveProfile.ID + `","secret_name":"` + tc.secretName + `"}`
			standbyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(standbyBody))
			standbyReq.Header.Set("Authorization", "Bearer "+standbyToken.RawToken)
			standbyRes := httptest.NewRecorder()
			handler.ServeHTTP(standbyRes, standbyReq)
			if standbyRes.Code != http.StatusForbidden {
				t.Fatalf("standby encoder must not resolve oauth archive runtime secret, status = %d body = %s", standbyRes.Code, standbyRes.Body.String())
			}

			wrongProfileReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"encoder-oauth-primary","stream_id":"`+stream.ID+`","archive_profile_id":"different-profile","secret_name":"`+tc.secretName+`"}`))
			wrongProfileReq.Header.Set("Authorization", "Bearer "+token.RawToken)
			wrongProfileRes := httptest.NewRecorder()
			handler.ServeHTTP(wrongProfileRes, wrongProfileReq)
			if wrongProfileRes.Code != http.StatusForbidden {
				t.Fatalf("oauth archive secret for a different archive profile must be forbidden, status = %d body = %s", wrongProfileRes.Code, wrongProfileRes.Body.String())
			}
		})
	}
}

func TestNodeRegistrationScopesAutomaticallyIncludeEncoderRuntimeSecrets(t *testing.T) {
	scopes := nodeRegistrationScopes("encoder_recorder", false, false)
	if !stringSliceContains(scopes, "service.secret.resolve") || !stringSliceContains(scopes, "encoder.status.write") || !stringSliceContains(scopes, "observability.ingest") {
		t.Fatalf("encoder scopes should include required runtime access automatically: %#v", scopes)
	}
}
