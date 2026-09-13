package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

type mariaDBFIX007RuntimeSemanticSnapshot struct {
	rotationCount  int
	rotationExists bool
	rotation       SystemUpdateRuntimeTokenRotation
	service        RegisteredService
	ownership      SystemUpdateExecutionHost
	policy         UpdaterPolicy
	services       map[string]RegisteredService
	tokens         map[string]ServiceToken
}

type mariaDBFIX008RuntimeValueSource string

// Generated IDs are expressed as relations to the operation or token-mutation
// result fixed by the pre-run expected model. Durable post rows are never an
// expected-value source.
const (
	mariaDBFIX008RuntimeValueLiteral                mariaDBFIX008RuntimeValueSource = "literal"
	mariaDBFIX008RuntimeValueOperationRotationID    mariaDBFIX008RuntimeValueSource = "operation_result.rotation_id"
	mariaDBFIX008RuntimeValueOperationStagedTokenID mariaDBFIX008RuntimeValueSource = "operation_result.staged_token_id"
	mariaDBFIX008RuntimeValueMutationTokenID        mariaDBFIX008RuntimeValueSource = "mutation_result.token_id"
)

type mariaDBFIX008ExpectedRuntimeValue struct {
	Source mariaDBFIX008RuntimeValueSource
	Value  string
}

type mariaDBFIX008ExpectedRuntimeState struct {
	Path                                string
	TokenMutation                       string
	OperationResult                     string
	RotationCount                       int
	RotationExists                      bool
	RotationID                          mariaDBFIX008ExpectedRuntimeValue
	ServiceID                           string
	ExecutionHostID                     string
	RotationStatus                      string
	Revision                            int64
	ExpectedOwnershipEpoch              int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	PreviousTokenID                     string
	OperationTokenID                    string
	CurrentTokenID                      mariaDBFIX008ExpectedRuntimeValue
	StagedTokenID                       mariaDBFIX008ExpectedRuntimeValue
	CurrentTokenRevoked                 bool
	PreviousTokenRevoked                bool
	StagedTokenRevoked                  bool
	ReplaySecretPresent                 bool
	ReplaySecretConsumed                bool
	ClaimFencePresent                   bool
	ClaimID                             string
	ActivationProofState                string
	CancelState                         string
	ConcurrentMutationResult            string
	StageTime                           time.Time
	PreService                          RegisteredService
	PreOwnership                        SystemUpdateExecutionHost
	PrePolicy                           UpdaterPolicy
}

type mariaDBFIX008RuntimeActualState struct {
	OperationResult   string
	OperationRotation SystemUpdateRuntimeTokenRotation
	RotationCount     int
	RotationExists    bool
	Rotation          SystemUpdateRuntimeTokenRotation
	Service           RegisteredService
	Ownership         SystemUpdateExecutionHost
	Policy            UpdaterPolicy
	Services          map[string]RegisteredService
	Tokens            map[string]ServiceToken
	MutationOutcome   mariaDBServiceTokenMutationResult
}

func deriveMariaDBFIX008ExpectedRuntimeState(
	testCase mariaDBFIX007RuntimeMatrixCase,
	operation mariaDBFIX005RuntimeOperation,
	pre mariaDBFIX007RuntimeSemanticSnapshot,
) (mariaDBFIX008ExpectedRuntimeState, error) {
	if strings.TrimSpace(operation.tokenID) == "" {
		return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf("operation token ID is empty")
	}
	if pre.service.TokenID != operation.tokenID {
		return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
			"pre-state current token %q does not equal operation token %q",
			pre.service.TokenID,
			operation.tokenID,
		)
	}
	if testCase.expected.PreviousTokenID != mariaDBFIX007TokenPrevious ||
		testCase.expected.StagedTokenID != mariaDBFIX007TokenStaged ||
		testCase.expected.StagedPreviousTokenID != mariaDBFIX007TokenCleared {
		return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
			"runtime token selector contract is incomplete: %#v",
			testCase.expected,
		)
	}
	rotationID := mariaDBFIX008ExpectedRuntimeValue{
		Source: mariaDBFIX008RuntimeValueLiteral,
		Value:  operation.rotationID,
	}
	stagedTokenID := mariaDBFIX008ExpectedRuntimeValue{Source: mariaDBFIX008RuntimeValueLiteral}
	if pre.rotationExists {
		if pre.rotation.ID != operation.rotationID {
			return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
				"pre-state rotation ID %q does not equal operation rotation ID %q",
				pre.rotation.ID,
				operation.rotationID,
			)
		}
		if pre.rotation.PreviousTokenID != pre.service.TokenID ||
			pre.rotation.PreviousTokenID != operation.tokenID {
			return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
				"pre-state previous token %q does not equal pre service %q and operation %q",
				pre.rotation.PreviousTokenID,
				pre.service.TokenID,
				operation.tokenID,
			)
		}
		if strings.TrimSpace(pre.rotation.StagedTokenID) == "" {
			return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf("pre-state staged token ID is empty")
		}
		stagedTokenID.Value = pre.rotation.StagedTokenID
	} else {
		if testCase.path != "stage" || operation.rotationID != "" {
			return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
				"only stage may derive generated IDs from its operation result: path=%q rotation=%q",
				testCase.path,
				operation.rotationID,
			)
		}
		rotationID = mariaDBFIX008ExpectedRuntimeValue{Source: mariaDBFIX008RuntimeValueOperationRotationID}
		stagedTokenID = mariaDBFIX008ExpectedRuntimeValue{Source: mariaDBFIX008RuntimeValueOperationStagedTokenID}
	}
	currentTokenID := mariaDBFIX008ExpectedRuntimeValue{Source: mariaDBFIX008RuntimeValueLiteral}
	switch testCase.expected.CurrentTokenID {
	case mariaDBFIX007TokenPrevious:
		currentTokenID.Value = pre.service.TokenID
	case mariaDBFIX007TokenStaged:
		currentTokenID = stagedTokenID
	case mariaDBFIX007TokenMutation:
		currentTokenID = mariaDBFIX008ExpectedRuntimeValue{Source: mariaDBFIX008RuntimeValueMutationTokenID}
	default:
		return mariaDBFIX008ExpectedRuntimeState{}, fmt.Errorf(
			"unknown current-token selector %q",
			testCase.expected.CurrentTokenID,
		)
	}
	return mariaDBFIX008ExpectedRuntimeState{
		Path:                                testCase.path,
		TokenMutation:                       testCase.tokenMutation,
		OperationResult:                     testCase.expected.OperationResult,
		RotationCount:                       1,
		RotationExists:                      true,
		RotationID:                          rotationID,
		ServiceID:                           pre.service.ServiceID,
		ExecutionHostID:                     pre.ownership.ExecutionHostID,
		RotationStatus:                      testCase.expected.RotationStatus,
		Revision:                            testCase.expected.Revision,
		ExpectedOwnershipEpoch:              pre.ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        pre.policy.Revision,
		ExpectedProjectionRevision:          pre.policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: pre.policy.LocalExecutorPolicyRevision,
		PreviousTokenID:                     pre.service.TokenID,
		OperationTokenID:                    operation.tokenID,
		CurrentTokenID:                      currentTokenID,
		StagedTokenID:                       stagedTokenID,
		CurrentTokenRevoked:                 testCase.expected.CurrentTokenRevoked,
		PreviousTokenRevoked:                testCase.expected.PreviousTokenRevoked,
		StagedTokenRevoked:                  testCase.expected.StagedTokenRevoked,
		ReplaySecretPresent:                 testCase.expected.ReplaySecretPresent,
		ReplaySecretConsumed:                testCase.expected.ReplaySecretConsumed,
		ClaimFencePresent:                   testCase.expected.ClaimFencePresent,
		ClaimID:                             operation.claimID,
		ActivationProofState:                testCase.expected.ActivationProofState,
		CancelState:                         testCase.expected.CancelState,
		ConcurrentMutationResult:            testCase.expected.ConcurrentMutationResult,
		StageTime:                           operation.stageTime,
		PreService:                          pre.service,
		PreOwnership:                        pre.ownership,
		PrePolicy:                           pre.policy,
	}, nil
}

func mariaDBFIX008PreviousTokenAnchorSources(expected mariaDBFIX008ExpectedRuntimeState) []string {
	sources := make([]string, 0, 2)
	if expected.PreviousTokenID == expected.PreService.TokenID {
		sources = append(sources, "pre.service.TokenID")
	}
	if expected.PreviousTokenID == expected.OperationTokenID {
		sources = append(sources, "operation.tokenID")
	}
	return sources
}

func mariaDBFIX008PostDerivedExpectedSelectorCount(expected mariaDBFIX008ExpectedRuntimeState) int {
	count := 0
	for _, value := range []mariaDBFIX008ExpectedRuntimeValue{
		expected.RotationID,
		expected.CurrentTokenID,
		expected.StagedTokenID,
	} {
		if strings.HasPrefix(string(value.Source), "post.") {
			count++
		}
	}
	return count
}

func snapshotMariaDBFIX007RuntimeSemanticState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBFIX005PullFixture,
	rotationID string,
) mariaDBFIX007RuntimeSemanticSnapshot {
	t.Helper()
	snapshot := mariaDBFIX007RuntimeSemanticSnapshot{
		services: make(map[string]RegisteredService),
		tokens:   make(map[string]ServiceToken),
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM system_update_runtime_token_rotations
WHERE service_id LIKE ? OR execution_host_id LIKE ? OR idempotency_key LIKE ?`,
		fixture.cleanup.prefix+"%",
		fixture.cleanup.prefix+"%",
		fixture.cleanup.prefix+"%",
	).Scan(&snapshot.rotationCount); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(rotationID) != "" {
		rotation, err := scanSystemUpdateRuntimeTokenRotation(db.QueryRowContext(
			ctx,
			systemUpdateRuntimeTokenRotationSelect+` WHERE id = ?`,
			rotationID,
		))
		if err != nil {
			t.Fatal(err)
		}
		snapshot.rotationExists = true
		snapshot.rotation = rotation
	}
	service, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.service = service
	ownership, err := fixture.updates.GetSystemUpdateExecutionHost(
		ctx,
		fixture.params.ExecutionHostID,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ownership = ownership
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.policy = policy
	services, err := fixture.auth.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, registered := range services {
		snapshot.services[registered.ServiceID] = registered
	}
	tokens, err := fixture.auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		snapshot.tokens[token.ID] = token
	}
	return snapshot
}

func assertMariaDBFIX007RuntimePreState(
	t *testing.T,
	testCase mariaDBFIX007RuntimeMatrixCase,
	operation mariaDBFIX005RuntimeOperation,
	pre mariaDBFIX007RuntimeSemanticSnapshot,
) {
	t.Helper()
	wantRotationCount := 1
	if testCase.path == "stage" {
		wantRotationCount = 0
	}
	if pre.rotationCount != wantRotationCount {
		t.Fatalf(
			"%s/%s pre-state rotation count = %d, want %d (%s)",
			testCase.path,
			testCase.tokenMutation,
			pre.rotationCount,
			wantRotationCount,
			testCase.expected.PreStateFixture,
		)
	}
	if testCase.path == "stage" {
		if pre.rotationExists || operation.rotationID != "" {
			t.Fatalf("stage pre-state unexpectedly contains rotation %q", operation.rotationID)
		}
		if pre.service.TokenID != operation.tokenID {
			t.Fatalf("stage pre-state current token = %q, want %q", pre.service.TokenID, operation.tokenID)
		}
		assertMariaDBFIX007TokenState(t, pre.tokens, operation.tokenID, false, "stage pre-state current")
		return
	}
	if !pre.rotationExists || pre.rotation.ID != operation.rotationID {
		t.Fatalf("%s pre-state rotation = %s, want ID %q", testCase.path, formatSafeSensitiveCompositeDiagnostic(pre.rotation), operation.rotationID)
	}
	if pre.rotation.Status != testCase.expected.PreRotationStatus ||
		pre.rotation.Revision != testCase.expected.PreRevision {
		t.Fatalf(
			"%s pre-state = status %q revision %d, want status %q revision %d",
			testCase.path,
			pre.rotation.Status,
			pre.rotation.Revision,
			testCase.expected.PreRotationStatus,
			testCase.expected.PreRevision,
		)
	}
	if pre.rotation.ServiceID != pre.service.ServiceID ||
		pre.rotation.ExecutionHostID != pre.ownership.ExecutionHostID ||
		pre.rotation.PreviousTokenID != operation.tokenID ||
		pre.service.TokenID != pre.rotation.PreviousTokenID {
		t.Fatalf("%s pre-state host/service/rotation closure is split: ownership=%s service=%s rotation=%s", testCase.path, formatSafeSensitiveCompositeDiagnostic(pre.ownership), formatSafeRegisteredServiceDiagnostic(pre.service), formatSafeSensitiveCompositeDiagnostic(pre.rotation))
	}
	if pre.rotation.ExpectedOwnershipEpoch != pre.ownership.OwnershipEpoch ||
		pre.rotation.ExpectedSourcePolicyRevision != pre.policy.Revision ||
		pre.rotation.ExpectedProjectionRevision != pre.policy.ProjectionRevision ||
		pre.rotation.ExpectedLocalExecutorPolicyRevision != pre.policy.LocalExecutorPolicyRevision {
		t.Fatalf("%s pre-state ownership/policy fence is split: rotation=%s ownership=%s policy=%s", testCase.path, formatSafeSensitiveCompositeDiagnostic(pre.rotation), formatSafeSensitiveCompositeDiagnostic(pre.ownership), formatSafeSensitiveCompositeDiagnostic(pre.policy))
	}
	assertMariaDBFIX007TokenState(t, pre.tokens, pre.rotation.PreviousTokenID, false, testCase.path+" pre-state previous")
	assertMariaDBFIX007TokenState(t, pre.tokens, pre.rotation.StagedTokenID, true, testCase.path+" pre-state staged")
	if pre.service.StagedNodePreviousTokenID != "" || pre.service.StagedNodeTokenID != "" {
		t.Fatalf("%s pre-state contains unrelated staged service references: %#v", testCase.path, pre.service)
	}
	assertMariaDBFIX007RuntimePreReplayState(t, testCase.path, operation, pre.rotation)
}

func assertMariaDBFIX007RuntimePreReplayState(
	t *testing.T,
	path string,
	operation mariaDBFIX005RuntimeOperation,
	rotation SystemUpdateRuntimeTokenRotation,
) {
	t.Helper()
	wantSecret := true
	wantClaimed := false
	wantClaimFence := false
	switch path {
	case "local_staged", "heartbeat_proof", "activate", "acknowledge_cancel":
		wantClaimed = true
		wantClaimFence = true
	}
	assertMariaDBFIX007RuntimeReplayState(
		t,
		rotation,
		wantSecret,
		wantClaimed,
		wantClaimFence,
		path+" pre-state",
	)
	if wantClaimed {
		if operation.claimID == "" ||
			rotation.credentialClaimIDHash != runtimeTokenRotationClaimIDHash(operation.claimID) ||
			rotation.credentialClaimRevision != 1 {
			t.Fatalf("%s pre-state claim owner/fence is wrong: claim_present=%t rotation=%s", path, operation.claimID != "", formatSafeSensitiveCompositeDiagnostic(rotation))
		}
	}
}

func assertMariaDBFIX007RuntimeExactFinalState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBFIX005PullFixture,
	expected mariaDBFIX008ExpectedRuntimeState,
	runtimeOutcome mariaDBFIX007RuntimeOperationResult,
	mutationOutcome mariaDBServiceTokenMutationResult,
) {
	t.Helper()
	rotationID, err := mariaDBFIX008ResolveExpectedRuntimeValue(
		expected.RotationID,
		mariaDBFIX008RuntimeActualState{
			OperationRotation: runtimeOutcome.rotation,
			MutationOutcome:   mutationOutcome,
		},
	)
	if err != nil {
		t.Fatalf("%s resolve expected rotation ID: %v", expected.Path, err)
	}
	post := snapshotMariaDBFIX007RuntimeSemanticState(t, ctx, db, fixture, rotationID)
	actual := mariaDBFIX008RuntimeActualState{
		OperationResult:   runtimeOutcome.result,
		OperationRotation: runtimeOutcome.rotation,
		RotationCount:     post.rotationCount,
		RotationExists:    post.rotationExists,
		Rotation:          post.rotation,
		Service:           post.service,
		Ownership:         post.ownership,
		Policy:            post.policy,
		Services:          post.services,
		Tokens:            post.tokens,
		MutationOutcome:   mutationOutcome,
	}
	if runtimeOutcome.err != nil {
		t.Fatalf("%s runtime operation: %v", expected.Path, runtimeOutcome.err)
	}
	if mismatches := compareMariaDBFIX008RuntimeState(expected, actual); len(mismatches) != 0 {
		t.Fatalf("%s runtime final-state mismatch: %s", expected.Path, mariaDBFIX008FormatMismatches(mismatches))
	}
}

func mariaDBFIX007EqualPublicRuntimeRotation(a, b SystemUpdateRuntimeTokenRotation) bool {
	return a.ID == b.ID &&
		a.ServiceID == b.ServiceID &&
		a.ExecutionHostID == b.ExecutionHostID &&
		a.Status == b.Status &&
		a.Revision == b.Revision &&
		a.ExpectedOwnershipEpoch == b.ExpectedOwnershipEpoch &&
		a.ExpectedSourcePolicyRevision == b.ExpectedSourcePolicyRevision &&
		a.ExpectedProjectionRevision == b.ExpectedProjectionRevision &&
		a.ExpectedLocalExecutorPolicyRevision == b.ExpectedLocalExecutorPolicyRevision &&
		a.PreviousTokenID == b.PreviousTokenID &&
		a.StagedTokenID == b.StagedTokenID &&
		a.LocalStageReceiptID == b.LocalStageReceiptID &&
		mariaDBFIX007OptionalTimesEqual(a.CredentialClaimedAt, b.CredentialClaimedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.LocalStageAcknowledgedAt, b.LocalStageAcknowledgedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.LocalStagedAt, b.LocalStagedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.HeartbeatProvedAt, b.HeartbeatProvedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.ActivatedAt, b.ActivatedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.CancelRequestedAt, b.CancelRequestedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.CancelAcknowledgedAt, b.CancelAcknowledgedAt) &&
		mariaDBFIX007OptionalTimesEqual(a.CanceledAt, b.CanceledAt) &&
		a.EmergencyRevokedTokenID == b.EmergencyRevokedTokenID &&
		mariaDBFIX007OptionalTimesEqual(a.EmergencyRevokedAt, b.EmergencyRevokedAt) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt)
}

func mariaDBFIX007OptionalTimesEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
