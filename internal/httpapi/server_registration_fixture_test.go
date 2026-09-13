package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
)

func disableNodeVersionUpdateChecks(t *testing.T) {
	t.Helper()
	for _, target := range nodeVersionUpdateTargets {
		t.Setenv(target.latestVersionEnv, "")
		t.Setenv(target.updateCheckURLEnv, "off")
	}
	t.Setenv(dockerVersionUpdateTarget.latestVersionEnv, "")
	t.Setenv(dockerVersionUpdateTarget.updateCheckURLEnv, "off")
}

func createServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType string, scopes []string) store.ServiceToken {
	return createBoundServiceTokenForTest(t, handler, cookie, csrf, serviceType, defaultServiceIDForTest(serviceType), scopes)
}

func createBoundServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType, serviceID string, scopes []string) store.ServiceToken {
	t.Helper()
	payload := map[string]any{"service_type": serviceType, "scopes": scopes}
	if stringSliceContains(scopes, "service.register") {
		payload["service_id"] = serviceID
		payload["service_name"] = serviceID
		payload["public_url"] = "https://" + serviceID + ".example.com"
		payload["version"] = "0.1.0"
		payload["capabilities"] = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", res.Code, res.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func defaultServiceIDForTest(serviceType string) string {
	switch serviceType {
	case "worker":
		return "worker-01"
	case "encoder_recorder":
		return "encoder-01"
	case "discord_bot":
		return "discord-01"
	case "observability":
		return "observability-01"
	default:
		return serviceType + "-01"
	}
}

func registerServiceWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, registration store.ServiceRegistration) store.RegisteredService {
	t.Helper()
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	service, err := auth.RegisterService(t.Context(), token, registration)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerObservabilityNodeForTest(t *testing.T, auth *store.MemoryAuthStore, _ string, publicURL string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"observability",
		[]string{"service.register", "service.heartbeat", "observability.ingest", "notifications.email.send", "remediation.execute"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeWithTokenForTest(t, auth, token, publicURL)
	return token
}

func registerObservabilityNodeWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, publicURL string) store.RegisteredService {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS", "127.0.0.1")
	service := registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "observability-01",
		ServiceType: "observability",
		ServiceName: "Observability",
		PublicURL:   publicURL,
	})
	ciphertext, nonce, err := security.EncryptSecret(token.RawToken, "test-secret-encryption-key-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	service, err = auth.SetServiceNodeTokenSecret(t.Context(), service.ServiceID, ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerServiceForTest(t *testing.T, handler http.Handler, rawToken, serviceID, serviceType string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"service_id": serviceID, "service_type": serviceType, "service_name": serviceID,
		"public_url": "https://" + serviceID + ".example.com", "version": "0.1.0", "capabilities": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("register service status = %d body = %s", res.Code, res.Body.String())
	}
}
