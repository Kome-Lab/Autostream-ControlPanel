package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func (s MariaDBStreamStore) RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error) {
	return s.AppendStreamLog(ctx, StreamLog{
		ID: newUUID(), StreamID: id, Level: "info", Message: "archive upload retry requested",
		Fields: map[string]any{"actor_user_id": actorUserID}, CreatedAt: time.Now().UTC(),
	})
}

func (s MariaDBStreamStore) AppendStreamLog(ctx context.Context, log StreamLog) (StreamLog, error) {
	if _, err := s.GetStream(ctx, strings.TrimSpace(log.StreamID)); err != nil {
		return StreamLog{}, err
	}
	log = normalizeStreamLog(log)
	fields, err := json.Marshal(log.Fields)
	if err != nil {
		return StreamLog{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stream_logs (id, stream_id, level, message, fields, created_at) VALUES (?, ?, ?, ?, ?, ?)`, log.ID, log.StreamID, log.Level, log.Message, string(fields), log.CreatedAt)
	return log, err
}

func (s MariaDBStreamStore) ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error) {
	if _, err := s.GetStream(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.stream_id, COALESCE(s.name, ''), s.deleted_at, l.level, l.message, l.fields, l.created_at
FROM stream_logs l
LEFT JOIN streams s ON s.id = l.stream_id
WHERE l.stream_id = ?
ORDER BY l.created_at DESC
LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []StreamLog
	for rows.Next() {
		var log StreamLog
		var fields string
		var deletedAt sql.NullTime
		if err := rows.Scan(&log.ID, &log.StreamID, &log.StreamName, &deletedAt, &log.Level, &log.Message, &fields, &log.CreatedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			value := deletedAt.Time
			log.StreamDeletedAt = &value
		}
		_ = json.Unmarshal([]byte(fields), &log.Fields)
		if log.Fields == nil {
			log.Fields = map[string]any{}
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s MariaDBStreamStore) ListStreamLogHistory(ctx context.Context, limit int, before time.Time, beforeID string) ([]StreamLog, error) {
	limit = boundedStreamLogLimit(limit)
	const columns = `SELECT l.id, l.stream_id, COALESCE(s.name, ''), s.deleted_at, l.level, l.message, l.fields, l.created_at
FROM stream_logs l
LEFT JOIN streams s ON s.id = l.stream_id`
	var rows *sql.Rows
	var err error
	if before.IsZero() {
		rows, err = s.db.QueryContext(ctx, columns+` ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, columns+` WHERE l.created_at < ? OR (l.created_at = ? AND l.id < ?) ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, before.UTC(), before.UTC(), strings.TrimSpace(beforeID), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]StreamLog, 0, limit)
	for rows.Next() {
		var log StreamLog
		var fields string
		var deletedAt sql.NullTime
		if err := rows.Scan(&log.ID, &log.StreamID, &log.StreamName, &deletedAt, &log.Level, &log.Message, &fields, &log.CreatedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			value := deletedAt.Time
			log.StreamDeletedAt = &value
		}
		_ = json.Unmarshal([]byte(fields), &log.Fields)
		if log.Fields == nil {
			log.Fields = map[string]any{}
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func normalizeStreamLog(log StreamLog) StreamLog {
	log.StreamID = strings.TrimSpace(log.StreamID)
	if strings.TrimSpace(log.ID) == "" {
		log.ID = newUUID()
	}
	log.Level = strings.ToLower(strings.TrimSpace(log.Level))
	switch log.Level {
	case "debug", "info", "warning", "error":
	default:
		log.Level = "info"
	}
	log.Message = strings.TrimSpace(log.Message)
	if log.Fields == nil {
		log.Fields = map[string]any{}
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	} else {
		log.CreatedAt = log.CreatedAt.UTC()
	}
	return log
}

func boundedStreamLogLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}
