package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func TestMariaDBFIX011InvalidConfigureCredentialWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-stage-target"
	externalID := cleanup.prefix + "fix011-stage-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-stage-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"stage_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_previous_token_id",
			tokenID:   oldToken.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	sealerCalled := false
	_, err = auth.StageServiceNodeConfiguration(
		operationCtx,
		targetID,
		configureToken+"-invalid",
		now,
		func(string) (string, string, error) {
			sealerCalled = true
			return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
		},
	)
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("invalid configure error = %v, want ErrUnauthorized only", err)
	}
	if sealerCalled {
		t.Fatal("invalid configure credential reached the sealer")
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_previous_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatal("invalid configure credential revoked the active token")
	}
}

func TestMariaDBFIX011InvalidActivationCredentialWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-activation-target"
	externalID := cleanup.prefix + "fix011-activation-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-activation-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "activation-ciphertext", cleanup.prefix + "activation-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"activate_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_token_id",
			tokenID:   staged.Token.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	activatedToken, activatedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken+"-invalid",
			now.Add(time.Minute),
			ServiceRuntimeReport{},
		)
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("invalid activation error = %v, want ErrUnauthorized only", err)
	}
	if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
		t.Fatalf(
			"invalid activation returned success values: token_id=%q service_id=%q already=%t",
			activatedToken.ID,
			activatedService.ServiceID,
			alreadyActivated,
		)
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	var stagedTokenRows int
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
	).Scan(&stagedTokenRows); err != nil {
		t.Fatal(err)
	}
	if stagedTokenRows != 0 || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatalf(
			"invalid activation changed token state: staged_rows=%d old_revoked=%t",
			stagedTokenRows,
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
		)
	}
}

func TestMariaDBFIX011DuplicateActivationReplayWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-replay-target"
	externalID := cleanup.prefix + "fix011-replay-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-replay-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "replay-ciphertext", cleanup.prefix + "replay-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		targetID,
		staged.Token.ID,
		staged.ActivationToken,
		now.Add(time.Minute),
		ServiceRuntimeReport{Version: "v1.0.0"},
	); err != nil || alreadyActivated {
		t.Fatalf("initial activation already=%t err=%v", alreadyActivated, err)
	}
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"activate_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_token_id",
			tokenID:   staged.Token.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	replayedToken, replayedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(2*time.Minute),
			ServiceRuntimeReport{Version: "v9.9.9"},
		)
	if err != nil || !alreadyActivated {
		t.Fatalf("duplicate activation replay already=%t err=%v", alreadyActivated, err)
	}
	if replayedToken.ID != staged.Token.ID || replayedService.ServiceID != targetID {
		t.Fatalf(
			"duplicate replay returned unexpected identity: token_id=%q service_id=%q",
			replayedToken.ID,
			replayedService.ServiceID,
		)
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	if !mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) {
		t.Fatalf(
			"duplicate replay changed revocation state: old_revoked=%t new_revoked=%t",
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID),
		)
	}
}

func TestMariaDBFIX011ReplayRevalidatesTargetAfterReferenceSetRetry(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-stale-replay-target"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-stale-replay-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "stale-replay-ciphertext", cleanup.prefix + "stale-replay-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		targetID,
		staged.Token.ID,
		staged.ActivationToken,
		now.Add(time.Minute),
		ServiceRuntimeReport{Version: "v1.0.0"},
	); err != nil || alreadyActivated {
		t.Fatalf("initial activation already=%t err=%v", alreadyActivated, err)
	}

	var rotatedToken ServiceToken
	var postRotationService RegisteredService
	rotationCommitted := false
	events := make([]string, 0, 24)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation != "activate_service_node_configuration" {
			return
		}
		events = append(events, string(phase))
		if phase != mariaDBServiceTokenReferenceDiscoveryComplete || rotationCommitted {
			return
		}
		var rotationErr error
		rotatedToken, rotationErr = auth.RotateServiceToken(ctx, staged.Token.ID)
		if rotationErr != nil {
			t.Fatal(rotationErr)
		}
		cleanup.trackToken(rotatedToken)
		postRotationService, rotationErr = auth.GetService(ctx, targetID)
		if rotationErr != nil {
			t.Fatal(rotationErr)
		}
		rotationCommitted = true
		events = append(events, "concurrent_target_rotation_committed")
	})
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		observer,
	)
	replayedToken, replayedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(2*time.Minute),
			ServiceRuntimeReport{Version: "v9.9.9"},
		)
	if !errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) ||
		alreadyActivated {
		t.Fatalf("stale replay already=%t err=%v, want ErrUnauthorized only", alreadyActivated, err)
	}
	if replayedToken.ID != "" || replayedService.ServiceID != "" {
		t.Fatalf(
			"stale replay returned success identity: token_id=%q service_id=%q",
			replayedToken.ID,
			replayedService.ServiceID,
		)
	}
	if !rotationCommitted || rotatedToken.ID == "" {
		t.Fatal("concurrent target rotation was not committed")
	}
	assertMariaDBFIX011PhaseSubsequence(t, events, []string{
		"reference_discovery_complete",
		"concurrent_target_rotation_committed",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		"reference_set_mismatch",
		"reference_retry_start",
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		string(mariaDBServiceTokenBindingsValidated),
		"stable_auth_replay_conflict",
	})
	for event, want := range map[string]int{
		"reference_discovery_complete": 2,
		"reference_set_mismatch":       1,
		"reference_retry_start":        1,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
	afterReplay, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterReplay, postRotationService) ||
		afterReplay.TokenID != rotatedToken.ID ||
		!mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		!mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, rotatedToken.ID) {
		t.Fatalf(
			"stale replay changed post-rotation state: service=%s old_revoked=%t replay_token_revoked=%t rotated_revoked=%t",
			formatSafeRegisteredServiceDiagnostic(afterReplay),
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, rotatedToken.ID),
		)
	}
}
