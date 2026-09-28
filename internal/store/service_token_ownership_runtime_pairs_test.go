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

func TestMariaDBServiceTokenUpdaterOwnershipVsRuntimeRotationPairs(t *testing.T) {
	db, parent := openMariaDBServiceTokenTest(t)
	for suiteRepetition := 1; suiteRepetition <= 3; suiteRepetition++ {
		for _, pairCase := range mariaDBServiceTokenPolicyPairInventory() {
			if pairCase.kind != "runtime_rotation" {
				continue
			}
			ownershipOperation := pairCase.secondOperation
			for iteration := 1; iteration <= pairCase.iterations; iteration++ {
				t.Run(fmt.Sprintf("repeat_%d/%s_vs_runtime_claim_staged_credential/%d", suiteRepetition, ownershipOperation, iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 30*time.Second)
					defer cancel()
					cleanup := newMariaDBServiceTokenCleanup(t, ctx, db)
					runtimeFixture := newMariaDBServiceTokenPullFixtureWithCleanup(
						t, ctx, db, cleanup, "runtime-", false, nil,
					)
					runtimeActivated, err := runtimeFixture.policies.ActivatePullUpdaterOwnership(
						ctx, runtimeFixture.auth, runtimeFixture.updates, runtimeFixture.params,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipFixture := newMariaDBServiceTokenPullFixtureWithCleanup(
						t, ctx, db, cleanup, "ownership-", false, nil,
					)
					if ownershipOperation == "activate" {
						prepareMariaDBServiceTokenExistingHostActivation(t, ctx, &ownershipFixture)
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
					installMariaDBServiceTokenSystemUpdateJobLaneAnchors(
						t,
						ctx,
						db,
						cleanup,
						runtimeFixture.params.ExecutionHostID,
						ownershipFixture.params.ExecutionHostID,
					)
					runtimeOperation := prepareMariaDBServiceTokenRuntimeOperation(
						t,
						ctx,
						db,
						runtimeFixture,
						runtimeActivated,
						"claim_staged_credential",
					)
					runtimeExpected := mariaDBServiceTokenUpdaterRuntimeClaimExpectedCase()
					runtimePreState := snapshotMariaDBServiceTokenRuntimeSemanticState(
						t, ctx, db, runtimeFixture, runtimeOperation.rotationID,
					)
					assertMariaDBServiceTokenRuntimePreState(
						t, runtimeExpected, runtimeOperation, runtimePreState,
					)
					runtimeExpectedState, err := deriveMariaDBServiceTokenExpectedRuntimeState(
						runtimeExpected, runtimeOperation, runtimePreState,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipPreState := snapshotMariaDBServiceTokenOwnershipSemanticState(
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
					runtimeResult := make(chan mariaDBServiceTokenRuntimeOperationResult, 1)
					go func() {
						runtimeResult <- runtimeOperation.run(runtimeCtx)
					}()
					assertMariaDBServiceTokenRuntimeLockPhaseSequence(
						t,
						runtimeLockPhases,
						[]mariaDBRuntimeTokenRotationLockPhase{
							mariaDBRuntimeTokenRotationHostLocksHeld,
							mariaDBRuntimeTokenRotationRotationLocksHeld,
							mariaDBRuntimeTokenRotationLaneLocksHeld,
							mariaDBRuntimeTokenRotationPolicyLocksHeld,
						},
					)
					assertMariaDBServiceTokenPhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
					})
					assertMariaDBServiceTokenExecutionHostRowLockHeld(
						t, ctx, db, runtimeFixture.params.ExecutionHostID,
					)
					assertMariaDBServiceTokenRuntimeRotationRowLockHeld(
						t, ctx, db, runtimeOperation.rotationID,
					)
					assertMariaDBServiceTokenUpdaterPolicyRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)
					assertMariaDBServiceTokenServiceRowLockHeld(
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
					ownershipResult := make(chan mariaDBServiceTokenOwnershipResult, 1)
					go func() {
						if ownershipOperation == "activate" {
							result, err := ownershipFixture.policies.ActivatePullUpdaterOwnership(
								ownershipCtx,
								ownershipFixture.auth,
								ownershipFixture.updates,
								ownershipFixture.params,
							)
							ownershipResult <- mariaDBServiceTokenOwnershipResult{ownership: result.Ownership, err: err}
							return
						}
						result, err := ownershipFixture.policies.DeactivatePullUpdaterOwnership(
							ownershipCtx,
							ownershipFixture.auth,
							ownershipFixture.updates,
							deactivateMariaDBServiceTokenParams(ownershipActivated),
						)
						ownershipResult <- mariaDBServiceTokenOwnershipResult{ownership: result.Ownership, err: err}
					}()
					for index, want := range []mariaDBUpdaterPolicyLockPhase{
						mariaDBUpdaterPolicyBeforeHostLock,
						mariaDBUpdaterPolicyHostLockHeld,
						mariaDBUpdaterPolicyLaneLocksHeld,
						mariaDBUpdaterPolicyBeforePolicyLocks,
					} {
						got := receiveMariaDBServiceTokenUpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBServiceTokenExecutionHostRowLockHeld(
						t, ctx, db, ownershipFixture.params.ExecutionHostID,
					)
					assertMariaDBServiceTokenUpdaterPolicyRowLockHeld(
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

					assertMariaDBServiceTokenPhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenTokenLocksHeld,
					})
					assertMariaDBServiceTokenServiceTokenRowLockHeld(
						t, ctx, db, runtimeOperation.tokenID,
					)
					releaseRuntimeTokenLockOnce.Do(func() { close(releaseRuntimeTokenLock) })
					assertMariaDBServiceTokenPhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBindingsValidated,
					})
					runtimeOutcome := receiveMariaDBServiceTokenRuntimeOperationResult(
						t, runtimeResult, "runtime credential claim",
					)
					assertMariaDBServiceTokenOperationError(t, "runtime credential claim", runtimeOutcome.err)
					var ownershipOutcome mariaDBServiceTokenOwnershipResult
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
						got := receiveMariaDBServiceTokenUpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater released policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater released policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBServiceTokenPhaseSequence(t, ownershipServicePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
						mariaDBServiceTokenTokenLocksHeld,
						mariaDBServiceTokenBindingsValidated,
					})
					assertMariaDBServiceTokenStrongOwnershipFinalState(
						t,
						ctx,
						ownershipPreState,
						[]mariaDBServiceTokenOwnershipAction{{
							fixture:   ownershipFixture,
							operation: ownershipOperation,
							result:    ownershipOutcome.ownership,
						}},
						"",
						mariaDBServiceTokenMutationResult{},
					)
					assertMariaDBServiceTokenRuntimeExactFinalState(
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

func assertMariaDBServiceTokenExecutionHostRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	executionHostID string,
) {
	t.Helper()
	assertMariaDBServiceTokenRowLockHeld(
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

func assertMariaDBServiceTokenUpdaterPolicyRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBServiceTokenRowLockHeld(
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

func assertMariaDBServiceTokenServiceRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBServiceTokenRowLockHeld(
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

func assertMariaDBServiceTokenRuntimeRotationRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	rotationID string,
) {
	t.Helper()
	assertMariaDBServiceTokenRowLockHeld(
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

func assertMariaDBServiceTokenServiceTokenRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tokenID string,
) {
	t.Helper()
	assertMariaDBServiceTokenRowLockHeld(
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

func assertMariaDBServiceTokenRowLockHeld(
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
