package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamArchiveArtifactAdminRoutes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-admin", Roles: []string{"archive_admin"}}, "correct horse battery", []string{"streams.read", "archives.read", "archives.download", "archives.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive managed stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	archiveStartedAt := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)
	archiving, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-admin", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-admin/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt, Kind: "metadata", Name: "metadata.json", RelativePath: "final/" + stream.ID + "/run-admin/metadata.json", SizeBytes: 12}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	var archiveArtifact store.StreamArtifact
	for _, artifact := range artifacts {
		if artifact.Name == "final.mp4" {
			archiveArtifact = artifact
		}
	}
	if archiveArtifact.ID == "" {
		t.Fatalf("archive artifact missing id: %#v", artifacts)
	}

	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "archive-admin", "correct horse battery")
	artifactPath := "/streams/" + stream.ID + "/artifacts/" + archiveArtifact.ID

	downloadReq := httptest.NewRequest(http.MethodGet, artifactPath+"/download", nil)
	downloadReq.AddCookie(cookie)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusOK || downloadRes.Body.String() != "archive-bytes" {
		t.Fatalf("download status=%d body=%q", downloadRes.Code, downloadRes.Body.String())
	}
	if got := downloadRes.Header().Get("Content-Disposition"); !strings.Contains(got, "final.mp4") {
		t.Fatalf("download content disposition = %q", got)
	}
	if dispatcher.archiveDownloadCalls != 1 || dispatcher.archiveArtifact.ID != archiveArtifact.ID {
		t.Fatalf("download did not dispatch expected artifact: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}

	previewReq := httptest.NewRequest(http.MethodGet, artifactPath+"/download?inline=1", nil)
	previewReq.Header.Set("Range", "bytes=0-3")
	previewReq.AddCookie(cookie)
	previewRes := httptest.NewRecorder()
	handler.ServeHTTP(previewRes, previewReq)
	if previewRes.Code != http.StatusPartialContent || previewRes.Body.String() != "arch" {
		t.Fatalf("preview status=%d body=%q", previewRes.Code, previewRes.Body.String())
	}
	if previewRes.Header().Get("Content-Range") != "bytes 0-3/13" || previewRes.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("preview range headers = %#v", previewRes.Header())
	}
	if got := previewRes.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") || !strings.Contains(got, "final.mp4") {
		t.Fatalf("preview content disposition = %q", got)
	}
	if dispatcher.archiveDownloadCalls != 2 || dispatcher.archiveArtifact.ID != archiveArtifact.ID || dispatcher.archiveByteRange != "bytes=0-3" {
		t.Fatalf("preview did not dispatch expected artifact: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	var downloadAudits int
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifact.download" && event.ResourceID == stream.ID {
			downloadAudits++
		}
	}
	if downloadAudits != 1 {
		t.Fatalf("inline playback must not be audited as a download: audits=%#v", auth.AuditEvents())
	}

	invalidRenameReq := httptest.NewRequest(http.MethodPut, artifactPath, bytes.NewBufferString(`{"name":"../secret.mp4"}`))
	invalidRenameReq.AddCookie(cookie)
	invalidRenameReq.Header.Set("X-CSRF-Token", csrf)
	invalidRenameRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidRenameRes, invalidRenameReq)
	if invalidRenameRes.Code != http.StatusBadRequest || !strings.Contains(invalidRenameRes.Body.String(), "invalid_stream_artifact") {
		t.Fatalf("invalid rename status=%d body=%s", invalidRenameRes.Code, invalidRenameRes.Body.String())
	}
	if dispatcher.archiveRenameCalls != 0 {
		t.Fatalf("invalid rename must not dispatch: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}

	renameReq := httptest.NewRequest(http.MethodPut, artifactPath, bytes.NewBufferString(`{"name":"renamed.mp4"}`))
	renameReq.AddCookie(cookie)
	renameReq.Header.Set("X-CSRF-Token", csrf)
	renameRes := httptest.NewRecorder()
	handler.ServeHTTP(renameRes, renameReq)
	if renameRes.Code != http.StatusOK || !strings.Contains(renameRes.Body.String(), "renamed.mp4") {
		t.Fatalf("rename status=%d body=%s", renameRes.Code, renameRes.Body.String())
	}
	if dispatcher.archiveRenameCalls != 1 || dispatcher.archiveRenameName != "renamed.mp4" {
		t.Fatalf("rename did not dispatch expected artifact: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	renamedArtifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !artifactListContains(renamedArtifacts, "renamed.mp4", "final/"+stream.ID+"/run-admin/renamed.mp4") || artifactListContains(renamedArtifacts, "final.mp4", "") {
		t.Fatalf("rename did not update artifact metadata safely: %#v", renamedArtifacts)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, artifactPath, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if dispatcher.archiveDeleteCalls != 1 {
		t.Fatalf("delete did not dispatch: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	finalArtifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if artifactListContains(finalArtifacts, "renamed.mp4", "") {
		t.Fatalf("delete did not remove artifact metadata: %#v", finalArtifacts)
	}
}

func TestArchiveArtifactSharePublicPlaybackWithoutLogin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "archive-admin", Roles: []string{"archive_admin"}}, "correct horse battery", []string{"streams.read", "archives.read", "archives.download", "archives.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "public share stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	shareStartedAt := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	sharing, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-share", shareStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: stream.ID, ArchiveRunID: sharing.ArchiveRunID, ArchiveStartedAt: sharing.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-share/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("unexpected artifacts: %#v", artifacts)
	}

	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "archive-admin", "correct horse battery")
	sharePath := "/streams/" + stream.ID + "/artifacts/" + artifacts[0].ID + "/shares"

	createReq := httptest.NewRequest(http.MethodPost, sharePath, bytes.NewBufferString(`{"expires_in_hours":2,"allow_download":false}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create share status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var createBody struct {
		ID     string `json:"id"`
		Token  string `json:"token"`
		APIURL string `json:"api_url"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.ID == "" || createBody.Token == "" || createBody.APIURL == "" || createBody.URL == "" {
		t.Fatalf("share response missing fields: %#v", createBody)
	}

	listReq := httptest.NewRequest(http.MethodGet, sharePath, nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list share status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), createBody.Token) || strings.Contains(listRes.Body.String(), "token_hash") {
		t.Fatalf("share list leaked token material: %s", listRes.Body.String())
	}

	publicReq := httptest.NewRequest(http.MethodGet, createBody.APIURL, nil)
	publicRes := httptest.NewRecorder()
	handler.ServeHTTP(publicRes, publicReq)
	if publicRes.Code != http.StatusOK || !strings.Contains(publicRes.Body.String(), "final.mp4") || strings.Contains(publicRes.Body.String(), "token_hash") {
		t.Fatalf("public share status=%d body=%s", publicRes.Code, publicRes.Body.String())
	}

	playbackReq := httptest.NewRequest(http.MethodGet, createBody.APIURL+"/download", nil)
	playbackRes := httptest.NewRecorder()
	handler.ServeHTTP(playbackRes, playbackReq)
	if playbackRes.Code != http.StatusOK || playbackRes.Body.String() != "archive-bytes" {
		t.Fatalf("playback status=%d body=%q", playbackRes.Code, playbackRes.Body.String())
	}
	if got := playbackRes.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("playback content disposition = %q", got)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, createBody.APIURL+"/download?download=1", nil)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusForbidden || !strings.Contains(downloadRes.Body.String(), "archive_share_download_disabled") {
		t.Fatalf("disabled download status=%d body=%s", downloadRes.Code, downloadRes.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, sharePath+"/"+createBody.ID, nil)
	revokeReq.AddCookie(cookie)
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeRes := httptest.NewRecorder()
	handler.ServeHTTP(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeRes.Code, revokeRes.Body.String())
	}
	revokedReq := httptest.NewRequest(http.MethodGet, createBody.APIURL, nil)
	revokedRes := httptest.NewRecorder()
	handler.ServeHTTP(revokedRes, revokedReq)
	if revokedRes.Code != http.StatusGone || !strings.Contains(revokedRes.Body.String(), "archive_share_revoked") {
		t.Fatalf("revoked share status=%d body=%s", revokedRes.Code, revokedRes.Body.String())
	}
}

func TestRetryUploadDispatchesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 || len(dispatcher.retriedServices) != 1 || dispatcher.retriedServices[0].ServiceID != "enc-01" {
		t.Fatalf("retry dispatcher was not called correctly: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestRetryUploadDispatchesStreamArchiveConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive retry stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
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
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("drive folder ID leaked in retry response: %s", res.Body.String())
	}
	if dispatcher.retriedArchiveConfig["folder_id"] == "raw-drive-folder-id" {
		t.Fatalf("retry archive config leaked raw folder ID: %#v", dispatcher.retriedArchiveConfig)
	}
	if dispatcher.retriedArchiveConfig["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || dispatcher.retriedArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || dispatcher.retriedArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) || dispatcher.retriedArchiveConfig["shared_drive"] != true {
		t.Fatalf("retry archive config missing scoped secret reference: %#v", dispatcher.retriedArchiveConfig)
	}
	for _, leaked := range []string{"service_account_json", "service_account_credentials_secret_name", "client_secret", "refresh_token", "folder_id"} {
		if _, ok := dispatcher.retriedArchiveConfig[leaked]; ok {
			t.Fatalf("retry archive config leaked raw or unsupported secret field %q: %#v", leaked, dispatcher.retriedArchiveConfig)
		}
	}
}

func TestRetryUploadDispatchesOAuthSharedDriveArchiveConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive retry stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Retry",
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
		AccountLabel: "archive retry account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth retry shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-retry", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveProfileID: archiveProfile.ID}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-google-refresh-token", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("raw OAuth/Drive secret leaked in retry response: %s", res.Body.String())
		}
	}
	cfg := dispatcher.retriedArchiveConfig
	if cfg["auth_mode"] != "oauth2" || cfg["folder_id"] == "raw-oauth-drive-folder-id" || cfg["client_secret"] == "raw-google-client-secret" || cfg["refresh_token"] == "raw-google-refresh-token" {
		t.Fatalf("OAuth retry archive config leaked raw secret values: %#v", cfg)
	}
	if cfg["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) || cfg["shared_drive"] != true {
		t.Fatalf("OAuth retry archive config missing scoped secret references: %#v", cfg)
	}
}

func TestRetryUploadDispatchFailureDoesNotLeakSecretError(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{failRetry: true, dispatchFailureError: `Post "https://encoder.example.com/streams/package?token=secret-token": Authorization Bearer secret-token`}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") || strings.Contains(res.Body.String(), "encoder.example.com") || !strings.Contains(res.Body.String(), "service dispatch failed") {
		t.Fatalf("dispatch secret leaked or sanitized error missing: %s", res.Body.String())
	}
	if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "secret-token") {
		t.Fatalf("dispatch secret leaked in audit events: %#v", auth.AuditEvents())
	}
}

func TestRetryUploadRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload"}); err != nil {
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
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/retry-upload", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || len(body.MissingServiceTypes) != 1 || body.MissingServiceTypes[0] != "encoder_recorder" {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	logs, err := streams.ListStreamLogs(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if strings.Contains(log.Message, "archive upload retry requested") {
			t.Fatalf("retry log should not be created on missing encoder assignment: %#v", logs)
		}
	}
}

func TestEncoderArtifactReportRequiresScopeAssignmentAndSafePaths(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "services.assign", "archives.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Artifact report")
	if err != nil {
		t.Fatal(err)
	}
	archiveStartedAt := time.Date(2026, 8, 18, 5, 6, 29, 0, time.UTC)
	archiving, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-main", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	limited := createBoundServiceTokenForTest(t, handler, cookie, csrf, "encoder_recorder", "encoder-limited", []string{"service.register"})
	registerServiceForTest(t, handler, limited.RawToken, "encoder-limited", "encoder_recorder")
	limitedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(`{"service_id":"encoder-limited","stream_id":"`+stream.ID+`","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/`+stream.ID+`/final.mp4","size_bytes":123}]}`))
	limitedReq.Header.Set("Authorization", "Bearer "+limited.RawToken)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden {
		t.Fatalf("limited artifact report status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	token := createServiceTokenForTest(t, handler, cookie, csrf, "encoder_recorder", []string{"service.register", "encoder.status.write"})
	registerServiceForTest(t, handler, token.RawToken, "encoder-01", "encoder_recorder")
	reportBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"` + archiving.ArchiveRunID + `","archive_started_at":"` + archiveStartedAt.Format(time.RFC3339Nano) + `","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/run-main/final.mp4","size_bytes":123}]}`
	unassignedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(reportBody))
	unassignedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedRes, unassignedReq)
	if unassignedRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned artifact report status = %d body = %s", unassignedRes.Code, unassignedRes.Body.String())
	}
	var unassignedAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "failure" && event.ResourceID == stream.ID {
			unassignedAudit = &event
		}
	}
	if unassignedAudit == nil || unassignedAudit.Metadata["reason"] != "service_not_assigned_to_stream" || unassignedAudit.Metadata["artifact_count"] != 1 {
		t.Fatalf("unassigned artifact report audit missing: %#v", auth.AuditEvents())
	}

	assignReq := httptest.NewRequest(http.MethodPost, "/services/encoder-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("assign encoder status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}

	unsafeReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(`{"service_id":"encoder-01","stream_id":"`+stream.ID+`","archive_run_id":"run-main","archive_started_at":"2026-08-18T05:06:29Z","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"../secret","size_bytes":123}]}`))
	unsafeReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unsafeRes := httptest.NewRecorder()
	handler.ServeHTTP(unsafeRes, unsafeReq)
	if unsafeRes.Code != http.StatusBadRequest || !strings.Contains(unsafeRes.Body.String(), "invalid_stream_artifact") {
		t.Fatalf("unsafe artifact report status = %d body = %s", unsafeRes.Code, unsafeRes.Body.String())
	}
	var invalidAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "failure" && event.Metadata["reason"] == "invalid_stream_artifact" {
			invalidAudit = &event
		}
	}
	if invalidAudit == nil || strings.Contains(toJSONForTest(t, invalidAudit), "../secret") {
		t.Fatalf("invalid artifact report audit missing or leaked path: %#v", auth.AuditEvents())
	}
	for _, unsafePath := range []string{
		`C:\var\lib\autostream\archives\final\` + stream.ID + `\final.mp4`,
		"final/another-stream/run-main/final.mp4",
		"final/" + stream.ID + "/run-main/metadata.json",
	} {
		body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"run-main","archive_started_at":"2026-08-18T05:06:29Z","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"` + strings.ReplaceAll(unsafePath, `\`, `\\`) + `","size_bytes":123}]}`
		req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_stream_artifact") {
			t.Fatalf("mismatched artifact path should be rejected: path=%q status=%d body=%s", unsafePath, res.Code, res.Body.String())
		}
	}
	serverOwnedBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"run-main","archive_started_at":"2026-08-18T05:06:29Z","artifacts":[{"id":"client-controlled","kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/run-main/final.mp4","size_bytes":123,"created_at":"2099-01-01T00:00:00Z"}]}`
	serverOwnedReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(serverOwnedBody))
	serverOwnedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	serverOwnedRes := httptest.NewRecorder()
	handler.ServeHTTP(serverOwnedRes, serverOwnedReq)
	if serverOwnedRes.Code != http.StatusBadRequest {
		t.Fatalf("server-owned artifact fields should be rejected: status=%d body=%s", serverOwnedRes.Code, serverOwnedRes.Body.String())
	}

	for _, size := range []int{123, 456} {
		body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"run-main","archive_started_at":"2026-08-18T05:06:29Z","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/run-main/final.mp4","size_bytes":` + strconv.Itoa(size) + `}]}`
		req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusAccepted {
			t.Fatalf("artifact report status = %d body = %s", res.Code, res.Body.String())
		}
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].SizeBytes != 456 || artifacts[0].RelativePath != "final/"+stream.ID+"/run-main/final.mp4" {
		t.Fatalf("artifact report was not upserted safely: %#v", artifacts)
	}
	nextStartedAt := time.Date(2026, 8, 18, 5, 7, 29, 0, time.UTC)
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-01", nextStartedAt); err != nil {
		t.Fatal(err)
	}
	runBody := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"run-01","archive_started_at":"2026-08-18T05:07:29Z","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/run-01/final.mp4","size_bytes":789}]}`
	runReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(runBody))
	runReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	runRes := httptest.NewRecorder()
	handler.ServeHTTP(runRes, runReq)
	if runRes.Code != http.StatusAccepted {
		t.Fatalf("run-scoped artifact report status = %d body = %s", runRes.Code, runRes.Body.String())
	}
	artifacts, err = streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("run-scoped artifact history = %#v err=%v", artifacts, err)
	}
	var runArtifact *store.StreamArtifact
	for index := range artifacts {
		if artifacts[index].ArchiveRunID == "run-01" {
			runArtifact = &artifacts[index]
		}
	}
	if runArtifact == nil || runArtifact.ArchiveStartedAt == nil || runArtifact.RelativePath != "final/"+stream.ID+"/run-01/final.mp4" {
		t.Fatalf("run-scoped artifact metadata was not retained: %#v", artifacts)
	}
	var successAuditCount int
	for _, event := range auth.AuditEvents() {
		if event.Action == "archive.artifacts.reported" && event.Result == "success" && event.ResourceID == stream.ID {
			successAuditCount++
			if event.ActorUsername != "encoder_recorder" || event.ActorUserID != "service:encoder_recorder" || strings.Contains(event.ActorUserID, token.ID) || event.Metadata["artifact_count"] != 1 {
				t.Fatalf("unsafe artifact success audit: %#v", event)
			}
		}
	}
	if successAuditCount != 3 {
		t.Fatalf("expected artifact success audit for each accepted report, got %d events=%#v", successAuditCount, auth.AuditEvents())
	}
}

func TestArchiveRunIDForStartUsesJSTAndNanoseconds(t *testing.T) {
	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 123456789, time.UTC)
	if got, want := archiveRunIDForStart(startedAt), "20260818_140629_123456789_JST"; got != want {
		t.Fatalf("archive run id = %q, want %q", got, want)
	}
}

func TestEncoderArtifactReportClassifiesStoreFailures(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Artifact report retry")
	if err != nil {
		t.Fatal(err)
	}
	archiveStartedAt := time.Date(2026, 8, 18, 6, 6, 29, 0, time.UTC)
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-failure", archiveStartedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, store.ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	body := `{"service_id":"encoder-01","stream_id":"` + stream.ID + `","archive_run_id":"run-failure","archive_started_at":"2026-08-18T06:06:29Z","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/` + stream.ID + `/run-failure/final.mp4","size_bytes":123}]}`
	for _, test := range []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{name: "transient connection", err: errors.New("write tcp: connection reset by peer"), status: http.StatusServiceUnavailable, code: "stream_artifact_store_unavailable", retryAfter: "1"},
		{name: "non-transient database failure", err: errors.New("database write failed"), status: http.StatusInternalServerError, code: "stream_artifact_store_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewServer(
				failingArtifactReportStreamStore{MemoryStreamStore: streams, err: test.err},
				WithAuditStore(auth),
				WithServiceRegistryStore(auth),
			)
			req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+token.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != test.status || !strings.Contains(res.Body.String(), test.code) {
				t.Fatalf("artifact store failure status = %d body = %s", res.Code, res.Body.String())
			}
			if res.Header().Get("Retry-After") != test.retryAfter {
				t.Fatalf("artifact store failure retry header = %q, want %q", res.Header().Get("Retry-After"), test.retryAfter)
			}
			var matched bool
			for _, event := range auth.AuditEvents() {
				if event.Action == "archive.artifacts.reported" && event.Result == "failure" && event.ResourceID == stream.ID && event.Metadata["reason"] == test.code {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("artifact store failure audit missing for %s: %#v", test.code, auth.AuditEvents())
			}
		})
	}

	handler := NewServer(
		failingArtifactReportStreamStore{MemoryStreamStore: streams, err: errors.New("must not be reached")},
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
	)
	invalidReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewBufferString(`{"service_id":" ","stream_id":"`+stream.ID+`","artifacts":[{"kind":"archive","name":"final.mp4","relative_path":"final/`+stream.ID+`/final.mp4","size_bytes":123}]}`))
	invalidReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	invalidRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidRes, invalidReq)
	if invalidRes.Code != http.StatusBadRequest || !strings.Contains(invalidRes.Body.String(), "invalid_service_id") || invalidRes.Header().Get("Retry-After") != "" {
		t.Fatalf("empty service id status = %d headers=%v body=%s", invalidRes.Code, invalidRes.Header(), invalidRes.Body.String())
	}
}
