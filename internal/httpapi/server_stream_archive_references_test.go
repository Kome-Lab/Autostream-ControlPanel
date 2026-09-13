package httpapi

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriveDestinationDeleteRejectsStreamAndArchiveProfileReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	profiles := store.NewMemoryProfileStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Archive Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
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
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveDriveDestinationID: destination.ID, ArchiveOAuthAccountID: account.ID}); err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "Shared Drive Archive", map[string]any{
		"format":               "mp4",
		"upload_enabled":       true,
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	body := deleteRes.Body.String()
	if deleteRes.Code != http.StatusConflict || !strings.Contains(body, "drive_destination_in_use") {
		t.Fatalf("expected destination in-use conflict, status=%d body=%s", deleteRes.Code, body)
	}
	for _, expected := range []string{"stream_archive_settings", "archive_profiles"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("delete conflict missing reference count %q: %s", expected, body)
		}
	}
	for _, raw := range []string{"raw-refresh-token", "raw-google-client-secret", "raw-drive-folder-id"} {
		if strings.Contains(body, raw) {
			t.Fatalf("delete conflict leaked secret material %q: %s", raw, body)
		}
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); err != nil {
		t.Fatalf("destination was deleted despite references: %v", err)
	}

	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.DeleteProfile(t.Context(), store.ProfileArchive, archiveProfile.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("destination delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected destination deletion, err=%v", err)
	}
}
