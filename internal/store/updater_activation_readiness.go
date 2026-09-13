package store

import (
	"encoding/json"
	"strings"
	"time"
)

func registeredPullObserverReadyForActivation(
	service RegisteredService,
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	now time.Time,
) bool {
	if !PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(service.Status), "online") ||
		service.LastHeartbeatAt == nil ||
		service.LastHeartbeatAt.IsZero() {
		return false
	}
	heartbeatAge := now.Sub(service.LastHeartbeatAt.UTC())
	if heartbeatAge < 0 || heartbeatAge > pullUpdaterActivationHeartbeatMaxAge {
		return false
	}
	capabilities := service.ReportedCapabilities
	reportedEpoch, epochOK := updaterPolicyCapabilityInt64(capabilities["ownership_epoch"])
	reportedPolicyRevision, revisionOK := updaterPolicyCapabilityInt64(capabilities["policy_revision"])
	recoveryPending, recoveryPendingOK := capabilities["recovery_pending"].(bool)
	if !updaterPolicyCapabilityBool(capabilities["host_agent"]) ||
		!updaterPolicyCapabilityBool(capabilities["observe_only"]) ||
		!updaterPolicyCapabilityBool(capabilities["update_executor"]) ||
		updaterPolicyCapabilityBool(capabilities["mutation_enabled"]) ||
		!recoveryPendingOK ||
		recoveryPending ||
		updaterPolicyCapabilityString(capabilities["transport_mode"]) != SystemUpdateTransportPullV2 ||
		updaterPolicyCapabilityString(capabilities["agent_protocol_version"]) != "2" ||
		updaterPolicyCapabilityString(capabilities["execution_host_id"]) != service.ExecutionHostID ||
		!epochOK ||
		reportedEpoch != 0 ||
		!revisionOK ||
		reportedPolicyRevision != policy.ProjectionRevision ||
		!strings.EqualFold(updaterPolicyCapabilityString(capabilities["policy_status"]), "applied") {
		return false
	}

	availability := updaterPolicyCapabilityStringMap(capabilities["target_availability"])
	availabilityCodes := updaterPolicyCapabilityStringMap(capabilities["target_availability_codes"])
	reportedPorts := updaterPolicyCapabilityInt64Map(capabilities["reported_ports"])
	reportedServiceTypes := updaterPolicyCapabilityStringMap(capabilities["reported_service_types"])
	reportedDeploymentModes := updaterPolicyCapabilityStringMap(capabilities["reported_deployment_modes"])
	reportedPolicyRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_executor_policy_revisions"])
	reportedPolicyDigests := updaterPolicyCapabilityStringMap(capabilities["reported_executor_policy_sha256"])
	reportedConfigRevisions := updaterPolicyCapabilityInt64Map(capabilities["reported_config_revisions"])
	reportedConfigDigests := updaterPolicyCapabilityStringMap(capabilities["reported_config_sha256"])
	reportedPortDrift := updaterPolicyCapabilityBoolMap(capabilities["port_drift"])
	for _, target := range policy.Targets {
		targetService, exists := servicesByID[target.ServiceID]
		if !exists || targetService.ServiceType != target.ServiceType || targetService.AppliedEndpoint == nil {
			return false
		}
		expectedReportedPort := targetService.AppliedEndpoint.Port
		if target.DeploymentMode == "systemd" {
			var listenerOK bool
			expectedReportedPort, listenerOK = PullUpdaterPolicyTargetLocalListenPort(
				target,
				targetService,
			)
			if !listenerOK {
				return false
			}
		}
		expectedConfigRevision := targetService.AppliedConfigRevision
		if expectedConfigRevision < 1 {
			return false
		}
		portDrift, portDriftReported := reportedPortDrift[target.ServiceID]
		reportedConfigDigest := reportedConfigDigests[target.ServiceID]
		if availability[target.ServiceID] != "available" ||
			availabilityCodes[target.ServiceID] != "executor_verified" ||
			reportedServiceTypes[target.ServiceID] != target.ServiceType ||
			strings.ToLower(reportedDeploymentModes[target.ServiceID]) != target.DeploymentMode ||
			reportedPolicyRevisions[target.ServiceID] != policy.LocalExecutorPolicyRevision ||
			reportedPolicyDigests[target.ServiceID] != policy.LocalExecutorPolicySHA256 ||
			reportedConfigRevisions[target.ServiceID] != expectedConfigRevision ||
			!validSystemUpdateDigest(reportedConfigDigest) ||
			(targetService.AppliedConfigSHA256 != "" &&
				reportedConfigDigest != targetService.AppliedConfigSHA256) ||
			reportedPorts[target.ServiceID] != int64(expectedReportedPort) ||
			!portDriftReported ||
			portDrift {
			return false
		}
		if target.DeploymentMode == "docker" {
			if _, complete := systemUpdateDockerPortBaselineFromAgent(
				service,
				policy,
				targetService,
			); !complete {
				return false
			}
		}
	}
	return len(policy.Targets) > 0
}

// PullUpdaterObserverReadyForActivation reports whether the latest registered
// observer heartbeat exactly matches the server-owned pull policy projection.
// It intentionally excludes execution-host ownership and active-job checks,
// which are fenced again inside ActivatePullUpdaterOwnership.
func PullUpdaterObserverReadyForActivation(
	service RegisteredService,
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	now time.Time,
) bool {
	return registeredPullObserverReadyForActivation(service, policy, servicesByID, now)
}

func pullActivationBaselineReservations(
	policy UpdaterPolicy,
	servicesByID map[string]RegisteredService,
	observer RegisteredService,
) ([]ServicePortReservation, error) {
	reservations := make([]ServicePortReservation, 0, len(policy.Targets))
	seen := make(map[servicePortReservationKey]ServicePortReservation, len(policy.Targets))
	for _, target := range policy.Targets {
		// service_port_reservations is deliberately owned by rows in services.
		// Control Panel is a server-owned synthetic target and cannot satisfy
		// that foreign key. Its fixed listener is fenced against systemd and
		// Docker host-port changes at job creation and verified by Host Agent.
		if updaterPolicyControlPanelTarget(target) {
			continue
		}
		service, exists := servicesByID[target.ServiceID]
		if !exists || service.AppliedEndpoint == nil {
			return nil, ErrSystemUpdateAgentNotReady
		}
		port := service.AppliedEndpoint.Port
		switch target.DeploymentMode {
		case "systemd":
			var listenerOK bool
			port, listenerOK = PullUpdaterPolicyTargetLocalListenPort(target, service)
			if !listenerOK {
				return nil, ErrSystemUpdateAgentNotReady
			}
		case "docker":
			baseline, complete := systemUpdateDockerPortBaselineFromAgent(
				observer,
				policy,
				service,
			)
			if !complete {
				return nil, ErrSystemUpdateAgentNotReady
			}
			port = baseline.PublishedPort
		default:
			return nil, ErrSystemUpdateAgentNotReady
		}
		if port < 1024 || port > 65535 {
			return nil, ErrSystemUpdateAgentNotReady
		}
		reservation := ServicePortReservation{
			ExecutionHostID:  policy.ExecutionHostID,
			NetworkNamespace: "host",
			Protocol:         "tcp",
			Port:             port,
			ServiceID:        target.ServiceID,
			ServiceRole:      "api",
		}
		key := servicePortKey(reservation)
		if existing, duplicate := seen[key]; duplicate {
			if !sameServicePortReservationOwner(existing, reservation) {
				return nil, ErrServicePortReserved
			}
			continue
		}
		seen[key] = reservation
		reservations = append(reservations, reservation)
	}
	sortServicePortReservations(reservations)
	return reservations, nil
}

func updaterPolicyCapabilityBool(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func updaterPolicyCapabilityString(value any) string {
	parsed, _ := value.(string)
	return strings.TrimSpace(parsed)
}

func updaterPolicyCapabilityInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		if typed >= 0 {
			return int64(typed), true
		}
	case int64:
		if typed >= 0 {
			return typed, true
		}
	case float64:
		integer := int64(typed)
		if typed >= 0 && float64(integer) == typed {
			return integer, true
		}
	case json.Number:
		integer, err := typed.Int64()
		if err == nil && integer >= 0 {
			return integer, true
		}
	}
	return 0, false
}

func updaterPolicyCapabilityStringMap(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(string); ok {
				result[strings.TrimSpace(key)] = strings.TrimSpace(parsed)
			}
		}
	}
	return result
}

func updaterPolicyCapabilityInt64Map(value any) map[string]int64 {
	result := map[string]int64{}
	switch typed := value.(type) {
	case map[string]int:
		for key, item := range typed {
			if item >= 0 {
				result[strings.TrimSpace(key)] = int64(item)
			}
		}
	case map[string]int64:
		for key, item := range typed {
			if item >= 0 {
				result[strings.TrimSpace(key)] = item
			}
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := updaterPolicyCapabilityInt64(item); ok {
				result[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return result
}

func updaterPolicyCapabilityBoolMap(value any) map[string]bool {
	result := map[string]bool{}
	switch typed := value.(type) {
	case map[string]bool:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = item
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(bool); ok {
				result[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return result
}
