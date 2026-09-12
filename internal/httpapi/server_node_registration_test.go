package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
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

func TestRotateServiceTokenRebindsPrecreatedService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "api_tokens.read", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createTokenReq := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"encoder_recorder","scopes":["service.register","service.heartbeat"],"service_id":"encoder-rotate","service_name":"Encoder Rotate","public_url":"https://encoder.example.com","version":"0.1.0"}`))
	createTokenReq.AddCookie(cookie)
	createTokenReq.Header.Set("X-CSRF-Token", csrf)
	createTokenRes := httptest.NewRecorder()
	handler.ServeHTTP(createTokenRes, createTokenReq)
	if createTokenRes.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", createTokenRes.Code, createTokenRes.Body.String())
	}
	var oldToken store.ServiceToken
	if err := json.NewDecoder(createTokenRes.Body).Decode(&oldToken); err != nil {
		t.Fatal(err)
	}
	if oldToken.RawToken == "" {
		t.Fatal("expected one-time raw token")
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/api-tokens/"+oldToken.ID+"/rotate", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusCreated {
		t.Fatalf("rotate token status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	if got := rotateRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("rotated one-time token response should be no-store, got %q", got)
	}
	var newToken store.ServiceToken
	if err := json.NewDecoder(rotateRes.Body).Decode(&newToken); err != nil {
		t.Fatal(err)
	}
	if newToken.ID == oldToken.ID || newToken.RawToken == "" || newToken.RawToken == oldToken.RawToken || newToken.TokenHash != "" {
		t.Fatalf("unexpected rotated token response: old=%s new=%s", formatSafeHTTPSensitiveDiagnostic(oldToken), formatSafeHTTPSensitiveDiagnostic(newToken))
	}
	if strings.Contains(rotateRes.Body.String(), oldToken.RawToken) {
		t.Fatal("old raw token leaked in rotate response")
	}

	oldRegisterReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-rotate","service_type":"encoder_recorder","service_name":"Old Token","public_url":"https://old.example.com","version":"0.1.1","capabilities":{}}`))
	oldRegisterReq.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
	oldRegisterRes := httptest.NewRecorder()
	handler.ServeHTTP(oldRegisterRes, oldRegisterReq)
	if oldRegisterRes.Code != http.StatusUnauthorized {
		t.Fatalf("old token register status = %d body = %s", oldRegisterRes.Code, oldRegisterRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"encoder-rotate","service_type":"encoder_recorder","service_name":"Encoder Rotate Live","public_url":"https://encoder-live.example.com","version":"0.1.2","capabilities":{"rtmps":true}}`))
	registerReq.Header.Set("Authorization", "Bearer "+newToken.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("new token register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var registered store.RegisteredService
	if err := json.NewDecoder(registerRes.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(registerRes.Body.String(), `"token_id"`) || strings.Contains(registerRes.Body.String(), newToken.ID) {
		t.Fatalf("service registration response leaked rotated token binding id: %s", registerRes.Body.String())
	}
	storedService, err := auth.GetService(t.Context(), "encoder-rotate")
	if err != nil {
		t.Fatal(err)
	}
	if storedService.TokenID != newToken.ID || storedService.Status != "registered" {
		t.Fatalf("service was not rebound to rotated token: %s", formatSafeHTTPSensitiveDiagnostic(storedService))
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), oldToken.RawToken) || strings.Contains(listRes.Body.String(), newToken.RawToken) {
		t.Fatalf("raw token leaked in list response: %s", listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	var oldRevoked, newActive bool
	for _, token := range tokens {
		if token.ID == oldToken.ID && token.RevokedAt != nil {
			oldRevoked = true
		}
		if token.ID == newToken.ID && token.RevokedAt == nil {
			newActive = true
		}
	}
	if !oldRevoked || !newActive {
		t.Fatalf("unexpected token rotation list state: %s", formatSafeHTTPSensitiveDiagnostic(tokens))
	}
}

func TestRotateServiceTokenRequiresRevokePermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-no-rotate", []string{"service.register"})

	rotateReq := httptest.NewRequest(http.MethodPost, "/api-tokens/"+token.ID+"/rotate", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusForbidden {
		t.Fatalf("rotate without revoke permission status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-no-rotate","service_type":"worker","service_name":"Worker","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("token should remain active after denied rotate, status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
}

func TestRotateServiceTokenRejectsScopeEscalation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "rotator", Roles: []string{"token_rotator"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "rotator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens/"+token.ID+"/rotate", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_escalation") {
		t.Fatalf("scope escalation rotation status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), token.RawToken, "service.secret.resolve"); err != nil {
		t.Fatalf("denied rotation must leave the original token active: %v", err)
	}
}

func TestGenericServiceTokenMutationsRejectPanelManagedNodeToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{ServiceID: "worker-managed", ServiceType: "worker", ServiceName: "Managed Worker", Host: "worker.example.com", Port: 8443, SSLEnabled: true, PublicURL: "https://worker.example.com:8443"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.SetServiceNodeTokenSecret(t.Context(), service.ServiceID, "ciphertext", "nonce"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens/"+token.ID+"/rotate", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "node_runtime_token_requires_node_rotation") {
		t.Fatalf("managed node generic rotation status = %d body = %s", res.Code, res.Body.String())
	}
	revokeReq := httptest.NewRequest(http.MethodDelete, "/api-tokens/"+token.ID, nil)
	revokeReq.AddCookie(cookie)
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeRes := httptest.NewRecorder()
	handler.ServeHTTP(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusConflict || !strings.Contains(revokeRes.Body.String(), "node_runtime_token_requires_node_deletion") {
		t.Fatalf("managed node generic revoke status = %d body = %s", revokeRes.Code, revokeRes.Body.String())
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), token.RawToken, "service.heartbeat"); err != nil {
		t.Fatalf("rejected generic mutation must leave the node token active: %v", err)
	}
}

func TestCreateServiceTokenPrecreateRequiresRegisterScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.heartbeat"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "service_register_scope_required") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("precreate without register scope should not create token: %s", formatSafeHTTPSensitiveDiagnostic(tokens))
	}
}

func TestCreateServiceTokenRejectsEmptyScopes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":[]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("empty scope request should not create token: %s", formatSafeHTTPSensitiveDiagnostic(tokens))
	}
}

func TestCreateNodeRegistrationTokenPrecreatesNode(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read", "service_health.read", "audit_logs.read", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true,"description":"Studio worker node","version":"must-not-persist","capabilities":{"runtime_config":true,"token":"must-redact"}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create node token status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("node registration token response should be no-store, got %q", got)
	}
	var body struct {
		ID                string                  `json:"id"`
		Token             string                  `json:"token"`
		ConfigureToken    string                  `json:"configure_token"`
		RuntimeToken      string                  `json:"runtime_token"`
		NodeType          string                  `json:"node_type"`
		Scopes            []string                `json:"scopes"`
		ConfigureCommand  string                  `json:"configure_command"`
		ConfigurationYAML string                  `json:"configuration_yaml"`
		SystemdUnit       string                  `json:"systemd_unit"`
		Node              store.RegisteredService `json:"node"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" || body.ID == "" || body.Node.ServiceID != "studio-worker-01" || body.NodeType != "worker" {
		t.Fatalf("unexpected node registration response: %s", formatSafeHTTPSensitiveDiagnostic(body))
	}
	if body.ConfigureToken == "" || body.ConfigureToken != body.Token || body.RuntimeToken == "" {
		t.Fatalf("expected one-time configure token and runtime token: %s", formatSafeHTTPSensitiveDiagnostic(body))
	}
	if body.Node.PublicURL != "https://worker.example.com:8443" || body.Node.Host != "worker.example.com" || body.Node.Port != 8443 || !body.Node.SSLEnabled {
		t.Fatalf("node endpoint was not built from host/port/ssl: %#v", body.Node)
	}
	if body.Node.Version != "" || body.Node.ReportedVersion != "" || len(body.Node.Capabilities) != 0 || len(body.Node.ReportedCapabilities) != 0 {
		t.Fatalf("manual version/capabilities must not be stored during node creation: %#v", body.Node)
	}
	expectedConfigureCommand := `sudo autostream-worker configure --panel-url 'http://example.com' --token ` + posixShellQuote(body.ConfigureToken) + ` --node 'studio-worker-01' --config '/etc/autostream-worker/config.yml'`
	if body.ConfigureCommand != expectedConfigureCommand {
		t.Fatalf("missing configure command fields: %s", body.ConfigureCommand)
	}
	legacyNodeBinary := "sudo autostream-" + "node"
	legacyNodePath := "/usr/local/bin/autostream-" + "node"
	if strings.Contains(body.ConfigureCommand, "command -v") || strings.Contains(body.ConfigureCommand, "$bin") || strings.Contains(body.ConfigureCommand, "/usr/local/bin/worker") || strings.Contains(body.ConfigureCommand, legacyNodeBinary) || strings.Contains(body.ConfigureCommand, legacyNodePath) || strings.Contains(body.ConfigureCommand, "config_path=") || body.SystemdUnit != "" {
		t.Fatalf("node registration should not reference a non-existent shared node binary or systemd unit: command=%s unit=%s", body.ConfigureCommand, body.SystemdUnit)
	}
	if !strings.Contains(body.ConfigurationYAML, `ssl_enabled: true`) || !strings.Contains(body.ConfigurationYAML, body.RuntimeToken) || !strings.Contains(body.ConfigurationYAML, `host: "worker.example.com"`) {
		t.Fatalf("configuration yaml missing node agent settings: %s", body.ConfigurationYAML)
	}
	if !strings.Contains(body.ConfigurationYAML, `stream_ingest:`) || !strings.Contains(body.ConfigurationYAML, `signing_key: "test-stream-ingest-signing-key-32-bytes"`) {
		t.Fatalf("configuration yaml missing stream ingest signing key: %s", body.ConfigurationYAML)
	}
	legacyNodeName := "autostream-" + "node"
	if strings.Contains(body.ConfigureCommand, legacyNodeName) || strings.Contains(body.ConfigurationYAML, legacyNodeName) || !strings.Contains(body.ConfigurationYAML, `/var/lib/autostream/worker`) {
		t.Fatalf("node configuration should use service-specific paths: command=%s yaml=%s", body.ConfigureCommand, body.ConfigurationYAML)
	}
	if !stringSliceContains(body.Scopes, "service.register") || !stringSliceContains(body.Scopes, "worker.events.write") || stringSliceContains(body.Scopes, "service.secret.resolve") {
		t.Fatalf("unexpected default node scopes: %#v", body.Scopes)
	}
	if _, ok := body.Node.Capabilities["token"]; ok || strings.Contains(res.Body.String(), "must-redact") || strings.Contains(res.Body.String(), `"token_id"`) {
		t.Fatalf("node response leaked secret-like capability or token binding: %s", res.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK || !strings.Contains(healthRes.Body.String(), "studio-worker-01") {
		t.Fatalf("service health missing precreated node: status=%d body=%s", healthRes.Code, healthRes.Body.String())
	}
	if strings.Contains(healthRes.Body.String(), body.Token) || strings.Contains(healthRes.Body.String(), body.ID) {
		t.Fatalf("service health leaked node registration token material: %s", healthRes.Body.String())
	}

	configureReq := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+body.ConfigureToken+`"}`))
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusOK {
		t.Fatalf("node configure status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		Config struct {
			Auth struct {
				TokenID string `json:"token_id"`
				Token   string `json:"token"`
			} `json:"auth"`
			StreamIngest struct {
				SigningKey string `json:"signing_key"`
			} `json:"stream_ingest"`
		} `json:"config"`
		ConfigYML string `json:"config_yml"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}
	if configureBody.Config.Auth.TokenID == "" || configureBody.Config.Auth.Token == "" || configureBody.Config.Auth.Token == body.RuntimeToken || !strings.Contains(configureBody.ConfigYML, configureBody.Config.Auth.Token) {
		t.Fatalf("configure endpoint did not rotate and return runtime token once: %#v", configureBody)
	}
	if configureBody.Config.StreamIngest.SigningKey != "test-stream-ingest-signing-key-32-bytes" || !strings.Contains(configureBody.ConfigYML, `signing_key: "test-stream-ingest-signing-key-32-bytes"`) {
		t.Fatalf("configure endpoint did not return the stream ingest signing key: %#v", configureBody)
	}
	configuredNode, err := auth.GetService(t.Context(), "studio-worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if configuredNode.Status != "registered" {
		t.Fatalf("configure should move pending node to registered, got %s", formatSafeHTTPSensitiveDiagnostic(configuredNode))
	}
	reuseReq := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+body.ConfigureToken+`"}`))
	reuseRes := httptest.NewRecorder()
	handler.ServeHTTP(reuseRes, reuseReq)
	if reuseRes.Code != http.StatusUnauthorized {
		t.Fatalf("configure token reuse status = %d body = %s", reuseRes.Code, reuseRes.Body.String())
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/api/node-agent/heartbeat", bytes.NewBufferString(`{"nodeId":"studio-worker-01","status":"online","version":"1.4.2","capabilities":{"streaming":true,"archive_upload":true},"hostname":"studio-worker-01","os":"linux","arch":"amd64","api":{"host":"worker.example.com","port":8443,"sslEnabled":true},"metrics":{"cpuUsage":12.5,"runningJobs":2}}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+configureBody.Config.Auth.Token)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("node heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	var heartbeatService store.RegisteredService
	if err := json.NewDecoder(heartbeatRes.Body).Decode(&heartbeatService); err != nil {
		t.Fatal(err)
	}
	if heartbeatService.ReportedVersion != "1.4.2" || heartbeatService.ReportedOS != "linux" || heartbeatService.ReportedArch != "amd64" || heartbeatService.ReportedCapabilities["streaming"] != true {
		t.Fatalf("node reported fields were not saved: %s", formatSafeHTTPSensitiveDiagnostic(heartbeatService))
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action=nodes.registration_token.create", nil)
	auditReq.AddCookie(cookie)
	auditRes := httptest.NewRecorder()
	handler.ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK {
		t.Fatalf("audit log status = %d body = %s", auditRes.Code, auditRes.Body.String())
	}
	auditBody := auditRes.Body.String()
	if !strings.Contains(auditBody, "nodes.registration_token.create") || !strings.Contains(auditBody, "studio-worker-01") {
		t.Fatalf("audit log missing node registration event: %s", auditBody)
	}
	if strings.Contains(auditBody, body.Token) || strings.Contains(auditBody, body.ID) {
		t.Fatalf("audit log leaked node registration token material: %s", auditBody)
	}
	if strings.Contains(auditBody, `"token_id"`) && !strings.Contains(auditBody, `"\u003credacted\u003e"`) {
		t.Fatalf("audit log token binding was not redacted: %s", auditBody)
	}
}

func TestCreateNodeRegistrationTokenSupportsEndpointlessPullAgent(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "secrets.update", "system_updates.execute"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"update_agent","node_id":"host-agent-a","name":"Host Agent A","transport_mode":"pull_v2","execution_host_id":"host-a"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create endpointless pull agent status = %d body = %s", res.Code, res.Body.String())
	}

	var body struct {
		Node              store.RegisteredService `json:"node"`
		RuntimeToken      string                  `json:"runtime_token"`
		ConfigureToken    string                  `json:"configure_token"`
		ConfigureCommand  string                  `json:"configure_command"`
		ConfigurationPath string                  `json:"configuration_path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Node.TransportMode != "pull_v2" || body.Node.ExecutionHostID != "host-a" || body.Node.OwnershipEpoch != 0 {
		t.Fatalf("unexpected pull agent binding: %#v", body.Node)
	}
	if body.Node.Host != "" || body.Node.Port != 0 || body.Node.PublicURL != "" || body.Node.AppliedEndpoint != nil {
		t.Fatalf("pull agent unexpectedly has an inbound endpoint: %#v", body.Node)
	}
	wantConfigureCommand := "sudo /usr/local/bin/autostream-host-agent configure --panel-url 'http://example.com' --node 'host-agent-a' --config '/etc/autostream/updater/agent.yaml'"
	if body.ConfigureCommand != wantConfigureCommand ||
		body.ConfigurationPath != "/etc/autostream/updater/agent.yaml" {
		t.Fatalf("pull agent configure metadata = %s", formatSafeHTTPSensitiveDiagnostic(body))
	}
	for _, forbidden := range []string{`"host":`, `"port":`, `"public_url":`} {
		if strings.Contains(res.Body.String(), forbidden) {
			t.Fatalf("endpointless pull agent response contains %s: %s", forbidden, res.Body.String())
		}
	}

	for name, payload := range map[string]string{
		"execution host value":  `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","execution_host_id":"host-a","version":"v2.0.0"}`,
		"execution host empty":  `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","execution_host_id":"","version":"v2.0.0"}`,
		"execution host null":   `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","execution_host_id":null,"version":"v2.0.0"}`,
		"ownership epoch value": `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","ownership_epoch":1,"version":"v2.0.0"}`,
		"ownership epoch zero":  `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","ownership_epoch":0,"version":"v2.0.0"}`,
		"ownership epoch null":  `{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","ownership_epoch":null,"version":"v2.0.0"}`,
	} {
		registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(payload))
		registerReq.Header.Set("Authorization", "Bearer "+body.RuntimeToken)
		registerRes := httptest.NewRecorder()
		handler.ServeHTTP(registerRes, registerReq)
		if registerRes.Code != http.StatusBadRequest ||
			!strings.Contains(registerRes.Body.String(), `"code":"invalid_service_registration"`) {
			t.Fatalf("runtime pull %s self-claim status = %d body = %s", name, registerRes.Code, registerRes.Body.String())
		}
	}
	pending, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != "pending" ||
		pending.ExecutionHostID != "host-a" ||
		pending.OwnershipEpoch != 0 {
		t.Fatalf("runtime binding self-claim mutated precreated service: %s", formatSafeHTTPSensitiveDiagnostic(pending))
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"host-agent-a","service_type":"update_agent","service_name":"Host Agent A","transport_mode":"pull_v2","version":"v2.0.0","capabilities":{"agent_protocol_version":2,"observe_only":true}}`))
	registerReq.Header.Set("Authorization", "Bearer "+body.RuntimeToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusAccepted {
		t.Fatalf("register endpointless pull agent status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var registered store.RegisteredService
	if err := json.NewDecoder(registerRes.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.TransportMode != "pull_v2" || registered.ExecutionHostID != "host-a" || registered.OwnershipEpoch != 0 {
		t.Fatalf("server-owned pull binding was not restored: %s", formatSafeHTTPSensitiveDiagnostic(registered))
	}
	if registered.Host != "" || registered.Port != 0 || registered.PublicURL != "" {
		t.Fatalf("endpointless registration gained an inbound endpoint: %s", formatSafeHTTPSensitiveDiagnostic(registered))
	}

	configurationReq := httptest.NewRequest(http.MethodGet, "/nodes/host-agent-a/configuration", nil)
	configurationReq.AddCookie(cookie)
	configurationRes := httptest.NewRecorder()
	handler.ServeHTTP(configurationRes, configurationReq)
	if configurationRes.Code != http.StatusOK {
		t.Fatalf("get pull agent configuration status = %d body = %s", configurationRes.Code, configurationRes.Body.String())
	}
	var configurationBody map[string]json.RawMessage
	if err := json.Unmarshal(configurationRes.Body.Bytes(), &configurationBody); err != nil {
		t.Fatal(err)
	}
	if _, exists := configurationBody["node_api_url"]; exists {
		t.Fatalf("pull agent configuration exposed an inbound API URL: %s", configurationRes.Body.String())
	}
	var configurationPath string
	if err := json.Unmarshal(configurationBody["configuration_path"], &configurationPath); err != nil {
		t.Fatal(err)
	}
	if configurationPath != "/etc/autostream/updater/agent.yaml" {
		t.Fatalf("pull agent configuration path = %q", configurationPath)
	}

	regenerateReq := httptest.NewRequest(http.MethodPost, "/nodes/host-agent-a/configure-token", nil)
	regenerateReq.AddCookie(cookie)
	regenerateReq.Header.Set("X-CSRF-Token", csrf)
	regenerateRes := httptest.NewRecorder()
	handler.ServeHTTP(regenerateRes, regenerateReq)
	if regenerateRes.Code != http.StatusCreated {
		t.Fatalf("regenerate pull configure token status = %d body = %s", regenerateRes.Code, regenerateRes.Body.String())
	}
	var regenerated struct {
		ConfigureToken    string `json:"configure_token"`
		ConfigureCommand  string `json:"configure_command"`
		ConfigurationPath string `json:"configuration_path"`
	}
	if err := json.NewDecoder(regenerateRes.Body).Decode(&regenerated); err != nil {
		t.Fatal(err)
	}
	if regenerated.ConfigureToken == "" ||
		regenerated.ConfigureCommand != wantConfigureCommand ||
		regenerated.ConfigurationPath != "/etc/autostream/updater/agent.yaml" {
		t.Fatalf("regenerated pull configure metadata = %#v", regenerated)
	}

	stageReq := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/runtime-identity/stage",
		strings.NewReader(`{"nodeId":"host-agent-a","configureToken":"`+regenerated.ConfigureToken+`"}`),
	)
	stageRes := httptest.NewRecorder()
	handler.ServeHTTP(stageRes, stageReq)
	if stageRes.Code != http.StatusConflict ||
		!strings.Contains(stageRes.Body.String(), `"code":"host_agent_configure_protocol_required"`) {
		t.Fatalf(
			"legacy pull configure did not fail closed: status=%d body=%s",
			stageRes.Code,
			stageRes.Body.String(),
		)
	}
	unconsumed, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if unconsumed.ConfigureTokenUsedAt != nil || unconsumed.StagedNodeTokenID != "" {
		t.Fatalf("protocol rejection consumed or staged credentials: %s", formatSafeHTTPSensitiveDiagnostic(unconsumed))
	}

	beforeRotation, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	beforeRotationTokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rotateReq := httptest.NewRequest(http.MethodPost, "/nodes/host-agent-a/rotate-token", nil)
	rotateReq.AddCookie(cookie)
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusConflict ||
		!strings.Contains(rotateRes.Body.String(), `"code":"staged_runtime_token_rotation_required"`) {
		t.Fatalf("rotate pull runtime token status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	afterRotation, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	afterRotationTokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if afterRotation.TokenID != beforeRotation.TokenID ||
		afterRotation.StagedNodeTokenID != beforeRotation.StagedNodeTokenID ||
		afterRotation.NodeTokenRotatedAt == nil ||
		beforeRotation.NodeTokenRotatedAt == nil ||
		!afterRotation.NodeTokenRotatedAt.Equal(*beforeRotation.NodeTokenRotatedAt) {
		t.Fatalf("rejected pull runtime rotation mutated service: before=%s after=%s", formatSafeHTTPSensitiveDiagnostic(beforeRotation), formatSafeHTTPSensitiveDiagnostic(afterRotation))
	}
	if len(afterRotationTokens) != len(beforeRotationTokens) {
		t.Fatalf("rejected pull runtime rotation changed token count: before=%d after=%d", len(beforeRotationTokens), len(afterRotationTokens))
	}

	editReq := httptest.NewRequest(http.MethodPut, "/nodes/host-agent-a", bytes.NewBufferString(`{"service_name":"Host Agent A Updated","description":"Portless agent"}`))
	editReq.AddCookie(cookie)
	editReq.Header.Set("X-CSRF-Token", csrf)
	editRes := httptest.NewRecorder()
	handler.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("edit endpointless pull agent status = %d body = %s", editRes.Code, editRes.Body.String())
	}
	if strings.Contains(editRes.Body.String(), `"host":`) || strings.Contains(editRes.Body.String(), `"port":`) {
		t.Fatalf("editing endpointless pull agent added an endpoint: %s", editRes.Body.String())
	}
}

func TestCreateNodeRegistrationTokenRejectsEndpointOnPullAgent(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "secrets.update", "system_updates.execute"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"update_agent","node_id":"host-agent-a","name":"Host Agent A","transport_mode":"pull_v2","execution_host_id":"host-a","host":"127.0.0.1","port":8090}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_node_endpoint") {
		t.Fatalf("pull agent with endpoint status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCreateNodeRegistrationTokenRejectsPullBindingSelfClaim(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "secrets.update", "system_updates.execute"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for name, payload := range map[string]string{
		"missing execution host": `{"node_type":"update_agent","node_id":"host-agent-missing-host","name":"Host Agent","transport_mode":"pull_v2"}`,
		"ownership epoch":        `{"node_type":"update_agent","node_id":"host-agent-owned","name":"Host Agent","transport_mode":"pull_v2","execution_host_id":"host-a","ownership_epoch":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(payload))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest ||
				!strings.Contains(res.Body.String(), `"code":"invalid_node_registration"`) {
				t.Fatalf("pull binding self-claim status = %d body = %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestNodeConfigureCommandUsesPOSIXShellQuoting(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com/it's/$(touch pwn)")
	command := nodeConfigureCommand(
		nil,
		store.RegisteredService{
			ServiceID:     "updater'$(touch pwn)`id`",
			ServiceType:   "update_agent",
			TransportMode: store.SystemUpdateTransportPullV2,
		},
		"ast_cfg_secret'$(touch token)",
		"/etc/autostream/updater'config.json",
	)
	for _, quoted := range []string{
		posixShellQuote("https://panel.example.com/it's/$(touch pwn)"),
		posixShellQuote("updater'$(touch pwn)`id`"),
		posixShellQuote("/etc/autostream/updater'config.json"),
	} {
		if !strings.Contains(command, quoted) {
			t.Fatalf("configure command omitted shell-safe argument %q: %s", quoted, command)
		}
	}
	if strings.Contains(command, `"$(touch`) || strings.Contains(command, "\"`id`\"") {
		t.Fatalf("configure command left command substitution in double quotes: %s", command)
	}
	if strings.Contains(command, "ast_cfg_secret") || strings.Contains(command, "--token") {
		t.Fatalf("updater configure command exposed the Configure Token in argv: %s", command)
	}
}

func TestNodeConfigurationYAMLScopesSigningKeyToOneTimeWorkerEncoderConfigs(t *testing.T) {
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	request := httptest.NewRequest(http.MethodGet, "https://control.example.jp/nodes", nil)
	worker := store.RegisteredService{ServiceID: "worker-01", ServiceName: "Worker 01", ServiceType: "worker", Host: "worker.example.jp", Port: 8443, SSLEnabled: true}

	oneTime := nodeConfigurationYAML(request, worker, "token-id", "runtime-token")
	if !strings.Contains(oneTime, `stream_ingest:`) || !strings.Contains(oneTime, `signing_key: "test-stream-ingest-signing-key-32-bytes"`) {
		t.Fatalf("one-time worker config omitted signing key: %s", oneTime)
	}
	redacted := nodeConfigurationYAML(request, worker, "token-id", "")
	if strings.Contains(redacted, "test-stream-ingest-signing-key-32-bytes") || strings.Contains(redacted, "stream_ingest") {
		t.Fatalf("normal node config leaked signing key: %s", redacted)
	}
	discord := worker
	discord.ServiceType = "discord_bot"
	discordConfig := nodeConfigurationYAML(request, discord, "token-id", "runtime-token")
	if strings.Contains(discordConfig, "test-stream-ingest-signing-key-32-bytes") || strings.Contains(discordConfig, "stream_ingest") {
		t.Fatalf("discord config received an unrelated signing key: %s", discordConfig)
	}
}

func TestNodeAgentConfigureRejectsMissingEncryptionBeforeConsumingConfigureToken(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "studio-worker-01",
		ServiceType: "worker",
		ServiceName: "Studio Worker 01",
		Host:        "worker.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	configureToken := "ast_cfg_test_runtime_report"
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"studio-worker-01","configureToken":"`+configureToken+`","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"studio-worker-01","os":"linux","arch":"amd64"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "store_node_runtime_token_failed") {
		t.Fatalf("configure without encryption key status = %d body = %s", res.Code, res.Body.String())
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.ReportedVersion != "" || got.ReportedCommit != "" || got.ReportedBuildDate != "" || got.ReportedHostname != "" || got.ReportedOS != "" || got.ReportedArch != "" {
		t.Fatalf("configure must not mutate the runtime report before encryption is available: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
	if got.TokenID != token.ID {
		t.Fatalf("runtime token should not rotate before encryption is available: old=%s got=%s", token.ID, got.TokenID)
	}
	if got.ConfigureTokenUsedAt != nil {
		t.Fatalf("configure token must remain usable after prerequisite failure: %s", formatSafeHTTPSensitiveDiagnostic(got))
	}
}

func TestNodeRuntimeTokenEncryptionKeyRejectsWeakValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "short", key: "short-key"},
		{name: "placeholder", key: "<CHANGE_ME_32_BYTES>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", tc.key)
			if _, err := nodeRuntimeTokenEncryptionKey(); err == nil {
				t.Fatalf("weak encryption key %q should be rejected", tc.key)
			}
		})
	}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	if key, err := nodeRuntimeTokenEncryptionKey(); err != nil || key == "" {
		t.Fatalf("strong encryption key should be accepted: key=%q err=%v", key, err)
	}
}

func TestNodeRuntimeTokenDecryptRequiresValidatedEncryptionKey(t *testing.T) {
	const weakKey = "short-key"
	ciphertext, nonce, err := security.EncryptSecret("runtime-token", weakKey)
	if err != nil {
		t.Fatal(err)
	}
	service := store.RegisteredService{NodeTokenCiphertext: ciphertext, NodeTokenNonce: nonce}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", weakKey)
	if _, err := nodeRuntimeToken(service); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("weak decryption key should be rejected, got %v", err)
	}

	strongKey := "test-secret-encryption-key-32-bytes"
	ciphertext, nonce, err = security.EncryptSecret("runtime-token", strongKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", strongKey)
	if got, err := nodeRuntimeToken(store.RegisteredService{NodeTokenCiphertext: ciphertext, NodeTokenNonce: nonce}); err != nil || got != "runtime-token" {
		t.Fatalf("runtime token decrypt = %q, err=%v", got, err)
	}
}

func TestNodeAgentConfigurePersistsRuntimeReportForSupportedNodeTypes(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	for _, serviceType := range []string{"worker", "encoder_recorder", "discord_bot", "observability"} {
		t.Run(serviceType, func(t *testing.T) {
			token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.config.read"})
			if err != nil {
				t.Fatal(err)
			}
			serviceID := strings.ReplaceAll(serviceType, "_", "-") + "-01"
			service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
				ServiceID:   serviceID,
				ServiceType: serviceType,
				ServiceName: serviceType + " Node 01",
				Host:        serviceID + ".example.com",
				Port:        8443,
				SSLEnabled:  true,
				PublicURL:   "https://" + serviceID + ".example.com:8443",
			})
			if err != nil {
				t.Fatal(err)
			}
			configureToken := "ast_cfg_test_" + strings.ReplaceAll(serviceType, "_", "-")
			if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			payload := `{"nodeId":"` + service.ServiceID + `","configureToken":"` + configureToken + `","version":"1.4.1","commit":"abc1234","build_date":"2026-07-09T00:00:00Z","hostname":"` + service.ServiceID + `","os":"linux","arch":"amd64"}`
			req := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(payload))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("configure node status = %d body = %s", res.Code, res.Body.String())
			}
			got, err := auth.GetService(t.Context(), service.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "registered" || got.ReportedVersion != "1.4.1" || got.ReportedCommit != "abc1234" || got.ReportedBuildDate != "2026-07-09T00:00:00Z" || got.ReportedHostname != service.ServiceID || got.ReportedOS != "linux" || got.ReportedArch != "amd64" || got.LastReportedAt == nil {
				t.Fatalf("configure runtime report was not persisted for %s: %s", serviceType, formatSafeHTTPSensitiveDiagnostic(got))
			}
			if got.ConfigureTokenUsedAt == nil {
				t.Fatalf("configure token was not marked used for %s: %s", serviceType, formatSafeHTTPSensitiveDiagnostic(got))
			}
		})
	}
}

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

func TestCreateNodeRegistrationTokenReportsBlockedEndpoint(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"studio-worker-01","name":"Studio Worker 01","host":"worker.example.jp","port":8443,"ssl_enabled":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("blocked node endpoint status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "node_endpoint_blocked") {
		t.Fatalf("blocked endpoint should return actionable code: %s", res.Body.String())
	}
}

func TestNodeRegistrationScopesIncludeUpdateAgentClaimContract(t *testing.T) {
	scopes := nodeRegistrationScopes("update_agent", false, false)
	if !validUpdateAgentServiceTokenScopes("update_agent", scopes) {
		t.Fatalf("update agent node registration scopes do not satisfy the claim contract: %#v", scopes)
	}
	for _, required := range []string{"service.register", "service.heartbeat"} {
		if !stringSliceContains(scopes, required) {
			t.Fatalf("update agent node registration omitted %s: %#v", required, scopes)
		}
	}
}

func TestCreateWorkerOrEncoderNodeRequiresStreamIngestSigningKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		code string
	}{
		{name: "missing", code: "stream_ingest_signing_key_required"},
		{name: "short", key: "short-signing-key", code: "stream_ingest_signing_key_invalid"},
		{name: "placeholder", key: "<CHANGE_ME_64_BYTES>", code: "stream_ingest_signing_key_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
			t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", tc.key)
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read", "secrets.update"}); err != nil {
				t.Fatal(err)
			}
			handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
			cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

			for _, serviceType := range []string{"worker", "encoder_recorder"} {
				req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"`+serviceType+`","node_id":"`+serviceType+`-01","name":"Node 01","host":"node.example.com","port":8443,"ssl_enabled":true}`))
				req.AddCookie(cookie)
				req.Header.Set("X-CSRF-Token", csrf)
				res := httptest.NewRecorder()
				handler.ServeHTTP(res, req)
				if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), tc.code) {
					t.Fatalf("%s with %s signing key status = %d body = %s", serviceType, tc.name, res.Code, res.Body.String())
				}
			}

			listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
			listReq.AddCookie(cookie)
			listRes := httptest.NewRecorder()
			handler.ServeHTTP(listRes, listReq)
			if listRes.Code != http.StatusOK {
				t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
			}
			var tokens []store.ServiceToken
			if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 0 {
				t.Fatalf("invalid signing key must fail before creating a token: %s", formatSafeHTTPSensitiveDiagnostic(tokens))
			}
		})
	}
}

func TestCreateNodeRegistrationTokenRejectsSecretScopeEscalation(t *testing.T) {
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for _, serviceType := range []string{"worker", "encoder_recorder"} {
		req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"`+serviceType+`","node_id":"`+serviceType+`-01","name":"Node 01","public_url":"https://node.example.com","version":"0.1.0"}`))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s secret disclosure escalation status = %d body = %s", serviceType, res.Code, res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "permission_escalation") {
			t.Fatalf("%s unexpected response: %s", serviceType, res.Body.String())
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api-tokens", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list token status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var tokens []store.ServiceToken
	if err := json.NewDecoder(listRes.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("denied node registration should not create token: %s", formatSafeHTTPSensitiveDiagnostic(tokens))
	}
}

func TestValidateNodeConfigurationSecretPermissions(t *testing.T) {
	for _, serviceType := range []string{"worker", "encoder_recorder", "update_agent"} {
		if err := validateNodeConfigurationSecretPermissions([]string{"api_tokens.create"}, serviceType); !errors.Is(err, store.ErrPermissionEscalation) {
			t.Fatalf("%s without secrets.update error = %v", serviceType, err)
		}
		if err := validateNodeConfigurationSecretPermissions([]string{"secrets.update"}, serviceType); err != nil {
			t.Fatalf("%s with secrets.update error = %v", serviceType, err)
		}
	}
	for _, serviceType := range []string{"discord_bot", "observability"} {
		if err := validateNodeConfigurationSecretPermissions([]string{"api_tokens.create"}, serviceType); err != nil {
			t.Fatalf("%s should not require signing-key disclosure permission: %v", serviceType, err)
		}
	}
}

func TestCreateDiscordNodeRegistrationTokenRequiresStartAndStopPermissions(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "start-only", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	limitedCookie, limitedCSRF := loginForTest(t, handler, "limited", "correct horse battery")

	limitedReq := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"discord_bot","node_id":"discord-01","name":"Discord 01","host":"discord.example.com","port":8443,"ssl_enabled":true}`))
	limitedReq.AddCookie(limitedCookie)
	limitedReq.Header.Set("X-CSRF-Token", limitedCSRF)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden || !strings.Contains(limitedRes.Body.String(), "permission_escalation") {
		t.Fatalf("limited discord node status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	startOnlyCookie, startOnlyCSRF := loginForTest(t, handler, "start-only", "correct horse battery")
	startOnlyReq := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"discord_bot","node_id":"discord-start-only","name":"Discord Start Only","host":"discord.example.com","port":8443,"ssl_enabled":true}`))
	startOnlyReq.AddCookie(startOnlyCookie)
	startOnlyReq.Header.Set("X-CSRF-Token", startOnlyCSRF)
	startOnlyRes := httptest.NewRecorder()
	handler.ServeHTTP(startOnlyRes, startOnlyReq)
	if startOnlyRes.Code != http.StatusForbidden || !strings.Contains(startOnlyRes.Body.String(), "permission_escalation") {
		t.Fatalf("start-only discord node status = %d body = %s", startOnlyRes.Code, startOnlyRes.Body.String())
	}

	adminCookie, adminCSRF := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"discord_bot","node_id":"discord-01","name":"Discord 01","host":"discord.example.com","port":8443,"ssl_enabled":true}`))
	req.AddCookie(adminCookie)
	req.Header.Set("X-CSRF-Token", adminCSRF)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("admin discord node status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(body.Scopes, "streams.start") || !stringSliceContains(body.Scopes, "streams.stop") || !stringSliceContains(body.Scopes, "discord.status.write") {
		t.Fatalf("discord node scopes missing paired auto-start/stop permissions: %#v", body.Scopes)
	}
}

func TestLegacyDiscordNodeConfigureRequiresStopPermissionAndUpgradesScope(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "start-only", Roles: []string{"node_operator"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"node_operator"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	legacyToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.heartbeat", "service.config.read", "discord.status.write", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), legacyToken, store.ServiceRegistration{
		ServiceID:   "discord-legacy",
		ServiceType: "discord_bot",
		ServiceName: "Legacy Discord Bot",
		Host:        "discord.example.com",
		Port:        8443,
		SSLEnabled:  true,
		PublicURL:   "https://discord.example.com:8443",
	}); err != nil {
		t.Fatalf("precreate legacy Discord Bot: %v", err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	startOnlyCookie, startOnlyCSRF := loginForTest(t, handler, "start-only", "correct horse battery")
	deniedReq := httptest.NewRequest(http.MethodPost, "/nodes/discord-legacy/configure-token", nil)
	deniedReq.AddCookie(startOnlyCookie)
	deniedReq.Header.Set("X-CSRF-Token", startOnlyCSRF)
	deniedRes := httptest.NewRecorder()
	handler.ServeHTTP(deniedRes, deniedReq)
	if deniedRes.Code != http.StatusForbidden || !strings.Contains(deniedRes.Body.String(), "permission_escalation") {
		t.Fatalf("legacy Discord Configure Token without streams.stop status = %d body = %s", deniedRes.Code, deniedRes.Body.String())
	}
	rotateReq := httptest.NewRequest(http.MethodPost, "/nodes/discord-legacy/rotate-token", nil)
	rotateReq.AddCookie(startOnlyCookie)
	rotateReq.Header.Set("X-CSRF-Token", startOnlyCSRF)
	rotateRes := httptest.NewRecorder()
	handler.ServeHTTP(rotateRes, rotateReq)
	if rotateRes.Code != http.StatusForbidden || !strings.Contains(rotateRes.Body.String(), "permission_escalation") {
		t.Fatalf("legacy Discord Runtime Token rotation without streams.stop status = %d body = %s", rotateRes.Code, rotateRes.Body.String())
	}
	genericRotateReq := httptest.NewRequest(http.MethodPost, "/api-tokens/"+legacyToken.ID+"/rotate", nil)
	genericRotateReq.AddCookie(startOnlyCookie)
	genericRotateReq.Header.Set("X-CSRF-Token", startOnlyCSRF)
	genericRotateRes := httptest.NewRecorder()
	handler.ServeHTTP(genericRotateRes, genericRotateReq)
	if genericRotateRes.Code != http.StatusForbidden || !strings.Contains(genericRotateRes.Body.String(), "permission_escalation") {
		t.Fatalf("legacy Discord generic token rotation without streams.stop status = %d body = %s", genericRotateRes.Code, genericRotateRes.Body.String())
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), legacyToken.RawToken, "streams.start"); err != nil {
		t.Fatalf("denied configuration or rotation must leave legacy token active: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), legacyToken.RawToken, "streams.stop"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("legacy token unexpectedly gained streams.stop: %v", err)
	}

	operatorCookie, operatorCSRF := loginForTest(t, handler, "operator", "correct horse battery")
	configureReq := httptest.NewRequest(http.MethodPost, "/nodes/discord-legacy/configure-token", nil)
	configureReq.AddCookie(operatorCookie)
	configureReq.Header.Set("X-CSRF-Token", operatorCSRF)
	configureRes := httptest.NewRecorder()
	handler.ServeHTTP(configureRes, configureReq)
	if configureRes.Code != http.StatusCreated {
		t.Fatalf("legacy Discord Configure Token status = %d body = %s", configureRes.Code, configureRes.Body.String())
	}
	var configureBody struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(configureRes.Body).Decode(&configureBody); err != nil {
		t.Fatal(err)
	}
	if configureBody.ConfigureToken == "" {
		t.Fatal("Configure Token was not returned once")
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/services/runtime-identity/configuration", bytes.NewBufferString(`{"nodeId":"discord-legacy","configureToken":"`+configureBody.ConfigureToken+`","version":"1.3.7"}`))
	activateRes := httptest.NewRecorder()
	handler.ServeHTTP(activateRes, activateReq)
	if activateRes.Code != http.StatusOK {
		t.Fatalf("legacy Discord configure status = %d body = %s", activateRes.Code, activateRes.Body.String())
	}
	var activateBody struct {
		Config struct {
			Auth struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"config"`
	}
	if err := json.NewDecoder(activateRes.Body).Decode(&activateBody); err != nil {
		t.Fatal(err)
	}
	if activateBody.Config.Auth.Token == "" {
		t.Fatal("updated Node Runtime Token was not returned once")
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), activateBody.Config.Auth.Token, "streams.stop"); err != nil {
		t.Fatalf("reconfigured Discord Bot must authorize streams.stop: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), legacyToken.RawToken, "streams.start"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("legacy token must be retired after reconfiguration: %v", err)
	}
}

func TestServiceRegisterRejectsWrongServiceType(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
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
	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"discord-01","service_type":"discord_bot","service_name":"Discord","public_url":"https://discord.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusForbidden {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	var failureAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.register" && event.Result == "failure" {
			failureAudit = &event
		}
	}
	if failureAudit == nil || failureAudit.ResourceID != "discord-01" || failureAudit.Metadata["reason"] != "service_token_scope_mismatch" || failureAudit.ActorUsername != "worker" {
		t.Fatalf("service register failure audit missing: %#v", auth.AuditEvents())
	}
}

func TestServiceRegisterPreservesReservedBindingValidationForNonUpdateAgents(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-reserved-binding", []string{"service.register"})

	for name, field := range map[string]string{
		"execution host":  `"execution_host_id":"attacker-host"`,
		"ownership epoch": `"ownership_epoch":7`,
	} {
		t.Run(name, func(t *testing.T) {
			payload := `{"service_id":"worker-reserved-binding","service_type":"worker","service_name":"Worker","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{},` + field + `}`
			registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(payload))
			registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
			registerRes := httptest.NewRecorder()
			handler.ServeHTTP(registerRes, registerReq)

			if registerRes.Code != http.StatusBadRequest ||
				!strings.Contains(registerRes.Body.String(), `"code":"invalid_service_registration"`) {
				t.Fatalf("reserved binding field status = %d body = %s", registerRes.Code, registerRes.Body.String())
			}
		})
	}
}

func TestServiceRegisterRejectsNonHTTPPublicURL(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewBufferString(`{"service_type":"worker","scopes":["service.register"],"service_id":"worker-01","service_name":"Worker 01","public_url":"https://worker.example.com","version":"0.1.0","capabilities":{}}`))
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
	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker","public_url":"ftp://worker.example.com","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusBadRequest {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if !strings.Contains(registerRes.Body.String(), "invalid_service_registration") || strings.Contains(registerRes.Body.String(), "worker.example.com") {
		t.Fatalf("unexpected register error response: %s", registerRes.Body.String())
	}
}

func TestServiceRegisterRejectsPrivatePublicURLByDefault(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createServiceTokenForTest(t, handler, cookie, csrf, "worker", []string{"service.register"})

	registerReq := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewBufferString(`{"service_id":"worker-01","service_type":"worker","service_name":"Worker","public_url":"http://169.254.169.254/latest/meta-data","version":"0.1.0","capabilities":{}}`))
	registerReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	registerRes := httptest.NewRecorder()
	handler.ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusBadRequest {
		t.Fatalf("register status = %d body = %s", registerRes.Code, registerRes.Body.String())
	}
	if !strings.Contains(registerRes.Body.String(), "invalid_service_registration") || strings.Contains(registerRes.Body.String(), "169.254.169.254") {
		t.Fatalf("unexpected register error response: %s", registerRes.Body.String())
	}
}
