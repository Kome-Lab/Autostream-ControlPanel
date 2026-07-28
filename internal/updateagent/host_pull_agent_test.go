package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestObserveOnlyHostAgentUsesEndpointlessOutboundControlLoop(t *testing.T) {
	var mu sync.Mutex
	var registration map[string]any
	var heartbeats []map[string]any
	var policyRequests []map[string]any
	var forbiddenCalls atomic.Int32
	heartbeatObserved := make(chan struct{}, 1)

	statusPort, err := net.Listen("tcp", "127.0.0.1:8090")
	if err != nil {
		t.Skipf("cannot reserve legacy updater status port for listener-free proof: %v", err)
	}
	defer statusPort.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "update-jobs") ||
			strings.Contains(r.URL.Path, "authorize") ||
			strings.Contains(r.URL.Path, "mutation-grants") {
			forbiddenCalls.Add(1)
			http.Error(w, "mutation endpoint must not be used", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/services/register":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			registration = body
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"service_id":"host-agent-a","service_type":"update_agent","transport_mode":"pull_v2","execution_host_id":"host-a","ownership_epoch":0}`))
		case "/services/host-agent/policy":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			policyRequests = append(policyRequests, body)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(`{
				"service_id":"host-agent-a",
				"transport_mode":"pull_v2",
				"execution_host_id":"host-a",
				"ownership_epoch":0,
				"revision":7,
				"source_policy_revision":3,
				"local_executor_policy_revision":0,
				"observe_only":true,
				"targets":[{
					"service_id":"worker-a",
					"service_type":"worker",
					"deployment_mode":"systemd",
					"desired_endpoint":{"host":"127.0.0.1","port":18082,"ssl_enabled":false,"public_url":"http://127.0.0.1:18082"},
					"applied_endpoint":{"host":"127.0.0.1","port":18081,"ssl_enabled":false,"public_url":"http://127.0.0.1:18081"},
					"local_listen_endpoint":{"host":"127.0.0.1","port":18082,"ssl_enabled":false,"public_url":"http://127.0.0.1:18082"}
				}]
			}`))
		case "/services/heartbeat":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			heartbeats = append(heartbeats, body)
			mu.Unlock()
			select {
			case heartbeatObserved <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	bootstrap := managedHostAgentBootstrap(server.URL)
	agent, err := NewObserveOnlyHostAgent(bootstrap, HostPullAgentOptions{
		StateDir:          t.TempDir(),
		HTTPClient:        server.Client(),
		PollInterval:      5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		ObserveTargets: func(_ context.Context, policy HostAgentPolicy) ([]HostTargetObservation, error) {
			if len(policy.Targets) != 1 || policy.Targets[0].ServiceID != "worker-a" {
				t.Fatalf("unexpected policy: %#v", policy)
			}
			return []HostTargetObservation{{
				ServiceID:        "worker-a",
				Availability:     TargetAvailabilityAvailable,
				ReportedPort:     18081,
				AvailabilityCode: "healthy",
			}}, nil
		},
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	select {
	case <-heartbeatObserved:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("observe-only heartbeat was not sent")
	}
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}

	if forbiddenCalls.Load() != 0 {
		t.Fatalf("observe-only agent called %d mutation endpoints", forbiddenCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if registration == nil {
		t.Fatal("registration was not sent")
	}
	for _, forbidden := range []string{"host", "port", "ssl_enabled", "public_url", "execution_host_id", "ownership_epoch"} {
		if _, exists := registration[forbidden]; exists {
			t.Fatalf("endpointless registration included %q: %#v", forbidden, registration)
		}
	}
	if registration["transport_mode"] != HostTransportPullV2 {
		t.Fatalf("transport_mode = %#v", registration["transport_mode"])
	}
	if len(policyRequests) == 0 {
		t.Fatal("policy request was not captured")
	}
	for _, forbidden := range []string{"execution_host_id", "ownership_epoch", "host_id"} {
		if _, exists := policyRequests[0][forbidden]; exists {
			t.Fatalf("policy request self-asserted %q: %#v", forbidden, policyRequests[0])
		}
	}
	if policyRequests[0]["service_id"] != "host-agent-a" || policyRequests[0]["current_revision"] != float64(0) {
		t.Fatalf("policy request = %#v", policyRequests[0])
	}
	if len(heartbeats) == 0 {
		t.Fatal("heartbeat was not captured")
	}
	capabilities, ok := heartbeats[len(heartbeats)-1]["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("heartbeat capabilities = %#v", heartbeats[len(heartbeats)-1]["capabilities"])
	}
	if capabilities["observe_only"] != true ||
		capabilities["agent_protocol_version"] != HostAgentProtocolVersion ||
		capabilities["execution_host_id"] != "host-a" ||
		capabilities["ownership_epoch"] != float64(0) ||
		capabilities["policy_revision"] != float64(7) {
		t.Fatalf("unexpected host capabilities: %#v", capabilities)
	}
	availability, ok := capabilities["target_availability"].(map[string]any)
	if !ok || availability["worker-a"] != TargetAvailabilityAvailable {
		t.Fatalf("target availability = %#v", capabilities["target_availability"])
	}
	drift, ok := capabilities["port_drift"].(map[string]any)
	if !ok || drift["worker-a"] != true {
		t.Fatalf("port drift = %#v", capabilities["port_drift"])
	}
	if _, exists := heartbeats[len(heartbeats)-1]["api"]; exists {
		t.Fatalf("portless heartbeat included api endpoint: %#v", heartbeats[len(heartbeats)-1])
	}
}

func TestObserveOnlyHostAgentPreservesInterruptedJournalWithoutClaimOrReport(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.SetActive(&UpdateJob{
		ID: "job-awaiting-pull-v2", TargetID: "worker-a", ServiceType: "worker",
		DeploymentMode: ModeSystemd, TargetVersion: "v2.0.0", ReportSequence: 3,
	}); err != nil {
		t.Fatal(err)
	}

	var forbiddenCalls atomic.Int32
	heartbeatObserved := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"service_id":"host-agent-a","service_type":"update_agent","transport_mode":"pull_v2","execution_host_id":"host-a","ownership_epoch":1}`))
		case "/services/host-agent/policy":
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
		case "/services/heartbeat":
			select {
			case heartbeatObserved <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			forbiddenCalls.Add(1)
			http.Error(w, "observe-only attempted a job operation", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	agent, err := NewObserveOnlyHostAgent(managedHostAgentBootstrap(server.URL), HostPullAgentOptions{
		StateDir:          stateDir,
		HTTPClient:        server.Client(),
		PollInterval:      5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	select {
	case <-heartbeatObserved:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("heartbeat was not observed")
	}
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	if forbiddenCalls.Load() != 0 {
		t.Fatalf("observe-only agent attempted %d job operations", forbiddenCalls.Load())
	}
	reopened, err := OpenJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if active := reopened.Active(); active == nil || active.ID != "job-awaiting-pull-v2" {
		t.Fatalf("recovery cursor was changed: %#v", active)
	}
}

func TestFetchHostAgentPolicyRejectsUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{
			"service_id":"host-agent-a",
			"transport_mode":"pull_v2",
			"execution_host_id":"host-a",
			"ownership_epoch":1,
			"revision":1,
			"source_policy_revision":1,
			"local_executor_policy_revision":1,
			"observe_only":false,
			"targets":[],
			"unexpected":"must fail closed"
		}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	if _, _, err := client.FetchHostAgentPolicy(context.Background(), "host-agent-a", 0); err == nil || !strings.Contains(err.Error(), "decode host agent policy") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestFetchHostAgentPolicyRejectsCrossHostBinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{
			"service_id":"different-host-agent",
			"transport_mode":"pull_v2",
			"execution_host_id":"host-b",
			"ownership_epoch":1,
			"revision":1,
			"source_policy_revision":1,
			"local_executor_policy_revision":1,
			"observe_only":false,
			"targets":[]
		}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	if _, _, err := client.FetchHostAgentPolicy(context.Background(), "host-agent-a", 0); err == nil ||
		!strings.Contains(err.Error(), "identity, revision, or mode is invalid") {
		t.Fatalf("cross-host policy error = %v", err)
	}
}

func TestFetchHostAgentPolicyAcceptsSamePolicyRevisionWithRefreshedTargetConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{
			"service_id":"host-agent-a",
			"transport_mode":"pull_v2",
			"execution_host_id":"host-a",
			"ownership_epoch":1,
			"revision":4,
			"source_policy_revision":2,
			"local_executor_policy_revision":3,
			"local_executor_policy_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"observe_only":false,
			"targets":[{
				"service_id":"worker-a",
				"service_type":"worker",
				"deployment_mode":"systemd",
				"applied_config_revision":2,
				"desired_endpoint":{"host":"127.0.0.1","port":28084,"public_url":"http://127.0.0.1:28084"},
				"applied_endpoint":{"host":"127.0.0.1","port":28084,"public_url":"http://127.0.0.1:28084"},
				"local_listen_endpoint":{"host":"127.0.0.1","port":28084,"public_url":"http://127.0.0.1:28084"},
				"local_health_endpoint":{"host":"127.0.0.1","port":28084,"public_url":"http://127.0.0.1:28084"}
			}]
		}`))
	}))
	defer server.Close()

	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	policy, changed, err := client.FetchHostAgentPolicy(context.Background(), "host-agent-a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || policy == nil || len(policy.Targets) != 1 ||
		policy.Targets[0].AppliedConfigRevision != 2 ||
		policy.Targets[0].AppliedEndpoint == nil ||
		policy.Targets[0].AppliedEndpoint.Port != 28084 {
		t.Fatalf("same-revision refreshed policy = %#v changed=%v", policy, changed)
	}
}

func TestHostAgentPolicyRejectsNonHTTPURLAndPaddedDuplicateTarget(t *testing.T) {
	base := HostAgentPolicy{
		ServiceID:                   "host-agent-a",
		TransportMode:               HostTransportPullV2,
		ExecutionHostID:             "host-a",
		OwnershipEpoch:              1,
		Revision:                    1,
		SourcePolicyRevision:        1,
		LocalExecutorPolicyRevision: 1,
		LocalExecutorPolicySHA256:   "sha256:" + strings.Repeat("a", 64),
		ObserveOnly:                 false,
	}
	t.Run("non HTTP endpoint", func(t *testing.T) {
		policy := base
		policy.Targets = []HostAgentPolicyTarget{{
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  ModeSystemd,
			DesiredEndpoint: &HostAgentEndpoint{Host: "127.0.0.1", Port: 18081, PublicURL: "file:///tmp/fake-health"},
		}}
		if err := policy.validateForService("host-agent-a", 0); err == nil {
			t.Fatal("non-HTTP endpoint was accepted")
		}
	})
	t.Run("padded duplicate", func(t *testing.T) {
		policy := base
		policy.Targets = []HostAgentPolicyTarget{
			{ServiceID: "worker-a", ServiceType: "worker", DeploymentMode: ModeSystemd},
			{ServiceID: " worker-a ", ServiceType: "worker", DeploymentMode: ModeSystemd},
		}
		if err := policy.validateForService("host-agent-a", 0); err == nil {
			t.Fatal("padded duplicate target was accepted")
		}
	})
}

func TestHostAgentPolicyObserveOnlyMatchesOwnershipEpochExactly(t *testing.T) {
	base := HostAgentPolicy{
		ServiceID: "host-agent-a", TransportMode: HostTransportPullV2,
		ExecutionHostID: "host-a", Revision: 1,
		SourcePolicyRevision: 1, LocalExecutorPolicyRevision: 1,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
	}
	for name, policy := range map[string]HostAgentPolicy{
		"observer active flag": func() HostAgentPolicy {
			value := base
			value.OwnershipEpoch = 0
			value.ObserveOnly = false
			return value
		}(),
		"owner observer flag": func() HostAgentPolicy {
			value := base
			value.OwnershipEpoch = 1
			value.ObserveOnly = true
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := policy.validateForService("host-agent-a", 0); err == nil {
				t.Fatal("inconsistent observe_only/ownership_epoch was accepted")
			}
		})
	}
	observer := base
	observer.OwnershipEpoch, observer.ObserveOnly = 0, true
	if err := observer.validateForService("host-agent-a", 0); err != nil {
		t.Fatalf("valid observer policy rejected: %v", err)
	}
	active := base
	active.OwnershipEpoch, active.ObserveOnly = 1, false
	if err := active.validateForService("host-agent-a", 0); err != nil {
		t.Fatalf("valid active policy rejected: %v", err)
	}
}

func TestHostAgentPolicyObserverExecutorBindingIsAllOrNothing(t *testing.T) {
	base := HostAgentPolicy{
		ServiceID: "host-agent-a", TransportMode: HostTransportPullV2,
		ExecutionHostID: "host-a", OwnershipEpoch: 0,
		Revision: 3, SourcePolicyRevision: 2, ObserveOnly: true,
	}
	if err := base.validateForService("host-agent-a", 0); err != nil {
		t.Fatalf("valid unpinned observer rejected: %v", err)
	}

	ready := base
	ready.LocalExecutorPolicyRevision = 5
	ready.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("b", 64)
	if err := ready.validateForService("host-agent-a", 0); err != nil {
		t.Fatalf("valid ready observer rejected: %v", err)
	}

	revisionOnly := base
	revisionOnly.LocalExecutorPolicyRevision = 5
	if err := revisionOnly.validateForService("host-agent-a", 0); err == nil {
		t.Fatal("observer executor revision without digest was accepted")
	}
	digestOnly := base
	digestOnly.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("b", 64)
	if err := digestOnly.validateForService("host-agent-a", 0); err == nil {
		t.Fatal("observer executor digest without revision was accepted")
	}
}

func TestRegisterHostAgentDoesNotFollowRedirect(t *testing.T) {
	var redirectedCalls atomic.Int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedCalls.Add(1)
		if r.Header.Get("Authorization") == "Bearer runtime-token" {
			t.Error("runtime token reached redirect destination")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service_id":"host-agent-a","service_type":"update_agent","transport_mode":"pull_v2","execution_host_id":"host-a","ownership_epoch":1}`))
	}))
	defer redirected.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client := PanelClient{BaseURL: redirector.URL, Token: "runtime-token", HTTP: redirector.Client()}
	if _, err := client.RegisterHostAgent(context.Background(), managedHostAgentBootstrap(redirector.URL), nil); err == nil {
		t.Fatal("redirected registration unexpectedly succeeded")
	}
	if redirectedCalls.Load() != 0 {
		t.Fatalf("registration followed %d redirects", redirectedCalls.Load())
	}
}

func TestNewObserveOnlyHostAgentUsesDedicatedStateDirectory(t *testing.T) {
	bootstrap := managedHostAgentBootstrap("https://panel.example.com")
	agent, err := NewObserveOnlyHostAgent(bootstrap, HostPullAgentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if agent.StateDir != HostPullAgentStateDir {
		t.Fatalf("state dir = %q, want %q", agent.StateDir, HostPullAgentStateDir)
	}
}

func TestObserveOnlyHostAgentFailsClosedWhenJournalCannotOpen(t *testing.T) {
	agent, err := NewObserveOnlyHostAgent(managedHostAgentBootstrap("https://panel.example.com"), HostPullAgentOptions{
		StateDir: t.TempDir(),
		OpenJournal: func(string) (*Journal, error) {
			return nil, errors.New("unavailable")
		},
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "open host pull agent journal") {
		t.Fatalf("journal open error = %v", err)
	}
}

func TestHostAgentCapabilitiesAdvertiseSelfUpdateRecoveryProtocol(t *testing.T) {
	state, err := NewHostSelfUpdateState("v1.7.8", "v1.7.8")
	if err != nil {
		t.Fatal(err)
	}
	agent := &HostPullAgent{AgentVersion: "v1.7.8"}
	agent.selfUpdateStatus.Store(&HostSelfUpdateRuntimeStatus{
		State:                   state,
		CurrentSlot:             HostSelfUpdateSlotA,
		ExecutorVersion:         "v1.7.8",
		ExecutorProtocolVersion: LocalExecutorMutationProtocolVersion,
	})
	capabilities := agent.capabilities(
		HostAgentBinding{},
		nil,
		nil,
		false,
	)
	if got := capabilities["recovery_protocol_version"]; got != HostSelfUpdateRecoveryProtocolVersion {
		t.Fatalf(
			"recovery_protocol_version = %#v, want %d",
			got,
			HostSelfUpdateRecoveryProtocolVersion,
		)
	}
	if capabilities["self_update_ready"] != true ||
		capabilities["self_update_phase"] != HostSelfUpdatePhaseStable ||
		capabilities["self_update_active_agent_version"] != "v1.7.8" ||
		capabilities["self_update_active_executor_version"] != "v1.7.8" {
		t.Fatalf(
			"stable self-update runtime was not emitted: %#v",
			capabilities,
		)
	}
	agentProtocol, err := strconv.Atoi(
		capabilities["agent_protocol_version"].(string),
	)
	if err != nil {
		t.Fatal(err)
	}
	if agentProtocol <
		store.SystemUpdateHostSelfUpdateMinimumAgentProtocolVersion ||
		capabilities["executor_protocol_version"].(int) <
			store.SystemUpdateHostSelfUpdateMinimumExecutorProtocolVersion ||
		capabilities["mutation_protocol_version"].(int) <
			store.SystemUpdateHostSelfUpdateMinimumMutationProtocolVersion ||
		capabilities["recovery_protocol_version"].(int) <
			store.SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion {
		t.Fatalf(
			"actual capabilities do not satisfy Store readiness contract: %#v",
			capabilities,
		)
	}
}

func TestHostAgentCapabilitiesDoNotTreatAppliedPortAsLocalObservation(t *testing.T) {
	agent := &HostPullAgent{}
	policy := &HostAgentPolicy{
		Revision: 1,
		Targets: []HostAgentPolicyTarget{{
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  ModeSystemd,
			DesiredEndpoint: &HostAgentEndpoint{Host: "127.0.0.1", Port: 18082, PublicURL: "http://127.0.0.1:18082"},
			AppliedEndpoint: &HostAgentEndpoint{Host: "127.0.0.1", Port: 18081, PublicURL: "http://127.0.0.1:18081"},
		}},
	}
	for name, testCase := range map[string]struct {
		observations []HostTargetObservation
		failed       bool
	}{
		"no observer":     {},
		"observer failed": {failed: true},
		"partial result":  {observations: []HostTargetObservation{{ServiceID: "different-target", Availability: TargetAvailabilityAvailable, ReportedPort: 19000}}},
	} {
		t.Run(name, func(t *testing.T) {
			capabilities := agent.capabilities(HostAgentBinding{}, policy, testCase.observations, testCase.failed)
			reportedPorts := capabilities["reported_ports"].(map[string]int)
			if _, exists := reportedPorts["worker-a"]; exists {
				t.Fatalf("applied endpoint was synthesized as locally reported: %#v", reportedPorts)
			}
			portDrift := capabilities["port_drift"].(map[string]bool)
			if _, exists := portDrift["worker-a"]; exists {
				t.Fatalf("unknown local port was reported as drift=false: %#v", portDrift)
			}
		})
	}
}

func TestHostAgentPortDriftNeverComparesLocalPortToAdvertisedEndpoint(t *testing.T) {
	agent := &HostPullAgent{}
	policy := &HostAgentPolicy{
		Revision: 1,
		Targets: []HostAgentPolicyTarget{{
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  ModeDocker,
			DesiredEndpoint: &HostAgentEndpoint{Host: "worker.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://worker.example.com"},
			AppliedEndpoint: &HostAgentEndpoint{Host: "worker.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://worker.example.com"},
		}},
	}
	observation := []HostTargetObservation{{
		ServiceID: "worker-a", Availability: TargetAvailabilityAvailable, ReportedPort: 18081,
	}}
	capabilities := agent.capabilities(HostAgentBinding{}, policy, observation, false)
	if drift := capabilities["port_drift"].(map[string]bool); len(drift) != 0 {
		t.Fatalf("advertised :443 was compared with local :18081: %#v", drift)
	}

	policy.Targets[0].LocalListenEndpoint = &HostAgentEndpoint{
		Host: "127.0.0.1", Port: 18082, PublicURL: "http://127.0.0.1:18082",
	}
	capabilities = agent.capabilities(HostAgentBinding{}, policy, observation, false)
	if drift := capabilities["port_drift"].(map[string]bool); drift["worker-a"] != true {
		t.Fatalf("explicit local listen :18082 was not compared with local :18081: %#v", drift)
	}
}

func TestObserveOnlyHostAgentBacksOffAfterPanelOutage(t *testing.T) {
	controlPlane := &failingHostPullControlPlane{}
	agent, err := NewObserveOnlyHostAgent(managedHostAgentBootstrap("https://panel.example.com"), HostPullAgentOptions{
		StateDir:          t.TempDir(),
		ControlPlane:      controlPlane,
		PollInterval:      5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	if err := agent.Run(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Run error = %v", err)
	}
	if calls := controlPlane.registerCalls.Load(); calls < 2 || calls > 6 {
		t.Fatalf("register calls during outage = %d, want bounded exponential retry", calls)
	}
	if calls := controlPlane.heartbeatCalls.Load(); calls < 2 || calls > 6 {
		t.Fatalf("heartbeat calls during outage = %d, want bounded exponential retry", calls)
	}
}

func TestHostAgentRetryCadenceIsDeterministicallyJitteredPerIdentity(t *testing.T) {
	base := 30 * time.Second
	a := hostAgentJitteredInterval(base, "host-agent-a", "heartbeat")
	b := hostAgentJitteredInterval(base, "host-agent-b", "heartbeat")
	if a == b {
		t.Fatalf("different host identities received the same cadence: %s", a)
	}
	for name, interval := range map[string]time.Duration{"a": a, "b": b} {
		if interval < 27*time.Second || interval > 33*time.Second {
			t.Fatalf("%s jitter = %s, outside 10%% bound", name, interval)
		}
	}
	if again := hostAgentJitteredInterval(base, "host-agent-a", "heartbeat"); again != a {
		t.Fatalf("jitter is not stable across restarts: first=%s second=%s", a, again)
	}
}

type failingHostPullControlPlane struct {
	registerCalls  atomic.Int32
	heartbeatCalls atomic.Int32
}

func (f *failingHostPullControlPlane) RegisterHostAgent(context.Context, Config, map[string]any) (HostAgentBinding, error) {
	f.registerCalls.Add(1)
	return HostAgentBinding{}, fmt.Errorf("panel unavailable")
}

func (f *failingHostPullControlPlane) HeartbeatHostAgent(context.Context, Config, string, map[string]any) error {
	f.heartbeatCalls.Add(1)
	return fmt.Errorf("panel unavailable")
}

func (*failingHostPullControlPlane) FetchHostAgentPolicy(context.Context, string, int64) (*HostAgentPolicy, bool, error) {
	return nil, false, fmt.Errorf("panel unavailable")
}

func managedHostAgentBootstrap(panelURL string) Config {
	return Config{
		PanelURL: panelURL, NodeID: "host-agent-a", RuntimeToken: "runtime-token", ServiceName: "Host Agent A",
		configFields: map[string]bool{
			"panel_url": true, "node_id": true, "runtime_token": true, "service_name": true,
		},
	}
}
