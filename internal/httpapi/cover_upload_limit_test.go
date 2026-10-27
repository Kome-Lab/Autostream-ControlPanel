package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMediaAssetHTTPCoverUploadLimitAndDeletedDraftRecovery(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "quota-user", Username: "quota-user"}, "correct horse battery", []string{"streams.create", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	repository, err := mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithMediaAssetRepository(repository))
	cookie, csrf := loginForTest(t, handler, "quota-user", "correct horse battery")
	response := serveUserJSON(t, handler, http.MethodPost, "/media-assets/upload-sessions", `{}`, cookie, csrf)
	if response.Code != http.StatusCreated {
		t.Fatalf("session status=%d", response.Code)
	}
	var session mediaassets.UploadSession
	if err = json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	upload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("session_id", session.ID); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("usage_type", "video_cover"); err != nil {
			t.Fatal(err)
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="cover.png"`)
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(testAvatarPNG(t, 32, 18)); err != nil {
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
	var first mediaassets.Asset
	for i := 0; i < 4; i++ {
		response := upload()
		if response.Code != http.StatusCreated {
			t.Fatalf("upload %d status=%d body=%s", i+1, response.Code, response.Body.String())
		}
		if i == 0 {
			if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
				t.Fatal(err)
			}
		}
	}
	response = upload()
	var failure map[string]string
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || failure["code"] != "cover_upload_limit" {
		t.Fatalf("fifth upload status=%d code=%s", response.Code, failure["code"])
	}
	response = serveUserJSON(t, handler, http.MethodDelete, "/media-assets/"+first.ID, "", cookie, csrf)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	response = upload()
	if response.Code != http.StatusCreated {
		t.Fatalf("deleted draft did not free quota status=%d body=%s", response.Code, response.Body.String())
	}
}
