package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

type mariaDBLockedStream struct {
	ID                string
	Status            string
	ArchiveRunID      string
	ArchiveStartedAt  *time.Time
	ArchiveReportedAt *time.Time
	UpdatedAt         time.Time
	DeletedAt         *time.Time
}

func lockMariaDBStreamsSorted(ctx context.Context, tx *sql.Tx, streamIDs []string) (map[string]mariaDBLockedStream, error) {
	locked := make(map[string]mariaDBLockedStream)
	for _, streamID := range sortedUniqueStrings(streamIDs) {
		var row mariaDBLockedStream
		var startedAt, reportedAt, deletedAt sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT id, status, COALESCE(archive_run_id, ''), archive_started_at, archive_reported_at, updated_at, deleted_at
FROM streams WHERE id = ? FOR UPDATE`, streamID).Scan(&row.ID, &row.Status, &row.ArchiveRunID, &startedAt, &reportedAt, &row.UpdatedAt, &deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		row.ArchiveStartedAt = nullTimePtr(startedAt)
		row.ArchiveReportedAt = nullTimePtr(reportedAt)
		row.DeletedAt = nullTimePtr(deletedAt)
		locked[row.ID] = row
	}
	return locked, nil
}

func lockMariaDBServicesSorted(ctx context.Context, tx *sql.Tx, serviceIDs []string) (map[string]RegisteredService, error) {
	locked := make(map[string]RegisteredService)
	for _, serviceID := range sortedUniqueStrings(serviceIDs) {
		service, err := lockMariaDBService(ctx, tx, serviceID)
		if err != nil {
			return nil, err
		}
		locked[service.ServiceID] = service
	}
	return locked, nil
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func equalSortedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}

func mariaDBStreamAssignmentProtectionAfterLocks(ctx context.Context, tx *sql.Tx, streamID string) (streamAssignmentProtection, error) {
	query := `SELECT ` + streamSelectFields + `
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
LEFT JOIN drive_destinations dd ON dd.id = ss.archive_drive_destination_id
WHERE s.id = ? AND s.deleted_at IS NULL`
	stream, err := scanStreamRow(tx.QueryRowContext(ctx, query, streamID))
	if errors.Is(err, sql.ErrNoRows) {
		return streamAssignmentProtection{}, ErrNotFound
	}
	if err != nil {
		return streamAssignmentProtection{}, err
	}
	state := streamAssignmentProtection{Stream: stream}
	if err := tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM service_stream_events WHERE stream_id = s.id AND event_type = 'archive.artifacts.reported'),
	`+archiveRecordingArtifactExistsCondition+`,
	`+streamLogGuardPendingCondition+`
FROM streams s WHERE s.id = ?`,
		archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage,
		streamID,
	).Scan(
		&state.HasArchiveReport, &state.HasRecordingArtifact, &state.ArchiveRetryPending,
	); err != nil {
		return streamAssignmentProtection{}, err
	}
	return state, nil
}

func insertMariaDBStreamLogGuard(ctx context.Context, tx *sql.Tx, streamID, pendingMessage string, createdAt time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_logs (id, stream_id, level, message, fields, created_at)
VALUES (?, ?, 'info', ?, '{}', ?)`, newUUID(), strings.TrimSpace(streamID), pendingMessage, createdAt)
	return err
}

func closeMariaDBStreamLogGuard(ctx context.Context, tx *sql.Tx, streamID, pendingMessage, closedMessage string, closedAt time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT guard_pending.id
FROM stream_logs guard_pending
WHERE guard_pending.stream_id = ?
  AND guard_pending.message = ?
  AND NOT EXISTS (
    SELECT 1
    FROM stream_logs guard_closed
    WHERE guard_closed.stream_id = guard_pending.stream_id
      AND guard_closed.message = ?
      AND JSON_UNQUOTE(JSON_EXTRACT(guard_closed.fields, '$.pending_id')) = guard_pending.id
  )
ORDER BY guard_pending.id`, strings.TrimSpace(streamID), pendingMessage, closedMessage)
	if err != nil {
		return err
	}
	pendingIDs := make([]string, 0)
	for rows.Next() {
		var pendingID string
		if err := rows.Scan(&pendingID); err != nil {
			rows.Close()
			return err
		}
		pendingIDs = append(pendingIDs, pendingID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, pendingID := range pendingIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_logs (id, stream_id, level, message, fields, created_at)
VALUES (?, ?, 'info', ?, JSON_OBJECT('pending_id', ?), ?)`, newUUID(), strings.TrimSpace(streamID), closedMessage, pendingID, closedAt); err != nil {
			return err
		}
	}
	return nil
}
