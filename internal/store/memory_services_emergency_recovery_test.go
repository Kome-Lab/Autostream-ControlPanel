package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"testing"
	"time"
)

func TestRegeneratingConfigureTokenRetainsPendingTombstoneAndInvalidatesOldStage(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, bundle8bPullAgentRegistration("updater-restage")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 4, 0, 0, 0, time.UTC)
	if _, err := auth.SetServiceConfigureToken(ctx, "updater-restage", security.HashToken("first-configure"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(ctx, "updater-restage", "first-configure", now, func(string) (string, string, error) { return "cipher", "nonce", nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.SetServiceConfigureToken(ctx, "updater-restage", security.HashToken("replacement-configure"), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	service, err := auth.GetService(ctx, "updater-restage")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != oldToken.ID || service.StagedNodeTokenID != staged.Token.ID ||
		service.StagedNodePreviousTokenID != "" || service.StagedNodeTokenHash != "" ||
		len(service.StagedNodeTokenScopes) != 0 || service.StagedNodeTokenCiphertext != "" ||
		service.StagedNodeTokenNonce != "" || service.StagedNodeActivationTokenHash != "" ||
		service.StagedNodeTokenAt != nil || service.ConfigureTokenUsedAt != nil {
		t.Fatalf("configure regeneration did not retain a secret-free pending tombstone: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-restage", staged.Token.ID, staged.ActivationToken, now.Add(time.Minute), ServiceRuntimeReport{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalidated activation remained usable: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
		t.Fatalf("active token changed while discarding stage: %v", err)
	}
	replacement, err := auth.StageServiceNodeConfiguration(ctx, "updater-restage", "replacement-configure", now.Add(time.Minute), func(string) (string, string, error) {
		return "replacement-cipher", "replacement-nonce", nil
	})
	if err != nil {
		t.Fatalf("replacement stage did not overwrite tombstone: %v", err)
	}
	if replacement.Token.ID == staged.Token.ID || replacement.Service.StagedNodeTokenID != replacement.Token.ID {
		t.Fatalf("replacement stage did not bind a fresh pending identity: %s", formatSafeStagedServiceNodeConfigurationDiagnostic(replacement))
	}
}

func TestEmergencyRevokedNodeConfigurationAnchorRequiresScrubbedPullV2(t *testing.T) {
	now := time.Date(2026, time.July, 28, 17, 0, 0, 0, time.UTC)
	baseService := RegisteredService{
		ServiceID:            "emergency-anchor",
		ServiceType:          "update_agent",
		TransportMode:        SystemUpdateTransportPullV2,
		TokenID:              "revoked-token",
		Status:               "offline",
		ReportedCapabilities: map[string]any{},
	}
	baseToken := ServiceToken{
		ID:          "revoked-token",
		ServiceType: "update_agent",
		RevokedAt:   &now,
	}
	tests := []struct {
		name string
		edit func(*RegisteredService, *ServiceToken)
	}{
		{
			name: "wrong service type",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.ServiceType = "worker"
			},
		},
		{
			name: "wrong transport",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.TransportMode = "unsupported"
			},
		},
		{
			name: "different current token",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.TokenID = "different-token"
			},
		},
		{
			name: "service online",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.Status = "online"
			},
		},
		{
			name: "heartbeat retained",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.LastHeartbeatAt = &now
			},
		},
		{
			name: "active ciphertext retained",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.NodeTokenCiphertext = "ciphertext"
			},
		},
		{
			name: "active nonce retained",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.NodeTokenNonce = "nonce"
			},
		},
		{
			name: "reported capabilities retained",
			edit: func(service *RegisteredService, _ *ServiceToken) {
				service.ReportedCapabilities = map[string]any{"host_agent": true}
			},
		},
		{
			name: "wrong token type",
			edit: func(_ *RegisteredService, token *ServiceToken) {
				token.ServiceType = "worker"
			},
		},
		{
			name: "token not revoked",
			edit: func(_ *RegisteredService, token *ServiceToken) {
				token.RevokedAt = nil
			},
		},
	}
	if !isEmergencyRevokedNodeConfigurationAnchor(baseService, baseToken) {
		t.Fatal("valid emergency recovery anchor was rejected")
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := baseService
			token := baseToken
			test.edit(&service, &token)
			if isEmergencyRevokedNodeConfigurationAnchor(service, token) {
				t.Fatal("unsafe emergency recovery anchor was accepted")
			}
		})
	}
}

func TestEmergencyConfigureStageAcceptsOnlyDistinctSecretFreeTombstone(t *testing.T) {
	now := time.Date(2026, time.July, 28, 17, 30, 0, 0, time.UTC)
	base := RegisteredService{TokenID: "active-token"}
	tombstone := base
	tombstone.StagedNodeTokenID = "abandoned-token"
	if !hasStagedServiceNodeConfiguration(tombstone) ||
		!hasOnlyStagedNodeConfigurationTombstone(tombstone) {
		t.Fatal("distinct secret-free tombstone was rejected")
	}
	tests := []struct {
		name string
		edit func(*RegisteredService)
	}{
		{
			name: "same as active token id",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenID = service.TokenID
			},
		},
		{
			name: "previous token id",
			edit: func(service *RegisteredService) {
				service.StagedNodePreviousTokenID = "previous-token"
			},
		},
		{
			name: "token hash",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenHash = "hash"
			},
		},
		{
			name: "token scopes",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenScopes = []string{"updates.claim"}
			},
		},
		{
			name: "token ciphertext",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenCiphertext = "ciphertext"
			},
		},
		{
			name: "token nonce",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenNonce = "nonce"
			},
		},
		{
			name: "activation hash",
			edit: func(service *RegisteredService) {
				service.StagedNodeActivationTokenHash = "activation-hash"
			},
		},
		{
			name: "stage timestamp",
			edit: func(service *RegisteredService) {
				service.StagedNodeTokenAt = &now
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := tombstone
			test.edit(&service)
			if !hasStagedServiceNodeConfiguration(service) {
				t.Fatal("staged residue was not detected")
			}
			if hasOnlyStagedNodeConfigurationTombstone(service) {
				t.Fatal("unsafe staged residue was accepted as a tombstone")
			}
		})
	}
}
