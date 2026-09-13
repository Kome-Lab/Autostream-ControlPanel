package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUpdateAgentAssignmentEndpointIsRejected(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "assignment guard")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater",
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-assignment", Version: "v1.0.0",
	})
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/updater-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "service_assignment_unsupported") {
		t.Fatalf("update_agent assignment status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("update_agent assignment mutated store: %#v, %v", assignments, err)
	}
}

func TestUpdateAgentOnboardingUsesOneTimeConfigureCommand(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke", "service_health.read", "system_updates.execute", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker := registerServiceWithTokenForTest(
		t,
		auth,
		workerToken,
		store.ServiceRegistration{
			ServiceID:   "worker-onboarding",
			ServiceType: "worker",
			ServiceName: "Worker Onboarding",
			Host:        "worker.example.com",
			Port:        443,
			SSLEnabled:  true,
			PublicURL:   "https://worker.example.com",
			Version:     "v1.0.0",
		},
	)
	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	create := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", strings.NewReader(`{"node_type":"update_agent","node_id":"updater-01","name":"Updater 01","transport_mode":"pull_v2","execution_host_id":"host-01"}`))
	create.AddCookie(cookie)
	create.Header.Set("X-CSRF-Token", csrf)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create updater status = %d body = %s", created.Code, created.Body.String())
	}
	if created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("updater registration secret response cache control = %q", created.Header().Get("Cache-Control"))
	}
	createdPayload := append([]byte(nil), created.Body.Bytes()...)
	var createdBody struct {
		ConfigureToken    string   `json:"configure_token"`
		ConfigureCommand  string   `json:"configure_command"`
		ConfigurationPath string   `json:"configuration_path"`
		ManualRequired    bool     `json:"manual_configuration_required"`
		RuntimeToken      string   `json:"runtime_token"`
		Scopes            []string `json:"scopes"`
	}
	if err := json.NewDecoder(bytes.NewReader(createdPayload)).Decode(&createdBody); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(createdBody.ConfigureToken, "ast_cfg_") || !strings.HasPrefix(createdBody.ConfigureCommand, "sudo /usr/local/bin/autostream-host-agent configure ") || strings.Contains(createdBody.ConfigureCommand, createdBody.ConfigureToken) || strings.Contains(createdBody.ConfigureCommand, "--token") {
		t.Fatalf("updater configure metadata = %#v", createdBody)
	}
	if createdBody.ConfigurationPath != "/etc/autostream/updater/agent.yaml" || createdBody.ManualRequired || bytes.Contains(createdPayload, []byte(`"configuration_example"`)) {
		t.Fatalf("updater configuration metadata = %#v", createdBody)
	}
	if !slices.Contains(createdBody.Scopes, "updates.authorize") {
		t.Fatalf("updater registration omitted authorize scope: %#v", createdBody.Scopes)
	}
	if !strings.HasPrefix(createdBody.RuntimeToken, "ast_svc_") {
		t.Fatalf("updater registration omitted initial runtime token: %#v", createdBody)
	}
	registeredService, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if registeredService.TokenID == "" {
		t.Fatal("updater registration omitted the active token ID")
	}
	initialTokenID := registeredService.TokenID

	configuration := httptest.NewRequest(http.MethodGet, "/nodes/updater-01/configuration", nil)
	configuration.AddCookie(cookie)
	configurationResult := httptest.NewRecorder()
	handler.ServeHTTP(configurationResult, configuration)
	if configurationResult.Code != http.StatusOK {
		t.Fatalf("get updater configuration status = %d body = %s", configurationResult.Code, configurationResult.Body.String())
	}
	if strings.Contains(configurationResult.Body.String(), `"configure_command"`) || strings.Contains(configurationResult.Body.String(), "regenerate-configure-token") || strings.Contains(configurationResult.Body.String(), `"manual_configuration_required":true`) {
		t.Fatalf("get updater configuration exposed a fake or legacy configure workflow: %s", configurationResult.Body.String())
	}

	regenerate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/configure-token", nil)
	regenerate.AddCookie(cookie)
	regenerate.Header.Set("X-CSRF-Token", csrf)
	regenerateResult := httptest.NewRecorder()
	handler.ServeHTTP(regenerateResult, regenerate)
	if regenerateResult.Code != http.StatusCreated {
		t.Fatalf("updater configure-token status = %d body = %s", regenerateResult.Code, regenerateResult.Body.String())
	}
	if regenerateResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("configure token secret response cache control = %q", regenerateResult.Header().Get("Cache-Control"))
	}
	var regenerated struct {
		ConfigureToken   string `json:"configure_token"`
		ConfigureCommand string `json:"configure_command"`
	}
	if err := json.NewDecoder(regenerateResult.Body).Decode(&regenerated); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(regenerated.ConfigureToken, "ast_cfg_") || strings.Contains(regenerated.ConfigureCommand, regenerated.ConfigureToken) || strings.Contains(regenerated.ConfigureCommand, "--token") || !strings.HasPrefix(regenerated.ConfigureCommand, "sudo /usr/local/bin/autostream-host-agent configure ") {
		t.Fatalf("regenerated updater configure metadata = %#v", regenerated)
	}
	placeholderDigest := "sha256:" + strings.Repeat("a", 64)
	savedPolicy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"updater-01",
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-01",
			LocalExecutorPolicySHA256: placeholderDigest,
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:        worker.ServiceID,
				ServiceID:       worker.ServiceID,
				HostID:          "host-01",
				ServiceType:     worker.ServiceType,
				DeploymentMode:  updateradapter.ModeSystemd,
				LocalListenPort: 18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	newStageRequest := func(configureToken string) *http.Request {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"nodeId":          "updater-01",
			"configureToken":  configureToken,
			"protocolVersion": updateradapter.HostAgentConfigureProtocolVersion,
			"agentUid":        uint32(1001),
			"agentGid":        uint32(1002),
		})
		if err != nil {
			t.Fatal(err)
		}
		return httptest.NewRequest(
			http.MethodPost,
			"/services/host-agent/runtime-identity/stage",
			bytes.NewReader(payload),
		)
	}
	supersededConfigure := newStageRequest(createdBody.ConfigureToken)
	supersededConfigureResult := httptest.NewRecorder()
	handler.ServeHTTP(supersededConfigureResult, supersededConfigure)
	if supersededConfigureResult.Code != http.StatusUnauthorized || !strings.Contains(supersededConfigureResult.Body.String(), "invalid_configure_token") {
		t.Fatalf("superseded updater configure token status = %d body = %s", supersededConfigureResult.Code, supersededConfigureResult.Body.String())
	}

	agentConfigure := newStageRequest(regenerated.ConfigureToken)
	agentConfigureResult := httptest.NewRecorder()
	handler.ServeHTTP(agentConfigureResult, agentConfigure)
	if agentConfigureResult.Code != http.StatusOK {
		t.Fatalf("updater agent configure status = %d body = %s", agentConfigureResult.Code, agentConfigureResult.Body.String())
	}
	if agentConfigureResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("configured runtime token response cache control = %q", agentConfigureResult.Header().Get("Cache-Control"))
	}
	assertExactKeys := func(label string, fields map[string]json.RawMessage, expected ...string) {
		t.Helper()
		got := make([]string, 0, len(fields))
		for name := range fields {
			got = append(got, name)
		}
		slices.Sort(got)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			t.Fatalf("%s keys = %#v; want %#v", label, got, expected)
		}
	}
	stageResponse := agentConfigureResult.Body.Bytes()
	var configuredEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(stageResponse, &configuredEnvelope); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(
		"updater configure response",
		configuredEnvelope,
		"activation_expires_at",
		"activation_token",
		"config",
		"configuration_id",
		"configure_protocol_version",
		"local_executor_policy",
	)
	var configuredFields map[string]json.RawMessage
	if err := json.Unmarshal(configuredEnvelope["config"], &configuredFields); err != nil {
		t.Fatal(err)
	}
	assertExactKeys(
		"updater configure",
		configuredFields,
		"api",
		"node_id",
		"panel_url",
		"runtime_token",
		"service_name",
		"service_type",
		"transport_mode",
	)
	var staged updateradapter.UpdaterStagedConfiguration
	if err := json.Unmarshal(stageResponse, &staged); err != nil {
		t.Fatal(err)
	}
	if staged.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		staged.Config.TransportMode != store.SystemUpdateTransportPullV2 ||
		staged.Config.API != (updateradapter.UpdaterConfigureAPIAssertion{}) ||
		staged.LocalExecutorPolicy == nil {
		t.Fatal("updater stage did not return the canonical pull-v2 configure protocol")
	}
	if staged.Config.PanelURL != "https://panel.example.com" ||
		staged.Config.NodeID != "updater-01" ||
		staged.Config.ServiceName != "Updater 01" ||
		staged.Config.ServiceType != "update_agent" ||
		!strings.HasPrefix(staged.Config.RuntimeToken, "ast_svc_") ||
		!strings.HasPrefix(staged.ActivationToken, "ast_act_") ||
		staged.ActivationExpiresAt.IsZero() {
		t.Fatal("updater stage omitted required v2 identity or activation metadata")
	}
	if staged.LocalExecutorPolicy.SHA256 == "" ||
		staged.LocalExecutorPolicy.SourcePolicyRevision != savedPolicy.Revision ||
		staged.LocalExecutorPolicy.ProjectionRevision != savedPolicy.ProjectionRevision ||
		staged.LocalExecutorPolicy.PolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("updater stage returned an unbound Local Executor policy projection")
	}
	var stagedPolicy updateradapter.LocalExecutorPolicy
	if err := json.Unmarshal(staged.LocalExecutorPolicy.Policy, &stagedPolicy); err != nil {
		t.Fatal(err)
	}
	if stagedPolicy.AgentUID != 1001 ||
		stagedPolicy.AgentGID != 1002 ||
		stagedPolicy.HostID != "host-01" ||
		stagedPolicy.SourcePolicyRevision != savedPolicy.Revision ||
		stagedPolicy.ProjectionRevision != savedPolicy.ProjectionRevision ||
		stagedPolicy.PolicyRevision != savedPolicy.LocalExecutorPolicyRevision ||
		len(stagedPolicy.Targets) != 1 {
		t.Fatal("updater stage returned an unexpected Local Executor policy identity or revision")
	}
	stagedTarget := stagedPolicy.Targets[0]
	if stagedTarget.ServiceID != worker.ServiceID ||
		stagedTarget.ServiceType != worker.ServiceType ||
		stagedTarget.DeploymentMode != updateradapter.ModeSystemd ||
		stagedTarget.LocalListen != (updateradapter.LocalExecutorEndpoint{
			Host: "127.0.0.1",
			Port: 18081,
		}) {
		t.Fatal("updater stage returned an unexpected Local Executor target")
	}
	for _, forbidden := range []struct {
		label string
		value string
	}{
		{label: "initial raw configure token", value: createdBody.ConfigureToken},
		{label: "regenerated raw configure token", value: regenerated.ConfigureToken},
		{label: "legacy config_yml", value: `"config_yml"`},
		{label: "legacy configuration_yaml", value: `"configuration_yaml"`},
		{label: "GitHub token", value: `"github_token"`},
		{label: "legacy host inventory", value: `"hosts"`},
		{label: "legacy identity file", value: `"identity_file"`},
		{label: "private SSH key", value: `"ssh_private_key"`},
		{label: "private key", value: `"private_key"`},
		{label: "plaintext bootstrap token", value: `"bootstrap_token"`},
		{label: "plaintext bootstrap credential", value: `"bootstrap_credential"`},
	} {
		if strings.Contains(string(stageResponse), forbidden.value) {
			t.Fatalf("updater configure response exposed %s", forbidden.label)
		}
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); err != nil {
		t.Fatalf("initial updater runtime token stopped before activation: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), staged.Config.RuntimeToken, "updates.claim"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("staged updater runtime token was active before activation: %v", err)
	}
	registeredService, err = auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if staged.ConfigurationID == "" ||
		!strings.HasPrefix(staged.ConfigurationID, "hac1-") ||
		staged.ConfigurationID == registeredService.StagedNodeTokenID ||
		registeredService.StagedNodeTokenID == "" ||
		registeredService.TokenID != initialTokenID {
		t.Fatal("updater stage did not preserve the active token and bound external configuration ID")
	}
	replay := newStageRequest(regenerated.ConfigureToken)
	replayResult := httptest.NewRecorder()
	handler.ServeHTTP(replayResult, replay)
	if replayResult.Code != http.StatusUnauthorized || !strings.Contains(replayResult.Body.String(), "invalid_configure_token") {
		t.Fatalf("replayed updater configure token status = %d body = %s", replayResult.Code, replayResult.Body.String())
	}
	activation := hostAgentConfigureActivationPayload(
		staged,
		uint32(1001),
		uint32(1002),
		*staged.LocalExecutorPolicy,
	)
	activateResult := sendHostAgentConfigureActivation(t, handler, activation)
	if activateResult.Code != http.StatusOK {
		t.Fatalf("updater activation status = %d body = %s", activateResult.Code, activateResult.Body.String())
	}
	if activateResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("updater activation cache control = %q", activateResult.Header().Get("Cache-Control"))
	}
	var activationResult updateradapter.UpdaterActivationResult
	if err := json.NewDecoder(activateResult.Body).Decode(&activationResult); err != nil {
		t.Fatal(err)
	}
	if activationResult.State != "activated" ||
		activationResult.ConfigurationID != staged.ConfigurationID ||
		activationResult.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		activationResult.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		activationResult.SourcePolicyRevision != savedPolicy.Revision ||
		activationResult.ProjectionRevision != savedPolicy.ProjectionRevision ||
		activationResult.LocalExecutorPolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("updater activation result did not match the staged v2 policy binding")
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("initial updater runtime token survived activation: %v", err)
	}
	for _, scope := range []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"} {
		if _, err := auth.AuthenticateServiceToken(t.Context(), staged.Config.RuntimeToken, scope); err != nil {
			t.Fatalf("activated updater runtime token lacks %s: %v", scope, err)
		}
	}
	activateReplayResult := sendHostAgentConfigureActivation(t, handler, activation)
	if activateReplayResult.Code != http.StatusOK {
		t.Fatalf("idempotent updater activation status = %d body = %s", activateReplayResult.Code, activateReplayResult.Body.String())
	}
	var activationReplay updateradapter.UpdaterActivationResult
	if err := json.NewDecoder(activateReplayResult.Body).Decode(&activationReplay); err != nil {
		t.Fatal(err)
	}
	if activationReplay.State != "already_activated" ||
		activationReplay.ConfigurationID != staged.ConfigurationID ||
		activationReplay.ConfigureProtocol != updateradapter.HostAgentConfigureProtocolVersion ||
		activationReplay.LocalExecutorPolicySHA256 != staged.LocalExecutorPolicy.SHA256 ||
		activationReplay.SourcePolicyRevision != savedPolicy.Revision ||
		activationReplay.ProjectionRevision != savedPolicy.ProjectionRevision ||
		activationReplay.LocalExecutorPolicyRevision != savedPolicy.LocalExecutorPolicyRevision {
		t.Fatal("idempotent updater activation did not preserve the staged v2 policy binding")
	}
	audits, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"nodes.configure"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != "updater-01" {
		t.Fatalf("updater configure audit count=%d", len(audits))
	}
	auditMetadata := fmt.Sprint(audits[0].Metadata)
	for _, secret := range []string{
		createdBody.ConfigureToken,
		regenerated.ConfigureToken,
		createdBody.RuntimeToken,
		staged.Config.RuntimeToken,
		staged.ActivationToken,
	} {
		if strings.Contains(auditMetadata, secret) {
			t.Fatal("updater configure audit exposed a configure, runtime, or activation secret")
		}
	}

	outstandingRequest := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/configure-token", nil)
	outstandingRequest.AddCookie(cookie)
	outstandingRequest.Header.Set("X-CSRF-Token", csrf)
	outstandingResult := httptest.NewRecorder()
	handler.ServeHTTP(outstandingResult, outstandingRequest)
	if outstandingResult.Code != http.StatusCreated {
		t.Fatalf("create outstanding configure token status = %d", outstandingResult.Code)
	}
	if outstandingResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("outstanding configure token cache control = %q", outstandingResult.Header().Get("Cache-Control"))
	}
	var outstanding struct {
		ConfigureToken string `json:"configure_token"`
	}
	if err := json.NewDecoder(outstandingResult.Body).Decode(&outstanding); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(outstanding.ConfigureToken, "ast_cfg_") {
		t.Fatal("outstanding configure token has an unexpected format")
	}
	configuredRuntimeToken := staged.Config.RuntimeToken
	beforeRejectedRotate, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	tokensBeforeRejectedRotate, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if beforeRejectedRotate.ConfigureTokenHash == "" ||
		beforeRejectedRotate.ConfigureTokenExpiresAt == nil ||
		beforeRejectedRotate.ConfigureTokenUsedAt != nil {
		t.Fatal("outstanding configure credential is not pending before direct rotation")
	}
	if beforeRejectedRotate.StagedNodeTokenID != "" {
		t.Fatal("updater has a staged runtime token before direct rotation")
	}
	if beforeRejectedRotate.TokenID != registeredService.StagedNodeTokenID {
		t.Fatal("activated runtime token is not current before direct rotation")
	}

	rotate := httptest.NewRequest(http.MethodPost, "/nodes/updater-01/rotate-token", nil)
	rotate.AddCookie(cookie)
	rotate.Header.Set("X-CSRF-Token", csrf)
	rotateResult := httptest.NewRecorder()
	handler.ServeHTTP(rotateResult, rotate)
	if rotateResult.Code != http.StatusConflict {
		t.Fatalf("direct updater runtime token rotation status = %d; want 409", rotateResult.Code)
	}
	rotateResponse := rotateResult.Body.Bytes()
	for _, forbidden := range []struct {
		label string
		value string
	}{
		{label: "outstanding configure token", value: outstanding.ConfigureToken},
		{label: "initial configure token", value: createdBody.ConfigureToken},
		{label: "regenerated configure token", value: regenerated.ConfigureToken},
		{label: "initial runtime token", value: createdBody.RuntimeToken},
		{label: "configured runtime token", value: configuredRuntimeToken},
		{label: "activation token", value: staged.ActivationToken},
		{label: "runtime token field", value: `"runtime_token"`},
		{label: "runtime token prefix", value: "ast_svc_"},
		{label: "configuration path field", value: `"configuration_path"`},
		{label: "manual configuration field", value: `"manual_configuration_required"`},
		{label: "token ID field", value: `"token_id"`},
	} {
		if bytes.Contains(rotateResponse, []byte(forbidden.value)) {
			t.Fatalf("rejected direct rotation exposed %s", forbidden.label)
		}
	}
	var rotateFields map[string]json.RawMessage
	if err := json.Unmarshal(rotateResponse, &rotateFields); err != nil {
		t.Fatal("rejected direct rotation did not return a JSON object")
	}
	assertExactKeys("rejected direct rotation", rotateFields, "code")
	var rotateCode string
	if err := json.Unmarshal(rotateFields["code"], &rotateCode); err != nil {
		t.Fatal("rejected direct rotation code is not a string")
	}
	if rotateCode != "staged_runtime_token_rotation_required" {
		t.Fatal("rejected direct rotation did not require staged runtime token rotation")
	}

	afterRejectedRotate, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	tokensAfterRejectedRotate, err := auth.ListServiceTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sameOptionalTime := func(left, right *time.Time) bool {
		if left == nil || right == nil {
			return left == nil && right == nil
		}
		return left.Equal(*right)
	}
	for _, field := range []struct {
		name      string
		unchanged bool
	}{
		{"TokenID", beforeRejectedRotate.TokenID == afterRejectedRotate.TokenID},
		{"NodeTokenCiphertext", beforeRejectedRotate.NodeTokenCiphertext == afterRejectedRotate.NodeTokenCiphertext},
		{"NodeTokenNonce", beforeRejectedRotate.NodeTokenNonce == afterRejectedRotate.NodeTokenNonce},
		{"NodeTokenRotatedAt", sameOptionalTime(beforeRejectedRotate.NodeTokenRotatedAt, afterRejectedRotate.NodeTokenRotatedAt)},
		{"ConfigureTokenHash", beforeRejectedRotate.ConfigureTokenHash == afterRejectedRotate.ConfigureTokenHash},
		{"ConfigureTokenExpiresAt", sameOptionalTime(beforeRejectedRotate.ConfigureTokenExpiresAt, afterRejectedRotate.ConfigureTokenExpiresAt)},
		{"ConfigureTokenUsedAt", sameOptionalTime(beforeRejectedRotate.ConfigureTokenUsedAt, afterRejectedRotate.ConfigureTokenUsedAt)},
		{"StagedNodePreviousTokenID", beforeRejectedRotate.StagedNodePreviousTokenID == afterRejectedRotate.StagedNodePreviousTokenID},
		{"StagedNodeTokenID", beforeRejectedRotate.StagedNodeTokenID == afterRejectedRotate.StagedNodeTokenID},
		{"StagedNodeTokenHash", beforeRejectedRotate.StagedNodeTokenHash == afterRejectedRotate.StagedNodeTokenHash},
		{"StagedNodeTokenScopes", slices.Equal(beforeRejectedRotate.StagedNodeTokenScopes, afterRejectedRotate.StagedNodeTokenScopes)},
		{"StagedNodeTokenCiphertext", beforeRejectedRotate.StagedNodeTokenCiphertext == afterRejectedRotate.StagedNodeTokenCiphertext},
		{"StagedNodeTokenNonce", beforeRejectedRotate.StagedNodeTokenNonce == afterRejectedRotate.StagedNodeTokenNonce},
		{"StagedNodeActivationTokenHash", beforeRejectedRotate.StagedNodeActivationTokenHash == afterRejectedRotate.StagedNodeActivationTokenHash},
		{"StagedNodeTokenAt", sameOptionalTime(beforeRejectedRotate.StagedNodeTokenAt, afterRejectedRotate.StagedNodeTokenAt)},
		{"service-token inventory count", len(tokensBeforeRejectedRotate) == len(tokensAfterRejectedRotate)},
	} {
		if !field.unchanged {
			t.Fatalf("rejected direct rotation changed %s", field.name)
		}
	}
	for _, scope := range []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"} {
		if _, err := auth.AuthenticateServiceToken(t.Context(), configuredRuntimeToken, scope); err != nil {
			t.Fatalf("configured updater runtime token lacks %s after rejected direct rotation", scope)
		}
	}
	if _, err := auth.AuthenticateServiceToken(t.Context(), createdBody.RuntimeToken, "service.heartbeat"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("initial updater runtime token is not unauthorized after rejected direct rotation")
	}
}
