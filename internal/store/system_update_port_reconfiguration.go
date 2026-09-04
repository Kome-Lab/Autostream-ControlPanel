package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

const (
	SystemUpdateOperationSoftwareUpdate  = "software_update"
	SystemUpdateOperationPortReconfigure = "port_reconfigure"

	SystemUpdateMutationOperationPortReconfigure          = "port_reconfigure"
	SystemUpdateMutationOperationPortReconfigureReconcile = "port_reconfigure_reconcile"
)

type SystemUpdatePortProtocol string

const (
	SystemUpdatePortProtocolTCP SystemUpdatePortProtocol = "tcp"
	SystemUpdatePortProtocolUDP SystemUpdatePortProtocol = "udp"
)

type SystemUpdatePortReconfigurationResult string

const (
	SystemUpdatePortReconfigurationApplied        SystemUpdatePortReconfigurationResult = "applied"
	SystemUpdatePortReconfigurationRolledBack     SystemUpdatePortReconfigurationResult = "rolled_back"
	SystemUpdatePortReconfigurationUnchanged      SystemUpdatePortReconfigurationResult = "unchanged"
	SystemUpdatePortReconfigurationRollbackFailed SystemUpdatePortReconfigurationResult = "rollback_failed"
)

// SystemUpdatePortReconfiguration is the nested public shape for a systemd
// port-reconfiguration plan or result. Persistence remains flat so every
// revision and digest can participate in SQL fencing; HTTP must expose this
// nested shape and must omit it for software-update jobs.
type SystemUpdatePortReconfiguration struct {
	NetworkNamespace               string                                 `json:"network_namespace,omitempty"`
	Protocol                       SystemUpdatePortProtocol               `json:"protocol,omitempty"`
	OldPort                        int                                    `json:"old_port,omitempty"`
	NewPort                        int                                    `json:"new_port,omitempty"`
	ExpectedEndpointRevision       int64                                  `json:"expected_endpoint_revision,omitempty"`
	TargetEndpointRevision         int64                                  `json:"target_endpoint_revision,omitempty"`
	ExpectedConfigRevision         int64                                  `json:"expected_config_revision,omitempty"`
	TargetConfigRevision           int64                                  `json:"target_config_revision,omitempty"`
	ExpectedConfigSHA256           string                                 `json:"expected_config_sha256,omitempty"`
	TargetConfigSHA256             string                                 `json:"target_config_sha256,omitempty"`
	ExpectedSourcePolicyRevision   int64                                  `json:"expected_source_policy_revision,omitempty"`
	ExpectedUpdaterPolicyRevision  int64                                  `json:"expected_updater_policy_revision,omitempty"`
	ExpectedExecutorPolicyRevision int64                                  `json:"expected_executor_policy_revision,omitempty"`
	ExpectedExecutorPolicySHA256   string                                 `json:"expected_executor_policy_sha256,omitempty"`
	PortPlanSHA256                 string                                 `json:"port_plan_sha256,omitempty"`
	Docker                         *SystemUpdateDockerPortReconfiguration `json:"docker,omitempty"`
	Result                         SystemUpdatePortReconfigurationResult  `json:"result,omitempty"`
}

// SystemUpdateDockerPortReconfiguration contains the Docker-only rollback
// baseline. Paths, Compose files, service names, commands and URLs are
// intentionally absent: the root-owned Local Executor policy resolves those
// from the fixed canonical Node service profile.
type SystemUpdateDockerPortReconfiguration struct {
	PublishedHostIP             string `json:"published_host_ip"`
	OldPublishedPort            int    `json:"old_published_port"`
	NewPublishedPort            int    `json:"new_published_port"`
	OldContainerPort            int    `json:"old_container_port"`
	NewContainerPort            int    `json:"new_container_port"`
	OldHealthPort               int    `json:"old_health_port"`
	NewHealthPort               int    `json:"new_health_port"`
	ApprovedComposeConfigSHA256 string `json:"approved_compose_config_sha256"`
	ApprovedComposeRevision     int64  `json:"approved_compose_revision"`
	ExpectedVersionEnvSHA256    string `json:"expected_version_env_sha256"`
	ExpectedContainerID         string `json:"expected_container_id"`
	ExpectedImageID             string `json:"expected_image_id"`
	ExpectedRepositoryDigest    string `json:"expected_repository_digest"`
}

// CreateSystemUpdatePortReconfigurationParams contains only the two
// client-selected fields. The store/server derives every other immutable plan
// field from current service, endpoint, policy and local-executor state.
type CreateSystemUpdatePortReconfigurationParams struct {
	NewPort                  int
	ExpectedEndpointRevision int64
}

// CreateSystemdPortReconfigurationJobParams is the complete public store
// command. Target identity, policy revisions, current port/config state,
// execution-host ownership and all digests are server-derived inside one
// transaction.
type CreateSystemdPortReconfigurationJobParams struct {
	TargetID                 string
	NewPort                  int
	ExpectedEndpointRevision int64
	IdempotencyKey           string
	RequestedByUserID        string
	RequestedByUsername      string
	// ControlPanelTarget is server-owned runtime state used only to fence the
	// synthetic Control Panel listener, which intentionally has no services or
	// service_port_reservations row.
	ControlPanelTarget *PullUpdaterControlPanelTarget
}

// SystemUpdatePortReconfigurationStore is deliberately additive so Bridge
// callers can require the transactional port path without changing software
// update callers.
type SystemUpdatePortReconfigurationStore interface {
	CreateSystemdPortReconfigurationJob(
		ctx context.Context,
		services ServiceRegistryStore,
		policies UpdaterPolicyStore,
		params CreateSystemdPortReconfigurationJobParams,
	) (job SystemUpdateJob, created bool, err error)
}

var (
	ErrSystemUpdatePortStoreMismatch = errors.New("system update port coordinator stores do not share one transaction boundary")
	ErrSystemUpdatePortUnsupported   = errors.New("system update target does not support direct systemd port reconfiguration")
	ErrSystemUpdateEndpointStale     = errors.New("system update target endpoint revision or pending state is stale")
)

const (
	systemUpdatePortNetworkNamespace = "host"
	systemUpdatePortProtocol         = "tcp"
	systemUpdatePortCurrentRole      = "api"
	systemUpdatePortPendingRole      = "api_pending"
	systemUpdatePortIntentSchema     = 1
)

func normalizeCreateSystemdPortReconfigurationJobParams(params CreateSystemdPortReconfigurationJobParams) CreateSystemdPortReconfigurationJobParams {
	params.TargetID = strings.TrimSpace(params.TargetID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	params.RequestedByUserID = strings.TrimSpace(params.RequestedByUserID)
	params.RequestedByUsername = strings.TrimSpace(params.RequestedByUsername)
	return params
}

func validateCreateSystemdPortReconfigurationJobParams(params CreateSystemdPortReconfigurationJobParams) error {
	if !serviceIDPattern.MatchString(params.TargetID) ||
		params.NewPort < 1024 || params.NewPort > 65535 ||
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

func normalizeSystemUpdatePortReconfiguration(input *SystemUpdatePortReconfiguration) *SystemUpdatePortReconfiguration {
	if input == nil {
		return nil
	}
	value := *input
	value.NetworkNamespace = strings.ToLower(strings.TrimSpace(value.NetworkNamespace))
	value.Protocol = SystemUpdatePortProtocol(strings.ToLower(strings.TrimSpace(string(value.Protocol))))
	value.ExpectedConfigSHA256 = strings.ToLower(strings.TrimSpace(value.ExpectedConfigSHA256))
	value.TargetConfigSHA256 = strings.ToLower(strings.TrimSpace(value.TargetConfigSHA256))
	value.ExpectedExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(value.ExpectedExecutorPolicySHA256))
	value.PortPlanSHA256 = strings.ToLower(strings.TrimSpace(value.PortPlanSHA256))
	if value.Docker != nil {
		docker := *value.Docker
		docker.PublishedHostIP = strings.TrimSpace(docker.PublishedHostIP)
		docker.ApprovedComposeConfigSHA256 = strings.ToLower(strings.TrimSpace(docker.ApprovedComposeConfigSHA256))
		docker.ExpectedVersionEnvSHA256 = strings.ToLower(strings.TrimSpace(docker.ExpectedVersionEnvSHA256))
		docker.ExpectedContainerID = strings.ToLower(strings.TrimSpace(docker.ExpectedContainerID))
		docker.ExpectedImageID = strings.ToLower(strings.TrimSpace(docker.ExpectedImageID))
		docker.ExpectedRepositoryDigest = strings.ToLower(strings.TrimSpace(docker.ExpectedRepositoryDigest))
		value.Docker = &docker
	}
	value.Result = SystemUpdatePortReconfigurationResult(strings.ToLower(strings.TrimSpace(string(value.Result))))
	return &value
}

func cloneSystemUpdatePortReconfiguration(input *SystemUpdatePortReconfiguration) *SystemUpdatePortReconfiguration {
	if input == nil {
		return nil
	}
	value := *input
	if input.Docker != nil {
		docker := *input.Docker
		value.Docker = &docker
	}
	return &value
}

func validateSystemUpdatePortReconfigurationPlan(plan *SystemUpdatePortReconfiguration) error {
	return validateSystemUpdatePortReconfigurationPlanForDeployment(plan, "systemd")
}

func validateSystemUpdatePortReconfigurationPlanForDeployment(
	plan *SystemUpdatePortReconfiguration,
	deploymentMode string,
) error {
	if plan == nil ||
		plan.NetworkNamespace != systemUpdatePortNetworkNamespace ||
		plan.Protocol != SystemUpdatePortProtocolTCP ||
		plan.OldPort < 1 || plan.OldPort > 65535 ||
		plan.NewPort < 1 || plan.NewPort > 65535 ||
		plan.ExpectedEndpointRevision < 1 ||
		plan.TargetEndpointRevision != plan.ExpectedEndpointRevision+1 ||
		plan.ExpectedConfigRevision < 1 ||
		plan.TargetConfigRevision != plan.ExpectedConfigRevision+1 ||
		!validSystemUpdateDigest(plan.ExpectedConfigSHA256) ||
		!validSystemUpdateDigest(plan.TargetConfigSHA256) ||
		plan.ExpectedConfigSHA256 == "" ||
		plan.TargetConfigSHA256 == "" ||
		plan.ExpectedConfigSHA256 == plan.TargetConfigSHA256 ||
		plan.ExpectedSourcePolicyRevision < 1 ||
		plan.ExpectedUpdaterPolicyRevision < 1 ||
		plan.ExpectedExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(plan.ExpectedExecutorPolicySHA256) ||
		plan.ExpectedExecutorPolicySHA256 == "" ||
		len(plan.PortPlanSHA256) != 64 ||
		plan.PortPlanSHA256 != strings.ToLower(plan.PortPlanSHA256) ||
		plan.Result != "" {
		return ErrInvalidSystemUpdate
	}
	if _, err := hex.DecodeString(plan.PortPlanSHA256); err != nil {
		return ErrInvalidSystemUpdate
	}
	switch deploymentMode {
	case "systemd":
		if plan.Docker != nil ||
			plan.OldPort < 1024 ||
			plan.NewPort < 1024 ||
			plan.OldPort == plan.NewPort {
			return ErrInvalidSystemUpdate
		}
		return nil
	case "docker":
		if err := validateSystemUpdateDockerPortReconfiguration(plan.Docker, plan); err != nil {
			return err
		}
		if plan.OldPort == plan.NewPort &&
			plan.Docker.OldPublishedPort == plan.Docker.NewPublishedPort &&
			plan.Docker.OldContainerPort == plan.Docker.NewContainerPort {
			return ErrInvalidSystemUpdate
		}
		return nil
	default:
		return ErrInvalidSystemUpdate
	}
}

func validateSystemUpdateDockerPortReconfiguration(
	docker *SystemUpdateDockerPortReconfiguration,
	plan *SystemUpdatePortReconfiguration,
) error {
	if docker == nil ||
		docker.PublishedHostIP != "127.0.0.1" ||
		docker.OldPublishedPort < 1024 || docker.OldPublishedPort > 65535 ||
		docker.NewPublishedPort < 1024 || docker.NewPublishedPort > 65535 ||
		docker.OldContainerPort < 1024 || docker.OldContainerPort > 65535 ||
		docker.NewContainerPort < 1024 || docker.NewContainerPort > 65535 ||
		docker.OldHealthPort != docker.OldPublishedPort ||
		docker.NewHealthPort != docker.NewPublishedPort ||
		docker.ApprovedComposeRevision != plan.ExpectedExecutorPolicyRevision ||
		len(docker.ApprovedComposeConfigSHA256) != 64 ||
		docker.ApprovedComposeConfigSHA256 != strings.ToLower(docker.ApprovedComposeConfigSHA256) ||
		docker.ExpectedVersionEnvSHA256 == "" ||
		!validSystemUpdateDigest(docker.ExpectedVersionEnvSHA256) ||
		!validSystemUpdateDockerContainerID(docker.ExpectedContainerID) ||
		docker.ExpectedImageID == "" ||
		!validSystemUpdateDigest(docker.ExpectedImageID) ||
		docker.ExpectedRepositoryDigest == "" ||
		!validSystemUpdateDigest(docker.ExpectedRepositoryDigest) {
		return ErrInvalidSystemUpdate
	}
	if _, err := hex.DecodeString(docker.ApprovedComposeConfigSHA256); err != nil {
		return ErrInvalidSystemUpdate
	}
	return nil
}

func validSystemUpdateDockerContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateSystemUpdatePortReconfigurationResult(result *SystemUpdatePortReconfiguration) error {
	if result == nil {
		return nil
	}
	emptyPlan := *result
	emptyPlan.Result = ""
	emptyPlan.Docker = nil
	if emptyPlan != (SystemUpdatePortReconfiguration{}) {
		return ErrInvalidSystemUpdate
	}
	if result.Docker != nil {
		return ErrInvalidSystemUpdate
	}
	switch result.Result {
	case SystemUpdatePortReconfigurationApplied,
		SystemUpdatePortReconfigurationRolledBack,
		SystemUpdatePortReconfigurationUnchanged,
		SystemUpdatePortReconfigurationRollbackFailed:
		return nil
	default:
		return ErrInvalidSystemUpdate
	}
}

func sameSystemUpdatePortReconfigurationIntent(left, right *SystemUpdatePortReconfiguration) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftCopy := *left
	rightCopy := *right
	leftCopy.Result = ""
	rightCopy.Result = ""
	leftDocker := leftCopy.Docker
	rightDocker := rightCopy.Docker
	leftCopy.Docker = nil
	rightCopy.Docker = nil
	if leftCopy != rightCopy {
		return false
	}
	if leftDocker == nil || rightDocker == nil {
		return leftDocker == rightDocker
	}
	return *leftDocker == *rightDocker
}

func sameSystemUpdatePortReconfigurationResult(plan *SystemUpdatePortReconfiguration, report *SystemUpdatePortReconfiguration) bool {
	if plan == nil {
		return report == nil
	}
	if report == nil {
		return plan.Result == ""
	}
	return plan.Result == report.Result
}

func systemUpdatePortReportCode(result SystemUpdatePortReconfigurationResult) string {
	if result == "" {
		return ""
	}
	return "port_reconfigure." + string(result)
}

func systemUpdatePortResultFromPersistedJob(status, code string) SystemUpdatePortReconfigurationResult {
	code = strings.TrimSpace(code)
	const prefix = "port_reconfigure."
	if strings.HasPrefix(code, prefix) {
		result := SystemUpdatePortReconfigurationResult(strings.TrimPrefix(code, prefix))
		if validateSystemUpdatePortReconfigurationResult(&SystemUpdatePortReconfiguration{Result: result}) == nil {
			return result
		}
	}
	switch status {
	case SystemUpdateStatusSucceeded:
		return SystemUpdatePortReconfigurationApplied
	case SystemUpdateStatusRolledBack:
		return SystemUpdatePortReconfigurationRolledBack
	case SystemUpdateStatusFailed:
		return SystemUpdatePortReconfigurationRollbackFailed
	default:
		return ""
	}
}

func canonicalizeSystemUpdatePortReport(job SystemUpdateJob, report SystemUpdateReport) (SystemUpdateReport, error) {
	if job.Operation != SystemUpdateOperationPortReconfigure {
		if report.PortReconfigure != nil {
			return SystemUpdateReport{}, ErrInvalidSystemUpdate
		}
		return report, nil
	}
	terminal := isTerminalSystemUpdateStatus(report.Status)
	if !terminal {
		if report.PortReconfigure != nil {
			return SystemUpdateReport{}, ErrInvalidSystemUpdate
		}
		return report, nil
	}
	if report.PortReconfigure == nil ||
		validateSystemUpdatePortReconfigurationResult(report.PortReconfigure) != nil {
		return SystemUpdateReport{}, ErrInvalidSystemUpdate
	}
	result := report.PortReconfigure.Result
	validPair := (report.Status == SystemUpdateStatusSucceeded &&
		(result == SystemUpdatePortReconfigurationApplied || result == SystemUpdatePortReconfigurationUnchanged)) ||
		(report.Status == SystemUpdateStatusRolledBack && result == SystemUpdatePortReconfigurationRolledBack) ||
		(report.Status == SystemUpdateStatusFailed && result == SystemUpdatePortReconfigurationRollbackFailed)
	if !validPair {
		return SystemUpdateReport{}, ErrSystemUpdateTransition
	}
	canonicalCode := systemUpdatePortReportCode(result)
	if report.Code != "" && report.Code != canonicalCode {
		return SystemUpdateReport{}, ErrInvalidSystemUpdate
	}
	report.Code = canonicalCode
	return report, nil
}

func systemUpdatePortServiceType(serviceType string) (string, bool) {
	switch serviceType {
	case "worker", "encoder_recorder", "discord_bot", "observability":
		return serviceType, true
	default:
		return "", false
	}
}

func systemUpdatePortSidecarSHA256(serviceType string, port int, configRevision int64) (string, error) {
	_, ok := systemUpdatePortServiceType(serviceType)
	if !ok || port < 1024 || port > 65535 || configRevision < 1 {
		return "", ErrSystemUpdatePortUnsupported
	}
	body, err := contracts.MarshalNodeListenerConfig(contracts.NodeListenerConfig{
		SchemaVersion: 2, ServiceType: serviceType,
		BindAddress: net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)), ConfigRevision: configRevision,
	})
	if err != nil {
		return "", ErrSystemUpdatePortUnsupported
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func systemUpdateEndpointWithPort(current *ServiceEndpoint, port int) (*ServiceEndpoint, error) {
	if current == nil || strings.TrimSpace(current.Host) == "" ||
		port < 1 || port > 65535 {
		return nil, ErrSystemUpdatePortUnsupported
	}
	next := *current
	next.Port = port
	publicURL, err := url.Parse(strings.TrimSpace(current.PublicURL))
	if err != nil || publicURL.Scheme == "" || publicURL.Hostname() == "" || publicURL.User != nil {
		return nil, ErrSystemUpdatePortUnsupported
	}
	publicURL.Host = net.JoinHostPort(publicURL.Hostname(), fmt.Sprintf("%d", port))
	next.PublicURL = publicURL.String()
	return &next, nil
}

func sameServiceEndpoint(left, right *ServiceEndpoint) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// ComputeSystemUpdatePortIntentSHA256 hashes only server-owned immutable
// intent. It intentionally excludes job ID, lease generation, session ID and
// all timestamps. A Host Agent must derive a separate runtime plan hash after
// claim for mutation-grant and Local Executor binding.
func ComputeSystemUpdatePortIntentSHA256(job SystemUpdateJob) (string, error) {
	if job.Operation != SystemUpdateOperationPortReconfigure ||
		job.PortReconfigure == nil {
		return "", ErrInvalidSystemUpdate
	}
	plan := *job.PortReconfigure
	plan.PortPlanSHA256 = ""
	plan.Result = ""
	validationPlan := plan
	validationPlan.PortPlanSHA256 = strings.Repeat("0", 64)
	if validateSystemUpdatePortReconfigurationPlanForDeployment(
		&validationPlan,
		strings.ToLower(strings.TrimSpace(job.DeploymentMode)),
	) != nil {
		return "", ErrInvalidSystemUpdate
	}
	payload := struct {
		SchemaVersion       int                             `json:"schema_version"`
		TargetID            string                          `json:"target_id"`
		ServiceType         string                          `json:"service_type"`
		AgentServiceID      string                          `json:"agent_service_id"`
		ExecutionHostID     string                          `json:"execution_host_id"`
		TransportMode       string                          `json:"transport_mode"`
		OwnershipEpoch      int64                           `json:"ownership_epoch"`
		OwnershipRevision   int64                           `json:"ownership_revision"`
		PortReconfiguration SystemUpdatePortReconfiguration `json:"port_reconfiguration"`
	}{
		SchemaVersion:       systemUpdatePortIntentSchema,
		TargetID:            strings.TrimSpace(job.TargetID),
		ServiceType:         strings.TrimSpace(job.TargetServiceType),
		AgentServiceID:      strings.TrimSpace(job.AgentServiceID),
		ExecutionHostID:     strings.TrimSpace(job.ExecutionHostID),
		TransportMode:       normalizedSystemUpdateTransportMode(job.TransportMode),
		OwnershipEpoch:      job.OwnershipEpoch,
		OwnershipRevision:   job.PolicyRevision,
		PortReconfiguration: plan,
	}
	if !serviceIDPattern.MatchString(payload.TargetID) ||
		!serviceIDPattern.MatchString(payload.AgentServiceID) ||
		!validSystemUpdateExecutionHostID(payload.ExecutionHostID) ||
		payload.TransportMode != SystemUpdateTransportPullV2 ||
		payload.OwnershipEpoch < 1 || payload.OwnershipRevision < 1 {
		return "", ErrInvalidSystemUpdate
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func systemUpdatePortJobFromState(
	params CreateSystemdPortReconfigurationJobParams,
	target RegisteredService,
	agent RegisteredService,
	policy UpdaterPolicy,
	ownership SystemUpdateExecutionHost,
	now time.Time,
) (SystemUpdateJob, error) {
	if target.AppliedEndpoint == nil || target.AppliedConfigRevision < 1 ||
		!validSystemUpdateDigest(target.AppliedConfigSHA256) ||
		target.AppliedConfigSHA256 == "" ||
		target.EndpointRevision != params.ExpectedEndpointRevision ||
		target.EndpointRevision == math.MaxInt64 ||
		target.AppliedConfigRevision == math.MaxInt64 ||
		params.NewPort == target.AppliedEndpoint.Port {
		return SystemUpdateJob{}, ErrSystemUpdateEndpointStale
	}
	targetEndpoint, err := systemUpdateEndpointWithPort(target.AppliedEndpoint, params.NewPort)
	if err != nil {
		return SystemUpdateJob{}, err
	}
	targetConfigRevision := target.AppliedConfigRevision + 1
	targetConfigSHA256, err := systemUpdatePortSidecarSHA256(
		target.ServiceType, params.NewPort, targetConfigRevision,
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
		NewPort:                        params.NewPort,
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
	}
	job := SystemUpdateJob{
		ID: newUUID(), TargetID: target.ServiceID, TargetServiceType: target.ServiceType,
		Operation: SystemUpdateOperationPortReconfigure, PortReconfigure: portPlan,
		DeploymentMode: "systemd", CurrentVersion: version, TargetVersion: version,
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
	_ = targetEndpoint
	return job, nil
}

func systemUpdatePortDesiredEndpoint(job SystemUpdateJob, applied *ServiceEndpoint) (*ServiceEndpoint, error) {
	if job.PortReconfigure == nil {
		return nil, ErrInvalidSystemUpdate
	}
	return systemUpdateEndpointWithPort(applied, job.PortReconfigure.NewPort)
}

var _ SystemUpdatePortReconfigurationStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdatePortReconfigurationStore = (*MariaDBSystemUpdateStore)(nil)
