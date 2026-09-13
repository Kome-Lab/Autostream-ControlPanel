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
