package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestUIPreferenceAPISelfOnlyMatrixRevisionAndFallback(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{}
	if err := auth.AddUser(store.User{ID: "user-a", Username: "alice"}, "password-a", permissions); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "user-b", Username: "bob"}, "password-b", permissions); err != nil {
		t.Fatal(err)
	}
	preferences := store.NewMemoryUserUIPreferenceStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithUserUIPreferenceStore(preferences))
	cookieA, csrfA := loginForTest(t, handler, "alice", "password-a")
	cookieB, _ := loginForTest(t, handler, "bob", "password-b")
	missingRevision := serveUserJSON(t, handler, http.MethodPut, "/account/preferences/ui", `{"theme_id":"ocean","color_mode":"dark"}`, cookieA, csrfA)
	if missingRevision.Code != http.StatusBadRequest {
		t.Fatalf("missing expected_revision=%d %s", missingRevision.Code, missingRevision.Body.String())
	}
	revision := uint64(0)
	for _, themeID := range store.UserThemeIDs {
		for _, mode := range store.UserColorModes {
			response := serveUserJSON(t, handler, http.MethodPut, "/account/preferences/ui", fmt.Sprintf(`{"theme_id":%q,"color_mode":%q,"expected_revision":%d}`, themeID, mode, revision), cookieA, csrfA)
			if response.Code != http.StatusOK {
				t.Fatalf("%s/%s status=%d body=%s", themeID, mode, response.Code, response.Body.String())
			}
			revision++
		}
	}
	bob := serveUserJSON(t, handler, http.MethodGet, "/account/preferences/ui", "", cookieB, "")
	if bob.Code != http.StatusOK || !strings.Contains(bob.Body.String(), `"theme_id":"autostream"`) || !strings.Contains(bob.Body.String(), `"revision":0`) {
		t.Fatalf("second user response=%d %s", bob.Code, bob.Body.String())
	}
	stale := serveUserJSON(t, handler, http.MethodPut, "/account/preferences/ui", `{"theme_id":"ocean","color_mode":"dark","expected_revision":0}`, cookieA, csrfA)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "revision_conflict") {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body.String())
	}
}

func TestDiscordTargetPresetHTTPStreamAuditRedactsRawTargets(t *testing.T) {
	markers := []string{"991234567890123451", "991234567890123452", "991234567890123453"}
	metadata := streamSettingsAuditMetadata(store.Stream{DiscordGuildID: markers[0], DiscordTextID: markers[1], DiscordVoiceID: markers[2]})
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range markers {
		if bytes.Contains(encoded, []byte(marker)) {
			t.Fatalf("stream audit leaked raw Discord target: %s", encoded)
		}
	}
	if metadata["discord_guild_configured"] != true || metadata["discord_text_channel_configured"] != true || metadata["discord_voice_channel_configured"] != true {
		t.Fatalf("configured state was not retained safely: %#v", metadata)
	}
}

func TestDiscordTargetPresetHTTPPermissionsCRUDAndAuditRedactsRawIDs(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{"discord_target_presets.read", "discord_target_presets.create", "discord_target_presets.update", "discord_target_presets.delete"}
	if err := auth.AddUser(store.User{ID: "operator", Username: "operator"}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithDiscordTargetPresetStore(store.NewMemoryDiscordTargetPresetStore()))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	createdResponse := serveUserJSON(t, handler, http.MethodPost, "/discord/target-presets", `{"name":"Main","guild_id":"123456789012345678","text_channel_id":"234567890123456789","voice_channel_id":"345678901234567890"}`, cookie, csrf)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created store.DiscordTargetPreset
	if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	updated := serveUserJSON(t, handler, http.MethodPut, "/discord/target-presets/"+created.ID, fmt.Sprintf(`{"name":"Updated","guild_id":"111111111111111111","text_channel_id":"222222222222222222","voice_channel_id":"333333333333333333","expected_revision":%d}`, created.Revision), cookie, csrf)
	if updated.Code != http.StatusOK {
		t.Fatalf("update=%d %s", updated.Code, updated.Body.String())
	}
	var current store.DiscordTargetPreset
	if err := json.NewDecoder(updated.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}
	deleted := serveUserJSON(t, handler, http.MethodDelete, "/discord/target-presets/"+created.ID, fmt.Sprintf(`{"expected_revision":%d}`, current.Revision), cookie, csrf)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	listed := serveUserJSON(t, handler, http.MethodGet, "/discord/target-presets", "", cookie, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"items":[]`) {
		t.Fatalf("list=%d %s", listed.Code, listed.Body.String())
	}
	for _, event := range auth.AuditEvents() {
		encoded, _ := json.Marshal(event.Metadata)
		for _, raw := range []string{"123456789012345678", "234567890123456789", "345678901234567890", "111111111111111111", "222222222222222222", "333333333333333333"} {
			if bytes.Contains(encoded, []byte(raw)) {
				t.Fatalf("audit leaked raw Discord ID in %s: %s", event.Action, encoded)
			}
		}
	}
}

func TestDiscordTargetPresetHTTPPermissionDeniedDoesNotReachStore(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "reader", Username: "reader"}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	counting := &countingDiscordPresetStore{DiscordTargetPresetStore: store.NewMemoryDiscordTargetPresetStore()}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithDiscordTargetPresetStore(counting))
	cookie, csrf := loginForTest(t, handler, "reader", "correct horse battery")
	response := serveUserJSON(t, handler, http.MethodPost, "/discord/target-presets", `{"name":"x","guild_id":"1","text_channel_id":"2","voice_channel_id":"3"}`, cookie, csrf)
	if response.Code != http.StatusForbidden || counting.createCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, counting.createCalls, response.Body.String())
	}
}

type countingDiscordPresetStore struct {
	store.DiscordTargetPresetStore
	createCalls int
}

func (s *countingDiscordPresetStore) CreateDiscordTargetPreset(ctx context.Context, preset store.DiscordTargetPreset) (store.DiscordTargetPreset, error) {
	s.createCalls++
	return s.DiscordTargetPresetStore.CreateDiscordTargetPreset(ctx, preset)
}

func serveUserJSON(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
