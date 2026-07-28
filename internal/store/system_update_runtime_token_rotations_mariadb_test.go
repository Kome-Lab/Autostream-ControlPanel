package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBRuntimeTokenRotationLifecycleAndReplay(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	params, seal, unseal := mariaDBRuntimeTokenRotationStageParams(
		t, fixture, activated, "mariadb-rotation-lifecycle-"+fixture.suffix,
	)
	staged, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth, fixture.policies, params, seal,
	)
	if err != nil || !staged.Created {
		t.Fatalf("stage = %#v err=%v", staged, err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || !blocked {
		t.Fatalf("staged identity mutation fence = %v, %v", blocked, err)
	}
	var tokenHash, ciphertext, nonce string
	if err := db.QueryRowContext(ctx, `SELECT staged_token_hash,
staged_token_ciphertext, staged_token_nonce
FROM system_update_runtime_token_rotations
WHERE id = ?`, staged.Rotation.ID).Scan(&tokenHash, &ciphertext, &nonce); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth, fixture.policies, params, seal,
	)
	if err != nil || replayed.Created ||
		replayed.Rotation.ID != staged.Rotation.ID {
		t.Fatalf("stage replay = %#v err=%v", replayed, err)
	}
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	policy.PollIntervalSeconds++
	if _, err := fixture.policies.SavePullUpdaterPolicy(
		ctx, fixture.updates, params.ServiceID, policy.Revision,
		params.ExpectedOwnershipEpoch, policy,
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationBusy) {
		t.Fatalf("active rotation policy mutation error = %v", err)
	}
	preClaimRaw, err := unseal(ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		ctx, fixture.auth, fixture.policies,
		store.MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: params.ExecutionHostID,
			ExpectedRevision: 1, RawStagedToken: preClaimRaw,
			Now: params.Now.Add(time.Second),
		},
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationTransition) {
		t.Fatalf("local stage accepted before claim: %v", err)
	}
	claimParams := store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
		RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
		ExecutionHostID:              params.ExecutionHostID,
		AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
		ClaimID:                      "10000000-0000-4000-8000-000000000001",
		ExpectedRevision:             1,
		Now:                          params.Now.Add(time.Second),
	}
	for name, invalid := range map[string]store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
		"wrong host": func() store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ExecutionHostID = "host-wrong-" + fixture.suffix
			return value
		}(),
		"wrong service": func() store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ServiceID = "agent-wrong-" + fixture.suffix
			return value
		}(),
		"wrong previous token": func() store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.AuthenticatedPreviousTokenID = staged.Rotation.StagedTokenID
			return value
		}(),
		"wrong revision": func() store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ExpectedRevision = 99
			return value
		}(),
	} {
		t.Run("claim "+name, func(t *testing.T) {
			if _, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
				ctx, fixture.auth, fixture.policies, invalid, unseal,
			); err == nil {
				t.Fatalf("%s claim unexpectedly succeeded", name)
			}
		})
	}
	var credentialClaimedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT credential_claimed_at
FROM system_update_runtime_token_rotations WHERE id = ?`,
		staged.Rotation.ID,
	).Scan(&credentialClaimedAt); err != nil {
		t.Fatal(err)
	}
	if credentialClaimedAt.Valid {
		t.Fatal("failed claim left a durable claim marker")
	}
	if _, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies, claimParams,
		func(_, _ string) (string, error) {
			return "", errors.New("test unseal failure")
		},
	); err == nil {
		t.Fatal("unseal failure unexpectedly claimed credential")
	}
	if err := db.QueryRowContext(ctx, `SELECT credential_claimed_at
FROM system_update_runtime_token_rotations WHERE id = ?`,
		staged.Rotation.ID,
	).Scan(&credentialClaimedAt); err != nil {
		t.Fatal(err)
	}
	if credentialClaimedAt.Valid {
		t.Fatal("unseal failure left a durable claim marker")
	}
	claimed, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies, claimParams, unseal,
	)
	if err != nil || !claimed.Claimed || claimed.Rotation.Revision != 2 ||
		claimed.Token.RawToken == "" ||
		claimed.Token.ID != staged.Rotation.StagedTokenID {
		t.Fatalf("claim = %#v err=%v", claimed, err)
	}
	rawStagedToken := claimed.Token.RawToken
	if tokenHash == rawStagedToken ||
		ciphertext == rawStagedToken ||
		nonce == rawStagedToken {
		t.Fatal("raw staged token was persisted")
	}
	if _, err := fixture.auth.AuthenticateServiceToken(
		ctx, rawStagedToken, "updates.claim",
	); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("staged token authenticated before activation: %v", err)
	}
	claimReplay, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies, claimParams, unseal,
	)
	if err != nil || claimReplay.Claimed ||
		claimReplay.Token.RawToken != rawStagedToken {
		t.Fatalf("claim replay = %#v err=%v", claimReplay, err)
	}
	differentClaim := claimParams
	differentClaim.ClaimID = "10000000-0000-4000-8000-000000000002"
	if _, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies, differentClaim, unseal,
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationCredentialClaimed) {
		t.Fatalf("different claim ID error = %v", err)
	}
	currentRevisionClaim := claimParams
	currentRevisionClaim.ExpectedRevision = claimed.Rotation.Revision
	if _, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies, currentRevisionClaim, unseal,
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationStale) {
		t.Fatalf("current revision reclaimed credential: %v", err)
	}
	active, err := fixture.updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		ctx, params.ExecutionHostID,
	)
	if err != nil || active.ID != staged.Rotation.ID ||
		active.CredentialClaimedAt == nil {
		t.Fatalf("active rotation = %#v err=%v", active, err)
	}
	local, applied, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		ctx, fixture.auth, fixture.policies,
		store.MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: params.ExecutionHostID,
			ExpectedRevision: 2, RawStagedToken: rawStagedToken,
			Now: params.Now.Add(2 * time.Second),
		},
	)
	if err != nil || !applied || local.Revision != 3 ||
		local.LocalStageReceiptID != "staged-token:"+staged.Rotation.StagedTokenID {
		t.Fatalf("local stage = %#v applied=%v err=%v", local, applied, err)
	}
	proofParams := readyMariaDBRuntimeTokenRotationHeartbeatProof(
		t, ctx, db, fixture, params, local, rawStagedToken,
		params.Now.Add(4*time.Second),
	)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE services SET last_heartbeat_at = ?, updated_at = ? WHERE service_id = ?`,
		local.LocalStageAcknowledgedAt,
		local.LocalStageAcknowledgedAt,
		params.ServiceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx, fixture.auth, fixture.policies, proofParams,
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationHeartbeatProof) {
		t.Fatalf("equal local-stage heartbeat proof error = %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE services SET last_heartbeat_at = ?, updated_at = ? WHERE service_id = ?`,
		proofParams.Now,
		proofParams.Now,
		params.ServiceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx, fixture.auth, fixture.policies,
		func() store.ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
			value := proofParams
			value.RawStagedToken = "ast_svc_wrong"
			return value
		}(),
	); !errors.Is(err, store.ErrSystemUpdateRuntimeTokenRotationToken) {
		t.Fatalf("wrong token proof error = %v", err)
	}
	proved, applied, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx, fixture.auth, fixture.policies, proofParams,
	)
	if err != nil || !applied || proved.Revision != 4 {
		t.Fatalf("prove = %#v applied=%v err=%v", proved, applied, err)
	}
	if replay, applied, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx, fixture.auth, fixture.policies, proofParams,
	); err != nil || applied || replay.Revision != proved.Revision {
		t.Fatalf("proof replay = %#v applied=%v err=%v", replay, applied, err)
	}
	activatedRotation, applied, err := fixture.updates.ActivateSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth, store.ActivateSystemUpdateRuntimeTokenRotationParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: params.ExecutionHostID,
			ExpectedRevision: 4, RawStagedToken: rawStagedToken,
			Now: params.Now.Add(6 * time.Second),
		},
	)
	if err != nil || !applied ||
		activatedRotation.Status != store.SystemUpdateRuntimeTokenRotationActivated ||
		activatedRotation.Revision != 5 {
		t.Fatalf("activate = %#v applied=%v err=%v", activatedRotation, applied, err)
	}
	if replay, applied, err := fixture.updates.ActivateSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth, store.ActivateSystemUpdateRuntimeTokenRotationParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: params.ExecutionHostID,
			ExpectedRevision: 4, RawStagedToken: rawStagedToken,
			Now: params.Now.Add(7 * time.Second),
		},
	); err != nil || applied || replay.Revision != activatedRotation.Revision {
		t.Fatalf("activation replay = %#v applied=%v err=%v", replay, applied, err)
	}
	var (
		terminalCipher, terminalNonce, terminalClaimHash sql.NullString
		terminalClaimRevision                            sql.NullInt64
	)
	if err := db.QueryRowContext(ctx, `SELECT staged_token_ciphertext,
staged_token_nonce, credential_claim_id_sha256, credential_claim_revision
FROM system_update_runtime_token_rotations WHERE id = ?`,
		staged.Rotation.ID,
	).Scan(
		&terminalCipher, &terminalNonce,
		&terminalClaimHash, &terminalClaimRevision,
	); err != nil {
		t.Fatal(err)
	}
	if terminalCipher.Valid || terminalNonce.Valid ||
		terminalClaimHash.Valid || terminalClaimRevision.Valid {
		t.Fatalf(
			"activated replay secrets survived: cipher=%#v nonce=%#v claim=%#v/%#v",
			terminalCipher, terminalNonce,
			terminalClaimHash, terminalClaimRevision,
		)
	}
	if token, err := fixture.auth.AuthenticateServiceToken(
		ctx, rawStagedToken, "updates.claim",
	); err != nil || token.ID != staged.Rotation.StagedTokenID {
		t.Fatalf("activated token = %#v err=%v", token, err)
	}
	var oldRevoked, newRevoked bool
	if err := db.QueryRowContext(ctx, `SELECT
(SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?),
(SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?)`,
		staged.Rotation.PreviousTokenID, staged.Rotation.StagedTokenID,
	).Scan(&oldRevoked, &newRevoked); err != nil {
		t.Fatal(err)
	}
	if !oldRevoked || newRevoked {
		t.Fatalf("activation revoke state old=%v new=%v", oldRevoked, newRevoked)
	}
	if _, err := fixture.updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		ctx, params.ExecutionHostID,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("activated rotation remained active: %v", err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || !blocked {
		t.Fatalf("pre-heartbeat activated identity fence = %v, %v", blocked, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE services
SET status = 'online',
    last_heartbeat_at = node_token_rotated_at,
    updated_at = node_token_rotated_at
WHERE service_id = ?`, params.ServiceID); err != nil {
		t.Fatal(err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || !blocked {
		t.Fatalf("equal-timestamp heartbeat identity fence = %v, %v", blocked, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE services
SET status = 'online',
    last_heartbeat_at = DATE_ADD(node_token_rotated_at, INTERVAL 1 SECOND),
    updated_at = DATE_ADD(node_token_rotated_at, INTERVAL 1 SECOND)
WHERE service_id = ?`, params.ServiceID); err != nil {
		t.Fatal(err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || blocked {
		t.Fatalf("fresh-token heartbeat identity fence = %v, %v", blocked, err)
	}
}

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

func TestMariaDBEmergencyConfigureRetryActivatesWithRevokedAnchor(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	params, rotationSeal, _ := mariaDBRuntimeTokenRotationStageParams(
		t,
		fixture,
		activated,
		"mariadb-emergency-configure-"+fixture.suffix,
	)
	stagedRotation, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
		ctx,
		fixture.auth,
		fixture.policies,
		params,
		rotationSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	emergencyAt := params.Now.Add(time.Second)
	emergency, applied, err :=
		fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
			ctx,
			fixture.auth,
			store.EmergencyRevokeSystemUpdateRuntimeTokenParams{
				RotationID:       stagedRotation.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: stagedRotation.Rotation.Revision,
				TokenID:          stagedRotation.Rotation.PreviousTokenID,
				Now:              emergencyAt,
			},
		)
	if err != nil || !applied ||
		emergency.Status != store.SystemUpdateRuntimeTokenRotationCanceled {
		t.Fatalf("emergency=%#v applied=%v err=%v", emergency, applied, err)
	}
	var revokedBefore time.Time
	if err := db.QueryRowContext(
		ctx,
		`SELECT revoked_at FROM service_tokens WHERE id = ?`,
		stagedRotation.Rotation.PreviousTokenID,
	).Scan(&revokedBefore); err != nil {
		t.Fatal(err)
	}
	if recovery, err := fixture.updates.IsSystemUpdateEmergencyIdentityRecovery(
		ctx,
		fixture.auth,
		params.ServiceID,
	); err != nil || !recovery {
		t.Fatalf("initial emergency recovery=%v err=%v", recovery, err)
	}
	beforePolicy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}

	configureSeal := store.NodeTokenSealer(func(raw string) (string, string, error) {
		return "sealed:" + security.HashToken(raw), "configure-nonce", nil
	})
	firstConfigureToken := "first-emergency-configure-" + fixture.suffix
	if _, err := fixture.auth.SetServiceConfigureToken(
		ctx,
		params.ServiceID,
		security.HashToken(firstConfigureToken),
		emergencyAt.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	abandoned, err := fixture.auth.StageServiceNodeConfiguration(
		ctx,
		params.ServiceID,
		firstConfigureToken,
		emergencyAt.Add(time.Second),
		configureSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery, err := fixture.updates.IsSystemUpdateEmergencyIdentityRecovery(
		ctx,
		fixture.auth,
		params.ServiceID,
	); err != nil || !recovery {
		t.Fatalf("staged emergency retry recovery=%v err=%v", recovery, err)
	}

	secondConfigureToken := "second-emergency-configure-" + fixture.suffix
	if _, err := fixture.auth.SetServiceConfigureToken(
		ctx,
		params.ServiceID,
		security.HashToken(secondConfigureToken),
		emergencyAt.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	tombstone, err := fixture.auth.GetService(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.StagedNodeTokenID != abandoned.Token.ID ||
		tombstone.StagedNodePreviousTokenID != "" ||
		tombstone.StagedNodeTokenHash != "" ||
		len(tombstone.StagedNodeTokenScopes) != 0 ||
		tombstone.StagedNodeTokenCiphertext != "" ||
		tombstone.StagedNodeTokenNonce != "" ||
		tombstone.StagedNodeActivationTokenHash != "" ||
		tombstone.StagedNodeTokenAt != nil {
		t.Fatalf("regenerated Configure Token retained staged secrets: %#v", tombstone)
	}
	replacement, err := fixture.auth.StageServiceNodeConfiguration(
		ctx,
		params.ServiceID,
		secondConfigureToken,
		emergencyAt.Add(2*time.Second),
		configureSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Token.ID == abandoned.Token.ID {
		t.Fatal("Configure retry reused the abandoned staged token")
	}
	active, service, alreadyActivated, err :=
		fixture.auth.ActivateServiceNodeConfiguration(
			ctx,
			params.ServiceID,
			replacement.Token.ID,
			replacement.ActivationToken,
			emergencyAt.Add(3*time.Second),
			store.ServiceRuntimeReport{Version: "v2.0.0"},
		)
	if err != nil || alreadyActivated ||
		active.ID != replacement.Token.ID ||
		service.TokenID != replacement.Token.ID {
		t.Fatalf(
			"emergency Configure activation token=%#v service=%#v replay=%v err=%v",
			active,
			service,
			alreadyActivated,
			err,
		)
	}
	if _, err := fixture.auth.AuthenticateServiceToken(
		ctx,
		replacement.Token.RawToken,
		"updates.claim",
	); err != nil {
		t.Fatalf("replacement runtime token is not active: %v", err)
	}
	if _, err := fixture.auth.AuthenticateServiceToken(
		ctx,
		abandoned.Token.RawToken,
		"updates.claim",
	); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("abandoned staged token became active: %v", err)
	}
	var revokedAfter time.Time
	if err := db.QueryRowContext(
		ctx,
		`SELECT revoked_at FROM service_tokens WHERE id = ?`,
		stagedRotation.Rotation.PreviousTokenID,
	).Scan(&revokedAfter); err != nil {
		t.Fatal(err)
	}
	if !revokedAfter.Equal(revokedBefore) {
		t.Fatalf(
			"emergency revocation timestamp changed: before=%s after=%s",
			revokedBefore,
			revokedAfter,
		)
	}
	afterPolicy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPolicy.Revision != beforePolicy.Revision ||
		afterPolicy.ProjectionRevision != beforePolicy.ProjectionRevision ||
		afterPolicy.LocalExecutorPolicyRevision !=
			beforePolicy.LocalExecutorPolicyRevision ||
		afterPolicy.LocalExecutorPolicySHA256 !=
			beforePolicy.LocalExecutorPolicySHA256 {
		t.Fatalf(
			"emergency Configure changed policy: before=%#v after=%#v",
			beforePolicy,
			afterPolicy,
		)
	}
}

func testMariaDBRuntimeTokenRotationEmergencyPhase(t *testing.T, phase string) {
	t.Helper()
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	params, seal, unseal := mariaDBRuntimeTokenRotationStageParams(
		t, fixture, activated,
		"mariadb-rotation-emergency-"+phase+"-"+fixture.suffix,
	)
	if _, err := fixture.auth.SetServiceConfigureToken(
		ctx,
		params.ServiceID,
		security.HashToken("configure-before-emergency-"+phase),
		params.Now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth, fixture.policies, params, seal,
	)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext, nonce string
	if err := db.QueryRowContext(ctx, `SELECT staged_token_ciphertext,
staged_token_nonce
FROM system_update_runtime_token_rotations
WHERE id = ?`, staged.Rotation.ID).Scan(&ciphertext, &nonce); err != nil {
		t.Fatal(err)
	}
	rawStagedToken, err := unseal(ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	current := staged.Rotation
	if phase != "staged" {
		claimed, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
			ctx, fixture.auth, fixture.policies,
			store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
				RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
				ExecutionHostID:              params.ExecutionHostID,
				AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
				ClaimID:                      "20000000-0000-4000-8000-000000000001",
				ExpectedRevision:             current.Revision,
				Now:                          params.Now.Add(time.Second),
			},
			unseal,
		)
		if err != nil {
			t.Fatal(err)
		}
		current = claimed.Rotation
		rawStagedToken = claimed.Token.RawToken
	}
	if phase == "local_staged" || phase == "heartbeat_proved" {
		local, _, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
			ctx, fixture.auth, fixture.policies,
			store.MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
				RotationID:       staged.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: current.Revision,
				RawStagedToken:   rawStagedToken,
				Now:              params.Now.Add(2 * time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		current = local
		if phase == "heartbeat_proved" {
			proof := readyMariaDBRuntimeTokenRotationHeartbeatProof(
				t, ctx, db, fixture, params, current, rawStagedToken,
				params.Now.Add(3*time.Second),
			)
			proved, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
				ctx, fixture.auth, fixture.policies, proof,
			)
			if err != nil {
				t.Fatal(err)
			}
			current = proved
		}
	}
	if phase == "cancel_requested" {
		cancelRequested, _, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
			ctx, fixture.auth,
			store.CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID:       staged.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: current.Revision,
				Now:              params.Now.Add(2 * time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		current = cancelRequested
	}

	request := store.EmergencyRevokeSystemUpdateRuntimeTokenParams{
		RotationID:       staged.Rotation.ID,
		ExecutionHostID:  params.ExecutionHostID,
		ExpectedRevision: current.Revision,
		TokenID:          staged.Rotation.PreviousTokenID,
		Now:              params.Now.Add(4 * time.Second),
	}
	emergency, applied, err := fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
		ctx, fixture.auth, request,
	)
	if err != nil || !applied ||
		emergency.Status != store.SystemUpdateRuntimeTokenRotationCanceled ||
		emergency.CanceledAt == nil ||
		emergency.EmergencyRevokedTokenID != staged.Rotation.PreviousTokenID {
		t.Fatalf(
			"emergency = %#v applied=%v err=%v",
			emergency, applied, err,
		)
	}
	replayed, applied, err := fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
		ctx, fixture.auth, request,
	)
	if err != nil || applied || replayed.Revision != emergency.Revision {
		t.Fatalf("emergency replay = %#v applied=%v err=%v", replayed, applied, err)
	}

	var (
		status                         string
		cipher, storedNonce, claimHash sql.NullString
		claimRevision                  sql.NullInt64
		canceledAt, emergencyAt        sql.NullTime
		activeHost                     sql.NullString
	)
	if err := db.QueryRowContext(ctx, `SELECT status, staged_token_ciphertext,
staged_token_nonce, credential_claim_id_sha256, credential_claim_revision,
canceled_at, emergency_revoked_at, active_execution_host_id
FROM system_update_runtime_token_rotations
WHERE id = ?`, staged.Rotation.ID).Scan(
		&status, &cipher, &storedNonce, &claimHash, &claimRevision,
		&canceledAt, &emergencyAt, &activeHost,
	); err != nil {
		t.Fatal(err)
	}
	if status != store.SystemUpdateRuntimeTokenRotationCanceled ||
		cipher.Valid || storedNonce.Valid || claimHash.Valid ||
		claimRevision.Valid || !canceledAt.Valid || !emergencyAt.Valid ||
		activeHost.Valid {
		t.Fatalf(
			"terminal durable state status=%s cipher=%#v nonce=%#v claim=%#v/%#v canceled=%#v emergency=%#v active=%#v",
			status, cipher, storedNonce, claimHash, claimRevision,
			canceledAt, emergencyAt, activeHost,
		)
	}
	var previousRevoked, stagedRevoked bool
	if err := db.QueryRowContext(ctx, `SELECT
(SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?),
(SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?)`,
		staged.Rotation.PreviousTokenID,
		staged.Rotation.StagedTokenID,
	).Scan(&previousRevoked, &stagedRevoked); err != nil {
		t.Fatal(err)
	}
	if !previousRevoked || !stagedRevoked {
		t.Fatalf(
			"emergency tokens previous=%v staged=%v",
			previousRevoked, stagedRevoked,
		)
	}
	var (
		serviceStatus, capabilities string
		lastHeartbeat               sql.NullTime
		nodeCipher, nodeNonce       sql.NullString
		configureHash               sql.NullString
		configureExpires            sql.NullTime
		configureUsed               sql.NullTime
	)
	if err := db.QueryRowContext(ctx, `SELECT status, last_heartbeat_at,
reported_capabilities, node_token_ciphertext, node_token_nonce,
configure_token_hash, configure_token_expires_at, configure_token_used_at
FROM services WHERE service_id = ?`, params.ServiceID).Scan(
		&serviceStatus, &lastHeartbeat, &capabilities, &nodeCipher, &nodeNonce,
		&configureHash, &configureExpires, &configureUsed,
	); err != nil {
		t.Fatal(err)
	}
	if serviceStatus != "offline" || lastHeartbeat.Valid ||
		capabilities != "{}" || nodeCipher.Valid || nodeNonce.Valid ||
		configureHash.Valid || configureExpires.Valid || configureUsed.Valid {
		t.Fatalf(
			"emergency service status=%s heartbeat=%#v capabilities=%q cipher=%#v nonce=%#v configure=%#v/%#v/%#v",
			serviceStatus, lastHeartbeat, capabilities, nodeCipher, nodeNonce,
			configureHash, configureExpires, configureUsed,
		)
	}
	if _, err := fixture.updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		ctx, params.ExecutionHostID,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("emergency rotation kept active lane: %v", err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || blocked {
		t.Fatalf("emergency terminal identity fence = %v, %v", blocked, err)
	}
	if recovery, err := fixture.updates.IsSystemUpdateEmergencyIdentityRecovery(
		ctx, fixture.auth, params.ServiceID,
	); err != nil || !recovery {
		t.Fatalf("emergency manual identity recovery = %v, %v", recovery, err)
	}
	if _, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		ctx, fixture.auth, fixture.policies,
		store.ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
			RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
			ExecutionHostID:              params.ExecutionHostID,
			AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
			ClaimID:                      "20000000-0000-4000-8000-000000000002",
			ExpectedRevision:             emergency.Revision,
			Now:                          params.Now.Add(5 * time.Second),
		},
		unseal,
	); err == nil {
		t.Fatal("claim succeeded after emergency termination")
	}
	if _, _, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		ctx, fixture.auth, fixture.policies,
		store.MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID:       staged.Rotation.ID,
			ExecutionHostID:  params.ExecutionHostID,
			ExpectedRevision: emergency.Revision,
			RawStagedToken:   rawStagedToken,
			Now:              params.Now.Add(5 * time.Second),
		},
	); err == nil {
		t.Fatal("local stage succeeded after emergency termination")
	}
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	receiptID := current.LocalStageReceiptID
	if receiptID == "" {
		receiptID = "staged-token:" + staged.Rotation.StagedTokenID
	}
	if _, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		ctx, fixture.auth, fixture.policies,
		store.ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
			RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
			ExecutionHostID:  params.ExecutionHostID,
			ExpectedRevision: emergency.Revision,
			RawStagedToken:   rawStagedToken,
			Phase:            store.SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
			AgentVersion:     "v1.7.8", ExecutorVersion: "v1.7.8",
			AgentProtocolVersion: 2, ExecutorProtocolVersion: 1,
			MutationProtocolVersion:             1,
			ExpectedOwnershipEpoch:              params.ExpectedOwnershipEpoch,
			ExpectedSourcePolicyRevision:        params.ExpectedSourcePolicyRevision,
			ExpectedProjectionRevision:          params.ExpectedProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: params.ExpectedLocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
			LocalStageReceiptID:                 receiptID,
			Now:                                 params.Now.Add(5 * time.Second),
		},
	); err == nil {
		t.Fatal("heartbeat proof succeeded after emergency termination")
	}
	if _, _, err := fixture.updates.ActivateSystemUpdateRuntimeTokenRotation(
		ctx, fixture.auth,
		store.ActivateSystemUpdateRuntimeTokenRotationParams{
			RotationID:       staged.Rotation.ID,
			ExecutionHostID:  params.ExecutionHostID,
			ExpectedRevision: emergency.Revision,
			RawStagedToken:   rawStagedToken,
			Now:              params.Now.Add(5 * time.Second),
		},
	); err == nil {
		t.Fatal("activation succeeded after emergency termination")
	}
	if _, _, err := fixture.updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
		ctx, fixture.auth, fixture.policies,
		store.AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
			RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
			ExecutionHostID:              params.ExecutionHostID,
			AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
			ExpectedRevision:             emergency.Revision,
			Now:                          params.Now.Add(5 * time.Second),
		},
	); err == nil {
		t.Fatal("cancel acknowledgement succeeded after emergency termination")
	}
}

func mariaDBRuntimeTokenRotationStageParams(
	t *testing.T,
	fixture mariaDBPullActivationFixture,
	activated store.ActivatePullUpdaterOwnershipResult,
	idempotencyKey string,
) (
	store.StageSystemUpdateRuntimeTokenRotationParams,
	store.NodeTokenSealer,
	store.NodeTokenUnsealer,
) {
	t.Helper()
	key := "mariadb-runtime-token-rotation-key-" + fixture.suffix
	seal := store.NodeTokenSealer(func(raw string) (string, string, error) {
		return security.EncryptSecret(raw, key)
	})
	unseal := store.NodeTokenUnsealer(func(ciphertext, nonce string) (string, error) {
		return security.DecryptSecret(ciphertext, nonce, key)
	})
	return store.StageSystemUpdateRuntimeTokenRotationParams{
		ServiceID: fixture.params.ServiceID, ExecutionHostID: fixture.params.ExecutionHostID,
		IdempotencyKey:                      idempotencyKey,
		ExpectedOwnershipEpoch:              activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		Now:                                 time.Now().UTC(),
	}, seal, unseal
}

func readyMariaDBRuntimeTokenRotationHeartbeatProof(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBPullActivationFixture,
	stageParams store.StageSystemUpdateRuntimeTokenRotationParams,
	local store.SystemUpdateRuntimeTokenRotation,
	rawStagedToken string,
	now time.Time,
) store.ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
	t.Helper()
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, stageParams.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	const (
		agentVersion    = "v1.7.8"
		executorVersion = "v1.7.8"
	)
	capabilities, err := json.Marshal(map[string]any{
		"host_agent":                     true,
		"update_executor":                true,
		"mutation_enabled":               true,
		"recovery_pending":               false,
		"agent_version":                  agentVersion,
		"agent_protocol_version":         2,
		"executor_version":               executorVersion,
		"executor_protocol_version":      1,
		"mutation_protocol_version":      1,
		"execution_host_id":              stageParams.ExecutionHostID,
		"ownership_epoch":                stageParams.ExpectedOwnershipEpoch,
		"source_policy_revision":         stageParams.ExpectedSourcePolicyRevision,
		"projection_revision":            stageParams.ExpectedProjectionRevision,
		"local_executor_policy_revision": stageParams.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         local.LocalStageReceiptID,
		"local_phase":                    store.SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE services
SET status = 'online', last_heartbeat_at = ?, reported_version = ?,
    reported_capabilities = ?, updated_at = ?
WHERE service_id = ?`,
		now, agentVersion, capabilities, now, stageParams.ServiceID,
	); err != nil {
		t.Fatal(err)
	}
	return store.ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
		RotationID: local.ID, ServiceID: stageParams.ServiceID,
		ExecutionHostID:  stageParams.ExecutionHostID,
		ExpectedRevision: local.Revision, RawStagedToken: rawStagedToken,
		Phase:        store.SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
		AgentVersion: agentVersion, ExecutorVersion: executorVersion,
		AgentProtocolVersion: 2, ExecutorProtocolVersion: 1,
		MutationProtocolVersion:             1,
		ExpectedOwnershipEpoch:              stageParams.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        stageParams.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          stageParams.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: stageParams.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		LocalStageReceiptID:                 local.LocalStageReceiptID,
		Now:                                 now,
	}
}
