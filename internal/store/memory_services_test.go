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
		t.Fatalf("unexpected precreated service: %s", formatSafeRegisteredServiceDiagnostic(precreated))
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
		t.Fatalf("unexpected registered service: %s", formatSafeRegisteredServiceDiagnostic(registered))
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
		t.Fatalf("metadata was not updated: %s", formatSafeRegisteredServiceDiagnostic(updated))
	}
	if updated.Status != "online" || updated.LastHeartbeatAt == nil || updated.Metrics["cpu_percent"] != 12.5 || updated.TokenID != token.ID {
		t.Fatalf("runtime state should be preserved: %s", formatSafeRegisteredServiceDiagnostic(updated))
	}
}

func TestHeartbeatReportsEndpointWithoutChangingAppliedEndpoint(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{
		ServiceID:   "worker-endpoint",
		ServiceType: "worker",
		ServiceName: "Worker Endpoint",
		Host:        "worker.example.com",
		Port:        8084,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8084",
	}); err != nil {
		t.Fatalf("precreate service: %v", err)
	}

	service, err := auth.Heartbeat(ctx, token, ServiceHeartbeat{
		ServiceID: "worker-endpoint",
		Status:    "online",
		API: &NodeAgentAPI{
			Host:       "127.0.0.1",
			Port:       19084,
			SSLEnabled: false,
		},
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if service.Host != "worker.example.com" || service.Port != 8084 || !service.SSLEnabled || service.PublicURL != "https://worker.example.com:8084" {
		t.Fatalf("heartbeat changed the applied endpoint: %s", formatSafeRegisteredServiceDiagnostic(service))
	}
	if service.AppliedEndpoint == nil || service.AppliedEndpoint.Host != "worker.example.com" || service.AppliedEndpoint.Port != 8084 {
		t.Fatalf("applied endpoint was not retained: %#v", service.AppliedEndpoint)
	}
	if service.DesiredEndpoint == nil || service.DesiredEndpoint.Host != "worker.example.com" || service.DesiredEndpoint.Port != 8084 {
		t.Fatalf("desired endpoint was not retained: %#v", service.DesiredEndpoint)
	}
	if service.ReportedEndpoint == nil || service.ReportedEndpoint.Host != "127.0.0.1" || service.ReportedEndpoint.Port != 19084 || service.ReportedEndpoint.PublicURL != "http://127.0.0.1:19084" {
		t.Fatalf("reported endpoint was not recorded: %#v", service.ReportedEndpoint)
	}
}

func TestPullUpdateAgentRegistrationIsEndpointlessAndHostBound(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	precreated, err := auth.PrecreateService(ctx, token, ServiceRegistration{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent A",
		TransportMode:   "pull_v2",
		ExecutionHostID: "host-a",
		OwnershipEpoch:  7,
	})
	if err != nil {
		t.Fatalf("precreate endpointless pull agent: %v", err)
	}
	if precreated.PublicURL != "" || precreated.AppliedEndpoint != nil {
		t.Fatalf("endpointless pull agent unexpectedly has an endpoint: %s", formatSafeRegisteredServiceDiagnostic(precreated))
	}

	registered, err := auth.RegisterService(ctx, token, ServiceRegistration{
		ServiceID:   "host-agent-a",
		ServiceType: "update_agent",
		ServiceName: "Host Agent A",
		Version:     "v2.0.0",
	})
	if err != nil {
		t.Fatalf("register endpointless pull agent: %v", err)
	}
	if registered.TransportMode != "pull_v2" || registered.ExecutionHostID != "host-a" || registered.OwnershipEpoch != 7 {
		t.Fatalf("agent-controlled registration changed host ownership: %s", formatSafeRegisteredServiceDiagnostic(registered))
	}
	if registered.PublicURL != "" || registered.AppliedEndpoint != nil {
		t.Fatalf("endpointless pull agent unexpectedly has an endpoint after registration: %s", formatSafeRegisteredServiceDiagnostic(registered))
	}
}

func TestEndpointlessRegistrationRemainsInvalidOutsidePullUpdateAgent(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		serviceType   string
		transport     string
		executionHost string
	}{
		{name: "worker", serviceType: "worker"},
		{name: "legacy updater", serviceType: "update_agent", transport: "ssh_v1"},
		{name: "pull updater without host binding", serviceType: "update_agent", transport: "pull_v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := NewMemoryAuthStore()
			token, err := auth.CreateServiceToken(ctx, tc.serviceType, []string{"service.register"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = auth.PrecreateService(ctx, token, ServiceRegistration{
				ServiceID:       "endpointless-invalid",
				ServiceType:     tc.serviceType,
				ServiceName:     "Endpointless Invalid",
				TransportMode:   tc.transport,
				ExecutionHostID: tc.executionHost,
				OwnershipEpoch:  1,
			})
			if !errors.Is(err, ErrInvalidServiceRegistration) {
				t.Fatalf("endpointless registration err = %v, want %v", err, ErrInvalidServiceRegistration)
			}
		})
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
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "updater-rotate", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{
		ServiceID: "updater-rotate", Status: "online", Capabilities: map[string]any{"generation": "old"},
	}); err != nil {
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
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{
		ServiceID:   "updater-revoke",
		ServiceType: "update_agent",
		ServiceName: "Updater",
		PublicURL:   "https://updater.example.com",
	}); err != nil {
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
	if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{ServiceID: "updater-staged", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Capabilities: map[string]any{}}); err != nil {
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
	if _, err := auth.PrecreateService(t.Context(), token, ServiceRegistration{
		ServiceID:    serviceID,
		ServiceType:  "update_agent",
		ServiceName:  serviceID,
		PublicURL:    "https://" + serviceID + ".example.com",
		Capabilities: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	return token
}

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
	if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{
		ServiceID:   serviceID,
		ServiceType: "update_agent",
		ServiceName: "Updater",
		PublicURL:   "https://updater.example.com",
	}); err != nil {
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
	if _, err := auth.PrecreateService(ctx, legacyToken, ServiceRegistration{
		ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "Legacy Updater",
		PublicURL: "https://legacy-updater.example.com",
	}); err != nil {
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

func TestRegeneratingConfigureTokenRetainsPendingTombstoneAndInvalidatesOldStage(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	oldToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
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
				service.TransportMode = SystemUpdateTransportSSHV1
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
