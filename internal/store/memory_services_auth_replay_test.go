package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func TestFIX011MemoryAuthAndReplayPrecedeStableSharedReferences(t *testing.T) {
	t.Run("invalid configure credential", func(t *testing.T) {
		ctx := t.Context()
		auth := NewMemoryAuthStore()
		oldToken := createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-stage-target")
		createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-stage-other")
		now := time.Date(2026, time.August, 25, 3, 0, 0, 0, time.UTC)
		configureToken := "fix011-memory-stage-configure"
		if _, err := auth.SetServiceConfigureToken(
			ctx,
			"fix011-memory-stage-target",
			security.HashToken(configureToken),
			now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		auth.mu.Lock()
		other := auth.services["fix011-memory-stage-other"]
		other.StagedNodePreviousTokenID = oldToken.ID
		auth.services[other.ServiceID] = other
		beforeTarget := auth.services["fix011-memory-stage-target"]
		beforeOther := other
		beforeOldToken := auth.serviceTokens[oldToken.ID]
		beforeTokenCount := len(auth.serviceTokens)
		auth.mu.Unlock()
		sealerCalled := false
		_, err := auth.StageServiceNodeConfiguration(
			ctx,
			"fix011-memory-stage-target",
			configureToken+"-invalid",
			now,
			func(string) (string, string, error) {
				sealerCalled = true
				return "unexpected-ciphertext", "unexpected-nonce", nil
			},
		)
		if !errors.Is(err, ErrUnauthorized) ||
			errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
			t.Fatalf("invalid configure error = %v, want ErrUnauthorized only", err)
		}
		if sealerCalled {
			t.Fatal("invalid configure credential reached the sealer")
		}
		auth.mu.Lock()
		defer auth.mu.Unlock()
		if !reflect.DeepEqual(auth.services[beforeTarget.ServiceID], beforeTarget) ||
			!reflect.DeepEqual(auth.services[beforeOther.ServiceID], beforeOther) ||
			!reflect.DeepEqual(auth.serviceTokens[oldToken.ID], beforeOldToken) ||
			len(auth.serviceTokens) != beforeTokenCount {
			t.Fatal("invalid configure credential changed memory state")
		}
	})

	t.Run("invalid activation credential", func(t *testing.T) {
		ctx := t.Context()
		auth := NewMemoryAuthStore()
		oldToken := createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-activation-target")
		now := time.Date(2026, time.August, 25, 3, 30, 0, 0, time.UTC)
		configureToken := "fix011-memory-activation-configure"
		if _, err := auth.SetServiceConfigureToken(
			ctx,
			"fix011-memory-activation-target",
			security.HashToken(configureToken),
			now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		staged, err := auth.StageServiceNodeConfiguration(
			ctx,
			"fix011-memory-activation-target",
			configureToken,
			now,
			func(string) (string, string, error) {
				return "fix011-memory-ciphertext", "fix011-memory-nonce", nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-activation-other")
		auth.mu.Lock()
		other := auth.services["fix011-memory-activation-other"]
		other.StagedNodeTokenID = staged.Token.ID
		auth.services[other.ServiceID] = other
		beforeTarget := auth.services["fix011-memory-activation-target"]
		beforeOther := other
		beforeOldToken := auth.serviceTokens[oldToken.ID]
		beforeTokenCount := len(auth.serviceTokens)
		auth.mu.Unlock()
		_, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
			ctx,
			"fix011-memory-activation-target",
			staged.Token.ID,
			staged.ActivationToken+"-invalid",
			now.Add(time.Minute),
			ServiceRuntimeReport{},
		)
		if !errors.Is(err, ErrUnauthorized) ||
			errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) ||
			alreadyActivated {
			t.Fatalf("invalid activation already=%t err=%v, want ErrUnauthorized only", alreadyActivated, err)
		}
		auth.mu.Lock()
		defer auth.mu.Unlock()
		if !reflect.DeepEqual(auth.services[beforeTarget.ServiceID], beforeTarget) ||
			!reflect.DeepEqual(auth.services[beforeOther.ServiceID], beforeOther) ||
			!reflect.DeepEqual(auth.serviceTokens[oldToken.ID], beforeOldToken) ||
			len(auth.serviceTokens) != beforeTokenCount {
			t.Fatal("invalid activation credential changed memory state")
		}
	})

	t.Run("duplicate activation replay", func(t *testing.T) {
		ctx := t.Context()
		auth := NewMemoryAuthStore()
		oldToken := createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-replay-target")
		now := time.Date(2026, time.August, 25, 4, 0, 0, 0, time.UTC)
		configureToken := "fix011-memory-replay-configure"
		if _, err := auth.SetServiceConfigureToken(
			ctx,
			"fix011-memory-replay-target",
			security.HashToken(configureToken),
			now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		staged, err := auth.StageServiceNodeConfiguration(
			ctx,
			"fix011-memory-replay-target",
			configureToken,
			now,
			func(string) (string, string, error) {
				return "fix011-memory-replay-ciphertext", "fix011-memory-replay-nonce", nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
			ctx,
			"fix011-memory-replay-target",
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(time.Minute),
			ServiceRuntimeReport{Version: "v1.0.0"},
		); err != nil || alreadyActivated {
			t.Fatalf("initial activation already=%t err=%v", alreadyActivated, err)
		}
		createMemoryFIX010UpdateAgentService(t, auth, "fix011-memory-replay-other")
		auth.mu.Lock()
		other := auth.services["fix011-memory-replay-other"]
		other.StagedNodeTokenID = staged.Token.ID
		auth.services[other.ServiceID] = other
		beforeTarget := auth.services["fix011-memory-replay-target"]
		beforeOther := other
		beforeOldToken := auth.serviceTokens[oldToken.ID]
		beforeNewToken := auth.serviceTokens[staged.Token.ID]
		beforeTokenCount := len(auth.serviceTokens)
		auth.mu.Unlock()
		if _, _, _, err := auth.ActivateServiceNodeConfiguration(
			ctx,
			"fix011-memory-replay-target",
			staged.Token.ID,
			staged.ActivationToken+"-invalid",
			now.Add(2*time.Minute),
			ServiceRuntimeReport{},
		); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("wrong replay token error = %v, want ErrUnauthorized", err)
		}
		replayedToken, replayedService, alreadyActivated, err :=
			auth.ActivateServiceNodeConfiguration(
				ctx,
				"fix011-memory-replay-target",
				staged.Token.ID,
				staged.ActivationToken,
				now.Add(2*time.Minute),
				ServiceRuntimeReport{Version: "v9.9.9"},
			)
		if err != nil || !alreadyActivated ||
			replayedToken.ID != staged.Token.ID ||
			replayedService.ServiceID != beforeTarget.ServiceID {
			t.Fatalf(
				"duplicate replay token_id=%q service_id=%q already=%t err=%v",
				replayedToken.ID,
				replayedService.ServiceID,
				alreadyActivated,
				err,
			)
		}
		auth.mu.Lock()
		defer auth.mu.Unlock()
		if !reflect.DeepEqual(auth.services[beforeTarget.ServiceID], beforeTarget) ||
			!reflect.DeepEqual(auth.services[beforeOther.ServiceID], beforeOther) ||
			!reflect.DeepEqual(auth.serviceTokens[oldToken.ID], beforeOldToken) ||
			!reflect.DeepEqual(auth.serviceTokens[staged.Token.ID], beforeNewToken) ||
			len(auth.serviceTokens) != beforeTokenCount {
			t.Fatal("duplicate activation replay changed memory state")
		}
	})
}

func TestConsumedUpdateAgentConfigureTokenIsNotActivationReplay(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{
		"service.register",
		"service.heartbeat",
		"updates.claim",
		"updates.report",
		"updates.authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	const serviceID = "updater-consumed-configure-token"
	if _, err := auth.PrecreateService(ctx, token, bundle8bPullAgentRegistration(serviceID)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 3, 10, 0, 0, time.UTC)
	const configureToken = "consumed-configure-token"
	if _, err := auth.SetServiceConfigureToken(
		ctx,
		serviceID,
		security.HashToken(configureToken),
		now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, serviceID, configureToken, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		serviceID,
		token.ID,
		configureToken,
		now.Add(time.Minute),
		ServiceRuntimeReport{},
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("consumed configure token authenticated as an activation replay: %v", err)
	}
}

func TestUpdateAgentConfigurationRejectsLegacyScopesBeforeStageOrActivation(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	legacyToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	const serviceID = "updater-legacy-scope"
	if _, err := auth.PrecreateService(ctx, legacyToken, bundle8bPullAgentRegistration(serviceID)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 3, 20, 0, 0, time.UTC)
	configureToken := "configure-legacy-update-agent"
	if _, err := auth.SetServiceConfigureToken(ctx, serviceID, security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	sealerCalled := false
	if _, err := auth.StageServiceNodeConfiguration(ctx, serviceID, configureToken, now, func(string) (string, string, error) {
		sealerCalled = true
		return "ciphertext", "nonce", nil
	}); !errors.Is(err, ErrInvalidServiceScope) {
		t.Fatalf("legacy updater stage err = %v, want ErrInvalidServiceScope", err)
	}
	if sealerCalled {
		t.Fatal("legacy updater stage generated a replacement secret before rejecting its scopes")
	}
	service, err := auth.GetService(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != legacyToken.ID || service.ConfigureTokenUsedAt != nil || service.StagedNodeTokenID != "" {
		t.Fatalf("rejected legacy updater stage mutated service: %s", formatSafeRegisteredServiceDiagnostic(service))
	}

	// Simulate an incomplete staged configuration persisted by a pre-upgrade
	// server. Activation must validate the staged scopes again under the store
	// lock so deployment across the stage/activate boundary stays fail closed.
	const stagedTokenID = "legacy-staged-token"
	const stagedRawToken = "ast_svc_legacy_staged"
	const activationToken = "ast_act_legacy_staged"
	stageTime := now.Add(time.Minute)
	auth.mu.Lock()
	service = auth.services[serviceID]
	service.StagedNodePreviousTokenID = legacyToken.ID
	service.StagedNodeTokenID = stagedTokenID
	service.StagedNodeTokenHash = security.HashToken(stagedRawToken)
	service.StagedNodeTokenScopes = []string{"service.register", "service.heartbeat"}
	service.StagedNodeTokenCiphertext = "legacy-staged-ciphertext"
	service.StagedNodeTokenNonce = "legacy-staged-nonce"
	service.StagedNodeActivationTokenHash = security.HashToken(activationToken)
	service.StagedNodeTokenAt = &stageTime
	service.ConfigureTokenUsedAt = &stageTime
	auth.services[serviceID] = service
	auth.mu.Unlock()

	if _, _, _, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		serviceID,
		stagedTokenID,
		activationToken,
		now.Add(2*time.Hour),
		ServiceRuntimeReport{},
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired legacy updater activation err = %v, want ErrUnauthorized", err)
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		serviceID,
		stagedTokenID,
		activationToken,
		now.Add(2*time.Minute),
		ServiceRuntimeReport{},
	); !errors.Is(err, ErrInvalidServiceScope) {
		t.Fatalf("legacy updater activation err = %v, want ErrInvalidServiceScope", err)
	}
	service, err = auth.GetService(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != legacyToken.ID ||
		service.StagedNodeTokenID != stagedTokenID ||
		service.NodeTokenCiphertext != "" ||
		service.NodeTokenNonce != "" {
		t.Fatalf("rejected legacy updater activation mutated service: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, legacyToken.RawToken, "service.heartbeat"); err != nil {
		t.Fatalf("rejected legacy updater activation invalidated old token: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, stagedRawToken, "service.heartbeat"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("rejected legacy updater activation enabled staged token: %v", err)
	}
}

func TestUpdateAgentConfigurationRejectsExpiredActivationWithoutChangingActiveToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	const serviceID = "updater-expired-stage"
	if _, err := auth.PrecreateService(ctx, oldToken, bundle8bPullAgentRegistration(serviceID)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 3, 30, 0, 0, time.UTC)
	configureToken := "configure-expiring-update-agent"
	if _, err := auth.SetServiceConfigureToken(ctx, serviceID, security.HashToken(configureToken), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(ctx, serviceID, configureToken, now, func(string) (string, string, error) {
		return "expired-ciphertext", "expired-nonce", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(ctx, serviceID, staged.Token.ID, staged.ActivationToken, now.Add(2*time.Minute), ServiceRuntimeReport{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired activation token err = %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
		t.Fatalf("expired activation changed the old active token: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired activation enabled the staged token: %v", err)
	}
}
