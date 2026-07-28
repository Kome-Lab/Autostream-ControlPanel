package store

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestNormalizeActivatePullUpdaterOwnershipRejectsEpochOverflow(t *testing.T) {
	params := ActivatePullUpdaterOwnershipParams{
		ServiceID:                           "host-agent-a",
		ExecutionHostID:                     "host-a",
		ExpectedExecutionHostOwnershipEpoch: math.MaxInt64,
		ExpectedSourcePolicyRevision:        1,
		ExpectedProjectionRevision:          1,
		ExpectedLocalExecutorPolicyRevision: 1,
		ExpectedLocalExecutorPolicySHA256:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if _, err := normalizeActivatePullUpdaterOwnershipParams(params); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("overflowing activation epoch error = %v", err)
	}
}

func TestMemoryActivatePullUpdaterOwnershipFromSyntheticAndSSHOwner(t *testing.T) {
	for _, test := range []struct {
		name     string
		sshOwner bool
	}{
		{name: "synthetic"},
		{name: "ssh_owner", sshOwner: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates, params := newMemoryPullActivationFixture(t, test.sshOwner)

			result, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
			if err != nil {
				t.Fatal(err)
			}
			if result.Service.OwnershipEpoch != params.ExpectedExecutionHostOwnershipEpoch+1 ||
				result.Ownership.OwnershipEpoch != result.Service.OwnershipEpoch ||
				result.Ownership.TransportMode != SystemUpdateTransportPullV2 ||
				result.Ownership.AgentServiceID != params.ServiceID ||
				result.Ownership.PolicyRevision != params.ExpectedProjectionRevision {
				t.Fatalf("activation result = %#v", result)
			}
			policy, err := policies.GetUpdaterPolicy(t.Context(), params.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			if policy.Revision != params.ExpectedSourcePolicyRevision ||
				policy.ProjectionRevision != params.ExpectedProjectionRevision ||
				policy.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision ||
				policy.LocalExecutorPolicySHA256 != params.ExpectedLocalExecutorPolicySHA256 {
				t.Fatalf("activation mutated policy = %#v", policy)
			}
			reservations, err := updates.ListServicePortReservations(t.Context(), params.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			if len(reservations) != 1 ||
				reservations[0].NetworkNamespace != "host" ||
				reservations[0].Protocol != "tcp" ||
				reservations[0].Port != 18081 ||
				reservations[0].ServiceID != "worker-a" ||
				reservations[0].ServiceRole != "api" {
				t.Fatalf("activation baseline reservations = %#v", reservations)
			}
			targetService, err := registry.GetService(t.Context(), "worker-a")
			if err != nil {
				t.Fatal(err)
			}
			if !validSystemUpdateDigest(targetService.AppliedConfigSHA256) {
				t.Fatalf("activation did not pin applied config digest: %#v", targetService)
			}
		})
	}
}

func TestMemoryActivatePullUpdaterOwnershipPortBaselineFences(t *testing.T) {
	t.Run("existing other service reservation", func(t *testing.T) {
		policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
		now := time.Now().UTC()
		conflict := ServicePortReservation{
			ExecutionHostID: params.ExecutionHostID, NetworkNamespace: "host", Protocol: "tcp", Port: 18081,
			ServiceID: "another-worker", ServiceRole: "api", CreatedAt: now, UpdatedAt: now,
		}
		updates.portReservations[servicePortKey(conflict)] = conflict
		beforeOwner, _ := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
		if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); !errors.Is(err, ErrServicePortReserved) {
			t.Fatalf("activation error = %v, want ErrServicePortReserved", err)
		}
		afterOwner, _ := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
		service, _ := registry.GetService(t.Context(), params.ServiceID)
		if afterOwner != beforeOwner || service.OwnershipEpoch != 0 {
			t.Fatalf("port conflict partially activated: owner=%#v service=%#v", afterOwner, service)
		}
	})

	t.Run("same service role is idempotent", func(t *testing.T) {
		policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
		createdAt := time.Now().UTC().Add(-time.Hour)
		existing := ServicePortReservation{
			ExecutionHostID: params.ExecutionHostID, NetworkNamespace: "host", Protocol: "tcp", Port: 18081,
			ServiceID: "worker-a", ServiceRole: "api", CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		updates.portReservations[servicePortKey(existing)] = existing
		if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); err != nil {
			t.Fatal(err)
		}
		reservations, err := updates.ListServicePortReservations(t.Context(), params.ExecutionHostID)
		if err != nil {
			t.Fatal(err)
		}
		if len(reservations) != 1 || !reservations[0].CreatedAt.Equal(createdAt) {
			t.Fatalf("idempotent baseline reservation = %#v", reservations)
		}
	})

	t.Run("same port on another host is independent", func(t *testing.T) {
		policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
		now := time.Now().UTC()
		otherHost := ServicePortReservation{
			ExecutionHostID: "host-b", NetworkNamespace: "host", Protocol: "tcp", Port: 18081,
			ServiceID: "worker-b", ServiceRole: "api", CreatedAt: now, UpdatedAt: now,
		}
		updates.portReservations[servicePortKey(otherHost)] = otherHost
		if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("two targets cannot share the same applied port", func(t *testing.T) {
		policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
		policy := policies.policies[params.ServiceID]
		policy.Targets = append(policy.Targets, UpdaterPolicyTarget{
			TargetID: "worker-b", ServiceID: "worker-b", HostID: params.ExecutionHostID,
			ServiceType: "worker", DeploymentMode: "systemd",
		})
		policies.policies[params.ServiceID] = policy
		workerA := registry.services["worker-a"]
		workerB := workerA
		workerB.ServiceID = "worker-b"
		workerB.ServiceName = "worker-b"
		registry.services[workerB.ServiceID] = workerB
		agent := registry.services[params.ServiceID]
		addPullActivationTargetReport(agent.ReportedCapabilities, "worker-b", workerB, policy)
		registry.services[params.ServiceID] = agent

		if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); !errors.Is(err, ErrServicePortReserved) {
			t.Fatalf("duplicate target port error = %v, want ErrServicePortReserved", err)
		}
		owner, _ := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
		service, _ := registry.GetService(t.Context(), params.ServiceID)
		if owner.TransportMode != SystemUpdateTransportSSHV1 || service.OwnershipEpoch != 0 {
			t.Fatalf("duplicate target port partially activated: owner=%#v service=%#v", owner, service)
		}
	})
}

func TestMemoryActivatePullUpdaterOwnershipRejectsStaleAndUnreadyWithoutPartialMutation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore, *ActivatePullUpdaterOwnershipParams)
		wantErr error
	}{
		{
			name: "stale owner epoch",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				params.ExpectedExecutionHostOwnershipEpoch++
			},
			wantErr: ErrSystemUpdateExecutionHostStale,
		},
		{
			name: "stale policy revision",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				params.ExpectedSourcePolicyRevision++
			},
			wantErr: ErrConflict,
		},
		{
			name: "digest mismatch",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				params.ExpectedLocalExecutorPolicySHA256 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
			wantErr: ErrConflict,
		},
		{
			name: "stale heartbeat",
			mutate: func(_ *MemoryUpdaterPolicyStore, registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				service := registry.services[params.ServiceID]
				stale := time.Now().UTC().Add(-pullUpdaterActivationHeartbeatMaxAge - time.Second)
				service.LastHeartbeatAt = &stale
				registry.services[params.ServiceID] = service
			},
			wantErr: ErrSystemUpdateAgentNotReady,
		},
		{
			name: "revoked token",
			mutate: func(_ *MemoryUpdaterPolicyStore, registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				service := registry.services[params.ServiceID]
				token := registry.serviceTokens[service.TokenID]
				now := time.Now().UTC()
				token.RevokedAt = &now
				registry.serviceTokens[token.ID] = token
			},
			wantErr: ErrSystemUpdateAgentInactive,
		},
		{
			name: "target readiness mismatch",
			mutate: func(_ *MemoryUpdaterPolicyStore, registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *ActivatePullUpdaterOwnershipParams) {
				service := registry.services[params.ServiceID]
				service.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]string{
					"worker-a": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				}
				registry.services[params.ServiceID] = service
			},
			wantErr: ErrSystemUpdateAgentNotReady,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
			beforeOwner, err := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(policies, registry, updates, &params)
			if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); !errors.Is(err, test.wantErr) {
				t.Fatalf("activation error = %v, want %v", err, test.wantErr)
			}
			afterOwner, err := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			if afterOwner != beforeOwner {
				t.Fatalf("rejected activation mutated owner: before=%#v after=%#v", beforeOwner, afterOwner)
			}
			service, err := registry.GetService(t.Context(), params.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			if service.OwnershipEpoch != 0 {
				t.Fatalf("rejected activation mutated service epoch = %d", service.OwnershipEpoch)
			}
		})
	}
}

func TestMemoryActivatePullUpdaterOwnershipRejectsActiveJob(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, true)
	owner, err := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    owner.AgentServiceID,
		ExecutionHostID:   owner.ExecutionHostID,
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "activation-active-job",
		RequestedByUserID: "admin",
	}); err != nil || !created {
		t.Fatalf("create active job: created=%v err=%v", created, err)
	}
	if _, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params); !errors.Is(err, ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("activation error = %v, want ErrSystemUpdateExecutionHostBusy", err)
	}
	service, err := registry.GetService(t.Context(), params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.OwnershipEpoch != 0 {
		t.Fatalf("busy activation mutated service epoch = %d", service.OwnershipEpoch)
	}
}

func TestMemoryActivatePullUpdaterOwnershipConcurrentCASAllowsOneWinner(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	start := make(chan struct{})
	errorsByCaller := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
			errorsByCaller <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByCaller)

	successes := 0
	conflicts := 0
	for err := range errorsByCaller {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSystemUpdateAgentBindingMismatch),
			errors.Is(err, ErrSystemUpdateExecutionHostStale):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent activation error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent activation results: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestMemoryActivateAndObserverPolicyUpdatePreserveRevisionFence(t *testing.T) {
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	input := validPullUpdaterPolicyForOwnership()
	input.PollIntervalSeconds = 20
	start := make(chan struct{})
	var activationErr, saveErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, activationErr = policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
	}()
	go func() {
		defer wait.Done()
		<-start
		_, saveErr = policies.SavePullUpdaterPolicy(
			t.Context(),
			updates,
			params.ServiceID,
			params.ExpectedSourcePolicyRevision,
			params.ExpectedExecutionHostOwnershipEpoch,
			input,
		)
	}()
	close(start)
	wait.Wait()
	if activationErr == nil && saveErr == nil {
		t.Fatalf("activation and stale observer save both succeeded")
	}

	policy, err := policies.GetUpdaterPolicy(t.Context(), params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.TransportMode == SystemUpdateTransportPullV2 &&
		ownership.PolicyRevision != policy.ProjectionRevision {
		t.Fatalf("active owner/policy revision diverged: owner=%#v policy=%#v", ownership, policy)
	}
}

func newMemoryPullActivationFixture(
	t *testing.T,
	sshOwner bool,
) (*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore, ActivatePullUpdaterOwnershipParams) {
	t.Helper()
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	registry := NewMemoryAuthStore()
	updates := NewMemorySystemUpdateStore()

	expectedOwnerEpoch := int64(0)
	if sshOwner {
		legacyNow := time.Now().UTC()
		legacyToken := ServiceToken{
			ID:          "token-central-updater",
			ServiceType: "update_agent",
			Scopes: []string{
				"updates.claim",
				"updates.report",
				"updates.authorize",
			},
			CreatedAt: legacyNow,
		}
		registry.serviceTokens[legacyToken.ID] = legacyToken
		registry.services["central-updater"] = RegisteredService{
			ServiceID:     "central-updater",
			ServiceType:   "update_agent",
			ServiceName:   "central-updater",
			TransportMode: SystemUpdateTransportSSHV1,
			Status:        "online",
			TokenID:       legacyToken.ID,
			CreatedAt:     legacyNow,
			UpdatedAt:     legacyNow,
		}
		legacyPolicy := validUpdaterPolicy()
		legacyPolicy.UpdaterID = ""
		if _, err := policies.SaveUpdaterPolicy(
			ctx,
			"central-updater",
			0,
			legacyPolicy,
		); err != nil {
			t.Fatal(err)
		}
		owner, err := updates.SwitchSystemUpdateExecutionHost(
			ctx,
			"host-a",
			0,
			SystemUpdateTransportSSHV1,
			"central-updater",
			7,
		)
		if err != nil {
			t.Fatal(err)
		}
		expectedOwnerEpoch = owner.OwnershipEpoch
	}
	policy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		"host-agent-a",
		0,
		expectedOwnerEpoch,
		validPullUpdaterPolicyForOwnership(),
	)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	target := RegisteredService{
		ServiceID:             "worker-a",
		ServiceType:           "worker",
		ServiceName:           "worker-a",
		Status:                "online",
		EndpointRevision:      3,
		AppliedConfigRevision: 3,
		AppliedEndpoint: &ServiceEndpoint{
			Host:       "127.0.0.1",
			Port:       18081,
			SSLEnabled: false,
			PublicURL:  "http://127.0.0.1:18081",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	token := ServiceToken{
		ID:          "token-host-agent-a",
		ServiceType: "update_agent",
		CreatedAt:   now,
	}
	agent := RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		ServiceName:     "host-agent-a",
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  0,
		Status:          "online",
		LastHeartbeatAt: &now,
		TokenID:         token.ID,
		ReportedCapabilities: map[string]any{
			"host_agent":                         true,
			"observe_only":                       true,
			"update_executor":                    true,
			"mutation_enabled":                   false,
			"recovery_pending":                   false,
			"transport_mode":                     SystemUpdateTransportPullV2,
			"agent_protocol_version":             "2",
			"execution_host_id":                  "host-a",
			"ownership_epoch":                    int64(0),
			"policy_revision":                    policy.ProjectionRevision,
			"policy_status":                      "applied",
			"target_availability":                map[string]string{"worker-a": "available"},
			"target_availability_codes":          map[string]string{"worker-a": "executor_verified"},
			"reported_ports":                     map[string]int64{"worker-a": 18081},
			"port_drift":                         map[string]bool{"worker-a": false},
			"reported_service_types":             map[string]string{"worker-a": "worker"},
			"reported_deployment_modes":          map[string]string{"worker-a": "systemd"},
			"reported_executor_policy_revisions": map[string]int64{"worker-a": policy.LocalExecutorPolicyRevision},
			"reported_executor_policy_sha256":    map[string]string{"worker-a": policy.LocalExecutorPolicySHA256},
			"reported_config_revisions":          map[string]int64{"worker-a": target.AppliedConfigRevision},
			"reported_config_sha256": map[string]string{
				"worker-a": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	registry.serviceTokens[token.ID] = token
	registry.services[agent.ServiceID] = agent
	registry.services[target.ServiceID] = target

	return policies, registry, updates, ActivatePullUpdaterOwnershipParams{
		ServiceID:                           agent.ServiceID,
		ExecutionHostID:                     agent.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: expectedOwnerEpoch,
		ExpectedSourcePolicyRevision:        policy.Revision,
		ExpectedProjectionRevision:          policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
	}
}

func addPullActivationTargetReport(
	capabilities map[string]any,
	serviceID string,
	service RegisteredService,
	policy UpdaterPolicy,
) {
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
		configDigests, ok := capabilities["reported_config_sha256"].(map[string]string)
		if !ok {
			configDigests = map[string]string{}
			capabilities["reported_config_sha256"] = configDigests
		}
		configDigests[serviceID] = service.AppliedConfigSHA256
	}
}
