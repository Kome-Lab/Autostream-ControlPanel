package store_test

import (
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"testing"
	"time"
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
