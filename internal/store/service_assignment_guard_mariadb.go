package store

import (
	"context"
	"errors"
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
