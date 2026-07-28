package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SystemUpdateTransportSSHV1  = "ssh_v1"
	SystemUpdateTransportPullV2 = "pull_v2"
)

var (
	ErrInvalidSystemUpdateExecutionHost   = errors.New("invalid system update execution host")
	ErrSystemUpdateExecutionHostStale     = errors.New("system update execution host ownership epoch is stale")
	ErrSystemUpdateExecutionHostBusy      = errors.New("system update execution host has a nonterminal job")
	ErrSystemUpdateAgentInactive          = errors.New("system update agent service is inactive")
	ErrSystemUpdateAgentNotReady          = errors.New("system update agent service is not ready for activation")
	ErrSystemUpdateAgentBindingMismatch   = errors.New("system update agent service binding does not match the execution host transition")
	ErrSystemUpdateExecutionStoreMismatch = errors.New("system update execution host store does not share updater policy persistence")
	ErrInvalidServicePortReservation      = errors.New("invalid service port reservation")
	ErrServicePortReserved                = errors.New("service port is already reserved")

	servicePortNamespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
	servicePortRolePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)
)

type SystemUpdateExecutionHost struct {
	ExecutionHostID      string    `json:"execution_host_id"`
	TransportMode        string    `json:"transport_mode"`
	AgentServiceID       string    `json:"agent_service_id,omitempty"`
	LegacyAgentServiceID string    `json:"legacy_agent_service_id,omitempty"`
	OwnershipEpoch       int64     `json:"ownership_epoch"`
	PolicyRevision       int64     `json:"policy_revision"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

type SystemUpdateExecutionHostStore interface {
	GetSystemUpdateExecutionHost(ctx context.Context, executionHostID string) (SystemUpdateExecutionHost, error)
	SwitchSystemUpdateExecutionHost(ctx context.Context, executionHostID string, expectedEpoch int64, nextTransportMode, nextAgentServiceID string, policyRevision int64) (SystemUpdateExecutionHost, error)
}

type ServicePortReservation struct {
	ExecutionHostID  string    `json:"execution_host_id"`
	NetworkNamespace string    `json:"network_namespace"`
	Protocol         string    `json:"protocol"`
	Port             int       `json:"port"`
	ServiceID        string    `json:"service_id"`
	ServiceRole      string    `json:"service_role"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type ServicePortReservationStore interface {
	ReserveServicePort(ctx context.Context, reservation ServicePortReservation) (stored ServicePortReservation, created bool, err error)
	ListServicePortReservations(ctx context.Context, executionHostID string) ([]ServicePortReservation, error)
	ReleaseServicePort(ctx context.Context, reservation ServicePortReservation) error
}

type servicePortReservationKey struct {
	executionHostID  string
	networkNamespace string
	protocol         string
	port             int
}

func syntheticSystemUpdateExecutionHost(executionHostID string) SystemUpdateExecutionHost {
	return SystemUpdateExecutionHost{
		ExecutionHostID: executionHostID,
		TransportMode:   SystemUpdateTransportSSHV1,
		OwnershipEpoch:  0,
		PolicyRevision:  0,
	}
}

func normalizeSystemUpdateExecutionHostSwitch(executionHostID, transportMode, agentServiceID string) (string, string, string) {
	return strings.TrimSpace(executionHostID),
		strings.ToLower(strings.TrimSpace(transportMode)),
		strings.TrimSpace(agentServiceID)
}

func validateSystemUpdateExecutionHostSwitch(executionHostID string, expectedEpoch int64, transportMode, agentServiceID string, policyRevision int64) error {
	if !executionHostIDPattern.MatchString(executionHostID) ||
		expectedEpoch < 0 ||
		(transportMode != SystemUpdateTransportSSHV1 && transportMode != SystemUpdateTransportPullV2) ||
		!serviceIDPattern.MatchString(agentServiceID) ||
		policyRevision < 0 {
		return ErrInvalidSystemUpdateExecutionHost
	}
	return nil
}

func (s *MemorySystemUpdateStore) GetSystemUpdateExecutionHost(ctx context.Context, executionHostID string) (SystemUpdateExecutionHost, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateExecutionHost{}, ErrInvalidSystemUpdateExecutionHost
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	host, ok := s.executionHosts[executionHostID]
	if !ok {
		return syntheticSystemUpdateExecutionHost(executionHostID), nil
	}
	return host, nil
}

func (s *MemorySystemUpdateStore) SwitchSystemUpdateExecutionHost(
	ctx context.Context,
	executionHostID string,
	expectedEpoch int64,
	nextTransportMode, nextAgentServiceID string,
	policyRevision int64,
) (SystemUpdateExecutionHost, error) {
	if err := ctx.Err(); err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	executionHostID, nextTransportMode, nextAgentServiceID = normalizeSystemUpdateExecutionHostSwitch(executionHostID, nextTransportMode, nextAgentServiceID)
	if err := validateSystemUpdateExecutionHostSwitch(executionHostID, expectedEpoch, nextTransportMode, nextAgentServiceID, policyRevision); err != nil {
		return SystemUpdateExecutionHost{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.executionHosts[executionHostID]
	if !ok {
		current = syntheticSystemUpdateExecutionHost(executionHostID)
	}
	if current.OwnershipEpoch != expectedEpoch {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostStale
	}
	for _, job := range s.jobs {
		if job.ExecutionHostID == executionHostID && !isTerminalSystemUpdateStatus(job.Status) {
			return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostBusy
		}
	}
	if _, found := activeMemorySystemUpdateRuntimeTokenRotationForHostLocked(s, executionHostID); found {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}

	now := time.Now().UTC()
	legacyAgentServiceID := nextSystemUpdateLegacyAgentServiceID(
		current,
		nextTransportMode,
		nextAgentServiceID,
	)
	next := SystemUpdateExecutionHost{
		ExecutionHostID:      executionHostID,
		TransportMode:        nextTransportMode,
		AgentServiceID:       nextAgentServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       current.OwnershipEpoch + 1,
		PolicyRevision:       policyRevision,
		CreatedAt:            current.CreatedAt,
		UpdatedAt:            now,
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = now
	}
	if s.executionHosts == nil {
		s.executionHosts = map[string]SystemUpdateExecutionHost{}
	}
	s.executionHosts[executionHostID] = next
	return next, nil
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdateExecutionHost(ctx context.Context, executionHostID string) (SystemUpdateExecutionHost, error) {
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateExecutionHost{}, ErrInvalidSystemUpdateExecutionHost
	}
	host, err := scanSystemUpdateExecutionHost(s.db.QueryRowContext(ctx, systemUpdateExecutionHostSelect+` WHERE execution_host_id = ?`, executionHostID))
	if errors.Is(err, sql.ErrNoRows) {
		return syntheticSystemUpdateExecutionHost(executionHostID), nil
	}
	return host, err
}

func (s *MariaDBSystemUpdateStore) SwitchSystemUpdateExecutionHost(
	ctx context.Context,
	executionHostID string,
	expectedEpoch int64,
	nextTransportMode, nextAgentServiceID string,
	policyRevision int64,
) (SystemUpdateExecutionHost, error) {
	executionHostID, nextTransportMode, nextAgentServiceID = normalizeSystemUpdateExecutionHostSwitch(executionHostID, nextTransportMode, nextAgentServiceID)
	if err := validateSystemUpdateExecutionHostSwitch(executionHostID, expectedEpoch, nextTransportMode, nextAgentServiceID, policyRevision); err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	defer tx.Rollback()

	current, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(ctx, systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`, executionHostID))
	missing := errors.Is(err, sql.ErrNoRows)
	if missing {
		current = syntheticSystemUpdateExecutionHost(executionHostID)
	} else if err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	if current.OwnershipEpoch != expectedEpoch {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostStale
	}

	var activeAgent int
	var registeredTransportMode, registeredExecutionHostID string
	var registeredOwnershipEpoch int64
	err = tx.QueryRowContext(ctx, `SELECT 1,
       COALESCE(s.transport_mode, ''),
       COALESCE(s.execution_host_id, ''),
       COALESCE(s.ownership_epoch, 0)
FROM services s
JOIN service_tokens st ON st.id = s.token_id
WHERE s.service_id = ?
  AND s.service_type = 'update_agent'
  AND st.service_type = 'update_agent'
  AND st.revoked_at IS NULL
LIMIT 1
FOR UPDATE`, nextAgentServiceID).Scan(
		&activeAgent,
		&registeredTransportMode,
		&registeredExecutionHostID,
		&registeredOwnershipEpoch,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateAgentInactive
	}
	if err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	registeredTransportMode = strings.ToLower(strings.TrimSpace(registeredTransportMode))
	registeredExecutionHostID = strings.TrimSpace(registeredExecutionHostID)
	switch nextTransportMode {
	case SystemUpdateTransportPullV2:
		if registeredTransportMode != SystemUpdateTransportPullV2 ||
			registeredExecutionHostID != executionHostID ||
			registeredOwnershipEpoch != expectedEpoch+1 {
			return SystemUpdateExecutionHost{}, ErrSystemUpdateAgentBindingMismatch
		}
	case SystemUpdateTransportSSHV1:
		if registeredTransportMode == SystemUpdateTransportPullV2 {
			return SystemUpdateExecutionHost{}, ErrSystemUpdateAgentBindingMismatch
		}
	}

	var activeJobID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_jobs
WHERE execution_host_id = ?
  AND status NOT IN ('succeeded','rolled_back','failed','canceled')
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE`, executionHostID).Scan(&activeJobID)
	if err == nil {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateExecutionHost{}, err
	}
	var activeRotationID string
	err = tx.QueryRowContext(ctx, `SELECT id
FROM system_update_runtime_token_rotations
WHERE active_execution_host_id = ?
LIMIT 1
FOR UPDATE`, executionHostID).Scan(&activeRotationID)
	if err == nil {
		return SystemUpdateExecutionHost{}, ErrSystemUpdateRuntimeTokenRotationBusy
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateExecutionHost{}, err
	}

	now := time.Now().UTC()
	legacyAgentServiceID := nextSystemUpdateLegacyAgentServiceID(
		current,
		nextTransportMode,
		nextAgentServiceID,
	)
	next := SystemUpdateExecutionHost{
		ExecutionHostID:      executionHostID,
		TransportMode:        nextTransportMode,
		AgentServiceID:       nextAgentServiceID,
		LegacyAgentServiceID: legacyAgentServiceID,
		OwnershipEpoch:       current.OwnershipEpoch + 1,
		PolicyRevision:       policyRevision,
		CreatedAt:            current.CreatedAt,
		UpdatedAt:            now,
	}
	if missing {
		next.CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO system_update_execution_hosts
(execution_host_id, transport_mode, agent_service_id, legacy_agent_service_id, ownership_epoch, policy_revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			next.ExecutionHostID, next.TransportMode, next.AgentServiceID,
			nullString(next.LegacyAgentServiceID), next.OwnershipEpoch,
			next.PolicyRevision, next.CreatedAt, next.UpdatedAt)
		if isDuplicateKeyError(err) {
			return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostStale
		}
	} else {
		result, updateErr := tx.ExecContext(ctx, `UPDATE system_update_execution_hosts
SET transport_mode = ?, agent_service_id = ?, legacy_agent_service_id = ?, ownership_epoch = ?, policy_revision = ?, updated_at = ?
WHERE execution_host_id = ? AND ownership_epoch = ?`,
			next.TransportMode, next.AgentServiceID, nullString(next.LegacyAgentServiceID),
			next.OwnershipEpoch, next.PolicyRevision, next.UpdatedAt,
			next.ExecutionHostID, expectedEpoch)
		err = updateErr
		if err == nil {
			affected, affectedErr := result.RowsAffected()
			if affectedErr != nil {
				return SystemUpdateExecutionHost{}, affectedErr
			}
			if affected != 1 {
				return SystemUpdateExecutionHost{}, ErrSystemUpdateExecutionHostStale
			}
		}
	}
	if err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	if err := tx.Commit(); err != nil {
		return SystemUpdateExecutionHost{}, err
	}
	return next, nil
}

const systemUpdateExecutionHostSelect = `SELECT execution_host_id, transport_mode, agent_service_id, legacy_agent_service_id, ownership_epoch, policy_revision, created_at, updated_at
FROM system_update_execution_hosts`

type executionHostScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateExecutionHost(scanner executionHostScanner) (SystemUpdateExecutionHost, error) {
	var host SystemUpdateExecutionHost
	var legacyAgentServiceID sql.NullString
	err := scanner.Scan(
		&host.ExecutionHostID,
		&host.TransportMode,
		&host.AgentServiceID,
		&legacyAgentServiceID,
		&host.OwnershipEpoch,
		&host.PolicyRevision,
		&host.CreatedAt,
		&host.UpdatedAt,
	)
	host.LegacyAgentServiceID = strings.TrimSpace(legacyAgentServiceID.String)
	return host, err
}

func nextSystemUpdateLegacyAgentServiceID(
	current SystemUpdateExecutionHost,
	nextTransportMode string,
	nextAgentServiceID string,
) string {
	if nextTransportMode == SystemUpdateTransportSSHV1 {
		return strings.TrimSpace(nextAgentServiceID)
	}
	legacyAgentServiceID := strings.TrimSpace(current.LegacyAgentServiceID)
	if legacyAgentServiceID == "" &&
		current.TransportMode == SystemUpdateTransportSSHV1 &&
		current.AgentServiceID != "" {
		legacyAgentServiceID = strings.TrimSpace(current.AgentServiceID)
	}
	return legacyAgentServiceID
}

func getSystemUpdateExecutionHostForUpdate(ctx context.Context, tx *sql.Tx, executionHostID string) (SystemUpdateExecutionHost, error) {
	host, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(
		ctx,
		systemUpdateExecutionHostSelect+` WHERE execution_host_id = ? FOR UPDATE`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return syntheticSystemUpdateExecutionHost(executionHostID), nil
	}
	return host, err
}

func normalizeServicePortReservation(reservation ServicePortReservation) ServicePortReservation {
	reservation.ExecutionHostID = strings.TrimSpace(reservation.ExecutionHostID)
	reservation.NetworkNamespace = strings.TrimSpace(reservation.NetworkNamespace)
	reservation.Protocol = strings.ToLower(strings.TrimSpace(reservation.Protocol))
	reservation.ServiceID = strings.TrimSpace(reservation.ServiceID)
	reservation.ServiceRole = strings.ToLower(strings.TrimSpace(reservation.ServiceRole))
	return reservation
}

func validateServicePortReservation(reservation ServicePortReservation) error {
	if !executionHostIDPattern.MatchString(reservation.ExecutionHostID) ||
		!servicePortNamespacePattern.MatchString(reservation.NetworkNamespace) ||
		(reservation.Protocol != "tcp" && reservation.Protocol != "udp") ||
		reservation.Port < 1024 ||
		reservation.Port > 65535 ||
		!serviceIDPattern.MatchString(reservation.ServiceID) ||
		!servicePortRolePattern.MatchString(reservation.ServiceRole) {
		return ErrInvalidServicePortReservation
	}
	return nil
}

func servicePortKey(reservation ServicePortReservation) servicePortReservationKey {
	return servicePortReservationKey{
		executionHostID:  reservation.ExecutionHostID,
		networkNamespace: reservation.NetworkNamespace,
		protocol:         reservation.Protocol,
		port:             reservation.Port,
	}
}

func sameServicePortReservationOwner(left, right ServicePortReservation) bool {
	return left.ServiceID == right.ServiceID && left.ServiceRole == right.ServiceRole
}

func (s *MemorySystemUpdateStore) ReserveServicePort(ctx context.Context, reservation ServicePortReservation) (ServicePortReservation, bool, error) {
	if err := ctx.Err(); err != nil {
		return ServicePortReservation{}, false, err
	}
	reservation = normalizeServicePortReservation(reservation)
	if err := validateServicePortReservation(reservation); err != nil {
		return ServicePortReservation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.executionHosts[reservation.ExecutionHostID]; !ok {
		return ServicePortReservation{}, false, ErrNotFound
	}
	key := servicePortKey(reservation)
	if existing, ok := s.portReservations[key]; ok {
		if sameServicePortReservationOwner(existing, reservation) {
			return existing, false, nil
		}
		return ServicePortReservation{}, false, ErrServicePortReserved
	}
	now := time.Now().UTC()
	reservation.CreatedAt = now
	reservation.UpdatedAt = now
	if s.portReservations == nil {
		s.portReservations = map[servicePortReservationKey]ServicePortReservation{}
	}
	s.portReservations[key] = reservation
	return reservation, true, nil
}

func (s *MemorySystemUpdateStore) ListServicePortReservations(ctx context.Context, executionHostID string) ([]ServicePortReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return nil, ErrInvalidServicePortReservation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reservations := make([]ServicePortReservation, 0)
	for _, reservation := range s.portReservations {
		if reservation.ExecutionHostID == executionHostID {
			reservations = append(reservations, reservation)
		}
	}
	sortServicePortReservations(reservations)
	return reservations, nil
}

func (s *MemorySystemUpdateStore) ReleaseServicePort(ctx context.Context, reservation ServicePortReservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reservation = normalizeServicePortReservation(reservation)
	if err := validateServicePortReservation(reservation); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := servicePortKey(reservation)
	existing, ok := s.portReservations[key]
	if !ok {
		return nil
	}
	if !sameServicePortReservationOwner(existing, reservation) {
		return ErrServicePortReserved
	}
	delete(s.portReservations, key)
	return nil
}

func (s *MariaDBSystemUpdateStore) ReserveServicePort(ctx context.Context, reservation ServicePortReservation) (ServicePortReservation, bool, error) {
	reservation = normalizeServicePortReservation(reservation)
	if err := validateServicePortReservation(reservation); err != nil {
		return ServicePortReservation{}, false, err
	}
	now := time.Now().UTC()
	reservation.CreatedAt = now
	reservation.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO service_port_reservations
(execution_host_id, network_namespace, protocol, port, service_id, service_role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		reservation.ExecutionHostID,
		reservation.NetworkNamespace,
		reservation.Protocol,
		reservation.Port,
		reservation.ServiceID,
		reservation.ServiceRole,
		reservation.CreatedAt,
		reservation.UpdatedAt,
	)
	if err == nil {
		return reservation, true, nil
	}
	if !isDuplicateKeyError(err) {
		return ServicePortReservation{}, false, err
	}
	existing, getErr := scanServicePortReservation(s.db.QueryRowContext(ctx, servicePortReservationSelect+`
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?`,
		reservation.ExecutionHostID, reservation.NetworkNamespace, reservation.Protocol, reservation.Port))
	if getErr != nil {
		return ServicePortReservation{}, false, getErr
	}
	if sameServicePortReservationOwner(existing, reservation) {
		return existing, false, nil
	}
	return ServicePortReservation{}, false, ErrServicePortReserved
}

func (s *MariaDBSystemUpdateStore) ListServicePortReservations(ctx context.Context, executionHostID string) ([]ServicePortReservation, error) {
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return nil, ErrInvalidServicePortReservation
	}
	rows, err := s.db.QueryContext(ctx, servicePortReservationSelect+`
WHERE execution_host_id = ?
ORDER BY network_namespace, protocol, port, service_id, service_role`, executionHostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reservations := make([]ServicePortReservation, 0)
	for rows.Next() {
		reservation, scanErr := scanServicePortReservation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		reservations = append(reservations, reservation)
	}
	return reservations, rows.Err()
}

func (s *MariaDBSystemUpdateStore) ReleaseServicePort(ctx context.Context, reservation ServicePortReservation) error {
	reservation = normalizeServicePortReservation(reservation)
	if err := validateServicePortReservation(reservation); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM service_port_reservations
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
  AND service_id = ? AND service_role = ?`,
		reservation.ExecutionHostID,
		reservation.NetworkNamespace,
		reservation.Protocol,
		reservation.Port,
		reservation.ServiceID,
		reservation.ServiceRole,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 1 {
		return err
	}
	var occupied int
	err = s.db.QueryRowContext(ctx, `SELECT 1
FROM service_port_reservations
WHERE execution_host_id = ? AND network_namespace = ? AND protocol = ? AND port = ?
LIMIT 1`,
		reservation.ExecutionHostID,
		reservation.NetworkNamespace,
		reservation.Protocol,
		reservation.Port,
	).Scan(&occupied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrServicePortReserved
}

const servicePortReservationSelect = `SELECT execution_host_id, network_namespace, protocol, port, service_id, service_role, created_at, updated_at
FROM service_port_reservations`

func scanServicePortReservation(scanner executionHostScanner) (ServicePortReservation, error) {
	var reservation ServicePortReservation
	err := scanner.Scan(
		&reservation.ExecutionHostID,
		&reservation.NetworkNamespace,
		&reservation.Protocol,
		&reservation.Port,
		&reservation.ServiceID,
		&reservation.ServiceRole,
		&reservation.CreatedAt,
		&reservation.UpdatedAt,
	)
	return reservation, err
}

func sortServicePortReservations(reservations []ServicePortReservation) {
	sort.Slice(reservations, func(i, j int) bool {
		left, right := reservations[i], reservations[j]
		if left.NetworkNamespace != right.NetworkNamespace {
			return left.NetworkNamespace < right.NetworkNamespace
		}
		if left.Protocol != right.Protocol {
			return left.Protocol < right.Protocol
		}
		if left.Port != right.Port {
			return left.Port < right.Port
		}
		if left.ServiceID != right.ServiceID {
			return left.ServiceID < right.ServiceID
		}
		return left.ServiceRole < right.ServiceRole
	})
}

var _ SystemUpdateExecutionHostStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateExecutionHostStore = (*MariaDBSystemUpdateStore)(nil)
var _ ServicePortReservationStore = (*MemorySystemUpdateStore)(nil)
var _ ServicePortReservationStore = (*MariaDBSystemUpdateStore)(nil)
