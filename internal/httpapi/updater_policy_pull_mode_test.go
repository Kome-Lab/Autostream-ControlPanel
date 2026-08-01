package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestNormalizedUpdaterPolicyRequestUsesServerOwnedPullBinding(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	agent := store.RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, updaterPolicyUpdateRequest{
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: digest,
		Targets: []updaterPolicyTargetRequest{{
			TargetID:        "worker-a",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  "systemd",
			LocalListenPort: json.RawMessage("8084"),
		}},
	})
	if err != nil {
		t.Fatalf("normalizedUpdaterPolicyRequest: %v", err)
	}
	if policy.UpdaterID != agent.ServiceID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != agent.ExecutionHostID ||
		policy.Targets[0].HostID != agent.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != digest {
		t.Fatalf("policy = %#v", policy)
	}
}

func TestNormalizedUpdaterPolicyRequestAllowsObserveOnlyEpochZero(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	agent := store.RegisteredService{
		ServiceID:       "host-agent-observer",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  0,
		Capabilities:    map[string]any{"observe_only": true},
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, updaterPolicyUpdateRequest{
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: digest,
		Targets: []updaterPolicyTargetRequest{{
			TargetID:        "worker-a",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  "systemd",
			LocalListenPort: json.RawMessage("8084"),
		}},
	})
	if err != nil {
		t.Fatalf("observe-only pull policy: %v", err)
	}
	if policy.ExecutionHostID != agent.ExecutionHostID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 {
		t.Fatalf("observe-only pull policy = %#v", policy)
	}
}

func TestNormalizedUpdaterPolicyRequestRejectsUnownedPullAgentOutsideInitialPendingBootstrap(t *testing.T) {
	t.Parallel()

	base := store.RegisteredService{
		ServiceID:       "host-agent-unowned",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  0,
	}
	for name, agent := range map[string]store.RegisteredService{
		"registered without observer report": func() store.RegisteredService {
			candidate := base
			candidate.Status = "registered"
			return candidate
		}(),
		"pending after a capability report": func() store.RegisteredService {
			candidate := base
			candidate.Status = "pending"
			candidate.ReportedCapabilities = map[string]any{"agent_protocol_version": 2}
			return candidate
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizedUpdaterPolicyRequest(agent, updaterPolicyUpdateRequest{
				PollIntervalSeconds:      15,
				HeartbeatIntervalSeconds: 30,
				Targets: []updaterPolicyTargetRequest{{
					TargetID:       "worker-a",
					ServiceID:      "worker-a",
					ServiceType:    "worker",
					DeploymentMode: "systemd",
				}},
			})
			if !errors.Is(err, store.ErrInvalidSettings) {
				t.Fatalf("unowned pull agent policy error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

func TestNewEndpointlessPullNodeInitialPolicyPreservesExecutionHostOwnership(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	for _, testCase := range []struct {
		name               string
		legacySSHOwnership bool
		pullOwnerID        string
	}{
		{name: "unassigned execution host"},
		{name: "legacy SSH-owned execution host", legacySSHOwnership: true},
		{name: "same service already owns pull host", pullOwnerID: "host-agent-bootstrap"},
		{name: "another service owns pull host", pullOwnerID: "other-host-agent"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(
				store.User{Username: "pull-bootstrap-admin", Roles: []string{"super_admin"}},
				"correct horse battery",
				[]string{"api_tokens.create", "secrets.update", "system_updates.execute"},
			); err != nil {
				t.Fatal(err)
			}
			policies := store.NewMemoryUpdaterPolicyStore()
			updates := store.NewMemorySystemUpdateStore()
			if testCase.legacySSHOwnership {
				if _, err := updates.SwitchSystemUpdateExecutionHost(
					t.Context(),
					"host-bootstrap",
					0,
					store.SystemUpdateTransportSSHV1,
					"legacy-updater",
					7,
				); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.pullOwnerID != "" {
				if _, err := updates.SwitchSystemUpdateExecutionHost(
					t.Context(),
					"host-bootstrap",
					0,
					store.SystemUpdateTransportPullV2,
					testCase.pullOwnerID,
					0,
				); err != nil {
					t.Fatal(err)
				}
			}
			ownershipBefore, err := updates.GetSystemUpdateExecutionHost(
				t.Context(),
				"host-bootstrap",
			)
			if err != nil {
				t.Fatal(err)
			}
			handler := NewServer(
				store.NewMemoryStreamStore(),
				WithAuthStore(auth),
				WithAuditStore(auth),
				WithServiceRegistryStore(auth),
				WithUpdaterPolicyStore(policies),
				WithSystemUpdateStore(updates),
			)
			cookie, csrf := loginForTest(
				t,
				handler,
				"pull-bootstrap-admin",
				"correct horse battery",
			)

			createRequest := httptest.NewRequest(
				http.MethodPost,
				"/nodes/registration-tokens",
				strings.NewReader(`{
					"node_type":"update_agent",
					"node_id":"host-agent-bootstrap",
					"name":"Host Agent Bootstrap",
					"transport_mode":"pull_v2",
					"execution_host_id":"host-bootstrap"
				}`),
			)
			createRequest.AddCookie(cookie)
			createRequest.Header.Set("X-CSRF-Token", csrf)
			createResponse := httptest.NewRecorder()
			handler.ServeHTTP(createResponse, createRequest)
			if createResponse.Code != http.StatusCreated {
				t.Fatalf(
					"create endpointless pull node = %d %s",
					createResponse.Code,
					createResponse.Body.String(),
				)
			}
			pending, err := auth.GetService(t.Context(), "host-agent-bootstrap")
			if err != nil {
				t.Fatal(err)
			}
			if pending.Status != "pending" ||
				pending.TransportMode != store.SystemUpdateTransportPullV2 ||
				pending.ExecutionHostID != "host-bootstrap" ||
				pending.OwnershipEpoch != 0 ||
				len(pending.ReportedCapabilities) != 0 ||
				pending.LastReportedAt != nil {
				t.Fatalf("new endpointless pull node = %#v", pending)
			}

			policyPayload, err := json.Marshal(map[string]any{
				"expected_revision":          0,
				"poll_interval_seconds":      15,
				"heartbeat_interval_seconds": 30,
				"targets": []map[string]any{{
					"target_id":       "control-panel",
					"service_id":      "control-panel",
					"service_type":    "control_panel",
					"deployment_mode": "systemd",
					"database_name":   "autostream-kometubu_panel",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			policyRequest := httptest.NewRequest(
				http.MethodPut,
				"/system-updates/updaters/host-agent-bootstrap/settings",
				bytes.NewReader(policyPayload),
			)
			policyRequest.AddCookie(cookie)
			policyRequest.Header.Set("X-CSRF-Token", csrf)
			policyResponse := httptest.NewRecorder()
			handler.ServeHTTP(policyResponse, policyRequest)
			wantStatus := http.StatusOK
			if testCase.pullOwnerID != "" {
				wantStatus = http.StatusConflict
			}
			if policyResponse.Code != wantStatus {
				t.Fatalf(
					"save initial pull policy before configure = %d %s; want %d",
					policyResponse.Code,
					policyResponse.Body.String(),
					wantStatus,
				)
			}

			ownershipAfter, err := updates.GetSystemUpdateExecutionHost(
				t.Context(),
				"host-bootstrap",
			)
			if err != nil {
				t.Fatal(err)
			}
			if ownershipAfter != ownershipBefore {
				t.Fatalf(
					"initial policy save changed execution-host ownership: before=%#v after=%#v",
					ownershipBefore,
					ownershipAfter,
				)
			}
			if testCase.pullOwnerID != "" {
				if !strings.Contains(
					policyResponse.Body.String(),
					`"code":"system_update_ownership_conflict"`,
				) {
					t.Fatalf("active pull ownership error = %s", policyResponse.Body.String())
				}
				if _, err := policies.GetUpdaterPolicy(
					t.Context(),
					"host-agent-bootstrap",
				); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("rejected initial pull policy was persisted: %v", err)
				}
				return
			}
			stored, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-bootstrap")
			if err != nil {
				t.Fatal(err)
			}
			if stored.Revision != 1 ||
				stored.TransportMode != store.SystemUpdateTransportPullV2 ||
				stored.ExecutionHostID != "host-bootstrap" ||
				len(stored.Targets) != 1 ||
				stored.Targets[0].HostID != "host-bootstrap" ||
				stored.Targets[0].DatabaseName != "autostream-kometubu_panel" {
				t.Fatalf("initial pull policy = %#v", stored)
			}
		})
	}
}

func TestNormalizedUpdaterPolicyRequestRejectsPullSSHAndLongLivedReleaseToken(t *testing.T) {
	t.Parallel()

	agent := store.RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}
	base := updaterPolicyUpdateRequest{
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Targets: []updaterPolicyTargetRequest{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	token := "long-lived-token"
	tests := map[string]func(*updaterPolicyUpdateRequest){
		"SSH host": func(body *updaterPolicyUpdateRequest) {
			body.Hosts = validUpdaterPolicyRequest(t).Hosts
		},
		"inbound API": func(body *updaterPolicyUpdateRequest) {
			body.API.Port = 8090
		},
		"release token": func(body *updaterPolicyUpdateRequest) {
			body.GitHubToken = &token
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Targets = append([]updaterPolicyTargetRequest(nil), base.Targets...)
			mutate(&candidate)
			if _, err := normalizedUpdaterPolicyRequest(agent, candidate); err == nil {
				t.Fatal("unsafe pull settings were accepted")
			}
		})
	}
}

func TestNormalizedUpdaterPolicyRequestRequiresExplicitSystemdLocalListenPort(t *testing.T) {
	t.Parallel()
	agent := store.RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}
	base := updaterPolicyUpdateRequest{
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Targets: []updaterPolicyTargetRequest{{
			TargetID:        "observability-a",
			ServiceID:       "observability-a",
			ServiceType:     "observability",
			DeploymentMode:  "systemd",
			DatabaseName:    json.RawMessage(`"autostream_observability"`),
			LocalListenPort: json.RawMessage("8082"),
		}},
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, base)
	if err != nil {
		t.Fatalf("explicit local listener: %v", err)
	}
	if len(policy.Targets) != 1 || policy.Targets[0].LocalListenPort != 8082 {
		t.Fatalf("normalized local listener = %#v", policy.Targets)
	}
	for name, rawPort := range map[string]json.RawMessage{
		"missing":      nil,
		"public https": json.RawMessage("443"),
		"too high":     json.RawMessage("65536"),
		"null":         json.RawMessage("null"),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Targets = append([]updaterPolicyTargetRequest(nil), base.Targets...)
			candidate.Targets[0].LocalListenPort = rawPort
			if _, err := normalizedUpdaterPolicyRequest(agent, candidate); !errors.Is(err, errInvalidUpdaterLocalListenPort) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCanonicalizePullUpdaterLocalListenerStoresOnlySplitEndpoints(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "worker.example.com,observability.example.com")
	auth := store.NewMemoryAuthStore()
	register := func(serviceType string, registration store.ServiceRegistration) {
		t.Helper()
		token, err := auth.CreateServiceToken(
			t.Context(),
			serviceType,
			[]string{"service.register", "service.heartbeat"},
		)
		if err != nil {
			t.Fatal(err)
		}
		registerServiceWithTokenForTest(t, auth, token, registration)
	}
	register("worker", store.ServiceRegistration{
		ServiceID:   "worker-a",
		ServiceType: "worker",
		ServiceName: "Worker A",
		PublicURL:   "https://worker.example.com:8084",
	})
	register("observability", store.ServiceRegistration{
		ServiceID:   "observability-a",
		ServiceType: "observability",
		ServiceName: "Observability A",
		PublicURL:   "https://observability.example.com",
	})
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
	)
	policy, err := server.canonicalizePullUpdaterLocalListenerBindings(
		t.Context(),
		store.UpdaterPolicy{
			TransportMode: store.SystemUpdateTransportPullV2,
			Targets: []store.UpdaterPolicyTarget{
				{
					TargetID: "worker-a", ServiceID: "worker-a",
					ServiceType: "worker", DeploymentMode: "systemd",
					LocalListenPort: 8084,
				},
				{
					TargetID: "observability-a", ServiceID: "observability-a",
					ServiceType: "observability", DeploymentMode: "systemd",
					LocalListenPort: 8082,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Targets[0].LocalListenPort != 0 ||
		policy.Targets[1].LocalListenPort != 8082 {
		t.Fatalf("canonical listener bindings = %#v", policy.Targets)
	}
}

func TestPullUpdaterPolicyDatabaseNamesRoundTripAndRejectInvalidScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "database-policy-admin", Username: "database-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-database",
		"host-database",
		1,
		map[string]any{},
	)
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := store.NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-database",
		0,
		store.SystemUpdateTransportPullV2,
		"host-agent-database",
		0,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "database-policy-admin", "correct horse battery")
	save := func(targets []map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"expected_revision":            0,
			"poll_interval_seconds":        15,
			"heartbeat_interval_seconds":   30,
			"local_executor_policy_sha256": "sha256:" + strings.Repeat("d", 64),
			"targets":                      targets,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPut,
			"/system-updates/updaters/host-agent-database/settings",
			bytes.NewReader(payload),
		)
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	baseTarget := map[string]any{
		"target_id":       "control-panel",
		"service_id":      "control-panel",
		"host_id":         "host-database",
		"service_type":    "control_panel",
		"deployment_mode": "systemd",
	}
	invalidTargets := []map[string]any{
		baseTarget,
		{
			"target_id":       "control-panel",
			"service_id":      "control-panel",
			"service_type":    "control_panel",
			"deployment_mode": "systemd",
			"database_name":   "autostream.panel",
		},
		{
			"target_id":         "worker-main",
			"service_id":        "worker-main",
			"service_type":      "worker",
			"deployment_mode":   "systemd",
			"database_name":     "autostream_worker",
			"local_listen_port": 8084,
		},
		{
			"target_id":         "worker-main",
			"service_id":        "worker-main",
			"service_type":      "worker",
			"deployment_mode":   "systemd",
			"database_name":     nil,
			"local_listen_port": 8084,
		},
		{
			"target_id":       "control-panel",
			"service_id":      "control-panel",
			"service_type":    "control_panel",
			"deployment_mode": "docker",
			"database_name":   "autostream_panel",
		},
		{
			"target_id":       "control-panel",
			"service_id":      "control-panel",
			"service_type":    "control_panel",
			"deployment_mode": "docker",
			"database_name":   nil,
		},
	}
	for index, target := range invalidTargets {
		response := save([]map[string]any{target})
		if response.Code != http.StatusBadRequest ||
			!strings.Contains(response.Body.String(), `"code":"invalid_updater_database_name"`) {
			t.Fatalf("invalid database target %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	if _, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-database"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid database target mutated policy: %v", err)
	}

	validTarget := map[string]any{}
	for key, value := range baseTarget {
		validTarget[key] = value
	}
	validTarget["database_name"] = "  autostream-kometubu_panel  "
	observabilityTarget := map[string]any{
		"target_id":         "observability",
		"service_id":        "observability",
		"host_id":           "host-database",
		"service_type":      "observability",
		"deployment_mode":   "systemd",
		"database_name":     "autostream-kometubu_o11y",
		"local_listen_port": 8082,
	}
	response := save([]map[string]any{validTarget, observabilityTarget})
	if response.Code != http.StatusOK {
		t.Fatalf("valid database target = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Targets []struct {
			HostID          string `json:"host_id"`
			ServiceType     string `json:"service_type"`
			DatabaseName    string `json:"database_name"`
			LocalListenPort int    `json:"local_listen_port"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Targets) != 2 ||
		body.Targets[0].HostID != "host-database" ||
		body.Targets[0].ServiceType != "control_panel" ||
		body.Targets[0].DatabaseName != "autostream-kometubu_panel" ||
		body.Targets[1].ServiceType != "observability" ||
		body.Targets[1].DatabaseName != "autostream-kometubu_o11y" ||
		body.Targets[1].LocalListenPort != 8082 {
		t.Fatalf("database target response = %#v", body.Targets)
	}
	stored, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-database")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Targets) != 2 ||
		stored.Targets[0].DatabaseName != "autostream-kometubu_panel" ||
		stored.Targets[1].DatabaseName != "autostream-kometubu_o11y" ||
		stored.Targets[1].LocalListenPort != 8082 {
		t.Fatalf("stored database target = %#v", stored.Targets)
	}
	foundSaveAudit := false
	for _, event := range auth.AuditEvents() {
		if event.Action != "system_updates.updater_policy.save" {
			continue
		}
		foundSaveAudit = true
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(metadata), "autostream-kometubu_panel") ||
			strings.Contains(string(metadata), "autostream-kometubu_o11y") ||
			strings.Contains(string(metadata), "database_name") ||
			strings.Contains(strings.ToLower(string(metadata)), "dsn") {
			t.Fatalf("database target leaked into audit metadata: %s", metadata)
		}
	}
	if !foundSaveAudit {
		t.Fatal("database target save audit was not written")
	}
}

func TestPullUpdaterPolicySaveNeedsNoSecretPermissionAndAdvancesOwnershipRevision(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "pull-policy-admin", Username: "pull-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		1,
		map[string]any{},
	)
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	sharedReleaseToken := "github_pat_ssh_only"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"legacy-updater",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&sharedReleaseToken,
	); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportPullV2,
		"host-agent-a",
		0,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "pull-policy-admin", "correct horse battery")
	requestBody := updaterPolicyUpdateRequest{
		ExpectedRevision:          0,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		Targets: []updaterPolicyTargetRequest{{
			TargetID:        "legacy-slot",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  "systemd",
			LocalListenPort: json.RawMessage("8084"),
		}},
	}
	save := func(body updaterPolicyUpdateRequest) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPut,
			"/system-updates/updaters/host-agent-a/settings",
			bytes.NewReader(payload),
		)
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	created := save(requestBody)
	if created.Code != http.StatusOK {
		t.Fatalf("create pull policy = %d %s", created.Code, created.Body.String())
	}
	var createdBody map[string]any
	if err := json.NewDecoder(created.Body).Decode(&createdBody); err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"api", "hosts", "github_token_configured", "github_token_fingerprint"} {
		if _, exists := createdBody[forbiddenField]; exists {
			t.Fatalf("pull response exposed %q: %#v", forbiddenField, createdBody)
		}
	}
	if createdBody["transport_mode"] != store.SystemUpdateTransportPullV2 ||
		createdBody["execution_host_id"] != "host-a" ||
		createdBody["revision"] != float64(1) {
		t.Fatalf("pull response identity = %#v", createdBody)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch != 1 || ownership.PolicyRevision != 1 {
		t.Fatalf("ownership after create = %#v", ownership)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != sharedReleaseToken {
		t.Fatalf("pull create touched shared release token: %q, %v", storedToken, err)
	}

	requestBody.ExpectedRevision = 1
	requestBody.PollIntervalSeconds = 20
	updated := save(requestBody)
	if updated.Code != http.StatusOK {
		t.Fatalf("update pull policy = %d %s", updated.Code, updated.Body.String())
	}
	ownership, err = updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch != 1 || ownership.PolicyRevision != 2 {
		t.Fatalf("ownership after update = %#v", ownership)
	}

	requestBody.ExpectedRevision = 1
	conflict := save(requestBody)
	if conflict.Code != http.StatusConflict ||
		!strings.Contains(conflict.Body.String(), "updater_policy_revision_conflict") {
		t.Fatalf("stale pull policy save = %d %s", conflict.Code, conflict.Body.String())
	}
	stored, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if err != nil || stored.Revision != 2 {
		t.Fatalf("stale save mutated policy = %#v, %v", stored, err)
	}
	ownership, err = updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil || ownership.PolicyRevision != 2 {
		t.Fatalf("stale save mutated ownership = %#v, %v", ownership, err)
	}

	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "host-agent-a",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "block-policy-while-active",
		RequestedByUserID: "pull-policy-admin",
	}); err != nil {
		t.Fatal(err)
	}
	requestBody.ExpectedRevision = 2
	busy := save(requestBody)
	if busy.Code != http.StatusConflict ||
		!strings.Contains(busy.Body.String(), "system_update_execution_host_busy") {
		t.Fatalf("active-job pull policy save = %d %s", busy.Code, busy.Body.String())
	}
}

func TestPullObserverPolicySavePreservesExistingSSHOwnershipAndActiveJob(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "observer-policy-admin", Username: "observer-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-observer",
		"host-a",
		0,
		map[string]any{"observe_only": true},
	)
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	sharedReleaseToken := "github_pat_ssh_observer"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"legacy-updater",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&sharedReleaseToken,
	); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	sshOwnership, err := updates.SwitchSystemUpdateExecutionHost(
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
	if _, created, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "central-updater",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "ssh-job-survives-observer-policy",
		RequestedByUserID: "observer-policy-admin",
	}); err != nil || !created {
		t.Fatalf("create SSH-owned job: created=%v err=%v", created, err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "observer-policy-admin", "correct horse battery")
	body, err := json.Marshal(updaterPolicyUpdateRequest{
		ExpectedRevision:          0,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("c", 64),
		Targets: []updaterPolicyTargetRequest{{
			TargetID:        "worker-slot",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  "systemd",
			LocalListenPort: json.RawMessage("8084"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPut,
		"/system-updates/updaters/host-agent-observer/settings",
		bytes.NewReader(body),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observer pull policy save = %d %s", response.Code, response.Body.String())
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if after != sshOwnership {
		t.Fatalf("observer policy changed SSH ownership: before=%#v after=%#v", sshOwnership, after)
	}
	policy, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-observer")
	if err != nil || policy.Revision != 1 {
		t.Fatalf("observer policy = %#v, %v", policy, err)
	}
	token, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || token != sharedReleaseToken {
		t.Fatalf("observer policy touched release token: %q, %v", token, err)
	}
	for _, forbiddenField := range []string{`"api"`, `"hosts"`, `"github_token_`} {
		if strings.Contains(response.Body.String(), forbiddenField) {
			t.Fatalf("observer response exposed SSH field %q: %s", forbiddenField, response.Body.String())
		}
	}
}

func TestUpdaterPolicyResponseUsesCurrentPullAgentBindingAndStripsSSHFields(t *testing.T) {
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := updaterPolicyForHTTPTest(hostKey)
	policy.UpdaterID = "host-agent-a"
	policy.Revision = 3
	policy.TransportMode = store.SystemUpdateTransportSSHV1
	policy.Targets[0].ServiceID = "worker-a"
	agent := store.RegisteredService{
		ServiceID:       policy.UpdaterID,
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  2,
	}
	tokenConfigured := store.SecretStatus{Configured: true, Fingerprint: "must-not-leak"}
	encoded, err := json.Marshal(makeUpdaterPolicyResponse(policy, &tokenConfigured, &agent, true))
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response["transport_mode"] != store.SystemUpdateTransportPullV2 ||
		response["execution_host_id"] != "host-a" {
		t.Fatalf("response did not use current server-owned binding: %s", encoded)
	}
	for _, forbiddenField := range []string{"api", "hosts", "github_token_configured", "github_token_fingerprint"} {
		if _, exists := response[forbiddenField]; exists {
			t.Fatalf("pull response exposed stale SSH field %q: %s", forbiddenField, encoded)
		}
	}
	targets, ok := response["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("pull response targets = %#v", response["targets"])
	}
	target, ok := targets[0].(map[string]any)
	if !ok || target["service_id"] != "worker-a" || target["host_id"] != "host-a" {
		t.Fatalf("pull response target binding = %#v", targets[0])
	}
}

func validUpdaterPolicyRequest(t *testing.T) updaterPolicyUpdateRequest {
	t.Helper()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := updaterPolicyForHTTPTest(hostKey)
	return updaterPolicyUpdateRequest{
		API:                      policy.API,
		PollIntervalSeconds:      policy.PollIntervalSeconds,
		HeartbeatIntervalSeconds: policy.HeartbeatIntervalSeconds,
		Hosts:                    policy.Hosts,
		Targets:                  updaterPolicyTargetRequestsForTest(policy.Targets),
	}
}
