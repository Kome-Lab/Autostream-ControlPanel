package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestIntegrationRegistryAPIDoesNotReturnRawSecrets(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.read", "integrations.create", "integrations.update", "integrations.delete", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	providerReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(`{"provider_type":"google","name":"Google Login","enabled":true,"client_id":"google-client-id.apps.exampleusercontent.com","client_secret":"raw-client-secret","scopes":["openid","email"],"allowed_domains":["example.com"],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`))
	providerReq.AddCookie(cookie)
	providerReq.Header.Set("X-CSRF-Token", csrf)
	providerRes := httptest.NewRecorder()
	handler.ServeHTTP(providerRes, providerReq)
	if providerRes.Code != http.StatusCreated {
		t.Fatalf("create provider failed: %d %s", providerRes.Code, providerRes.Body.String())
	}
	if strings.Contains(providerRes.Body.String(), "raw-client-secret") || strings.Contains(providerRes.Body.String(), "client_secret\":") {
		t.Fatalf("provider secret leaked: %s", providerRes.Body.String())
	}
	var provider store.OAuthProvider
	if err := json.Unmarshal(providerRes.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	if !provider.ClientSecretConfigured {
		t.Fatalf("expected provider secret configured status: %#v", provider)
	}

	accountReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts", bytes.NewBufferString(`{"provider_id":"`+provider.ID+`","provider_type":"google","account_label":"Archive Account","email":"archive@example.com","scopes":["https://www.googleapis.com/auth/drive.file"],"refresh_token":"raw-refresh-token"}`))
	accountReq.AddCookie(cookie)
	accountReq.Header.Set("X-CSRF-Token", csrf)
	accountRes := httptest.NewRecorder()
	handler.ServeHTTP(accountRes, accountReq)
	if accountRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("manual account create route should be absent: %d %s", accountRes.Code, accountRes.Body.String())
	}
	if strings.Contains(accountRes.Body.String(), "raw-refresh-token") || strings.Contains(accountRes.Body.String(), "refresh_token\":") {
		t.Fatalf("manual account create rejection leaked token: %s", accountRes.Body.String())
	}

	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{ProviderID: provider.ID, ProviderType: "google", AccountLabel: "Archive Account", Email: "archive@example.com", Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, RefreshToken: "raw-refresh-token"})
	if err != nil {
		t.Fatal(err)
	}
	if !account.RefreshTokenConfigured || account.TokenFingerprint == "" {
		t.Fatalf("expected token status and fingerprint: %#v", account)
	}

	updateAccountReq := httptest.NewRequest(http.MethodPut, "/integrations/oauth-accounts/"+account.ID, bytes.NewBufferString(`{"account_label":"Archive Account 2","refresh_token":"raw-refresh-token-2"}`))
	updateAccountReq.AddCookie(cookie)
	updateAccountReq.Header.Set("X-CSRF-Token", csrf)
	updateAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(updateAccountRes, updateAccountReq)
	if updateAccountRes.Code != http.StatusBadRequest {
		t.Fatalf("manual account refresh token field should be rejected: %d %s", updateAccountRes.Code, updateAccountRes.Body.String())
	}
	if strings.Contains(updateAccountRes.Body.String(), "raw-refresh-token-2") || strings.Contains(updateAccountRes.Body.String(), `"refresh_token":`) {
		t.Fatalf("manual account update rejection leaked token context: %s", updateAccountRes.Body.String())
	}

	renameAccountReq := httptest.NewRequest(http.MethodPut, "/integrations/oauth-accounts/"+account.ID, bytes.NewBufferString(`{"account_label":"Archive Account 2"}`))
	renameAccountReq.AddCookie(cookie)
	renameAccountReq.Header.Set("X-CSRF-Token", csrf)
	renameAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(renameAccountRes, renameAccountReq)
	if renameAccountRes.Code != http.StatusOK {
		t.Fatalf("account label update failed: %d %s", renameAccountRes.Code, renameAccountRes.Body.String())
	}
	if strings.Contains(renameAccountRes.Body.String(), "raw-refresh-token") || strings.Contains(renameAccountRes.Body.String(), "refresh_token\":") {
		t.Fatalf("account label update leaked token: %s", renameAccountRes.Body.String())
	}
	var renamedAccount store.OAuthAccount
	if err := json.Unmarshal(renameAccountRes.Body.Bytes(), &renamedAccount); err != nil {
		t.Fatal(err)
	}
	if renamedAccount.AccountLabel != "Archive Account 2" || renamedAccount.DisplayName != "Archive Account 2" {
		t.Fatalf("account label update did not expose display name: %#v", renamedAccount)
	}

	driveReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"Shared Drive Archive","auth_mode":"oauth2","oauth_account_id":"`+account.ID+`","folder_id":"raw-drive-folder-id","shared_drive":true}`))
	driveReq.AddCookie(cookie)
	driveReq.Header.Set("X-CSRF-Token", csrf)
	driveRes := httptest.NewRecorder()
	handler.ServeHTTP(driveRes, driveReq)
	if driveRes.Code != http.StatusCreated {
		t.Fatalf("create drive destination failed: %d %s", driveRes.Code, driveRes.Body.String())
	}
	if strings.Contains(driveRes.Body.String(), "raw-drive-folder-id") || strings.Contains(driveRes.Body.String(), `"folder_id":"`) {
		t.Fatalf("drive folder id leaked: %s", driveRes.Body.String())
	}
	var destination store.DriveDestination
	if err := json.Unmarshal(driveRes.Body.Bytes(), &destination); err != nil {
		t.Fatal(err)
	}
	if !destination.FolderIDConfigured || destination.FolderIDFingerprint == "" || !destination.SharedDrive {
		t.Fatalf("expected drive destination status: %#v", destination)
	}
	legacyBasePathReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"Legacy Base Path","auth_mode":"oauth2","oauth_account_id":"`+account.ID+`","folder_id":"raw-drive-folder-id","base_path":"AutoStream"}`))
	legacyBasePathReq.AddCookie(cookie)
	legacyBasePathReq.Header.Set("X-CSRF-Token", csrf)
	legacyBasePathRes := httptest.NewRecorder()
	handler.ServeHTTP(legacyBasePathRes, legacyBasePathReq)
	if legacyBasePathRes.Code != http.StatusBadRequest {
		t.Fatalf("legacy base_path field should be rejected: %d %s", legacyBasePathRes.Code, legacyBasePathRes.Body.String())
	}

	serviceAccountDriveReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"Legacy Service Account","auth_mode":"service_account","folder_id":"raw-drive-folder-id","shared_drive":true}`))
	serviceAccountDriveReq.AddCookie(cookie)
	serviceAccountDriveReq.Header.Set("X-CSRF-Token", csrf)
	serviceAccountDriveRes := httptest.NewRecorder()
	handler.ServeHTTP(serviceAccountDriveRes, serviceAccountDriveReq)
	if serviceAccountDriveRes.Code != http.StatusBadRequest || !strings.Contains(serviceAccountDriveRes.Body.String(), "drive_destination_auth_mode_unsupported") {
		t.Fatalf("service account drive destination should be rejected: %d %s", serviceAccountDriveRes.Code, serviceAccountDriveRes.Body.String())
	}
	if strings.Contains(serviceAccountDriveRes.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("service account drive rejection leaked folder id: %s", serviceAccountDriveRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/archive/destinations", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || strings.Contains(listRes.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("list leaked drive secret or failed: %d %s", listRes.Code, listRes.Body.String())
	}
}

func TestIntegrationRegistryRejectsInvalidConnectedAccountReferences(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	permissions := []string{"integrations.create", "youtube_outputs.create"}
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	manualAccountReq := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts", bytes.NewBufferString(`{"provider_id":"missing-provider","provider_type":"google","account_label":"Missing Provider","scopes":["https://www.googleapis.com/auth/drive.file"],"refresh_token":"raw-refresh-token"}`))
	manualAccountReq.AddCookie(cookie)
	manualAccountReq.Header.Set("X-CSRF-Token", csrf)
	manualAccountRes := httptest.NewRecorder()
	handler.ServeHTTP(manualAccountRes, manualAccountReq)
	if manualAccountRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("manual account create status = %d body = %s", manualAccountRes.Code, manualAccountRes.Body.String())
	}
	if strings.Contains(manualAccountRes.Body.String(), "raw-refresh-token") || strings.Contains(manualAccountRes.Body.String(), `"refresh_token":`) {
		t.Fatalf("manual account create response leaked refresh token: %s", manualAccountRes.Body.String())
	}

	driveProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-drive-client-id",
		ClientSecret: "raw-google-drive-client-secret",
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
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-drive-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeWithDriveReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"bad-youtube-live","mode":"live_api","rtmp_url":"rtmps://example.youtube.com/live2","oauth_account_id":"`+driveAccount.ID+`"}`))
	youtubeWithDriveReq.AddCookie(cookie)
	youtubeWithDriveReq.Header.Set("X-CSRF-Token", csrf)
	youtubeWithDriveRes := httptest.NewRecorder()
	handler.ServeHTTP(youtubeWithDriveRes, youtubeWithDriveReq)
	if youtubeWithDriveRes.Code != http.StatusBadRequest || !strings.Contains(youtubeWithDriveRes.Body.String(), "youtube_output_youtube_scope_required") {
		t.Fatalf("youtube with drive account status = %d body = %s", youtubeWithDriveRes.Code, youtubeWithDriveRes.Body.String())
	}

	youtubeProvider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "google-youtube-client-id",
		ClientSecret: "raw-google-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	youtubeAccount, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   youtubeProvider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube Account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-youtube-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	driveWithYouTubeReq := httptest.NewRequest(http.MethodPost, "/archive/destinations", bytes.NewBufferString(`{"name":"bad-drive","auth_mode":"oauth2","oauth_account_id":"`+youtubeAccount.ID+`","folder_id":"raw-drive-folder-id","shared_drive":true}`))
	driveWithYouTubeReq.AddCookie(cookie)
	driveWithYouTubeReq.Header.Set("X-CSRF-Token", csrf)
	driveWithYouTubeRes := httptest.NewRecorder()
	handler.ServeHTTP(driveWithYouTubeRes, driveWithYouTubeReq)
	if driveWithYouTubeRes.Code != http.StatusBadRequest || !strings.Contains(driveWithYouTubeRes.Body.String(), "drive_destination_drive_scope_required") {
		t.Fatalf("drive with youtube account status = %d body = %s", driveWithYouTubeRes.Code, driveWithYouTubeRes.Body.String())
	}
	for _, raw := range []string{"raw-github-refresh-token", "raw-drive-refresh-token", "raw-youtube-refresh-token", "raw-drive-folder-id"} {
		if strings.Contains(youtubeWithDriveRes.Body.String(), raw) || strings.Contains(driveWithYouTubeRes.Body.String(), raw) {
			t.Fatalf("validation response leaked raw secret %q", raw)
		}
	}
}

func TestOAuthProviderDeleteRejectsConnectedAccountReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Connected Accounts",
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
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-providers/"+provider.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusConflict || !strings.Contains(deleteRes.Body.String(), "oauth_provider_in_use") {
		t.Fatalf("expected provider in-use conflict, status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if strings.Contains(deleteRes.Body.String(), "raw-refresh-token") || strings.Contains(deleteRes.Body.String(), "raw-google-client-secret") {
		t.Fatalf("delete conflict leaked secret material: %s", deleteRes.Body.String())
	}
	if _, err := integrations.GetOAuthProvider(t.Context(), provider.ID); err != nil {
		t.Fatalf("provider was deleted despite connected account reference: %v", err)
	}

	if err := integrations.DeleteOAuthAccount(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/integrations/oauth-providers/"+provider.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("provider delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
}
