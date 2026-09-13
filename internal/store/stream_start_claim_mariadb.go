package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s MariaDBStreamStore) ClaimStreamStart(ctx context.Context, request StreamStartClaimRequest) (claimed ClaimedStreamStart, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			claimed = ClaimedStreamStart{}
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	request.StreamID = strings.TrimSpace(request.StreamID)
	request.MaterializeServiceID = strings.TrimSpace(request.MaterializeServiceID)
	expected, err := expectedPrimaryStartAssignments(request.ExpectedPrimaryAssignments)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	serviceIDs := make([]string, 0, len(expected))
	for _, service := range expected {
		serviceIDs = append(serviceIDs, service.ServiceID)
	}
	discoveredTarget, err := discoverMariaDBAssignmentsForStream(ctx, s.db, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	discovered := append([]mariaDBAssignmentRow(nil), discoveredTarget...)
	for _, serviceID := range sortedUniqueStrings(serviceIDs) {
		rows, discoverErr := discoverMariaDBAssignmentsForService(ctx, s.db, serviceID)
		if discoverErr != nil {
			return ClaimedStreamStart{}, discoverErr
		}
		discovered = mergeMariaDBAssignmentRows(discovered, rows)
	}
	streamIDs := []string{request.StreamID}
	for _, row := range discovered {
		serviceIDs = append(serviceIDs, row.ServiceID)
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
	revalidatedTarget, err := discoverMariaDBAssignmentsForStream(ctx, tx, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	revalidated := append([]mariaDBAssignmentRow(nil), revalidatedTarget...)
	for _, serviceID := range sortedUniqueStrings(serviceIDs) {
		rows, revalidateErr := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
		if revalidateErr != nil {
			return ClaimedStreamStart{}, revalidateErr
		}
		revalidated = mergeMariaDBAssignmentRows(revalidated, rows)
	}
	if !mariaDBAssignmentRowsEqual(discovered, revalidated) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	lockedTarget, ok := lockedStreams[request.StreamID]
	if !ok || lockedTarget.DeletedAt != nil || !streamStartClaimStatus(lockedTarget.Status) ||
		!strings.EqualFold(strings.TrimSpace(lockedTarget.Status), strings.TrimSpace(request.ExpectedStatus)) ||
		!lockedTarget.UpdatedAt.Equal(request.ExpectedStreamUpdatedAt) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	if state.protected() {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}

	actual := make(map[string]RegisteredService, len(expected))
	for _, row := range revalidatedTarget {
		if normalizeAssignmentRole(row.AssignmentRole) != "primary" {
			continue
		}
		service, exists := lockedServices[row.ServiceID]
		if !exists || service.ServiceType != row.ServiceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil || owner != request.StreamID || role != "primary" {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = "primary"
		if _, duplicate := actual[service.ServiceType]; duplicate {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		actual[service.ServiceType] = service
	}

	var materialized *RegisteredService
	var materializePreviousAssignmentID string
	if request.MaterializeServiceID != "" {
		candidate, exists := lockedServices[request.MaterializeServiceID]
		if !exists {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		expectedCandidate, exists := expected[candidate.ServiceType]
		if !exists || expectedCandidate.ServiceID != candidate.ServiceID || normalizeAssignmentRole(expectedCandidate.AssignmentRole) != "primary" {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if _, exists := actual[candidate.ServiceType]; exists {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, candidate)
		if consistencyErr != nil || (owner != "" && owner != request.StreamID) || (owner == request.StreamID && role == "primary") {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if owner == request.StreamID {
			for _, row := range revalidatedTarget {
				if row.ServiceID == candidate.ServiceID {
					materializePreviousAssignmentID = row.ID
					break
				}
			}
			if materializePreviousAssignmentID == "" {
				return ClaimedStreamStart{}, ErrServiceAssignmentConflict
			}
		}
		candidate.AssignmentRole = "primary"
		actual[candidate.ServiceType] = candidate
		materialized = &candidate
	}
	if len(actual) != len(expected) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	for serviceType, expectedService := range expected {
		current, exists := actual[serviceType]
		if !exists || current.ServiceID != expectedService.ServiceID || current.ServiceType != expectedService.ServiceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
	}
	for _, requiredType := range []string{"encoder_recorder", "worker", "discord_bot"} {
		if _, exists := actual[requiredType]; !exists {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
	}

	// streams.updated_at is DATETIME(0). Keep the returned immutable claim
	// byte-for-byte equal to the value that a later transaction reads back.
	now := time.Now().UTC().Truncate(time.Second)
	if materialized != nil {
		if materializePreviousAssignmentID != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, materializePreviousAssignmentID); err != nil {
				return ClaimedStreamStart{}, err
			}
		}
		assignmentID := newUUID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_service_assignments
(id, stream_id, service_id, service_type, assignment_role, assigned_by_user_id, assigned_at)
VALUES (?, ?, ?, ?, 'primary', NULLIF(?, ''), ?)`, assignmentID, request.StreamID, materialized.ServiceID, materialized.ServiceType, request.MaterializeActorUserID, now); err != nil {
			return ClaimedStreamStart{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = ?, status = 'assigned', updated_at = ? WHERE service_id = ?`, request.StreamID, now, materialized.ServiceID); err != nil {
			return ClaimedStreamStart{}, err
		}
		materialized.CurrentStreamID = request.StreamID
		materialized.Status = "assigned"
		materialized.AssignmentRole = "primary"
		materialized.UpdatedAt = now
		lockedServices[materialized.ServiceID] = *materialized
		actual[materialized.ServiceType] = *materialized
	}

	authority := StreamArchiveAuthority{}
	archiveRunID := ""
	var archiveStartedAt any
	if request.ArchiveEnabled {
		startedAt := request.ArchiveStartedAt.UTC().Truncate(time.Microsecond)
		if startedAt.IsZero() {
			startedAt = now
		}
		archiveRunID = StreamArchiveRunIDForStart(startedAt)
		archiveStartedAt = startedAt
		authority.RunID = archiveRunID
		authority.StartedAt = cloneTimePtr(&startedAt)
	}
	result, err := tx.ExecContext(ctx, `UPDATE streams
SET status = 'starting', archive_run_id = ?, archive_started_at = ?, archive_reported_at = NULL, updated_at = ?
WHERE id = ? AND status = ? AND updated_at = ? AND deleted_at IS NULL`, archiveRunID, archiveStartedAt, now, request.StreamID, lockedTarget.Status, lockedTarget.UpdatedAt)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	claimedRows, err := discoverMariaDBAssignmentsForStream(ctx, tx, request.StreamID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	primaryAssignments := make([]RegisteredService, 0, 3)
	assignmentClaims := make([]StreamStartAssignmentClaim, 0, 3)
	for _, row := range claimedRows {
		if normalizeAssignmentRole(row.AssignmentRole) != "primary" {
			continue
		}
		service, exists := lockedServices[row.ServiceID]
		if !exists || strings.TrimSpace(service.CurrentStreamID) != request.StreamID {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = "primary"
		primaryAssignments = append(primaryAssignments, service)
		assignmentClaims = append(assignmentClaims, StreamStartAssignmentClaim{AssignmentID: row.ID, ServiceID: row.ServiceID, ServiceType: row.ServiceType, Role: "primary"})
	}
	if len(primaryAssignments) != 3 {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	sort.Slice(primaryAssignments, func(i, j int) bool {
		if primaryAssignments[i].ServiceType == primaryAssignments[j].ServiceType {
			return primaryAssignments[i].ServiceID < primaryAssignments[j].ServiceID
		}
		return primaryAssignments[i].ServiceType < primaryAssignments[j].ServiceType
	})
	sort.Slice(assignmentClaims, func(i, j int) bool { return assignmentClaims[i].AssignmentID < assignmentClaims[j].AssignmentID })
	stream := state.Stream
	stream.Status = "starting"
	stream.ArchiveRunID = archiveRunID
	stream.ArchiveStartedAt = cloneTimePtr(authority.StartedAt)
	stream.ArchiveReportedAt = nil
	stream.UpdatedAt = now
	ownership := StreamStartOwnershipClaim{StreamID: stream.ID, StreamUpdatedAt: now, StreamIdentity: streamStartOwnershipIdentity(stream), Assignments: assignmentClaims, Archive: authority}
	if err := tx.Commit(); err != nil {
		return ClaimedStreamStart{}, err
	}
	return ClaimedStreamStart{Stream: stream, PrimaryAssignments: primaryAssignments, ArchiveAuthority: authority, OwnershipClaim: ownership, Materialized: materialized}, nil
}

func (s MariaDBStreamStore) TransitionClaimedStreamStart(ctx context.Context, claim StreamStartOwnershipClaim, status string) (stream Stream, transitioned bool, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			stream = Stream{}
			transitioned = false
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	claim.StreamID = strings.TrimSpace(claim.StreamID)
	streamIDs := []string{claim.StreamID}
	serviceIDs := make([]string, 0, len(claim.Assignments))
	claims := append([]StreamStartAssignmentClaim(nil), claim.Assignments...)
	for _, assignment := range claims {
		serviceIDs = append(serviceIDs, assignment.ServiceID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, false, err
	}
	defer tx.Rollback()
	_, err = lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return Stream{}, false, err
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return Stream{}, false, ErrServiceAssignmentConflict
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].AssignmentID < claims[j].AssignmentID })
	for _, assignment := range claims {
		var row mariaDBAssignmentRow
		err := tx.QueryRowContext(ctx, `SELECT id, stream_id, service_id, service_type, assignment_role, assigned_at
FROM stream_service_assignments WHERE id = ? FOR UPDATE`, assignment.AssignmentID).Scan(
			&row.ID, &row.StreamID, &row.ServiceID, &row.ServiceType, &row.AssignmentRole, &row.AssignedAt,
		)
		if err != nil || row.StreamID != claim.StreamID || row.ServiceID != assignment.ServiceID || row.ServiceType != assignment.ServiceType || normalizeAssignmentRole(row.AssignmentRole) != normalizeAssignmentRole(assignment.Role) {
			return Stream{}, false, ErrServiceAssignmentConflict
		}
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, claim.StreamID)
	if err != nil {
		return Stream{}, false, err
	}
	stream = state.Stream
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return stream, false, nil
	}
	if strings.TrimSpace(claim.StreamIdentity) == "" || streamStartOwnershipIdentity(stream) != claim.StreamIdentity || !stream.UpdatedAt.Equal(claim.StreamUpdatedAt) || !archiveAuthorityMatchesClaim(stream, claim.Archive) {
		return stream, false, ErrServiceAssignmentConflict
	}
	for _, assignment := range claims {
		service, exists := lockedServices[assignment.ServiceID]
		if !exists || strings.TrimSpace(service.CurrentStreamID) != claim.StreamID {
			return stream, false, ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil || owner != claim.StreamID || role != normalizeAssignmentRole(assignment.Role) {
			return stream, false, ErrServiceAssignmentConflict
		}
	}
	// streams.updated_at is DATETIME(0), unlike archive_started_at DATETIME(6).
	now := time.Now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx, `UPDATE streams SET status = ?, updated_at = ? WHERE id = ? AND status = 'starting' AND updated_at = ?`, strings.TrimSpace(status), now, claim.StreamID, claim.StreamUpdatedAt)
	if err != nil {
		return Stream{}, false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return stream, false, ErrServiceAssignmentConflict
	}
	stream.Status = strings.TrimSpace(status)
	stream.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return Stream{}, false, err
	}
	return stream, true, nil
}
