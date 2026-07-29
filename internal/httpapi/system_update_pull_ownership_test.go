package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestSystemUpdateAgentTopologyExposesTransportOwnershipIdentity(t *testing.T) {
	now := time.Now().UTC()
	_, updaters, _ := systemUpdateAgentTopology([]store.RegisteredService{
		{
			ServiceID:       "updater-pull",
			ServiceType:     "update_agent",
			ServiceName:     "Pull updater",
			TransportMode:   store.SystemUpdateTransportPullV2,
			ExecutionHostID: "host-a",
			OwnershipEpoch:  3,
			Status:          "registered",
			LastHeartbeatAt: &now,
		},
		{
			ServiceID:   "updater-ssh",
			ServiceType: "update_agent",
			ServiceName: "SSH updater",
			Status:      "registered",
		},
	}, now)
	if len(updaters) != 2 {
		t.Fatalf("updaters = %#v", updaters)
	}
	byID := map[string]systemUpdateAgentResponse{}
	for _, updater := range updaters {
		byID[updater.UpdaterID] = updater
	}
	pull := byID["updater-pull"]
	if pull.TransportMode != store.SystemUpdateTransportPullV2 ||
		pull.ExecutionHostID != "host-a" ||
		pull.OwnershipEpoch != 3 {
		t.Fatalf("pull updater identity = %#v", pull)
	}
	ssh := byID["updater-ssh"]
	if ssh.TransportMode != store.SystemUpdateTransportSSHV1 ||
		ssh.ExecutionHostID != "" ||
		ssh.OwnershipEpoch != 0 {
		t.Fatalf("ssh updater identity = %#v", ssh)
	}
	encoded, err := json.Marshal(pull)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"transport_mode":"pull_v2"`,
		`"execution_host_id":"host-a"`,
		`"ownership_epoch":3`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("pull updater response missing %s: %s", field, encoded)
		}
	}
}

func TestPullSystemUpdateTargetApprovalUsesServiceIdentityAndNoReleaseToken(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	now := time.Now().UTC()
	agent, service, policy := pullSystemUpdateTargetApprovalFixture(now)
	assignments, updaters, hosts := systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent, service},
		now,
		map[string]store.UpdaterPolicy{agent.ServiceID: policy},
		false,
	)
	if _, exposed := assignments["legacy-slot"]; exposed {
		t.Fatalf("pull target was keyed by target_id: %#v", assignments)
	}
	assignment, ok := assignments[service.ServiceID]
	if !ok {
		t.Fatalf("pull target assignment missing: %#v", assignments)
	}
	if !assignment.PolicyReady ||
		assignment.ReleaseTokenRequired ||
		assignment.ReleaseTokenConfigured ||
		!assignment.Available ||
		assignment.HostID != "host-a" ||
		assignment.HostReachability != "reachable" {
		t.Fatalf("pull target assignment = %#v", assignment)
	}
	if len(updaters) != 1 ||
		updaters[0].TransportMode != store.SystemUpdateTransportPullV2 ||
		updaters[0].ExecutionHostID != "host-a" ||
		updaters[0].OwnershipEpoch != 1 {
		t.Fatalf("pull updater topology = %#v", updaters)
	}
	if len(hosts) != 1 || hosts[0].HostID != "host-a" || hosts[0].Reachability != "reachable" {
		t.Fatalf("pull host topology = %#v", hosts)
	}
	target := buildSystemUpdateTarget(
		service.ServiceID,
		service.ServiceType,
		service.ServiceName,
		service.ReportedVersion,
		"",
		false,
		assignment,
		map[string]serviceUpdateInfoResponse{
			"worker": {LatestVersion: "v1.1.0", ManifestVerified: true},
		},
	)
	if !target.Eligible || target.BlockedReason != "" {
		t.Fatalf("approved pull target = %#v", target)
	}
}

func TestPullSystemUpdateTargetApprovalSynthesizesControlPanelService(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")

	now := time.Now().UTC()
	controlPanelService, err := controlPanelSystemUpdateService()
	if err != nil {
		t.Fatal(err)
	}
	agent, _, policy := pullSystemUpdateTargetApprovalFixture(now)
	policy.Targets[0] = store.UpdaterPolicyTarget{
		TargetID:       "control-panel",
		ServiceID:      "control-panel",
		HostID:         "host-a",
		ServiceType:    "control_panel",
		DeploymentMode: "systemd",
		DatabaseName:   "autostream_panel",
	}
	agent.ReportedCapabilities["target_availability"] = map[string]any{
		"control-panel": "available",
	}
	agent.ReportedCapabilities["target_availability_codes"] = map[string]any{
		"control-panel": "executor_verified",
	}
	agent.ReportedCapabilities["reported_ports"] = map[string]any{
		"control-panel": float64(36190),
	}
	agent.ReportedCapabilities["port_drift"] = map[string]any{
		"control-panel": false,
	}
	agent.ReportedCapabilities["reported_service_types"] = map[string]any{
		"control-panel": "control_panel",
	}
	agent.ReportedCapabilities["reported_deployment_modes"] = map[string]any{
		"control-panel": "systemd",
	}
	agent.ReportedCapabilities["reported_executor_policy_revisions"] = map[string]any{
		"control-panel": float64(2),
	}
	agent.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]any{
		"control-panel": policy.LocalExecutorPolicySHA256,
	}
	agent.ReportedCapabilities["reported_config_revisions"] = map[string]any{
		"control-panel": float64(7),
	}
	agent.ReportedCapabilities["reported_config_sha256"] = map[string]any{
		"control-panel": controlPanelService.AppliedConfigSHA256,
	}

	assignments, _, _ := systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{agent.ServiceID: policy},
		false,
	)
	assignment, ok := assignments["control-panel"]
	if !ok {
		t.Fatalf("synthetic Control Panel assignment missing: %#v", assignments)
	}
	if !assignment.PolicyReady ||
		assignment.PolicyBlockedReason != "" ||
		assignment.TargetServiceType != "control_panel" ||
		assignment.DeploymentMode != "systemd" ||
		assignment.HostReachability != "reachable" {
		t.Fatalf("synthetic Control Panel assignment = %#v", assignment)
	}

	policy.Targets[0].DatabaseName = ""
	assignments, _, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{agent.ServiceID: policy},
		false,
	)
	assignment = assignments["control-panel"]
	if assignment.PolicyReady ||
		assignment.PolicyBlockedReason != "updater_policy_mismatch" {
		t.Fatalf("missing Control Panel database binding = %#v", assignment)
	}
}

func TestPullObserverDoesNotReserveTargetsOrExecutionHost(t *testing.T) {
	now := time.Now().UTC()
	agent, service, policy := pullSystemUpdateTargetApprovalFixture(now)
	agent.OwnershipEpoch = 0
	agent.Capabilities = map[string]any{"observe_only": true}
	agent.ReportedCapabilities["observe_only"] = true
	agent.ReportedCapabilities["update_executor"] = false
	agent.ReportedCapabilities["mutation_enabled"] = false
	agent.ReportedCapabilities["ownership_epoch"] = float64(0)

	assignments, updaters, hosts := systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent, service},
		now,
		map[string]store.UpdaterPolicy{agent.ServiceID: policy},
		false,
	)
	if len(assignments) != 0 || len(hosts) != 0 {
		t.Fatalf("observer reserved update routing: assignments=%#v hosts=%#v", assignments, hosts)
	}
	if len(updaters) != 1 ||
		updaters[0].TransportMode != store.SystemUpdateTransportPullV2 ||
		updaters[0].ExecutionHostID != "host-a" ||
		updaters[0].OwnershipEpoch != 0 {
		t.Fatalf("observer updater topology = %#v", updaters)
	}
}

func TestPullSystemUpdateTargetApprovalFailsClosedOnReportedDrift(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*store.RegisteredService, *store.RegisteredService, *store.UpdaterPolicy)
	}{
		{
			name: "empty service id does not fall back to target id",
			mutate: func(_ *store.RegisteredService, _ *store.RegisteredService, policy *store.UpdaterPolicy) {
				policy.Targets[0].ServiceID = ""
			},
		},
		{
			name: "observe only",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["observe_only"] = true
			},
		},
		{
			name: "mutation disabled",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["mutation_enabled"] = false
			},
		},
		{
			name: "not explicitly online",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.Status = "registered"
			},
		},
		{
			name: "ownership epoch mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["ownership_epoch"] = int64(2)
			},
		},
		{
			name: "availability mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["target_availability"] = map[string]any{"worker-a": "unknown"}
			},
		},
		{
			name: "executor verification code missing",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["target_availability_codes"] = map[string]any{}
			},
		},
		{
			name: "service type mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_service_types"] = map[string]any{"worker-a": "encoder"}
			},
		},
		{
			name: "deployment mode mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_deployment_modes"] = map[string]any{"worker-a": "docker"}
			},
		},
		{
			name: "executor policy revision mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_executor_policy_revisions"] = map[string]any{"worker-a": float64(1)}
			},
		},
		{
			name: "executor policy digest mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]any{
					"worker-a": "sha256:" + strings.Repeat("b", 64),
				}
			},
		},
		{
			name: "config revision mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_config_revisions"] = map[string]any{"worker-a": float64(3)}
			},
		},
		{
			name: "reported port mismatch",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["reported_ports"] = map[string]any{"worker-a": float64(18082)}
			},
		},
		{
			name: "port drift",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["port_drift"] = map[string]any{"worker-a": true}
			},
		},
		{
			name: "port drift report missing",
			mutate: func(agent, _ *store.RegisteredService, _ *store.UpdaterPolicy) {
				agent.ReportedCapabilities["port_drift"] = map[string]any{}
			},
		},
		{
			name: "applied endpoint missing",
			mutate: func(_ *store.RegisteredService, service *store.RegisteredService, _ *store.UpdaterPolicy) {
				service.AppliedEndpoint = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agent, service, policy := pullSystemUpdateTargetApprovalFixture(now)
			test.mutate(&agent, &service, &policy)
			assignments, _, _ := systemUpdateAgentTopologyWithPolicies(
				[]store.RegisteredService{agent, service},
				now,
				map[string]store.UpdaterPolicy{agent.ServiceID: policy},
				false,
			)
			if test.name == "empty service id does not fall back to target id" {
				if len(assignments) != 0 {
					t.Fatalf("empty pull service_id produced assignments: %#v", assignments)
				}
				return
			}
			assignment, ok := assignments[service.ServiceID]
			if !ok {
				t.Fatalf("blocked pull assignment missing: %#v", assignments)
			}
			if assignment.PolicyReady || assignment.PolicyBlockedReason != "updater_policy_mismatch" {
				t.Fatalf("drifted pull assignment = %#v", assignment)
			}
		})
	}
}

func pullSystemUpdateTargetApprovalFixture(now time.Time) (store.RegisteredService, store.RegisteredService, store.UpdaterPolicy) {
	digest := "sha256:" + strings.Repeat("a", 64)
	configDigest := "sha256:" + strings.Repeat("b", 64)
	agent := store.RegisteredService{
		ServiceID:       "updater-pull",
		ServiceType:     "update_agent",
		ServiceName:     "Pull updater",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
		Status:          "online",
		Version:         "v1.0.0",
		ReportedVersion: "v1.0.0",
		LastHeartbeatAt: &now,
		ReportedCapabilities: map[string]any{
			"host_agent":             true,
			"observe_only":           false,
			"update_executor":        true,
			"mutation_enabled":       true,
			"transport_mode":         store.SystemUpdateTransportPullV2,
			"agent_protocol_version": "2",
			"execution_host_id":      "host-a",
			"ownership_epoch":        float64(1),
			"policy_revision":        float64(2),
			"policy_status":          "applied",
			"target_availability": map[string]any{
				"worker-a": "available",
			},
			"target_availability_codes": map[string]any{
				"worker-a": "executor_verified",
			},
			"reported_ports": map[string]any{
				"worker-a": float64(18081),
			},
			"port_drift": map[string]any{
				"worker-a": false,
			},
			"reported_service_types": map[string]any{
				"worker-a": "worker",
			},
			"reported_deployment_modes": map[string]any{
				"worker-a": "systemd",
			},
			"reported_executor_policy_revisions": map[string]any{
				"worker-a": float64(2),
			},
			"reported_executor_policy_sha256": map[string]any{
				"worker-a": digest,
			},
			"reported_config_revisions": map[string]any{
				"worker-a": float64(4),
			},
			"reported_config_sha256": map[string]any{
				"worker-a": configDigest,
			},
		},
	}
	service := store.RegisteredService{
		ServiceID:             "worker-a",
		ServiceType:           "worker",
		ServiceName:           "Worker A",
		Version:               "v1.0.0",
		ReportedVersion:       "v1.0.0",
		Port:                  19000,
		AppliedEndpoint:       &store.ServiceEndpoint{Host: "127.0.0.1", Port: 18081},
		EndpointRevision:      4,
		AppliedConfigRevision: 4,
		AppliedConfigSHA256:   configDigest,
	}
	policy := store.UpdaterPolicy{
		UpdaterID:                   agent.ServiceID,
		Revision:                    2,
		ProjectionRevision:          2,
		LocalExecutorPolicyRevision: 2,
		TransportMode:               store.SystemUpdateTransportPullV2,
		ExecutionHostID:             "host-a",
		LocalExecutorPolicySHA256:   digest,
		Targets: []store.UpdaterPolicyTarget{
			{
				TargetID:       "legacy-slot",
				ServiceID:      service.ServiceID,
				HostID:         "host-a",
				ServiceType:    service.ServiceType,
				DeploymentMode: "systemd",
			},
		},
	}
	return agent, service, policy
}

func TestPullSystemUpdateClaimIgnoresRequestHostAndUsesRegisteredExecutionHost(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest("host-a", map[string]string{
		"worker-a": "systemd",
		"worker-b": "systemd",
	})
	capabilities["target_hosts"] = map[string]any{
		"worker-a": "host-a",
		"worker-b": "host-b",
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	capabilities["host_statuses"] = map[string]any{"host-a": "reachable", "host-b": "reachable"}
	capabilities["host_checked_at"] = map[string]any{"host-a": checkedAt, "host-b": checkedAt}
	capabilities["host_names"] = map[string]any{"host-a": "host-a", "host-b": "host-b"}
	token := registerPullSystemUpdateAgentForOwnershipTest(t, auth, "updater-host-a", "host-a", 1, capabilities)

	updates := store.NewMemorySystemUpdateStore()
	for _, hostID := range []string{"host-a", "host-b"} {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(),
			hostID,
			0,
			store.SystemUpdateTransportPullV2,
			"updater-host-a",
			1,
		); err != nil {
			t.Fatal(err)
		}
	}
	job, created, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-b",
		TargetServiceType: "worker",
		AgentServiceID:    "updater-host-a",
		ExecutionHostID:   "host-b",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "pull-request-host-confusion",
		RequestedByUserID: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create host B job: created=%v err=%v", created, err)
	}

	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithSystemUpdateStore(updates),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/claim",
		strings.NewReader(`{"service_id":"updater-host-a","host_id":"host-b"}`),
	)
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("pull claim with forged host = %d %s", response.Code, response.Body.String())
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != store.SystemUpdateStatusQueued || active.LeaseGeneration != 0 {
		t.Fatalf("forged request host claimed host B job: %#v", active)
	}
}

func TestPullSystemUpdateClaimRejectsRegisteredOwnershipEpochDrift(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest("host-a", map[string]string{"worker-a": "systemd"})
	token := registerPullSystemUpdateAgentForOwnershipTest(t, auth, "updater-host-a", "host-a", 1, capabilities)
	updates := store.NewMemorySystemUpdateStore()
	first, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportPullV2,
		"updater-host-a",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		first.OwnershipEpoch,
		store.SystemUpdateTransportPullV2,
		"updater-host-a",
		2,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithSystemUpdateStore(updates),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/claim",
		strings.NewReader(`{"service_id":"updater-host-a"}`),
	)
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"system_update_ownership_conflict"`) {
		t.Fatalf("pull claim with stale registered epoch = %d %s", response.Code, response.Body.String())
	}
}

func TestPullSystemUpdateReportRejectsJobOnDifferentExecutionHost(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest("host-a", map[string]string{"worker-b": "systemd"})
	token := registerPullSystemUpdateAgentForOwnershipTest(t, auth, "updater-host-a", "host-a", 1, capabilities)
	updates := store.NewMemorySystemUpdateStore()
	for _, hostID := range []string{"host-a", "host-b"} {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(),
			hostID,
			0,
			store.SystemUpdateTransportPullV2,
			"updater-host-a",
			1,
		); err != nil {
			t.Fatal(err)
		}
	}
	job, created, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-b",
		TargetServiceType: "worker",
		AgentServiceID:    "updater-host-a",
		ExecutionHostID:   "host-b",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "pull-report-cross-host",
		RequestedByUserID: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create host B job: created=%v err=%v", created, err)
	}
	claim, _, err := updates.ClaimSystemUpdateJob(
		t.Context(),
		"updater-host-a",
		"host-b",
		"",
		map[string]string{"worker-b": "systemd"},
		time.Now().UTC(),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithSystemUpdateStore(updates),
	)
	body := `{"service_id":"updater-host-a","lease_token":"` + claim.LeaseToken +
		`","lease_generation":1,"sequence":1,"status":"downloading","progress":10}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/"+job.ID+"/report",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"system_update_ownership_conflict"`) {
		t.Fatalf("cross-host pull report = %d %s", response.Code, response.Body.String())
	}
}

func registerPullSystemUpdateAgentForOwnershipTest(
	t *testing.T,
	auth *store.MemoryAuthStore,
	serviceID, executionHostID string,
	ownershipEpoch int64,
	capabilities map[string]any,
) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{
		"service.register",
		"service.heartbeat",
		"updates.claim",
		"updates.report",
		"updates.authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent A",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: executionHostID,
		OwnershipEpoch:  ownershipEpoch,
		Version:         "v1.0.0",
		Capabilities:    capabilities,
	}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{
		ServiceID:    registration.ServiceID,
		Status:       "online",
		Version:      registration.Version,
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	return token
}
