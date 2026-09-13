package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func registerAssignedServices(t *testing.T, auth *store.MemoryAuthStore, streamID string, serviceTypes ...string) {
	t.Helper()
	for _, serviceType := range serviceTypes {
		serviceID := serviceType + "-01"
		registerServiceInstance(t, auth, serviceID, serviceType)
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, streamID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
}

func createDiscordConfigForTest(t *testing.T, profiles *store.MemoryProfileStore, name, serviceID, _, _, _ string) store.Profile {
	t.Helper()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, name, map[string]any{
		"service_id":           serviceID,
		"bot_token_configured": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func withManualDiscordTargetForTest(t *testing.T, streams store.StreamStore, streamID, guildID, textChannelID, voiceChannelID string) ServerOption {
	t.Helper()
	repository := streamvisual.NewMemoryRepository(streams)
	if _, err := repository.Update(t.Context(), streamID, "test", streamvisual.Update{
		ExpectedRevision: 1,
		DiscordTarget: streamvisual.OptionalDiscordTarget{
			Set: true,
			Value: streamvisual.DiscordTarget{
				Mode:           "manual",
				GuildID:        guildID,
				TextChannelID:  textChannelID,
				VoiceChannelID: voiceChannelID,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return WithStreamVisualRepository(repository)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func registerServiceInstance(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string) {
	t.Helper()
	capabilities := map[string]any{}
	if serviceType == "encoder_recorder" {
		capabilities["output_relay_mode"] = "direct"
	}
	registerServiceInstanceWithCapabilities(t, auth, serviceID, serviceType, capabilities)
}

func registerServiceInstanceWithCapabilities(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string, capabilities map[string]any) {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Version: "0.1.0", Capabilities: capabilities})
}

func assignServiceForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s status = %d body = %s", serviceID, streamID, res.Code, res.Body.String())
	}
}

func assignServiceWithRoleForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID, role string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`","assignment_role":"`+role+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s as %s status = %d body = %s", serviceID, streamID, role, res.Code, res.Body.String())
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasAuditAction(events []store.AuditEvent, action string) bool {
	for _, event := range events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func testAvatarPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(24 + x%80), G: uint8(100 + y%100), B: 180, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func loginForTest(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("missing session cookie")
	}
	var response struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.CSRFToken == "" {
		t.Fatal("missing csrf token")
	}
	return cookie, response.CSRFToken
}

func loginMFAChallengeForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("MFA login challenge status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ChallengeToken == "" {
		t.Fatal("missing MFA challenge token")
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("MFA challenge must not issue cookies")
	}
	return response.ChallengeToken
}
