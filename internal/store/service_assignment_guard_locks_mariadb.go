package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func lockMariaDBService(ctx context.Context, tx *sql.Tx, serviceID string) (RegisteredService, error) {
	service, err := scanService(tx.QueryRowContext(ctx, serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredService{}, ErrNotFound
	}
	return service, err
}

func consistentMariaDBServiceAssignment(ctx context.Context, tx *sql.Tx, service RegisteredService) (string, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments WHERE service_id = ?`, service.ServiceID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	assignments := make([]StreamServiceAssignment, 0, 1)
	for rows.Next() {
		var assignment StreamServiceAssignment
		if err := rows.Scan(&assignment.StreamID, &assignment.ServiceID, &assignment.ServiceType, &assignment.AssignmentRole, &assignment.AssignedAt); err != nil {
			return "", "", err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if len(assignments) > 1 || (len(assignments) == 0) != (strings.TrimSpace(service.CurrentStreamID) == "") {
		return "", "", ErrServiceAssignmentConflict
	}
	if len(assignments) == 0 {
		return "", "", nil
	}
	assignment := assignments[0]
	if assignment.StreamID != strings.TrimSpace(service.CurrentStreamID) || assignment.ServiceType != service.ServiceType {
		return "", "", ErrServiceAssignmentConflict
	}
	return assignment.StreamID, normalizeAssignmentRole(assignment.AssignmentRole), nil
}

func lockMariaDBStreamAssignmentProtection(ctx context.Context, tx *sql.Tx, streamID string) (streamAssignmentProtection, error) {
	query := `SELECT ` + streamSelectFields + `
FROM streams s
LEFT JOIN stream_settings ss ON ss.stream_id = s.id
LEFT JOIN drive_destinations dd ON dd.id = ss.archive_drive_destination_id
WHERE s.id = ? AND s.deleted_at IS NULL
FOR UPDATE`
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
	).Scan(&state.HasArchiveReport, &state.HasRecordingArtifact, &state.ArchiveRetryPending); err != nil {
		return streamAssignmentProtection{}, err
	}
	return state, nil
}

func lockMariaDBTargetPrimaryOwners(ctx context.Context, tx *sql.Tx, streamID, serviceType string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT service_id FROM stream_service_assignments
WHERE stream_id = ? AND service_type = ? AND assignment_role = 'primary'
ORDER BY service_id FOR UPDATE`, streamID, serviceType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := make([]string, 0, 1)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, err
		}
		owners = append(owners, serviceID)
	}
	return owners, rows.Err()
}
