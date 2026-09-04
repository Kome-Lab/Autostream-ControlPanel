package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryActivatePullUpdaterOwnershipFromObserver(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	result, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ownership.TransportMode != SystemUpdateTransportPullV2 ||
		result.Ownership.AgentServiceID != params.ServiceID ||
		result.Ownership.OwnershipEpoch != 1 ||
		result.Ownership.PolicyRevision != result.Policy.ProjectionRevision ||
		result.Service.OwnershipEpoch != result.Ownership.OwnershipEpoch {
		t.Fatalf("activation result = %#v", result)
	}
}

func TestMemoryActivatePullUpdaterOwnershipRejectsUnreadyWithoutMutation(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	agent := registry.services[params.ServiceID]
	agent.ReportedCapabilities["mutation_enabled"] = true
	registry.services[params.ServiceID] = agent

	if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); !errors.Is(err, ErrSystemUpdateAgentNotReady) {
		t.Fatalf("activation error = %v, want ErrSystemUpdateAgentNotReady", err)
	}
	owner, err := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.OwnershipEpoch != 0 || owner.AgentServiceID != "" {
		t.Fatalf("failed activation mutated ownership: %#v", owner)
	}
}

func TestMemoryActivatePullUpdaterOwnershipConcurrentCASAllowsOneWinner(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for range contenders {
		go func() {
			ready.Done()
			<-start
			_, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	succeeded := 0
	for range contenders {
		err := <-results
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrSystemUpdateExecutionHostStale) &&
			!errors.Is(err, ErrSystemUpdateAgentBindingMismatch) {
			t.Fatalf("concurrent activation error = %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful activations = %d, want 1", succeeded)
	}
}

func newMemoryPullActivationFixture(
	t *testing.T,
	_ bool,
) (*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore, ActivatePullUpdaterOwnershipParams) {
	t.Helper()
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	registry := NewMemoryAuthStore()
	updates := NewMemorySystemUpdateStore()
	policy, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", 0, 0, validPullUpdaterPolicyForOwnership())
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	target := RegisteredService{
		ServiceID: "worker-a", ServiceType: "worker", ServiceName: "worker-a",
		Status: "online", EndpointRevision: 3, AppliedConfigRevision: 3,
		AppliedEndpoint: &ServiceEndpoint{Host: "127.0.0.1", Port: 18081, PublicURL: "http://127.0.0.1:18081"},
		CreatedAt:       now, UpdatedAt: now,
	}
	token := ServiceToken{ID: "token-host-agent-a", ServiceType: "update_agent", CreatedAt: now}
	agent := RegisteredService{
		ServiceID: "host-agent-a", ServiceType: "update_agent", ServiceName: "host-agent-a",
		TransportMode: SystemUpdateTransportPullV2, ExecutionHostID: "host-a",
		Status: "online", LastHeartbeatAt: &now, TokenID: token.ID,
		ReportedCapabilities: map[string]any{
			"host_agent": true, "observe_only": true, "update_executor": true,
			"mutation_enabled": false, "recovery_pending": false,
			"transport_mode": SystemUpdateTransportPullV2, "agent_protocol_version": "2",
			"execution_host_id": "host-a", "ownership_epoch": int64(0),
			"policy_revision": policy.ProjectionRevision, "policy_status": "applied",
			"target_availability":                map[string]string{"worker-a": "available"},
			"target_availability_codes":          map[string]string{"worker-a": "executor_verified"},
			"reported_ports":                     map[string]int64{"worker-a": 18081},
			"port_drift":                         map[string]bool{"worker-a": false},
			"reported_service_types":             map[string]string{"worker-a": "worker"},
			"reported_deployment_modes":          map[string]string{"worker-a": "systemd"},
			"reported_executor_policy_revisions": map[string]int64{"worker-a": policy.LocalExecutorPolicyRevision},
			"reported_executor_policy_sha256":    map[string]string{"worker-a": policy.LocalExecutorPolicySHA256},
			"reported_config_revisions":          map[string]int64{"worker-a": target.AppliedConfigRevision},
			"reported_config_sha256":             map[string]string{},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	registry.serviceTokens[token.ID] = token
	registry.services[agent.ServiceID] = agent
	registry.services[target.ServiceID] = target
	return policies, registry, updates, ActivatePullUpdaterOwnershipParams{
		ServiceID: agent.ServiceID, ExecutionHostID: agent.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: 0,
		ExpectedSourcePolicyRevision:        policy.Revision,
		ExpectedProjectionRevision:          policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
	}
}

func addPullActivationTargetReport(capabilities map[string]any, serviceID string, service RegisteredService, policy UpdaterPolicy) {
	capabilities["target_availability"].(map[string]string)[serviceID] = "available"
	capabilities["target_availability_codes"].(map[string]string)[serviceID] = "executor_verified"
	capabilities["reported_ports"].(map[string]int64)[serviceID] = int64(service.AppliedEndpoint.Port)
	capabilities["port_drift"].(map[string]bool)[serviceID] = false
	capabilities["reported_service_types"].(map[string]string)[serviceID] = service.ServiceType
	capabilities["reported_deployment_modes"].(map[string]string)[serviceID] = "systemd"
	capabilities["reported_executor_policy_revisions"].(map[string]int64)[serviceID] = policy.LocalExecutorPolicyRevision
	capabilities["reported_executor_policy_sha256"].(map[string]string)[serviceID] = policy.LocalExecutorPolicySHA256
	capabilities["reported_config_revisions"].(map[string]int64)[serviceID] = service.AppliedConfigRevision
	if service.AppliedConfigSHA256 != "" {
		capabilities["reported_config_sha256"].(map[string]string)[serviceID] = service.AppliedConfigSHA256
	}
}
