package store

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var (
	serviceIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	executionHostIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,190}$`)
)

type ServiceToken struct {
	ID          string     `json:"id"`
	ServiceType string     `json:"service_type"`
	Scopes      []string   `json:"scopes"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`

	RawToken  string `json:"token,omitempty"`
	TokenHash string `json:"-"`
}

// ServiceEndpoint describes the Control Panel-visible API endpoint of a
// registered service. The legacy Host/Port/SSLEnabled/PublicURL fields remain
// the applied endpoint during the bridge period.
type ServiceEndpoint struct {
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	SSLEnabled bool   `json:"ssl_enabled"`
	PublicURL  string `json:"public_url,omitempty"`
}

type RegisteredService struct {
	ServiceID                     string           `json:"service_id"`
	ServiceType                   string           `json:"service_type"`
	ServiceName                   string           `json:"service_name"`
	Description                   string           `json:"description,omitempty"`
	TransportMode                 string           `json:"transport_mode,omitempty"`
	ExecutionHostID               string           `json:"execution_host_id,omitempty"`
	OwnershipEpoch                int64            `json:"ownership_epoch,omitempty"`
	Host                          string           `json:"host,omitempty"`
	Port                          int              `json:"port,omitempty"`
	SSLEnabled                    bool             `json:"ssl_enabled"`
	PublicURL                     string           `json:"public_url,omitempty"`
	DesiredEndpoint               *ServiceEndpoint `json:"desired_endpoint,omitempty"`
	AppliedEndpoint               *ServiceEndpoint `json:"applied_endpoint,omitempty"`
	ReportedEndpoint              *ServiceEndpoint `json:"reported_endpoint,omitempty"`
	EndpointRevision              int64            `json:"endpoint_revision,omitempty"`
	AppliedEndpointRevision       int64            `json:"applied_endpoint_revision,omitempty"`
	EndpointStatus                string           `json:"endpoint_status,omitempty"`
	AppliedConfigRevision         int64            `json:"applied_config_revision,omitempty"`
	AppliedConfigSHA256           string           `json:"applied_config_sha256,omitempty"`
	Version                       string           `json:"version"`
	ReportedVersion               string           `json:"reported_version,omitempty"`
	ReportedCommit                string           `json:"reported_commit,omitempty"`
	ReportedBuildDate             string           `json:"reported_build_date,omitempty"`
	Status                        string           `json:"status"`
	AssignmentRole                string           `json:"assignment_role,omitempty"`
	LastHeartbeatAt               *time.Time       `json:"last_heartbeat_at,omitempty"`
	LastReportedAt                *time.Time       `json:"last_reported_at,omitempty"`
	CurrentStreamID               string           `json:"current_stream_id,omitempty"`
	Capabilities                  map[string]any   `json:"capabilities"`
	ReportedCapabilities          map[string]any   `json:"reported_capabilities,omitempty"`
	Metrics                       map[string]any   `json:"metrics,omitempty"`
	TokenID                       string           `json:"-"`
	NodeTokenCiphertext           string           `json:"-"`
	NodeTokenNonce                string           `json:"-"`
	StagedNodePreviousTokenID     string           `json:"-"`
	StagedNodeTokenID             string           `json:"-"`
	StagedNodeTokenHash           string           `json:"-"`
	StagedNodeTokenScopes         []string         `json:"-"`
	StagedNodeTokenCiphertext     string           `json:"-"`
	StagedNodeTokenNonce          string           `json:"-"`
	StagedNodeActivationTokenHash string           `json:"-"`
	StagedNodeTokenAt             *time.Time       `json:"-"`
	ReportedHostname              string           `json:"reported_hostname,omitempty"`
	ReportedOS                    string           `json:"reported_os,omitempty"`
	ReportedArch                  string           `json:"reported_arch,omitempty"`
	ConfigureTokenHash            string           `json:"-"`
	ConfigureTokenExpiresAt       *time.Time       `json:"configure_token_expires_at,omitempty"`
	ConfigureTokenUsedAt          *time.Time       `json:"configure_token_used_at,omitempty"`
	NodeTokenRotatedAt            *time.Time       `json:"node_token_rotated_at,omitempty"`
	CreatedAt                     time.Time        `json:"created_at"`
	UpdatedAt                     time.Time        `json:"updated_at"`
}

type ServiceRegistration struct {
	ServiceID       string         `json:"service_id"`
	ServiceType     string         `json:"service_type"`
	ServiceName     string         `json:"service_name"`
	Description     string         `json:"description,omitempty"`
	TransportMode   string         `json:"transport_mode,omitempty"`
	ExecutionHostID string         `json:"execution_host_id,omitempty"`
	OwnershipEpoch  int64          `json:"ownership_epoch,omitempty"`
	Host            string         `json:"host,omitempty"`
	Port            int            `json:"port,omitempty"`
	SSLEnabled      bool           `json:"ssl_enabled"`
	PublicURL       string         `json:"public_url,omitempty"`
	Version         string         `json:"version"`
	Commit          string         `json:"commit,omitempty"`
	BuildDate       string         `json:"build_date,omitempty"`
	Capabilities    map[string]any `json:"capabilities"`
	Hostname        string         `json:"hostname,omitempty"`
	OS              string         `json:"os,omitempty"`
	Arch            string         `json:"arch,omitempty"`
}

type ServiceMetadataUpdate struct {
	ServiceName      string
	Description      string
	Host             string
	Port             int
	SSLEnabled       bool
	PublicURL        string
	Endpointless     bool
	PreserveEndpoint bool
}

type ServiceHeartbeat struct {
	ServiceID       string         `json:"service_id"`
	NodeID          string         `json:"nodeId,omitempty"`
	NodeIDSnake     string         `json:"node_id,omitempty"`
	Status          string         `json:"status"`
	CurrentStreamID string         `json:"current_stream_id,omitempty"`
	Version         string         `json:"version,omitempty"`
	Commit          string         `json:"commit,omitempty"`
	BuildDate       string         `json:"build_date,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	Hostname        string         `json:"hostname,omitempty"`
	OS              string         `json:"os,omitempty"`
	Arch            string         `json:"arch,omitempty"`
	API             *NodeAgentAPI  `json:"api,omitempty"`
	Metrics         map[string]any `json:"metrics,omitempty"`
}

type ServiceMetricSnapshot struct {
	Name        string    `json:"name"`
	ServiceID   string    `json:"service_id"`
	ServiceType string    `json:"service_type"`
	Status      string    `json:"status,omitempty"`
	Value       float64   `json:"value"`
	ObservedAt  time.Time `json:"updated_at"`
}

type ServiceRuntimeReport struct {
	ServiceID string
	Version   string
	Commit    string
	BuildDate string
	Hostname  string
	OS        string
	Arch      string
}

type NodeTokenSealer func(rawToken string) (ciphertext, nonce string, err error)

type StagedServiceNodeConfiguration struct {
	Token               ServiceToken
	Service             RegisteredService
	ActivationToken     string
	ActivationExpiresAt time.Time
}

func clearStagedNodeConfiguration(service *RegisteredService) {
	service.StagedNodePreviousTokenID = ""
	service.StagedNodeTokenID = ""
	service.StagedNodeTokenHash = ""
	service.StagedNodeTokenScopes = nil
	service.StagedNodeTokenCiphertext = ""
	service.StagedNodeTokenNonce = ""
	service.StagedNodeActivationTokenHash = ""
	service.StagedNodeTokenAt = nil
}

var errNodeTokenSealerRequired = errors.New("node token sealer is required")

type NodeAgentAPI struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	SSLEnabled bool   `json:"sslEnabled"`
}

const serviceSelectColumns = `SELECT service_id, service_type, service_name, COALESCE(description, ''),
COALESCE(host, ''), COALESCE(port, 0), COALESCE(ssl_enabled, 0), public_url,
COALESCE(transport_mode, ''), COALESCE(execution_host_id, ''), COALESCE(ownership_epoch, 0),
COALESCE(desired_host, host, ''), COALESCE(desired_port, port, 0), COALESCE(desired_ssl_enabled, ssl_enabled, 0), COALESCE(desired_public_url, public_url, ''),
COALESCE(reported_api_host, ''), COALESCE(reported_api_port, 0), COALESCE(reported_api_ssl_enabled, 0), COALESCE(reported_api_public_url, ''),
COALESCE(endpoint_revision, 1), COALESCE(applied_endpoint_revision, 0), COALESCE(endpoint_status, 'applied'),
COALESCE(applied_config_revision, 1), COALESCE(applied_config_sha256, ''),
version, COALESCE(reported_version, ''), COALESCE(reported_commit, ''), COALESCE(reported_build_date, ''),
status, last_heartbeat_at, last_reported_at, current_stream_id, capabilities, COALESCE(reported_capabilities, capabilities), metrics, token_id,
COALESCE(node_token_ciphertext, ''), COALESCE(node_token_nonce, ''), COALESCE(staged_node_previous_token_id, ''), COALESCE(staged_node_token_id, ''),
COALESCE(staged_node_token_hash, ''), COALESCE(staged_node_token_scopes, '[]'), COALESCE(staged_node_token_ciphertext, ''),
COALESCE(staged_node_token_nonce, ''), COALESCE(staged_node_activation_token_hash, ''), staged_node_token_at,
COALESCE(reported_hostname, ''), COALESCE(reported_os, ''), COALESCE(reported_arch, ''),
configure_token_expires_at, configure_token_used_at, node_token_rotated_at, created_at, updated_at`

const serviceSelectColumnsAliased = `SELECT s.service_id, s.service_type, s.service_name, COALESCE(s.description, ''),
COALESCE(s.host, ''), COALESCE(s.port, 0), COALESCE(s.ssl_enabled, 0), s.public_url,
COALESCE(s.transport_mode, ''), COALESCE(s.execution_host_id, ''), COALESCE(s.ownership_epoch, 0),
COALESCE(s.desired_host, s.host, ''), COALESCE(s.desired_port, s.port, 0), COALESCE(s.desired_ssl_enabled, s.ssl_enabled, 0), COALESCE(s.desired_public_url, s.public_url, ''),
COALESCE(s.reported_api_host, ''), COALESCE(s.reported_api_port, 0), COALESCE(s.reported_api_ssl_enabled, 0), COALESCE(s.reported_api_public_url, ''),
COALESCE(s.endpoint_revision, 1), COALESCE(s.applied_endpoint_revision, 0), COALESCE(s.endpoint_status, 'applied'),
COALESCE(s.applied_config_revision, 1), COALESCE(s.applied_config_sha256, ''),
s.version, COALESCE(s.reported_version, ''), COALESCE(s.reported_commit, ''), COALESCE(s.reported_build_date, ''),
s.status, s.last_heartbeat_at, s.last_reported_at, s.current_stream_id, s.capabilities, COALESCE(s.reported_capabilities, s.capabilities), s.metrics, s.token_id,
COALESCE(s.node_token_ciphertext, ''), COALESCE(s.node_token_nonce, ''), COALESCE(s.staged_node_previous_token_id, ''), COALESCE(s.staged_node_token_id, ''),
COALESCE(s.staged_node_token_hash, ''), COALESCE(s.staged_node_token_scopes, '[]'), COALESCE(s.staged_node_token_ciphertext, ''),
COALESCE(s.staged_node_token_nonce, ''), COALESCE(s.staged_node_activation_token_hash, ''), s.staged_node_token_at,
COALESCE(s.reported_hostname, ''), COALESCE(s.reported_os, ''), COALESCE(s.reported_arch, ''),
s.configure_token_expires_at, s.configure_token_used_at, s.node_token_rotated_at, s.created_at, s.updated_at`

type StreamServiceAssignment struct {
	StreamID       string    `json:"stream_id"`
	ServiceID      string    `json:"service_id"`
	ServiceType    string    `json:"service_type"`
	AssignmentRole string    `json:"assignment_role"`
	AssignedAt     time.Time `json:"assigned_at"`
}

type ServiceStreamEvent struct {
	ServiceID string         `json:"service_id"`
	StreamID  string         `json:"stream_id"`
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload"`
}

type ServiceRegistryStore interface {
	CreateServiceToken(ctx context.Context, serviceType string, scopes []string) (ServiceToken, error)
	ListServiceTokens(ctx context.Context) ([]ServiceToken, error)
	RevokeServiceToken(ctx context.Context, id string) error
	RotateServiceToken(ctx context.Context, id string) (ServiceToken, error)
	RotateServiceNodeToken(ctx context.Context, serviceID, expectedTokenID string, seal NodeTokenSealer) (ServiceToken, RegisteredService, error)
	AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error)
	PrecreateService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error)
	RegisterService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error)
	Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error)
	UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error)
	SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error)
	ValidateServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (bool, error)
	ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error)
	ConfigureServiceNode(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, report ServiceRuntimeReport, seal NodeTokenSealer) (ServiceToken, RegisteredService, error)
	StageServiceNodeConfiguration(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, seal NodeTokenSealer) (StagedServiceNodeConfiguration, error)
	ActivateServiceNodeConfiguration(ctx context.Context, serviceID, configurationID, rawActivationToken string, now time.Time, report ServiceRuntimeReport) (ServiceToken, RegisteredService, bool, error)
	SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error)
	ListServices(ctx context.Context) ([]RegisteredService, error)
	ListServiceMetricSnapshots(ctx context.Context, since time.Time, maxPointsPerSeries int) ([]ServiceMetricSnapshot, error)
	ListWorkers(ctx context.Context) ([]RegisteredService, error)
	GetService(ctx context.Context, id string) (RegisteredService, error)
	UpdateServiceMetadata(ctx context.Context, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error)
	DeleteService(ctx context.Context, serviceID string) error
	AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error)
	AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error)
	UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error)
	ServiceAssignmentGuardStore
	ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error)
	ListServiceAssignmentsForService(ctx context.Context, serviceID string) ([]StreamServiceAssignment, error)
	RequestServiceRestart(ctx context.Context, serviceID string) (RegisteredService, error)
	WriteStreamEvent(ctx context.Context, token ServiceToken, event ServiceStreamEvent) error
}

var ErrForbidden = errors.New("forbidden")
var ErrAlreadyExists = errors.New("already exists")
var ErrInvalidServiceRegistration = errors.New("invalid service registration")
var ErrInvalidServiceScope = errors.New("invalid service scope")
var ErrInvalidServiceStreamEvent = errors.New("invalid service stream event")
var ErrInvalidServiceAssignment = errors.New("service type cannot be assigned to a stream")
var ErrTwoPhaseConfigureRequired = errors.New("two-phase configure is required")
