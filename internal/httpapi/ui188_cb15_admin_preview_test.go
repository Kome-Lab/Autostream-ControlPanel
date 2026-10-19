package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestUI188CB15AdminPreviewAfterTokenRevocation(t *testing.T) {
	auth, streams, profiles := store.NewMemoryAuthStore(), store.NewMemoryStreamStore(), store.NewMemoryProfileStore()
	if err := auth.AddUser(store.User{Username: "binding-admin"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "revoked-preview-bot", ServiceType: "discord_bot", ServiceName: "revoked preview", Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com", Capabilities: map[string]any{}})
	if _, err = profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "revoked diagnostic profile", map[string]any{"service_id": "revoked-preview-bot", "bot_token_secret_name": "discord_bot_token_reference_only"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "binding-admin", "correct horse battery")
	if err = auth.RevokeServiceToken(t.Context(), token.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/service-health/revoked-preview-bot/runtime-config", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var payload serviceRuntimeConfigResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.CurrentDiscordBindings == nil || payload.CurrentDiscordBindings.AvailabilityCode != "service_token_unavailable" || payload.CurrentDiscordBindings.Bindings == nil || len(payload.CurrentDiscordBindings.Bindings) != 0 {
		t.Fatal("revoked Bot removed admin diagnostic preview or granted current authority", w.Code)
	}
	if !strings.Contains(w.Body.String(), "discord_bot_token_reference_only") || strings.Contains(w.Body.String(), token.RawToken) || strings.Contains(w.Body.String(), token.ID) {
		t.Fatal("preview lost reference or exposed auth material")
	}
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/service-health/revoked-preview-bot/runtime-config", nil))
	if unauth.Code != 401 {
		t.Fatal("preview authentication weakened")
	}
}
