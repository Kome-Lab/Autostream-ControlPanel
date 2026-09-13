package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
