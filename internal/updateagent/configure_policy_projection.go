package updateagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const HostAgentConfigureProtocolVersion = 1

// ConfigurePolicyProjection is the exact root policy payload delivered during
// configure staging. The Control Panel never accepts a manually typed digest:
// activation compares the digest of the exact installed canonical bytes with
// the same server-owned source/projection revisions.
type ConfigurePolicyProjection struct {
	Policy               json.RawMessage `json:"policy"`
	SHA256               string          `json:"sha256"`
	SourcePolicyRevision int64           `json:"source_policy_revision"`
	ProjectionRevision   int64           `json:"projection_revision"`
	PolicyRevision       int64           `json:"policy_revision"`
}

// HostAgentConfigurePolicySource contains only server-owned policy state plus
// the peer credentials the root configure command read from the local account
// database. It intentionally has no path, command, image, or arbitrary policy
// fields: those are expanded from fixed Local Executor profiles below.
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
	EndpointRevision      int64
	AppliedConfigRevision int64
	AppliedConfigSHA256   string
	AppliedEndpointPort   int
}

// BuildHostAgentConfigurePolicy expands a declarative pull policy into the
// exact root Local Executor policy installed by Host Agent configure.
//
// Systemd paths and commands come only from the compiled allowlist. Docker
// targets and systemd services with operator-selected database names remain
// fail-closed until their additional server-owned runtime state is modeled.
func BuildHostAgentConfigurePolicy(
	source HostAgentConfigurePolicySource,
) (ConfigurePolicyProjection, error) {
	source.PanelURL = strings.TrimSpace(source.PanelURL)
	source.ExecutionHostID = strings.TrimSpace(source.ExecutionHostID)
	if err := validatePanelURL(source.PanelURL); err != nil {
		return ConfigurePolicyProjection{}, errors.New("Host Agent configure panel URL is invalid")
	}
	if !identifierPattern.MatchString(source.ExecutionHostID) ||
		source.AgentUID == 0 ||
		source.AgentGID == 0 ||
		source.SourcePolicyRevision < 1 ||
		source.ProjectionRevision < 1 ||
		source.LocalExecutorPolicyRevision < 1 ||
		len(source.Targets) == 0 {
		return ConfigurePolicyProjection{}, errors.New("Host Agent configure policy source is incomplete")
	}

	targets := append([]HostAgentConfigurePolicyTarget(nil), source.Targets...)
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ServiceID < targets[j].ServiceID
	})
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
			return ConfigurePolicyProjection{}, fmt.Errorf(
				"Host Agent configure targets[%d]: %w",
				index,
				err,
			)
		}
		policy.Targets = append(policy.Targets, target)
	}
	return BuildConfigurePolicyProjection(policy)
}

func buildHostAgentConfigureSystemdTarget(
	source HostAgentConfigurePolicyTarget,
) (LocalExecutorTarget, error) {
	source.ServiceID = strings.TrimSpace(source.ServiceID)
	source.ServiceType = strings.TrimSpace(source.ServiceType)
	source.DeploymentMode = strings.TrimSpace(source.DeploymentMode)
	source.AppliedConfigSHA256 = strings.ToLower(strings.TrimSpace(source.AppliedConfigSHA256))
	if !identifierPattern.MatchString(source.ServiceID) ||
		!validLocalExecutorServiceType(source.ServiceType) ||
		source.EndpointRevision < 1 ||
		source.AppliedConfigRevision < 1 ||
		source.AppliedEndpointPort < 1024 ||
		source.AppliedEndpointPort > 65535 {
		return LocalExecutorTarget{}, errors.New("applied target state is incomplete")
	}
	if source.DeploymentMode != ModeSystemd {
		return LocalExecutorTarget{}, errors.New("automatic Docker authority is unavailable")
	}
	profile, ok := standardSystemdProfileFor(source.ServiceType)
	if !ok {
		return LocalExecutorTarget{}, errors.New("systemd service type is unsupported")
	}
	if profile.backupExecutable != "" {
		return LocalExecutorTarget{}, errors.New("server-owned database name is unavailable")
	}
	adapter, err := systemdPortAdapterFor(source.ServiceType, profile.unit)
	if err != nil {
		return LocalExecutorTarget{}, err
	}
	configSHA256 := systemdPortSidecarSHA256(systemdPortSidecarBytes(
		adapter.BindVariable,
		"127.0.0.1",
		source.AppliedEndpointPort,
		source.AppliedConfigRevision,
	))
	if source.AppliedConfigSHA256 != "" &&
		source.AppliedConfigSHA256 != configSHA256 {
		return LocalExecutorTarget{}, errors.New("applied config digest does not match the canonical systemd sidecar")
	}
	return LocalExecutorTarget{
		ServiceID:        source.ServiceID,
		ServiceType:      source.ServiceType,
		DeploymentMode:   ModeSystemd,
		EndpointRevision: source.EndpointRevision,
		ConfigRevision:   source.AppliedConfigRevision,
		ConfigSHA256:     configSHA256,
		LocalListen: LocalExecutorEndpoint{
			Host: "127.0.0.1",
			Port: source.AppliedEndpointPort,
		},
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

func BuildConfigurePolicyProjection(
	policy LocalExecutorPolicy,
) (ConfigurePolicyProjection, error) {
	if err := policy.Validate(); err != nil {
		return ConfigurePolicyProjection{}, err
	}
	if policy.SourcePolicyRevision < 1 ||
		policy.ProjectionRevision < 1 ||
		policy.PolicyRevision < 1 {
		return ConfigurePolicyProjection{}, errors.New("configure policy revisions are incomplete")
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return ConfigurePolicyProjection{}, errors.New("encode canonical configure policy")
	}
	return ConfigurePolicyProjection{
		Policy:               append(json.RawMessage(nil), payload...),
		SHA256:               "sha256:" + sha256Hex(payload),
		SourcePolicyRevision: policy.SourcePolicyRevision,
		ProjectionRevision:   policy.ProjectionRevision,
		PolicyRevision:       policy.PolicyRevision,
	}, nil
}

func ValidateConfigurePolicyActivation(
	payload []byte,
	expectedSHA256 string,
	expectedSourcePolicyRevision int64,
	expectedProjectionRevision int64,
	expectedPolicyRevision int64,
) error {
	if len(payload) == 0 ||
		len(payload) > localExecutorPolicyMaxBytes ||
		expectedSourcePolicyRevision < 1 ||
		expectedProjectionRevision < 1 ||
		expectedPolicyRevision < 1 ||
		!digestPattern.MatchString(strings.TrimSpace(expectedSHA256)) ||
		expectedSHA256 != strings.TrimSpace(expectedSHA256) {
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
	if !bytes.Equal(payload, projection.Policy) ||
		projection.SHA256 != expectedSHA256 ||
		projection.SourcePolicyRevision != expectedSourcePolicyRevision ||
		projection.ProjectionRevision != expectedProjectionRevision ||
		projection.PolicyRevision != expectedPolicyRevision {
		return errors.New("installed configure policy does not match the staged canonical projection")
	}
	return nil
}

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
