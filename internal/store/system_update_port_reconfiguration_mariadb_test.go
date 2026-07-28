package store_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBSystemdPortReconfigurationCreateAndCancelTransaction(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	markMariaDBPullAgentMutationReady(t, db, fixture, activated, target)
	params := store.CreateSystemdPortReconfigurationJobParams{
		TargetID: fixture.targetID, NewPort: 18084,
		ExpectedEndpointRevision: target.EndpointRevision,
		IdempotencyKey:           "mariadb-port-cancel-" + fixture.suffix,
		RequestedByUserID:        "mariadb-admin",
		RequestedByUsername:      "MariaDB Admin",
	}
	job, created, err := fixture.updates.CreateSystemdPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies, params,
	)
	if err != nil || !created || job.PortReconfigure == nil {
		t.Fatalf("create = %#v created=%v err=%v", job, created, err)
	}
	replayed, created, err := fixture.updates.CreateSystemdPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies, params,
	)
	if err != nil || created || replayed.ID != job.ID {
		t.Fatalf("replay = %#v created=%v err=%v", replayed, created, err)
	}
	pending, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.AppliedEndpoint.Port != 18081 ||
		pending.DesiredEndpoint.Port != 18084 ||
		pending.EndpointRevision != target.EndpointRevision+1 ||
		pending.EndpointStatus != "pending" {
		t.Fatalf("pending service = %#v", pending)
	}
	reservations, err := fixture.updates.ListServicePortReservations(ctx, activated.Ownership.ExecutionHostID)
	if err != nil || len(reservations) != 2 {
		t.Fatalf("pending reservations = %#v err=%v", reservations, err)
	}

	canceled, err := fixture.updates.CancelSystemUpdateJob(ctx, job.ID, "mariadb-admin")
	if err != nil || canceled.Status != store.SystemUpdateStatusCancelled {
		t.Fatalf("cancel = %#v err=%v", canceled, err)
	}
	rolledBack, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.AppliedEndpoint.Port != 18081 ||
		rolledBack.DesiredEndpoint.Port != 18081 ||
		rolledBack.EndpointRevision != target.EndpointRevision+2 ||
		rolledBack.EndpointStatus != "applied" {
		t.Fatalf("rolled-back service = %#v", rolledBack)
	}
	reservations, err = fixture.updates.ListServicePortReservations(ctx, activated.Ownership.ExecutionHostID)
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != 18081 ||
		reservations[0].ServiceRole != "api" {
		t.Fatalf("rolled-back reservations = %#v err=%v", reservations, err)
	}
}

func TestMariaDBSystemdPortReconfigurationTerminalCommit(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	markMariaDBPullAgentMutationReady(t, db, fixture, activated, target)
	job, _, err := fixture.updates.CreateSystemdPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies,
		store.CreateSystemdPortReconfigurationJobParams{
			TargetID: fixture.targetID, NewPort: 18084,
			ExpectedEndpointRevision: target.EndpointRevision,
			IdempotencyKey:           "mariadb-port-applied-" + fixture.suffix,
			RequestedByUserID:        "mariadb-admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	claim, _, err := fixture.updates.ClaimSystemUpdateJob(
		ctx, fixture.params.ServiceID, fixture.params.ExecutionHostID, "",
		map[string]string{fixture.targetID: "systemd"}, base, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	report := store.SystemUpdateReport{
		AgentServiceID:  fixture.params.ServiceID,
		ExecutionHostID: fixture.params.ExecutionHostID,
		LeaseToken:      claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Sequence: claim.ReportSequence, Status: store.SystemUpdateStatusSucceeded,
		Progress: 100,
		PortReconfigure: &store.SystemUpdatePortReconfiguration{
			Result: store.SystemUpdatePortReconfigurationApplied,
		},
	}
	terminal, applied, err := fixture.updates.ReportSystemUpdateJob(
		ctx, job.ID, report, base.Add(time.Second), time.Minute,
	)
	if err != nil || !applied ||
		terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != store.SystemUpdatePortReconfigurationApplied {
		t.Fatalf("terminal = %#v applied=%v err=%v", terminal, applied, err)
	}
	stored, err := fixture.updates.GetSystemUpdateJobByIdempotency(
		ctx, "mariadb-admin", job.IdempotencyKey,
	)
	if err != nil || stored.PortReconfigure == nil ||
		stored.PortReconfigure.Result != store.SystemUpdatePortReconfigurationApplied {
		t.Fatalf("stored terminal = %#v err=%v", stored, err)
	}
	service, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if service.AppliedEndpoint.Port != 18084 ||
		service.DesiredEndpoint.Port != 18084 ||
		service.EndpointRevision != job.PortReconfigure.TargetEndpointRevision ||
		service.AppliedConfigRevision != job.PortReconfigure.TargetConfigRevision ||
		service.AppliedConfigSHA256 != job.PortReconfigure.TargetConfigSHA256 ||
		service.EndpointStatus != "applied" {
		t.Fatalf("applied service = %#v", service)
	}
	reservations, err := fixture.updates.ListServicePortReservations(
		ctx, activated.Ownership.ExecutionHostID,
	)
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != 18084 ||
		reservations[0].ServiceRole != "api" {
		t.Fatalf("applied reservations = %#v err=%v", reservations, err)
	}
}

func TestMariaDBSystemdPortReconfigurationTerminalUnchangedKeepsPreviousPort(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	markMariaDBPullAgentMutationReady(t, db, fixture, activated, target)
	job, _, err := fixture.updates.CreateSystemdPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies,
		store.CreateSystemdPortReconfigurationJobParams{
			TargetID: fixture.targetID, NewPort: 18084,
			ExpectedEndpointRevision: target.EndpointRevision,
			IdempotencyKey:           "mariadb-port-unchanged-" + fixture.suffix,
			RequestedByUserID:        "mariadb-admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	claim, _, err := fixture.updates.ClaimSystemUpdateJob(
		ctx, fixture.params.ServiceID, fixture.params.ExecutionHostID, "",
		map[string]string{fixture.targetID: "systemd"}, base, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	report := store.SystemUpdateReport{
		AgentServiceID:  fixture.params.ServiceID,
		ExecutionHostID: fixture.params.ExecutionHostID,
		LeaseToken:      claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Sequence: claim.ReportSequence, Status: store.SystemUpdateStatusSucceeded,
		Progress: 100,
		PortReconfigure: &store.SystemUpdatePortReconfiguration{
			Result: store.SystemUpdatePortReconfigurationUnchanged,
		},
	}
	terminal, applied, err := fixture.updates.ReportSystemUpdateJob(
		ctx, job.ID, report, base.Add(time.Second), time.Minute,
	)
	if err != nil || !applied ||
		terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != store.SystemUpdatePortReconfigurationUnchanged {
		t.Fatalf("terminal = %#v applied=%v err=%v", terminal, applied, err)
	}
	service, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if service.AppliedEndpoint.Port != target.AppliedEndpoint.Port ||
		service.DesiredEndpoint.Port != target.AppliedEndpoint.Port ||
		service.EndpointRevision != job.PortReconfigure.TargetEndpointRevision+1 ||
		service.AppliedConfigRevision != job.PortReconfigure.ExpectedConfigRevision ||
		service.AppliedConfigSHA256 != job.PortReconfigure.ExpectedConfigSHA256 ||
		service.EndpointStatus != "applied" {
		t.Fatalf("unchanged service = %#v", service)
	}
	reservations, err := fixture.updates.ListServicePortReservations(
		ctx, activated.Ownership.ExecutionHostID,
	)
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != target.AppliedEndpoint.Port ||
		reservations[0].ServiceRole != "api" {
		t.Fatalf("unchanged reservations = %#v err=%v", reservations, err)
	}
}

func markMariaDBPullAgentMutationReady(
	t *testing.T,
	db *sql.DB,
	fixture mariaDBPullActivationFixture,
	activated store.ActivatePullUpdaterOwnershipResult,
	target store.RegisteredService,
) {
	t.Helper()
	body := markMariaDBPullAgentMutationReadySQL(t, fixture, activated, target)
	now := time.Now().UTC()
	result, err := db.Exec(`UPDATE services
SET status = 'online', last_heartbeat_at = ?, reported_capabilities = ?, updated_at = ?
WHERE service_id = ? AND ownership_epoch = ?`,
		now, body, now, fixture.params.ServiceID, activated.Ownership.OwnershipEpoch,
	)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("mark pull agent ready affected %d rows", affected)
	}
}

func markMariaDBPullAgentMutationReadySQL(
	t *testing.T,
	fixture mariaDBPullActivationFixture,
	activated store.ActivatePullUpdaterOwnershipResult,
	target store.RegisteredService,
) []byte {
	t.Helper()
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           false,
		"update_executor":        true,
		"mutation_enabled":       true,
		"recovery_pending":       false,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      fixture.params.ExecutionHostID,
		"ownership_epoch":        activated.Ownership.OwnershipEpoch,
		"policy_revision":        activated.Policy.ProjectionRevision,
		"policy_status":          "applied",
		"target_availability": map[string]any{
			fixture.targetID: "available",
		},
		"target_availability_codes": map[string]any{
			fixture.targetID: "executor_verified",
		},
		"reported_ports": map[string]any{
			fixture.targetID: int64(target.AppliedEndpoint.Port),
		},
		"port_drift": map[string]any{
			fixture.targetID: false,
		},
		"reported_service_types": map[string]any{
			fixture.targetID: target.ServiceType,
		},
		"reported_deployment_modes": map[string]any{
			fixture.targetID: "systemd",
		},
		"reported_executor_policy_revisions": map[string]any{
			fixture.targetID: activated.Policy.LocalExecutorPolicyRevision,
		},
		"reported_executor_policy_sha256": map[string]any{
			fixture.targetID: activated.Policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			fixture.targetID: target.AppliedConfigRevision,
		},
		"reported_config_sha256": map[string]any{
			fixture.targetID: target.AppliedConfigSHA256,
		},
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
