package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const systemUpdateRuntimeTokenRotationSelect = `SELECT id, service_id, execution_host_id,
idempotency_key, intent_sha256, status, revision,
expected_ownership_epoch, expected_source_policy_revision,
expected_projection_revision, expected_local_executor_policy_revision,
previous_token_id, staged_token_id, staged_token_hash, staged_token_scopes,
staged_token_ciphertext, staged_token_nonce, local_stage_receipt_id,
credential_claim_id_sha256, credential_claim_revision,
credential_claimed_at, local_stage_acknowledged_at, local_staged_at,
heartbeat_proved_at, activated_at,
cancel_requested_at, cancel_acknowledged_at, canceled_at,
emergency_revoked_token_id, emergency_revoked_at,
created_at, updated_at
FROM system_update_runtime_token_rotations`

type mariaDBRuntimeTokenRotationLockPhase string

const (
	mariaDBRuntimeTokenRotationHostLocksHeld     mariaDBRuntimeTokenRotationLockPhase = "host_locks_held"
	mariaDBRuntimeTokenRotationLaneLocksHeld     mariaDBRuntimeTokenRotationLockPhase = "lane_locks_held"
	mariaDBRuntimeTokenRotationRotationLocksHeld mariaDBRuntimeTokenRotationLockPhase = "rotation_locks_held"
	mariaDBRuntimeTokenRotationPolicyLocksHeld   mariaDBRuntimeTokenRotationLockPhase = "policy_locks_held"
)

type mariaDBRuntimeTokenRotationLockObserver func(string, mariaDBRuntimeTokenRotationLockPhase)
type mariaDBRuntimeTokenRotationLockObserverContextKey struct{}

func observeMariaDBRuntimeTokenRotationLockPhase(
	ctx context.Context,
	operation string,
	phase mariaDBRuntimeTokenRotationLockPhase,
) {
	observer, _ := ctx.Value(mariaDBRuntimeTokenRotationLockObserverContextKey{}).(mariaDBRuntimeTokenRotationLockObserver)
	if observer != nil {
		observer(operation, phase)
	}
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdateRuntimeTokenRotation(
	ctx context.Context,
	id string,
) (SystemUpdateRuntimeTokenRotation, error) {
	id = strings.TrimSpace(id)
	if !serviceIDPattern.MatchString(id) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	rotation, err := scanSystemUpdateRuntimeTokenRotation(s.db.QueryRowContext(
		ctx, systemUpdateRuntimeTokenRotationSelect+` WHERE id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

func (s *MariaDBSystemUpdateStore) GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
	ctx context.Context,
	executionHostID string,
) (SystemUpdateRuntimeTokenRotation, error) {
	executionHostID = strings.TrimSpace(executionHostID)
	if !executionHostIDPattern.MatchString(executionHostID) {
		return SystemUpdateRuntimeTokenRotation{}, ErrInvalidSystemUpdateRuntimeTokenRotation
	}
	rotation, err := scanSystemUpdateRuntimeTokenRotation(s.db.QueryRowContext(
		ctx,
		systemUpdateRuntimeTokenRotationSelect+`
WHERE active_execution_host_id = ?`,
		executionHostID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return SystemUpdateRuntimeTokenRotation{}, ErrNotFound
	}
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	return publicSystemUpdateRuntimeTokenRotation(rotation), nil
}

type mariaDBRuntimeTokenRotationLockPlan struct {
	RotationID      string
	ServiceID       string
	PreviousTokenID string
	StagedTokenID   string
	References      []mariaDBServiceTokenReference
}

func (plan mariaDBRuntimeTokenRotationLockPlan) tokenIDs() []string {
	return sortedUniqueStrings([]string{plan.PreviousTokenID, plan.StagedTokenID})
}

func (plan mariaDBRuntimeTokenRotationLockPlan) matches(rotation SystemUpdateRuntimeTokenRotation) bool {
	return plan.RotationID == rotation.ID &&
		plan.ServiceID == rotation.ServiceID &&
		plan.PreviousTokenID == rotation.PreviousTokenID &&
		plan.StagedTokenID == rotation.StagedTokenID
}

func (s *MariaDBSystemUpdateStore) discoverMariaDBRuntimeTokenRotationLockPlan(
	ctx context.Context,
	rotationID string,
) (mariaDBRuntimeTokenRotationLockPlan, error) {
	rotation, err := s.GetSystemUpdateRuntimeTokenRotation(ctx, rotationID)
	if err != nil {
		return mariaDBRuntimeTokenRotationLockPlan{}, err
	}
	plan := mariaDBRuntimeTokenRotationLockPlan{
		RotationID:      rotation.ID,
		ServiceID:       rotation.ServiceID,
		PreviousTokenID: rotation.PreviousTokenID,
		StagedTokenID:   rotation.StagedTokenID,
	}
	plan.References, err = discoverMariaDBServiceTokenReferences(ctx, s.db, plan.tokenIDs())
	if err != nil {
		return mariaDBRuntimeTokenRotationLockPlan{}, err
	}
	return plan, nil
}

func (s *MariaDBSystemUpdateStore) discoverMariaDBRuntimeTokenStageLockPlan(
	ctx context.Context,
	serviceID string,
) (mariaDBRuntimeTokenRotationLockPlan, error) {
	service, err := NewMariaDBAuthStore(s.db).getService(ctx, serviceID)
	if err != nil {
		return mariaDBRuntimeTokenRotationLockPlan{}, err
	}
	plan := mariaDBRuntimeTokenRotationLockPlan{
		ServiceID:       service.ServiceID,
		PreviousTokenID: service.TokenID,
	}
	plan.References, err = discoverMariaDBServiceTokenReferences(ctx, s.db, plan.tokenIDs())
	if err != nil {
		return mariaDBRuntimeTokenRotationLockPlan{}, err
	}
	return plan, nil
}

func lockMariaDBRuntimeTokenRotationPlan(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	plan mariaDBRuntimeTokenRotationLockPlan,
) (RegisteredService, map[string]ServiceToken, error) {
	services, tokens, err := lockMariaDBServiceTokenMutation(
		ctx, tx, operation, plan.References, plan.tokenIDs(), plan.ServiceID,
	)
	if errors.Is(err, ErrNotFound) {
		return RegisteredService{}, nil, ErrSystemUpdateOwnershipConflict
	}
	if err != nil {
		return RegisteredService{}, nil, err
	}
	service, ok := services[plan.ServiceID]
	if !ok {
		return RegisteredService{}, nil, ErrSystemUpdateOwnershipConflict
	}
	for _, reference := range plan.References {
		if reference.ServiceID != plan.ServiceID {
			return RegisteredService{}, nil, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	if !mariaDBServiceTokenReferenceTypesMatch(plan.References, services, tokens) {
		return RegisteredService{}, nil, ErrSystemUpdateRuntimeTokenRotationToken
	}
	return service, tokens, nil
}

var _ SystemUpdateRuntimeTokenRotationStore = (*MemorySystemUpdateStore)(nil)
var _ SystemUpdateRuntimeTokenRotationStore = (*MariaDBSystemUpdateStore)(nil)
