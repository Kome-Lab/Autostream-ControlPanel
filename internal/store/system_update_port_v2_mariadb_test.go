package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"github.com/go-sql-driver/mysql"
)

// These integration cases run only with the existing opt-in MariaDB fixture.
// They exercise the production coordinator, grant, restart reader and report
// transaction; no alternate in-test persistence implementation is involved.
func TestMariaDBSTPortV2CompleteSnapshotBindingsAndBaseline(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	before := f.snapshot(t)
	if before.Ref.SourcePolicyRevision != 11 || before.Ref.ProjectionRevision != 17 || before.Ref.ExecutorPolicyRevision != 23 || len(before.Snapshot.Bindings) != 2 || len(before.Snapshot.Targets) != 2 {
		t.Fatal("complete snapshot omitted independent revisions or a target")
	}
	var jsonOnly store.UpdaterPolicy
	if err := json.Unmarshal(before.Snapshot.Policy, &jsonOnly); err != nil {
		t.Fatal(err)
	}
	for _, target := range jsonOnly.Targets {
		if target.LocalListenPort != 0 || target.DatabaseName != "" {
			t.Fatal("fixture no longer exercises separately persisted bindings")
		}
	}
	for _, binding := range before.Snapshot.Bindings {
		if binding.BindingPolicyRevision != 11 || binding.LocalListenPort == nil {
			t.Fatal("listener binding is incomplete")
		}
		if binding.ServiceID == f.observabilityID && (binding.DatabaseName == nil || *binding.DatabaseName != "autostream_observability") {
			t.Fatal("database binding lost")
		}
	}
	f.updates = store.NewMariaDBSystemUpdateStore(f.db)
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("new store instance changed full snapshot")
	}
	for _, table := range []string{"update_agent_target_databases", "update_agent_target_local_listeners"} {
		t.Run(table+"_independent_revision", func(t *testing.T) {
			// Both identifiers are fixed literals above, never user input.
			f.exec(t, "UPDATE "+table+" SET binding_policy_revision=10 WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, f.observabilityID)
			_, err := f.updates.GetSystemUpdatePortPolicySnapshot(f.ctx, f.auth, f.policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
			if !errors.Is(err, store.ErrSystemUpdatePortPolicySnapshotUnavailable) {
				t.Fatalf("stale original binding row accepted: %v", err)
			}
			f.exec(t, "UPDATE "+table+" SET binding_policy_revision=11 WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, f.observabilityID)
		})
	}
	for _, binding := range []struct {
		table, column string
		value         any
	}{
		{"update_agent_target_databases", "database_name", "autostream_observability"},
		{"update_agent_target_local_listeners", "local_listen_port", 18082},
	} {
		// Identifiers are fixed schema literals. Mutations touch this fixture's
		// exact updater/target key and preserve all production constraints.
		insert := "INSERT INTO " + binding.table + " (updater_service_id,target_id,binding_policy_revision," + binding.column + ",updated_at) VALUES (?,?,11,?,?)"
		t.Run(binding.table+"_missing", func(t *testing.T) {
			var updated time.Time
			if err := f.db.QueryRowContext(f.ctx, "SELECT updated_at FROM "+binding.table+" WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, f.observabilityID).Scan(&updated); err != nil {
				t.Fatal(err)
			}
			f.exec(t, "DELETE FROM "+binding.table+" WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, f.observabilityID)
			defer f.exec(t, insert, f.policy.UpdaterID, f.observabilityID, binding.value, updated)
			f.assertSnapshotRejected(t, store.ErrSystemUpdatePortPolicySnapshotUnavailable)
			var count int
			if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM "+binding.table+" WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, f.observabilityID).Scan(&count); err != nil || count != 0 {
				t.Fatal("GET repaired the missing binding")
			}
		})
		t.Run(binding.table+"_orphan", func(t *testing.T) {
			orphan := "orphan-port-" + f.suffix
			f.exec(t, insert, f.policy.UpdaterID, orphan, binding.value, time.Now().UTC())
			defer f.exec(t, "DELETE FROM "+binding.table+" WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, orphan)
			f.assertSnapshotRejected(t, store.ErrSystemUpdatePortPolicySnapshotUnavailable)
			var count int
			if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM "+binding.table+" WHERE updater_service_id=? AND target_id=?", f.policy.UpdaterID, orphan).Scan(&count); err != nil || count != 1 {
				t.Fatal("GET removed an orphan instead of rejecting it")
			}
		})
		t.Run(binding.table+"_duplicate_primary_key", func(t *testing.T) {
			// Physical duplicates are unrepresentable because migration 059/060
			// has PRIMARY KEY(updater_service_id,target_id). Verify that boundary.
			_, err := f.db.ExecContext(f.ctx, insert, f.policy.UpdaterID, f.observabilityID, binding.value, time.Now().UTC())
			var duplicate *mysql.MySQLError
			if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
				t.Fatalf("duplicate binding was not rejected by its primary key: %v", err)
			}
			if !reflect.DeepEqual(before, f.snapshot(t)) {
				t.Fatal("rejected duplicate changed complete snapshot")
			}
		})
	}
	for _, mutation := range []string{"foreign_host", "duplicate_target"} {
		t.Run("policy_"+mutation, func(t *testing.T) {
			var original []byte
			if err := f.db.QueryRowContext(f.ctx, `SELECT policy_json FROM update_agent_policies WHERE service_id=?`, f.policy.UpdaterID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			var invalid store.UpdaterPolicy
			if err := json.Unmarshal(original, &invalid); err != nil {
				t.Fatal(err)
			}
			if mutation == "foreign_host" {
				invalid.Targets[1].HostID = "foreign-host-" + f.suffix
			} else {
				invalid.Targets = append(invalid.Targets, invalid.Targets[1])
			}
			body, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `UPDATE update_agent_policies SET policy_json=? WHERE service_id=?`, body, f.policy.UpdaterID)
			defer f.exec(t, `UPDATE update_agent_policies SET policy_json=? WHERE service_id=?`, original, f.policy.UpdaterID)
			// Existing strict stored-policy parsing rejects these before binding
			// restoration. Do not rewrite its established error classification.
			f.assertSnapshotRejected(t, store.ErrInvalidSettings)
			var unchanged []byte
			if err := f.db.QueryRowContext(f.ctx, `SELECT policy_json FROM update_agent_policies WHERE service_id=?`, f.policy.UpdaterID).Scan(&unchanged); err != nil || string(unchanged) != string(body) {
				t.Fatal("GET rewrote the invalid policy source")
			}
		})
	}
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("negative fixture restoration changed complete source")
	}
	f.exec(t, `UPDATE services SET applied_endpoint_revision=NULL WHERE service_id=?`, f.targetID)
	if _, err := f.updates.GetSystemUpdatePortPolicySnapshot(f.ctx, f.auth, f.policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder}); err == nil {
		t.Fatal("unknown AE was inferred during GET")
	}
	var unknown sql.NullInt64
	if err := f.db.QueryRowContext(f.ctx, `SELECT applied_endpoint_revision FROM services WHERE service_id=?`, f.targetID).Scan(&unknown); err != nil || unknown.Valid {
		t.Fatal("read mutated unknown AE")
	}
	params := store.ConfirmSystemUpdatePortPolicyBaselineParams{Baseline: &f.baseline, AgentServiceID: f.policy.UpdaterID, ExpectedSourcePolicyRevision: 11, ExpectedProjectionRevision: 17, ExpectedExecutorPolicyRevision: 23, ExpectedExecutorPolicySHA256: f.policy.LocalExecutorPolicySHA256, AppliedEndpointRevisions: map[string]int64{f.targetID: 3, f.observabilityID: 3}, BuildPolicySnapshot: mariaDBSTPortV2Builder}
	if err := f.updates.ConfirmSystemUpdatePortPolicyBaseline(f.ctx, f.auth, f.policies, params); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("confirmed baseline changed canonical source")
	}
}

func (f *mariaDBSTPortV2Fixture) assertSnapshotRejected(t *testing.T, expected error) {
	t.Helper()
	_, err := f.updates.GetSystemUpdatePortPolicySnapshot(f.ctx, f.auth, f.policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if !errors.Is(err, expected) {
		t.Fatalf("invalid persisted source was not rejected at its storage boundary: %v", err)
	}
}

func TestMariaDBSTPortV2ConsumeRestartRollbackRecovery(t *testing.T) {
	for _, mode := range []contracts.SystemUpdatePortMode{contracts.SystemUpdatePortModeLocalOnly, contracts.SystemUpdatePortModeLocalAndAdvertised} {
		t.Run(string(mode), func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			job := f.create(t, mode, 18084, 8443, "recovery")
			beforePlan, _ := json.Marshal(job.PortReconfigure)
			job = f.claim(t, job, "").Job
			f.report(t, job, 1, store.SystemUpdateStatusInstalling, nil)
			f.consume(t, job, store.SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
			policy, err := f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
			if err != nil || policy.Revision != 12 || policy.ProjectionRevision != 18 || policy.LocalExecutorPolicyRevision != 24 {
				t.Fatalf("consume did not commit T once: %v", err)
			}
			activeProjection, err := f.updates.GetSystemUpdatePortPolicyProjection(f.ctx, f.observabilityID, policy)
			if err != nil || activeProjection.Ref.ProjectionRevision != 18 || activeProjection.Ref.ConfigRevision != 31 || activeProjection.Ref.LocalListenPort != 18082 {
				t.Fatalf("active other-target projection: %v", err)
			}
			failed := f.report(t, job, 2, store.SystemUpdateStatusFailed, mariaDBSTPortV2Result(job, contracts.SystemUpdatePortReconfigurationRollbackFailed))
			if !failed.RecoveryRequired || failed.PortResult != nil || failed.LastRecoveryObservation == nil {
				t.Fatal("failed recovery occupied accepted result")
			}
			f.updates = store.NewMariaDBSystemUpdateStore(f.db)
			loaded, err := f.updates.GetSystemUpdateJob(f.ctx, job.ID)
			if err != nil || !loaded.RecoveryRequired || loaded.LastRecoveryObservation == nil {
				t.Fatalf("restart lost recovery hold: %v", err)
			}
			afterPlan, _ := json.Marshal(loaded.PortReconfigure)
			if !reflect.DeepEqual(beforePlan, afterPlan) || loaded.PolicyRevision != 17 {
				t.Fatal("restart changed immutable plan/JP")
			}
			active, err := f.updates.GetActiveSystemUpdateJob(f.ctx, f.targetID)
			if err != nil || active.ID != job.ID {
				t.Fatal("failed hold is absent from active lookup")
			}
			_, _, err = f.updates.CreateSystemUpdateJob(f.ctx, store.CreateSystemUpdateJobParams{TargetID: f.observabilityID, TargetServiceType: "observability", AgentServiceID: f.policy.UpdaterID, ExecutionHostID: f.policy.ExecutionHostID, DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.0.1", Strategy: store.SystemUpdateStrategyMaintenance, IdempotencyKey: "blocked-" + f.suffix, RequestedByUserID: "mariadb-admin"})
			if !errors.Is(err, store.ErrSystemUpdateExecutionHostBusy) {
				t.Fatalf("same-host software escaped recovery hold: %v", err)
			}
			claim := f.claim(t, loaded, loaded.ID)
			if !claim.RecoveryRequired || claim.Job.PolicyRevision != 17 {
				t.Fatal("recovery was replaced by a new forward job")
			}
			f.consume(t, claim.Job, store.SystemUpdateMutationOperationPortReconfigureReconcile, "session-2234567890123456")
			policy, _ = f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
			if policy.Revision != 12 || policy.ProjectionRevision != 18 || policy.LocalExecutorPolicyRevision != 24 {
				t.Fatal("reconcile consumed another policy generation")
			}
			result := mariaDBSTPortV2Result(claim.Job, contracts.SystemUpdatePortReconfigurationRolledBack)
			accepted := f.report(t, claim.Job, 1, store.SystemUpdateStatusRolledBack, result)
			if accepted.PortResult == nil || accepted.RecoveryRequired {
				t.Fatal("R did not settle recovery")
			}
			f.updates = store.NewMariaDBSystemUpdateStore(f.db)
			stored, err := f.updates.GetSystemUpdateJob(f.ctx, job.ID)
			if err != nil || !contracts.EqualSystemUpdatePortResults(*stored.PortResult, *result) {
				t.Fatalf("accepted proof changed across restart: %v", err)
			}
			policy, _ = f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
			if policy.Revision != 13 || policy.ProjectionRevision != 19 || policy.LocalExecutorPolicyRevision != 25 {
				t.Fatal("R independent revisions mismatch")
			}
			projection, err := f.updates.GetSystemUpdatePortPolicyProjection(f.ctx, f.targetID, policy)
			if err != nil || !reflect.DeepEqual(projection.Ref, *job.PortReconfigure.Rollback) {
				t.Fatalf("restart accepted projection: %v", err)
			}
			for _, target := range policy.Targets {
				if target.ServiceID == f.observabilityID && (target.LocalListenPort != 18082 || target.DatabaseName != "autostream_observability") {
					t.Fatal("unselected binding was lost")
				}
			}
			service, _ := f.auth.GetService(f.ctx, f.targetID)
			wantE := int64(3)
			if mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
				wantE = 5
			}
			if service.Port != 443 || service.EndpointRevision != wantE || service.AppliedEndpointRevision != wantE || service.AppliedConfigRevision != 33 || service.AppliedConfigSHA256 != job.PortReconfigure.Rollback.ConfigSHA256 {
				t.Fatal("R service does not match frozen snapshot")
			}
			other, _ := f.auth.GetService(f.ctx, f.observabilityID)
			if other.AppliedConfigRevision != 31 || other.EndpointRevision != 3 || other.AppliedEndpointRevision != 3 {
				t.Fatal("R changed an unselected target")
			}
			reservations, _ := f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
			if len(reservations) != 2 {
				t.Fatal("R released another target reservation or retained pending")
			}
			replay, applied, err := f.updates.ReportSystemUpdateJob(f.ctx, job.ID, mariaDBSTPortV2Report(claim.Job, 2, store.SystemUpdateStatusRolledBack, result), time.Now().UTC(), time.Minute)
			if err != nil || applied || !contracts.EqualSystemUpdatePortResults(*replay.PortResult, *result) {
				t.Fatalf("exact proof replay failed: %v", err)
			}
			changed := *result
			changed.Observation.ObservedAt = changed.Observation.ObservedAt.Add(time.Second)
			if _, _, err = f.updates.ReportSystemUpdateJob(f.ctx, job.ID, mariaDBSTPortV2Report(claim.Job, 2, store.SystemUpdateStatusRolledBack, &changed), time.Now().UTC(), time.Minute); err == nil {
				t.Fatal("accepted first observed_at was rewritten")
			}
		})
	}
}

func TestMariaDBSTPortV2MemoryMariaDBSnapshotParity(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	before := f.snapshot(t)
	services := f.sourceServices(t)
	host, err := f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := f.updates.ListServicePortReservations(f.ctx, host.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	auth, policies, updates := store.NewMemorySystemUpdatePortSourceForTest(f.policy, host, services, reservations)
	memoryBefore, err := updates.GetSystemUpdatePortPolicySnapshot(f.ctx, auth, policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if err != nil || !reflect.DeepEqual(memoryBefore, before) {
		t.Fatalf("Memory/MariaDB full B differs: %v", err)
	}
	params := store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalAndAdvertised, TargetID: f.targetID, NewLocalListenPort: 18084, NewAdvertisedPort: 8443, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "parity-" + f.suffix, RequestedByUserID: "mariadb-admin", BuildPolicySnapshot: mariaDBSTPortV2Builder}
	memoryJob, created, err := updates.CreateSystemdPortReconfigurationJob(f.ctx, auth, policies, params)
	if err != nil || !created {
		t.Fatalf("Memory create: %v", err)
	}
	databaseJob, created, err := f.updates.CreateSystemdPortReconfigurationJob(f.ctx, f.auth, f.policies, params)
	if err != nil || !created || !reflect.DeepEqual(memoryJob.PortReconfigure, databaseJob.PortReconfigure) {
		t.Fatalf("Memory/MariaDB frozen B/T/R plan differs: %v", err)
	}
	if _, err := updates.CancelSystemUpdateJob(f.ctx, memoryJob.ID, "mariadb-admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.updates.CancelSystemUpdateJob(f.ctx, databaseJob.ID, "mariadb-admin"); err != nil {
		t.Fatal(err)
	}
	memoryK, err := updates.GetSystemUpdatePortPolicySnapshot(f.ctx, auth, policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if err != nil || !reflect.DeepEqual(memoryK, f.snapshot(t)) {
		t.Fatalf("Memory/MariaDB full K differs: %v", err)
	}
}

func TestMariaDBSTPortV2ObservedLockPhaseCollision(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	before := f.snapshot(t)
	services := f.sourceServices(t)
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	held, release := make(chan struct{}), make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	blocked := store.WithSystemUpdatePortLocksHeldForTest(ctx, func() {
		heldOnce.Do(func() {
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	params := store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalOnly, TargetID: f.targetID, NewLocalListenPort: 18084, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "lock-first-" + f.suffix, RequestedByUserID: "mariadb-admin", BuildPolicySnapshot: mariaDBSTPortV2Builder}
	type outcome struct {
		job     store.SystemUpdateJob
		created bool
		err     error
	}
	first, second := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		job, created, err := f.updates.CreateSystemdPortReconfigurationJob(blocked, f.auth, f.policies, params)
		first <- outcome{job, created, err}
	}()
	select {
	case <-held:
	case result := <-first:
		t.Fatalf("create ended before actual lock phase: %v", result.err)
	case <-ctx.Done():
		t.Fatal("bounded lock-phase barrier timed out")
	}
	store.AssertSystemUpdatePortSourceLocksHeldForTest(t, ctx, f.db, f.policy.ExecutionHostID, f.policy.UpdaterID, services)
	other := params
	other.IdempotencyKey = "lock-second-" + f.suffix
	go func() {
		job, created, err := f.updates.CreateSystemdPortReconfigurationJob(ctx, f.auth, f.policies, other)
		second <- outcome{job, created, err}
	}()
	releaseOnce.Do(func() { close(release) })
	var winner, loser outcome
	select {
	case winner = <-first:
	case <-ctx.Done():
		t.Fatal("first create did not finish")
	}
	select {
	case loser = <-second:
	case <-ctx.Done():
		t.Fatal("colliding create did not finish")
	}
	if winner.err != nil || !winner.created || !errors.Is(loser.err, store.ErrSystemUpdateExecutionHostBusy) || loser.created {
		t.Fatalf("host collision did not serialize: first=%v second=%v", winner.err, loser.err)
	}
	var count int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_update_port_transactions WHERE execution_host_id=?`, f.policy.ExecutionHostID).Scan(&count); err != nil || count != 1 {
		t.Fatal("colliding create left a second dependent transaction")
	}
}

func (f *mariaDBSTPortV2Fixture) sourceServices(t *testing.T) []store.RegisteredService {
	t.Helper()
	services := []store.RegisteredService{}
	ids := []string{f.policy.UpdaterID}
	for _, target := range f.policy.Targets {
		ids = append(ids, target.ServiceID)
	}
	for _, id := range ids {
		service, err := f.auth.GetService(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	return services
}

func TestMariaDBSTPortV2ProjectionKeepsBootstrapPolling(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	f.exec(t, `UPDATE system_update_execution_hosts SET ownership_epoch=0,policy_revision=0 WHERE execution_host_id=?`, f.policy.ExecutionHostID)
	f.policy.LocalExecutorPolicySHA256 = ""
	if _, err := f.updates.GetSystemUpdatePortPolicyProjection(f.ctx, f.targetID, f.policy); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("normal policy polling was blocked without a v2 transaction: %v", err)
	}
}

func TestMariaDBSTPortV2NoOpAndPreconsumeCancel(t *testing.T) {
	t.Run("no_op", func(t *testing.T) {
		f := newMariaDBSTPortV2Fixture(t)
		before := f.snapshot(t)
		serviceBefore, _ := f.auth.GetService(f.ctx, f.targetID)
		job := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18081, 443, "noop")
		job = f.claim(t, job, "").Job
		accepted := f.report(t, job, 1, store.SystemUpdateStatusSucceeded, mariaDBSTPortV2Result(job, contracts.SystemUpdatePortReconfigurationUnchanged))
		if accepted.PortResult == nil || !reflect.DeepEqual(before, f.snapshot(t)) {
			t.Fatal("no-op changed complete policy snapshot")
		}
		serviceAfter, _ := f.auth.GetService(f.ctx, f.targetID)
		if !serviceBefore.UpdatedAt.Equal(serviceAfter.UpdatedAt) {
			t.Fatal("no-op wrote service state")
		}
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM system_update_mutation_grants WHERE job_id=?`, job.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("no-op created a mutation grant")
		}
	})
	t.Run("K", func(t *testing.T) {
		f := newMariaDBSTPortV2Fixture(t)
		job := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, 18084, 8443, "cancel")
		if _, err := f.updates.CancelSystemUpdateJob(f.ctx, job.ID, "mariadb-admin"); err != nil {
			t.Fatal(err)
		}
		state := f.snapshot(t)
		if state.Ref.SourcePolicyRevision != 11 || state.Ref.ProjectionRevision != 17 || state.Ref.ExecutorPolicyRevision != 23 || state.Ref.EndpointRevision != 5 || state.Ref.AppliedEndpointRevision != 3 || state.Ref.ConfigRevision != 31 || state.Ref.LocalListenPort != 18081 {
			t.Fatal("preconsume K changed root/config/AE")
		}
	})
}

type mariaDBSTPortV2Fixture struct {
	mariaDBPullActivationFixture
	db              *sql.DB
	ctx             context.Context
	policy          store.UpdaterPolicy
	observabilityID string
	baseline        contracts.UpdaterPortPolicyBaseline
}

func newMariaDBSTPortV2Fixture(t *testing.T) *mariaDBSTPortV2Fixture {
	t.Helper()
	db, ctx := openMariaDBPullActivationTest(t)
	base := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := base.policies.ActivatePullUpdaterOwnership(ctx, base.auth, base.updates, base.params)
	if err != nil {
		t.Fatal(err)
	}
	f := &mariaDBSTPortV2Fixture{mariaDBPullActivationFixture: base, db: db, ctx: ctx, policy: activated.Policy, observabilityID: "obs-port-" + base.suffix}
	f.registerCleanup(t, db)
	registerMariaDBExecutionHostFixture(t, ctx, f.auth, store.ServiceRegistration{ServiceID: f.observabilityID, ServiceType: "observability", ServiceName: f.observabilityID, PublicURL: "https://observability.example.com:18082"})
	f.policy.Revision = 11
	f.policy.ProjectionRevision = 17
	f.policy.LocalExecutorPolicyRevision = 23
	f.policy.Targets[0].LocalListenPort = 18081
	f.policy.Targets = append(f.policy.Targets, store.UpdaterPolicyTarget{TargetID: f.observabilityID, ServiceID: f.observabilityID, HostID: f.policy.ExecutionHostID, ServiceType: "observability", DeploymentMode: "systemd", DatabaseName: "autostream_observability", LocalListenPort: 18082})
	for _, target := range f.policy.Targets {
		config, err := contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetType(target.ServiceType), contracts.SystemUpdateDeploymentSystemd, target.LocalListenPort, 31)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE services SET endpoint_revision=3,applied_endpoint_revision=3,applied_config_revision=31,applied_config_sha256=?,endpoint_status='applied' WHERE service_id=?`, contracts.ComputeSystemUpdatePortBytesSHA256(config), target.ServiceID)
		f.exec(t, `INSERT INTO update_agent_target_local_listeners (updater_service_id,target_id,binding_policy_revision,local_listen_port,updated_at) VALUES (?,?,11,?,?) ON DUPLICATE KEY UPDATE binding_policy_revision=11,local_listen_port=VALUES(local_listen_port)`, f.policy.UpdaterID, target.TargetID, target.LocalListenPort, time.Now().UTC())
	}
	f.exec(t, `UPDATE services SET host='worker.example.test',port=443,ssl_enabled=1,public_url='https://worker.example.test:443/api?tenant=a',desired_host='worker.example.test',desired_port=443,desired_ssl_enabled=1,desired_public_url='https://worker.example.test:443/api?tenant=a' WHERE service_id=?`, f.targetID)
	f.exec(t, `INSERT INTO update_agent_target_databases (updater_service_id,target_id,binding_policy_revision,database_name,updated_at) VALUES (?,?,11,'autostream_observability',?)`, f.policy.UpdaterID, f.observabilityID, time.Now().UTC())
	f.exec(t, `INSERT INTO service_port_reservations (execution_host_id,network_namespace,protocol,port,service_id,service_role,created_at,updated_at) VALUES (?,'host','tcp',18082,?,'api',?,?)`, f.policy.ExecutionHostID, f.observabilityID, time.Now().UTC(), time.Now().UTC())
	services := []store.RegisteredService{}
	for _, target := range f.policy.Targets {
		service, err := f.auth.GetService(ctx, target.ServiceID)
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	materialized, err := mariaDBSTPortV2Builder(f.policy, services)
	if err != nil {
		t.Fatal(err)
	}
	f.policy.LocalExecutorPolicySHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(materialized.ExecutorPolicyJSON)
	body, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE update_agent_policies SET revision=11,projection_revision=17,local_executor_policy_revision=23,policy_json=? WHERE service_id=?`, body, f.policy.UpdaterID)
	f.exec(t, `UPDATE system_update_execution_hosts SET policy_revision=17 WHERE execution_host_id=?`, f.policy.ExecutionHostID)
	worker, _ := f.auth.GetService(ctx, f.targetID)
	activated.Policy = f.policy
	var capabilities map[string]any
	_ = json.Unmarshal(markMariaDBPullAgentMutationReadySQL(t, base, activated, worker), &capabilities)
	capabilities["port_contract_version"] = 2
	capabilities["policy_transition_version"] = 1
	capabilities["reported_ports"] = map[string]int64{f.targetID: 18081, f.observabilityID: 18082}
	f.baseline = contracts.UpdaterPortPolicyBaseline{PortContractVersion: 2, PolicyTransitionVersion: 1, AgentUID: 1001, AgentGID: 1001, SourcePolicyRevision: 11, ProjectionRevision: 17, ExecutorPolicyRevision: 23, ExecutorPolicySHA256: f.policy.LocalExecutorPolicySHA256, ObservedAt: time.Now().UTC()}
	for _, target := range f.policy.Targets {
		service, _ := f.auth.GetService(ctx, target.ServiceID)
		f.baseline.Targets = append(f.baseline.Targets, contracts.UpdaterPortPolicyBaselineTarget{ServiceID: target.ServiceID, ServiceType: contracts.SystemUpdateTargetType(target.ServiceType), DeploymentMode: contracts.SystemUpdateDeploymentSystemd, EndpointRevision: 3, ConfigRevision: 31, ConfigSHA256: service.AppliedConfigSHA256, LocalListenPort: target.LocalListenPort})
	}
	capabilities["port_policy_baseline"] = f.baseline
	body, _ = json.Marshal(capabilities)
	f.exec(t, `UPDATE services SET reported_capabilities=?,status='online',last_heartbeat_at=? WHERE service_id=?`, body, time.Now().UTC(), f.policy.UpdaterID)
	return f
}

func mariaDBSTPortV2Builder(policy store.UpdaterPolicy, services []store.RegisteredService) (store.SystemUpdatePortPolicyMaterialization, error) {
	source := updateradapter.HostAgentConfigurePolicySource{PanelURL: "https://panel.example.test", ExecutionHostID: policy.ExecutionHostID, AgentUID: 1001, AgentGID: 1001, SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision, LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision}
	byID := map[string]store.RegisteredService{}
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	for _, target := range policy.Targets {
		service := byID[target.ServiceID]
		source.Targets = append(source.Targets, updateradapter.HostAgentConfigurePolicyTarget{ServiceID: target.ServiceID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode, DatabaseName: target.DatabaseName, EndpointRevision: service.AppliedEndpointRevision, AppliedConfigRevision: service.AppliedConfigRevision, AppliedConfigSHA256: service.AppliedConfigSHA256, AppliedEndpointPort: service.AppliedEndpoint.Port, LocalListenPort: target.LocalListenPort})
	}
	projection, err := updateradapter.BuildHostAgentConfigurePolicy(source)
	return store.SystemUpdatePortPolicyMaterialization{ExecutorPolicyJSON: projection.Policy}, err
}
func (f *mariaDBSTPortV2Fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *mariaDBSTPortV2Fixture) snapshot(t *testing.T) store.SystemUpdatePortPolicySnapshot {
	t.Helper()
	snapshot, err := f.updates.GetSystemUpdatePortPolicySnapshot(f.ctx, f.auth, f.policies, store.SystemUpdatePortSnapshotParams{TargetID: f.targetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func (f *mariaDBSTPortV2Fixture) create(t *testing.T, mode contracts.SystemUpdatePortMode, local, advertised int, key string) store.SystemUpdateJob {
	t.Helper()
	before := f.snapshot(t)
	desired := before.Ref.ConfigRevision
	if local != before.Ref.LocalListenPort {
		desired++
	}
	if mode == contracts.SystemUpdatePortModeLocalOnly {
		advertised = 0
	}
	job, created, err := f.updates.CreateSystemdPortReconfigurationJob(f.ctx, f.auth, f.policies, store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: mode, TargetID: f.targetID, NewLocalListenPort: local, NewAdvertisedPort: advertised, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: before.Ref.EndpointRevision, ExpectedDesiredRevision: desired, ExpectedFence: 1, IdempotencyKey: key + "-" + f.suffix, RequestedByUserID: "mariadb-admin", BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	return job
}
func (f *mariaDBSTPortV2Fixture) claim(t *testing.T, job store.SystemUpdateJob, active string) store.SystemUpdateClaim {
	t.Helper()
	lease := int64(1)
	if active != "" {
		lease = job.LeaseGeneration
	}
	claim, _, err := f.updates.ClaimSystemUpdateJobV2(f.ctx, f.policy.UpdaterID, f.policy.ExecutionHostID, active, lease, job.OwnershipEpoch, map[string]string{f.targetID: "systemd"}, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}
func mariaDBSTPortV2Report(job store.SystemUpdateJob, sequence int64, status string, result *contracts.SystemUpdatePortResultV2) store.SystemUpdateReport {
	return store.SystemUpdateReport{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, DesiredRevision: job.PortReconfigure.Target.ConfigRevision, Fence: job.OwnershipEpoch, Sequence: sequence, Status: status, Progress: 100, PortResult: result}
}
func (f *mariaDBSTPortV2Fixture) report(t *testing.T, job store.SystemUpdateJob, sequence int64, status string, result *contracts.SystemUpdatePortResultV2) store.SystemUpdateJob {
	t.Helper()
	next, applied, err := f.updates.ReportSystemUpdateJob(f.ctx, job.ID, mariaDBSTPortV2Report(job, sequence, status, result), time.Now().UTC(), time.Minute)
	if err != nil || !applied {
		t.Fatalf("report %s: applied=%v err=%v", status, applied, err)
	}
	return next
}
func (f *mariaDBSTPortV2Fixture) consume(t *testing.T, job store.SystemUpdateJob, operation, session string) {
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
	if _, replayed, err := f.updates.ConsumeSystemUpdateMutationGrant(f.ctx, job.ID, issued.GrantToken, job.LeaseGeneration, binding, time.Now().UTC()); err != nil || replayed {
		t.Fatalf("consume: replay=%v err=%v", replayed, err)
	}
	if _, replayed, err := f.updates.ConsumeSystemUpdateMutationGrant(f.ctx, job.ID, issued.GrantToken, job.LeaseGeneration, binding, time.Now().UTC()); err != nil || !replayed {
		t.Fatalf("consume replay: replay=%v err=%v", replayed, err)
	}
}
func mariaDBSTPortV2Result(job store.SystemUpdateJob, result contracts.SystemUpdatePortReconfigurationResult) *contracts.SystemUpdatePortResultV2 {
	value := &contracts.SystemUpdatePortResultV2{Result: result, Observation: contracts.SystemUpdatePortObservation{ObservedAt: time.Now().UTC()}}
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
	return value
}
