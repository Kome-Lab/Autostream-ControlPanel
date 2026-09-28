package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type mariaDBServiceTokenRuntimeOperation struct {
	name       string
	path       string
	tokenID    string
	rotationID string
	claimID    string
	stageTime  time.Time
	run        func(context.Context) mariaDBServiceTokenRuntimeOperationResult
}

type mariaDBServiceTokenRuntimeOperationResult struct {
	rotation SystemUpdateRuntimeTokenRotation
	result   string
	err      error
}

func TestMariaDBServiceTokenRuntimeTokenRotationMatrixInventory(t *testing.T) {
	seen := make(map[string]map[string]int)
	for _, testCase := range mariaDBServiceTokenRuntimeMatrixInventory() {
		if seen[testCase.path] == nil {
			seen[testCase.path] = make(map[string]int)
		}
		seen[testCase.path][testCase.tokenMutation]++
	}
	if len(seen) != 8 {
		t.Fatalf("runtime path count = %d, want 8", len(seen))
	}
	for path, mutations := range seen {
		for _, mutation := range []string{"rotate", "revoke"} {
			if mutations[mutation] != 1 {
				t.Fatalf("runtime matrix %s/%s count = %d, want 1", path, mutation, mutations[mutation])
			}
		}
	}
	if got := len(mariaDBServiceTokenRuntimeMatrixInventory()); got != 16 {
		t.Fatalf("runtime matrix pair count = %d, want 16", got)
	}
}

func TestMariaDBServiceTokenUpdaterRuntimeBarrierUsesObservedLockPhase(t *testing.T) {
	sourceBytes, err := readServiceTokenLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "TestMariaDBServiceTokenUpdaterOwnershipVsRuntimeRotationPairs("
	nextMarker := "func " + "assertMariaDBServiceTokenExecutionHostRowLockHeld("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("updater-vs-runtime test not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following updater-vs-runtime test not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	for _, forbidden := range []string{"ownershipStarted", "75 * time.Millisecond", "time.Sleep("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("updater-vs-runtime barrier still relies on %q", forbidden)
		}
	}
	for _, required := range []string{
		"mariaDBUpdaterPolicyLockObserver",
		"mariaDBUpdaterPolicyLaneLocksHeld",
		"mariaDBRuntimeTokenRotationLockObserver",
		"assertMariaDBServiceTokenRuntimeRotationRowLockHeld",
		"assertMariaDBServiceTokenServiceTokenRowLockHeld",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("updater-vs-runtime barrier does not require %q", required)
		}
	}
}

func TestMariaDBServiceTokenCleanupUsesExactTrackedIDs(t *testing.T) {
	sourceBytes, err := readServiceTokenLockOrderSource()
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "(fixture *mariaDBServiceTokenCleanup) cleanupInTransaction("
	nextMarker := "func " + "(fixture *mariaDBServiceTokenCleanup) residueCount("
	start := strings.Index(source, startMarker)
	end := strings.Index(source, nextMarker)
	if start < 0 || end <= start {
		t.Fatal("FIX-006 cleanup implementation source was not found")
	}
	body := source[start:end]
	if strings.Contains(body, " LIKE ") || strings.Contains(body, "fixture.prefix + \"%\"") {
		t.Fatal("FIX-006 cleanup still deletes rows by a prefix pattern")
	}
	for _, required := range []string{
		"serviceIDs",
		"trackedTokenIDs",
		"trackedStreamIDs",
		"assignmentIDs",
		"artifactIDs",
		"hostIDs",
		"policyIDs",
		"rotationIDs",
		"archiveMarkerStreamIDs",
		"retryMarkerStreamIDs",
		"auxiliaryServiceIDs",
		"auxiliaryStreamIDs",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("FIX-006 cleanup does not consume tracked registry %q", required)
		}
	}
}

func TestMariaDBServiceTokenRuntimeTokenRotationLockOrderMatrix(t *testing.T) {
	db, parent := openMariaDBServiceTokenTest(t)
	for _, testCase := range mariaDBServiceTokenRuntimeMatrixInventory() {
		testCase := testCase
		path := testCase.path
		mutation := testCase.tokenMutation
		for iteration := 1; iteration <= testCase.iterations; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", path, mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 30*time.Second)
				defer cancel()
				fixture := newMariaDBServiceTokenPullFixture(t, ctx, db, false)
				activated, err := fixture.policies.ActivatePullUpdaterOwnership(
					ctx, fixture.auth, fixture.updates, fixture.params,
				)
				if err != nil {
					t.Fatal(err)
				}
				operation := prepareMariaDBServiceTokenRuntimeOperation(
					t, ctx, db, fixture, activated, path,
				)
				preState := snapshotMariaDBServiceTokenRuntimeSemanticState(
					t, ctx, db, fixture, operation.rotationID,
				)
				assertMariaDBServiceTokenRuntimePreState(t, testCase, operation, preState)
				expectedState, err := deriveMariaDBServiceTokenExpectedRuntimeState(
					testCase, operation, preState,
				)
				if err != nil {
					t.Fatal(err)
				}
				blocker := lockMariaDBServiceTokenForTest(t, ctx, db, operation.tokenID)
				defer blocker.Rollback()

				runtimePhases := make(chan mariaDBServiceTokenLockPhase, 8)
				runtimeObserver := mariaDBServiceTokenLockObserver(func(
					observedOperation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if observedOperation == operation.name {
						runtimePhases <- phase
					}
				})
				runtimeLockPhases := make(chan mariaDBRuntimeTokenRotationLockPhase, 8)
				runtimeLockObserver := mariaDBRuntimeTokenRotationLockObserver(func(
					observedOperation string,
					phase mariaDBRuntimeTokenRotationLockPhase,
				) {
					if observedOperation == operation.name {
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
				go func() { runtimeResult <- operation.run(runtimeCtx) }()
				assertMariaDBServiceTokenRuntimeLockPhaseSequence(
					t, runtimeLockPhases, testCase.requiredLockPhases,
				)
				assertMariaDBServiceTokenPhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				assertMariaDBServiceTokenServiceRowLockHeld(t, ctx, db, fixture.params.ServiceID)

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationResult := startMariaDBServiceTokenTokenMutation(
					ctx, fixture.auth, operation.tokenID, mutation, mutationPhases,
				)
				assertMariaDBServiceTokenPhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
				})
				assertMariaDBServiceTokenServiceRowLockHeld(t, ctx, db, fixture.params.ServiceID)
				if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					t.Fatal(err)
				}

				runtimeOutcome := receiveMariaDBServiceTokenRuntimeOperationResult(t, runtimeResult, path)
				assertMariaDBServiceTokenOperationError(t, path, runtimeOutcome.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				fixture.cleanup.trackToken(mutationOutcome.token)
				assertMariaDBServiceTokenPhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})
				assertMariaDBServiceTokenMutationTail(t, mutationPhases, path == "activate")
				assertMariaDBServiceTokenRuntimeExactFinalState(
					t,
					ctx,
					db,
					fixture,
					expectedState,
					runtimeOutcome,
					mutationOutcome,
				)
			})
		}
	}
}

func prepareMariaDBServiceTokenRuntimeOperation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBServiceTokenPullFixture,
	activated ActivatePullUpdaterOwnershipResult,
	path string,
) mariaDBServiceTokenRuntimeOperation {
	t.Helper()
	stageParams, seal, unseal := mariaDBServiceTokenRuntimeStageParams(
		fixture, activated, fixture.cleanup.prefix+path,
	)
	stage := func() StageSystemUpdateRuntimeTokenRotationResult {
		result, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
			ctx, fixture.auth, fixture.policies, stageParams, seal,
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.cleanup.trackRotationID(result.Rotation.ID)
		fixture.cleanup.trackToken(ServiceToken{ID: result.Rotation.StagedTokenID})
		return result
	}
	var claimedID string
	claim := func(staged StageSystemUpdateRuntimeTokenRotationResult) ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult {
		claimedID = newUUID()
		result, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
			ctx,
			fixture.auth,
			fixture.policies,
			ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
				RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
				ExecutionHostID:              stageParams.ExecutionHostID,
				AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
				ClaimID:                      claimedID,
				ExpectedRevision:             staged.Rotation.Revision,
				Now:                          stageParams.Now.Add(time.Second),
			},
			unseal,
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	markLocal := func(
		staged StageSystemUpdateRuntimeTokenRotationResult,
		claimed ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult,
	) SystemUpdateRuntimeTokenRotation {
		result, _, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
			ctx,
			fixture.auth,
			fixture.policies,
			MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
				RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
				ExpectedRevision: claimed.Rotation.Revision,
				RawStagedToken:   claimed.Token.RawToken,
				Now:              stageParams.Now.Add(2 * time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	switch path {
	case "stage":
		return mariaDBServiceTokenRuntimeOperation{
			name: "stage_system_update_runtime_token_rotation", path: path,
			tokenID: activated.Service.TokenID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				result, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
					callCtx, fixture.auth, fixture.policies, stageParams, seal,
				)
				fixture.cleanup.trackRotationID(result.Rotation.ID)
				fixture.cleanup.trackToken(ServiceToken{ID: result.Rotation.StagedTokenID})
				operationResult := "existing"
				if result.Created {
					operationResult = "created"
				}
				return mariaDBServiceTokenRuntimeOperationResult{
					rotation: result.Rotation, result: operationResult, err: err,
				}
			},
		}
	case "claim_staged_credential":
		staged := stage()
		claimID := newUUID()
		return mariaDBServiceTokenRuntimeOperation{
			name: "claim_system_update_runtime_token_rotation_credential", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				result, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
					callCtx,
					fixture.auth,
					fixture.policies,
					ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
						RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
						ExecutionHostID:              stageParams.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ClaimID:                      claimID,
						ExpectedRevision:             staged.Rotation.Revision,
						Now:                          stageParams.Now.Add(time.Second),
					},
					unseal,
				)
				operationResult := "duplicate"
				if result.Claimed {
					operationResult = "claimed"
				}
				return mariaDBServiceTokenRuntimeOperationResult{
					rotation: result.Rotation, result: operationResult, err: err,
				}
			},
		}
	case "local_staged", "heartbeat_proof", "activate":
		staged := stage()
		claimed := claim(staged)
		if path == "local_staged" {
			return mariaDBServiceTokenRuntimeOperation{
				name: "mark_system_update_runtime_token_rotation_local_staged", path: path,
				tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
				claimID: claimedID, stageTime: stageParams.Now,
				run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
					rotation, applied, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
						callCtx,
						fixture.auth,
						fixture.policies,
						MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
							RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
							ExpectedRevision: claimed.Rotation.Revision,
							RawStagedToken:   claimed.Token.RawToken,
							Now:              stageParams.Now.Add(2 * time.Second),
						},
					)
					return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
				},
			}
		}
		local := markLocal(staged, claimed)
		proof := readyMariaDBServiceTokenRuntimeHeartbeatProof(
			t, ctx, db, fixture, stageParams, local, claimed.Token.RawToken,
			stageParams.Now.Add(3*time.Second),
		)
		if path == "heartbeat_proof" {
			return mariaDBServiceTokenRuntimeOperation{
				name: "prove_system_update_runtime_token_rotation_heartbeat", path: path,
				tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
				claimID: claimedID, stageTime: stageParams.Now,
				run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
					rotation, applied, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
						callCtx, fixture.auth, fixture.policies, proof,
					)
					return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
				},
			}
		}
		proved, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
			ctx, fixture.auth, fixture.policies, proof,
		)
		if err != nil {
			t.Fatal(err)
		}
		return mariaDBServiceTokenRuntimeOperation{
			name: "activate_system_update_runtime_token_rotation", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimedID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				rotation, applied, err := fixture.updates.ActivateSystemUpdateRuntimeTokenRotation(
					callCtx,
					fixture.auth,
					ActivateSystemUpdateRuntimeTokenRotationParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: proved.Revision,
						RawStagedToken:   claimed.Token.RawToken,
						Now:              stageParams.Now.Add(4 * time.Second),
					},
				)
				return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "cancel":
		staged := stage()
		return mariaDBServiceTokenRuntimeOperation{
			name: "cancel_system_update_runtime_token_rotation", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				rotation, applied, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
					callCtx,
					fixture.auth,
					CancelSystemUpdateRuntimeTokenRotationParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: staged.Rotation.Revision,
						Now:              stageParams.Now.Add(time.Second),
					},
				)
				return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "acknowledge_cancel":
		staged := stage()
		claimed := claim(staged)
		canceled, _, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
			ctx,
			fixture.auth,
			CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
				ExpectedRevision: claimed.Rotation.Revision,
				Now:              stageParams.Now.Add(time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return mariaDBServiceTokenRuntimeOperation{
			name: "acknowledge_system_update_runtime_token_rotation_cancel", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimedID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				rotation, applied, err := fixture.updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
					callCtx,
					fixture.auth,
					fixture.policies,
					AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
						RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
						ExecutionHostID:              stageParams.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ExpectedRevision:             canceled.Revision,
						Now:                          stageParams.Now.Add(2 * time.Second),
					},
				)
				return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "emergency_revoke":
		staged := stage()
		return mariaDBServiceTokenRuntimeOperation{
			name: "emergency_revoke_system_update_runtime_token", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBServiceTokenRuntimeOperationResult {
				rotation, applied, err := fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
					callCtx,
					fixture.auth,
					EmergencyRevokeSystemUpdateRuntimeTokenParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: staged.Rotation.Revision,
						TokenID:          staged.Rotation.PreviousTokenID,
						Now:              stageParams.Now.Add(time.Second),
					},
				)
				return mariaDBServiceTokenRuntimeTransitionResult(rotation, applied, err)
			},
		}
	default:
		t.Fatalf("unknown runtime path %q", path)
		return mariaDBServiceTokenRuntimeOperation{}
	}
}

func mariaDBServiceTokenRuntimeTransitionResult(
	rotation SystemUpdateRuntimeTokenRotation,
	applied bool,
	err error,
) mariaDBServiceTokenRuntimeOperationResult {
	result := "no_op"
	if applied {
		result = "applied"
	}
	return mariaDBServiceTokenRuntimeOperationResult{rotation: rotation, result: result, err: err}
}

func receiveMariaDBServiceTokenRuntimeOperationResult(
	t *testing.T,
	result <-chan mariaDBServiceTokenRuntimeOperationResult,
	label string,
) mariaDBServiceTokenRuntimeOperationResult {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return mariaDBServiceTokenRuntimeOperationResult{}
	}
}

func assertMariaDBServiceTokenMutationTail(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	expectReferenceSetRetry bool,
) {
	t.Helper()
	assertMariaDBServiceTokenPhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
		mariaDBServiceTokenServiceLocksHeld,
		mariaDBServiceTokenBeforeTokenLocks,
		mariaDBServiceTokenTokenLocksHeld,
	})
	if expectReferenceSetRetry {
		assertMariaDBServiceTokenPhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
			mariaDBServiceTokenBeforeServiceLocks,
			mariaDBServiceTokenServiceLocksHeld,
			mariaDBServiceTokenBeforeTokenLocks,
			mariaDBServiceTokenTokenLocksHeld,
			mariaDBServiceTokenBindingsValidated,
		})
		select {
		case phase := <-phases:
			t.Fatalf("unexpected token mutation phase after reference-set retry %q", phase)
		default:
		}
		return
	}
	select {
	case phase := <-phases:
		if phase != mariaDBServiceTokenBindingsValidated {
			t.Fatalf("unexpected token mutation tail phase %q", phase)
		}
	default:
	}
}
