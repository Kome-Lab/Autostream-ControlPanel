package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

func TestMemoryCreateSystemdPortReconfigurationIsAtomicAndIdempotent(t *testing.T) {
	for _, mode := range []contracts.SystemUpdatePortMode{contracts.SystemUpdatePortModeLocalOnly, contracts.SystemUpdatePortModeLocalAndAdvertised} {
		t.Run(string(mode), func(t *testing.T) {
			f := newSTPortV2Fixture(t)
			params := f.params(t, mode, 18084, 8443, "create")
			before := f.snapshot(t)
			job, created, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params)
			if err != nil || !created {
				t.Fatalf("create: created=%v err=%v", created, err)
			}
			if job.PortReconfigure.PortContractVersion != 2 || job.PortReconfigure.Before.LocalListenPort != 18081 || job.PortReconfigure.Target.LocalListenPort != 18084 || job.PortReconfigure.OldPort != 0 || job.PortReconfigure.NewPort != 0 {
				t.Fatal("explicit local plan or legacy separation mismatch")
			}
			delta := int64(0)
			ad := 443
			if mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
				delta = 1
				ad = 8443
			}
			if job.PortReconfigure.Target.EndpointRevision != 3+delta || job.PortReconfigure.Target.ConfigRevision != 32 || job.PortReconfigure.Target.SourcePolicyRevision != 12 || job.PortReconfigure.Target.ProjectionRevision != 18 || job.PortReconfigure.Target.ExecutorPolicyRevision != 24 || job.PortReconfigure.Target.AdvertisedPort != ad {
				t.Fatal("independent transition counters mismatch")
			}
			policy, _ := f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
			if !portPolicyMatchesSnapshot(policy, before) {
				t.Fatal("create changed canonical policy before consume")
			}
			service, _ := f.registry.GetService(t.Context(), "worker-a")
			if service.AppliedEndpoint.Port != 443 || service.DesiredEndpoint.Port != ad || service.AppliedEndpointRevision != 3 || service.AppliedConfigRevision != 31 || service.EndpointRevision != 3+delta {
				t.Fatal("create applied/desired split mismatch")
			}
			reservations, _ := f.updates.ListServicePortReservations(t.Context(), "host-a")
			if len(reservations) != 2 || reservations[0].Port != 18081 || reservations[1].Port != 18084 {
				t.Fatal("local reservations mismatch")
			}
			replay, created, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params)
			if err != nil || created || replay.ID != job.ID {
				t.Fatalf("idempotent create: %v", err)
			}
			params.NewLocalListenPort++
			if _, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params); !errors.Is(err, ErrSystemUpdatePortIdempotencyConflict) {
				t.Fatalf("conflicting key: %v", err)
			}
		})
	}
}

func TestSystemdPortPlanKeepsAdvertisedHostOutOfLocalSidecarDigest(t *testing.T) {
	f := newSTPortV2Fixture(t)
	params := f.params(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18084, 8443, "independent")
	job, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetWorker, contracts.SystemUpdateDeploymentSystemd, 18084, 32)
	if err != nil {
		t.Fatal(err)
	}
	if job.PortReconfigure.Target.ConfigSHA256 != contracts.ComputeSystemUpdatePortBytesSHA256(expected) {
		t.Fatal("listener digest contains advertisement")
	}
	service, _ := f.registry.GetService(t.Context(), "worker-a")
	if service.DesiredEndpoint.Host != "worker.example.test" || !service.DesiredEndpoint.SSLEnabled || service.DesiredEndpoint.PublicURL != "https://worker.example.test:8443/api?tenant=a" {
		t.Fatal("advertised non-port fields changed")
	}
}

func TestMemoryCancelSystemdPortReconfigurationRollsBackPendingStateMonotonically(t *testing.T) {
	f := newSTPortV2Fixture(t)
	before := f.snapshot(t)
	params := f.params(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18084, 8443, "cancel")
	job, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := f.updates.CancelSystemUpdateJob(t.Context(), job.ID, "admin-a")
	if err != nil || canceled.Status != SystemUpdateStatusCancelled {
		t.Fatalf("cancel: %v", err)
	}
	service, _ := f.registry.GetService(t.Context(), "worker-a")
	if service.EndpointRevision != 5 || service.AppliedEndpointRevision != 3 || service.AppliedConfigRevision != 31 || !sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) {
		t.Fatal("K must advance E only")
	}
	policy, _ := f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if !portPolicyMatchesSnapshot(policy, before) {
		t.Fatal("cancel changed canonical root/policy")
	}
	reservations, _ := f.updates.ListServicePortReservations(t.Context(), "host-a")
	if len(reservations) != 1 || reservations[0].Port != 18081 {
		t.Fatal("cancel reservations mismatch")
	}
	next := f.params(t, contracts.SystemUpdatePortModeLocalOnly, 18085, 0, "after-cancel")
	created, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, next)
	if err != nil || created.PortReconfigure.Before.EndpointRevision != 5 || created.PortReconfigure.Before.AppliedEndpointRevision != 3 {
		t.Fatalf("create after K: %v", err)
	}
}

func TestMemorySystemdPortReconfigurationRejectsCollisionWithoutPartialState(t *testing.T) {
	f := newSTPortV2Fixture(t)
	before := f.snapshot(t)
	if _, _, err := f.updates.ReserveServicePort(t.Context(), ServicePortReservation{ExecutionHostID: "host-a", NetworkNamespace: "host", Protocol: "tcp", Port: 18084, ServiceID: "other", ServiceRole: "api"}); err != nil {
		t.Fatal(err)
	}
	params := f.params(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "collision")
	if _, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("collision: %v", err)
	}
	if after := f.snapshot(t); !portSnapshotsEqual(before, after) {
		t.Fatal("collision changed snapshot")
	}
	if jobs, _ := f.updates.ListSystemUpdateJobs(t.Context(), 10); len(jobs) != 0 {
		t.Fatal("collision inserted job")
	}
}

func TestMemorySystemdPortReconfigurationRejectsSyntheticControlPanelPortWithoutPartialState(t *testing.T) {
	f := newSTPortV2Fixture(t)
	configSHA256, err := updateradapter.SystemdConfigurePortSidecarSHA256("control_panel", 18080, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.synthetic = &PullUpdaterControlPanelTarget{ServiceID: "control-panel", ServiceType: "control_panel", EndpointRevision: 1, AppliedConfigRevision: 1, AppliedConfigSHA256: configSHA256, AppliedEndpoint: ServiceEndpoint{Host: "127.0.0.1", Port: 18080, PublicURL: "http://127.0.0.1:18080"}}
	f.policies.mu.Lock()
	policy := f.policies.policies["host-agent-a"]
	policy.Targets = append(policy.Targets, UpdaterPolicyTarget{TargetID: "control-panel", ServiceID: "control-panel", HostID: "host-a", ServiceType: "control_panel", DeploymentMode: "systemd", DatabaseName: "autostream"})
	f.policies.policies[policy.UpdaterID] = policy
	f.policies.mu.Unlock()
	f.refresh(t)
	before := f.snapshot(t)
	params := f.params(t, contracts.SystemUpdatePortModeLocalOnly, 18080, 0, "synthetic-collision")
	if _, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("synthetic collision: %v", err)
	}
	params.TargetID = "control-panel"
	params.NewLocalListenPort = 18090
	if _, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params); err == nil {
		t.Fatal("synthetic Control Panel change allowed")
	}
	if after := f.snapshot(t); !portSnapshotsEqual(before, after) {
		t.Fatal("synthetic rejection changed snapshot")
	}
}

func TestSystemUpdatePortIntentHashExcludesRuntimeLeaseAndJobIdentity(t *testing.T) {
	f := newSTPortV2Fixture(t)
	job, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, f.params(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "intent"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil {
		t.Fatal(err)
	}
	changed := job
	changed.ID = newUUID()
	changed.LeaseGeneration = 99
	changed.CreatedAt = job.CreatedAt.Add(time.Hour)
	after, err := ComputeSystemUpdatePortIntentSHA256(changed)
	if err != nil || before != after {
		t.Fatal("transport changed immutable intent")
	}
	job.LeaseGeneration = 1
	runtimeBefore, err := ComputeSystemUpdatePortRuntimePlanSHA256(job, "session-1234567890123456")
	if err != nil {
		t.Fatal(err)
	}
	changed = job
	changed.LeaseGeneration = 2
	runtimeAfter, err := ComputeSystemUpdatePortRuntimePlanSHA256(changed, "session-1234567890123456")
	if err != nil || runtimeBefore == runtimeAfter {
		t.Fatal("runtime hash omitted lease identity")
	}
}

func TestMemorySystemdPortTerminalReportsCommitOrRollbackState(t *testing.T) {
	for _, result := range []contracts.SystemUpdatePortReconfigurationResult{contracts.SystemUpdatePortReconfigurationApplied, contracts.SystemUpdatePortReconfigurationRolledBack, contracts.SystemUpdatePortReconfigurationUnchanged, contracts.SystemUpdatePortReconfigurationRollbackFailed} {
		t.Run(string(result), func(t *testing.T) {
			f := newSTPortV2Fixture(t)
			local, ad := 18084, 8443
			if result == contracts.SystemUpdatePortReconfigurationUnchanged {
				local = 18081
				ad = 443
			}
			before := f.snapshot(t)
			job := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, local, ad, "terminal")
			claim := f.claim(t, job, "")
			job = claim.Job
			sequence := int64(1)
			if result != contracts.SystemUpdatePortReconfigurationUnchanged {
				f.report(t, job, sequence, SystemUpdateStatusInstalling, nil)
				sequence++
				f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
			}
			status := SystemUpdateStatusSucceeded
			if result == contracts.SystemUpdatePortReconfigurationRolledBack {
				f.report(t, job, sequence, SystemUpdateStatusRollingBack, nil)
				sequence++
				status = SystemUpdateStatusRolledBack
			}
			if result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
				status = SystemUpdateStatusFailed
			}
			observed := stPortResult(job, result, time.Now().UTC())
			terminal := f.report(t, job, sequence, status, observed)
			if terminal.PortReconfigure.Result != "" {
				t.Fatal("immutable plan became mutable result")
			}
			if result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
				if terminal.PortResult != nil || !terminal.RecoveryRequired || terminal.LastRecoveryObservation == nil {
					t.Fatal("failed recovery occupied accepted slot")
				}
				reservations, _ := f.updates.ListServicePortReservations(t.Context(), "host-a")
				if len(reservations) != 2 {
					t.Fatal("failed rollback released reservation")
				}
				return
			}
			if terminal.PortResult == nil || terminal.PortResult.Result != result || terminal.RecoveryRequired {
				t.Fatal("typed accepted result missing")
			}
			if !contracts.EqualSystemUpdatePortResults(*terminal.PortResult, *observed) {
				t.Fatal("typed result changed during persistence")
			}
			report := stPortReport(job, sequence, status, observed)
			replay, applied, err := f.updates.ReportSystemUpdateJob(t.Context(), job.ID, report, time.Now().UTC(), time.Minute)
			if err != nil || applied || !contracts.EqualSystemUpdatePortResults(*replay.PortResult, *observed) {
				t.Fatalf("exact replay: %v", err)
			}
			expected := job.PortReconfigure.Target
			if result == contracts.SystemUpdatePortReconfigurationRolledBack {
				expected = job.PortReconfigure.Rollback
			}
			if result == contracts.SystemUpdatePortReconfigurationUnchanged {
				expected = job.PortReconfigure.Before
			}
			currentPolicy, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
			projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, currentPolicy)
			if err != nil || projection.Ref.SnapshotID != expected.SnapshotID || projection.Ref.SnapshotSHA256 != expected.SnapshotSHA256 {
				t.Fatalf("accepted projection: %v", err)
			}
			service, _ := f.registry.GetService(t.Context(), "worker-a")
			if service.Port != expected.AdvertisedPort || service.EndpointRevision != expected.EndpointRevision || service.AppliedEndpointRevision != expected.AppliedEndpointRevision || service.AppliedConfigRevision != expected.ConfigRevision || service.AppliedConfigSHA256 != expected.ConfigSHA256 {
				t.Fatal("terminal state differs from fixed snapshot")
			}
			if result == contracts.SystemUpdatePortReconfigurationRolledBack {
				bytes, _ := contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetWorker, contracts.SystemUpdateDeploymentSystemd, 18081, 33)
				if service.AppliedConfigSHA256 != contracts.ComputeSystemUpdatePortBytesSHA256(bytes) || service.AppliedConfigRevision != 33 {
					t.Fatal("rollback reused B bytes/revision")
				}
			}
			if result == contracts.SystemUpdatePortReconfigurationUnchanged {
				after := f.snapshot(t)
				if !portSnapshotsEqual(before, after) {
					t.Fatal("no-op mutated policy/config")
				}
			}
		})
	}
	t.Run("changed_target_cannot_report_unchanged", func(t *testing.T) {
		f := newSTPortV2Fixture(t)
		job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "negative-noop")
		claim := f.claim(t, job, "")
		result := stPortResult(claim.Job, contracts.SystemUpdatePortReconfigurationUnchanged, time.Now().UTC())
		if _, _, err := f.updates.ReportSystemUpdateJob(t.Context(), job.ID, stPortReport(claim.Job, 1, SystemUpdateStatusSucceeded, result), time.Now().UTC(), time.Minute); !errors.Is(err, ErrSystemUpdatePortResultMismatch) {
			t.Fatalf("changed unchanged accepted: %v", err)
		}
	})
}

type stPortV2Fixture struct {
	policies  *MemoryUpdaterPolicyStore
	registry  *MemoryAuthStore
	updates   *MemorySystemUpdateStore
	synthetic *PullUpdaterControlPanelTarget
}

func newSTPortV2Fixture(t *testing.T) *stPortV2Fixture {
	t.Helper()
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	policy := policies.policies["host-agent-a"]
	policy.Revision = 11
	policy.ProjectionRevision = 17
	policy.LocalExecutorPolicyRevision = 23
	policies.policies[policy.UpdaterID] = policy
	host := updates.executionHosts["host-a"]
	host.PolicyRevision = 17
	updates.executionHosts["host-a"] = host
	service := registry.services["worker-a"]
	service.EndpointRevision = 3
	service.AppliedEndpointRevision = 3
	service.AppliedConfigRevision = 31
	config, _ := contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetWorker, contracts.SystemUpdateDeploymentSystemd, 18081, 31)
	service.AppliedConfigSHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(config)
	service.AppliedEndpoint = &ServiceEndpoint{Host: "worker.example.test", Port: 443, SSLEnabled: true, PublicURL: "https://worker.example.test:443/api?tenant=a"}
	service.DesiredEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
	service.Host = service.AppliedEndpoint.Host
	service.Port = 443
	service.SSLEnabled = true
	service.PublicURL = service.AppliedEndpoint.PublicURL
	registry.services[service.ServiceID] = service
	f := &stPortV2Fixture{policies: policies, registry: registry, updates: updates}
	f.refresh(t)
	return f
}
func stPortTestBuilder(policy UpdaterPolicy, services []RegisteredService) (SystemUpdatePortPolicyMaterialization, error) {
	source := updateradapter.HostAgentConfigurePolicySource{PanelURL: "https://panel.example.test", ExecutionHostID: policy.ExecutionHostID, AgentUID: 1001, AgentGID: 1001, SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision, LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision}
	byID := map[string]RegisteredService{}
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	for _, target := range policy.Targets {
		service := byID[target.ServiceID]
		local, ok := PullUpdaterPolicyTargetLocalListenPort(target, service)
		var docker *contracts.SystemUpdatePortDockerSnapshot
		var dockerRoot *contracts.UpdaterPortDockerRootBaseline
		if target.DeploymentMode == "docker" {
			var err error
			docker, err = systemUpdatePortDockerSnapshotFromAgent(byID[policy.UpdaterID], target.ServiceID)
			if err != nil {
				return SystemUpdatePortPolicyMaterialization{}, err
			}
			local, ok = docker.PublishedPort, true
			body, _ := json.Marshal(byID[policy.UpdaterID].ReportedCapabilities["port_policy_baseline"])
			var baseline contracts.UpdaterPortPolicyBaseline
			_ = json.Unmarshal(body, &baseline)
			for _, observed := range baseline.Targets {
				if observed.ServiceID == target.ServiceID {
					dockerRoot = observed.DockerRoot
				}
			}
		}
		if !ok {
			return SystemUpdatePortPolicyMaterialization{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		source.Targets = append(source.Targets, updateradapter.HostAgentConfigurePolicyTarget{ServiceID: target.ServiceID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode, DatabaseName: target.DatabaseName, EndpointRevision: service.AppliedEndpointRevision, AppliedConfigRevision: service.AppliedConfigRevision, AppliedConfigSHA256: service.AppliedConfigSHA256, AppliedEndpointPort: service.AppliedEndpoint.Port, LocalListenPort: local, DockerSnapshot: docker, DockerRoot: dockerRoot})
	}
	projection, err := updateradapter.BuildSystemUpdatePortPolicy(source)
	return SystemUpdatePortPolicyMaterialization{ExecutorPolicyJSON: projection.Policy}, err
}
func (f *stPortV2Fixture) refresh(t *testing.T) {
	t.Helper()
	policy := f.policies.policies["host-agent-a"]
	all, err := sortedPortServices(f.registry.services, policy, f.synthetic)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := stPortTestBuilder(policy, all)
	if err != nil {
		t.Fatal(err)
	}
	policy.LocalExecutorPolicySHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(materialized.ExecutorPolicyJSON)
	f.policies.policies[policy.UpdaterID] = policy
	agent := f.registry.services["host-agent-a"]
	agent.ReportedCapabilities["port_contract_version"] = 2
	agent.ReportedCapabilities["policy_transition_version"] = 1
	agent.ReportedCapabilities["policy_revision"] = policy.ProjectionRevision
	baseline := contracts.UpdaterPortPolicyBaseline{PortContractVersion: 2, PolicyTransitionVersion: 1, AgentUID: 1001, AgentGID: 1001, SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision, ExecutorPolicyRevision: policy.LocalExecutorPolicyRevision, ExecutorPolicySHA256: policy.LocalExecutorPolicySHA256, ObservedAt: time.Now().UTC()}
	for _, target := range policy.Targets {
		service, ok := f.registry.services[target.ServiceID]
		if !ok && f.synthetic != nil && target.ServiceID == "control-panel" {
			service = f.synthetic.registeredService()
			service.AppliedEndpointRevision = service.EndpointRevision
		}
		local, _ := PullUpdaterPolicyTargetLocalListenPort(target, service)
		baseline.Targets = append(baseline.Targets, contracts.UpdaterPortPolicyBaselineTarget{ServiceID: target.ServiceID, ServiceType: contracts.SystemUpdateTargetType(target.ServiceType), DeploymentMode: contracts.SystemUpdateDeploymentMode(target.DeploymentMode), EndpointRevision: service.AppliedEndpointRevision, ConfigRevision: service.AppliedConfigRevision, ConfigSHA256: service.AppliedConfigSHA256, LocalListenPort: local})
		if target.ServiceID == "worker-a" {
			agent.ReportedCapabilities["reported_ports"] = map[string]int64{"worker-a": int64(local)}
			agent.ReportedCapabilities["reported_config_revisions"] = map[string]int64{"worker-a": service.AppliedConfigRevision}
			agent.ReportedCapabilities["reported_config_sha256"] = map[string]string{"worker-a": service.AppliedConfigSHA256}
			agent.ReportedCapabilities["reported_executor_policy_revisions"] = map[string]int64{"worker-a": policy.LocalExecutorPolicyRevision}
			agent.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]string{"worker-a": policy.LocalExecutorPolicySHA256}
		}
	}
	agent.ReportedCapabilities["port_policy_baseline"] = baseline
	f.registry.services[agent.ServiceID] = agent
}
func (f *stPortV2Fixture) snapshot(t *testing.T) SystemUpdatePortPolicySnapshot {
	t.Helper()
	snapshot, err := f.updates.GetSystemUpdatePortPolicySnapshot(t.Context(), f.registry, f.policies, SystemUpdatePortSnapshotParams{TargetID: "worker-a", ControlPanelTarget: f.synthetic, BuildPolicySnapshot: stPortTestBuilder})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func (f *stPortV2Fixture) params(t *testing.T, mode contracts.SystemUpdatePortMode, local, advertised int, key string) CreateSystemdPortReconfigurationJobParams {
	t.Helper()
	before := f.snapshot(t)
	desired := before.Ref.ConfigRevision
	if local != before.Ref.LocalListenPort {
		desired++
	}
	if mode == contracts.SystemUpdatePortModeLocalOnly {
		advertised = 0
	}
	return CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: mode, TargetID: "worker-a", NewLocalListenPort: local, NewAdvertisedPort: advertised, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: before.Ref.EndpointRevision, ExpectedDesiredRevision: desired, ExpectedFence: f.updates.executionHosts["host-a"].OwnershipEpoch, IdempotencyKey: key, RequestedByUserID: "admin-a", BuildPolicySnapshot: stPortTestBuilder, ControlPanelTarget: f.synthetic}
}
func (f *stPortV2Fixture) create(t *testing.T, mode contracts.SystemUpdatePortMode, local, advertised int, key string) SystemUpdateJob {
	t.Helper()
	job, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, f.params(t, mode, local, advertised, key))
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func (f *stPortV2Fixture) claim(t *testing.T, job SystemUpdateJob, active string) SystemUpdateClaim {
	t.Helper()
	lease := int64(1)
	if active != "" {
		lease = job.LeaseGeneration
	}
	claim, _, err := f.updates.ClaimSystemUpdateJobV2(t.Context(), "host-agent-a", "host-a", active, lease, job.OwnershipEpoch, map[string]string{"worker-a": job.DeploymentMode}, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}
func stPortReport(job SystemUpdateJob, sequence int64, status string, result *SystemUpdatePortResultV2) SystemUpdateReport {
	return SystemUpdateReport{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, DesiredRevision: job.PortReconfigure.Target.ConfigRevision, Fence: job.OwnershipEpoch, Sequence: sequence, Status: status, Progress: 100, PortResult: result}
}
func (f *stPortV2Fixture) report(t *testing.T, job SystemUpdateJob, sequence int64, status string, result *SystemUpdatePortResultV2) SystemUpdateJob {
	t.Helper()
	next, applied, err := f.updates.ReportSystemUpdateJob(t.Context(), job.ID, stPortReport(job, sequence, status, result), time.Now().UTC(), time.Minute)
	if err != nil || !applied {
		t.Fatalf("report %s: applied=%v err=%v", status, applied, err)
	}
	return next
}
func (f *stPortV2Fixture) consume(t *testing.T, job SystemUpdateJob, operation, session string) SystemUpdateMutationGrant {
	t.Helper()
	runtime, err := ComputeSystemUpdatePortRuntimePlanSHA256(job, session)
	if err != nil {
		t.Fatal(err)
	}
	binding := SystemUpdateMutationGrantBinding{HostID: job.ExecutionHostID, TransportMode: job.TransportMode, OwnershipEpoch: job.OwnershipEpoch, PolicyRevision: job.PolicyRevision, TargetID: job.TargetID, TargetServiceType: job.TargetServiceType, TargetVersion: job.TargetVersion, DeploymentMode: job.DeploymentMode, JobOperation: job.Operation, Operation: operation, PlanSHA256: runtime, SessionID: session, PortReconfigure: cloneSystemUpdatePortReconfiguration(job.PortReconfigure)}
	issued, err := f.updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, Binding: binding}, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	consumed, replayed, err := f.updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, job.LeaseGeneration, binding, time.Now().UTC())
	if err != nil || replayed {
		t.Fatalf("consume: replayed=%v err=%v", replayed, err)
	}
	return consumed
}
func stPortResult(job SystemUpdateJob, result contracts.SystemUpdatePortReconfigurationResult, observedAt time.Time) *SystemUpdatePortResultV2 {
	value := &SystemUpdatePortResultV2{Result: result, Observation: contracts.SystemUpdatePortObservation{ObservedAt: observedAt}}
	if result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
		return value
	}
	ref := job.PortReconfigure.Target
	if result == contracts.SystemUpdatePortReconfigurationRolledBack {
		ref = job.PortReconfigure.Rollback
	}
	if result == contracts.SystemUpdatePortReconfigurationUnchanged {
		ref = job.PortReconfigure.Before
	}
	value.ObservedSnapshotID = ref.SnapshotID
	value.ObservedSnapshotSHA256 = ref.SnapshotSHA256
	value.ObservedConfigRevision = ref.ConfigRevision
	value.ObservedConfigSHA256 = ref.ConfigSHA256
	value.ObservedExecutorPolicyRevision = ref.ExecutorPolicyRevision
	value.ObservedExecutorPolicySHA256 = ref.ExecutorPolicySHA256
	value.Observation.PolicyDiskVerified = true
	value.Observation.PolicyMemoryVerified = true
	value.Observation.AgentProjectionVerified = true
	value.Observation.ListenerVerified = true
	if ref.Docker != nil {
		container := strings.Repeat("f", 64)
		if result == contracts.SystemUpdatePortReconfigurationUnchanged {
			container = job.PortReconfigure.DockerBaseline.ExpectedContainerID
		}
		value.RuntimeInstance = &contracts.SystemUpdatePortRuntimeInstance{ContainerID: container, ImageID: ref.Docker.ImageID, RepositoryDigest: ref.Docker.RepositoryDigest}
	}
	return value
}

func TestMemorySTPortV2RecoveryAfterRollbackFailure(t *testing.T) {
	f := newSTPortV2Fixture(t)
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "recovery")
	claim := f.claim(t, job, "")
	job = claim.Job
	f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
	f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
	failedResult := stPortResult(job, contracts.SystemUpdatePortReconfigurationRollbackFailed, time.Now().UTC())
	failed := f.report(t, job, 2, SystemUpdateStatusFailed, failedResult)
	if failed.PortResult != nil || !failed.RecoveryRequired {
		t.Fatal("rollback_failed consumed accepted result")
	}
	active, err := f.updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil || active.ID != job.ID {
		t.Fatal("recovery hold disappeared from active lookup")
	}
	if _, complete, err := f.updates.InspectSystemUpdateActiveJob(t.Context(), job.AgentServiceID, job.ID); err != nil || complete {
		t.Fatal("failed recovery incorrectly reported complete")
	}
	restarted := f.claim(t, failed, job.ID)
	if !restarted.RecoveryRequired || restarted.Job.PolicyRevision != 17 {
		t.Fatal("recovery changed immutable JP")
	}
	f.consume(t, restarted.Job, SystemUpdateMutationOperationPortReconfigureReconcile, "session-2234567890123456")
	policy, _ := f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if policy.Revision != 12 || policy.ProjectionRevision != 18 || policy.LocalExecutorPolicyRevision != 24 {
		t.Fatal("reconcile incremented policy twice")
	}
	accepted := f.report(t, restarted.Job, 1, SystemUpdateStatusRolledBack, stPortResult(restarted.Job, contracts.SystemUpdatePortReconfigurationRolledBack, time.Now().UTC()))
	if accepted.PortResult == nil || accepted.RecoveryRequired || accepted.PortResult.Result != contracts.SystemUpdatePortReconfigurationRolledBack {
		t.Fatal("R recovery did not become first accepted result")
	}
	policy, _ = f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if policy.Revision != 13 || policy.ProjectionRevision != 19 || policy.LocalExecutorPolicyRevision != 25 {
		t.Fatal("R policy counters mismatch")
	}
	for _, late := range []*SystemUpdatePortResultV2{failedResult, stPortResult(job, contracts.SystemUpdatePortReconfigurationApplied, time.Now().UTC())} {
		if _, _, err := f.updates.ReportSystemUpdateJob(t.Context(), job.ID, stPortReport(job, 2, SystemUpdateStatusFailed, late), time.Now().UTC(), time.Minute); err == nil {
			t.Fatal("late conflicting result accepted")
		}
	}
	after, _ := f.updates.GetSystemUpdateJob(t.Context(), job.ID)
	if !contracts.EqualSystemUpdatePortResults(*accepted.PortResult, *after.PortResult) {
		t.Fatal("late result rewrote accepted result")
	}
	reservations, _ := f.updates.ListServicePortReservations(t.Context(), "host-a")
	if len(reservations) != 1 || reservations[0].Port != 18081 {
		t.Fatal("R did not release pending")
	}
}

func TestMemorySTPortV2BaselineAndSnapshotBindings(t *testing.T) {
	f := newSTPortV2Fixture(t)
	before := f.snapshot(t)
	body, _ := json.Marshal(before)
	var restored SystemUpdatePortPolicySnapshot
	if json.Unmarshal(body, &restored) != nil {
		t.Fatal("snapshot JSON failed")
	}
	policy, err := portSnapshotPolicy(restored)
	if err != nil || policy.Targets[0].LocalListenPort != 18081 {
		t.Fatal("JSON-excluded listener lost on restore")
	}
	for _, field := range []string{"listener", "executor_digest", "applied_revision"} {
		t.Run(field, func(t *testing.T) {
			candidate := newSTPortV2Fixture(t)
			switch field {
			case "listener":
				p := candidate.policies.policies["host-agent-a"]
				p.Targets[0].LocalListenPort = 0
				candidate.policies.policies[p.UpdaterID] = p
			case "executor_digest":
				p := candidate.policies.policies["host-agent-a"]
				p.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("0", 64)
				candidate.policies.policies[p.UpdaterID] = p
			case "applied_revision":
				s := candidate.registry.services["worker-a"]
				s.AppliedEndpointRevision = 0
				candidate.registry.services[s.ServiceID] = s
			}
			if _, err := candidate.updates.GetSystemUpdatePortPolicySnapshot(t.Context(), candidate.registry, candidate.policies, SystemUpdatePortSnapshotParams{TargetID: "worker-a", BuildPolicySnapshot: stPortTestBuilder}); err == nil {
				t.Fatal("incomplete snapshot accepted")
			}
		})
	}
	service := f.registry.services["worker-a"]
	service.AppliedEndpointRevision = 0
	f.registry.services[service.ServiceID] = service
	baseline := f.registry.services["host-agent-a"].ReportedCapabilities["port_policy_baseline"].(contracts.UpdaterPortPolicyBaseline)
	params := ConfirmSystemUpdatePortPolicyBaselineParams{Baseline: &baseline, AgentServiceID: "host-agent-a", ExpectedSourcePolicyRevision: 11, ExpectedProjectionRevision: 17, ExpectedExecutorPolicyRevision: 23, ExpectedExecutorPolicySHA256: before.Ref.ExecutorPolicySHA256, AppliedEndpointRevisions: map[string]int64{"worker-a": 3}, BuildPolicySnapshot: stPortTestBuilder}
	if err := f.updates.ConfirmSystemUpdatePortPolicyBaseline(t.Context(), f.registry, f.policies, params); err != nil {
		t.Fatal(err)
	}
	if after := f.snapshot(t); !portSnapshotsEqual(before, after) {
		t.Fatal("baseline confirmation changed source")
	}
	baseline.Targets[0].ConfigRevision++
	if err := f.updates.ConfirmSystemUpdatePortPolicyBaseline(t.Context(), f.registry, f.policies, params); err == nil {
		t.Fatal("conflicting baseline accepted")
	}
}

func TestMemorySTPortV2PolicySaveRevisionContinuity(t *testing.T) {
	f := newSTPortV2Fixture(t)
	policy, _ := f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	saved, err := f.policies.SavePullUpdaterPolicy(t.Context(), f.updates, policy.UpdaterID, policy.Revision, f.updates.executionHosts["host-a"].OwnershipEpoch, policy)
	if err != nil || saved.Revision != 12 || saved.ProjectionRevision != 18 || saved.LocalExecutorPolicyRevision != 24 {
		t.Fatalf("normal policy save counter continuity: %v", err)
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
