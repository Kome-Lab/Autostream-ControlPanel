package store

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type mariaDBFIX008OracleMismatch struct {
	Field  string
	Detail string
}

func mariaDBFIX008Mismatch(field, format string, args ...any) mariaDBFIX008OracleMismatch {
	return mariaDBFIX008OracleMismatch{
		Field:  field,
		Detail: fmt.Sprintf(format, args...),
	}
}

func mariaDBFIX008FormatMismatches(mismatches []mariaDBFIX008OracleMismatch) string {
	parts := make([]string, 0, len(mismatches))
	for _, mismatch := range mismatches {
		parts = append(parts, mismatch.Field+": "+mismatch.Detail)
	}
	return strings.Join(parts, "; ")
}

func mariaDBFIX008HasMismatchField(mismatches []mariaDBFIX008OracleMismatch, field string) bool {
	for _, mismatch := range mismatches {
		if mismatch.Field == field {
			return true
		}
	}
	return false
}

func mariaDBFIX008ResolveExpectedRuntimeValue(
	expected mariaDBFIX008ExpectedRuntimeValue,
	actual mariaDBFIX008RuntimeActualState,
) (string, error) {
	switch expected.Source {
	case mariaDBFIX008RuntimeValueLiteral:
		if strings.TrimSpace(expected.Value) == "" {
			return "", fmt.Errorf("literal expected value is empty")
		}
		return expected.Value, nil
	case mariaDBFIX008RuntimeValueOperationRotationID:
		if strings.TrimSpace(actual.OperationRotation.ID) == "" {
			return "", fmt.Errorf("operation result rotation ID is empty")
		}
		return actual.OperationRotation.ID, nil
	case mariaDBFIX008RuntimeValueOperationStagedTokenID:
		if strings.TrimSpace(actual.OperationRotation.StagedTokenID) == "" {
			return "", fmt.Errorf("operation result staged token ID is empty")
		}
		return actual.OperationRotation.StagedTokenID, nil
	case mariaDBFIX008RuntimeValueMutationTokenID:
		if strings.TrimSpace(actual.MutationOutcome.token.ID) == "" {
			return "", fmt.Errorf("mutation result token ID is empty")
		}
		return actual.MutationOutcome.token.ID, nil
	default:
		return "", fmt.Errorf("unknown expected value source %q", expected.Source)
	}
}

func compareMariaDBFIX008RuntimeState(
	expected mariaDBFIX008ExpectedRuntimeState,
	actual mariaDBFIX008RuntimeActualState,
) []mariaDBFIX008OracleMismatch {
	mismatches := make([]mariaDBFIX008OracleMismatch, 0)
	resolve := func(field string, value mariaDBFIX008ExpectedRuntimeValue) string {
		resolved, err := mariaDBFIX008ResolveExpectedRuntimeValue(value, actual)
		if err != nil {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(field, "%v", err))
		}
		return resolved
	}
	wantRotationID := resolve("runtime.rotation_id_source", expected.RotationID)
	wantCurrentTokenID := resolve("runtime.current_token_id_source", expected.CurrentTokenID)
	wantStagedTokenID := resolve("runtime.staged_token_id_source", expected.StagedTokenID)
	rotation := actual.Rotation
	if actual.OperationResult != expected.OperationResult {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.operation_result", "got %q want %q", actual.OperationResult, expected.OperationResult))
	}
	if actual.RotationCount != expected.RotationCount || actual.RotationExists != expected.RotationExists {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.rotation_count", "got count=%d exists=%t want count=%d exists=%t", actual.RotationCount, actual.RotationExists, expected.RotationCount, expected.RotationExists))
	}
	if rotation.ID != wantRotationID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.rotation_id", "got %q want %q", rotation.ID, wantRotationID))
	}
	if rotation.ServiceID != expected.ServiceID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.service_id", "got %q want %q", rotation.ServiceID, expected.ServiceID))
	}
	if rotation.ExecutionHostID != expected.ExecutionHostID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.execution_host_id", "got %q want %q", rotation.ExecutionHostID, expected.ExecutionHostID))
	}
	if rotation.Status != expected.RotationStatus {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.status", "got %q want %q", rotation.Status, expected.RotationStatus))
	}
	if rotation.Revision != expected.Revision {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.revision", "got %d want %d", rotation.Revision, expected.Revision))
	}
	if rotation.ExpectedOwnershipEpoch != expected.ExpectedOwnershipEpoch {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.ownership_epoch", "got %d want %d", rotation.ExpectedOwnershipEpoch, expected.ExpectedOwnershipEpoch))
	}
	if rotation.ExpectedSourcePolicyRevision != expected.ExpectedSourcePolicyRevision {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.policy_revision", "got %d want %d", rotation.ExpectedSourcePolicyRevision, expected.ExpectedSourcePolicyRevision))
	}
	if rotation.ExpectedProjectionRevision != expected.ExpectedProjectionRevision {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.projection_revision", "got %d want %d", rotation.ExpectedProjectionRevision, expected.ExpectedProjectionRevision))
	}
	if rotation.ExpectedLocalExecutorPolicyRevision != expected.ExpectedLocalExecutorPolicyRevision {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.local_executor_policy_revision", "got %d want %d", rotation.ExpectedLocalExecutorPolicyRevision, expected.ExpectedLocalExecutorPolicyRevision))
	}
	anchorSources := mariaDBFIX008PreviousTokenAnchorSources(expected)
	if !reflect.DeepEqual(anchorSources, []string{"pre.service.TokenID", "operation.tokenID"}) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.previous_token_anchor", "sources=%v previous=%q pre=%q operation=%q", anchorSources, expected.PreviousTokenID, expected.PreService.TokenID, expected.OperationTokenID))
	}
	if count := mariaDBFIX008PostDerivedExpectedSelectorCount(expected); count != 0 {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.post_derived_expected_selectors", "got %d want 0", count))
	}
	if rotation.PreviousTokenID != expected.PreviousTokenID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.previous_token_id", "got %q want pre/operation token %q", rotation.PreviousTokenID, expected.PreviousTokenID))
	}
	if rotation.StagedTokenID != wantStagedTokenID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.staged_token_id", "got %q want %q", rotation.StagedTokenID, wantStagedTokenID))
	}
	if actual.Service.TokenID != wantCurrentTokenID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.current_token_id", "got %q want %q", actual.Service.TokenID, wantCurrentTokenID))
	}
	if rotation.PreviousTokenID == "" || rotation.StagedTokenID == "" || rotation.PreviousTokenID == rotation.StagedTokenID {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.token_identity", "previous=%q staged=%q", rotation.PreviousTokenID, rotation.StagedTokenID))
	}
	if !mariaDBFIX007EqualPublicRuntimeRotation(
		actual.OperationRotation,
		publicSystemUpdateRuntimeTokenRotation(rotation),
	) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.operation_durable_result", "operation result and durable rotation differ"))
	}
	if !reflect.DeepEqual(actual.Ownership, expected.PreOwnership) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.ownership_state", "got %#v want %#v", actual.Ownership, expected.PreOwnership))
	}
	if !reflect.DeepEqual(actual.Policy, expected.PrePolicy) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.policy_state", "got %#v want %#v", actual.Policy, expected.PrePolicy))
	}
	if actual.Service.ExecutionHostID != expected.PreService.ExecutionHostID ||
		actual.Service.TransportMode != expected.PreService.TransportMode ||
		actual.Service.OwnershipEpoch != expected.PreService.OwnershipEpoch {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.service_ownership", "got host=%q mode=%q epoch=%d want host=%q mode=%q epoch=%d", actual.Service.ExecutionHostID, actual.Service.TransportMode, actual.Service.OwnershipEpoch, expected.PreService.ExecutionHostID, expected.PreService.TransportMode, expected.PreService.OwnershipEpoch))
	}
	if actual.Service.StagedNodePreviousTokenID != "" || actual.Service.StagedNodeTokenID != "" ||
		actual.Service.StagedNodeTokenHash != "" || len(actual.Service.StagedNodeTokenScopes) != 0 ||
		actual.Service.StagedNodeTokenCiphertext != "" || actual.Service.StagedNodeTokenNonce != "" ||
		actual.Service.StagedNodeActivationTokenHash != "" || actual.Service.StagedNodeTokenAt != nil {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.staged_service_token_state", "stale staged service-token fields remain"))
	}

	mismatches = append(mismatches, mariaDBFIX008CompareMutationResult(expected, actual.MutationOutcome)...)
	tokenExpectations := make(map[string]bool)
	addTokenExpectation := func(field, tokenID string, revoked bool) {
		if tokenID == "" {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(field, "expected token ID is empty"))
			return
		}
		if prior, exists := tokenExpectations[tokenID]; exists && prior != revoked {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(field, "token %q has conflicting revoked expectations %t/%t", tokenID, prior, revoked))
			return
		}
		tokenExpectations[tokenID] = revoked
	}
	addTokenExpectation("runtime.current_token_revoked", wantCurrentTokenID, expected.CurrentTokenRevoked)
	addTokenExpectation("runtime.previous_token_revoked", expected.PreviousTokenID, expected.PreviousTokenRevoked)
	addTokenExpectation("runtime.staged_token_revoked", wantStagedTokenID, expected.StagedTokenRevoked)
	tokenExpectationIDs := make(map[string]struct{}, len(tokenExpectations))
	for tokenID := range tokenExpectations {
		tokenExpectationIDs[tokenID] = struct{}{}
	}
	for _, tokenID := range mariaDBFIX008SortedSetKeys(tokenExpectationIDs) {
		wantRevoked := tokenExpectations[tokenID]
		token, exists := actual.Tokens[tokenID]
		if !exists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.token_exists", "token %q is missing", tokenID))
			continue
		}
		if token.ID != tokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.token_id", "map key %q contains ID %q", tokenID, token.ID))
		}
		if token.ServiceType != "update_agent" {
			mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.token_service_type", "token %q type=%q want update_agent", tokenID, token.ServiceType))
		}
		if gotRevoked := token.RevokedAt != nil; gotRevoked != wantRevoked {
			field := "runtime.current_token_revoked"
			switch tokenID {
			case expected.PreviousTokenID:
				field = "runtime.previous_token_revoked"
			case wantStagedTokenID:
				field = "runtime.staged_token_revoked"
			}
			mismatches = append(mismatches, mariaDBFIX008Mismatch(field, "token %q got %t want %t", tokenID, gotRevoked, wantRevoked))
		}
	}
	relevantTokenIDs := map[string]struct{}{
		expected.OperationTokenID: {},
		expected.PreviousTokenID:  {},
		wantCurrentTokenID:        {},
		wantStagedTokenID:         {},
	}
	for _, tokenID := range []string{
		rotation.PreviousTokenID,
		rotation.StagedTokenID,
		actual.Service.TokenID,
		actual.MutationOutcome.token.ID,
	} {
		if tokenID != "" {
			relevantTokenIDs[tokenID] = struct{}{}
		}
	}
	for _, tokenID := range mariaDBFIX008SortedSetKeys(relevantTokenIDs) {
		wantReferences := []string{}
		if tokenID == wantCurrentTokenID {
			wantReferences = append(wantReferences, expected.ServiceID+"/current")
		}
		gotReferences := mariaDBFIX007TokenReferenceOwners(actual.Services, tokenID)
		if !reflect.DeepEqual(gotReferences, wantReferences) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.token_reference_closure", "token %q got %v want %v", tokenID, gotReferences, wantReferences))
		}
	}

	hasCiphertext := strings.TrimSpace(rotation.stagedTokenCiphertext) != ""
	hasNonce := strings.TrimSpace(rotation.stagedTokenNonce) != ""
	if hasCiphertext != expected.ReplaySecretPresent || hasNonce != expected.ReplaySecretPresent {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.replay_secret_present", "ciphertext=%t nonce=%t want both=%t", hasCiphertext, hasNonce, expected.ReplaySecretPresent))
	}
	if gotConsumed := rotation.CredentialClaimedAt != nil; gotConsumed != expected.ReplaySecretConsumed {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.replay_secret_consumed", "got %t want %t", gotConsumed, expected.ReplaySecretConsumed))
	}
	hasClaimHash := strings.TrimSpace(rotation.credentialClaimIDHash) != ""
	hasClaimRevision := rotation.credentialClaimRevision > 0
	if hasClaimHash != expected.ClaimFencePresent || hasClaimRevision != expected.ClaimFencePresent {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.claim_fence_state", "hash=%t revision=%t want both=%t", hasClaimHash, hasClaimRevision, expected.ClaimFencePresent))
	} else if expected.ClaimFencePresent &&
		(expected.ClaimID == "" || rotation.credentialClaimIDHash != runtimeTokenRotationClaimIDHash(expected.ClaimID) || rotation.credentialClaimRevision != 1) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.claim_fence_state", "claim ID hash/revision do not match operation claim"))
	}
	if rotation.stagedTokenHash == "" || len(rotation.stagedTokenScopes) == 0 {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.staged_token_identity", "hash/scopes are incomplete"))
	}
	mismatches = append(mismatches, mariaDBFIX008CompareRuntimeProof(expected, rotation)...)
	mismatches = append(mismatches, mariaDBFIX008CompareRuntimeCancel(expected, rotation)...)
	mismatches = append(mismatches, mariaDBFIX008CompareRuntimeTimes(expected, rotation)...)
	mismatches = append(mismatches, mariaDBFIX008CompareRuntimeServiceConvergence(expected, actual.Service)...)
	return mismatches
}

func mariaDBFIX008CompareMutationResult(
	expected mariaDBFIX008ExpectedRuntimeState,
	outcome mariaDBServiceTokenMutationResult,
) []mariaDBFIX008OracleMismatch {
	valid := false
	switch expected.ConcurrentMutationResult {
	case "none":
		valid = outcome.err == nil && outcome.token.ID == ""
	case "success":
		valid = outcome.err == nil &&
			((expected.TokenMutation == "rotate" && outcome.token.ID != "") ||
				(expected.TokenMutation == "revoke" && outcome.token.ID == ""))
	case "not_found":
		valid = errors.Is(outcome.err, ErrNotFound) && outcome.token.ID == ""
	}
	if valid {
		return nil
	}
	return []mariaDBFIX008OracleMismatch{mariaDBFIX008Mismatch(
		"runtime.mutation_result",
		"mutation=%q got token=%q err=%v want %q",
		expected.TokenMutation,
		outcome.token.ID,
		outcome.err,
		expected.ConcurrentMutationResult,
	)}
}

func assertMariaDBFIX007TokenState(
	t *testing.T,
	tokens map[string]ServiceToken,
	tokenID string,
	wantRevoked bool,
	label string,
) {
	t.Helper()
	token, exists := tokens[tokenID]
	if !exists {
		t.Fatalf("%s token %q does not exist", label, tokenID)
	}
	if token.ServiceType != "update_agent" {
		t.Fatalf("%s token %q type = %q, want update_agent", label, tokenID, token.ServiceType)
	}
	if gotRevoked := token.RevokedAt != nil; gotRevoked != wantRevoked {
		t.Fatalf("%s token %q revoked = %t, want %t", label, tokenID, gotRevoked, wantRevoked)
	}
}

func mariaDBFIX007TokenReferenceOwners(
	services map[string]RegisteredService,
	tokenID string,
) []string {
	references := make([]string, 0, 3)
	for serviceID, service := range services {
		if service.TokenID == tokenID {
			references = append(references, serviceID+"/current")
		}
		if service.StagedNodePreviousTokenID == tokenID {
			references = append(references, serviceID+"/staged_previous")
		}
		if service.StagedNodeTokenID == tokenID {
			references = append(references, serviceID+"/staged")
		}
	}
	sort.Strings(references)
	return references
}

func assertMariaDBFIX007RuntimeReplayState(
	t *testing.T,
	rotation SystemUpdateRuntimeTokenRotation,
	wantSecret, wantConsumed, wantClaimFence bool,
	label string,
) {
	t.Helper()
	hasCiphertext := strings.TrimSpace(rotation.stagedTokenCiphertext) != ""
	hasNonce := strings.TrimSpace(rotation.stagedTokenNonce) != ""
	if hasCiphertext != wantSecret || hasNonce != wantSecret {
		t.Fatalf("%s replay secret = ciphertext %t nonce %t, want both %t", label, hasCiphertext, hasNonce, wantSecret)
	}
	if gotConsumed := rotation.CredentialClaimedAt != nil; gotConsumed != wantConsumed {
		t.Fatalf("%s replay secret consumed = %t, want %t", label, gotConsumed, wantConsumed)
	}
	hasClaimHash := strings.TrimSpace(rotation.credentialClaimIDHash) != ""
	hasClaimRevision := rotation.credentialClaimRevision > 0
	if hasClaimHash != wantClaimFence || hasClaimRevision != wantClaimFence {
		t.Fatalf("%s claim fence = hash %t revision %t, want both %t", label, hasClaimHash, hasClaimRevision, wantClaimFence)
	}
	if rotation.stagedTokenHash == "" || len(rotation.stagedTokenScopes) == 0 {
		t.Fatalf("%s lost staged token semantic identity: %s", label, formatSafeSensitiveCompositeDiagnostic(rotation))
	}
}

type mariaDBFIX008RuntimeTimes struct {
	CreatedAt                time.Time
	UpdatedAt                time.Time
	CredentialClaimedAt      *time.Time
	LocalStageAcknowledgedAt *time.Time
	LocalStagedAt            *time.Time
	HeartbeatProvedAt        *time.Time
	ActivatedAt              *time.Time
	CancelRequestedAt        *time.Time
	CancelAcknowledgedAt     *time.Time
	CanceledAt               *time.Time
	EmergencyRevokedAt       *time.Time
}

func mariaDBFIX008ExpectedRuntimeTimes(path string, stageTime time.Time) (mariaDBFIX008RuntimeTimes, error) {
	times := mariaDBFIX008RuntimeTimes{CreatedAt: stageTime, UpdatedAt: stageTime}
	timeAt := func(offset time.Duration) *time.Time {
		value := stageTime.Add(offset)
		return &value
	}
	switch path {
	case "stage":
	case "claim_staged_credential":
		times.UpdatedAt = stageTime.Add(time.Second)
		times.CredentialClaimedAt = timeAt(time.Second)
	case "local_staged":
		times.UpdatedAt = stageTime.Add(2 * time.Second)
		times.CredentialClaimedAt = timeAt(time.Second)
		times.LocalStageAcknowledgedAt = timeAt(2 * time.Second)
		times.LocalStagedAt = timeAt(2 * time.Second)
	case "heartbeat_proof":
		times.UpdatedAt = stageTime.Add(3 * time.Second)
		times.CredentialClaimedAt = timeAt(time.Second)
		times.LocalStageAcknowledgedAt = timeAt(2 * time.Second)
		times.LocalStagedAt = timeAt(2 * time.Second)
		times.HeartbeatProvedAt = timeAt(3 * time.Second)
	case "activate":
		times.UpdatedAt = stageTime.Add(4 * time.Second)
		times.CredentialClaimedAt = timeAt(time.Second)
		times.LocalStageAcknowledgedAt = timeAt(2 * time.Second)
		times.LocalStagedAt = timeAt(2 * time.Second)
		times.HeartbeatProvedAt = timeAt(3 * time.Second)
		times.ActivatedAt = timeAt(4 * time.Second)
	case "cancel":
		times.UpdatedAt = stageTime.Add(time.Second)
		times.CanceledAt = timeAt(time.Second)
	case "acknowledge_cancel":
		times.UpdatedAt = stageTime.Add(2 * time.Second)
		times.CredentialClaimedAt = timeAt(time.Second)
		times.CancelRequestedAt = timeAt(time.Second)
		times.CancelAcknowledgedAt = timeAt(2 * time.Second)
		times.CanceledAt = timeAt(2 * time.Second)
	case "emergency_revoke":
		times.UpdatedAt = stageTime.Add(time.Second)
		times.CanceledAt = timeAt(time.Second)
		times.EmergencyRevokedAt = timeAt(time.Second)
	default:
		return mariaDBFIX008RuntimeTimes{}, fmt.Errorf("unknown runtime path %q", path)
	}
	return times, nil
}

func mariaDBFIX008CompareRuntimeTimes(
	expected mariaDBFIX008ExpectedRuntimeState,
	rotation SystemUpdateRuntimeTokenRotation,
) []mariaDBFIX008OracleMismatch {
	want, err := mariaDBFIX008ExpectedRuntimeTimes(expected.Path, expected.StageTime)
	if err != nil {
		return []mariaDBFIX008OracleMismatch{mariaDBFIX008Mismatch("runtime.timestamps", "%v", err)}
	}
	mismatches := make([]mariaDBFIX008OracleMismatch, 0)
	if !rotation.CreatedAt.Equal(want.CreatedAt) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.created_at", "got %s want %s", rotation.CreatedAt, want.CreatedAt))
	}
	if !rotation.UpdatedAt.Equal(want.UpdatedAt) {
		mismatches = append(mismatches, mariaDBFIX008Mismatch("runtime.updated_at", "got %s want %s", rotation.UpdatedAt, want.UpdatedAt))
	}
	compareOptional := func(field string, got, want *time.Time) {
		if !mariaDBFIX007OptionalTimesEqual(got, want) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(field, "got %v want %v", got, want))
		}
	}
	compareOptional("runtime.credential_claimed_at", rotation.CredentialClaimedAt, want.CredentialClaimedAt)
	compareOptional("runtime.local_stage_acknowledged_at", rotation.LocalStageAcknowledgedAt, want.LocalStageAcknowledgedAt)
	compareOptional("runtime.local_staged_at", rotation.LocalStagedAt, want.LocalStagedAt)
	compareOptional("runtime.heartbeat_proved_at", rotation.HeartbeatProvedAt, want.HeartbeatProvedAt)
	compareOptional("runtime.activated_at", rotation.ActivatedAt, want.ActivatedAt)
	compareOptional("runtime.cancel_requested_at", rotation.CancelRequestedAt, want.CancelRequestedAt)
	compareOptional("runtime.cancel_acknowledged_at", rotation.CancelAcknowledgedAt, want.CancelAcknowledgedAt)
	compareOptional("runtime.canceled_at", rotation.CanceledAt, want.CanceledAt)
	compareOptional("runtime.emergency_revoked_at", rotation.EmergencyRevokedAt, want.EmergencyRevokedAt)
	return mismatches
}

func mariaDBFIX008CompareRuntimeProof(
	expected mariaDBFIX008ExpectedRuntimeState,
	rotation SystemUpdateRuntimeTokenRotation,
) []mariaDBFIX008OracleMismatch {
	valid := false
	switch expected.ActivationProofState {
	case mariaDBFIX007ProofNone:
		valid = rotation.LocalStageReceiptID == "" && rotation.LocalStageAcknowledgedAt == nil &&
			rotation.LocalStagedAt == nil && rotation.HeartbeatProvedAt == nil && rotation.ActivatedAt == nil
	case mariaDBFIX007ProofLocal:
		valid = rotation.LocalStageReceiptID != "" &&
			rotation.LocalStageReceiptID == runtimeTokenRotationLocalStageReceiptID(rotation) &&
			rotation.LocalStageAcknowledgedAt != nil && rotation.LocalStagedAt != nil &&
			rotation.HeartbeatProvedAt == nil && rotation.ActivatedAt == nil
	case mariaDBFIX007ProofHeartbeat:
		valid = rotation.LocalStageReceiptID != "" &&
			rotation.LocalStageReceiptID == runtimeTokenRotationLocalStageReceiptID(rotation) &&
			rotation.LocalStageAcknowledgedAt != nil && rotation.LocalStagedAt != nil &&
			rotation.HeartbeatProvedAt != nil && rotation.ActivatedAt == nil
	case mariaDBFIX007ProofActivated:
		valid = rotation.LocalStageReceiptID != "" &&
			rotation.LocalStageReceiptID == runtimeTokenRotationLocalStageReceiptID(rotation) &&
			rotation.LocalStageAcknowledgedAt != nil && rotation.LocalStagedAt != nil &&
			rotation.HeartbeatProvedAt != nil && rotation.ActivatedAt != nil
	}
	if valid {
		return nil
	}
	return []mariaDBFIX008OracleMismatch{mariaDBFIX008Mismatch(
		"runtime.activation_proof_state",
		"rotation proof fields do not represent %q",
		expected.ActivationProofState,
	)}
}

func mariaDBFIX008CompareRuntimeCancel(
	expected mariaDBFIX008ExpectedRuntimeState,
	rotation SystemUpdateRuntimeTokenRotation,
) []mariaDBFIX008OracleMismatch {
	valid := false
	switch expected.CancelState {
	case mariaDBFIX007CancelNone:
		valid = rotation.CancelRequestedAt == nil && rotation.CancelAcknowledgedAt == nil &&
			rotation.CanceledAt == nil && rotation.EmergencyRevokedAt == nil &&
			rotation.EmergencyRevokedTokenID == ""
	case mariaDBFIX007CancelImmediate:
		valid = rotation.CancelRequestedAt == nil && rotation.CancelAcknowledgedAt == nil &&
			rotation.CanceledAt != nil && rotation.EmergencyRevokedAt == nil &&
			rotation.EmergencyRevokedTokenID == ""
	case mariaDBFIX007CancelAcknowledged:
		valid = rotation.CancelRequestedAt != nil && rotation.CancelAcknowledgedAt != nil &&
			rotation.CanceledAt != nil && rotation.EmergencyRevokedAt == nil &&
			rotation.EmergencyRevokedTokenID == ""
	case mariaDBFIX007CancelEmergency:
		valid = rotation.CancelRequestedAt == nil && rotation.CancelAcknowledgedAt == nil &&
			rotation.CanceledAt != nil && rotation.EmergencyRevokedAt != nil &&
			rotation.EmergencyRevokedTokenID == expected.PreviousTokenID
	}
	if valid {
		return nil
	}
	return []mariaDBFIX008OracleMismatch{mariaDBFIX008Mismatch(
		"runtime.cancel_state",
		"rotation cancel fields do not represent %q",
		expected.CancelState,
	)}
}

func mariaDBFIX008CompareRuntimeServiceConvergence(
	expected mariaDBFIX008ExpectedRuntimeState,
	post RegisteredService,
) []mariaDBFIX008OracleMismatch {
	valid := false
	switch {
	case expected.ConcurrentMutationResult == "none":
		valid = reflect.DeepEqual(expected.PreService, post)
	case expected.ActivationProofState == mariaDBFIX007ProofActivated:
		valid = post.LastHeartbeatAt != nil && len(post.ReportedCapabilities) != 0
	case expected.CancelState == mariaDBFIX007CancelEmergency:
		valid = post.Status == "offline" && post.LastHeartbeatAt == nil && len(post.ReportedCapabilities) == 0
	default:
		valid = post.Status == expected.PreService.Status &&
			post.LastHeartbeatAt == nil && len(post.ReportedCapabilities) == 0
	}
	if valid {
		return nil
	}
	return []mariaDBFIX008OracleMismatch{mariaDBFIX008Mismatch(
		"runtime.service_convergence",
		"post service does not satisfy path %q mutation result %q",
		expected.Path,
		expected.ConcurrentMutationResult,
	)}
}
