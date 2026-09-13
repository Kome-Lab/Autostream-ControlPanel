package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func runMariaDBSharedServiceTokenNodeRotationPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "worker-a"
	heartbeatID := cleanup.prefix + "worker-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "worker", &oldToken, cleanup)
	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "rotate_service_node_token" {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	rotationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, err := auth.RotateServiceNodeToken(mutationCtx, targetID, oldToken.ID, func(string) (string, string, error) {
			return "test-ciphertext", "test-nonce", nil
		})
		rotationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, "shared node token service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first shared rotation phase = %q", phase)
	}
	select {
	case phase := <-phases:
		t.Fatalf("shared rotation advanced to %q while referenced service was locked", phase)
	case <-time.After(75 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	assertMariaDBServiceTokenOperationError(t, "shared heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared heartbeat"))
	rotation := receiveMariaDBServiceTokenMutation(t, rotationResult)
	cleanup.trackToken(rotation.token)
	assertMariaDBServiceTokenOperationError(t, "shared node rotation", rotation.err)
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatService, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if target.TokenID != rotation.token.ID || heartbeatService.TokenID != oldToken.ID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, rotation.token.ID) {
		t.Fatalf("shared token rotation split state: target=%s heartbeat=%s token=%s", formatSafeRegisteredServiceDiagnostic(target), formatSafeRegisteredServiceDiagnostic(heartbeatService), formatSafeServiceTokenDiagnostic("rotate", rotation.token, 0, "unexpected_result"))
	}
}

func runMariaDBSharedServiceTokenNodeConfigurePair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	suffix := cleanup.prefix + "configure"
	targetID := cleanup.prefix + "worker-a"
	heartbeatID := cleanup.prefix + "worker-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "worker", &oldToken, cleanup)
	now := time.Now().UTC()
	rawConfigureToken := "configure-node-" + suffix
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(rawConfigureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "configure_service_node" {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	configurationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, err := auth.ConfigureServiceNode(
			mutationCtx,
			targetID,
			rawConfigureToken,
			now,
			ServiceRuntimeReport{},
			func(string) (string, string, error) { return "test-ciphertext", "test-nonce", nil },
		)
		configurationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	assertMariaDBSharedNodeMutationBlocksOnService(t, phases, blocker, "configure service node")
	assertMariaDBServiceTokenOperationError(
		t, "shared configure heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared configure heartbeat"),
	)
	configured := receiveMariaDBServiceTokenMutation(t, configurationResult)
	cleanup.trackToken(configured.token)
	assertMariaDBServiceTokenOperationError(t, "shared node configure", configured.err)
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	assertMariaDBSharedNodeMutationFinalState(t, ctx, db, auth, targetID, heartbeatID, oldToken.ID, configured.token)
}

func runMariaDBSharedServiceTokenNodeActivationPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	suffix := cleanup.prefix + "activate"
	targetID := cleanup.prefix + "updater-a"
	heartbeatID := cleanup.prefix + "updater-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "update_agent", nil, cleanup)
	now := time.Now().UTC()
	rawConfigureToken := "activate-node-" + suffix
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(rawConfigureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		rawConfigureToken,
		now,
		func(string) (string, string, error) { return "staged-ciphertext", "staged-nonce", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "update_agent", &oldToken, cleanup)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHeartbeat, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}

	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "activate_service_node_configuration" &&
			isMariaDBServiceTokenCanonicalLockPhase(phase) {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	activationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, _, err := auth.ActivateServiceNodeConfiguration(
			mutationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(time.Minute),
			ServiceRuntimeReport{},
		)
		activationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	assertMariaDBSharedNodeMutationBlocksOnService(t, phases, blocker, "activate service node configuration")
	assertMariaDBServiceTokenOperationError(
		t, "shared activation heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared activation heartbeat"),
	)
	activated := receiveMariaDBServiceTokenMutation(t, activationResult)
	if !errors.Is(activated.err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("shared node activation error = %v, want shared-token conflict", activated.err)
	}
	if activated.token.ID != "" {
		t.Fatalf("shared node activation returned token = %s", formatSafeServiceTokenDiagnostic("activate", activated.token, 0, "unexpected_success"))
	}
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	afterTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	afterHeartbeat, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	var stagedTokenRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID).Scan(&stagedTokenRows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterTarget, beforeTarget) ||
		afterHeartbeat.TokenID != beforeHeartbeat.TokenID ||
		afterHeartbeat.StagedNodePreviousTokenID != beforeHeartbeat.StagedNodePreviousTokenID ||
		afterHeartbeat.StagedNodeTokenID != beforeHeartbeat.StagedNodeTokenID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		stagedTokenRows != 0 {
		t.Fatalf(
			"shared node activation partially mutated state: target=%s heartbeat=%s old_revoked=%t staged_rows=%d",
			formatSafeRegisteredServiceDiagnostic(afterTarget),
			formatSafeRegisteredServiceDiagnostic(afterHeartbeat),
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			stagedTokenRows,
		)
	}
}

func assertMariaDBSharedNodeMutationBlocksOnService(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	blocker *sql.Tx,
	label string,
) {
	t.Helper()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, label+" service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first %s phase = %q", label, phase)
	}
	select {
	case phase := <-phases:
		t.Fatalf("%s advanced to %q while referenced service was locked", label, phase)
	case <-time.After(75 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

func isMariaDBServiceTokenCanonicalLockPhase(phase mariaDBServiceTokenLockPhase) bool {
	switch phase {
	case mariaDBServiceTokenBeforeServiceLocks,
		mariaDBServiceTokenServiceLocksHeld,
		mariaDBServiceTokenBeforeTokenLocks,
		mariaDBServiceTokenTokenLocksHeld,
		mariaDBServiceTokenBindingsValidated:
		return true
	default:
		return false
	}
}

func assertMariaDBSharedNodeMutationFinalState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
	targetID,
	heartbeatID,
	oldTokenID string,
	newToken ServiceToken,
) {
	t.Helper()
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatService, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if newToken.ID == "" || target.TokenID != newToken.ID || heartbeatService.TokenID != oldTokenID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldTokenID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, newToken.ID) {
		t.Fatalf("shared node mutation split state: target=%s heartbeat=%s token=%s", formatSafeRegisteredServiceDiagnostic(target), formatSafeRegisteredServiceDiagnostic(heartbeatService), formatSafeServiceTokenDiagnostic("node_mutation", newToken, 0, "unexpected_result"))
	}
}

func assertMariaDBSharedTokenDeleteFailsClosed(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "worker-a"
	otherID := cleanup.prefix + "worker-b"
	token := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, otherID, "worker", &token, cleanup)
	if err := auth.DeleteService(ctx, targetID); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("shared-token delete error = %v, want ErrServiceAssignmentConflict", err)
	}
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.GetService(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if target.TokenID != token.ID || other.TokenID != token.ID || mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
		t.Fatalf(
			"shared-token delete partially mutated state: target=%s other=%s",
			formatSafeRegisteredServiceDiagnostic(target),
			formatSafeRegisteredServiceDiagnostic(other),
		)
	}
}
