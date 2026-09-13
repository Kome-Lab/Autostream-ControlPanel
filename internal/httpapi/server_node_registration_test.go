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

func TestCreateStreamRejectsRequestedPrimaryNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"auto ready stream","encoder_service_id":"encoder-01","worker_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("rejected legacy create persisted streams: %#v", items)
	}
	events := auth.AuditEvents()
	assignAudits := 0
	for _, event := range events {
		if event.Action == "services.assign" && event.ResourceID != "" && event.Metadata["source"] == "stream_settings" {
			assignAudits++
		}
	}
	if assignAudits != 0 {
		t.Fatalf("create must not write assignment audit events, got %d events=%#v", assignAudits, events)
	}
}

func TestCreateStreamRejectsPrimaryNodeAssignmentWithoutPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "encoder-01", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked stream","encoder_service_id":"encoder-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("rejected legacy create persisted streams: %#v", items)
	}
}

func TestCreateStreamRejectsPrimaryNodeFieldBeforeResolvingType(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"wrong node stream","encoder_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("create status = %d body = %s", res.Code, res.Body.String())
	}
	items, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("rejected legacy create persisted streams: %#v", items)
	}
}

func TestServiceTokenRegisterHeartbeatAndRevoke(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.read", "api_tokens.create", "api_tokens.revoke", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register","service.heartbeat"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	if got := createTokenRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("one-time service token response should be no-store, got %q", got)
	}
	var token store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.RawToken == "" || token.TokenHash != "" {
		t.Fatalf("token response leaked hash or missed raw token: %s", formatSafeHTTPSensitiveDiagnostic(token))
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{"overlay":true,"webhook_url":"https://discord.com/api/webhooks/id/raw-secret-token","google_drive_folder_id":"drive-folder-secret-id","endpoint":"rtsp://user:password@camera.example.com/live","nested":{"access_token":"super-secret-token","drive_folder_id":"nested-folder-secret-id","safe":true}}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if strings.Contains(registerRes.Body.String(), `"token_id"`) || strings.Contains(registerRes.Body.String(), token.ID) {
		t.Fatalf("service registration response leaked token binding id: %s", registerRes.Body.String())
	}
	var registerAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.register" && event.ResourceID == "worker-01" {
			registerAudit = &event
		}
	}
	if registerAudit == nil || registerAudit.Result != "success" || registerAudit.ActorUsername != "worker" || registerAudit.ActorUserID != "service:worker" {
		t.Fatalf("service registration audit missing or unsafe: %#v", auth.AuditEvents())
	}
	if strings.Contains(registerAudit.ActorUserID, token.ID) {
		t.Fatalf("service registration audit leaked token binding id: %#v", registerAudit)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []struct {
		ServiceID       string         `json:"service_id"`
		HealthStatus    string         `json:"health_status"`
		HeartbeatStale  bool           `json:"heartbeat_stale"`
		HeartbeatAgeSec *int64         `json:"heartbeat_age_sec"`
		Metrics         map[string]any `json:"metrics"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].ServiceID != "worker-01" || health[0].HealthStatus != "unconfigured" || !health[0].HeartbeatStale || health[0].HeartbeatAgeSec != nil {
		t.Fatalf("unexpected pre-heartbeat health: %#v", health)
	}
	if strings.Contains(healthRes.Body.String(), "raw-secret-token") || strings.Contains(healthRes.Body.String(), "password@") || strings.Contains(healthRes.Body.String(), "super-secret-token") || strings.Contains(healthRes.Body.String(), "folder-secret-id") || strings.Contains(healthRes.Body.String(), "webhook_url") || strings.Contains(healthRes.Body.String(), "access_token") || strings.Contains(healthRes.Body.String(), "folder_id") || strings.Contains(healthRes.Body.String(), `"token_id"`) || strings.Contains(healthRes.Body.String(), token.ID) {
		t.Fatalf("service capabilities leaked secret-like values: %s", healthRes.Body.String())
	}
	if health[0].Capabilities["overlay"] != true {
		t.Fatalf("safe capability was not preserved: %#v", health[0].Capabilities)
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","metrics":{"discord.audio_forward_active":1,"discord.audio_forwarded_total":4,"last_forward_error":"secret should not persist"}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	if strings.Contains(heartbeatRes.Body.String(), `"token_id"`) || strings.Contains(heartbeatRes.Body.String(), token.ID) {
		t.Fatalf("heartbeat response leaked token binding id: %s", heartbeatRes.Body.String())
	}
	healthReq = httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes = httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health after heartbeat status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	health = nil
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].HealthStatus != "healthy" || health[0].HeartbeatStale || health[0].HeartbeatAgeSec == nil {
		t.Fatalf("unexpected post-heartbeat health: %#v", health)
	}
	if health[0].Metrics["discord.audio_forward_active"] == nil || health[0].Metrics["discord.audio_forwarded_total"] == nil {
		t.Fatalf("heartbeat metrics were not returned: %#v", health[0].Metrics)
	}
	if _, ok := health[0].Metrics["last_forward_error"]; ok || strings.Contains(healthRes.Body.String(), "secret should not persist") || strings.Contains(healthRes.Body.String(), `"token_id"`) || strings.Contains(healthRes.Body.String(), token.ID) {
		t.Fatalf("string heartbeat metric should not be persisted: %s", healthRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), token.RawToken) {
		t.Fatal("raw service token leaked in list response")
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api-tokens/"+token.ID, nil)
	revokeReq.AddCookie(cookie)
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeRes := httptest.NewRecorder()
	handler.ServeHTTP(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", revokeRes.Code, revokeRes.Body.String())
	}

	heartbeatReq = httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes = httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusUnauthorized {
		t.Fatalf("revoked heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
}

func TestCreateServiceTokenCanPrecreateService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register","service.heartbeat"],"service_id":"encoder-01","service_name":"Encoder 01","public_url":"https://encoder.example.com","version":"0.1.0","capabilities":{"rtmps":true,"stream_key":"must-not-persist"}}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.RawToken == "" {
		t.Fatal("expected one-time raw service token")
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []struct {
		ServiceID    string         `json:"service_id"`
		ServiceType  string         `json:"service_type"`
		ServiceName  string         `json:"service_name"`
		Status       string         `json:"status"`
		HealthStatus string         `json:"health_status"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].ServiceID != "encoder-01" || health[0].Status != "pending" || health[0].HealthStatus != "unconfigured" {
		t.Fatalf("unexpected precreated service health: %#v", health)
	}
	if health[0].Capabilities["rtmps"] != true || strings.Contains(healthRes.Body.String(), "must-not-persist") || strings.Contains(healthRes.Body.String(), "stream_key") {
		t.Fatalf("service capabilities leaked secret-like values: %s", healthRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-01","service_type":"encoder_recorder","service_name":"Encoder 01 Live","public_url":"https://encoder-live.example.com","version":"0.1.1","capabilities":{"rtmps":true}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var registered store.RegisteredService
	if err := json.NewDecoder(registerRes.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.Status != "registered" || registered.PublicURL != "https://encoder-live.example.com" {
		t.Fatalf("unexpected registered service: %s", formatSafeHTTPSensitiveDiagnostic(registered))
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register"],"service_id":"encoder-01","service_name":"Duplicate","public_url":"https://duplicate.example.com"}`))
	duplicateReq.AddCookie(cookie)
	duplicateReq.Header.Set("X-CSRF-Token", csrf)
	duplicateRes := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusConflict {
		t.Fatalf("duplicate precreate status = %d body = %s", duplicateRes.Code, duplicateRes.Body.String())
	}
}
