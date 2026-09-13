package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func (s MariaDBAuthStore) PrecreateService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	registration = normalizeServiceRegistration(registration)
	token.ID = strings.TrimSpace(token.ID)
	token.ServiceType = strings.TrimSpace(token.ServiceType)
	if registration.ServiceType != token.ServiceType || token.ID == "" {
		return RegisteredService{}, ErrForbidden
	}
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(registration.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{token.ID})
	if err != nil {
		return RegisteredService{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()

	observeMariaDBServiceTokenLockPhase(ctx, "precreate_service", mariaDBServiceTokenBeforeServiceLocks)
	lockedServices, err := lockMariaDBPrecreateServiceAuthority(
		ctx, tx, registration.ServiceID, discovered,
	)
	if err != nil {
		return RegisteredService{}, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, "precreate_service", mariaDBServiceTokenServiceLocksHeld)
	result, err := tx.ExecContext(ctx, `INSERT INTO services (
service_id, service_type, service_name, description,
transport_mode, execution_host_id, ownership_epoch,
host, port, ssl_enabled, public_url,
desired_host, desired_port, desired_ssl_enabled, desired_public_url,
version, reported_version, status, capabilities, reported_capabilities, metrics,
token_id, node_token_rotated_at, created_at, updated_at
) VALUES (
?, ?, ?, ?,
?, ?, ?,
?, ?, ?, ?,
?, ?, ?, ?,
?, '', 'pending', ?, ?, ?,
?, ?, ?, ?
)`,
		registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.Description,
		registration.TransportMode, registration.ExecutionHostID, registration.OwnershipEpoch,
		registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL,
		registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL,
		registration.Version, string(capabilities), string(capabilities), "{}",
		token.ID, token.CreatedAt, now, now,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return RegisteredService{}, ErrAlreadyExists
		}
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrAlreadyExists
	}

	observeMariaDBServiceTokenLockPhase(ctx, "precreate_service", mariaDBServiceTokenBeforeTokenLocks)
	lockedTokens, err := lockMariaDBServiceTokensSorted(ctx, tx, []string{token.ID})
	if err != nil {
		return RegisteredService{}, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, "precreate_service", mariaDBServiceTokenTokenLocksHeld)
	lockedToken, ok := lockedTokens[token.ID]
	if !ok || !mariaDBServiceTokenSnapshotMatches(token, lockedToken) ||
		lockedToken.ServiceType != registration.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	inserted, err := scanService(tx.QueryRowContext(
		ctx,
		serviceSelectColumns+` FROM services WHERE service_id = ?`,
		registration.ServiceID,
	))
	if err != nil {
		return RegisteredService{}, err
	}
	lockedServices[inserted.ServiceID] = inserted
	expectedReferences := mariaDBServiceTokenReferencesWithCurrentBinding(
		discovered, registration.ServiceID, token.ID,
	)
	revalidated, err := discoverMariaDBServiceTokenReferences(ctx, tx, []string{token.ID})
	if err != nil {
		return RegisteredService{}, err
	}
	if !mariaDBServiceTokenReferencesEqual(expectedReferences, revalidated) ||
		!mariaDBServiceTokenReferenceTypesMatch(revalidated, lockedServices, lockedTokens) {
		return RegisteredService{}, ErrForbidden
	}
	observeMariaDBServiceTokenLockPhase(ctx, "precreate_service", mariaDBServiceTokenBindingsValidated)
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, registration.ServiceID)
}

func (s MariaDBAuthStore) RegisterService(ctx context.Context, token ServiceToken, registration ServiceRegistration) (RegisteredService, error) {
	if registration.ServiceType != token.ServiceType {
		return RegisteredService{}, ErrForbidden
	}
	registration = normalizeServiceRegistration(registration)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	var existingTokenID, existingType, existingStatus, existingTransportMode, existingExecutionHostID string
	var existingOwnershipEpoch int64
	err = tx.QueryRowContext(ctx, `SELECT token_id, service_type, status, COALESCE(transport_mode, ''), COALESCE(execution_host_id, ''), ownership_epoch FROM services WHERE service_id = ? FOR UPDATE`, registration.ServiceID).Scan(
		&existingTokenID,
		&existingType,
		&existingStatus,
		&existingTransportMode,
		&existingExecutionHostID,
		&existingOwnershipEpoch,
	)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrForbidden
	}
	if err != nil {
		return RegisteredService{}, err
	}
	if existingType != registration.ServiceType || existingTokenID != token.ID {
		return RegisteredService{}, ErrForbidden
	}
	registration = bindPrecreatedUpdateAgentRegistration(
		registration,
		existingTransportMode,
		existingExecutionHostID,
		existingOwnershipEpoch,
	)
	if err := validateServiceRegistration(registration); err != nil {
		return RegisteredService{}, err
	}
	now := time.Now().UTC()
	capabilities, err := json.Marshal(sanitizeServiceCapabilities(registration.Capabilities))
	if err != nil {
		return RegisteredService{}, err
	}
	preserveConfiguredCapabilities := existingType == "update_agent" && existingStatus != "pending"
	_, err = tx.ExecContext(ctx, `INSERT INTO services (
service_id, service_type, service_name, description,
transport_mode, execution_host_id, ownership_epoch,
host, port, ssl_enabled, public_url,
desired_host, desired_port, desired_ssl_enabled, desired_public_url,
reported_api_host, reported_api_port, reported_api_ssl_enabled, reported_api_public_url,
version, reported_version, reported_commit, reported_build_date, status,
capabilities, reported_capabilities, reported_hostname, reported_os, reported_arch, last_reported_at,
metrics, token_id, created_at, updated_at
) VALUES (
?, ?, ?, ?,
?, ?, ?,
?, ?, ?, ?,
?, ?, ?, ?,
?, ?, ?, ?,
?, ?, ?, ?, 'registered',
?, ?, ?, ?, ?, ?,
?, ?, ?, ?
)
ON DUPLICATE KEY UPDATE
service_type = VALUES(service_type),
service_name = VALUES(service_name),
description = VALUES(description),
host = CASE WHEN status = 'pending' THEN VALUES(host) ELSE host END,
port = CASE WHEN status = 'pending' THEN VALUES(port) ELSE port END,
ssl_enabled = CASE WHEN status = 'pending' THEN VALUES(ssl_enabled) ELSE ssl_enabled END,
public_url = CASE WHEN status = 'pending' THEN VALUES(public_url) ELSE public_url END,
desired_host = CASE WHEN status = 'pending' THEN VALUES(desired_host) ELSE desired_host END,
desired_port = CASE WHEN status = 'pending' THEN VALUES(desired_port) ELSE desired_port END,
desired_ssl_enabled = CASE WHEN status = 'pending' THEN VALUES(desired_ssl_enabled) ELSE desired_ssl_enabled END,
desired_public_url = CASE WHEN status = 'pending' THEN VALUES(desired_public_url) ELSE desired_public_url END,
reported_api_host = VALUES(reported_api_host),
reported_api_port = VALUES(reported_api_port),
reported_api_ssl_enabled = VALUES(reported_api_ssl_enabled),
reported_api_public_url = VALUES(reported_api_public_url),
version = VALUES(version),
reported_version = VALUES(reported_version),
reported_commit = VALUES(reported_commit),
reported_build_date = VALUES(reported_build_date),
status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END,
capabilities = CASE WHEN ? THEN capabilities ELSE VALUES(capabilities) END,
reported_capabilities = VALUES(reported_capabilities),
reported_hostname = VALUES(reported_hostname),
reported_os = VALUES(reported_os),
reported_arch = VALUES(reported_arch),
last_reported_at = VALUES(last_reported_at),
token_id = VALUES(token_id),
updated_at = VALUES(updated_at)`,
		registration.ServiceID, registration.ServiceType, registration.ServiceName, registration.Description,
		registration.TransportMode, registration.ExecutionHostID, registration.OwnershipEpoch,
		registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL,
		registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL,
		registration.Host, registration.Port, registration.SSLEnabled, registration.PublicURL,
		registration.Version, registration.Version, registration.Commit, registration.BuildDate,
		string(capabilities), string(capabilities), registration.Hostname, registration.OS, registration.Arch, now,
		"{}", token.ID, now, now, preserveConfiguredCapabilities,
	)
	if err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, registration.ServiceID)
}
