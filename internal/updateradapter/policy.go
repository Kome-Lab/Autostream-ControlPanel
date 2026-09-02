// Package updateradapter owns the Control Panel side of the independent
// Updater contract. It projects server-owned policy into the exact wire format
// consumed by Autostream-Updater, but deliberately contains no host command,
// process, systemd, Docker, socket, or filesystem mutation runtime.
package updateradapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	HostAgentConfigureProtocolVersion        = 1
	LocalExecutorMutationPolicySchemaVersion = 2
	LocalExecutorMutationProtocolVersion     = 2
	LocalExecutorSocketPath                  = "/run/autostream-local-executor/executor.sock"
	localExecutorPolicyMaxBytes              = 1 << 20
	ModeSystemd                              = "systemd"
)

var (
	identifierPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	digestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	unitPattern         = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)
	userPattern         = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// ConfigurePolicyProjection is the exact root policy payload delivered during
// Host Agent configuration. Its field order and JSON tags are part of the
// cross-repository compatibility contract.
type ConfigurePolicyProjection struct {
	Policy               json.RawMessage `json:"policy"`
	SHA256               string          `json:"sha256"`
	SourcePolicyRevision int64           `json:"source_policy_revision"`
	ProjectionRevision   int64           `json:"projection_revision"`
	PolicyRevision       int64           `json:"policy_revision"`
}

type HostAgentConfigurePolicySource struct {
	PanelURL                    string
	ExecutionHostID             string
	AgentUID                    uint32
	AgentGID                    uint32
	SourcePolicyRevision        int64
	ProjectionRevision          int64
	LocalExecutorPolicyRevision int64
	Targets                     []HostAgentConfigurePolicyTarget
}

type HostAgentConfigurePolicyTarget struct {
	ServiceID             string
	ServiceType           string
	DeploymentMode        string
	DatabaseName          string
	EndpointRevision      int64
	AppliedConfigRevision int64
	AppliedConfigSHA256   string
	AppliedEndpointPort   int
	LocalListenPort       int
}

type LocalExecutorPolicy struct {
	SchemaVersion        int                          `json:"schema_version"`
	ProtocolVersion      int                          `json:"protocol_version"`
	HostID               string                       `json:"host_id"`
	AgentUID             uint32                       `json:"agent_uid"`
	AgentGID             uint32                       `json:"agent_gid"`
	SocketPath           string                       `json:"socket_path"`
	SourcePolicyRevision int64                        `json:"source_policy_revision,omitempty"`
	ProjectionRevision   int64                        `json:"projection_revision,omitempty"`
	PolicyRevision       int64                        `json:"policy_revision"`
	Mutation             *LocalExecutorMutationPolicy `json:"mutation,omitempty"`
	Targets              []LocalExecutorTarget        `json:"targets"`
}

type LocalExecutorMutationPolicy struct {
	PanelURL string `json:"panel_url"`
}

type LocalExecutorTarget struct {
	ServiceID        string                `json:"service_id"`
	ServiceType      string                `json:"service_type"`
	DeploymentMode   string                `json:"deployment_mode"`
	DatabaseName     string                `json:"database_name,omitempty"`
	EndpointRevision int64                 `json:"endpoint_revision,omitempty"`
	ConfigRevision   int64                 `json:"config_revision"`
	ConfigSHA256     string                `json:"config_sha256,omitempty"`
	LocalListen      LocalExecutorEndpoint `json:"local_listen_endpoint"`
	Systemd          *SystemdTarget        `json:"systemd,omitempty"`
}

type LocalExecutorEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type SystemdTarget struct {
	SystemctlPath string   `json:"systemctl_path"`
	RunuserPath   string   `json:"runuser_path"`
	SmokeUser     string   `json:"smoke_user"`
	Unit          string   `json:"unit"`
	ReleaseRoot   string   `json:"release_root"`
	CurrentLink   string   `json:"current_link"`
	BinaryPath    string   `json:"binary_path"`
	RequiredPaths []string `json:"required_paths,omitempty"`
}

type standardSystemdProfile struct {
	unit             string
	releaseRoot      string
	currentLink      string
	binaryPath       string
	requiredPaths    []string
	backupExecutable string
	bindVariable     string
}

func standardSystemdProfileFor(serviceType string) (standardSystemdProfile, bool) {
	switch serviceType {
	case "control_panel":
		return standardSystemdProfile{
			unit:             "autostream-control-panel.service",
			releaseRoot:      "/opt/autostream/control-panel/releases",
			currentLink:      "/opt/autostream/control-panel/current",
			binaryPath:       "bin/control-panel",
			requiredPaths:    []string{"share/autostream-control-panel"},
			backupExecutable: "/usr/local/sbin/autostream-backup-control-panel",
			bindVariable:     "AUTOSTREAM_BIND_ADDR",
		}, true
	case "encoder_recorder":
		return standardSystemdProfile{
			unit:         "autostream-encoder-recorder.service",
			releaseRoot:  "/opt/autostream/encoder-recorder/releases",
			currentLink:  "/opt/autostream/encoder-recorder/current",
			binaryPath:   "bin/autostream-encoder-recorder",
			bindVariable: "AUTOSTREAM_BIND_ADDR",
		}, true
	case "observability":
		return standardSystemdProfile{
			unit:             "autostream-observability.service",
			releaseRoot:      "/opt/autostream/observability/releases",
			currentLink:      "/opt/autostream/observability/current",
			binaryPath:       "bin/autostream-observability",
			backupExecutable: "/usr/local/sbin/autostream-backup-observability",
			bindVariable:     "OBSERVABILITY_BIND_ADDR",
		}, true
	case "discord_bot":
		return standardSystemdProfile{
			unit:         "autostream-discord-bot.service",
			releaseRoot:  "/opt/autostream/discord-bot/releases",
			currentLink:  "/opt/autostream/discord-bot/current",
			binaryPath:   "bin/autostream-discord-bot",
			bindVariable: "AUTOSTREAM_BIND_ADDR",
		}, true
	case "worker":
		return standardSystemdProfile{
			unit:         "autostream-worker.service",
			releaseRoot:  "/opt/autostream/worker/releases",
			currentLink:  "/opt/autostream/worker/current",
			binaryPath:   "bin/autostream-worker",
			bindVariable: "AUTOSTREAM_BIND_ADDR",
		}, true
	default:
		return standardSystemdProfile{}, false
	}
}

// BuildHostAgentConfigurePolicy expands only server-owned identities and
// revisions through the fixed independent-Updater profile table.
func BuildHostAgentConfigurePolicy(source HostAgentConfigurePolicySource) (ConfigurePolicyProjection, error) {
	source.PanelURL = strings.TrimSpace(source.PanelURL)
	source.ExecutionHostID = strings.TrimSpace(source.ExecutionHostID)
	if err := validatePanelURL(source.PanelURL); err != nil {
		return ConfigurePolicyProjection{}, errors.New("Host Agent configure panel URL is invalid")
	}
	if !identifierPattern.MatchString(source.ExecutionHostID) || source.AgentUID == 0 || source.AgentGID == 0 ||
		source.SourcePolicyRevision < 1 || source.ProjectionRevision < 1 ||
		source.LocalExecutorPolicyRevision < 1 || len(source.Targets) == 0 {
		return ConfigurePolicyProjection{}, errors.New("Host Agent configure policy source is incomplete")
	}

	targets := append([]HostAgentConfigurePolicyTarget(nil), source.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].ServiceID < targets[j].ServiceID })
	policy := LocalExecutorPolicy{
		SchemaVersion:        LocalExecutorMutationPolicySchemaVersion,
		ProtocolVersion:      LocalExecutorMutationProtocolVersion,
		HostID:               source.ExecutionHostID,
		AgentUID:             source.AgentUID,
		AgentGID:             source.AgentGID,
		SocketPath:           LocalExecutorSocketPath,
		SourcePolicyRevision: source.SourcePolicyRevision,
		ProjectionRevision:   source.ProjectionRevision,
		PolicyRevision:       source.LocalExecutorPolicyRevision,
		Mutation:             &LocalExecutorMutationPolicy{PanelURL: source.PanelURL},
		Targets:              make([]LocalExecutorTarget, 0, len(targets)),
	}
	for index, sourceTarget := range targets {
		target, err := buildHostAgentConfigureSystemdTarget(sourceTarget)
		if err != nil {
			return ConfigurePolicyProjection{}, fmt.Errorf("Host Agent configure targets[%d]: %w", index, err)
		}
		policy.Targets = append(policy.Targets, target)
	}
	return BuildConfigurePolicyProjection(policy)
}

func buildHostAgentConfigureSystemdTarget(source HostAgentConfigurePolicyTarget) (LocalExecutorTarget, error) {
	source.ServiceID = strings.TrimSpace(source.ServiceID)
	source.ServiceType = strings.TrimSpace(source.ServiceType)
	source.DeploymentMode = strings.TrimSpace(source.DeploymentMode)
	source.DatabaseName = strings.TrimSpace(source.DatabaseName)
	source.AppliedConfigSHA256 = strings.ToLower(strings.TrimSpace(source.AppliedConfigSHA256))
	localListenPort := source.LocalListenPort
	if localListenPort == 0 {
		localListenPort = source.AppliedEndpointPort
	}
	if !identifierPattern.MatchString(source.ServiceID) || !validServiceType(source.ServiceType) ||
		source.EndpointRevision < 1 || source.AppliedConfigRevision < 1 ||
		source.AppliedEndpointPort < 1 || source.AppliedEndpointPort > 65535 ||
		localListenPort < 1024 || localListenPort > 65535 {
		return LocalExecutorTarget{}, errors.New("applied target state is incomplete")
	}
	if source.DeploymentMode != ModeSystemd {
		return LocalExecutorTarget{}, errors.New("automatic Docker authority is unavailable")
	}
	profile, ok := standardSystemdProfileFor(source.ServiceType)
	if !ok {
		return LocalExecutorTarget{}, errors.New("systemd service type is unsupported")
	}
	if profile.backupExecutable == "" {
		if source.DatabaseName != "" {
			return LocalExecutorTarget{}, errors.New("server-owned database name is not allowed for this systemd service")
		}
	} else if !databaseNamePattern.MatchString(source.DatabaseName) {
		return LocalExecutorTarget{}, errors.New("server-owned database name is invalid or unavailable")
	}
	configSHA256, err := SystemdConfigurePortSidecarSHA256(source.ServiceType, localListenPort, source.AppliedConfigRevision)
	if err != nil {
		return LocalExecutorTarget{}, err
	}
	if source.AppliedConfigSHA256 != "" && source.AppliedConfigSHA256 != configSHA256 {
		return LocalExecutorTarget{}, errors.New("applied config digest does not match the canonical systemd sidecar")
	}
	return LocalExecutorTarget{
		ServiceID:        source.ServiceID,
		ServiceType:      source.ServiceType,
		DeploymentMode:   ModeSystemd,
		DatabaseName:     source.DatabaseName,
		EndpointRevision: source.EndpointRevision,
		ConfigRevision:   source.AppliedConfigRevision,
		ConfigSHA256:     configSHA256,
		LocalListen:      LocalExecutorEndpoint{Host: "127.0.0.1", Port: localListenPort},
		Systemd: &SystemdTarget{
			SystemctlPath: "/usr/bin/systemctl",
			RunuserPath:   "/usr/sbin/runuser",
			SmokeUser:     "autostream",
			Unit:          profile.unit,
			ReleaseRoot:   profile.releaseRoot,
			CurrentLink:   profile.currentLink,
			BinaryPath:    profile.binaryPath,
			RequiredPaths: append([]string(nil), profile.requiredPaths...),
		},
	}, nil
}

// SystemdConfigurePortSidecarSHA256 computes the independent Updater's exact
// canonical configure-only loopback sidecar digest.
func SystemdConfigurePortSidecarSHA256(serviceType string, port int, configRevision int64) (string, error) {
	if port < 1024 || port > 65535 || configRevision < 1 {
		return "", errors.New("systemd configure port sidecar state is incomplete")
	}
	profile, ok := standardSystemdProfileFor(serviceType)
	if !ok {
		return "", errors.New("systemd configure service type is unsupported")
	}
	body := []byte(fmt.Sprintf("%s=%s\nAUTOSTREAM_CONFIG_REVISION=%d\n", profile.bindVariable, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)), configRevision))
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func BuildConfigurePolicyProjection(policy LocalExecutorPolicy) (ConfigurePolicyProjection, error) {
	if err := policy.validate(); err != nil {
		return ConfigurePolicyProjection{}, err
	}
	if policy.SourcePolicyRevision < 1 || policy.ProjectionRevision < 1 || policy.PolicyRevision < 1 {
		return ConfigurePolicyProjection{}, errors.New("configure policy revisions are incomplete")
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return ConfigurePolicyProjection{}, errors.New("encode canonical configure policy")
	}
	digest := sha256.Sum256(payload)
	return ConfigurePolicyProjection{
		Policy:               append(json.RawMessage(nil), payload...),
		SHA256:               "sha256:" + hex.EncodeToString(digest[:]),
		SourcePolicyRevision: policy.SourcePolicyRevision,
		ProjectionRevision:   policy.ProjectionRevision,
		PolicyRevision:       policy.PolicyRevision,
	}, nil
}

func ValidateConfigurePolicyActivation(payload []byte, expectedSHA256 string, expectedSourcePolicyRevision, expectedProjectionRevision, expectedPolicyRevision int64) error {
	if len(payload) == 0 || len(payload) > localExecutorPolicyMaxBytes || expectedSourcePolicyRevision < 1 ||
		expectedProjectionRevision < 1 || expectedPolicyRevision < 1 ||
		!digestPattern.MatchString(strings.TrimSpace(expectedSHA256)) || expectedSHA256 != strings.TrimSpace(expectedSHA256) {
		return errors.New("configure policy activation binding is invalid")
	}
	var policy LocalExecutorPolicy
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return errors.New("decode configure policy activation payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("configure policy activation payload contains trailing data")
	}
	projection, err := BuildConfigurePolicyProjection(policy)
	if err != nil {
		return err
	}
	if !bytes.Equal(payload, projection.Policy) || projection.SHA256 != expectedSHA256 ||
		projection.SourcePolicyRevision != expectedSourcePolicyRevision || projection.ProjectionRevision != expectedProjectionRevision ||
		projection.PolicyRevision != expectedPolicyRevision {
		return errors.New("installed configure policy does not match the staged canonical projection")
	}
	return nil
}

func (policy LocalExecutorPolicy) validate() error {
	if policy.SchemaVersion != LocalExecutorMutationPolicySchemaVersion || policy.ProtocolVersion != LocalExecutorMutationProtocolVersion ||
		policy.Mutation == nil || validatePanelURL(strings.TrimSpace(policy.Mutation.PanelURL)) != nil ||
		policy.Mutation.PanelURL != strings.TrimSpace(policy.Mutation.PanelURL) {
		return errors.New("local executor mutation policy is invalid")
	}
	if policy.HostID != strings.TrimSpace(policy.HostID) || !identifierPattern.MatchString(policy.HostID) ||
		policy.AgentUID == 0 || policy.AgentGID == 0 || policy.SocketPath != LocalExecutorSocketPath ||
		policy.SourcePolicyRevision < 1 || policy.ProjectionRevision < 1 || policy.PolicyRevision < 1 || len(policy.Targets) == 0 {
		return errors.New("local executor policy identity or revision is invalid")
	}
	seenServices := make(map[string]struct{}, len(policy.Targets))
	seenProfiles := make(map[string]struct{}, len(policy.Targets))
	for index := range policy.Targets {
		if err := policy.Targets[index].validate(); err != nil {
			return fmt.Errorf("targets[%d]: %w", index, err)
		}
		if _, exists := seenServices[policy.Targets[index].ServiceID]; exists {
			return fmt.Errorf("duplicate local executor service_id %q", policy.Targets[index].ServiceID)
		}
		seenServices[policy.Targets[index].ServiceID] = struct{}{}
		key := policy.Targets[index].DeploymentMode + "\x00" + policy.Targets[index].ServiceType
		if _, exists := seenProfiles[key]; exists {
			return fmt.Errorf("duplicate local executor privileged target for %s %s", policy.Targets[index].DeploymentMode, policy.Targets[index].ServiceType)
		}
		seenProfiles[key] = struct{}{}
	}
	return nil
}

func (target LocalExecutorTarget) validate() error {
	if target.ServiceID != strings.TrimSpace(target.ServiceID) || !identifierPattern.MatchString(target.ServiceID) ||
		target.ServiceType != strings.TrimSpace(target.ServiceType) || !validServiceType(target.ServiceType) ||
		target.DeploymentMode != ModeSystemd || target.ConfigRevision < 1 || target.EndpointRevision < 0 ||
		(target.ConfigSHA256 != "" && !digestPattern.MatchString(target.ConfigSHA256)) ||
		!validCanonicalLoopback(target.LocalListen.Host) || target.LocalListen.Port < 1024 || target.LocalListen.Port > 65535 ||
		target.Systemd == nil {
		return errors.New("local executor target is invalid")
	}
	profile, ok := standardSystemdProfileFor(target.ServiceType)
	if !ok || target.Systemd.SystemctlPath != "/usr/bin/systemctl" || target.Systemd.RunuserPath != "/usr/sbin/runuser" ||
		target.Systemd.SmokeUser != "autostream" || target.Systemd.Unit != profile.unit || target.Systemd.ReleaseRoot != profile.releaseRoot ||
		target.Systemd.CurrentLink != profile.currentLink || target.Systemd.BinaryPath != profile.binaryPath ||
		len(target.Systemd.RequiredPaths) != len(profile.requiredPaths) {
		return errors.New("systemd target does not match the fixed privileged service profile")
	}
	for index := range profile.requiredPaths {
		if target.Systemd.RequiredPaths[index] != profile.requiredPaths[index] {
			return errors.New("systemd target does not match the fixed privileged service profile")
		}
	}
	if !userPattern.MatchString(target.Systemd.SmokeUser) || !unitPattern.MatchString(target.Systemd.Unit) ||
		!absoluteNonRootPath(target.Systemd.ReleaseRoot) || !absoluteNonRootPath(target.Systemd.CurrentLink) ||
		filepath.Clean(target.Systemd.ReleaseRoot) == filepath.Clean(target.Systemd.CurrentLink) ||
		!safeRelativePath(target.Systemd.BinaryPath) {
		return errors.New("systemd target profile is invalid")
	}
	for _, path := range target.Systemd.RequiredPaths {
		if !safeRelativePath(path) {
			return errors.New("systemd artifact path is unsafe")
		}
	}
	if profile.backupExecutable == "" {
		if target.DatabaseName != "" {
			return errors.New("database_name is not allowed for this systemd service")
		}
	} else if !databaseNamePattern.MatchString(target.DatabaseName) {
		return errors.New("database_name is required for this systemd service")
	}
	return nil
}

func validatePanelURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("panel_url must be an absolute canonical HTTP(S) URL")
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return errors.New("remote panel_url must use HTTPS")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func validCanonicalLoopback(value string) bool {
	if value != strings.TrimSpace(value) {
		return false
	}
	address, err := netip.ParseAddr(value)
	return err == nil && address.IsLoopback() && value == address.String()
}

func validServiceType(value string) bool {
	switch value {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

func absoluteNonRootPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) != string(filepath.Separator)
}

func safeRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := filepath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
