package store

import (
	"reflect"
	"testing"
	"time"
)

type mariaDBFIX007PolicyPairCase struct {
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

func mariaDBFIX007PolicyPairInventory() []mariaDBFIX007PolicyPairCase {
	return []mariaDBFIX007PolicyPairCase{
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
	mariaDBFIX007TokenPrevious = "previous"
	mariaDBFIX007TokenStaged   = "staged"
	mariaDBFIX007TokenMutation = "mutation"
	mariaDBFIX007TokenCleared  = "cleared"

	mariaDBFIX007ProofNone      = "none"
	mariaDBFIX007ProofLocal     = "local_staged"
	mariaDBFIX007ProofHeartbeat = "heartbeat_proved"
	mariaDBFIX007ProofActivated = "activated"

	mariaDBFIX007CancelNone         = "none"
	mariaDBFIX007CancelImmediate    = "immediate_canceled"
	mariaDBFIX007CancelAcknowledged = "acknowledged_canceled"
	mariaDBFIX007CancelEmergency    = "emergency_revoked"
)

type mariaDBFIX007ExpectedRuntimeTokenState struct {
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

type mariaDBFIX007RuntimeMatrixCase struct {
	path               string
	tokenMutation      string
	iterations         int
	expected           mariaDBFIX007ExpectedRuntimeTokenState
	requiredLockPhases []mariaDBRuntimeTokenRotationLockPhase
	exactFinalOracle   bool
	genericOnlyOracle  bool
}

func mariaDBFIX007RuntimeMatrixInventory() []mariaDBFIX007RuntimeMatrixCase {
	return []mariaDBFIX007RuntimeMatrixCase{
		{
			path: "stage", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationLaneLocksHeld,
				mariaDBRuntimeTokenRotationPolicyLocksHeld,
			},
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "created", PreStateFixture: "no rotation; active previous token",
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 1,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ActivationProofState: mariaDBFIX007ProofNone,
				CancelState: mariaDBFIX007CancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "created", PreStateFixture: "no rotation; active previous token",
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 1,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ActivationProofState: mariaDBFIX007ProofNone,
				CancelState: mariaDBFIX007CancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 2,
				RotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, Revision: 3,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofLocal, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 2,
				RotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, Revision: 3,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofLocal, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "local staged rotation with matching heartbeat fixture",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, Revision: 4,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofHeartbeat, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "local staged rotation with matching heartbeat fixture",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationLocalStaged, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, Revision: 4,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretPresent: true, ReplaySecretConsumed: true, ClaimFencePresent: true,
				ActivationProofState: mariaDBFIX007ProofHeartbeat, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "heartbeat-proved rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, PreRevision: 4,
				RotationStatus: SystemUpdateRuntimeTokenRotationActivated, Revision: 5,
				CurrentTokenID: mariaDBFIX007TokenStaged, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBFIX007ProofActivated, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "heartbeat-proved rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationHeartbeatProved, PreRevision: 4,
				RotationStatus: SystemUpdateRuntimeTokenRotationActivated, Revision: 5,
				CurrentTokenID: mariaDBFIX007TokenStaged, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBFIX007ProofActivated, CancelState: mariaDBFIX007CancelNone,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelImmediate,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelImmediate,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed cancel-requested rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationCancelRequested, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 4,
				CurrentTokenID: mariaDBFIX007TokenMutation, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				PreviousTokenRevoked: true, StagedTokenRevoked: true, ReplaySecretConsumed: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelAcknowledged,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "claimed cancel-requested rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationCancelRequested, PreRevision: 3,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 4,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ReplaySecretConsumed: true, ActivationProofState: mariaDBFIX007ProofNone,
				CancelState: mariaDBFIX007CancelAcknowledged, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "success",
			},
		},
		{
			path: "emergency_revoke", tokenMutation: "rotate", iterations: 3, exactFinalOracle: true,
			requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
				mariaDBRuntimeTokenRotationHostLocksHeld,
				mariaDBRuntimeTokenRotationRotationLocksHeld,
			},
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelEmergency,
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
			expected: mariaDBFIX007ExpectedRuntimeTokenState{
				OperationResult: "applied", PreStateFixture: "unclaimed staged rotation",
				PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
				RotationStatus: SystemUpdateRuntimeTokenRotationCanceled, Revision: 2,
				CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
				StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
				CurrentTokenRevoked: true, PreviousTokenRevoked: true, StagedTokenRevoked: true,
				ActivationProofState: mariaDBFIX007ProofNone, CancelState: mariaDBFIX007CancelEmergency,
				OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
				ConcurrentMutation: "revoke", ConcurrentMutationResult: "not_found",
			},
		},
	}
}

func mariaDBFIX007UpdaterRuntimeClaimExpectedCase() mariaDBFIX007RuntimeMatrixCase {
	return mariaDBFIX007RuntimeMatrixCase{
		path: "claim_staged_credential", iterations: 3, exactFinalOracle: true,
		requiredLockPhases: []mariaDBRuntimeTokenRotationLockPhase{
			mariaDBRuntimeTokenRotationHostLocksHeld,
			mariaDBRuntimeTokenRotationRotationLocksHeld,
			mariaDBRuntimeTokenRotationLaneLocksHeld,
			mariaDBRuntimeTokenRotationPolicyLocksHeld,
		},
		expected: mariaDBFIX007ExpectedRuntimeTokenState{
			OperationResult: "claimed", PreStateFixture: "staged rotation before first credential claim",
			PreRotationStatus: SystemUpdateRuntimeTokenRotationStaged, PreRevision: 1,
			RotationStatus: SystemUpdateRuntimeTokenRotationStaged, Revision: 2,
			CurrentTokenID: mariaDBFIX007TokenPrevious, PreviousTokenID: mariaDBFIX007TokenPrevious,
			StagedTokenID: mariaDBFIX007TokenStaged, StagedPreviousTokenID: mariaDBFIX007TokenCleared,
			StagedTokenRevoked: true, ReplaySecretPresent: true, ReplaySecretConsumed: true,
			ClaimFencePresent: true, ActivationProofState: mariaDBFIX007ProofNone,
			CancelState: mariaDBFIX007CancelNone, OwnershipEpoch: "unchanged", PolicyRevision: "unchanged",
			ConcurrentMutation: "none", ConcurrentMutationResult: "none",
		},
	}
}

func TestMariaDBFIX007MatrixInventorySelfCheck(t *testing.T) {
	policyCases := mariaDBFIX007PolicyPairInventory()
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

	runtimeCases := mariaDBFIX007RuntimeMatrixInventory()
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

func assertMariaDBFIX007RuntimeLockPhaseSequence(
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
