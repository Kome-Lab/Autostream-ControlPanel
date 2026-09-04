package store

import (
	"errors"
	"strings"
	"testing"
)

func TestMemorySavePullUpdaterPolicyUsesSyntheticObserverAndAdvancesActiveRevision(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	input := validPullUpdaterPolicyForOwnership()
	created, err := policies.SavePullUpdaterPolicy(t.Context(), updates, "host-agent-a", 0, 0, input)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if owner.OwnershipEpoch != 0 || owner.AgentServiceID != "" || owner.TransportMode != SystemUpdateTransportPullV2 {
		t.Fatalf("observer ownership = %#v", owner)
	}
	owner, err = updates.SwitchSystemUpdateExecutionHost(
		t.Context(), "host-a", 0, SystemUpdateTransportPullV2, "host-agent-a", created.ProjectionRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.PollIntervalSeconds = 20
	updated, err := policies.SavePullUpdaterPolicy(
		t.Context(), updates, "host-agent-a", created.Revision, owner.OwnershipEpoch, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertPullPolicyAndOwnershipRevision(t, policies, updates, "host-agent-a", "host-a", updated.Revision)
}

func TestMemorySavePullUpdaterPolicyRejectsStaleOrForeignOwnership(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	input := validPullUpdaterPolicyForOwnership()
	if _, err := policies.SavePullUpdaterPolicy(t.Context(), updates, "host-agent-a", 0, 1, input); !errors.Is(err, ErrSystemUpdateExecutionHostStale) {
		t.Fatalf("stale observer save error = %v", err)
	}
	if _, err := updates.SwitchSystemUpdateExecutionHost(t.Context(), "host-a", 0, SystemUpdateTransportPullV2, "other-agent", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := policies.SavePullUpdaterPolicy(t.Context(), updates, "host-agent-a", 0, 1, input); !errors.Is(err, ErrSystemUpdateAgentBindingMismatch) {
		t.Fatalf("foreign owner save error = %v", err)
	}
}

func validPullUpdaterPolicyForOwnership() UpdaterPolicy {
	return UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID: "worker-a", ServiceID: "worker-a",
			ServiceType: "worker", DeploymentMode: "systemd", LocalListenPort: 18081,
		}},
	}
}

func assertPullPolicyAndOwnershipRevision(
	t *testing.T,
	policies *MemoryUpdaterPolicyStore,
	updates *MemorySystemUpdateStore,
	serviceID, hostID string,
	wantRevision int64,
) {
	t.Helper()
	policy, err := policies.GetUpdaterPolicy(t.Context(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != wantRevision || ownership.PolicyRevision != wantRevision {
		t.Fatalf("policy/ownership revision = %d/%d, want %d", policy.Revision, ownership.PolicyRevision, wantRevision)
	}
}
