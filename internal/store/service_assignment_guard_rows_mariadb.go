package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

type mariaDBAssignmentRow struct {
	ID             string
	StreamID       string
	ServiceID      string
	ServiceType    string
	AssignmentRole string
	AssignedAt     time.Time
}

type mariaDBAssignmentQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func discoverMariaDBAssignmentsForService(ctx context.Context, queryer mariaDBAssignmentQueryer, serviceID string) ([]mariaDBAssignmentRow, error) {
	return queryMariaDBAssignmentRows(ctx, queryer, `SELECT id, stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments WHERE service_id = ? ORDER BY id`, strings.TrimSpace(serviceID))
}

func discoverMariaDBAssignmentsForStream(ctx context.Context, queryer mariaDBAssignmentQueryer, streamID string) ([]mariaDBAssignmentRow, error) {
	return queryMariaDBAssignmentRows(ctx, queryer, `SELECT id, stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments WHERE stream_id = ? ORDER BY id`, strings.TrimSpace(streamID))
}

func discoverMariaDBTargetPrimaryAssignments(ctx context.Context, queryer mariaDBAssignmentQueryer, streamID, serviceType string) ([]mariaDBAssignmentRow, error) {
	return queryMariaDBAssignmentRows(ctx, queryer, `SELECT id, stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments
WHERE stream_id = ? AND service_type = ? AND assignment_role = 'primary'
ORDER BY id`, strings.TrimSpace(streamID), strings.TrimSpace(serviceType))
}

func discoverMariaDBCurrentStreamServiceIDs(ctx context.Context, queryer mariaDBAssignmentQueryer, streamID string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT service_id FROM services WHERE current_stream_id = ? ORDER BY service_id`, strings.TrimSpace(streamID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	serviceIDs := make([]string, 0)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, err
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	return serviceIDs, rows.Err()
}

func queryMariaDBAssignmentRows(ctx context.Context, queryer mariaDBAssignmentQueryer, query string, args ...any) ([]mariaDBAssignmentRow, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]mariaDBAssignmentRow, 0)
	for rows.Next() {
		var row mariaDBAssignmentRow
		if err := rows.Scan(&row.ID, &row.StreamID, &row.ServiceID, &row.ServiceType, &row.AssignmentRole, &row.AssignedAt); err != nil {
			return nil, err
		}
		row.AssignmentRole = normalizeAssignmentRole(row.AssignmentRole)
		result = append(result, row)
	}
	return result, rows.Err()
}

func mergeMariaDBAssignmentRows(groups ...[]mariaDBAssignmentRow) []mariaDBAssignmentRow {
	byID := make(map[string]mariaDBAssignmentRow)
	for _, group := range groups {
		for _, row := range group {
			byID[row.ID] = row
		}
	}
	rows := make([]mariaDBAssignmentRow, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func lockMariaDBAssignmentRowsSorted(ctx context.Context, tx *sql.Tx, discovered []mariaDBAssignmentRow) error {
	rows := append([]mariaDBAssignmentRow(nil), discovered...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for _, expected := range rows {
		var current mariaDBAssignmentRow
		err := tx.QueryRowContext(ctx, `SELECT id, stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments WHERE id = ? FOR UPDATE`, expected.ID).Scan(
			&current.ID, &current.StreamID, &current.ServiceID, &current.ServiceType, &current.AssignmentRole, &current.AssignedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServiceAssignmentConflict
		}
		if err != nil {
			return err
		}
		current.AssignmentRole = normalizeAssignmentRole(current.AssignmentRole)
		if !mariaDBAssignmentRowEqual(current, expected) {
			return ErrServiceAssignmentConflict
		}
	}
	return nil
}

func mariaDBAssignmentRowsEqual(left, right []mariaDBAssignmentRow) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]mariaDBAssignmentRow(nil), left...)
	right = append([]mariaDBAssignmentRow(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i].ID < left[j].ID })
	sort.Slice(right, func(i, j int) bool { return right[i].ID < right[j].ID })
	for index := range left {
		if !mariaDBAssignmentRowEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func mariaDBAssignmentRowEqual(left, right mariaDBAssignmentRow) bool {
	return left.ID == right.ID && left.StreamID == right.StreamID && left.ServiceID == right.ServiceID &&
		left.ServiceType == right.ServiceType && normalizeAssignmentRole(left.AssignmentRole) == normalizeAssignmentRole(right.AssignmentRole) &&
		left.AssignedAt.Equal(right.AssignedAt)
}
