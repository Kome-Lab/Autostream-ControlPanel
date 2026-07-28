package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBDockerPortReconfigurationIndependentChangesAndReservations(t *testing.T) {
	_, ctx, fixture, activated := readyMariaDBDockerPortCoordinator(t)
	tests := []struct {
		name              string
		newAdvertisedPort int
		newPublishedPort  int
		newContainerPort  int
		wantReservations  int
	}{
		{
			name: "advertised-only", newAdvertisedPort: 443,
			newPublishedPort: 18081, newContainerPort: 8080,
			wantReservations: 1,
		},
		{
			name: "published-only", newAdvertisedPort: 18081,
			newPublishedPort: 18084, newContainerPort: 8080,
			wantReservations: 2,
		},
		{
			name: "container-only", newAdvertisedPort: 18081,
			newPublishedPort: 18081, newContainerPort: 18080,
			wantReservations: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := fixture.auth.GetService(ctx, fixture.targetID)
			if err != nil {
				t.Fatal(err)
			}
			params := store.CreateDockerPortReconfigurationJobParams{
				TargetID:                 fixture.targetID,
				NewAdvertisedPort:        test.newAdvertisedPort,
				NewPublishedPort:         test.newPublishedPort,
				NewContainerPort:         test.newContainerPort,
				ExpectedEndpointRevision: target.EndpointRevision,
				IdempotencyKey:           "mariadb-docker-" + test.name + "-" + fixture.suffix,
				RequestedByUserID:        "mariadb-admin",
				RequestedByUsername:      "MariaDB Admin",
			}
			job, created, err := fixture.updates.CreateDockerPortReconfigurationJob(
				ctx, fixture.auth, fixture.policies, params,
			)
			if err != nil || !created || job.PortReconfigure == nil ||
				job.PortReconfigure.Docker == nil {
				t.Fatalf("create=%#v created=%v err=%v", job, created, err)
			}
			replayed, replayCreated, err := fixture.updates.CreateDockerPortReconfigurationJob(
				ctx, fixture.auth, fixture.policies, params,
			)
			if err != nil || replayCreated || replayed.ID != job.ID {
				t.Fatalf("replay=%#v created=%v err=%v", replayed, replayCreated, err)
			}
			reservations, err := fixture.updates.ListServicePortReservations(
				ctx, activated.Ownership.ExecutionHostID,
			)
			if err != nil || len(reservations) != test.wantReservations {
				t.Fatalf("reservations=%#v err=%v", reservations, err)
			}
			if reservations[0].Port != 18081 || reservations[0].ServiceRole != "api" {
				t.Fatalf("current reservation=%#v", reservations)
			}
			canceled, err := fixture.updates.CancelSystemUpdateJob(
				ctx, job.ID, "mariadb-admin",
			)
			if err != nil || canceled.Status != store.SystemUpdateStatusCancelled {
				t.Fatalf("cancel=%#v err=%v", canceled, err)
			}
			reservations, err = fixture.updates.ListServicePortReservations(
				ctx, activated.Ownership.ExecutionHostID,
			)
			if err != nil || len(reservations) != 1 ||
				reservations[0].Port != 18081 ||
				reservations[0].ServiceRole != "api" {
				t.Fatalf("reservations after cancel=%#v err=%v", reservations, err)
			}
		})
	}

	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	collision := store.ServicePortReservation{
		ExecutionHostID:  activated.Ownership.ExecutionHostID,
		NetworkNamespace: "host", Protocol: "tcp", Port: 19000,
		ServiceID: fixture.targetID, ServiceRole: "test-collision",
	}
	if _, created, err := fixture.updates.ReserveServicePort(ctx, collision); err != nil || !created {
		t.Fatalf("collision fixture created=%v err=%v", created, err)
	}
	_, _, err = fixture.updates.CreateDockerPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies,
		store.CreateDockerPortReconfigurationJobParams{
			TargetID: fixture.targetID, NewAdvertisedPort: 18081,
			NewPublishedPort: 19000, NewContainerPort: 8080,
			ExpectedEndpointRevision: target.EndpointRevision,
			IdempotencyKey:           "mariadb-docker-collision-" + fixture.suffix,
			RequestedByUserID:        "mariadb-admin",
		},
	)
	if !errors.Is(err, store.ErrServicePortReserved) {
		t.Fatalf("published-port collision result=%v", err)
	}
	if err := fixture.updates.ReleaseServicePort(ctx, collision); err != nil {
		t.Fatal(err)
	}
}

func TestMariaDBDockerPortReconfigurationSamePublishedTerminalCommitAndRollback(t *testing.T) {
	_, ctx, fixture, activated := readyMariaDBDockerPortCoordinator(t)
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	rolledBackJob, _, err := fixture.updates.CreateDockerPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies,
		store.CreateDockerPortReconfigurationJobParams{
			TargetID: fixture.targetID, NewAdvertisedPort: 18081,
			NewPublishedPort: 18081, NewContainerPort: 18080,
			ExpectedEndpointRevision: target.EndpointRevision,
			IdempotencyKey:           "mariadb-docker-rollback-" + fixture.suffix,
			RequestedByUserID:        "mariadb-admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reportMariaDBDockerPortTerminal(
		t, ctx, fixture, rolledBackJob,
		store.SystemUpdateStatusRolledBack,
		store.SystemUpdatePortReconfigurationRolledBack,
	)
	rolledBack, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.AppliedEndpoint.Port != 18081 ||
		rolledBack.AppliedConfigRevision != 3 ||
		rolledBack.EndpointStatus != "rolled_back" {
		t.Fatalf("rolled-back service=%#v", rolledBack)
	}
	assertSingleMariaDBDockerCurrentReservation(t, ctx, fixture, activated)

	appliedJob, _, err := fixture.updates.CreateDockerPortReconfigurationJob(
		ctx, fixture.auth, fixture.policies,
		store.CreateDockerPortReconfigurationJobParams{
			TargetID: fixture.targetID, NewAdvertisedPort: 18081,
			NewPublishedPort: 18081, NewContainerPort: 18080,
			ExpectedEndpointRevision: rolledBack.EndpointRevision,
			IdempotencyKey:           "mariadb-docker-applied-" + fixture.suffix,
			RequestedByUserID:        "mariadb-admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reportMariaDBDockerPortTerminal(
		t, ctx, fixture, appliedJob,
		store.SystemUpdateStatusSucceeded,
		store.SystemUpdatePortReconfigurationApplied,
	)
	applied, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	if applied.AppliedEndpoint.Port != 18081 ||
		applied.AppliedConfigRevision != 4 ||
		applied.AppliedConfigSHA256 != appliedJob.PortReconfigure.TargetConfigSHA256 ||
		applied.EndpointStatus != "applied" {
		t.Fatalf("applied service=%#v", applied)
	}
	assertSingleMariaDBDockerCurrentReservation(t, ctx, fixture, activated)
}

func readyMariaDBDockerPortCoordinator(
	t *testing.T,
) (*sql.DB, context.Context, mariaDBPullActivationFixture, store.ActivatePullUpdaterOwnershipResult) {
	t.Helper()
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range policy.Targets {
		if policy.Targets[index].ServiceID == fixture.targetID {
			policy.Targets[index].DeploymentMode = "docker"
		}
	}
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
	fixture.params.ExpectedSourcePolicyRevision = policy.Revision
	fixture.params.ExpectedProjectionRevision = policy.ProjectionRevision
	fixture.params.ExpectedLocalExecutorPolicyRevision = policy.LocalExecutorPolicyRevision
	fixture.params.ExpectedLocalExecutorPolicySHA256 = policy.LocalExecutorPolicySHA256
	configSHA256 := dockerPortEnvSHA256ForMariaDBTest("worker", 18081, 8080, 3)
	result, err := db.ExecContext(ctx, `UPDATE services
SET applied_config_revision = 3,
    applied_config_sha256 = ?,
    endpoint_status = 'applied',
    desired_host = host,
    desired_port = port,
    desired_ssl_enabled = ssl_enabled,
    desired_public_url = public_url
WHERE service_id = ?`,
		configSHA256,
		fixture.targetID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("prepare Docker target affected=%d err=%v", affected, err)
	}
	target, err := fixture.auth.GetService(ctx, fixture.targetID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := dockerPortAgentCapabilitiesForMariaDBTest(
		fixture,
		policy,
		target,
		0,
		true,
	)
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result, err = db.ExecContext(ctx, `UPDATE services
SET status = 'online', last_heartbeat_at = ?, reported_capabilities = ?, updated_at = ?
WHERE service_id = ? AND ownership_epoch = 0`,
		now, body, now, fixture.params.ServiceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("prepare Docker observer affected=%d err=%v", affected, err)
	}
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities = dockerPortAgentCapabilitiesForMariaDBTest(
		fixture,
		activated.Policy,
		target,
		activated.Ownership.OwnershipEpoch,
		false,
	)
	body, err = json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	result, err = db.ExecContext(ctx, `UPDATE services
SET status = 'online', last_heartbeat_at = ?, reported_capabilities = ?, updated_at = ?
WHERE service_id = ? AND ownership_epoch = ?`,
		now, body, now, fixture.params.ServiceID, activated.Ownership.OwnershipEpoch,
	)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("prepare Docker agent affected=%d err=%v", affected, err)
	}
	return db, ctx, fixture, activated
}

func dockerPortAgentCapabilitiesForMariaDBTest(
	fixture mariaDBPullActivationFixture,
	policy store.UpdaterPolicy,
	target store.RegisteredService,
	ownershipEpoch int64,
	observeOnly bool,
) map[string]any {
	return map[string]any{
		"host_agent":             true,
		"observe_only":           observeOnly,
		"update_executor":        true,
		"mutation_enabled":       !observeOnly,
		"recovery_pending":       false,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      fixture.params.ExecutionHostID,
		"ownership_epoch":        ownershipEpoch,
		"policy_revision":        policy.ProjectionRevision,
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
			fixture.targetID: "docker",
		},
		"reported_executor_policy_revisions": map[string]any{
			fixture.targetID: policy.LocalExecutorPolicyRevision,
		},
		"reported_executor_policy_sha256": map[string]any{
			fixture.targetID: policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			fixture.targetID: target.AppliedConfigRevision,
		},
		"reported_config_sha256": map[string]any{
			fixture.targetID: target.AppliedConfigSHA256,
		},
		"reported_docker_port_capabilities": map[string]any{
			fixture.targetID: "v1",
		},
		"reported_docker_published_ports": map[string]any{
			fixture.targetID: int64(18081),
		},
		"reported_docker_container_ports": map[string]any{
			fixture.targetID: int64(8080),
		},
		"reported_docker_health_ports": map[string]any{
			fixture.targetID: int64(18081),
		},
		"reported_docker_compose_sha256": map[string]any{
			fixture.targetID: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
		"reported_docker_compose_revisions": map[string]any{
			fixture.targetID: policy.LocalExecutorPolicyRevision,
		},
		"reported_docker_version_env_sha256": map[string]any{
			fixture.targetID: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
		"reported_docker_container_ids": map[string]any{
			fixture.targetID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		"reported_docker_image_ids": map[string]any{
			fixture.targetID: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		"reported_docker_repository_digests": map[string]any{
			fixture.targetID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	}
}

func reportMariaDBDockerPortTerminal(
	t *testing.T,
	ctx context.Context,
	fixture mariaDBPullActivationFixture,
	job store.SystemUpdateJob,
	status string,
	portResult store.SystemUpdatePortReconfigurationResult,
) {
	t.Helper()
	base := time.Now().UTC()
	claim, _, err := fixture.updates.ClaimSystemUpdateJob(
		ctx,
		fixture.params.ServiceID,
		fixture.params.ExecutionHostID,
		"",
		map[string]string{fixture.targetID: "docker"},
		base,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	sequence := claim.ReportSequence
	if status == store.SystemUpdateStatusRolledBack {
		for index, intermediate := range []string{
			store.SystemUpdateStatusInstalling,
			store.SystemUpdateStatusRollingBack,
		} {
			_, applied, err := fixture.updates.ReportSystemUpdateJob(
				ctx, job.ID, store.SystemUpdateReport{
					AgentServiceID:  fixture.params.ServiceID,
					ExecutionHostID: fixture.params.ExecutionHostID,
					LeaseToken:      claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
					Sequence: sequence, Status: intermediate, Progress: 70 + index*10,
				}, base.Add(time.Duration(index+1)*time.Second), time.Minute,
			)
			if err != nil || !applied {
				t.Fatalf("%s report applied=%v err=%v", intermediate, applied, err)
			}
			sequence++
		}
	}
	terminal, applied, err := fixture.updates.ReportSystemUpdateJob(
		ctx, job.ID, store.SystemUpdateReport{
			AgentServiceID:  fixture.params.ServiceID,
			ExecutionHostID: fixture.params.ExecutionHostID,
			LeaseToken:      claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
			Sequence: sequence, Status: status, Progress: 100,
			PortReconfigure: &store.SystemUpdatePortReconfiguration{Result: portResult},
		}, base.Add(3*time.Second), time.Minute,
	)
	if err != nil || !applied || terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != portResult {
		t.Fatalf("terminal=%#v applied=%v err=%v", terminal, applied, err)
	}
}

func assertSingleMariaDBDockerCurrentReservation(
	t *testing.T,
	ctx context.Context,
	fixture mariaDBPullActivationFixture,
	activated store.ActivatePullUpdaterOwnershipResult,
) {
	t.Helper()
	reservations, err := fixture.updates.ListServicePortReservations(
		ctx, activated.Ownership.ExecutionHostID,
	)
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != 18081 ||
		reservations[0].ServiceRole != "api" {
		t.Fatalf("current reservation=%#v err=%v", reservations, err)
	}
}

func dockerPortEnvSHA256ForMariaDBTest(
	serviceType string,
	publishedPort, containerPort int,
	configRevision int64,
) string {
	publishedVariable := "AUTOSTREAM_WORKER_PORT"
	containerVariable := "AUTOSTREAM_WORKER_CONTAINER_PORT"
	if serviceType != "worker" {
		panic("unsupported Docker port test service")
	}
	body := []byte(fmt.Sprintf(
		"%s=%d\n%s=%d\nAUTOSTREAM_CONFIG_REVISION=%d\n",
		publishedVariable,
		publishedPort,
		containerVariable,
		containerPort,
		configRevision,
	))
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}
