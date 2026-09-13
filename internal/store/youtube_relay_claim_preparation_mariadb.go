package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s MariaDBStreamStore) FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim YouTubeRelayBindingClaim, runtime StreamYouTubeRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	runtime = normalizeRelayStaticRuntime(runtime)
	if err := validateYouTubeRelayBindingClaimFinalize(claim, runtime); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
			return ErrYouTubeRelayBindingClaimConflict
		}
		existingRuntime, err := getStreamYouTubeRuntimeTx(ctx, tx, runtime.StreamID)
		if err != nil {
			return err
		}
		if !sameRelayStaticRuntime(existingRuntime, runtime) {
			return ErrYouTubeRelayBindingClaimConflict
		}
		return tx.Commit()
	case YouTubeRelayBindingClaimStateReserved:
		if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched {
			return ErrYouTubeRelayBindingClaimState
		}
		if runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
			return ErrInvalidYouTubeRelayBindingClaim
		}
		// Continue below.
	default:
		return ErrYouTubeRelayBindingClaimState
	}
	if existingRuntime, err := getStreamYouTubeRuntimeTx(ctx, tx, runtime.StreamID); err == nil {
		if sameRelayStaticRuntime(existingRuntime, runtime) {
			return ErrYouTubeRelayBindingClaimState
		}
		return ErrYouTubeRelayBindingClaimConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	now := nowYouTubeRelayBindingClaim()
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	runtime.UpdatedAt = now
	if err := saveStreamYouTubeRuntimeTx(ctx, tx, runtime); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
	SET broadcast_id = ?, state = ?, dispatch_state = ?, last_error = '', updated_at = ?
	WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt, YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrYouTubeRelayBindingClaimState
	}
	return tx.Commit()
}

// MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched fences the
// external Start boundary. It intentionally changes state before dispatch: a
// timeout after that point is treated as potentially running and can never use
// the no-process recovery path.
func (s MariaDBStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
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
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "starting") {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) || runtime.CompleteRetryCount != 0 || !runtime.CompleteNextRetryAt.IsZero() || strings.TrimSpace(runtime.CompleteLastError) != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.DispatchState {
	case YouTubeRelayBindingClaimDispatchStatePossiblyDispatched:
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimDispatchStateNotDispatched:
		// Continue below.
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET dispatch_state = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimDispatchStatePossiblyDispatched, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
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
	existing.DispatchState = YouTubeRelayBindingClaimDispatchStatePossiblyDispatched
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

// MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed stores a
// non-secret receipt immediately after the primary Encoder acknowledges Stop.
// It accepts the prepared claim or its runtime-free recovery descendant. The
// timestamp is created in this transaction, never accepted from a caller.
func (s MariaDBStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
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
	if !isYouTubeRelayBindingClaimEncoderStopReceiptStreamStatus(streamStatus) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || existing.BroadcastID != claim.BroadcastID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	switch existing.State {
	case YouTubeRelayBindingClaimStatePrepared:
		if existing.LastError != "" {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
		runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
		if err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		}
	case YouTubeRelayBindingClaimStateRecoveryRequired:
		if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
		} else if !errors.Is(err, ErrNotFound) {
			return YouTubeRelayBindingClaim{}, err
		}
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if !existing.EncoderStopConfirmedAt.IsZero() {
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET encoder_stop_confirmed_at = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state IN (?, ?) AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
		now, now, claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		claim.BroadcastID, YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimStateRecoveryRequired, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
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
	existing.EncoderStopConfirmedAt = now
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}
