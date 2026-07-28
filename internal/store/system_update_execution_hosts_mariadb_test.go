package store_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBExecutionHostOwnershipAndPortReservation(t *testing.T) {
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

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	hostID := "host-" + suffix
	firstAgentID := "updater-host-" + suffix
	secondAgentID := "updater-central-" + suffix
	wrongHostAgentID := "updater-wrong-host-" + suffix
	firstServiceID := "worker-a-" + suffix
	secondServiceID := "worker-b-" + suffix
	auth := store.NewMariaDBAuthStore(db)
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID: firstAgentID, ServiceType: "update_agent", ServiceName: firstAgentID,
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: hostID, OwnershipEpoch: 1,
	})
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID: secondAgentID, ServiceType: "update_agent", ServiceName: secondAgentID,
		TransportMode: store.SystemUpdateTransportSSHV1, PublicURL: "https://updater.example.com:8090",
	})
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID: wrongHostAgentID, ServiceType: "update_agent", ServiceName: wrongHostAgentID,
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "other-" + hostID, OwnershipEpoch: 2,
	})
	for _, serviceID := range []string{firstServiceID, secondServiceID} {
		registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
			ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID,
			PublicURL: "https://worker.example.com:8081",
		})
	}

	updates := store.NewMariaDBSystemUpdateStore(db)
	missing, err := updates.GetSystemUpdateExecutionHost(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.TransportMode != store.SystemUpdateTransportSSHV1 || missing.OwnershipEpoch != 0 {
		t.Fatalf("missing host = %#v", missing)
	}

	owned, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		0,
		store.SystemUpdateTransportPullV2,
		firstAgentID,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if owned.OwnershipEpoch != 1 || owned.AgentServiceID != firstAgentID {
		t.Fatalf("owned host = %#v", owned)
	}
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		0,
		store.SystemUpdateTransportSSHV1,
		secondAgentID,
		2,
	); !errors.Is(err, store.ErrSystemUpdateExecutionHostStale) {
		t.Fatalf("stale switch err = %v", err)
	}
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		owned.OwnershipEpoch,
		store.SystemUpdateTransportPullV2,
		wrongHostAgentID,
		2,
	); !errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch) {
		t.Fatalf("wrong registered host switch err = %v", err)
	}

	reservation := store.ServicePortReservation{
		ExecutionHostID:  hostID,
		NetworkNamespace: "host",
		Protocol:         "tcp",
		Port:             18080,
		ServiceID:        firstServiceID,
		ServiceRole:      "api",
	}
	stored, created, err := updates.ReserveServicePort(ctx, reservation)
	if err != nil || !created {
		t.Fatalf("reserve: reservation=%#v created=%v err=%v", stored, created, err)
	}
	if _, created, err := updates.ReserveServicePort(ctx, reservation); err != nil || created {
		t.Fatalf("idempotent reserve: created=%v err=%v", created, err)
	}
	conflict := reservation
	conflict.ServiceID = secondServiceID
	if _, _, err := updates.ReserveServicePort(ctx, conflict); !errors.Is(err, store.ErrServicePortReserved) {
		t.Fatalf("conflicting reserve err = %v", err)
	}
	list, err := updates.ListServicePortReservations(ctx, hostID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list reservations = %#v, err=%v", list, err)
	}

	job, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
		TargetID:          firstServiceID,
		TargetServiceType: "worker",
		AgentServiceID:    firstAgentID,
		ExecutionHostID:   hostID,
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "ownership-busy-" + suffix,
		RequestedByUserID: "mariadb-ownership-test",
	})
	if err != nil || !created {
		t.Fatalf("create busy job: created=%v err=%v", created, err)
	}
	if job.TransportMode != owned.TransportMode ||
		job.OwnershipEpoch != owned.OwnershipEpoch ||
		job.PolicyRevision != owned.PolicyRevision {
		t.Fatalf("job ownership snapshot = %#v, ownership=%#v", job, owned)
	}
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		owned.OwnershipEpoch,
		store.SystemUpdateTransportSSHV1,
		secondAgentID,
		2,
	); !errors.Is(err, store.ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("busy switch err = %v", err)
	}
	now := time.Now().UTC()
	claim, _, err := updates.ClaimSystemUpdateJob(
		ctx,
		firstAgentID,
		hostID,
		"",
		map[string]string{firstServiceID: "systemd"},
		now,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, store.SystemUpdateReport{
		AgentServiceID:  firstAgentID,
		ExecutionHostID: hostID,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Sequence:        claim.ReportSequence,
		Status:          store.SystemUpdateStatusInstalling,
		Progress:        70,
	}, now.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	binding := store.SystemUpdateMutationGrantBinding{
		HostID:         hostID,
		TransportMode:  owned.TransportMode,
		OwnershipEpoch: owned.OwnershipEpoch,
		PolicyRevision: owned.PolicyRevision,
		TargetID:       firstServiceID,
		TargetVersion:  "v1.1.0",
		DeploymentMode: "systemd",
		Operation:      store.SystemUpdateMutationOperationApply,
		PlanSHA256:     strings.Repeat("a", 64),
		SessionID:      "mariadb-ownership-" + suffix,
	}
	issued, err := updates.IssueSystemUpdateMutationGrant(ctx, job.ID, store.IssueSystemUpdateMutationGrantParams{
		AgentServiceID:  firstAgentID,
		ExecutionHostID: hostID,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Binding:         binding,
	}, now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET ownership_epoch = ownership_epoch + 1, policy_revision = policy_revision + 1, updated_at = ?
WHERE execution_host_id = ?`, now.Add(3*time.Second), hostID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(ctx, job.ID, store.SystemUpdateReport{
		AgentServiceID:  firstAgentID,
		ExecutionHostID: hostID,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Sequence:        claim.ReportSequence + 1,
		Status:          store.SystemUpdateStatusSucceeded,
		Progress:        100,
	}, now.Add(4*time.Second), time.Minute); !errors.Is(err, store.ErrSystemUpdateOwnershipConflict) {
		t.Fatalf("stale MariaDB report err = %v", err)
	}
	if _, _, err := updates.ConsumeSystemUpdateMutationGrant(
		ctx,
		job.ID,
		issued.GrantToken,
		claim.LeaseGeneration,
		binding,
		now.Add(4*time.Second),
	); !errors.Is(err, store.ErrSystemUpdateMutationGrantConflict) {
		t.Fatalf("stale MariaDB grant consume err = %v", err)
	}

	if err := updates.ReleaseServicePort(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if err := updates.ReleaseServicePort(ctx, reservation); err != nil {
		t.Fatalf("idempotent release err = %v", err)
	}
}

func registerMariaDBExecutionHostFixture(
	t *testing.T,
	ctx context.Context,
	auth store.ServiceRegistryStore,
	registration store.ServiceRegistration,
) {
	t.Helper()
	scopes := []string{"service.register", "service.heartbeat"}
	if registration.ServiceType == "update_agent" {
		scopes = append(scopes, "updates.claim", "updates.report", "updates.authorize")
	}
	token, err := auth.CreateServiceToken(ctx, registration.ServiceType, scopes)
	if err != nil {
		t.Fatal(err)
	}
	registration.Version = "v1.0.0"
	registration.Capabilities = map[string]any{}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
}
