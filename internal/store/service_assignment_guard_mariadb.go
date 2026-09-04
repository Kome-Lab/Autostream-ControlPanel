package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s MariaDBAuthStore) AssignServiceToStreamGuarded(ctx context.Context, mutation ServiceAssignmentMutation) (service RegisteredService, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			service = RegisteredService{}
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	mutation.StreamID = strings.TrimSpace(mutation.StreamID)
	mutation.AssignmentRole = normalizeAssignmentRole(mutation.AssignmentRole)
	discoveredService, err := s.getService(ctx, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	if !streamAssignableServiceType(discoveredService.ServiceType) {
		return RegisteredService{}, ErrInvalidServiceAssignment
	}
	if _, err := (MariaDBStreamStore{db: s.db}).GetStream(ctx, mutation.StreamID); err != nil {
		return RegisteredService{}, err
	}
	discoveredServiceRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	discoveredTargetPrimary := []mariaDBAssignmentRow(nil)
	if mutation.AssignmentRole == "primary" {
		discoveredTargetPrimary, err = discoverMariaDBTargetPrimaryAssignments(ctx, s.db, mutation.StreamID, discoveredService.ServiceType)
		if err != nil {
			return RegisteredService{}, err
		}
	}
	discoveredRows := mergeMariaDBAssignmentRows(discoveredServiceRows, discoveredTargetPrimary)
	streamIDs := []string{mutation.StreamID, discoveredService.CurrentStreamID}
	serviceIDs := []string{mutation.ServiceID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
		serviceIDs = append(serviceIDs, row.ServiceID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if target, ok := lockedStreams[mutation.StreamID]; !ok || target.DeletedAt != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	service, ok := lockedServices[mutation.ServiceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return RegisteredService{}, err
	}
	revalidatedServiceRows, err := discoverMariaDBAssignmentsForService(ctx, tx, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	revalidatedTargetPrimary := []mariaDBAssignmentRow(nil)
	if mutation.AssignmentRole == "primary" {
		revalidatedTargetPrimary, err = discoverMariaDBTargetPrimaryAssignments(ctx, tx, mutation.StreamID, service.ServiceType)
		if err != nil {
			return RegisteredService{}, err
		}
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, mergeMariaDBAssignmentRows(revalidatedServiceRows, revalidatedTargetPrimary)) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	currentStreamID, currentRole, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if len(revalidatedTargetPrimary) > 1 {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == mutation.StreamID && currentRole == mutation.AssignmentRole {
		if mutation.AssignmentRole == "primary" && (len(revalidatedTargetPrimary) != 1 || revalidatedTargetPrimary[0].ServiceID != service.ServiceID) {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = currentRole
		if err := tx.Commit(); err != nil {
			return RegisteredService{}, err
		}
		return service, nil
	}
	streamStates := make(map[string]streamAssignmentProtection, 2)
	for _, streamID := range sortedUniqueStrings([]string{currentStreamID, mutation.StreamID}) {
		state, stateErr := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, streamID)
		if stateErr != nil {
			if errors.Is(stateErr, ErrNotFound) && streamID == currentStreamID {
				return RegisteredService{}, ErrServiceAssignmentConflict
			}
			return RegisteredService{}, stateErr
		}
		streamStates[streamID] = state
	}
	if currentStreamID != "" && streamStates[currentStreamID].protected() {
		return RegisteredService{}, ErrServiceAssignmentProtectedStream
	}
	if streamStates[mutation.StreamID].protected() {
		return RegisteredService{}, ErrServiceAssignmentProtectedStream
	}

	replacedServices := make([]RegisteredService, 0, 1)
	if mutation.AssignmentRole == "primary" && len(revalidatedTargetPrimary) == 1 && revalidatedTargetPrimary[0].ServiceID != service.ServiceID {
		replaced, exists := lockedServices[revalidatedTargetPrimary[0].ServiceID]
		if !exists {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
		replacedOwner, replacedRole, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, replaced)
		if consistencyErr != nil || replacedOwner != mutation.StreamID || replacedRole != "primary" {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
		replacedServices = append(replacedServices, replaced)
	}

	now := time.Now().UTC()
	deleteIDs := make([]string, 0, len(revalidatedServiceRows)+len(revalidatedTargetPrimary))
	for _, row := range revalidatedServiceRows {
		deleteIDs = append(deleteIDs, row.ID)
	}
	if mutation.AssignmentRole == "primary" {
		for _, row := range revalidatedTargetPrimary {
			deleteIDs = append(deleteIDs, row.ID)
		}
	}
	for _, assignmentID := range sortedUniqueStrings(deleteIDs) {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, assignmentID)
		if deleteErr != nil {
			return RegisteredService{}, deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
	}
	for _, replaced := range replacedServices {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ?`, now, replaced.ServiceID); err != nil {
			return RegisteredService{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_service_assignments (id, stream_id, service_id, service_type, assignment_role, assigned_by_user_id, assigned_at)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?)`, newUUID(), mutation.StreamID, service.ServiceID, service.ServiceType, mutation.AssignmentRole, mutation.ActorUserID, now); err != nil {
		return RegisteredService{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = ?, status = 'assigned', updated_at = ? WHERE service_id = ?`, mutation.StreamID, now, service.ServiceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	service.CurrentStreamID = mutation.StreamID
	service.Status = "assigned"
	service.AssignmentRole = mutation.AssignmentRole
	service.UpdatedAt = now
	return service, nil
}

func (s MariaDBAuthStore) UnassignServiceFromStreamGuarded(ctx context.Context, mutation ServiceUnassignmentMutation) (service RegisteredService, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			service = RegisteredService{}
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	discoveredService, err := s.getService(ctx, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	streamIDs := []string{discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	if _, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs); err != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{mutation.ServiceID})
	if err != nil {
		return RegisteredService{}, err
	}
	service, ok := lockedServices[mutation.ServiceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return RegisteredService{}, err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, mutation.ServiceID)
	if err != nil {
		return RegisteredService{}, err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	currentStreamID, currentRole, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == "" {
		service.AssignmentRole = currentRole
		if err := tx.Commit(); err != nil {
			return RegisteredService{}, err
		}
		return service, nil
	}
	owner, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, currentStreamID)
	if errors.Is(err, ErrNotFound) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if err != nil {
		return RegisteredService{}, err
	}
	if owner.protected() {
		return RegisteredService{}, ErrServiceUnassignProtectedStream
	}
	now := time.Now().UTC()
	for _, row := range revalidatedRows {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, row.ID)
		if deleteErr != nil {
			return RegisteredService{}, deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ?`, now, service.ServiceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	service.CurrentStreamID = ""
	if service.Status == "assigned" {
		service.Status = "registered"
	}
	service.AssignmentRole = ""
	service.UpdatedAt = now
	return service, nil
}

func (s MariaDBAuthStore) BeginStreamArchiveRetryGuarded(ctx context.Context, serviceID, streamID string) (stream Stream, err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			stream = Stream{}
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	serviceID = strings.TrimSpace(serviceID)
	streamID = strings.TrimSpace(streamID)
	discoveredService, err := s.getService(ctx, serviceID)
	if err != nil {
		return Stream{}, err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, serviceID)
	if err != nil {
		return Stream{}, err
	}
	streamIDs := []string{streamID, discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	if _, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs); err != nil {
		return Stream{}, ErrServiceAssignmentConflict
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{serviceID})
	if err != nil {
		return Stream{}, err
	}
	service, ok := lockedServices[serviceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) {
		return Stream{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return Stream{}, err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
	if err != nil {
		return Stream{}, err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return Stream{}, ErrServiceAssignmentConflict
	}
	if service.ServiceType != "encoder_recorder" {
		return Stream{}, ErrInvalidServiceAssignment
	}
	owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil {
		return Stream{}, err
	}
	if owner != streamID {
		return Stream{}, ErrServiceAssignmentConflict
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, streamID)
	if err != nil {
		return Stream{}, err
	}
	stream = state.Stream
	if !state.ArchiveRetryPending {
		if err := insertMariaDBStreamLogGuard(ctx, tx, streamID, archiveRetryAssignmentGuardLogMessage, time.Now().UTC()); err != nil {
			return Stream{}, err
		}
	}
	if stream.ArchiveReportedAt != nil {
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE streams SET archive_reported_at = NULL, updated_at = ? WHERE id = ?`, now, streamID); err != nil {
			return Stream{}, err
		}
		stream.ArchiveReportedAt = nil
		stream.UpdatedAt = now
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	return stream, nil
}

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
