package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func TestRegisterServiceRejectsServiceIDTakeoverByDifferentToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	first, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, first, ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if _, err := auth.RegisterService(ctx, first, ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}
	if _, err := auth.RegisterService(ctx, second, ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Attacker", PublicURL: "https://attacker.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for takeover registration, got %v", err)
	}
	svc, err := auth.GetService(ctx, "worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if svc.TokenID != first.ID || svc.PublicURL != "https://worker-01.example.com" {
		t.Fatalf("service was overwritten: token_id=%s public_url=%s", svc.TokenID, svc.PublicURL)
	}
	if _, err := auth.RegisterService(ctx, first, ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01b", PublicURL: "https://worker-01b.example.com", Version: "0.1.1", Capabilities: map[string]any{"updated": true}}); err != nil {
		t.Fatalf("same-token update should be allowed: %v", err)
	}
}

func TestCreateServiceTokenRequiresAtLeastOneScope(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	if _, err := auth.CreateServiceToken(ctx, "worker", nil); err == nil {
		t.Fatal("expected empty service token scopes to be rejected")
	}
}

func TestUpdateAgentCannotBeAssignedToStream(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(ctx, "updater-01", "stream-01", "admin"); !errors.Is(err, ErrInvalidServiceAssignment) {
		t.Fatalf("update_agent assignment err = %v", err)
	}
}

func TestPrecreateServiceAllowsSameTokenRegistrationOnly(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	first, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	precreated, err := auth.PrecreateService(ctx, first, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{"rtmps": true}})
	if err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if precreated.Status != "pending" || precreated.TokenID != first.ID {
		t.Fatalf("unexpected precreated service: %#v", precreated)
	}
	if _, err := auth.PrecreateService(ctx, second, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Attacker", PublicURL: "https://attacker.example.com"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected duplicate precreate to fail, got %v", err)
	}
	if _, err := auth.RegisterService(ctx, second, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Attacker", PublicURL: "https://attacker.example.com", Version: "0.1.0"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected wrong-token register to fail, got %v", err)
	}
	registered, err := auth.RegisterService(ctx, first, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01 Live", PublicURL: "https://encoder-live.example.com", Version: "0.1.1", Capabilities: map[string]any{"rtmps": true, "token": "must-redact"}})
	if err != nil {
		t.Fatalf("same-token register should succeed: %v", err)
	}
	if registered.Status != "registered" || registered.PublicURL != "https://encoder-live.example.com" {
		t.Fatalf("unexpected registered service: %#v", registered)
	}
	if _, ok := registered.Capabilities["token"]; ok {
		t.Fatalf("secret-like capability key was persisted: %#v", registered.Capabilities)
	}
}

func TestUpdateServiceMetadataPreservesRuntimeState(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if _, err := auth.Heartbeat(ctx, token, ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Metrics: map[string]any{"cpu_percent": 12.5}}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	updated, err := auth.UpdateServiceMetadata(ctx, "worker-01", ServiceMetadataUpdate{ServiceName: "Worker Edited", Description: "renamed", Host: "worker-edited.example.com", Port: 9443, SSLEnabled: true})
	if err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if updated.ServiceName != "Worker Edited" || updated.Description != "renamed" || updated.PublicURL != "https://worker-edited.example.com:9443" {
		t.Fatalf("metadata was not updated: %#v", updated)
	}
	if updated.Status != "online" || updated.LastHeartbeatAt == nil || updated.Metrics["cpu_percent"] != 12.5 || updated.TokenID != token.ID {
		t.Fatalf("runtime state should be preserved: %#v", updated)
	}
}

func TestHeartbeatWithoutCurrentStreamPreservesAssignment(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}
	if _, err := auth.RegisterService(ctx, token, ServiceRegistration{ServiceID: "encoder-01", ServiceType: "encoder_recorder", ServiceName: "Encoder 01", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}}); err != nil {
		t.Fatalf("register service: %v", err)
	}
	streamID := "stream-01"
	if _, err := auth.AssignServiceToStream(ctx, "encoder-01", streamID, "admin"); err != nil {
		t.Fatalf("assign service: %v", err)
	}
	if _, err := auth.Heartbeat(ctx, token, ServiceHeartbeat{ServiceID: "encoder-01", Status: "online", Metrics: map[string]any{"encoder.process_alive": 0}}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	svc, err := auth.GetService(ctx, "encoder-01")
	if err != nil {
		t.Fatal(err)
	}
	if svc.CurrentStreamID != streamID {
		t.Fatalf("heartbeat without current_stream_id cleared assignment: got %q want %q", svc.CurrentStreamID, streamID)
	}
	if svc.Metrics["encoder.process_alive"] != 0 {
		t.Fatalf("heartbeat metrics were not stored: %#v", svc.Metrics)
	}
}

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
		t.Fatalf("service mutated after sealer failure:\nbefore=%#v\nafter=%#v", beforeService, afterService)
	}
	if !reflect.DeepEqual(afterTokens, beforeTokens) {
		t.Fatalf("tokens mutated after sealer failure:\nbefore=%#v\nafter=%#v", beforeTokens, afterTokens)
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
		t.Fatalf("only the target service should detach: rotated=%#v sibling=%#v", rotated, sibling)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); err != nil || authenticated.ID != oldToken.ID {
		t.Fatalf("shared legacy token must remain active for sibling: token=%#v err=%v", authenticated, err)
	}
}

func TestRotateServiceNodeTokenInvalidatesOutstandingConfigureToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "updater-rotate", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	configureToken := "outstanding-configure-token"
	if _, err := auth.SetServiceConfigureToken(ctx, "updater-rotate", security.HashToken(configureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.RotateServiceNodeToken(ctx, "updater-rotate", oldToken.ID, func(string) (string, string, error) {
		return "new-ciphertext", "new-nonce", nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, "updater-rotate", configureToken, time.Now().UTC()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outstanding configure token survived runtime rotation: %v", err)
	}
	service, err := auth.GetService(ctx, "updater-rotate")
	if err != nil {
		t.Fatal(err)
	}
	if service.ConfigureTokenHash != "" || service.ConfigureTokenExpiresAt != nil || service.ConfigureTokenUsedAt != nil {
		t.Fatalf("runtime rotation retained configure token metadata: %#v", service)
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
		t.Fatalf("rotated token should authorize email relay: token=%#v err=%v", authenticated, err)
	}
	service, err := auth.GetService(ctx, "observability-shared")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != newToken.ID {
		t.Fatalf("registered service still references the old token: %#v", service)
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
		t.Fatalf("rotated token should authorize email relay: token=%#v err=%v", authenticated, err)
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
		t.Fatalf("configured token should authorize email relay: token=%#v err=%v", authenticated, err)
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
		t.Fatalf("service mutated after sealer failure:\nbefore=%#v\nafter=%#v", beforeService, afterService)
	}
	if !reflect.DeepEqual(afterTokens, beforeTokens) {
		t.Fatalf("tokens mutated after sealer failure:\nbefore=%#v\nafter=%#v", beforeTokens, afterTokens)
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
		t.Fatalf("unexpected rotated token: old=%#v new=%#v sealed=%q", oldToken, newToken, sealedRawToken)
	}
	if service.TokenID != newToken.ID || service.NodeTokenCiphertext != "sealed-runtime-token" || service.NodeTokenNonce != "runtime-nonce" {
		t.Fatalf("runtime token secret was not committed with service: %#v", service)
	}
	if service.ConfigureTokenUsedAt == nil || !service.ConfigureTokenUsedAt.Equal(now) || service.NodeTokenRotatedAt == nil || !service.NodeTokenRotatedAt.Equal(now) {
		t.Fatalf("configure consumption/rotation timestamps are wrong: %#v", service)
	}
	if service.Status != "registered" || service.Version != "1.2.3" || service.ReportedVersion != "1.2.3" || service.ReportedCommit != "abc123" || service.ReportedBuildDate != "2026-07-17" || service.ReportedHostname != "encoder-host" || service.ReportedOS != "linux" || service.ReportedArch != "amd64" {
		t.Fatalf("runtime report was not committed: %#v", service)
	}
	if service.LastReportedAt == nil || !service.LastReportedAt.Equal(now) || !service.UpdatedAt.Equal(now) {
		t.Fatalf("runtime report timestamps are wrong: %#v", service)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old runtime token should be revoked, got %v", err)
	}
	if authenticated, err := auth.AuthenticateServiceToken(ctx, newToken.RawToken, "service.heartbeat"); err != nil || authenticated.ID != newToken.ID {
		t.Fatalf("new runtime token should authenticate: token=%#v err=%v", authenticated, err)
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
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "updater-staged", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
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
		t.Fatalf("unexpected staged credentials: %#v", staged)
	}
	service, err := auth.GetService(ctx, "updater-staged")
	if err != nil {
		t.Fatal(err)
	}
	if service.TokenID != oldToken.ID || service.StagedNodePreviousTokenID != oldToken.ID || service.StagedNodeTokenID != staged.Token.ID || service.NodeTokenCiphertext != "" || service.ConfigureTokenUsedAt == nil || service.Status != "pending" {
		t.Fatalf("stage changed the active updater identity: %#v", service)
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
		t.Fatalf("activation was not atomic: token=%#v service=%#v already=%v", activatedToken, activatedService, alreadyActivated)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "service.heartbeat"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token survived activation: %v", err)
	}
	for _, scope := range scopes {
		if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, scope); err != nil {
			t.Fatalf("activated token lacks %s: %v", scope, err)
		}
	}
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-staged", staged.Token.ID, staged.ActivationToken, now.Add(2*time.Minute), ServiceRuntimeReport{}); err != nil || !alreadyActivated {
		t.Fatalf("activation replay was not idempotent: already=%v err=%v", alreadyActivated, err)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, "updater-staged", configureToken, now.Add(3*time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("configure token replay survived stage: %v", err)
	}
}

func TestUpdateAgentConfigurationRejectsExpiredActivationWithoutChangingActiveToken(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim"})
	if err != nil {
		t.Fatal(err)
	}
	const serviceID = "updater-expired-stage"
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
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

func TestRegeneratingConfigureTokenDiscardsInactiveUpdateAgentStage(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "updater-restage", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
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
	if service.TokenID != oldToken.ID || service.StagedNodeTokenID != "" || service.StagedNodeActivationTokenHash != "" || service.ConfigureTokenUsedAt != nil {
		t.Fatalf("configure regeneration retained an inactive stage: %#v", service)
	}
	if _, _, _, err := auth.ActivateServiceNodeConfiguration(ctx, "updater-restage", staged.Token.ID, staged.ActivationToken, now.Add(time.Minute), ServiceRuntimeReport{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("discarded activation remained usable: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
		t.Fatalf("active token changed while discarding stage: %v", err)
	}
}
