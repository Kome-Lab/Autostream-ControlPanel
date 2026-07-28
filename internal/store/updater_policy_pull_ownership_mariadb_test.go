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

func TestMariaDBSavePullUpdaterPolicyCASAndOwnershipRevision(t *testing.T) {
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
	hostID := "host-policy-" + suffix
	agentID := "agent-policy-" + suffix
	targetID := "worker-policy-" + suffix
	auth := store.NewMariaDBAuthStore(db)
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:       agentID,
		ServiceType:     "update_agent",
		ServiceName:     agentID,
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: hostID,
		OwnershipEpoch:  1,
	})
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:   targetID,
		ServiceType: "worker",
		ServiceName: targetID,
		PublicURL:   "https://worker.example.com:18081",
	})

	updates := store.NewMariaDBSystemUpdateStore(db)
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		0,
		store.SystemUpdateTransportPullV2,
		agentID,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policies := store.NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	input := store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           hostID,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       targetID,
			ServiceID:      targetID,
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}

	created, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, 0, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	assertMariaDBPullPolicyOwnership(t, ctx, policies, updates, agentID, hostID, 1, ownership.OwnershipEpoch)

	if _, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, 0, ownership.OwnershipEpoch, input); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale create error = %v, want ErrConflict", err)
	}
	assertMariaDBPullPolicyOwnership(t, ctx, policies, updates, agentID, hostID, 1, ownership.OwnershipEpoch)

	input.PollIntervalSeconds = 20
	updated, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, created.Revision, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.PollIntervalSeconds != 20 {
		t.Fatalf("updated policy = %#v", updated)
	}
	assertMariaDBPullPolicyOwnership(t, ctx, policies, updates, agentID, hostID, 2, ownership.OwnershipEpoch)

	job, createdJob, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
		TargetID:          targetID,
		TargetServiceType: "worker",
		AgentServiceID:    agentID,
		ExecutionHostID:   hostID,
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "pull-policy-busy-" + suffix,
		RequestedByUserID: "mariadb-policy-test",
	})
	if err != nil || !createdJob {
		t.Fatalf("create active job: job=%#v created=%v err=%v", job, createdJob, err)
	}
	input.PollIntervalSeconds = 25
	if _, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, updated.Revision, ownership.OwnershipEpoch, input); !errors.Is(err, store.ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("active-job save error = %v, want ErrSystemUpdateExecutionHostBusy", err)
	}
	assertMariaDBPullPolicyOwnership(t, ctx, policies, updates, agentID, hostID, 2, ownership.OwnershipEpoch)

	if _, err := updates.CancelSystemUpdateJob(ctx, job.ID, "mariadb-policy-test"); err != nil {
		t.Fatal(err)
	}
	afterCancel, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, updated.Revision, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	if afterCancel.Revision != 3 {
		t.Fatalf("post-cancel policy revision = %d", afterCancel.Revision)
	}
	assertMariaDBPullPolicyOwnership(t, ctx, policies, updates, agentID, hostID, 3, ownership.OwnershipEpoch)
}

func TestMariaDBSavePullUpdaterPolicyPreservesSSHObserverOwnership(t *testing.T) {
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
	hostID := "host-observer-" + suffix
	centralAgentID := "central-observer-" + suffix
	observerAgentID := "agent-observer-" + suffix
	targetID := "worker-observer-" + suffix
	auth := store.NewMariaDBAuthStore(db)
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:     centralAgentID,
		ServiceType:   "update_agent",
		ServiceName:   centralAgentID,
		TransportMode: store.SystemUpdateTransportSSHV1,
		PublicURL:     "https://updater.example.com:8090",
	})
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:       observerAgentID,
		ServiceType:     "update_agent",
		ServiceName:     observerAgentID,
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: hostID,
		OwnershipEpoch:  1,
	})
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:   targetID,
		ServiceType: "worker",
		ServiceName: targetID,
		PublicURL:   "https://worker.example.com:18081",
	})

	updates := store.NewMariaDBSystemUpdateStore(db)
	sshOwnership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		hostID,
		0,
		store.SystemUpdateTransportSSHV1,
		centralAgentID,
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	persistedSSHOwnership, err := updates.GetSystemUpdateExecutionHost(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
		TargetID:          targetID,
		TargetServiceType: "worker",
		AgentServiceID:    centralAgentID,
		ExecutionHostID:   hostID,
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "observer-active-" + suffix,
		RequestedByUserID: "mariadb-observer-test",
	}); err != nil || !created {
		t.Fatalf("create SSH-owned active job: created=%v err=%v", created, err)
	}

	policies := store.NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	input := store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           hostID,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("b", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       targetID,
			ServiceID:      targetID,
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	observerPolicy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		observerAgentID,
		0,
		sshOwnership.OwnershipEpoch,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observerPolicy.Revision != 1 {
		t.Fatalf("observer policy revision = %d", observerPolicy.Revision)
	}
	after, err := updates.GetSystemUpdateExecutionHost(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if after != persistedSSHOwnership {
		t.Fatalf("observer policy changed SSH ownership: before=%#v after=%#v", persistedSSHOwnership, after)
	}
}

func assertMariaDBPullPolicyOwnership(
	t *testing.T,
	ctx context.Context,
	policies store.MariaDBUpdaterPolicyStore,
	updates *store.MariaDBSystemUpdateStore,
	serviceID, hostID string,
	wantRevision, wantEpoch int64,
) {
	t.Helper()
	policy, err := policies.GetUpdaterPolicy(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != wantRevision ||
		ownership.PolicyRevision != wantRevision ||
		ownership.OwnershipEpoch != wantEpoch {
		t.Fatalf("policy/ownership = revision %d / %#v, want revision %d epoch %d", policy.Revision, ownership, wantRevision, wantEpoch)
	}
}
