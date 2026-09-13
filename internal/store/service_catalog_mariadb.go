package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s MariaDBAuthStore) ListServices(ctx context.Context) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, serviceSelectColumnsAliased+`, COALESCE(a.assignment_role, '')
FROM services s
LEFT JOIN stream_service_assignments a ON a.service_id = s.service_id AND a.stream_id = s.current_stream_id
ORDER BY s.service_type, s.service_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RegisteredService
	for rows.Next() {
		service, err := scanServiceWithExtraRole(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s MariaDBAuthStore) ListWorkers(ctx context.Context) ([]RegisteredService, error) {
	rows, err := s.db.QueryContext(ctx, serviceSelectColumnsAliased+`, COALESCE(a.assignment_role, '')
FROM services s
LEFT JOIN stream_service_assignments a ON a.service_id = s.service_id AND a.stream_id = s.current_stream_id
WHERE s.service_type = 'worker'
ORDER BY s.service_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var services []RegisteredService
	for rows.Next() {
		service, err := scanServiceWithExtraRole(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s MariaDBAuthStore) GetService(ctx context.Context, id string) (RegisteredService, error) {
	return s.getService(ctx, id)
}

func (s MariaDBAuthStore) UpdateServiceMetadata(ctx context.Context, serviceID string, update ServiceMetadataUpdate) (RegisteredService, error) {
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(serviceID) == "" {
		return RegisteredService{}, ErrNotFound
	}
	if err := validateServiceMetadataUpdate(update); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	if update.PreserveEndpoint {
		result, err := s.db.ExecContext(ctx, `UPDATE services SET service_name = ?, description = ?, updated_at = ? WHERE service_id = ?`,
			update.ServiceName, update.Description, now, serviceID)
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
	if update.Endpointless {
		existing, err := s.getService(ctx, serviceID)
		if err != nil {
			return RegisteredService{}, err
		}
		if existing.ServiceType != "update_agent" || existing.TransportMode != "pull_v2" {
			return RegisteredService{}, ErrInvalidServiceRegistration
		}
		result, err := s.db.ExecContext(ctx, `UPDATE services SET service_name = ?, description = ?, updated_at = ? WHERE service_id = ? AND service_type = 'update_agent' AND transport_mode = 'pull_v2'`,
			update.ServiceName, update.Description, now, serviceID)
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
	result, err := s.db.ExecContext(ctx, `UPDATE services SET
service_name = ?,
description = ?,
host = ?,
port = ?,
ssl_enabled = ?,
public_url = ?,
desired_host = ?,
desired_port = ?,
desired_ssl_enabled = ?,
desired_public_url = ?,
endpoint_revision = endpoint_revision + 1,
endpoint_status = 'applied',
updated_at = ?
WHERE service_id = ?`,
		update.ServiceName, update.Description,
		update.Host, update.Port, update.SSLEnabled, update.PublicURL,
		update.Host, update.Port, update.SSLEnabled, update.PublicURL,
		now, serviceID,
	)
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

func (s MariaDBAuthStore) DeleteService(ctx context.Context, serviceID string) (err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	serviceID = strings.TrimSpace(serviceID)
	discoveredService, err := s.getService(ctx, serviceID)
	if err != nil {
		return err
	}
	discoveredTokenReferences, err := discoverMariaDBServiceTokenReferences(
		ctx, s.db, []string{discoveredService.TokenID},
	)
	if err != nil {
		return err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, serviceID)
	if err != nil {
		return err
	}
	streamIDs := []string{discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs); err != nil {
		return ErrServiceAssignmentConflict
	}
	lockedServices, err := lockMariaDBServicesSorted(
		ctx,
		tx,
		mariaDBServiceTokenReferenceServiceIDs(discoveredTokenReferences, serviceID),
	)
	if errors.Is(err, ErrNotFound) {
		return ErrServiceAssignmentConflict
	}
	if err != nil {
		return err
	}
	service, ok := lockedServices[serviceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) || service.TokenID != discoveredService.TokenID {
		return ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
	if err != nil {
		return err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return ErrServiceAssignmentConflict
	}
	owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil {
		return err
	}
	if owner != "" {
		state, lockErr := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, owner)
		if errors.Is(lockErr, ErrNotFound) {
			return ErrServiceAssignmentConflict
		}
		if lockErr != nil {
			return lockErr
		}
		if state.protected() {
			return ErrServiceUnassignProtectedStream
		}
	}
	lockedTokens, err := lockMariaDBServiceTokensSorted(ctx, tx, []string{service.TokenID})
	if err != nil {
		return err
	}
	if _, ok := lockedTokens[service.TokenID]; !ok {
		return ErrServiceAssignmentConflict
	}
	revalidatedTokenReferences, err := discoverMariaDBServiceTokenReferences(
		ctx, tx, []string{service.TokenID},
	)
	if err != nil {
		return err
	}
	if !mariaDBServiceTokenReferencesEqual(discoveredTokenReferences, revalidatedTokenReferences) ||
		!mariaDBServiceTokenReferenceTypesMatch(
			discoveredTokenReferences, lockedServices, lockedTokens,
		) {
		return ErrServiceAssignmentConflict
	}
	for _, reference := range revalidatedTokenReferences {
		if reference.ServiceID != serviceID {
			return ErrServiceAssignmentConflict
		}
	}
	now := time.Now().UTC()
	for _, row := range revalidatedRows {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, row.ID)
		if deleteErr != nil {
			return deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrServiceAssignmentConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_stream_events WHERE service_id = ?`, serviceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM services WHERE service_id = ?`, serviceID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, now, service.TokenID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s MariaDBAuthStore) getService(ctx context.Context, id string) (RegisteredService, error) {
	row := s.db.QueryRowContext(ctx, serviceSelectColumns+` FROM services WHERE service_id = ?`, id)
	service, err := scanService(row)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrNotFound
	}
	return service, err
}
