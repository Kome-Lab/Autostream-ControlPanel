package httpapi

import (
	"context"
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

type invalidScopeActivationServiceStore struct {
	store.ServiceRegistryStore
}

func (invalidScopeActivationServiceStore) ActivateServiceNodeConfiguration(
	context.Context,
	string,
	string,
	string,
	time.Time,
	store.ServiceRuntimeReport,
) (store.ServiceToken, store.RegisteredService, bool, error) {
	return store.ServiceToken{}, store.RegisteredService{}, false, store.ErrInvalidServiceScope
}

func TestUpdateAgentTokenMutationsRejectMissingSystemUpdatePermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited"}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{ServiceID: "updater-limited", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	originalConfigureHash := security.HashToken("original-configure-token")
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, originalConfigureHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "limited", "correct horse battery")
	for _, path := range []string{"/nodes/updater-limited/configure-token", "/nodes/updater-limited/rotate-token"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_escalation") {
			t.Fatalf("%s without system_updates.execute status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != token.ID || got.ConfigureTokenHash != originalConfigureHash || got.ConfigureTokenUsedAt != nil {
		t.Fatalf("denied updater token mutation changed credentials: %#v", got)
	}
}

func TestUpdateAgentCredentialMutationsRejectMissingSecretPermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "limited"},
		"correct horse battery",
		[]string{"api_tokens.create", "api_tokens.revoke", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "limited", "correct horse battery")

	register := httptest.NewRequest(
		http.MethodPost,
		"/nodes/registration-tokens",
		strings.NewReader(`{"node_type":"update_agent","node_id":"updater-created","name":"Updater","host":"updater.example.com","port":8090,"ssl_enabled":true}`),
	)
	register.AddCookie(cookie)
	register.Header.Set("X-CSRF-Token", csrf)
	registerResult := httptest.NewRecorder()
	handler.ServeHTTP(registerResult, register)
	if registerResult.Code != http.StatusForbidden || !strings.Contains(registerResult.Body.String(), "permission_escalation") {
		t.Fatalf("updater registration without secrets.update status=%d body=%s", registerResult.Code, registerResult.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "updater-created"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("denied updater registration created a service: %v", err)
	}

	createAPI := httptest.NewRequest(
		http.MethodPost,
		"/api-tokens",
		strings.NewReader(`{"service_type":"update_agent","scopes":["service.register","service.heartbeat","updates.claim","updates.report","updates.authorize"],"service_id":"updater-api","service_name":"Updater API","public_url":"https://updater-api.example.com"}`),
	)
	createAPI.AddCookie(cookie)
	createAPI.Header.Set("X-CSRF-Token", csrf)
	createAPIResult := httptest.NewRecorder()
	handler.ServeHTTP(createAPIResult, createAPI)
	if createAPIResult.Code != http.StatusForbidden || !strings.Contains(createAPIResult.Body.String(), "permission_escalation") {
		t.Fatalf("updater API token creation without secrets.update status=%d body=%s", createAPIResult.Code, createAPIResult.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "updater-api"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("denied updater API token creation precreated a service: %v", err)
	}

	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID: "updater-limited", ServiceType: "update_agent", ServiceName: "Updater",
		PublicURL: "https://updater.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalConfigureHash := security.HashToken("original-configure-token")
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, originalConfigureHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/nodes/updater-limited/configure-token",
		"/nodes/updater-limited/rotate-token",
		"/api-tokens/" + token.ID + "/rotate",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_escalation") {
			t.Fatalf("%s without secrets.update status=%d body=%s", path, res.Code, res.Body.String())
		}
	}

	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != token.ID || got.ConfigureTokenHash != originalConfigureHash || got.ConfigureTokenUsedAt != nil {
		t.Fatalf("denied updater credential mutation changed service credentials: %#v", got)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), token.RawToken, "updates.claim"); err != nil {
		t.Fatalf("denied generic rotation invalidated the existing runtime token: %v", err)
	}
	tokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].RevokedAt != nil {
		t.Fatalf("denied updater credential operations changed tokens: %#v", tokens)
	}
}

func TestGenericUpdateAgentTokenCreationRequiresClaimScopes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "admin"},
		"correct horse battery",
		[]string{"api_tokens.create", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	request := func(serviceID string, scopes []string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"service_type": "update_agent",
			"service_id":   serviceID,
			"service_name": "Updater",
			"public_url":   "https://updater.example.com",
			"scopes":       scopes,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api-tokens", strings.NewReader(string(payload)))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	required := []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"}
	for _, missing := range []string{"updates.claim", "updates.report", "updates.authorize"} {
		scopes := make([]string, 0, len(required)-1)
		for _, scope := range required {
			if scope != missing {
				scopes = append(scopes, scope)
			}
		}
		res := request("updater-missing-"+strings.TrimPrefix(missing, "updates."), scopes)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_service_scope"`) {
			t.Fatalf("missing %s status=%d body=%s", missing, res.Code, res.Body.String())
		}
	}
	missingRegister := request(
		"updater-missing-register",
		[]string{"service.heartbeat", "updates.claim", "updates.report", "updates.authorize"},
	)
	if missingRegister.Code != http.StatusBadRequest ||
		!strings.Contains(missingRegister.Body.String(), `"code":"service_register_scope_required"`) {
		t.Fatalf("missing service.register status=%d body=%s", missingRegister.Code, missingRegister.Body.String())
	}
	tokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("invalid updater scope requests created tokens: %#v", tokens)
	}

	valid := request("updater-valid", required)
	if valid.Code != http.StatusCreated {
		t.Fatalf("valid updater token status=%d body=%s", valid.Code, valid.Body.String())
	}
	var created store.ServiceToken
	if err := json.NewDecoder(valid.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ServiceType != "update_agent" ||
		!validUpdateAgentServiceTokenScopes(created.ServiceType, created.Scopes) {
		t.Fatalf("valid updater token response = %#v", created)
	}
}

func TestLegacyUpdateAgentCredentialRotationFailsClosed(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{Username: "admin"},
		"correct horse battery",
		[]string{"api_tokens.create", "api_tokens.revoke", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	legacyToken, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), legacyToken, store.ServiceRegistration{
		ServiceID: "updater-legacy", ServiceType: "update_agent", ServiceName: "Legacy Updater",
		PublicURL: "https://updater-legacy.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	rawConfigureToken := "ast_cfg_legacy_updater"
	originalConfigureHash := security.HashToken(rawConfigureToken)
	if _, err := auth.SetServiceConfigureToken(
		t.Context(),
		service.ServiceID,
		originalConfigureHash,
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for _, path := range []string{
		"/api-tokens/" + legacyToken.ID + "/rotate",
		"/nodes/" + service.ServiceID + "/configure-token",
		"/nodes/" + service.ServiceID + "/rotate-token",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_service_scope"`) {
			t.Fatalf("%s legacy scope status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	stage := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/runtime-identity/stage",
		strings.NewReader(`{"nodeId":"`+service.ServiceID+`","configureToken":"`+rawConfigureToken+`"}`),
	)
	stageResult := httptest.NewRecorder()
	handler.ServeHTTP(stageResult, stage)
	if stageResult.Code != http.StatusConflict ||
		!strings.Contains(stageResult.Body.String(), `"code":"invalid_service_scope"`) {
		t.Fatalf("legacy updater stage status=%d body=%s", stageResult.Code, stageResult.Body.String())
	}

	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != legacyToken.ID ||
		got.ConfigureTokenHash != originalConfigureHash ||
		got.ConfigureTokenUsedAt != nil ||
		got.StagedNodeTokenID != "" {
		t.Fatalf("rejected legacy updater rotation mutated service: %#v", got)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), legacyToken.RawToken, "service.heartbeat"); err != nil {
		t.Fatalf("rejected legacy updater rotation invalidated old token: %v", err)
	}
	tokens, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].ID != legacyToken.ID || tokens[0].RevokedAt != nil {
		t.Fatalf("rejected legacy updater rotation created or revoked tokens: %#v", tokens)
	}
}

func TestLegacyStagedUpdateAgentActivationFailsClosed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(invalidScopeActivationServiceStore{ServiceRegistryStore: auth}),
	)
	activate := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/runtime-identity/activate",
		strings.NewReader(`{"nodeId":"updater-legacy-staged","configurationId":"legacy-configuration","activationToken":"ast_act_legacy"}`),
	)
	activateResult := httptest.NewRecorder()
	handler.ServeHTTP(activateResult, activate)
	if activateResult.Code != http.StatusConflict ||
		!strings.Contains(activateResult.Body.String(), `"code":"invalid_service_scope"`) {
		t.Fatalf("legacy staged updater activation status=%d body=%s", activateResult.Code, activateResult.Body.String())
	}
}
