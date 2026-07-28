package updateagent

import (
	"testing"
	"time"
)

func TestRuntimeTokenRotationRequiresNewTokenHeartbeatBeforeActivation(t *testing.T) {
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	state, err := StageRuntimeTokenRotation(RuntimeTokenRotationRequest{
		RotationID:      "rotation-001",
		NodeID:          "updater-node-a",
		PreviousTokenID: "token-old",
		StagedTokenID:   "token-new",
		StagedAt:        now,
	}, HostLifecycleBlockers{})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, _, err := ActivateRuntimeTokenRotation(state, "token-new", now.Add(time.Second)); err == nil {
		t.Fatal("rotation activated before new-token heartbeat proof")
	}

	proved, err := ProveRuntimeTokenHeartbeat(state, "token-new", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("prove heartbeat: %v", err)
	}
	activated, result, err := ActivateRuntimeTokenRotation(proved, "token-new", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if result.State != RuntimeTokenRotationActivated ||
		result.AlreadyActivated ||
		result.RevokeTokenID != "token-old" ||
		activated.Phase != RuntimeTokenRotationActivated {
		t.Fatalf("unexpected activation: result=%#v state=%#v", result, activated)
	}
}

func TestRuntimeTokenRotationActivationResponseLossIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	state, err := StageRuntimeTokenRotation(validRuntimeTokenRotationRequest(now), HostLifecycleBlockers{})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	state, err = ProveRuntimeTokenHeartbeat(state, "token-new", now.Add(time.Second))
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	state, _, err = ActivateRuntimeTokenRotation(state, "token-new", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("activate: %v", err)
	}

	replayed, result, err := ActivateRuntimeTokenRotation(state, "token-new", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !result.AlreadyActivated ||
		result.State != RuntimeTokenRotationActivated ||
		result.RevokeTokenID != "token-old" ||
		replayed.ActivatedAt != state.ActivatedAt {
		t.Fatalf("activation replay changed state: result=%#v state=%#v", result, replayed)
	}
}

func TestRuntimeTokenRotationRejectsActiveJobMutationAndRecovery(t *testing.T) {
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	for _, blockers := range []HostLifecycleBlockers{
		{ActiveJob: true},
		{MutationInProgress: true},
		{RecoveryPending: true},
		{SelfUpdatePending: true},
	} {
		if _, err := StageRuntimeTokenRotation(validRuntimeTokenRotationRequest(now), blockers); err == nil {
			t.Fatalf("rotation ignored active lifecycle work: %#v", blockers)
		}
	}
}

func TestEmergencyRuntimeTokenRevokePreservesConsumedMutationRollback(t *testing.T) {
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	state, err := StageRuntimeTokenRotation(validRuntimeTokenRotationRequest(now), HostLifecycleBlockers{})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	revoked, result, err := EmergencyRevokeRuntimeToken(state, "token-old", true, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("emergency revoke: %v", err)
	}
	if result.RevokeTokenID != "token-old" ||
		!result.AllowConsumedMutationRollback ||
		revoked.EmergencyRevokedTokenID != "token-old" {
		t.Fatalf("emergency revoke blocked local rollback: result=%#v state=%#v", result, revoked)
	}
}

func TestRuntimeTokenRotationRejectsWrongProofToken(t *testing.T) {
	now := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	state, err := StageRuntimeTokenRotation(validRuntimeTokenRotationRequest(now), HostLifecycleBlockers{})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := ProveRuntimeTokenHeartbeat(state, "token-old", now.Add(time.Second)); err == nil {
		t.Fatal("old-token heartbeat satisfied staged-token proof")
	}
	if _, err := ProveRuntimeTokenHeartbeat(state, "token-other", now.Add(time.Second)); err == nil {
		t.Fatal("unrelated-token heartbeat satisfied staged-token proof")
	}
}

func validRuntimeTokenRotationRequest(now time.Time) RuntimeTokenRotationRequest {
	return RuntimeTokenRotationRequest{
		RotationID:      "rotation-001",
		NodeID:          "updater-node-a",
		PreviousTokenID: "token-old",
		StagedTokenID:   "token-new",
		StagedAt:        now,
	}
}
