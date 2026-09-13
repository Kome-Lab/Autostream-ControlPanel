package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"math"
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	pullUpdaterActivationHeartbeatMaxAge = 180 * time.Second
	updaterPolicySnapshotReadMaxAttempts = 3
)

var (
	ErrConflict                      = errors.New("conflict")
	errUpdaterPolicySnapshotChanged  = errors.New("updater policy snapshot changed")
	updaterPolicyIdentifierPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	updaterPolicyHostIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	updaterPolicyLinuxUserPattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	updaterPolicyDatabaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type UpdaterPolicy struct {
	UpdaterID                   string                `json:"updater_id"`
	Revision                    int64                 `json:"revision"`
	ProjectionRevision          int64                 `json:"projection_revision,omitempty"`
	LocalExecutorPolicyRevision int64                 `json:"local_executor_policy_revision,omitempty"`
	TransportMode               string                `json:"transport_mode"`
	ExecutionHostID             string                `json:"execution_host_id,omitempty"`
	LocalExecutorPolicySHA256   string                `json:"local_executor_policy_sha256,omitempty"`
	PollIntervalSeconds         int                   `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds    int                   `json:"heartbeat_interval_seconds"`
	Hosts                       []UpdaterPolicyHost   `json:"hosts,omitempty"`
	Targets                     []UpdaterPolicyTarget `json:"targets"`
	UpdatedAt                   time.Time             `json:"updated_at"`
}

// UpdaterPolicyHost is bootstrap-only connection metadata consumed by the
// independent Updater. It does not grant Control Panel a host executor path.
type UpdaterPolicyHost struct {
	HostID        string `json:"host_id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Arch          string `json:"arch"`
	HostPublicKey string `json:"host_public_key"`
}

type UpdaterPolicyTarget struct {
	TargetID       string `json:"target_id"`
	ServiceID      string `json:"service_id"`
	HostID         string `json:"host_id"`
	ServiceType    string `json:"service_type"`
	DeploymentMode string `json:"deployment_mode"`
	// DatabaseName is revision-bound in update_agent_target_databases.
	DatabaseName string `json:"-"`
	// LocalListenPort is revision-bound in update_agent_target_local_listeners.
	LocalListenPort int `json:"-"`
}

type UpdaterPolicyStore interface {
	GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error)
	ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error)
}

type UpdaterPolicyAdminStore interface {
	UpdaterPolicyStore
	SavePullUpdaterPolicy(ctx context.Context, executionHosts SystemUpdateExecutionHostStore, serviceID string, expectedRevision, expectedOwnershipEpoch int64, input UpdaterPolicy) (UpdaterPolicy, error)
	BindPullUpdaterConfigurePolicy(ctx context.Context, params BindPullUpdaterConfigurePolicyParams) (UpdaterPolicy, error)
	ActivatePullUpdaterOwnership(ctx context.Context, services ServiceRegistryStore, executionHosts SystemUpdateExecutionHostStore, params ActivatePullUpdaterOwnershipParams) (ActivatePullUpdaterOwnershipResult, error)
	DeactivatePullUpdaterOwnership(ctx context.Context, services ServiceRegistryStore, executionHosts SystemUpdateExecutionHostStore, params DeactivatePullUpdaterOwnershipParams) (DeactivatePullUpdaterOwnershipResult, error)
}

type BindPullUpdaterConfigurePolicyParams struct {
	ServiceID                           string
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	LocalExecutorPolicySHA256           string
}

type ActivatePullUpdaterOwnershipParams struct {
	ServiceID                           string
	ExecutionHostID                     string
	ExpectedExecutionHostOwnershipEpoch int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	ExpectedLocalExecutorPolicySHA256   string
	ControlPanelTarget                  *PullUpdaterControlPanelTarget
}

// PullUpdaterControlPanelTarget is the server-owned runtime view of the
// Control Panel process. The Control Panel is not a registered Node service,
// so activation receives this narrowly validated synthetic target instead of
// accepting a caller-provided services row.
type PullUpdaterControlPanelTarget struct {
	ServiceID             string
	ServiceType           string
	EndpointRevision      int64
	AppliedConfigRevision int64
	AppliedConfigSHA256   string
	AppliedEndpoint       ServiceEndpoint
}

func (target PullUpdaterControlPanelTarget) registeredService() RegisteredService {
	endpoint := target.AppliedEndpoint
	return RegisteredService{
		ServiceID:             target.ServiceID,
		ServiceType:           target.ServiceType,
		ServiceName:           "Control Panel",
		Host:                  endpoint.Host,
		Port:                  endpoint.Port,
		SSLEnabled:            endpoint.SSLEnabled,
		PublicURL:             endpoint.PublicURL,
		DesiredEndpoint:       copyServiceEndpoint(&endpoint),
		AppliedEndpoint:       copyServiceEndpoint(&endpoint),
		EndpointRevision:      target.EndpointRevision,
		EndpointStatus:        "applied",
		AppliedConfigRevision: target.AppliedConfigRevision,
		AppliedConfigSHA256:   target.AppliedConfigSHA256,
		Status:                "online",
	}
}

type ActivatePullUpdaterOwnershipResult struct {
	Service   RegisteredService
	Ownership SystemUpdateExecutionHost
	Policy    UpdaterPolicy
}

type DeactivatePullUpdaterOwnershipParams struct {
	ServiceID                           string
	ExecutionHostID                     string
	ExpectedExecutionHostOwnershipEpoch int64
	ExpectedSourcePolicyRevision        int64
	ExpectedProjectionRevision          int64
	ExpectedLocalExecutorPolicyRevision int64
	ExpectedLocalExecutorPolicySHA256   string
}

type DeactivatePullUpdaterOwnershipResult struct {
	Service   RegisteredService
	Ownership SystemUpdateExecutionHost
	Policy    UpdaterPolicy
}

func prepareUpdaterPolicySave(serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, []byte, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, nil, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	normalized.Revision = expectedRevision + 1
	normalized.ProjectionRevision = normalized.Revision
	if normalized.TransportMode == SystemUpdateTransportPullV2 {
		normalized.LocalExecutorPolicyRevision = normalized.Revision
	} else {
		normalized.LocalExecutorPolicyRevision = 0
	}
	normalized.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(normalized)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	return normalized, body, nil
}

func normalizeActivatePullUpdaterOwnershipParams(params ActivatePullUpdaterOwnershipParams) (ActivatePullUpdaterOwnershipParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(params.ExpectedLocalExecutorPolicySHA256))
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.ExpectedExecutionHostOwnershipEpoch < 0 ||
		params.ExpectedExecutionHostOwnershipEpoch == math.MaxInt64 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.ExpectedLocalExecutorPolicySHA256) {
		return ActivatePullUpdaterOwnershipParams{}, ErrInvalidSettings
	}
	controlPanelTarget, err := normalizePullUpdaterControlPanelTarget(params.ControlPanelTarget)
	if err != nil {
		return ActivatePullUpdaterOwnershipParams{}, err
	}
	params.ControlPanelTarget = controlPanelTarget
	return params, nil
}

func normalizePullUpdaterControlPanelTarget(
	input *PullUpdaterControlPanelTarget,
) (*PullUpdaterControlPanelTarget, error) {
	if input == nil {
		return nil, nil
	}
	target := *input
	target.ServiceID = strings.TrimSpace(target.ServiceID)
	target.ServiceType = strings.TrimSpace(target.ServiceType)
	target.AppliedConfigSHA256 = strings.ToLower(strings.TrimSpace(target.AppliedConfigSHA256))
	target.AppliedEndpoint.Host = strings.TrimSpace(target.AppliedEndpoint.Host)
	target.AppliedEndpoint.PublicURL = strings.TrimSpace(target.AppliedEndpoint.PublicURL)
	if target.ServiceID != "control-panel" ||
		target.ServiceType != "control_panel" ||
		target.EndpointRevision < 1 ||
		target.AppliedConfigRevision < 1 ||
		target.AppliedConfigSHA256 == "" ||
		!validSystemUpdateDigest(target.AppliedConfigSHA256) ||
		target.AppliedEndpoint.Host != "127.0.0.1" ||
		target.AppliedEndpoint.Port < 1024 ||
		target.AppliedEndpoint.Port > 65535 ||
		target.AppliedEndpoint.SSLEnabled {
		return nil, ErrInvalidSettings
	}
	canonicalPublicURL := buildServiceURL(
		target.AppliedEndpoint.Host,
		target.AppliedEndpoint.Port,
		false,
	)
	if target.AppliedEndpoint.PublicURL != "" &&
		target.AppliedEndpoint.PublicURL != canonicalPublicURL {
		return nil, ErrInvalidSettings
	}
	target.AppliedEndpoint.PublicURL = canonicalPublicURL
	return &target, nil
}

func updaterPolicyControlPanelTarget(target UpdaterPolicyTarget) bool {
	return target.TargetID == "control-panel" &&
		target.ServiceID == "control-panel" &&
		target.ServiceType == "control_panel" &&
		target.DeploymentMode == "systemd"
}

func updaterPolicyTargetAllowsExplicitLocalListener(target UpdaterPolicyTarget) bool {
	return target.DeploymentMode == "systemd" &&
		!updaterPolicyControlPanelTarget(target)
}

// PullUpdaterPolicyTargetLocalListenPort resolves the local host listener used
// by a systemd pull target. Node services require an explicit revision-bound
// listener. The exact synthetic Control Panel target cannot be overridden
// because its loopback listener is server-owned.
func PullUpdaterPolicyTargetLocalListenPort(
	target UpdaterPolicyTarget,
	service RegisteredService,
) (int, bool) {
	if target.DeploymentMode != "systemd" {
		return 0, false
	}
	if updaterPolicyControlPanelTarget(target) {
		if target.LocalListenPort != 0 {
			return 0, false
		}
		if service.AppliedEndpoint == nil ||
			service.AppliedEndpoint.Port < 1024 ||
			service.AppliedEndpoint.Port > 65535 {
			return 0, false
		}
		return service.AppliedEndpoint.Port, true
	} else {
		if target.LocalListenPort < 1024 || target.LocalListenPort > 65535 {
			return 0, false
		}
		return target.LocalListenPort, true
	}
}

func normalizeDeactivatePullUpdaterOwnershipParams(
	params DeactivatePullUpdaterOwnershipParams,
) (DeactivatePullUpdaterOwnershipParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.ExecutionHostID = strings.TrimSpace(params.ExecutionHostID)
	params.ExpectedLocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(params.ExpectedLocalExecutorPolicySHA256),
	)
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		!executionHostIDPattern.MatchString(params.ExecutionHostID) ||
		params.ExpectedExecutionHostOwnershipEpoch < 1 ||
		params.ExpectedExecutionHostOwnershipEpoch == math.MaxInt64 ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.ExpectedLocalExecutorPolicySHA256) {
		return DeactivatePullUpdaterOwnershipParams{}, ErrInvalidSettings
	}
	return params, nil
}

func normalizeBindPullUpdaterConfigurePolicyParams(
	params BindPullUpdaterConfigurePolicyParams,
) (BindPullUpdaterConfigurePolicyParams, error) {
	params.ServiceID = strings.TrimSpace(params.ServiceID)
	params.LocalExecutorPolicySHA256 = strings.ToLower(
		strings.TrimSpace(params.LocalExecutorPolicySHA256),
	)
	if !updaterPolicyIdentifierPattern.MatchString(params.ServiceID) ||
		params.ExpectedSourcePolicyRevision < 1 ||
		params.ExpectedProjectionRevision < 1 ||
		params.ExpectedLocalExecutorPolicyRevision < 1 ||
		!validSystemUpdateDigest(params.LocalExecutorPolicySHA256) {
		return BindPullUpdaterConfigurePolicyParams{}, ErrInvalidSettings
	}
	return params, nil
}

func normalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicyWithDatabaseBindings(serviceID, input, true)
}

func normalizeStoredUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicyWithDatabaseBindings(serviceID, input, false)
}

func normalizeUpdaterPolicyWithDatabaseBindings(
	serviceID string,
	input UpdaterPolicy,
	requireDatabaseBindings bool,
) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	input.UpdaterID = strings.TrimSpace(input.UpdaterID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) ||
		(input.UpdaterID != "" && input.UpdaterID != serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	input.UpdaterID = serviceID
	input.TransportMode = strings.ToLower(strings.TrimSpace(input.TransportMode))
	input.ExecutionHostID = strings.TrimSpace(input.ExecutionHostID)
	input.LocalExecutorPolicySHA256 = strings.ToLower(strings.TrimSpace(input.LocalExecutorPolicySHA256))

	if input.PollIntervalSeconds == 0 {
		input.PollIntervalSeconds = 15
	}
	if input.HeartbeatIntervalSeconds == 0 {
		input.HeartbeatIntervalSeconds = 30
	}
	if input.PollIntervalSeconds < 5 || input.PollIntervalSeconds > 3600 ||
		input.HeartbeatIntervalSeconds < 5 || input.HeartbeatIntervalSeconds > 60 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if input.TransportMode != SystemUpdateTransportPullV2 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	return normalizePullUpdaterPolicy(input, requireDatabaseBindings)
}

func normalizePullUpdaterPolicy(
	input UpdaterPolicy,
	requireDatabaseBindings bool,
) (UpdaterPolicy, error) {
	if !executionHostIDPattern.MatchString(input.ExecutionHostID) ||
		!validSystemUpdateDigest(input.LocalExecutorPolicySHA256) ||
		len(input.Hosts) > 128 ||
		len(input.Targets) == 0 ||
		len(input.Targets) > 1024 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	hosts := make(map[string]bool, len(input.Hosts))
	for index := range input.Hosts {
		host := &input.Hosts[index]
		host.HostID = strings.TrimSpace(host.HostID)
		host.Name = strings.TrimSpace(host.Name)
		host.Address = strings.Trim(strings.TrimSpace(host.Address), "[]")
		host.User = strings.TrimSpace(host.User)
		host.Arch = strings.TrimSpace(host.Arch)
		host.HostPublicKey = strings.TrimSpace(host.HostPublicKey)
		canonicalHostPublicKey, validHostPublicKey := normalizeUpdaterPolicyHostPublicKey(host.HostPublicKey)
		if !updaterPolicyHostIDPattern.MatchString(host.HostID) || hosts[host.HostID] ||
			host.Name == "" || len([]rune(host.Name)) > 128 || updaterPolicyContainsControl(host.Name) ||
			!validUpdaterPolicyHost(host.Address) || host.Port < 1 || host.Port > 65535 ||
			!updaterPolicyLinuxUserPattern.MatchString(host.User) || host.User == "root" ||
			(host.Arch != "amd64" && host.Arch != "arm64") ||
			!validHostPublicKey {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		host.HostPublicKey = canonicalHostPublicKey
		hosts[host.HostID] = true
	}

	targets := make(map[string]bool, len(input.Targets))
	serviceIDs := make(map[string]bool, len(input.Targets))
	for index := range input.Targets {
		target := &input.Targets[index]
		target.TargetID = strings.TrimSpace(target.TargetID)
		target.ServiceID = strings.TrimSpace(target.ServiceID)
		if target.ServiceID == "" {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if target.TargetID == "" {
			target.TargetID = target.ServiceID
		}
		target.HostID = strings.TrimSpace(target.HostID)
		if target.HostID == "" {
			target.HostID = input.ExecutionHostID
		}
		target.ServiceType = strings.TrimSpace(target.ServiceType)
		target.DeploymentMode = strings.TrimSpace(target.DeploymentMode)
		if !updaterPolicyIdentifierPattern.MatchString(target.TargetID) ||
			targets[target.TargetID] ||
			!updaterPolicyIdentifierPattern.MatchString(target.ServiceID) ||
			serviceIDs[target.ServiceID] ||
			target.HostID != input.ExecutionHostID ||
			!validUpdaterPolicyServiceType(target.ServiceType) ||
			(target.DeploymentMode != "systemd" && target.DeploymentMode != "docker") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		requiresDatabase := updaterPolicyTargetRequiresDatabase(*target)
		if target.ServiceType == "control_panel" &&
			(target.TargetID != "control-panel" ||
				target.ServiceID != "control-panel" ||
				target.DeploymentMode != "systemd") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if target.LocalListenPort != 0 &&
			(target.LocalListenPort < 1024 ||
				target.LocalListenPort > 65535 ||
				!updaterPolicyTargetAllowsExplicitLocalListener(*target)) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		if requireDatabaseBindings && target.DeploymentMode == "systemd" &&
			!updaterPolicyControlPanelTarget(*target) && target.LocalListenPort == 0 {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		switch {
		case requiresDatabase && requireDatabaseBindings &&
			!updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName):
			return UpdaterPolicy{}, ErrInvalidSettings
		case requiresDatabase && !requireDatabaseBindings &&
			target.DatabaseName != "" &&
			!updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName):
			return UpdaterPolicy{}, ErrInvalidSettings
		case !requiresDatabase && target.DatabaseName != "":
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		targets[target.TargetID] = true
		serviceIDs[target.ServiceID] = true
	}

	input.Revision = 0
	input.ProjectionRevision = 0
	input.LocalExecutorPolicyRevision = 0
	input.UpdatedAt = time.Time{}
	return cloneUpdaterPolicy(input), nil
}

// NormalizeUpdaterPolicy validates and canonicalizes the declarative policy
// without assigning a database revision or update timestamp.
func NormalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicy(serviceID, input)
}

// PullUpdaterPolicyDatabaseBindingsReady reports whether every database-owning
// pull target has an exact, revision-bound database binding and no other target
// has one. It is safe to use on policies loaded for display: missing bindings
// remain visible as empty values so an administrator can repair them.
func PullUpdaterPolicyDatabaseBindingsReady(policy UpdaterPolicy) bool {
	if policy.TransportMode != SystemUpdateTransportPullV2 || len(policy.Targets) == 0 {
		return false
	}
	for _, target := range policy.Targets {
		if updaterPolicyTargetRequiresDatabase(target) {
			if !updaterPolicyDatabaseNamePattern.MatchString(target.DatabaseName) {
				return false
			}
			continue
		}
		if target.DatabaseName != "" {
			return false
		}
	}
	return true
}

func updaterPolicyTargetRequiresDatabase(target UpdaterPolicyTarget) bool {
	if target.DeploymentMode != "systemd" {
		return false
	}
	return target.ServiceType == "control_panel" || target.ServiceType == "observability"
}

func decodeUpdaterPolicy(serviceID string, revision int64, body []byte, updatedAt time.Time) (UpdaterPolicy, error) {
	return decodeUpdaterPolicyRevisions(serviceID, revision, revision, 0, body, updatedAt)
}

func decodeUpdaterPolicyRevisions(
	serviceID string,
	revision int64,
	projectionRevision int64,
	localExecutorPolicyRevision int64,
	body []byte,
	updatedAt time.Time,
) (UpdaterPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var policy UpdaterPolicy
	if err := decoder.Decode(&policy); err != nil {
		return UpdaterPolicy{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return UpdaterPolicy{}, errors.New("updater policy contains trailing data")
	}
	normalized, err := normalizeStoredUpdaterPolicy(serviceID, policy)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if revision < 1 ||
		projectionRevision < 1 ||
		localExecutorPolicyRevision < 0 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	normalized.Revision = revision
	normalized.ProjectionRevision = projectionRevision
	normalized.LocalExecutorPolicyRevision = localExecutorPolicyRevision
	normalized.UpdatedAt = updatedAt.UTC()
	return normalized, nil
}

func cloneUpdaterPolicy(policy UpdaterPolicy) UpdaterPolicy {
	policy.Hosts = append([]UpdaterPolicyHost(nil), policy.Hosts...)
	policy.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
	return policy
}

func validUpdaterPolicyHost(value string) bool {
	if value == "" || len(value) > 253 || updaterPolicyContainsControl(value) || strings.ContainsAny(value, " /\\@?#[]") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func updaterPolicyContainsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func normalizeUpdaterPolicyHostPublicKey(value string) (string, bool) {
	if value == "" || len(value) > 16*1024 || updaterPolicyContainsControl(value) {
		return "", false
	}
	publicKey, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 || comment != "" || len(options) != 0 || len(rest) != 0 {
		return "", false
	}
	canonical := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(publicKey)), "\n")
	if value != canonical {
		return "", false
	}
	return canonical, true
}

func validUpdaterPolicyServiceType(value string) bool {
	switch value {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

var (
	_ UpdaterPolicyStore      = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyAdminStore = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyStore      = MariaDBUpdaterPolicyStore{}
	_ UpdaterPolicyAdminStore = MariaDBUpdaterPolicyStore{}
)
