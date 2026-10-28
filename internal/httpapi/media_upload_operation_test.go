package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func uploadOperationFixture(t *testing.T) (http.Handler, *mediaassets.MemoryRepository, *http.Cookie, string, mediaassets.UploadSession) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "upload-owner", Username: "upload-owner"}, "fixture password", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	repo, err := mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithMediaAssetRepository(repo))
	cookie, csrf := loginForTest(t, handler, "upload-owner", "fixture password")
	session, err := repo.CreateUploadSession(t.Context(), "upload-owner", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return handler, repo, cookie, csrf, session
}

func uploadOperationRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, sessionID, operationID string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, item := range [][2]string{{"session_id", sessionID}, {"usage_type", "video_cover"}, {"client_operation_id", operationID}} {
		if err := writer.WriteField(item[0], item[1]); err != nil {
			t.Fatal(err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="operation.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(testAvatarPNG(t, 32, 32)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/media-assets", &body)
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestDraftUploadOperationReplayDoesNotConsumeAnotherCoverSlot(t *testing.T) {
	handler, _, cookie, csrf, session := uploadOperationFixture(t)
	const operation = "3cf1a246-eac1-421b-b00e-48ec97ca6075"
	first := uploadOperationRequest(t, handler, cookie, csrf, session.ID, operation)
	second := uploadOperationRequest(t, handler, cookie, csrf, session.ID, operation)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("upload statuses %d/%d", first.Code, second.Code)
	}
	var a, b mediaassets.Asset
	if json.Unmarshal(first.Body.Bytes(), &a) != nil || json.Unmarshal(second.Body.Bytes(), &b) != nil || a.ID == "" || a.ID != b.ID {
		t.Fatal("replayed upload operation allocated another original asset")
	}
	for _, next := range []string{"44bb36e4-d76a-4ce8-bb1d-2bbb05e435f1", "fdfb83df-4a51-4da0-9d01-a166b97cfc09", "75972aab-8707-4758-87c8-7ed16937aba1"} {
		if response := uploadOperationRequest(t, handler, cookie, csrf, session.ID, next); response.Code != http.StatusCreated {
			t.Fatalf("replay consumed a cover slot: %d", response.Code)
		}
	}
	if response := uploadOperationRequest(t, handler, cookie, csrf, session.ID, operation); response.Code != http.StatusCreated {
		t.Fatalf("same operation must still resolve at four originals: %d", response.Code)
	}
	if response := uploadOperationRequest(t, handler, cookie, csrf, session.ID, "e6e1ca34-995f-42e8-8c39-c59859cce24d"); response.Code != http.StatusConflict {
		t.Fatalf("fifth distinct operation must reject: %d", response.Code)
	}
}

func TestDraftUploadOperationLostResponseCanBeInspectedWithoutMutation(t *testing.T) {
	handler, repo, cookie, csrf, session := uploadOperationFixture(t)
	const operation = "3cf1a246-eac1-421b-b00e-48ec97ca6075"
	upload := uploadOperationRequest(t, handler, cookie, csrf, session.ID, operation)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload=%d", upload.Code)
	}
	var asset mediaassets.Asset
	if err := json.Unmarshal(upload.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	variant, err := repo.EnsureVariant(t.Context(), "upload-owner", asset.ID, 64, 64, true)
	if err != nil {
		t.Fatal(err)
	}
	path := "/media-assets/upload-sessions/" + session.ID + "/assets?operation_id=" + operation
	inspect := serveUserJSON(t, handler, http.MethodGet, path, "", cookie, csrf)
	if inspect.Code != http.StatusOK {
		t.Fatalf("read-only response recovery missing: %d", inspect.Code)
	}
	var snapshot struct {
		Session     mediaassets.UploadSession `json:"session"`
		OperationID string                    `json:"operation_id"`
		Assets      []struct {
			Asset    mediaassets.Asset     `json:"asset"`
			Variants []mediaassets.Variant `json:"variants"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(inspect.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.ID != session.ID || snapshot.OperationID != operation || len(snapshot.Assets) != 1 || snapshot.Assets[0].Asset.ID != asset.ID || len(snapshot.Assets[0].Variants) != 1 || snapshot.Assets[0].Variants[0].ID != variant.ID {
		t.Fatal("read-only recovery does not identify exact operation/asset/variant")
	}
	for _, forbidden := range []string{"storage_key", "owner_user_id", "user_id", "client_operation_session_id"} {
		if strings.Contains(inspect.Body.String(), `"`+forbidden+`"`) {
			t.Fatal("inspection leaked private storage or user metadata")
		}
	}
	foreign, err := repo.CreateUploadSession(t.Context(), "different-owner", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if response := serveUserJSON(t, handler, http.MethodGet, "/media-assets/upload-sessions/"+foreign.ID+"/assets?operation_id="+operation, "", cookie, csrf); response.Code != http.StatusNotFound {
		t.Fatalf("foreign session must remain unavailable: %d", response.Code)
	}
	if err = repo.SoftDeleteDraftAsset(t.Context(), "upload-owner", asset.ID, session.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if response := uploadOperationRequest(t, handler, cookie, csrf, session.ID, operation); response.Code == http.StatusCreated {
		t.Fatal("deleted operation must not resurrect or allocate another original")
	}
}
