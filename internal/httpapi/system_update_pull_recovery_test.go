package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type pullRecoveryHTTPFixture struct {
	auth      *store.MemoryAuthStore
	policies  *store.MemoryUpdaterPolicyStore
	updates   *store.MemorySystemUpdateStore
	handler   *Server
	token     store.ServiceToken
	policy    store.UpdaterPolicy
	ownership store.SystemUpdateExecutionHost
	job       store.SystemUpdateJob
	claim     store.SystemUpdateClaim
}

func newPullRecoveryHTTPFixture(t *testing.T) pullRecoveryHTTPFixture {
	t.Helper()
	ctx := t.Context()
	auth := store.NewMemoryAuthStore()
	workerToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-a", ServiceType: "worker", ServiceName: "Worker A",
		PublicURL: "https://worker.example.com", Version: "v1.0.0",
	})

	updates := store.NewMemorySystemUpdateStore()
	policies := store.NewMemoryUpdaterPolicyStore()
	policy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		"host-agent-a",
		0,
		0,
		store.UpdaterPolicy{
			TransportMode:             store.SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []store.UpdaterPolicyTarget{{
				TargetID: "worker-a", ServiceID: "worker-a", ServiceType: "worker", DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		store.SystemUpdateTransportPullV2,
		"host-agent-a",
		policy.ProjectionRevision,
	)
	if err != nil {
		t.Fatal(err)
	}

	// These reported values intentionally fail normal PolicyReady,
	// executorVerified and target-health checks. A fresh online heartbeat is
	// still required, but recovery must use the server-owned durable binding.
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           false,
		"update_executor":        false,
		"mutation_enabled":       false,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      "host-a",
		"ownership_epoch":        ownership.OwnershipEpoch,
		"policy_revision":        policy.ProjectionRevision,
		"policy_status":          "failed",
		"target_availability": map[string]any{
			"worker-a": "unavailable",
		},
		"target_availability_codes": map[string]any{
			"worker-a": "executor_probe_mismatch",
		},
	}
	token := registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		ownership.OwnershipEpoch,
		capabilities,
	)
	job, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
		TargetID: "worker-a", TargetServiceType: "worker",
		AgentServiceID: "host-agent-a", ExecutionHostID: "host-a", DeploymentMode: "systemd",
		CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "pull-interrupted-apply",
		RequestedByUserID: "admin", RequestedByUsername: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create interrupted pull job: created=%v err=%v", created, err)
	}
	claim, clear, err := updates.ClaimSystemUpdateJob(
		ctx,
		"host-agent-a",
		"host-a",
		"",
		map[string]string{"worker-a": "systemd"},
		time.Now().UTC(),
		time.Minute,
	)
	if err != nil || clear || claim.Job.ID != job.ID {
		t.Fatalf("initial interrupted pull claim = %#v clear=%v err=%v", claim, clear, err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	return pullRecoveryHTTPFixture{
		auth: auth, policies: policies, updates: updates, handler: handler,
		token: token, policy: policy, ownership: ownership, job: job, claim: claim,
	}
}

func TestPullSystemUpdateActiveRecoveryIgnoresTargetHealthButNewClaimsRemainStrict(t *testing.T) {
	fixture := newPullRecoveryHTTPFixture(t)
	agent, err := fixture.auth.GetService(t.Context(), "host-agent-a")
	if err != nil {
		t.Fatal(err)
	}
	active, clear, err := fixture.updates.InspectSystemUpdateActiveJob(
		t.Context(), agent.ServiceID, fixture.job.ID,
	)
	if err != nil || clear {
		t.Fatalf("inspect active pull job = %#v clear=%v err=%v", active, clear, err)
	}
	eligible, err := fixture.handler.systemUpdatePullRecoveryEligibleTarget(
		t.Context(), agent, "host-a", active,
	)
	if err != nil || len(eligible) != 1 || eligible["worker-a"] != "systemd" {
		t.Fatalf("active recovery targets = %#v err=%v", eligible, err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/claim",
		strings.NewReader(`{"service_id":"host-agent-a","active_job_id":"`+fixture.job.ID+`"}`),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unhealthy active recovery claim = %d %s", response.Code, response.Body.String())
	}
	var recovered systemUpdateClaimResponse
	if err := json.NewDecoder(response.Body).Decode(&recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Job.ID != fixture.job.ID ||
		recovered.Job.Status != store.SystemUpdateStatusReconciling ||
		!recovered.RecoveryRequired ||
		recovered.LeaseGeneration <= fixture.claim.LeaseGeneration {
		t.Fatalf("unhealthy active recovery response = %#v", recovered)
	}

	if _, applied, err := fixture.updates.ReportSystemUpdateJob(
		t.Context(),
		fixture.job.ID,
		store.SystemUpdateReport{
			AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
			LeaseToken: recovered.LeaseToken, LeaseGeneration: recovered.LeaseGeneration,
			Sequence: recovered.ReportSequence, Status: store.SystemUpdateStatusSucceeded, Progress: 100,
		},
		time.Now().UTC(),
		time.Minute,
	); err != nil || !applied {
		t.Fatalf("complete recovered pull job: applied=%v err=%v", applied, err)
	}
	queued, created, err := fixture.updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-a", TargetServiceType: "worker",
		AgentServiceID: "host-agent-a", ExecutionHostID: "host-a", DeploymentMode: "systemd",
		CurrentVersion: "v1.1.0", TargetVersion: "v1.2.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "pull-new-claim-stays-strict",
		RequestedByUserID: "admin", RequestedByUsername: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create strict new pull job: created=%v err=%v", created, err)
	}
	newRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/claim",
		strings.NewReader(`{"service_id":"host-agent-a"}`),
	)
	newRequest.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	newResponse := httptest.NewRecorder()
	fixture.handler.ServeHTTP(newResponse, newRequest)
	if newResponse.Code != http.StatusNoContent {
		t.Fatalf("unhealthy new pull claim = %d %s", newResponse.Code, newResponse.Body.String())
	}
	stillQueued, err := fixture.updates.GetActiveSystemUpdateJob(t.Context(), queued.TargetID)
	if err != nil || stillQueued.ID != queued.ID || stillQueued.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("strict new claim mutated queued job = %#v err=%v", stillQueued, err)
	}
}

func TestPullSystemUpdateActiveRecoveryRequiresExactDurableBinding(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*store.SystemUpdateJob)
		wantErr error
	}{
		{name: "foreign agent", mutate: func(job *store.SystemUpdateJob) { job.AgentServiceID = "foreign-agent" }, wantErr: store.ErrSystemUpdateOwnershipConflict},
		{name: "wrong host", mutate: func(job *store.SystemUpdateJob) { job.ExecutionHostID = "host-b" }, wantErr: store.ErrSystemUpdateOwnershipConflict},
		{name: "transport drift", mutate: func(job *store.SystemUpdateJob) { job.TransportMode = store.SystemUpdateTransportSSHV1 }, wantErr: store.ErrSystemUpdateOwnershipConflict},
		{name: "ownership epoch drift", mutate: func(job *store.SystemUpdateJob) { job.OwnershipEpoch++ }, wantErr: store.ErrSystemUpdateOwnershipConflict},
		{name: "policy revision drift", mutate: func(job *store.SystemUpdateJob) { job.PolicyRevision++ }, wantErr: store.ErrSystemUpdateOwnershipConflict},
		{name: "target drift", mutate: func(job *store.SystemUpdateJob) { job.TargetID = "worker-b" }, wantErr: store.ErrSystemUpdateActiveUnavailable},
		{name: "target type drift", mutate: func(job *store.SystemUpdateJob) { job.TargetServiceType = "observability" }, wantErr: store.ErrSystemUpdateActiveUnavailable},
		{name: "deployment target drift", mutate: func(job *store.SystemUpdateJob) { job.DeploymentMode = "docker" }, wantErr: store.ErrSystemUpdateActiveUnavailable},
		{name: "operation drift", mutate: func(job *store.SystemUpdateJob) { job.Operation = "attacker_operation" }, wantErr: store.ErrSystemUpdateActiveUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPullRecoveryHTTPFixture(t)
			agent, err := fixture.auth.GetService(t.Context(), "host-agent-a")
			if err != nil {
				t.Fatal(err)
			}
			job := fixture.claim.Job
			test.mutate(&job)
			eligible, err := fixture.handler.systemUpdatePullRecoveryEligibleTarget(
				t.Context(), agent, "host-a", job,
			)
			if !errors.Is(err, test.wantErr) || len(eligible) != 0 {
				t.Fatalf("drifted recovery targets = %#v err=%v, want %v", eligible, err, test.wantErr)
			}
		})
	}
}
