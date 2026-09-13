package store

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func TestFIX010UpdateAgentStageRejectsSharedTokenReferences(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RegisteredService, string)
	}{
		{
			name: "current",
			edit: func(service *RegisteredService, oldTokenID string) {
				service.TokenID = oldTokenID
			},
		},
		{
			name: "staged_previous",
			edit: func(service *RegisteredService, oldTokenID string) {
				service.StagedNodePreviousTokenID = oldTokenID
			},
		},
		{
			name: "staged_token",
			edit: func(service *RegisteredService, oldTokenID string) {
				service.StagedNodeTokenID = oldTokenID
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			auth := NewMemoryAuthStore()
			oldToken := createMemoryFIX010UpdateAgentService(t, auth, "fix010-stage-target")
			createMemoryFIX010UpdateAgentService(t, auth, "fix010-stage-other")
			now := time.Date(2026, time.August, 25, 1, 0, 0, 0, time.UTC)
			configureToken := "fix010-stage-configure-" + test.name
			if _, err := auth.SetServiceConfigureToken(
				ctx,
				"fix010-stage-target",
				security.HashToken(configureToken),
				now.Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}

			auth.mu.Lock()
			other := auth.services["fix010-stage-other"]
			test.edit(&other, oldToken.ID)
			auth.services[other.ServiceID] = other
			beforeTarget := auth.services["fix010-stage-target"]
			beforeOther := auth.services["fix010-stage-other"]
			beforeOldToken := auth.serviceTokens[oldToken.ID]
			beforeTokenCount := len(auth.serviceTokens)
			auth.mu.Unlock()

			sealerCalled := false
			_, err := auth.StageServiceNodeConfiguration(
				ctx,
				"fix010-stage-target",
				configureToken,
				now,
				func(string) (string, string, error) {
					sealerCalled = true
					return "fix010-stage-ciphertext", "fix010-stage-nonce", nil
				},
			)
			if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
				t.Fatalf("stage error = %v, want shared-token conflict", err)
			}
			if sealerCalled {
				t.Fatal("stage sealed a new token before rejecting the shared reference")
			}

			auth.mu.Lock()
			afterTarget := auth.services["fix010-stage-target"]
			afterOther := auth.services["fix010-stage-other"]
			afterOldToken := auth.serviceTokens[oldToken.ID]
			afterTokenCount := len(auth.serviceTokens)
			auth.mu.Unlock()
			if !reflect.DeepEqual(afterTarget, beforeTarget) || !reflect.DeepEqual(afterOther, beforeOther) {
				t.Fatalf(
					"shared stage changed service references: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q",
					afterTarget.TokenID,
					afterTarget.StagedNodePreviousTokenID,
					afterTarget.StagedNodeTokenID,
					afterOther.TokenID,
					afterOther.StagedNodePreviousTokenID,
					afterOther.StagedNodeTokenID,
				)
			}
			if !reflect.DeepEqual(afterOldToken, beforeOldToken) || afterTokenCount != beforeTokenCount {
				t.Fatalf(
					"shared stage changed token state: old_revoked=%t token_count=%d want_count=%d",
					afterOldToken.RevokedAt != nil,
					afterTokenCount,
					beforeTokenCount,
				)
			}
		})
	}
}

func TestFIX010UpdateAgentActivationRejectsSharedTokenReferences(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RegisteredService, string, string)
	}{
		{
			name: "old_current",
			edit: func(service *RegisteredService, oldTokenID, _ string) {
				service.TokenID = oldTokenID
			},
		},
		{
			name: "old_staged_previous",
			edit: func(service *RegisteredService, oldTokenID, _ string) {
				service.StagedNodePreviousTokenID = oldTokenID
			},
		},
		{
			name: "old_staged_token",
			edit: func(service *RegisteredService, oldTokenID, _ string) {
				service.StagedNodeTokenID = oldTokenID
			},
		},
		{
			name: "new_current",
			edit: func(service *RegisteredService, _, stagedTokenID string) {
				service.TokenID = stagedTokenID
			},
		},
		{
			name: "new_staged_previous",
			edit: func(service *RegisteredService, _, stagedTokenID string) {
				service.StagedNodePreviousTokenID = stagedTokenID
			},
		},
		{
			name: "new_staged_token",
			edit: func(service *RegisteredService, _, stagedTokenID string) {
				service.StagedNodeTokenID = stagedTokenID
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			auth := NewMemoryAuthStore()
			oldToken := createMemoryFIX010UpdateAgentService(t, auth, "fix010-activate-target")
			now := time.Date(2026, time.August, 25, 2, 0, 0, 0, time.UTC)
			configureToken := "fix010-activate-configure-" + test.name
			if _, err := auth.SetServiceConfigureToken(
				ctx,
				"fix010-activate-target",
				security.HashToken(configureToken),
				now.Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
			staged, err := auth.StageServiceNodeConfiguration(
				ctx,
				"fix010-activate-target",
				configureToken,
				now,
				func(string) (string, string, error) {
					return "fix010-activation-ciphertext", "fix010-activation-nonce", nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			createMemoryFIX010UpdateAgentService(t, auth, "fix010-activate-other")

			auth.mu.Lock()
			other := auth.services["fix010-activate-other"]
			test.edit(&other, oldToken.ID, staged.Token.ID)
			auth.services[other.ServiceID] = other
			beforeTarget := auth.services["fix010-activate-target"]
			beforeOther := auth.services["fix010-activate-other"]
			beforeOldToken := auth.serviceTokens[oldToken.ID]
			beforeTokenCount := len(auth.serviceTokens)
			auth.mu.Unlock()

			activatedToken, activatedService, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
				ctx,
				"fix010-activate-target",
				staged.Token.ID,
				staged.ActivationToken,
				now.Add(time.Minute),
				ServiceRuntimeReport{},
			)
			if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
				t.Fatalf("activation error = %v, want shared-token conflict", err)
			}
			if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
				t.Fatalf(
					"shared activation returned a success value: token_id=%q service_id=%q already=%t",
					activatedToken.ID,
					activatedService.ServiceID,
					alreadyActivated,
				)
			}

			auth.mu.Lock()
			afterTarget := auth.services["fix010-activate-target"]
			afterOther := auth.services["fix010-activate-other"]
			afterOldToken := auth.serviceTokens[oldToken.ID]
			_, stagedTokenPersisted := auth.serviceTokens[staged.Token.ID]
			afterTokenCount := len(auth.serviceTokens)
			auth.mu.Unlock()
			if !reflect.DeepEqual(afterTarget, beforeTarget) || !reflect.DeepEqual(afterOther, beforeOther) {
				t.Fatalf(
					"shared activation changed service references: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q",
					afterTarget.TokenID,
					afterTarget.StagedNodePreviousTokenID,
					afterTarget.StagedNodeTokenID,
					afterOther.TokenID,
					afterOther.StagedNodePreviousTokenID,
					afterOther.StagedNodeTokenID,
				)
			}
			if !reflect.DeepEqual(afterOldToken, beforeOldToken) || stagedTokenPersisted || afterTokenCount != beforeTokenCount {
				t.Fatalf(
					"shared activation changed token state: old_revoked=%t staged_persisted=%t token_count=%d want_count=%d",
					afterOldToken.RevokedAt != nil,
					stagedTokenPersisted,
					afterTokenCount,
					beforeTokenCount,
				)
			}
		})
	}
}

func createMemoryFIX010UpdateAgentService(
	t *testing.T,
	auth *MemoryAuthStore,
	serviceID string,
) ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{
		"service.register",
		"service.heartbeat",
		"updates.claim",
		"updates.report",
		"updates.authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, bundle8bPullAgentRegistration(serviceID)); err != nil {
		t.Fatal(err)
	}
	return token
}
