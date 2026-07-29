package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const systemUpdateDockerPortCapabilityVersion = "v1"

type CreateDockerPortReconfigurationJobParams struct {
	TargetID                 string
	NewAdvertisedPort        int
	NewPublishedPort         int
	NewContainerPort         int
	ExpectedEndpointRevision int64
	IdempotencyKey           string
	RequestedByUserID        string
	RequestedByUsername      string
	// ControlPanelTarget is server-owned runtime state used only to fence the
	// synthetic Control Panel host listener.
	ControlPanelTarget *PullUpdaterControlPanelTarget
}

type SystemUpdateDockerPortReconfigurationStore interface {
	CreateDockerPortReconfigurationJob(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params CreateDockerPortReconfigurationJobParams,
	) (job SystemUpdateJob, created bool, err error)
}

type systemUpdateDockerPortBaseline struct {
	PublishedPort               int
	ContainerPort               int
	HealthPort                  int
	ApprovedComposeConfigSHA256 string
	ApprovedComposeRevision     int64
	VersionEnvSHA256            string
	ContainerID                 string
	ImageID                     string
	RepositoryDigest            string
}

func normalizeCreateDockerPortReconfigurationJobParams(
	params CreateDockerPortReconfigurationJobParams,
) CreateDockerPortReconfigurationJobParams {
	params.TargetID = strings.TrimSpace(params.TargetID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	return params
}

func validateCreateDockerPortReconfigurationJobParams(
	params CreateDockerPortReconfigurationJobParams,
) error {
	if !serviceIDPattern.MatchString(params.TargetID) ||
		params.NewAdvertisedPort < 1 || params.NewAdvertisedPort > 65535 ||
		params.NewPublishedPort < 1024 || params.NewPublishedPort > 65535 ||
		params.NewContainerPort < 1024 || params.NewContainerPort > 65535 ||
		params.ExpectedEndpointRevision < 1 ||
		params.ExpectedEndpointRevision >= math.MaxInt64-1 ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 ||
		params.RequestedByUserID == "" || len(params.RequestedByUserID) > 64 ||
		len(params.RequestedByUsername) > 255 ||
		containsControl(params.IdempotencyKey) {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func systemUpdateDockerPortEnvVariables(serviceType string) (string, string, bool) {
	switch serviceType {
	case "worker":
		return "AUTOSTREAM_WORKER_PORT", "AUTOSTREAM_WORKER_CONTAINER_PORT", true
	case "encoder_recorder":
		return "AUTOSTREAM_ENCODER_RECORDER_PORT", "AUTOSTREAM_ENCODER_RECORDER_CONTAINER_PORT", true
	case "discord_bot":
		return "AUTOSTREAM_DISCORD_BOT_PORT", "AUTOSTREAM_DISCORD_BOT_CONTAINER_PORT", true
	case "observability":
		return "AUTOSTREAM_OBSERVABILITY_PORT", "AUTOSTREAM_OBSERVABILITY_CONTAINER_PORT", true
	default:
		return "", "", false
	}
}

func systemUpdateDockerPortEnvBytes(
	serviceType string,
	publishedPort, containerPort int,
	configRevision int64,
) ([]byte, error) {
	publishedVariable, containerVariable, ok := systemUpdateDockerPortEnvVariables(serviceType)
	if !ok ||
		publishedPort < 1024 || publishedPort > 65535 ||
		containerPort < 1024 || containerPort > 65535 ||
		configRevision < 1 {
		return nil, ErrSystemUpdatePortUnsupported
	}
	return []byte(fmt.Sprintf(
		"%s=%d\n%s=%d\nAUTOSTREAM_CONFIG_REVISION=%d\n",
		publishedVariable,
		publishedPort,
		containerVariable,
		containerPort,
		configRevision,
	)), nil
}

func systemUpdateDockerPortEnvSHA256(
	serviceType string,
	publishedPort, containerPort int,
	configRevision int64,
) (string, error) {
	body, err := systemUpdateDockerPortEnvBytes(
		serviceType,
		publishedPort,
		containerPort,
		configRevision,
	)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func SystemUpdateDockerPortConfigSHA256(
	serviceType string,
	publishedPort, containerPort int,
	configRevision int64,
) (string, error) {
	return systemUpdateDockerPortEnvSHA256(
		serviceType,
		publishedPort,
		containerPort,
		configRevision,
	)
}

func systemUpdateDockerPortBaselineFromAgent(
	agent RegisteredService,
	policy UpdaterPolicy,
	target RegisteredService,
) (systemUpdateDockerPortBaseline, bool) {
	serviceID := target.ServiceID
	capabilities := agent.ReportedCapabilities
	versions := updaterPolicyCapabilityStringMap(capabilities["reported_docker_port_capabilities"])
	publishedPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_docker_published_ports"])
	containerPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_docker_container_ports"])
	healthPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_docker_health_ports"])
	composeDigests := updaterPolicyCapabilityStringMap(capabilities["reported_docker_compose_sha256"])
	composeRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_docker_compose_revisions"])
	versionEnvDigests := updaterPolicyCapabilityStringMap(capabilities["reported_docker_version_env_sha256"])
	containerIDs := updaterPolicyCapabilityStringMap(capabilities["reported_docker_container_ids"])
	imageIDs := updaterPolicyCapabilityStringMap(capabilities["reported_docker_image_ids"])
	repositoryDigests := updaterPolicyCapabilityStringMap(capabilities["reported_docker_repository_digests"])
	baseline := systemUpdateDockerPortBaseline{
		PublishedPort:               int(publishedPorts[serviceID]),
		ContainerPort:               int(containerPorts[serviceID]),
		HealthPort:                  int(healthPorts[serviceID]),
		ApprovedComposeConfigSHA256: strings.ToLower(strings.TrimSpace(composeDigests[serviceID])),
		ApprovedComposeRevision:     composeRevisions[serviceID],
		VersionEnvSHA256:            strings.ToLower(strings.TrimSpace(versionEnvDigests[serviceID])),
		ContainerID:                 strings.ToLower(strings.TrimSpace(containerIDs[serviceID])),
		ImageID:                     strings.ToLower(strings.TrimSpace(imageIDs[serviceID])),
		RepositoryDigest:            strings.ToLower(strings.TrimSpace(repositoryDigests[serviceID])),
	}
	candidate := &SystemUpdateDockerPortReconfiguration{
		PublishedHostIP:             "127.0.0.1",
		OldPublishedPort:            baseline.PublishedPort,
		NewPublishedPort:            baseline.PublishedPort,
		OldContainerPort:            baseline.ContainerPort,
		NewContainerPort:            baseline.ContainerPort,
		OldHealthPort:               baseline.HealthPort,
		NewHealthPort:               baseline.HealthPort,
		ApprovedComposeConfigSHA256: baseline.ApprovedComposeConfigSHA256,
		ApprovedComposeRevision:     baseline.ApprovedComposeRevision,
		ExpectedVersionEnvSHA256:    baseline.VersionEnvSHA256,
		ExpectedContainerID:         baseline.ContainerID,
		ExpectedImageID:             baseline.ImageID,
		ExpectedRepositoryDigest:    baseline.RepositoryDigest,
	}
	validationPlan := &SystemUpdatePortReconfiguration{
		ExpectedExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
	}
	return baseline,
		versions[serviceID] == systemUpdateDockerPortCapabilityVersion &&
			validateSystemUpdateDockerPortReconfiguration(candidate, validationPlan) == nil
}

func pullAgentReadyForDockerPortChange(
	agent RegisteredService,
	policy UpdaterPolicy,
	target RegisteredService,
	now time.Time,
) (systemUpdateDockerPortBaseline, bool) {
	if agent.Status != "online" || agent.LastHeartbeatAt == nil ||
		now.Sub(agent.LastHeartbeatAt.UTC()) < 0 ||
		now.Sub(agent.LastHeartbeatAt.UTC()) > pullUpdaterActivationHeartbeatMaxAge {
		return systemUpdateDockerPortBaseline{}, false
	}
	capabilities := agent.ReportedCapabilities
	ownershipEpoch, ownershipEpochOK := updaterPolicyCapabilityInt64(capabilities["ownership_epoch"])
	policyRevision, policyRevisionOK := updaterPolicyCapabilityInt64(capabilities["policy_revision"])
	recoveryPending, recoveryPendingOK := capabilities["recovery_pending"].(bool)
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
	baseline, baselineComplete := systemUpdateDockerPortBaselineFromAgent(agent, policy, target)
	if !updaterPolicyCapabilityBool(capabilities["host_agent"]) ||
		!updaterPolicyCapabilityBool(capabilities["update_executor"]) ||
		!updaterPolicyCapabilityBool(capabilities["mutation_enabled"]) ||
		!recoveryPendingOK || recoveryPending ||
		updaterPolicyCapabilityString(capabilities["transport_mode"]) != SystemUpdateTransportPullV2 ||
		updaterPolicyCapabilityString(capabilities["execution_host_id"]) != policy.ExecutionHostID ||
		!ownershipEpochOK || ownershipEpoch != agent.OwnershipEpoch ||
		!policyRevisionOK || policyRevision != policy.ProjectionRevision ||
		!strings.EqualFold(updaterPolicyCapabilityString(capabilities["policy_status"]), "applied") ||
		availability[serviceID] != "available" ||
		availabilityCodes[serviceID] != "executor_verified" ||
		!baselineComplete ||
		target.AppliedEndpoint == nil ||
		reportedPorts[serviceID] != int64(target.AppliedEndpoint.Port) ||
		reportedTypes[serviceID] != target.ServiceType ||
		strings.ToLower(reportedModes[serviceID]) != "docker" ||
		reportedExecutorRevisions[serviceID] != policy.LocalExecutorPolicyRevision ||
		reportedExecutorDigests[serviceID] != policy.LocalExecutorPolicySHA256 ||
		reportedConfigRevisions[serviceID] != target.AppliedConfigRevision ||
		reportedConfigDigests[serviceID] != target.AppliedConfigSHA256 ||
		!driftReported || drift {
		return systemUpdateDockerPortBaseline{}, false
	}
	return baseline, true
}

func validateDockerPortCoordinatorState(
	policy UpdaterPolicy,
	policyTarget UpdaterPolicyTarget,
	target RegisteredService,
	agent RegisteredService,
	ownership SystemUpdateExecutionHost,
	params CreateDockerPortReconfigurationJobParams,
	now time.Time,
) (systemUpdateDockerPortBaseline, error) {
	if policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID == "" ||
		policy.Revision < 1 || policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(policy.LocalExecutorPolicySHA256) ||
		policy.LocalExecutorPolicySHA256 == "" ||
		policyTarget.ServiceID != params.TargetID ||
		policyTarget.DeploymentMode != "docker" ||
		!supportedSystemdPortServiceType(policyTarget.ServiceType) ||
		target.ServiceID != policyTarget.ServiceID ||
		target.ServiceType != policyTarget.ServiceType ||
		target.ServiceType == "control_panel" ||
		target.ServiceType == "update_agent" ||
		target.EndpointRevision != params.ExpectedEndpointRevision ||
		(target.EndpointStatus != "applied" && target.EndpointStatus != "rolled_back") ||
		target.AppliedEndpoint == nil ||
		!sameServiceEndpoint(target.DesiredEndpoint, target.AppliedEndpoint) ||
		target.AppliedEndpoint.Port < 1 || target.AppliedEndpoint.Port > 65535 ||
		target.AppliedConfigRevision < 1 ||
		!validSystemUpdateDigest(target.AppliedConfigSHA256) ||
		target.AppliedConfigSHA256 == "" {
		return systemUpdateDockerPortBaseline{}, ErrSystemUpdateEndpointStale
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
		return systemUpdateDockerPortBaseline{}, ErrSystemUpdateOwnershipConflict
	}
	baseline, ready := pullAgentReadyForDockerPortChange(agent, policy, target, now)
	if !ready {
		return systemUpdateDockerPortBaseline{}, ErrSystemUpdateAgentNotReady
	}
	expectedConfigSHA256, err := systemUpdateDockerPortEnvSHA256(
		target.ServiceType,
		baseline.PublishedPort,
		baseline.ContainerPort,
		target.AppliedConfigRevision,
	)
	if err != nil || expectedConfigSHA256 != target.AppliedConfigSHA256 {
		return systemUpdateDockerPortBaseline{}, ErrSystemUpdateEndpointStale
	}
	if params.NewAdvertisedPort == target.AppliedEndpoint.Port &&
		params.NewPublishedPort == baseline.PublishedPort &&
		params.NewContainerPort == baseline.ContainerPort {
		return systemUpdateDockerPortBaseline{}, ErrSystemUpdateEndpointStale
	}
	return baseline, nil
}

func systemUpdateDockerPortJobFromState(
	params CreateDockerPortReconfigurationJobParams,
	target RegisteredService,
	agent RegisteredService,
	policy UpdaterPolicy,
	ownership SystemUpdateExecutionHost,
	baseline systemUpdateDockerPortBaseline,
	now time.Time,
) (SystemUpdateJob, error) {
	if target.AppliedEndpoint == nil ||
		target.EndpointRevision >= math.MaxInt64 ||
		target.AppliedConfigRevision >= math.MaxInt64 {
		return SystemUpdateJob{}, ErrSystemUpdateEndpointStale
	}
	targetConfigRevision := target.AppliedConfigRevision + 1
	targetConfigSHA256, err := systemUpdateDockerPortEnvSHA256(
		target.ServiceType,
		params.NewPublishedPort,
		params.NewContainerPort,
		targetConfigRevision,
	)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	version := strings.TrimSpace(target.ReportedVersion)
	if version == "" {
		version = strings.TrimSpace(target.Version)
	}
	if !systemUpdateJobVersionPattern.MatchString(version) {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	portPlan := &SystemUpdatePortReconfiguration{
		NetworkNamespace:               systemUpdatePortNetworkNamespace,
		Protocol:                       SystemUpdatePortProtocolTCP,
		OldPort:                        target.AppliedEndpoint.Port,
		NewPort:                        params.NewAdvertisedPort,
		ExpectedEndpointRevision:       target.EndpointRevision,
		TargetEndpointRevision:         target.EndpointRevision + 1,
		ExpectedConfigRevision:         target.AppliedConfigRevision,
		TargetConfigRevision:           targetConfigRevision,
		ExpectedConfigSHA256:           target.AppliedConfigSHA256,
		TargetConfigSHA256:             targetConfigSHA256,
		ExpectedSourcePolicyRevision:   policy.Revision,
		ExpectedUpdaterPolicyRevision:  policy.ProjectionRevision,
		ExpectedExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		Docker: &SystemUpdateDockerPortReconfiguration{
			PublishedHostIP:             "127.0.0.1",
			OldPublishedPort:            baseline.PublishedPort,
			NewPublishedPort:            params.NewPublishedPort,
			OldContainerPort:            baseline.ContainerPort,
			NewContainerPort:            params.NewContainerPort,
			OldHealthPort:               baseline.HealthPort,
			NewHealthPort:               params.NewPublishedPort,
			ApprovedComposeConfigSHA256: baseline.ApprovedComposeConfigSHA256,
			ApprovedComposeRevision:     baseline.ApprovedComposeRevision,
			ExpectedVersionEnvSHA256:    baseline.VersionEnvSHA256,
			ExpectedContainerID:         baseline.ContainerID,
			ExpectedImageID:             baseline.ImageID,
			ExpectedRepositoryDigest:    baseline.RepositoryDigest,
		},
	}
	job := SystemUpdateJob{
		ID: newUUID(), TargetID: target.ServiceID, TargetServiceType: target.ServiceType,
		Operation: SystemUpdateOperationPortReconfigure, PortReconfigure: portPlan,
		DeploymentMode: "docker", CurrentVersion: version, TargetVersion: version,
		Strategy: SystemUpdateStrategyMaintenance, Status: SystemUpdateStatusQueued,
		IdempotencyKey: params.IdempotencyKey, RequestedByUserID: params.RequestedByUserID,
		RequestedByUsername: params.RequestedByUsername, AgentServiceID: agent.ServiceID,
		ExecutionHostID: ownership.ExecutionHostID, TransportMode: ownership.TransportMode,
		OwnershipEpoch: ownership.OwnershipEpoch, PolicyRevision: ownership.PolicyRevision,
		CreatedAt: now, UpdatedAt: now,
	}
	intentSHA256, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	job.PortReconfigure.PortPlanSHA256 = intentSHA256
	if validateSystemUpdateCreate(CreateSystemUpdateJobParams{
		TargetID: job.TargetID, TargetServiceType: job.TargetServiceType,
		Operation: job.Operation, PortReconfigure: job.PortReconfigure,
		AgentServiceID: job.AgentServiceID, ExecutionHostID: job.ExecutionHostID,
		DeploymentMode: job.DeploymentMode, CurrentVersion: job.CurrentVersion,
		TargetVersion: job.TargetVersion, Strategy: job.Strategy,
		IdempotencyKey: job.IdempotencyKey, RequestedByUserID: job.RequestedByUserID,
		RequestedByUsername: job.RequestedByUsername,
	}) != nil {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	return job, nil
}

func sameDockerPortCreateRequest(
	job SystemUpdateJob,
	params CreateDockerPortReconfigurationJobParams,
) bool {
	return job.Operation == SystemUpdateOperationPortReconfigure &&
		job.DeploymentMode == "docker" &&
		job.TargetID == params.TargetID &&
		job.PortReconfigure != nil &&
		job.PortReconfigure.Docker != nil &&
		job.PortReconfigure.NewPort == params.NewAdvertisedPort &&
		job.PortReconfigure.Docker.NewPublishedPort == params.NewPublishedPort &&
		job.PortReconfigure.Docker.NewContainerPort == params.NewContainerPort &&
		job.PortReconfigure.ExpectedEndpointRevision == params.ExpectedEndpointRevision
}

func (s *MemorySystemUpdateStore) CreateDockerPortReconfigurationJob(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateDockerPortReconfigurationJobParams,
) (SystemUpdateJob, bool, error) {
	params = normalizeCreateDockerPortReconfigurationJobParams(params)
	if err := validateCreateDockerPortReconfigurationJobParams(params); err != nil {
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

	policyStore.mu.Lock()
	defer policyStore.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if existing, found := memorySystemUpdateByIdempotencyLocked(
		s,
		params.RequestedByUserID,
		params.IdempotencyKey,
	); found {
		if sameDockerPortCreateRequest(existing, params) {
			return publicMemorySystemUpdateJob(existing), false, nil
		}
		return SystemUpdateJob{}, false, ErrAlreadyExists
	}
	policy, policyTarget, err := memoryPullPolicyForPortTargetLocked(
		policyStore,
		params.TargetID,
	)
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
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(
		s,
		ownership.ExecutionHostID,
	); found {
		return SystemUpdateJob{}, false, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	now := time.Now().UTC()
	baseline, err := validateDockerPortCoordinatorState(
		policy,
		policyTarget,
		target,
		agent,
		ownership,
		params,
		now,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(
		policy,
		params.TargetID,
		params.NewPublishedPort,
		params.ControlPanelTarget,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateMemoryPortReservationsLocked(
		s,
		ownership.ExecutionHostID,
		target.ServiceID,
		baseline.PublishedPort,
		params.NewPublishedPort,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	for _, existing := range s.jobs {
		if existing.TargetID == target.ServiceID &&
			!isTerminalSystemUpdateStatus(existing.Status) {
			return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
		}
	}
	job, err := systemUpdateDockerPortJobFromState(
		params,
		target,
		agent,
		policy,
		ownership,
		baseline,
		now,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	desiredEndpoint, err := systemUpdatePortDesiredEndpoint(job, target.AppliedEndpoint)
	if err != nil {
		return SystemUpdateJob{}, false, err
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
	if baseline.PublishedPort != params.NewPublishedPort {
		pending := ServicePortReservation{
			ExecutionHostID:  ownership.ExecutionHostID,
			NetworkNamespace: systemUpdatePortNetworkNamespace,
			Protocol:         systemUpdatePortProtocol,
			Port:             params.NewPublishedPort,
			ServiceID:        target.ServiceID,
			ServiceRole:      systemUpdatePortPendingRole,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		s.portReservations[servicePortKey(pending)] = pending
	}
	s.portJobRegistries[job.ID] = registry
	target.DesiredEndpoint = desiredEndpoint
	target.EndpointRevision = job.PortReconfigure.TargetEndpointRevision
	target.EndpointStatus = "pending"
	target.UpdatedAt = now
	registry.services[target.ServiceID] = target
	return publicMemorySystemUpdateJob(job), true, nil
}

func (s *MariaDBSystemUpdateStore) CreateDockerPortReconfigurationJob(
	ctx context.Context,
	services ServiceRegistryStore,
	policies UpdaterPolicyStore,
	params CreateDockerPortReconfigurationJobParams,
) (SystemUpdateJob, bool, error) {
	params = normalizeCreateDockerPortReconfigurationJobParams(params)
	if err := validateCreateDockerPortReconfigurationJobParams(params); err != nil {
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
		ctx,
		systemUpdateSelect+` WHERE requested_by_user_id = ? AND idempotency_key = ? FOR UPDATE`,
		params.RequestedByUserID,
		params.IdempotencyKey,
	))
	if err == nil {
		if sameDockerPortCreateRequest(existing, params) {
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

	discoveredPolicy, discoveredTarget, err := mariaDBPullPolicyForPortTarget(
		ctx,
		tx,
		params.TargetID,
		false,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	ownership, err := getSystemUpdateExecutionHostForUpdate(
		ctx,
		tx,
		discoveredPolicy.ExecutionHostID,
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
		ctx,
		tx,
		params.TargetID,
		true,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if !sameMariaDBPortPolicySelection(
		discoveredPolicy,
		discoveredTarget,
		policy,
		policyTarget,
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
			ctx,
			serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
			serviceID,
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
	baseline, err := validateDockerPortCoordinatorState(
		policy,
		policyTarget,
		target,
		agent,
		ownership,
		params,
		now,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateSyntheticControlPanelHostPortFence(
		policy,
		params.TargetID,
		params.NewPublishedPort,
		params.ControlPanelTarget,
	); err != nil {
		return SystemUpdateJob{}, false, err
	}
	if err := validateMariaDBPortReservationsForUpdate(
		ctx,
		tx,
		ownership.ExecutionHostID,
		target.ServiceID,
		baseline.PublishedPort,
		params.NewPublishedPort,
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
	job, err := systemUpdateDockerPortJobFromState(
		params,
		target,
		agent,
		policy,
		ownership,
		baseline,
		now,
	)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	desiredEndpoint, err := systemUpdatePortDesiredEndpoint(job, target.AppliedEndpoint)
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	port := job.PortReconfigure
	docker := port.Docker
	_, err = tx.ExecContext(ctx, `INSERT INTO system_update_jobs
(id, target_id, target_service_type, operation, network_namespace, protocol,
 old_port, new_port, expected_endpoint_revision, target_endpoint_revision,
 expected_config_revision, target_config_revision, expected_config_sha256,
 expected_source_policy_revision, target_config_sha256,
 expected_updater_policy_revision, expected_executor_policy_revision,
 expected_executor_policy_sha256, port_plan_sha256,
 docker_published_host_ip, docker_old_published_port, docker_new_published_port,
 docker_old_container_port, docker_new_container_port,
 docker_old_health_port, docker_new_health_port,
 docker_approved_compose_config_sha256, docker_approved_compose_revision,
 docker_expected_version_env_sha256, docker_expected_container_id,
 docker_expected_image_id, docker_expected_repository_digest,
 agent_service_id, execution_host_id, transport_mode, ownership_epoch,
 policy_revision, deployment_mode, current_version, target_version, strategy,
 status, idempotency_key, requested_by_user_id, requested_by_username,
 sequence, progress, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		job.ID,
		job.TargetID,
		job.TargetServiceType,
		job.Operation,
		port.NetworkNamespace,
		port.Protocol,
		port.OldPort,
		port.NewPort,
		port.ExpectedEndpointRevision,
		port.TargetEndpointRevision,
		port.ExpectedConfigRevision,
		port.TargetConfigRevision,
		port.ExpectedConfigSHA256,
		port.ExpectedSourcePolicyRevision,
		port.TargetConfigSHA256,
		port.ExpectedUpdaterPolicyRevision,
		port.ExpectedExecutorPolicyRevision,
		port.ExpectedExecutorPolicySHA256,
		port.PortPlanSHA256,
		docker.PublishedHostIP,
		docker.OldPublishedPort,
		docker.NewPublishedPort,
		docker.OldContainerPort,
		docker.NewContainerPort,
		docker.OldHealthPort,
		docker.NewHealthPort,
		docker.ApprovedComposeConfigSHA256,
		docker.ApprovedComposeRevision,
		docker.ExpectedVersionEnvSHA256,
		docker.ExpectedContainerID,
		docker.ExpectedImageID,
		docker.ExpectedRepositoryDigest,
		job.AgentServiceID,
		job.ExecutionHostID,
		job.TransportMode,
		job.OwnershipEpoch,
		job.PolicyRevision,
		job.DeploymentMode,
		job.CurrentVersion,
		job.TargetVersion,
		job.Strategy,
		job.Status,
		job.IdempotencyKey,
		job.RequestedByUserID,
		job.RequestedByUsername,
		job.CreatedAt,
		job.UpdatedAt,
	)
	if isDuplicateKeyError(err) {
		return SystemUpdateJob{}, false, ErrSystemUpdateTargetActive
	}
	if err != nil {
		return SystemUpdateJob{}, false, err
	}
	if baseline.PublishedPort != params.NewPublishedPort {
		_, err = tx.ExecContext(ctx, `INSERT INTO service_port_reservations
(execution_host_id, network_namespace, protocol, port, service_id,
 service_role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ownership.ExecutionHostID,
			systemUpdatePortNetworkNamespace,
			systemUpdatePortProtocol,
			params.NewPublishedPort,
			target.ServiceID,
			systemUpdatePortPendingRole,
			now,
			now,
		)
		if isDuplicateKeyError(err) {
			return SystemUpdateJob{}, false, ErrServicePortReserved
		}
		if err != nil {
			return SystemUpdateJob{}, false, err
		}
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
		desiredEndpoint.Host,
		desiredEndpoint.Port,
		desiredEndpoint.SSLEnabled,
		desiredEndpoint.PublicURL,
		port.TargetEndpointRevision,
		now,
		target.ServiceID,
		port.ExpectedEndpointRevision,
		port.ExpectedConfigRevision,
		port.ExpectedConfigSHA256,
		port.OldPort,
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

var _ SystemUpdateDockerPortReconfigurationStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateDockerPortReconfigurationStore = (*MariaDBSystemUpdateStore)(nil)
