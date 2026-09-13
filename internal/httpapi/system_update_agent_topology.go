package httpapi

import (
	"github.com/example/autostream-control-panel/internal/store"
	"golang.org/x/crypto/ssh"
	"sort"
	"strings"
	"time"
)

func buildSystemUpdateTarget(targetID, serviceType, name, serviceVersion, currentStreamID string, busy bool, assignment systemUpdateAgentAssignment, checks map[string]serviceUpdateInfoResponse) systemUpdateTargetResponse {
	target := systemUpdateTargetResponse{TargetID: targetID, ServiceType: serviceType, Name: name, HostID: assignment.HostID, CurrentVersion: serviceVersion, DeploymentMode: assignment.DeploymentMode, UpdateAgentID: assignment.AgentID, UpdaterOnline: assignment.Available, Busy: busy, CurrentStreamID: currentStreamID}
	checkKey := serviceType
	if targetID == "control-panel" {
		checkKey = "control-panel"
	}
	if assignment.DeploymentMode == "docker" {
		checkKey = "docker"
		if assignment.CurrentVersion != "" {
			target.CurrentVersion = assignment.CurrentVersion
		} else {
			target.CurrentVersion = ""
		}
	}
	check := checks[checkKey]
	target.LatestVersion = strings.TrimSpace(check.LatestVersion)
	target.UpdateCheckSource = check.UpdateCheckSource
	target.UpdateCheckError = check.UpdateCheckError
	versionValid := validSystemUpdateVersion(target.LatestVersion)
	currentVersionValid := validSystemUpdateVersion(target.CurrentVersion)
	if versionValid && currentVersionValid && versionIsNewer(target.LatestVersion, target.CurrentVersion) {
		target.UpdateAvailable = true
	}

	if assignment.AgentID == "" {
		target.BlockedReason = "updater_missing"
		return target
	}
	if assignment.PolicyManaged && !assignment.PolicyReady {
		target.BlockedReason = assignment.PolicyBlockedReason
		if target.BlockedReason == "" {
			target.BlockedReason = "updater_policy_pending"
		}
		return target
	}
	if assignment.TargetServiceType != "" && assignment.TargetServiceType != serviceType {
		target.BlockedReason = "updater_policy_target_type_mismatch"
		return target
	}
	if !assignment.Available {
		target.BlockedReason = "updater_offline"
		return target
	}
	if assignment.HostReachability == "unreachable" {
		target.BlockedReason = "target_unreachable"
		return target
	}
	if assignment.HostReachability != "reachable" {
		target.BlockedReason = "target_reachability_unknown"
		return target
	}
	if assignment.DeploymentMode != "systemd" && assignment.DeploymentMode != "docker" {
		target.BlockedReason = "unsupported_deployment_mode"
		return target
	}
	if !currentVersionValid {
		target.BlockedReason = "current_version_unknown"
		return target
	}
	if target.LatestVersion == "" {
		target.BlockedReason = "release_manifest_unavailable"
		return target
	}
	if !versionValid {
		target.BlockedReason = "release_version_invalid"
		return target
	}
	if !target.UpdateAvailable {
		target.BlockedReason = "update_not_available"
		return target
	}
	if check.ManifestErrorCode != "" {
		target.BlockedReason = check.ManifestErrorCode
		return target
	}
	if !check.ManifestVerified {
		target.BlockedReason = "manifest_unverified"
		return target
	}
	if check.MinimumAgentVersion != "" && !systemUpdateAgentVersionAtLeast(assignment.AgentVersion, check.MinimumAgentVersion) {
		target.BlockedReason = "updater_version_incompatible"
		return target
	}
	target.Eligible = true
	return target
}

func systemUpdateAgentAssignments(services []store.RegisteredService) map[string]systemUpdateAgentAssignment {
	assignments, _, _ := systemUpdateAgentTopology(services, time.Now().UTC())
	return assignments
}

func systemUpdateAgentTopology(services []store.RegisteredService, now time.Time) (map[string]systemUpdateAgentAssignment, []systemUpdateAgentResponse, []systemUpdateHostResponse) {
	return systemUpdateAgentTopologyWithPolicies(services, now, nil)
}

func systemUpdateAgentTopologyWithPolicies(services []store.RegisteredService, now time.Time, policies map[string]store.UpdaterPolicy) (map[string]systemUpdateAgentAssignment, []systemUpdateAgentResponse, []systemUpdateHostResponse) {
	agentServices := make([]store.RegisteredService, 0)
	servicesByID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
		if service.ServiceType == "update_agent" &&
			systemUpdateAgentTransportMode(service) == store.SystemUpdateTransportPullV2 {
			agentServices = append(agentServices, service)
		}
	}
	sort.Slice(agentServices, func(i, j int) bool {
		iAvailable := systemUpdateAgentAvailable(agentServices[i], now)
		jAvailable := systemUpdateAgentAvailable(agentServices[j], now)
		if iAvailable != jAvailable {
			return iAvailable
		}
		return agentServices[i].ServiceID < agentServices[j].ServiceID
	})
	assignments := map[string]systemUpdateAgentAssignment{}
	updaters := make([]systemUpdateAgentResponse, 0, len(agentServices))
	hostOwners := map[string]string{}
	hostsByID := map[string]systemUpdateHostResponse{}
	for _, agent := range agentServices {
		agentVersion := systemUpdateAgentVersion(agent)
		transportMode := strings.ToLower(strings.TrimSpace(agent.TransportMode))
		updater := systemUpdateAgentResponse{
			UpdaterID: agent.ServiceID, Name: systemUpdateDisplayName(agent.ServiceName, agent.ServiceID), Status: strings.TrimSpace(agent.Status),
			TransportMode: transportMode,
			Online:        systemUpdateAgentAvailable(agent, now), Version: agentVersion, LastHeartbeat: agent.LastHeartbeatAt,
		}
		if transportMode == store.SystemUpdateTransportPullV2 {
			updater.ExecutionHostID = strings.TrimSpace(agent.ExecutionHostID)
			ownershipEpoch := agent.OwnershipEpoch
			updater.OwnershipEpoch = &ownershipEpoch
		}
		updater.BootstrapEncryptionPublicKey, updater.BootstrapEncryptionKeyFingerprint = updateHostBootstrapEncryptionIdentity(agent)
		var managedPolicy *store.UpdaterPolicy
		if policy, ok := policies[agent.ServiceID]; ok {
			managedPolicy = &policy
			updater.DesiredRevision = policy.ProjectionRevision
			updater.AppliedRevision, updater.PolicyStatus, updater.PolicyErrorCode = systemUpdateManagedPolicyReport(agent)
			managedHostIDs := make(map[string]bool, len(policy.Hosts))
			for _, host := range policy.Hosts {
				managedHostIDs[host.HostID] = true
			}
			updater.SSHClientPublicKeys, updater.SSHClientKeyFingerprints = systemUpdateSSHClientKeys(agent, managedHostIDs)
		}
		updaters = append(updaters, updater)
		targetServicesByID := servicesByID
		if managedPolicy != nil &&
			updaterPolicyUsesControlPanelSystemUpdateTarget(*managedPolicy) {
			targetServicesByID = make(
				map[string]store.RegisteredService,
				len(servicesByID)+1,
			)
			for serviceID, service := range servicesByID {
				targetServicesByID[serviceID] = service
			}
			if err := addControlPanelSystemUpdateServiceForPolicy(
				targetServicesByID,
				*managedPolicy,
			); err != nil {
				delete(targetServicesByID, controlPanelSystemUpdateServiceID)
			}
		}
		approved := approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
			agent,
			now,
			managedPolicy,
			targetServicesByID,
		)
		for _, targetID := range sortedApprovedSystemUpdateTargetIDs(approved) {
			if _, exists := assignments[targetID]; exists {
				continue
			}
			approvedTarget := approved[targetID]
			if owner, exists := hostOwners[approvedTarget.Host.HostID]; exists && owner != agent.ServiceID {
				continue
			}
			hostOwners[approvedTarget.Host.HostID] = agent.ServiceID
			if _, exists := hostsByID[approvedTarget.Host.HostID]; !exists {
				hostsByID[approvedTarget.Host.HostID] = approvedTarget.Host
			}
			assignments[targetID] = systemUpdateAgentAssignment{
				AgentID: agent.ServiceID, AgentVersion: agentVersion, AgentTransportMode: transportMode, DeploymentMode: approvedTarget.DeploymentMode,
				CurrentVersion: approvedTarget.CurrentVersion, Available: systemUpdateAgentAvailable(agent, now),
				HostID: approvedTarget.Host.HostID, HostName: approvedTarget.Host.Name, HostReachability: approvedTarget.Host.Reachability,
				HostCheckedAt: approvedTarget.Host.CheckedAt, HostCode: approvedTarget.Host.Code,
				TargetServiceType: approvedTarget.ServiceType, LocalListenPortBound: approvedTarget.LocalListenPortBound,
				PolicyManaged: approvedTarget.PolicyManaged,
				PolicyReady:   approvedTarget.PolicyReady, PolicyBlockedReason: approvedTarget.PolicyBlockedReason,
			}
		}
	}
	sort.Slice(updaters, func(i, j int) bool { return updaters[i].UpdaterID < updaters[j].UpdaterID })
	hosts := make([]systemUpdateHostResponse, 0, len(hostsByID))
	for _, host := range hostsByID {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Name != hosts[j].Name {
			return hosts[i].Name < hosts[j].Name
		}
		return hosts[i].HostID < hosts[j].HostID
	})
	return assignments, updaters, hosts
}

func systemUpdateSSHClientKeys(agent store.RegisteredService, allowedHostIDs map[string]bool) (map[string]string, map[string]string) {
	reported := capabilityStringMap(agent.ReportedCapabilities["ssh_client_public_keys"])
	keys := make(map[string]string, len(allowedHostIDs))
	fingerprints := make(map[string]string, len(allowedHostIDs))
	for hostID, raw := range reported {
		if !allowedHostIDs[hostID] || !validSystemUpdateCapabilityIdentifier(hostID) {
			continue
		}
		key, err := parseUpdaterED25519PublicKey(raw)
		if err != nil {
			continue
		}
		keys[hostID] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		fingerprints[hostID] = ssh.FingerprintSHA256(key)
	}
	return keys, fingerprints
}
