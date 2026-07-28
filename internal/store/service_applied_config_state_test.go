package store

import (
	"context"
	"strings"
	"testing"
)

func TestServiceAppliedConfigStateDefaultsAndSurvivesMemoryRegistration(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registration := ServiceRegistration{
		ServiceID: "worker-config-state", ServiceType: "worker", ServiceName: "Worker",
		Host: "worker.example.com", Port: 8084, PublicURL: "https://worker.example.com:8084",
		Version: "v1.0.0", Capabilities: map[string]any{},
	}
	precreated, err := auth.PrecreateService(ctx, token, registration)
	if err != nil {
		t.Fatal(err)
	}
	if precreated.AppliedConfigRevision != 1 || precreated.AppliedConfigSHA256 != "" {
		t.Fatalf("new service applied config state = revision %d digest %q", precreated.AppliedConfigRevision, precreated.AppliedConfigSHA256)
	}

	const revision = int64(7)
	digest := "sha256:" + strings.Repeat("a", 64)
	auth.mu.Lock()
	stored := auth.services[registration.ServiceID]
	stored.AppliedConfigRevision = revision
	stored.AppliedConfigSHA256 = digest
	auth.services[registration.ServiceID] = stored
	auth.mu.Unlock()

	registered, err := auth.RegisterService(ctx, token, registration)
	if err != nil {
		t.Fatal(err)
	}
	if registered.AppliedConfigRevision != revision || registered.AppliedConfigSHA256 != digest {
		t.Fatalf("registration lost applied config state: %#v", registered)
	}
}

func TestServiceSelectsCarryAppliedConfigStateIntoBothScanners(t *testing.T) {
	for name, query := range map[string]string{
		"plain":   serviceSelectColumns,
		"aliased": serviceSelectColumnsAliased,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(query, "applied_config_revision") ||
				!strings.Contains(query, "applied_config_sha256") {
				t.Fatalf("%s service select omits applied config state:\n%s", name, query)
			}
		})
	}
}
