package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s MariaDBStreamStore) MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateReserved:
		if (existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted && existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared) || !existing.EncoderStopConfirmedAt.IsZero() {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
		if err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET broadcast_id = ?, state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state IN (?, ?, ?) AND dispatch_state = ?`,
		claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimStateRecoveryRequired,
		YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	claim.State = YouTubeRelayBindingClaimStateRecoveryRequired
	claim.PrepareState = existing.PrepareState
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	claim.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired
// records an uncertain provider cleanup after a prepared runtime could not be
// dispatched. The runtime deletion and recovery fence transition share one
// transaction so completion retry cannot race the recovery workflow.
func (s MariaDBStreamStore) AbandonPreparedStreamYouTubeRuntimeAndMarkRelayBindingRecoveryRequired(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.BroadcastID != claim.BroadcastID || existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.BroadcastID != claim.BroadcastID {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
	  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	claim.State = YouTubeRelayBindingClaimStateRecoveryRequired
	claim.PrepareState = YouTubeRelayBindingClaimPrepareStatePossiblyPrepared
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
	claim.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence closes the
// write/read-loss window for the durable dispatch marker. The caller must have
// already moved the stream to failed. Regardless of whether the marker made it
// to storage, the static runtime is removed atomically and the external
// broadcast stays fenced for explicit recovery.
func (s MariaDBStreamStore) ReconcilePreparedStreamYouTubeRelayBindingClaimAfterDispatchFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "failed") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if existing.LastError != claim.LastError {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state IN (?, ?)`,
		YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared,
		YouTubeRelayBindingClaimDispatchStateNotDispatched, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if affected != 1 {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
	existing.LastError = claim.LastError
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}
