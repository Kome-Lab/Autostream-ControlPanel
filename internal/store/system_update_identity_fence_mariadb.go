package store

import (
	"context"
	"strings"
)

func (s *MariaDBSystemUpdateStore) HasActiveSystemUpdateReference(ctx context.Context, serviceID string) (bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrInvalidSystemUpdate
	}
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM system_update_jobs
  WHERE active_target_id IS NOT NULL AND (target_id = ? OR agent_service_id = ?)
)`, serviceID, serviceID).Scan(&active)
	return active, err
}

func (s *MariaDBSystemUpdateStore) HasSystemUpdateIdentityMutationFence(
	ctx context.Context,
	services ServiceRegistryStore,
	serviceID string,
) (bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrInvalidSystemUpdate
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	var blocked bool
	err := s.db.QueryRowContext(ctx, `SELECT (
  EXISTS(
    SELECT 1
    FROM system_update_runtime_token_rotations rotation
    JOIN services service ON service.service_id = rotation.service_id
    WHERE rotation.service_id = ?
      AND (
        rotation.active_execution_host_id IS NOT NULL
        OR (
          rotation.status = 'activated'
          AND service.token_id = rotation.staged_token_id
          AND (
            service.node_token_rotated_at IS NULL
            OR service.last_heartbeat_at IS NULL
            OR service.last_heartbeat_at <= service.node_token_rotated_at
          )
        )
      )
  )
  OR EXISTS(
    SELECT 1
    FROM system_update_host_self_updates self_update
    WHERE self_update.agent_service_id = ?
      AND self_update.active_execution_host_id IS NOT NULL
  )
)`, serviceID, serviceID).Scan(&blocked)
	return blocked, err
}

func (s *MariaDBSystemUpdateStore) IsSystemUpdateEmergencyIdentityRecovery(
	ctx context.Context,
	services ServiceRegistryStore,
	serviceID string,
) (bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrInvalidSystemUpdate
	}
	registryDB, ok := mariaDBFromServiceRegistryStore(services)
	if !ok || registryDB != s.db {
		return false, ErrSystemUpdateRuntimeTokenRotationStoreMismatch
	}
	var recovery bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1
  FROM system_update_runtime_token_rotations rotation
  JOIN services service ON service.service_id = rotation.service_id
  JOIN service_tokens current_token ON current_token.id = service.token_id
  WHERE rotation.service_id = ?
    AND rotation.status = 'canceled'
    AND rotation.emergency_revoked_token_id IS NOT NULL
    AND rotation.emergency_revoked_at IS NOT NULL
    AND service.token_id IN (rotation.previous_token_id, rotation.staged_token_id)
    AND service.status = 'offline'
    AND service.last_heartbeat_at IS NULL
    AND COALESCE(JSON_LENGTH(service.reported_capabilities), 0) = 0
    AND service.node_token_ciphertext IS NULL
    AND service.node_token_nonce IS NULL
    AND current_token.revoked_at IS NOT NULL
)`, serviceID).Scan(&recovery)
	return recovery, err
}
