package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	ServiceID       string         `json:"service_id"`
	ServiceType     string         `json:"service_type"`
	ServiceName     string         `json:"service_name"`
	PublicURL       string         `json:"public_url"`
	Version         string         `json:"version"`
	Status          string         `json:"status"`
	AssignmentRole  string         `json:"assignment_role,omitempty"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	CurrentStreamID string         `json:"current_stream_id,omitempty"`
	Capabilities    map[string]any `json:"capabilities"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	TokenID         string         `json:"-"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type ServiceRegistration struct {
	ServiceID    string         `json:"service_id"`
	ServiceType  string         `json:"service_type"`
	ServiceName  string         `json:"service_name"`
	PublicURL    string         `json:"public_url"`
	Version      string         `json:"version"`
	Capabilities map[string]any `json:"capabilities"`
}

type ServiceHeartbeat struct {
	ServiceID       string         `json:"service_id"`
	Status          string         `json:"status"`
	CurrentStreamID string         `json:"current_stream_id,omitempty"`
	Metrics         map[string]any `json:"metrics,omitempty"`
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
	ListServices(ctx context.Context) ([]RegisteredService, error)
	ListWorkers(ctx context.Context) ([]RegisteredService, error)
	GetService(ctx context.Context, id string) (RegisteredService, error)
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
	if _, err := tx.ExecContext(ctx, `UPDATE services SET token_id = ?, updated_at = ? WHERE token_id = ?`, token.ID, now, oldToken.ID); err != nil {
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
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(registration.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO services (service_id, service_type, service_name, public_url, version, status, capabilities, metrics, token_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?)`, registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.PublicURL, registration.Version, string(capabilities), "{}", token.ID, now, now)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO services (service_id, service_type, service_name, public_url, version, status, capabilities, metrics, token_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'registered', ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE service_type = VALUES(service_type), service_name = VALUES(service_name), public_url = VALUES(public_url), version = VALUES(version), status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, capabilities = VALUES(capabilities), token_id = VALUES(token_id), updated_at = VALUES(updated_at)`, registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.PublicURL, registration.Version, string(capabilities), "{}", token.ID, now, now)
	if err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, registration.ServiceID)
}

func (s MariaDBAuthStore) Heartbeat(ctx context.Context, token ServiceToken, heartbeat ServiceHeartbeat) (RegisteredService, error) {
	if heartbeat.CurrentStreamID != "" {
		assigned, err := s.isServiceAssigned(ctx, heartbeat.ServiceID, heartbeat.CurrentStreamID)
		if err != nil {
			return RegisteredService{}, err
		}
		if !assigned {
			return RegisteredService{}, ErrForbidden
		}
	}
	now := time.Now().UTC()
	metrics, err := json.Marshal(sanitizeServiceMetrics(heartbeat.Metrics))
	if err != nil {
		return RegisteredService{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE services SET status = ?, last_heartbeat_at = ?, current_stream_id = CASE WHEN ? = '' THEN current_stream_id ELSE ? END, metrics = ?, updated_at = ? WHERE service_id = ? AND token_id = ?`, heartbeat.Status, now, heartbeat.CurrentStreamID, heartbeat.CurrentStreamID, string(metrics), now, heartbeat.ServiceID, token.ID)
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
	return s.getService(ctx, heartbeat.ServiceID)
}

func (s MariaDBAuthStore) ListServices(ctx context.Context) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, s.public_url, s.version, s.status, s.last_heartbeat_at, s.current_stream_id, s.capabilities, s.metrics, s.token_id, s.created_at, s.updated_at, COALESCE(a.assignment_role, '')
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
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, s.public_url, s.version, s.status, s.last_heartbeat_at, s.current_stream_id, s.capabilities, s.metrics, s.token_id, s.created_at, s.updated_at, COALESCE(a.assignment_role, '')
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
	rows, err := s.db.QueryContext(ctx, `SELECT s.service_id, s.service_type, s.service_name, s.public_url, s.version, s.status, s.last_heartbeat_at, s.current_stream_id, s.capabilities, s.metrics, s.token_id, s.created_at, s.updated_at, a.assignment_role
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
	row := s.db.QueryRowContext(ctx, `SELECT service_id, service_type, service_name, public_url, version, status, last_heartbeat_at, current_stream_id, capabilities, metrics, token_id, created_at, updated_at FROM services WHERE service_id = ?`, id)
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
	var currentStream sql.NullString
	var capabilities string
	var metrics string
	err := row.Scan(&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.PublicURL, &service.Version, &service.Status, &lastHeartbeat, &currentStream, &capabilities, &metrics, &service.TokenID, &service.CreatedAt, &service.UpdatedAt)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
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
	var currentStream sql.NullString
	var capabilities string
	var metrics string
	err := row.Scan(&service.ServiceID, &service.ServiceType, &service.ServiceName, &service.PublicURL, &service.Version, &service.Status, &lastHeartbeat, &currentStream, &capabilities, &metrics, &service.TokenID, &service.CreatedAt, &service.UpdatedAt, &service.AssignmentRole)
	if err != nil {
		return RegisteredService{}, err
	}
	if lastHeartbeat.Valid {
		service.LastHeartbeatAt = &lastHeartbeat.Time
	}
	if currentStream.Valid {
		service.CurrentStreamID = currentStream.String
	}
	_ = json.Unmarshal([]byte(capabilities), &service.Capabilities)
	if service.Capabilities == nil {
		service.Capabilities = map[string]any{}
	}
	_ = json.Unmarshal([]byte(metrics), &service.Metrics)
	if service.Metrics == nil {
		service.Metrics = map[string]any{}
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
