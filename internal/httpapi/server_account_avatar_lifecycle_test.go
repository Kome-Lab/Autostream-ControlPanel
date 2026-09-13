package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurrentUserAvatarLifecycle(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-avatar", Username: "avatar-admin", Email: "avatar@example.jp", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	cookie, csrfToken := loginForTest(t, handler, "avatar-admin", "correct horse battery")
	avatarBody := testAvatarPNG(t, 96, 96)

	upload := httptest.NewRequest(http.MethodPut, "/auth/avatar", bytes.NewReader(avatarBody))
	upload.AddCookie(cookie)
	upload.Header.Set("Content-Type", "image/png")
	upload.Header.Set("X-CSRF-Token", csrfToken)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("avatar upload status = %d body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if strings.Contains(uploadResponse.Body.String(), "image_data") || strings.Contains(uploadResponse.Body.String(), "iVBOR") {
		t.Fatalf("avatar upload response leaked binary data: %s", uploadResponse.Body.String())
	}
	var uploaded struct {
		AvatarURL   string `json:"avatar_url"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uploaded.AvatarURL, "/auth/avatar?v=") || uploaded.ContentType != "image/png" || uploaded.SizeBytes != int64(len(avatarBody)) {
		t.Fatalf("unexpected avatar response: %#v", uploaded)
	}

	me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	me.AddCookie(cookie)
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK || !strings.Contains(meResponse.Body.String(), uploaded.AvatarURL) || !strings.Contains(meResponse.Body.String(), "avatar_updated_at") {
		t.Fatalf("auth me did not expose avatar metadata: status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}

	download := httptest.NewRequest(http.MethodGet, uploaded.AvatarURL, nil)
	download.AddCookie(cookie)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("avatar download status = %d body = %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if !bytes.Equal(downloadResponse.Body.Bytes(), avatarBody) {
		t.Fatal("avatar download bytes did not match upload")
	}
	if got := downloadResponse.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("avatar content type = %q", got)
	}
	if got := downloadResponse.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("avatar cache control = %q", got)
	}
	if got := downloadResponse.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("avatar nosniff header = %q", got)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/auth/avatar", nil)
	remove.AddCookie(cookie)
	remove.Header.Set("X-CSRF-Token", csrfToken)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("avatar delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/auth/avatar", nil)
	missing.AddCookie(cookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted avatar status = %d body = %s", missingResponse.Code, missingResponse.Body.String())
	}

	events := auth.AuditEvents()
	if len(events) < 3 || !hasAuditAction(events, "auth.avatar.update") || !hasAuditAction(events, "auth.avatar.delete") {
		t.Fatalf("avatar audit actions missing: %#v", events)
	}
}
