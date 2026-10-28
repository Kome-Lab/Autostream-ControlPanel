package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceMetadataRevisionBindsConfigurationAndExcludesObservations(t *testing.T) {
	base := RegisteredService{ServiceID: "worker-cas", ServiceType: "worker", ServiceName: "Original", Description: "Original description", Host: "worker.example.com", Port: 8084, SSLEnabled: true, PublicURL: "https://worker.example.com:8084", EndpointRevision: 3, TransportMode: "pull_v2", ExecutionHostID: "host-cas", OwnershipEpoch: 2, CurrentStreamID: "stream-cas"}
	stamp := ServiceMetadataRevision(base)
	if !ValidServiceMetadataRevision(stamp) {
		t.Fatal("semantic metadata revision is not a lowercase SHA256 stamp")
	}
	observed := base
	now := time.Now().UTC()
	observed.UpdatedAt, observed.CreatedAt = now, now
	observed.LastHeartbeatAt, observed.LastReportedAt = &now, &now
	observed.Status, observed.Version, observed.ReportedVersion, observed.ReportedHostname = "offline", "reported-version", "other-version", "reported-host"
	observed.Metrics = map[string]any{"cpu_percent": 80}
	observed.Capabilities = map[string]any{"streaming": true}
	observed.ReportedCapabilities = map[string]any{"observer": true}
	observed.ReportedEndpoint = &ServiceEndpoint{Host: "127.0.0.1", Port: 19084}
	observed.AppliedEndpointRevision, observed.AppliedConfigRevision, observed.AppliedConfigSHA256, observed.EndpointStatus = 2, 8, "observed-config", "pending"
	observed.TokenID, observed.NodeTokenCiphertext, observed.ConfigureTokenHash = "synthetic-token-reference", "synthetic-ciphertext", "synthetic-hash"
	if ServiceMetadataRevision(observed) != stamp {
		t.Fatal("monitoring or secret fields changed the metadata edit authority")
	}
	explicit := base
	explicit.DesiredEndpoint = &ServiceEndpoint{Host: base.Host, Port: base.Port, SSLEnabled: base.SSLEnabled, PublicURL: base.PublicURL}
	if ServiceMetadataRevision(explicit) != stamp {
		t.Fatal("implicit and explicit identical desired endpoints have different authority")
	}
	changes := []func(*RegisteredService){
		func(s *RegisteredService) { s.ServiceID += "-other" }, func(s *RegisteredService) { s.ServiceType = "encoder_recorder" },
		func(s *RegisteredService) { s.ServiceName += " edited" }, func(s *RegisteredService) { s.Description += " edited" },
		func(s *RegisteredService) { s.Host = "other.example.com" }, func(s *RegisteredService) { s.Port++ },
		func(s *RegisteredService) { s.SSLEnabled = false }, func(s *RegisteredService) { s.PublicURL += "/api" },
		func(s *RegisteredService) {
			s.DesiredEndpoint = &ServiceEndpoint{Host: "other.example.com", Port: 8084}
		},
		func(s *RegisteredService) { s.EndpointRevision++ }, func(s *RegisteredService) { s.TransportMode = "" },
		func(s *RegisteredService) { s.ExecutionHostID += "-other" }, func(s *RegisteredService) { s.OwnershipEpoch++ },
		func(s *RegisteredService) { s.CurrentStreamID = "other-stream" },
	}
	for index, change := range changes {
		changed := base
		change(&changed)
		if ServiceMetadataRevision(changed) == stamp {
			t.Fatalf("meaningful metadata authority field %d was omitted", index)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64), " " + stamp} {
		if ValidServiceMetadataRevision(invalid) {
			t.Fatal("invalid metadata revision format was accepted")
		}
	}
}

func TestServiceMetadataRevisionMemoryConcurrentEditors(t *testing.T) {
	auth := NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := auth.PrecreateService(t.Context(), token, ServiceRegistration{ServiceID: "worker-metadata-cas", ServiceType: "worker", ServiceName: "Original", Host: "worker.example.com", Port: 8084, SSLEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stamp := ServiceMetadataRevision(initial)
	if _, err := auth.Heartbeat(t.Context(), token, ServiceHeartbeat{ServiceID: initial.ServiceID, Status: "online", Metrics: map[string]any{"cpu_percent": 50.0}, API: &NodeAgentAPI{Host: "127.0.0.1", Port: 19084}}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, name := range []string{"Editor one", "Editor two"} {
		go func(name string) {
			ready.Done()
			<-start
			_, err := auth.UpdateServiceMetadata(t.Context(), initial.ServiceID, ServiceMetadataUpdate{ServiceName: name, Description: "One committed edit", PreserveEndpoint: true, ExpectedMetadataRevision: &stamp})
			outcomes <- err
		}(name)
	}
	ready.Wait()
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			wins++
		} else if errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal("unexpected metadata contender rejection")
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("same metadata baseline did not admit exactly one editor")
	}
	saved, err := auth.GetService(t.Context(), initial.ServiceID)
	if err != nil || saved.Description != "One committed edit" || saved.EndpointRevision != initial.EndpointRevision || saved.Metrics["cpu_percent"] != 50.0 {
		t.Fatal("metadata CAS changed endpoint or monitoring state")
	}
	current := ServiceMetadataRevision(saved)
	noop, err := auth.UpdateServiceMetadata(t.Context(), initial.ServiceID, ServiceMetadataUpdate{ServiceName: saved.ServiceName, Description: saved.Description, PreserveEndpoint: true, ExpectedMetadataRevision: &current})
	if err != nil || ServiceMetadataRevision(noop) != current || noop.EndpointRevision != saved.EndpointRevision {
		t.Fatal("metadata no-op changed semantic or endpoint revision")
	}
	legacy, err := auth.UpdateServiceMetadata(t.Context(), initial.ServiceID, ServiceMetadataUpdate{ServiceName: "Legacy explicit save", PreserveEndpoint: true})
	if err != nil || legacy.ServiceName != "Legacy explicit save" {
		t.Fatal("omitted metadata stamp legacy compatibility was removed")
	}
}

func TestServiceEndpointMetadataMemoryOwnershipAndCompatibility(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	policies, registry, updates, params := newMemoryPullActivationFixture(t, false)
	before, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	stamp := ServiceMetadataRevision(before)
	update := ServiceMetadataUpdate{ServiceName: before.ServiceName, Description: before.Description, Host: "worker-edited.example.com", Port: 18081, PublicURL: "https://worker-edited.example.com:18081", SSLEnabled: true, ExpectedMetadataRevision: &stamp}
	observed, err := registry.UpdateServiceMetadataWithEndpointOwnership(t.Context(), policies, updates, before.ServiceID, update)
	if err != nil || observed.Host != update.Host || observed.EndpointRevision != before.EndpointRevision+1 {
		t.Fatal("observe-only endpoint edit was not accepted through the coordinator")
	}
	active, err := policies.ActivatePullUpdaterOwnership(t.Context(), registry, updates, params)
	if err != nil {
		t.Fatal("real activation after public endpoint edit failed")
	}
	stamp = ServiceMetadataRevision(observed)
	update.Host, update.PublicURL = "another-worker.example.com", "https://another-worker.example.com:18081"
	if _, err := registry.UpdateServiceMetadataWithEndpointOwnership(t.Context(), policies, updates, before.ServiceID, update); !errors.Is(err, ErrServiceEndpointManagedByUpdater) {
		t.Fatal("active managed endpoint edit was accepted")
	}
	metadata, err := registry.UpdateServiceMetadata(t.Context(), before.ServiceID, ServiceMetadataUpdate{ServiceName: "Metadata while active", Description: observed.Description, PreserveEndpoint: true, ExpectedMetadataRevision: &stamp})
	if err != nil || metadata.EndpointRevision != observed.EndpointRevision {
		t.Fatal("active ownership disabled safe metadata-only edits")
	}
	deactivated, err := policies.DeactivatePullUpdaterOwnership(t.Context(), registry, updates, DeactivatePullUpdaterOwnershipParams{ServiceID: params.ServiceID, ExecutionHostID: params.ExecutionHostID, ExpectedExecutionHostOwnershipEpoch: active.Ownership.OwnershipEpoch, ExpectedSourcePolicyRevision: active.Policy.Revision, ExpectedProjectionRevision: active.Policy.ProjectionRevision, ExpectedLocalExecutorPolicyRevision: active.Policy.LocalExecutorPolicyRevision, ExpectedLocalExecutorPolicySHA256: active.Policy.LocalExecutorPolicySHA256})
	if err != nil || deactivated.Service.OwnershipEpoch != 0 || deactivated.Ownership.OwnershipEpoch <= 0 {
		t.Fatal("real deactivation did not retain observer-compatible ownership")
	}
	stamp = ServiceMetadataRevision(metadata)
	update.ServiceName = metadata.ServiceName
	if _, err := registry.UpdateServiceMetadataWithEndpointOwnership(t.Context(), policies, updates, before.ServiceID, update); err != nil {
		t.Fatal("positive host epoch incorrectly blocked a deactivated observer endpoint edit")
	}
	current, err := registry.GetService(t.Context(), before.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	stamp = ServiceMetadataRevision(current)
	update.Host, update.PublicURL = "unavailable.example.com", "https://unavailable.example.com:18081"
	if _, err := registry.UpdateServiceMetadataWithEndpointOwnership(t.Context(), policies, nil, before.ServiceID, update); !errors.Is(err, ErrServiceEndpointOwnershipUnavailable) {
		t.Fatal("unavailable authority did not fail closed")
	}
	after, err := registry.GetService(t.Context(), before.ServiceID)
	if err != nil || ServiceMetadataRevision(after) != stamp {
		t.Fatal("unavailable authority changed the service")
	}
}
