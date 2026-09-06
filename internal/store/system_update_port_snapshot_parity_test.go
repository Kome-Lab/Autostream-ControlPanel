package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMemorySTPortV2NegativeSnapshotClassification(t *testing.T) {
	for _, invalid := range stPortInvalidSnapshotCases() {
		t.Run(invalid.name, func(t *testing.T) {
			ctx := context.Background()
			auth, policies, updates := store.NewMemorySystemUpdatePortFixtureForTest(t)
			policy, err := policies.GetUpdaterPolicy(ctx, "host-agent-a")
			if err != nil {
				t.Fatal(err)
			}
			before, err := updates.GetSystemUpdatePortPolicySnapshot(ctx, auth, policies, store.SystemUpdatePortSnapshotParams{TargetID: "worker-a", BuildPolicySnapshot: mariaDBSTPortV2Builder})
			if err != nil {
				t.Fatal(err)
			}
			job, created, err := updates.CreateSystemdPortReconfigurationJob(ctx, auth, policies, store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalAndAdvertised, TargetID: "worker-a", NewLocalListenPort: 18084, NewAdvertisedPort: 8443, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "negative-memory", RequestedByUserID: "admin", BuildPolicySnapshot: mariaDBSTPortV2Builder})
			if err != nil || !created {
				t.Fatalf("valid prestate: %v", err)
			}
			store.MutateMemorySystemUpdatePortBeforeForTest(updates, job.ID, invalid.mutate)
			persisted := store.MemorySystemUpdatePortStateForTest(auth, policies, updates, job.ID)
			_, err = updates.GetSystemUpdatePortPolicyProjection(ctx, "worker-a", policy)
			if !errors.Is(err, store.ErrSystemUpdatePortPolicySnapshotUnavailable) {
				t.Fatalf("fixed negative classification: %v", err)
			}
			if !reflect.DeepEqual(persisted, store.MemorySystemUpdatePortStateForTest(auth, policies, updates, job.ID)) {
				t.Fatal("rejected projection mutated persisted state")
			}
		})
	}
}

// The physical 059/060 table negatives remain in the integration fixture.
// This matrix applies the same single invalid complete snapshot to both real
// persisted job/projection paths. Expected classification is fixed beforehand.
func TestMariaDBSTPortV2NegativeSnapshotClassificationParity(t *testing.T) {
	for _, invalid := range stPortInvalidSnapshotCases() {
		t.Run(invalid.name, func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			before := f.snapshot(t)
			host, err := f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			reservations, err := f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			auth, policies, memory := store.NewMemorySystemUpdatePortSourceForTest(f.policy, host, f.sourceServices(t), reservations)
			params := store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalAndAdvertised, TargetID: f.targetID, NewLocalListenPort: 18084, NewAdvertisedPort: 8443, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: 32, ExpectedFence: 1, IdempotencyKey: "negative-" + f.suffix, RequestedByUserID: "mariadb-admin", BuildPolicySnapshot: mariaDBSTPortV2Builder}
			memoryJob, created, err := memory.CreateSystemdPortReconfigurationJob(f.ctx, auth, policies, params)
			if err != nil || !created {
				t.Fatalf("valid Memory prestate: %v", err)
			}
			databaseJob, created, err := f.updates.CreateSystemdPortReconfigurationJob(f.ctx, f.auth, f.policies, params)
			if err != nil || !created || !reflect.DeepEqual(memoryJob.PortReconfigure, databaseJob.PortReconfigure) {
				t.Fatalf("valid MariaDB prestate: %v", err)
			}
			var original []byte
			if err := f.db.QueryRowContext(f.ctx, `SELECT before_json FROM system_update_port_transactions WHERE job_id=?`, databaseJob.ID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			var corrupted store.SystemUpdatePortPolicySnapshot
			if err := json.Unmarshal(original, &corrupted); err != nil {
				t.Fatal(err)
			}
			invalid.mutate(&corrupted.Snapshot)
			body, err := json.Marshal(corrupted)
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `UPDATE system_update_port_transactions SET before_json=? WHERE job_id=?`, body, databaseJob.ID)
			defer f.exec(t, `UPDATE system_update_port_transactions SET before_json=? WHERE job_id=?`, original, databaseJob.ID)
			store.MutateMemorySystemUpdatePortBeforeForTest(memory, memoryJob.ID, invalid.mutate)
			memoryBefore := store.MemorySystemUpdatePortStateForTest(auth, policies, memory, memoryJob.ID)
			databaseBefore := f.databaseState(t, "")
			jobBefore := f.rawPortJobState(t, databaseJob.ID)
			_, memoryErr := memory.GetSystemUpdatePortPolicyProjection(f.ctx, f.targetID, f.policy)
			_, databaseErr := f.updates.GetSystemUpdatePortPolicyProjection(f.ctx, f.targetID, f.policy)
			// Each backend independently has to satisfy this contract. Equality
			// of two accidental errors is never the oracle.
			if !errors.Is(memoryErr, store.ErrSystemUpdatePortPolicySnapshotUnavailable) {
				t.Fatalf("Memory negative classification: %v", memoryErr)
			}
			if !errors.Is(databaseErr, store.ErrSystemUpdatePortPolicySnapshotUnavailable) {
				t.Fatalf("MariaDB negative classification: %v", databaseErr)
			}
			if !reflect.DeepEqual(memoryBefore, store.MemorySystemUpdatePortStateForTest(auth, policies, memory, memoryJob.ID)) {
				t.Fatal("Memory rejection changed persisted state")
			}
			var after []byte
			if err := f.db.QueryRowContext(f.ctx, `SELECT before_json FROM system_update_port_transactions WHERE job_id=?`, databaseJob.ID).Scan(&after); err != nil || string(after) != string(body) {
				t.Fatal("MariaDB rejection rewrote the invalid snapshot")
			}
			if !reflect.DeepEqual(databaseBefore, f.databaseState(t, "")) {
				t.Fatal("MariaDB rejection changed policy/bindings/services/host/reservations")
			}
			if !reflect.DeepEqual(jobBefore, f.rawPortJobState(t, databaseJob.ID)) {
				t.Fatal("MariaDB rejection changed persisted job or any dependent transaction field")
			}
		})
	}
}

// Read raw persisted rows without decoding the intentionally invalid snapshot.
// Values are only compared in memory; diagnostics never print credentials.
func (f *mariaDBSTPortV2Fixture) rawPortJobState(t *testing.T, jobID string) []byte {
	t.Helper()
	var persisted [][][]byte
	for _, query := range []string{`SELECT * FROM system_update_jobs WHERE id=?`, `SELECT * FROM system_update_port_transactions WHERE job_id=?`} {
		rows, err := f.db.QueryContext(f.ctx, query, jobID)
		if err != nil {
			t.Fatal("raw job state read failed")
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal("raw job columns unavailable")
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("raw job row missing")
		}
		values, pointers := make([][]byte, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			rows.Close()
			t.Fatal("raw job row unreadable")
		}
		persisted = append(persisted, values)
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("raw job identity was not unique")
		}
		rows.Close()
	}
	body, _ := json.Marshal(persisted)
	return body
}

type stPortInvalidSnapshot struct {
	name   string
	mutate func(*contracts.SystemUpdatePortPolicySnapshot)
}

func stPortInvalidSnapshotCases() []stPortInvalidSnapshot {
	find := func(snapshot *contracts.SystemUpdatePortPolicySnapshot, database bool) *contracts.SystemUpdatePortPolicyBinding {
		for i := range snapshot.Bindings {
			if (snapshot.Bindings[i].DatabaseName != nil) == database {
				return &snapshot.Bindings[i]
			}
		}
		panic("fixture lacks a required binding")
	}
	return []stPortInvalidSnapshot{
		{"missing_database", func(s *contracts.SystemUpdatePortPolicySnapshot) { find(s, true).DatabaseName = nil }},
		{"missing_listener", func(s *contracts.SystemUpdatePortPolicySnapshot) { find(s, false).LocalListenPort = nil }},
		{"orphan_database", func(s *contracts.SystemUpdatePortPolicySnapshot) {
			b := *find(s, true)
			b.TargetID = "orphan-port"
			b.ServiceID = "orphan-port"
			b.LocalListenPort = nil
			s.Bindings = append(s.Bindings, b)
		}},
		{"orphan_listener", func(s *contracts.SystemUpdatePortPolicySnapshot) {
			b := *find(s, false)
			b.TargetID = "orphan-port"
			b.ServiceID = "orphan-port"
			s.Bindings = append(s.Bindings, b)
		}},
		{"duplicate_database", func(s *contracts.SystemUpdatePortPolicySnapshot) {
			b := *find(s, true)
			b.LocalListenPort = nil
			s.Bindings = append(s.Bindings, b)
		}},
		{"duplicate_listener", func(s *contracts.SystemUpdatePortPolicySnapshot) { s.Bindings = append(s.Bindings, *find(s, false)) }},
		{"stale_database_binding", func(s *contracts.SystemUpdatePortPolicySnapshot) { find(s, true).BindingPolicyRevision-- }},
		{"stale_listener_binding", func(s *contracts.SystemUpdatePortPolicySnapshot) { find(s, false).BindingPolicyRevision-- }},
		{"foreign_host_binding", func(s *contracts.SystemUpdatePortPolicySnapshot) { find(s, true).HostID = "foreign-host" }},
		{"foreign_host_target", func(s *contracts.SystemUpdatePortPolicySnapshot) { s.Targets[0].HostID = "foreign-host" }},
		{"duplicate_target", func(s *contracts.SystemUpdatePortPolicySnapshot) { s.Targets = append(s.Targets, s.Targets[0]) }},
	}
}

type stPortTransitionBackend interface {
	store.SystemUpdateStore
	store.SystemUpdateV2Store
	store.SystemUpdateMutationGrantStore
	store.SystemUpdatePortSnapshotStore
	GetSystemUpdatePortPolicyProjection(context.Context, string, store.UpdaterPolicy) (store.SystemUpdatePortPolicySnapshot, error)
}

func TestMariaDBSTPortV2FullSnapshotTransitionParity(t *testing.T) {
	for _, mode := range []contracts.SystemUpdatePortMode{contracts.SystemUpdatePortModeLocalOnly, contracts.SystemUpdatePortModeLocalAndAdvertised} {
		for _, ending := range []string{"applied", "rolled_back", "unchanged", "canceled"} {
			t.Run(string(mode)+"/"+ending, func(t *testing.T) {
				f := newMariaDBSTPortV2Fixture(t)
				before := f.snapshot(t)
				host, err := f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
				if err != nil {
					t.Fatal(err)
				}
				reservations, err := f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
				if err != nil {
					t.Fatal(err)
				}
				auth, policies, memory := store.NewMemorySystemUpdatePortSourceForTest(f.policy, host, f.sourceServices(t), reservations)
				local, advertised, desired := 18084, 8443, int64(32)
				if ending == "unchanged" {
					local, advertised, desired = 18081, 443, 31
				}
				if mode == contracts.SystemUpdatePortModeLocalOnly {
					advertised = 0
				}
				params := store.CreateSystemdPortReconfigurationJobParams{PortContractVersion: 2, Mode: mode, TargetID: f.targetID, NewLocalListenPort: local, NewAdvertisedPort: advertised, ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: 3, ExpectedDesiredRevision: desired, ExpectedFence: 1, IdempotencyKey: "whole-parity-" + f.suffix, RequestedByUserID: "admin", BuildPolicySnapshot: mariaDBSTPortV2Builder}
				memoryJob, fresh, err := memory.CreateSystemdPortReconfigurationJob(f.ctx, auth, policies, params)
				if err != nil || !fresh {
					t.Fatalf("Memory create: %v", err)
				}
				databaseJob, fresh, err := f.updates.CreateSystemdPortReconfigurationJob(f.ctx, f.auth, f.policies, params)
				if err != nil || !fresh || !reflect.DeepEqual(memoryJob.PortReconfigure, databaseJob.PortReconfigure) {
					t.Fatalf("complete frozen B/T/R plans differ: %v", err)
				}
				memoryFinal := stPortExerciseTransition(t, f.ctx, memory, auth, policies, memoryJob, ending)
				databaseFinal := stPortExerciseTransition(t, f.ctx, f.updates, f.auth, f.policies, databaseJob, ending)
				if !reflect.DeepEqual(memoryFinal, databaseFinal) {
					t.Fatal("complete policy/bindings/credentials/targets/root bytes differ across backends")
				}
				expected := *databaseJob.PortReconfigure.Target
				if ending == "rolled_back" {
					expected = *databaseJob.PortReconfigure.Rollback
				}
				if ending == "unchanged" {
					expected = *databaseJob.PortReconfigure.Before
				}
				if ending != "canceled" && !reflect.DeepEqual(databaseFinal.Ref, expected) {
					t.Fatal("final snapshot differs from independently frozen selected result")
				}
				step := int64(1)
				if ending == "rolled_back" {
					step = 2
				}
				if ending == "unchanged" || ending == "canceled" {
					step = 0
				}
				if databaseFinal.Ref.SourcePolicyRevision != 11+step || databaseFinal.Ref.ProjectionRevision != 17+step || databaseFinal.Ref.ExecutorPolicyRevision != 23+step || databaseFinal.Ref.ConfigRevision != 31+step {
					t.Fatal("full transition lost independent S/P/X/C counters")
				}
				expectedEndpoint := int64(3)
				if mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
					if ending == "applied" {
						expectedEndpoint = 4
					}
					if ending == "rolled_back" || ending == "canceled" {
						expectedEndpoint = 5
					}
				}
				expectedAppliedEndpoint := expectedEndpoint
				if ending == "canceled" {
					expectedAppliedEndpoint = 3
				}
				if databaseFinal.Ref.EndpointRevision != expectedEndpoint || databaseFinal.Ref.AppliedEndpointRevision != expectedAppliedEndpoint {
					t.Fatal("final E/AE differs from operation input and prestate")
				}
				if ending == "canceled" {
					if databaseFinal.Ref.LocalListenPort != 18081 || databaseFinal.Ref.AdvertisedPort != 443 || string(databaseFinal.Snapshot.RootPolicy) != string(before.Snapshot.RootPolicy) {
						t.Fatal("K did not preserve B functional policy")
					}
				}
				if ending == "unchanged" && !reflect.DeepEqual(databaseFinal, before) {
					t.Fatal("N did not preserve complete B")
				}
				for _, source := range before.Snapshot.Targets {
					if source.ServiceID != f.targetID {
						found := false
						for _, current := range databaseFinal.Snapshot.Targets {
							if current.ServiceID == source.ServiceID {
								found = true
								if !reflect.DeepEqual(current, source) {
									t.Fatal("transition changed unrelated target metadata")
								}
							}
						}
						if !found {
							t.Fatal("transition omitted another target")
						}
					}
				}
				if len(databaseFinal.Snapshot.CredentialReferences) != len(before.Snapshot.CredentialReferences) || !reflect.DeepEqual(databaseFinal.Snapshot.CredentialReferences, before.Snapshot.CredentialReferences) {
					t.Fatal("transition changed credential references")
				}
			})
		}
	}
}

func stPortExerciseTransition(t *testing.T, ctx context.Context, backend stPortTransitionBackend, auth store.ServiceRegistryStore, policies store.UpdaterPolicyStore, job store.SystemUpdateJob, ending string) store.SystemUpdatePortPolicySnapshot {
	t.Helper()
	if ending == "canceled" {
		if _, err := backend.CancelSystemUpdateJob(ctx, job.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	} else {
		claim, clear, err := backend.ClaimSystemUpdateJobV2(ctx, job.AgentServiceID, job.ExecutionHostID, "", 1, job.OwnershipEpoch, map[string]string{job.TargetID: "systemd"}, time.Now().UTC(), time.Minute)
		if err != nil || clear || claim.Job.ID != job.ID {
			t.Fatalf("claim: %v", err)
		}
		job = claim.Job
		sequence := int64(1)
		if ending != "unchanged" {
			if _, applied, err := backend.ReportSystemUpdateJob(ctx, job.ID, mariaDBSTPortV2Report(job, sequence, store.SystemUpdateStatusInstalling, nil), time.Now().UTC(), time.Minute); err != nil || !applied {
				t.Fatalf("installing report: %v", err)
			}
			sequence++
			session := "session-1234567890123456"
			runtime, err := store.ComputeSystemUpdatePortRuntimePlanSHA256(job, session)
			if err != nil {
				t.Fatal(err)
			}
			binding := store.SystemUpdateMutationGrantBinding{HostID: job.ExecutionHostID, TransportMode: job.TransportMode, OwnershipEpoch: job.OwnershipEpoch, PolicyRevision: job.PolicyRevision, TargetID: job.TargetID, TargetServiceType: job.TargetServiceType, TargetVersion: job.TargetVersion, DeploymentMode: job.DeploymentMode, JobOperation: job.Operation, Operation: store.SystemUpdateMutationOperationPortReconfigure, PlanSHA256: runtime, SessionID: session, PortReconfigure: job.PortReconfigure}
			grant, err := backend.IssueSystemUpdateMutationGrant(ctx, job.ID, store.IssueSystemUpdateMutationGrantParams{ProtocolVersion: 2, AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID, LeaseGeneration: job.LeaseGeneration, Binding: binding}, time.Now().UTC(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, replay, err := backend.ConsumeSystemUpdateMutationGrant(ctx, job.ID, grant.GrantToken, job.LeaseGeneration, binding, time.Now().UTC()); err != nil || replay {
				t.Fatalf("consume: %v", err)
			}
			policy, err := policies.GetUpdaterPolicy(ctx, job.AgentServiceID)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := backend.GetSystemUpdatePortPolicyProjection(ctx, job.TargetID, policy)
			if err != nil || !reflect.DeepEqual(canonical.Ref, *job.PortReconfigure.Target) {
				t.Fatalf("canonical T whole snapshot projection: %v", err)
			}
		}
		status := store.SystemUpdateStatusSucceeded
		if ending == "rolled_back" {
			if _, applied, err := backend.ReportSystemUpdateJob(ctx, job.ID, mariaDBSTPortV2Report(job, sequence, store.SystemUpdateStatusRollingBack, nil), time.Now().UTC(), time.Minute); err != nil || !applied {
				t.Fatalf("rollback report: %v", err)
			}
			sequence++
			status = store.SystemUpdateStatusRolledBack
		}
		proof := mariaDBSTPortV2Result(job, contracts.SystemUpdatePortReconfigurationResult(ending))
		report := mariaDBSTPortV2Report(job, sequence, status, proof)
		result, applied, err := backend.ReportSystemUpdateJob(ctx, job.ID, report, time.Now().UTC(), time.Minute)
		if err != nil || !applied || result.PortResult == nil || !contracts.EqualSystemUpdatePortResults(*result.PortResult, *proof) {
			t.Fatalf("terminal result: %v", err)
		}
		if _, applied, err = backend.ReportSystemUpdateJob(ctx, job.ID, report, time.Now().UTC(), time.Minute); err != nil || applied {
			t.Fatalf("terminal exact replay: %v", err)
		}
	}
	final, err := backend.GetSystemUpdatePortPolicySnapshot(ctx, auth, policies, store.SystemUpdatePortSnapshotParams{TargetID: job.TargetID, BuildPolicySnapshot: mariaDBSTPortV2Builder})
	if err != nil {
		t.Fatal(err)
	}
	return final
}
