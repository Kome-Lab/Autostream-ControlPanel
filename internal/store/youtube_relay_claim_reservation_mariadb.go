package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s MariaDBStreamStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservation(claim); err != nil {
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
	var assignedOutputID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT youtube_output_id FROM stream_settings WHERE stream_id = ? FOR UPDATE`, claim.StreamID).Scan(&assignedOutputID)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimStreamOutputConflict
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if strings.TrimSpace(assignedOutputID.String) != claim.YouTubeOutputID {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimStreamOutputConflict
	}
	var outputRevision uint64
	err = tx.QueryRowContext(ctx, `SELECT youtube_relay_binding_revision
FROM profiles WHERE id = ? AND kind = ? FOR UPDATE`, claim.YouTubeOutputID, string(ProfileYouTubeOutput)).Scan(&outputRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if outputRevision != *claim.ExpectedYouTubeOutputRevision {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimProfileRevisionConflict
	}
	now := nowYouTubeRelayBindingClaim()
	claim.ReservationToken = newUUID()
	claim.YouTubeOutputRevision = outputRevision
	claim.ExpectedYouTubeOutputRevision = nil
	claim.State = YouTubeRelayBindingClaimStateReserved
	claim.PrepareState = YouTubeRelayBindingClaimPrepareStateNotAttempted
	claim.DispatchState = YouTubeRelayBindingClaimDispatchStateNotDispatched
	claim.EncoderStopConfirmedAt = time.Time{}
	claim.BroadcastID = ""
	claim.LastError = ""
	claim.CreatedAt = now
	claim.UpdatedAt = now
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_youtube_relay_binding_claims
	  (relay_binding_id, reservation_token, stream_id, youtube_output_id, youtube_output_revision, oauth_account_id, reusable_live_stream_id, broadcast_id, state, prepare_state, dispatch_state, encoder_stop_confirmed_at, last_error, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, NULL, '', ?, ?)`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.YouTubeOutputID, claim.YouTubeOutputRevision, claim.OAuthAccountID, claim.ReusableLiveStreamID, claim.State, claim.PrepareState, claim.DispatchState, claim.CreatedAt, claim.UpdatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimConflict
		}
		return YouTubeRelayBindingClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return claim, nil
}

// MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared fences the
// external YouTube PrepareRelayStatic boundary. It intentionally advances the
// claim before the provider call: a timeout or process crash after this point
// is always recovered as a possible provider-side Broadcast creation.
func (s MariaDBStreamStore) MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPrepareFence(claim); err != nil {
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
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return YouTubeRelayBindingClaim{}, err
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaim{}, err
		}
		return existing, nil
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		// Continue below.
	default:
		return YouTubeRelayBindingClaim{}, ErrYouTubeRelayBindingClaimState
	}
	now := nowYouTubeRelayBindingClaim()
	result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET prepare_state = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, now,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
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
	existing.PrepareState = YouTubeRelayBindingClaimPrepareStatePossiblyPrepared
	existing.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	return existing, nil
}

// ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence safely
// converges a marker write/read failure without trusting the caller's observed
// phase. A not_attempted reservation proves no provider Prepare call occurred
// and is released; a possibly_prepared reservation is retained as recovery.
func (s MariaDBStreamStore) ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx context.Context, claim YouTubeRelayBindingClaim) (YouTubeRelayBindingClaimPrepareFenceResolution, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimRecovery(claim); err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	defer tx.Rollback()
	var streamID, streamStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM streams WHERE id = ? FOR UPDATE`, claim.StreamID).Scan(&streamID, &streamStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrNotFound
	}
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State == YouTubeRelayBindingClaimStateRecoveryRequired {
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || existing.BroadcastID != "" || existing.LastError != "" || !existing.EncoderStopConfirmedAt.IsZero() {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
	}
	switch existing.PrepareState {
	case YouTubeRelayBindingClaimPrepareStateNotAttempted:
		result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
			claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
			YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		if affected != 1 {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
		}
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Released: true}, nil
	case YouTubeRelayBindingClaimPrepareStatePossiblyPrepared:
		now := nowYouTubeRelayBindingClaim()
		result, err := tx.ExecContext(ctx, `UPDATE stream_youtube_relay_binding_claims
SET broadcast_id = ?, state = ?, last_error = ?, updated_at = ?
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
			claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired, claim.LastError, now,
			claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
			YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStateNotDispatched)
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		if affected != 1 {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
		}
		existing.BroadcastID = claim.BroadcastID
		existing.State = YouTubeRelayBindingClaimStateRecoveryRequired
		existing.LastError = claim.LastError
		existing.UpdatedAt = now
		if err := tx.Commit(); err != nil {
			return YouTubeRelayBindingClaimPrepareFenceResolution{}, err
		}
		return YouTubeRelayBindingClaimPrepareFenceResolution{Claim: existing}, nil
	default:
		return YouTubeRelayBindingClaimPrepareFenceResolution{}, ErrYouTubeRelayBindingClaimState
	}
}
