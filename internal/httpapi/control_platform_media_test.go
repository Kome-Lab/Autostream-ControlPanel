package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMediaAssetHTTPUploadVariantAndInternalDeliveryAuthorization(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "asset-user", Username: "asset-user"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Media stream")
	if err != nil {
		t.Fatal(err)
	}
	otherStream, err := streams.CreateStream(t.Context(), "Other stream")
	if err != nil {
		t.Fatal(err)
	}
	storageRoot := t.TempDir()
	repository, err := mediaassets.NewMemoryRepository(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithMediaAssetRepository(repository))
	cookie, csrf := loginForTest(t, handler, "asset-user", "correct horse battery")
	sessionResponse := serveUserJSON(t, handler, http.MethodPost, "/media-assets/upload-sessions", `{}`, cookie, csrf)
	if sessionResponse.Code != http.StatusCreated {
		t.Fatalf("session=%d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	var session mediaassets.UploadSession
	if err = json.NewDecoder(sessionResponse.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	_ = writer.WriteField("session_id", session.ID)
	_ = writer.WriteField("usage_type", "scene_background")
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="SECRET-UPLOAD-MARKER.png"`)
	fileHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(fileHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(testAvatarPNG(t, 96, 64)); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/media-assets", &multipartBody)
	uploadRequest.AddCookie(cookie)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set("X-CSRF-Token", csrf)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload=%d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	encodedStorageRoot, err := json.Marshal(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uploadResponse.Body.String(), "storage_key") || strings.Contains(uploadResponse.Body.String(), storageRoot) || strings.Contains(uploadResponse.Body.String(), strings.Trim(string(encodedStorageRoot), `"`)) {
		t.Fatalf("upload leaked storage data: %s", uploadResponse.Body.String())
	}
	var asset mediaassets.Asset
	if err = json.NewDecoder(uploadResponse.Body).Decode(&asset); err != nil {
		t.Fatal(err)
	}
	variantResponse := serveUserJSON(t, handler, http.MethodPost, "/media-assets/"+asset.ID+"/variants", `{"width":64,"height":64,"opaque":true}`, cookie, csrf)
	if variantResponse.Code != http.StatusOK {
		t.Fatalf("variant=%d %s", variantResponse.Code, variantResponse.Body.String())
	}
	var variant mediaassets.Variant
	if err = json.NewDecoder(variantResponse.Body).Decode(&variant); err != nil {
		t.Fatal(err)
	}
	unreferencedResponse := serveUserJSON(t, handler, http.MethodPost, "/media-assets/"+asset.ID+"/variants", `{"width":32,"height":32,"opaque":true}`, cookie, csrf)
	var unreferenced mediaassets.Variant
	if err = json.NewDecoder(unreferencedResponse.Body).Decode(&unreferenced); err != nil {
		t.Fatal(err)
	}
	if err = repository.ClaimDraft(t.Context(), "asset-user", session.ID, stream.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	repository.ReferenceVariant(stream.ID, variant.ID)
	workerToken := createRegisteredAssignedService(t, auth, "worker-media", "worker", stream.ID)
	success := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, workerToken.RawToken)
	if success.Code != http.StatusOK {
		t.Fatalf("internal=%d %s", success.Code, success.Body.String())
	}
	if success.Header().Get("Digest") == "" || success.Header().Get("Content-Type") != "image/png" || success.Header().Get("X-AutoStream-Asset-ID") != asset.ID || success.Header().Get("X-AutoStream-Variant-ID") != variant.ID {
		t.Fatalf("headers=%v", success.Header())
	}
	if success.Body.Len() != int(variant.ByteSize) {
		t.Fatalf("bytes=%d want=%d", success.Body.Len(), variant.ByteSize)
	}
	if err = repository.SoftDeleteAsset(t.Context(), "asset-user", asset.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stillReferenced := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, workerToken.RawToken)
	if stillReferenced.Code != http.StatusOK {
		t.Fatalf("soft-deleted snapshot fetch=%d %s", stillReferenced.Code, stillReferenced.Body.String())
	}
	if len(variant.SHA256) != 64 {
		t.Fatalf("variant digest missing from safe metadata: %#v", variant)
	}
	variantPath := filepath.Join(storageRoot, variant.SHA256[:2], variant.SHA256[2:4], variant.SHA256+".png")
	variantFile, err := os.OpenFile(variantPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = variantFile.WriteAt([]byte("tamper"), 20); err != nil {
		_ = variantFile.Close()
		t.Fatal(err)
	}
	_ = variantFile.Close()
	tampered := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, workerToken.RawToken)
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "media_asset_integrity") {
		t.Fatalf("tampered internal asset=%d %s", tampered.Code, tampered.Body.String())
	}
	wrongStream := serveServiceGET(handler, "/internal/streams/"+otherStream.ID+"/media-assets/"+variant.ID, workerToken.RawToken)
	if wrongStream.Code != http.StatusForbidden {
		t.Fatalf("wrong stream=%d %s", wrongStream.Code, wrongStream.Body.String())
	}
	notReferenced := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+unreferenced.ID, workerToken.RawToken)
	if notReferenced.Code != http.StatusForbidden {
		t.Fatalf("unreferenced=%d %s", notReferenced.Code, notReferenced.Body.String())
	}
	discordToken := createRegisteredAssignedService(t, auth, "discord-media", "discord_bot", stream.ID)
	wrongType := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, discordToken.RawToken)
	if wrongType.Code != http.StatusForbidden {
		t.Fatalf("wrong type=%d %s", wrongType.Code, wrongType.Body.String())
	}
	unassignedToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, unassignedToken, store.ServiceRegistration{ServiceID: "worker-unassigned", ServiceType: "worker", ServiceName: "Worker Unassigned", PublicURL: "https://worker-unassigned.example.com"})
	unassigned := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, unassignedToken.RawToken)
	if unassigned.Code != http.StatusForbidden {
		t.Fatalf("unassigned=%d %s", unassigned.Code, unassigned.Body.String())
	}
	if err = auth.RevokeServiceToken(t.Context(), workerToken.ID); err != nil {
		t.Fatal(err)
	}
	revoked := serveServiceGET(handler, "/internal/streams/"+stream.ID+"/media-assets/"+variant.ID, workerToken.RawToken)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked=%d %s", revoked.Code, revoked.Body.String())
	}
	for _, event := range auth.AuditEvents() {
		encoded, _ := json.Marshal(event)
		if bytes.Contains(encoded, []byte("SECRET-UPLOAD-MARKER")) {
			t.Fatalf("audit leaked source filename: %s", encoded)
		}
	}
}

func createRegisteredAssignedService(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType, streamID string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com"})
	if _, err = auth.AssignServiceToStreamWithRole(t.Context(), serviceID, streamID, "test-user", "primary"); err != nil {
		t.Fatal(err)
	}
	return token
}
func serveServiceGET(handler http.Handler, path, rawToken string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
