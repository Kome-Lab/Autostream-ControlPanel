package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"math"
	"sort"
	"strings"
	"time"
)

type MariaDBUpdaterPolicyStore struct {
	db *sql.DB
}

func NewMariaDBUpdaterPolicyStore(db *sql.DB) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db}
}

func NewMariaDBUpdaterPolicyAdminStore(db *sql.DB, _ string) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db}
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	for attempt := 0; attempt < updaterPolicySnapshotReadMaxAttempts; attempt++ {
		policy, err := s.getUpdaterPolicyOnce(ctx, serviceID)
		if !errors.Is(err, errUpdaterPolicySnapshotChanged) {
			return policy, err
		}
	}
	return UpdaterPolicy{}, ErrConflict
}

func (s MariaDBUpdaterPolicyStore) getUpdaterPolicyOnce(
	ctx context.Context,
	serviceID string,
) (UpdaterPolicy, error) {
	var (
		revision                    int64
		projectionRevision          int64
		localExecutorPolicyRevision int64
		body                        []byte
		updatedAt                   time.Time
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?`,
		serviceID,
	).Scan(&revision, &projectionRevision, &localExecutorPolicyRevision, &body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	policy, err := decodeUpdaterPolicyRevisions(
		serviceID,
		revision,
		projectionRevision,
		localExecutorPolicyRevision,
		body,
		updatedAt,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetDatabases(ctx, s.db, &policy); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetLocalListeners(ctx, s.db, &policy); err != nil {
		return UpdaterPolicy{}, err
	}
	return policy, nil
}

func (s MariaDBUpdaterPolicyStore) ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error) {
	for attempt := 0; attempt < updaterPolicySnapshotReadMaxAttempts; attempt++ {
		policies, err := s.listUpdaterPoliciesOnce(ctx)
		if !errors.Is(err, errUpdaterPolicySnapshotChanged) {
			return policies, err
		}
	}
	return nil, ErrConflict
}

func (s MariaDBUpdaterPolicyStore) listUpdaterPoliciesOnce(
	ctx context.Context,
) ([]UpdaterPolicy, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT service_id, revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
ORDER BY service_id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	policies := []UpdaterPolicy{}
	for rows.Next() {
		var (
			serviceID                   string
			revision                    int64
			projectionRevision          int64
			localExecutorPolicyRevision int64
			body                        []byte
			updatedAt                   time.Time
		)
		if err := rows.Scan(
			&serviceID,
			&revision,
			&projectionRevision,
			&localExecutorPolicyRevision,
			&body,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		policy, err := decodeUpdaterPolicyRevisions(
			serviceID,
			revision,
			projectionRevision,
			localExecutorPolicyRevision,
			body,
			updatedAt,
		)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range policies {
		if err := attachUpdaterTargetDatabases(ctx, s.db, &policies[index]); err != nil {
			return nil, err
		}
		if err := attachUpdaterTargetLocalListeners(ctx, s.db, &policies[index]); err != nil {
			return nil, err
		}
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].UpdaterID < policies[j].UpdaterID
	})
	return policies, nil
}

func (s MariaDBUpdaterPolicyStore) SavePullUpdaterPolicy(
	ctx context.Context,
	executionHosts SystemUpdateExecutionHostStore,
	serviceID string,
	expectedRevision, expectedOwnershipEpoch int64,
	input UpdaterPolicy,
) (UpdaterPolicy, error) {
	if expectedOwnershipEpoch < 0 {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if normalized.TransportMode != SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	updates, ok := executionHosts.(*MariaDBSystemUpdateStore)
	if !ok || updates == nil || updates.db == nil || updates.db != s.db {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionStoreMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	defer tx.Rollback()

	ownership, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		normalized.ExecutionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		ownership = syntheticSystemUpdateExecutionHost(normalized.ExecutionHostID)
		err = nil
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if ownership.OwnershipEpoch != expectedOwnershipEpoch {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "save_pull_updater_policy", mariaDBUpdaterPolicyHostLockHeld)
	if ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.ExecutionHostID != normalized.ExecutionHostID {
		return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
	}
	activePullOwner := ownership.AgentServiceID != ""
	if activePullOwner {
		if ownership.AgentServiceID != normalized.UpdaterID {
			return UpdaterPolicy{}, ErrSystemUpdateAgentBindingMismatch
		}
		if ownership.OwnershipEpoch <= 0 {
			return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
		}
		var activeJobID string
		err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions port_hold WHERE port_hold.job_id = system_update_jobs.id AND port_hold.recovery_required = 1))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, normalized.ExecutionHostID).Scan(&activeJobID)
		if err == nil {
			return UpdaterPolicy{}, ErrSystemUpdateExecutionHostBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return UpdaterPolicy{}, err
		}
		var activeRotationID string
		err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, normalized.ExecutionHostID).Scan(&activeRotationID)
		if err == nil {
			return UpdaterPolicy{}, ErrSystemUpdateRuntimeTokenRotationBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return UpdaterPolicy{}, err
		}
	} else if ownership.OwnershipEpoch != 0 || ownership.PolicyRevision != 0 {
		return UpdaterPolicy{}, ErrSystemUpdateExecutionHostStale
	}

	var (
		currentPolicyRevision                    int64
		currentPolicyProjectionRevision          int64
		currentPolicyLocalExecutorPolicyRevision int64
		currentPolicyBody                        []byte
		currentPolicyUpdatedAt                   time.Time
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		normalized.UpdaterID,
	).Scan(
		&currentPolicyRevision,
		&currentPolicyProjectionRevision,
		&currentPolicyLocalExecutorPolicyRevision,
		&currentPolicyBody,
		&currentPolicyUpdatedAt,
	)
	currentPolicyExists := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if (!currentPolicyExists && expectedRevision != 0) ||
		(currentPolicyExists && currentPolicyRevision != expectedRevision) {
		return UpdaterPolicy{}, ErrConflict
	}
	if currentPolicyExists {
		currentPolicy, decodeErr := decodeUpdaterPolicyRevisions(
			normalized.UpdaterID,
			currentPolicyRevision,
			currentPolicyProjectionRevision,
			currentPolicyLocalExecutorPolicyRevision,
			currentPolicyBody,
			currentPolicyUpdatedAt,
		)
		if decodeErr != nil {
			return UpdaterPolicy{}, decodeErr
		}
		if activePullOwner && ownership.PolicyRevision != currentPolicy.ProjectionRevision {
			return UpdaterPolicy{}, ErrConflict
		}
		if currentPolicy.ProjectionRevision >= math.MaxInt64 || currentPolicy.LocalExecutorPolicyRevision >= math.MaxInt64 {
			return UpdaterPolicy{}, ErrConflict
		}
		normalized.ProjectionRevision = currentPolicy.ProjectionRevision + 1
		normalized.LocalExecutorPolicyRevision = currentPolicy.LocalExecutorPolicyRevision + 1
		body, err = json.Marshal(normalized)
		if err != nil {
			return UpdaterPolicy{}, err
		}
	} else if activePullOwner && ownership.PolicyRevision != 0 {
		return UpdaterPolicy{}, ErrConflict
	}

	if err := saveUpdaterPolicyCAS(ctx, tx, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := replaceUpdaterTargetDatabases(ctx, tx, normalized); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := replaceUpdaterTargetLocalListeners(ctx, tx, normalized); err != nil {
		return UpdaterPolicy{}, err
	}
	if !activePullOwner {
		if err := tx.Commit(); err != nil {
			return UpdaterPolicy{}, err
		}
		return normalized, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET policy_revision = ?, updated_at = ?
WHERE execution_host_id = ?
  AND transport_mode = ?
  AND agent_service_id = ?
  AND ownership_epoch = ?
  AND policy_revision = ?`,
		normalized.ProjectionRevision,
		normalized.UpdatedAt,
		normalized.ExecutionHostID,
		SystemUpdateTransportPullV2,
		normalized.UpdaterID,
		ownership.OwnershipEpoch,
		ownership.PolicyRevision,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if affected != 1 {
		return UpdaterPolicy{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, err
	}
	return normalized, nil
}

func (s MariaDBUpdaterPolicyStore) BindPullUpdaterConfigurePolicy(
	ctx context.Context,
	params BindPullUpdaterConfigurePolicyParams,
) (UpdaterPolicy, error) {
	params, err := normalizeBindPullUpdaterConfigurePolicyParams(params)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	defer tx.Rollback()
	var (
		revision                    int64
		projectionRevision          int64
		localExecutorPolicyRevision int64
		body                        []byte
		updatedAt                   time.Time
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE`,
		params.ServiceID,
	).Scan(
		&revision,
		&projectionRevision,
		&localExecutorPolicyRevision,
		&body,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	current, err := decodeUpdaterPolicyRevisions(
		params.ServiceID,
		revision,
		projectionRevision,
		localExecutorPolicyRevision,
		body,
		updatedAt,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetDatabases(ctx, tx, &current); err != nil {
		return UpdaterPolicy{}, err
	}
	if err := attachUpdaterTargetLocalListeners(ctx, tx, &current); err != nil {
		return UpdaterPolicy{}, err
	}
	if current.TransportMode != SystemUpdateTransportPullV2 ||
		current.Revision != params.ExpectedSourcePolicyRevision ||
		current.ProjectionRevision != params.ExpectedProjectionRevision ||
		current.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision {
		return UpdaterPolicy{}, ErrConflict
	}
	if current.LocalExecutorPolicySHA256 == params.LocalExecutorPolicySHA256 {
		if err := tx.Commit(); err != nil {
			return UpdaterPolicy{}, err
		}
		return current, nil
	}
	now := time.Now().UTC()
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	current.LocalExecutorPolicySHA256 = params.LocalExecutorPolicySHA256
	current.UpdatedAt = now
	body, err = json.Marshal(current)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE update_agent_policies
SET policy_json = ?, updated_at = ?
WHERE service_id = ?
  AND revision = ?
  AND projection_revision = ?
  AND local_executor_policy_revision = ?`,
		body,
		now,
		params.ServiceID,
		params.ExpectedSourcePolicyRevision,
		params.ExpectedProjectionRevision,
		params.ExpectedLocalExecutorPolicyRevision,
	)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if affected != 1 {
		return UpdaterPolicy{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, err
	}
	return current, nil
}

func mariaDBAuthStoreForUpdaterPolicy(services ServiceRegistryStore) (MariaDBAuthStore, bool) {
	switch typed := services.(type) {
	case MariaDBAuthStore:
		return typed, true
	case *MariaDBAuthStore:
		if typed != nil {
			return *typed, true
		}
	}
	return MariaDBAuthStore{}, false
}

func saveUpdaterPolicyCAS(ctx context.Context, execer updaterPolicyExecer, expectedRevision int64, policy UpdaterPolicy, body []byte) error {
	if expectedRevision == 0 {
		_, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_policies
(service_id, revision, projection_revision, local_executor_policy_revision, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			policy.Revision,
			policy.ProjectionRevision,
			policy.LocalExecutorPolicyRevision,
			body,
			policy.UpdatedAt,
		)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrConflict
			}
			return err
		}
		return nil
	}
	result, err := execer.ExecContext(
		ctx,
		`UPDATE update_agent_policies
SET revision = ?, projection_revision = ?, local_executor_policy_revision = ?, policy_json = ?, updated_at = ?
WHERE service_id = ? AND revision = ?`,
		policy.Revision,
		policy.ProjectionRevision,
		policy.LocalExecutorPolicyRevision,
		body,
		policy.UpdatedAt,
		policy.UpdaterID,
		expectedRevision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}
