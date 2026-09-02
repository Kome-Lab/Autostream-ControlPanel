package updateradapter

import (
	"encoding/json"
	"time"
)

// These are compatibility wire projections. They model payloads retained by
// the Control Panel adapter and do not implement an Agent or Local Executor.
type HostSelfUpdateReleaseIdentity struct {
	Tag                     string    `json:"tag"`
	Commit                  string    `json:"commit"`
	PublishedAt             time.Time `json:"published_at"`
	ManifestAssetID         int64     `json:"manifest_asset_id"`
	ManifestAssetName       string    `json:"manifest_asset_name"`
	ManifestSHA256          string    `json:"manifest_sha256"`
	ManifestChecksumAssetID int64     `json:"manifest_checksum_asset_id"`
	ManifestChecksumSHA256  string    `json:"manifest_checksum_sha256"`
	ArchiveAssetID          int64     `json:"archive_asset_id"`
	ArchiveAssetName        string    `json:"archive_asset_name"`
	ArchiveSize             int64     `json:"archive_size"`
	ArchiveSHA256           string    `json:"archive_sha256"`
	ArchiveChecksumAssetID  int64     `json:"archive_checksum_asset_id"`
	ArchiveChecksumSHA256   string    `json:"archive_checksum_sha256"`
	Arch                    string    `json:"arch"`
	AgentProtocolVersion    int       `json:"agent_protocol_version"`
	ExecutorProtocolVersion int       `json:"executor_protocol_version"`
	MutationProtocolVersion int       `json:"mutation_protocol_version"`
	RecoveryProtocolVersion int       `json:"recovery_protocol_version"`
	MinimumPanelVersion     string    `json:"minimum_panel_version"`
}

type HostSelfUpdateRequest struct {
	Generation              string                        `json:"generation"`
	AgentVersion            string                        `json:"agent_version"`
	ExecutorVersion         string                        `json:"executor_version"`
	Commit                  string                        `json:"commit"`
	ArtifactSHA256          string                        `json:"artifact_sha256"`
	AgentProtocolVersion    int                           `json:"agent_protocol_version"`
	ExecutorProtocolVersion int                           `json:"executor_protocol_version"`
	MutationProtocolVersion int                           `json:"mutation_protocol_version"`
	RecoveryProtocolVersion int                           `json:"recovery_protocol_version"`
	Release                 HostSelfUpdateReleaseIdentity `json:"release"`
}

type UpdaterConfigureAPIAssertion struct {
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	SSLEnabled bool   `json:"ssl_enabled,omitempty"`
}

type UpdaterConfigureIdentity struct {
	PanelURL      string                       `json:"panel_url"`
	NodeID        string                       `json:"node_id"`
	RuntimeToken  string                       `json:"runtime_token"`
	ServiceName   string                       `json:"service_name"`
	ServiceType   string                       `json:"service_type"`
	TransportMode string                       `json:"transport_mode,omitempty"`
	API           UpdaterConfigureAPIAssertion `json:"api"`
}

type UpdaterStagedConfiguration struct {
	Config              UpdaterConfigureIdentity   `json:"config"`
	ConfigurationID     string                     `json:"configuration_id"`
	ActivationToken     string                     `json:"activation_token"`
	ActivationExpiresAt time.Time                  `json:"activation_expires_at"`
	ConfigureProtocol   int                        `json:"configure_protocol_version,omitempty"`
	LocalExecutorPolicy *ConfigurePolicyProjection `json:"local_executor_policy,omitempty"`
}

type UpdaterActivationResult struct {
	State                       string `json:"state"`
	ConfigurationID             string `json:"configuration_id"`
	ConfigureProtocol           int    `json:"configure_protocol_version,omitempty"`
	LocalExecutorPolicySHA256   string `json:"local_executor_policy_sha256,omitempty"`
	SourcePolicyRevision        int64  `json:"source_policy_revision,omitempty"`
	ProjectionRevision          int64  `json:"projection_revision,omitempty"`
	LocalExecutorPolicyRevision int64  `json:"local_executor_policy_revision,omitempty"`
}

// ManagedPolicy is only the response shape retained by the Control Panel's
// legacy-compatible policy endpoint.
type ManagedPolicy struct {
	UpdaterID                string              `json:"updater_id"`
	Revision                 int64               `json:"revision"`
	API                      json.RawMessage     `json:"api"`
	PollIntervalSeconds      int                 `json:"poll_interval_seconds,omitempty"`
	HeartbeatIntervalSeconds int                 `json:"heartbeat_interval_seconds,omitempty"`
	Hosts                    []ManagedPolicyHost `json:"hosts"`
	Targets                  []json.RawMessage   `json:"targets"`
	UpdatedAt                time.Time           `json:"updated_at"`
}

type ManagedPolicyHost struct {
	HostID                   string `json:"host_id"`
	Name                     string `json:"name"`
	Address                  string `json:"address"`
	Port                     int    `json:"port"`
	User                     string `json:"user"`
	Arch                     string `json:"arch"`
	HostPublicKey            string `json:"host_public_key"`
	HostPublicKeyFingerprint string `json:"host_public_key_fingerprint"`
}

type JobReport struct {
	ServiceID       string                        `json:"service_id"`
	LeaseToken      string                        `json:"lease_token"`
	Sequence        uint64                        `json:"sequence"`
	LeaseGeneration uint64                        `json:"lease_generation"`
	Status          string                        `json:"status"`
	Progress        int                           `json:"progress,omitempty"`
	Code            string                        `json:"code,omitempty"`
	Message         string                        `json:"message,omitempty"`
	ArtifactDigest  string                        `json:"artifact_digest,omitempty"`
	PreviousDigest  string                        `json:"previous_digest,omitempty"`
	PortReconfigure *PortReconfigurationJobReport `json:"port_reconfigure,omitempty"`
}

type PortReconfigurationJobReport struct {
	Result string `json:"result"`
}
