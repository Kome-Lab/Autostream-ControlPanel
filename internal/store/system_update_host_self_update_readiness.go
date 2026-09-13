package store

import (
	"strconv"
	"strings"
)

func validateSystemUpdateHostSelfUpdateReady(
	ownership SystemUpdateExecutionHost,
	policy UpdaterPolicy,
	agent RegisteredService,
	release SystemUpdateHostReleaseMetadata,
) (systemUpdateHostPreviousRuntime, error) {
	if ownership.TransportMode != SystemUpdateTransportPullV2 ||
		ownership.OwnershipEpoch < 1 ||
		ownership.AgentServiceID == "" ||
		policy.UpdaterID != ownership.AgentServiceID ||
		policy.TransportMode != SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != ownership.ExecutionHostID ||
		policy.ProjectionRevision != ownership.PolicyRevision ||
		policy.Revision < 1 || policy.ProjectionRevision < 1 ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!systemUpdateHostSelfUpdatePolicyDigestPattern.MatchString(
			policy.LocalExecutorPolicySHA256,
		) ||
		agent.ServiceID != ownership.AgentServiceID ||
		agent.ServiceType != "update_agent" ||
		agent.TransportMode != SystemUpdateTransportPullV2 ||
		agent.ExecutionHostID != ownership.ExecutionHostID ||
		agent.OwnershipEpoch != ownership.OwnershipEpoch {
		return systemUpdateHostPreviousRuntime{},
			ErrSystemUpdateOwnershipConflict
	}
	caps := agent.ReportedCapabilities
	if !updaterPolicyCapabilityBool(caps["self_update_ready"]) ||
		!updaterPolicyCapabilityBool(caps["mutation_enabled"]) ||
		updaterPolicyCapabilityBool(caps["recovery_pending"]) ||
		strings.ToLower(updaterPolicyCapabilityString(caps["self_update_phase"])) != "stable" ||
		strings.ToLower(strings.TrimSpace(agent.ReportedOS)) != "linux" ||
		strings.ToLower(strings.TrimSpace(agent.ReportedArch)) != release.Arch {
		return systemUpdateHostPreviousRuntime{},
			ErrSystemUpdateExecutionHostBusy
	}
	previous := systemUpdateHostPreviousRuntime{
		agentVersion:    strings.TrimSpace(agent.ReportedVersion),
		executorVersion: updaterPolicyCapabilityString(caps["executor_version"]),
	}
	if updaterPolicyCapabilityString(
		caps["self_update_active_agent_version"],
	) != previous.agentVersion ||
		updaterPolicyCapabilityString(
			caps["self_update_active_executor_version"],
		) != previous.executorVersion {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	var okProtocol bool
	previous.agentProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["agent_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.executorProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["executor_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.mutationProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["mutation_protocol_version"],
	)
	if !okProtocol {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	previous.recoveryProtocol, okProtocol = hostSelfUpdateCapabilityProtocol(
		caps["recovery_protocol_version"],
	)
	if !okProtocol ||
		!systemUpdateHostSelfUpdateCurrentProtocolsAreCompatible(previous) ||
		!systemUpdateJobVersionPattern.MatchString(previous.agentVersion) ||
		!systemUpdateJobVersionPattern.MatchString(previous.executorVersion) ||
		!systemUpdateHostSelfUpdateTargetIsStrictlyNewer(
			release.Tag,
			previous.agentVersion,
			previous.executorVersion,
		) {
		return systemUpdateHostPreviousRuntime{}, ErrSystemUpdateAgentNotReady
	}
	return previous, nil
}

func hostSelfUpdateCapabilityProtocol(value any) (int, bool) {
	if parsed, ok := updaterPolicyCapabilityInt64(value); ok &&
		parsed >= 1 && parsed <= int64(^uint(0)>>1) {
		return int(parsed), true
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil && parsed >= 1 {
			return parsed, true
		}
	}
	return 0, false
}

func activeMemorySystemUpdateHostSelfUpdateForHostLocked(
	s *MemorySystemUpdateStore,
	executionHostID string,
) (SystemUpdateHostSelfUpdate, bool) {
	for _, update := range s.hostSelfUpdates {
		if update.ExecutionHostID == executionHostID &&
			!isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
			return update, true
		}
	}
	return SystemUpdateHostSelfUpdate{}, false
}
