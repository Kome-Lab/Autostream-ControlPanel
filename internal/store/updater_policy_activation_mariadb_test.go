package store_test

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBPullUpdaterOwnershipActivationAndObserveOnlyRoundTrip(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(ctx, fixture.auth, fixture.updates, fixture.params)
	if err != nil {
		t.Fatal(err)
	}
	if activated.Ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		activated.Ownership.AgentServiceID != fixture.params.ServiceID ||
		activated.Ownership.OwnershipEpoch != 1 ||
		activated.Service.OwnershipEpoch != activated.Ownership.OwnershipEpoch {
		t.Fatalf("activation result = %#v", activated)
	}
	deactivated, err := fixture.policies.DeactivatePullUpdaterOwnership(ctx, fixture.auth, fixture.updates, store.DeactivatePullUpdaterOwnershipParams{
		ServiceID:                           activated.Service.ServiceID,
		ExecutionHostID:                     activated.Ownership.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activated.Policy.LocalExecutorPolicySHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deactivated.Ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		deactivated.Ownership.AgentServiceID != fixture.params.ServiceID ||
		deactivated.Ownership.OwnershipEpoch != activated.Ownership.OwnershipEpoch+1 ||
		deactivated.Service.OwnershipEpoch != 0 {
		t.Fatalf("observe-only result = %#v", deactivated)
	}
}

type mariaDBPullActivationFixture struct {
	auth       store.MariaDBAuthStore
	policies   store.MariaDBUpdaterPolicyStore
	updates    *store.MariaDBSystemUpdateStore
	params     store.ActivatePullUpdaterOwnershipParams
	agentToken store.ServiceToken
	targetID   string
	suffix     string
}

func newMariaDBPullActivationFixture(t *testing.T, ctx context.Context, db *sql.DB, _ bool) mariaDBPullActivationFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	hostID := "host-activation-" + suffix
	agentID := "agent-activation-" + suffix
	targetID := "worker-activation-" + suffix
	auth := store.NewMariaDBAuthStore(db)
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID: targetID, ServiceType: "worker", ServiceName: targetID,
		PublicURL: "https://worker.example.com:18081",
	})
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{
		"service.register", "service.heartbeat", "service.config.read",
		"updates.claim", "updates.report", "updates.mutation_grant.issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{
		ServiceID: agentID, ServiceType: "update_agent", ServiceName: agentID,
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: hostID, OwnershipEpoch: 0, Version: "v2.0.0",
		Capabilities: map[string]any{"observe_only": true},
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMariaDBSystemUpdateStore(db)
	policies := store.NewMariaDBUpdaterPolicyAdminStore(db, "")
	policy, err := policies.SavePullUpdaterPolicy(ctx, updates, agentID, 0, 0, store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           hostID,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15, HeartbeatIntervalSeconds: 30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID: targetID, ServiceID: targetID,
			ServiceType: "worker", DeploymentMode: "systemd",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent": true, "observe_only": true, "update_executor": true,
		"mutation_enabled": false, "recovery_pending": false,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2", "execution_host_id": hostID,
		"ownership_epoch": int64(0), "policy_revision": policy.ProjectionRevision,
		"policy_status":                      "applied",
		"target_availability":                map[string]any{targetID: "available"},
		"target_availability_codes":          map[string]any{targetID: "executor_verified"},
		"reported_ports":                     map[string]any{targetID: int64(18081)},
		"port_drift":                         map[string]any{targetID: false},
		"reported_service_types":             map[string]any{targetID: "worker"},
		"reported_deployment_modes":          map[string]any{targetID: "systemd"},
		"reported_executor_policy_revisions": map[string]any{targetID: policy.LocalExecutorPolicyRevision},
		"reported_executor_policy_sha256":    map[string]any{targetID: policy.LocalExecutorPolicySHA256},
		"reported_config_revisions":          map[string]any{targetID: int64(1)},
		"reported_config_sha256":             map[string]any{targetID: "sha256:" + strings.Repeat("c", 64)},
	}
	if _, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{
		ServiceID: agentID, Status: "online", Version: "v2.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBPullActivationFixture{
		auth: auth, policies: policies, updates: updates, agentToken: token,
		targetID: targetID, suffix: suffix,
		params: store.ActivatePullUpdaterOwnershipParams{
			ServiceID: agentID, ExecutionHostID: hostID,
			ExpectedExecutionHostOwnershipEpoch: 0,
			ExpectedSourcePolicyRevision:        policy.Revision,
			ExpectedProjectionRevision:          policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		},
	}
}

func openMariaDBPullActivationTest(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}
