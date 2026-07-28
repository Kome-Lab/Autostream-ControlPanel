package store_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBServiceEndpointStateAndEndpointlessPullAgent(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	workerID := "worker-endpoint-" + suffix
	workerToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	workerRegistration := store.ServiceRegistration{
		ServiceID:   workerID,
		ServiceType: "worker",
		ServiceName: "Worker endpoint state",
		Host:        "worker.example.com",
		Port:        8084,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:8084",
	}
	if _, err := auth.PrecreateService(ctx, workerToken, workerRegistration); err != nil {
		t.Fatalf("precreate worker: %v", err)
	}
	worker, err := auth.Heartbeat(ctx, workerToken, store.ServiceHeartbeat{
		ServiceID: workerID,
		Status:    "online",
		API:       &store.NodeAgentAPI{Host: "127.0.0.1", Port: 19084},
	})
	if err != nil {
		t.Fatalf("worker heartbeat: %v", err)
	}
	if worker.Host != "worker.example.com" || worker.Port != 8084 || worker.PublicURL != "https://worker.example.com:8084" {
		t.Fatalf("heartbeat changed MariaDB applied endpoint: %#v", worker)
	}
	if worker.DesiredEndpoint == nil || worker.AppliedEndpoint == nil || worker.ReportedEndpoint == nil {
		t.Fatalf("MariaDB endpoint state is incomplete: %#v", worker)
	}
	if worker.ReportedEndpoint.Host != "127.0.0.1" || worker.ReportedEndpoint.Port != 19084 {
		t.Fatalf("MariaDB reported endpoint mismatch: %#v", worker.ReportedEndpoint)
	}
	beforeMetadata := worker
	worker, err = auth.UpdateServiceMetadata(ctx, workerID, store.ServiceMetadataUpdate{
		ServiceName:      "Worker endpoint state renamed",
		Description:      "metadata-only update",
		PreserveEndpoint: true,
	})
	if err != nil {
		t.Fatalf("metadata-only worker update: %v", err)
	}
	if worker.ServiceName != "Worker endpoint state renamed" ||
		worker.Description != "metadata-only update" {
		t.Fatalf("MariaDB metadata-only update was not applied: %#v", worker)
	}
	if worker.EndpointRevision != beforeMetadata.EndpointRevision ||
		worker.EndpointStatus != beforeMetadata.EndpointStatus ||
		worker.AppliedConfigRevision != beforeMetadata.AppliedConfigRevision ||
		worker.AppliedConfigSHA256 != beforeMetadata.AppliedConfigSHA256 ||
		!sameEndpointForMariaDBTest(worker.AppliedEndpoint, beforeMetadata.AppliedEndpoint) ||
		!sameEndpointForMariaDBTest(worker.DesiredEndpoint, beforeMetadata.DesiredEndpoint) ||
		!sameEndpointForMariaDBTest(worker.ReportedEndpoint, beforeMetadata.ReportedEndpoint) {
		t.Fatalf(
			"MariaDB metadata-only update changed endpoint state:\nbefore=%#v\nafter=%#v",
			beforeMetadata,
			worker,
		)
	}

	agentID := "host-agent-" + suffix
	hostID := "host-" + suffix
	agentToken, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	agentRegistration := store.ServiceRegistration{
		ServiceID:       agentID,
		ServiceType:     "update_agent",
		ServiceName:     "Host Pull Agent",
		TransportMode:   "pull_v2",
		ExecutionHostID: hostID,
		OwnershipEpoch:  3,
	}
	if _, err := auth.PrecreateService(ctx, agentToken, agentRegistration); err != nil {
		t.Fatalf("precreate pull agent: %v", err)
	}
	agent, err := auth.RegisterService(ctx, agentToken, store.ServiceRegistration{
		ServiceID:   agentID,
		ServiceType: "update_agent",
		ServiceName: "Host Pull Agent",
	})
	if err != nil {
		t.Fatalf("register pull agent: %v", err)
	}
	if agent.TransportMode != "pull_v2" || agent.ExecutionHostID != hostID || agent.OwnershipEpoch != 3 {
		t.Fatalf("MariaDB pull agent changed server-owned host binding: %#v", agent)
	}
	if agent.PublicURL != "" || agent.AppliedEndpoint != nil || agent.DesiredEndpoint != nil || agent.ReportedEndpoint != nil {
		t.Fatalf("MariaDB pull agent unexpectedly has an inbound endpoint: %#v", agent)
	}
}

func sameEndpointForMariaDBTest(left, right *store.ServiceEndpoint) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
