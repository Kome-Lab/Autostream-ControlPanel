package store

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMariaDBFIX008RuntimeStageRotatePreStateAnchor(t *testing.T) {
	expected, actual, err := mariaDBFIX008RuntimeOracleFixture("stage", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		mariaDBFIX008PreviousTokenAnchorSources(expected),
		[]string{"pre.service.TokenID", "operation.tokenID"},
	) || expected.PreviousTokenID != "pre-operation-token" ||
		expected.OperationTokenID != "pre-operation-token" ||
		mariaDBFIX008PostDerivedExpectedSelectorCount(expected) != 0 {
		t.Fatalf("stage/rotate previous-token anchor = %#v", expected)
	}
	actual.Rotation.PreviousTokenID = "wrong-valid-previous-token"
	actual.OperationRotation.PreviousTokenID = "wrong-valid-previous-token"
	actual.Tokens["wrong-valid-previous-token"] = ServiceToken{
		ID: "wrong-valid-previous-token", ServiceType: "update_agent", RevokedAt: nonNilMariaDBFIX007Time(),
	}
	mismatches := compareMariaDBFIX008RuntimeState(expected, actual)
	if !mariaDBFIX008HasMismatchField(mismatches, "runtime.previous_token_id") {
		t.Fatalf("shared runtime core accepted coherent wrong previous token: %s", mariaDBFIX008FormatMismatches(mismatches))
	}
}

func TestMariaDBFIX008RuntimeOracleMutationMatrix(t *testing.T) {
	for _, mutation := range mariaDBFIX008RuntimeMutationMatrix() {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			expected, actual, err := mariaDBFIX008RuntimeOracleFixture(mutation.path, mutation.tokenMutation)
			if err != nil {
				t.Fatal(err)
			}
			if mismatches := compareMariaDBFIX008RuntimeState(expected, actual); len(mismatches) != 0 {
				t.Fatalf("valid runtime oracle fixture mismatched: %s", mariaDBFIX008FormatMismatches(mismatches))
			}
			mutation.mutate(&actual)
			mismatches := compareMariaDBFIX008RuntimeState(expected, actual)
			if !mariaDBFIX008HasMismatchField(mismatches, mutation.diagnosticField) {
				t.Fatalf("shared runtime core did not report %q: %s", mutation.diagnosticField, mariaDBFIX008FormatMismatches(mismatches))
			}
		})
	}
}

func TestMariaDBFIX008OracleInventorySelfCheck(t *testing.T) {
	policyCoreRefs := []struct {
		consumer string
		core     func(mariaDBFIX007OwnershipSemanticSnapshot, mariaDBFIX007OwnershipSemanticSnapshot) []mariaDBFIX008OracleMismatch
	}{
		{consumer: "real MariaDB pair final-state assertion", core: compareMariaDBFIX008StrongOwnership},
		{consumer: "policy negative mutation matrix", core: compareMariaDBFIX008StrongOwnership},
	}
	policyCorePointers := make(map[uintptr][]string)
	for _, wiring := range policyCoreRefs {
		pointer := reflect.ValueOf(wiring.core).Pointer()
		policyCorePointers[pointer] = append(policyCorePointers[pointer], wiring.consumer)
	}
	if len(policyCorePointers) != 1 {
		t.Fatalf("policy consumers use %d comparison cores: %#v", len(policyCorePointers), policyCorePointers)
	}
	policyExpected := mariaDBFIX008PolicyOracleFixture()
	policyMutationFields := make(map[string]struct{})
	for _, mutation := range mariaDBFIX008PolicyMutationMatrix() {
		actual := cloneMariaDBFIX007OwnershipSemanticState(policyExpected)
		mutation.mutate(&actual)
		mismatches := compareMariaDBFIX008StrongOwnership(policyExpected, actual)
		if !mariaDBFIX008HasMismatchField(mismatches, mutation.diagnosticField) {
			t.Fatalf("policy mutation %q did not exercise %q: %s", mutation.name, mutation.diagnosticField, mariaDBFIX008FormatMismatches(mismatches))
		}
		for _, mismatch := range mismatches {
			policyMutationFields[mismatch.Field] = struct{}{}
		}
	}
	if len(policyMutationFields) < 25 {
		t.Fatalf("policy core observed strong field count = %d, want at least 25", len(policyMutationFields))
	}
	for _, required := range []string{
		"host.host-1.execution_host_id",
		"host.host-1.transport_mode",
		"host.host-1.agent_service_id",
		"host.host-1.ownership_epoch",
		"host.host-1.policy_revision",
		"host.host-1.active_policy_binding",
		"service.pull-agent.ownership_epoch",
		"service.pull-agent.execution_host_id",
		"service.pull-agent.service_id",
		"service.pull-agent.service_type",
		"service.pull-agent.transport_mode",
		"service.pull-agent.current_token_id",
		"service.pull-agent.staged_previous_token_id",
		"service.pull-agent.staged_token_id",
		"host.host-1.policy_revision_closure",
		"policy.pull-agent.updater_id",
		"policy.pull-agent.revision",
		"policy.pull-agent.projection_revision",
		"policy.pull-agent.local_executor_policy_revision",
		"policy.pull-agent.transport_mode",
		"policy.pull-agent.execution_host_id",
		"policy.pull-agent.local_executor_policy_sha256",
		"token.current-token.id",
		"token.current-token.exists",
		"token.current-token.revoked",
		"token.current-token.service_type",
		"token.current-token.references",
		"token.staged-token.revoked",
	} {
		if _, exists := policyMutationFields[required]; !exists {
			t.Fatalf("policy mutation matrix does not cover %q", required)
		}
	}

	runtimeCases := mariaDBFIX007RuntimeMatrixInventory()
	if len(runtimeCases) != 16 {
		t.Fatalf("runtime pair count = %d, want 16", len(runtimeCases))
	}
	runtimeConstructorPointers := make(map[uintptr][]string)
	runtimeCorePointers := make(map[uintptr][]string)
	runtimeConstructorPointers[reflect.ValueOf(deriveMariaDBFIX008ExpectedRuntimeState).Pointer()] = []string{
		"runtime negative mutation matrix",
	}
	runtimeCorePointers[reflect.ValueOf(compareMariaDBFIX008RuntimeState).Pointer()] = []string{
		"runtime negative mutation matrix",
	}
	seen := make(map[string]map[string]int)
	genericOnly := 0
	for _, testCase := range runtimeCases {
		if seen[testCase.path] == nil {
			seen[testCase.path] = make(map[string]int)
		}
		seen[testCase.path][testCase.tokenMutation]++
		if testCase.iterations < 3 {
			t.Fatalf("runtime pair %s/%s iterations = %d, want at least 3", testCase.path, testCase.tokenMutation, testCase.iterations)
		}
		if testCase.genericOnlyOracle || !testCase.exactFinalOracle {
			genericOnly++
		}
		consumer := testCase.path + "/" + testCase.tokenMutation
		runtimeConstructorPointers[reflect.ValueOf(deriveMariaDBFIX008ExpectedRuntimeState).Pointer()] = append(
			runtimeConstructorPointers[reflect.ValueOf(deriveMariaDBFIX008ExpectedRuntimeState).Pointer()],
			consumer,
		)
		runtimeCorePointers[reflect.ValueOf(compareMariaDBFIX008RuntimeState).Pointer()] = append(
			runtimeCorePointers[reflect.ValueOf(compareMariaDBFIX008RuntimeState).Pointer()],
			consumer,
		)
		expected, actual, err := mariaDBFIX008RuntimeOracleFixture(testCase.path, testCase.tokenMutation)
		if err != nil {
			t.Fatalf("runtime pair %s expected constructor: %v", consumer, err)
		}
		if !reflect.DeepEqual(
			mariaDBFIX008PreviousTokenAnchorSources(expected),
			[]string{"pre.service.TokenID", "operation.tokenID"},
		) || expected.PreviousTokenID != expected.PreService.TokenID ||
			expected.PreviousTokenID != expected.OperationTokenID ||
			mariaDBFIX008PostDerivedExpectedSelectorCount(expected) != 0 {
			t.Fatalf("runtime pair %s previous-token anchor = %#v", consumer, expected)
		}
		if mismatches := compareMariaDBFIX008RuntimeState(expected, actual); len(mismatches) != 0 {
			t.Fatalf("runtime pair %s shared core fixture mismatch: %s", consumer, mariaDBFIX008FormatMismatches(mismatches))
		}
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
	if len(runtimeConstructorPointers) != 1 {
		t.Fatalf("runtime pairs use %d expected constructors: %#v", len(runtimeConstructorPointers), runtimeConstructorPointers)
	}
	if len(runtimeCorePointers) != 1 {
		t.Fatalf("runtime pairs use %d comparison cores: %#v", len(runtimeCorePointers), runtimeCorePointers)
	}
	if genericOnly != 0 {
		t.Fatalf("runtime generic-only oracle count = %d, want 0", genericOnly)
	}
	runtimeMutationFields := make(map[string]struct{})
	for _, mutation := range mariaDBFIX008RuntimeMutationMatrix() {
		expected, actual, err := mariaDBFIX008RuntimeOracleFixture(mutation.path, mutation.tokenMutation)
		if err != nil {
			t.Fatalf("runtime mutation %q fixture: %v", mutation.name, err)
		}
		mutation.mutate(&actual)
		mismatches := compareMariaDBFIX008RuntimeState(expected, actual)
		if !mariaDBFIX008HasMismatchField(mismatches, mutation.diagnosticField) {
			t.Fatalf("runtime mutation %q did not exercise %q: %s", mutation.name, mutation.diagnosticField, mariaDBFIX008FormatMismatches(mismatches))
		}
		for _, mismatch := range mismatches {
			runtimeMutationFields[mismatch.Field] = struct{}{}
		}
	}
	if len(runtimeMutationFields) < 28 {
		t.Fatalf("runtime core observed strong field count = %d, want at least 28", len(runtimeMutationFields))
	}
	for _, required := range []string{
		"runtime.operation_result",
		"runtime.rotation_count",
		"runtime.rotation_id",
		"runtime.service_id",
		"runtime.execution_host_id",
		"runtime.previous_token_id",
		"runtime.current_token_id",
		"runtime.staged_token_id",
		"runtime.token_identity",
		"runtime.previous_token_revoked",
		"runtime.current_token_revoked",
		"runtime.staged_token_revoked",
		"runtime.token_exists",
		"runtime.token_id",
		"runtime.token_service_type",
		"runtime.status",
		"runtime.revision",
		"runtime.projection_revision",
		"runtime.local_executor_policy_revision",
		"runtime.replay_secret_present",
		"runtime.replay_secret_consumed",
		"runtime.claim_fence_state",
		"runtime.staged_token_identity",
		"runtime.activation_proof_state",
		"runtime.cancel_state",
		"runtime.ownership_epoch",
		"runtime.policy_revision",
		"runtime.operation_durable_result",
		"runtime.ownership_state",
		"runtime.policy_state",
		"runtime.service_ownership",
		"runtime.staged_service_token_state",
		"runtime.mutation_result",
		"runtime.token_reference_closure",
		"runtime.service_convergence",
		"runtime.created_at",
	} {
		if _, exists := runtimeMutationFields[required]; !exists {
			t.Fatalf("runtime mutation matrix does not cover %q", required)
		}
	}
}

type mariaDBFIX007LockEvidence struct {
	HostLocked     bool
	LaneLocked     bool
	RotationLocked bool
	PolicyLocked   bool
	ServiceLocked  bool
	TokenLocked    bool
}

func assertMariaDBFIX007LegacyLockEvidence(actual mariaDBFIX007LockEvidence) error {
	if !actual.HostLocked || !actual.PolicyLocked || !actual.ServiceLocked || !actual.TokenLocked {
		return fmt.Errorf("legacy lock evidence is incomplete: %+v", actual)
	}
	return nil
}

func assertMariaDBFIX007CompleteLockEvidence(actual mariaDBFIX007LockEvidence) error {
	if err := assertMariaDBFIX007LegacyLockEvidence(actual); err != nil {
		return err
	}
	if !actual.LaneLocked {
		return fmt.Errorf("lane lock was not observed")
	}
	if !actual.RotationLocked {
		return fmt.Errorf("runtime rotation lock was not observed")
	}
	return nil
}

func TestMariaDBFIX007RedEvidence(t *testing.T) {
	t.Run("legacy barrier accepts missing lane and rotation evidence", func(t *testing.T) {
		actual := mariaDBFIX007LockEvidence{
			HostLocked:    true,
			PolicyLocked:  true,
			ServiceLocked: true,
			TokenLocked:   true,
		}
		if err := assertMariaDBFIX007LegacyLockEvidence(actual); err != nil {
			t.Fatalf("legacy barrier unexpectedly rejected the R6 fixture: %v", err)
		}
		if err := assertMariaDBFIX007CompleteLockEvidence(actual); err == nil {
			t.Errorf("complete barrier accepted missing lane and rotation lock evidence")
		}
	})
}
