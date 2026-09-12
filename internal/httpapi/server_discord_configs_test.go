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

func TestDiscordConfigRequiresRegisteredDiscordBotService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"discord_configs.create", "discord_configs.update"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"missing-service","service_id":"discord-missing"}`))
	missingReq.AddCookie(cookie)
	missingReq.Header.Set("X-CSRF-Token", csrf)
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusConflict || !strings.Contains(missingRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("missing service status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}

	wrongTypeReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"wrong-type","service_id":"encoder-01"}`))
	wrongTypeReq.AddCookie(cookie)
	wrongTypeReq.Header.Set("X-CSRF-Token", csrf)
	wrongTypeRes := httptest.NewRecorder()
	handler.ServeHTTP(wrongTypeRes, wrongTypeReq)
	if wrongTypeRes.Code != http.StatusConflict || !strings.Contains(wrongTypeRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("wrong service type status = %d body = %s", wrongTypeRes.Code, wrongTypeRes.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"valid","service_id":"discord-01"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created discordConfigResponse
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/discord/configs/"+created.ID, bytes.NewBufferString(`{"name":"invalid-update","service_id":"encoder-01"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusConflict || !strings.Contains(updateRes.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("update wrong service type status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
}

func TestDiscordConfigStoresReconnectPolicyFields(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"discord_configs.create", "discord_configs.read"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"reconnect","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","reconnect_enabled":false,"audio_forward_enabled":false,"reconnect_max_attempts":7,"reconnect_base_delay":"3s","reconnect_max_delay":"45s"}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created discordConfigResponse
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.ReconnectEnabled || !created.AudioForwardEnabled || created.ReconnectMaxAttempts != 7 || created.ReconnectBaseDelay != "3s" || created.ReconnectMaxDelay != "45s" {
		t.Fatalf("unexpected reconnect response: %#v", created)
	}

	profile, err := profiles.GetProfile(t.Context(), store.ProfileDiscordConfig, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config["reconnect_max_attempts"] != 7 || profile.Config["reconnect_base_delay"] != "3s" || profile.Config["reconnect_max_delay"] != "45s" {
		t.Fatalf("unexpected stored reconnect config: %#v", profile.Config)
	}
	if profile.Config["reconnect_enabled"] != true || profile.Config["audio_forward_enabled"] != true {
		t.Fatalf("discord transfer flags should be forced enabled: %#v", profile.Config)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"bad-reconnect","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","reconnect_base_delay":"soon"}`))
	badReq.AddCookie(cookie)
	badReq.Header.Set("X-CSRF-Token", csrf)
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusBadRequest || !strings.Contains(badRes.Body.String(), "invalid_discord_config") {
		t.Fatalf("bad duration status = %d body = %s", badRes.Code, badRes.Body.String())
	}
}
