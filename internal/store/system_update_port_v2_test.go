package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

func TestMemorySTPortV2FullSourceDriftIsAtomic(t *testing.T) {
	mutations := map[string]func(*stPortV2Fixture){
		"unselected_config": func(f *stPortV2Fixture) {
			s := f.registry.services["observability-a"]
			s.AppliedConfigRevision++
			f.registry.services[s.ServiceID] = s
		},
		"credential_reference": func(f *stPortV2Fixture) {
			s := f.registry.services["observability-a"]
			s.TokenID = "replacement-reference"
			f.registry.services[s.ServiceID] = s
		},
		"listener_binding": func(f *stPortV2Fixture) {
			p := f.policies.policies["host-agent-a"]
			p.Targets[1].LocalListenPort++
			f.policies.policies[p.UpdaterID] = p
		},
		"database_binding": func(f *stPortV2Fixture) {
			p := f.policies.policies["host-agent-a"]
			p.Targets[1].DatabaseName = "other_database"
			f.policies.policies[p.UpdaterID] = p
		},
		"old_reservation": func(f *stPortV2Fixture) {
			delete(f.updates.portReservations, servicePortReservationKey{executionHostID: "host-a", networkNamespace: "host", protocol: "tcp", port: 18081})
		},
		"pending_reservation": func(f *stPortV2Fixture) {
			key := servicePortReservationKey{executionHostID: "host-a", networkNamespace: "host", protocol: "tcp", port: 18084}
			r := f.updates.portReservations[key]
			r.ServiceID = "other"
			f.updates.portReservations[key] = r
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newSTPortV2Fixture(t)
			addSTPortV2Observability(t, f)
			job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "drift")
			job = f.claim(t, job, "").Job
			f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
			binding := stPortV2GrantBinding(t, job, SystemUpdateMutationOperationPortReconfigure)
			issued, err := f.updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, Binding: binding}, time.Now().UTC(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			mutate(f)
			serviceBefore := f.registry.services["worker-a"]
			policyBefore := cloneUpdaterPolicy(f.policies.policies["host-agent-a"])
			if _, _, err := f.updates.ConsumeSystemUpdateMutationGrant(t.Context(), job.ID, issued.GrantToken, job.LeaseGeneration, binding, time.Now().UTC()); err == nil {
				t.Fatal("source drift consumed grant")
			}
			if !reflect.DeepEqual(serviceBefore, f.registry.services["worker-a"]) || !reflect.DeepEqual(policyBefore, f.policies.policies["host-agent-a"]) {
				t.Fatal("rejected consume partially committed T")
			}
			stored, _ := f.updates.GetSystemUpdateJob(t.Context(), job.ID)
			if stored.portTransaction.Phase != "created" {
				t.Fatal("rejected consume advanced phase")
			}
		})
	}
}

func TestMemorySTPortV2BindingRoundTripAndHostProjection(t *testing.T) {
	f := newSTPortV2Fixture(t)
	addSTPortV2Observability(t, f)
	before := f.snapshot(t)
	body, _ := json.Marshal(before)
	var decoded SystemUpdatePortPolicySnapshot
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	policy, err := portSnapshotPolicy(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Targets) != 2 || policy.Targets[1].DatabaseName != "autostream_observability" || policy.Targets[1].LocalListenPort != 18082 {
		t.Fatal("full persistence DTO lost json-excluded fields")
	}
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "complete")
	currentBefore, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
	beforeProjection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), "observability-a", currentBefore)
	if err != nil || beforeProjection.Ref.ProjectionRevision != 17 || beforeProjection.Ref.ConfigRevision != 44 {
		t.Fatalf("active B projection: %v", err)
	}
	job = f.claim(t, job, "").Job
	f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
	f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
	currentTarget, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
	activeProjection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), "observability-a", currentTarget)
	if err != nil || activeProjection.Ref.ProjectionRevision != 18 || activeProjection.Ref.ConfigRevision != 44 || activeProjection.Ref.SnapshotID != job.PortReconfigure.Target.SnapshotID {
		t.Fatalf("active full-host T projection: %v", err)
	}
	for _, binding := range job.portTransaction.Target.Snapshot.Bindings {
		if binding.BindingPolicyRevision != 12 {
			t.Fatal("unselected binding did not move with S")
		}
	}
	for _, target := range job.portTransaction.Target.Snapshot.Targets {
		if target.ServiceID == "observability-a" && (target.ConfigRevision != 44 || target.EndpointRevision != 8 || target.AppliedEndpointRevision != 8 || target.LocalListenPort != 18082) {
			t.Fatal("host projection changed unrelated target state")
		}
	}
	accepted := f.report(t, job, 2, SystemUpdateStatusSucceeded, stPortResult(job, contracts.SystemUpdatePortReconfigurationApplied, time.Now().UTC()))
	if accepted.PortResult == nil {
		t.Fatal("accepted proof absent")
	}
	other := f.registry.services["observability-a"]
	if other.AppliedConfigRevision != 44 || other.AppliedEndpointRevision != 8 {
		t.Fatal("apply wrote unselected service")
	}
	current, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
	projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), "observability-a", current)
	if err != nil || projection.Ref.ConfigRevision != 44 || projection.Ref.LocalListenPort != 18082 || projection.Ref.ProjectionRevision != 18 {
		t.Fatalf("accepted full-host projection: %v", err)
	}
}

func TestMemorySTPortV2RejectsUnconsumedReconcileAndAdvertisedOnly(t *testing.T) {
	t.Run("unconsumed_reconcile", func(t *testing.T) {
		f := newSTPortV2Fixture(t)
		job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "reconcile")
		job = f.claim(t, job, "").Job
		f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
		f.report(t, job, 2, SystemUpdateStatusReconciling, nil)
		binding := stPortV2GrantBinding(t, job, SystemUpdateMutationOperationPortReconfigureReconcile)
		if _, err := f.updates.IssueSystemUpdateMutationGrant(t.Context(), job.ID, IssueSystemUpdateMutationGrantParams{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, Binding: binding}, time.Now().UTC(), time.Minute); !errors.Is(err, ErrSystemUpdateAuthorizationState) {
			t.Fatalf("unconsumed B authorized root write: %v", err)
		}
		p, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
		if p.Revision != 11 || p.ProjectionRevision != 17 || p.LocalExecutorPolicyRevision != 23 {
			t.Fatal("reconcile advanced unconsumed policy")
		}
	})
	t.Run("advertised_only", func(t *testing.T) {
		f := newSTPortV2Fixture(t)
		before := f.snapshot(t)
		params := f.params(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18081, 8443, "ad-only")
		if _, _, err := f.updates.CreateSystemdPortReconfigurationJob(t.Context(), f.registry, f.policies, params); !errors.Is(err, ErrSystemUpdateAdvertisedOnlyUnsupported) {
			t.Fatalf("advertised-only accepted: %v", err)
		}
		if !portSnapshotsEqual(before, f.snapshot(t)) {
			t.Fatal("unsupported mode changed source")
		}
	})
}

func TestMemorySTPortV2DockerModesAndMappingDigest(t *testing.T) {
	for _, mode := range []contracts.SystemUpdatePortMode{contracts.SystemUpdatePortModeLocalOnly, contracts.SystemUpdatePortModeLocalAndAdvertised} {
		for _, result := range []contracts.SystemUpdatePortReconfigurationResult{contracts.SystemUpdatePortReconfigurationApplied, contracts.SystemUpdatePortReconfigurationRolledBack} {
			t.Run(string(mode)+"_"+string(result), func(t *testing.T) {
				f := newSTPortV2DockerFixture(t)
				before := f.snapshot(t)
				baseline := f.registry.services["host-agent-a"].ReportedCapabilities["port_policy_baseline"].(contracts.UpdaterPortPolicyBaseline)
				wantCompose := baseline.Targets[0].DockerRoot.ComposeConfigSHA256
				if before.Ref.AdvertisedPort == before.Ref.Docker.PublishedPort || "sha256:"+wantCompose == before.Ref.Docker.ComposePolicySHA256 {
					t.Fatal("fixture must distinguish advertised/published ports and full/policy Compose digests")
				}
				ad := 0
				if mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
					ad = 8443
				}
				job, created, err := f.updates.CreateDockerPortReconfigurationJob(t.Context(), f.registry, f.policies, CreateDockerPortReconfigurationJobParams{PortContractVersion: 2, Mode: mode, TargetID: "worker-a", NewPublishedPort: 18084, NewContainerPort: 8081, NewAdvertisedPort: ad, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "docker", RequestedByUserID: "admin-a", BuildPolicySnapshot: stPortTestBuilder})
				if err != nil || !created {
					t.Fatalf("Docker create: %v", err)
				}
				if job.PortReconfigure.DockerBaseline == nil || job.PortReconfigure.DockerBaseline.ApprovedComposeConfigSHA256 != wantCompose {
					t.Fatal("Docker execution baseline lost the observed full Compose digest")
				}
				for _, ref := range []*contracts.SystemUpdatePortSnapshotRef{job.PortReconfigure.Target, job.PortReconfigure.Rollback} {
					want, err := contracts.SystemUpdateDockerPortConfigSHA256(contracts.SystemUpdateTargetWorker, ref.Docker.PublishedPort, ref.Docker.ContainerPort, ref.ConfigRevision)
					if err != nil || want != ref.ConfigSHA256 {
						t.Fatal("Docker mapping digest differs from shared bytes")
					}
					if ref.Docker.ComposePolicySHA256 != before.Ref.Docker.ComposePolicySHA256 || ref.Docker.ComposeRevision != ref.ExecutorPolicyRevision {
						t.Fatal("Docker compose authority changed outside port delta")
					}
				}
				job = f.claim(t, job, "").Job
				f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
				f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
				status := SystemUpdateStatusSucceeded
				sequence := int64(2)
				ref := job.PortReconfigure.Target
				if result == contracts.SystemUpdatePortReconfigurationRolledBack {
					f.report(t, job, sequence, SystemUpdateStatusRollingBack, nil)
					sequence++
					status = SystemUpdateStatusRolledBack
					ref = job.PortReconfigure.Rollback
				}
				accepted := f.report(t, job, sequence, status, stPortResult(job, result, time.Now().UTC()))
				service := f.registry.services["worker-a"]
				if accepted.PortResult == nil || service.Port != ref.AdvertisedPort || service.AppliedConfigSHA256 != ref.ConfigSHA256 || service.AppliedEndpointRevision != ref.AppliedEndpointRevision {
					t.Fatal("Docker accepted state differs from fixed snapshot")
				}
				current, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
				projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, current)
				if err != nil || !reflect.DeepEqual(projection.Ref, *ref) {
					t.Fatalf("accepted Docker projection requires fresh baseline: %v", err)
				}
			})
		}
	}
	t.Run("reported_tuple_mismatch", func(t *testing.T) {
		f := newSTPortV2DockerFixture(t)
		before := f.snapshot(t)
		agent := f.registry.services["host-agent-a"]
		agent.ReportedCapabilities["reported_docker_published_ports"] = map[string]int64{"worker-a": 443}
		f.registry.services[agent.ServiceID] = agent
		_, _, err := f.updates.CreateDockerPortReconfigurationJob(t.Context(), f.registry, f.policies, CreateDockerPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalOnly, TargetID: "worker-a", NewPublishedPort: 18084, NewContainerPort: 8081, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "docker-negative", RequestedByUserID: "admin-a", BuildPolicySnapshot: stPortTestBuilder})
		if err == nil {
			t.Fatal("public443 was accepted as published listener")
		}
		if jobs, _ := f.updates.ListSystemUpdateJobs(t.Context(), 10); len(jobs) != 0 {
			t.Fatal("Docker mismatch inserted job")
		}
	})
}

func TestMemorySTPortV2ProjectionKeepsBootstrapPolling(t *testing.T) {
	for _, state := range []string{"unbound", "observe_only", "missing_host", "legacy_job"} {
		t.Run(state, func(t *testing.T) {
			f := newSTPortV2Fixture(t)
			current := f.policies.policies["host-agent-a"]
			current.LocalExecutorPolicySHA256 = ""
			f.policies.policies[current.UpdaterID] = current
			host := f.updates.executionHosts[current.ExecutionHostID]
			switch state {
			case "observe_only":
				host.OwnershipEpoch = 0
				host.PolicyRevision = 0
				f.updates.executionHosts[current.ExecutionHostID] = host
			case "missing_host":
				delete(f.updates.executionHosts, current.ExecutionHostID)
			case "legacy_job":
				f.updates.jobs["legacy"] = SystemUpdateJob{ID: "legacy", ExecutionHostID: current.ExecutionHostID, Status: SystemUpdateStatusSucceeded}
			}
			if _, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), "worker-a", current); !errors.Is(err, ErrNotFound) {
				t.Fatalf("normal policy polling was blocked without a v2 transaction: %v", err)
			}
		})
	}
}

func TestMemorySTPortV2ProjectionRejectsCanonicalAndStoredDrift(t *testing.T) {
	f := newSTPortV2Fixture(t)
	current, _ := f.policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if _, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), "worker-a", current); !errors.Is(err, ErrNotFound) {
		t.Fatalf("initial projection must be absent: %v", err)
	}
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "projection")
	job = f.claim(t, job, "").Job
	f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
	f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
	f.report(t, job, 2, SystemUpdateStatusSucceeded, stPortResult(job, contracts.SystemUpdatePortReconfigurationApplied, time.Now().UTC()))
	current, _ = f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
	wrong := cloneUpdaterPolicy(current)
	wrong.ProjectionRevision++
	if _, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, wrong); !errors.Is(err, ErrSystemUpdatePortPolicySnapshotUnavailable) {
		t.Fatalf("caller policy bypassed canonical state: %v", err)
	}
	f.refresh(t)
	next := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18086, 8443, "next-projection")
	if next.PortReconfigure.Before.SnapshotID != job.PortReconfigure.Target.SnapshotID {
		t.Fatal("second job did not start at accepted canonical snapshot")
	}
	if projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, current); err != nil || projection.Ref.ConfigRevision != 32 || projection.Ref.AdvertisedPort != 443 {
		t.Fatalf("accepted/current active B projection: %v", err)
	}
	stored := f.updates.jobs[job.ID]
	stored.portTransaction = cloneSystemUpdatePortTransaction(stored.portTransaction)
	stored.portTransaction.Target.Snapshot.Bindings[0].LocalListenPort = nil
	f.updates.jobs[job.ID] = stored
	if _, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, current); !errors.Is(err, ErrSystemUpdatePortPolicySnapshotUnavailable) {
		t.Fatalf("corrupt accepted snapshot fell back: %v", err)
	}
}

func TestMemorySTPortV2AcceptedThenKAndNoOpProjection(t *testing.T) {
	f := newSTPortV2Fixture(t)
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "accepted-before-k")
	job = f.claim(t, job, "").Job
	f.report(t, job, 1, SystemUpdateStatusInstalling, nil)
	f.consume(t, job, SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
	f.report(t, job, 2, SystemUpdateStatusSucceeded, stPortResult(job, contracts.SystemUpdatePortReconfigurationApplied, time.Now().UTC()))
	f.refresh(t)
	for _, key := range []string{"cancel-1", "cancel-2"} {
		next := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18086, 8443, key)
		if _, err := f.updates.CancelSystemUpdateJob(t.Context(), next.ID, "admin-a"); err != nil {
			t.Fatal(err)
		}
		current, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
		projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, current)
		if err != nil || !portSnapshotsEqual(projection, f.snapshot(t)) || projection.Ref.AppliedEndpointRevision != 3 || projection.Ref.ConfigRevision != 32 || projection.Ref.EndpointRevision != next.portTransaction.CancelEndpointRevision {
			t.Fatalf("K projection was blocked by old accepted proof: %v", err)
		}
	}
	noop := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "noop-after-k")
	noop = f.claim(t, noop, "").Job
	f.report(t, noop, 1, SystemUpdateStatusSucceeded, stPortResult(noop, contracts.SystemUpdatePortReconfigurationUnchanged, time.Now().UTC()))
	current, _ := f.policies.GetUpdaterPolicy(t.Context(), job.AgentServiceID)
	projection, err := f.updates.GetSystemUpdatePortPolicyProjection(t.Context(), job.TargetID, current)
	if err != nil || !portSnapshotsEqual(projection, f.snapshot(t)) || projection.Ref.EndpointRevision != 7 {
		t.Fatalf("no-op after K projection: %v", err)
	}
}

func TestMemorySTPortV2RegistrationPreservesAppliedEndpointRevision(t *testing.T) {
	for _, revision := range []int64{0, 3} {
		t.Run(map[int64]string{0: "unknown", 3: "confirmed"}[revision], func(t *testing.T) {
			f := newSTPortV2Fixture(t)
			token, err := f.registry.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
			if err != nil {
				t.Fatal(err)
			}
			before := f.registry.services["worker-a"]
			before.TokenID = token.ID
			before.AppliedEndpointRevision = revision
			f.registry.services[before.ServiceID] = before
			after, err := f.registry.RegisterService(t.Context(), token, ServiceRegistration{ServiceID: before.ServiceID, ServiceType: "worker", ServiceName: "worker-a", PublicURL: "https://worker-a.example.com:18084", Version: "v1.0.0"})
			if err != nil {
				t.Fatal(err)
			}
			if after.AppliedEndpointRevision != revision || after.EndpointRevision != before.EndpointRevision || !sameServiceEndpoint(after.AppliedEndpoint, before.AppliedEndpoint) || after.AppliedConfigRevision != before.AppliedConfigRevision {
				t.Fatal("registration changed confirmed or unknown applied state")
			}
		})
	}
}

func stPortV2GrantBinding(t *testing.T, job SystemUpdateJob, operation string) SystemUpdateMutationGrantBinding {
	t.Helper()
	runtime, err := ComputeSystemUpdatePortRuntimePlanSHA256(job, "session-1234567890123456")
	if err != nil {
		t.Fatal(err)
	}
	return SystemUpdateMutationGrantBinding{HostID: job.ExecutionHostID, TransportMode: job.TransportMode, OwnershipEpoch: job.OwnershipEpoch, PolicyRevision: job.PolicyRevision, TargetID: job.TargetID, TargetServiceType: job.TargetServiceType, TargetVersion: job.TargetVersion, DeploymentMode: job.DeploymentMode, JobOperation: job.Operation, Operation: operation, PlanSHA256: runtime, SessionID: "session-1234567890123456", PortReconfigure: cloneSystemUpdatePortReconfiguration(job.PortReconfigure)}
}

func addSTPortV2Observability(t *testing.T, f *stPortV2Fixture) {
	t.Helper()
	token, err := f.registry.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	service := f.registry.services["worker-a"]
	service.ServiceID = "observability-a"
	service.ServiceType = "observability"
	service.TokenID = token.ID
	service.EndpointRevision = 8
	service.AppliedEndpointRevision = 8
	service.AppliedConfigRevision = 44
	config, _ := contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetObservability, contracts.SystemUpdateDeploymentSystemd, 18082, 44)
	service.AppliedConfigSHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(config)
	f.registry.services[service.ServiceID] = service
	p := f.policies.policies["host-agent-a"]
	p.Targets = append(p.Targets, UpdaterPolicyTarget{TargetID: service.ServiceID, ServiceID: service.ServiceID, HostID: "host-a", ServiceType: "observability", DeploymentMode: "systemd", DatabaseName: "autostream_observability", LocalListenPort: 18082})
	f.policies.policies[p.UpdaterID] = p
	reservation := ServicePortReservation{ExecutionHostID: "host-a", NetworkNamespace: "host", Protocol: "tcp", Port: 18082, ServiceID: service.ServiceID, ServiceRole: "api"}
	f.updates.portReservations[servicePortKey(reservation)] = reservation
	f.refresh(t)
}

func TestMemorySTPortV2DockerReadinessKeepsDistinctPortProofs(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"published_as_advertised": func(c map[string]any) { c["reported_ports"] = map[string]int64{"worker-a": 18081} },
		"published_drift":         func(c map[string]any) { c["reported_docker_published_ports"] = map[string]int64{"worker-a": 18082} },
		"container_drift":         func(c map[string]any) { c["reported_docker_container_ports"] = map[string]int64{"worker-a": 8081} },
		"health_drift":            func(c map[string]any) { c["reported_docker_health_ports"] = map[string]int64{"worker-a": 18082} },
		"full_digest_as_policy": func(c map[string]any) {
			c["reported_docker_compose_sha256"] = map[string]string{"worker-a": strings.Repeat("f", 64)}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newSTPortV2DockerFixture(t)
			policy := f.policies.policies["host-agent-a"]
			agent := f.registry.services["host-agent-a"]
			service := f.registry.services["worker-a"]
			host := f.updates.executionHosts["host-a"]
			params := CreateSystemdPortReconfigurationJobParams{TargetID: "worker-a"}
			now := time.Now().UTC()
			if err := validateSystemUpdatePortV2Ready(policy, policy.Targets[0], service, agent, host, params, now); err != nil {
				t.Fatalf("valid independent Docker observations rejected: %v", err)
			}
			mutate(agent.ReportedCapabilities)
			if err := validateSystemUpdatePortV2Ready(policy, policy.Targets[0], service, agent, host, params, now); !errors.Is(err, ErrSystemUpdateAgentNotReady) {
				t.Fatalf("mismatched Docker observation was not rejected: %v", err)
			}
		})
	}
}

func newSTPortV2DockerFixture(t *testing.T) *stPortV2Fixture {
	t.Helper()
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	p := policies.policies["host-agent-a"]
	p.Revision = 11
	p.ProjectionRevision = 17
	p.LocalExecutorPolicyRevision = 23
	p.Targets[0].LocalListenPort = 0
	policies.policies[p.UpdaterID] = p
	host := updates.executionHosts["host-a"]
	host.PolicyRevision = 17
	updates.executionHosts[host.ExecutionHostID] = host
	service := registry.services["worker-a"]
	service.Port = 443
	service.PublicURL = "https://worker-a.example.com:443"
	service.AppliedEndpoint = &ServiceEndpoint{Host: service.Host, Port: 443, SSLEnabled: true, PublicURL: service.PublicURL}
	service.DesiredEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
	service.EndpointRevision = 3
	service.AppliedEndpointRevision = 3
	service.AppliedConfigRevision = 31
	service.AppliedConfigSHA256, _ = SystemUpdateDockerPortConfigSHA256("worker", 18081, 8080, 31)
	registry.services[service.ServiceID] = service
	docker := &contracts.SystemUpdatePortDockerSnapshot{PublishedHostIP: "127.0.0.1", PublishedPort: 18081, ContainerPort: 8080, HealthPort: 18081, ComposePolicySHA256: "sha256:" + strings.Repeat("d", 64), ComposeRevision: 23, VersionEnvSHA256: "sha256:" + strings.Repeat("e", 64), ImageID: "sha256:" + strings.Repeat("b", 64), RepositoryDigest: "sha256:" + strings.Repeat("c", 64)}
	baseline := contracts.UpdaterPortPolicyBaseline{PortContractVersion: 2, PolicyTransitionVersion: 1, AgentUID: 1001, AgentGID: 1001, SourcePolicyRevision: 11, ProjectionRevision: 17, ExecutorPolicyRevision: 23, ExecutorPolicySHA256: p.LocalExecutorPolicySHA256, ObservedAt: time.Now().UTC(), Targets: []contracts.UpdaterPortPolicyBaselineTarget{{ServiceID: "worker-a", ServiceType: contracts.SystemUpdateTargetWorker, DeploymentMode: contracts.SystemUpdateDeploymentDocker, EndpointRevision: 3, ConfigRevision: 31, ConfigSHA256: service.AppliedConfigSHA256, LocalListenPort: 18081, Docker: docker, DockerRoot: &contracts.UpdaterPortDockerRootBaseline{ComposeConfigSHA256: strings.Repeat("f", 64), CurrentVersion: "v1.0.0"}}}}
	agent := registry.services["host-agent-a"]
	agent.ReportedCapabilities["port_contract_version"] = 2
	agent.ReportedCapabilities["policy_transition_version"] = 1
	agent.ReportedCapabilities["policy_revision"] = 17
	agent.ReportedCapabilities["reported_executor_policy_revisions"] = map[string]int64{"worker-a": 23}
	agent.ReportedCapabilities["reported_ports"] = map[string]int64{"worker-a": int64(service.AppliedEndpoint.Port)}
	agent.ReportedCapabilities["reported_docker_compose_revisions"] = map[string]int64{"worker-a": 23}
	agent.ReportedCapabilities["reported_config_revisions"] = map[string]int64{"worker-a": 31}
	agent.ReportedCapabilities["reported_config_sha256"] = map[string]string{"worker-a": service.AppliedConfigSHA256}
	agent.ReportedCapabilities["port_policy_baseline"] = baseline
	registry.services[agent.ServiceID] = agent
	f := &stPortV2Fixture{policies: policies, registry: registry, updates: updates}
	all, err := sortedPortServices(registry.services, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := stPortTestBuilder(p, all)
	if err != nil {
		t.Fatal(err)
	}
	p.LocalExecutorPolicySHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(materialized.ExecutorPolicyJSON)
	policies.policies[p.UpdaterID] = p
	baseline.ExecutorPolicySHA256 = p.LocalExecutorPolicySHA256
	agent.ReportedCapabilities["port_policy_baseline"] = baseline
	agent.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]string{"worker-a": p.LocalExecutorPolicySHA256}
	registry.services[agent.ServiceID] = agent
	return f
}
