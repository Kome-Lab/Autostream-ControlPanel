package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBActivatePullUpdaterOwnershipAndBaselineReservation(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	for _, test := range []struct {
		name     string
		sshOwner bool
	}{
		{name: "synthetic"},
		{name: "ssh_owner", sshOwner: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMariaDBPullActivationFixture(t, ctx, db, test.sshOwner)
			result, err := fixture.policies.ActivatePullUpdaterOwnership(
				ctx,
				fixture.auth,
				fixture.updates,
				fixture.params,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
				result.Ownership.OwnershipEpoch != fixture.params.ExpectedExecutionHostOwnershipEpoch+1 ||
				result.Ownership.PolicyRevision != fixture.params.ExpectedProjectionRevision ||
				result.Service.OwnershipEpoch != result.Ownership.OwnershipEpoch {
				t.Fatalf("activation result = %#v", result)
			}
			reservations, err := fixture.updates.ListServicePortReservations(ctx, fixture.params.ExecutionHostID)
			if err != nil {
				t.Fatal(err)
			}
			if len(reservations) != 1 ||
				reservations[0].NetworkNamespace != "host" ||
				reservations[0].Protocol != "tcp" ||
				reservations[0].Port != 18081 ||
				reservations[0].ServiceID != fixture.targetID ||
				reservations[0].ServiceRole != "api" {
				t.Fatalf("baseline reservations = %#v", reservations)
			}
		})
	}
}

func TestMariaDBActivatePullUpdaterOwnershipBackfillsLegacyConfigDigestWithoutChangingRevision(
	t *testing.T,
) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)

	var beforeRevision int64
	var beforeDigest sql.NullString
	if err := db.QueryRowContext(
		ctx,
		`SELECT applied_config_revision, applied_config_sha256
FROM services
WHERE service_id = ?`,
		fixture.targetID,
	).Scan(&beforeRevision, &beforeDigest); err != nil {
		t.Fatal(err)
	}
	if beforeRevision < 1 || beforeDigest.Valid {
		t.Fatalf(
			"legacy target config state = revision %d digest %#v",
			beforeRevision,
			beforeDigest,
		)
	}

	agent, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	reportedConfigDigests, ok := agent.ReportedCapabilities["reported_config_sha256"].(map[string]any)
	if !ok {
		t.Fatalf("reported config digests = %#v", agent.ReportedCapabilities["reported_config_sha256"])
	}
	reportedConfigDigest, ok := reportedConfigDigests[fixture.targetID].(string)
	if !ok || reportedConfigDigest != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("reported target config digest = %#v", reportedConfigDigests[fixture.targetID])
	}

	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}

	var afterRevision int64
	var afterDigest sql.NullString
	if err := db.QueryRowContext(
		ctx,
		`SELECT applied_config_revision, applied_config_sha256
FROM services
WHERE service_id = ?`,
		fixture.targetID,
	).Scan(&afterRevision, &afterDigest); err != nil {
		t.Fatal(err)
	}
	if afterRevision != beforeRevision ||
		!afterDigest.Valid ||
		afterDigest.String != reportedConfigDigest ||
		activated.Ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		activated.Ownership.AgentServiceID != fixture.params.ServiceID {
		t.Fatalf(
			"activation did not atomically backfill legacy target config: before_revision=%d after_revision=%d reported_digest=%q after_digest=%#v ownership=%#v",
			beforeRevision,
			afterRevision,
			reportedConfigDigest,
			afterDigest,
			activated.Ownership,
		)
	}
}

func TestMariaDBActivatePullUpdaterOwnershipDoesNotPersistSyntheticControlPanelReservation(
	t *testing.T,
) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Targets = append(policy.Targets, store.UpdaterPolicyTarget{
		TargetID:       "control-panel",
		ServiceID:      "control-panel",
		HostID:         fixture.params.ExecutionHostID,
		ServiceType:    "control_panel",
		DeploymentMode: "systemd",
		DatabaseName:   "autostream_panel",
	})
	policy, err = fixture.policies.SavePullUpdaterPolicy(
		ctx,
		fixture.updates,
		fixture.params.ServiceID,
		policy.Revision,
		fixture.params.ExpectedExecutionHostOwnershipEpoch,
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	controlPanelTarget := &store.PullUpdaterControlPanelTarget{
		ServiceID:             "control-panel",
		ServiceType:           "control_panel",
		EndpointRevision:      1,
		AppliedConfigRevision: 1,
		AppliedConfigSHA256:   "sha256:" + strings.Repeat("d", 64),
		AppliedEndpoint: store.ServiceEndpoint{
			Host:      "127.0.0.1",
			Port:      18080,
			PublicURL: "http://127.0.0.1:18080",
		},
	}
	agent, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := agent.ReportedCapabilities
	capabilities["policy_revision"] = policy.ProjectionRevision
	capabilities["policy_status"] = "applied"
	setTargetCapability := func(key string, value any) {
		t.Helper()
		values, ok := capabilities[key].(map[string]any)
		if !ok {
			t.Fatalf("capability %s = %#v", key, capabilities[key])
		}
		values["control-panel"] = value
	}
	setTargetCapability("target_availability", "available")
	setTargetCapability("target_availability_codes", "executor_verified")
	setTargetCapability("reported_ports", int64(18080))
	setTargetCapability("port_drift", false)
	setTargetCapability("reported_service_types", "control_panel")
	setTargetCapability("reported_deployment_modes", "systemd")
	for targetID := range capabilities["reported_executor_policy_revisions"].(map[string]any) {
		capabilities["reported_executor_policy_revisions"].(map[string]any)[targetID] =
			policy.LocalExecutorPolicyRevision
	}
	setTargetCapability(
		"reported_executor_policy_revisions",
		policy.LocalExecutorPolicyRevision,
	)
	setTargetCapability(
		"reported_executor_policy_sha256",
		policy.LocalExecutorPolicySHA256,
	)
	setTargetCapability("reported_config_revisions", int64(1))
	setTargetCapability(
		"reported_config_sha256",
		controlPanelTarget.AppliedConfigSHA256,
	)
	if _, err := fixture.auth.Heartbeat(ctx, fixture.agentToken, store.ServiceHeartbeat{
		ServiceID:    fixture.params.ServiceID,
		Status:       "online",
		Version:      "v1.0.0",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.params.ExpectedSourcePolicyRevision = policy.Revision
	fixture.params.ExpectedProjectionRevision = policy.ProjectionRevision
	fixture.params.ExpectedLocalExecutorPolicyRevision =
		policy.LocalExecutorPolicyRevision
	fixture.params.ExpectedLocalExecutorPolicySHA256 =
		policy.LocalExecutorPolicySHA256
	fixture.params.ControlPanelTarget = controlPanelTarget

	if _, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		fixture.params,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.auth.GetService(
		ctx,
		"control-panel",
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("synthetic Control Panel leaked into services: %v", err)
	}
	reservations, err := fixture.updates.ListServicePortReservations(
		ctx,
		fixture.params.ExecutionHostID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 ||
		reservations[0].ServiceID != fixture.targetID ||
		reservations[0].Port != 18081 {
		t.Fatalf("synthetic Control Panel leaked into reservations: %#v", reservations)
	}
	var controlPanelReservationCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
FROM service_port_reservations
WHERE execution_host_id = ? AND service_id = 'control-panel'`,
		fixture.params.ExecutionHostID,
	).Scan(&controlPanelReservationCount); err != nil {
		t.Fatal(err)
	}
	if controlPanelReservationCount != 0 {
		t.Fatalf(
			"synthetic Control Panel reservation rows = %d",
			controlPanelReservationCount,
		)
	}
}

func TestMariaDBActivatePullUpdaterOwnershipConcurrentCASAllowsOneWinner(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := fixture.policies.ActivatePullUpdaterOwnership(
				ctx,
				fixture.auth,
				fixture.updates,
				fixture.params,
			)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch),
			errors.Is(err, store.ErrSystemUpdateExecutionHostStale):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent activation error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestMariaDBActivatePullUpdaterOwnershipRollsBackOnBaselineInsertFailure(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
	beforeOwner, err := fixture.updates.GetSystemUpdateExecutionHost(ctx, fixture.params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	triggerName := "trg_activation_" + fixture.suffix
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER %s
BEFORE INSERT ON service_port_reservations
FOR EACH ROW
BEGIN
  IF NEW.service_id = '%s' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced activation reservation failure';
  END IF;
END`, triggerName, fixture.targetID)
	if _, err := db.ExecContext(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName)
	})

	if _, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		fixture.params,
	); err == nil {
		t.Fatal("forced baseline insert failure was accepted")
	}
	afterOwner, err := fixture.updates.GetSystemUpdateExecutionHost(ctx, fixture.params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	service, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := fixture.updates.ListServicePortReservations(ctx, fixture.params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if afterOwner != beforeOwner ||
		service.OwnershipEpoch != 0 ||
		target.AppliedConfigSHA256 != "" ||
		len(reservations) != 0 {
		t.Fatalf("failed activation partially committed: before=%#v after=%#v service=%#v target=%#v reservations=%#v",
			beforeOwner, afterOwner, service, target, reservations)
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipReturnsObserverAndPreservesRoundTrip(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
	legacyPolicy, err := fixture.policies.GetUpdaterPolicy(ctx, fixture.legacyID)
	if err != nil {
		t.Fatal(err)
	}
	legacyPolicy.PollIntervalSeconds++
	legacyPolicy, err = fixture.policies.SaveUpdaterPolicy(
		ctx,
		fixture.legacyID,
		legacyPolicy.Revision,
		legacyPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	if activated.Ownership.LegacyAgentServiceID != fixture.legacyID {
		t.Fatalf("activation legacy owner = %#v", activated.Ownership)
	}
	deactivated, err := fixture.policies.DeactivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		store.DeactivatePullUpdaterOwnershipParams{
			ServiceID:                           fixture.params.ServiceID,
			ExecutionHostID:                     fixture.params.ExecutionHostID,
			ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        fixture.params.ExpectedSourcePolicyRevision,
			ExpectedProjectionRevision:          fixture.params.ExpectedProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: fixture.params.ExpectedLocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   fixture.params.ExpectedLocalExecutorPolicySHA256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if deactivated.Ownership.TransportMode != store.SystemUpdateTransportSSHV1 ||
		deactivated.Ownership.AgentServiceID != fixture.legacyID ||
		deactivated.Ownership.LegacyAgentServiceID != fixture.legacyID ||
		deactivated.Ownership.OwnershipEpoch != activated.Ownership.OwnershipEpoch+1 ||
		deactivated.Ownership.PolicyRevision != legacyPolicy.ProjectionRevision ||
		deactivated.Service.OwnershipEpoch != 0 {
		t.Fatalf("deactivation result = %#v", deactivated)
	}

	agent, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := agent.ReportedCapabilities
	capabilities["observe_only"] = true
	capabilities["mutation_enabled"] = false
	capabilities["recovery_pending"] = false
	capabilities["ownership_epoch"] = int64(0)
	if _, err := fixture.auth.Heartbeat(ctx, fixture.agentToken, store.ServiceHeartbeat{
		ServiceID:    fixture.params.ServiceID,
		Status:       "online",
		Version:      "v1.0.1-observer",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.params.ExpectedExecutionHostOwnershipEpoch = deactivated.Ownership.OwnershipEpoch
	reactivated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Ownership.LegacyAgentServiceID != fixture.legacyID ||
		reactivated.Ownership.OwnershipEpoch != deactivated.Ownership.OwnershipEpoch+1 {
		t.Fatalf("reactivation did not preserve legacy owner = %#v", reactivated)
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipRejectsInactiveOrMissingLegacyOwner(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	t.Run("inactive", func(t *testing.T) {
		fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
		activated, err := fixture.policies.ActivatePullUpdaterOwnership(
			ctx,
			fixture.auth,
			fixture.updates,
			fixture.params,
		)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := fixture.auth.GetService(ctx, fixture.legacyID)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.auth.RevokeServiceToken(ctx, legacy.TokenID); err != nil {
			t.Fatal(err)
		}
		_, err = fixture.policies.DeactivatePullUpdaterOwnership(
			ctx,
			fixture.auth,
			fixture.updates,
			store.DeactivatePullUpdaterOwnershipParams{
				ServiceID:                           fixture.params.ServiceID,
				ExecutionHostID:                     fixture.params.ExecutionHostID,
				ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
				ExpectedSourcePolicyRevision:        fixture.params.ExpectedSourcePolicyRevision,
				ExpectedProjectionRevision:          fixture.params.ExpectedProjectionRevision,
				ExpectedLocalExecutorPolicyRevision: fixture.params.ExpectedLocalExecutorPolicyRevision,
				ExpectedLocalExecutorPolicySHA256:   fixture.params.ExpectedLocalExecutorPolicySHA256,
			},
		)
		if !errors.Is(err, store.ErrSystemUpdateAgentInactive) {
			t.Fatalf("inactive legacy owner error = %v", err)
		}
		after, getErr := fixture.updates.GetSystemUpdateExecutionHost(
			ctx,
			fixture.params.ExecutionHostID,
		)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if !sameMariaDBExecutionHostFence(after, activated.Ownership) {
			t.Fatalf("inactive legacy owner partially deactivated: before=%#v after=%#v", activated.Ownership, after)
		}
	})

	t.Run("missing", func(t *testing.T) {
		fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
		activated, err := fixture.policies.ActivatePullUpdaterOwnership(
			ctx,
			fixture.auth,
			fixture.updates,
			fixture.params,
		)
		if err != nil {
			t.Fatal(err)
		}
		if activated.Ownership.LegacyAgentServiceID != "" {
			t.Fatalf("new pull-only host resolved unexpected legacy owner: %#v", activated.Ownership)
		}
		_, err = fixture.policies.DeactivatePullUpdaterOwnership(
			ctx,
			fixture.auth,
			fixture.updates,
			store.DeactivatePullUpdaterOwnershipParams{
				ServiceID:                           fixture.params.ServiceID,
				ExecutionHostID:                     fixture.params.ExecutionHostID,
				ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
				ExpectedSourcePolicyRevision:        fixture.params.ExpectedSourcePolicyRevision,
				ExpectedProjectionRevision:          fixture.params.ExpectedProjectionRevision,
				ExpectedLocalExecutorPolicyRevision: fixture.params.ExpectedLocalExecutorPolicyRevision,
				ExpectedLocalExecutorPolicySHA256:   fixture.params.ExpectedLocalExecutorPolicySHA256,
			},
		)
		if !errors.Is(err, store.ErrSystemUpdateAgentBindingMismatch) {
			t.Fatalf("missing legacy owner error = %v", err)
		}
	})
}

func TestMariaDBDeactivatePullUpdaterOwnershipRejectsUnsafeLegacyRoute(
	t *testing.T,
) {
	db, ctx := openMariaDBPullActivationTest(t)
	for _, test := range []struct {
		name    string
		mutate  func(*testing.T, mariaDBPullActivationFixture)
		wantErr error
	}{
		{
			name: "missing required token scope",
			mutate: func(t *testing.T, fixture mariaDBPullActivationFixture) {
				legacy, err := fixture.auth.GetService(ctx, fixture.legacyID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(
					ctx,
					`UPDATE service_tokens SET scopes = ? WHERE id = ?`,
					`["updates.claim","updates.report"]`,
					legacy.TokenID,
				); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: store.ErrSystemUpdateAgentInactive,
		},
		{
			name: "incomplete pull target coverage",
			mutate: func(t *testing.T, fixture mariaDBPullActivationFixture) {
				legacyPolicy, err := fixture.policies.GetUpdaterPolicy(
					ctx,
					fixture.legacyID,
				)
				if err != nil {
					t.Fatal(err)
				}
				legacyPolicy.Targets[0].TargetID = "worker-other-" + fixture.suffix
				legacyPolicy.Targets[0].ServiceID = "worker-other-" + fixture.suffix
				if _, err := fixture.policies.SaveUpdaterPolicy(
					ctx,
					fixture.legacyID,
					legacyPolicy.Revision,
					legacyPolicy,
				); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: store.ErrSystemUpdateAgentBindingMismatch,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
			activated, err := fixture.policies.ActivatePullUpdaterOwnership(
				ctx,
				fixture.auth,
				fixture.updates,
				fixture.params,
			)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, fixture)
			_, err = fixture.policies.DeactivatePullUpdaterOwnership(
				ctx,
				fixture.auth,
				fixture.updates,
				store.DeactivatePullUpdaterOwnershipParams{
					ServiceID:                           fixture.params.ServiceID,
					ExecutionHostID:                     fixture.params.ExecutionHostID,
					ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
					ExpectedSourcePolicyRevision:        fixture.params.ExpectedSourcePolicyRevision,
					ExpectedProjectionRevision:          fixture.params.ExpectedProjectionRevision,
					ExpectedLocalExecutorPolicyRevision: fixture.params.ExpectedLocalExecutorPolicyRevision,
					ExpectedLocalExecutorPolicySHA256:   fixture.params.ExpectedLocalExecutorPolicySHA256,
				},
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("unsafe legacy route error = %v, want %v", err, test.wantErr)
			}
			after, getErr := fixture.updates.GetSystemUpdateExecutionHost(
				ctx,
				fixture.params.ExecutionHostID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if !sameMariaDBExecutionHostFence(after, activated.Ownership) {
				t.Fatalf(
					"unsafe legacy route rejection mutated owner: before=%#v after=%#v",
					activated.Ownership,
					after,
				)
			}
		})
	}
}

func sameMariaDBExecutionHostFence(
	left store.SystemUpdateExecutionHost,
	right store.SystemUpdateExecutionHost,
) bool {
	return left.ExecutionHostID == right.ExecutionHostID &&
		left.TransportMode == right.TransportMode &&
		left.AgentServiceID == right.AgentServiceID &&
		left.LegacyAgentServiceID == right.LegacyAgentServiceID &&
		left.OwnershipEpoch == right.OwnershipEpoch &&
		left.PolicyRevision == right.PolicyRevision
}

type mariaDBPullActivationFixture struct {
	auth       store.MariaDBAuthStore
	policies   store.MariaDBUpdaterPolicyStore
	updates    *store.MariaDBSystemUpdateStore
	params     store.ActivatePullUpdaterOwnershipParams
	agentToken store.ServiceToken
	targetID   string
	legacyID   string
	suffix     string
}

func newMariaDBPullActivationFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	sshOwner bool,
) mariaDBPullActivationFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	hostID := "host-activation-" + suffix
	agentID := "agent-activation-" + suffix
	targetID := "worker-activation-" + suffix
	auth := store.NewMariaDBAuthStore(db)
	registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
		ServiceID:   targetID,
		ServiceType: "worker",
		ServiceName: targetID,
		PublicURL:   "https://worker.example.com:18081",
	})
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{
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
	registration := store.ServiceRegistration{
		ServiceID:       agentID,
		ServiceType:     "update_agent",
		ServiceName:     agentID,
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: hostID,
		OwnershipEpoch:  0,
		Version:         "v1.0.0",
		Capabilities:    map[string]any{"observe_only": true},
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}

	updates := store.NewMariaDBSystemUpdateStore(db)
	policies := store.NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	expectedOwnerEpoch := int64(0)
	legacyID := ""
	if sshOwner {
		centralID := "central-activation-" + suffix
		legacyID = centralID
		registerMariaDBExecutionHostFixture(t, ctx, auth, store.ServiceRegistration{
			ServiceID:     centralID,
			ServiceType:   "update_agent",
			ServiceName:   centralID,
			TransportMode: store.SystemUpdateTransportSSHV1,
			PublicURL:     "https://updater.example.com:8090",
		})
		if _, err := policies.SaveUpdaterPolicy(
			ctx,
			centralID,
			0,
			store.UpdaterPolicy{
				API: store.UpdaterPolicyAPI{
					BindHost: "127.0.0.1",
					Host:     "127.0.0.1",
					Port:     8090,
				},
				PollIntervalSeconds:      15,
				HeartbeatIntervalSeconds: 30,
				Hosts: []store.UpdaterPolicyHost{{
					HostID:        hostID,
					Name:          hostID,
					Address:       "host-a.example.com",
					Port:          55850,
					User:          "autostream-update-host",
					Arch:          "amd64",
					HostPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g",
				}},
				Targets: []store.UpdaterPolicyTarget{{
					TargetID:       targetID,
					ServiceID:      targetID,
					HostID:         hostID,
					ServiceType:    "worker",
					DeploymentMode: "systemd",
				}},
			},
		); err != nil {
			t.Fatal(err)
		}
		owner, err := updates.SwitchSystemUpdateExecutionHost(
			ctx,
			hostID,
			0,
			store.SystemUpdateTransportSSHV1,
			centralID,
			7,
		)
		if err != nil {
			t.Fatal(err)
		}
		expectedOwnerEpoch = owner.OwnershipEpoch
	}
	policy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		agentID,
		0,
		expectedOwnerEpoch,
		store.UpdaterPolicy{
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
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           true,
		"update_executor":        true,
		"mutation_enabled":       false,
		"recovery_pending":       false,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      hostID,
		"ownership_epoch":        int64(0),
		"policy_revision":        policy.ProjectionRevision,
		"policy_status":          "applied",
		"target_availability": map[string]any{
			targetID: "available",
		},
		"target_availability_codes": map[string]any{
			targetID: "executor_verified",
		},
		"reported_ports": map[string]any{
			targetID: int64(18081),
		},
		"port_drift": map[string]any{
			targetID: false,
		},
		"reported_service_types": map[string]any{
			targetID: "worker",
		},
		"reported_deployment_modes": map[string]any{
			targetID: "systemd",
		},
		"reported_executor_policy_revisions": map[string]any{
			targetID: policy.LocalExecutorPolicyRevision,
		},
		"reported_executor_policy_sha256": map[string]any{
			targetID: policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			targetID: int64(1),
		},
		"reported_config_sha256": map[string]any{
			targetID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	}
	if _, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{
		ServiceID: agentID, Status: "online", Version: "v1.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBPullActivationFixture{
		auth: auth, policies: policies, updates: updates, agentToken: token,
		targetID: targetID, legacyID: legacyID, suffix: suffix,
		params: store.ActivatePullUpdaterOwnershipParams{
			ServiceID:                           agentID,
			ExecutionHostID:                     hostID,
			ExpectedExecutionHostOwnershipEpoch: expectedOwnerEpoch,
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
