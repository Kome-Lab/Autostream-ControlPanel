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
