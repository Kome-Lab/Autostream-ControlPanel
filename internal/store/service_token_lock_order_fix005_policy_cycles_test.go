package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func assertMariaDBFIX005PhaseSequence(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	expected []mariaDBServiceTokenLockPhase,
) {
	t.Helper()
	for index, want := range expected {
		got := receiveMariaDBServiceTokenPhase(t, phases, fmt.Sprintf("phase %d", index+1))
		if got != want {
			t.Fatalf("lock phase %d = %q, want %q", index+1, got, want)
		}
	}
}

func receiveMariaDBFIX006UpdaterPolicyPhase(
	t *testing.T,
	phases <-chan mariaDBUpdaterPolicyLockPhase,
	label string,
) mariaDBUpdaterPolicyLockPhase {
	t.Helper()
	select {
	case phase := <-phases:
		return phase
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not reach its bounded barrier", label)
		return ""
	}
}

type mariaDBFIX005OwnershipResult struct {
	ownership SystemUpdateExecutionHost
	err       error
}

func TestFIX009ReusedSchemaPairSuitesFenceJobLanesAndRepeatInternally(t *testing.T) {
	sourceBytes, err := readFIX005LockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	anchorCall := "installMariaDBFIX009SystemUpdate" + "JobLaneAnchors("
	if count := strings.Count(source, anchorCall); count != 3 {
		t.Fatalf("FIX-009 job-lane anchor definition/call count = %d, want 3", count)
	}
	repeatLoop := "for suiteRepetition := 1; suiteRepetition <= 3;" + " suiteRepetition++"
	if count := strings.Count(source, repeatLoop); count != 2 {
		t.Fatalf("FIX-009 internal suite repeat count = %d, want 2", count)
	}
	for _, anchor := range []string{
		`terminalStatuses := []string{"canceled", "failed", "rolled_back", "succeeded"}`,
		`executionHostID + "~fix009-upper"`,
		"cleanup.trackJobID(jobID)",
	} {
		if !strings.Contains(source, anchor) {
			t.Fatalf("FIX-009 reused-schema lane anchor is missing %q", anchor)
		}
	}
}

func TestMariaDBFIX006PolicyCyclePairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for suiteRepetition := 1; suiteRepetition <= 3; suiteRepetition++ {
		for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
			if pairCase.kind != "cycle" {
				continue
			}
			pairCase := pairCase
			pair := struct {
				first  string
				second string
			}{first: pairCase.firstOperation, second: pairCase.secondOperation}
			for iteration := 1; iteration <= pairCase.iterations; iteration++ {
				t.Run(fmt.Sprintf("repeat_%d/%s_vs_%s/%d", suiteRepetition, pair.first, pair.second, iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 30*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					firstFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "cycle-a-", false, nil,
					)
					secondFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t,
						ctx,
						db,
						cleanup,
						"cycle-b-",
						false,
						&firstFixture.targetToken,
					)
					if firstFixture.targetToken.ID == "" ||
						firstFixture.targetToken.ID != secondFixture.targetToken.ID {
						t.Fatalf(
							"policy cycle fixture does not share its target token: first=%q second=%q",
							firstFixture.targetToken.ID,
							secondFixture.targetToken.ID,
						)
					}
					if pair.first == "activate" {
						prepareMariaDBFIX006ExistingHostActivation(t, ctx, &firstFixture)
					}
					if pair.second == "activate" {
						prepareMariaDBFIX006ExistingHostActivation(t, ctx, &secondFixture)
					}
					firstPlan, err := firstFixture.policies.discoverMariaDBPullUpdaterOwnershipLockPlan(
						ctx,
						firstFixture.auth,
						firstFixture.params.ServiceID,
						firstFixture.params.ExecutionHostID,
					)
					if err != nil {
						t.Fatal(err)
					}
					closure := make(map[string]struct{}, len(firstPlan.ServiceIDs))
					for _, serviceID := range firstPlan.ServiceIDs {
						closure[serviceID] = struct{}{}
					}
					for _, serviceID := range []string{firstFixture.targetID, secondFixture.targetID} {
						if _, exists := closure[serviceID]; !exists {
							t.Fatalf("shared-token closure omitted service %q: %#v", serviceID, firstPlan.ServiceIDs)
						}
					}
					var firstActivated, secondActivated ActivatePullUpdaterOwnershipResult
					if pair.first == "deactivate" {
						firstActivated, err = firstFixture.policies.ActivatePullUpdaterOwnership(
							ctx, firstFixture.auth, firstFixture.updates, firstFixture.params,
						)
						if err != nil {
							t.Fatal(err)
						}
					}
					if pair.second == "deactivate" {
						secondActivated, err = secondFixture.policies.ActivatePullUpdaterOwnership(
							ctx, secondFixture.auth, secondFixture.updates, secondFixture.params,
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
						firstFixture.params.ExecutionHostID,
						secondFixture.params.ExecutionHostID,
					)
					ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
						t, ctx, firstFixture, secondFixture,
					)

					firstOperation := pair.first + "_pull_updater_ownership"
					firstPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					firstServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					releaseFirst := make(chan struct{})
					var releaseOnce sync.Once
					defer releaseOnce.Do(func() { close(releaseFirst) })
					firstPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation != firstOperation {
							return
						}
						firstPolicyPhases <- phase
						if phase == mariaDBUpdaterPolicyPolicyLocksHeld {
							select {
							case <-releaseFirst:
							case <-ctx.Done():
							}
						}
					})
					firstServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == firstOperation {
							firstServicePhases <- phase
						}
					})
					firstCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							firstPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						firstServiceObserver,
					)
					firstResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						firstResult <- runMariaDBFIX006OwnershipOperation(
							firstCtx, firstFixture, pair.first, firstActivated,
						)
					}()
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						firstPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyBeforeHostLock,
							mariaDBUpdaterPolicyHostLockHeld,
							mariaDBUpdaterPolicyLaneLocksHeld,
							mariaDBUpdaterPolicyBeforePolicyLocks,
							mariaDBUpdaterPolicyPolicyLocksHeld,
						},
					)
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, firstFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, secondFixture.params.ServiceID,
					)

					secondOperation := pair.second + "_pull_updater_ownership"
					secondPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					secondServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					secondPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation == secondOperation {
							secondPolicyPhases <- phase
						}
					})
					secondServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == secondOperation {
							secondServicePhases <- phase
						}
					})
					secondCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							secondPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						secondServiceObserver,
					)
					secondResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						secondResult <- runMariaDBFIX006OwnershipOperation(
							secondCtx, secondFixture, pair.second, secondActivated,
						)
					}()
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						secondPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyBeforeHostLock,
							mariaDBUpdaterPolicyHostLockHeld,
							mariaDBUpdaterPolicyLaneLocksHeld,
							mariaDBUpdaterPolicyBeforePolicyLocks,
						},
					)
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, secondFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, firstFixture.params.ServiceID,
					)

					releaseOnce.Do(func() { close(releaseFirst) })
					firstOutcome := receiveMariaDBFIX006OwnershipResult(t, firstResult, "first ownership")
					secondOutcome := receiveMariaDBFIX006OwnershipResult(t, secondResult, "second ownership")
					assertMariaDBServiceTokenOperationError(t, pair.first, firstOutcome.err)
					assertMariaDBServiceTokenOperationError(t, pair.second, secondOutcome.err)
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						firstPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{mariaDBUpdaterPolicyPolicySetRevalidated},
					)
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						secondPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyPolicyLocksHeld,
							mariaDBUpdaterPolicyPolicySetRevalidated,
						},
					)
					for _, phases := range []<-chan mariaDBServiceTokenLockPhase{
						firstServicePhases,
						secondServicePhases,
					} {
						assertMariaDBFIX005PhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
							mariaDBServiceTokenBeforeServiceLocks,
							mariaDBServiceTokenServiceLocksHeld,
							mariaDBServiceTokenBeforeTokenLocks,
							mariaDBServiceTokenTokenLocksHeld,
							mariaDBServiceTokenBindingsValidated,
						})
					}
					assertMariaDBFIX007StrongOwnershipFinalState(
						t,
						ctx,
						ownershipPreState,
						[]mariaDBFIX007OwnershipAction{
							{fixture: firstFixture, operation: pair.first, result: firstOutcome.ownership},
							{fixture: secondFixture, operation: pair.second, result: secondOutcome.ownership},
						},
						"",
						mariaDBServiceTokenMutationResult{},
					)
				})
			}
		}
	}
}

func runMariaDBFIX006OwnershipOperation(
	ctx context.Context,
	fixture mariaDBFIX005PullFixture,
	operation string,
	activated ActivatePullUpdaterOwnershipResult,
) mariaDBFIX005OwnershipResult {
	if operation == "activate" {
		result, err := fixture.policies.ActivatePullUpdaterOwnership(
			ctx, fixture.auth, fixture.updates, fixture.params,
		)
		return mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
	}
	result, err := fixture.policies.DeactivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		deactivateMariaDBFIX005Params(activated),
	)
	return mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
}

func assertMariaDBFIX006UpdaterPolicyPhaseSequence(
	t *testing.T,
	phases <-chan mariaDBUpdaterPolicyLockPhase,
	expected []mariaDBUpdaterPolicyLockPhase,
) {
	t.Helper()
	for index, want := range expected {
		got := receiveMariaDBFIX006UpdaterPolicyPhase(
			t, phases, fmt.Sprintf("updater policy phase %d", index+1),
		)
		if got != want {
			t.Fatalf("updater policy phase %d = %q, want %q", index+1, got, want)
		}
	}
}

func receiveMariaDBFIX006OwnershipResult(
	t *testing.T,
	result <-chan mariaDBFIX005OwnershipResult,
	label string,
) mariaDBFIX005OwnershipResult {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return mariaDBFIX005OwnershipResult{}
	}
}

func TestMariaDBFIX006UpdaterOwnershipVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
		if pairCase.kind != "token_mutation" {
			continue
		}
		ownershipOperation := pairCase.firstOperation
		tokenMutation := pairCase.secondOperation
		for iteration := 1; iteration <= pairCase.iterations; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", ownershipOperation, tokenMutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 25*time.Second)
				defer cancel()
				fixture := newMariaDBFIX005PullFixture(t, ctx, db, true)
				var activated ActivatePullUpdaterOwnershipResult
				if ownershipOperation == "deactivate" {
					var err error
					activated, err = fixture.policies.ActivatePullUpdaterOwnership(
						ctx, fixture.auth, fixture.updates, fixture.params,
					)
					if err != nil {
						t.Fatal(err)
					}
				}
				ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
					t, ctx, fixture,
				)

				blocker := lockMariaDBServiceTokenForTest(t, ctx, db, fixture.agentToken.ID)
				defer blocker.Rollback()
				ownershipPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				observedOwnershipOperation := ownershipOperation + "_pull_updater_ownership"
				ownershipObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == observedOwnershipOperation {
						ownershipPhases <- phase
					}
				})
				policyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
				policyObserver := mariaDBUpdaterPolicyLockObserver(func(
					operation string,
					phase mariaDBUpdaterPolicyLockPhase,
				) {
					if operation == observedOwnershipOperation {
						policyPhases <- phase
					}
				})
				ownershipCtx := context.WithValue(
					context.WithValue(
						ctx,
						mariaDBUpdaterPolicyLockObserverContextKey{},
						policyObserver,
					),
					mariaDBServiceTokenLockObserverContextKey{},
					ownershipObserver,
				)
				ownershipResult := make(chan mariaDBFIX005OwnershipResult, 1)
				go func() {
					if ownershipOperation == "activate" {
						result, err := fixture.policies.ActivatePullUpdaterOwnership(
							ownershipCtx, fixture.auth, fixture.updates, fixture.params,
						)
						ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
						return
					}
					result, err := fixture.policies.DeactivatePullUpdaterOwnership(
						ownershipCtx,
						fixture.auth,
						fixture.updates,
						deactivateMariaDBFIX005Params(activated),
					)
					ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
				}()
				for index, want := range []mariaDBUpdaterPolicyLockPhase{
					mariaDBUpdaterPolicyBeforeHostLock,
					mariaDBUpdaterPolicyHostLockHeld,
					mariaDBUpdaterPolicyLaneLocksHeld,
					mariaDBUpdaterPolicyBeforePolicyLocks,
					mariaDBUpdaterPolicyPolicyLocksHeld,
					mariaDBUpdaterPolicyPolicySetRevalidated,
				} {
					got := receiveMariaDBFIX006UpdaterPolicyPhase(
						t, policyPhases, fmt.Sprintf("ownership policy phase %d", index+1),
					)
					if got != want {
						t.Fatalf("ownership policy phase %d = %q, want %q", index+1, got, want)
					}
				}
				assertMariaDBFIX005PhaseSequence(t, ownershipPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				for _, serviceID := range []string{
					fixture.peerID,
					fixture.targetID,
					fixture.params.ServiceID,
				} {
					assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, serviceID)
				}

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationResult := startMariaDBFIX005TokenMutation(
					ctx,
					fixture.auth,
					fixture.agentToken.ID,
					tokenMutation,
					mutationPhases,
				)
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
				})
				assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, fixture.peerID)
				if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					t.Fatal(err)
				}

				var ownershipOutcome mariaDBFIX005OwnershipResult
				select {
				case ownershipOutcome = <-ownershipResult:
				case <-time.After(10 * time.Second):
					t.Fatal("updater ownership operation did not complete")
				}
				assertMariaDBServiceTokenOperationError(t, ownershipOperation, ownershipOutcome.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, tokenMutation, mutationOutcome.err)
				fixture.cleanup.trackToken(mutationOutcome.token)
				assertMariaDBFIX005PhaseSequence(t, ownershipPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
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
						fixture: fixture, operation: ownershipOperation, result: ownershipOutcome.ownership,
					}},
					tokenMutation,
					mutationOutcome,
				)
			})
		}
	}
}

func startMariaDBFIX005TokenMutation(
	ctx context.Context,
	auth MariaDBAuthStore,
	tokenID, mutation string,
	phases chan<- mariaDBServiceTokenLockPhase,
) <-chan mariaDBServiceTokenMutationResult {
	operation := mutation + "_service_token"
	observer := mariaDBServiceTokenLockObserver(func(
		observedOperation string,
		phase mariaDBServiceTokenLockPhase,
	) {
		if observedOperation == operation {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	result := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		if mutation == "rotate" {
			rotated, err := auth.RotateServiceToken(mutationCtx, tokenID)
			result <- mariaDBServiceTokenMutationResult{token: rotated, err: err}
			return
		}
		result <- mariaDBServiceTokenMutationResult{err: auth.RevokeServiceToken(mutationCtx, tokenID)}
	}()
	return result
}
