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
