package store

import (
	"fmt"
	"strings"
	"time"
)

type mariaDBFIX008RuntimeMutationCase struct {
	name            string
	path            string
	tokenMutation   string
	diagnosticField string
	mutate          func(*mariaDBFIX008RuntimeActualState)
}

func mariaDBFIX008RuntimeCase(path, tokenMutation string) (mariaDBFIX007RuntimeMatrixCase, bool) {
	for _, testCase := range mariaDBFIX007RuntimeMatrixInventory() {
		if testCase.path == path && testCase.tokenMutation == tokenMutation {
			return testCase, true
		}
	}
	return mariaDBFIX007RuntimeMatrixCase{}, false
}

func mariaDBFIX008RuntimeOracleFixture(
	path, tokenMutation string,
) (mariaDBFIX008ExpectedRuntimeState, mariaDBFIX008RuntimeActualState, error) {
	testCase, exists := mariaDBFIX008RuntimeCase(path, tokenMutation)
	if !exists {
		return mariaDBFIX008ExpectedRuntimeState{}, mariaDBFIX008RuntimeActualState{}, fmt.Errorf(
			"runtime case %s/%s does not exist",
			path,
			tokenMutation,
		)
	}
	stageTime := time.Unix(1_700_000_000, 0).UTC()
	heartbeatTime := stageTime.Add(-time.Second)
	service := RegisteredService{
		ServiceID: "agent-service", ServiceType: "update_agent",
		ExecutionHostID: "host-1", TransportMode: SystemUpdateTransportPullV2,
		OwnershipEpoch: 11, TokenID: "pre-operation-token", Status: "online",
		LastHeartbeatAt: &heartbeatTime, ReportedCapabilities: map[string]any{"ready": true},
	}
	ownership := SystemUpdateExecutionHost{
		ExecutionHostID: "host-1", TransportMode: SystemUpdateTransportPullV2,
		AgentServiceID: service.ServiceID,
		OwnershipEpoch: 11, PolicyRevision: 23,
	}
	policy := UpdaterPolicy{
		UpdaterID: service.ServiceID, Revision: 19, ProjectionRevision: 23,
		LocalExecutorPolicyRevision: 29, TransportMode: SystemUpdateTransportPullV2,
		ExecutionHostID: ownership.ExecutionHostID, LocalExecutorPolicySHA256: strings.Repeat("a", 64),
	}
	pre := mariaDBFIX007RuntimeSemanticSnapshot{
		service: service, ownership: ownership, policy: policy,
		services: map[string]RegisteredService{service.ServiceID: service},
		tokens: map[string]ServiceToken{
			"pre-operation-token": {ID: "pre-operation-token", ServiceType: "update_agent"},
			"pre-staged-token":    {ID: "pre-staged-token", ServiceType: "update_agent"},
		},
	}
	operation := mariaDBFIX005RuntimeOperation{
		path: path, tokenID: service.TokenID, stageTime: stageTime,
	}
	if path != "stage" {
		pre.rotationCount = 1
		pre.rotationExists = true
		pre.rotation = SystemUpdateRuntimeTokenRotation{
			ID: "pre-rotation", ServiceID: service.ServiceID,
			ExecutionHostID: ownership.ExecutionHostID,
			Status:          testCase.expected.PreRotationStatus, Revision: testCase.expected.PreRevision,
			ExpectedOwnershipEpoch:              ownership.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        policy.Revision,
			ExpectedProjectionRevision:          policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			PreviousTokenID:                     service.TokenID, StagedTokenID: "pre-staged-token",
		}
		operation.rotationID = pre.rotation.ID
	}
	if testCase.expected.ClaimFencePresent {
		operation.claimID = "claim-1"
	}
	expected, err := deriveMariaDBFIX008ExpectedRuntimeState(testCase, operation, pre)
	if err != nil {
		return mariaDBFIX008ExpectedRuntimeState{}, mariaDBFIX008RuntimeActualState{}, err
	}
	actual, err := mariaDBFIX008RuntimeActualFixture(expected)
	if err != nil {
		return mariaDBFIX008ExpectedRuntimeState{}, mariaDBFIX008RuntimeActualState{}, err
	}
	return expected, actual, nil
}

func mariaDBFIX008RuntimeActualFixture(
	expected mariaDBFIX008ExpectedRuntimeState,
) (mariaDBFIX008RuntimeActualState, error) {
	actual := mariaDBFIX008RuntimeActualState{
		OperationResult: expected.OperationResult,
		OperationRotation: SystemUpdateRuntimeTokenRotation{
			ID: "generated-rotation", StagedTokenID: "generated-staged-token",
		},
		RotationCount:  expected.RotationCount,
		RotationExists: expected.RotationExists,
		Ownership:      expected.PreOwnership,
		Policy:         expected.PrePolicy,
		Services:       make(map[string]RegisteredService),
		Tokens:         make(map[string]ServiceToken),
	}
	switch expected.ConcurrentMutationResult {
	case "success":
		if expected.TokenMutation == "rotate" {
			actual.MutationOutcome.token = ServiceToken{ID: "generated-mutation-token", ServiceType: "update_agent"}
		}
	case "not_found":
		actual.MutationOutcome.err = ErrNotFound
	case "none":
	default:
		return mariaDBFIX008RuntimeActualState{}, fmt.Errorf(
			"unknown mutation result %q",
			expected.ConcurrentMutationResult,
		)
	}
	rotationID, err := mariaDBFIX008ResolveExpectedRuntimeValue(expected.RotationID, actual)
	if err != nil {
		return mariaDBFIX008RuntimeActualState{}, err
	}
	currentTokenID, err := mariaDBFIX008ResolveExpectedRuntimeValue(expected.CurrentTokenID, actual)
	if err != nil {
		return mariaDBFIX008RuntimeActualState{}, err
	}
	stagedTokenID, err := mariaDBFIX008ResolveExpectedRuntimeValue(expected.StagedTokenID, actual)
	if err != nil {
		return mariaDBFIX008RuntimeActualState{}, err
	}
	wantTimes, err := mariaDBFIX008ExpectedRuntimeTimes(expected.Path, expected.StageTime)
	if err != nil {
		return mariaDBFIX008RuntimeActualState{}, err
	}
	rotation := SystemUpdateRuntimeTokenRotation{
		ID: rotationID, ServiceID: expected.ServiceID, ExecutionHostID: expected.ExecutionHostID,
		Status: expected.RotationStatus, Revision: expected.Revision,
		ExpectedOwnershipEpoch:              expected.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        expected.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          expected.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: expected.ExpectedLocalExecutorPolicyRevision,
		PreviousTokenID:                     expected.PreviousTokenID, StagedTokenID: stagedTokenID,
		CreatedAt: wantTimes.CreatedAt, UpdatedAt: wantTimes.UpdatedAt,
		CredentialClaimedAt:      wantTimes.CredentialClaimedAt,
		LocalStageAcknowledgedAt: wantTimes.LocalStageAcknowledgedAt,
		LocalStagedAt:            wantTimes.LocalStagedAt,
		HeartbeatProvedAt:        wantTimes.HeartbeatProvedAt,
		ActivatedAt:              wantTimes.ActivatedAt,
		CancelRequestedAt:        wantTimes.CancelRequestedAt,
		CancelAcknowledgedAt:     wantTimes.CancelAcknowledgedAt,
		CanceledAt:               wantTimes.CanceledAt,
		EmergencyRevokedAt:       wantTimes.EmergencyRevokedAt,
		stagedTokenHash:          "staged-token-hash", stagedTokenScopes: []string{"system_updates:execute"},
	}
	if expected.ReplaySecretPresent {
		rotation.stagedTokenCiphertext = "ciphertext"
		rotation.stagedTokenNonce = "nonce"
	}
	if expected.ClaimFencePresent {
		rotation.credentialClaimIDHash = runtimeTokenRotationClaimIDHash(expected.ClaimID)
		rotation.credentialClaimRevision = 1
	}
	if expected.ActivationProofState != mariaDBFIX007ProofNone {
		rotation.LocalStageReceiptID = runtimeTokenRotationLocalStageReceiptID(rotation)
	}
	if expected.CancelState == mariaDBFIX007CancelEmergency {
		rotation.EmergencyRevokedTokenID = expected.PreviousTokenID
	}
	service := expected.PreService
	service.TokenID = currentTokenID
	service.StagedNodePreviousTokenID = ""
	service.StagedNodeTokenID = ""
	service.StagedNodeTokenHash = ""
	service.StagedNodeTokenScopes = nil
	service.StagedNodeTokenCiphertext = ""
	service.StagedNodeTokenNonce = ""
	service.StagedNodeActivationTokenHash = ""
	service.StagedNodeTokenAt = nil
	switch {
	case expected.ConcurrentMutationResult == "none":
	case expected.ActivationProofState == mariaDBFIX007ProofActivated:
	case expected.CancelState == mariaDBFIX007CancelEmergency:
		service.Status = "offline"
		service.LastHeartbeatAt = nil
		service.ReportedCapabilities = map[string]any{}
	default:
		service.LastHeartbeatAt = nil
		service.ReportedCapabilities = map[string]any{}
	}
	actual.Rotation = rotation
	actual.Service = service
	actual.Services[service.ServiceID] = service
	setToken := func(tokenID string, revoked bool) error {
		if tokenID == "" {
			return fmt.Errorf("fixture token ID is empty")
		}
		if existing, exists := actual.Tokens[tokenID]; exists {
			if (existing.RevokedAt != nil) != revoked {
				return fmt.Errorf("fixture token %q has conflicting revoked states", tokenID)
			}
			return nil
		}
		token := ServiceToken{ID: tokenID, ServiceType: "update_agent"}
		if revoked {
			token.RevokedAt = nonNilMariaDBFIX007Time()
		}
		actual.Tokens[tokenID] = token
		return nil
	}
	for _, tokenState := range []struct {
		id      string
		revoked bool
	}{
		{currentTokenID, expected.CurrentTokenRevoked},
		{expected.PreviousTokenID, expected.PreviousTokenRevoked},
		{stagedTokenID, expected.StagedTokenRevoked},
	} {
		if err := setToken(tokenState.id, tokenState.revoked); err != nil {
			return mariaDBFIX008RuntimeActualState{}, err
		}
	}
	actual.OperationRotation = publicSystemUpdateRuntimeTokenRotation(rotation)
	return actual, nil
}

func mariaDBFIX008RuntimeMutationMatrix() []mariaDBFIX008RuntimeMutationCase {
	return []mariaDBFIX008RuntimeMutationCase{
		{
			name: "stage rotate wrong PreviousTokenID", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.previous_token_id",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				actual.Rotation.PreviousTokenID = "wrong-valid-previous-token"
			},
		},
		{
			name: "wrong CurrentTokenID", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.current_token_id",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				service := actual.Service
				service.TokenID = "wrong-current-token"
				actual.Service = service
				actual.Services[service.ServiceID] = service
				actual.Tokens["wrong-current-token"] = ServiceToken{ID: "wrong-current-token", ServiceType: "update_agent"}
			},
		},
		{
			name: "wrong StagedTokenID", path: "local_staged", tokenMutation: "rotate",
			diagnosticField: "runtime.staged_token_id",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				actual.Rotation.StagedTokenID = "wrong-staged-token"
			},
		},
		{
			name: "wrong PreviousTokenRevoked", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.previous_token_revoked",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				token := actual.Tokens["pre-operation-token"]
				token.RevokedAt = nil
				actual.Tokens[token.ID] = token
			},
		},
		{
			name: "wrong CurrentTokenRevoked", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.current_token_revoked",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				token := actual.Tokens["generated-mutation-token"]
				token.RevokedAt = nonNilMariaDBFIX007Time()
				actual.Tokens[token.ID] = token
			},
		},
		{
			name: "wrong StagedTokenRevoked", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.staged_token_revoked",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				token := actual.Tokens["generated-staged-token"]
				token.RevokedAt = nil
				actual.Tokens[token.ID] = token
			},
		},
		{
			name: "wrong status", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.status",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.Status = "wrong-status" },
		},
		{
			name: "wrong revision", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.revision",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.Revision++ },
		},
		{
			name: "replay secret present error", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.replay_secret_present",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.stagedTokenCiphertext = "" },
		},
		{
			name: "replay secret consumed error", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.replay_secret_consumed",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				value := actual.Rotation.CreatedAt.Add(time.Second)
				actual.Rotation.CredentialClaimedAt = &value
			},
		},
		{
			name: "wrong claim state", path: "local_staged", tokenMutation: "rotate",
			diagnosticField: "runtime.claim_fence_state",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.credentialClaimIDHash = "wrong-claim" },
		},
		{
			name: "wrong proof state", path: "local_staged", tokenMutation: "rotate",
			diagnosticField: "runtime.activation_proof_state",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.LocalStageReceiptID = "wrong-receipt" },
		},
		{
			name: "wrong cancel state", path: "acknowledge_cancel", tokenMutation: "rotate",
			diagnosticField: "runtime.cancel_state",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.CancelAcknowledgedAt = nil },
		},
		{
			name: "wrong OwnershipEpoch", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.ownership_epoch",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ExpectedOwnershipEpoch-- },
		},
		{
			name: "wrong PolicyRevision", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.policy_revision",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ExpectedSourcePolicyRevision-- },
		},
		{
			name: "wrong token service type", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.token_service_type",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				token := actual.Tokens["pre-operation-token"]
				token.ServiceType = "worker"
				actual.Tokens[token.ID] = token
			},
		},
		{
			name: "stale unexpected token reference", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.token_reference_closure",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				actual.Services["unexpected-service"] = RegisteredService{
					ServiceID: "unexpected-service", ServiceType: "update_agent", TokenID: "pre-operation-token",
				}
			},
		},
		{
			name: "wrong operation result", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.operation_result",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.OperationResult = "wrong-result" },
		},
		{
			name: "wrong rotation count", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.rotation_count",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.RotationCount++ },
		},
		{
			name: "wrong rotation ID", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.rotation_id",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ID = "wrong-rotation" },
		},
		{
			name: "wrong rotation service ID", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.service_id",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ServiceID = "wrong-service" },
		},
		{
			name: "wrong rotation execution host", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.execution_host_id",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ExecutionHostID = "wrong-host" },
		},
		{
			name: "wrong projection revision", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.projection_revision",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ExpectedProjectionRevision-- },
		},
		{
			name: "wrong local executor policy revision", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.local_executor_policy_revision",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.ExpectedLocalExecutorPolicyRevision-- },
		},
		{
			name: "operation result differs from durable row", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.operation_durable_result",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.OperationRotation.Status = "wrong-status" },
		},
		{
			name: "wrong ownership state", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.ownership_state",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Ownership.OwnershipEpoch-- },
		},
		{
			name: "wrong policy state", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.policy_state",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Policy.Revision-- },
		},
		{
			name: "wrong service ownership", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.service_ownership",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				service := actual.Service
				service.ExecutionHostID = "wrong-host"
				actual.Service = service
				actual.Services[service.ServiceID] = service
			},
		},
		{
			name: "stale staged service token state", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.staged_service_token_state",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				service := actual.Service
				service.StagedNodeTokenID = "stale-staged-token"
				actual.Service = service
				actual.Services[service.ServiceID] = service
			},
		},
		{
			name: "wrong mutation result", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.mutation_result",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.MutationOutcome.err = ErrNotFound },
		},
		{
			name: "missing token row", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.token_exists",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { delete(actual.Tokens, "pre-operation-token") },
		},
		{
			name: "wrong token ID", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.token_id",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				token := actual.Tokens["pre-operation-token"]
				token.ID = "wrong-token-id"
				actual.Tokens["pre-operation-token"] = token
			},
		},
		{
			name: "missing staged token semantic identity", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.staged_token_identity",
			mutate:          func(actual *mariaDBFIX008RuntimeActualState) { actual.Rotation.stagedTokenHash = "" },
		},
		{
			name: "wrong service convergence", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.service_convergence",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				value := time.Unix(1_700_000_001, 0).UTC()
				service := actual.Service
				service.LastHeartbeatAt = &value
				actual.Service = service
				actual.Services[service.ServiceID] = service
			},
		},
		{
			name: "wrong created timestamp", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.created_at",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				actual.Rotation.CreatedAt = actual.Rotation.CreatedAt.Add(time.Second)
			},
		},
		{
			name: "invalid token identity", path: "stage", tokenMutation: "rotate",
			diagnosticField: "runtime.token_identity",
			mutate: func(actual *mariaDBFIX008RuntimeActualState) {
				actual.Rotation.StagedTokenID = actual.Rotation.PreviousTokenID
			},
		},
	}
}
