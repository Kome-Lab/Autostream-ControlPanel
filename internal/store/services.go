package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
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

type RegisteredService struct {
	ServiceID               string         `json:"service_id"`
	ServiceType             string         `json:"service_type"`
	ServiceName             string         `json:"service_name"`
	Description             string         `json:"description,omitempty"`
	Host                    string         `json:"host,omitempty"`
	Port                    int            `json:"port,omitempty"`
	SSLEnabled              bool           `json:"ssl_enabled"`
	PublicURL               string         `json:"public_url"`
	Version                 string         `json:"version"`
	ReportedVersion         string         `json:"reported_version,omitempty"`
	ReportedCommit          string         `json:"reported_commit,omitempty"`
	ReportedBuildDate       string         `json:"reported_build_date,omitempty"`
	Status                  string         `json:"status"`
	AssignmentRole          string         `json:"assignment_role,omitempty"`
	LastHeartbeatAt         *time.Time     `json:"last_heartbeat_at,omitempty"`
	LastReportedAt          *time.Time     `json:"last_reported_at,omitempty"`
	CurrentStreamID         string         `json:"current_stream_id,omitempty"`
	Capabilities            map[string]any `json:"capabilities"`
	ReportedCapabilities    map[string]any `json:"reported_capabilities,omitempty"`
	Metrics                 map[string]any `json:"metrics,omitempty"`
	TokenID                 string         `json:"-"`
	NodeTokenCiphertext     string         `json:"-"`
	NodeTokenNonce          string         `json:"-"`
	ReportedHostname        string         `json:"reported_hostname,omitempty"`
	ReportedOS              string         `json:"reported_os,omitempty"`
	ReportedArch            string         `json:"reported_arch,omitempty"`
	ConfigureTokenHash      string         `json:"-"`
	ConfigureTokenExpiresAt *time.Time     `json:"configure_token_expires_at,omitempty"`
	ConfigureTokenUsedAt    *time.Time     `json:"configure_token_used_at,omitempty"`
	NodeTokenRotatedAt      *time.Time     `json:"node_token_rotated_at,omitempty"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
}

type ServiceRegistration struct {
	ServiceID    string         `json:"service_id"`
	ServiceType  string         `json:"service_type"`
	ServiceName  string         `json:"service_name"`
	Description  string         `json:"description,omitempty"`
	Host         string         `json:"host,omitempty"`
	Port         int            `json:"port,omitempty"`
	SSLEnabled   bool           `json:"ssl_enabled"`
	PublicURL    string         `json:"public_url"`
	Version      string         `json:"version"`
	Commit       string         `json:"commit,omitempty"`
	BuildDate    string         `json:"build_date,omitempty"`
	Capabilities map[string]any `json:"capabilities"`
	Hostname     string         `json:"hostname,omitempty"`
	OS           string         `json:"os,omitempty"`
	Arch         string         `json:"arch,omitempty"`
}

type ServiceMetadataUpdate struct {
	ServiceName string
	Description string
	Host        string
	Port        int
	SSLEnabled  bool
	PublicURL   string
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

type NodeAgentAPI struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	SSLEnabled bool   `json:"sslEnabled"`
}

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
	AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error)
	PrecreateService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error)
	RegisterService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error)
	Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error)
	UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error)
	SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error)
	ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error)
	SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error)
	ListServices(ctx context.Context) ([]RegisteredService, error)
	ListServiceMetricSnapshots(ctx context.Context, since time.Time) ([]ServiceMetricSnapshot, error)
	ListWorkers(ctx context.Context) ([]RegisteredService, error)
	GetService(ctx context.Context, id string) (RegisteredService, error)
	UpdateServiceMetadata(ctx context.Context, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error)
	DeleteService(ctx context.Context, serviceID string) error
	AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error)
	AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error)
	UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error)
	ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error)
	ListServiceAssignmentsForService(ctx context.Context, serviceID string) ([]StreamServiceAssignment, error)
	RequestServiceRestart(ctx context.Context, serviceID string) (RegisteredService, error)
	WriteStreamEvent(ctx context.Context, token ServiceToken, event ServiceStreamEvent) error
}

var ErrForbidden = errors.New("forbidden")
var ErrAlreadyExists = errors.New("already exists")
var ErrInvalidServiceRegistration = errors.New("invalid service registration")
var ErrInvalidServiceStreamEvent = errors.New("invalid service stream event")

func (s MariaDBAuthStore) CreateServiceToken(ctx context.Context, serviceType string, scopes []string) (ServiceToken, error) {
	if err := validateServiceType(serviceType); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceScopes(scopes); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	token := ServiceToken{ID: newUUID(), ServiceType: serviceType, Scopes: scopes, RawToken: "ast_svc_" + raw, CreatedAt: time.Now().UTC()}
	token.TokenHash = security.HashToken(token.RawToken)
	body, err := json.Marshal(scopes)
	if err != nil {
		return ServiceToken{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, string(body), token.CreatedAt)
	if err != nil {
		return ServiceToken{}, err
	}
	return token, nil
}

func (s MariaDBAuthStore) ListServiceTokens(ctx context.Context) ([]ServiceToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, service_type, scopes, revoked_at, created_at FROM service_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []ServiceToken
	for rows.Next() {
		var token ServiceToken
		var scopes string
		var revoked sql.NullTime
		if err := rows.Scan(&token.ID, &token.ServiceType, &scopes, &revoked, &token.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopes), &token.Scopes)
		if revoked.Valid {
			token.RevokedAt = &revoked.Time
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s MariaDBAuthStore) RevokeServiceToken(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s MariaDBAuthStore) RotateServiceToken(ctx context.Context, id string) (ServiceToken, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ServiceToken{}, err
	}
	defer tx.Rollback()

	var oldToken ServiceToken
	var scopesJSON string
	var revoked sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT id, service_type, scopes, revoked_at FROM service_tokens WHERE id = ? FOR UPDATE`, id).Scan(&oldToken.ID, &oldToken.ServiceType, &scopesJSON, &revoked)
	if err == sql.ErrNoRows {
		return ServiceToken{}, ErrNotFound
	}
	if err != nil {
		return ServiceToken{}, err
	}
	if revoked.Valid {
		return ServiceToken{}, ErrNotFound
	}
	if err := json.Unmarshal([]byte(scopesJSON), &oldToken.Scopes); err != nil {
		return ServiceToken{}, err
	}

	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	now := time.Now().UTC()
	token := ServiceToken{
		ID:          newUUID(),
		ServiceType: oldToken.ServiceType,
		Scopes:      append([]string(nil), oldToken.Scopes...),
		RawToken:    "ast_svc_" + raw,
		CreatedAt:   now,
	}
	token.TokenHash = security.HashToken(token.RawToken)
	body, err := json.Marshal(token.Scopes)
	if err != nil {
		return ServiceToken{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, string(body), token.CreatedAt); err != nil {
		return ServiceToken{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET token_id = ?, node_token_rotated_at = ?, updated_at = ? WHERE token_id = ?`, token.ID, now, now, oldToken.ID); err != nil {
		return ServiceToken{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = ? WHERE id = ?`, now, oldToken.ID); err != nil {
		return ServiceToken{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceToken{}, err
	}
	return token, nil
}

func (s MariaDBAuthStore) AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error) {
	if rawToken == "" {
		return ServiceToken{}, ErrUnauthorized
	}
	var token ServiceToken
	var scopes string
	var revoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, service_type, token_hash, scopes, revoked_at, created_at FROM service_tokens WHERE token_hash = ?`, security.HashToken(rawToken)).Scan(&token.ID, &token.ServiceType, &token.TokenHash, &scopes, &revoked, &token.CreatedAt)
	if err == sql.ErrNoRows {
		return ServiceToken{}, ErrUnauthorized
	}
	if err != nil {
		return ServiceToken{}, err
	}
	if revoked.Valid {
		return ServiceToken{}, ErrUnauthorized
	}
	_ = json.Unmarshal([]byte(scopes), &token.Scopes)
	if requiredScope != "" && !hasString(token.Scopes, requiredScope) {
		return ServiceToken{}, ErrForbidden
	}
	return token, nil
}

func (s MariaDBAuthStore) PrecreateService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	if registration.ServiceType != token.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	registration = normalizeServiceRegistration(registration)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(registration.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO services (service_id, service_type, service_name, description, host, port, ssl_enabled, public_url, version, reported_version, status, capabilities, reported_capabilities, metrics, token_id, node_token_rotated_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', 'pending', ?, ?, ?, ?, ?, ?, ?)`, registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.Description, registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL, registration.Version, string(capabilities), string(capabilities), "{}", token.ID, token.CreatedAt, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return RegisteredService{}, ErrAlreadyExists
		}
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrAlreadyExists
	}
	return s.getService(ctx, registration.ServiceID)
}

func (s MariaDBAuthStore) RegisterService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	if registration.ServiceType != token.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	registration = normalizeServiceRegistration(registration)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(registration.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	var existingTokenID, existingType string
	err = tx.QueryRowContext(ctx, `SELECT token_id, service_type FROM services WHERE service_id = ? FOR UPDATE`, registration.ServiceID).Scan(&existingTokenID, &existingType)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrForbidden
	}
	if err != nil {
		return RegisteredService{}, err
	}
	if existingType != registration.ServiceType || existingTokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO services (service_id, service_type, service_name, description, host, port, ssl_enabled, public_url, version, reported_version, reported_commit, reported_build_date, status, capabilities, reported_capabilities, reported_hostname, reported_os, reported_arch, last_reported_at, metrics, token_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'registered', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE service_type = VALUES(service_type), service_name = VALUES(service_name), description = VALUES(description), host = VALUES(host), port = VALUES(port), ssl_enabled = VALUES(ssl_enabled), public_url = VALUES(public_url), version = VALUES(version), reported_version = VALUES(reported_version), reported_commit = VALUES(reported_commit), reported_build_date = VALUES(reported_build_date), status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, capabilities = VALUES(capabilities), reported_capabilities = VALUES(reported_capabilities), reported_hostname = VALUES(reported_hostname), reported_os = VALUES(reported_os), reported_arch = VALUES(reported_arch), last_reported_at = VALUES(last_reported_at), token_id = VALUES(token_id), updated_at = VALUES(updated_at)`, registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.Description, registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL, registration.Version, registration.Version, registration.Commit, registration.BuildDate, string(capabilities), string(capabilities), registration.Hostname, registration.OS, registration.Arch, now, "{}", token.ID, now, now)
	if err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, registration.ServiceID)
}

func (s MariaDBAuthStore) Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error) {
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeID)
	}
	if heartbeat.ServiceID == "" {
		heartbeat.ServiceID = strings.TrimSpace(heartbeat.NodeIDSnake)
	}
	if heartbeat.CurrentStreamID != "" {
		assigned, err := s.isServiceAssigned(ctx, heartbeat.ServiceID, heartbeat.CurrentStreamID)
		if err != nil {
			return RegisteredService{}, err
		}
		if !assigned {
			return RegisteredService{}, ErrForbidden
		}
	}
	heartbeat.Version = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Version), 80)
	heartbeat.Commit = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Commit), 80)
	heartbeat.BuildDate = truncateServiceReportedValue(strings.TrimSpace(heartbeat.BuildDate), 80)
	heartbeat.Hostname = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Hostname), 255)
	heartbeat.OS = truncateServiceReportedValue(strings.TrimSpace(heartbeat.OS), 80)
	heartbeat.Arch = truncateServiceReportedValue(strings.TrimSpace(heartbeat.Arch), 80)
	now := time.Now().UTC()
	sanitizedMetrics := sanitizeServiceMetrics(heartbeat.Metrics)
	metrics, err := json.Marshal(sanitizedMetrics)
	if err != nil {
		return RegisteredService{}, err
	}
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(heartbeat.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	apiHost := ""
	apiPort := 0
	apiSSL := false
	if heartbeat.API != nil {
		apiHost = strings.TrimSpace(heartbeat.API.Host)
		apiPort = heartbeat.API.Port
		apiSSL = heartbeat.API.SSLEnabled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE services SET status = ?, last_heartbeat_at = ?, current_stream_id = CASE WHEN ? = '' THEN current_stream_id ELSE ? END, metrics = ?, version = CASE WHEN ? = '' THEN version ELSE ? END, reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END, reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END, reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END, capabilities = CASE WHEN ? = '{}' THEN capabilities ELSE ? END, reported_capabilities = CASE WHEN ? = '{}' THEN reported_capabilities ELSE ? END, reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END, reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END, reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END, host = CASE WHEN ? = '' THEN host ELSE ? END, port = CASE WHEN ? = 0 THEN port ELSE ? END, ssl_enabled = CASE WHEN ? = '' THEN ssl_enabled ELSE ? END, public_url = CASE WHEN ? = '' THEN public_url ELSE ? END, last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '{}' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END, updated_at = ? WHERE service_id = ? AND token_id = ?`, heartbeat.Status, now, heartbeat.CurrentStreamID, heartbeat.CurrentStreamID, string(metrics), heartbeat.Version, heartbeat.Version, heartbeat.Version, heartbeat.Version, heartbeat.Commit, heartbeat.Commit, heartbeat.BuildDate, heartbeat.BuildDate, string(capabilities), string(capabilities), string(capabilities), string(capabilities), heartbeat.Hostname, heartbeat.Hostname, heartbeat.OS, heartbeat.OS, heartbeat.Arch, heartbeat.Arch, apiHost, apiHost, apiPort, apiPort, apiHost, apiSSL, buildServiceURL(apiHost, apiPort, apiSSL), buildServiceURL(apiHost, apiPort, apiSSL), heartbeat.Version, heartbeat.Commit, heartbeat.BuildDate, string(capabilities), heartbeat.Hostname, heartbeat.OS, heartbeat.Arch, now, now, heartbeat.ServiceID, token.ID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrForbidden
	}
	if err := s.recordServiceMetricSnapshots(ctx, heartbeat.ServiceID, token.ServiceType, heartbeat.Status, sanitizedMetrics, now); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, heartbeat.ServiceID)
}

func (s MariaDBAuthStore) recordServiceMetricSnapshots(ctx context.Context, serviceID, serviceType, status string, metrics map[string]any, observedAt time.Time) error {
	for name, raw := range metrics {
		value, ok := serviceMetricSnapshotNumber(raw)
		if !ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO service_metric_snapshots (service_id, service_type, metric_name, status, value, observed_at) VALUES (?, ?, ?, ?, ?, ?)`, serviceID, serviceType, name, status, value, observedAt); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM service_metric_snapshots WHERE observed_at < ?`, observedAt.Add(-3*time.Hour))
	return err
}

func (s MariaDBAuthStore) ListServiceMetricSnapshots(ctx context.Context, since time.Time) ([]ServiceMetricSnapshot, error) {
	if since.IsZero() {
		since = time.Now().UTC().Add(-3 * time.Hour)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT metric_name, service_id, service_type, status, value, observed_at FROM service_metric_snapshots WHERE observed_at >= ? ORDER BY observed_at ASC`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceMetricSnapshot
	for rows.Next() {
		var snapshot ServiceMetricSnapshot
		if err := rows.Scan(&snapshot.Name, &snapshot.ServiceID, &snapshot.ServiceType, &snapshot.Status, &snapshot.Value, &snapshot.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func (s MariaDBAuthStore) UpdateServiceRuntimeReport(ctx context.Context, report ServiceRuntimeReport) (RegisteredService, error) {
	report = normalizeServiceRuntimeReport(report)
	if report.ServiceID == "" {
		return RegisteredService{}, ErrNotFound
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, version = CASE WHEN ? = '' THEN version ELSE ? END, reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END, reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END, reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END, reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END, reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END, reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END, last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END, updated_at = ? WHERE service_id = ?`,
		report.Version, report.Version, report.Version, report.Version, report.Commit, report.Commit, report.BuildDate, report.BuildDate, report.Hostname, report.Hostname, report.OS, report.OS, report.Arch, report.Arch, report.Version, report.Commit, report.BuildDate, report.Hostname, report.OS, report.Arch, now, now, report.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, report.ServiceID)
}

func (s MariaDBAuthStore) SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET configure_token_hash = ?, configure_token_expires_at = ?, configure_token_used_at = NULL, updated_at = ? WHERE service_id = ?`, tokenHash, expiresAt, now, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	var tokenHash string
	var expiresAt sql.NullTime
	var usedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT configure_token_hash, configure_token_expires_at, configure_token_used_at FROM services WHERE service_id = ? FOR UPDATE`, serviceID).Scan(&tokenHash, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrNotFound
	}
	if err != nil {
		return RegisteredService{}, err
	}
	if tokenHash == "" || !expiresAt.Valid || usedAt.Valid || !now.Before(expiresAt.Time) || !security.VerifyTokenHash(rawToken, tokenHash) {
		return RegisteredService{}, ErrUnauthorized
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET configure_token_used_at = ?, updated_at = ? WHERE service_id = ?`, now, now, serviceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET node_token_ciphertext = ?, node_token_nonce = ?, updated_at = ? WHERE service_id = ?`, ciphertext, nonce, now, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) ListServices(ctx context.Context) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, COALESCE(s.description, ''), COALESCE(s.host, ''), COALESCE(s.port, 0), COALESCE(s.ssl_enabled, 0), s.public_url, s.version, COALESCE(s.reported_version, ''), COALESCE(s.reported_commit, ''), COALESCE(s.reported_build_date, ''), s.status, s.last_heartbeat_at, s.last_reported_at, s.current_stream_id, s.capabilities, COALESCE(s.reported_capabilities, s.capabilities), s.metrics, s.token_id, COALESCE(s.node_token_ciphertext, ''), COALESCE(s.node_token_nonce, ''), COALESCE(s.reported_hostname, ''), COALESCE(s.reported_os, ''), COALESCE(s.reported_arch, ''), s.configure_token_expires_at, s.configure_token_used_at, s.node_token_rotated_at, s.created_at, s.updated_at, COALESCE(a.assignment_role, '')
FROM services s
LEFT JOIN stream_service_assignments a ON a.service_id = s.service_id AND a.stream_id = s.current_stream_id
ORDER BY s.service_type, s.service_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RegisteredService
	for rows.Next() {
		service, err := scanServiceWithExtraRole(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s MariaDBAuthStore) ListWorkers(ctx context.Context) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, COALESCE(s.description, ''), COALESCE(s.host, ''), COALESCE(s.port, 0), COALESCE(s.ssl_enabled, 0), s.public_url, s.version, COALESCE(s.reported_version, ''), COALESCE(s.reported_commit, ''), COALESCE(s.reported_build_date, ''), s.status, s.last_heartbeat_at, s.last_reported_at, s.current_stream_id, s.capabilities, COALESCE(s.reported_capabilities, s.capabilities), s.metrics, s.token_id, COALESCE(s.node_token_ciphertext, ''), COALESCE(s.node_token_nonce, ''), COALESCE(s.reported_hostname, ''), COALESCE(s.reported_os, ''), COALESCE(s.reported_arch, ''), s.configure_token_expires_at, s.configure_token_used_at, s.node_token_rotated_at, s.created_at, s.updated_at, COALESCE(a.assignment_role, '')
FROM services s
LEFT JOIN stream_service_assignments a ON a.service_id = s.service_id AND a.stream_id = s.current_stream_id
WHERE s.service_type = 'worker'
ORDER BY s.service_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RegisteredService
	for rows.Next() {
		service, err := scanServiceWithExtraRole(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s MariaDBAuthStore) GetService(ctx context.Context, id string) (RegisteredService, error) {
	return s.getService(ctx, id)
}

func (s MariaDBAuthStore) UpdateServiceMetadata(ctx context.Context, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error) {
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(serviceID) == "" {
		return RegisteredService{}, ErrNotFound
	}
	if err := validateServiceMetadataUpdate(update); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET service_name = ?, description = ?, host = ?, port = ?, ssl_enabled = ?, public_url = ?, updated_at = ? WHERE service_id = ?`, update.ServiceName, update.Description, update.Host, update.Port, update.SSLEnabled, update.PublicURL, now, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) DeleteService(ctx context.Context, serviceID string) error {
	service, err := s.getService(ctx, serviceID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE service_id = ?`, serviceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_stream_events WHERE service_id = ?`, serviceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM services WHERE service_id = ?`, serviceID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, now, service.TokenID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s MariaDBAuthStore) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error) {
	return s.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, "primary")
}

func (s MariaDBAuthStore) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error) {
	assignmentRole = normalizeAssignmentRole(assignmentRole)
	service, err := s.getService(ctx, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT service_id FROM stream_service_assignments WHERE service_id = ? OR (stream_id = ? AND service_type = ? AND assignment_role = 'primary' AND ? = 'primary')`, serviceID, streamID, service.ServiceType, assignmentRole)
	if err != nil {
		return RegisteredService{}, err
	}
	var replacedServiceIDs []string
	for rows.Next() {
		var replacedServiceID string
		if err := rows.Scan(&replacedServiceID); err != nil {
			rows.Close()
			return RegisteredService{}, err
		}
		if replacedServiceID != serviceID {
			replacedServiceIDs = append(replacedServiceIDs, replacedServiceID)
		}
	}
	if err := rows.Close(); err != nil {
		return RegisteredService{}, err
	}
	if err := rows.Err(); err != nil {
		return RegisteredService{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE service_id = ? OR (stream_id = ? AND service_type = ? AND assignment_role = 'primary' AND ? = 'primary')`, serviceID, streamID, service.ServiceType, assignmentRole); err != nil {
		return RegisteredService{}, err
	}
	for _, replacedServiceID := range replacedServiceIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ?`, now, replacedServiceID); err != nil {
			return RegisteredService{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_service_assignments (id, stream_id, service_id, service_type, assignment_role, assigned_by_user_id, assigned_at)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?)
ON DUPLICATE KEY UPDATE assignment_role = VALUES(assignment_role), assigned_by_user_id = VALUES(assigned_by_user_id), assigned_at = VALUES(assigned_at)`, newUUID(), streamID, serviceID, service.ServiceType, assignmentRole, actorUserID, now); err != nil {
		return RegisteredService{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = ?, status = 'assigned', updated_at = ? WHERE service_id = ?`, streamID, now, serviceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	service, err = s.getService(ctx, serviceID)
	service.AssignmentRole = assignmentRole
	return service, err
}

func (s MariaDBAuthStore) UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error) {
	if _, err := s.getService(ctx, serviceID); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE service_id = ?`, serviceID); err != nil {
		return RegisteredService{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ?`, now, serviceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, COALESCE(s.description, ''), COALESCE(s.host, ''), COALESCE(s.port, 0), COALESCE(s.ssl_enabled, 0), s.public_url, s.version, COALESCE(s.reported_version, ''), COALESCE(s.reported_commit, ''), COALESCE(s.reported_build_date, ''), s.status, s.last_heartbeat_at, s.last_reported_at, s.current_stream_id, s.capabilities, COALESCE(s.reported_capabilities, s.capabilities), s.metrics, s.token_id, COALESCE(s.node_token_ciphertext, ''), COALESCE(s.node_token_nonce, ''), COALESCE(s.reported_hostname, ''), COALESCE(s.reported_os, ''), COALESCE(s.reported_arch, ''), s.configure_token_expires_at, s.configure_token_used_at, s.node_token_rotated_at, s.created_at, s.updated_at, a.assignment_role
FROM stream_service_assignments a
JOIN services s ON s.service_id = a.service_id
WHERE a.stream_id = ?
ORDER BY s.service_type, a.assignment_role, s.service_name`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RegisteredService
	for rows.Next() {
		service, err := scanAssignedService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s MariaDBAuthStore) ListServiceAssignmentsForService(ctx context.Context, serviceID string) ([]StreamServiceAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments
WHERE service_id = ?
ORDER BY assigned_at DESC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := make([]StreamServiceAssignment, 0)
	for rows.Next() {
		var assignment StreamServiceAssignment
		if err := rows.Scan(&assignment.StreamID, &assignment.ServiceID, &assignment.ServiceType, &assignment.AssignmentRole, &assignment.AssignedAt); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s MariaDBAuthStore) RequestServiceRestart(ctx context.Context, serviceID string) (RegisteredService, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE services SET status = 'restart_requested', updated_at = ? WHERE service_id = ?`, time.Now().UTC(), serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) WriteStreamEvent(ctx context.Context, token ServiceToken, event ServiceStreamEvent) error {
	if event.ServiceID == "" || event.StreamID == "" || event.EventType == "" {
		return errors.New("missing required stream event field")
	}
	service, err := s.getService(ctx, event.ServiceID)
	if err != nil {
		return err
	}
	if service.TokenID != token.ID {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(service.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	assigned, err := s.isServiceAssigned(ctx, event.ServiceID, event.StreamID)
	if err != nil {
		return err
	}
	if !assigned {
		return ErrForbidden
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	body, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO service_stream_events (id, service_id, stream_id, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`, newUUID(), event.ServiceID, event.StreamID, event.EventType, string(body), time.Now().UTC())
	return err
}

func serviceStreamEventAllowed(serviceType, eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		return false
	}
	allowedPrefixes := map[string][]string{
		"worker":           {"worker.", "overlay.", "caption.", "participant.", "active_speaker.", "current_time."},
		"encoder_recorder": {"encoder.", "recorder.", "archive.", "gdrive.", "media.", "rtmp.", "stream."},
		"discord_bot":      {"discord.", "participant.", "active_speaker."},
		"observability":    {"observability.", "incident.", "diagnostic.", "remediation.", "notification."},
	}
	for _, prefix := range allowedPrefixes[serviceType] {
		if strings.HasPrefix(eventType, prefix) {
			return true
		}
	}
	return false
}

func sanitizeServiceEventPayload(payload map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range payload {
		key = strings.TrimSpace(key)
		if key == "" || serviceCapabilitySecretKey(key) {
			continue
		}
		out[key] = sanitizeServiceEventValue(value)
	}
	return out
}

func sanitizeServiceEventValue(value any) any {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		if secretLikeValue(typed) {
			return "<redacted>"
		}
		return typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeServiceEventValue(item))
		}
		return out
	case map[string]any:
		return sanitizeServiceEventPayload(typed)
	default:
		return nil
	}
}

func (s MariaDBAuthStore) getService(ctx context.Context, id string) (RegisteredService, error) {
	row := s.db.QueryRowContext(ctx, `SELECT service_id, service_type, service_name, COALESCE(description, ''), COALESCE(host, ''), COALESCE(port, 0), COALESCE(ssl_enabled, 0), public_url, version, COALESCE(reported_version, ''), COALESCE(reported_commit, ''), COALESCE(reported_build_date, ''), status, last_heartbeat_at, last_reported_at, current_stream_id, capabilities, COALESCE(reported_capabilities, capabilities), metrics, token_id, COALESCE(node_token_ciphertext, ''), COALESCE(node_token_nonce, ''), COALESCE(reported_hostname, ''), COALESCE(reported_os, ''), COALESCE(reported_arch, ''), configure_token_expires_at, configure_token_used_at, node_token_rotated_at, created_at, updated_at FROM services WHERE service_id = ?`, id)
	service, err := scanService(row)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrNotFound
	}
	return service, err
}

func (s MariaDBAuthStore) isServiceAssigned(ctx context.Context, serviceID, streamID string) (bool, error) {
	var got string
	err := s.db.QueryRowContext(ctx, `SELECT service_id FROM stream_service_assignments WHERE service_id = ? AND stream_id = ?`, serviceID, streamID).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func normalizeAssignmentRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "standby" {
		return "standby"
	}
	return "primary"
}

type serviceScanner interface {
	Scan(dest ...any) error
}

func scanService(row serviceScanner) (RegisteredService, error) {
	var service RegisteredService
	var lastHeartbeat sql.NullTime
	var lastReported sql.NullTime
	var currentStream sql.NullString
	var capabilities string
	var reportedCapabilities string
	var metrics string
	var configureExpires sql.NullTime
	var configureUsed sql.NullTime
	var nodeTokenRotated sql.NullTime
	err := row.Scan(&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.Description, &service.Host, &service.Port, &service.SSLEnabled, &service.PublicURL, &service.Version, &service.ReportedVersion, &service.ReportedCommit, &service.ReportedBuildDate, &service.Status, &lastHeartbeat, &lastReported, &currentStream, &capabilities, &reportedCapabilities, &metrics, &service.TokenID, &service.NodeTokenCiphertext, &service.NodeTokenNonce, &service.ReportedHostname, &service.ReportedOS, &service.ReportedArch, &configureExpires, &configureUsed, &nodeTokenRotated, &service.CreatedAt, &service.UpdatedAt)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if lastReported.Valid {
		service.LastReportedAt = &lastReported.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(reportedCapabilities), &service.ReportedCapabilities)
	if service.ReportedCapabilities == nil {
		service.ReportedCapabilities = map[string]any{}
	}
	if service.ReportedVersion == "" {
		service.ReportedVersion = service.Version
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
	}
	if configureExpires.Valid {
		service.ConfigureTokenExpiresAt = &configureExpires.Time
	}
	if configureUsed.Valid {
		service.ConfigureTokenUsedAt = &configureUsed.Time
	}
	if nodeTokenRotated.Valid {
		service.NodeTokenRotatedAt = &nodeTokenRotated.Time
	}
	if service.Host == "" || service.Port == 0 {
		fillServiceEndpointFromURL(&service)
	}
	return service, nil
}

func scanAssignedService(row serviceScanner) (RegisteredService, error) {
	service, err := scanServiceWithExtraRole(row)
	if err != nil {
		return RegisteredService{}, err
	}
	return service, nil
}

func scanServiceWithExtraRole(row serviceScanner) (RegisteredService, error) {
	var service RegisteredService
	var lastHeartbeat sql.NullTime
	var lastReported sql.NullTime
	var currentStream sql.NullString
	var capabilities string
	var reportedCapabilities string
	var metrics string
	var configureExpires sql.NullTime
	var configureUsed sql.NullTime
	var nodeTokenRotated sql.NullTime
	err := row.Scan(&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.Description, &service.Host, &service.Port, &service.SSLEnabled, &service.PublicURL, &service.Version, &service.ReportedVersion, &service.ReportedCommit, &service.ReportedBuildDate, &service.Status, &lastHeartbeat, &lastReported, &currentStream, &capabilities, &reportedCapabilities, &metrics, &service.TokenID, &service.NodeTokenCiphertext, &service.NodeTokenNonce, &service.ReportedHostname, &service.ReportedOS, &service.ReportedArch, &configureExpires, &configureUsed, &nodeTokenRotated, &service.CreatedAt, &service.UpdatedAt, &service.AssignmentRole)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if lastReported.Valid {
		service.LastReportedAt = &lastReported.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(reportedCapabilities), &service.ReportedCapabilities)
	if service.ReportedCapabilities == nil {
		service.ReportedCapabilities = map[string]any{}
	}
	if service.ReportedVersion == "" {
		service.ReportedVersion = service.Version
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
	}
	if configureExpires.Valid {
		service.ConfigureTokenExpiresAt = &configureExpires.Time
	}
	if configureUsed.Valid {
		service.ConfigureTokenUsedAt = &configureUsed.Time
	}
	if nodeTokenRotated.Valid {
		service.NodeTokenRotatedAt = &nodeTokenRotated.Time
	}
	if service.Host == "" || service.Port == 0 {
		fillServiceEndpointFromURL(&service)
	}
	return service, nil
}

func sanitizeServiceMetrics(metrics map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metrics {
		if strings.TrimSpace(key) == "" {
			continue
		}
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			out[key] = typed
		}
	}
	return out
}

func serviceMetricSnapshotNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func sanitizeServiceCapabilities(capabilities map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range capabilities {
		key = strings.TrimSpace(key)
		if key == "" || serviceCapabilitySecretKey(key) {
			continue
		}
		out[key] = sanitizeServiceCapabilityValue(value)
	}
	return out
}

func sanitizeServiceCapabilityValue(value any) any {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		if secretLikeValue(typed) {
			return "<redacted>"
		}
		return typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeServiceCapabilityValue(item))
		}
		return out
	case map[string]any:
		return sanitizeServiceCapabilities(typed)
	default:
		return nil
	}
}

func serviceCapabilitySecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"password", "passwd", "token", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "client_secret", "refresh_token", "access_token", "authorization", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func validateServiceRegistration(registration ServiceRegistration) error {
	registration = normalizeServiceRegistration(registration)
	if strings.TrimSpace(registration.ServiceID) == "" || strings.TrimSpace(registration.ServiceName) == "" || strings.TrimSpace(registration.PublicURL) == "" {
		return ErrInvalidServiceRegistration
	}
	if err := validateServiceType(registration.ServiceType); err != nil {
		return err
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(registration.PublicURL); err != nil {
		return ErrInvalidServiceRegistration
	}
	return nil
}

func validateServiceMetadataUpdate(update ServiceMetadataUpdate) error {
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(update.ServiceName) == "" || strings.TrimSpace(update.PublicURL) == "" {
		return ErrInvalidServiceRegistration
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(update.PublicURL); err != nil {
		return ErrInvalidServiceRegistration
	}
	return nil
}

func normalizeServiceRegistration(registration ServiceRegistration) ServiceRegistration {
	registration.ServiceID = strings.TrimSpace(registration.ServiceID)
	registration.ServiceType = strings.TrimSpace(registration.ServiceType)
	registration.ServiceName = strings.TrimSpace(registration.ServiceName)
	registration.Description = strings.TrimSpace(registration.Description)
	registration.Host = strings.TrimSpace(registration.Host)
	registration.PublicURL = strings.TrimSpace(registration.PublicURL)
	registration.Version = strings.TrimSpace(registration.Version)
	registration.Commit = truncateServiceReportedValue(strings.TrimSpace(registration.Commit), 80)
	registration.BuildDate = truncateServiceReportedValue(strings.TrimSpace(registration.BuildDate), 80)
	registration.Hostname = strings.TrimSpace(registration.Hostname)
	registration.OS = strings.TrimSpace(registration.OS)
	registration.Arch = strings.TrimSpace(registration.Arch)
	if registration.Host == "" || registration.Port == 0 {
		host, port, sslEnabled := endpointFromServiceURL(registration.PublicURL)
		if registration.Host == "" {
			registration.Host = host
		}
		if registration.Port == 0 {
			registration.Port = port
		}
		registration.SSLEnabled = sslEnabled
	}
	if registration.PublicURL == "" {
		registration.PublicURL = buildServiceURL(registration.Host, registration.Port, registration.SSLEnabled)
	}
	return registration
}

func normalizeServiceMetadataUpdate(update ServiceMetadataUpdate) ServiceMetadataUpdate {
	update.ServiceName = strings.TrimSpace(update.ServiceName)
	update.Description = strings.TrimSpace(update.Description)
	update.Host = strings.TrimSpace(update.Host)
	update.PublicURL = strings.TrimSpace(update.PublicURL)
	if update.Host == "" || update.Port == 0 {
		host, port, sslEnabled := endpointFromServiceURL(update.PublicURL)
		if update.Host == "" {
			update.Host = host
		}
		if update.Port == 0 {
			update.Port = port
		}
		if host != "" {
			update.SSLEnabled = sslEnabled
		}
	}
	if update.PublicURL == "" {
		update.PublicURL = buildServiceURL(update.Host, update.Port, update.SSLEnabled)
	}
	return update
}

func normalizeServiceRuntimeReport(report ServiceRuntimeReport) ServiceRuntimeReport {
	report.ServiceID = strings.TrimSpace(report.ServiceID)
	report.Version = truncateServiceReportedValue(strings.TrimSpace(report.Version), 80)
	report.Commit = truncateServiceReportedValue(strings.TrimSpace(report.Commit), 80)
	report.BuildDate = truncateServiceReportedValue(strings.TrimSpace(report.BuildDate), 80)
	report.Hostname = truncateServiceReportedValue(strings.TrimSpace(report.Hostname), 255)
	report.OS = truncateServiceReportedValue(strings.TrimSpace(report.OS), 80)
	report.Arch = truncateServiceReportedValue(strings.TrimSpace(report.Arch), 80)
	return report
}

func truncateServiceReportedValue(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func buildServiceURL(host string, port int, sslEnabled bool) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ""
	}
	scheme := "http"
	if sslEnabled {
		scheme = "https"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(port)
}

func endpointFromServiceURL(raw string) (string, int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", 0, false
	}
	port := 0
	if parsed.Port() != "" {
		if value, err := strconv.Atoi(parsed.Port()); err == nil {
			port = value
		}
	}
	sslEnabled := parsed.Scheme == "https"
	if port == 0 {
		if sslEnabled {
			port = 443
		} else if parsed.Scheme == "http" {
			port = 80
		}
	}
	return parsed.Hostname(), port, sslEnabled
}

func fillServiceEndpointFromURL(service *RegisteredService) {
	if service == nil {
		return
	}
	host, port, sslEnabled := endpointFromServiceURL(service.PublicURL)
	if service.Host == "" {
		service.Host = host
	}
	if service.Port == 0 {
		service.Port = port
	}
	if host != "" {
		service.SSLEnabled = sslEnabled
	}
}

func validateServiceType(serviceType string) error {
	switch serviceType {
	case "discord_bot", "encoder_recorder", "worker", "observability":
		return nil
	default:
		return errors.New("invalid service type")
	}
}

func validateServiceScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("service token requires at least one scope")
	}
	allowed := map[string]bool{
		"service.register": true, "service.heartbeat": true, "service.logs.write": true, "service.status.write": true, "service.config.read": true, "service.secret.resolve": true,
		"worker.events.write": true, "encoder.status.write": true, "discord.status.write": true, "observability.ingest": true,
		"streams.start":       true,
		"remediation.execute": true,
	}
	for _, scope := range scopes {
		if !allowed[scope] {
			return errors.New("invalid service scope")
		}
	}
	return nil
}

func hasString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
