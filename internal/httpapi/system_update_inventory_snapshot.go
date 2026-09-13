package httpapi

import (
	"context"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
	"sort"
	"strings"
	"time"
)

func (s *Server) systemUpdateTargets(ctx context.Context) ([]systemUpdateTargetResponse, error) {
	targets, _, _, err := s.systemUpdateSnapshot(ctx)
	return targets, err
}

func (s *Server) systemUpdateSnapshot(ctx context.Context) ([]systemUpdateTargetResponse, []systemUpdateAgentResponse, []systemUpdateHostResponse, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	policyItems, err := s.updaterPolicies.ListUpdaterPolicies(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	policies := make(map[string]store.UpdaterPolicy, len(policyItems))
	for _, policy := range policyItems {
		policies[policy.UpdaterID] = policy
	}
	now := time.Now().UTC()
	agents, updaters, hosts := systemUpdateAgentTopologyWithPolicies(services, now, policies)
	checks := latestVersions(ctx, append(append([]versionUpdateTarget{}, controlPanelVersionUpdateTarget), append(nodeVersionUpdateTargets, dockerVersionUpdateTarget)...))
	panelBusy, err := s.systemUpdateControlPanelBusy(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	targets := make([]systemUpdateTargetResponse, 0, len(services)+1)
	controlPanelAssignment := agents["control-panel"]
	controlPanelTarget := buildSystemUpdateTarget("control-panel", "control_panel", "Control Panel", version.Current(), "", panelBusy, controlPanelAssignment, checks)
	decorateSystemUpdateTargetOperations(&controlPanelTarget, nil, controlPanelAssignment)
	targets = append(targets, controlPanelTarget)
	for _, service := range services {
		if service.ServiceType == "update_agent" {
			continue
		}
		currentVersion := strings.TrimSpace(service.ReportedVersion)
		if currentVersion == "" {
			currentVersion = strings.TrimSpace(service.Version)
		}
		busy, err := s.systemUpdateServiceBusy(ctx, service)
		if err != nil {
			return nil, nil, nil, err
		}
		name := strings.TrimSpace(service.ServiceName)
		if name == "" {
			name = service.ServiceID
		}
		assignment := agents[service.ServiceID]
		target := buildSystemUpdateTarget(service.ServiceID, service.ServiceType, name, currentVersion, service.CurrentStreamID, busy, assignment, checks)
		if assignment.DeploymentMode == "docker" {
			target.PortMapping = systemUpdateDockerPortMappingSnapshot(
				service,
				servicesByID[assignment.AgentID],
				policies[assignment.AgentID],
				assignment,
			)
		}
		decorateSystemUpdateTargetOperations(&target, &service, assignment)
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].TargetID == "control-panel" {
			return true
		}
		if targets[j].TargetID == "control-panel" {
			return false
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, updaters, hosts, nil
}

func decorateSystemUpdateTargetOperations(
	target *systemUpdateTargetResponse,
	service *store.RegisteredService,
	assignment systemUpdateAgentAssignment,
) {
	if target == nil {
		return
	}
	target.EligibleOperations = make([]string, 0, 2)
	target.OperationBlockedReasons = make(map[string]string, 2)
	if target.Eligible {
		target.EligibleOperations = append(target.EligibleOperations, store.SystemUpdateOperationSoftwareUpdate)
	} else {
		reason := strings.TrimSpace(target.BlockedReason)
		if reason == "" {
			reason = "system_update_target_unavailable"
		}
		target.OperationBlockedReasons[store.SystemUpdateOperationSoftwareUpdate] = reason
	}
	if reason := systemUpdatePortReconfigureBlockedReason(*target, service, assignment); reason != "" {
		target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] = reason
	} else {
		target.EligibleOperations = append(target.EligibleOperations, store.SystemUpdateOperationPortReconfigure)
	}
}

func systemUpdatePortReconfigureBlockedReason(
	target systemUpdateTargetResponse,
	service *store.RegisteredService,
	assignment systemUpdateAgentAssignment,
) string {
	if service == nil ||
		service.ServiceID != target.TargetID ||
		!supportedSystemUpdatePortServiceType(service.ServiceType) {
		return "system_update_port_reconfigure_not_ready"
	}
	if assignment.AgentID == "" {
		return "updater_missing"
	}
	if target.Busy || strings.TrimSpace(target.CurrentStreamID) != "" {
		return "system_update_target_busy"
	}
	if (assignment.DeploymentMode != "systemd" &&
		assignment.DeploymentMode != "docker") ||
		assignment.AgentTransportMode != store.SystemUpdateTransportPullV2 ||
		!assignment.PolicyManaged {
		return "system_update_port_reconfigure_not_ready"
	}
	if assignment.PolicyManaged && !assignment.PolicyReady {
		if reason := strings.TrimSpace(assignment.PolicyBlockedReason); reason != "" {
			return reason
		}
		return "updater_policy_pending"
	}
	if assignment.TargetServiceType != "" && assignment.TargetServiceType != service.ServiceType {
		return "updater_policy_target_type_mismatch"
	}
	if assignment.DeploymentMode == "systemd" && !assignment.LocalListenPortBound {
		return "system_update_port_reconfigure_not_ready"
	}
	if !assignment.Available {
		return "updater_offline"
	}
	if assignment.HostReachability == "unreachable" {
		return "target_unreachable"
	}
	if assignment.HostReachability != "reachable" {
		return "target_reachability_unknown"
	}
	minAdvertisedPort := 1
	if assignment.DeploymentMode == "docker" {
		minAdvertisedPort = 1
		if target.PortMapping == nil ||
			target.PortMapping.Mode != "docker" ||
			target.PortMapping.State != "applied" {
			return "system_update_port_reconfigure_not_ready"
		}
	}
	if service.EndpointStatus != "applied" ||
		service.EndpointRevision < 1 ||
		service.AppliedConfigRevision < 1 ||
		!validUpdateManifestDigest(service.AppliedConfigSHA256) ||
		service.AppliedEndpoint == nil ||
		service.DesiredEndpoint == nil ||
		!sameSystemUpdateServiceEndpoint(service.AppliedEndpoint, service.DesiredEndpoint) ||
		service.AppliedEndpoint.Port < minAdvertisedPort ||
		service.AppliedEndpoint.Port > 65535 {
		return "system_update_endpoint_revision_conflict"
	}
	return ""
}

func systemUpdateDockerPortMappingSnapshot(
	service store.RegisteredService,
	agent store.RegisteredService,
	policy store.UpdaterPolicy,
	assignment systemUpdateAgentAssignment,
) *systemUpdatePortMappingResponse {
	unavailable := &systemUpdatePortMappingResponse{Mode: "docker", State: "unavailable"}
	if assignment.DeploymentMode != "docker" ||
		assignment.AgentTransportMode != store.SystemUpdateTransportPullV2 ||
		assignment.AgentID == "" ||
		agent.ServiceID != assignment.AgentID ||
		agent.ServiceType != "update_agent" ||
		!assignment.PolicyManaged ||
		!assignment.Available ||
		policy.UpdaterID != assignment.AgentID ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!validUpdateManifestDigest(policy.LocalExecutorPolicySHA256) ||
		!validSystemUpdateCapabilityIdentifier(service.ServiceID) ||
		!supportedSystemUpdatePortServiceType(service.ServiceType) ||
		service.AppliedEndpoint == nil ||
		service.AppliedEndpoint.Port < 1 ||
		service.AppliedEndpoint.Port > 65535 ||
		service.AppliedConfigRevision < 1 ||
		!validUpdateManifestDigest(service.AppliedConfigSHA256) ||
		agent.LastHeartbeatAt == nil ||
		agent.LastHeartbeatAt.IsZero() {
		return unavailable
	}
	serviceID := service.ServiceID
	capabilities := agent.ReportedCapabilities
	availability := capabilityStringMap(capabilities["target_availability"])
	availabilityCodes := capabilityStringMap(capabilities["target_availability_codes"])
	reportedServiceTypes := capabilityStringMap(capabilities["reported_service_types"])
	reportedDeploymentModes := capabilityStringMap(capabilities["reported_deployment_modes"])
	reportedPolicyRevisions := capabilityInt64Map(capabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := capabilityStringMap(capabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := capabilityInt64Map(capabilities["reported_config_revisions"])
	reportedConfigDigests := capabilityStringMap(capabilities["reported_config_sha256"])
	reportedAdvertisedPorts := capabilityInt64Map(capabilities["reported_ports"])
	reportedPortDrift := capabilityBoolMap(capabilities["port_drift"])
	versions := capabilityStringMap(capabilities["reported_docker_port_capabilities"])
	publishedPorts := capabilityInt64Map(capabilities["reported_docker_published_ports"])
	containerPorts := capabilityInt64Map(capabilities["reported_docker_container_ports"])
	healthPorts := capabilityInt64Map(capabilities["reported_docker_health_ports"])
	composeDigests := capabilityStringMap(capabilities["reported_docker_compose_sha256"])
	composeRevisions := capabilityInt64Map(capabilities["reported_docker_compose_revisions"])
	versionEnvDigests := capabilityStringMap(capabilities["reported_docker_version_env_sha256"])
	containerIDs := capabilityStringMap(capabilities["reported_docker_container_ids"])
	imageIDs := capabilityStringMap(capabilities["reported_docker_image_ids"])
	repositoryDigests := capabilityStringMap(capabilities["reported_docker_repository_digests"])

	reportedAvailability, availabilityOK := availability[serviceID]
	reportedAvailabilityCode, availabilityCodeOK := availabilityCodes[serviceID]
	reportedServiceType, serviceTypeOK := reportedServiceTypes[serviceID]
	reportedDeploymentMode, deploymentModeOK := reportedDeploymentModes[serviceID]
	reportedPolicyRevision, policyRevisionOK := reportedPolicyRevisions[serviceID]
	reportedPolicyDigest, policyDigestOK := reportedPolicyDigests[serviceID]
	reportedConfigRevision, configRevisionOK := reportedConfigRevisions[serviceID]
	reportedConfigDigest, configDigestOK := reportedConfigDigests[serviceID]
	reportedAdvertisedPort, advertisedPortOK := reportedAdvertisedPorts[serviceID]
	portDrift, portDriftOK := reportedPortDrift[serviceID]
	version, versionOK := versions[serviceID]
	publishedPort, publishedPortOK := publishedPorts[serviceID]
	containerPort, containerPortOK := containerPorts[serviceID]
	healthPort, healthPortOK := healthPorts[serviceID]
	composeDigest, composeDigestOK := composeDigests[serviceID]
	composeRevision, composeRevisionOK := composeRevisions[serviceID]
	versionEnvDigest, versionEnvDigestOK := versionEnvDigests[serviceID]
	containerID, containerIDOK := containerIDs[serviceID]
	imageID, imageIDOK := imageIDs[serviceID]
	repositoryDigest, repositoryDigestOK := repositoryDigests[serviceID]
	containerID = strings.TrimSpace(containerID)

	if !availabilityOK || reportedAvailability != "available" ||
		!availabilityCodeOK || reportedAvailabilityCode != "executor_verified" ||
		!serviceTypeOK || !supportedSystemUpdatePortServiceType(reportedServiceType) ||
		!deploymentModeOK ||
		(reportedDeploymentMode != "systemd" && reportedDeploymentMode != "docker") ||
		!policyRevisionOK || reportedPolicyRevision < 1 ||
		!policyDigestOK || !validUpdateManifestDigest(reportedPolicyDigest) ||
		!configRevisionOK || reportedConfigRevision < 1 ||
		!configDigestOK || !validUpdateManifestDigest(reportedConfigDigest) ||
		!advertisedPortOK || reportedAdvertisedPort < 1 || reportedAdvertisedPort > 65535 ||
		!portDriftOK ||
		!versionOK || version != "v1" ||
		!publishedPortOK ||
		publishedPort < 1024 || publishedPort > 65535 ||
		!containerPortOK ||
		containerPort < 1024 || containerPort > 65535 ||
		!healthPortOK ||
		healthPort < 1024 || healthPort > 65535 ||
		!composeDigestOK || !systemUpdateRawSHA256Pattern.MatchString(composeDigest) ||
		!composeRevisionOK || composeRevision < 1 ||
		!versionEnvDigestOK || !validUpdateManifestDigest(versionEnvDigest) ||
		!containerIDOK ||
		!systemUpdateDockerContainerIDPattern.MatchString(containerID) ||
		!imageIDOK || !validUpdateManifestDigest(imageID) ||
		!repositoryDigestOK || !validUpdateManifestDigest(repositoryDigest) {
		return unavailable
	}
	reportedConfigSHA256, err := store.SystemUpdateDockerPortConfigSHA256(
		reportedServiceType,
		int(publishedPort),
		int(containerPort),
		reportedConfigRevision,
	)
	if err != nil || reportedConfigSHA256 != reportedConfigDigest {
		return unavailable
	}
	reportedAt := agent.LastHeartbeatAt.UTC()
	mapping := &systemUpdatePortMappingResponse{
		Mode: "docker", AdvertisedPort: int(reportedAdvertisedPort),
		PublishedHostIP: "127.0.0.1", PublishedPort: int(publishedPort),
		ContainerPort: int(containerPort), HealthPort: int(healthPort),
		ConfigRevision: reportedConfigRevision,
		State:          "drifted", ReportedAt: &reportedAt,
	}
	if !portDrift &&
		reportedServiceType == service.ServiceType &&
		reportedDeploymentMode == "docker" &&
		reportedPolicyRevision == policy.LocalExecutorPolicyRevision &&
		reportedPolicyDigest == policy.LocalExecutorPolicySHA256 &&
		reportedConfigRevision == service.AppliedConfigRevision &&
		reportedConfigDigest == service.AppliedConfigSHA256 &&
		reportedAdvertisedPort == int64(service.AppliedEndpoint.Port) &&
		healthPort == publishedPort &&
		composeRevision == policy.LocalExecutorPolicyRevision {
		mapping.State = "applied"
	}
	return mapping
}

func supportedSystemUpdatePortServiceType(serviceType string) bool {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

func sameSystemUpdateServiceEndpoint(left, right *store.ServiceEndpoint) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
