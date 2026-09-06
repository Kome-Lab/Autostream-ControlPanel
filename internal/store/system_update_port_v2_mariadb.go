package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

// The dependent record is the only v2 plan authority. This projection adds one
// nullable column to every existing job reader, including restart/replay reads.
const systemUpdatePortTransactionProjection = `(SELECT CONCAT('{"Plan":', p.plan_json,
 ',"Before":', p.before_json, ',"Target":', p.target_json, ',"Rollback":', p.rollback_json,
 ',"RequestSHA256":', JSON_QUOTE(p.request_sha256), ',"CancelEndpointRevision":', p.cancel_endpoint_revision,
 ',"Phase":', JSON_QUOTE(p.phase), ',"RecoveryRequired":', IF(p.recovery_required, 'true', 'false'),
 ',"AcceptedResult":', COALESCE(p.accepted_result_json, 'null'),
 ',"LastRecoveryObservation":', COALESCE(p.last_recovery_observation_json, 'null'), '}')
 FROM system_update_port_transactions p WHERE p.job_id = system_update_jobs.id)`

func decodeSystemUpdatePortTransaction(job *SystemUpdateJob, body string) error {
	if body == "" {
		return nil
	}
	if len(body) > 4<<20 {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	var value systemUpdatePortTransaction
	if json.Unmarshal([]byte(body), &value) != nil || value.Plan == nil || value.Plan.PortContractVersion != 2 {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	for _, snapshot := range []SystemUpdatePortPolicySnapshot{value.Before, value.Target, value.Rollback} {
		computed, err := systemUpdatePortSnapshotWithRef(snapshot.Snapshot, job.TargetID)
		if err != nil || !reflect.DeepEqual(computed.Ref, snapshot.Ref) {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
	}
	if !reflect.DeepEqual(value.Plan.Before, &value.Before.Ref) || !reflect.DeepEqual(value.Plan.Target, &value.Target.Ref) || !reflect.DeepEqual(value.Plan.Rollback, &value.Rollback.Ref) {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if contracts.ValidateSystemUpdatePortPlan(systemUpdatePortContractsPlan(value.Plan)) != nil || value.Before.Snapshot.UpdaterID != job.AgentServiceID || value.Before.Snapshot.HostID != job.ExecutionHostID || value.Before.Snapshot.OwnershipEpoch != job.OwnershipEpoch || value.Before.Ref.ProjectionRevision != job.PolicyRevision {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	switch value.Phase {
	case "created", "consumed", "canceled", "premutation_failed":
		if value.AcceptedResult != nil || value.RecoveryRequired {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
	case "rollback_latched":
		if value.AcceptedResult != nil || !value.RecoveryRequired || value.LastRecoveryObservation == nil {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
	case "accepted":
		if value.AcceptedResult == nil || value.RecoveryRequired || !contracts.IsAcceptedSystemUpdatePortResult(*value.AcceptedResult) || contracts.ValidateSystemUpdatePortResult(systemUpdatePortContractsPlan(value.Plan), *value.AcceptedResult) != nil {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
	default:
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if value.LastRecoveryObservation != nil && (value.LastRecoveryObservation.Result != contracts.SystemUpdatePortReconfigurationRollbackFailed || contracts.ValidateSystemUpdatePortResult(systemUpdatePortContractsPlan(value.Plan), *value.LastRecoveryObservation) != nil) {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	job.portTransaction = &value
	projectSystemUpdatePortTransaction(job)
	return nil
}

func loadMariaDBSystemUpdatePortPolicy(ctx context.Context, tx *sql.Tx, updaterID string, locked bool) (UpdaterPolicy, error) {
	query := `SELECT revision, projection_revision, local_executor_policy_revision, policy_json, updated_at FROM update_agent_policies WHERE service_id = ?`
	if locked {
		query += ` FOR UPDATE`
	}
	var revision, projection, executor int64
	var body []byte
	var updated time.Time
	if err := tx.QueryRowContext(ctx, query, updaterID).Scan(&revision, &projection, &executor, &body, &updated); err != nil {
		return UpdaterPolicy{}, err
	}
	policy, err := decodeUpdaterPolicyRevisions(updaterID, revision, projection, executor, body, updated)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := restoreMariaDBSystemUpdatePortBindings(ctx, tx, &policy, locked); err != nil {
		return UpdaterPolicy{}, err
	}
	return policy, nil
}

// Unlike general configure discovery, a port snapshot cannot omit stale,
// orphaned or missing rows. Both binding tables are checked before restoration.
func restoreMariaDBSystemUpdatePortBindings(ctx context.Context, tx *sql.Tx, policy *UpdaterPolicy, locked bool) error {
	indexes := map[string]int{}
	for i, target := range policy.Targets {
		if _, duplicate := indexes[target.TargetID]; duplicate {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		indexes[target.TargetID] = i
	}
	suffix := ""
	if locked {
		suffix = " FOR UPDATE"
	}
	rows, err := tx.QueryContext(ctx, `SELECT target_id, binding_policy_revision, database_name FROM update_agent_target_databases WHERE updater_service_id = ? ORDER BY target_id`+suffix, policy.UpdaterID)
	if err != nil {
		return err
	}
	databases := map[string]string{}
	for rows.Next() {
		var id, name string
		var revision int64
		if err := rows.Scan(&id, &revision, &name); err != nil {
			rows.Close()
			return err
		}
		i, ok := indexes[id]
		_, duplicate := databases[id]
		if !ok || duplicate || revision != policy.Revision || !updaterPolicyTargetRequiresDatabase(policy.Targets[i]) || !updaterPolicyDatabaseNamePattern.MatchString(name) {
			rows.Close()
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		databases[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT target_id, binding_policy_revision, local_listen_port FROM update_agent_target_local_listeners WHERE updater_service_id = ? ORDER BY target_id`+suffix, policy.UpdaterID)
	if err != nil {
		return err
	}
	listeners := map[string]int{}
	for rows.Next() {
		var id string
		var revision int64
		var port int
		if err := rows.Scan(&id, &revision, &port); err != nil {
			rows.Close()
			return err
		}
		i, ok := indexes[id]
		_, duplicate := listeners[id]
		if !ok || duplicate || revision != policy.Revision || !updaterPolicyTargetAllowsExplicitLocalListener(policy.Targets[i]) || port < 1024 || port > 65535 {
			rows.Close()
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		listeners[id] = port
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := range policy.Targets {
		target := &policy.Targets[i]
		if updaterPolicyTargetRequiresDatabase(*target) && databases[target.TargetID] == "" {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if updaterPolicyTargetAllowsExplicitLocalListener(*target) && listeners[target.TargetID] == 0 {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		target.DatabaseName = databases[target.TargetID]
		target.LocalListenPort = listeners[target.TargetID]
	}
	return nil
}

func loadMariaDBPortServices(ctx context.Context, tx *sql.Tx, policy UpdaterPolicy, locked bool, synthetic *PullUpdaterControlPanelTarget) (map[string]RegisteredService, error) {
	ids := mariaDBUpdaterPolicyPersistentServiceIDs(policy)
	ids = sortedUniqueStrings(append(ids, policy.UpdaterID))
	discovered := map[string]RegisteredService{}
	tokenIDs := []string{}
	for _, id := range ids {
		service, err := scanService(tx.QueryRowContext(ctx, serviceSelectColumns+` FROM services WHERE service_id = ?`, id))
		if err != nil {
			return nil, err
		}
		discovered[id] = service
		if service.TokenID != "" {
			tokenIDs = append(tokenIDs, service.TokenID)
		}
	}
	if locked {
		tokenIDs = sortedUniqueStrings(tokenIDs)
		references, err := discoverMariaDBServiceTokenReferences(ctx, tx, tokenIDs)
		if err != nil {
			return nil, err
		}
		services, tokens, err := lockMariaDBServiceTokenMutation(ctx, tx, "st_port_snapshot", references, tokenIDs, ids...)
		if err != nil {
			return nil, err
		}
		for id, before := range discovered {
			current, ok := services[id]
			if !ok || current.TokenID != before.TokenID {
				return nil, ErrSystemUpdatePortSnapshotStale
			}
		}
		agent := services[policy.UpdaterID]
		token, ok := tokens[agent.TokenID]
		if !ok || token.RevokedAt != nil {
			return nil, ErrSystemUpdateAgentInactive
		}
		discovered = services
	}
	if synthetic != nil {
		service := synthetic.registeredService()
		service.AppliedEndpointRevision = service.EndpointRevision
		discovered[service.ServiceID] = service
	}
	return discovered, nil
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdatePortPolicySnapshot(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params SystemUpdatePortSnapshotParams) (SystemUpdatePortPolicySnapshot, error) {
	registry, ok := mariaDBFromServiceRegistryStore(services)
	policyDB, pok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || !pok || registry != s.db || policyDB != s.db {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	defer tx.Rollback()
	selected, _, err := mariaDBPullPolicyForPortTarget(ctx, tx, params.TargetID, false)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	policy, err := loadMariaDBSystemUpdatePortPolicy(ctx, tx, selected.UpdaterID, false)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	host, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(ctx, systemUpdateExecutionHostSelect+` WHERE execution_host_id = ?`, policy.ExecutionHostID))
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	all, err := loadMariaDBPortServices(ctx, tx, policy, false, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	ordered, err := sortedPortServices(all, policy, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	result, err := restoreSystemUpdatePortPolicySnapshot(policy, host, ordered, params)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	return result, nil
}

func rejectMariaDBSystemUpdatePortHold(ctx context.Context, tx *sql.Tx, hostID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT j.id FROM system_update_jobs j JOIN system_update_port_transactions p ON p.job_id=j.id WHERE j.execution_host_id=? AND (j.status NOT IN ('succeeded','rolled_back','failed','canceled') OR p.recovery_required=1) ORDER BY j.id LIMIT 1 FOR UPDATE`, hostID).Scan(&id)
	if err == nil {
		return ErrSystemUpdateExecutionHostBusy
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func lockMariaDBPortHostLane(ctx context.Context, tx *sql.Tx, hostID, allowJobID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM system_update_jobs WHERE execution_host_id = ? AND (status NOT IN ('succeeded','rolled_back','failed','canceled') OR EXISTS (SELECT 1 FROM system_update_port_transactions p WHERE p.job_id = system_update_jobs.id AND p.recovery_required = 1)) ORDER BY id FOR UPDATE`, hostID)
	if err != nil {
		return err
	}
	busy := false
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if id != allowJobID {
			busy = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if busy {
		return ErrSystemUpdateExecutionHostBusy
	}
	for _, query := range []string{`SELECT id FROM system_update_runtime_token_rotations WHERE active_execution_host_id = ? LIMIT 1 FOR UPDATE`, `SELECT id FROM system_update_host_self_updates WHERE active_execution_host_id = ? LIMIT 1 FOR UPDATE`} {
		var id string
		err := tx.QueryRowContext(ctx, query, hostID).Scan(&id)
		if err == nil {
			return ErrSystemUpdateExecutionHostBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *MariaDBSystemUpdateStore) ConfirmSystemUpdatePortPolicyBaseline(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params ConfirmSystemUpdatePortPolicyBaselineParams) error {
	registry, ok := mariaDBFromServiceRegistryStore(services)
	policyDB, pok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || !pok || registry != s.db || policyDB != s.db {
		return ErrSystemUpdatePortStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	policy, err := loadMariaDBSystemUpdatePortPolicy(ctx, tx, params.AgentServiceID, false)
	if err != nil {
		return err
	}
	host, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, policy.ExecutionHostID)
	if err != nil {
		return err
	}
	if err := lockMariaDBPortHostLane(ctx, tx, host.ExecutionHostID, ""); err != nil {
		return err
	}
	policy, err = loadMariaDBSystemUpdatePortPolicy(ctx, tx, params.AgentServiceID, true)
	if err != nil {
		return err
	}
	all, err := loadMariaDBPortServices(ctx, tx, policy, true, params.ControlPanelTarget)
	if err != nil {
		return err
	}
	ordered, err := sortedPortServices(all, policy, params.ControlPanelTarget)
	if err != nil {
		return err
	}
	updates, err := validateSystemUpdatePortBaseline(policy, host, ordered, params)
	if err != nil {
		return err
	}
	for id, revision := range updates {
		if id == "control-panel" {
			continue
		}
		if all[id].AppliedEndpointRevision != 0 {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE services SET applied_endpoint_revision = ? WHERE service_id = ? AND applied_endpoint_revision IS NULL AND endpoint_revision = ? AND applied_config_revision = ? AND applied_config_sha256 = ?`, revision, id, all[id].EndpointRevision, all[id].AppliedConfigRevision, all[id].AppliedConfigSHA256)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrSystemUpdatePortSnapshotStale
		}
	}
	return tx.Commit()
}

func (s *MariaDBSystemUpdateStore) createSystemUpdatePortV2(ctx context.Context, services ServiceRegistryStore, policies UpdaterPolicyStore, params CreateSystemdPortReconfigurationJobParams) (SystemUpdateJob, bool, error) {
	params = normalizeCreateSystemdPortReconfigurationJobParams(params)
	if err := validateCreateSystemUpdatePortV2Params(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	registry, ok := mariaDBFromServiceRegistryStore(services)
	policyDB, pok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || !pok || registry != s.db || policyDB != s.db {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	// Nonlocking lookup is only a discovery/replay optimization. New insertion
	// repeats idempotency under the host lock before acquiring any policy lock.
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_replay_lookup")
	if existing, err := s.GetSystemUpdateJobByIdempotency(ctx, params.RequestedByUserID, params.IdempotencyKey); err == nil {
		if existing.portTransaction != nil && existing.portTransaction.RequestSHA256 == systemUpdatePortV2RequestDigest(params) {
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrSystemUpdatePortIdempotencyConflict
	} else if !errors.Is(err, ErrNotFound) {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_begin_tx")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	defer tx.Rollback()
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_policy_discovery")
	discovered, _, err := mariaDBPullPolicyForPortTarget(ctx, tx, params.TargetID, false)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", mariaDBUpdaterPolicyBeforeHostLock)
	host, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, discovered.ExecutionHostID)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_idempotency_lock")
	existing, err := scanSystemUpdateJob(tx.QueryRowContext(ctx, systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ? FOR UPDATE`, params.RequestedByUserID, params.IdempotencyKey))
	if err == nil {
		if existing.portTransaction != nil && existing.portTransaction.RequestSHA256 == systemUpdatePortV2RequestDigest(params) {
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrSystemUpdatePortIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_lane_locks")
	if err := lockMariaDBPortHostLane(ctx, tx, host.ExecutionHostID, ""); err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", mariaDBUpdaterPolicyBeforePolicyLocks)
	policy, err := loadMariaDBSystemUpdatePortPolicy(ctx, tx, discovered.UpdaterID, true)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if policy.ExecutionHostID != host.ExecutionHostID {
		return SystemUpdateJob{}, false, ErrSystemUpdateOwnershipConflict
	}
	var target UpdaterPolicyTarget
	for _, candidate := range policy.Targets {
		if candidate.ServiceID == params.TargetID {
			target = candidate
		}
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_service_token_locks")
	all, err := loadMariaDBPortServices(ctx, tx, policy, true, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_snapshot_validation")
	ordered, err := sortedPortServices(all, policy, params.ControlPanelTarget)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	service, agent := all[params.TargetID], all[policy.UpdaterID]
	now := time.Now().UTC()
	if err := validateSystemUpdatePortV2Ready(policy, target, service, agent, host, params, now); err != nil {
		return SystemUpdateJob{}, false, err
	}
	before, err := restoreSystemUpdatePortPolicySnapshot(policy, host, ordered, SystemUpdatePortSnapshotParams{TargetID: params.TargetID, ControlPanelTarget: params.ControlPanelTarget, BuildPolicySnapshot: params.BuildPolicySnapshot})
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(policy, params.TargetID, params.NewLocalListenPort, params.ControlPanelTarget); err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_reservation_locks")
	if err := validateMariaDBPortReservationsForUpdate(ctx, tx, host.ExecutionHostID, params.TargetID, before.Ref.LocalListenPort, params.NewLocalListenPort); err != nil {
		return SystemUpdateJob{}, false, err
	}
	job, err := systemUpdatePortV2Job(params, before, service, agent, host, now)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_job_insert")
	_, err = tx.ExecContext(ctx, `INSERT INTO system_update_jobs (id,target_id,target_service_type,operation,port_contract_version,agent_service_id,execution_host_id,transport_mode,ownership_epoch,policy_revision,deployment_mode,current_version,target_version,strategy,status,idempotency_key,requested_by_user_id,requested_by_username,sequence,progress,created_at,updated_at) VALUES (?,?,?,'port_reconfigure',2,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,?,?)`, job.ID, job.TargetID, job.TargetServiceType, job.AgentServiceID, job.ExecutionHostID, job.TransportMode, job.OwnershipEpoch, job.PolicyRevision, job.DeploymentMode, job.CurrentVersion, job.TargetVersion, job.Strategy, job.Status, job.IdempotencyKey, job.RequestedByUserID, job.RequestedByUsername, now, now)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_transaction_insert")
	if err := insertMariaDBSystemUpdatePortTransaction(ctx, tx, job, now); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if !systemUpdatePortV2NoOp(job) {
		if before.Ref.LocalListenPort != params.NewLocalListenPort {
			observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_pending_reservation_insert")
			_, err = tx.ExecContext(ctx, `INSERT INTO service_port_reservations (execution_host_id,network_namespace,protocol,port,service_id,service_role,created_at,updated_at) VALUES (?,'host','tcp',?,?,'api_pending',?,?)`, host.ExecutionHostID, params.NewLocalListenPort, params.TargetID, now, now)
			if isDuplicateKeyError(err) {
				return SystemUpdateJob{}, false, ErrServicePortReserved
			}
			if err != nil {
				return SystemUpdateJob{}, false, err
			}
		}
		for _, state := range job.portTransaction.Target.Snapshot.Targets {
			if state.ServiceID == params.TargetID {
				observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_endpoint_write")
				_, err = tx.ExecContext(ctx, `UPDATE services SET desired_host=?,desired_port=?,desired_ssl_enabled=?,desired_public_url=?,endpoint_revision=?,endpoint_status='pending',updated_at=? WHERE service_id=?`, state.DesiredEndpoint.Host, state.DesiredEndpoint.Port, state.DesiredEndpoint.SSLEnabled, state.DesiredEndpoint.PublicURL, state.EndpointRevision, now, params.TargetID)
				if err != nil {
					return SystemUpdateJob{}, false, err
				}
			}
		}
	}
	observeMariaDBUpdaterPolicyLockPhase(ctx, "st_port_create", "before_commit")
	if err := tx.Commit(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	return job, true, nil
}

func insertMariaDBSystemUpdatePortTransaction(ctx context.Context, tx *sql.Tx, job SystemUpdateJob, now time.Time) error {
	value := job.portTransaction
	plan, _ := json.Marshal(value.Plan)
	before, _ := json.Marshal(value.Before)
	target, _ := json.Marshal(value.Target)
	rollback, _ := json.Marshal(value.Rollback)
	if len(before) > 1<<20 || len(target) > 1<<20 || len(rollback) > 1<<20 || len(before)+len(target)+len(rollback)+len(plan) > 4<<20 {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO system_update_port_transactions (job_id,execution_host_id,updater_service_id,target_id,ownership_epoch,job_policy_revision,port_contract_version,mode,request_sha256,intent_sha256,plan_json,before_json,target_json,rollback_json,cancel_endpoint_revision,phase,recovery_required,created_at,updated_at) VALUES (?,?,?,?,?,?,2,?,?,?,?,?,?,?,?,?,0,?,?)`, job.ID, job.ExecutionHostID, job.AgentServiceID, job.TargetID, job.OwnershipEpoch, job.PolicyRevision, job.PortReconfigure.Mode, value.RequestSHA256, job.PortReconfigure.PortPlanSHA256, plan, before, target, rollback, value.CancelEndpointRevision, value.Phase, now, now)
	return err
}

func saveMariaDBSystemUpdatePortTransaction(ctx context.Context, tx *sql.Tx, before SystemUpdateJob, after SystemUpdateJob, now time.Time) error {
	value := after.portTransaction
	var accepted, last any
	if value.AcceptedResult != nil {
		body, _ := json.Marshal(value.AcceptedResult)
		accepted = body
	}
	if value.LastRecoveryObservation != nil {
		body, _ := json.Marshal(value.LastRecoveryObservation)
		last = body
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_port_transactions SET phase=?,recovery_required=?,accepted_result_json=?,last_recovery_observation_json=?,updated_at=? WHERE job_id=? AND execution_host_id=? AND ownership_epoch=? AND job_policy_revision=? AND intent_sha256=? AND phase=? AND accepted_result_json IS NULL`, value.Phase, value.RecoveryRequired, accepted, last, now, after.ID, after.ExecutionHostID, after.OwnershipEpoch, after.PolicyRevision, after.PortReconfigure.PortPlanSHA256, before.portTransaction.Phase)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrSystemUpdatePortResultMismatch
	}
	return nil
}

func lockMariaDBPortV2State(ctx context.Context, tx *sql.Tx, job SystemUpdateJob) (UpdaterPolicy, map[string]RegisteredService, error) {
	host, err := getSystemUpdateExecutionHostForUpdate(ctx, tx, job.ExecutionHostID)
	if err != nil || !systemUpdatePortV2OwnershipMatches(job, host) {
		return UpdaterPolicy{}, nil, ErrSystemUpdateOwnershipConflict
	}
	if err := lockMariaDBPortHostLane(ctx, tx, job.ExecutionHostID, job.ID); err != nil {
		return UpdaterPolicy{}, nil, err
	}
	policy, err := loadMariaDBSystemUpdatePortPolicy(ctx, tx, job.AgentServiceID, true)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	all, err := loadMariaDBPortServices(ctx, tx, policy, true, nil)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	expected := job.portTransaction.Before
	if job.portTransaction.Phase == "consumed" || job.portTransaction.Phase == "rollback_latched" {
		expected = job.portTransaction.Target
	}
	if !portPolicyMatchesSnapshot(policy, expected) {
		return UpdaterPolicy{}, nil, ErrSystemUpdatePortSnapshotStale
	}
	// Reuse the exact Memory validator with a temporary value, not another
	// authority. No mutex or mutable registry escapes this SQL transaction.
	validator := &MemorySystemUpdateStore{portPolicyStore: &MemoryUpdaterPolicyStore{policies: map[string]UpdaterPolicy{job.AgentServiceID: policy}}}
	if err := validateMemorySystemUpdatePortV2StateLocked(validator, &MemoryAuthStore{services: all}, job); err != nil {
		return UpdaterPolicy{}, nil, err
	}
	oldPort, newPort := job.PortReconfigure.Before.LocalListenPort, job.PortReconfigure.Target.LocalListenPort
	ports := []int{oldPort}
	if oldPort != newPort {
		ports = append(ports, newPort)
	}
	sort.Ints(ports)
	for _, port := range ports {
		reservation, err := scanServicePortReservation(tx.QueryRowContext(ctx, servicePortReservationSelect+` WHERE execution_host_id=? AND network_namespace='host' AND protocol='tcp' AND port=? FOR UPDATE`, job.ExecutionHostID, port))
		if err != nil {
			return UpdaterPolicy{}, nil, err
		}
		role := systemUpdatePortCurrentRole
		if port == newPort && oldPort != newPort {
			role = systemUpdatePortPendingRole
		}
		if reservation.ServiceID != job.TargetID || reservation.ServiceRole != role {
			return UpdaterPolicy{}, nil, ErrSystemUpdatePortSnapshotStale
		}
	}
	return policy, all, nil
}

func saveMariaDBPortPolicySnapshot(ctx context.Context, tx *sql.Tx, job SystemUpdateJob, before UpdaterPolicy, next SystemUpdatePortPolicySnapshot, now time.Time) error {
	policy, err := portSnapshotPolicy(next)
	if err != nil {
		return err
	}
	if portPolicyMatchesSnapshot(before, next) {
		return nil
	}
	policy.UpdatedAt = now
	body, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if err := saveUpdaterPolicyCAS(ctx, tx, before.Revision, policy, body); err != nil {
		return err
	}
	if err := replaceUpdaterTargetDatabases(ctx, tx, policy); err != nil {
		return err
	}
	if err := replaceUpdaterTargetLocalListeners(ctx, tx, policy); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts SET policy_revision=?,updated_at=? WHERE execution_host_id=? AND ownership_epoch=? AND policy_revision=? AND agent_service_id=?`, policy.ProjectionRevision, now, job.ExecutionHostID, job.OwnershipEpoch, before.ProjectionRevision, job.AgentServiceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrSystemUpdateOwnershipConflict
	}
	return nil
}

func consumeMariaDBSystemUpdatePortV2(ctx context.Context, tx *sql.Tx, job SystemUpdateJob, operation string, now time.Time) error {
	policy, _, err := lockMariaDBPortV2State(ctx, tx, job)
	if err != nil {
		return err
	}
	if job.PortResult != nil || systemUpdatePortV2NoOp(job) {
		return ErrSystemUpdateAuthorizationState
	}
	after := job
	after.portTransaction = cloneSystemUpdatePortTransaction(job.portTransaction)
	if operation == SystemUpdateMutationOperationPortReconfigure {
		if after.portTransaction.Phase != "created" || after.RecoveryRequired {
			return ErrSystemUpdateAuthorizationState
		}
		if err := saveMariaDBPortPolicySnapshot(ctx, tx, job, policy, job.portTransaction.Target, now); err != nil {
			return err
		}
		after.portTransaction.Phase = "consumed"
	} else if operation != SystemUpdateMutationOperationPortReconfigureReconcile || (after.portTransaction.Phase != "consumed" && after.portTransaction.Phase != "rollback_latched") {
		return ErrSystemUpdateAuthorizationState
	}
	if after.portTransaction.Phase == job.portTransaction.Phase {
		return nil
	}
	return saveMariaDBSystemUpdatePortTransaction(ctx, tx, job, after, now)
}

func finishMariaDBSystemUpdatePortV2(ctx context.Context, tx *sql.Tx, job *SystemUpdateJob, report SystemUpdateReport, now time.Time) error {
	policy, all, err := lockMariaDBPortV2State(ctx, tx, *job)
	if err != nil {
		return err
	}
	before := *job
	value := cloneSystemUpdatePortTransaction(job.portTransaction)
	job.portTransaction = value
	if report.PortResult == nil {
		if err := cancelMariaDBSystemUpdatePortV2(ctx, tx, *job, now); err != nil {
			return err
		}
		value.Phase = "premutation_failed"
	} else if string(report.PortResult.Result) == "rollback_failed" {
		if value.Phase != "consumed" && value.Phase != "rollback_latched" {
			return ErrSystemUpdatePortResultMismatch
		}
		value.Phase = "rollback_latched"
		value.RecoveryRequired = true
		value.LastRecoveryObservation = cloneSystemUpdatePortV2Result(report.PortResult)
		if _, err := tx.ExecContext(ctx, `UPDATE services SET endpoint_status='rollback_failed',updated_at=? WHERE service_id=?`, now, job.TargetID); err != nil {
			return err
		}
	} else {
		final := value.Target
		switch string(report.PortResult.Result) {
		case "unchanged":
			if !systemUpdatePortV2NoOp(*job) || value.Phase != "created" {
				return ErrSystemUpdatePortResultMismatch
			}
			final = value.Before
		case "applied":
			if value.Phase != "consumed" || value.RecoveryRequired {
				return ErrSystemUpdatePortResultMismatch
			}
		case "rolled_back":
			if value.Phase != "consumed" && value.Phase != "rollback_latched" {
				return ErrSystemUpdatePortResultMismatch
			}
			final = value.Rollback
		default:
			return ErrSystemUpdatePortResultMismatch
		}
		if !systemUpdatePortV2NoOp(*job) {
			if err := saveMariaDBPortPolicySnapshot(ctx, tx, *job, policy, final, now); err != nil {
				return err
			}
			for _, state := range final.Snapshot.Targets {
				if state.ServiceID != job.TargetID {
					continue
				}
				status := "applied"
				if string(report.PortResult.Result) == "rolled_back" {
					status = "rolled_back"
				}
				_, err := tx.ExecContext(ctx, `UPDATE services SET host=?,port=?,ssl_enabled=?,public_url=?,desired_host=?,desired_port=?,desired_ssl_enabled=?,desired_public_url=?,endpoint_revision=?,applied_endpoint_revision=?,applied_config_revision=?,applied_config_sha256=?,endpoint_status=?,updated_at=? WHERE service_id=?`, state.AppliedEndpoint.Host, state.AppliedEndpoint.Port, state.AppliedEndpoint.SSLEnabled, state.AppliedEndpoint.PublicURL, state.DesiredEndpoint.Host, state.DesiredEndpoint.Port, state.DesiredEndpoint.SSLEnabled, state.DesiredEndpoint.PublicURL, state.EndpointRevision, state.AppliedEndpointRevision, state.ConfigRevision, state.ConfigSHA256, status, now, job.TargetID)
				if err != nil {
					return err
				}
			}
			oldPort, newPort := value.Before.Ref.LocalListenPort, value.Target.Ref.LocalListenPort
			if oldPort != newPort {
				if string(report.PortResult.Result) == "applied" {
					if _, err := tx.ExecContext(ctx, `DELETE FROM service_port_reservations WHERE execution_host_id=? AND network_namespace='host' AND protocol='tcp' AND port=? AND service_id=? AND service_role='api'`, job.ExecutionHostID, oldPort, job.TargetID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE service_port_reservations SET service_role='api',updated_at=? WHERE execution_host_id=? AND network_namespace='host' AND protocol='tcp' AND port=? AND service_id=? AND service_role='api_pending'`, now, job.ExecutionHostID, newPort, job.TargetID); err != nil {
						return err
					}
				} else {
					if _, err := tx.ExecContext(ctx, `DELETE FROM service_port_reservations WHERE execution_host_id=? AND network_namespace='host' AND protocol='tcp' AND port=? AND service_id=? AND service_role='api_pending'`, job.ExecutionHostID, newPort, job.TargetID); err != nil {
						return err
					}
				}
			}
		}
		value.AcceptedResult = cloneSystemUpdatePortV2Result(report.PortResult)
		value.RecoveryRequired = false
		value.Phase = "accepted"
	}
	_ = all
	projectSystemUpdatePortTransaction(job)
	return saveMariaDBSystemUpdatePortTransaction(ctx, tx, before, *job, now)
}

func cancelMariaDBSystemUpdatePortV2(ctx context.Context, tx *sql.Tx, job SystemUpdateJob, now time.Time) error {
	if job.portTransaction == nil || job.portTransaction.Phase != "created" || job.PortResult != nil {
		return ErrSystemUpdateNotCancellable
	}
	if _, _, err := lockMariaDBPortV2State(ctx, tx, job); err != nil {
		return err
	}
	if !systemUpdatePortV2NoOp(job) {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET desired_host=host,desired_port=port,desired_ssl_enabled=ssl_enabled,desired_public_url=public_url,endpoint_revision=?,endpoint_status='applied',updated_at=? WHERE service_id=?`, job.portTransaction.CancelEndpointRevision, now, job.TargetID); err != nil {
			return err
		}
		if job.PortReconfigure.Before.LocalListenPort != job.PortReconfigure.Target.LocalListenPort {
			if _, err := tx.ExecContext(ctx, `DELETE FROM service_port_reservations WHERE execution_host_id=? AND network_namespace='host' AND protocol='tcp' AND port=? AND service_id=? AND service_role='api_pending'`, job.ExecutionHostID, job.PortReconfigure.Target.LocalListenPort, job.TargetID); err != nil {
				return err
			}
		}
	}
	return nil
}
