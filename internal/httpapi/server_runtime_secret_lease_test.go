package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceRuntimeSecretResolveIsScopedToRuntimeProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	configOnlyToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime secret response must not be cached, got %q", got)
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SecretName != "discord_bot_token_profile-01" || body.Value != "Bot <RAW_DISCORD_TOKEN>" || body.ExpiresInSec <= 0 {
		t.Fatalf("unexpected runtime secret response: %#v", body)
	}
	configOnlyReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	configOnlyReq.Header.Set("Authorization", "Bearer "+configOnlyToken.RawToken)
	configOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(configOnlyRes, configOnlyReq)
	if configOnlyRes.Code != http.StatusForbidden || !strings.Contains(configOnlyRes.Body.String(), "missing_service_scope") || strings.Contains(configOnlyRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("config-only token must not resolve runtime secret, status = %d body = %s", configOnlyRes.Code, configOnlyRes.Body.String())
	}
	replayReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	replayReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	replayRes := httptest.NewRecorder()
	handler.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusConflict || !strings.Contains(replayRes.Body.String(), "runtime_secret_lease_active") || strings.Contains(replayRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("active runtime secret lease replay status = %d body = %s", replayRes.Code, replayRes.Body.String())
	}

	crossReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	crossReq.Header.Set("Authorization", "Bearer "+tokenTwo.RawToken)
	crossRes := httptest.NewRecorder()
	handler.ServeHTTP(crossRes, crossReq)
	if crossRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime secret status = %d body = %s", crossRes.Code, crossRes.Body.String())
	}

	unreferencedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_unreferenced"}`))
	unreferencedReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	unreferencedRes := httptest.NewRecorder()
	handler.ServeHTTP(unreferencedRes, unreferencedReq)
	if unreferencedRes.Code != http.StatusForbidden {
		t.Fatalf("unreferenced runtime secret status = %d body = %s", unreferencedRes.Code, unreferencedRes.Body.String())
	}

	cfgReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	cfgReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	cfgRes := httptest.NewRecorder()
	handler.ServeHTTP(cfgRes, cfgReq)
	if cfgRes.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", cfgRes.Code, cfgRes.Body.String())
	}
	if strings.Contains(cfgRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime config leaked raw token: %s", cfgRes.Body.String())
	}
}

func TestServiceRuntimeSecretResolveRequiresSecureTransportInProduction(t *testing.T) {
	t.Setenv("AUTOSTREAM_ENV", "production")
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")

	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	requestBody := `{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`
	insecureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	insecureReq.RemoteAddr = "198.51.100.10:12345"
	insecureReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	insecureRes := httptest.NewRecorder()
	handler.ServeHTTP(insecureRes, insecureReq)
	if insecureRes.Code != http.StatusForbidden || !strings.Contains(insecureRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("production HTTP runtime secret resolve status = %d body = %s", insecureRes.Code, insecureRes.Body.String())
	}
	if insecureRes.Header().Get("Cache-Control") != "no-store" || strings.Contains(insecureRes.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("insecure runtime secret rejection must not be cached or leak raw value: headers=%v body=%s", insecureRes.Header(), insecureRes.Body.String())
	}

	spoofedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	spoofedReq.RemoteAddr = "198.51.100.10:12345"
	spoofedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	spoofedReq.Header.Set("X-Forwarded-Proto", "https")
	spoofedRes := httptest.NewRecorder()
	handler.ServeHTTP(spoofedRes, spoofedReq)
	if spoofedRes.Code != http.StatusForbidden || !strings.Contains(spoofedRes.Body.String(), "runtime_secret_transport_insecure") {
		t.Fatalf("untrusted forwarded proto runtime secret status = %d body = %s", spoofedRes.Code, spoofedRes.Body.String())
	}

	trustedReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(requestBody))
	trustedReq.RemoteAddr = "10.0.0.10:12345"
	trustedReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	trustedReq.Header.Set("X-Forwarded-Proto", "https")
	trustedRes := httptest.NewRecorder()
	handler.ServeHTTP(trustedRes, trustedReq)
	if trustedRes.Code != http.StatusOK {
		t.Fatalf("trusted HTTPS forwarded runtime secret status = %d body = %s", trustedRes.Code, trustedRes.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(trustedRes.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected trusted forwarded runtime secret response: %#v", resolved)
	}
}

func TestServiceRuntimeSecretLeaseActiveIsCheckedBeforeSecretValue(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	secrets := &trackingSecretStore{}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithRuntimeSecretLeaseStore(activeRuntimeSecretLeaseStore{}), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "runtime_secret_lease_active") {
		t.Fatalf("active runtime secret lease status = %d body = %s", res.Code, res.Body.String())
	}
	if secrets.getCalls != 0 {
		t.Fatalf("runtime secret value was read before active lease rejection, calls=%d", secrets.getCalls)
	}
	if strings.Contains(res.Body.String(), "<RAW_DISCORD_TOKEN>") {
		t.Fatalf("runtime secret replay leaked raw value: %s", res.Body.String())
	}
}

func TestServiceRuntimeSecretResolveReleasesLeaseWhenSecretIsMissing(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"bot_token_secret_name": "discord_bot_token_profile-01",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))

	missingReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	missingReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound || !strings.Contains(missingRes.Body.String(), "runtime_secret_not_configured") {
		t.Fatalf("missing runtime secret status = %d body = %s", missingRes.Code, missingRes.Body.String())
	}

	if _, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_profile-01", "Bot <RAW_DISCORD_TOKEN>"); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(`{"service_id":"discord-01","secret_name":"discord_bot_token_profile-01"}`))
	retryReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("runtime secret retry after configuration status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}
	var body serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(retryRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Value != "Bot <RAW_DISCORD_TOKEN>" {
		t.Fatalf("unexpected retry runtime secret response: %#v", body)
	}
}

func TestServiceRuntimeSecretResolveAllowsLiveAPIDryRunStreamSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	secrets := store.NewMemorySecretStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run runtime secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "encoder-dry-run", ServiceType: "encoder_recorder", ServiceName: "Encoder Dry Run",
		PublicURL: "https://encoder-dry-run.example.com", Version: "0.1.0", Capabilities: map[string]any{},
	})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-dry-run", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "dry-run output", map[string]any{
		"mode": "live_api_dry_run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	secretName := "youtube_stream_key_runtime_dry_run"
	if _, err := secrets.UpdateSecret(t.Context(), secretName, "raw-dry-run-stream-key"); err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(t.Context(), store.StreamYouTubeRuntime{
		StreamID: stream.ID, YouTubeOutput: youtube.ID, Mode: "live_api_dry_run", StreamKeySecretName: secretName,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithAuditStore(auth))
	body := `{"service_id":"encoder-dry-run","stream_id":"` + stream.ID + `","secret_name":"` + secretName + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/runtime-secrets/resolve", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("dry-run runtime secret resolve status = %d body = %s", res.Code, res.Body.String())
	}
	var resolved serviceRuntimeSecretResolveResponse
	if err := json.NewDecoder(res.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.SecretName != secretName || resolved.Value != "raw-dry-run-stream-key" || resolved.ExpiresInSec <= 0 {
		t.Fatalf("unexpected dry-run runtime secret: %#v", resolved)
	}
}
