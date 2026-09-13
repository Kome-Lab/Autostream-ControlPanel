package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"testing"
	"time"
)

type mariaDBFIX005PullFixture struct {
	cleanup     *mariaDBFIX005Cleanup
	auth        MariaDBAuthStore
	policies    MariaDBUpdaterPolicyStore
	updates     *MariaDBSystemUpdateStore
	params      ActivatePullUpdaterOwnershipParams
	agentToken  ServiceToken
	targetToken ServiceToken
	targetID    string
	peerID      string
}

func newMariaDBFIX005PullFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	shareAgentToken bool,
) mariaDBFIX005PullFixture {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	return newMariaDBFIX005PullFixtureWithCleanup(
		t, ctx, db, cleanup, "", shareAgentToken, nil,
	)
}

func newMariaDBFIX005PullFixtureWithCleanup(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	cleanup *mariaDBFIX005Cleanup,
	fixtureSuffix string,
	shareAgentToken bool,
	sharedTargetToken *ServiceToken,
) mariaDBFIX005PullFixture {
	t.Helper()
	auth := NewMariaDBAuthStore(db)
	updates := NewMariaDBSystemUpdateStore(db)
	policies := NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	fixturePrefix := cleanup.prefix + strings.TrimSpace(fixtureSuffix)
	hostID := fixturePrefix + "host"
	peerID := fixturePrefix + "a-observer"
	peerHostID := fixturePrefix + "peer-host"
	targetID := fixturePrefix + "m-target"
	agentID := fixturePrefix + "z-pull"

	targetToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: targetID, ServiceType: "worker", ServiceName: targetID,
		PublicURL: "https://worker.example.com:18081",
	}, sharedTargetToken)
	cleanup.trackHostID(hostID)
	peerToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: peerID, ServiceType: "update_agent", ServiceName: peerID,
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: peerHostID, OwnershipEpoch: 0,
		Capabilities: map[string]any{"observe_only": true},
	}, nil)
	cleanup.trackPolicyID(agentID)

	var existing *ServiceToken
	if shareAgentToken {
		existing = &peerToken
	}
	agentToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: agentID, ServiceType: "update_agent", ServiceName: agentID,
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: hostID, OwnershipEpoch: 0,
		Capabilities: map[string]any{"observe_only": true},
	}, existing)
	policy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		agentID,
		0,
		0,
		UpdaterPolicy{
			TransportMode:             SystemUpdateTransportPullV2,
			ExecutionHostID:           hostID,
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []UpdaterPolicyTarget{{
				TargetID: targetID, ServiceID: targetID,
				ServiceType: "worker", DeploymentMode: "systemd", LocalListenPort: 18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           true,
		"update_executor":        true,
		"mutation_enabled":       false,
		"recovery_pending":       false,
		"transport_mode":         SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      hostID,
		"ownership_epoch":        int64(0),
		"policy_revision":        policy.ProjectionRevision,
		"policy_status":          "applied",
		"target_availability": map[string]any{
			targetID: "available",
		},
		"target_availability_codes": map[string]any{
			targetID: "executor_verified",
		},
		"reported_ports": map[string]any{
			targetID: int64(18081),
		},
		"port_drift": map[string]any{
			targetID: false,
		},
		"reported_service_types": map[string]any{
			targetID: "worker",
		},
		"reported_deployment_modes": map[string]any{
			targetID: "systemd",
		},
		"reported_executor_policy_revisions": map[string]any{
			targetID: policy.LocalExecutorPolicyRevision,
		},
		"reported_executor_policy_sha256": map[string]any{
			targetID: policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			targetID: int64(1),
		},
		"reported_config_sha256": map[string]any{
			targetID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	}
	if _, err := auth.Heartbeat(ctx, agentToken, ServiceHeartbeat{
		ServiceID: agentID, Status: "online", Version: "v1.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBFIX005PullFixture{
		cleanup: cleanup, auth: auth, policies: policies, updates: updates,
		agentToken: agentToken, targetToken: targetToken, targetID: targetID, peerID: peerID,
		params: ActivatePullUpdaterOwnershipParams{
			ServiceID:                           agentID,
			ExecutionHostID:                     hostID,
			ExpectedExecutionHostOwnershipEpoch: 0,
			ExpectedSourcePolicyRevision:        policy.Revision,
			ExpectedProjectionRevision:          policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		},
	}
}

func installMariaDBFIX009SystemUpdateJobLaneAnchors(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	cleanup *mariaDBFIX005Cleanup,
	executionHostIDs ...string,
) {
	t.Helper()
	terminalStatuses := []string{"canceled", "failed", "rolled_back", "succeeded"}
	for _, executionHostID := range sortedUniqueStrings(executionHostIDs) {
		anchorRows := make([]struct {
			executionHostID string
			status          string
		}, 0, len(terminalStatuses)+1)
		for _, status := range terminalStatuses {
			anchorRows = append(anchorRows, struct {
				executionHostID string
				status          string
			}{executionHostID: executionHostID, status: status})
		}
		anchorRows = append(anchorRows, struct {
			executionHostID string
			status          string
		}{executionHostID: executionHostID + "~fix009-upper", status: "canceled"})

		for index, anchor := range anchorRows {
			jobID := newUUID()
			cleanup.trackJobID(jobID)
			observedAt := time.Now().UTC().Add(time.Duration(index) * time.Microsecond)
			if _, err := db.ExecContext(ctx, `INSERT INTO system_update_jobs
(id, target_id, target_service_type, execution_host_id, deployment_mode, target_version, strategy, status, idempotency_key, requested_by_user_id, created_at, updated_at)
VALUES (?, ?, 'worker', ?, 'systemd', 'v0.0.0', 'immediate', ?, ?, ?, ?, ?)`,
				jobID,
				cleanup.prefix+"lane-anchor-"+jobID,
				anchor.executionHostID,
				anchor.status,
				"lane-anchor-"+jobID,
				cleanup.prefix,
				observedAt,
				observedAt,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func registerMariaDBFIX005Service(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	cleanup *mariaDBFIX005Cleanup,
	registration ServiceRegistration,
	existing *ServiceToken,
) ServiceToken {
	t.Helper()
	cleanup.trackServiceID(registration.ServiceID)
	var token ServiceToken
	if existing == nil {
		scopes := []string{"service.register", "service.heartbeat"}
		if registration.ServiceType == "update_agent" {
			scopes = append(
				scopes,
				"service.config.read",
				"updates.claim",
				"updates.report",
				"updates.authorize",
			)
		}
		var err error
		token, err = auth.CreateServiceToken(ctx, registration.ServiceType, scopes)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		token = *existing
	}
	cleanup.trackToken(token)
	registration.Version = "v1.0.0"
	if registration.Capabilities == nil {
		registration.Capabilities = map[string]any{}
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	return token
}

func deactivateMariaDBFIX005Params(
	activated ActivatePullUpdaterOwnershipResult,
) DeactivatePullUpdaterOwnershipParams {
	return DeactivatePullUpdaterOwnershipParams{
		ServiceID:                           activated.Service.ServiceID,
		ExecutionHostID:                     activated.Ownership.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activated.Policy.LocalExecutorPolicySHA256,
	}
}

func prepareMariaDBFIX006ExistingHostActivation(t *testing.T, ctx context.Context, fixture *mariaDBFIX005PullFixture) {
	t.Helper()
	// Initial activation creates the ownership row only at its final write.
	// These lock-pair tests probe an existing row before that write, so establish
	// a committed, deactivated host through the normal ownership lifecycle.
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(ctx, fixture.auth, fixture.updates, fixture.params)
	if err != nil {
		t.Fatalf("prepare existing host activation: %v", err)
	}
	deactivated, err := fixture.policies.DeactivatePullUpdaterOwnership(ctx, fixture.auth, fixture.updates, deactivateMariaDBFIX005Params(activated))
	if err != nil {
		t.Fatalf("prepare existing host deactivation: %v", err)
	}
	fixture.params.ExpectedExecutionHostOwnershipEpoch = deactivated.Ownership.OwnershipEpoch
	var persistedEpoch int64
	if err := fixture.updates.db.QueryRowContext(ctx, `SELECT ownership_epoch FROM system_update_execution_hosts WHERE execution_host_id = ?`, fixture.params.ExecutionHostID).Scan(&persistedEpoch); err != nil {
		t.Fatalf("read committed ownership before lock pair: %v", err)
	}
	if persistedEpoch != fixture.params.ExpectedExecutionHostOwnershipEpoch || deactivated.Service.OwnershipEpoch != 0 {
		t.Fatal("existing host activation fixture did not commit its deactivated ownership")
	}
}

func mariaDBFIX005RuntimeStageParams(
	fixture mariaDBFIX005PullFixture,
	activated ActivatePullUpdaterOwnershipResult,
	idempotencyKey string,
) (StageSystemUpdateRuntimeTokenRotationParams, NodeTokenSealer, NodeTokenUnsealer) {
	key := "fix006-runtime-key-" + fixture.cleanup.prefix
	seal := NodeTokenSealer(func(raw string) (string, string, error) {
		return security.EncryptSecret(raw, key)
	})
	unseal := NodeTokenUnsealer(func(ciphertext, nonce string) (string, error) {
		return security.DecryptSecret(ciphertext, nonce, key)
	})
	return StageSystemUpdateRuntimeTokenRotationParams{
		ServiceID: fixture.params.ServiceID, ExecutionHostID: fixture.params.ExecutionHostID,
		IdempotencyKey:                      idempotencyKey,
		ExpectedOwnershipEpoch:              activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		Now:                                 time.Now().UTC().Truncate(time.Microsecond),
	}, seal, unseal
}

func readyMariaDBFIX005RuntimeHeartbeatProof(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBFIX005PullFixture,
	stageParams StageSystemUpdateRuntimeTokenRotationParams,
	local SystemUpdateRuntimeTokenRotation,
	rawStagedToken string,
	now time.Time,
) ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
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
		"host_agent": true, "update_executor": true,
		"mutation_enabled": true, "recovery_pending": false,
		"agent_version": agentVersion, "agent_protocol_version": 2,
		"executor_version": executorVersion, "executor_protocol_version": 1,
		"mutation_protocol_version":      1,
		"execution_host_id":              stageParams.ExecutionHostID,
		"ownership_epoch":                stageParams.ExpectedOwnershipEpoch,
		"source_policy_revision":         stageParams.ExpectedSourcePolicyRevision,
		"projection_revision":            stageParams.ExpectedProjectionRevision,
		"local_executor_policy_revision": stageParams.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         local.LocalStageReceiptID,
		"local_phase":                    SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
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
	return ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
		RotationID: local.ID, ServiceID: stageParams.ServiceID,
		ExecutionHostID:  stageParams.ExecutionHostID,
		ExpectedRevision: local.Revision, RawStagedToken: rawStagedToken,
		Phase:        SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
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
