package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

func rollbackMemoryQueuedSystemdPortJobLocked(
	updates *MemorySystemUpdateStore,
	registry *MemoryAuthStore,
	job SystemUpdateJob,
	now time.Time,
) error {
	target, current, pending, err := validateMemorySystemdPortPendingStateLocked(
		updates, registry, job,
	)
	if err != nil {
		return err
	}
	_ = current
	_, _, reservationChanges, err := systemUpdatePortReservationTransition(job)
	if err != nil {
		return err
	}
	if reservationChanges {
		delete(updates.portReservations, servicePortKey(pending))
	}
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	target.EndpointRevision = nextSystemUpdateRollbackEndpointRevision(target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision)
	target.EndpointStatus = "applied"
	target.UpdatedAt = now
	registry.services[target.ServiceID] = target
	return nil
}

func applyMemorySystemdPortTerminalStateLocked(
	updates *MemorySystemUpdateStore,
	registry *MemoryAuthStore,
	job SystemUpdateJob,
	result SystemUpdatePortReconfigurationResult,
	now time.Time,
) error {
	target, current, pending, err := validateMemorySystemdPortPendingStateLocked(
		updates, registry, job,
	)
	if err != nil {
		return err
	}
	_, _, reservationChanges, err := systemUpdatePortReservationTransition(job)
	if err != nil {
		return err
	}
	switch result {
	case SystemUpdatePortReconfigurationApplied:
		if reservationChanges {
			delete(updates.portReservations, servicePortKey(current))
			pending.ServiceRole = systemUpdatePortCurrentRole
			pending.UpdatedAt = now
			updates.portReservations[servicePortKey(pending)] = pending
		}
		target.AppliedEndpoint = copyServiceEndpoint(target.DesiredEndpoint)
		target.Host = target.AppliedEndpoint.Host
		target.Port = target.AppliedEndpoint.Port
		target.SSLEnabled = target.AppliedEndpoint.SSLEnabled
		target.PublicURL = target.AppliedEndpoint.PublicURL
		target.AppliedConfigRevision = job.PortReconfigure.TargetConfigRevision
		target.AppliedConfigSHA256 = job.PortReconfigure.TargetConfigSHA256
		target.EndpointStatus = "applied"
	case SystemUpdatePortReconfigurationUnchanged:
		// "unchanged" proves that the mutation never displaced the verified
		// previous listener. Release the pending reservation and advance the
		// endpoint generation without promoting the requested port.
		if reservationChanges {
			delete(updates.portReservations, servicePortKey(pending))
		}
		target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
		target.EndpointRevision = nextSystemUpdateRollbackEndpointRevision(
			target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision,
		)
		target.EndpointStatus = "applied"
	case SystemUpdatePortReconfigurationRolledBack:
		if reservationChanges {
			delete(updates.portReservations, servicePortKey(pending))
		}
		target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
		target.EndpointRevision = nextSystemUpdateRollbackEndpointRevision(
			target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision,
		)
		target.EndpointStatus = "rolled_back"
	case SystemUpdatePortReconfigurationRollbackFailed:
		// Keep both the current and pending reservations. The state is
		// intentionally fail-closed until an explicit reconciliation proves
		// which listener owns the new port.
		target.EndpointStatus = "rollback_failed"
	default:
		return ErrInvalidSystemUpdate
	}
	target.UpdatedAt = now
	registry.services[target.ServiceID] = target
	return nil
}

func validateMemorySystemdPortPendingStateLocked(
	updates *MemorySystemUpdateStore,
	registry *MemoryAuthStore,
	job SystemUpdateJob,
) (RegisteredService, ServicePortReservation, ServicePortReservation, error) {
	target, exists := registry.services[job.TargetID]
	if !exists {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrNotFound
	}
	if err := validateSystemdPortPendingService(job, target); err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	oldReservationPort, newReservationPort, reservationChanges, err :=
		systemUpdatePortReservationTransition(job)
	if err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	currentKey := servicePortReservationKey{
		executionHostID: job.ExecutionHostID, networkNamespace: systemUpdatePortNetworkNamespace,
		protocol: systemUpdatePortProtocol, port: oldReservationPort,
	}
	current, exists := updates.portReservations[currentKey]
	if !exists || current.ServiceID != target.ServiceID ||
		current.ServiceRole != systemUpdatePortCurrentRole {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	if !reservationChanges {
		return target, current, ServicePortReservation{}, nil
	}
	pendingKey := servicePortReservationKey{
		executionHostID: job.ExecutionHostID, networkNamespace: systemUpdatePortNetworkNamespace,
		protocol: systemUpdatePortProtocol, port: newReservationPort,
	}
	pending, exists := updates.portReservations[pendingKey]
	if !exists || pending.ServiceID != target.ServiceID ||
		pending.ServiceRole != systemUpdatePortPendingRole {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	return target, current, pending, nil
}

func rollbackMariaDBQueuedSystemdPortJob(
	ctx context.Context,
	tx *sql.Tx,
	job SystemUpdateJob,
	now time.Time,
) error {
	target, current, pending, err := lockMariaDBSystemdPortPendingState(ctx, tx, job)
	if err != nil {
		return err
	}
	_ = current
	_, _, reservationChanges, err := systemUpdatePortReservationTransition(job)
	if err != nil {
		return err
	}
	if reservationChanges {
		if err := deleteMariaDBPortReservation(ctx, tx, pending); err != nil {
			return err
		}
	}
	nextRevision := nextSystemUpdateRollbackEndpointRevision(
		target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision,
	)
	result, err := tx.ExecContext(ctx, `UPDATE services
SET desired_host = host,
    desired_port = port,
    desired_ssl_enabled = ssl_enabled,
    desired_public_url = public_url,
    endpoint_revision = ?,
    endpoint_status = 'applied',
    updated_at = ?
WHERE service_id = ? AND endpoint_revision = ?`,
		nextRevision, now, target.ServiceID, target.EndpointRevision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSystemUpdateEndpointStale
	}
	return nil
}

func applyMariaDBSystemdPortTerminalState(
	ctx context.Context,
	tx *sql.Tx,
	job SystemUpdateJob,
	portResult SystemUpdatePortReconfigurationResult,
	now time.Time,
) error {
	target, current, pending, err := lockMariaDBSystemdPortPendingState(ctx, tx, job)
	if err != nil {
		return err
	}
	_, _, reservationChanges, err := systemUpdatePortReservationTransition(job)
	if err != nil {
		return err
	}
	var result sql.Result
	switch portResult {
	case SystemUpdatePortReconfigurationApplied:
		if reservationChanges {
			if err := deleteMariaDBPortReservation(ctx, tx, current); err != nil {
				return err
			}
			result, err = tx.ExecContext(ctx, `UPDATE service_port_reservations
SET service_role = ?, updated_at = ?
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ?
  AND port = ? AND service_id = ? AND service_role = ?`,
				systemUpdatePortCurrentRole, now,
				pending.ExecutionHostID, pending.NetworkNamespace, pending.Protocol,
				pending.Port, pending.ServiceID, systemUpdatePortPendingRole,
			)
			if err != nil {
				return err
			}
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				return affectedErr
			} else if affected != 1 {
				return ErrSystemUpdateEndpointStale
			}
		}
		result, err = tx.ExecContext(ctx, `UPDATE services
SET host = desired_host,
    port = desired_port,
    ssl_enabled = desired_ssl_enabled,
    public_url = desired_public_url,
    applied_config_revision = ?,
    applied_config_sha256 = ?,
    endpoint_status = 'applied',
    updated_at = ?
WHERE service_id = ? AND endpoint_revision = ?`,
			job.PortReconfigure.TargetConfigRevision,
			job.PortReconfigure.TargetConfigSHA256,
			now, target.ServiceID, target.EndpointRevision,
		)
	case SystemUpdatePortReconfigurationUnchanged:
		if reservationChanges {
			if err := deleteMariaDBPortReservation(ctx, tx, pending); err != nil {
				return err
			}
		}
		nextRevision := nextSystemUpdateRollbackEndpointRevision(
			target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision,
		)
		result, err = tx.ExecContext(ctx, `UPDATE services
SET desired_host = host,
    desired_port = port,
    desired_ssl_enabled = ssl_enabled,
    desired_public_url = public_url,
    endpoint_revision = ?,
    endpoint_status = 'applied',
    updated_at = ?
WHERE service_id = ? AND endpoint_revision = ?`,
			nextRevision, now, target.ServiceID, target.EndpointRevision,
		)
	case SystemUpdatePortReconfigurationRolledBack:
		if reservationChanges {
			if err := deleteMariaDBPortReservation(ctx, tx, pending); err != nil {
				return err
			}
		}
		nextRevision := nextSystemUpdateRollbackEndpointRevision(
			target.EndpointRevision, job.PortReconfigure.TargetEndpointRevision,
		)
		result, err = tx.ExecContext(ctx, `UPDATE services
SET desired_host = host,
    desired_port = port,
    desired_ssl_enabled = ssl_enabled,
    desired_public_url = public_url,
    endpoint_revision = ?,
    endpoint_status = 'rolled_back',
    updated_at = ?
WHERE service_id = ? AND endpoint_revision = ?`,
			nextRevision, now, target.ServiceID, target.EndpointRevision,
		)
	case SystemUpdatePortReconfigurationRollbackFailed:
		result, err = tx.ExecContext(ctx, `UPDATE services
SET endpoint_status = 'rollback_failed', updated_at = ?
WHERE service_id = ? AND endpoint_revision = ?`,
			now, target.ServiceID, target.EndpointRevision,
		)
	default:
		return ErrInvalidSystemUpdate
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSystemUpdateEndpointStale
	}
	return nil
}

func lockMariaDBSystemdPortPendingState(
	ctx context.Context,
	tx *sql.Tx,
	job SystemUpdateJob,
) (RegisteredService, ServicePortReservation, ServicePortReservation, error) {
	target, err := scanService(tx.QueryRowContext(
		ctx, serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`, job.TargetID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrNotFound
	}
	if err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	if err := validateSystemdPortPendingService(job, target); err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	oldReservationPort, newReservationPort, reservationChanges, err :=
		systemUpdatePortReservationTransition(job)
	if err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	current, err := scanServicePortReservation(tx.QueryRowContext(
		ctx, servicePortReservationSelect+`
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
FOR UPDATE`,
		job.ExecutionHostID, systemUpdatePortNetworkNamespace, systemUpdatePortProtocol,
		oldReservationPort,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	if err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	if current.ServiceID != target.ServiceID || current.ServiceRole != systemUpdatePortCurrentRole {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	if !reservationChanges {
		return target, current, ServicePortReservation{}, nil
	}
	pending, err := scanServicePortReservation(tx.QueryRowContext(
		ctx, servicePortReservationSelect+`
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
FOR UPDATE`,
		job.ExecutionHostID, systemUpdatePortNetworkNamespace, systemUpdatePortProtocol,
		newReservationPort,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	if err != nil {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, err
	}
	if pending.ServiceID != target.ServiceID || pending.ServiceRole != systemUpdatePortPendingRole {
		return RegisteredService{}, ServicePortReservation{}, ServicePortReservation{}, ErrSystemUpdateEndpointStale
	}
	return target, current, pending, nil
}

func systemUpdatePortReservationTransition(
	job SystemUpdateJob,
) (oldPort, newPort int, changes bool, err error) {
	if job.PortReconfigure == nil {
		return 0, 0, false, ErrInvalidSystemUpdate
	}
	oldPort = job.PortReconfigure.OldPort
	newPort = job.PortReconfigure.NewPort
	switch job.DeploymentMode {
	case "systemd":
		if job.PortReconfigure.Docker != nil {
			return 0, 0, false, ErrInvalidSystemUpdate
		}
	case "docker":
		if job.PortReconfigure.Docker == nil {
			return 0, 0, false, ErrInvalidSystemUpdate
		}
		oldPort = job.PortReconfigure.Docker.OldPublishedPort
		newPort = job.PortReconfigure.Docker.NewPublishedPort
	default:
		return 0, 0, false, ErrInvalidSystemUpdate
	}
	if oldPort < 1024 || oldPort > 65535 || newPort < 1024 || newPort > 65535 {
		return 0, 0, false, ErrInvalidSystemUpdate
	}
	return oldPort, newPort, oldPort != newPort, nil
}

func validateSystemdPortPendingService(job SystemUpdateJob, target RegisteredService) error {
	if job.Operation != SystemUpdateOperationPortReconfigure ||
		job.PortReconfigure == nil ||
		target.ServiceID != job.TargetID ||
		target.ServiceType != job.TargetServiceType ||
		target.EndpointRevision != job.PortReconfigure.TargetEndpointRevision ||
		target.EndpointStatus != "pending" ||
		target.AppliedEndpoint == nil ||
		target.AppliedEndpoint.Port != job.PortReconfigure.OldPort ||
		target.AppliedConfigRevision != job.PortReconfigure.ExpectedConfigRevision ||
		target.AppliedConfigSHA256 != job.PortReconfigure.ExpectedConfigSHA256 {
		return ErrSystemUpdateEndpointStale
	}
	expectedDesired, err := systemUpdatePortDesiredEndpoint(job, target.AppliedEndpoint)
	if err != nil || !sameServiceEndpoint(target.DesiredEndpoint, expectedDesired) {
		return ErrSystemUpdateEndpointStale
	}
	return nil
}

func deleteMariaDBPortReservation(
	ctx context.Context,
	tx *sql.Tx,
	reservation ServicePortReservation,
) error {
	result, err := tx.ExecContext(ctx, `DELETE FROM service_port_reservations
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ?
  AND port = ? AND service_id = ? AND service_role = ?`,
		reservation.ExecutionHostID, reservation.NetworkNamespace,
		reservation.Protocol, reservation.Port, reservation.ServiceID,
		reservation.ServiceRole,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSystemUpdateEndpointStale
	}
	return nil
}

func nextSystemUpdateRollbackEndpointRevision(current, target int64) int64 {
	next := current
	if target > next {
		next = target
	}
	if next >= math.MaxInt64 {
		// Creation excludes this boundary; this fallback keeps the function
		// total if persisted state was externally corrupted.
		return math.MaxInt64
	}
	return next + 1
}
