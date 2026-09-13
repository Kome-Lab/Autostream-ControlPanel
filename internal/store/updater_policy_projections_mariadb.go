package store

import (
	"context"
	"database/sql"
)

type updaterPolicyExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type updaterPolicyQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type updaterPolicyRowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func attachUpdaterTargetDatabases(
	ctx context.Context,
	queryer updaterPolicyRowsQueryer,
	policy *UpdaterPolicy,
) error {
	if policy == nil || queryer == nil {
		return ErrInvalidSettings
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT p.revision AS current_policy_revision,
       d.target_id,
       d.binding_policy_revision,
       d.database_name
FROM update_agent_policies AS p
LEFT JOIN update_agent_target_databases AS d
  ON d.updater_service_id = p.service_id
WHERE p.service_id = ?
ORDER BY d.target_id`,
		policy.UpdaterID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	targetIndexes := make(map[string]int, len(policy.Targets))
	for index := range policy.Targets {
		targetIndexes[policy.Targets[index].TargetID] = index
	}
	databases := make(map[int]string, len(policy.Targets))
	snapshotFound := false
	for rows.Next() {
		var (
			currentPolicyRevision int64
			targetID              sql.NullString
			bindingPolicyRevision sql.NullInt64
			databaseName          sql.NullString
		)
		if err := rows.Scan(
			&currentPolicyRevision,
			&targetID,
			&bindingPolicyRevision,
			&databaseName,
		); err != nil {
			return err
		}
		snapshotFound = true
		if currentPolicyRevision != policy.Revision {
			return errUpdaterPolicySnapshotChanged
		}
		if !targetID.Valid && !bindingPolicyRevision.Valid && !databaseName.Valid {
			continue
		}
		if !targetID.Valid || !bindingPolicyRevision.Valid || !databaseName.Valid {
			return ErrInvalidSettings
		}
		if bindingPolicyRevision.Int64 != policy.Revision {
			continue
		}
		index, exists := targetIndexes[targetID.String]
		if !exists ||
			!updaterPolicyTargetRequiresDatabase(policy.Targets[index]) ||
			!updaterPolicyDatabaseNamePattern.MatchString(databaseName.String) {
			continue
		}
		databases[index] = databaseName.String
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !snapshotFound {
		return errUpdaterPolicySnapshotChanged
	}
	candidate := cloneUpdaterPolicy(*policy)
	for index := range candidate.Targets {
		candidate.Targets[index].DatabaseName = databases[index]
	}
	*policy = candidate
	return nil
}

func replaceUpdaterTargetDatabases(
	ctx context.Context,
	execer updaterPolicyExecer,
	policy UpdaterPolicy,
) error {
	if execer == nil ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.Revision < 1 ||
		!PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return ErrInvalidSettings
	}
	if _, err := execer.ExecContext(
		ctx,
		`DELETE FROM update_agent_target_databases WHERE updater_service_id = ?`,
		policy.UpdaterID,
	); err != nil {
		return err
	}
	for _, target := range policy.Targets {
		if !updaterPolicyTargetRequiresDatabase(target) {
			continue
		}
		if _, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_target_databases
(updater_service_id, target_id, binding_policy_revision, database_name, updated_at)
VALUES (?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			target.TargetID,
			policy.Revision,
			target.DatabaseName,
			policy.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func attachUpdaterTargetLocalListeners(
	ctx context.Context,
	queryer updaterPolicyRowsQueryer,
	policy *UpdaterPolicy,
) error {
	if policy == nil || queryer == nil {
		return ErrInvalidSettings
	}
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT p.revision AS current_policy_revision,
       listener.target_id,
       listener.binding_policy_revision,
       listener.local_listen_port
FROM update_agent_policies AS p
LEFT JOIN update_agent_target_local_listeners AS listener
  ON listener.updater_service_id = p.service_id
WHERE p.service_id = ?
ORDER BY listener.target_id`,
		policy.UpdaterID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	targetIndexes := make(map[string]int, len(policy.Targets))
	for index := range policy.Targets {
		targetIndexes[policy.Targets[index].TargetID] = index
	}
	listeners := make(map[int]int, len(policy.Targets))
	snapshotFound := false
	for rows.Next() {
		var (
			currentPolicyRevision int64
			targetID              sql.NullString
			bindingPolicyRevision sql.NullInt64
			localListenPort       sql.NullInt64
		)
		if err := rows.Scan(
			&currentPolicyRevision,
			&targetID,
			&bindingPolicyRevision,
			&localListenPort,
		); err != nil {
			return err
		}
		snapshotFound = true
		if currentPolicyRevision != policy.Revision {
			return errUpdaterPolicySnapshotChanged
		}
		if !targetID.Valid && !bindingPolicyRevision.Valid && !localListenPort.Valid {
			continue
		}
		if !targetID.Valid || !bindingPolicyRevision.Valid || !localListenPort.Valid {
			return ErrInvalidSettings
		}
		if bindingPolicyRevision.Int64 != policy.Revision {
			continue
		}
		index, exists := targetIndexes[targetID.String]
		if !exists ||
			localListenPort.Int64 < 1024 ||
			localListenPort.Int64 > 65535 ||
			!updaterPolicyTargetAllowsExplicitLocalListener(policy.Targets[index]) {
			return ErrInvalidSettings
		}
		listeners[index] = int(localListenPort.Int64)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !snapshotFound {
		return errUpdaterPolicySnapshotChanged
	}
	candidate := cloneUpdaterPolicy(*policy)
	for index := range candidate.Targets {
		candidate.Targets[index].LocalListenPort = listeners[index]
	}
	*policy = candidate
	return nil
}

func replaceUpdaterTargetLocalListeners(
	ctx context.Context,
	execer updaterPolicyExecer,
	policy UpdaterPolicy,
) error {
	if execer == nil ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.Revision < 1 {
		return ErrInvalidSettings
	}
	for _, target := range policy.Targets {
		if target.LocalListenPort == 0 {
			continue
		}
		if target.LocalListenPort < 1024 ||
			target.LocalListenPort > 65535 ||
			!updaterPolicyTargetAllowsExplicitLocalListener(target) {
			return ErrInvalidSettings
		}
	}
	if _, err := execer.ExecContext(
		ctx,
		`DELETE FROM update_agent_target_local_listeners WHERE updater_service_id = ?`,
		policy.UpdaterID,
	); err != nil {
		return err
	}
	for _, target := range policy.Targets {
		if target.LocalListenPort == 0 {
			continue
		}
		if _, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_target_local_listeners
(updater_service_id, target_id, binding_policy_revision, local_listen_port, updated_at)
VALUES (?, ?, ?, ?, ?)`,
			policy.UpdaterID,
			target.TargetID,
			policy.Revision,
			target.LocalListenPort,
			policy.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}
