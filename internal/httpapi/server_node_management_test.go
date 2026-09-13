package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListNodesForRegistrationDoesNotRequireServiceHealthRead(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "node-admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "node-admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create node status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var createBody struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&createBody); err != nil {
		t.Fatal(err)
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+createBody.ConfigureToken+`","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"studio-worker-01","os":"linux","arch":"amd64"}`))
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusOK {
		t.Fatalf("configure node status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		Config struct {
			Auth struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"config"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}

	nodesBeforeHeartbeatReq := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	nodesBeforeHeartbeatReq.AddCookie(cookie)
	nodesBeforeHeartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(nodesBeforeHeartbeatRes, nodesBeforeHeartbeatReq)
	if nodesBeforeHeartbeatRes.Code != http.StatusOK {
		t.Fatalf("list nodes before heartbeat status = %d body = %s", nodesBeforeHeartbeatRes.Code, nodesBeforeHeartbeatRes.Body.String())
	}
	bodyBeforeHeartbeat := nodesBeforeHeartbeatRes.Body.String()
	for _, want := range []string{`"service_id":"studio-worker-01"`, `"status":"registered"`, `"health_status":"unconfigured"`, `"reported_version":"1.4.1"`, `"reported_commit":"abc1234"`, `"reported_build_date":"2026-07-09T00:00:00Z"`, `"reported_os":"linux"`, `"reported_arch":"amd64"`} {
		if !strings.Contains(bodyBeforeHeartbeat, want) {
			t.Fatalf("node list before heartbeat missing %s: %s", want, bodyBeforeHeartbeat)
		}
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/heartbeat", bytes.NewBufferString(`{"nodeId":"studio-worker-01","status":"online","version":"1.4.2","commit":"def5678","build_date":"2026-07-09T01:00:00Z","capabilities":{"streaming":true},"hostname":"studio-worker-01","os":"linux","arch":"amd64","api":{"host":"worker.example.com","port":8443,"sslEnabled":true},"metrics":{"cpuUsage":12.5,"runningJobs":2}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+configureBody.Config.Auth.Token)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("node heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusForbidden {
		t.Fatalf("service-health should still require service_health.read, got %d body = %s", healthRes.Code, healthRes.Body.String())
	}

	nodesReq := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	nodesReq.AddCookie(cookie)
	nodesRes := httptest.NewRecorder()
	handler.ServeHTTP(nodesRes, nodesReq)
	if nodesRes.Code != http.StatusOK {
		t.Fatalf("list nodes status = %d body = %s", nodesRes.Code, nodesRes.Body.String())
	}
	body := nodesRes.Body.String()
	for _, want := range []string{`"service_id":"studio-worker-01"`, `"health_status":"healthy"`, `"reported_version":"1.4.2"`, `"reported_commit":"def5678"`, `"reported_build_date":"2026-07-09T01:00:00Z"`, `"reported_os":"linux"`, `"reported_arch":"amd64"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("node list missing %s: %s", want, body)
		}
	}
	for _, want := range []string{`"capabilities":{"streaming":true}`, `"reported_capabilities":{"streaming":true}`, `"metrics":{"cpuUsage":12.5,"runningJobs":2}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("node list missing sanitized runtime field %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{`"token_id"`, createBody.ConfigureToken, configureBody.Config.Auth.Token} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("node list leaked %q: %s", forbidden, body)
		}
	}
}

func TestNodeManagementUpdateRotateAndDelete(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "node-admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "services.disable", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "studio-worker-01",
		ServiceType: "worker",
		ServiceName: "Studio Worker 01",
		Host:        "worker.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8443",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "node-admin", "correct horse battery")

	updateReq := httptest.NewRequest(http.MethodPut, "/nodes/studio-worker-01", bytes.NewBufferString(`{"service_name":"Studio Worker Edited","description":"編集済みNode","host":"worker-edited.example.com","port":9443,"ssl_enabled":true}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update node status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	if !strings.Contains(updateRes.Body.String(), `"service_name":"Studio Worker Edited"`) || !strings.Contains(updateRes.Body.String(), `"public_url":"https://worker-edited.example.com:9443"`) {
		t.Fatalf("update response missing edited node fields: %s", updateRes.Body.String())
	}
	updated, err := auth.GetService(t.Context(), "studio-worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ServiceName != "Studio Worker Edited" || updated.Description != "編集済みNode" || updated.Host != "worker-edited.example.com" || updated.Port != 9443 || updated.PublicURL != "https://worker-edited.example.com:9443" {
		t.Fatalf("node metadata was not updated: %s", formatSafeHTTPSensitiveDiagnostic(updated))
	}

	configReq := httptest.NewRequest(http.MethodGet, "/nodes/studio-worker-01/configuration", nil)
	configReq.AddCookie(cookie)
	configRes := httptest.NewRecorder()
	handler.ServeHTTP(configRes, configReq)
	if configRes.Code != http.StatusOK {
		t.Fatalf("node configuration status = %d body = %s", configRes.Code, configRes.Body.String())
	}
	if !strings.Contains(configRes.Body.String(), `"node_api_url":"https://worker-edited.example.com:9443"`) {
		t.Fatalf("node configuration should use edited endpoint: %s", configRes.Body.String())
	}
	if strings.Contains(configRes.Body.String(), "test-stream-ingest-signing-key-32-bytes") || strings.Contains(configRes.Body.String(), "stream_ingest") {
		t.Fatalf("normal node configuration response leaked one-time signing material: %s", configRes.Body.String())
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/nodes/studio-worker-01/configure-token", nil)
	configureReq.AddCookie(cookie)
	configureReq.Header.Set("X-CSRF-Token", csrf)
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusCreated {
		t.Fatalf("configure token rotate status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		ConfigureToken string `json:"configure_token"`
		Command        string `json:"configure_command"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}
	if configureBody.ConfigureToken == "" || !strings.Contains(configureBody.Command, configureBody.ConfigureToken) {
		t.Fatalf("configure token was not returned once: %#v", configureBody)
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/nodes/studio-worker-01/rotate-token", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusCreated {
		t.Fatalf("runtime token rotate status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	var rotateBody struct {
		RuntimeToken      string `json:"runtime_token"`
		RuntimeTokenID    string `json:"runtime_token_id"`
		ConfigurationYAML string `json:"configuration_yaml"`
	}
	if err := json.NewDecoder(rotateRes.Body).Decode(&rotateBody); err != nil {
		t.Fatal(err)
	}
	if rotateBody.RuntimeToken == "" || rotateBody.RuntimeTokenID == "" || !strings.Contains(rotateBody.ConfigurationYAML, rotateBody.RuntimeToken) {
		t.Fatalf("runtime token was not returned once in config: %#v", rotateBody)
	}
	if !strings.Contains(rotateBody.ConfigurationYAML, `signing_key: "test-stream-ingest-signing-key-32-bytes"`) {
		t.Fatalf("rotated one-time config omitted stream ingest signing key: %#v", rotateBody)
	}
	if rotateBody.RuntimeToken == token.RawToken || rotateBody.RuntimeTokenID == token.ID {
		t.Fatalf("runtime token was not rotated: %#v", rotateBody)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/services/studio-worker-01", nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete service status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "studio-worker-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted node should be gone, got %v", err)
	}
}

func TestNodeTokenMutationsRejectPermissionEscalation(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "creator", Roles: []string{"node_creator"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "rotator", Roles: []string{"node_rotator"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.heartbeat", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", Host: "encoder.example.com", Port: 8443, SSLEnabled: true, PublicURL: "https://encoder.example.com:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalConfigureHash := security.HashToken("original-configure-token")
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, originalConfigureHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	creatorCookie, creatorCSRF := loginForTest(t, handler, "creator", "correct horse battery")
	creatorReq := httptest.NewRequest(http.MethodPost, "/nodes/encoder-01/configure-token", nil)
	creatorReq.AddCookie(creatorCookie)
	creatorReq.Header.Set("X-CSRF-Token", creatorCSRF)
	creatorRes := httptest.NewRecorder()
	handler.ServeHTTP(creatorRes, creatorReq)
	if creatorRes.Code != http.StatusForbidden || !strings.Contains(creatorRes.Body.String(), "permission_denied") {
		t.Fatalf("configure token rotation without revoke permission status = %d body = %s", creatorRes.Code, creatorRes.Body.String())
	}

	rotatorCookie, rotatorCSRF := loginForTest(t, handler, "rotator", "correct horse battery")
	for _, path := range []string{"/nodes/encoder-01/configure-token", "/nodes/encoder-01/rotate-token"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(rotatorCookie)
		req.Header.Set("X-CSRF-Token", rotatorCSRF)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_escalation") {
			t.Fatalf("%s without secrets.update status = %d body = %s", path, res.Code, res.Body.String())
		}
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != token.ID || got.ConfigureTokenHash != originalConfigureHash || got.ConfigureTokenUsedAt != nil {
		t.Fatalf("denied token operations must not mutate the node: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
}

func TestNodeTokenMutationsRejectInvalidSigningKeyBeforeMutation(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "<CHANGE_ME_64_BYTES>")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", Host: "worker.example.com", Port: 8443, SSLEnabled: true, PublicURL: "https://worker.example.com:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	configureToken := "original-configure-token"
	originalConfigureHash := security.HashToken(configureToken)
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, originalConfigureHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for _, path := range []string{"/nodes/worker-01/configure-token", "/nodes/worker-01/rotate-token"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "stream_ingest_signing_key_invalid") {
			t.Fatalf("%s with invalid signing key status = %d body = %s", path, res.Code, res.Body.String())
		}
	}
	configureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"worker-01","configureToken":"`+configureToken+`","version":"1.4.1"}`))
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusServiceUnavailable || !strings.Contains(configureRes.Body.String(), "stream_ingest_signing_key_invalid") {
		t.Fatalf("auto configure with invalid signing key status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != token.ID || got.ConfigureTokenHash != originalConfigureHash || got.ConfigureTokenUsedAt != nil || got.Status != "pending" || got.ReportedVersion != "" {
		t.Fatalf("invalid signing key must fail before mutating the node: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
}
