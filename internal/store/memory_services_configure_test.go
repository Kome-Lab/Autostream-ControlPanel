package store

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"reflect"
	"testing"
	"time"
)

func TestConfigureServiceNodeAddsRequiredObservabilityEmailScope(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "observability", []string{"service.register", "service.heartbeat", "observability.ingest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{
		ServiceID:    "observability-configure",
		ServiceType:  "observability",
		ServiceName:  "Legacy Observability",
		PublicURL:    "https://observability.example.com",
		Capabilities: map[string]any{},
	}); err != nil {
		t.Fatalf("precreate observability service: %v", err)
	}

	configureToken := "configure-observability-once"
	now := time.Date(2026, time.July, 18, 2, 0, 0, 0, time.UTC)
	if _, err := auth.SetServiceConfigureToken(ctx, "observability-configure", security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatalf("set configure token: %v", err)
	}

	newToken, _, err := auth.ConfigureServiceNode(ctx, "observability-configure", configureToken, now, ServiceRuntimeReport{Version: "1.2.3"}, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	})
	if err != nil {
		t.Fatalf("configure legacy observability node: %v", err)
	}
	wantScopes := []string{"service.register", "service.heartbeat", "observability.ingest", "notifications.email.send"}
	if !reflect.DeepEqual(newToken.Scopes, wantScopes) {
		t.Fatalf("configured observability scopes were not upgraded: got %#v want %#v", newToken.Scopes, wantScopes)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "notifications.email.send"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("configured token should authorize email relay: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
}

func TestConfigureServiceNodeAddsPairedDiscordStopScope(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "discord_bot", []string{"service.register", "service.heartbeat", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{
		ServiceID:    "discord-configure",
		ServiceType:  "discord_bot",
		ServiceName:  "Legacy Discord Bot",
		PublicURL:    "https://discord.example.com",
		Capabilities: map[string]any{},
	}); err != nil {
		t.Fatalf("precreate discord service: %v", err)
	}

	configureToken := "configure-discord-once"
	now := time.Date(2026, time.August, 9, 2, 0, 0, 0, time.UTC)
	if _, err := auth.SetServiceConfigureToken(ctx, "discord-configure", security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatalf("set configure token: %v", err)
	}

	newToken, _, err := auth.ConfigureServiceNode(ctx, "discord-configure", configureToken, now, ServiceRuntimeReport{Version: "1.3.7"}, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	})
	if err != nil {
		t.Fatalf("configure legacy discord node: %v", err)
	}
	if !hasString(newToken.Scopes, "streams.start") || !hasString(newToken.Scopes, "streams.stop") {
		t.Fatalf("configured Discord Bot scopes were not upgraded: %#v", newToken.Scopes)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "streams.stop"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("configured token should authorize paired auto-stop: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "streams.stop"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy token unexpectedly gained stop permission: %v", err)
	}
}

func TestConfigureServiceNodeSealerFailureDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "service.heartbeat", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "encoder-atomic", ServiceType: "encoder_recorder", ServiceName: "Atomic Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	configureToken := "configure-once"
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	if _, err := auth.SetServiceConfigureToken(ctx, "encoder-atomic", security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatalf("set configure token: %v", err)
	}
	beforeService, err := auth.GetService(ctx, "encoder-atomic")
	if err != nil {
		t.Fatal(err)
	}
	beforeTokens, err := auth.ListServiceTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sealErr := errors.New("seal failed")
	if _, _, err := auth.ConfigureServiceNode(ctx, "encoder-atomic", configureToken, now, ServiceRuntimeReport{Version: "1.2.3", Hostname: "encoder-host"}, func(string) (string, string, error) {
		return "", "", sealErr
	}); !errors.Is(err, sealErr) {
		t.Fatalf("expected sealer error, got %v", err)
	}

	afterService, err := auth.GetService(ctx, "encoder-atomic")
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

func TestConfigureServiceNodeCommitsTokenSecretReportAndConsumptionTogether(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "service.heartbeat", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "encoder-configure", ServiceType: "encoder_recorder", ServiceName: "Configured Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if _, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{
		ServiceID: "encoder-configure", Status: "pending", Capabilities: map[string]any{"generation": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	configureToken := "configure-once"
	now := time.Date(2026, time.July, 17, 13, 0, 0, 0, time.UTC)
	if _, err := auth.SetServiceConfigureToken(ctx, "encoder-configure", security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatalf("set configure token: %v", err)
	}
	var sealedRawToken string
	newToken, service, err := auth.ConfigureServiceNode(ctx, "encoder-configure", configureToken, now, ServiceRuntimeReport{
		Version: "1.2.3", Commit: "abc123", BuildDate: "2026-07-17", Hostname: "encoder-host", OS: "linux", Arch: "amd64",
	}, func(rawToken string) (string, string, error) {
		sealedRawToken = rawToken
		return "sealed-runtime-token", "runtime-nonce", nil
	})
	if err != nil {
		t.Fatalf("configure service node: %v", err)
	}
	if sealedRawToken == "" || sealedRawToken != newToken.RawToken || newToken.ID == oldToken.ID {
		t.Fatalf("unexpected rotated token: old=%s new=%s sealer_matches_new=%t", formatSafeServiceTokenDiagnostic("configure", oldToken, 0, "old"), formatSafeServiceTokenDiagnostic("configure", newToken, 0, "new"), sealedRawToken == newToken.RawToken)
	}
	if service.TokenID != newToken.ID || service.NodeTokenCiphertext != "sealed-runtime-token" || service.NodeTokenNonce != "runtime-nonce" {
		t.Fatalf("runtime token secret was not committed with service: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if service.ConfigureTokenUsedAt == nil || !service.ConfigureTokenUsedAt.Equal(now) || service.NodeTokenRotatedAt == nil || !service.NodeTokenRotatedAt.Equal(now) {
		t.Fatalf("configure consumption/rotation timestamps are wrong: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if service.Status != "registered" || service.Version != "1.2.3" || service.ReportedVersion != "1.2.3" || service.ReportedCommit != "abc123" || service.ReportedBuildDate != "2026-07-17" || service.ReportedHostname != "encoder-host" || service.ReportedOS != "linux" || service.ReportedArch != "amd64" {
		t.Fatalf("runtime report was not committed: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if service.LastReportedAt == nil || !service.LastReportedAt.Equal(now) || !service.UpdatedAt.Equal(now) {
		t.Fatalf("runtime report timestamps are wrong: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("configure retained old identity liveness metadata: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old runtime token should be revoked, got %v", err)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "service.heartbeat"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("new runtime token should authenticate: token=%s err=%v", formatSafeServiceTokenDiagnostic("authenticate", authenticated, 0, "unexpected_result"), err)
	}
	sealerCalled := false
	if _, _, err := auth.ConfigureServiceNode(ctx, "encoder-configure", configureToken, now.Add(time.Second), ServiceRuntimeReport{}, func(string) (string, string, error) {
		sealerCalled = true
		return "unexpected", "unexpected", nil
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("configure token reuse should be rejected, got %v", err)
	}
	if sealerCalled {
		t.Fatal("sealer must not run for an already-consumed configure token")
	}
}

func TestUpdateAgentConfigurationStagesBeforeActivation(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	scopes := []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"}
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, bundle8bPullAgentRegistration("updater-staged")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{
		ServiceID: "updater-staged", Status: "pending", Capabilities: map[string]any{"generation": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 3, 0, 0, 0, time.UTC)
	configureToken := "configure-update-agent-once"
	if _, err := auth.SetServiceConfigureToken(ctx, "updater-staged", security.HashToken(configureToken), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.ConfigureServiceNode(ctx, "updater-staged", configureToken, now, ServiceRuntimeReport{}, func(string) (string, string, error) { return "cipher", "nonce", nil }); !errors.Is(err, ErrTwoPhaseConfigureRequired) {
		t.Fatalf("legacy single-phase updater configure err = %v", err)
	}
	staged, err := auth.StageServiceNodeConfiguration(ctx, "updater-staged", configureToken, now, func(raw string) (string, string, error) {
		if raw == "" {
			t.Fatal("stage did not generate a runtime token")
		}
		return "staged-ciphertext", "staged-nonce", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Token.RawToken == "" || staged.ActivationToken == "" || staged.Token.ID == oldToken.ID {
		t.Fatalf("unexpected staged credentials: %s", formatSafeStagedServiceNodeConfigurationDiagnostic(staged))
	}
	service, err := auth.GetService(ctx, "updater-staged")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != oldToken.ID || service.StagedNodePreviousTokenID != oldToken.ID || service.StagedNodeTokenID != staged.Token.ID || service.NodeTokenCiphertext != "" || service.ConfigureTokenUsedAt == nil || service.Status != "pending" {
		t.Fatalf("stage changed the active updater identity: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
		t.Fatalf("old token stopped before activation: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("staged token became active before activation: %v", err)
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-staged", staged.Token.ID, "wrong-activation", now.Add(time.Minute), ServiceRuntimeReport{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong activation token err = %v", err)
	}
	activatedToken, activatedService, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-staged", staged.Token.ID, staged.ActivationToken, now.Add(time.Minute), ServiceRuntimeReport{Version: "v1.7.0", Commit: "abc123", BuildDate: "2026-07-21", Hostname: "central", OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if alreadyActivated || activatedToken.ID != staged.Token.ID || activatedService.TokenID != staged.Token.ID || activatedService.NodeTokenCiphertext != "staged-ciphertext" || activatedService.NodeTokenNonce != "staged-nonce" || activatedService.Status != "registered" || activatedService.ReportedVersion != "v1.7.0" {
		t.Fatalf("activation was not atomic: token=%s service=%s already=%v", formatSafeServiceTokenDiagnostic("activate", activatedToken, 0, "unexpected_result"), formatSafeRegisteredServiceDiagnostic(activatedService), alreadyActivated)
	}
	if activatedService.LastHeartbeatAt != nil || len(activatedService.ReportedCapabilities) != 0 {
		t.Fatalf("activation retained old identity liveness metadata: %s", formatSafeRegisteredServiceDiagnostic(activatedService))
	}
	if activatedService.StagedNodePreviousTokenID != "" ||
		activatedService.StagedNodeTokenID != "" ||
		activatedService.StagedNodeTokenHash != "" ||
		len(activatedService.StagedNodeTokenScopes) != 0 ||
		activatedService.StagedNodeTokenCiphertext != "" ||
		activatedService.StagedNodeTokenNonce != "" ||
		activatedService.StagedNodeActivationTokenHash != "" ||
		activatedService.StagedNodeTokenAt != nil ||
		activatedService.ConfigureTokenExpiresAt != nil {
		t.Fatalf("activation retained staged identity metadata: %s", formatSafeRegisteredServiceDiagnostic(activatedService))
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token survived activation: %v", err)
	}
	for _, scope := range scopes {
		if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, scope); err != nil {
			t.Fatalf("activated token lacks %s: %v", scope, err)
		}
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-staged", staged.Token.ID, "wrong-activation", now.Add(2*time.Minute), ServiceRuntimeReport{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("activation replay accepted the wrong activation token: %v", err)
	}
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-staged", staged.Token.ID, staged.ActivationToken, now.Add(2*time.Minute), ServiceRuntimeReport{}); err != nil || !alreadyActivated {
		t.Fatalf("activation replay was not idempotent: already=%v err=%v", alreadyActivated, err)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, "updater-staged", configureToken, now.Add(3*time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("configure token replay survived stage: %v", err)
	}
}
