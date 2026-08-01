package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *MemorySystemUpdateStore) CreateSystemdPortReconfigurationJob(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateSystemdPortReconfigurationJobParams,
) (SystemUpdateJob, bool, error) {
	params = normalizeCreateSystemdPortReconfigurationJobParams(params)
	if err := validateCreateSystemdPortReconfigurationJobParams(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	registry, ok := services.(*MemoryAuthStore)
	if !ok || registry == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	policyStore, ok := policies.(*MemoryUpdaterPolicyStore)
	if !ok || policyStore == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}

	// Match the established updater-policy transaction order.
	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, false, err
	}

	if existing, found := memorySystemUpdateByIdempotencyLocked(s, params.RequestedByUserID, params.IdempotencyKey); found {
		if sameSystemdPortCreateRequest(existing, params) {
			return publicMemorySystemUpdateJob(existing), false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	policy, policyTarget, err := memoryPullPolicyForPortTargetLocked(policyStore, params.TargetID)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	target, exists := registry.services[policyTarget.ServiceID]
	if !exists {
		return SystemUpdateJob{}, false, ErrNotFound
	}
	agent, exists := registry.services[policy.UpdaterID]
	if !exists {
		return SystemUpdateJob{}, false, ErrSystemUpdateAgentInactive
	}
	ownership, exists := s.executionHosts[policy.ExecutionHostID]
	if !exists {
		return SystemUpdateJob{}, false, ErrSystemUpdateOwnershipConflict
	}
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(s, ownership.ExecutionHostID); found {
		return SystemUpdateJob{}, false, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	now := time.Now().UTC()
	if err := validateSystemdPortCoordinatorState(policy, policyTarget, target, agent, ownership, params, now); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(
		policy,
		params.TargetID,
		params.NewPort,
		params.ControlPanelTarget,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateMemoryPortReservationsLocked(s, ownership.ExecutionHostID, target.ServiceID, target.AppliedEndpoint.Port, params.NewPort); err != nil {
		return SystemUpdateJob{}, false, err
	}
	for _, existing := range s.jobs {
		if existing.TargetID == target.ServiceID && !isTerminalSystemUpdateStatus(existing.Status) {
			return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
		}
	}

	job, err := systemUpdatePortJobFromState(params, target, agent, policy, ownership, now)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	desiredEndpoint, err := systemUpdatePortDesiredEndpoint(job, target.AppliedEndpoint)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	pending := ServicePortReservation{
		ExecutionHostID: ownership.ExecutionHostID, NetworkNamespace: systemUpdatePortNetworkNamespace,
		Protocol: systemUpdatePortProtocol, Port: params.NewPort, ServiceID: target.ServiceID,
		ServiceRole: systemUpdatePortPendingRole, CreatedAt: now, UpdatedAt: now,
	}
	if s.jobs == nil {
		s.jobs = map[string]SystemUpdateJob{}
	}
	if s.portReservations == nil {
		s.portReservations = map[servicePortReservationKey]ServicePortReservation{}
	}
	if s.portJobRegistries == nil {
		s.portJobRegistries = map[string]*MemoryAuthStore{}
	}
	s.jobs[job.ID] = job
	s.portReservations[servicePortKey(pending)] = pending
	s.portJobRegistries[job.ID] = registry
	target.DesiredEndpoint = desiredEndpoint
	target.EndpointRevision = job.PortReconfigure.TargetEndpointRevision
	target.EndpointStatus = "pending"
	target.UpdatedAt = now
	registry.services[target.ServiceID] = target
	return publicMemorySystemUpdateJob(job), true, nil
}

func memorySystemUpdateByIdempotencyLocked(s *MemorySystemUpdateStore, userID, key string) (SystemUpdateJob, bool) {
	for _, job := range s.jobs {
		if job.RequestedByUserID == userID && job.IdempotencyKey == key {
			return job, true
		}
	}
	return SystemUpdateJob{}, false
}

func memoryPullPolicyForPortTargetLocked(
	policies *MemoryUpdaterPolicyStore,
	targetID string,
) (UpdaterPolicy, UpdaterPolicyTarget, error) {
	var selectedPolicy UpdaterPolicy
	var selectedTarget UpdaterPolicyTarget
	matches := 0
	for _, policy := range policies.policies {
		for _, target := range policy.Targets {
			if target.ServiceID != targetID {
				continue
			}
			matches++
			selectedPolicy = cloneUpdaterPolicy(policy)
			selectedTarget = target
		}
	}
	if matches == 0 {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, ErrNotFound
	}
	if matches != 1 {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, ErrConflict
	}
	return selectedPolicy, selectedTarget, nil
}

func validateMemoryPortReservationsLocked(
	s *MemorySystemUpdateStore,
	hostID, serviceID string,
	oldPort, newPort int,
) error {
	currentKey := servicePortReservationKey{
		executionHostID: hostID, networkNamespace: systemUpdatePortNetworkNamespace,
		protocol: systemUpdatePortProtocol, port: oldPort,
	}
	current, exists := s.portReservations[currentKey]
	if !exists || current.ServiceID != serviceID || current.ServiceRole != systemUpdatePortCurrentRole {
		return ErrSystemUpdateEndpointStale
	}
	for _, reservation := range s.portReservations {
		if reservation.ExecutionHostID == hostID && reservation.ServiceID == serviceID &&
			reservation.ServiceRole == systemUpdatePortPendingRole {
			return ErrSystemUpdateTargetActive
		}
	}
	if oldPort == newPort {
		return nil
	}
	newKey := servicePortReservationKey{
		executionHostID: hostID, networkNamespace: systemUpdatePortNetworkNamespace,
		protocol: systemUpdatePortProtocol, port: newPort,
	}
	if _, exists := s.portReservations[newKey]; exists {
		return ErrServicePortReserved
	}
	return nil
}

func (s *MariaDBSystemUpdateStore) CreateSystemdPortReconfigurationJob(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateSystemdPortReconfigurationJobParams,
) (SystemUpdateJob, bool, error) {
	params = normalizeCreateSystemdPortReconfigurationJobParams(params)
	if err := validateCreateSystemdPortReconfigurationJobParams(params); err != nil {
		return SystemUpdateJob{}, false, err
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	policyDB, ok := mariaDBFromUpdaterPolicyStore(policies)
	if !ok || policyDB != s.db {
		return SystemUpdateJob{}, false, ErrSystemUpdatePortStoreMismatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	defer tx.Rollback()

	existing, err := scanSystemUpdateJob(tx.QueryRowContext(
		ctx, systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ? FOR UPDATE`,
		params.RequestedByUserID, params.IdempotencyKey,
	))
	if err == nil {
		if sameSystemdPortCreateRequest(existing, params) {
			if err := tx.Commit(); err != nil {
				return SystemUpdateJob{}, false, err
			}
			return existing, false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}

	// Target lookup is initially non-locking because the execution host is
	// policy-owned. Once discovered, take the durable host fence before any
	// policy/service/token lock, then re-read the selection with FOR UPDATE.
	discoveredPolicy, discoveredTarget, err := mariaDBPullPolicyForPortTarget(
		ctx, tx, params.TargetID, false,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	ownership, err := getSystemUpdateExecutionHostForUpdate(
		ctx, tx, discoveredPolicy.ExecutionHostID,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	var activeRotationID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, ownership.ExecutionHostID).Scan(&activeRotationID)
	if err == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}
	policy, policyTarget, err := mariaDBPullPolicyForPortTarget(
		ctx, tx, params.TargetID, true,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if !sameMariaDBPortPolicySelection(
		discoveredPolicy, discoveredTarget, policy, policyTarget,
	) || policy.ExecutionHostID != ownership.ExecutionHostID {
		return SystemUpdateJob{}, false, ErrSystemUpdateOwnershipConflict
	}
	serviceIDs := []string{policy.UpdaterID, policyTarget.ServiceID}
	sort.Strings(serviceIDs)
	lockedServices := make(map[string]RegisteredService, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		if _, exists := lockedServices[serviceID]; exists {
			continue
		}
		service, serviceErr := scanService(tx.QueryRowContext(
			ctx, serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`, serviceID,
		))
		if errors.Is(serviceErr, sql.ErrNoRows) {
			return SystemUpdateJob{}, false, ErrNotFound
		}
		if serviceErr != nil {
			return SystemUpdateJob{}, false, serviceErr
		}
		lockedServices[serviceID] = service
	}
	agent := lockedServices[policy.UpdaterID]
	target := lockedServices[policyTarget.ServiceID]
	if _, err := selectActiveServiceTokenForUpdate(ctx, tx, agent.TokenID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return SystemUpdateJob{}, false, ErrSystemUpdateAgentInactive
		}
		return SystemUpdateJob{}, false, err
	}
	now := time.Now().UTC()
	if err := validateSystemdPortCoordinatorState(policy, policyTarget, target, agent, ownership, params, now); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(
		policy,
		params.TargetID,
		params.NewPort,
		params.ControlPanelTarget,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateMariaDBPortReservationsForUpdate(
		ctx, tx, ownership.ExecutionHostID, target.ServiceID,
		target.AppliedEndpoint.Port, params.NewPort,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	var activeID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM system_update_jobs
WHERE active_target_id = ? LIMIT 1 FOR UPDATE`, target.ServiceID).Scan(&activeID)
	if err == nil {
		return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateJob{}, false, err
	}

	job, err := systemUpdatePortJobFromState(params, target, agent, policy, ownership, now)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	desiredEndpoint, err := systemUpdatePortDesiredEndpoint(job, target.AppliedEndpoint)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	port := job.PortReconfigure
	_, err = tx.ExecContext(ctx, `INSERT INTO system_update_jobs
(id, target_id, target_service_type, operation, network_namespace, protocol,
 old_port, new_port, expected_endpoint_revision, target_endpoint_revision,
 expected_config_revision, target_config_revision, expected_config_sha256,
 expected_source_policy_revision, target_config_sha256,
 expected_updater_policy_revision, expected_executor_policy_revision,
 expected_executor_policy_sha256, port_plan_sha256,
 agent_service_id, execution_host_id, transport_mode, ownership_epoch,
 policy_revision, deployment_mode, current_version, target_version, strategy,
 status, idempotency_key, requested_by_user_id, requested_by_username,
 sequence, progress, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		job.ID, job.TargetID, job.TargetServiceType, job.Operation,
		port.NetworkNamespace, port.Protocol, port.OldPort, port.NewPort,
		port.ExpectedEndpointRevision, port.TargetEndpointRevision,
		port.ExpectedConfigRevision, port.TargetConfigRevision,
		port.ExpectedConfigSHA256, port.ExpectedSourcePolicyRevision,
		port.TargetConfigSHA256, port.ExpectedUpdaterPolicyRevision,
		port.ExpectedExecutorPolicyRevision, port.ExpectedExecutorPolicySHA256,
		port.PortPlanSHA256, job.AgentServiceID, job.ExecutionHostID,
		job.TransportMode, job.OwnershipEpoch, job.PolicyRevision,
		job.DeploymentMode, job.CurrentVersion, job.TargetVersion, job.Strategy,
		job.Status, job.IdempotencyKey, job.RequestedByUserID,
		job.RequestedByUsername, job.CreatedAt, job.UpdatedAt,
	)
	if isDuplicateKeyError(err) {
		return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO service_port_reservations
(execution_host_id, network_namespace, protocol, port, service_id,
 service_role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ownership.ExecutionHostID, systemUpdatePortNetworkNamespace,
		systemUpdatePortProtocol, params.NewPort, target.ServiceID,
		systemUpdatePortPendingRole, now, now,
	)
	if isDuplicateKeyError(err) {
		return SystemUpdateJob{}, false, ErrServicePortReserved
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE services
SET desired_host = ?, desired_port = ?, desired_ssl_enabled = ?,
    desired_public_url = ?, endpoint_revision = ?,
    endpoint_status = 'pending', updated_at = ?
WHERE service_id = ?
  AND endpoint_revision = ?
  AND applied_config_revision = ?
  AND applied_config_sha256 = ?
  AND COALESCE(port, 0) = ?`,
		desiredEndpoint.Host, desiredEndpoint.Port, desiredEndpoint.SSLEnabled,
		desiredEndpoint.PublicURL, port.TargetEndpointRevision, now,
		target.ServiceID, port.ExpectedEndpointRevision,
		port.ExpectedConfigRevision, port.ExpectedConfigSHA256, port.OldPort,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if affected != 1 {
		return SystemUpdateJob{}, false, ErrSystemUpdateEndpointStale
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	return job, true, nil
}

func mariaDBFromServiceRegistryStore(store ServiceRegistryStore) (*sql.DB, bool) {
	switch value := store.(type) {
	case MariaDBAuthStore:
		return value.db, value.db != nil
	case *MariaDBAuthStore:
		return value.db, value != nil && value.db != nil
	default:
		return nil, false
	}
}

func mariaDBFromUpdaterPolicyStore(store UpdaterPolicyStore) (*sql.DB, bool) {
	switch value := store.(type) {
	case MariaDBUpdaterPolicyStore:
		return value.db, value.db != nil
	case *MariaDBUpdaterPolicyStore:
		return value.db, value != nil && value.db != nil
	default:
		return nil, false
	}
}

func mariaDBPullPolicyForPortTarget(
	ctx context.Context,
	tx *sql.Tx,
	targetID string,
	forUpdate bool,
) (UpdaterPolicy, UpdaterPolicyTarget, error) {
	query := `SELECT service_id, revision,
projection_revision, local_executor_policy_revision, policy_json, updated_at
FROM update_agent_policies
ORDER BY service_id`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, err
	}
	defer rows.Close()
	var selectedPolicy UpdaterPolicy
	var selectedTarget UpdaterPolicyTarget
	matches := 0
	for rows.Next() {
		var (
			serviceID                                      string
			revision, projectionRevision, executorRevision int64
			body                                           []byte
			updatedAt                                      time.Time
		)
		if err := rows.Scan(
			&serviceID, &revision, &projectionRevision, &executorRevision,
			&body, &updatedAt,
		); err != nil {
			return UpdaterPolicy{}, UpdaterPolicyTarget{}, err
		}
		policy, err := decodeUpdaterPolicyRevisions(
			serviceID, revision, projectionRevision, executorRevision, body, updatedAt,
		)
		if err != nil {
			return UpdaterPolicy{}, UpdaterPolicyTarget{}, err
		}
		for _, target := range policy.Targets {
			if target.ServiceID != targetID {
				continue
			}
			matches++
			selectedPolicy = policy
			selectedTarget = target
		}
	}
	if err := rows.Err(); err != nil {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, err
	}
	if matches == 0 {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, ErrNotFound
	}
	if matches != 1 {
		return UpdaterPolicy{}, UpdaterPolicyTarget{}, ErrConflict
	}
	return selectedPolicy, selectedTarget, nil
}

func sameMariaDBPortPolicySelection(
	discoveredPolicy UpdaterPolicy,
	discoveredTarget UpdaterPolicyTarget,
	lockedPolicy UpdaterPolicy,
	lockedTarget UpdaterPolicyTarget,
) bool {
	return discoveredPolicy.UpdaterID == lockedPolicy.UpdaterID &&
		discoveredPolicy.ExecutionHostID == lockedPolicy.ExecutionHostID &&
		discoveredTarget.TargetID == lockedTarget.TargetID &&
		discoveredTarget.ServiceID == lockedTarget.ServiceID &&
		discoveredTarget.HostID == lockedTarget.HostID
}

func validateMariaDBPortReservationsForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	hostID, serviceID string,
	oldPort, newPort int,
) error {
	current, err := scanServicePortReservation(tx.QueryRowContext(
		ctx, servicePortReservationSelect+`
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
FOR UPDATE`,
		hostID, systemUpdatePortNetworkNamespace, systemUpdatePortProtocol, oldPort,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSystemUpdateEndpointStale
	}
	if err != nil {
		return err
	}
	if current.ServiceID != serviceID || current.ServiceRole != systemUpdatePortCurrentRole {
		return ErrSystemUpdateEndpointStale
	}
	var pendingPort int
	err = tx.QueryRowContext(ctx, `SELECT port FROM service_port_reservations
WHERE execution_host_id = ? AND service_id = ? AND service_role = ?
LIMIT 1 FOR UPDATE`, hostID, serviceID, systemUpdatePortPendingRole).Scan(&pendingPort)
	if err == nil {
		return ErrSystemUpdateTargetActive
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if oldPort == newPort {
		return nil
	}
	_, err = scanServicePortReservation(tx.QueryRowContext(
		ctx, servicePortReservationSelect+`
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
FOR UPDATE`,
		hostID, systemUpdatePortNetworkNamespace, systemUpdatePortProtocol, newPort,
	))
	if err == nil {
		return ErrServicePortReserved
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func sameSystemdPortCreateRequest(job SystemUpdateJob, params CreateSystemdPortReconfigurationJobParams) bool {
	return job.Operation == SystemUpdateOperationPortReconfigure &&
		job.TargetID == params.TargetID &&
		job.PortReconfigure != nil &&
		job.PortReconfigure.NewPort == params.NewPort &&
		job.PortReconfigure.ExpectedEndpointRevision == params.ExpectedEndpointRevision
}

func validateSyntheticControlPanelHostPortFence(
	policy UpdaterPolicy,
	targetID string,
	requestedHostPort int,
	runtimeTarget *PullUpdaterControlPanelTarget,
) error {
	usesControlPanel := false
	for _, target := range policy.Targets {
		if updaterPolicyControlPanelTarget(target) {
			usesControlPanel = true
			break
		}
	}
	if !usesControlPanel {
		return nil
	}
	controlPanelTarget, err := normalizePullUpdaterControlPanelTarget(
		runtimeTarget,
	)
	if err != nil || controlPanelTarget == nil {
		return ErrSystemUpdateAgentNotReady
	}
	if targetID != controlPanelTarget.ServiceID &&
		requestedHostPort == controlPanelTarget.AppliedEndpoint.Port {
		return ErrServicePortReserved
	}
	return nil
}

func validateSystemdPortCoordinatorState(
	policy UpdaterPolicy,
	policyTarget UpdaterPolicyTarget,
	target RegisteredService,
	agent RegisteredService,
	ownership SystemUpdateExecutionHost,
	params CreateSystemdPortReconfigurationJobParams,
	now time.Time,
) error {
	if policyTarget.LocalListenPort != 0 {
		// Explicit local listener bindings are independent from advertised
		// endpoints. The legacy transaction rewrites both as one port and must
		// not run until it can update the revision-bound binding atomically.
		return ErrSystemUpdatePortUnsupported
	}
	if policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID == "" ||
		policy.Revision < 1 || policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(policy.LocalExecutorPolicySHA256) ||
		policy.LocalExecutorPolicySHA256 == "" ||
		policyTarget.ServiceID != params.TargetID ||
		policyTarget.DeploymentMode != "systemd" ||
		!supportedSystemdPortServiceType(policyTarget.ServiceType) ||
		target.ServiceID != policyTarget.ServiceID ||
		target.ServiceType != policyTarget.ServiceType ||
		target.ServiceType == "update_agent" ||
		target.EndpointRevision != params.ExpectedEndpointRevision ||
		(target.EndpointStatus != "applied" && target.EndpointStatus != "rolled_back") ||
		target.AppliedEndpoint == nil ||
		!sameServiceEndpoint(target.DesiredEndpoint, target.AppliedEndpoint) ||
		target.AppliedEndpoint.Port < 1024 || target.AppliedEndpoint.Port > 65535 ||
		target.AppliedConfigRevision < 1 ||
		!validSystemUpdateDigest(target.AppliedConfigSHA256) ||
		target.AppliedConfigSHA256 == "" {
		return ErrSystemUpdateEndpointStale
	}
	if ownership.ExecutionHostID != policy.ExecutionHostID ||
		ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != policy.UpdaterID ||
		ownership.OwnershipEpoch < 1 ||
		ownership.PolicyRevision != policy.ProjectionRevision ||
		agent.ServiceID != policy.UpdaterID ||
		agent.ServiceType != "update_agent" ||
		agent.TransportMode != SystemUpdateTransportPullV2 ||
		agent.ExecutionHostID != ownership.ExecutionHostID ||
		agent.OwnershipEpoch != ownership.OwnershipEpoch {
		return ErrSystemUpdateOwnershipConflict
	}
	if !pullAgentReadyForSystemdPortChange(agent, policy, target, now) {
		return ErrSystemUpdateAgentNotReady
	}
	return nil
}

func supportedSystemdPortServiceType(serviceType string) bool {
	_, ok := systemUpdatePortBindVariable(serviceType)
	return ok
}

func pullAgentReadyForSystemdPortChange(
	agent RegisteredService,
	policy UpdaterPolicy,
	target RegisteredService,
	now time.Time,
) bool {
	if agent.Status != "online" || agent.LastHeartbeatAt == nil ||
		now.Sub(agent.LastHeartbeatAt.UTC()) < 0 ||
		now.Sub(agent.LastHeartbeatAt.UTC()) > pullUpdaterActivationHeartbeatMaxAge {
		return false
	}
	capabilities := agent.ReportedCapabilities
	ownershipEpoch, ownershipEpochOK := updaterPolicyCapabilityInt64(capabilities["ownership_epoch"])
	policyRevision, policyRevisionOK := updaterPolicyCapabilityInt64(capabilities["policy_revision"])
	recoveryPending, recoveryPendingOK := capabilities["recovery_pending"].(bool)
	if !updaterPolicyCapabilityBool(capabilities["host_agent"]) ||
		!updaterPolicyCapabilityBool(capabilities["update_executor"]) ||
		!updaterPolicyCapabilityBool(capabilities["mutation_enabled"]) ||
		!recoveryPendingOK || recoveryPending ||
		updaterPolicyCapabilityString(capabilities["transport_mode"]) != SystemUpdateTransportPullV2 ||
		updaterPolicyCapabilityString(capabilities["execution_host_id"]) != policy.ExecutionHostID ||
		!ownershipEpochOK || ownershipEpoch != agent.OwnershipEpoch ||
		!policyRevisionOK || policyRevision != policy.ProjectionRevision ||
		!strings.EqualFold(updaterPolicyCapabilityString(capabilities["policy_status"]), "applied") {
		return false
	}
	serviceID := target.ServiceID
	availability := updaterPolicyCapabilityStringMap(capabilities["target_availability"])
	availabilityCodes := updaterPolicyCapabilityStringMap(capabilities["target_availability_codes"])
	reportedPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_ports"])
	reportedTypes := updaterPolicyCapabilityStringMap(capabilities["reported_service_types"])
	reportedModes := updaterPolicyCapabilityStringMap(capabilities["reported_deployment_modes"])
	reportedExecutorRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_executor_policy_revisions"])
	reportedExecutorDigests := updaterPolicyCapabilityStringMap(capabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_config_revisions"])
	reportedConfigDigests := updaterPolicyCapabilityStringMap(capabilities["reported_config_sha256"])
	reportedPortDrift := updaterPolicyCapabilityBoolMap(capabilities["port_drift"])
	drift, driftReported := reportedPortDrift[serviceID]
	return availability[serviceID] == "available" &&
		availabilityCodes[serviceID] == "executor_verified" &&
		reportedPorts[serviceID] == int64(target.AppliedEndpoint.Port) &&
		reportedTypes[serviceID] == target.ServiceType &&
		strings.ToLower(reportedModes[serviceID]) == "systemd" &&
		reportedExecutorRevisions[serviceID] == policy.LocalExecutorPolicyRevision &&
		reportedExecutorDigests[serviceID] == policy.LocalExecutorPolicySHA256 &&
		reportedConfigRevisions[serviceID] == target.AppliedConfigRevision &&
		reportedConfigDigests[serviceID] == target.AppliedConfigSHA256 &&
		driftReported && !drift
}
