package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type MariaDBAuditStore struct {
	db *sql.DB
}

func NewMariaDBAuditStore(db *sql.DB) MariaDBAuditStore {
	return MariaDBAuditStore{db: db}
}

func (s MariaDBAuditStore) WriteAudit(ctx context.Context, event AuditEvent) error {
	event = redactedAuditEvent(event)
	if event.ID == "" {
		event.ID = newUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	body, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_logs (id, timestamp, actor_user_id, actor_username, actor_ip, user_agent, action, resource_type, resource_id, result, metadata, request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Timestamp, nullString(event.ActorUserID), nullString(event.ActorUsername), nullString(event.ActorIP), nullString(event.UserAgent), event.Action, event.ResourceType, nullString(event.ResourceID), event.Result, string(body), event.RequestID)
	return err
}

func (s MariaDBAuditStore) ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	filter = normalizeAuditFilter(filter)
	where := make([]string, 0, 6)
	args := make([]any, 0, len(filter.Actions)+len(filter.ExcludedActions)+4)
	if len(filter.Actions) > 0 {
		placeholders := make([]string, 0, len(filter.Actions))
		for _, action := range filter.Actions {
			placeholders = append(placeholders, "?")
			args = append(args, action)
		}
		where = append(where, "action IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(filter.ExcludedActions) > 0 {
		placeholders := make([]string, 0, len(filter.ExcludedActions))
		for _, action := range filter.ExcludedActions {
			placeholders = append(placeholders, "?")
			args = append(args, action)
		}
		where = append(where, "action NOT IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Result != "" {
		where = append(where, "result = ?")
		args = append(args, filter.Result)
	}
	if !filter.From.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		where = append(where, "timestamp < ?")
		args = append(args, filter.To)
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		where = append(where, "(action LIKE ? OR actor_username LIKE ? OR resource_type LIKE ? OR resource_id LIKE ? OR result LIKE ? OR metadata LIKE ?)")
		args = append(args, like, like, like, like, like, like)
	}
	query := `SELECT id, timestamp, actor_user_id, actor_username, actor_ip, user_agent, action, resource_type, resource_id, result, metadata, request_id FROM audit_logs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY timestamp DESC LIMIT ?"
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var actorUserID, actorUsername, actorIP, userAgent, resourceID sql.NullString
		var metadata string
		if err := rows.Scan(&event.ID, &event.Timestamp, &actorUserID, &actorUsername, &actorIP, &userAgent, &event.Action, &event.ResourceType, &resourceID, &event.Result, &metadata, &event.RequestID); err != nil {
			return nil, err
		}
		event.ActorUserID = actorUserID.String
		event.ActorUsername = actorUsername.String
		event.ActorIP = actorIP.String
		event.UserAgent = userAgent.String
		event.ResourceID = resourceID.String
		event.Metadata = map[string]any{}
		_ = json.Unmarshal([]byte(metadata), &event.Metadata)
		event = redactedAuditEvent(event)
		events = append(events, event)
	}
	return events, rows.Err()
}

func normalizeAuditFilter(filter AuditFilter) AuditFilter {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	filter.Result = strings.TrimSpace(filter.Result)
	if filter.Result != "success" && filter.Result != "failure" {
		filter.Result = ""
	}
	filter.Query = strings.TrimSpace(filter.Query)
	normalizeActions := func(values []string) []string {
		actions := make([]string, 0, len(values))
		seen := map[string]bool{}
		for _, action := range values {
			action = strings.TrimSpace(action)
			if action == "" || seen[action] {
				continue
			}
			seen[action] = true
			actions = append(actions, action)
		}
		return actions
	}
	filter.Actions = normalizeActions(filter.Actions)
	filter.ExcludedActions = normalizeActions(filter.ExcludedActions)
	return filter
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}
