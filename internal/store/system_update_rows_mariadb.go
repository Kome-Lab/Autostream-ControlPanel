package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"sort"
	"strings"
	"time"
)

const systemUpdateSelect = `SELECT id, target_id, target_service_type, operation,
network_namespace, protocol, old_port, new_port,
expected_endpoint_revision, target_endpoint_revision,
expected_config_revision, target_config_revision,
expected_config_sha256, expected_source_policy_revision, target_config_sha256,
 expected_updater_policy_revision, expected_executor_policy_revision,
 expected_executor_policy_sha256, port_plan_sha256,
 docker_published_host_ip, docker_old_published_port, docker_new_published_port,
 docker_old_container_port, docker_new_container_port,
 docker_old_health_port, docker_new_health_port,
 docker_approved_compose_config_sha256, docker_approved_compose_revision,
 docker_expected_version_env_sha256, docker_expected_container_id,
 docker_expected_image_id, docker_expected_repository_digest,
 deployment_mode, current_version, target_version, strategy, status, idempotency_key,
requested_by_user_id, requested_by_username, COALESCE(agent_service_id, ''),
execution_host_id, transport_mode, ownership_epoch, policy_revision,
lease_generation, COALESCE(lease_token_hash, ''), lease_expires_at, sequence, progress,
COALESCE(code, ''), COALESCE(message, ''), COALESCE(artifact_digest, ''),
COALESCE(previous_digest, ''), created_at, updated_at, claimed_at, completed_at,
cancelled_at, port_contract_version, ` + systemUpdatePortTransactionProjection + ` FROM system_update_jobs`

type systemUpdateScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateJob(row systemUpdateScanner) (SystemUpdateJob, error) {
	var job SystemUpdateJob
	var portTransactionJSON sql.NullString
	var portContractVersion sql.NullInt64
	var leaseExpiresAt, claimedAt, completedAt, cancelledAt sql.NullTime
	var (
		networkNamespace, protocol, expectedConfigSHA256, targetConfigSHA256 sql.NullString
		expectedExecutorPolicySHA256, portPlanSHA256                         sql.NullString
		dockerPublishedHostIP, dockerApprovedComposeSHA256                   sql.NullString
		dockerExpectedVersionEnvSHA256, dockerExpectedContainerID            sql.NullString
		dockerExpectedImageID, dockerExpectedRepositoryDigest                sql.NullString
		oldPort, newPort                                                     sql.NullInt64
		dockerOldPublishedPort, dockerNewPublishedPort                       sql.NullInt64
		dockerOldContainerPort, dockerNewContainerPort                       sql.NullInt64
		dockerOldHealthPort, dockerNewHealthPort                             sql.NullInt64
		dockerApprovedComposeRevision                                        sql.NullInt64
		expectedEndpointRevision, targetEndpointRevision                     sql.NullInt64
		expectedConfigRevision, targetConfigRevision                         sql.NullInt64
		expectedSourcePolicyRevision, expectedUpdaterPolicyRevision          sql.NullInt64
		expectedExecutorPolicyRevision                                       sql.NullInt64
	)
	err := row.Scan(
		&job.ID, &job.TargetID, &job.TargetServiceType, &job.Operation,
		&networkNamespace, &protocol, &oldPort, &newPort,
		&expectedEndpointRevision, &targetEndpointRevision,
		&expectedConfigRevision, &targetConfigRevision,
		&expectedConfigSHA256, &expectedSourcePolicyRevision, &targetConfigSHA256,
		&expectedUpdaterPolicyRevision, &expectedExecutorPolicyRevision,
		&expectedExecutorPolicySHA256, &portPlanSHA256,
		&dockerPublishedHostIP, &dockerOldPublishedPort, &dockerNewPublishedPort,
		&dockerOldContainerPort, &dockerNewContainerPort,
		&dockerOldHealthPort, &dockerNewHealthPort,
		&dockerApprovedComposeSHA256, &dockerApprovedComposeRevision,
		&dockerExpectedVersionEnvSHA256, &dockerExpectedContainerID,
		&dockerExpectedImageID, &dockerExpectedRepositoryDigest,
		&job.DeploymentMode, &job.CurrentVersion, &job.TargetVersion,
		&job.Strategy, &job.Status, &job.IdempotencyKey,
		&job.RequestedByUserID, &job.RequestedByUsername, &job.AgentServiceID,
		&job.ExecutionHostID, &job.TransportMode, &job.OwnershipEpoch,
		&job.PolicyRevision, &job.LeaseGeneration, &job.leaseTokenHash,
		&leaseExpiresAt, &job.Sequence, &job.Progress, &job.Code, &job.Message,
		&job.ArtifactDigest, &job.PreviousDigest, &job.CreatedAt, &job.UpdatedAt,
		&claimedAt, &completedAt, &cancelledAt, &portContractVersion, &portTransactionJSON,
	)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	if job.Operation == "" {
		job.Operation = SystemUpdateOperationSoftwareUpdate
	}
	if job.Operation == SystemUpdateOperationPortReconfigure {
		job.PortReconfigure = &SystemUpdatePortReconfiguration{
			NetworkNamespace:               networkNamespace.String,
			Protocol:                       SystemUpdatePortProtocol(protocol.String),
			OldPort:                        int(oldPort.Int64),
			NewPort:                        int(newPort.Int64),
			ExpectedEndpointRevision:       expectedEndpointRevision.Int64,
			TargetEndpointRevision:         targetEndpointRevision.Int64,
			ExpectedConfigRevision:         expectedConfigRevision.Int64,
			TargetConfigRevision:           targetConfigRevision.Int64,
			ExpectedConfigSHA256:           expectedConfigSHA256.String,
			TargetConfigSHA256:             targetConfigSHA256.String,
			ExpectedSourcePolicyRevision:   expectedSourcePolicyRevision.Int64,
			ExpectedUpdaterPolicyRevision:  expectedUpdaterPolicyRevision.Int64,
			ExpectedExecutorPolicyRevision: expectedExecutorPolicyRevision.Int64,
			ExpectedExecutorPolicySHA256:   expectedExecutorPolicySHA256.String,
			PortPlanSHA256:                 portPlanSHA256.String,
			Result:                         systemUpdatePortResultFromPersistedJob(job.Status, job.Code),
		}
		if job.DeploymentMode == "docker" {
			job.PortReconfigure.Docker = &SystemUpdateDockerPortReconfiguration{
				PublishedHostIP:             dockerPublishedHostIP.String,
				OldPublishedPort:            int(dockerOldPublishedPort.Int64),
				NewPublishedPort:            int(dockerNewPublishedPort.Int64),
				OldContainerPort:            int(dockerOldContainerPort.Int64),
				NewContainerPort:            int(dockerNewContainerPort.Int64),
				OldHealthPort:               int(dockerOldHealthPort.Int64),
				NewHealthPort:               int(dockerNewHealthPort.Int64),
				ApprovedComposeConfigSHA256: dockerApprovedComposeSHA256.String,
				ApprovedComposeRevision:     dockerApprovedComposeRevision.Int64,
				ExpectedVersionEnvSHA256:    dockerExpectedVersionEnvSHA256.String,
				ExpectedContainerID:         dockerExpectedContainerID.String,
				ExpectedImageID:             dockerExpectedImageID.String,
				ExpectedRepositoryDigest:    dockerExpectedRepositoryDigest.String,
			}
		}
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if claimedAt.Valid {
		job.ClaimedAt = &claimedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if cancelledAt.Valid {
		job.CancelledAt = &cancelledAt.Time
	}
	if portContractVersion.Valid && (portContractVersion.Int64 != 2 || job.Operation != SystemUpdateOperationPortReconfigure || !portTransactionJSON.Valid) ||
		!portContractVersion.Valid && portTransactionJSON.Valid {
		return SystemUpdateJob{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if err := decodeSystemUpdatePortTransaction(&job, portTransactionJSON.String); err != nil {
		return SystemUpdateJob{}, err
	}
	return job, nil
}

func (s *MariaDBSystemUpdateStore) systemUpdateJobExecutionHost(ctx context.Context, id string) (string, error) {
	var executionHostID string
	err := s.db.QueryRowContext(ctx, `SELECT execution_host_id FROM system_update_jobs WHERE id = ?`, id).Scan(&executionHostID)
	return executionHostID, err
}

func (s *MariaDBSystemUpdateStore) getSystemUpdateByIdempotency(ctx context.Context, userID, key string) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ?`, userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func (s *MariaDBSystemUpdateStore) getActiveSystemUpdateForTarget(ctx context.Context, targetID string) (SystemUpdateJob, error) {
	job, err := scanSystemUpdateJob(s.db.QueryRowContext(ctx, systemUpdateSelect+` WHERE target_id = ? AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions p WHERE p.job_id=system_update_jobs.id AND p.recovery_required=1)) ORDER BY created_at LIMIT 1`, targetID))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, ErrNotFound
	}
	return job, err
}

func findClaimableSystemUpdate(ctx context.Context, tx *sql.Tx, agentID, executionHostID string, targets map[string]string, now time.Time, expired bool) (SystemUpdateJob, error) {
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2+5)
	query := systemUpdateSelect + ` WHERE `
	if expired {
		query += `status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') AND lease_expires_at <= ?`
		args = append(args, now)
		if executionHostID != "" {
			query += ` AND execution_host_id = ?`
			args = append(args, executionHostID)
		}
		query += ` AND target_id IN (` + placeholders + `)`
	} else {
		query += `status = 'queued' AND execution_host_id = ? AND target_id IN (` + placeholders + `)`
		args = append(args, executionHostID)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	query += ` AND (`
	for i, id := range ids {
		if i > 0 {
			query += ` OR `
		}
		query += `(target_id = ? AND deployment_mode = ?)`
		args = append(args, id, targets[id])
	}
	query += `)`
	if agentID != "" {
		query += ` AND agent_service_id = ?`
		args = append(args, agentID)
	}
	query += ` ORDER BY created_at ASC LIMIT 1 FOR UPDATE`
	return scanSystemUpdateJob(tx.QueryRowContext(ctx, query, args...))
}

func findExecutingSystemUpdateForAgentHost(ctx context.Context, tx *sql.Tx, agentID, executionHostID string) (SystemUpdateJob, error) {
	return scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') AND agent_service_id = ? AND execution_host_id = ? ORDER BY created_at ASC LIMIT 1 FOR UPDATE`, agentID, executionHostID))
}

func newSystemUpdateLeaseToken() (string, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", err
	}
	return "ast_update_" + raw, nil
}
