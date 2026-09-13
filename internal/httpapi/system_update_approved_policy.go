package httpapi

import (
	"github.com/example/autostream-control-panel/internal/store"
	"strings"
	"time"
)

func approvedSystemUpdateAgentTargets(agent store.RegisteredService) (map[string]string, map[string]string) {
	approved := approvedSystemUpdateAgentTargetAssignments(agent, time.Now().UTC())
	modes := make(map[string]string, len(approved))
	versions := make(map[string]string, len(approved))
	for targetID, target := range approved {
		modes[targetID] = target.DeploymentMode
		versions[targetID] = target.CurrentVersion
	}
	return modes, versions
}

type systemUpdateApprovedTarget struct {
	DeploymentMode       string
	CurrentVersion       string
	ServiceType          string
	LocalListenPortBound bool
	Host                 systemUpdateHostResponse
	PolicyManaged        bool
	PolicyReady          bool
	PolicyBlockedReason  string
}

func approvedSystemUpdateAgentTargetAssignments(agent store.RegisteredService, now time.Time) map[string]systemUpdateApprovedTarget {
	return approvedSystemUpdateAgentTargetAssignmentsForPolicy(agent, now, nil)
}

func approvedSystemUpdateAgentTargetAssignmentsForPolicy(agent store.RegisteredService, now time.Time, policy *store.UpdaterPolicy) map[string]systemUpdateApprovedTarget {
	return approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(agent, now, policy, nil)
}

func approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
	agent store.RegisteredService,
	now time.Time,
	policy *store.UpdaterPolicy,
	servicesByID map[string]store.RegisteredService,
) map[string]systemUpdateApprovedTarget {
	if policy == nil || systemUpdateAgentTransportMode(agent) != store.SystemUpdateTransportPullV2 {
		return map[string]systemUpdateApprovedTarget{}
	}
	return approvedPullSystemUpdateAgentTargetAssignments(agent, now, *policy, servicesByID)
}

func approvedPullSystemUpdateAgentTargetAssignments(
	agent store.RegisteredService,
	now time.Time,
	policy store.UpdaterPolicy,
	servicesByID map[string]store.RegisteredService,
) map[string]systemUpdateApprovedTarget {
	if agent.OwnershipEpoch < 1 {
		return map[string]systemUpdateApprovedTarget{}
	}
	appliedRevision, policyStatus, _ := systemUpdateManagedPolicyReport(agent)
	policyApplied := appliedRevision == policy.ProjectionRevision && policyStatus == "applied"
	availability := capabilityStringMap(agent.ReportedCapabilities["target_availability"])
	availabilityCodes := capabilityStringMap(agent.ReportedCapabilities["target_availability_codes"])
	reportedPorts := capabilityInt64Map(agent.ReportedCapabilities["reported_ports"])
	reportedServiceTypes := capabilityStringMap(agent.ReportedCapabilities["reported_service_types"])
	reportedDeploymentModes := capabilityStringMap(agent.ReportedCapabilities["reported_deployment_modes"])
	reportedPolicyRevisions := capabilityInt64Map(agent.ReportedCapabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := capabilityStringMap(agent.ReportedCapabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := capabilityInt64Map(agent.ReportedCapabilities["reported_config_revisions"])
	reportedConfigDigests := capabilityStringMap(agent.ReportedCapabilities["reported_config_sha256"])
	reportedPortDrift := capabilityBoolMap(agent.ReportedCapabilities["port_drift"])

	executionHostID := strings.TrimSpace(policy.ExecutionHostID)
	reportedOwnershipEpoch, reportedOwnershipEpochOK := capabilityInt64(agent.ReportedCapabilities["ownership_epoch"])
	agentOnline := strings.EqualFold(strings.TrimSpace(agent.Status), "online") &&
		systemUpdateAgentAvailable(agent, now)
	mutationReady := capabilityBool(agent.ReportedCapabilities["host_agent"]) &&
		!capabilityBool(agent.ReportedCapabilities["observe_only"]) &&
		capabilityBool(agent.ReportedCapabilities["update_executor"]) &&
		capabilityBool(agent.ReportedCapabilities["mutation_enabled"]) &&
		capabilityString(agent.ReportedCapabilities["agent_protocol_version"]) == "2"
	bindingReady := policy.TransportMode == store.SystemUpdateTransportPullV2 &&
		executionHostID != "" &&
		executionHostID == strings.TrimSpace(agent.ExecutionHostID) &&
		agent.OwnershipEpoch > 0 &&
		capabilityString(agent.ReportedCapabilities["transport_mode"]) == store.SystemUpdateTransportPullV2 &&
		capabilityString(agent.ReportedCapabilities["execution_host_id"]) == executionHostID &&
		reportedOwnershipEpochOK &&
		reportedOwnershipEpoch == agent.OwnershipEpoch &&
		policy.ProjectionRevision > 0 &&
		policy.LocalExecutorPolicyRevision > 0 &&
		validUpdateManifestDigest(policy.LocalExecutorPolicySHA256) &&
		store.PullUpdaterPolicyDatabaseBindingsReady(policy)
	approved := make(map[string]systemUpdateApprovedTarget, len(policy.Targets))
	for _, target := range policy.Targets {
		serviceID := strings.TrimSpace(target.ServiceID)
		if !validSystemUpdateCapabilityIdentifier(serviceID) {
			continue
		}
		service, serviceExists := servicesByID[serviceID]
		expectedServiceType := strings.TrimSpace(target.ServiceType)
		expectedDeploymentMode := strings.ToLower(strings.TrimSpace(target.DeploymentMode))
		expectedConfigRevision := int64(1)
		expectedConfigSHA256 := ""
		expectedPort := 0
		if serviceExists {
			if service.AppliedConfigRevision > 0 {
				expectedConfigRevision = service.AppliedConfigRevision
			}
			expectedConfigSHA256 = service.AppliedConfigSHA256
			if expectedDeploymentMode == "systemd" {
				if localListenPort, ok := store.PullUpdaterPolicyTargetLocalListenPort(target, service); ok {
					expectedPort = localListenPort
				}
			} else if service.AppliedEndpoint != nil && service.AppliedEndpoint.Port > 0 {
				expectedPort = service.AppliedEndpoint.Port
			}
		}
		host := systemUpdateHostResponse{
			HostID: executionHostID, Name: executionHostID, UpdaterID: agent.ServiceID, Reachability: "unknown",
		}
		if agent.LastHeartbeatAt != nil {
			checkedAt := agent.LastHeartbeatAt.UTC()
			host.CheckedAt = &checkedAt
		}
		portDrift, portDriftReported := reportedPortDrift[serviceID]
		validExpectedPort := expectedPort >= 1024 && expectedPort <= 65535
		if expectedDeploymentMode == "docker" {
			validExpectedPort = expectedPort >= 1 && expectedPort <= 65535
		}
		executorVerified := availability[serviceID] == "available" &&
			availabilityCodes[serviceID] == "executor_verified" &&
			reportedServiceTypes[serviceID] == expectedServiceType &&
			strings.ToLower(reportedDeploymentModes[serviceID]) == expectedDeploymentMode &&
			reportedPolicyRevisions[serviceID] == policy.LocalExecutorPolicyRevision &&
			reportedPolicyDigests[serviceID] == policy.LocalExecutorPolicySHA256 &&
			reportedConfigRevisions[serviceID] == expectedConfigRevision &&
			(expectedConfigSHA256 == "" || reportedConfigDigests[serviceID] == expectedConfigSHA256) &&
			portDriftReported &&
			!portDrift &&
			validExpectedPort &&
			reportedPorts[serviceID] == int64(expectedPort)
		if agentOnline {
			switch {
			case executorVerified:
				host.Reachability = "reachable"
			case availability[serviceID] == "unavailable":
				host.Reachability = "unreachable"
			}
		}
		entry := systemUpdateApprovedTarget{
			DeploymentMode:       expectedDeploymentMode,
			ServiceType:          expectedServiceType,
			LocalListenPortBound: target.LocalListenPort != 0,
			Host:                 host,
			PolicyManaged:        true,
		}
		if serviceExists {
			entry.CurrentVersion = strings.TrimSpace(service.ReportedVersion)
			if entry.CurrentVersion == "" {
				entry.CurrentVersion = strings.TrimSpace(service.Version)
			}
		}
		switch {
		case policyStatus == "failed":
			entry.PolicyBlockedReason = "updater_policy_failed"
		case !policyApplied:
			entry.PolicyBlockedReason = "updater_policy_pending"
		case !bindingReady ||
			!mutationReady ||
			!agentOnline ||
			strings.TrimSpace(target.HostID) != executionHostID ||
			!serviceExists ||
			service.ServiceType == "update_agent" ||
			service.ServiceType != expectedServiceType ||
			(expectedDeploymentMode != "systemd" && expectedDeploymentMode != "docker") ||
			!executorVerified:
			entry.PolicyBlockedReason = "updater_policy_mismatch"
		default:
			entry.PolicyReady = true
		}
		approved[serviceID] = entry
	}
	return approved
}
