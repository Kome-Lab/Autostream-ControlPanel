package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"strings"
	"time"
)

func (s *Server) systemUpdateEligibleTargetsForAgent(ctx context.Context, agent store.RegisteredService) (map[string]string, error) {
	return s.systemUpdateTargetsForAgentClaim(ctx, agent, false)
}

func (s *Server) systemUpdateTargetsForAgentClaim(ctx context.Context, agent store.RegisteredService, allowBusyRecovery bool) (map[string]string, error) {
	return s.systemUpdateTargetsForAgentHostClaim(ctx, agent, "", allowBusyRecovery)
}

// systemUpdatePullRecoveryEligibleTarget deliberately does not consume the
// agent's reported target availability or executor-probe state. A target can
// be unhealthy precisely because an Apply was interrupted. Recovery is
// limited to the single durable job target and is instead fenced by the
// server-owned agent, execution-host ownership and updater-policy snapshots.
func (s *Server) systemUpdatePullRecoveryEligibleTarget(
	ctx context.Context,
	agent store.RegisteredService,
	hostID string,
	job store.SystemUpdateJob,
) (map[string]string, error) {
	hostID = strings.TrimSpace(hostID)
	if systemUpdateAgentTransportMode(agent) != store.SystemUpdateTransportPullV2 ||
		job.AgentServiceID != agent.ServiceID ||
		job.ExecutionHostID != hostID ||
		job.TransportMode != store.SystemUpdateTransportPullV2 ||
		job.OwnershipEpoch < 1 ||
		job.OwnershipEpoch != agent.OwnershipEpoch ||
		job.PolicyRevision < 1 {
		return nil, store.ErrSystemUpdateOwnershipConflict
	}

	ownershipStore, ok := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	if !ok {
		return nil, store.ErrSystemUpdateOwnershipConflict
	}
	ownership, err := ownershipStore.GetSystemUpdateExecutionHost(ctx, hostID)
	if err != nil {
		return nil, err
	}
	portV2 := job.Operation == store.SystemUpdateOperationPortReconfigure && job.PortReconfigure != nil && job.PortReconfigure.PortContractVersion == 2
	if ownership.ExecutionHostID != hostID ||
		ownership.TransportMode != store.SystemUpdateTransportPullV2 ||
		ownership.AgentServiceID != agent.ServiceID ||
		ownership.OwnershipEpoch != job.OwnershipEpoch ||
		(!portV2 && ownership.PolicyRevision != job.PolicyRevision) {
		return nil, store.ErrSystemUpdateOwnershipConflict
	}

	policy, err := s.updaterPolicies.GetUpdaterPolicy(ctx, agent.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.ErrSystemUpdateActiveUnavailable
	}
	if err != nil {
		return nil, err
	}
	if policy.UpdaterID != agent.ServiceID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		strings.TrimSpace(policy.ExecutionHostID) != hostID ||
		(!portV2 && policy.ProjectionRevision != job.PolicyRevision) ||
		policy.LocalExecutorPolicyRevision < 1 ||
		!validUpdateManifestDigest(policy.LocalExecutorPolicySHA256) ||
		!store.PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return nil, store.ErrSystemUpdateActiveUnavailable
	}

	deploymentMode := strings.ToLower(strings.TrimSpace(job.DeploymentMode))
	if job.DeploymentMode != deploymentMode ||
		(deploymentMode != "systemd" && deploymentMode != "docker") ||
		!validSystemUpdateCapabilityIdentifier(job.TargetID) ||
		!validSystemUpdateCapabilityIdentifier(job.TargetServiceType) {
		return nil, store.ErrSystemUpdateActiveUnavailable
	}
	switch job.Operation {
	case store.SystemUpdateOperationSoftwareUpdate:
		if job.PortReconfigure != nil {
			return nil, store.ErrSystemUpdateActiveUnavailable
		}
	case store.SystemUpdateOperationPortReconfigure:
		if portV2 {
			if !systemUpdatePortRecoveryPolicyMatches(job, ownership, policy) {
				return nil, store.ErrSystemUpdateActiveUnavailable
			}
			break
		}
		if job.PortReconfigure == nil ||
			job.PortReconfigure.ExpectedSourcePolicyRevision != policy.Revision ||
			job.PortReconfigure.ExpectedUpdaterPolicyRevision != policy.ProjectionRevision ||
			job.PortReconfigure.ExpectedExecutorPolicyRevision != policy.LocalExecutorPolicyRevision ||
			job.PortReconfigure.ExpectedExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 {
			return nil, store.ErrSystemUpdateActiveUnavailable
		}
	default:
		return nil, store.ErrSystemUpdateActiveUnavailable
	}

	matchingTargets := 0
	for _, target := range policy.Targets {
		if strings.TrimSpace(target.ServiceID) != job.TargetID {
			continue
		}
		matchingTargets++
		if strings.TrimSpace(target.HostID) != hostID ||
			strings.TrimSpace(target.ServiceType) != job.TargetServiceType ||
			strings.ToLower(strings.TrimSpace(target.DeploymentMode)) != deploymentMode {
			return nil, store.ErrSystemUpdateActiveUnavailable
		}
	}
	if matchingTargets != 1 {
		return nil, store.ErrSystemUpdateActiveUnavailable
	}

	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	servicesByID := make(map[string]store.RegisteredService, len(services)+1)
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}
	if err := addControlPanelSystemUpdateServiceForPolicy(servicesByID, policy); err != nil {
		return nil, err
	}
	targetService, ok := servicesByID[job.TargetID]
	if !ok ||
		targetService.ServiceID != job.TargetID ||
		targetService.ServiceType == "update_agent" ||
		targetService.ServiceType != job.TargetServiceType {
		return nil, store.ErrSystemUpdateActiveUnavailable
	}
	return map[string]string{job.TargetID: deploymentMode}, nil
}

func (s *Server) systemUpdateTargetsForAgentHostClaim(ctx context.Context, agent store.RegisteredService, hostID string, allowBusyRecovery bool) (map[string]string, error) {
	var managedPolicy *store.UpdaterPolicy
	policy, err := s.updaterPolicies.GetUpdaterPolicy(ctx, agent.ServiceID)
	switch {
	case err == nil:
		managedPolicy = &policy
	case errors.Is(err, store.ErrNotFound):
	default:
		return nil, err
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.RegisteredService, len(services))
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	if managedPolicy != nil {
		if err := addControlPanelSystemUpdateServiceForPolicy(byID, *managedPolicy); err != nil {
			return nil, err
		}
	}
	approved := approvedSystemUpdateAgentTargetAssignmentsForPolicyWithHosts(
		agent,
		time.Now().UTC(),
		managedPolicy,
		byID,
	)
	eligible := map[string]string{}
	for _, targetID := range sortedApprovedSystemUpdateTargetIDs(approved) {
		targetApproval := approved[targetID]
		if targetApproval.PolicyManaged && !targetApproval.PolicyReady {
			continue
		}
		if hostID != "" && targetApproval.Host.HostID != hostID {
			continue
		}
		if !allowBusyRecovery && targetApproval.Host.Reachability != "reachable" {
			continue
		}
		mode := targetApproval.DeploymentMode
		if targetID == "control-panel" {
			if targetApproval.ServiceType != "" && targetApproval.ServiceType != "control_panel" {
				continue
			}
			busy, err := s.systemUpdateControlPanelBusy(ctx)
			if err != nil {
				return nil, err
			}
			if allowBusyRecovery || !busy {
				eligible[targetID] = mode
			}
			continue
		}
		target, ok := byID[targetID]
		if !ok || target.ServiceType == "update_agent" || (targetApproval.ServiceType != "" && targetApproval.ServiceType != target.ServiceType) {
			continue
		}
		busy, err := s.systemUpdateServiceBusy(ctx, target)
		if err != nil {
			return nil, err
		}
		if allowBusyRecovery || !busy {
			eligible[targetID] = mode
		}
	}
	return eligible, nil
}

func (s *Server) systemUpdateServiceBusy(ctx context.Context, service store.RegisteredService) (bool, error) {
	streamID := strings.TrimSpace(service.CurrentStreamID)
	if streamID != "" {
		active, err := s.systemUpdateStreamActive(ctx, streamID)
		if err != nil || active {
			return active, err
		}
	}
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, service.ServiceID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		assignmentStreamID := strings.TrimSpace(assignment.StreamID)
		if assignmentStreamID == "" || assignmentStreamID == streamID {
			continue
		}
		active, err := s.systemUpdateStreamActive(ctx, assignmentStreamID)
		if err != nil || active {
			return active, err
		}
	}
	return false, nil
}

func (s *Server) systemUpdateControlPanelBusy(ctx context.Context) (bool, error) {
	if activeStore, ok := s.streams.(store.ActiveStreamStore); ok {
		return activeStore.HasActiveStream(ctx)
	}
	activeStreams, err := s.systemUpdateActiveStreams(ctx)
	return len(activeStreams) > 0, err
}

func (s *Server) systemUpdateStreamActive(ctx context.Context, streamID string) (bool, error) {
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isActiveStreamStatus(stream.Status), nil
}

func (s *Server) systemUpdateActiveStreams(ctx context.Context) (map[string]bool, error) {
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool)
	for _, stream := range streams {
		if isActiveStreamStatus(stream.Status) {
			active[stream.ID] = true
		}
	}
	return active, nil
}
