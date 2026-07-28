package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
)

const systemUpdateHostSelfUpdateGrantSelect = `SELECT
id, self_update_id, attempt_generation, operation,
execution_host_id, agent_service_id, expected_self_update_revision,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
expected_local_executor_policy_sha256,
agent_version, executor_version, release_commit, artifact_sha256, release_binding,
agent_protocol_version, executor_protocol_version, mutation_protocol_version,
recovery_protocol_version, directive_issued_at, plan_sha256, session_id,
token_hash, revision, issued_at, expires_at, consumed_at,
stage_claim_revision, stage_claimed_at, created_at, updated_at
FROM system_update_host_self_update_grants`

type systemUpdateHostSelfUpdateGrantScanner interface {
	Scan(...any) error
}

func scanSystemUpdateHostSelfUpdateGrant(
	scanner systemUpdateHostSelfUpdateGrantScanner,
) (SystemUpdateHostSelfUpdateGrant, error) {
	var grant SystemUpdateHostSelfUpdateGrant
	var consumed, stageClaimed sql.NullTime
	var stageClaimRevision sql.NullInt64
	var releaseBinding []byte
	err := scanner.Scan(
		&grant.ID, &grant.SelfUpdateID, &grant.AttemptGeneration,
		&grant.Operation, &grant.ExecutionHostID, &grant.AgentServiceID,
		&grant.ExpectedSelfUpdateRevision,
		&grant.ExpectedOwnershipEpoch,
		&grant.ExpectedSourcePolicyRevision,
		&grant.ExpectedProjectionRevision,
		&grant.ExpectedLocalExecutorPolicyRevision,
		&grant.ExpectedLocalExecutorPolicySHA256,
		&grant.AgentVersion, &grant.ExecutorVersion,
		&grant.ReleaseCommit, &grant.ArtifactSHA256,
		&releaseBinding,
		&grant.AgentProtocolVersion, &grant.ExecutorProtocolVersion,
		&grant.MutationProtocolVersion, &grant.RecoveryProtocolVersion,
		&grant.DirectiveIssuedAt, &grant.PlanSHA256, &grant.SessionID,
		&grant.tokenHash, &grant.Revision, &grant.IssuedAt,
		&grant.ExpiresAt, &consumed,
		&stageClaimRevision, &stageClaimed,
		&grant.CreatedAt, &grant.UpdatedAt,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdateGrant{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(releaseBinding))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grant.Release); err != nil {
		return SystemUpdateHostSelfUpdateGrant{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SystemUpdateHostSelfUpdateGrant{}, errors.New(
			"host self-update grant release binding contains trailing data",
		)
	}
	if consumed.Valid {
		grant.ConsumedAt = cloneTimePtr(&consumed.Time)
	}
	if stageClaimRevision.Valid {
		grant.StageClaimRevision = stageClaimRevision.Int64
	}
	if stageClaimed.Valid {
		grant.StageClaimedAt = cloneTimePtr(&stageClaimed.Time)
	}
	grant.DirectiveIssuedAt = grant.DirectiveIssuedAt.UTC()
	grant.Release.PublishedAt = grant.Release.PublishedAt.UTC()
	grant.IssuedAt = grant.IssuedAt.UTC()
	grant.ExpiresAt = grant.ExpiresAt.UTC()
	if grant.ConsumedAt != nil {
		consumedAt := grant.ConsumedAt.UTC()
		grant.ConsumedAt = &consumedAt
	}
	if grant.StageClaimedAt != nil {
		claimedAt := grant.StageClaimedAt.UTC()
		grant.StageClaimedAt = &claimedAt
	}
	grant.CreatedAt = grant.CreatedAt.UTC()
	grant.UpdatedAt = grant.UpdatedAt.UTC()
	return grant, nil
}

func (s *MariaDBSystemUpdateStore) IssueSystemUpdateHostSelfUpdateGrant(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params IssueSystemUpdateHostSelfUpdateGrantParams,
) (IssueSystemUpdateHostSelfUpdateGrantResult, error) {
	params = normalizeIssueSystemUpdateHostSelfUpdateGrantParams(params)
	if err := validateIssueSystemUpdateHostSelfUpdateGrantParams(params); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if !systemUpdateHostSelfUpdateSharesMariaDB(
		s.db, services, policies,
	) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(
		ctx, tx, params.ExecutionHostID,
	)
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateSelect+` WHERE id = ? FOR UPDATE`,
			params.SelfUpdateID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, ErrNotFound
	}
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if err := validateMariaDBSystemUpdateHostSelfUpdateGrantStateTx(
		ctx, tx, ownership, update, params.ExecutionHostID,
		params.AgentServiceID, params.ExpectedRevision,
	); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if params.Operation == SystemUpdateHostSelfUpdateGrantStage &&
		update.Status != SystemUpdateHostSelfUpdateQueued {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateState
	}
	if params.Operation == SystemUpdateHostSelfUpdateGrantReconcile &&
		(update.Status == SystemUpdateHostSelfUpdateQueued ||
			isTerminalSystemUpdateHostSelfUpdateStatus(update.Status)) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateState
	}
	existing, err := scanSystemUpdateHostSelfUpdateGrant(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateGrantSelect+
				` WHERE self_update_id = ? AND operation = ? AND session_id = ? FOR UPDATE`,
			update.ID, params.Operation, params.SessionID,
		),
	)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if exists && existing.ConsumedAt != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateConsumed
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	raw = "ast_hsug_" + raw
	grant := systemUpdateHostSelfUpdateGrantFromUpdate(
		update, params, params.Now,
	)
	if exists &&
		!sameSystemUpdateHostSelfUpdateGrantIssueIntent(existing, grant) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	releaseBinding, err := json.Marshal(grant.Release)
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	grant.tokenHash = security.HashToken(raw)
	if exists {
		grant.ID = existing.ID
		grant.Revision = existing.Revision + 1
		grant.CreatedAt = existing.CreatedAt
		_, err = tx.ExecContext(
			ctx,
			`UPDATE system_update_host_self_update_grants SET
expected_self_update_revision = ?, token_hash = ?, revision = ?,
issued_at = ?, expires_at = ?, updated_at = ?
WHERE id = ? AND consumed_at IS NULL`,
			grant.ExpectedSelfUpdateRevision, grant.tokenHash,
			grant.Revision, grant.IssuedAt, grant.ExpiresAt,
			grant.UpdatedAt, grant.ID,
		)
	} else {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO system_update_host_self_update_grants (
id, self_update_id, attempt_generation, operation,
execution_host_id, agent_service_id, expected_self_update_revision,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
expected_local_executor_policy_sha256,
agent_version, executor_version, release_commit, artifact_sha256, release_binding,
agent_protocol_version, executor_protocol_version, mutation_protocol_version,
recovery_protocol_version, directive_issued_at, plan_sha256, session_id,
token_hash, revision, issued_at, expires_at, created_at, updated_at
) VALUES (
?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?
)`,
			grant.ID, grant.SelfUpdateID, grant.AttemptGeneration,
			grant.Operation, grant.ExecutionHostID, grant.AgentServiceID,
			grant.ExpectedSelfUpdateRevision,
			grant.ExpectedOwnershipEpoch,
			grant.ExpectedSourcePolicyRevision,
			grant.ExpectedProjectionRevision,
			grant.ExpectedLocalExecutorPolicyRevision,
			grant.ExpectedLocalExecutorPolicySHA256,
			grant.AgentVersion, grant.ExecutorVersion,
			grant.ReleaseCommit, grant.ArtifactSHA256,
			releaseBinding,
			grant.AgentProtocolVersion, grant.ExecutorProtocolVersion,
			grant.MutationProtocolVersion, grant.RecoveryProtocolVersion,
			grant.DirectiveIssuedAt, grant.PlanSHA256, grant.SessionID,
			grant.tokenHash, grant.Revision, grant.IssuedAt,
			grant.ExpiresAt, grant.CreatedAt, grant.UpdatedAt,
		)
	}
	if isDuplicateKeyError(err) {
		return IssueSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IssueSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	return IssueSystemUpdateHostSelfUpdateGrantResult{
		Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
		RawToken: raw,
		Issued:   true,
	}, nil
}

func (s *MariaDBSystemUpdateStore) ConsumeSystemUpdateHostSelfUpdateGrant(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params ConsumeSystemUpdateHostSelfUpdateGrantParams,
) (ConsumeSystemUpdateHostSelfUpdateGrantResult, error) {
	params.RawToken = strings.TrimSpace(params.RawToken)
	params.Now = params.Now.UTC()
	if !strings.HasPrefix(params.RawToken, "ast_hsug_") ||
		len(params.RawToken) > 256 || params.Now.IsZero() {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if !systemUpdateHostSelfUpdateSharesMariaDB(
		s.db, services, policies,
	) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateStore
	}
	tokenHash := security.HashToken(params.RawToken)
	var hostID string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT execution_host_id
FROM system_update_host_self_update_grants WHERE token_hash = ?`,
		tokenHash,
	).Scan(&hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	defer tx.Rollback()
	ownership, err := getSystemUpdateExecutionHostForUpdate(
		ctx, tx, hostID,
	)
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	grant, err := scanSystemUpdateHostSelfUpdateGrant(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateGrantSelect+
				` WHERE token_hash = ? FOR UPDATE`,
			tokenHash,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if grant.ExecutionHostID != hostID ||
		!sameSystemUpdateHostSelfUpdateGrantBinding(
			grant, params.Binding,
		) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateGrant
	}
	if grant.ConsumedAt == nil && params.Now.After(grant.ExpiresAt) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateExpired
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateSelect+` WHERE id = ? FOR UPDATE`,
			grant.SelfUpdateID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, ErrNotFound
	}
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if grant.ConsumedAt != nil {
		if err := validateConsumedSystemUpdateHostSelfUpdateGrant(
			grant,
			update,
		); err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{
			Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
			Consumed: false,
		}, nil
	}
	if err := validateMariaDBSystemUpdateHostSelfUpdateGrantStateTx(
		ctx, tx, ownership, update, grant.ExecutionHostID,
		grant.AgentServiceID, grant.ExpectedSelfUpdateRevision,
	); err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if grant.Operation == SystemUpdateHostSelfUpdateGrantStage {
		update, err = reserveSystemUpdateHostSelfUpdateStage(
			update,
			params.Now,
		)
		if err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE system_update_host_self_updates SET
status = ?, revision = ?, started_at = ?, updated_at = ?
WHERE id = ? AND status = ? AND revision = ?`,
			update.Status,
			update.Revision,
			update.StartedAt,
			update.UpdatedAt,
			update.ID,
			SystemUpdateHostSelfUpdateQueued,
			grant.ExpectedSelfUpdateRevision,
		)
		if err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
		}
		if affected != 1 {
			return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
				ErrSystemUpdateHostSelfUpdateStale
		}
		grant.StageClaimRevision = update.Revision
		grant.StageClaimedAt = cloneTimePtr(&params.Now)
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE system_update_host_self_update_grants
SET consumed_at = ?, stage_claim_revision = ?, stage_claimed_at = ?,
updated_at = ?
WHERE id = ? AND token_hash = ? AND consumed_at IS NULL`,
		params.Now,
		hostSelfUpdateNullablePositiveInt64(grant.StageClaimRevision),
		grant.StageClaimedAt,
		params.Now,
		grant.ID,
		tokenHash,
	)
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	if affected != 1 {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{},
			ErrSystemUpdateHostSelfUpdateConsumed
	}
	grant.ConsumedAt = cloneTimePtr(&params.Now)
	grant.UpdatedAt = params.Now
	if err := tx.Commit(); err != nil {
		return ConsumeSystemUpdateHostSelfUpdateGrantResult{}, err
	}
	return ConsumeSystemUpdateHostSelfUpdateGrantResult{
		Grant:    publicSystemUpdateHostSelfUpdateGrant(grant),
		Consumed: true,
	}, nil
}

func hostSelfUpdateNullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func validateMariaDBSystemUpdateHostSelfUpdateGrantStateTx(
	ctx context.Context,
	tx *sql.Tx,
	ownership SystemUpdateExecutionHost,
	update SystemUpdateHostSelfUpdate,
	hostID, agentID string,
	expectedRevision int64,
) error {
	if update.ExecutionHostID != hostID ||
		update.AgentServiceID != agentID {
		return ErrSystemUpdateOwnershipConflict
	}
	if update.Revision != expectedRevision ||
		isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	if ownership.ExecutionHostID != hostID ||
		ownership.AgentServiceID != agentID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch != update.ExpectedOwnershipEpoch ||
		ownership.PolicyRevision != update.ExpectedSourcePolicyRevision {
		return ErrSystemUpdateOwnershipConflict
	}
	if err := lockMariaDBHostSelfUpdateOtherLanes(ctx, tx, hostID); err != nil {
		return err
	}
	policy, err := getMariaDBUpdaterPolicyForHostSelfUpdate(
		ctx, tx, agentID,
	)
	if err != nil {
		return err
	}
	if policy.ExecutionHostID != hostID ||
		policy.Revision != update.ExpectedSourcePolicyRevision ||
		policy.ProjectionRevision != update.ExpectedProjectionRevision ||
		policy.LocalExecutorPolicyRevision !=
			update.ExpectedLocalExecutorPolicyRevision ||
		policy.LocalExecutorPolicySHA256 !=
			update.ExpectedLocalExecutorPolicySHA256 {
		return ErrSystemUpdateHostSelfUpdateStale
	}
	agent, err := scanService(
		tx.QueryRowContext(
			ctx,
			serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
			agentID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if agent.ExecutionHostID != hostID ||
		agent.OwnershipEpoch != update.ExpectedOwnershipEpoch ||
		hasStagedServiceNodeConfiguration(agent) {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}
