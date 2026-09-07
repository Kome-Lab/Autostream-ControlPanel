package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

func (f *mariaDBSTPortV2Fixture) laneRegressionParams(t *testing.T, key string) store.CreateSystemdPortReconfigurationJobParams {
	t.Helper()
	before := f.snapshot(t)
	return store.CreateSystemdPortReconfigurationJobParams{
		PortContractVersion: 2, Mode: contracts.SystemUpdatePortModeLocalOnly,
		TargetID: f.targetID, NewLocalListenPort: 18084,
		ExpectedSnapshotID: before.Ref.SnapshotID, ExpectedEndpointRevision: before.Ref.EndpointRevision,
		ExpectedDesiredRevision: before.Ref.ConfigRevision + 1, ExpectedFence: 1,
		IdempotencyKey: key + "-" + f.suffix, RequestedByUserID: "mariadb-admin",
		BuildPolicySnapshot: mariaDBSTPortV2Builder,
	}
}

func (f *mariaDBSTPortV2Fixture) createLaneRegression(ctx context.Context, params store.CreateSystemdPortReconfigurationJobParams) stPortConcurrentOutcome {
	job, created, err := f.updates.CreateSystemdPortReconfigurationJob(ctx, f.auth, f.policies, params)
	return stPortConcurrentOutcome{job: job, id: job.ID, created: created, err: err}
}

// The first create has performed all writes but still holds its real host and
// source locks. The second call must reach its actual source phase while that
// transaction is uncommitted; goroutine startup is not the witness.
func (f *mariaDBSTPortV2Fixture) runLaneRegressionBeforeCommit(t *testing.T, params store.CreateSystemdPortReconfigurationJobParams, secondPhase string, second func(context.Context) stPortConcurrentOutcome) (stPortConcurrentOutcome, stPortConcurrentOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	held, reached, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var heldOnce, reachedOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	firstCtx := store.WithSystemUpdatePortCreatePhaseForTest(ctx, func(phase string) {
		if phase == "before_commit" {
			heldOnce.Do(func() {
				close(held)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
	})
	firstDone, secondDone := make(chan stPortConcurrentOutcome, 1), make(chan stPortConcurrentOutcome, 1)
	go func() { firstDone <- f.createLaneRegression(firstCtx, params) }()
	select {
	case <-held:
	case <-firstDone:
		t.Fatal("first create ended before its pre-commit boundary")
	case <-ctx.Done():
		t.Fatal("first create exceeded the bounded pre-commit wait")
	}
	store.AssertSystemUpdatePortSourceLocksHeldForTest(t, ctx, f.db, f.policy.ExecutionHostID, f.policy.UpdaterID, f.sourceServices(t))
	secondCtx := store.WithSystemUpdatePortCreatePhaseForTest(ctx, func(phase string) {
		if phase == secondPhase {
			reachedOnce.Do(func() { close(reached) })
		}
	})
	go func() { secondDone <- second(secondCtx) }()
	select {
	case <-reached:
	case <-secondDone:
		t.Fatal("second operation ended before its required source boundary")
	case <-ctx.Done():
		t.Fatal("second operation did not reach its source boundary while the first held its locks")
	}
	releaseOnce.Do(func() { close(release) })
	var firstResult, secondResult stPortConcurrentOutcome
	select {
	case firstResult = <-firstDone:
	case <-ctx.Done():
		t.Fatal("first create did not complete after releasing its boundary")
	}
	select {
	case secondResult = <-secondDone:
	case <-ctx.Done():
		t.Fatal("second operation did not complete after releasing the first")
	}
	if firstResult.err != nil || !firstResult.created {
		t.Fatal("pre-commit winner did not commit exactly once")
	}
	return firstResult, secondResult
}

func TestMariaDBSTPortV2HostWaitUsesFreshLaneAndIdempotency(t *testing.T) {
	for _, kind := range []string{"different_key", "same_key_replay", "same_key_payload_conflict", "baseline"} {
		t.Run(kind, func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			params := f.laneRegressionParams(t, "host-wait")
			other := params
			if kind == "different_key" {
				other.IdempotencyKey += "-other"
			}
			if kind == "same_key_payload_conflict" {
				other.NewLocalListenPort++
			}
			second := func(ctx context.Context) stPortConcurrentOutcome { return f.createLaneRegression(ctx, other) }
			if kind == "baseline" {
				baseline := store.ConfirmSystemUpdatePortPolicyBaselineParams{
					Baseline: &f.baseline, AgentServiceID: f.policy.UpdaterID,
					ExpectedSourcePolicyRevision: 11, ExpectedProjectionRevision: 17, ExpectedExecutorPolicyRevision: 23,
					ExpectedExecutorPolicySHA256: f.policy.LocalExecutorPolicySHA256,
					AppliedEndpointRevisions:     map[string]int64{f.targetID: 3, f.observabilityID: 3}, BuildPolicySnapshot: mariaDBSTPortV2Builder,
				}
				second = func(ctx context.Context) stPortConcurrentOutcome {
					return stPortConcurrentOutcome{err: f.updates.ConfirmSystemUpdatePortPolicyBaseline(ctx, f.auth, f.policies, baseline)}
				}
			}
			winner, result := f.runLaneRegressionBeforeCommit(t, params, "before_host_lock", second)
			if result.created {
				t.Fatal("host waiter created a second job")
			}
			switch kind {
			case "same_key_replay":
				if result.err != nil || result.job.ID != winner.job.ID || !reflect.DeepEqual(result.job.PortReconfigure, winner.job.PortReconfigure) {
					t.Fatal("same-key waiter did not replay the committed immutable plan")
				}
			case "same_key_payload_conflict":
				if !errors.Is(result.err, store.ErrSystemUpdatePortIdempotencyConflict) {
					t.Fatal("same-key changed payload was not rejected as an idempotency conflict")
				}
			default:
				if !errors.Is(result.err, store.ErrSystemUpdateExecutionHostBusy) {
					t.Fatal("host waiter missed the lane member committed during its wait")
				}
			}
			f.assertLaneCounts(t, 1, 1, 0, 0)
		})
	}
}

func TestMariaDBSTPortV2DifferentHostGlobalKeyConflict(t *testing.T) {
	f, other := newMariaDBSTPortV2Fixture(t), newMariaDBSTPortV2Fixture(t)
	params := f.laneRegressionParams(t, "global-key")
	otherParams := other.laneRegressionParams(t, "global-key")
	otherParams.RequestedByUserID, otherParams.IdempotencyKey = params.RequestedByUserID, params.IdempotencyKey
	before := other.databaseState(t, "")
	winner, loser := f.runLaneRegressionBeforeCommit(t, params, "before_job_insert", func(ctx context.Context) stPortConcurrentOutcome {
		return other.createLaneRegression(ctx, otherParams)
	})
	if loser.created || !errors.Is(loser.err, store.ErrSystemUpdatePortIdempotencyConflict) {
		t.Fatal("different hosts did not arbitrate the shared global user/key as a payload conflict")
	}
	stored, err := f.updates.GetSystemUpdateJobByIdempotency(f.ctx, params.RequestedByUserID, params.IdempotencyKey)
	if err != nil || stored.ID != winner.job.ID {
		t.Fatal("global idempotency winner changed after the losing insert")
	}
	if !reflect.DeepEqual(before, other.databaseState(t, "")) {
		t.Fatal("losing global-key insert changed the other host's source or dependent rows")
	}
	f.assertLaneCounts(t, 1, 1, 0, 0)
	other.assertLaneCounts(t, 0, 0, 0, 0)
}

func TestMariaDBSTPortV2InsertConflictFreshReplayBoundary(t *testing.T) {
	for _, kind := range []string{"terminal_replay", "changed_payload", "other_unique"} {
		t.Run(kind, func(t *testing.T) {
			f := newMariaDBSTPortV2Fixture(t)
			params := f.laneRegressionParams(t, "insert-conflict")
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal("could not begin the conflict-boundary transaction")
			}
			defer tx.Rollback()
			var before int
			if err := tx.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM system_update_jobs WHERE requested_by_user_id=? AND idempotency_key=?`, params.RequestedByUserID, params.IdempotencyKey).Scan(&before); err != nil || before != 0 {
				t.Fatal("conflict boundary did not start with a missing key in its RR snapshot")
			}
			winner := f.createLaneRegression(f.ctx, params)
			if winner.err != nil || !winner.created {
				t.Fatal("conflict-boundary winner was not created")
			}
			canceled, err := f.updates.CancelSystemUpdateJob(f.ctx, winner.job.ID, params.RequestedByUserID)
			if err != nil || canceled.Status != store.SystemUpdateStatusCancelled {
				t.Fatal("conflict-boundary winner was not terminal before replay")
			}
			if kind == "changed_payload" {
				params.NewLocalListenPort++
			}
			idExpression := "UUID()"
			args := []any{params.TargetID, f.policy.ExecutionHostID, params.IdempotencyKey, params.RequestedByUserID, time.Now().UTC(), time.Now().UTC()}
			if kind == "other_unique" {
				params.IdempotencyKey += "-missing"
				idExpression = "?"
				args = []any{winner.job.ID, params.TargetID, f.policy.ExecutionHostID, params.IdempotencyKey, params.RequestedByUserID, time.Now().UTC(), time.Now().UTC()}
			}
			// This is the actual schema duplicate at the error-handling boundary.
			// The only interpolated SQL is one of the two fixed expressions above.
			_, insertErr := tx.ExecContext(f.ctx, `INSERT INTO system_update_jobs (id,target_id,execution_host_id,target_service_type,deployment_mode,target_version,strategy,status,idempotency_key,requested_by_user_id,created_at,updated_at) VALUES (`+idExpression+`,?,?,'worker','systemd','v1.0.1','maintenance','succeeded',?,?,?,?)`, args...)
			var duplicate *mysql.MySQLError
			if !errors.As(insertErr, &duplicate) || duplicate.Number != 1062 {
				t.Fatal("schema did not produce the expected typed duplicate error")
			}
			got, created, err := store.ResolveSystemUpdatePortV2InsertErrorForTest(f.ctx, f.updates, tx, params, insertErr)
			if created {
				t.Fatal("insert-error resolution claimed to create a job")
			}
			if _, err := tx.ExecContext(f.ctx, `SELECT 1`); !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("duplicate resolution retained the losing transaction and read view")
			}
			switch kind {
			case "terminal_replay":
				if err != nil || got.ID != canceled.ID || got.Status != canceled.Status || !reflect.DeepEqual(got.PortReconfigure, canceled.PortReconfigure) || !reflect.DeepEqual(got.PortResult, canceled.PortResult) {
					t.Fatal("fresh duplicate replay did not return the committed terminal job and plan")
				}
			case "changed_payload":
				if !errors.Is(err, store.ErrSystemUpdatePortIdempotencyConflict) {
					t.Fatal("duplicate error with changed payload was not an idempotency conflict")
				}
			case "other_unique":
				if !errors.Is(err, insertErr) {
					t.Fatal("another unique constraint was misclassified as an idempotency replay")
				}
			}
			f.assertLaneCounts(t, 1, 1, 0, 0)
		})
	}
}
