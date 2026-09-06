package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

type stPortProjectionCapture struct {
	store.SystemUpdateStore
	snapshot    store.SystemUpdatePortPolicySnapshot
	err         error
	activeCalls int
}

func (s *stPortProjectionCapture) GetSystemUpdatePortPolicyProjection(context.Context, string, store.UpdaterPolicy) (store.SystemUpdatePortPolicySnapshot, error) {
	return s.snapshot, s.err
}

func (s *stPortProjectionCapture) GetActiveSystemUpdateJob(context.Context, string) (store.SystemUpdateJob, error) {
	s.activeCalls++
	return store.SystemUpdateJob{}, store.ErrNotFound
}

func TestSTPortHTTPDockerCanonicalProjectionAndInitialBaseline(t *testing.T) {
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	endpoint := &store.ServiceEndpoint{Host: "worker.example.test", Port: 443, SSLEnabled: true}
	endpointSHA, err := contracts.ComputeSystemUpdatePortEndpointSHA256(contracts.SystemUpdatePortEndpoint{Host: endpoint.Host, Port: endpoint.Port, SSLEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	configSHA, err := contracts.SystemUpdateDockerPortConfigSHA256(contracts.SystemUpdateTargetWorker, 18084, 18080, 4)
	if err != nil {
		t.Fatal(err)
	}
	docker := &contracts.SystemUpdatePortDockerSnapshot{PublishedHostIP: "127.0.0.1", PublishedPort: 18084, ContainerPort: 18080, HealthPort: 18084,
		ComposePolicySHA256: digest("a"), ComposeRevision: 10, VersionEnvSHA256: digest("b"), ImageID: digest("c"), RepositoryDigest: digest("d")}
	ref := contracts.SystemUpdatePortSnapshotRef{SnapshotID: "ps1:" + strings.Repeat("e", 64), SnapshotSHA256: digest("e"), SourcePolicyRevision: 12, ProjectionRevision: 18,
		ExecutorPolicyRevision: 24, ExecutorPolicySHA256: digest("f"), EndpointRevision: 8, AppliedEndpointRevision: 8, ConfigRevision: 4, ConfigSHA256: configSHA,
		LocalListenPort: 18084, AdvertisedPort: 443, AdvertisedEndpointSHA256: endpointSHA, Docker: docker}
	policy := store.UpdaterPolicy{UpdaterID: "agent-a", ExecutionHostID: "host-a", Revision: 12, ProjectionRevision: 18, LocalExecutorPolicyRevision: 24, LocalExecutorPolicySHA256: ref.ExecutorPolicySHA256}
	agent := store.RegisteredService{ServiceID: "agent-a", ServiceType: "update_agent", ExecutionHostID: "host-a", OwnershipEpoch: 2}
	initial := hostAgentPolicyTarget{ServiceID: "worker-a", ServiceType: "worker", DeploymentMode: "docker", EndpointRevision: 8, AppliedEndpointRevision: 8,
		AppliedConfigRevision: 4, AppliedConfigSHA256: configSHA, AppliedEndpoint: endpoint, DesiredEndpoint: copyHostAgentEndpoint(endpoint)}
	baseline := contracts.UpdaterPortPolicyBaseline{PortContractVersion: 2, PolicyTransitionVersion: 1, AgentUID: 1001, AgentGID: 1001,
		SourcePolicyRevision: 12, ProjectionRevision: 18, ExecutorPolicyRevision: 24, ExecutorPolicySHA256: ref.ExecutorPolicySHA256, ObservedAt: time.Now().UTC(),
		Targets: []contracts.UpdaterPortPolicyBaselineTarget{{ServiceID: "worker-a", ServiceType: contracts.SystemUpdateTargetWorker, DeploymentMode: contracts.SystemUpdateDeploymentDocker,
			EndpointRevision: 8, ConfigRevision: 4, ConfigSHA256: configSHA, LocalListenPort: 18084, Docker: docker, DockerRoot: &contracts.UpdaterPortDockerRootBaseline{ComposeConfigSHA256: strings.Repeat("1", 64)}}}}
	if err := contracts.ValidateUpdaterPortPolicyBaseline(baseline); err != nil {
		t.Fatal("baseline fixture invalid")
	}
	agent.ReportedCapabilities = map[string]any{"port_policy_baseline": baseline}
	t.Run("durable_current_snapshot_survives_clear_active", func(t *testing.T) {
		capture := &stPortProjectionCapture{snapshot: store.SystemUpdatePortPolicySnapshot{Ref: ref}}
		server := &Server{systemUpdates: capture}
		item := initial
		if err := server.projectSystemUpdatePortPolicyTarget(t.Context(), agent, policy, &item); err != nil {
			t.Fatal(err)
		}
		if capture.activeCalls != 0 || item.LocalListenEndpoint == nil || item.LocalListenEndpoint.Port != 18084 || item.LocalHealthEndpoint.Port != 18084 ||
			item.AppliedEndpoint.Port != 443 || item.AppliedConfigRevision != 4 || item.EndpointRevision != 8 || item.AppliedEndpointRevision != 8 {
			t.Fatal("canonical projection lost separate local/advertised state")
		}
		if initial.LocalListenEndpoint != nil || initial.AppliedEndpoint.Port != 443 {
			t.Fatal("read projection mutated input state")
		}
	})
	t.Run("unavailable_proof_does_not_fallback_to_heartbeat", func(t *testing.T) {
		capture := &stPortProjectionCapture{err: store.ErrSystemUpdatePortPolicySnapshotUnavailable}
		item := initial
		if err := (&Server{systemUpdates: capture}).projectSystemUpdatePortPolicyTarget(t.Context(), agent, policy, &item); !errors.Is(err, store.ErrSystemUpdatePortPolicySnapshotUnavailable) || capture.activeCalls != 0 || item.LocalListenEndpoint != nil {
			t.Fatal("corrupt durable proof used a heartbeat fallback")
		}
	})
	t.Run("stale_canonical_and_endpoint_rejected", func(t *testing.T) {
		for _, mutate := range []func(*store.UpdaterPolicy, *hostAgentPolicyTarget){
			func(p *store.UpdaterPolicy, _ *hostAgentPolicyTarget) { p.ProjectionRevision++ },
			func(_ *store.UpdaterPolicy, item *hostAgentPolicyTarget) {
				item.AppliedEndpoint = &store.ServiceEndpoint{Host: "wrong.example.test", Port: 443}
				item.DesiredEndpoint = item.AppliedEndpoint
			},
		} {
			p, item := policy, initial
			mutate(&p, &item)
			capture := &stPortProjectionCapture{snapshot: store.SystemUpdatePortPolicySnapshot{Ref: ref}}
			if err := (&Server{systemUpdates: capture}).projectSystemUpdatePortPolicyTarget(t.Context(), agent, p, &item); !errors.Is(err, store.ErrSystemUpdatePortSnapshotStale) || item.LocalListenEndpoint != nil {
				t.Fatal("stale source or advertised endpoint accepted")
			}
		}
	})
	t.Run("first_authenticated_baseline_and_drift", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(*store.UpdaterPolicy, *hostAgentPolicyTarget)
			want   bool
		}{
			{"exact", func(*store.UpdaterPolicy, *hostAgentPolicyTarget) {}, true},
			{"policy", func(p *store.UpdaterPolicy, _ *hostAgentPolicyTarget) { p.LocalExecutorPolicyRevision++ }, false},
			{"unknown_applied", func(_ *store.UpdaterPolicy, i *hostAgentPolicyTarget) { i.AppliedEndpointRevision = 0 }, false},
			{"config", func(_ *store.UpdaterPolicy, i *hostAgentPolicyTarget) { i.AppliedConfigRevision++ }, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p, item := policy, initial
				tc.mutate(&p, &item)
				capture := &stPortProjectionCapture{err: store.ErrNotFound}
				if err := (&Server{systemUpdates: capture}).projectSystemUpdatePortPolicyTarget(t.Context(), agent, p, &item); err != nil {
					t.Fatal(err)
				}
				if (item.LocalListenEndpoint != nil) != tc.want {
					t.Fatal("baseline did not enforce exact current authority")
				}
			})
		}
	})
}

func portV2CreateBody(body string) string {
	mode := "local_only"
	if strings.Contains(body, `"new_advertised_port"`) {
		mode = "local_and_advertised"
	}
	return `{"protocol_version":2,"port_contract_version":2,"mode":"` + mode + `","expected_snapshot_id":"ps1:` + strings.Repeat("a", 64) + `","desired_revision":4,"fence":2,"required_capability":"host.port",` + strings.TrimPrefix(body, "{")
}

func portV2HTTPPlan(mode string, endpointRevision int64, localPort, advertisedPort, containerPort int) *store.SystemUpdatePortReconfiguration {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	b := contracts.SystemUpdatePortSnapshotRef{SnapshotID: "ps1:" + strings.Repeat("a", 64), SnapshotSHA256: digest("a"),
		SourcePolicyRevision: 11, ProjectionRevision: 4, ExecutorPolicyRevision: 23, ExecutorPolicySHA256: digest("b"),
		EndpointRevision: endpointRevision, AppliedEndpointRevision: endpointRevision, ConfigRevision: 3, ConfigSHA256: digest("c"),
		LocalListenPort: 8081, AdvertisedPort: 8081, AdvertisedEndpointSHA256: digest("d")}
	if advertisedPort == 0 {
		advertisedPort = b.AdvertisedPort
	}
	if containerPort != 0 {
		b.LocalListenPort = 18081
		b.Docker = &contracts.SystemUpdatePortDockerSnapshot{PublishedHostIP: "127.0.0.1", PublishedPort: 18081, ContainerPort: 8080, HealthPort: 18081,
			ComposePolicySHA256: digest("7"), ComposeRevision: 9, VersionEnvSHA256: digest("8"), ImageID: digest("9"), RepositoryDigest: digest("0")}
	}
	target, rollback := b, b
	changed := localPort != b.LocalListenPort || b.Docker != nil && containerPort != b.Docker.ContainerPort
	if changed {
		for i, next := range []*contracts.SystemUpdatePortSnapshotRef{&target, &rollback} {
			step := int64(i + 1)
			next.SourcePolicyRevision += step
			next.ProjectionRevision += step
			next.ExecutorPolicyRevision += step
			next.ConfigRevision += step
			if advertisedPort != b.AdvertisedPort {
				next.EndpointRevision += step
				next.AppliedEndpointRevision += step
			}
		}
		target.SnapshotSHA256, target.ExecutorPolicySHA256, target.ConfigSHA256 = digest("e"), digest("f"), digest("1")
		target.SnapshotID = "ps1:" + strings.Repeat("e", 64)
		target.LocalListenPort, target.AdvertisedPort = localPort, advertisedPort
		if advertisedPort != b.AdvertisedPort {
			target.AdvertisedEndpointSHA256 = digest("2")
		}
		rollback.SnapshotSHA256, rollback.ExecutorPolicySHA256, rollback.ConfigSHA256 = digest("3"), digest("4"), digest("5")
		rollback.SnapshotID = "ps1:" + strings.Repeat("3", 64)
		if b.Docker != nil {
			td, rd := *b.Docker, *b.Docker
			td.PublishedPort, td.ContainerPort, td.HealthPort = localPort, containerPort, localPort
			td.ComposeRevision++
			rd.ComposeRevision += 2
			target.Docker, rollback.Docker = &td, &rd
		}
	}
	plan := &store.SystemUpdatePortReconfiguration{PortContractVersion: 2, Mode: contracts.SystemUpdatePortMode(mode), NetworkNamespace: "host", Protocol: store.SystemUpdatePortProtocolTCP, Before: &b, Target: &target, Rollback: &rollback}
	if b.Docker != nil {
		plan.DockerBaseline = &contracts.SystemUpdatePortDockerBaseline{ExpectedContainerID: strings.Repeat("c", 64), ExpectedImageID: b.Docker.ImageID,
			ExpectedRepositoryDigest: b.Docker.RepositoryDigest, ExpectedVersionEnvSHA256: b.Docker.VersionEnvSHA256, ApprovedComposeConfigSHA256: strings.Repeat("7", 64), ApprovedComposeRevision: b.Docker.ComposeRevision}
	}
	plan.PortPlanSHA256, _ = contracts.ComputeSystemUpdatePortPlanSHA256(*systemUpdateV2PortPlan(plan))
	return plan
}

func TestSTPortHTTPVersionModesAndStableErrors(t *testing.T) {
	handler, cookie, csrf, capture := newPortCoordinatorHTTPFixture(t, nil)
	legacy := `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":7,"idempotency_key":"legacy"}`
	response := postSystemUpdateForTest(t, handler, cookie, csrf, legacy)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "system_update_port_contract_required") || capture.createCalls != 0 {
		t.Fatal("legacy request reached coordinator or lost contract error")
	}
	valid := portV2CreateBody(`{"operation":"port_reconfigure","target_id":"worker-a","new_local_listen_port":18081,"expected_endpoint_revision":7,"idempotency_key":"local-v2"}`)
	for _, invalid := range []string{
		strings.Replace(valid, `"local_only"`, `"advertised_only"`, 1),
		strings.Replace(valid, `"new_local_listen_port":18081`, `"new_local_listen_port":18081,"new_advertised_port":443`, 1),
		strings.Replace(valid, `"new_local_listen_port":18081`, `"new_local_listen_port":18081,"new_port":18081`, 1),
		strings.Replace(valid, `"new_local_listen_port":18081`, `"new_local_listen_port":18081,"root_policy":{}`, 1),
		strings.Replace(valid, `"new_local_listen_port":18081`, `"new_local_listen_port":18081,"new_local_listen_port":18082`, 1),
	} {
		response := postSystemUpdateForTest(t, handler, cookie, csrf, invalid)
		if response.Code != http.StatusBadRequest || capture.createCalls != 0 {
			t.Fatal("invalid v2 input reached coordinator")
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrSystemUpdateAdvertisedOnlyUnsupported, 400, "system_update_advertised_only_unsupported"},
		{store.ErrSystemUpdatePortSnapshotStale, 409, "system_update_port_snapshot_stale"},
		{store.ErrSystemUpdatePortPolicySnapshotUnavailable, 409, "system_update_port_policy_snapshot_unavailable"},
		{store.ErrSystemUpdatePortRecoveryRequired, 409, "system_update_port_recovery_required"},
		{store.ErrSystemUpdateExecutionHostBusy, 409, "system_update_host_busy"},
	} {
		capture.createErr = tc.err
		response := postSystemUpdateForTest(t, handler, cookie, csrf, valid)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) {
			t.Fatalf("stable code missing: %s", tc.code)
		}
	}
}

func TestSTPortHTTPPlanAndTypedResultProjection(t *testing.T) {
	for _, mode := range []string{"local_only", "local_and_advertised"} {
		advertised := 0
		if mode == "local_and_advertised" {
			advertised = 443
		}
		for _, container := range []int{0, 18080} {
			plan := portV2HTTPPlan(mode, 7, 18084, advertised, container)
			wire := systemUpdateV2PortPlan(plan)
			if err := contracts.ValidateSystemUpdatePortPlan(*wire); err != nil {
				t.Fatalf("fixture plan invalid (%s docker=%t)", mode, container != 0)
			}
			encoded, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			var decoded contracts.SystemUpdatePortReconfiguration
			if json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(decoded, *wire) {
				t.Fatal("plan fields were lost across store/API JSON")
			}
		}
	}
	for _, value := range []contracts.SystemUpdatePortReconfigurationResult{contracts.SystemUpdatePortReconfigurationApplied, contracts.SystemUpdatePortReconfigurationRolledBack, contracts.SystemUpdatePortReconfigurationUnchanged} {
		handler, token, capture, _ := newPortReportHTTPFixture(t)
		observed := time.Now().UTC()
		localPort := 18084
		if value == contracts.SystemUpdatePortReconfigurationUnchanged {
			localPort = 8081
		}
		plan := portV2HTTPPlan("local_only", 7, localPort, 0, 0)
		capture.reportJob.PortReconfigure = plan
		lease, err := handler.systemUpdateV2Lease(t.Context(), capture.reportJob)
		if err != nil {
			t.Fatal("typed job could not project its lease")
		}
		authorization := lease.Command.MutationAuthorization
		ref := plan.Target
		status := contracts.SystemUpdateSucceeded
		outcome := contracts.UpdaterOutcomeSucceeded
		if value == contracts.SystemUpdatePortReconfigurationRolledBack {
			ref, status = plan.Rollback, contracts.SystemUpdateRolledBack
			outcome = contracts.UpdaterOutcomeRolledBack
		}
		if value == contracts.SystemUpdatePortReconfigurationUnchanged {
			ref = plan.Before
		}
		result := contracts.UpdaterResultEnvelope{ProtocolVersion: 2, CommandID: lease.Command.CommandID, JobID: authorization.JobID,
			UpdaterID: authorization.UpdaterID, HostID: authorization.HostID, LeaseID: lease.LeaseID, LeaseGeneration: lease.LeaseGeneration,
			IdempotencyKey: lease.Command.IdempotencyKey, CanonicalPayloadDigest: lease.Command.CanonicalPayloadDigest, AuthorizationID: authorization.AuthorizationID,
			DesiredRevision: authorization.DesiredRevision, AppliedRevision: ref.ConfigRevision, Fence: authorization.Fence, Outcome: outcome,
			Status: status, AuditCorrelationID: lease.Command.AuditCorrelationID,
			Evidence: []contracts.UpdaterEvidence{{EvidenceCode: "application_probe_verified", ObservedAt: observed, ObservedRevision: ref.ConfigRevision}},
			PortReconfigure: &contracts.SystemUpdatePortResultV2{Result: value,
				ObservedSnapshotID: ref.SnapshotID, ObservedSnapshotSHA256: ref.SnapshotSHA256, ObservedConfigRevision: ref.ConfigRevision, ObservedConfigSHA256: ref.ConfigSHA256,
				ObservedExecutorPolicyRevision: ref.ExecutorPolicyRevision, ObservedExecutorPolicySHA256: ref.ExecutorPolicySHA256,
				Observation: contracts.SystemUpdatePortObservation{PolicyDiskVerified: true, PolicyMemoryVerified: true, AgentProjectionVerified: true, ListenerVerified: true, ObservedAt: observed}}}
		if value == contracts.SystemUpdatePortReconfigurationRolledBack {
			result.Evidence = append(result.Evidence, contracts.UpdaterEvidence{EvidenceCode: "rollback_verified", ObservedAt: observed, ObservedRevision: ref.ConfigRevision})
		}
		payload, _ := json.Marshal(result)
		var after contracts.UpdaterResultEnvelope
		if json.Unmarshal(payload, &after) != nil || !contracts.EqualSystemUpdatePortResults(*result.PortReconfigure, *after.PortReconfigure) {
			t.Fatal("typed result or first observation was lost")
		}
		mapped, err := mapSystemUpdateV2Report(capture.reportJob, lease, payload)
		if err != nil || mapped.PortReconfigure != nil || mapped.PortResult == nil || !contracts.EqualSystemUpdatePortResults(*mapped.PortResult, *result.PortReconfigure) {
			t.Fatal("v2 adapter lost the typed result")
		}
		response := postSystemUpdateV2JSON(t, handler, token.RawToken, "/services/update-jobs/port-job-1/report", result, http.StatusOK)
		if capture.report.PortResult == nil || !contracts.EqualSystemUpdatePortResults(*capture.report.PortResult, *result.PortReconfigure) {
			t.Fatal("HTTP discarded accepted result evidence")
		}
		var returned store.SystemUpdateJob
		if json.Unmarshal(response.Body.Bytes(), &returned) != nil || returned.PortResult == nil || returned.PortReconfigure.Result != "" ||
			!contracts.EqualSystemUpdatePortResults(*returned.PortResult, *result.PortReconfigure) {
			t.Fatal("public job did not keep separate immutable plan and result")
		}
	}
}
