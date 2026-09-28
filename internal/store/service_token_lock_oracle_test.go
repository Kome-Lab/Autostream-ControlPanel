package store

import (
	"reflect"
	"testing"
	"time"
)

type mariaDBServiceTokenPolicyPairCase struct {
	name                    string
	kind                    string
	firstOperation          string
	secondOperation         string
	iterations              int
	firstExpectedResult     string
	secondExpectedResult    string
	strongFinalOracle       bool
	requireLaneEvidence     bool
	requireRotationEvidence bool
}

func mariaDBServiceTokenPolicyPairInventory() []mariaDBServiceTokenPolicyPairCase {
	return []mariaDBServiceTokenPolicyPairCase{
		{
			name: "activate_vs_activate", kind: "cycle",
			firstOperation: "activate", secondOperation: "activate", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "activate_vs_deactivate", kind: "cycle",
			firstOperation: "activate", secondOperation: "deactivate", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "deactivate_vs_activate", kind: "cycle",
			firstOperation: "deactivate", secondOperation: "activate", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "activate_vs_rotate", kind: "token_mutation",
			firstOperation: "activate", secondOperation: "rotate", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "activate_vs_revoke", kind: "token_mutation",
			firstOperation: "activate", secondOperation: "revoke", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "deactivate_vs_rotate", kind: "token_mutation",
			firstOperation: "deactivate", secondOperation: "rotate", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "deactivate_vs_revoke", kind: "token_mutation",
			firstOperation: "deactivate", secondOperation: "revoke", iterations: 3,
			firstExpectedResult: "success", secondExpectedResult: "success", strongFinalOracle: true,
		},
		{
			name: "runtime_claim_vs_activate", kind: "runtime_rotation",
			firstOperation: "claim_staged_credential", secondOperation: "activate", iterations: 3,
			firstExpectedResult: "claimed", secondExpectedResult: "success", strongFinalOracle: true,
			requireLaneEvidence: true, requireRotationEvidence: true,
		},
		{
			name: "runtime_claim_vs_deactivate", kind: "runtime_rotation",
			firstOperation: "claim_staged_credential", secondOperation: "deactivate", iterations: 3,
			firstExpectedResult: "claimed", secondExpectedResult: "success", strongFinalOracle: true,
			requireLaneEvidence: true, requireRotationEvidence: true,
		},
	}
}

const (
	mariaDBServiceTokenTokenPrevious = "previous"
	mariaDBServiceTokenTokenStaged   = "staged"
	mariaDBServiceTokenTokenMutation = "mutation"
	mariaDBServiceTokenTokenCleared  = "cleared"

	mariaDBServiceTokenProofNone      = "none"
	mariaDBServiceTokenProofLocal     = "local_staged"
	mariaDBServiceTokenProofHeartbeat = "heartbeat_proved"
	mariaDBServiceTokenProofActivated = "activated"

	mariaDBServiceTokenCancelNone         = "none"
	mariaDBServiceTokenCancelImmediate    = "immediate_canceled"
	mariaDBServiceTokenCancelAcknowledged = "acknowledged_canceled"
	mariaDBServiceTokenCancelEmergency    = "emergency_revoked"
)

type mariaDBServiceTokenExpectedRuntimeTokenState struct {
	OperationResult          string
	PreStateFixture          string
	PreRotationStatus        string
	PreRevision              int64
	RotationStatus           string
	Revision                 int64
	CurrentTokenID           string
	PreviousTokenID          string
	StagedTokenID            string
	StagedPreviousTokenID    string
	CurrentTokenRevoked      bool
	PreviousTokenRevoked     bool
	StagedTokenRevoked       bool
	ReplaySecretPresent      bool
	ReplaySecretConsumed     bool
	ClaimFencePresent        bool
	ActivationProofState     string
	CancelState              string
	OwnershipEpoch           string
	PolicyRevision           string
	ConcurrentMutation       string
	ConcurrentMutationResult string
}

type mariaDBServiceTokenRuntimeMatrixCase struct {
	path               string
	tokenMutation      string
	iterations         int
	expected           mariaDBServiceTokenExpectedRuntimeTokenState
	requiredLockPhases []mariaDBRuntimeTokenRotationLockPhase
	exactFinalOracle   bool
	genericOnlyOracle  bool
}

func mariaDBServiceTokenRuntimeMatrixInventory() []mariaDBServiceTokenRuntimeMatrixCase {
	return []mariaDBServiceTokenRuntimeMatrixCase{
		{
			path: "stage", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "created", PreStateFixture: "no rotation; active previous token",
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 1,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ActivationProofState: mariaDBServiceTokenProofNone,
				CancelState: mariaDBServiceTokenCancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "stage", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "created", PreStateFixture: "no rotation; active previous token",
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 1,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ActivationProofState: mariaDBServiceTokenProofNone,
				CancelState: mariaDBServiceTokenCancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "claim_staged_credential", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "claim_staged_credential", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "local_staged", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 2,
				RotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, Revision: 3,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofLocal, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "local_staged", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 2,
				RotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, Revision: 3,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofLocal, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "heartbeat_proof", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "local staged rotation with matching heartbeat fixture",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, Revision: 4,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofHeartbeat, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "heartbeat_proof", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "local staged rotation with matching heartbeat fixture",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, Revision: 4,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBServiceTokenProofHeartbeat, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "activate", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "heartbeat-proved rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, PreRevision: 4,
				RotationStatus: SystemUpdateRuntimeTokenRotationActivated, Revision: 5,
				CurrentTokenID: mariaDBServiceTokenTokenStaged, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBServiceTokenProofActivated, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "not_found",
			},
		},
		{
			path: "activate", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "heartbeat-proved rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, PreRevision: 4,
				RotationStatus: SystemUpdateRuntimeTokenRotationActivated, Revision: 5,
				CurrentTokenID: mariaDBServiceTokenTokenStaged, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBServiceTokenProofActivated, CancelState: mariaDBServiceTokenCancelNone,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "not_found",
			},
		},
		{
			path: "cancel", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelImmediate,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "cancel", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelImmediate,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "acknowledge_cancel", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed cancel-requested rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationCancelRequested, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 4,
				CurrentTokenID: mariaDBServiceTokenTokenMutation, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelAcknowledged,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "acknowledge_cancel", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed cancel-requested rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationCancelRequested, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 4,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretConsumed: true, ActivationProofState: mariaDBServiceTokenProofNone,
				CancelState: mariaDBServiceTokenCancelAcknowledged, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "emergency_revoke", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelEmergency,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "rotate", ConcurrentMutationResult: "not_found",
			},
		},
		{
			path: "emergency_revoke", tokenMutation: "revoke", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBServiceTokenExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
				StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBServiceTokenProofNone, CancelState: mariaDBServiceTokenCancelEmergency,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "not_found",
			},
		},
	}
}

func mariaDBServiceTokenUpdaterRuntimeClaimExpectedCase() mariaDBServiceTokenRuntimeMatrixCase {
	return mariaDBServiceTokenRuntimeMatrixCase{
		path: "claim_staged_credential", iterations: 3, exactFinalOracle: true,
		requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		expected: mariaDBServiceTokenExpectedRuntimeTokenState{
			OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
			PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
			RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
			CurrentTokenID: mariaDBServiceTokenTokenPrevious, PreviousTokenID: mariaDBServiceTokenTokenPrevious,
			StagedTokenID: mariaDBServiceTokenTokenStaged, StagedPreviousTokenID: mariaDBServiceTokenTokenCleared,
			StagedTokenRevoked: true, ReplaySecretPresent: true, ReplaySecretConsumed: true,
			ClaimFencePresent: true, ActivationProofState: mariaDBServiceTokenProofNone,
			CancelState: mariaDBServiceTokenCancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
			ConcurrentMutation: "none", ConcurrentMutationResult: "none",
		},
	}
}

func TestMariaDBServiceTokenMatrixInventorySelfCheck(t *testing.T) {
	policyCases := mariaDBServiceTokenPolicyPairInventory()
	if len(policyCases) != 9 {
		t.Fatalf("policy pair count = %d, want 9", len(policyCases))
	}
	policyKinds := map[string]int{}
	policyNames := map[string]struct{}{}
	for _, testCase := range policyCases {
		if _, exists := policyNames[testCase.name]; exists {
			t.Fatalf("duplicate policy pair %q", testCase.name)
		}
		policyNames[testCase.name] = struct{}{}
		policyKinds[testCase.kind]++
		if testCase.iterations < 3 {
			t.Fatalf("policy pair %q iterations = %d, want at least 3", testCase.name, testCase.iterations)
		}
		if !testCase.strongFinalOracle || testCase.firstExpectedResult == "" || testCase.secondExpectedResult == "" {
			t.Fatalf("policy pair %q does not define a strong pair-specific final result", testCase.name)
		}
		if testCase.kind == "runtime_rotation" && (!testCase.requireLaneEvidence || !testCase.requireRotationEvidence) {
			t.Fatalf("policy pair %q does not require lane and rotation evidence", testCase.name)
		}
	}
	if policyKinds["cycle"] != 3 || policyKinds["token_mutation"] != 4 || policyKinds["runtime_rotation"] != 2 {
		t.Fatalf("policy pair kinds = %#v, want cycle=3 token_mutation=4 runtime_rotation=2", policyKinds)
	}

	runtimeCases := mariaDBServiceTokenRuntimeMatrixInventory()
	if len(runtimeCases) != 16 {
		t.Fatalf("runtime matrix pair count = %d, want 16", len(runtimeCases))
	}
	wantLockPhases := map[string][]mariaDBRuntimeTokenRotationLockPhase{
		"stage": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		"claim_staged_credential": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		"local_staged": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		"heartbeat_proof": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		"activate": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
		},
		"cancel": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
		},
		"acknowledge_cancel": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		"emergency_revoke": {
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
		},
	}
	seen := make(map[string]map[string]int)
	for _, testCase := range runtimeCases {
		if seen[testCase.path] == nil {
			seen[testCase.path] = make(map[string]int)
		}
		seen[testCase.path][testCase.tokenMutation]++
		if testCase.iterations < 3 {
			t.Fatalf("runtime pair %s/%s iterations = %d, want at least 3", testCase.path, testCase.tokenMutation, testCase.iterations)
		}
		if !testCase.exactFinalOracle || testCase.genericOnlyOracle {
			t.Fatalf("runtime pair %s/%s is generic-only", testCase.path, testCase.tokenMutation)
		}
		expected := testCase.expected
		if expected.OperationResult == "" || expected.PreStateFixture == "" ||
			expected.RotationStatus == "" || expected.Revision < 1 ||
			expected.CurrentTokenID == "" || expected.PreviousTokenID == "" ||
			expected.StagedTokenID == "" || expected.StagedPreviousTokenID == "" ||
			expected.ActivationProofState == "" || expected.CancelState == "" ||
			expected.OwnershipEpoch != "unchanged" || expected.PolicyRevision != "unchanged" ||
			expected.ConcurrentMutation != testCase.tokenMutation || expected.ConcurrentMutationResult == "" {
			t.Fatalf("runtime pair %s/%s lacks its dedicated expected-state definition: %#v", testCase.path, testCase.tokenMutation, expected)
		}
		wantPhases, exists := wantLockPhases[testCase.path]
		if !exists || !reflect.DeepEqual(testCase.requiredLockPhases, wantPhases) {
			t.Fatalf(
				"runtime pair %s/%s lock phases = %#v, want %#v",
				testCase.path,
				testCase.tokenMutation,
				testCase.requiredLockPhases,
				wantPhases,
			)
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
}

func TestMariaDBRuntimeTokenRotationLockObserverNilIsNoOp(t *testing.T) {
	observeMariaDBRuntimeTokenRotationLockPhase(
		t.Context(),
		"claim_system_update_runtime_token_rotation_credential",
		mariaDBRuntimeTokenRotationRotationLocksHeld,
	)
}

func assertMariaDBServiceTokenRuntimeLockPhaseSequence(
	t *testing.T,
	phases <-chan mariaDBRuntimeTokenRotationLockPhase,
	expected []mariaDBRuntimeTokenRotationLockPhase,
) {
	t.Helper()
	for index, want := range expected {
		select {
		case got := <-phases:
			if got != want {
				t.Fatalf("runtime lock phase %d = %q, want %q", index+1, got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("runtime lock phase %d (%q) was not observed", index+1, want)
		}
	}
}
