package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func TestRotateServiceNodeTokenSealerFailureDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "worker-atomic", ServiceType: "worker", ServiceName: "Atomic Worker", PublicURL: "https://worker.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if _, err := auth.SetServiceNodeTokenSecret(ctx, "worker-atomic", "old-ciphertext", "old-nonce"); err != nil {
		t.Fatalf("set initial node token secret: %v", err)
	}
	beforeService, err := auth.GetService(ctx, "worker-atomic")
	if err != nil {
		t.Fatal(err)
	}
	beforeTokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sealErr := errors.New("seal failed")
	if _, _, err := auth.RotateServiceNodeToken(ctx, "worker-atomic", oldToken.ID, func(string) (string, string, error) {
		return "", "", sealErr
	}); !errors.Is(err, sealErr) {
		t.Fatalf("expected sealer error, got %v", err)
	}

	afterService, err := auth.GetService(ctx, "worker-atomic")
	if err != nil {
		t.Fatal(err)
	}
	afterTokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterService, beforeService) {
		t.Fatalf("service mutated after sealer failure: before=%s after=%s", formatSafeRegisteredServiceDiagnostic(beforeService), formatSafeRegisteredServiceDiagnostic(afterService))
	}
	if !reflect.DeepEqual(afterTokens, beforeTokens) {
		t.Fatalf("tokens mutated after sealer failure: before=%s after=%s", formatSafeSensitiveCompositeDiagnostic(beforeTokens), formatSafeSensitiveCompositeDiagnostic(afterTokens))
	}
}

func TestRotateServiceNodeTokenRejectsImmediatePullV2RotationWithoutMutation(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{
		"service.register",
		"service.heartbeat",
		"service.config.read",
		"updates.claim",
		"updates.report",
		"updates.authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{
		ServiceID:       "host-agent-rotation-guard",
		ServiceType:     "update_agent",
		ServiceName:     "Guarded Host Agent",
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: "host-rotation-guard",
		OwnershipEpoch:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.SetServiceNodeTokenSecret(ctx, "host-agent-rotation-guard", "old-ciphertext", "old-nonce"); err != nil {
		t.Fatal(err)
	}
	beforeService, err := auth.GetService(ctx, "host-agent-rotation-guard")
	if err != nil {
		t.Fatal(err)
	}
	beforeTokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sealCalled := false
	if _, _, err := auth.RotateServiceNodeToken(ctx, beforeService.ServiceID, oldToken.ID, func(string) (string, string, error) {
		sealCalled = true
		return "new-ciphertext", "new-nonce", nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("immediate pull_v2 rotation error = %v, want ErrConflict", err)
	}
	if sealCalled {
		t.Fatal("immediate pull_v2 rotation generated and sealed a replacement token")
	}
	afterService, err := auth.GetService(ctx, "host-agent-rotation-guard")
	if err != nil {
		t.Fatal(err)
	}
	afterTokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterService, beforeService) {
		t.Fatalf("rejected pull_v2 rotation mutated service: before=%s after=%s", formatSafeRegisteredServiceDiagnostic(beforeService), formatSafeRegisteredServiceDiagnostic(afterService))
	}
	if !reflect.DeepEqual(afterTokens, beforeTokens) {
		t.Fatalf("rejected pull_v2 rotation mutated tokens: before=%s after=%s", formatSafeSensitiveCompositeDiagnostic(beforeTokens), formatSafeSensitiveCompositeDiagnostic(afterTokens))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); err != nil {
		t.Fatalf("rejected pull_v2 rotation invalidated the existing token: %v", err)
	}
}

func TestRotateServiceNodeTokenPreservesSharedLegacyToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{"worker-shared-a", "worker-shared-b"} {
		if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Capabilities: map[string]any{}}); err != nil {
			t.Fatalf("precreate %s: %v", serviceID, err)
		}
	}
	newToken, rotated, err := auth.RotateServiceNodeToken(ctx, "worker-shared-a", oldToken.ID, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	})
	if err != nil {
		t.Fatalf("rotate shared legacy token: %v", err)
	}
	sibling, err := auth.GetService(ctx, "worker-shared-b")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.TokenID != newToken.ID || sibling.TokenID != oldToken.ID {
		t.Fatalf("only the target service should detach: rotated=%s sibling=%s", formatSafeRegisteredServiceDiagnostic(rotated), formatSafeRegisteredServiceDiagnostic(sibling))
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); err != nil || authenticated.ID != oldToken.ID {
		t.Fatalf("shared legacy token must remain active for sibling: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
}

func TestRotateServiceNodeTokenInvalidatesOutstandingConfigureToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "worker-rotate", ServiceType: "worker", ServiceName: "Worker", PublicURL: "https://worker.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{
		ServiceID: "worker-rotate", Status: "online", Capabilities: map[string]any{"generation": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	configureToken := "outstanding-configure-token"
	if _, err := auth.SetServiceConfigureToken(ctx, "worker-rotate", security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.RotateServiceNodeToken(ctx, "worker-rotate", oldToken.ID, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, "worker-rotate", configureToken, time.Now().UTC()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outstanding configure token survived runtime rotation: %v", err)
	}
	service, err := auth.GetService(ctx, "worker-rotate")
	if err != nil {
		t.Fatal(err)
	}
	if service.ConfigureTokenHash != "" || service.ConfigureTokenExpiresAt != nil ||
		service.ConfigureTokenUsedAt != nil || service.LastHeartbeatAt != nil ||
		len(service.ReportedCapabilities) != 0 {
		t.Fatalf("runtime rotation retained configure token or liveness metadata: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
}

func TestRevokeServiceTokenClearsRuntimeReadinessAndRejectsPreviouslyAuthenticatedHeartbeat(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(
		ctx,
		"update_agent",
		[]string{"service.register", "service.heartbeat", "updates.claim"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, token, bundle8bPullAgentRegistration("updater-revoke")); err != nil {
		t.Fatal(err)
	}
	authenticated, err := auth.AuthenticateServiceToken(
		ctx,
		token.RawToken,
		"service.heartbeat",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(ctx, authenticated, ServiceHeartbeat{
		ServiceID: "updater-revoke",
		Status:    "online",
		Capabilities: map[string]any{
			"bootstrap_encryption_key_fingerprint": "stale-fingerprint",
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := auth.RevokeServiceToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	service, err := auth.GetService(ctx, "updater-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("revocation retained runtime readiness: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, err := auth.Heartbeat(ctx, authenticated, ServiceHeartbeat{
		ServiceID:    "updater-revoke",
		Status:       "online",
		Capabilities: map[string]any{"restored": true},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("previously authenticated token restored heartbeat after revoke: %v", err)
	}
	service, err = auth.GetService(ctx, "updater-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("rejected heartbeat restored runtime readiness: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, token.RawToken, "updates.claim"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token still authenticated: %v", err)
	}
}

func TestRotateServiceTokenAddsRequiredObservabilityEmailScope(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "observability", []string{"service.register", "service.heartbeat", "observability.ingest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "observability-shared", ServiceType: "observability", ServiceName: "Legacy Observability", PublicURL: "https://observability.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate observability service: %v", err)
	}
	if _, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{
		ServiceID: "observability-shared", Status: "online", Capabilities: map[string]any{"generation": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	newToken, err := auth.RotateServiceToken(ctx, oldToken.ID)
	if err != nil {
		t.Fatalf("rotate legacy observability token: %v", err)
	}
	wantScopes := []string{"service.register", "service.heartbeat", "observability.ingest", "notifications.email.send"}
	if !reflect.DeepEqual(newToken.Scopes, wantScopes) {
		t.Fatalf("rotated observability scopes were not upgraded: got %#v want %#v", newToken.Scopes, wantScopes)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "observability.ingest"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old runtime token should be revoked, got %v", err)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "notifications.email.send"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("rotated token should authorize email relay: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
	service, err := auth.GetService(ctx, "observability-shared")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != newToken.ID || service.LastHeartbeatAt != nil ||
		len(service.ReportedCapabilities) != 0 {
		t.Fatalf("generic rotation retained old identity/liveness metadata: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
}

func TestRotateServiceNodeTokenAddsRequiredObservabilityEmailScope(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "observability", []string{"service.register", "service.heartbeat", "observability.ingest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "observability-legacy", ServiceType: "observability", ServiceName: "Legacy Observability", PublicURL: "https://observability.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate observability service: %v", err)
	}

	newToken, _, err := auth.RotateServiceNodeToken(ctx, "observability-legacy", oldToken.ID, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	})
	if err != nil {
		t.Fatalf("rotate legacy observability token: %v", err)
	}
	if !hasString(newToken.Scopes, "observability.ingest") || !hasString(newToken.Scopes, "notifications.email.send") {
		t.Fatalf("rotated observability scopes were not upgraded: %#v", newToken.Scopes)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "notifications.email.send"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("rotated token should authorize email relay: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
}

func TestServiceTokenScopesForRotationUpgradesLegacyObservabilityToken(t *testing.T) {
	originalScopes := []string{"service.register", "service.heartbeat", "observability.ingest"}
	rotatedScopes := serviceTokenScopesForRotation(ServiceToken{
		ServiceType: "observability",
		Scopes:      originalScopes,
	})

	want := []string{"service.register", "service.heartbeat", "observability.ingest", "notifications.email.send"}
	if !reflect.DeepEqual(rotatedScopes, want) {
		t.Fatalf("unexpected rotated scopes: got %#v want %#v", rotatedScopes, want)
	}
	if !reflect.DeepEqual(originalScopes, []string{"service.register", "service.heartbeat", "observability.ingest"}) {
		t.Fatalf("rotation mutated the old token scopes: %#v", originalScopes)
	}

	alreadyUpgraded := serviceTokenScopesForRotation(ServiceToken{
		ServiceType: "observability",
		Scopes:      []string{"observability.ingest", "notifications.email.send"},
	})
	if !reflect.DeepEqual(alreadyUpgraded, []string{"observability.ingest", "notifications.email.send"}) {
		t.Fatalf("email scope was duplicated: %#v", alreadyUpgraded)
	}

	workerScopes := serviceTokenScopesForRotation(ServiceToken{
		ServiceType: "worker",
		Scopes:      []string{"service.register", "service.heartbeat"},
	})
	if !reflect.DeepEqual(workerScopes, []string{"service.register", "service.heartbeat"}) {
		t.Fatalf("non-observability scopes must be preserved unchanged: %#v", workerScopes)
	}
}
