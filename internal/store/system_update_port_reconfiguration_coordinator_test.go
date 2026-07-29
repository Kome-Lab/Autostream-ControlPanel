package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryCreateSystemdPortReconfigurationIsAtomicAndIdempotent(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	params := CreateSystemdPortReconfigurationJobParams{
		TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
		IdempotencyKey: "port-worker-a-3", RequestedByUserID: "admin-a",
		RequestedByUsername: "Admin A",
	}
	job, created, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, params,
	)
	if err != nil || !created {
		t.Fatalf("create = %#v created=%v err=%v", job, created, err)
	}
	if job.Operation != SystemUpdateOperationPortReconfigure ||
		job.PortReconfigure == nil ||
		job.PortReconfigure.OldPort != 18081 ||
		job.PortReconfigure.NewPort != 18084 ||
		job.PortReconfigure.ExpectedEndpointRevision != 3 ||
		job.PortReconfigure.TargetEndpointRevision != 4 ||
		job.PortReconfigure.ExpectedConfigRevision != 3 ||
		job.PortReconfigure.TargetConfigRevision != 4 ||
		job.PortReconfigure.ExpectedSourcePolicyRevision < 1 ||
		job.PortReconfigure.ExpectedUpdaterPolicyRevision < 1 ||
		job.PortReconfigure.ExpectedExecutorPolicyRevision < 1 {
		t.Fatalf("derived port job = %#v", job)
	}
	intentSHA256, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil || intentSHA256 != job.PortReconfigure.PortPlanSHA256 {
		t.Fatalf("intent digest = %q err=%v, job=%q", intentSHA256, err, job.PortReconfigure.PortPlanSHA256)
	}
	replayed, created, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, params,
	)
	if err != nil || created || replayed.ID != job.ID {
		t.Fatalf("replay = %#v created=%v err=%v", replayed, created, err)
	}
	conflict := params
	conflict.NewPort++
	if _, _, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, conflict,
	); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("idempotency conflict = %v", err)
	}

	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.AppliedEndpoint == nil || service.AppliedEndpoint.Port != 18081 ||
		service.DesiredEndpoint == nil || service.DesiredEndpoint.Port != 18084 ||
		service.EndpointRevision != 4 || service.EndpointStatus != "pending" ||
		service.AppliedConfigRevision != 3 {
		t.Fatalf("pending service = %#v", service)
	}
	reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 2 ||
		reservations[0].Port != 18081 || reservations[0].ServiceRole != systemUpdatePortCurrentRole ||
		reservations[1].Port != 18084 || reservations[1].ServiceRole != systemUpdatePortPendingRole {
		t.Fatalf("reservations = %#v", reservations)
	}
}

func TestSystemdPortPlanKeepsAdvertisedHostOutOfLocalSidecarDigest(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.Host = "worker-a.example.com"
	target.PublicURL = "https://worker-a.example.com:18081"
	target.AppliedEndpoint = &ServiceEndpoint{
		Host: "worker-a.example.com", Port: 18081, SSLEnabled: true,
		PublicURL: "https://worker-a.example.com:18081",
	}
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	registry.services[target.ServiceID] = target
	registry.mu.Unlock()

	job, created, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
			IdempotencyKey: "external-advertised-host", RequestedByUserID: "admin-a",
		},
	)
	if err != nil || !created || job.PortReconfigure == nil {
		t.Fatalf("create = %#v created=%v err=%v", job, created, err)
	}
	want, err := systemUpdatePortSidecarSHA256("worker", 18084, 4)
	if err != nil {
		t.Fatal(err)
	}
	if job.PortReconfigure.TargetConfigSHA256 != want {
		t.Fatalf(
			"target config digest used advertised host: got=%q want=%q",
			job.PortReconfigure.TargetConfigSHA256,
			want,
		)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.DesiredEndpoint == nil ||
		service.DesiredEndpoint.Host != "worker-a.example.com" ||
		service.DesiredEndpoint.Port != 18084 {
		t.Fatalf("advertised endpoint was not preserved independently: %#v", service.DesiredEndpoint)
	}
}

func TestMemoryCancelSystemdPortReconfigurationRollsBackPendingStateMonotonically(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	job, _, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
			IdempotencyKey: "port-cancel", RequestedByUserID: "admin-a",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := updates.CancelSystemUpdateJob(t.Context(), job.ID, "admin-a")
	if err != nil || canceled.Status != SystemUpdateStatusCancelled {
		t.Fatalf("cancel = %#v err=%v", canceled, err)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if !sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) ||
		service.AppliedEndpoint.Port != 18081 ||
		service.EndpointRevision != 5 ||
		service.EndpointStatus != "applied" {
		t.Fatalf("canceled service = %#v", service)
	}
	reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != 18081 ||
		reservations[0].ServiceRole != systemUpdatePortCurrentRole {
		t.Fatalf("reservations after cancel = %#v err=%v", reservations, err)
	}
	next, created, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID: "worker-a", NewPort: 18085,
			ExpectedEndpointRevision: service.EndpointRevision,
			IdempotencyKey:           "port-after-queued-cancel",
			RequestedByUserID:        "admin-a",
		},
	)
	if err != nil || !created || next.PortReconfigure == nil ||
		next.PortReconfigure.ExpectedEndpointRevision != 5 ||
		next.PortReconfigure.ExpectedConfigRevision != 3 ||
		next.PortReconfigure.ExpectedConfigSHA256 != service.AppliedConfigSHA256 {
		t.Fatalf("next job after queued cancel = %#v created=%v err=%v", next, created, err)
	}
}

func TestMemorySystemdPortReconfigurationRejectsCollisionWithoutPartialState(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	if _, created, err := updates.ReserveServicePort(t.Context(), ServicePortReservation{
		ExecutionHostID: "host-a", NetworkNamespace: "host", Protocol: "tcp",
		Port: 18084, ServiceID: "other-service", ServiceRole: "api",
	}); err != nil || !created {
		t.Fatalf("reserve collision created=%v err=%v", created, err)
	}
	if _, _, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
			IdempotencyKey: "port-collision", RequestedByUserID: "admin-a",
		},
	); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("collision create = %v", err)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.EndpointRevision != 3 || service.EndpointStatus != "applied" ||
		!sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) {
		t.Fatalf("collision partially mutated service = %#v", service)
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 100)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("collision partially created jobs = %#v err=%v", jobs, err)
	}
}

func TestMemorySystemdPortReconfigurationRejectsSyntheticControlPanelPortWithoutPartialState(t *testing.T) {
	policies, registry, updates, activation := newMemoryPullActivationFixture(t, false)
	policy := policies.policies[activation.ServiceID]
	policy.Targets = append(policy.Targets, UpdaterPolicyTarget{
		TargetID:       "control-panel",
		ServiceID:      "control-panel",
		HostID:         activation.ExecutionHostID,
		ServiceType:    "control_panel",
		DeploymentMode: "systemd",
		DatabaseName:   "autostream_panel",
	})
	policies.policies[activation.ServiceID] = policy
	controlPanelTarget := &PullUpdaterControlPanelTarget{
		ServiceID:             "control-panel",
		ServiceType:           "control_panel",
		EndpointRevision:      1,
		AppliedConfigRevision: 1,
		AppliedConfigSHA256:   "sha256:" + strings.Repeat("d", 64),
		AppliedEndpoint: ServiceEndpoint{
			Host: "127.0.0.1", Port: 18080, PublicURL: "http://127.0.0.1:18080",
		},
	}
	activation.ControlPanelTarget = controlPanelTarget
	agent := registry.services[activation.ServiceID]
	addPullActivationTargetReport(
		agent.ReportedCapabilities,
		controlPanelTarget.ServiceID,
		controlPanelTarget.registeredService(),
		policy,
	)
	registry.services[activation.ServiceID] = agent

	activated, err := policies.ActivatePullUpdaterOwnership(
		t.Context(), registry, updates, activation,
	)
	if err != nil {
		t.Fatal(err)
	}
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.Host = target.AppliedEndpoint.Host
	target.Port = target.AppliedEndpoint.Port
	target.SSLEnabled = target.AppliedEndpoint.SSLEnabled
	target.PublicURL = target.AppliedEndpoint.PublicURL
	target.Version = "v1.0.0"
	target.ReportedVersion = "v1.0.0"
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	target.EndpointStatus = "applied"
	registry.services[target.ServiceID] = target
	agent = registry.services[activation.ServiceID]
	agent.Status = "online"
	now := time.Now().UTC()
	agent.LastHeartbeatAt = &now
	agent.ReportedCapabilities["observe_only"] = false
	agent.ReportedCapabilities["mutation_enabled"] = true
	agent.ReportedCapabilities["ownership_epoch"] = activated.Ownership.OwnershipEpoch
	agent.ReportedCapabilities["policy_revision"] = activated.Policy.ProjectionRevision
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()

	if _, _, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewPort:                  controlPanelTarget.AppliedEndpoint.Port,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "synthetic-control-panel-port-collision",
			RequestedByUserID:        "admin-a",
			ControlPanelTarget:       controlPanelTarget,
		},
	); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("synthetic Control Panel port collision = %v, want ErrServicePortReserved", err)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.EndpointRevision != 3 || service.EndpointStatus != "applied" ||
		!sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) {
		t.Fatalf("collision partially mutated service = %#v", service)
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 100)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("collision partially created jobs = %#v err=%v", jobs, err)
	}
	reservations, err := updates.ListServicePortReservations(
		t.Context(), activation.ExecutionHostID,
	)
	if err != nil || len(reservations) != 1 ||
		reservations[0].ServiceID != "worker-a" ||
		reservations[0].Port != 18081 {
		t.Fatalf("collision partially mutated reservations = %#v err=%v", reservations, err)
	}
}

func TestSyntheticControlPanelHostPortFenceFailsClosedOnlyForExactPolicy(t *testing.T) {
	malformed := &PullUpdaterControlPanelTarget{
		ServiceID:   "control-panel-alias",
		ServiceType: "control_panel",
	}
	workerPolicy := UpdaterPolicy{
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	if err := validateSyntheticControlPanelHostPortFence(
		workerPolicy,
		"worker-a",
		18084,
		malformed,
	); err != nil {
		t.Fatalf("non-Control Panel policy read unrelated runtime: %v", err)
	}

	controlPanelPolicy := workerPolicy
	controlPanelPolicy.Targets = append(
		append([]UpdaterPolicyTarget(nil), workerPolicy.Targets...),
		UpdaterPolicyTarget{
			TargetID:       "control-panel",
			ServiceID:      "control-panel",
			ServiceType:    "control_panel",
			DeploymentMode: "systemd",
		},
	)
	for name, runtimeTarget := range map[string]*PullUpdaterControlPanelTarget{
		"missing":   nil,
		"malformed": malformed,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSyntheticControlPanelHostPortFence(
				controlPanelPolicy,
				"worker-a",
				18084,
				runtimeTarget,
			); !errors.Is(err, ErrSystemUpdateAgentNotReady) {
				t.Fatalf("error = %v, want ErrSystemUpdateAgentNotReady", err)
			}
		})
	}
}

func TestSystemUpdatePortIntentHashExcludesRuntimeLeaseAndJobIdentity(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	job, _, err := updates.CreateSystemdPortReconfigurationJob(
		t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
			TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
			IdempotencyKey: "port-intent", RequestedByUserID: "admin-a",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := job.PortReconfigure.PortPlanSHA256
	runtimeVariant := job
	runtimeVariant.ID = "a-different-runtime-job-id"
	runtimeVariant.LeaseGeneration = 99
	runtimeVariant.LeaseExpiresAt = ptrTime(time.Now().UTC())
	got, err := ComputeSystemUpdatePortIntentSHA256(runtimeVariant)
	if err != nil || got != want {
		t.Fatalf("runtime variant digest = %q err=%v, want %q", got, err, want)
	}
	differentIntent := job
	differentIntent.PortReconfigure = cloneSystemUpdatePortReconfiguration(job.PortReconfigure)
	differentIntent.PortReconfigure.NewPort++
	got, err = ComputeSystemUpdatePortIntentSHA256(differentIntent)
	if err != nil || got == want {
		t.Fatalf("different intent digest = %q err=%v, original %q", got, err, want)
	}
}

func TestMemorySystemdPortTerminalReportsCommitOrRollbackState(t *testing.T) {
	for _, test := range []struct {
		name               string
		result             SystemUpdatePortReconfigurationResult
		status             string
		wantPort           int
		wantRevision       int64
		wantConfigRevision int64
		wantEndpointStatus string
		wantReservations   int
	}{
		{
			name: "applied", result: SystemUpdatePortReconfigurationApplied,
			status: SystemUpdateStatusSucceeded, wantPort: 18084, wantRevision: 4,
			wantConfigRevision: 4, wantEndpointStatus: "applied", wantReservations: 1,
		},
		{
			name: "unchanged", result: SystemUpdatePortReconfigurationUnchanged,
			status: SystemUpdateStatusSucceeded, wantPort: 18081, wantRevision: 5,
			wantConfigRevision: 3, wantEndpointStatus: "applied", wantReservations: 1,
		},
		{
			name: "rolled back", result: SystemUpdatePortReconfigurationRolledBack,
			status: SystemUpdateStatusRolledBack, wantPort: 18081, wantRevision: 5,
			wantConfigRevision: 3, wantEndpointStatus: "rolled_back", wantReservations: 1,
		},
		{
			name: "rollback failed", result: SystemUpdatePortReconfigurationRollbackFailed,
			status: SystemUpdateStatusFailed, wantPort: 18081, wantRevision: 4,
			wantConfigRevision: 3, wantEndpointStatus: "rollback_failed", wantReservations: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates := readyMemorySystemdPortCoordinator(t)
			job, _, err := updates.CreateSystemdPortReconfigurationJob(
				t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
					TargetID: "worker-a", NewPort: 18084, ExpectedEndpointRevision: 3,
					IdempotencyKey: "port-terminal-" + test.name, RequestedByUserID: "admin-a",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			base := time.Now().UTC()
			claim, _, err := updates.ClaimSystemUpdateJob(
				t.Context(), "host-agent-a", "host-a", "",
				map[string]string{"worker-a": "systemd"}, base, time.Minute,
			)
			if err != nil {
				t.Fatal(err)
			}
			sequence := claim.ReportSequence
			if test.status == SystemUpdateStatusRolledBack {
				if _, applied, err := updates.ReportSystemUpdateJob(
					t.Context(), job.ID, SystemUpdateReport{
						AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
						LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
						Sequence: sequence, Status: SystemUpdateStatusInstalling, Progress: 70,
					}, base.Add(time.Second), time.Minute,
				); err != nil || !applied {
					t.Fatalf("installing report applied=%v err=%v", applied, err)
				}
				sequence++
				if _, applied, err := updates.ReportSystemUpdateJob(
					t.Context(), job.ID, SystemUpdateReport{
						AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
						LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
						Sequence: sequence, Status: SystemUpdateStatusRollingBack, Progress: 80,
					}, base.Add(1500*time.Millisecond), time.Minute,
				); err != nil || !applied {
					t.Fatalf("rolling back report applied=%v err=%v", applied, err)
				}
				sequence++
			}
			report := SystemUpdateReport{
				AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
				LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
				Sequence: sequence, Status: test.status, Progress: 100,
				PortReconfigure: &SystemUpdatePortReconfiguration{Result: test.result},
			}
			terminal, applied, err := updates.ReportSystemUpdateJob(
				t.Context(), job.ID, report, base.Add(2*time.Second), time.Minute,
			)
			if err != nil || !applied || terminal.PortReconfigure == nil ||
				terminal.PortReconfigure.Result != test.result ||
				terminal.Code != systemUpdatePortReportCode(test.result) {
				t.Fatalf("terminal = %#v applied=%v err=%v", terminal, applied, err)
			}
			replayed, applied, err := updates.ReportSystemUpdateJob(
				t.Context(), job.ID, report, base.Add(3*time.Second), time.Minute,
			)
			if err != nil || applied || replayed.PortReconfigure.Result != test.result {
				t.Fatalf("terminal replay = %#v applied=%v err=%v", replayed, applied, err)
			}
			service, err := registry.GetService(t.Context(), "worker-a")
			if err != nil {
				t.Fatal(err)
			}
			if service.AppliedEndpoint.Port != test.wantPort ||
				service.EndpointRevision != test.wantRevision ||
				service.AppliedConfigRevision != test.wantConfigRevision ||
				service.EndpointStatus != test.wantEndpointStatus {
				t.Fatalf("terminal service = %#v", service)
			}
			reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
			if err != nil || len(reservations) != test.wantReservations {
				t.Fatalf("terminal reservations = %#v err=%v", reservations, err)
			}
			if test.result == SystemUpdatePortReconfigurationApplied &&
				(reservations[0].Port != 18084 || reservations[0].ServiceRole != systemUpdatePortCurrentRole) {
				t.Fatalf("applied reservation = %#v", reservations)
			}
			if test.result == SystemUpdatePortReconfigurationRolledBack ||
				test.result == SystemUpdatePortReconfigurationUnchanged {
				next, created, err := updates.CreateSystemdPortReconfigurationJob(
					t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
						TargetID: "worker-a", NewPort: 18085,
						ExpectedEndpointRevision: test.wantRevision,
						IdempotencyKey:           "port-after-" + test.name,
						RequestedByUserID:        "admin-a",
					},
				)
				if err != nil || !created || next.PortReconfigure == nil ||
					next.PortReconfigure.ExpectedEndpointRevision != test.wantRevision {
					t.Fatalf("create after %s = %#v created=%v err=%v", test.name, next, created, err)
				}
			}
		})
	}
}

func readyMemorySystemdPortCoordinator(
	t *testing.T,
) (*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore) {
	t.Helper()
	policies, registry, updates, activation := newMemoryPullActivationFixture(t, false)
	activated, err := policies.ActivatePullUpdaterOwnership(
		t.Context(), registry, updates, activation,
	)
	if err != nil {
		t.Fatal(err)
	}
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.Host = target.AppliedEndpoint.Host
	target.Port = target.AppliedEndpoint.Port
	target.SSLEnabled = target.AppliedEndpoint.SSLEnabled
	target.PublicURL = target.AppliedEndpoint.PublicURL
	target.Version = "v1.0.0"
	target.ReportedVersion = "v1.0.0"
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	target.EndpointStatus = "applied"
	registry.services[target.ServiceID] = target
	agent := registry.services["host-agent-a"]
	agent.Status = "online"
	now := time.Now().UTC()
	agent.LastHeartbeatAt = &now
	agent.ReportedCapabilities["observe_only"] = false
	agent.ReportedCapabilities["mutation_enabled"] = true
	agent.ReportedCapabilities["ownership_epoch"] = activated.Ownership.OwnershipEpoch
	agent.ReportedCapabilities["policy_revision"] = activated.Policy.ProjectionRevision
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()
	return policies, registry, updates
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
