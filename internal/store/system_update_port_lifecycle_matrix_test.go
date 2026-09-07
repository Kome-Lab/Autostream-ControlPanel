package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

var stPortLifecycleCases = []string{"policy_save", "runtime_rotation", "ownership_deactivate", "software_update", "self_update"}

type stPortConcurrentOutcome struct {
	job     store.SystemUpdateJob
	id      string
	created bool
	err     error
}

func TestMariaDBSTPortV2LifecycleLockMatrix(t *testing.T) {
	for _, operation := range stPortLifecycleCases {
		for _, portFirst := range []bool{true, false} {
			order := "port_first"
			if !portFirst {
				order = "lifecycle_first"
			}
			t.Run(operation+"/"+order, func(t *testing.T) {
				f := newMariaDBSTPortV2Fixture(t)
				f.prepareLifecycleReadiness(t)
				before := f.databaseState(t, "")
				snapshot := f.snapshot(t)
				port := f.portCreateOperation(snapshot, "matrix-port")
				other := f.lifecycleOperation(t, operation)
				var winner, loser stPortConcurrentOutcome
				if portFirst {
					winner, loser = f.runAtLockBoundary(t, port, other, true)
				} else {
					winner, loser = f.runAtLockBoundary(t, other, port, false)
				}
				if winner.err != nil || !winner.created {
					t.Fatalf("first operation failed: %v", winner.err)
				}
				expected := error(store.ErrSystemUpdateExecutionHostBusy)
				if !portFirst && operation == "policy_save" {
					expected = store.ErrSystemUpdateAgentNotReady
				}
				if !portFirst && operation == "ownership_deactivate" {
					expected = store.ErrSystemUpdateOwnershipConflict
				}
				if loser.created || !errors.Is(loser.err, expected) {
					t.Fatalf("fixed loser classification: created=%v error=%v expected=%v", loser.created, loser.err, expected)
				}
				if portFirst {
					f.assertPortCreateWinner(t, before, snapshot, winner.job)
					f.assertLaneCounts(t, 1, 1, 0, 0)
				} else {
					f.assertLifecycleWinner(t, operation, before, winner)
				}
			})
		}
	}
}

func TestMariaDBSTPortV2RecoveryHoldExcludesLifecycle(t *testing.T) {
	for _, operation := range stPortLifecycleCases {
		t.Run(operation, func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			f.prepareLifecycleReadiness(t)
			other := f.lifecycleOperation(t, operation)
			job := f.claim(t, f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18084, 8443, "hold"), "").Job
			f.report(t, job, 1, store.SystemUpdateStatusInstalling, nil)
			f.consume(t, job, store.SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
			failed := mariaDBSTPortV2Result(job, contracts.SystemUpdatePortReconfigurationRollbackFailed)
			job = f.report(t, job, 2, store.SystemUpdateStatusFailed, failed)
			before := f.databaseState(t, job.ID)
			// Even a terminal failed status retains the same host lane until verified R.
			result := other(f.ctx)
			if result.created || !errors.Is(result.err, store.ErrSystemUpdateExecutionHostBusy) {
				t.Fatalf("recovery hold classification: %v", result.err)
			}
			if !reflect.DeepEqual(before, f.databaseState(t, job.ID)) {
				t.Fatal("blocked lifecycle changed the recovery hold or canonical source")
			}
			recovery := f.claim(t, job, job.ID)
			token, binding := f.issuePortGrant(t, recovery.Job, store.SystemUpdateMutationOperationPortReconfigureReconcile, "session-2234567890123456")
			consume := func(ctx context.Context) stPortConcurrentOutcome {
				_, replay, err := f.updates.ConsumeSystemUpdateMutationGrant(ctx, job.ID, token, recovery.Job.LeaseGeneration, binding, time.Now().UTC())
				return stPortConcurrentOutcome{created: !replay && err == nil, err: err}
			}
			winner, blocked := f.runAtLockBoundary(t, consume, other, true)
			if winner.err != nil || !winner.created || !errors.Is(blocked.err, store.ErrSystemUpdateExecutionHostBusy) || blocked.created {
				t.Fatalf("same-job recovery consume did not retain exclusion: winner=%v blocked=%v", winner.err, blocked.err)
			}
			proof := mariaDBSTPortV2Result(recovery.Job, contracts.SystemUpdatePortReconfigurationRolledBack)
			f.report(t, recovery.Job, 1, store.SystemUpdateStatusRolledBack, proof)
			f.assertC11CommittedState(t, job, proof, store.SystemUpdateStatusRolledBack)
			f.assertLaneCounts(t, 1, 1, 0, 0)
		})
	}
}

func TestMariaDBSTPortV2ConsumeCancelLockMatrix(t *testing.T) {
	for _, consumeFirst := range []bool{true, false} {
		name := "consume_first"
		if !consumeFirst {
			name = "cancel_first"
		}
		t.Run(name, func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			job := f.claim(t, f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18084, 8443, "consume-cancel"), "").Job
			f.report(t, job, 1, store.SystemUpdateStatusInstalling, nil)
			token, binding := f.issuePortGrant(t, job, store.SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
			before := f.databaseState(t, job.ID)
			consume := func(ctx context.Context) stPortConcurrentOutcome {
				_, replay, err := f.updates.ConsumeSystemUpdateMutationGrant(ctx, job.ID, token, job.LeaseGeneration, binding, time.Now().UTC())
				return stPortConcurrentOutcome{created: !replay && err == nil, err: err}
			}
			cancel := func(ctx context.Context) stPortConcurrentOutcome {
				_, err := f.updates.CancelSystemUpdateJob(ctx, job.ID, "admin")
				return stPortConcurrentOutcome{created: err == nil, err: err}
			}
			var consumed, cancelled stPortConcurrentOutcome
			if consumeFirst {
				consumed, cancelled = f.runAtLockBoundary(t, consume, cancel, true)
			} else {
				cancelled, consumed = f.runAtLockBoundary(t, cancel, consume, false)
			}
			if consumed.err != nil || !consumed.created || cancelled.created || !errors.Is(cancelled.err, store.ErrSystemUpdateNotCancellable) {
				t.Fatalf("claimed cancellation contract changed: consume=%v cancel=%v", consumed.err, cancelled.err)
			}
			f.assertCanonicalConsumed(t, job)
			if _, replay, err := f.updates.ConsumeSystemUpdateMutationGrant(f.ctx, job.ID, token, job.LeaseGeneration, binding, time.Now().UTC()); err != nil || !replay {
				t.Fatalf("consume exact replay: %v", err)
			}
			after := f.databaseState(t, job.ID)
			if before.Job.Status != after.Job.Status || before.Job.Sequence != after.Job.Sequence || !reflect.DeepEqual(before.Services, after.Services) || !reflect.DeepEqual(before.Reservations, after.Reservations) {
				t.Fatal("consume/cancel changed applied state or job progress before result")
			}
			f.assertLaneCounts(t, 1, 1, 0, 0)
		})
	}
}

func TestMariaDBSTPortV2DifferentHostRemainsAvailable(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	other := newMariaDBSTPortV2Fixture(t)
	snapshot, otherSnapshot := f.snapshot(t), other.snapshot(t)
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	heldDiagnostics, independentDiagnostics := store.WithSystemUpdatePortCreateGapDiagnosticsForTest(t, ctx)
	held, release := make(chan struct{}), make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	blocked := store.WithSystemUpdatePortLocksHeldForTest(heldDiagnostics, func() {
		heldOnce.Do(func() {
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	done := make(chan stPortConcurrentOutcome, 1)
	go func() { done <- f.portCreateOperation(snapshot, "host-a")(blocked) }()
	select {
	case <-held:
	case result := <-done:
		t.Fatalf("first host did not reach source locks: %v", result.err)
	case <-ctx.Done():
		t.Fatal("bounded host barrier timed out")
	}
	store.AssertSystemUpdatePortSourceLocksHeldForTest(t, ctx, f.db, f.policy.ExecutionHostID, f.policy.UpdaterID, f.sourceServices(t))
	if f.policy.ExecutionHostID == other.policy.ExecutionHostID || f.policy.UpdaterID == other.policy.UpdaterID {
		t.Fatal("different-host fixture reused host or Agent identity")
	}
	store.LogSystemUpdatePortHostLanePlansForTest(t, ctx, other.db, other.policy.ExecutionHostID)
	started := time.Now()
	phaseCount := 0
	observed := store.WithSystemUpdatePortCreatePhaseForTest(independentDiagnostics, func(phase string) {
		phaseCount++
		if phaseCount <= 20 {
			t.Logf("unrelated host create phase=%s elapsed_ms=%d", phase, time.Since(started).Milliseconds())
		}
	})
	otherDone := make(chan stPortConcurrentOutcome, 1)
	go func() { otherDone <- other.portCreateOperation(otherSnapshot, "host-b")(observed) }()
	var result stPortConcurrentOutcome
	diagnosticTimer := time.NewTimer(2 * time.Second)
	defer diagnosticTimer.Stop()
	select {
	case result = <-otherDone:
	case <-diagnosticTimer.C:
		store.LogSystemUpdatePortHostLaneWaitForTest(t, ctx, other.db)
		result = <-otherDone
	}
	if result.err != nil || !result.created {
		t.Fatalf("unrelated host was blocked: %v", result.err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case result = <-done:
		if result.err != nil || !result.created {
			t.Fatalf("held host failed: %v", result.err)
		}
	case <-ctx.Done():
		t.Fatal("held host did not finish")
	}
	f.assertLaneCounts(t, 1, 1, 0, 0)
	other.assertLaneCounts(t, 1, 1, 0, 0)
}

func (f *mariaDBSTPortV2Fixture) runAtLockBoundary(t *testing.T, first, second func(context.Context) stPortConcurrentOutcome, fullSource bool) (stPortConcurrentOutcome, stPortConcurrentOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	held, release := make(chan struct{}), make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	pause := func() {
		heldOnce.Do(func() {
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}
	observed := store.WithSystemUpdateLifecycleHostLockForTest(ctx, pause)
	if fullSource {
		observed = store.WithSystemUpdatePortLocksHeldForTest(ctx, pause)
	}
	firstDone, secondDone := make(chan stPortConcurrentOutcome, 1), make(chan stPortConcurrentOutcome, 1)
	go func() { firstDone <- first(observed) }()
	select {
	case <-held:
	case result := <-firstDone:
		t.Fatalf("operation ended before actual lock phase: %v", result.err)
	case <-ctx.Done():
		t.Fatal("bounded actual-lock barrier timed out")
	}
	if fullSource {
		store.AssertSystemUpdatePortSourceLocksHeldForTest(t, ctx, f.db, f.policy.ExecutionHostID, f.policy.UpdaterID, f.sourceServices(t))
	} else {
		store.AssertSystemUpdateHostRowLockForTest(t, ctx, f.db, f.policy.ExecutionHostID)
	}
	go func() { secondDone <- second(ctx) }()
	releaseOnce.Do(func() { close(release) })
	var left, right stPortConcurrentOutcome
	select {
	case left = <-firstDone:
	case <-ctx.Done():
		t.Fatal("first operation exceeded lock budget")
	}
	select {
	case right = <-secondDone:
	case <-ctx.Done():
		t.Fatal("second operation exceeded lock budget")
	}
	return left, right
}

func (f *mariaDBSTPortV2Fixture) portCreateOperation(before store.SystemUpdatePortPolicySnapshot, key string) func(context.Context) stPortConcurrentOutcome {
	params := store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalAndAdvertised, TargetID: f.targetID, NewLocalListenPort: 18084, NewAdvertisedPort: 8443, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: before.Ref.EndpointRevision, ExpectedDesiredRevision: before.Ref.ConfigRevision + 1, ExpectedFence: 1, IdempotencyKey: key + "-" + f.suffix, RequestedByUserID: "admin", BuildPolicySnapshot: mariaDBSTPortV2Builder}
	return func(ctx context.Context) stPortConcurrentOutcome {
		job, created, err := f.updates.CreateSystemdPortReconfigurationJob(ctx, f.auth, f.policies, params)
		return stPortConcurrentOutcome{job: job, id: job.ID, created: created, err: err}
	}
}

func (f *mariaDBSTPortV2Fixture) prepareLifecycleReadiness(t *testing.T) {
	t.Helper()
	agent, err := f.auth.GetService(f.ctx, f.policy.UpdaterID)
	if err != nil {
		t.Fatal(err)
	}
	caps := agent.ReportedCapabilities
	caps["self_update_ready"], caps["self_update_phase"], caps["recovery_pending"] = true, "stable", false
	caps["self_update_active_agent_version"], caps["self_update_active_executor_version"], caps["executor_version"] = "v1.7.8", "v1.7.8", "v1.7.8"
	caps["agent_protocol_version"], caps["executor_protocol_version"], caps["mutation_protocol_version"], caps["recovery_protocol_version"] = 2, 2, 2, store.SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion
	if _, err = f.auth.Heartbeat(f.ctx, store.ServiceToken{ID: agent.TokenID, ServiceType: "update_agent"}, store.ServiceHeartbeat{ServiceID: agent.ServiceID, Status: "online", Version: "v1.7.8", OS: "linux", Arch: "amd64", Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	userID := "self-update-user-" + f.suffix
	f.exec(t, `INSERT INTO users (id,username,password_hash,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)`, userID, userID, "not-used-by-this-test", time.Now().UTC(), time.Now().UTC())
}

func (f *mariaDBSTPortV2Fixture) lifecycleOperation(t *testing.T, name string) func(context.Context) stPortConcurrentOutcome {
	t.Helper()
	switch name {
	case "policy_save":
		input := f.policy
		input.PollIntervalSeconds++
		return func(ctx context.Context) stPortConcurrentOutcome {
			_, err := f.policies.SavePullUpdaterPolicy(ctx, f.updates, input.UpdaterID, 11, 1, input)
			return stPortConcurrentOutcome{created: err == nil, err: err}
		}
	case "runtime_rotation":
		host, err := f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
		if err != nil {
			t.Fatal(err)
		}
		params, seal, _ := mariaDBRuntimeTokenRotationStageParams(t, f.mariaDBPullActivationFixture, store.ActivatePullUpdaterOwnershipResult{Policy: f.policy, Ownership: host}, "matrix-rotation-"+f.suffix)
		return func(ctx context.Context) stPortConcurrentOutcome {
			result, err := f.updates.StageSystemUpdateRuntimeTokenRotation(ctx, f.auth, f.policies, params, seal)
			return stPortConcurrentOutcome{id: result.Rotation.ID, created: result.Created, err: err}
		}
	case "ownership_deactivate":
		params := store.DeactivatePullUpdaterOwnershipParams{ServiceID: f.policy.UpdaterID, ExecutionHostID: f.policy.ExecutionHostID, ExpectedExecutionHostOwnershipEpoch: 1, ExpectedSourcePolicyRevision: 11, ExpectedProjectionRevision: 17, ExpectedLocalExecutorPolicyRevision: 23, ExpectedLocalExecutorPolicySHA256: f.policy.LocalExecutorPolicySHA256}
		return func(ctx context.Context) stPortConcurrentOutcome {
			_, err := f.policies.DeactivatePullUpdaterOwnership(ctx, f.auth, f.updates, params)
			return stPortConcurrentOutcome{created: err == nil, err: err}
		}
	case "software_update":
		params := store.CreateSystemUpdateJobParams{TargetID: f.targetID, TargetServiceType: "worker", AgentServiceID: f.policy.UpdaterID, ExecutionHostID: f.policy.ExecutionHostID, DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.0.1", Strategy: store.SystemUpdateStrategyMaintenance, IdempotencyKey: "matrix-software-" + f.suffix, RequestedByUserID: "admin"}
		return func(ctx context.Context) stPortConcurrentOutcome {
			job, created, err := f.updates.CreateSystemUpdateJob(ctx, params)
			return stPortConcurrentOutcome{job: job, id: job.ID, created: created, err: err}
		}
	case "self_update":
		userID := "self-update-user-" + f.suffix
		params := store.CreateSystemUpdateHostSelfUpdateParams{ExecutionHostID: f.policy.ExecutionHostID, TargetVersion: "v1.8.0", IdempotencyKey: "matrix-self-" + f.suffix, RequestedByUserID: userID, RequestedByUsername: userID, Release: mariaDBHostSelfUpdateRelease(time.Now().UTC()), Now: time.Now().UTC()}
		return func(ctx context.Context) stPortConcurrentOutcome {
			value, created, err := f.updates.CreateSystemUpdateHostSelfUpdate(ctx, f.auth, f.policies, params)
			return stPortConcurrentOutcome{id: value.ID, created: created, err: err}
		}
	default:
		t.Fatalf("unknown fixed lifecycle case %s", name)
		return nil
	}
}

func (f *mariaDBSTPortV2Fixture) assertLaneCounts(t *testing.T, jobs, ports, rotations, selfUpdates int) {
	t.Helper()
	for _, check := range []struct {
		table    string
		expected int
	}{{"system_update_jobs", jobs}, {"system_update_port_transactions", ports}, {"system_update_runtime_token_rotations", rotations}, {"system_update_host_self_updates", selfUpdates}} {
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM `+check.table+` WHERE execution_host_id=?`, f.policy.ExecutionHostID).Scan(&count); err != nil || count != check.expected {
			t.Fatalf("%s count=%d expected=%d", check.table, count, check.expected)
		}
	}
}

func (f *mariaDBSTPortV2Fixture) assertPortCreateWinner(t *testing.T, before stPortDatabaseState, snapshot store.SystemUpdatePortPolicySnapshot, job store.SystemUpdateJob) {
	t.Helper()
	after := f.databaseState(t, job.ID)
	if string(before.Policy) != string(after.Policy) || !reflect.DeepEqual(before.Host, after.Host) || !reflect.DeepEqual(before.Bindings, after.Bindings) {
		t.Fatal("queued port create changed canonical source/host/bindings")
	}
	if job.PolicyRevision != 17 || job.OwnershipEpoch != 1 || job.PortReconfigure.Before.SourcePolicyRevision != 11 || job.PortReconfigure.Target.ProjectionRevision != 18 || job.PortReconfigure.Rollback.ExecutorPolicyRevision != 25 {
		t.Fatal("queued job mixed independent counters")
	}
	var persisted store.SystemUpdatePortPolicySnapshot
	var body []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT before_json FROM system_update_port_transactions WHERE job_id=?`, job.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(body, &persisted) != nil || !reflect.DeepEqual(persisted, snapshot) {
		t.Fatal("queued job changed full frozen B")
	}
	for i, source := range before.Services {
		current := after.Services[i]
		if source.ID == f.targetID {
			expected := source
			expected.E = 4
			expected.Status = "pending"
			expected.Desired = &store.ServiceEndpoint{Host: source.Desired.Host, Port: 8443, SSLEnabled: source.Desired.SSLEnabled, PublicURL: "https://worker.example.test:8443/api?tenant=a"}
			expected.UpdatedAt = current.UpdatedAt
			if !reflect.DeepEqual(expected, current) {
				t.Fatal("queued selected service changed beyond expected desired endpoint")
			}
		} else if !reflect.DeepEqual(source, current) {
			t.Fatal("queued port create changed another service or token reference")
		}
	}
	if len(after.Reservations) != len(before.Reservations)+1 {
		t.Fatal("queued create did not retain B and pending T reservations")
	}
	for _, expected := range before.Reservations {
		found := false
		for _, actual := range after.Reservations {
			if reflect.DeepEqual(expected, actual) {
				found = true
			}
		}
		if !found {
			t.Fatal("queued create changed existing reservation")
		}
	}
}

func (f *mariaDBSTPortV2Fixture) assertLifecycleWinner(t *testing.T, name string, before stPortDatabaseState, result stPortConcurrentOutcome) {
	t.Helper()
	after := f.databaseState(t, "")
	jobs, rotations, selfUpdates := 0, 0, 0
	switch name {
	case "software_update":
		jobs = 1
	case "runtime_rotation":
		rotations = 1
	case "self_update":
		selfUpdates = 1
	}
	f.assertLaneCounts(t, jobs, 0, rotations, selfUpdates)
	if !reflect.DeepEqual(before.Reservations, after.Reservations) {
		t.Fatal("non-port winner changed port reservations")
	}
	if name == "policy_save" {
		current, err := f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
		if err != nil {
			t.Fatal(err)
		}
		expected := f.policy
		expected.Revision = 12
		expected.ProjectionRevision = 18
		expected.LocalExecutorPolicyRevision = 24
		expected.PollIntervalSeconds++
		expected.UpdatedAt = current.UpdatedAt
		if !reflect.DeepEqual(current, expected) || after.Host.PolicyRevision != 18 || after.Host.OwnershipEpoch != 1 {
			t.Fatal("policy save winner mixed source/projection/executor counters")
		}
		if !reflect.DeepEqual(before.Services, after.Services) {
			t.Fatal("policy save changed service/token state")
		}
		var stale int
		for _, table := range []string{"update_agent_target_databases", "update_agent_target_local_listeners"} {
			if err = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM `+table+` WHERE updater_service_id=? AND binding_policy_revision<>12`, f.policy.UpdaterID).Scan(&stale); err != nil || stale != 0 {
				t.Fatal("policy save left stale original binding row")
			}
		}
		return
	}
	if string(before.Policy) != string(after.Policy) || !reflect.DeepEqual(before.Bindings, after.Bindings) {
		t.Fatal("non-policy lifecycle changed full source policy or bindings")
	}
	if name == "ownership_deactivate" {
		if after.Host.OwnershipEpoch != 2 || after.Host.PolicyRevision != 17 || after.Host.AgentServiceID != before.Host.AgentServiceID {
			t.Fatal("deactivation winner changed unexpected host identity")
		}
		agent, err := f.auth.GetService(f.ctx, f.policy.UpdaterID)
		if err != nil || agent.OwnershipEpoch != 0 {
			t.Fatal("deactivation failed to revoke active ownership")
		}
		for i, service := range before.Services {
			current := after.Services[i]
			if service.ID == f.policy.UpdaterID {
				service.UpdatedAt = current.UpdatedAt
			}
			if !reflect.DeepEqual(service, current) {
				t.Fatal("deactivation changed config/endpoint/credential reference")
			}
		}
		return
	}
	if !reflect.DeepEqual(before.Host, after.Host) || !reflect.DeepEqual(before.Services, after.Services) {
		t.Fatal("lane creation changed source configuration or ownership")
	}
	if name == "software_update" && (result.job.PolicyRevision != 17 || result.job.OwnershipEpoch != 1) {
		t.Fatal("software winner mixed host projection and source revision")
	}
	if name == "self_update" {
		value, err := f.updates.GetSystemUpdateHostSelfUpdate(f.ctx, result.id)
		if err != nil || value.ExpectedSourcePolicyRevision != 11 || value.ExpectedProjectionRevision != 17 || value.ExpectedLocalExecutorPolicyRevision != 23 {
			t.Fatal("self-update winner mixed independent counters")
		}
		issued, err := f.updates.IssueSystemUpdateHostSelfUpdateGrant(f.ctx, f.auth, f.policies, store.IssueSystemUpdateHostSelfUpdateGrantParams{SelfUpdateID: value.ID, ExecutionHostID: value.ExecutionHostID, AgentServiceID: value.AgentServiceID, ExpectedRevision: value.Revision, Operation: store.SystemUpdateHostSelfUpdateGrantStage, PlanSHA256: strings.Repeat("d", 64), SessionID: "independent-counters", Now: time.Now().UTC(), TTL: time.Minute})
		if err != nil || !issued.Issued || issued.RawToken == "" || issued.Grant.ExpectedSourcePolicyRevision != 11 || issued.Grant.ExpectedProjectionRevision != 17 {
			t.Fatalf("real MariaDB self-update grant rejected independent counters: %v", err)
		}
	}
	if name == "runtime_rotation" {
		value, err := f.updates.GetSystemUpdateRuntimeTokenRotation(f.ctx, result.id)
		if err != nil || value.ExpectedOwnershipEpoch != 1 || value.ExpectedSourcePolicyRevision != 11 || value.ExpectedProjectionRevision != 17 || value.ExpectedLocalExecutorPolicyRevision != 23 || value.Status != store.SystemUpdateRuntimeTokenRotationStaged || value.StagedTokenID == "" || value.PreviousTokenID == value.StagedTokenID {
			t.Fatal("rotation winner mixed credentials or independent counters")
		}
		for _, service := range before.Services {
			if service.ID == f.policy.UpdaterID && value.PreviousTokenID != service.TokenID {
				t.Fatal("rotation captured a different active credential reference")
			}
		}
		var stagedRevoked, previousRevoked bool
		if err = f.db.QueryRowContext(f.ctx, `SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id=?`, value.StagedTokenID).Scan(&stagedRevoked); err != nil || !stagedRevoked {
			t.Fatal("staged rotation credential became prematurely active")
		}
		if err = f.db.QueryRowContext(f.ctx, `SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id=?`, value.PreviousTokenID).Scan(&previousRevoked); err != nil || previousRevoked {
			t.Fatal("rotation revoked the current credential before activation")
		}
	}
}

func (f *mariaDBSTPortV2Fixture) issuePortGrant(t *testing.T, job store.SystemUpdateJob, operation, session string) (string, store.SystemUpdateMutationGrantBinding) {
	t.Helper()
	runtime, err := store.ComputeSystemUpdatePortRuntimePlanSHA256(job, session)
	if err != nil {
		t.Fatal(err)
	}
	binding := store.SystemUpdateMutationGrantBinding{HostID: job.ExecutionHostID, TransportMode: job.TransportMode, OwnershipEpoch: job.OwnershipEpoch, PolicyRevision: job.PolicyRevision, TargetID: job.TargetID, TargetServiceType: job.TargetServiceType, TargetVersion: job.TargetVersion, DeploymentMode: job.DeploymentMode, JobOperation: job.Operation, Operation: operation, PlanSHA256: runtime, SessionID: session, PortReconfigure: job.PortReconfigure}
	issued, err := f.updates.IssueSystemUpdateMutationGrant(f.ctx, job.ID, store.IssueSystemUpdateMutationGrantParams{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, Binding: binding}, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return issued.GrantToken, binding
}

func (f *mariaDBSTPortV2Fixture) assertCanonicalConsumed(t *testing.T, job store.SystemUpdateJob) {
	t.Helper()
	policy, err := f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
	if err != nil {
		t.Fatal(err)
	}
	target := job.PortReconfigure.Target
	if policy.Revision != target.SourcePolicyRevision || policy.ProjectionRevision != target.ProjectionRevision || policy.LocalExecutorPolicyRevision != target.ExecutorPolicyRevision || policy.LocalExecutorPolicySHA256 != target.ExecutorPolicySHA256 {
		t.Fatal("consume did not commit exact canonical T")
	}
	host, err := f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
	if err != nil || host.PolicyRevision != target.ProjectionRevision || host.OwnershipEpoch != job.OwnershipEpoch {
		t.Fatal("consume host projection/fence differs")
	}
	var phase string
	var accepted any
	var hold bool
	if err = f.db.QueryRowContext(f.ctx, `SELECT phase,accepted_result_json,recovery_required FROM system_update_port_transactions WHERE job_id=?`, job.ID).Scan(&phase, &accepted, &hold); err != nil || phase != "consumed" || accepted != nil || hold {
		t.Fatal("consume prematurely accepted a runtime observation")
	}
	for _, targetState := range policy.Targets {
		if targetState.ServiceID == job.TargetID && targetState.LocalListenPort != 18084 {
			t.Fatal("selected consumed listener mismatch")
		}
	}
}
