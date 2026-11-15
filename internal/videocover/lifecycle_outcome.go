package videocover

import (
	"context"
	"database/sql"
	"errors"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
)

// ObservationRecorder is a product-owned extension, not an execution permit.
// The observation happens before this method. Its callback is only a local
// transaction participant and must never perform provider I/O.
type ObservationRecorder interface {
	RecordObservation(context.Context, State, string, string, func() error) (State, error)
}

// ObservationOutcome keeps the remote and local persistence boundaries separate.
// A structural validation result is never used to authorize a cover mutation.
func ObservationOutcome(observation lifecycle.BoundaryObservation, limits lifecycle.Limits) (string, error) {
	result := lifecycle.ValidateBoundaryObservation(observation, limits)
	if observation.Address.Kind != lifecycle.BackendMutation || observation.Address.SubBoundary != lifecycle.ServiceApply {
		return "", ErrInvalidRequest
	}
	if result.Kind == lifecycle.Unknown {
		return "confirming", nil
	}
	if result.Kind != lifecycle.StructurallyCoherent || observation.Outcome.Value == nil {
		return "", ErrInvalidRequest
	}
	switch *observation.Outcome.Value {
	case lifecycle.SuccessAtBoundary:
		return "applied", nil
	case lifecycle.FailureAtBoundary:
		return "failed", nil
	default:
		return "", ErrInvalidRequest
	}
}

func sameObservedAction(current, expected State) bool {
	return current.StreamID == expected.StreamID && current.JobGeneration == expected.JobGeneration &&
		current.DesiredRevision == expected.DesiredRevision && current.DesiredActive == expected.DesiredActive &&
		current.AssetVariantID == expected.AssetVariantID && current.LastIdempotencyKey == expected.LastIdempotencyKey &&
		current.LastIdempotencyKey != ""
}

// RecordObservation commits a current action and its critical audit under the
// same memory lock. Audit refusal happens before either state change.
func (m *MemoryRepository) RecordObservation(ctx context.Context, expected State, status, code string, criticalAudit func() error) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if criticalAudit == nil || status != "applied" && status != "failed" {
		return State{}, ErrInvalidRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states := m.states[expected.StreamID]
	var latest uint64
	for generation := range states {
		if generation > latest {
			latest = generation
		}
	}
	state, ok := states[latest]
	if !ok || latest != expected.JobGeneration || !sameObservedAction(state, expected) {
		return State{}, ErrStaleGeneration
	}
	key := actionKey(expected.StreamID, expected.JobGeneration, expected.LastIdempotencyKey)
	action, ok := m.actions[key]
	if !ok || action.RequestedRevision != expected.DesiredRevision || action.RequestedAssetVariantID != expected.AssetVariantID {
		return State{}, ErrIdempotencyConflict
	}
	if !currentActionMayResolve(state, state.LastIdempotencyKey, action) {
		if state.Status == status && action.Status == status {
			if err := criticalAudit(); err != nil {
				return State{}, err
			}
			return NormalizeState(state), nil
		}
		return State{}, ErrRevisionConflict
	}
	if err := criticalAudit(); err != nil {
		return State{}, err
	}
	action.Status, action.SafeErrorCode = status, safeCode(code)
	state.Status, state.LastErrorCode = status, action.SafeErrorCode
	if status == "applied" {
		action.SafeErrorCode, state.LastErrorCode = "", ""
		active, revision := state.DesiredActive, state.DesiredRevision
		state.AppliedActive, state.AppliedRevision = &active, &revision
		state.AppliedAssetVariantID = ""
		if active {
			state.AppliedAssetVariantID = state.AssetVariantID
		}
	}
	state.UpdatedAt = m.now()
	m.actions[key], states[latest] = action, state
	state.RecordedAction = recordedMemoryAction(action)
	return NormalizeState(state), nil
}

// RecordObservationTx joins the exact owning database transaction. It cannot
// open a second pool, commit, or write audit independently of the caller.
func (s *SQLRepository) RecordObservationTx(ctx context.Context, owner *sql.DB, tx *sql.Tx, expected State, status, code string) (State, bool, error) {
	if owner == nil || owner != s.db || tx == nil || status != "applied" && status != "failed" {
		return State{}, false, ErrInvalidRequest
	}
	state, err := scanState(tx.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? AND retired_at IS NULL ORDER BY job_generation DESC LIMIT 1 FOR UPDATE`, expected.StreamID))
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, false, ErrNotFound
	}
	if err != nil {
		return State{}, false, err
	}
	if !sameObservedAction(state, expected) {
		return State{}, false, ErrStaleGeneration
	}
	var revision uint64
	var active bool
	var actionStatus string
	var variant sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT requested_revision,requested_active,requested_asset_variant_id,result_status FROM stream_video_cover_actions WHERE stream_id=? AND job_generation=? AND idempotency_key=? FOR UPDATE`, expected.StreamID, expected.JobGeneration, expected.LastIdempotencyKey).Scan(&revision, &active, &variant, &actionStatus)
	if err != nil {
		return State{}, false, err
	}
	if revision != expected.DesiredRevision || active != expected.DesiredActive || variant.String != expected.AssetVariantID {
		return State{}, false, ErrIdempotencyConflict
	}
	if !actionMayResolve(actionStatus) || state.Status != "confirming" && !(state.Status == "idle" && actionStatus == "pending") {
		if state.Status == status && actionStatus == status {
			return state, false, nil
		}
		return State{}, false, ErrRevisionConflict
	}
	if actionStatus == "confirming" && state.Status != "confirming" || actionStatus == "pending" && state.Status != "idle" {
		return State{}, false, ErrRevisionConflict
	}
	if status == "applied" {
		code = ""
	} else {
		code = safeCode(code)
	}
	now := s.now()
	if _, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_actions SET result_status=?,safe_error_code=?,updated_at=? WHERE stream_id=? AND job_generation=? AND idempotency_key=?`, status, nullString(code), now, state.StreamID, state.JobGeneration, state.LastIdempotencyKey); err != nil {
		return State{}, false, err
	}
	if status == "applied" {
		_, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET applied_active=desired_active,applied_revision=desired_revision,applied_asset_variant_id=IF(desired_active,asset_variant_id,NULL),last_error_code=NULL,reconciliation_status='applied',updated_at=? WHERE stream_id=? AND job_generation=?`, now, state.StreamID, state.JobGeneration)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET last_error_code=?,reconciliation_status='failed',updated_at=? WHERE stream_id=? AND job_generation=?`, code, now, state.StreamID, state.JobGeneration)
	}
	if err != nil {
		return State{}, false, err
	}
	state, err = scanState(tx.QueryRowContext(ctx, stateSelect+` WHERE stream_id=? AND job_generation=?`, state.StreamID, state.JobGeneration))
	if err != nil {
		return State{}, false, err
	}
	state.RecordedAction = &PreparedAction{RequestedRevision: revision, RequestedAssetVariantID: variant.String, Outcome: status, SafeErrorCode: code}
	return state, true, nil
}
