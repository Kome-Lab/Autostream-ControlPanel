package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"testing"
	"time"
)

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
