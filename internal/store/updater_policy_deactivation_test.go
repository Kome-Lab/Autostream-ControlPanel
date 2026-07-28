package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMemoryActivatePullUpdaterOwnershipResolvesOnlyUniqueLegacyOwnerForNewRow(t *testing.T) {
	for _, test := range []struct {
		name               string
		legacyIDs          []string
		legacyTokenScopes  []string
		mutateLegacyPolicy func(*UpdaterPolicy)
		wantLegacy         string
	}{
		{
			name:              "unique",
			legacyIDs:         []string{"central-updater"},
			legacyTokenScopes: requiredUpdateAgentTestScopes(),
			wantLegacy:        "central-updater",
		},
		{
			name:              "ambiguous",
			legacyIDs:         []string{"central-updater-a", "central-updater-b"},
			legacyTokenScopes: requiredUpdateAgentTestScopes(),
		},
		{
			name:              "missing required token scope",
			legacyIDs:         []string{"central-updater"},
			legacyTokenScopes: []string{"updates.claim", "updates.report"},
		},
		{
			name:              "incomplete pull target coverage",
			legacyIDs:         []string{"central-updater"},
			legacyTokenScopes: requiredUpdateAgentTestScopes(),
			mutateLegacyPolicy: func(policy *UpdaterPolicy) {
				policy.Targets[0].TargetID = "worker-other"
				policy.Targets[0].ServiceID = "worker-other"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
			for _, legacyID := range test.legacyIDs {
				now := time.Now().UTC()
				token := ServiceToken{
					ID:          "token-" + legacyID,
					ServiceType: "update_agent",
					Scopes:      append([]string(nil), test.legacyTokenScopes...),
					CreatedAt:   now,
				}
				registry.serviceTokens[token.ID] = token
				registry.services[legacyID] = RegisteredService{
					ServiceID: legacyID, ServiceType: "update_agent",
					ServiceName: legacyID, TransportMode: SystemUpdateTransportSSHV1,
					Status: "online", TokenID: token.ID, CreatedAt: now, UpdatedAt: now,
				}
				legacyPolicy := validUpdaterPolicy()
				legacyPolicy.UpdaterID = ""
				if test.mutateLegacyPolicy != nil {
					test.mutateLegacyPolicy(&legacyPolicy)
				}
				if _, err := policies.SaveUpdaterPolicy(
					t.Context(),
					legacyID,
					0,
					legacyPolicy,
				); err != nil {
					t.Fatal(err)
				}
			}
			activated, err := policies.ActivatePullUpdaterOwnership(
				t.Context(),
				registry,
				updates,
				params,
			)
			if err != nil {
				t.Fatal(err)
			}
			if activated.Ownership.LegacyAgentServiceID != test.wantLegacy {
				t.Fatalf("resolved legacy owner = %#v, want %q", activated.Ownership, test.wantLegacy)
			}
		})
	}
}

func TestMemoryDeactivatePullUpdaterOwnershipReturnsAgentToObserverAndCanReactivate(t *testing.T) {
	policies, registry, updates, activateParams := newMemoryPullActivationFixture(t, true)
	legacyPolicy, err := policies.GetUpdaterPolicy(t.Context(), "central-updater")
	if err != nil {
		t.Fatal(err)
	}
	legacyPolicy.PollIntervalSeconds++
	legacyPolicy, err = policies.SaveUpdaterPolicy(
		t.Context(),
		"central-updater",
		legacyPolicy.Revision,
		legacyPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	activated, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		registry,
		updates,
		activateParams,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyBefore, err := policies.GetUpdaterPolicy(t.Context(), activateParams.ServiceID)
	if err != nil {
		t.Fatal(err)
	}

	deactivated, err := policies.DeactivatePullUpdaterOwnership(
		t.Context(),
		registry,
		updates,
		deactivateParamsFromActivation(activateParams, activated),
	)
	if err != nil {
		t.Fatal(err)
	}
	if deactivated.Ownership.TransportMode != SystemUpdateTransportSSHV1 ||
		deactivated.Ownership.AgentServiceID != "central-updater" ||
		deactivated.Ownership.LegacyAgentServiceID != "central-updater" ||
		deactivated.Ownership.OwnershipEpoch != activated.Ownership.OwnershipEpoch+1 ||
		deactivated.Ownership.PolicyRevision != legacyPolicy.ProjectionRevision ||
		deactivated.Service.TransportMode != SystemUpdateTransportPullV2 ||
		deactivated.Service.ExecutionHostID != activateParams.ExecutionHostID ||
		deactivated.Service.OwnershipEpoch != 0 {
		t.Fatalf("deactivation result = %#v", deactivated)
	}
	policyAfter, err := policies.GetUpdaterPolicy(t.Context(), activateParams.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policyAfter, policyBefore) {
		t.Fatalf("deactivation mutated policy:\nbefore=%#v\nafter=%#v", policyBefore, policyAfter)
	}

	observer := registry.services[activateParams.ServiceID]
	now := time.Now().UTC()
	observer.LastHeartbeatAt = &now
	observer.Status = "online"
	observer.ReportedCapabilities["observe_only"] = true
	observer.ReportedCapabilities["mutation_enabled"] = false
	observer.ReportedCapabilities["recovery_pending"] = false
	observer.ReportedCapabilities["ownership_epoch"] = int64(0)
	registry.services[activateParams.ServiceID] = observer
	activateParams.ExpectedExecutionHostOwnershipEpoch = deactivated.Ownership.OwnershipEpoch
	reactivated, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		registry,
		updates,
		activateParams,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Ownership.TransportMode != SystemUpdateTransportPullV2 ||
		reactivated.Ownership.AgentServiceID != activateParams.ServiceID ||
		reactivated.Ownership.OwnershipEpoch != deactivated.Ownership.OwnershipEpoch+1 ||
		reactivated.Service.OwnershipEpoch != reactivated.Ownership.OwnershipEpoch {
		t.Fatalf("reactivation result = %#v", reactivated)
	}
}

func TestMemoryDeactivatePullUpdaterOwnershipFailsClosedWithoutPartialMutation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*MemoryAuthStore, *MemorySystemUpdateStore, *DeactivatePullUpdaterOwnershipParams)
		wantErr error
	}{
		{
			name: "stale ownership epoch",
			mutate: func(_ *MemoryAuthStore, _ *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				params.ExpectedExecutionHostOwnershipEpoch++
			},
			wantErr: ErrSystemUpdateExecutionHostStale,
		},
		{
			name: "wrong current agent",
			mutate: func(_ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				ownership := updates.executionHosts[params.ExecutionHostID]
				ownership.AgentServiceID = "host-agent-other"
				updates.executionHosts[params.ExecutionHostID] = ownership
			},
			wantErr: ErrSystemUpdateAgentBindingMismatch,
		},
		{
			name: "inactive agent token",
			mutate: func(registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				service := registry.services[params.ServiceID]
				token := registry.serviceTokens[service.TokenID]
				now := time.Now().UTC()
				token.RevokedAt = &now
				registry.serviceTokens[token.ID] = token
			},
			wantErr: ErrSystemUpdateAgentInactive,
		},
		{
			name: "inactive legacy agent token",
			mutate: func(registry *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				ownership := updates.executionHosts[params.ExecutionHostID]
				legacyService := registry.services[ownership.LegacyAgentServiceID]
				token := registry.serviceTokens[legacyService.TokenID]
				now := time.Now().UTC()
				token.RevokedAt = &now
				registry.serviceTokens[token.ID] = token
			},
			wantErr: ErrSystemUpdateAgentInactive,
		},
		{
			name: "legacy agent token misses required scope",
			mutate: func(registry *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				ownership := updates.executionHosts[params.ExecutionHostID]
				legacyService := registry.services[ownership.LegacyAgentServiceID]
				token := registry.serviceTokens[legacyService.TokenID]
				token.Scopes = []string{"updates.claim", "updates.report"}
				registry.serviceTokens[token.ID] = token
			},
			wantErr: ErrSystemUpdateAgentInactive,
		},
		{
			name: "missing legacy agent owner",
			mutate: func(_ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				ownership := updates.executionHosts[params.ExecutionHostID]
				ownership.LegacyAgentServiceID = ""
				updates.executionHosts[params.ExecutionHostID] = ownership
			},
			wantErr: ErrSystemUpdateAgentBindingMismatch,
		},
		{
			name: "recovery pending",
			mutate: func(registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				service := registry.services[params.ServiceID]
				service.ReportedCapabilities["recovery_pending"] = true
				registry.services[params.ServiceID] = service
			},
			wantErr: ErrSystemUpdateAgentNotReady,
		},
		{
			name: "active host self update",
			mutate: func(_ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				updates.hostSelfUpdates["self-update-a"] = SystemUpdateHostSelfUpdate{
					ID: "self-update-a", ExecutionHostID: params.ExecutionHostID,
					AgentServiceID: params.ServiceID, Status: SystemUpdateHostSelfUpdateStaging,
				}
			},
			wantErr: ErrSystemUpdateHostSelfUpdateBusy,
		},
		{
			name: "active runtime token rotation",
			mutate: func(_ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				updates.runtimeTokenRotations["rotation-a"] = SystemUpdateRuntimeTokenRotation{
					ID: "rotation-a", ExecutionHostID: params.ExecutionHostID,
					ServiceID: params.ServiceID, Status: SystemUpdateRuntimeTokenRotationStaged,
				}
			},
			wantErr: ErrSystemUpdateRuntimeTokenRotationBusy,
		},
		{
			name: "unsettled mutation grant",
			mutate: func(_ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *DeactivatePullUpdaterOwnershipParams) {
				updates.jobs["completed-job-a"] = SystemUpdateJob{
					ID: "completed-job-a", ExecutionHostID: params.ExecutionHostID,
					Status: SystemUpdateStatusSucceeded,
				}
				updates.mutationGrants["grant-a"] = SystemUpdateMutationGrant{
					ID: "grant-a", JobID: "completed-job-a", AgentServiceID: params.ServiceID,
					ExpiresAt: time.Now().UTC().Add(time.Minute),
				}
			},
			wantErr: ErrSystemUpdateExecutionHostBusy,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates, activateParams := newMemoryPullActivationFixture(t, true)
			activated, err := policies.ActivatePullUpdaterOwnership(
				t.Context(),
				registry,
				updates,
				activateParams,
			)
			if err != nil {
				t.Fatal(err)
			}
			params := deactivateParamsFromActivation(activateParams, activated)
			test.mutate(registry, updates, &params)
			ownerBefore := updates.executionHosts[activateParams.ExecutionHostID]
			serviceBefore := registry.services[activateParams.ServiceID]

			_, err = policies.DeactivatePullUpdaterOwnership(
				t.Context(),
				registry,
				updates,
				params,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("deactivation error = %v, want %v", err, test.wantErr)
			}
			if ownerAfter := updates.executionHosts[activateParams.ExecutionHostID]; ownerAfter != ownerBefore {
				t.Fatalf("rejected deactivation mutated owner:\nbefore=%#v\nafter=%#v", ownerBefore, ownerAfter)
			}
			if serviceAfter := registry.services[activateParams.ServiceID]; serviceAfter.OwnershipEpoch != serviceBefore.OwnershipEpoch {
				t.Fatalf("rejected deactivation mutated service epoch: before=%d after=%d", serviceBefore.OwnershipEpoch, serviceAfter.OwnershipEpoch)
			}
		})
	}
}

func TestMemoryDeactivatePullUpdaterOwnershipRejectsIncompleteLegacyTargetCoverage(
	t *testing.T,
) {
	policies, registry, updates, activateParams := newMemoryPullActivationFixture(t, true)
	activated, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		registry,
		updates,
		activateParams,
	)
	if err != nil {
		t.Fatal(err)
	}
	legacyPolicy := policies.policies["central-updater"]
	legacyPolicy.Targets = []UpdaterPolicyTarget{{
		TargetID:       "worker-other",
		ServiceID:      "worker-other",
		HostID:         activateParams.ExecutionHostID,
		ServiceType:    "worker",
		DeploymentMode: "systemd",
	}}
	policies.policies["central-updater"] = legacyPolicy
	before := updates.executionHosts[activateParams.ExecutionHostID]

	_, err = policies.DeactivatePullUpdaterOwnership(
		t.Context(),
		registry,
		updates,
		deactivateParamsFromActivation(activateParams, activated),
	)
	if !errors.Is(err, ErrSystemUpdateAgentBindingMismatch) {
		t.Fatalf("incomplete legacy target coverage error = %v", err)
	}
	if after := updates.executionHosts[activateParams.ExecutionHostID]; after != before {
		t.Fatalf("target coverage rejection mutated owner: before=%#v after=%#v", before, after)
	}
}

func deactivateParamsFromActivation(
	activateParams ActivatePullUpdaterOwnershipParams,
	activated ActivatePullUpdaterOwnershipResult,
) DeactivatePullUpdaterOwnershipParams {
	return DeactivatePullUpdaterOwnershipParams{
		ServiceID:                           activateParams.ServiceID,
		ExecutionHostID:                     activateParams.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activateParams.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          activateParams.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activateParams.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activateParams.ExpectedLocalExecutorPolicySHA256,
	}
}

func requiredUpdateAgentTestScopes() []string {
	return []string{"updates.claim", "updates.report", "updates.authorize"}
}
