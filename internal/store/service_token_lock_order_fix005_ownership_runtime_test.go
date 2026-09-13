package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"sync"
	"testing"
	"time"
)

func TestMariaDBFIX006UpdaterOwnershipVsRuntimeRotationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for suiteRepetition := 1; suiteRepetition <= 3; suiteRepetition++ {
		for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
			if pairCase.kind != "runtime_rotation" {
				continue
			}
			ownershipOperation := pairCase.secondOperation
			for iteration := 1; iteration <= pairCase.iterations; iteration++ {
				t.Run(fmt.Sprintf("repeat_%d/%s_vs_runtime_claim_staged_credential/%d", suiteRepetition, ownershipOperation, iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 30*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					runtimeFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "runtime-", false, nil,
					)
					runtimeActivated, err := runtimeFixture.policies.ActivatePullUpdaterOwnership(
						ctx, runtimeFixture.auth, runtimeFixture.updates, runtimeFixture.params,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "ownership-", false, nil,
					)
					if ownershipOperation == "activate" {
						prepareMariaDBFIX006ExistingHostActivation(t, ctx, &ownershipFixture)
					}
					var ownershipActivated ActivatePullUpdaterOwnershipResult
					if ownershipOperation == "deactivate" {
						ownershipActivated, err = ownershipFixture.policies.ActivatePullUpdaterOwnership(
							ctx, ownershipFixture.auth, ownershipFixture.updates, ownershipFixture.params,
						)
						if err != nil {
							t.Fatal(err)
						}
					}
					installMariaDBFIX009SystemUpdateJobLaneAnchors(
						t,
						ctx,
						db,
						cleanup,
						runtimeFixture.params.ExecutionHostID,
						ownershipFixture.params.ExecutionHostID,
					)
					runtimeOperation := prepareMariaDBFIX005RuntimeOperation(
						t,
						ctx,
						db,
						runtimeFixture,
						runtimeActivated,
						"claim_staged_credential",
					)
					runtimeExpected := mariaDBFIX007UpdaterRuntimeClaimExpectedCase()
					runtimePreState := snapshotMariaDBFIX007RuntimeSemanticState(
						t, ctx, db, runtimeFixture, runtimeOperation.rotationID,
					)
					assertMariaDBFIX007RuntimePreState(
						t, runtimeExpected, runtimeOperation, runtimePreState,
					)
					runtimeExpectedState, err := deriveMariaDBFIX008ExpectedRuntimeState(
						runtimeExpected, runtimeOperation, runtimePreState,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
						t, ctx, ownershipFixture,
					)
					blocker := lockMariaDBServiceTokenForTest(t, ctx, db, runtimeOperation.tokenID)
					defer blocker.Rollback()
					runtimePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					releaseRuntimeTokenLock := make(chan struct{})
					var releaseRuntimeTokenLockOnce sync.Once
					defer releaseRuntimeTokenLockOnce.Do(func() { close(releaseRuntimeTokenLock) })
					runtimeObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == runtimeOperation.name {
							runtimePhases <- phase
							if phase == mariaDBServiceTokenTokenLocksHeld {
								select {
								case <-releaseRuntimeTokenLock:
								case <-ctx.Done():
								}
							}
						}
					})
					runtimeLockPhases := make(chan mariaDBRuntimeTokenRotationLockPhase, 8)
					runtimeLockObserver := mariaDBRuntimeTokenRotationLockObserver(func(
						observedOperation string,
						phase mariaDBRuntimeTokenRotationLockPhase,
					) {
						if observedOperation == runtimeOperation.name {
							runtimeLockPhases <- phase
						}
					})
					runtimeCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBRuntimeTokenRotationLockObserverContextKey{},
							runtimeLockObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						runtimeObserver,
					)
					runtimeResult := make(chan mariaDBFIX007RuntimeOperationResult, 1)
					go func() {
						runtimeResult <- runtimeOperation.run(runtimeCtx)
					}()
					assertMariaDBFIX007RuntimeLockPhaseSequence(
						t,
						runtimeLockPhases,
						[]mariaDBRuntimeTokenRotationLockPhase{
							mariaDBRuntimeTokenRotationHostLocksHeld,
							mariaDBRuntimeTokenRotationRotationLocksHeld,
							mariaDBRuntimeTokenRotationLaneLocksHeld,
							mariaDBRuntimeTokenRotationPolicyLocksHeld,
						},
					)
					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
					})
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, runtimeFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX007RuntimeRotationRowLockHeld(
						t, ctx, db, runtimeOperation.rotationID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)
					assertMariaDBFIX006ServiceRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)

					observedOwnershipOperation := ownershipOperation + "_pull_updater_ownership"
					ownershipPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					ownershipPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation == observedOwnershipOperation {
							ownershipPolicyPhases <- phase
						}
					})
					ownershipServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					ownershipServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == observedOwnershipOperation {
							ownershipServicePhases <- phase
						}
					})
					ownershipCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							ownershipPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						ownershipServiceObserver,
					)
					ownershipResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						if ownershipOperation == "activate" {
							result, err := ownershipFixture.policies.ActivatePullUpdaterOwnership(
								ownershipCtx,
								ownershipFixture.auth,
								ownershipFixture.updates,
								ownershipFixture.params,
							)
							ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
							return
						}
						result, err := ownershipFixture.policies.DeactivatePullUpdaterOwnership(
							ownershipCtx,
							ownershipFixture.auth,
							ownershipFixture.updates,
							deactivateMariaDBFIX005Params(ownershipActivated),
						)
						ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
					}()
					for index, want := range []mariaDBUpdaterPolicyLockPhase{
						mariaDBUpdaterPolicyBeforeHostLock,
						mariaDBUpdaterPolicyHostLockHeld,
						mariaDBUpdaterPolicyLaneLocksHeld,
						mariaDBUpdaterPolicyBeforePolicyLocks,
					} {
						got := receiveMariaDBFIX006UpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, ownershipFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)
					select {
					case phase := <-ownershipPolicyPhases:
						t.Fatalf("updater advanced past the contended policy phase before release: %q", phase)
					case outcome := <-ownershipResult:
						t.Fatalf("updater completed before the contended policy lock was released: %#v", outcome)
					default:
					}
					if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
						t.Fatal(err)
					}

					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenTokenLocksHeld,
					})
					assertMariaDBFIX007ServiceTokenRowLockHeld(
						t, ctx, db, runtimeOperation.tokenID,
					)
					releaseRuntimeTokenLockOnce.Do(func() { close(releaseRuntimeTokenLock) })
					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBindingsValidated,
					})
					runtimeOutcome := receiveMariaDBFIX007RuntimeOperationResult(
						t, runtimeResult, "runtime credential claim",
					)
					assertMariaDBServiceTokenOperationError(t, "runtime credential claim", runtimeOutcome.err)
					var ownershipOutcome mariaDBFIX005OwnershipResult
					select {
					case ownershipOutcome = <-ownershipResult:
					case <-time.After(10 * time.Second):
						t.Fatalf("%s did not complete", ownershipOperation)
					}
					assertMariaDBServiceTokenOperationError(t, ownershipOperation, ownershipOutcome.err)
					for index, want := range []mariaDBUpdaterPolicyLockPhase{
						mariaDBUpdaterPolicyPolicyLocksHeld,
						mariaDBUpdaterPolicyPolicySetRevalidated,
					} {
						got := receiveMariaDBFIX006UpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater released policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater released policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBFIX005PhaseSequence(t, ownershipServicePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
						mariaDBServiceTokenTokenLocksHeld,
						mariaDBServiceTokenBindingsValidated,
					})
					assertMariaDBFIX007StrongOwnershipFinalState(
						t,
						ctx,
						ownershipPreState,
						[]mariaDBFIX007OwnershipAction{{
							fixture:   ownershipFixture,
							operation: ownershipOperation,
							result:    ownershipOutcome.ownership,
						}},
						"",
						mariaDBServiceTokenMutationResult{},
					)
					assertMariaDBFIX007RuntimeExactFinalState(
						t,
						ctx,
						db,
						runtimeFixture,
						runtimeExpectedState,
						runtimeOutcome,
						mariaDBServiceTokenMutationResult{},
					)
				})
			}
		}
	}
}

func assertMariaDBFIX006ExecutionHostRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	executionHostID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT execution_host_id
FROM system_update_execution_hosts
WHERE execution_host_id = ?
FOR UPDATE NOWAIT`,
		executionHostID,
		"execution host",
	)
}

func assertMariaDBFIX006UpdaterPolicyRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT service_id FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE NOWAIT`,
		serviceID,
		"updater policy",
	)
}

func assertMariaDBFIX006ServiceRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT service_id FROM services
WHERE service_id = ?
FOR UPDATE NOWAIT`,
		serviceID,
		"service",
	)
}

func assertMariaDBFIX007RuntimeRotationRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	rotationID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT id FROM system_update_runtime_token_rotations
WHERE id = ?
FOR UPDATE NOWAIT`,
		rotationID,
		"runtime rotation",
	)
}

func assertMariaDBFIX007ServiceTokenRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tokenID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT id FROM service_tokens
WHERE id = ?
FOR UPDATE NOWAIT`,
		tokenID,
		"service token",
	)
}

func assertMariaDBFIX006RowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	query, id, label string,
) {
	t.Helper()
	probe, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	err = probe.QueryRowContext(ctx, query, id).Scan(&lockedID)
	_ = probe.Rollback()
	if err == nil {
		t.Fatalf("%s %s was not locked at the observed phase", label, id)
	}
	var driverErr *mysql.MySQLError
	if errors.As(err, &driverErr) && (driverErr.Number == 1205 || driverErr.Number == 3572) {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s %s disappeared before the lock barrier", label, id)
	}
	t.Fatalf("probe %s row lock: %v", label, err)
}
