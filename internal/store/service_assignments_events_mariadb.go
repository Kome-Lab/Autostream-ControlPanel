package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s MariaDBAuthStore) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error) {
	return s.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, ActorUserID: actorUserID, AssignmentRole: "primary"})
}

func (s MariaDBAuthStore) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error) {
	return s.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, ActorUserID: actorUserID, AssignmentRole: assignmentRole})
}

func streamAssignableServiceType(serviceType string) bool {
	switch strings.TrimSpace(serviceType) {
	case "discord_bot", "encoder_recorder", "worker", "observability":
		return true
	default:
		return false
	}
}

func (s MariaDBAuthStore) UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error) {
	return s.UnassignServiceFromStreamGuarded(ctx, ServiceUnassignmentMutation{ServiceID: serviceID, ActorUserID: actorUserID})
}

func (s MariaDBAuthStore) ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, serviceSelectColumnsAliased+`, a.assignment_role
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
