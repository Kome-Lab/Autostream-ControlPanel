package store_test

import (
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
	"sync"
	"testing"
	"time"
)

func TestMariaDBRuntimeTokenRotationNormalCancellationScrubsReplaySecrets(t *testing.T) {
	for _, claimedBeforeCancel := range []bool{false, true} {
		name := "unclaimed immediate"
		if claimedBeforeCancel {
			name = "claimed acknowledged"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx := openMariaDBPullActivationTest(t)
			fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
			activated, err := fixture.policies.ActivatePullUpdaterOwnership(
				ctx, fixture.auth, fixture.updates, fixture.params,
			)
			if err != nil {
				t.Fatal(err)
			}
			params, seal, unseal := mariaDBRuntimeTokenRotationStageParams(
				t,
				fixture,
				activated,
				"mariadb-normal-cancel-"+fixture.suffix,
			)
			staged, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
				ctx, fixture.auth, fixture.policies, params, seal,
			)
			if err != nil {
				t.Fatal(err)
			}
			current := staged.Rotation
			if claimedBeforeCancel {
				claimed, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
					ctx, fixture.auth, fixture.policies,
					store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
						RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
						ExecutionHostID:              params.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ClaimID:                      "10000000-0000-4000-8000-000000000006",
						ExpectedRevision:             current.Revision,
						Now:                          params.Now.Add(time.Second),
					},
					unseal,
				)
				if err != nil {
					t.Fatal(err)
				}
				current = claimed.Rotation
			}
			cancelParams := store.CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID:       staged.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: current.Revision,
				Now:              params.Now.Add(2 * time.Second),
			}
			canceled, applied, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
				ctx, fixture.auth, cancelParams,
			)
			if err != nil || !applied {
				t.Fatalf("cancel = %#v applied=%v err=%v", canceled, applied, err)
			}
			if claimedBeforeCancel {
				if canceled.Status != store.SystemUpdateRuntimeTokenRotationCancelRequested {
					t.Fatalf("cancel request = %#v", canceled)
				}
				ackParams := store.AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
					RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
					ExecutionHostID:              params.ExecutionHostID,
					AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
					ExpectedRevision:             canceled.Revision,
					Now:                          params.Now.Add(3 * time.Second),
				}
				canceled, applied, err = fixture.updates.
					AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
						ctx, fixture.auth, fixture.policies, ackParams,
					)
				if err != nil || !applied {
					t.Fatalf(
						"cancel acknowledgement = %#v applied=%v err=%v",
						canceled,
						applied,
						err,
					)
				}
				replayed, replayApplied, replayErr := fixture.updates.
					AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
						ctx, fixture.auth, fixture.policies, ackParams,
					)
				if replayErr != nil || replayApplied ||
					replayed.Revision != canceled.Revision {
					t.Fatalf(
						"cancel acknowledgement replay = %#v applied=%v err=%v",
						replayed,
						replayApplied,
						replayErr,
					)
				}
			} else {
				replayed, replayApplied, replayErr :=
					fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
						ctx, fixture.auth, cancelParams,
					)
				if replayErr != nil || replayApplied ||
					replayed.Revision != canceled.Revision {
					t.Fatalf(
						"cancel replay = %#v applied=%v err=%v",
						replayed,
						replayApplied,
						replayErr,
					)
				}
			}
			if canceled.Status != store.SystemUpdateRuntimeTokenRotationCanceled {
				t.Fatalf("terminal cancel = %#v", canceled)
			}
			var (
				ciphertext sql.NullString
				nonce      sql.NullString
				claimHash  sql.NullString
				claimRev   sql.NullInt64
			)
			if err := db.QueryRowContext(
				ctx,
				`SELECT staged_token_ciphertext, staged_token_nonce,
credential_claim_id_sha256, credential_claim_revision
FROM system_update_runtime_token_rotations WHERE id = ?`,
				staged.Rotation.ID,
			).Scan(&ciphertext, &nonce, &claimHash, &claimRev); err != nil {
				t.Fatal(err)
			}
			if ciphertext.Valid || nonce.Valid || claimHash.Valid || claimRev.Valid {
				t.Fatalf(
					"normal cancel replay secrets survived: cipher=%v nonce=%v claim_hash=%v claim_rev=%v",
					ciphertext.Valid,
					nonce.Valid,
					claimHash.Valid,
					claimRev.Valid,
				)
			}
		})
	}
}

func TestMariaDBRuntimeTokenRotationEmergencyAndTwoConnectionFence(t *testing.T) {
	for _, phase := range []string{
		"staged",
		"claimed",
		"local_staged",
		"heartbeat_proved",
		"cancel_requested",
	} {
		t.Run("emergency "+phase, func(t *testing.T) {
			testMariaDBRuntimeTokenRotationEmergencyPhase(t, phase)
		})
	}

	t.Run("rotation and job serialize on execution host", func(t *testing.T) {
		db, ctx := openMariaDBPullActivationTest(t)
		db.SetMaxOpenConns(8)
		fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
		activated, err := fixture.policies.ActivatePullUpdaterOwnership(
			ctx, fixture.auth, fixture.updates, fixture.params,
		)
		if err != nil {
			t.Fatal(err)
		}
		params, seal, _ := mariaDBRuntimeTokenRotationStageParams(
			t, fixture, activated, "mariadb-rotation-race-"+fixture.suffix,
		)
		secondDB, err := database.OpenFromEnv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = secondDB.Close() })
		secondDB.SetMaxOpenConns(4)
		secondUpdates := store.NewMariaDBSystemUpdateStore(secondDB)

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
				ctx, fixture.auth, fixture.policies, params, seal,
			)
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _, err := secondUpdates.CreateSystemUpdateJob(
				ctx, store.CreateSystemUpdateJobParams{
					TargetID: fixture.targetID, TargetServiceType: "worker",
					Operation:       store.SystemUpdateOperationSoftwareUpdate,
					AgentServiceID:  fixture.params.ServiceID,
					ExecutionHostID: fixture.params.ExecutionHostID,
					DeploymentMode:  "systemd", CurrentVersion: "v1.0.0",
					TargetVersion:     "v1.1.0",
					Strategy:          store.SystemUpdateStrategyWhenIdle,
					IdempotencyKey:    "mariadb-job-race-" + fixture.suffix,
					RequestedByUserID: "mariadb-admin",
				},
			)
			results <- err
		}()
		close(start)
		wg.Wait()
		close(results)
		successes, fenced := 0, 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationBusy),
				errors.Is(err, store.ErrSystemUpdateExecutionHostBusy):
				fenced++
			default:
				t.Fatalf("unexpected race error: %v", err)
			}
		}
		if successes != 1 || fenced != 1 {
			t.Fatalf("race results: successes=%d fenced=%d", successes, fenced)
		}
	})
}
