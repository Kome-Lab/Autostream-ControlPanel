package store

import (
	"context"
	"errors"
	"testing"
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
	if _, err := auth.PrecreateService(ctx, token, bundle8bPullAgentRegistration("updater-01")); err != nil {
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
