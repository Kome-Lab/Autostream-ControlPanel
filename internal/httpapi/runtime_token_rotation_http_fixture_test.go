package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type runtimeTokenRotationHTTPFixture struct {
	auth           *store.MemoryAuthStore
	handler        http.Handler
	cookie         *http.Cookie
	csrf           string
	oldToken       store.ServiceToken
	wrongHostToken store.ServiceToken
	policies       *store.MemoryUpdaterPolicyStore
	updates        *store.MemorySystemUpdateStore
	policy         store.UpdaterPolicy
	ownershipEpoch int64
}

func newRuntimeTokenRotationHTTPFixture(t *testing.T) runtimeTokenRotationHTTPFixture {
	return newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(t, nil)
}

func newRuntimeTokenRotationHTTPFixtureWithHeartbeatClock(
	t *testing.T,
	heartbeatNow func() time.Time,
) runtimeTokenRotationHTTPFixture {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", strings.Repeat("runtime-rotation-key-", 2))
	auth := store.NewMemoryAuthStore(
		store.WithMemoryServiceHeartbeatClock(heartbeatNow),
	)
	if err := auth.AddUser(
		store.User{ID: "rotation-admin", Username: "rotation-admin"},
		"correct horse battery",
		[]string{
			"system_updates.read",
			"system_updates.execute",
			"api_tokens.create",
			"api_tokens.revoke",
			"services.disable",
			"secrets.update",
		},
	); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	workerToken, err := auth.CreateServiceToken(
		t.Context(),
		"worker",
		[]string{"service.register", "service.heartbeat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(
		t,
		auth,
		workerToken,
		store.ServiceRegistration{
			ServiceID:   "worker-a",
			ServiceType: "worker",
			ServiceName: "Worker A",
			Host:        "worker.example.com",
			Port:        18081,
			SSLEnabled:  true,
			PublicURL:   "https://worker.example.com:18081",
			Version:     "v1.0.0",
		},
	)
	policy := store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:        "worker-a",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			HostID:          "host-a",
			DeploymentMode:  "systemd",
			LocalListenPort: 18081,
		}},
	}
	savedPolicy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		0,
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := registerRuntimeTokenRotationAgentForTest(
		t, auth, "host-agent-a", "host-a", 0,
	)
	reportedConfigSHA256, err := updateradapter.SystemdConfigurePortSidecarSHA256(
		"worker",
		18081,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), oldToken, store.ServiceHeartbeat{
		ServiceID: "host-agent-a",
		Status:    "online",
		Version:   "v2.0.0",
		Capabilities: map[string]any{
			"host_agent":                         true,
			"observe_only":                       true,
			"update_executor":                    true,
			"mutation_enabled":                   false,
			"recovery_pending":                   false,
			"transport_mode":                     store.SystemUpdateTransportPullV2,
			"agent_protocol_version":             "2",
			"execution_host_id":                  "host-a",
			"ownership_epoch":                    int64(0),
			"source_policy_revision":             savedPolicy.Revision,
			"policy_revision":                    savedPolicy.ProjectionRevision,
			"policy_status":                      "applied",
			"local_executor_policy_revision":     savedPolicy.LocalExecutorPolicyRevision,
			"target_availability":                map[string]any{"worker-a": "available"},
			"target_availability_codes":          map[string]any{"worker-a": "executor_verified"},
			"reported_ports":                     map[string]any{"worker-a": int64(18081)},
			"port_drift":                         map[string]any{"worker-a": false},
			"reported_service_types":             map[string]any{"worker-a": "worker"},
			"reported_deployment_modes":          map[string]any{"worker-a": "systemd"},
			"reported_executor_policy_revisions": map[string]any{"worker-a": savedPolicy.LocalExecutorPolicyRevision},
			"reported_executor_policy_sha256": map[string]any{
				"worker-a": savedPolicy.LocalExecutorPolicySHA256,
			},
			"reported_config_revisions": map[string]any{"worker-a": int64(1)},
			"reported_config_sha256": map[string]any{
				"worker-a": reportedConfigSHA256,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	activation, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		auth,
		updates,
		store.ActivatePullUpdaterOwnershipParams{
			ServiceID:                           "host-agent-a",
			ExecutionHostID:                     "host-a",
			ExpectedExecutionHostOwnershipEpoch: 0,
			ExpectedSourcePolicyRevision:        savedPolicy.Revision,
			ExpectedProjectionRevision:          savedPolicy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: savedPolicy.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   savedPolicy.LocalExecutorPolicySHA256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership := activation.Ownership
	wrongHostToken := registerRuntimeTokenRotationAgentForTest(
		t, auth, "host-agent-b", "host-b", 1,
	)
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(
		t, handler, "rotation-admin", "correct horse battery",
	)
	return runtimeTokenRotationHTTPFixture{
		auth:           auth,
		handler:        handler,
		cookie:         cookie,
		csrf:           csrf,
		oldToken:       oldToken,
		wrongHostToken: wrongHostToken,
		policies:       policies,
		updates:        updates,
		policy:         savedPolicy,
		ownershipEpoch: ownership.OwnershipEpoch,
	}
}

type runtimeTokenRotationHeartbeatClock struct {
	mu    sync.Mutex
	fixed *time.Time
}

func (c *runtimeTokenRotationHeartbeatClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fixed == nil {
		return time.Now().UTC()
	}
	return c.fixed.UTC()
}

func (c *runtimeTokenRotationHeartbeatClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now = now.UTC()
	c.fixed = &now
}

func registerRuntimeTokenRotationAgentForTest(
	t *testing.T,
	auth *store.MemoryAuthStore,
	serviceID string,
	executionHostID string,
	ownershipEpoch int64,
) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{
			"service.register",
			"service.heartbeat",
			"service.config.read",
			"updates.claim",
			"updates.report",
			"updates.authorize",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(
		t.Context(),
		token,
		store.ServiceRegistration{
			ServiceID:       serviceID,
			ServiceType:     "update_agent",
			ServiceName:     serviceID,
			TransportMode:   store.SystemUpdateTransportPullV2,
			ExecutionHostID: executionHostID,
			OwnershipEpoch:  ownershipEpoch,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(
		t.Context(),
		token,
		store.ServiceRegistration{
			ServiceID:       serviceID,
			ServiceType:     "update_agent",
			ServiceName:     serviceID,
			TransportMode:   store.SystemUpdateTransportPullV2,
			ExecutionHostID: executionHostID,
			OwnershipEpoch:  ownershipEpoch,
		},
	); err != nil {
		t.Fatal(err)
	}
	return token
}

func (f runtimeTokenRotationHTTPFixture) stage(
	t *testing.T,
	idempotencyKey string,
) runtimeTokenRotationResponse {
	t.Helper()
	response := f.adminRequest(
		t,
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/runtime-token-rotations",
		`{"idempotency_key":"`+idempotencyKey+`"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("stage status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Rotation runtimeTokenRotationResponse `json:"rotation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Rotation
}

func (f runtimeTokenRotationHTTPFixture) reportRuntimeTokenRotationHeartbeat(
	t *testing.T,
	rotation runtimeTokenRotationResponse,
) {
	f.reportRuntimeTokenRotationHeartbeatWithToken(
		t,
		rotation,
		f.oldToken.RawToken,
	)
}

func (f runtimeTokenRotationHTTPFixture) reportRuntimeTokenRotationHeartbeatWithToken(
	t *testing.T,
	rotation runtimeTokenRotationResponse,
	rawToken string,
) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"service_id": "host-agent-a",
		"status":     "online",
		"version":    "v2.0.0",
		"capabilities": map[string]any{
			"agent_version":                  "v2.0.0",
			"agent_protocol_version":         2,
			"executor_version":               "v2.0.0",
			"executor_protocol_version":      1,
			"mutation_protocol_version":      1,
			"execution_host_id":              rotation.ExecutionHostID,
			"ownership_epoch":                rotation.ExpectedOwnershipEpoch,
			"source_policy_revision":         rotation.ExpectedSourcePolicyRevision,
			"projection_revision":            rotation.ExpectedProjectionRevision,
			"local_executor_policy_revision": rotation.ExpectedLocalExecutorPolicyRevision,
			"local_executor_policy_sha256":   f.policy.LocalExecutorPolicySHA256,
			"local_stage_receipt_id":         rotation.LocalStageReceiptID,
			"local_phase":                    "staged_token_active",
			"host_agent":                     true,
			"update_executor":                true,
			"mutation_enabled":               true,
			"recovery_pending":               false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := f.serviceRequest(
		t,
		rawToken,
		"/services/heartbeat",
		string(body),
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf(
			"rotation heartbeat status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func runtimeTokenRotationHeartbeatProofBody(
	t *testing.T,
	fixture runtimeTokenRotationHTTPFixture,
	rotation runtimeTokenRotationResponse,
) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"expected_revision":              rotation.Revision,
		"agent_version":                  "v2.0.0",
		"agent_protocol_version":         2,
		"executor_version":               "v2.0.0",
		"executor_protocol_version":      1,
		"mutation_protocol_version":      1,
		"ownership_epoch":                rotation.ExpectedOwnershipEpoch,
		"source_policy_revision":         rotation.ExpectedSourcePolicyRevision,
		"projection_revision":            rotation.ExpectedProjectionRevision,
		"local_executor_policy_revision": rotation.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   fixture.policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         rotation.LocalStageReceiptID,
		"local_phase":                    "staged_token_active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func (f runtimeTokenRotationHTTPFixture) adminRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(f.cookie)
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", f.csrf)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f runtimeTokenRotationHTTPFixture) serviceRequest(
	t *testing.T,
	rawToken string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+rawToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f runtimeTokenRotationHTTPFixture) publicRequest(
	t *testing.T,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func assertRuntimeTokenRotationNoStore(
	t *testing.T,
	response *httptest.ResponseRecorder,
) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unsafe response headers: %#v", response.Header())
	}
}
