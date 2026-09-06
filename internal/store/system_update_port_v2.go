package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

type SystemUpdatePortResultV2 = contracts.SystemUpdatePortResultV2
type SystemUpdatePortPolicyMaterialization struct{ ExecutorPolicyJSON json.RawMessage }

// The builder is server-owned and executes only pure profile materialization.
// It must never perform network, filesystem, or database operations under locks.
type SystemUpdatePortPolicySnapshotBuilder func(UpdaterPolicy, []RegisteredService) (SystemUpdatePortPolicyMaterialization, error)
type SystemUpdatePortSnapshotParams struct {
	TargetID            string
	ControlPanelTarget  *PullUpdaterControlPanelTarget
	BuildPolicySnapshot SystemUpdatePortPolicySnapshotBuilder
}
type ConfirmSystemUpdatePortPolicyBaselineParams struct {
	Baseline                                                                                 *contracts.UpdaterPortPolicyBaseline
	AgentServiceID                                                                           string
	ExpectedSourcePolicyRevision, ExpectedProjectionRevision, ExpectedExecutorPolicyRevision int64
	ExpectedExecutorPolicySHA256                                                             string
	AppliedEndpointRevisions                                                                 map[string]int64
	BuildPolicySnapshot                                                                      SystemUpdatePortPolicySnapshotBuilder
	ControlPanelTarget                                                                       *PullUpdaterControlPanelTarget
}
type SystemUpdatePortPolicySnapshot struct {
	Ref      contracts.SystemUpdatePortSnapshotRef    `json:"ref"`
	Snapshot contracts.SystemUpdatePortPolicySnapshot `json:"snapshot"`
}
type SystemUpdatePortSnapshotStore interface {
	GetSystemUpdatePortPolicySnapshot(context.Context, ServiceRegistryStore, UpdaterPolicyStore, SystemUpdatePortSnapshotParams) (SystemUpdatePortPolicySnapshot, error)
	ConfirmSystemUpdatePortPolicyBaseline(context.Context, ServiceRegistryStore, UpdaterPolicyStore, ConfirmSystemUpdatePortPolicyBaselineParams) error
}

var (
	ErrSystemUpdatePortPolicySnapshotUnavailable = errors.New("system update complete port policy snapshot unavailable")
	ErrSystemUpdatePortSnapshotStale             = errors.New("system update port policy snapshot stale")
	ErrSystemUpdateAdvertisedOnlyUnsupported     = errors.New("system update advertised-only port change unsupported")
	ErrSystemUpdatePortContractRequired          = errors.New("system update port contract version 2 required")
	ErrSystemUpdatePortResultMismatch            = errors.New("system update port result does not match immutable snapshot")
	ErrSystemUpdatePortRecoveryRequired          = errors.New("system update port recovery required")
	ErrSystemUpdatePortIdempotencyConflict       = errors.New("system update port idempotency conflict")
)

// Immutable snapshots are inserted once. Only phase and the two distinct
// result slots change; rollback_failed never consumes the accepted slot.
type systemUpdatePortTransaction struct {
	Plan                     *SystemUpdatePortReconfiguration `json:"plan"`
	Before, Target, Rollback SystemUpdatePortPolicySnapshot
	RequestSHA256            string
	CancelEndpointRevision   int64
	Phase                    string
	RecoveryRequired         bool
	AcceptedResult           *SystemUpdatePortResultV2
	LastRecoveryObservation  *SystemUpdatePortResultV2
}

func isSystemUpdatePortV2(job SystemUpdateJob) bool {
	return job.Operation == SystemUpdateOperationPortReconfigure && job.PortReconfigure != nil && job.PortReconfigure.PortContractVersion == 2
}
func systemUpdatePortContractsPlan(plan *SystemUpdatePortReconfiguration) contracts.SystemUpdatePortReconfiguration {
	var out contracts.SystemUpdatePortReconfiguration
	body, _ := json.Marshal(plan)
	_ = json.Unmarshal(body, &out)
	return out
}
func systemUpdatePortV2NoOp(job SystemUpdateJob) bool {
	return isSystemUpdatePortV2(job) && contracts.SystemUpdatePortPlanIsNoOp(systemUpdatePortContractsPlan(job.PortReconfigure))
}
func systemUpdateJobHoldsHost(job SystemUpdateJob) bool {
	return !isTerminalSystemUpdateStatus(job.Status) || isSystemUpdatePortV2(job) && job.RecoveryRequired
}
func systemUpdatePortV2Recoverable(job SystemUpdateJob) bool {
	return isSystemUpdatePortV2(job) && job.RecoveryRequired && job.PortResult == nil && job.portTransaction != nil && job.portTransaction.Phase == "rollback_latched"
}
func cloneSystemUpdatePortV2Result(value *SystemUpdatePortResultV2) *SystemUpdatePortResultV2 {
	if value == nil {
		return nil
	}
	body, _ := json.Marshal(value)
	var out SystemUpdatePortResultV2
	_ = json.Unmarshal(body, &out)
	return &out
}
func cloneSystemUpdatePortTransaction(value *systemUpdatePortTransaction) *systemUpdatePortTransaction {
	if value == nil {
		return nil
	}
	body, _ := json.Marshal(value)
	var out systemUpdatePortTransaction
	_ = json.Unmarshal(body, &out)
	return &out
}
func projectSystemUpdatePortTransaction(job *SystemUpdateJob) {
	if job.portTransaction == nil {
		return
	}
	job.PortReconfigure = cloneSystemUpdatePortReconfiguration(job.portTransaction.Plan)
	job.PortResult = cloneSystemUpdatePortV2Result(job.portTransaction.AcceptedResult)
	job.RecoveryRequired = job.portTransaction.RecoveryRequired
	job.LastRecoveryObservation = cloneSystemUpdatePortV2Result(job.portTransaction.LastRecoveryObservation)
}

func validateCreateSystemUpdatePortV2Params(params CreateSystemdPortReconfigurationJobParams) error {
	if params.PortContractVersion != 2 || params.NewPort != 0 ||
		!serviceIDPattern.MatchString(params.TargetID) || params.NewLocalListenPort < 1024 || params.NewLocalListenPort > 65535 ||
		params.ExpectedEndpointRevision < 1 || params.ExpectedEndpointRevision > 9007199254740989 ||
		params.ExpectedDesiredRevision < 1 || params.ExpectedFence < 1 ||
		!strings.HasPrefix(params.ExpectedSnapshotID, "ps1:") || len(params.ExpectedSnapshotID) != 68 ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 || containsControl(params.IdempotencyKey) ||
		params.RequestedByUserID == "" || len(params.RequestedByUserID) > 64 || len(params.RequestedByUsername) > 255 {
		return ErrInvalidSystemUpdate
	}
	switch params.Mode {
	case contracts.SystemUpdatePortModeLocalOnly:
		if params.NewAdvertisedPort != 0 {
			return ErrInvalidSystemUpdate
		}
	case contracts.SystemUpdatePortModeLocalAndAdvertised:
		if params.NewAdvertisedPort < 1 || params.NewAdvertisedPort > 65535 {
			return ErrInvalidSystemUpdate
		}
	default:
		return ErrInvalidSystemUpdate
	}
	return nil
}

func systemUpdatePortV2DockerParams(params CreateDockerPortReconfigurationJobParams) CreateSystemdPortReconfigurationJobParams {
	return CreateSystemdPortReconfigurationJobParams{PortContractVersion: params.PortContractVersion, Mode: params.Mode, TargetID: params.TargetID, NewLocalListenPort: params.NewPublishedPort, NewAdvertisedPort: params.NewAdvertisedPort, newContainerPort: params.NewContainerPort, ExpectedSnapshotID: params.ExpectedSnapshotID, ExpectedEndpointRevision: params.ExpectedEndpointRevision, ExpectedDesiredRevision: params.ExpectedDesiredRevision, ExpectedFence: params.ExpectedFence, IdempotencyKey: params.IdempotencyKey, RequestedByUserID: params.RequestedByUserID, RequestedByUsername: params.RequestedByUsername, BuildPolicySnapshot: params.BuildPolicySnapshot, ControlPanelTarget: params.ControlPanelTarget}
}

func systemUpdatePortV2RequestDigest(params CreateSystemdPortReconfigurationJobParams) string {
	value := struct {
		Version                                  int
		Mode                                     contracts.SystemUpdatePortMode
		TargetID                                 string
		LocalPort, AdvertisedPort, ContainerPort int
		SnapshotID                               string
		EndpointRevision, DesiredRevision, Fence int64
	}{params.PortContractVersion, params.Mode, params.TargetID, params.NewLocalListenPort, params.NewAdvertisedPort, params.newContainerPort, params.ExpectedSnapshotID, params.ExpectedEndpointRevision, params.ExpectedDesiredRevision, params.ExpectedFence}
	body, _ := json.Marshal(value)
	return strings.TrimPrefix(contracts.ComputeSystemUpdatePortBytesSHA256(body), "sha256:")
}

func portSnapshotEndpoint(endpoint *ServiceEndpoint) contracts.SystemUpdatePortEndpoint {
	if endpoint == nil {
		return contracts.SystemUpdatePortEndpoint{}
	}
	return contracts.SystemUpdatePortEndpoint{Host: endpoint.Host, Port: endpoint.Port, SSLEnabled: endpoint.SSLEnabled, PublicURL: endpoint.PublicURL}
}
func portServiceEndpoint(endpoint contracts.SystemUpdatePortEndpoint) *ServiceEndpoint {
	return &ServiceEndpoint{Host: endpoint.Host, Port: endpoint.Port, SSLEnabled: endpoint.SSLEnabled, PublicURL: endpoint.PublicURL}
}
func portSourcePolicyJSON(policy UpdaterPolicy) (json.RawMessage, error) {
	body, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	delete(fields, "updated_at")
	return json.Marshal(fields)
}
func portSnapshotPolicy(snapshot SystemUpdatePortPolicySnapshot) (UpdaterPolicy, error) {
	var policy UpdaterPolicy
	if err := json.Unmarshal(snapshot.Snapshot.Policy, &policy); err != nil {
		return policy, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	bindings := map[string]contracts.SystemUpdatePortPolicyBinding{}
	for _, binding := range snapshot.Snapshot.Bindings {
		bindings[binding.TargetID] = binding
	}
	for i := range policy.Targets {
		binding, ok := bindings[policy.Targets[i].TargetID]
		if !ok || binding.BindingPolicyRevision != policy.Revision {
			return UpdaterPolicy{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if binding.DatabaseName != nil {
			policy.Targets[i].DatabaseName = *binding.DatabaseName
		}
		if binding.LocalListenPort != nil {
			policy.Targets[i].LocalListenPort = *binding.LocalListenPort
		}
	}
	return policy, nil
}

func restoreSystemUpdatePortPolicySnapshot(policy UpdaterPolicy, ownership SystemUpdateExecutionHost, services []RegisteredService, params SystemUpdatePortSnapshotParams) (SystemUpdatePortPolicySnapshot, error) {
	if params.BuildPolicySnapshot == nil || policy.TransportMode != SystemUpdateTransportPullV2 || policy.ExecutionHostID != ownership.ExecutionHostID ||
		policy.UpdaterID != ownership.AgentServiceID || ownership.OwnershipEpoch < 1 || ownership.PolicyRevision != policy.ProjectionRevision ||
		!PullUpdaterPolicyDatabaseBindingsReady(policy) {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	serviceMap := map[string]RegisteredService{}
	for _, service := range services {
		serviceMap[service.ServiceID] = service
	}
	materialized, err := params.BuildPolicySnapshot(cloneUpdaterPolicy(policy), services)
	if err != nil || len(materialized.ExecutorPolicyJSON) == 0 || len(materialized.ExecutorPolicyJSON) > 1<<20 || contracts.ComputeSystemUpdatePortBytesSHA256(materialized.ExecutorPolicyJSON) != policy.LocalExecutorPolicySHA256 {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	snapshot := contracts.SystemUpdatePortPolicySnapshot{UpdaterID: policy.UpdaterID, HostID: policy.ExecutionHostID, OwnershipEpoch: ownership.OwnershipEpoch, CredentialReferences: []string{}, RootPolicy: append(json.RawMessage(nil), materialized.ExecutorPolicyJSON...)}
	snapshot.Policy, err = portSourcePolicyJSON(policy)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	refs := map[string]bool{}
	if agent, ok := serviceMap[policy.UpdaterID]; ok && agent.TokenID != "" {
		refs[agent.TokenID] = true
	}
	for _, target := range policy.Targets {
		service, ok := serviceMap[target.ServiceID]
		if !ok || target.HostID != policy.ExecutionHostID || service.ServiceType != target.ServiceType || service.AppliedEndpoint == nil || service.DesiredEndpoint == nil ||
			service.AppliedEndpointRevision < 1 || service.AppliedEndpointRevision > service.EndpointRevision || !sameServiceEndpoint(service.AppliedEndpoint, service.DesiredEndpoint) {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		binding := contracts.SystemUpdatePortPolicyBinding{TargetID: target.TargetID, ServiceID: target.ServiceID, HostID: target.HostID, BindingPolicyRevision: policy.Revision}
		if target.DatabaseName != "" {
			database := target.DatabaseName
			binding.DatabaseName = &database
		}
		local := target.LocalListenPort
		var docker *contracts.SystemUpdatePortDockerSnapshot
		if target.DeploymentMode == "systemd" {
			var valid bool
			local, valid = PullUpdaterPolicyTargetLocalListenPort(target, service)
			if !valid {
				return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			if !updaterPolicyControlPanelTarget(target) {
				v := local
				binding.LocalListenPort = &v
			}
		} else {
			docker, err = systemUpdatePortDockerSnapshotFromAgent(serviceMap[policy.UpdaterID], target.ServiceID)
			if err != nil {
				return SystemUpdatePortPolicySnapshot{}, err
			}
			local = docker.PublishedPort
		}
		snapshot.Bindings = append(snapshot.Bindings, binding)
		snapshot.Targets = append(snapshot.Targets, contracts.SystemUpdatePortPolicyTargetState{TargetID: target.TargetID, ServiceID: target.ServiceID, HostID: target.HostID, ServiceType: contracts.SystemUpdateTargetType(target.ServiceType), DeploymentMode: contracts.SystemUpdateDeploymentMode(target.DeploymentMode), DesiredEndpoint: portSnapshotEndpoint(service.DesiredEndpoint), AppliedEndpoint: portSnapshotEndpoint(service.AppliedEndpoint), EndpointRevision: service.EndpointRevision, AppliedEndpointRevision: service.AppliedEndpointRevision, ConfigRevision: service.AppliedConfigRevision, ConfigSHA256: service.AppliedConfigSHA256, LocalListenPort: local, Docker: docker})
		if service.TokenID != "" {
			refs[service.TokenID] = true
		}
	}
	for ref := range refs {
		snapshot.CredentialReferences = append(snapshot.CredentialReferences, ref)
	}
	sort.Strings(snapshot.CredentialReferences)
	return systemUpdatePortSnapshotWithRef(snapshot, params.TargetID)
}

func systemUpdatePortSnapshotWithRef(snapshot contracts.SystemUpdatePortPolicySnapshot, targetID string) (SystemUpdatePortPolicySnapshot, error) {
	id, digest, err := contracts.ComputeSystemUpdatePortSnapshotIdentity(snapshot)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	var policy UpdaterPolicy
	if json.Unmarshal(snapshot.Policy, &policy) != nil {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	for _, target := range snapshot.Targets {
		if target.ServiceID == targetID {
			endpointDigest, err := contracts.ComputeSystemUpdatePortEndpointSHA256(target.AppliedEndpoint)
			if err != nil {
				return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			ref := contracts.SystemUpdatePortSnapshotRef{SnapshotID: id, SnapshotSHA256: digest, SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision, ExecutorPolicyRevision: policy.LocalExecutorPolicyRevision, ExecutorPolicySHA256: contracts.ComputeSystemUpdatePortBytesSHA256(snapshot.RootPolicy), EndpointRevision: target.EndpointRevision, AppliedEndpointRevision: target.AppliedEndpointRevision, ConfigRevision: target.ConfigRevision, ConfigSHA256: target.ConfigSHA256, AdvertisedPort: target.AppliedEndpoint.Port, AdvertisedEndpointSHA256: endpointDigest, LocalListenPort: target.LocalListenPort, Docker: target.Docker}
			return SystemUpdatePortPolicySnapshot{Ref: ref, Snapshot: snapshot}, nil
		}
	}
	return SystemUpdatePortPolicySnapshot{}, ErrNotFound
}

func systemUpdatePortDockerSnapshotFromAgent(agent RegisteredService, targetID string) (*contracts.SystemUpdatePortDockerSnapshot, error) {
	body, err := json.Marshal(agent.ReportedCapabilities["port_policy_baseline"])
	if err != nil {
		return nil, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	var baseline contracts.UpdaterPortPolicyBaseline
	if json.Unmarshal(body, &baseline) != nil || contracts.ValidateUpdaterPortPolicyBaseline(baseline) != nil {
		return nil, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	for _, target := range baseline.Targets {
		if target.ServiceID == targetID && target.Docker != nil {
			value := *target.Docker
			return &value, nil
		}
	}
	return nil, ErrSystemUpdatePortPolicySnapshotUnavailable
}

func systemUpdatePortTransitionSnapshot(before SystemUpdatePortPolicySnapshot, targetID string, localPort, advertisedPort, containerPort int, step int64) (SystemUpdatePortPolicySnapshot, error) {
	body, _ := json.Marshal(before.Snapshot)
	var snapshot contracts.SystemUpdatePortPolicySnapshot
	_ = json.Unmarshal(body, &snapshot)
	policy, err := portSnapshotPolicy(before)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	policy.Revision += step
	policy.ProjectionRevision += step
	policy.LocalExecutorPolicyRevision += step
	ref := before.Ref
	ref.SourcePolicyRevision += step
	ref.ProjectionRevision += step
	ref.ExecutorPolicyRevision += step
	ref.ConfigRevision += step
	ref.LocalListenPort = localPort
	ref.ExecutorPolicySHA256 = ""
	if advertisedPort != before.Ref.AdvertisedPort {
		ref.EndpointRevision += step
		ref.AppliedEndpointRevision = ref.EndpointRevision
	}
	ref.AdvertisedPort = advertisedPort
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if target.ServiceID != targetID {
			continue
		}
		target.ConfigRevision = ref.ConfigRevision
		target.EndpointRevision = ref.EndpointRevision
		target.AppliedEndpointRevision = ref.AppliedEndpointRevision
		target.LocalListenPort = localPort
		if advertisedPort != target.AppliedEndpoint.Port {
			endpoint, e := systemUpdateEndpointWithPort(portServiceEndpoint(target.AppliedEndpoint), advertisedPort)
			if e != nil {
				return SystemUpdatePortPolicySnapshot{}, e
			}
			target.AppliedEndpoint = portSnapshotEndpoint(endpoint)
			target.DesiredEndpoint = target.AppliedEndpoint
		}
		configPort := localPort
		if target.Docker != nil {
			target.Docker.PublishedPort = localPort
			target.Docker.ContainerPort = containerPort
			target.Docker.HealthPort = localPort
			target.Docker.ComposeRevision += step
			ref.Docker = target.Docker
			configPort = containerPort
		}
		if target.Docker != nil {
			ref.ConfigSHA256, err = SystemUpdateDockerPortConfigSHA256(string(target.ServiceType), localPort, containerPort, ref.ConfigRevision)
		} else {
			var config []byte
			config, err = contracts.SystemUpdatePortListenerConfig(target.ServiceType, target.DeploymentMode, configPort, ref.ConfigRevision)
			ref.ConfigSHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(config)
		}
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		target.ConfigSHA256 = ref.ConfigSHA256
		ref.AdvertisedEndpointSHA256, err = contracts.ComputeSystemUpdatePortEndpointSHA256(target.AppliedEndpoint)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
	}
	for i := range policy.Targets {
		if policy.Targets[i].ServiceID == targetID && policy.Targets[i].DeploymentMode == "systemd" {
			policy.Targets[i].LocalListenPort = localPort
		}
	}
	for i := range snapshot.Bindings {
		binding := &snapshot.Bindings[i]
		binding.BindingPolicyRevision = policy.Revision
		if binding.ServiceID == targetID && binding.LocalListenPort != nil {
			value := localPort
			binding.LocalListenPort = &value
		}
	}
	root, err := contracts.ApplySystemUpdatePortPolicyDelta(before.Snapshot.RootPolicy, targetID, before.Ref, ref)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	snapshot.RootPolicy = root
	policy.LocalExecutorPolicySHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(root)
	snapshot.Policy, err = portSourcePolicyJSON(policy)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	return systemUpdatePortSnapshotWithRef(snapshot, targetID)
}

func systemUpdatePortV2Job(params CreateSystemdPortReconfigurationJobParams, before SystemUpdatePortPolicySnapshot, target, agent RegisteredService, ownership SystemUpdateExecutionHost, now time.Time) (SystemUpdateJob, error) {
	if before.Ref.Docker != nil && (params.newContainerPort < 1024 || params.newContainerPort > 65535) || before.Ref.Docker == nil && params.newContainerPort != 0 {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	if params.ExpectedSnapshotID != before.Ref.SnapshotID || params.ExpectedEndpointRevision != before.Ref.EndpointRevision || params.ExpectedFence != ownership.OwnershipEpoch {
		return SystemUpdateJob{}, ErrSystemUpdatePortSnapshotStale
	}
	localChanged := params.NewLocalListenPort != before.Ref.LocalListenPort
	if before.Ref.Docker != nil {
		localChanged = localChanged || params.newContainerPort != before.Ref.Docker.ContainerPort
	}
	advertised := before.Ref.AdvertisedPort
	if params.Mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
		advertised = params.NewAdvertisedPort
	}
	adChanged := advertised != before.Ref.AdvertisedPort
	if !localChanged && adChanged {
		return SystemUpdateJob{}, ErrSystemUpdateAdvertisedOnlyUnsupported
	}
	targetSnapshot, rollback := before, before
	if localChanged {
		if before.Ref.SourcePolicyRevision > 9007199254740989 || before.Ref.ProjectionRevision > 9007199254740989 || before.Ref.ExecutorPolicyRevision > 9007199254740989 || before.Ref.ConfigRevision > 9007199254740989 {
			return SystemUpdateJob{}, ErrSystemUpdatePortSnapshotStale
		}
		var err error
		targetSnapshot, err = systemUpdatePortTransitionSnapshot(before, params.TargetID, params.NewLocalListenPort, advertised, params.newContainerPort, 1)
		if err != nil {
			return SystemUpdateJob{}, err
		}
		container := 0
		if before.Ref.Docker != nil {
			container = before.Ref.Docker.ContainerPort
		}
		rollback, err = systemUpdatePortTransitionSnapshot(before, params.TargetID, before.Ref.LocalListenPort, before.Ref.AdvertisedPort, container, 2)
		if err != nil {
			return SystemUpdateJob{}, err
		}
		if adChanged { // R restores the functional advertisement with a new desired/applied generation.
			for i := range rollback.Snapshot.Targets {
				state := &rollback.Snapshot.Targets[i]
				if state.ServiceID == params.TargetID {
					state.EndpointRevision = before.Ref.EndpointRevision + 2
					state.AppliedEndpointRevision = state.EndpointRevision
				}
			}
			ref := rollback.Ref
			ref.EndpointRevision = before.Ref.EndpointRevision + 2
			ref.AppliedEndpointRevision = ref.EndpointRevision
			ref.ExecutorPolicySHA256 = ""
			root, e := contracts.ApplySystemUpdatePortPolicyDelta(before.Snapshot.RootPolicy, params.TargetID, before.Ref, ref)
			if e != nil {
				return SystemUpdateJob{}, ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			rollback.Snapshot.RootPolicy = root
			p, e := portSnapshotPolicy(rollback)
			if e != nil {
				return SystemUpdateJob{}, e
			}
			p.LocalExecutorPolicySHA256 = contracts.ComputeSystemUpdatePortBytesSHA256(root)
			rollback.Snapshot.Policy, _ = portSourcePolicyJSON(p)
			rollback, e = systemUpdatePortSnapshotWithRef(rollback.Snapshot, params.TargetID)
			if e != nil {
				return SystemUpdateJob{}, e
			}
		}
	}
	if params.ExpectedDesiredRevision != targetSnapshot.Ref.ConfigRevision {
		return SystemUpdateJob{}, ErrSystemUpdatePortSnapshotStale
	}
	plan := &SystemUpdatePortReconfiguration{PortContractVersion: 2, Mode: params.Mode, NetworkNamespace: "host", Protocol: SystemUpdatePortProtocolTCP, Before: &before.Ref, Target: &targetSnapshot.Ref, Rollback: &rollback.Ref}
	deployment := "systemd"
	if before.Ref.Docker != nil {
		deployment = "docker"
		policy, _ := portSnapshotPolicy(before)
		baseline, ok := systemUpdateDockerPortBaselineFromAgent(agent, policy, target)
		if !ok {
			return SystemUpdateJob{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		plan.DockerBaseline = &contracts.SystemUpdatePortDockerBaseline{ExpectedContainerID: baseline.ContainerID, ExpectedImageID: baseline.ImageID, ExpectedRepositoryDigest: baseline.RepositoryDigest, ExpectedVersionEnvSHA256: baseline.VersionEnvSHA256, ApprovedComposeConfigSHA256: baseline.ApprovedComposeConfigSHA256, ApprovedComposeRevision: baseline.ApprovedComposeRevision}
	}
	version := target.ReportedVersion
	if version == "" {
		version = target.Version
	}
	if !systemUpdateJobVersionPattern.MatchString(version) {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	job := SystemUpdateJob{ID: newUUID(), TargetID: params.TargetID, TargetServiceType: target.ServiceType, Operation: SystemUpdateOperationPortReconfigure, PortReconfigure: plan, DeploymentMode: deployment, CurrentVersion: version, TargetVersion: version, Strategy: SystemUpdateStrategyMaintenance, Status: SystemUpdateStatusQueued, IdempotencyKey: params.IdempotencyKey, RequestedByUserID: params.RequestedByUserID, RequestedByUsername: params.RequestedByUsername, AgentServiceID: agent.ServiceID, ExecutionHostID: ownership.ExecutionHostID, TransportMode: ownership.TransportMode, OwnershipEpoch: ownership.OwnershipEpoch, PolicyRevision: ownership.PolicyRevision, CreatedAt: now, UpdatedAt: now}
	plan.PortPlanSHA256 = strings.Repeat("0", 64)
	digest, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	plan.PortPlanSHA256 = digest
	if contracts.ValidateSystemUpdatePortPlan(systemUpdatePortContractsPlan(plan)) != nil {
		return SystemUpdateJob{}, ErrInvalidSystemUpdate
	}
	cancelRevision := before.Ref.EndpointRevision
	if adChanged {
		cancelRevision += 2
	}
	job.portTransaction = &systemUpdatePortTransaction{Plan: cloneSystemUpdatePortReconfiguration(plan), Before: before, Target: targetSnapshot, Rollback: rollback, RequestSHA256: systemUpdatePortV2RequestDigest(params), CancelEndpointRevision: cancelRevision, Phase: "created"}
	return job, nil
}

func canonicalizeSystemUpdatePortV2Report(job SystemUpdateJob, report SystemUpdateReport) (SystemUpdateReport, error) {
	if report.PortReconfigure != nil {
		return SystemUpdateReport{}, ErrSystemUpdatePortResultMismatch
	}
	if report.DesiredRevision != job.PortReconfigure.Target.ConfigRevision {
		return SystemUpdateReport{}, ErrSystemUpdatePortResultMismatch
	}
	if report.PortResult == nil {
		if report.Status == SystemUpdateStatusSucceeded || report.Status == SystemUpdateStatusRolledBack {
			return SystemUpdateReport{}, ErrSystemUpdatePortResultMismatch
		}
		if report.Status == SystemUpdateStatusFailed && job.portTransaction != nil && job.portTransaction.Phase != "created" {
			return SystemUpdateReport{}, ErrSystemUpdatePortResultMismatch
		}
		return report, nil
	}
	if contracts.ValidateSystemUpdatePortResult(systemUpdatePortContractsPlan(job.PortReconfigure), *report.PortResult) != nil || report.PortResult.Observation.ObservedAt.Before(job.CreatedAt) {
		return SystemUpdateReport{}, ErrSystemUpdatePortResultMismatch
	}
	result := SystemUpdatePortReconfigurationResult(report.PortResult.Result)
	if report.Status == SystemUpdateStatusSucceeded && (result == SystemUpdatePortReconfigurationApplied || result == SystemUpdatePortReconfigurationUnchanged) || report.Status == SystemUpdateStatusRolledBack && result == SystemUpdatePortReconfigurationRolledBack || report.Status == SystemUpdateStatusFailed && result == SystemUpdatePortReconfigurationRollbackFailed {
		code := systemUpdatePortReportCode(result)
		if report.Code != "" && report.Code != code {
			return SystemUpdateReport{}, ErrInvalidSystemUpdate
		}
		report.Code = code
		return report, nil
	}
	return SystemUpdateReport{}, ErrSystemUpdateTransition
}

func sameSystemUpdatePortV2Report(job SystemUpdateJob, report SystemUpdateReport) bool {
	stored := job.PortResult
	if stored == nil {
		stored = job.LastRecoveryObservation
	}
	if stored == nil || report.PortResult == nil {
		return stored == report.PortResult
	}
	return contracts.EqualSystemUpdatePortResults(*stored, *report.PortResult)
}

func portPolicyMatchesSnapshot(policy UpdaterPolicy, snapshot SystemUpdatePortPolicySnapshot) bool {
	expected, err := portSnapshotPolicy(snapshot)
	if err != nil {
		return false
	}
	policy.UpdatedAt = time.Time{}
	expected.UpdatedAt = time.Time{}
	return reflect.DeepEqual(policy, expected)
}

func systemUpdatePortV2OwnershipMatches(job SystemUpdateJob, ownership SystemUpdateExecutionHost) bool {
	if !isSystemUpdatePortV2(job) || job.portTransaction == nil || job.PolicyRevision != job.PortReconfigure.Before.ProjectionRevision {
		return false
	}
	transaction := job.portTransaction
	expected := transaction.Before.Ref.ProjectionRevision
	if transaction.Phase == "consumed" || transaction.Phase == "rollback_latched" {
		expected = transaction.Target.Ref.ProjectionRevision
	}
	if transaction.AcceptedResult != nil && transaction.AcceptedResult.Result == contracts.SystemUpdatePortReconfigurationRolledBack {
		expected = transaction.Rollback.Ref.ProjectionRevision
	}
	if transaction.AcceptedResult != nil && transaction.AcceptedResult.Result == contracts.SystemUpdatePortReconfigurationApplied {
		expected = transaction.Target.Ref.ProjectionRevision
	}
	return ownership.PolicyRevision == expected
}

func allowedSystemUpdateJobTransition(job SystemUpdateJob, next string) bool {
	if systemUpdatePortV2Recoverable(job) && job.Status == SystemUpdateStatusReconciling && next == SystemUpdateStatusRollingBack {
		return true
	}
	return allowedSystemUpdateTransition(job.Status, next)
}

func systemUpdatePortV2AcceptedReplay(job SystemUpdateJob, report SystemUpdateReport) bool {
	return job.PortResult != nil && report.PortResult != nil && contracts.EqualSystemUpdatePortResults(*job.PortResult, *report.PortResult)
}

func portSnapshotsEqual(left, right SystemUpdatePortPolicySnapshot) bool {
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return bytes.Equal(l, r)
}

func sortedPortServices(services map[string]RegisteredService, policy UpdaterPolicy, synthetic *PullUpdaterControlPanelTarget) ([]RegisteredService, error) {
	ids := []string{policy.UpdaterID}
	for _, target := range policy.Targets {
		ids = append(ids, target.ServiceID)
	}
	sort.Strings(ids)
	result := make([]RegisteredService, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		service, ok := services[id]
		if !ok && id == "control-panel" && synthetic != nil {
			service = synthetic.registeredService()
			service.AppliedEndpointRevision = service.EndpointRevision
			ok = true
		}
		if !ok {
			return nil, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		result = append(result, service)
	}
	return result, nil
}
