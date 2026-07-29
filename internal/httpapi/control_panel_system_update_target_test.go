package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestControlPanelSystemUpdateServiceUsesExactRuntimeIdentity(t *testing.T) {
	t.Setenv("SERVICE_ID", "attacker-selected-alias")
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")

	service, err := controlPanelSystemUpdateService()
	if err != nil {
		t.Fatal(err)
	}
	expectedConfigSHA256, err := updateagent.SystemdConfigurePortSidecarSHA256(
		"control_panel",
		36190,
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if service.ServiceID != "control-panel" ||
		service.ServiceType != "control_panel" ||
		service.ServiceName != "Control Panel" ||
		service.Host != "127.0.0.1" ||
		service.Port != 36190 ||
		service.SSLEnabled ||
		service.PublicURL != "http://127.0.0.1:36190" ||
		service.EndpointRevision != 7 ||
		service.AppliedConfigRevision != 7 ||
		service.AppliedConfigSHA256 != expectedConfigSHA256 ||
		service.DesiredEndpoint == nil ||
		service.AppliedEndpoint == nil ||
		*service.DesiredEndpoint != (store.ServiceEndpoint{
			Host:      "127.0.0.1",
			Port:      36190,
			PublicURL: "http://127.0.0.1:36190",
		}) ||
		*service.AppliedEndpoint != *service.DesiredEndpoint {
		t.Fatalf("synthetic Control Panel service = %#v", service)
	}
}

func TestControlPanelSystemUpdateServiceUsesRuntimeDefaults(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "")

	service, err := controlPanelSystemUpdateService()
	if err != nil {
		t.Fatal(err)
	}
	if service.Host != "127.0.0.1" ||
		service.Port != 8080 ||
		service.EndpointRevision != 1 ||
		service.AppliedConfigRevision != 1 {
		t.Fatalf("default synthetic Control Panel service = %#v", service)
	}
}

func TestControlPanelSystemUpdateServiceRejectsUnsafeRuntimeState(t *testing.T) {
	tests := []struct {
		name     string
		bindAddr string
		revision string
	}{
		{name: "wildcard", bindAddr: "0.0.0.0:36190", revision: "1"},
		{name: "private address", bindAddr: "192.168.1.10:36190", revision: "1"},
		{name: "other ipv4 loopback", bindAddr: "127.0.0.2:36190", revision: "1"},
		{name: "ipv6 loopback", bindAddr: "[::1]:36190", revision: "1"},
		{name: "mapped ipv4 loopback", bindAddr: "[::ffff:127.0.0.1]:36190", revision: "1"},
		{name: "hostname", bindAddr: "localhost:36190", revision: "1"},
		{name: "privileged port", bindAddr: "127.0.0.1:443", revision: "1"},
		{name: "zero port", bindAddr: "127.0.0.1:0", revision: "1"},
		{name: "out of range port", bindAddr: "127.0.0.1:65536", revision: "1"},
		{name: "missing port", bindAddr: "127.0.0.1", revision: "1"},
		{name: "invalid config revision", bindAddr: "127.0.0.1:36190", revision: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_BIND_ADDR", test.bindAddr)
			t.Setenv("AUTOSTREAM_CONFIG_REVISION", test.revision)
			if service, err := controlPanelSystemUpdateService(); err == nil {
				t.Fatalf("unsafe runtime state produced service: %#v", service)
			}
		})
	}
}

func TestControlPanelSystemUpdateServiceInjectionIsPolicyScoped(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	servicesByID := map[string]store.RegisteredService{
		"worker-a": {
			ServiceID:   "worker-a",
			ServiceType: "worker",
		},
	}
	workerPolicy := store.UpdaterPolicy{
		TransportMode: store.SystemUpdateTransportPullV2,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	if err := addControlPanelSystemUpdateServiceForPolicy(
		servicesByID,
		workerPolicy,
	); err != nil {
		t.Fatalf("non-Control Panel policy read Control Panel runtime: %v", err)
	}
	if len(servicesByID) != 1 {
		t.Fatalf("non-Control Panel policy changed services: %#v", servicesByID)
	}

	aliasPolicy := workerPolicy
	aliasPolicy.Targets = []store.UpdaterPolicyTarget{{
		TargetID:       "control-panel-alias",
		ServiceID:      "control-panel",
		ServiceType:    "control_panel",
		DeploymentMode: "systemd",
	}}
	if err := addControlPanelSystemUpdateServiceForPolicy(
		servicesByID,
		aliasPolicy,
	); err != nil {
		t.Fatalf("non-exact Control Panel policy read runtime: %v", err)
	}
	if len(servicesByID) != 1 {
		t.Fatalf("non-exact Control Panel policy changed services: %#v", servicesByID)
	}

	exactPolicy := aliasPolicy
	exactPolicy.Targets = append(
		[]store.UpdaterPolicyTarget(nil),
		aliasPolicy.Targets...,
	)
	exactPolicy.Targets[0].TargetID = "control-panel"
	if err := addControlPanelSystemUpdateServiceForPolicy(
		servicesByID,
		exactPolicy,
	); err == nil {
		t.Fatal("exact Control Panel policy accepted unsafe runtime")
	}

	sshPolicy := exactPolicy
	sshPolicy.TransportMode = store.SystemUpdateTransportSSHV1
	if err := addControlPanelSystemUpdateServiceForPolicy(
		servicesByID,
		sshPolicy,
	); err != nil {
		t.Fatalf("SSH Control Panel policy read pull runtime: %v", err)
	}
	if len(servicesByID) != 1 {
		t.Fatalf("SSH Control Panel policy changed services: %#v", servicesByID)
	}
}

func TestHostAgentPolicySynthesizesControlPanelEndpoint(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")
	fixture := newControlPanelPullPolicyHTTPFixture(t)

	request := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-panel","current_revision":0}`),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("host agent policy status=%d body=%s", response.Code, response.Body.String())
	}
	var body hostAgentPolicyResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Targets) != 1 ||
		body.Targets[0].ServiceID != "control-panel" ||
		body.Targets[0].ServiceType != "control_panel" ||
		body.Targets[0].AppliedConfigRevision != 7 ||
		body.Targets[0].DesiredEndpoint == nil ||
		body.Targets[0].AppliedEndpoint == nil ||
		body.Targets[0].LocalListenEndpoint == nil ||
		body.Targets[0].LocalHealthEndpoint == nil ||
		body.Targets[0].AppliedEndpoint.Port != 36190 ||
		*body.Targets[0].LocalListenEndpoint != *body.Targets[0].LocalHealthEndpoint {
		t.Fatalf("synthetic Control Panel policy target = %#v", body.Targets)
	}
}

func TestHostAgentConfigureProjectionSynthesizesControlPanelAndDatabase(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.com")
	fixture := newControlPanelPullPolicyHTTPFixture(t)

	request := httptest.NewRequest(http.MethodPost, "/api/node-agent/configure/stage", nil)
	projection, err := fixture.server.hostAgentConfigurePolicyProjection(
		t.Context(),
		request,
		fixture.agent,
		1001,
		1002,
	)
	if err != nil {
		t.Fatal(err)
	}
	var policy updateagent.LocalExecutorPolicy
	if err := json.Unmarshal(projection.Policy, &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy.Targets) != 1 ||
		policy.Targets[0].ServiceID != "control-panel" ||
		policy.Targets[0].ServiceType != "control_panel" ||
		policy.Targets[0].DatabaseName != "autostream_panel" ||
		policy.Targets[0].EndpointRevision != 7 ||
		policy.Targets[0].ConfigRevision != 7 ||
		policy.Targets[0].LocalListen != (updateagent.LocalExecutorEndpoint{
			Host: "127.0.0.1",
			Port: 36190,
		}) {
		t.Fatalf("synthetic Control Panel configure policy = %#v", policy)
	}
}

func TestPullClaimEligibilitySynthesizesControlPanelService(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")
	fixture := newControlPanelPullPolicyHTTPFixture(t)
	controlPanelService, err := controlPanelSystemUpdateService()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := fixture.agent
	agent.OwnershipEpoch = 1
	agent.Status = "online"
	agent.LastHeartbeatAt = &now
	agent.ReportedCapabilities = map[string]any{
		"host_agent":             true,
		"observe_only":           false,
		"update_executor":        true,
		"mutation_enabled":       true,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      "host-panel",
		"ownership_epoch":        float64(1),
		"policy_revision":        float64(fixture.policy.ProjectionRevision),
		"policy_status":          "applied",
		"target_availability": map[string]any{
			"control-panel": "available",
		},
		"target_availability_codes": map[string]any{
			"control-panel": "executor_verified",
		},
		"reported_ports": map[string]any{
			"control-panel": float64(36190),
		},
		"port_drift": map[string]any{
			"control-panel": false,
		},
		"reported_service_types": map[string]any{
			"control-panel": "control_panel",
		},
		"reported_deployment_modes": map[string]any{
			"control-panel": "systemd",
		},
		"reported_executor_policy_revisions": map[string]any{
			"control-panel": float64(fixture.policy.LocalExecutorPolicyRevision),
		},
		"reported_executor_policy_sha256": map[string]any{
			"control-panel": fixture.policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			"control-panel": float64(7),
		},
		"reported_config_sha256": map[string]any{
			"control-panel": controlPanelService.AppliedConfigSHA256,
		},
	}

	eligible, err := fixture.server.systemUpdateTargetsForAgentHostClaim(
		t.Context(),
		agent,
		"host-panel",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 1 || eligible["control-panel"] != "systemd" {
		t.Fatalf("Control Panel eligible targets = %#v", eligible)
	}
}

func TestSSHControlPanelClaimIgnoresPullRuntimeEnvironment(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	const (
		updaterID = "central-updater"
		hostID    = "host-panel"
	)
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policies := store.NewMemoryUpdaterPolicyStore()
	policy, err := policies.SaveUpdaterPolicy(
		t.Context(),
		updaterID,
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
				HostID:        hostID,
				Name:          "Panel host",
				Address:       "panel.example.com",
				Port:          55850,
				User:          "autostream-update-host",
				Arch:          "amd64",
				HostPublicKey: hostKey,
			}},
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       "control-panel",
				ServiceID:      "control-panel",
				HostID:         hostID,
				ServiceType:    "control_panel",
				DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := centralUpdateCapabilitiesForTest(
		hostID,
		map[string]string{"control-panel": "systemd"},
	)
	capabilities["policy_revision"] = policy.ProjectionRevision
	capabilities["policy_status"] = "applied"
	now := time.Now().UTC()
	agent := store.RegisteredService{
		ServiceID:            updaterID,
		ServiceType:          "update_agent",
		TransportMode:        store.SystemUpdateTransportSSHV1,
		Status:               "online",
		LastHeartbeatAt:      &now,
		Capabilities:         capabilities,
		ReportedCapabilities: capabilities,
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithServiceRegistryStore(store.NewMemoryAuthStore()),
		WithUpdaterPolicyStore(policies),
	)
	eligible, err := server.systemUpdateTargetsForAgentHostClaim(
		t.Context(),
		agent,
		hostID,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 1 || eligible["control-panel"] != "systemd" {
		t.Fatalf("SSH Control Panel eligible targets = %#v", eligible)
	}
}

func TestControlPanelPullUpdaterActivationTargetRequiresExactPolicyTarget(t *testing.T) {
	t.Setenv("AUTOSTREAM_BIND_ADDR", "127.0.0.1:36190")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "7")
	exact := store.UpdaterPolicy{
		TransportMode: store.SystemUpdateTransportPullV2,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "control-panel",
			ServiceID:      "control-panel",
			ServiceType:    "control_panel",
			DeploymentMode: "systemd",
		}},
	}
	target, err := controlPanelPullUpdaterActivationTarget(exact)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil ||
		target.ServiceID != "control-panel" ||
		target.ServiceType != "control_panel" ||
		target.EndpointRevision != 7 ||
		target.AppliedConfigRevision != 7 ||
		target.AppliedConfigSHA256 == "" ||
		target.AppliedEndpoint.Host != "127.0.0.1" ||
		target.AppliedEndpoint.Port != 36190 {
		t.Fatalf("Control Panel activation target = %#v", target)
	}

	for name, mutate := range map[string]func(*store.UpdaterPolicyTarget){
		"target id": func(target *store.UpdaterPolicyTarget) {
			target.TargetID = "control-panel-alias"
		},
		"service id": func(target *store.UpdaterPolicyTarget) {
			target.ServiceID = "control-panel-alias"
		},
		"service type": func(target *store.UpdaterPolicyTarget) {
			target.ServiceType = "worker"
		},
		"deployment mode": func(target *store.UpdaterPolicyTarget) {
			target.DeploymentMode = "docker"
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := exact
			policy.Targets = append([]store.UpdaterPolicyTarget(nil), exact.Targets...)
			mutate(&policy.Targets[0])
			target, err := controlPanelPullUpdaterActivationTarget(policy)
			if err != nil {
				t.Fatal(err)
			}
			if target != nil {
				t.Fatalf("non-exact policy produced synthetic target: %#v", target)
			}
		})
	}
}

type controlPanelPullPolicyHTTPFixture struct {
	server *Server
	token  store.ServiceToken
	agent  store.RegisteredService
	policy store.UpdaterPolicy
}

func newControlPanelPullPolicyHTTPFixture(t *testing.T) controlPanelPullPolicyHTTPFixture {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{"service.register", "service.heartbeat", "service.config.read"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{
		ServiceID:       "host-agent-panel",
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent Panel",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-panel",
		OwnershipEpoch:  0,
		Version:         "v1.0.0",
	}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	agent, err := auth.GetService(t.Context(), registration.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	policy, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		registration.ServiceID,
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-panel",
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID:       "control-panel",
				ServiceID:      "control-panel",
				HostID:         "host-panel",
				ServiceType:    "control_panel",
				DeploymentMode: updateagent.ModeSystemd,
				DatabaseName:   "autostream_panel",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return controlPanelPullPolicyHTTPFixture{
		server: NewServer(
			store.NewMemoryStreamStore(),
			WithAuthStore(auth),
			WithUpdaterPolicyStore(policies),
			WithSystemUpdateStore(updates),
		),
		token:  token,
		agent:  agent,
		policy: policy,
	}
}
