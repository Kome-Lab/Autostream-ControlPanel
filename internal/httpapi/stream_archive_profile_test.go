package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestCreateStreamKeepsSelectedArchiveProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "shared archive", map[string]any{"format": "mp4", "retention_days": 30})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"profile-backed stream","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ArchiveProfileID != archiveProfile.ID {
		t.Fatalf("selected archive profile was not persisted: %#v", created)
	}
	archiveProfiles, err := profiles.ListProfiles(t.Context(), store.ProfileArchive)
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveProfiles) != 1 || archiveProfiles[0].ID != archiveProfile.ID {
		t.Fatalf("selecting an existing archive profile must not materialize another profile: %#v", archiveProfiles)
	}
}
