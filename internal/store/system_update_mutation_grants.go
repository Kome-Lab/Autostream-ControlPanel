package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

const SystemUpdateMutationGrantMaxTTL = 90 * time.Second

const (
	SystemUpdateMutationOperationApply     = "apply"
	SystemUpdateMutationOperationReconcile = "reconcile"
)

var (
	ErrSystemUpdateMutationGrantConflict = errors.New("system update mutation grant is invalid, expired, or conflicts with the current job")

	systemUpdateMutationPlanPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	systemUpdateMutationSessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
	systemUpdateMutationTokenPattern   = regexp.MustCompile(`^ast_mutation_[A-Za-z0-9_-]{43}$`)
)

// SystemUpdateMutationGrantBinding is the immutable remote execution identity.
// It deliberately contains no path, command, URL, or credential selected by a
// job. Privileged target details remain in the remote host's root-owned config.
type SystemUpdateMutationGrantBinding struct {
	HostID            string
	TransportMode     string
	OwnershipEpoch    int64
	PolicyRevision    int64
	TargetID          string
	TargetServiceType string
	TargetVersion     string
	DeploymentMode    string
	JobOperation      string
	Operation         string
	PlanSHA256        string
	SessionID         string
	PortReconfigure   *SystemUpdatePortReconfiguration
}

type IssueSystemUpdateMutationGrantParams struct {
	AgentServiceID  string
	ExecutionHostID string
	LeaseToken      string
	LeaseGeneration int64
	Binding         SystemUpdateMutationGrantBinding
}

type SystemUpdateMutationGrant struct {
	ID              string
	JobID           string
	AgentServiceID  string
	LeaseGeneration int64
	Binding         SystemUpdateMutationGrantBinding
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time

	tokenHash string
}

type IssuedSystemUpdateMutationGrant struct {
	Grant      SystemUpdateMutationGrant
	GrantToken string
}

type SystemUpdateMutationGrantStore interface {
	IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error)
	ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (grant SystemUpdateMutationGrant, replayed bool, err error)
}

func (s *MemorySystemUpdateStore) IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error) {
	if err := ctx.Err(); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	jobID = strings.TrimSpace(jobID)
	params = normalizeSystemUpdateMutationGrantIssue(params)
	if jobID == "" || validateSystemUpdateMutationGrantIssue(params) != nil || ttl <= 0 {
		return IssuedSystemUpdateMutationGrant{}, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	ttl = boundedSystemUpdateMutationGrantTTL(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return IssuedSystemUpdateMutationGrant{}, ErrNotFound
	}
	ownership := syntheticSystemUpdateExecutionHost(job.ExecutionHostID)
	if current, ok := s.executionHosts[job.ExecutionHostID]; ok {
		ownership = current
	}
	if err := authorizeSystemUpdateJobOwnership(job, ownership, params.AgentServiceID, authenticatedSystemUpdateExecutionHost(job, params.ExecutionHostID)); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	if err := authorizeSystemUpdateMutationGrantIssue(job, params, now); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	rawToken, err := newSystemUpdateMutationGrantToken()
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	expiresAt := now.Add(ttl)
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = job.LeaseExpiresAt.UTC()
	}
	grant := SystemUpdateMutationGrant{
		ID: newUUID(), JobID: job.ID, AgentServiceID: params.AgentServiceID,
		LeaseGeneration: params.LeaseGeneration, Binding: params.Binding,
		ExpiresAt: expiresAt, CreatedAt: now, tokenHash: security.HashToken(rawToken),
	}
	if s.mutationGrants == nil {
		s.mutationGrants = map[string]SystemUpdateMutationGrant{}
	}
	if _, exists := s.mutationGrants[grant.tokenHash]; exists {
		return IssuedSystemUpdateMutationGrant{}, errors.New("generate unique system update mutation grant")
	}
	s.mutationGrants[grant.tokenHash] = grant
	return IssuedSystemUpdateMutationGrant{Grant: publicSystemUpdateMutationGrant(grant), GrantToken: rawToken}, nil
}

func (s *MemorySystemUpdateStore) ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (SystemUpdateMutationGrant, bool, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	jobID = strings.TrimSpace(jobID)
	grantToken = strings.TrimSpace(grantToken)
	binding = normalizeSystemUpdateMutationGrantBinding(binding)
	if jobID == "" || leaseGeneration <= 0 || !systemUpdateMutationTokenPattern.MatchString(grantToken) || validateSystemUpdateMutationGrantBinding(binding) != nil {
		return SystemUpdateMutationGrant{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.mutationGrants[security.HashToken(grantToken)]
	if !ok || grant.JobID != jobID || grant.LeaseGeneration != leaseGeneration || !sameSystemUpdateMutationGrantBinding(grant.Binding, binding) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if !grant.ExpiresAt.After(now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	job, ok := s.jobs[jobID]
	if !ok {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	ownership := syntheticSystemUpdateExecutionHost(job.ExecutionHostID)
	if current, ok := s.executionHosts[job.ExecutionHostID]; ok {
		ownership = current
	}
	if authorizeSystemUpdateJobOwnership(job, ownership, grant.AgentServiceID, job.ExecutionHostID) != nil ||
		!systemUpdateMutationGrantMatchesCurrentJob(grant, job, now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if grant.ConsumedAt != nil {
		return publicSystemUpdateMutationGrant(grant), true, nil
	}
	consumedAt := now
	grant.ConsumedAt = &consumedAt
	s.mutationGrants[grant.tokenHash] = grant
	return publicSystemUpdateMutationGrant(grant), false, nil
}

func (s *MariaDBSystemUpdateStore) IssueSystemUpdateMutationGrant(ctx context.Context, jobID string, params IssueSystemUpdateMutationGrantParams, now time.Time, ttl time.Duration) (IssuedSystemUpdateMutationGrant, error) {
	jobID = strings.TrimSpace(jobID)
	params = normalizeSystemUpdateMutationGrantIssue(params)
	if jobID == "" || validateSystemUpdateMutationGrantIssue(params) != nil || ttl <= 0 {
		return IssuedSystemUpdateMutationGrant{}, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	ttl = boundedSystemUpdateMutationGrantTTL(ttl)
	executionHostID, err := s.systemUpdateJobExecutionHost(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedSystemUpdateMutationGrant{}, ErrNotFound
	}
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, executionHostID)
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return IssuedSystemUpdateMutationGrant{}, ErrNotFound
	}
	if err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	if err := authorizeSystemUpdateJobOwnership(job, ownership, params.AgentServiceID, authenticatedSystemUpdateExecutionHost(job, params.ExecutionHostID)); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	if err := authorizeSystemUpdateMutationGrantIssue(job, params, now); err != nil {
		return IssuedSystemUpdateMutationGrant{}, err
	}
	expiresAt := now.Add(ttl)
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = job.LeaseExpiresAt.UTC()
	}
	for attempt := 0; attempt < 3; attempt++ {
		rawToken, tokenErr := newSystemUpdateMutationGrantToken()
		if tokenErr != nil {
			return IssuedSystemUpdateMutationGrant{}, tokenErr
		}
		grant := SystemUpdateMutationGrant{
			ID: newUUID(), JobID: job.ID, AgentServiceID: params.AgentServiceID,
			LeaseGeneration: params.LeaseGeneration, Binding: params.Binding,
			ExpiresAt: expiresAt, CreatedAt: now, tokenHash: security.HashToken(rawToken),
		}
		var targetServiceType any
		if grant.Binding.TargetServiceType != "" {
			targetServiceType = grant.Binding.TargetServiceType
		}
		args := []any{
			grant.ID, grant.JobID, grant.tokenHash, grant.AgentServiceID, grant.LeaseGeneration,
			grant.Binding.HostID, grant.Binding.TransportMode, grant.Binding.OwnershipEpoch, grant.Binding.PolicyRevision,
			grant.Binding.TargetID, targetServiceType, grant.Binding.TargetVersion, grant.Binding.DeploymentMode,
			grant.Binding.JobOperation, grant.Binding.Operation, grant.Binding.PlanSHA256, grant.Binding.SessionID,
		}
		args = append(args, systemUpdateMutationGrantPortSQLValues(grant.Binding.PortReconfigure)...)
		args = append(args, grant.ExpiresAt, grant.CreatedAt)
		_, err = tx.ExecContext(ctx, `INSERT INTO system_update_mutation_grants
  (id, job_id, token_hash, agent_service_id, lease_generation,
   host_id, transport_mode, ownership_epoch, policy_revision,
   target_id, target_service_type, target_version, deployment_mode,
   job_operation, operation, plan_sha256, session_id,
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
    expires_at, consumed_at, created_at)
VALUES (
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, NULL, ?)`,
			args...)
		if err == nil {
			if err := tx.Commit(); err != nil {
				return IssuedSystemUpdateMutationGrant{}, err
			}
			return IssuedSystemUpdateMutationGrant{Grant: publicSystemUpdateMutationGrant(grant), GrantToken: rawToken}, nil
		}
		if !isDuplicateKeyError(err) {
			return IssuedSystemUpdateMutationGrant{}, err
		}
	}
	return IssuedSystemUpdateMutationGrant{}, errors.New("generate unique system update mutation grant")
}

func (s *MariaDBSystemUpdateStore) ConsumeSystemUpdateMutationGrant(ctx context.Context, jobID, grantToken string, leaseGeneration int64, binding SystemUpdateMutationGrantBinding, now time.Time) (SystemUpdateMutationGrant, bool, error) {
	jobID = strings.TrimSpace(jobID)
	grantToken = strings.TrimSpace(grantToken)
	binding = normalizeSystemUpdateMutationGrantBinding(binding)
	if jobID == "" || leaseGeneration <= 0 || !systemUpdateMutationTokenPattern.MatchString(grantToken) || validateSystemUpdateMutationGrantBinding(binding) != nil {
		return SystemUpdateMutationGrant{}, false, ErrInvalidSystemUpdate
	}
	now = now.UTC()
	executionHostID, err := s.systemUpdateJobExecutionHost(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, executionHostID)
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	grant, err := scanSystemUpdateMutationGrant(tx.QueryRowContext(ctx, systemUpdateMutationGrantSelect+` WHERE token_hash = ? FOR UPDATE`, security.HashToken(grantToken)))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	if grant.JobID != jobID || grant.LeaseGeneration != leaseGeneration || !sameSystemUpdateMutationGrantBinding(grant.Binding, binding) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if !grant.ExpiresAt.After(now) {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	job, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE id = ? FOR UPDATE`, jobID))
	if err != nil || !systemUpdateMutationGrantMatchesCurrentJob(grant, job, now) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SystemUpdateMutationGrant{}, false, err
		}
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if authorizeSystemUpdateJobOwnership(job, ownership, grant.AgentServiceID, job.ExecutionHostID) != nil {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if grant.ConsumedAt != nil {
		if err := tx.Commit(); err != nil {
			return SystemUpdateMutationGrant{}, false, err
		}
		return publicSystemUpdateMutationGrant(grant), true, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_mutation_grants SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, now, grant.ID)
	if err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return SystemUpdateMutationGrant{}, false, ErrSystemUpdateMutationGrantConflict
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateMutationGrant{}, false, err
	}
	consumedAt := now
	grant.ConsumedAt = &consumedAt
	return publicSystemUpdateMutationGrant(grant), false, nil
}

const systemUpdateMutationGrantSelect = `SELECT
  id, job_id, token_hash, agent_service_id, lease_generation,
  host_id, transport_mode, ownership_epoch, policy_revision,
  target_id, target_service_type, target_version, deployment_mode,
  job_operation, operation, plan_sha256, session_id,
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
  expires_at, consumed_at, created_at
FROM system_update_mutation_grants`

func scanSystemUpdateMutationGrant(row systemUpdateScanner) (SystemUpdateMutationGrant, error) {
	var grant SystemUpdateMutationGrant
	var consumedAt sql.NullTime
	var (
		targetServiceType                                                sql.NullString
		jobOperation, networkNamespace, protocol                         sql.NullString
		expectedConfigSHA256                                             sql.NullString
		targetConfigSHA256, expectedExecutorPolicySHA256, portPlanSHA256 sql.NullString
		dockerPublishedHostIP, dockerApprovedComposeSHA256               sql.NullString
		dockerExpectedVersionEnvSHA256, dockerExpectedContainerID        sql.NullString
		dockerExpectedImageID, dockerExpectedRepositoryDigest            sql.NullString
		oldPort, newPort                                                 sql.NullInt64
		dockerOldPublishedPort, dockerNewPublishedPort                   sql.NullInt64
		dockerOldContainerPort, dockerNewContainerPort                   sql.NullInt64
		dockerOldHealthPort, dockerNewHealthPort                         sql.NullInt64
		dockerApprovedComposeRevision                                    sql.NullInt64
		expectedEndpointRevision, targetEndpointRevision                 sql.NullInt64
		expectedConfigRevision, targetConfigRevision                     sql.NullInt64
		expectedSourcePolicyRevision, expectedUpdaterPolicyRevision      sql.NullInt64
		expectedExecutorPolicyRevision                                   sql.NullInt64
	)
	err := row.Scan(&grant.ID, &grant.JobID, &grant.tokenHash, &grant.AgentServiceID, &grant.LeaseGeneration,
		&grant.Binding.HostID, &grant.Binding.TransportMode, &grant.Binding.OwnershipEpoch, &grant.Binding.PolicyRevision,
		&grant.Binding.TargetID, &targetServiceType, &grant.Binding.TargetVersion, &grant.Binding.DeploymentMode,
		&jobOperation, &grant.Binding.Operation, &grant.Binding.PlanSHA256, &grant.Binding.SessionID,
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
		&grant.ExpiresAt, &consumedAt, &grant.CreatedAt)
	if err != nil {
		return SystemUpdateMutationGrant{}, err
	}
	grant.Binding.TargetServiceType = targetServiceType.String
	grant.Binding.JobOperation = normalizedSystemUpdateJobOperation(jobOperation.String)
	if grant.Binding.JobOperation == SystemUpdateOperationPortReconfigure {
		grant.Binding.PortReconfigure = &SystemUpdatePortReconfiguration{
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
		}
		if grant.Binding.DeploymentMode == "docker" {
			grant.Binding.PortReconfigure.Docker = &SystemUpdateDockerPortReconfiguration{
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
	if consumedAt.Valid {
		grant.ConsumedAt = &consumedAt.Time
	}
	return grant, nil
}

func systemUpdateMutationGrantPortSQLValues(port *SystemUpdatePortReconfiguration) []any {
	if port == nil {
		return make([]any, 28)
	}
	values := []any{
		port.NetworkNamespace, string(port.Protocol), port.OldPort, port.NewPort,
		port.ExpectedEndpointRevision, port.TargetEndpointRevision,
		port.ExpectedConfigRevision, port.TargetConfigRevision,
		port.ExpectedConfigSHA256, port.ExpectedSourcePolicyRevision, port.TargetConfigSHA256,
		port.ExpectedUpdaterPolicyRevision, port.ExpectedExecutorPolicyRevision,
		port.ExpectedExecutorPolicySHA256, port.PortPlanSHA256,
	}
	if port.Docker == nil {
		return append(values, make([]any, 13)...)
	}
	return append(values,
		port.Docker.PublishedHostIP,
		port.Docker.OldPublishedPort,
		port.Docker.NewPublishedPort,
		port.Docker.OldContainerPort,
		port.Docker.NewContainerPort,
		port.Docker.OldHealthPort,
		port.Docker.NewHealthPort,
		port.Docker.ApprovedComposeConfigSHA256,
		port.Docker.ApprovedComposeRevision,
		port.Docker.ExpectedVersionEnvSHA256,
		port.Docker.ExpectedContainerID,
		port.Docker.ExpectedImageID,
		port.Docker.ExpectedRepositoryDigest,
	)
}

func normalizeSystemUpdateMutationGrantIssue(params IssueSystemUpdateMutationGrantParams) IssueSystemUpdateMutationGrantParams {
	params.AgentServiceID = strings.TrimSpace(params.AgentServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.LeaseToken = strings.TrimSpace(params.LeaseToken)
	params.Binding = normalizeSystemUpdateMutationGrantBinding(params.Binding)
	return params
}

func normalizeSystemUpdateMutationGrantBinding(binding SystemUpdateMutationGrantBinding) SystemUpdateMutationGrantBinding {
	binding.HostID = strings.TrimSpace(binding.HostID)
	binding.TransportMode = strings.ToLower(strings.TrimSpace(binding.TransportMode))
	if binding.TransportMode == "" {
		binding.TransportMode = SystemUpdateTransportSSHV1
	}
	binding.TargetID = strings.TrimSpace(binding.TargetID)
	binding.TargetServiceType = strings.ToLower(strings.TrimSpace(binding.TargetServiceType))
	binding.TargetVersion = strings.TrimSpace(binding.TargetVersion)
	binding.DeploymentMode = strings.ToLower(strings.TrimSpace(binding.DeploymentMode))
	binding.JobOperation = strings.ToLower(strings.TrimSpace(binding.JobOperation))
	if binding.JobOperation == "" {
		binding.JobOperation = SystemUpdateOperationSoftwareUpdate
	}
	binding.Operation = strings.ToLower(strings.TrimSpace(binding.Operation))
	binding.PlanSHA256 = strings.TrimSpace(binding.PlanSHA256)
	binding.SessionID = strings.TrimSpace(binding.SessionID)
	binding.PortReconfigure = normalizeSystemUpdatePortReconfiguration(binding.PortReconfigure)
	return binding
}

func validateSystemUpdateMutationGrantIssue(params IssueSystemUpdateMutationGrantParams) error {
	if params.AgentServiceID == "" || len(params.AgentServiceID) > 191 || containsControl(params.AgentServiceID) ||
		len(params.LeaseToken) < 32 || len(params.LeaseToken) > 256 || containsControl(params.LeaseToken) || params.LeaseGeneration <= 0 {
		return ErrInvalidSystemUpdate
	}
	if params.ExecutionHostID != "" && !validSystemUpdateExecutionHostID(params.ExecutionHostID) {
		return ErrInvalidSystemUpdate
	}
	return validateSystemUpdateMutationGrantBinding(params.Binding)
}

func validateSystemUpdateMutationGrantBinding(binding SystemUpdateMutationGrantBinding) error {
	if !validSystemUpdateExecutionHostID(binding.HostID) || binding.TargetID == "" || len(binding.TargetID) > 191 || containsControl(binding.TargetID) ||
		(binding.TransportMode != SystemUpdateTransportSSHV1 && binding.TransportMode != SystemUpdateTransportPullV2) ||
		binding.OwnershipEpoch < 0 || binding.PolicyRevision < 0 ||
		len(binding.TargetServiceType) > 64 || containsControl(binding.TargetServiceType) ||
		binding.TargetVersion == "" || len(binding.TargetVersion) > 128 || containsControl(binding.TargetVersion) ||
		!validSystemUpdateDeploymentMode(binding.DeploymentMode) ||
		!systemUpdateMutationPlanPattern.MatchString(binding.PlanSHA256) || !systemUpdateMutationSessionPattern.MatchString(binding.SessionID) {
		return ErrInvalidSystemUpdate
	}
	if binding.TransportMode == SystemUpdateTransportPullV2 &&
		(binding.OwnershipEpoch < 1 || binding.PolicyRevision < 1) {
		return ErrInvalidSystemUpdate
	}
	switch binding.JobOperation {
	case SystemUpdateOperationSoftwareUpdate:
		if binding.PortReconfigure != nil ||
			(binding.Operation != SystemUpdateMutationOperationApply &&
				binding.Operation != SystemUpdateMutationOperationReconcile) {
			return ErrInvalidSystemUpdate
		}
	case SystemUpdateOperationPortReconfigure:
		if binding.TransportMode != SystemUpdateTransportPullV2 ||
			!supportedSystemdPortServiceType(binding.TargetServiceType) ||
			(binding.Operation != SystemUpdateMutationOperationPortReconfigure &&
				binding.Operation != SystemUpdateMutationOperationPortReconfigureReconcile) ||
			validateSystemUpdatePortReconfigurationPlanForDeployment(
				binding.PortReconfigure,
				binding.DeploymentMode,
			) != nil ||
			binding.PortReconfigure.PortPlanSHA256 != binding.PlanSHA256 {
			return ErrInvalidSystemUpdate
		}
	default:
		return ErrInvalidSystemUpdate
	}
	return nil
}

func authorizeSystemUpdateMutationGrantIssue(job SystemUpdateJob, params IssueSystemUpdateMutationGrantParams, now time.Time) error {
	authorization := SystemUpdateAuthorization{
		AgentServiceID: params.AgentServiceID, ExecutionHostID: params.Binding.HostID,
		LeaseToken: params.LeaseToken, LeaseGeneration: params.LeaseGeneration,
		TargetID: params.Binding.TargetID, TargetVersion: params.Binding.TargetVersion,
		DeploymentMode: params.Binding.DeploymentMode,
	}
	if err := authorizeSystemUpdateMutation(job, authorization, now); err != nil {
		return err
	}
	if !systemUpdateMutationGrantBindingMatchesJob(params.Binding, job) {
		return ErrSystemUpdateAuthorizationMismatch
	}
	if !systemUpdateMutationGrantOperationMatchesStatus(params.Binding, job.Status) {
		return ErrSystemUpdateAuthorizationState
	}
	return nil
}

func systemUpdateMutationGrantMatchesCurrentJob(grant SystemUpdateMutationGrant, job SystemUpdateJob, now time.Time) bool {
	if job.ID != grant.JobID || job.AgentServiceID != grant.AgentServiceID || job.LeaseGeneration != grant.LeaseGeneration ||
		job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) || job.ExecutionHostID != grant.Binding.HostID ||
		!systemUpdateMutationGrantBindingMatchesJob(grant.Binding, job) ||
		job.TargetID != grant.Binding.TargetID || job.TargetVersion != grant.Binding.TargetVersion || job.DeploymentMode != grant.Binding.DeploymentMode {
		return false
	}
	return systemUpdateMutationGrantOperationMatchesStatus(grant.Binding, job.Status)
}

func sameSystemUpdateMutationGrantBinding(left, right SystemUpdateMutationGrantBinding) bool {
	leftPort := left.PortReconfigure
	rightPort := right.PortReconfigure
	left.PortReconfigure = nil
	right.PortReconfigure = nil
	if left != right {
		return false
	}
	if leftPort == nil || rightPort == nil {
		return leftPort == rightPort
	}
	return *leftPort == *rightPort
}

func systemUpdateMutationGrantBindingMatchesJob(binding SystemUpdateMutationGrantBinding, job SystemUpdateJob) bool {
	jobTransportMode := strings.ToLower(strings.TrimSpace(job.TransportMode))
	if jobTransportMode == "" {
		jobTransportMode = SystemUpdateTransportSSHV1
	}
	if binding.TransportMode != jobTransportMode {
		return false
	}
	if binding.OwnershipEpoch != job.OwnershipEpoch ||
		binding.PolicyRevision != job.PolicyRevision ||
		binding.JobOperation != normalizedSystemUpdateJobOperation(job.Operation) {
		return false
	}
	if binding.TargetServiceType != "" &&
		binding.TargetServiceType != strings.ToLower(strings.TrimSpace(job.TargetServiceType)) {
		return false
	}
	switch binding.JobOperation {
	case SystemUpdateOperationSoftwareUpdate:
		return job.PortReconfigure == nil && binding.PortReconfigure == nil
	case SystemUpdateOperationPortReconfigure:
		if binding.TargetServiceType == "" ||
			binding.TargetServiceType != strings.ToLower(strings.TrimSpace(job.TargetServiceType)) ||
			!sameSystemUpdateMutationGrantPortSnapshot(binding.PortReconfigure, job.PortReconfigure) {
			return false
		}
		intentSHA256, err := ComputeSystemUpdatePortIntentSHA256(job)
		if err != nil || job.PortReconfigure.PortPlanSHA256 != intentSHA256 {
			return false
		}
		runtimeSHA256, err := computeSystemUpdatePortRuntimePlanSHA256(job, binding.SessionID)
		return err == nil &&
			binding.PlanSHA256 == runtimeSHA256 &&
			binding.PortReconfigure.PortPlanSHA256 == runtimeSHA256
	default:
		return false
	}
}

func normalizedSystemUpdateJobOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		return SystemUpdateOperationSoftwareUpdate
	}
	return operation
}

func systemUpdateMutationGrantOperationMatchesStatus(binding SystemUpdateMutationGrantBinding, status string) bool {
	switch binding.JobOperation {
	case SystemUpdateOperationSoftwareUpdate:
		return (binding.Operation == SystemUpdateMutationOperationApply && status == SystemUpdateStatusInstalling) ||
			(binding.Operation == SystemUpdateMutationOperationReconcile && status == SystemUpdateStatusReconciling)
	case SystemUpdateOperationPortReconfigure:
		return (binding.Operation == SystemUpdateMutationOperationPortReconfigure && status == SystemUpdateStatusInstalling) ||
			(binding.Operation == SystemUpdateMutationOperationPortReconfigureReconcile && status == SystemUpdateStatusReconciling)
	default:
		return false
	}
}

func sameSystemUpdateMutationGrantPortSnapshot(
	binding *SystemUpdatePortReconfiguration,
	job *SystemUpdatePortReconfiguration,
) bool {
	if binding == nil || job == nil {
		return binding == job
	}
	bindingCopy := *binding
	jobCopy := *job
	bindingCopy.PortPlanSHA256 = ""
	jobCopy.PortPlanSHA256 = ""
	bindingCopy.Result = ""
	jobCopy.Result = ""
	return sameSystemUpdatePortReconfigurationIntent(&bindingCopy, &jobCopy)
}

func computeSystemUpdatePortRuntimePlanSHA256(job SystemUpdateJob, sessionID string) (string, error) {
	if job.Operation != SystemUpdateOperationPortReconfigure ||
		job.PortReconfigure == nil ||
		job.LeaseGeneration <= 0 ||
		!systemUpdateMutationSessionPattern.MatchString(sessionID) {
		return "", ErrInvalidSystemUpdate
	}
	port := job.PortReconfigure
	if job.DeploymentMode == "docker" {
		if validateSystemUpdatePortReconfigurationPlanForDeployment(port, "docker") != nil {
			return "", ErrInvalidSystemUpdate
		}
		docker := port.Docker
		payload := struct {
			SchemaVersion                  int    `json:"schema_version"`
			JobID                          string `json:"job_id"`
			HostID                         string `json:"host_id"`
			TargetID                       string `json:"target_id"`
			ServiceType                    string `json:"service_type"`
			NetworkNamespace               string `json:"network_namespace"`
			Protocol                       string `json:"protocol"`
			OldAdvertisedPort              int    `json:"old_advertised_port"`
			NewAdvertisedPort              int    `json:"new_advertised_port"`
			PublishedHostIP                string `json:"published_host_ip"`
			OldPublishedPort               int    `json:"old_published_port"`
			NewPublishedPort               int    `json:"new_published_port"`
			OldContainerPort               int    `json:"old_container_port"`
			NewContainerPort               int    `json:"new_container_port"`
			OldHealthPort                  int    `json:"old_health_port"`
			NewHealthPort                  int    `json:"new_health_port"`
			ExpectedEndpointRevision       int64  `json:"expected_endpoint_revision"`
			TargetEndpointRevision         int64  `json:"target_endpoint_revision"`
			ExpectedConfigRevision         int64  `json:"expected_config_revision"`
			TargetConfigRevision           int64  `json:"target_config_revision"`
			ExpectedConfigSHA256           string `json:"expected_config_sha256"`
			TargetConfigSHA256             string `json:"target_config_sha256"`
			ApprovedComposeConfigSHA256    string `json:"approved_compose_config_sha256"`
			ApprovedComposeRevision        int64  `json:"approved_compose_revision"`
			ExpectedVersionEnvSHA256       string `json:"expected_version_env_sha256"`
			ExpectedContainerID            string `json:"expected_container_id"`
			ExpectedImageID                string `json:"expected_image_id"`
			ExpectedRepositoryDigest       string `json:"expected_repository_digest"`
			ExpectedSourcePolicyRevision   int64  `json:"expected_source_policy_revision"`
			ExpectedUpdaterPolicyRevision  int64  `json:"expected_updater_policy_revision"`
			ExpectedExecutorPolicyRevision int64  `json:"expected_executor_policy_revision"`
			ExpectedExecutorPolicySHA256   string `json:"expected_executor_policy_sha256"`
			OwnershipEpoch                 int64  `json:"ownership_epoch"`
			LeaseGeneration                uint64 `json:"lease_generation"`
			SessionID                      string `json:"session_id"`
		}{
			SchemaVersion: 2,
			JobID:         job.ID, HostID: job.ExecutionHostID,
			TargetID: job.TargetID, ServiceType: job.TargetServiceType,
			NetworkNamespace: port.NetworkNamespace, Protocol: string(port.Protocol),
			OldAdvertisedPort: port.OldPort, NewAdvertisedPort: port.NewPort,
			PublishedHostIP:                docker.PublishedHostIP,
			OldPublishedPort:               docker.OldPublishedPort,
			NewPublishedPort:               docker.NewPublishedPort,
			OldContainerPort:               docker.OldContainerPort,
			NewContainerPort:               docker.NewContainerPort,
			OldHealthPort:                  docker.OldHealthPort,
			NewHealthPort:                  docker.NewHealthPort,
			ExpectedEndpointRevision:       port.ExpectedEndpointRevision,
			TargetEndpointRevision:         port.TargetEndpointRevision,
			ExpectedConfigRevision:         port.ExpectedConfigRevision,
			TargetConfigRevision:           port.TargetConfigRevision,
			ExpectedConfigSHA256:           port.ExpectedConfigSHA256,
			TargetConfigSHA256:             port.TargetConfigSHA256,
			ApprovedComposeConfigSHA256:    docker.ApprovedComposeConfigSHA256,
			ApprovedComposeRevision:        docker.ApprovedComposeRevision,
			ExpectedVersionEnvSHA256:       docker.ExpectedVersionEnvSHA256,
			ExpectedContainerID:            docker.ExpectedContainerID,
			ExpectedImageID:                docker.ExpectedImageID,
			ExpectedRepositoryDigest:       docker.ExpectedRepositoryDigest,
			ExpectedSourcePolicyRevision:   port.ExpectedSourcePolicyRevision,
			ExpectedUpdaterPolicyRevision:  port.ExpectedUpdaterPolicyRevision,
			ExpectedExecutorPolicyRevision: port.ExpectedExecutorPolicyRevision,
			ExpectedExecutorPolicySHA256:   port.ExpectedExecutorPolicySHA256,
			OwnershipEpoch:                 job.OwnershipEpoch,
			LeaseGeneration:                uint64(job.LeaseGeneration),
			SessionID:                      sessionID,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(encoded)
		return hex.EncodeToString(digest[:]), nil
	}
	payload := struct {
		SchemaVersion                  int    `json:"schema_version"`
		JobID                          string `json:"job_id"`
		HostID                         string `json:"host_id"`
		TargetID                       string `json:"target_id"`
		ServiceType                    string `json:"service_type"`
		NetworkNamespace               string `json:"network_namespace"`
		Protocol                       string `json:"protocol"`
		OldPort                        int    `json:"old_port"`
		NewPort                        int    `json:"new_port"`
		ExpectedEndpointRevision       int64  `json:"expected_endpoint_revision"`
		TargetEndpointRevision         int64  `json:"target_endpoint_revision"`
		ExpectedConfigRevision         int64  `json:"expected_config_revision"`
		TargetConfigRevision           int64  `json:"target_config_revision"`
		ExpectedConfigSHA256           string `json:"expected_config_sha256"`
		TargetConfigSHA256             string `json:"target_config_sha256"`
		ExpectedSourcePolicyRevision   int64  `json:"expected_source_policy_revision"`
		ExpectedUpdaterPolicyRevision  int64  `json:"expected_updater_policy_revision"`
		ExpectedExecutorPolicyRevision int64  `json:"expected_executor_policy_revision"`
		ExpectedExecutorPolicySHA256   string `json:"expected_executor_policy_sha256"`
		OwnershipEpoch                 int64  `json:"ownership_epoch"`
		LeaseGeneration                uint64 `json:"lease_generation"`
		SessionID                      string `json:"session_id"`
	}{
		SchemaVersion: 1, JobID: job.ID, HostID: job.ExecutionHostID,
		TargetID: job.TargetID, ServiceType: job.TargetServiceType,
		NetworkNamespace: port.NetworkNamespace, Protocol: string(port.Protocol),
		OldPort: port.OldPort, NewPort: port.NewPort,
		ExpectedEndpointRevision:       port.ExpectedEndpointRevision,
		TargetEndpointRevision:         port.TargetEndpointRevision,
		ExpectedConfigRevision:         port.ExpectedConfigRevision,
		TargetConfigRevision:           port.TargetConfigRevision,
		ExpectedConfigSHA256:           port.ExpectedConfigSHA256,
		TargetConfigSHA256:             port.TargetConfigSHA256,
		ExpectedSourcePolicyRevision:   port.ExpectedSourcePolicyRevision,
		ExpectedUpdaterPolicyRevision:  port.ExpectedUpdaterPolicyRevision,
		ExpectedExecutorPolicyRevision: port.ExpectedExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256:   port.ExpectedExecutorPolicySHA256,
		OwnershipEpoch:                 job.OwnershipEpoch,
		LeaseGeneration:                uint64(job.LeaseGeneration),
		SessionID:                      sessionID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func boundedSystemUpdateMutationGrantTTL(ttl time.Duration) time.Duration {
	if ttl > SystemUpdateMutationGrantMaxTTL {
		return SystemUpdateMutationGrantMaxTTL
	}
	return ttl
}

func newSystemUpdateMutationGrantToken() (string, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", err
	}
	return "ast_mutation_" + raw, nil
}

func publicSystemUpdateMutationGrant(grant SystemUpdateMutationGrant) SystemUpdateMutationGrant {
	grant.tokenHash = ""
	grant.Binding.PortReconfigure = cloneSystemUpdatePortReconfiguration(grant.Binding.PortReconfigure)
	return grant
}

var _ SystemUpdateMutationGrantStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateMutationGrantStore = (*MariaDBSystemUpdateStore)(nil)
