package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/security"
	"testing"
	"time"
)

func TestMariaDBFIX011ReferenceSetRetryExhaustionUsesConflict(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-exhaustion-target"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-exhaustion-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	mutations := make([]mariaDBFIX011ReferenceMutation, 0, mariaDBServiceTokenReferenceRetryLimit)
	beforeExternal := make([]RegisteredService, 0, mariaDBServiceTokenReferenceRetryLimit)
	for attempt := 1; attempt <= mariaDBServiceTokenReferenceRetryLimit; attempt++ {
		externalID := fmt.Sprintf("%sfix011-exhaustion-external-%d", cleanup.prefix, attempt)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		external, err := auth.GetService(ctx, externalID)
		if err != nil {
			t.Fatal(err)
		}
		beforeExternal = append(beforeExternal, external)
		mutations = append(mutations, mariaDBFIX011ReferenceMutation{
			serviceID: externalID,
			column:    "staged_node_previous_token_id",
			tokenID:   oldToken.ID,
		})
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"stage_service_node_configuration",
		mutations,
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
		configureToken,
		now,
		func(string) (string, string, error) {
			sealerCalled = true
			return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
		},
	)
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("retry exhaustion error = %v, want ErrConflict only", err)
	}
	if sealerCalled {
		t.Fatal("retry exhaustion reached the sealer")
	}
	if recorder.commitCount != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf(
			"external reference commits = %d, want %d",
			recorder.commitCount,
			mariaDBServiceTokenReferenceRetryLimit,
		)
	}
	assertMariaDBFIX011ExhaustionPhases(t, recorder.events)
	for _, mutation := range mutations {
		clearMariaDBFIX011ServiceTokenReference(
			t, ctx, db, mutation.serviceID, mutation.column,
		)
	}
	services := append([]RegisteredService{beforeTarget}, beforeExternal...)
	assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, services...)
	if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatal("retry exhaustion revoked the active token")
	}
}

func TestMariaDBFIX011StableExternalOldAndNewReferencesRemainConflicts(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)

	t.Run("old token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
		targetID := cleanup.prefix + "fix011-stable-old-target"
		externalID := cleanup.prefix + "fix011-stable-old-external"
		oldToken := createMariaDBServiceTokenPairService(
			t, ctx, auth, targetID, "update_agent", nil, cleanup,
		)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		setMariaDBFIX010ServiceTokenReference(
			t, ctx, db, externalID, "staged_node_previous_token_id", oldToken.ID,
		)
		now := time.Now().UTC()
		configureToken := cleanup.prefix + "fix011-stable-old-configure"
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
			t, ctx, db, "stage_service_node_configuration", nil,
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
			configureToken,
			now,
			func(string) (string, string, error) {
				sealerCalled = true
				return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
			},
		)
		if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
			t.Fatalf("stable old-token reference error = %v, want shared-token conflict", err)
		}
		if sealerCalled {
			t.Fatal("stable old-token reference reached the sealer")
		}
		assertMariaDBFIX011StableConflictPhases(t, recorder.events)
		assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, beforeTarget, beforeExternal)
		if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
			t.Fatal("stable old-token conflict revoked the active token")
		}
	})

	t.Run("new token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
		targetID := cleanup.prefix + "fix011-stable-new-target"
		externalID := cleanup.prefix + "fix011-stable-new-external"
		oldToken := createMariaDBServiceTokenPairService(
			t, ctx, auth, targetID, "update_agent", nil, cleanup,
		)
		now := time.Now().UTC()
		configureToken := cleanup.prefix + "fix011-stable-new-configure"
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
				return cleanup.prefix + "stable-new-ciphertext", cleanup.prefix + "stable-new-nonce", nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cleanup.trackToken(staged.Token)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		setMariaDBFIX010ServiceTokenReference(
			t, ctx, db, externalID, "staged_node_token_id", staged.Token.ID,
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
			t, ctx, db, "activate_service_node_configuration", nil,
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
				staged.ActivationToken,
				now.Add(time.Minute),
				ServiceRuntimeReport{},
			)
		if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
			t.Fatalf("stable new-token reference error = %v, want shared-token conflict", err)
		}
		if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
			t.Fatalf(
				"stable new-token conflict returned success: token_id=%q service_id=%q already=%t",
				activatedToken.ID,
				activatedService.ServiceID,
				alreadyActivated,
			)
		}
		assertMariaDBFIX011StableConflictPhases(t, recorder.events)
		assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, beforeTarget, beforeExternal)
		var stagedTokenRows int
		if err := db.QueryRowContext(
			ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
		).Scan(&stagedTokenRows); err != nil {
			t.Fatal(err)
		}
		if stagedTokenRows != 0 || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
			t.Fatalf(
				"stable new-token conflict changed token state: staged_rows=%d old_revoked=%t",
				stagedTokenRows,
				mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			)
		}
	})
}
