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

func TestHeartbeatWithoutCurrentStreamPreservesAssignment(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
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
