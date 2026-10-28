package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

const mediaUploadOperationRoutePattern = "GET /media-assets/upload-sessions/{session_id}/assets"
const mediaUploadOperationRouteStatement = `s.mux.HandleFunc("GET /media-assets/upload-sessions/{session_id}/assets", s.requireAnyPermission([]string{"streams.create", "streams.update"}, s.inspectMediaUploadOperation))`

// Keep the historical 792c6c fixture unchanged. Extend only this one new
// authenticated, owner-scoped read route after its upload-session anchor.
func addMediaUploadOperationRouteCharacterization(t *testing.T, fixture *routeCharacterizationFixture) {
	t.Helper()
	anchor := -1
	for index, registration := range fixture.Registrations {
		if registration.Pattern == mediaUploadOperationRoutePattern {
			t.Fatal("upload operation route must be added exactly once")
		}
		if registration.Pattern == "POST /media-assets/upload-sessions" {
			if anchor != -1 {
				t.Fatal("upload operation route anchor must be unique")
			}
			anchor = index
		}
	}
	if anchor < 0 {
		t.Fatal("upload operation route baseline anchor is missing")
	}
	addition := fixture.Registrations[anchor]
	addition.Pattern = mediaUploadOperationRoutePattern
	addition.Statement = mediaUploadOperationRouteStatement
	index := anchor + 1
	fixture.Registrations = append(fixture.Registrations, addition)
	copy(fixture.Registrations[index+1:], fixture.Registrations[index:len(fixture.Registrations)-1])
	fixture.Registrations[index] = addition
}

func TestMediaUploadRouteCharacterizationRetainsEveryHistoricalRegistration(t *testing.T) {
	bytes, err := os.ReadFile(filepath.Join("testdata", "routes-characterization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var historical routeCharacterizationFixture
	if err := json.Unmarshal(bytes, &historical); err != nil {
		t.Fatal(err)
	}
	current := loadRouteCharacterizationFixture(t)
	additions := map[string]bool{}
	for _, pattern := range []string{
		"GET /streams/{id}/stop-outcome",
		"GET /streams/{id}/media-assets/{variant_id}",
		"GET /video-cover-presets/{id}/media-assets/{variant_id}",
		mediaUploadOperationRoutePattern,
	} {
		additions[pattern] = true
	}
	retained := current
	retained.Registrations = nil
	for index, registration := range current.Registrations {
		if !additions[registration.Pattern] {
			retained.Registrations = append(retained.Registrations, registration)
			continue
		}
		delete(additions, registration.Pattern)
		if registration.Pattern == mediaUploadOperationRoutePattern {
			if index == 0 || current.Registrations[index-1].Pattern != "POST /media-assets/upload-sessions" || registration.Statement != mediaUploadOperationRouteStatement {
				t.Fatal("upload operation route changed anchor, permissions or handler")
			}
		}
	}
	if len(additions) != 0 || !reflect.DeepEqual(retained, historical) {
		t.Fatal("finite route extension changed the historical registration contract")
	}
}

func TestMediaUploadOperationRouteKeepsAuthenticationMiddleware(t *testing.T) {
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	request := httptest.NewRequest(http.MethodGet, "/media-assets/upload-sessions/fixture/assets?operation_id=fixture", nil)
	_, pattern := server.mux.Handler(request)
	if pattern != mediaUploadOperationRoutePattern {
		t.Fatalf("upload operation route pattern=%q", pattern)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("upload operation route authentication status=%d", response.Code)
	}
}
