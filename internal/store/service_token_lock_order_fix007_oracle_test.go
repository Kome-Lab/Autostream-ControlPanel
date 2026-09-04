package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
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

type mariaDBFIX007OwnershipSemanticSnapshot struct {
	hosts       map[string]SystemUpdateExecutionHost
	services    map[string]RegisteredService
	policies    map[string]UpdaterPolicy
	tokens      map[string]ServiceToken
	fixtureIDs  map[string]struct{}
	relevantIDs map[string]struct{}
}

type mariaDBFIX007OwnershipAction struct {
	fixture   mariaDBFIX005PullFixture
	operation string
	result    SystemUpdateExecutionHost
}

func snapshotMariaDBFIX007OwnershipSemanticState(
	t *testing.T,
	ctx context.Context,
	fixtures ...mariaDBFIX005PullFixture,
) mariaDBFIX007OwnershipSemanticSnapshot {
	t.Helper()
	if len(fixtures) == 0 {
		t.Fatal("ownership snapshot requires at least one fixture")
	}
	snapshot := mariaDBFIX007OwnershipSemanticSnapshot{
		hosts:       make(map[string]SystemUpdateExecutionHost),
		services:    make(map[string]RegisteredService),
		policies:    make(map[string]UpdaterPolicy),
		tokens:      make(map[string]ServiceToken),
		fixtureIDs:  make(map[string]struct{}),
		relevantIDs: make(map[string]struct{}),
	}
	auth := fixtures[0].auth
	policies := fixtures[0].policies
	services, err := auth.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		snapshot.services[service.ServiceID] = service
	}
	policyRows, err := policies.ListUpdaterPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range policyRows {
		snapshot.policies[policy.UpdaterID] = policy
	}
	tokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		snapshot.tokens[token.ID] = token
	}
	for _, fixture := range fixtures {
		host, err := fixture.updates.GetSystemUpdateExecutionHost(
			ctx,
			fixture.params.ExecutionHostID,
		)
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err == nil {
			snapshot.hosts[host.ExecutionHostID] = host
		}
		for _, serviceID := range []string{
			fixture.peerID,
			fixture.targetID,
			fixture.params.ServiceID,
		} {
			snapshot.fixtureIDs[serviceID] = struct{}{}
			service, exists := snapshot.services[serviceID]
			if !exists {
				t.Fatalf("ownership fixture service %q is missing", serviceID)
			}
			for _, tokenID := range []string{
				service.TokenID,
				service.StagedNodePreviousTokenID,
				service.StagedNodeTokenID,
			} {
				if tokenID != "" {
					snapshot.relevantIDs[tokenID] = struct{}{}
				}
			}
		}
	}
	return snapshot
}

func cloneMariaDBFIX007OwnershipSemanticState(
	source mariaDBFIX007OwnershipSemanticSnapshot,
) mariaDBFIX007OwnershipSemanticSnapshot {
	clone := mariaDBFIX007OwnershipSemanticSnapshot{
		hosts:       make(map[string]SystemUpdateExecutionHost, len(source.hosts)),
		services:    make(map[string]RegisteredService, len(source.services)),
		policies:    make(map[string]UpdaterPolicy, len(source.policies)),
		tokens:      make(map[string]ServiceToken, len(source.tokens)),
		fixtureIDs:  make(map[string]struct{}, len(source.fixtureIDs)),
		relevantIDs: make(map[string]struct{}, len(source.relevantIDs)),
	}
	for id, host := range source.hosts {
		clone.hosts[id] = host
	}
	for id, service := range source.services {
		clone.services[id] = service
	}
	for id, policy := range source.policies {
		clone.policies[id] = policy
	}
	for id, token := range source.tokens {
		clone.tokens[id] = token
	}
	for id := range source.fixtureIDs {
		clone.fixtureIDs[id] = struct{}{}
	}
	for id := range source.relevantIDs {
		clone.relevantIDs[id] = struct{}{}
	}
	return clone
}

func assertMariaDBFIX007StrongOwnershipFinalState(
	t *testing.T,
	ctx context.Context,
	before mariaDBFIX007OwnershipSemanticSnapshot,
	actions []mariaDBFIX007OwnershipAction,
	tokenMutation string,
	mutationOutcome mariaDBServiceTokenMutationResult,
) {
	t.Helper()
	if len(actions) == 0 {
		t.Fatal("strong ownership oracle requires at least one action")
	}
	expected := cloneMariaDBFIX007OwnershipSemanticState(before)
	fixtures := make([]mariaDBFIX005PullFixture, 0, len(actions))
	for _, action := range actions {
		fixtures = append(fixtures, action.fixture)
		hostID := action.fixture.params.ExecutionHostID
		host, exists := expected.hosts[hostID]
		if !exists {
			host = syntheticSystemUpdateExecutionHost(hostID)
		}
		pullPolicy, exists := expected.policies[action.fixture.params.ServiceID]
		if !exists {
			t.Fatalf("pull policy %q is missing", action.fixture.params.ServiceID)
		}
		pullService := expected.services[action.fixture.params.ServiceID]
		switch action.operation {
		case "activate":
			host.TransportMode = SystemUpdateTransportPullV2
			host.AgentServiceID = action.fixture.params.ServiceID
			host.OwnershipEpoch++
			host.PolicyRevision = pullPolicy.ProjectionRevision
			pullService.OwnershipEpoch = host.OwnershipEpoch
		case "deactivate":
			host.TransportMode = SystemUpdateTransportPullV2
			host.AgentServiceID = action.fixture.params.ServiceID
			host.OwnershipEpoch++
			host.PolicyRevision = pullPolicy.ProjectionRevision
			pullService.OwnershipEpoch = 0
		default:
			t.Fatalf("unknown ownership operation %q", action.operation)
		}
		expected.hosts[hostID] = host
		expected.services[pullService.ServiceID] = pullService
		assertMariaDBFIX007OwnershipResultMatches(t, action, host)
	}

	if tokenMutation != "" {
		oldTokenID := actions[0].fixture.agentToken.ID
		expected.relevantIDs[oldTokenID] = struct{}{}
		oldToken, exists := expected.tokens[oldTokenID]
		if !exists {
			t.Fatalf("mutated token %q is missing from ownership pre-state", oldTokenID)
		}
		switch tokenMutation {
		case "rotate":
			if mutationOutcome.err != nil || mutationOutcome.token.ID == "" {
				t.Fatalf("ownership/rotate result = %s, want success", formatSafeSensitiveCompositeDiagnostic(mutationOutcome))
			}
			oldToken.RevokedAt = nonNilMariaDBFIX007Time()
			expected.tokens[oldTokenID] = oldToken
			replacement := mutationOutcome.token
			replacement.RevokedAt = nil
			expected.tokens[replacement.ID] = replacement
			expected.relevantIDs[replacement.ID] = struct{}{}
			for serviceID, service := range expected.services {
				if service.TokenID != oldTokenID {
					continue
				}
				service.TokenID = replacement.ID
				service.LastHeartbeatAt = nil
				service.ReportedCapabilities = map[string]any{}
				service.StagedNodePreviousTokenID = ""
				service.StagedNodeTokenID = ""
				service.StagedNodeTokenHash = ""
				service.StagedNodeTokenScopes = nil
				service.StagedNodeTokenCiphertext = ""
				service.StagedNodeTokenNonce = ""
				service.StagedNodeActivationTokenHash = ""
				service.StagedNodeTokenAt = nil
				expected.services[serviceID] = service
			}
		case "revoke":
			if mutationOutcome.err != nil || mutationOutcome.token.ID != "" {
				t.Fatalf("ownership/revoke result = %s, want success", formatSafeSensitiveCompositeDiagnostic(mutationOutcome))
			}
			oldToken.RevokedAt = nonNilMariaDBFIX007Time()
			expected.tokens[oldTokenID] = oldToken
			for serviceID, service := range expected.services {
				if service.TokenID != oldTokenID {
					continue
				}
				service.LastHeartbeatAt = nil
				service.ReportedCapabilities = map[string]any{}
				expected.services[serviceID] = service
			}
		default:
			t.Fatalf("unknown token mutation %q", tokenMutation)
		}
	}

	after := snapshotMariaDBFIX007OwnershipSemanticState(t, ctx, fixtures...)
	if mismatches := compareMariaDBFIX008StrongOwnership(expected, after); len(mismatches) != 0 {
		t.Fatalf("strong ownership final-state mismatch: %s", mariaDBFIX008FormatMismatches(mismatches))
	}
}

func assertMariaDBFIX007OwnershipResultMatches(
	t *testing.T,
	action mariaDBFIX007OwnershipAction,
	want SystemUpdateExecutionHost,
) {
	t.Helper()
	if !mariaDBFIX007EqualOwnershipHost(action.result, want) {
		t.Fatalf("%s ownership result = %#v, want %#v", action.operation, action.result, want)
	}
}

func mariaDBFIX007EqualOwnershipHost(a, b SystemUpdateExecutionHost) bool {
	return a.ExecutionHostID == b.ExecutionHostID &&
		a.TransportMode == b.TransportMode &&
		a.AgentServiceID == b.AgentServiceID &&
		a.OwnershipEpoch == b.OwnershipEpoch &&
		a.PolicyRevision == b.PolicyRevision
}

func compareMariaDBFIX008StrongOwnership(
	expected, actual mariaDBFIX007OwnershipSemanticSnapshot,
) []mariaDBFIX008OracleMismatch {
	mismatches := make([]mariaDBFIX008OracleMismatch, 0)
	hostIDs := make([]string, 0, len(expected.hosts))
	for hostID := range expected.hosts {
		hostIDs = append(hostIDs, hostID)
	}
	sort.Strings(hostIDs)
	for _, hostID := range hostIDs {
		want := expected.hosts[hostID]
		got, exists := actual.hosts[hostID]
		prefix := "host." + hostID + "."
		if !exists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "missing; want present"))
			continue
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.AgentServiceID != want.AgentServiceID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"agent_service_id", "got %q want %q", got.AgentServiceID, want.AgentServiceID))
		}
		if got.OwnershipEpoch != want.OwnershipEpoch {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"ownership_epoch", "got %d want %d", got.OwnershipEpoch, want.OwnershipEpoch))
		}
		if got.PolicyRevision != want.PolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"policy_revision", "got %d want %d", got.PolicyRevision, want.PolicyRevision))
		}
		wantActive := mariaDBFIX008ActivePolicyIDs(want, expected)
		gotActive := mariaDBFIX008ActivePolicyIDs(got, actual)
		if !reflect.DeepEqual(gotActive, wantActive) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"active_policy_binding", "got active %v agent %q want active %v agent %q", gotActive, got.AgentServiceID, wantActive, want.AgentServiceID))
		}
		policy, policyExists := actual.policies[got.AgentServiceID]
		if !policyExists || policy.ProjectionRevision != got.PolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"policy_revision_closure", "agent policy exists=%t projection=%d host revision=%d", policyExists, policy.ProjectionRevision, got.PolicyRevision))
		}
	}

	serviceIDs := mariaDBFIX008SortedSetKeys(expected.fixtureIDs)
	for _, serviceID := range serviceIDs {
		want, wantExists := expected.services[serviceID]
		got, gotExists := actual.services[serviceID]
		prefix := "service." + serviceID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
			continue
		}
		if !wantExists {
			continue
		}
		if got.ServiceID != want.ServiceID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_id", "got %q want %q", got.ServiceID, want.ServiceID))
		}
		if got.ServiceType != want.ServiceType {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_type", "got %q want %q", got.ServiceType, want.ServiceType))
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.OwnershipEpoch != want.OwnershipEpoch {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"ownership_epoch", "got %d want %d", got.OwnershipEpoch, want.OwnershipEpoch))
		}
		if got.TokenID != want.TokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"current_token_id", "got %q want %q", got.TokenID, want.TokenID))
		}
		if got.StagedNodePreviousTokenID != want.StagedNodePreviousTokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"staged_previous_token_id", "got %q want %q", got.StagedNodePreviousTokenID, want.StagedNodePreviousTokenID))
		}
		if got.StagedNodeTokenID != want.StagedNodeTokenID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"staged_token_id", "got %q want %q", got.StagedNodeTokenID, want.StagedNodeTokenID))
		}
	}

	for _, policyID := range serviceIDs {
		want, wantExists := expected.policies[policyID]
		got, gotExists := actual.policies[policyID]
		prefix := "policy." + policyID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
			continue
		}
		if !wantExists {
			continue
		}
		if got.UpdaterID != want.UpdaterID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"updater_id", "got %q want %q", got.UpdaterID, want.UpdaterID))
		}
		if got.Revision != want.Revision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"revision", "got %d want %d", got.Revision, want.Revision))
		}
		if got.ProjectionRevision != want.ProjectionRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"projection_revision", "got %d want %d", got.ProjectionRevision, want.ProjectionRevision))
		}
		if got.LocalExecutorPolicyRevision != want.LocalExecutorPolicyRevision {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"local_executor_policy_revision", "got %d want %d", got.LocalExecutorPolicyRevision, want.LocalExecutorPolicyRevision))
		}
		if got.TransportMode != want.TransportMode {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"transport_mode", "got %q want %q", got.TransportMode, want.TransportMode))
		}
		if got.ExecutionHostID != want.ExecutionHostID {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"execution_host_id", "got %q want %q", got.ExecutionHostID, want.ExecutionHostID))
		}
		if got.LocalExecutorPolicySHA256 != want.LocalExecutorPolicySHA256 {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"local_executor_policy_sha256", "got %q want %q", got.LocalExecutorPolicySHA256, want.LocalExecutorPolicySHA256))
		}
	}

	tokenIDs := make(map[string]struct{}, len(expected.relevantIDs))
	for tokenID := range expected.relevantIDs {
		tokenIDs[tokenID] = struct{}{}
	}
	for _, snapshot := range []mariaDBFIX007OwnershipSemanticSnapshot{expected, actual} {
		for _, serviceID := range serviceIDs {
			service, exists := snapshot.services[serviceID]
			if !exists {
				continue
			}
			for _, tokenID := range []string{service.TokenID, service.StagedNodePreviousTokenID, service.StagedNodeTokenID} {
				if tokenID != "" {
					tokenIDs[tokenID] = struct{}{}
				}
			}
		}
	}
	for _, tokenID := range mariaDBFIX008SortedSetKeys(tokenIDs) {
		want, wantExists := expected.tokens[tokenID]
		got, gotExists := actual.tokens[tokenID]
		prefix := "token." + tokenID + "."
		if wantExists != gotExists {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"exists", "got %t want %t", gotExists, wantExists))
		}
		if wantExists && gotExists {
			if got.ID != want.ID {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"id", "got %q want %q", got.ID, want.ID))
			}
			if got.ServiceType != want.ServiceType {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"service_type", "got %q want %q", got.ServiceType, want.ServiceType))
			}
			if (got.RevokedAt != nil) != (want.RevokedAt != nil) {
				mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"revoked", "got %t want %t", got.RevokedAt != nil, want.RevokedAt != nil))
			}
		}
		wantReferences := mariaDBFIX007TokenReferenceOwners(expected.services, tokenID)
		gotReferences := mariaDBFIX007TokenReferenceOwners(actual.services, tokenID)
		if !reflect.DeepEqual(gotReferences, wantReferences) {
			mismatches = append(mismatches, mariaDBFIX008Mismatch(prefix+"references", "got %v want %v", gotReferences, wantReferences))
		}
	}
	return mismatches
}

func mariaDBFIX008ActivePolicyIDs(
	host SystemUpdateExecutionHost,
	snapshot mariaDBFIX007OwnershipSemanticSnapshot,
) []string {
	active := make([]string, 0, 2)
	for policyID, policy := range snapshot.policies {
		service, serviceExists := snapshot.services[policyID]
		if host.TransportMode == SystemUpdateTransportPullV2 &&
			policy.TransportMode == SystemUpdateTransportPullV2 &&
			policy.ExecutionHostID == host.ExecutionHostID &&
			serviceExists && service.TransportMode == SystemUpdateTransportPullV2 &&
			service.ExecutionHostID == host.ExecutionHostID &&
			service.OwnershipEpoch == host.OwnershipEpoch {
			active = append(active, policyID)
		}
	}
	sort.Strings(active)
	return active
}

func mariaDBFIX008SortedSetKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nonNilMariaDBFIX007Time() *time.Time {
	value := time.Unix(1, 0).UTC()
	return &value
}

type mariaDBFIX008PolicyMutationCase struct {
	name            string
	diagnosticField string
	mutate          func(*mariaDBFIX007OwnershipSemanticSnapshot)
}

func mariaDBFIX008PolicyOracleFixture() mariaDBFIX007OwnershipSemanticSnapshot {
	const (
		hostID           = "host-1"
		pullServiceID    = "pull-agent"
		targetServiceID  = "target-service"
		currentTokenID   = "current-token"
		stagedPreviousID = "staged-previous-token"
		stagedTokenID    = "staged-token"
	)
	return mariaDBFIX007OwnershipSemanticSnapshot{
		hosts: map[string]SystemUpdateExecutionHost{
			hostID: {
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
				AgentServiceID: pullServiceID,
				OwnershipEpoch: 11, PolicyRevision: 23,
			},
		},
		services: map[string]RegisteredService{
			pullServiceID: {
				ServiceID: pullServiceID, ServiceType: "update_agent",
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
				OwnershipEpoch: 11, TokenID: currentTokenID,
				StagedNodePreviousTokenID: stagedPreviousID, StagedNodeTokenID: stagedTokenID,
			},
			targetServiceID: {
				ServiceID: targetServiceID, ServiceType: "control_panel",
				ExecutionHostID: hostID, TransportMode: SystemUpdateTransportPullV2,
			},
		},
		policies: map[string]UpdaterPolicy{
			pullServiceID: {
				UpdaterID: pullServiceID, Revision: 19, ProjectionRevision: 23,
				LocalExecutorPolicyRevision: 29, TransportMode: SystemUpdateTransportPullV2,
				ExecutionHostID: hostID, LocalExecutorPolicySHA256: strings.Repeat("a", 64),
			},
		},
		tokens: map[string]ServiceToken{
			currentTokenID:   {ID: currentTokenID, ServiceType: "update_agent"},
			stagedPreviousID: {ID: stagedPreviousID, ServiceType: "update_agent"},
			stagedTokenID:    {ID: stagedTokenID, ServiceType: "update_agent"},
		},
		fixtureIDs: map[string]struct{}{
			pullServiceID: {}, targetServiceID: {},
		},
		relevantIDs: map[string]struct{}{
			currentTokenID: {}, stagedPreviousID: {}, stagedTokenID: {},
		},
	}
}

func mariaDBFIX008PolicyMutationMatrix() []mariaDBFIX008PolicyMutationCase {
	return []mariaDBFIX008PolicyMutationCase{
		{
			name: "wrong AgentServiceID", diagnosticField: "host.host-1.agent_service_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.AgentServiceID = "legacy-agent"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host OwnershipEpoch", diagnosticField: "host.host-1.ownership_epoch",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.OwnershipEpoch--
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host PolicyRevision", diagnosticField: "host.host-1.policy_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.PolicyRevision--
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong service OwnershipEpoch", diagnosticField: "service.pull-agent.ownership_epoch",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.OwnershipEpoch--
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service ExecutionHostID", diagnosticField: "service.pull-agent.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ExecutionHostID = "wrong-host"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong current token ref", diagnosticField: "service.pull-agent.current_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TokenID = "unexpected-current-token"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong staged token ref", diagnosticField: "service.pull-agent.staged_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.StagedNodeTokenID = "unexpected-staged-token"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "multiple active policies", diagnosticField: "host.host-1.active_policy_count",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				actual.services["second-pull-agent"] = RegisteredService{
					ServiceID: "second-pull-agent", ServiceType: "update_agent",
					ExecutionHostID: "host-1", TransportMode: SystemUpdateTransportPullV2,
					OwnershipEpoch: 11, TokenID: "second-token",
				}
				actual.policies["second-pull-agent"] = UpdaterPolicy{
					UpdaterID: "second-pull-agent", Revision: 31, ProjectionRevision: 37,
					TransportMode: SystemUpdateTransportPullV2, ExecutionHostID: "host-1",
				}
				actual.tokens["second-token"] = ServiceToken{ID: "second-token", ServiceType: "update_agent"}
			},
		},
		{
			name: "wrong token revoked state", diagnosticField: "token.current-token.revoked",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.RevokedAt = nonNilMariaDBFIX007Time()
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "wrong token service type", diagnosticField: "token.current-token.service_type",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.ServiceType = "worker"
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "missing service-token reference", diagnosticField: "token.current-token.references",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TokenID = ""
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "unexpected shared service-token reference", diagnosticField: "token.current-token.references",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				actual.services["unexpected-service"] = RegisteredService{
					ServiceID: "unexpected-service", ServiceType: "update_agent", TokenID: "current-token",
				}
			},
		},
		{
			name: "wrong policy revision", diagnosticField: "policy.pull-agent.revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.Revision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong execution host ID", diagnosticField: "host.host-1.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.ExecutionHostID = "wrong-host"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong host transport mode", diagnosticField: "host.host-1.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.TransportMode = "unsupported"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong active policy binding", diagnosticField: "host.host-1.active_policy_binding",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				host := actual.hosts["host-1"]
				host.AgentServiceID = "legacy-agent"
				actual.hosts["host-1"] = host
			},
		},
		{
			name: "wrong service ID", diagnosticField: "service.pull-agent.service_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ServiceID = "wrong-service"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service type", diagnosticField: "service.pull-agent.service_type",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.ServiceType = "worker"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong service transport mode", diagnosticField: "service.pull-agent.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.TransportMode = "unsupported"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong staged previous token ref", diagnosticField: "service.pull-agent.staged_previous_token_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				service := actual.services["pull-agent"]
				service.StagedNodePreviousTokenID = "wrong-staged-previous"
				actual.services["pull-agent"] = service
			},
		},
		{
			name: "wrong policy updater ID", diagnosticField: "policy.pull-agent.updater_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.UpdaterID = "wrong-updater"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy projection revision", diagnosticField: "policy.pull-agent.projection_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.ProjectionRevision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong local executor policy revision", diagnosticField: "policy.pull-agent.local_executor_policy_revision",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.LocalExecutorPolicyRevision--
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy transport mode", diagnosticField: "policy.pull-agent.transport_mode",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.TransportMode = "unsupported"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy execution host", diagnosticField: "policy.pull-agent.execution_host_id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.ExecutionHostID = "wrong-host"
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong policy digest", diagnosticField: "policy.pull-agent.local_executor_policy_sha256",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				policy := actual.policies["pull-agent"]
				policy.LocalExecutorPolicySHA256 = strings.Repeat("b", 64)
				actual.policies["pull-agent"] = policy
			},
		},
		{
			name: "wrong token ID", diagnosticField: "token.current-token.id",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["current-token"]
				token.ID = "wrong-token-id"
				actual.tokens["current-token"] = token
			},
		},
		{
			name: "missing token row", diagnosticField: "token.current-token.exists",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				delete(actual.tokens, "current-token")
			},
		},
		{
			name: "wrong staged token revoked state", diagnosticField: "token.staged-token.revoked",
			mutate: func(actual *mariaDBFIX007OwnershipSemanticSnapshot) {
				token := actual.tokens["staged-token"]
				token.RevokedAt = nonNilMariaDBFIX007Time()
				actual.tokens["staged-token"] = token
			},
		},
	}
}

func TestMariaDBFIX008SharedPolicyOracleCoreMutationMatrix(t *testing.T) {
	expected := mariaDBFIX008PolicyOracleFixture()
	if mismatches := compareMariaDBFIX008StrongOwnership(expected, cloneMariaDBFIX007OwnershipSemanticState(expected)); len(mismatches) != 0 {
		t.Fatalf("valid policy oracle fixture mismatched: %s", mariaDBFIX008FormatMismatches(mismatches))
	}
	for _, mutation := range mariaDBFIX008PolicyMutationMatrix() {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			actual := cloneMariaDBFIX007OwnershipSemanticState(expected)
			mutation.mutate(&actual)
			mismatches := compareMariaDBFIX008StrongOwnership(expected, actual)
			if !mariaDBFIX008HasMismatchField(mismatches, mutation.diagnosticField) {
				t.Fatalf("shared policy core did not report %q: %s", mutation.diagnosticField, mariaDBFIX008FormatMismatches(mismatches))
			}
		})
	}
}

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
		"host.host-1.active_policy_count",
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
