package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestActivatePullUpdaterOwnershipUsesAtomicServerOwnedTransition(t *testing.T) {
	auth, policies, updates, handler, cookie, csrf, saved := newPullActivationHTTPFixture(t, true)
	_ = auth

	get := httptest.NewRequest(
		http.MethodGet,
		"/system-updates/updaters/host-agent-a/settings",
		nil,
	)
	get.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get observer settings = %d %s", getResponse.Code, getResponse.Body.String())
	}
	var settings updaterPolicyResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.ExecutionHostOwnership == nil ||
		settings.ExecutionHostOwnership.TransportMode != store.SystemUpdateTransportSSHV1 ||
		settings.PullActivation == nil ||
		!settings.PullActivation.Ready {
		t.Fatalf("observer activation status = %#v", settings)
	}

	body := activatePullUpdaterOwnershipRequest{
		ExpectedExecutionHostID:             "host-a",
		ExpectedOwnershipEpoch:              settings.ExecutionHostOwnership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        saved.Revision,
		ExpectedProjectionRevision:          saved.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
	}
	response := activatePullOwnershipForTest(t, handler, cookie, csrf, body)
	if response.Code != http.StatusOK {
		t.Fatalf("activate pull owner = %d %s", response.Code, response.Body.String())
	}
	var activated activatePullUpdaterOwnershipResponse
	if err := json.NewDecoder(response.Body).Decode(&activated); err != nil {
		t.Fatal(err)
	}
	if activated.UpdaterID != "host-agent-a" ||
		activated.ExecutionHostID != "host-a" ||
		activated.TransportMode != store.SystemUpdateTransportPullV2 ||
		activated.AgentServiceID != "host-agent-a" ||
		activated.OwnershipEpoch != body.ExpectedOwnershipEpoch+1 ||
		activated.SourcePolicyRevision != saved.Revision ||
		activated.ProjectionRevision != saved.ProjectionRevision ||
		activated.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision ||
		activated.LocalExecutorPolicySHA256 != saved.LocalExecutorPolicySHA256 {
		t.Fatalf("activation response = %#v", activated)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch != activated.OwnershipEpoch ||
		ownership.PolicyRevision != saved.ProjectionRevision ||
		service.OwnershipEpoch != activated.OwnershipEpoch {
		t.Fatalf("atomic activation state: owner=%#v service=%#v", ownership, service)
	}
	policy, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != saved.Revision ||
		policy.ProjectionRevision != saved.ProjectionRevision ||
		policy.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision {
		t.Fatalf("activation mutated policy = %#v", policy)
	}

	stale := activatePullOwnershipForTest(t, handler, cookie, csrf, body)
	if stale.Code != http.StatusConflict ||
		!strings.Contains(stale.Body.String(), `"code":"system_update_ownership_conflict"`) {
		t.Fatalf("stale activation = %d %s", stale.Code, stale.Body.String())
	}
}

func TestDeactivatePullUpdaterOwnershipRestoresSavedLegacyOwnerAtomically(t *testing.T) {
	auth, _, updates, handler, cookie, csrf, saved := newPullActivationHTTPFixture(t, true)
	before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	activatedResponse := activatePullOwnershipForTest(
		t,
		handler,
		cookie,
		csrf,
		activatePullUpdaterOwnershipRequest{
			ExpectedExecutionHostID:             "host-a",
			ExpectedOwnershipEpoch:              before.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	)
	if activatedResponse.Code != http.StatusOK {
		t.Fatalf("activate pull owner = %d %s", activatedResponse.Code, activatedResponse.Body.String())
	}
	var activated activatePullUpdaterOwnershipResponse
	if err := json.NewDecoder(activatedResponse.Body).Decode(&activated); err != nil {
		t.Fatal(err)
	}

	response := deactivatePullOwnershipForTest(
		t,
		handler,
		cookie,
		csrf,
		deactivatePullUpdaterOwnershipRequest{
			ExpectedExecutionHostID:             "host-a",
			ExpectedOwnershipEpoch:              activated.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("deactivate pull owner = %d %s", response.Code, response.Body.String())
	}
	var deactivated deactivatePullUpdaterOwnershipResponse
	if err := json.NewDecoder(response.Body).Decode(&deactivated); err != nil {
		t.Fatal(err)
	}
	if deactivated.UpdaterID != "host-agent-a" ||
		deactivated.ExecutionHostID != "host-a" ||
		deactivated.TransportMode != store.SystemUpdateTransportSSHV1 ||
		deactivated.AgentServiceID != "central-updater" ||
		deactivated.OwnershipEpoch != activated.OwnershipEpoch+1 ||
		deactivated.AgentOwnershipEpoch != 0 ||
		deactivated.SourcePolicyRevision != saved.Revision ||
		deactivated.ProjectionRevision != saved.ProjectionRevision ||
		deactivated.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision ||
		deactivated.LocalExecutorPolicySHA256 != saved.LocalExecutorPolicySHA256 {
		t.Fatalf("deactivation response = %#v", deactivated)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.TransportMode != store.SystemUpdateTransportSSHV1 ||
		ownership.AgentServiceID != "central-updater" ||
		ownership.LegacyAgentServiceID != "central-updater" ||
		ownership.OwnershipEpoch != deactivated.OwnershipEpoch ||
		service.OwnershipEpoch != 0 {
		t.Fatalf("atomic deactivation state: owner=%#v service=%#v", ownership, service)
	}
	foundAudit := false
	for _, event := range auth.AuditEvents() {
		if event.Action == "system_updates.pull_ownership.deactivate" &&
			event.ResourceID == "host-agent-a" &&
			event.Result == "success" {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatalf("deactivation audit missing: %#v", auth.AuditEvents())
	}

	stale := deactivatePullOwnershipForTest(
		t,
		handler,
		cookie,
		csrf,
		deactivatePullUpdaterOwnershipRequest{
			ExpectedExecutionHostID:             "host-a",
			ExpectedOwnershipEpoch:              activated.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	)
	if stale.Code != http.StatusConflict ||
		!strings.Contains(stale.Body.String(), `"code":"system_update_ownership_conflict"`) {
		t.Fatalf("stale deactivation = %d %s", stale.Code, stale.Body.String())
	}
}

func TestDeactivatePullUpdaterOwnershipRejectsLegacyTokenWithoutUpdateScopes(
	t *testing.T,
) {
	_, _, updates, handler, cookie, csrf, saved := newPullActivationHTTPFixtureWithLegacyScopes(
		t,
		true,
		[]string{"service.register", "service.heartbeat"},
	)
	before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	activatedResponse := activatePullOwnershipForTest(
		t,
		handler,
		cookie,
		csrf,
		activatePullUpdaterOwnershipRequest{
			ExpectedExecutionHostID:             "host-a",
			ExpectedOwnershipEpoch:              before.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	)
	if activatedResponse.Code != http.StatusOK {
		t.Fatalf("activate pull owner = %d %s", activatedResponse.Code, activatedResponse.Body.String())
	}
	var activated activatePullUpdaterOwnershipResponse
	if err := json.NewDecoder(activatedResponse.Body).Decode(&activated); err != nil {
		t.Fatal(err)
	}

	response := deactivatePullOwnershipForTest(
		t,
		handler,
		cookie,
		csrf,
		deactivatePullUpdaterOwnershipRequest{
			ExpectedExecutionHostID:             "host-a",
			ExpectedOwnershipEpoch:              activated.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"update_agent_inactive"`) {
		t.Fatalf("insufficient legacy token scopes = %d %s", response.Code, response.Body.String())
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if after.TransportMode != store.SystemUpdateTransportPullV2 ||
		after.AgentServiceID != "host-agent-a" ||
		after.OwnershipEpoch != activated.OwnershipEpoch {
		t.Fatalf("rejected deactivation mutated ownership: %#v", after)
	}
}

func TestActivatePullUpdaterOwnershipFailsClosedWhenObserverIsNotReady(t *testing.T) {
	auth, _, updates, handler, cookie, csrf, saved := newPullActivationHTTPFixture(t, false)
	before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	response := activatePullOwnershipForTest(t, handler, cookie, csrf, activatePullUpdaterOwnershipRequest{
		ExpectedExecutionHostID:             "host-a",
		ExpectedOwnershipEpoch:              before.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        saved.Revision,
		ExpectedProjectionRevision:          saved.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
	})
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"host_agent_not_ready"`) {
		t.Fatalf("unready activation = %d %s", response.Code, response.Body.String())
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if after != before || service.OwnershipEpoch != 0 {
		t.Fatalf("unready activation mutated state: before=%#v after=%#v service=%#v", before, after, service)
	}
}

func TestActivatePullUpdaterOwnershipRejectsUnknownRequestFields(t *testing.T) {
	_, _, _, handler, cookie, csrf, _ := newPullActivationHTTPFixture(t, true)
	request := httptest.NewRequest(
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/pull-ownership/activate",
		strings.NewReader(`{
			"expected_execution_host_id":"host-a",
			"expected_ownership_epoch":1,
			"expected_source_policy_revision":1,
			"expected_projection_revision":1,
			"expected_local_executor_policy_revision":1,
			"expected_local_executor_policy_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"agent_service_id":"attacker-selected"
		}`),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("unknown activation field = %d %s", response.Code, response.Body.String())
	}
}

func activatePullOwnershipForTest(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	csrf string,
	body activatePullUpdaterOwnershipRequest,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/pull-ownership/activate",
		bytes.NewReader(payload),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func deactivatePullOwnershipForTest(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	csrf string,
	body deactivatePullUpdaterOwnershipRequest,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/system-updates/updaters/host-agent-a/pull-ownership/deactivate",
		bytes.NewReader(payload),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newPullActivationHTTPFixture(
	t *testing.T,
	ready bool,
) (
	*store.MemoryAuthStore,
	*store.MemoryUpdaterPolicyStore,
	*store.MemorySystemUpdateStore,
	http.Handler,
	*http.Cookie,
	string,
	store.UpdaterPolicy,
) {
	t.Helper()
	return newPullActivationHTTPFixtureWithLegacyScopes(
		t,
		ready,
		[]string{
			"service.register",
			"service.heartbeat",
			"updates.claim",
			"updates.report",
			"updates.authorize",
		},
	)
}

func newPullActivationHTTPFixtureWithLegacyScopes(
	t *testing.T,
	ready bool,
	legacyTokenScopes []string,
) (
	*store.MemoryAuthStore,
	*store.MemoryUpdaterPolicyStore,
	*store.MemorySystemUpdateStore,
	http.Handler,
	*http.Cookie,
	string,
	store.UpdaterPolicy,
) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "activation-admin", Username: "activation-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
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
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-a",
		ServiceType: "worker",
		ServiceName: "Worker A",
		PublicURL:   "https://worker.example.com:18081",
		Version:     "v1.0.0",
	})

	legacyToken, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		legacyTokenScopes,
	)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, legacyToken, store.ServiceRegistration{
		ServiceID:     "central-updater",
		ServiceType:   "update_agent",
		ServiceName:   "Central Updater",
		TransportMode: store.SystemUpdateTransportSSHV1,
		PublicURL:     "https://updater.example.com:8090",
		Version:       "v1.0.0",
	})
	policies := store.NewMemoryUpdaterPolicyStore()
	if _, err := policies.SaveUpdaterPolicy(
		t.Context(),
		"central-updater",
		0,
		store.UpdaterPolicy{
			API: store.UpdaterPolicyAPI{
				BindHost: "127.0.0.1",
				Host:     "127.0.0.1",
				Port:     8090,
			},
			PollIntervalSeconds:      15,
			HeartbeatIntervalSeconds: 30,
			Hosts: []store.UpdaterPolicyHost{{
				HostID:        "host-a",
				Name:          "Host A",
				Address:       "host-a.example.com",
				Port:          55850,
				User:          "autostream-update-host",
				Arch:          "amd64",
				HostPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g",
			}},
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       "worker-a",
				ServiceID:      "worker-a",
				HostID:         "host-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	owner, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportSSHV1,
		"central-updater",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		owner.OwnershipEpoch,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       "worker-a",
				ServiceID:      "worker-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent":                         true,
		"observe_only":                       true,
		"update_executor":                    ready,
		"mutation_enabled":                   false,
		"recovery_pending":                   false,
		"transport_mode":                     store.SystemUpdateTransportPullV2,
		"agent_protocol_version":             "2",
		"execution_host_id":                  "host-a",
		"ownership_epoch":                    int64(0),
		"policy_revision":                    saved.ProjectionRevision,
		"policy_status":                      "applied",
		"target_availability":                map[string]any{"worker-a": "available"},
		"target_availability_codes":          map[string]any{"worker-a": "executor_verified"},
		"reported_ports":                     map[string]any{"worker-a": int64(18081)},
		"port_drift":                         map[string]any{"worker-a": false},
		"reported_service_types":             map[string]any{"worker-a": "worker"},
		"reported_deployment_modes":          map[string]any{"worker-a": "systemd"},
		"reported_executor_policy_revisions": map[string]any{"worker-a": saved.LocalExecutorPolicyRevision},
		"reported_executor_policy_sha256": map[string]any{
			"worker-a": saved.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{"worker-a": int64(1)},
		"reported_config_sha256": map[string]any{
			"worker-a": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		0,
		capabilities,
	)
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "activation-admin", "correct horse battery")
	return auth, policies, updates, handler, cookie, csrf, saved
}
