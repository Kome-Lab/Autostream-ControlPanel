package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s MariaDBStreamStore) claimSelectedStreamStart(ctx context.Context, request StreamStartClaimRequest) (claimed ClaimedStreamStart, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			claimed = ClaimedStreamStart{}
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	request.StreamID = strings.TrimSpace(request.StreamID)
	expected, err := expectedPrimaryStartAssignments(request.ExpectedPrimaryAssignments)
	if err != nil || len(expected) != 3 {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	targetRows, err := discoverMariaDBAssignmentsForStream(ctx, s.db, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	serviceIDs := make([]string, 0, 6)
	for _, service := range expected {
		serviceIDs = append(serviceIDs, service.ServiceID)
	}
	for _, row := range targetRows {
		serviceIDs = append(serviceIDs, row.ServiceID)
	}
	serviceIDs = sortedUniqueStrings(serviceIDs)
	discovered := append([]mariaDBAssignmentRow(nil), targetRows...)
	for _, serviceID := range serviceIDs {
		rows, discoverErr := discoverMariaDBAssignmentsForService(ctx, s.db, serviceID)
		if discoverErr != nil {
			return ClaimedStreamStart{}, discoverErr
		}
		discovered = mergeMariaDBAssignmentRows(discovered, rows)
	}
	streamIDs := []string{request.StreamID}
	for _, service := range expected {
		streamIDs = append(streamIDs, service.CurrentStreamID)
	}
	for _, row := range discovered {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discovered); err != nil {
		return ClaimedStreamStart{}, err
	}
	revalidated := []mariaDBAssignmentRow(nil)
	for _, serviceID := range serviceIDs {
		rows, revalidateErr := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
		if revalidateErr != nil {
			return ClaimedStreamStart{}, revalidateErr
		}
		revalidated = mergeMariaDBAssignmentRows(revalidated, rows)
	}
	revalidatedTarget, err := discoverMariaDBAssignmentsForStream(ctx, tx, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	if !mariaDBAssignmentRowsEqual(discovered, revalidated) || !mariaDBAssignmentRowsEqual(targetRows, revalidatedTarget) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	target, ok := lockedStreams[request.StreamID]
	if !ok || target.DeletedAt != nil || !streamStartClaimStatus(target.Status) ||
		!strings.EqualFold(strings.TrimSpace(target.Status), strings.TrimSpace(request.ExpectedStatus)) ||
		!target.UpdatedAt.Equal(request.ExpectedStreamUpdatedAt) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	if state.protected() {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	stream := state.Stream
	if _, err := validateSelectedStartClaim(request, stream); err != nil {
		return ClaimedStreamStart{}, err
	}
	wanted := make(map[string]RegisteredService, 3)
	for serviceType, observed := range expected {
		service, exists := lockedServices[observed.ServiceID]
		if !exists || service.ServiceType != serviceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, _, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if owner != "" && owner != request.StreamID {
			if _, locked := lockedStreams[owner]; !locked {
				return ClaimedStreamStart{}, ErrServiceAssignmentConflict
			}
			previous, stateErr := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, owner)
			if stateErr != nil || previous.DeletedAt != nil {
				return ClaimedStreamStart{}, ErrServiceAssignmentConflict
			}
			if previous.protected() {
				return ClaimedStreamStart{}, ErrNodeExecutionBusy
			}
		}
		if service.CurrentStreamID != observed.CurrentStreamID {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		wanted[service.ServiceID] = service
	}
	keep := make(map[string]mariaDBAssignmentRow, 3)
	remove := make([]mariaDBAssignmentRow, 0, len(revalidated))
	for _, row := range revalidated {
		_, selected := wanted[row.ServiceID]
		if row.StreamID != request.StreamID && !selected {
			continue
		}
		service, exists := lockedServices[row.ServiceID]
		if !exists || service.ServiceType != row.ServiceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil || owner != row.StreamID || role != normalizeAssignmentRole(row.AssignmentRole) {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if selected && row.StreamID == request.StreamID && normalizeAssignmentRole(row.AssignmentRole) == "primary" {
			keep[row.ServiceID] = row
			continue
		}
		remove = append(remove, row)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	authority := StreamArchiveAuthority{}
	if request.ArchiveEnabled {
		startedAt := request.ArchiveStartedAt.UTC().Truncate(time.Microsecond)
		if startedAt.IsZero() {
			startedAt = now
		}
		authority.RunID = StreamArchiveRunIDForStart(startedAt)
		authority.StartedAt = cloneTimePtr(&startedAt)
	}
	if streamStartClockConflict(stream, now, authority) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	for _, row := range remove {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, row.ID)
		if deleteErr != nil {
			return ClaimedStreamStart{}, deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if _, selected := wanted[row.ServiceID]; !selected {
			if _, err := tx.ExecContext(ctx, `UPDATE services
SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ?
WHERE service_id = ?`, now, row.ServiceID); err != nil {
				return ClaimedStreamStart{}, err
			}
		}
	}
	primary := make([]RegisteredService, 0, 3)
	claims := make([]StreamStartAssignmentClaim, 0, 3)
	for _, serviceID := range sortedUniqueStrings([]string{stream.SelectedEncoderID, stream.SelectedWorkerID, stream.SelectedDiscordID}) {
		service := wanted[serviceID]
		row, retained := keep[serviceID]
		if !retained {
			row = mariaDBAssignmentRow{ID: newUUID(), StreamID: stream.ID, ServiceID: serviceID, ServiceType: service.ServiceType, AssignmentRole: "primary", AssignedAt: now}
			if _, err := tx.ExecContext(ctx, `INSERT INTO stream_service_assignments
(id, stream_id, service_id, service_type, assignment_role, assigned_by_user_id, assigned_at)
VALUES (?, ?, ?, ?, 'primary', NULLIF(?, ''), ?)`, row.ID, stream.ID, serviceID, service.ServiceType, startClaimUserID(request.MaterializeActorUserID), now); err != nil {
				return ClaimedStreamStart{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = ?, status = 'assigned', updated_at = ? WHERE service_id = ?`, stream.ID, now, serviceID); err != nil {
				return ClaimedStreamStart{}, err
			}
			service.CurrentStreamID, service.Status, service.UpdatedAt = stream.ID, "assigned", now
		}
		service.AssignmentRole = "primary"
		primary = append(primary, service)
		claims = append(claims, StreamStartAssignmentClaim{AssignmentID: row.ID, ServiceID: serviceID, ServiceType: service.ServiceType, Role: "primary"})
	}
	stream.Status = "starting"
	stream.ArchiveRunID, stream.ArchiveStartedAt, stream.ArchiveReportedAt = authority.RunID, cloneTimePtr(authority.StartedAt), nil
	stream.UpdatedAt = now
	if err := recordStreamArchiveRunSourceTx(ctx, tx, stream, expected["encoder_recorder"].ServiceID); err != nil {
		return ClaimedStreamStart{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE streams
SET status = 'starting', archive_run_id = ?, archive_started_at = ?, archive_reported_at = NULL, updated_at = ?
WHERE id = ? AND status = ? AND updated_at = ? AND deleted_at IS NULL`,
		stream.ArchiveRunID, stream.ArchiveStartedAt, now, stream.ID, target.Status, target.UpdatedAt)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	sort.Slice(primary, func(i, j int) bool { return primary[i].ServiceType < primary[j].ServiceType })
	sort.Slice(claims, func(i, j int) bool { return claims[i].AssignmentID < claims[j].AssignmentID })
	ownership := StreamStartOwnershipClaim{StreamID: stream.ID, StreamUpdatedAt: now,
		StreamIdentity: streamStartOwnershipIdentity(stream), Assignments: claims, Archive: authority}
	if err := tx.Commit(); err != nil {
		return ClaimedStreamStart{}, err
	}
	return ClaimedStreamStart{Stream: stream, PrimaryAssignments: primary, ArchiveAuthority: authority, OwnershipClaim: ownership}, nil
}
