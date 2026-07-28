package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const systemUpdateHostSelfUpdateSelect = `SELECT
id, execution_host_id, agent_service_id, target_version, status, revision,
idempotency_key, intent_sha256, requested_by_user_id, requested_by_username,
retry_of_id, attempt_generation,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
expected_local_executor_policy_sha256,
previous_agent_version, previous_executor_version,
previous_agent_protocol_version, previous_executor_protocol_version,
previous_mutation_protocol_version, previous_recovery_protocol_version,
release_tag, release_commit, release_published_at,
manifest_asset_id, manifest_asset_name, manifest_sha256,
manifest_checksum_asset_id, manifest_checksum_sha256,
archive_asset_id, archive_asset_name, archive_size, archive_sha256,
archive_checksum_asset_id, archive_checksum_sha256, artifact_arch,
agent_protocol_version, executor_protocol_version, mutation_protocol_version,
recovery_protocol_version, minimum_panel_version, attestation_verified_at,
issued_at, observation_state, reported_phase, last_heartbeat_at, stalled_since,
cancel_requested_at, started_at, completed_at, code, message, created_at, updated_at
FROM system_update_host_self_updates`

type systemUpdateHostSelfUpdateScanner interface {
	Scan(...any) error
}

func scanSystemUpdateHostSelfUpdate(
	scanner systemUpdateHostSelfUpdateScanner,
) (SystemUpdateHostSelfUpdate, error) {
	var update SystemUpdateHostSelfUpdate
	var retryOf, phase, code, message sql.NullString
	var heartbeat, stalled, cancel, started, completed sql.NullTime
	err := scanner.Scan(
		&update.ID, &update.ExecutionHostID, &update.AgentServiceID,
		&update.TargetVersion, &update.Status, &update.Revision,
		&update.IdempotencyKey, &update.intentSHA256,
		&update.requestedByUserID, &update.RequestedByUsername,
		&retryOf, &update.AttemptGeneration,
		&update.ExpectedOwnershipEpoch,
		&update.ExpectedSourcePolicyRevision,
		&update.ExpectedProjectionRevision,
		&update.ExpectedLocalExecutorPolicyRevision,
		&update.ExpectedLocalExecutorPolicySHA256,
		&update.PreviousAgentVersion, &update.PreviousExecutorVersion,
		&update.PreviousAgentProtocolVersion,
		&update.PreviousExecutorProtocolVersion,
		&update.PreviousMutationProtocolVersion,
		&update.PreviousRecoveryProtocolVersion,
		&update.Release.Tag, &update.Release.Commit,
		&update.Release.PublishedAt,
		&update.Release.ManifestAssetID,
		&update.Release.ManifestAssetName,
		&update.Release.ManifestSHA256,
		&update.Release.ManifestChecksumAssetID,
		&update.Release.ManifestChecksumSHA256,
		&update.Release.ArchiveAssetID,
		&update.Release.ArchiveAssetName,
		&update.Release.ArchiveSize,
		&update.Release.ArchiveSHA256,
		&update.Release.ArchiveChecksumAssetID,
		&update.Release.ArchiveChecksumSHA256,
		&update.Release.Arch,
		&update.Release.AgentProtocolVersion,
		&update.Release.ExecutorProtocolVersion,
		&update.Release.MutationProtocolVersion,
		&update.Release.RecoveryProtocolVersion,
		&update.Release.MinimumPanelVersion,
		&update.Release.AttestationVerifiedAt,
		&update.IssuedAt, &update.ObservationState, &phase,
		&heartbeat, &stalled, &cancel, &started, &completed,
		&code, &message, &update.CreatedAt, &update.UpdatedAt,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	if retryOf.Valid {
		update.RetryOfID = retryOf.String
	}
	if phase.Valid {
		update.ReportedPhase = phase.String
	}
	if heartbeat.Valid {
		update.LastHeartbeatAt = cloneTimePtr(&heartbeat.Time)
	}
	if stalled.Valid {
		update.StalledSince = cloneTimePtr(&stalled.Time)
	}
	if cancel.Valid {
		update.CancelRequestedAt = cloneTimePtr(&cancel.Time)
	}
	if started.Valid {
		update.StartedAt = cloneTimePtr(&started.Time)
	}
	if completed.Valid {
		update.CompletedAt = cloneTimePtr(&completed.Time)
	}
	if code.Valid {
		update.Code = code.String
	}
	if message.Valid {
		update.Message = message.String
	}
	update.Release.PublishedAt = update.Release.PublishedAt.UTC()
	update.Release.AttestationVerifiedAt =
		update.Release.AttestationVerifiedAt.UTC()
	update.IssuedAt = update.IssuedAt.UTC()
	update.CreatedAt = update.CreatedAt.UTC()
	update.UpdatedAt = update.UpdatedAt.UTC()
	return update, nil
}

func (s *MariaDBSystemUpdateStore) ListSystemUpdateHostSelfUpdates(
	ctx context.Context,
	limit int,
) ([]SystemUpdateHostSelfUpdate, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(
		ctx,
		systemUpdateHostSelfUpdateSelect+
			` ORDER BY created_at DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SystemUpdateHostSelfUpdate, 0)
	for rows.Next() {
		update, scanErr := scanSystemUpdateHostSelfUpdate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, publicSystemUpdateHostSelfUpdate(update))
	}
	return result, rows.Err()
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdateHostSelfUpdate(
	ctx context.Context,
	id string,
) (SystemUpdateHostSelfUpdate, error) {
	id = strings.TrimSpace(id)
	if !serviceIDPattern.MatchString(id) {
		return SystemUpdateHostSelfUpdate{},
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		s.db.QueryRowContext(
			ctx, systemUpdateHostSelfUpdateSelect+` WHERE id = ?`, id,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	return publicSystemUpdateHostSelfUpdate(update), err
}

func (s *MariaDBSystemUpdateStore) GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
	ctx context.Context,
	executionHostID string,
) (SystemUpdateHostSelfUpdate, error) {
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateHostSelfUpdate{},
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		s.db.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateSelect+
				` WHERE active_execution_host_id = ?`,
			executionHostID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	return publicSystemUpdateHostSelfUpdate(update), err
}

func (s *MariaDBSystemUpdateStore) CreateSystemUpdateHostSelfUpdate(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateSystemUpdateHostSelfUpdateParams,
) (SystemUpdateHostSelfUpdate, bool, error) {
	params = normalizeCreateSystemUpdateHostSelfUpdateParams(params)
	if err := validateCreateSystemUpdateHostSelfUpdateParams(params); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if !systemUpdateHostSelfUpdateSharesMariaDB(
		s.db, services, policies,
	) {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateStore
	}
	intent, err := systemUpdateHostSelfUpdateIntentSHA256(params)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	defer tx.Rollback()
	update, created, err := createMariaDBSystemUpdateHostSelfUpdateTx(
		ctx, tx, params, intent,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	return publicSystemUpdateHostSelfUpdate(update), created, nil
}

func createMariaDBSystemUpdateHostSelfUpdateTx(
	ctx context.Context,
	tx *sql.Tx,
	params CreateSystemUpdateHostSelfUpdateParams,
	intent string,
) (SystemUpdateHostSelfUpdate, bool, error) {
	// Lock order is host -> self-update/idempotency -> other lifecycle lanes ->
	// policy -> service/token.
	ownership, err := getSystemUpdateExecutionHostForUpdate(
		ctx, tx, params.ExecutionHostID,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if ownership.OwnershipEpoch < 1 ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID == "" {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	existing, err := scanSystemUpdateHostSelfUpdate(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateSelect+
				` WHERE requested_by_user_id = ? AND idempotency_key = ? FOR UPDATE`,
			params.RequestedByUserID, params.IdempotencyKey,
		),
	)
	if err == nil {
		if existing.intentSHA256 != intent {
			return SystemUpdateHostSelfUpdate{}, false, ErrAlreadyExists
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	var activeID string
	err = tx.QueryRowContext(
		ctx,
		`SELECT id FROM system_update_host_self_updates
WHERE active_execution_host_id = ? LIMIT 1 FOR UPDATE`,
		params.ExecutionHostID,
	).Scan(&activeID)
	if err == nil {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if err := lockMariaDBHostSelfUpdateOtherLanes(
		ctx, tx, params.ExecutionHostID,
	); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	policy, err := getMariaDBUpdaterPolicyForHostSelfUpdate(
		ctx, tx, ownership.AgentServiceID,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	agent, err := scanService(
		tx.QueryRowContext(
			ctx,
			serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
			ownership.AgentServiceID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	var tokenServiceType string
	var revokedAt sql.NullTime
	err = tx.QueryRowContext(
		ctx,
		`SELECT service_type, revoked_at FROM service_tokens
WHERE id = ? FOR UPDATE`,
		agent.TokenID,
	).Scan(&tokenServiceType, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) ||
		tokenServiceType != "update_agent" || revokedAt.Valid {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	previous, err := validateSystemUpdateHostSelfUpdateReady(
		ownership, policy, agent, params.Release,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if hasStagedServiceNodeConfiguration(agent) {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateExecutionHostBusy
	}
	now := params.Now
	update := SystemUpdateHostSelfUpdate{
		ID:                                  newUUID(),
		ExecutionHostID:                     params.ExecutionHostID,
		AgentServiceID:                      agent.ServiceID,
		TargetVersion:                       params.TargetVersion,
		Status:                              SystemUpdateHostSelfUpdateQueued,
		Revision:                            1,
		IdempotencyKey:                      params.IdempotencyKey,
		RequestedByUsername:                 params.RequestedByUsername,
		RetryOfID:                           params.RetryOfID,
		AttemptGeneration:                   newUUID(),
		ExpectedOwnershipEpoch:              ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        policy.Revision,
		ExpectedProjectionRevision:          policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		PreviousAgentVersion:                previous.agentVersion,
		PreviousExecutorVersion:             previous.executorVersion,
		PreviousAgentProtocolVersion:        previous.agentProtocol,
		PreviousExecutorProtocolVersion:     previous.executorProtocol,
		PreviousMutationProtocolVersion:     previous.mutationProtocol,
		PreviousRecoveryProtocolVersion:     previous.recoveryProtocol,
		Release:                             params.Release,
		IssuedAt:                            now,
		ObservationState:                    SystemUpdateHostSelfUpdateObservationUnknown,
		CreatedAt:                           now,
		UpdatedAt:                           now,
		requestedByUserID:                   params.RequestedByUserID,
		intentSHA256:                        intent,
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO system_update_host_self_updates (
id, execution_host_id, agent_service_id, target_version, status, revision,
idempotency_key, intent_sha256, requested_by_user_id, requested_by_username,
retry_of_id, attempt_generation,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
expected_local_executor_policy_sha256,
previous_agent_version, previous_executor_version,
previous_agent_protocol_version, previous_executor_protocol_version,
previous_mutation_protocol_version, previous_recovery_protocol_version,
release_tag, release_commit, release_published_at,
manifest_asset_id, manifest_asset_name, manifest_sha256,
manifest_checksum_asset_id, manifest_checksum_sha256,
archive_asset_id, archive_asset_name, archive_size, archive_sha256,
archive_checksum_asset_id, archive_checksum_sha256, artifact_arch,
agent_protocol_version, executor_protocol_version, mutation_protocol_version,
recovery_protocol_version, minimum_panel_version, attestation_verified_at,
issued_at, observation_state, created_at, updated_at
) VALUES (
?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?
)`,
		update.ID, update.ExecutionHostID, update.AgentServiceID,
		update.TargetVersion, update.Status, update.Revision,
		update.IdempotencyKey, update.intentSHA256, update.requestedByUserID,
		update.RequestedByUsername, hostSelfUpdateNullIfEmpty(update.RetryOfID),
		update.AttemptGeneration, update.ExpectedOwnershipEpoch,
		update.ExpectedSourcePolicyRevision, update.ExpectedProjectionRevision,
		update.ExpectedLocalExecutorPolicyRevision,
		update.ExpectedLocalExecutorPolicySHA256,
		update.PreviousAgentVersion, update.PreviousExecutorVersion,
		update.PreviousAgentProtocolVersion,
		update.PreviousExecutorProtocolVersion,
		update.PreviousMutationProtocolVersion,
		update.PreviousRecoveryProtocolVersion,
		update.Release.Tag, update.Release.Commit,
		update.Release.PublishedAt, update.Release.ManifestAssetID,
		update.Release.ManifestAssetName, update.Release.ManifestSHA256,
		update.Release.ManifestChecksumAssetID,
		update.Release.ManifestChecksumSHA256,
		update.Release.ArchiveAssetID, update.Release.ArchiveAssetName,
		update.Release.ArchiveSize, update.Release.ArchiveSHA256,
		update.Release.ArchiveChecksumAssetID,
		update.Release.ArchiveChecksumSHA256, update.Release.Arch,
		update.Release.AgentProtocolVersion,
		update.Release.ExecutorProtocolVersion,
		update.Release.MutationProtocolVersion,
		update.Release.RecoveryProtocolVersion,
		update.Release.MinimumPanelVersion,
		update.Release.AttestationVerifiedAt,
		update.IssuedAt, update.ObservationState,
		update.CreatedAt, update.UpdatedAt,
	)
	if isDuplicateKeyError(err) {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateBusy
	}
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	return update, true, nil
}

func (s *MariaDBSystemUpdateStore) RetrySystemUpdateHostSelfUpdate(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params RetrySystemUpdateHostSelfUpdateParams,
) (SystemUpdateHostSelfUpdate, bool, error) {
	params.ID = strings.TrimSpace(params.ID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	params.Now = params.Now.UTC()
	if !serviceIDPattern.MatchString(params.ID) ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 ||
		containsControl(params.IdempotencyKey) ||
		!serviceIDPattern.MatchString(params.RequestedByUserID) ||
		params.RequestedByUsername == "" || params.Now.IsZero() {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	previous, err := s.GetSystemUpdateHostSelfUpdate(ctx, params.ID)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if !isTerminalSystemUpdateHostSelfUpdateStatus(previous.Status) {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateState
	}
	return s.CreateSystemUpdateHostSelfUpdate(
		ctx, services, policies,
		CreateSystemUpdateHostSelfUpdateParams{
			ExecutionHostID:     previous.ExecutionHostID,
			TargetVersion:       previous.TargetVersion,
			IdempotencyKey:      params.IdempotencyKey,
			RequestedByUserID:   params.RequestedByUserID,
			RequestedByUsername: params.RequestedByUsername,
			RetryOfID:           previous.ID,
			Release:             previous.Release,
			Now:                 params.Now,
		},
	)
}

func (s *MariaDBSystemUpdateStore) CancelSystemUpdateHostSelfUpdate(
	ctx context.Context,
	id, actorUserID string,
	expectedRevision int64,
	_ bool,
	now time.Time,
) (SystemUpdateHostSelfUpdate, error) {
	id = strings.TrimSpace(id)
	actorUserID = strings.TrimSpace(actorUserID)
	now = now.UTC()
	if !serviceIDPattern.MatchString(id) ||
		!serviceIDPattern.MatchString(actorUserID) ||
		expectedRevision < 1 || now.IsZero() {
		return SystemUpdateHostSelfUpdate{},
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	var hostID string
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT execution_host_id FROM system_update_host_self_updates WHERE id = ?`,
		id,
	).Scan(&hostID); errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	} else if err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	defer tx.Rollback()
	if _, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, hostID); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		tx.QueryRowContext(
			ctx, systemUpdateHostSelfUpdateSelect+` WHERE id = ? FOR UPDATE`, id,
		),
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	if update.Revision != expectedRevision {
		return SystemUpdateHostSelfUpdate{},
			ErrSystemUpdateHostSelfUpdateStale
	}
	switch {
	case update.Status == SystemUpdateHostSelfUpdateQueued:
		update.Status = SystemUpdateHostSelfUpdateCanceled
		update.Code = "canceled_by_admin"
		update.CompletedAt = cloneTimePtr(&now)
	default:
		return SystemUpdateHostSelfUpdate{},
			ErrSystemUpdateHostSelfUpdateCancel
	}
	update.Revision++
	update.UpdatedAt = now
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE system_update_host_self_updates SET
status = ?, revision = ?, cancel_requested_at = ?, completed_at = ?,
code = ?, updated_at = ? WHERE id = ?`,
		update.Status, update.Revision, update.CancelRequestedAt,
		update.CompletedAt, hostSelfUpdateNullIfEmpty(update.Code), update.UpdatedAt,
		update.ID,
	); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	return publicSystemUpdateHostSelfUpdate(update), nil
}

func (s *MariaDBSystemUpdateStore) ObserveSystemUpdateHostSelfUpdate(
	ctx context.Context,
	observation SystemUpdateHostSelfUpdateObservation,
) (SystemUpdateHostSelfUpdate, bool, error) {
	observation.ExecutionHostID = strings.TrimSpace(observation.ExecutionHostID)
	observation.AgentServiceID = strings.TrimSpace(observation.AgentServiceID)
	observation.Now = observation.Now.UTC()
	observation.HeartbeatAt = observation.HeartbeatAt.UTC()
	if !executionHostIDPattern.MatchString(observation.ExecutionHostID) ||
		!serviceIDPattern.MatchString(observation.AgentServiceID) ||
		observation.ExpectedRevision < 1 || observation.Now.IsZero() {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	defer tx.Rollback()
	if _, err := getSystemUpdateExecutionHostForUpdate(
		ctx, tx, observation.ExecutionHostID,
	); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	update, err := scanSystemUpdateHostSelfUpdate(
		tx.QueryRowContext(
			ctx,
			systemUpdateHostSelfUpdateSelect+
				` WHERE active_execution_host_id = ? FOR UPDATE`,
			observation.ExecutionHostID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if update.AgentServiceID != observation.AgentServiceID {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if update.Revision != observation.ExpectedRevision {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateStale
	}
	next, changed, err := reconcileSystemUpdateHostSelfUpdateObservation(
		update, observation,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE system_update_host_self_updates SET
status = ?, revision = ?, observation_state = ?, reported_phase = ?,
last_heartbeat_at = ?, stalled_since = ?, started_at = ?, completed_at = ?,
code = ?, message = ?, updated_at = ? WHERE id = ?`,
		next.Status, next.Revision, next.ObservationState,
		hostSelfUpdateNullIfEmpty(next.ReportedPhase), next.LastHeartbeatAt,
		next.StalledSince, next.StartedAt, next.CompletedAt,
		hostSelfUpdateNullIfEmpty(next.Code), hostSelfUpdateNullIfEmpty(next.Message),
		next.UpdatedAt, next.ID,
	); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	return publicSystemUpdateHostSelfUpdate(next), changed, nil
}

func getMariaDBUpdaterPolicyForHostSelfUpdate(
	ctx context.Context,
	tx *sql.Tx,
	serviceID string,
) (UpdaterPolicy, error) {
	var revision, projection, executor int64
	var body []byte
	var updatedAt time.Time
	err := tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision,
policy_json, updated_at FROM update_agent_policies
WHERE service_id = ? FOR UPDATE`,
		serviceID,
	).Scan(&revision, &projection, &executor, &body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	return decodeUpdaterPolicyRevisions(
		serviceID, revision, projection, executor, body, updatedAt,
	)
}

func lockMariaDBHostSelfUpdateOtherLanes(
	ctx context.Context,
	tx *sql.Tx,
	hostID string,
) error {
	var id string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM system_update_jobs
WHERE execution_host_id = ?
AND status NOT IN ('succeeded','rolled_back','failed','canceled')
ORDER BY created_at LIMIT 1 FOR UPDATE`,
		hostID,
	).Scan(&id)
	if err == nil {
		return ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT id FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ? LIMIT 1 FOR UPDATE`,
		hostID,
	).Scan(&id)
	if err == nil {
		return ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func systemUpdateHostSelfUpdateSharesMariaDB(
	db *sql.DB,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
) bool {
	var serviceDB *sql.DB
	switch typed := services.(type) {
	case MariaDBAuthStore:
		serviceDB = typed.db
	case *MariaDBAuthStore:
		if typed != nil {
			serviceDB = typed.db
		}
	}
	var policyDB *sql.DB
	switch typed := policies.(type) {
	case MariaDBUpdaterPolicyStore:
		policyDB = typed.db
	case *MariaDBUpdaterPolicyStore:
		if typed != nil {
			policyDB = typed.db
		}
	}
	return db != nil && serviceDB == db && policyDB == db
}

var _ SystemUpdateHostSelfUpdateStore = (*MariaDBSystemUpdateStore)(nil)

func hostSelfUpdateNullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
