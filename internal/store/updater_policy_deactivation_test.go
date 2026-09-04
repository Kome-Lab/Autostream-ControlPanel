package store

import (
	"errors"
	"testing"
)

func TestMemoryDeactivatePullUpdaterOwnershipReturnsObserveOnlyBindingAndCanReactivate(t *testing.T) {
	policies, registry, updates, activateParams := newMemoryPullActivationFixture(t, false)
	activated, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, activateParams)
	if err != nil {
		t.Fatal(err)
	}
	deactivated, err := policies.DeactivatePullUpdaterOwnership(
		t.Context(), registry, updates, deactivateParamsFromActivation(activateParams, activated),
	)
	if err != nil {
		t.Fatal(err)
	}
	if deactivated.Ownership.TransportMode != SystemUpdateTransportPullV2 ||
		deactivated.Ownership.AgentServiceID != activateParams.ServiceID ||
		deactivated.Ownership.OwnershipEpoch != activated.Ownership.OwnershipEpoch+1 ||
		deactivated.Ownership.PolicyRevision != activated.Policy.ProjectionRevision ||
		deactivated.Service.OwnershipEpoch != 0 {
		t.Fatalf("deactivation result = %#v", deactivated)
	}

	agent := registry.services[activateParams.ServiceID]
	agent.ReportedCapabilities["ownership_epoch"] = int64(0)
	registry.services[activateParams.ServiceID] = agent
	activateParams.ExpectedExecutionHostOwnershipEpoch = deactivated.Ownership.OwnershipEpoch
	reactivated, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, activateParams)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Ownership.OwnershipEpoch != deactivated.Ownership.OwnershipEpoch+1 {
		t.Fatalf("reactivation ownership = %#v", reactivated.Ownership)
	}
}

func TestMemoryDeactivatePullUpdaterOwnershipRejectsActiveJobWithoutPartialMutation(t *testing.T) {
	policies, registry, updates, activateParams := newMemoryPullActivationFixture(t, false)
	activated, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, activateParams)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-a", TargetServiceType: "worker",
		AgentServiceID: activateParams.ServiceID, ExecutionHostID: activateParams.ExecutionHostID,
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "deactivate-busy",
		RequestedByUserID: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	before := updates.executionHosts[activateParams.ExecutionHostID]
	_, err = policies.DeactivatePullUpdaterOwnership(
		t.Context(), registry, updates, deactivateParamsFromActivation(activateParams, activated),
	)
	if !errors.Is(err, ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("deactivation error = %v, want ErrSystemUpdateExecutionHostBusy", err)
	}
	if after := updates.executionHosts[activateParams.ExecutionHostID]; after != before {
		t.Fatalf("failed deactivation mutated ownership: before=%#v after=%#v", before, after)
	}
}

func deactivateParamsFromActivation(
	activateParams ActivatePullUpdaterOwnershipParams,
	activated ActivatePullUpdaterOwnershipResult,
) DeactivatePullUpdaterOwnershipParams {
	return DeactivatePullUpdaterOwnershipParams{
		ServiceID:                           activated.Service.ServiceID,
		ExecutionHostID:                     activated.Ownership.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activateParams.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          activateParams.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activateParams.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activateParams.ExpectedLocalExecutorPolicySHA256,
	}
}
