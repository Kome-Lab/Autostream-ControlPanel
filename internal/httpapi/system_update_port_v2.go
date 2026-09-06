package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

var errInvalidSystemUpdatePortMode = errors.New("invalid system update port mode")

func addSystemUpdatePortAuditFields(metadata map[string]any, plan *store.SystemUpdatePortReconfiguration) {
	if plan == nil || plan.PortContractVersion != 2 || plan.Before == nil || plan.Target == nil {
		return
	}
	metadata["port_contract_version"], metadata["mode"] = 2, plan.Mode
	metadata["old_local_listen_port"], metadata["new_local_listen_port"] = plan.Before.LocalListenPort, plan.Target.LocalListenPort
	metadata["old_advertised_port"], metadata["new_advertised_port"] = plan.Before.AdvertisedPort, plan.Target.AdvertisedPort
	metadata["expected_endpoint_revision"], metadata["target_endpoint_revision"] = plan.Before.EndpointRevision, plan.Target.EndpointRevision
	metadata["target_config_revision"] = plan.Target.ConfigRevision
	if plan.Before.Docker != nil && plan.Target.Docker != nil {
		metadata["old_published_port"], metadata["new_published_port"] = plan.Before.Docker.PublishedPort, plan.Target.Docker.PublishedPort
		metadata["old_container_port"], metadata["new_container_port"] = plan.Before.Docker.ContainerPort, plan.Target.Docker.ContainerPort
	}
}

// Public port creation has one closed, versioned input. Historical jobs remain
// readable, but a legacy request must never become an implicit combined change.
func decodeSystemUpdatePortV2CreateRequest(payload []byte) (systemUpdateCreateRequest, error) {
	var version struct {
		PortContractVersion int `json:"port_contract_version"`
	}
	if json.Unmarshal(payload, &version) != nil {
		return systemUpdateCreateRequest{}, errInvalidSystemUpdatePortMode
	}
	if version.PortContractVersion != 2 {
		return systemUpdateCreateRequest{}, store.ErrSystemUpdatePortContractRequired
	}
	if contracts.ValidateSystemUpdatePortCreateRequest(payload) != nil {
		return systemUpdateCreateRequest{}, errInvalidSystemUpdatePortMode
	}
	var input contracts.SystemUpdatePortCreateRequestV2
	if json.Unmarshal(payload, &input) != nil {
		return systemUpdateCreateRequest{}, errInvalidSystemUpdatePortMode
	}
	request := systemUpdateCreateRequest{
		Operation: store.SystemUpdateOperationPortReconfigure, PortContractVersion: 2,
		Mode: string(input.Mode), TargetID: input.TargetID, ExpectedSnapshotID: input.ExpectedSnapshotID,
		ExpectedEndpointRevision: input.ExpectedEndpointRevision, DesiredRevision: input.DesiredRevision,
		Fence: input.Fence, IdempotencyKey: input.IdempotencyKey,
	}
	if input.NewLocalListenPort != nil {
		request.NewLocalListenPort = *input.NewLocalListenPort
	}
	if input.NewAdvertisedPort != nil {
		request.NewAdvertisedPort = *input.NewAdvertisedPort
	}
	if input.NewPublishedPort != nil {
		request.Docker = true
		request.NewPublishedPort = *input.NewPublishedPort
	}
	if input.NewContainerPort != nil {
		request.NewContainerPort = *input.NewContainerPort
	}
	return request, nil
}

func sameSystemUpdatePortV2CreateRequest(job store.SystemUpdateJob, request systemUpdateCreateRequest) bool {
	p := job.PortReconfigure
	if p == nil || p.PortContractVersion != 2 || p.Before == nil || p.Target == nil ||
		string(p.Mode) != request.Mode || p.Before.SnapshotID != request.ExpectedSnapshotID ||
		p.Before.EndpointRevision != request.ExpectedEndpointRevision || p.Target.ConfigRevision != request.DesiredRevision ||
		job.OwnershipEpoch != request.Fence || (p.Target.Docker != nil) != request.Docker {
		return false
	}
	if request.Mode == "local_and_advertised" && p.Target.AdvertisedPort != request.NewAdvertisedPort {
		return false
	}
	if request.Docker {
		return p.Target.Docker.PublishedPort == request.NewPublishedPort && p.Target.Docker.ContainerPort == request.NewContainerPort
	}
	return p.Target.LocalListenPort == request.NewLocalListenPort
}

func writeSystemUpdatePortV2Error(w http.ResponseWriter, err error) bool {
	status, code := http.StatusConflict, ""
	switch {
	case errors.Is(err, store.ErrSystemUpdateAdvertisedOnlyUnsupported):
		status, code = http.StatusBadRequest, "system_update_advertised_only_unsupported"
	case errors.Is(err, store.ErrSystemUpdatePortContractRequired):
		code = "system_update_port_contract_required"
	case errors.Is(err, store.ErrSystemUpdatePortPolicySnapshotUnavailable):
		code = "system_update_port_policy_snapshot_unavailable"
	case errors.Is(err, store.ErrSystemUpdatePortSnapshotStale):
		code = "system_update_port_snapshot_stale"
	case errors.Is(err, store.ErrSystemUpdatePortIdempotencyConflict):
		code = "system_update_port_idempotency_conflict"
	case errors.Is(err, store.ErrSystemUpdatePortResultMismatch):
		code = "system_update_port_result_mismatch"
	case errors.Is(err, store.ErrSystemUpdatePortRecoveryRequired):
		code = "system_update_port_recovery_required"
	case errors.Is(err, store.ErrSystemUpdateExecutionHostBusy):
		code = "system_update_host_busy"
	default:
		return false
	}
	writeJSON(w, status, map[string]string{"code": code})
	return true
}

type systemUpdatePortSnapshotStore interface {
	GetSystemUpdatePortPolicySnapshot(context.Context, store.ServiceRegistryStore, store.UpdaterPolicyStore, store.SystemUpdatePortSnapshotParams) (store.SystemUpdatePortPolicySnapshot, error)
	ConfirmSystemUpdatePortPolicyBaseline(context.Context, store.ServiceRegistryStore, store.UpdaterPolicyStore, store.ConfirmSystemUpdatePortPolicyBaselineParams) error
}

// Only the existing configure serializer may materialize a baseline. The
// store compares its exact bytes against the previously bound root digest.
// UID/GID metadata cannot introduce a new origin, profile, service, or path.
func (s *Server) systemUpdatePortSnapshotBuilder(panelURL string) store.SystemUpdatePortPolicySnapshotBuilder {
	return func(policy store.UpdaterPolicy, services []store.RegisteredService) (store.SystemUpdatePortPolicyMaterialization, error) {
		byID := make(map[string]store.RegisteredService, len(services))
		for _, service := range services {
			byID[service.ServiceID] = service
		}
		baseline, err := systemUpdatePortBaseline(byID[policy.UpdaterID].ReportedCapabilities["port_policy_baseline"])
		if err != nil || strings.TrimSpace(panelURL) == "" {
			return store.SystemUpdatePortPolicyMaterialization{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if err := addControlPanelSystemUpdateServiceForPolicy(byID, policy); err != nil {
			return store.SystemUpdatePortPolicyMaterialization{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		targets := make([]updateradapter.HostAgentConfigurePolicyTarget, 0, len(policy.Targets))
		for _, target := range policy.Targets {
			service, ok := byID[target.ServiceID]
			if !ok || target.HostID != policy.ExecutionHostID || target.ServiceType != service.ServiceType || service.AppliedEndpoint == nil {
				return store.SystemUpdatePortPolicyMaterialization{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			appliedEndpointRevision := service.AppliedEndpointRevision
			if target.ServiceID == controlPanelSystemUpdateServiceID && target.ServiceType == controlPanelSystemUpdateServiceType {
				appliedEndpointRevision = service.EndpointRevision
			}
			localPort, available := store.PullUpdaterPolicyTargetLocalListenPort(target, service)
			var docker *contracts.SystemUpdatePortDockerSnapshot
			var dockerRoot *contracts.UpdaterPortDockerRootBaseline
			if target.DeploymentMode == "docker" {
				for _, observed := range baseline.Targets {
					if observed.ServiceID == target.ServiceID {
						docker, dockerRoot = observed.Docker, observed.DockerRoot
					}
				}
				if docker != nil && dockerRoot != nil {
					localPort, available = docker.PublishedPort, true
				}
			}
			if !available || appliedEndpointRevision < 1 {
				return store.SystemUpdatePortPolicyMaterialization{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			targets = append(targets, updateradapter.HostAgentConfigurePolicyTarget{
				DockerSnapshot: docker, DockerRoot: dockerRoot,
				ServiceID: target.ServiceID, ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode,
				DatabaseName: target.DatabaseName, EndpointRevision: appliedEndpointRevision,
				AppliedConfigRevision: service.AppliedConfigRevision, AppliedConfigSHA256: service.AppliedConfigSHA256,
				AppliedEndpointPort: service.AppliedEndpoint.Port, LocalListenPort: localPort,
			})
		}
		projection, err := updateradapter.BuildSystemUpdatePortPolicy(updateradapter.HostAgentConfigurePolicySource{
			PanelURL: panelURL, ExecutionHostID: policy.ExecutionHostID, AgentUID: baseline.AgentUID, AgentGID: baseline.AgentGID,
			SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision,
			LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision, Targets: targets,
		})
		if err != nil {
			return store.SystemUpdatePortPolicyMaterialization{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		return store.SystemUpdatePortPolicyMaterialization{ExecutorPolicyJSON: projection.Policy}, nil
	}
}

func systemUpdatePortBaseline(value any) (contracts.UpdaterPortPolicyBaseline, error) {
	var baseline contracts.UpdaterPortPolicyBaseline
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > maxSystemUpdateV2PayloadBytes {
		return baseline, store.ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&baseline) != nil || baseline.PortContractVersion != 2 || baseline.PolicyTransitionVersion != 1 ||
		baseline.AgentUID == 0 || baseline.AgentGID == 0 || baseline.SourcePolicyRevision < 1 || baseline.ProjectionRevision < 1 ||
		baseline.ExecutorPolicyRevision < 1 || !validUpdateManifestDigest(baseline.ExecutorPolicySHA256) ||
		len(baseline.Targets) == 0 || baseline.ObservedAt.IsZero() || baseline.ObservedAt.After(time.Now().Add(time.Minute)) {
		return contracts.UpdaterPortPolicyBaseline{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if contracts.ValidateUpdaterPortPolicyBaseline(baseline) != nil {
		return contracts.UpdaterPortPolicyBaseline{}, store.ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	return baseline, nil
}

func (s *Server) confirmSystemUpdatePortBaseline(ctx context.Context, service store.RegisteredService, panelURL string) error {
	value, supplied := service.ReportedCapabilities["port_policy_baseline"]
	if !supplied {
		return nil
	}
	baseline, err := systemUpdatePortBaseline(value)
	if err != nil {
		return err
	}
	coordinator, ok := s.systemUpdates.(systemUpdatePortSnapshotStore)
	if !ok {
		return store.ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	revisions := make(map[string]int64, len(baseline.Targets))
	for _, target := range baseline.Targets {
		if target.ServiceID == "" || target.EndpointRevision < 1 {
			return store.ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if _, duplicate := revisions[target.ServiceID]; duplicate {
			return store.ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		revisions[target.ServiceID] = target.EndpointRevision
	}
	controlPanel, _ := controlPanelPullUpdaterRuntimeTarget()
	return coordinator.ConfirmSystemUpdatePortPolicyBaseline(ctx, s.services, s.updaterPolicies, store.ConfirmSystemUpdatePortPolicyBaselineParams{
		Baseline:       &baseline,
		AgentServiceID: service.ServiceID, ExpectedSourcePolicyRevision: baseline.SourcePolicyRevision,
		ExpectedProjectionRevision: baseline.ProjectionRevision, ExpectedExecutorPolicyRevision: baseline.ExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256: baseline.ExecutorPolicySHA256, AppliedEndpointRevisions: revisions,
		BuildPolicySnapshot: s.systemUpdatePortSnapshotBuilder(panelURL), ControlPanelTarget: controlPanel,
	})
}

func (s *Server) decorateSystemUpdatePortSnapshots(r *http.Request, targets []systemUpdateTargetResponse) {
	coordinator, supported := s.systemUpdates.(systemUpdatePortSnapshotStore)
	controlPanel, _ := controlPanelPullUpdaterRuntimeTarget()
	for i := range targets {
		target := &targets[i]
		if !supportedSystemUpdatePortServiceType(target.ServiceType) {
			continue
		}
		target.PortContractVersion = 2
		code := "system_update_port_policy_snapshot_unavailable"
		if supported {
			snapshot, err := coordinator.GetSystemUpdatePortPolicySnapshot(r.Context(), s.services, s.updaterPolicies, store.SystemUpdatePortSnapshotParams{
				TargetID: target.TargetID, ControlPanelTarget: controlPanel, BuildPolicySnapshot: s.systemUpdatePortSnapshotBuilder(panelBaseURL(r)),
			})
			if err == nil {
				ref := snapshot.Ref
				target.OwnershipEpoch = snapshot.Snapshot.OwnershipEpoch
				target.PortPolicySnapshotID, target.LocalListenPort = ref.SnapshotID, ref.LocalListenPort
				target.EndpointRevision, target.AppliedEndpointRevision, target.AppliedConfigRevision = ref.EndpointRevision, ref.AppliedEndpointRevision, ref.ConfigRevision
				target.PortModes = []string{"local_only", "local_and_advertised"}
				continue
			}
			if errors.Is(err, store.ErrSystemUpdatePortContractRequired) {
				code = "system_update_port_contract_required"
			}
		}
		filtered := target.EligibleOperations[:0]
		for _, operation := range target.EligibleOperations {
			if operation != store.SystemUpdateOperationPortReconfigure {
				filtered = append(filtered, operation)
			}
		}
		target.EligibleOperations = filtered
		if target.OperationBlockedReasons == nil {
			target.OperationBlockedReasons = make(map[string]string)
		}
		target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] = code
	}
}

func systemUpdatePortRecoveryPolicyMatches(job store.SystemUpdateJob, ownership store.SystemUpdateExecutionHost, policy store.UpdaterPolicy) bool {
	plan := systemUpdateV2PortPlan(job.PortReconfigure)
	if plan == nil || contracts.ValidateSystemUpdatePortPlan(*plan) != nil || job.PolicyRevision != plan.Before.ProjectionRevision ||
		ownership.PolicyRevision != policy.ProjectionRevision || job.Status == store.SystemUpdateStatusCancelled ||
		(job.Status == store.SystemUpdateStatusFailed && !job.RecoveryRequired && job.PortResult == nil) {
		return false
	}
	for _, ref := range []*contracts.SystemUpdatePortSnapshotRef{plan.Before, plan.Target, plan.Rollback} {
		if policy.Revision == ref.SourcePolicyRevision && policy.ProjectionRevision == ref.ProjectionRevision &&
			policy.LocalExecutorPolicyRevision == ref.ExecutorPolicyRevision && policy.LocalExecutorPolicySHA256 == ref.ExecutorPolicySHA256 {
			return true
		}
	}
	return false
}

// A consumed job's desired projection is T while applied service metadata is
// still B. Resolve the projection from that exact durable plan, never by
// changing the applied rows or guessing revisions from the latest heartbeat.
func (s *Server) projectSystemUpdatePortPolicyTarget(ctx context.Context, agent store.RegisteredService, policy store.UpdaterPolicy, item *hostAgentPolicyTarget) error {
	if projections, ok := s.systemUpdates.(store.SystemUpdatePortProjectionStore); ok {
		snapshot, err := projections.GetSystemUpdatePortPolicyProjection(ctx, item.ServiceID, policy)
		if err == nil {
			return applySystemUpdatePortPolicyTargetRef(policy, item, snapshot.Ref)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	job, err := s.systemUpdates.GetActiveSystemUpdateJob(ctx, item.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		projectInitialSystemUpdatePortDockerBaseline(agent, policy, item)
		return nil
	}
	if err != nil {
		return err
	}
	if job.PortReconfigure == nil || job.PortReconfigure.PortContractVersion != 2 {
		return nil
	}
	plan := systemUpdateV2PortPlan(job.PortReconfigure)
	if plan == nil || contracts.ValidateSystemUpdatePortPlan(*plan) != nil || job.AgentServiceID != policy.UpdaterID ||
		job.ExecutionHostID != policy.ExecutionHostID || job.PolicyRevision != plan.Before.ProjectionRevision {
		return store.ErrSystemUpdatePortSnapshotStale
	}
	for _, ref := range []*contracts.SystemUpdatePortSnapshotRef{plan.Before, plan.Target, plan.Rollback} {
		if ref.SourcePolicyRevision != policy.Revision || ref.ProjectionRevision != policy.ProjectionRevision ||
			ref.ExecutorPolicyRevision != policy.LocalExecutorPolicyRevision || ref.ExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 {
			continue
		}
		return applySystemUpdatePortPolicyTargetRef(policy, item, *ref)
	}
	return store.ErrSystemUpdatePortSnapshotStale
}

func applySystemUpdatePortPolicyTargetRef(policy store.UpdaterPolicy, item *hostAgentPolicyTarget, ref contracts.SystemUpdatePortSnapshotRef) error {
	if ref.SourcePolicyRevision != policy.Revision || ref.ProjectionRevision != policy.ProjectionRevision ||
		ref.ExecutorPolicyRevision != policy.LocalExecutorPolicyRevision || ref.ExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 ||
		ref.LocalListenPort < 1024 || ref.LocalListenPort > 65535 || (item.DeploymentMode == "docker") != (ref.Docker != nil) {
		return store.ErrSystemUpdatePortSnapshotStale
	}
	var advertised *store.ServiceEndpoint
	for _, candidate := range []*store.ServiceEndpoint{item.AppliedEndpoint, item.DesiredEndpoint} {
		if candidate == nil {
			continue
		}
		digest, err := contracts.ComputeSystemUpdatePortEndpointSHA256(contracts.SystemUpdatePortEndpoint{Host: candidate.Host, Port: candidate.Port, SSLEnabled: candidate.SSLEnabled, PublicURL: candidate.PublicURL})
		if err == nil && candidate.Port == ref.AdvertisedPort && digest == ref.AdvertisedEndpointSHA256 {
			advertised = copyHostAgentEndpoint(candidate)
			break
		}
	}
	if advertised == nil {
		return store.ErrSystemUpdatePortSnapshotStale
	}
	item.AppliedEndpoint = advertised
	item.DesiredEndpoint = copyHostAgentEndpoint(advertised)
	item.AppliedConfigRevision, item.AppliedConfigSHA256 = ref.ConfigRevision, ref.ConfigSHA256
	item.EndpointRevision = ref.EndpointRevision
	item.AppliedEndpointRevision = ref.AppliedEndpointRevision
	item.LocalListenEndpoint = localSystemdHostAgentEndpoint(ref.LocalListenPort)
	item.LocalHealthEndpoint = copyHostAgentEndpoint(item.LocalListenEndpoint)
	return nil
}

// Before the first accepted port job, the authenticated baseline can supply a
// Docker local endpoint only when it still describes the exact bound policy and
// applied target. Stale or absent observations keep local endpoints unavailable;
// they cannot authorize a port job, and do not prevent ordinary policy polling.
func projectInitialSystemUpdatePortDockerBaseline(agent store.RegisteredService, policy store.UpdaterPolicy, item *hostAgentPolicyTarget) {
	if item.DeploymentMode != "docker" || agent.ServiceID != policy.UpdaterID || agent.ExecutionHostID != policy.ExecutionHostID ||
		agent.OwnershipEpoch < 1 || item.AppliedEndpointRevision < 1 || !reflect.DeepEqual(item.DesiredEndpoint, item.AppliedEndpoint) {
		return
	}
	baseline, err := systemUpdatePortBaseline(agent.ReportedCapabilities["port_policy_baseline"])
	if err != nil || baseline.SourcePolicyRevision != policy.Revision || baseline.ProjectionRevision != policy.ProjectionRevision ||
		baseline.ExecutorPolicyRevision != policy.LocalExecutorPolicyRevision || baseline.ExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 {
		return
	}
	for _, target := range baseline.Targets {
		if target.ServiceID != item.ServiceID {
			continue
		}
		if string(target.ServiceType) != item.ServiceType || string(target.DeploymentMode) != item.DeploymentMode || target.Docker == nil || target.DockerRoot == nil ||
			target.EndpointRevision != item.AppliedEndpointRevision || target.ConfigRevision != item.AppliedConfigRevision || target.ConfigSHA256 != item.AppliedConfigSHA256 ||
			target.LocalListenPort != target.Docker.PublishedPort || target.Docker.HealthPort != target.Docker.PublishedPort {
			return
		}
		digest, err := contracts.SystemUpdateDockerPortConfigSHA256(target.ServiceType, target.Docker.PublishedPort, target.Docker.ContainerPort, target.ConfigRevision)
		if err != nil || digest != target.ConfigSHA256 {
			return
		}
		item.LocalListenEndpoint = localSystemdHostAgentEndpoint(target.LocalListenPort)
		item.LocalHealthEndpoint = copyHostAgentEndpoint(item.LocalListenEndpoint)
		return
	}
}
