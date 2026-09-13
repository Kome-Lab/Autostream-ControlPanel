package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s MariaDBStreamStore) ReleaseReservedStreamYouTubeRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimReservationFence(claim); err != nil {
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
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.State != YouTubeRelayBindingClaimStateReserved || existing.PrepareState != YouTubeRelayBindingClaimPrepareStateNotAttempted || existing.DispatchState != YouTubeRelayBindingClaimDispatchStateNotDispatched || !existing.EncoderStopConfirmedAt.IsZero() || existing.BroadcastID != "" || existing.LastError != "" {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ? AND encoder_stop_confirmed_at IS NULL`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStateReserved, YouTubeRelayBindingClaimPrepareStateNotAttempted, YouTubeRelayBindingClaimDispatchStateNotDispatched)
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

func (s MariaDBStreamStore) CompleteStreamYouTubeRuntimeAndReleaseRelayBindingClaim(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
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
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStatePrepared || existing.PrepareState != YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || existing.BroadcastID != claim.BroadcastID || existing.DispatchState != YouTubeRelayBindingClaimDispatchStatePossiblyDispatched {
		return ErrYouTubeRelayBindingClaimState
	}
	// A fixed relay may be reused only after the normal stop lifecycle has
	// durably recorded a completed stream. `failed` also represents an
	// unacknowledged force stop or a partial downstream failure, so it is not
	// evidence that the Encoder has stopped sending to the shared relay.
	if !strings.EqualFold(strings.TrimSpace(streamStatus), "completed") {
		return ErrYouTubeRelayBindingClaimState
	}
	runtime, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID)
	if err != nil {
		return err
	}
	if !runtimeMatchesYouTubeRelayBindingClaim(runtime, existing) {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, claim.StreamID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND state = ? AND prepare_state = ? AND dispatch_state = ?`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt,
		YouTubeRelayBindingClaimStatePrepared, YouTubeRelayBindingClaimPrepareStatePossiblyPrepared, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
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

func (s MariaDBStreamStore) ResolveStreamYouTubeRelayBindingRecovery(ctx context.Context, claim YouTubeRelayBindingClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim = normalizeYouTubeRelayBindingClaim(claim)
	if err := validateYouTubeRelayBindingClaimPreparedFence(claim); err != nil {
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
	if isActiveYouTubeRelayBindingClaimStreamStatus(streamStatus) {
		return ErrYouTubeRelayBindingClaimState
	}
	existing, err := getYouTubeRelayBindingClaimTx(ctx, tx, claim.RelayBindingID)
	if err != nil {
		return err
	}
	if !sameYouTubeRelayBindingReservation(existing, claim) {
		return ErrYouTubeRelayBindingClaimConflict
	}
	if existing.State != YouTubeRelayBindingClaimStateRecoveryRequired || existing.BroadcastID != claim.BroadcastID || !isYouTubeRelayBindingClaimDispatchFenceState(existing.DispatchState) {
		return ErrYouTubeRelayBindingClaimState
	}
	if existing.DispatchState == YouTubeRelayBindingClaimDispatchStatePossiblyDispatched && existing.EncoderStopConfirmedAt.IsZero() {
		return ErrYouTubeRelayBindingClaimState
	}
	if _, err := getStreamYouTubeRuntimeTx(ctx, tx, claim.StreamID); err == nil {
		return ErrYouTubeRelayBindingClaimState
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_relay_binding_claims
WHERE relay_binding_id = ? AND reservation_token = ? AND stream_id = ? AND created_at = ?
  AND broadcast_id = ? AND state = ?
  AND (dispatch_state = ? OR (dispatch_state = ? AND encoder_stop_confirmed_at IS NOT NULL))`,
		claim.RelayBindingID, claim.ReservationToken, claim.StreamID, claim.CreatedAt, claim.BroadcastID, YouTubeRelayBindingClaimStateRecoveryRequired,
		YouTubeRelayBindingClaimDispatchStateNotDispatched, YouTubeRelayBindingClaimDispatchStatePossiblyDispatched)
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

func (s MariaDBStreamStore) GetStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	claim, err := scanYouTubeRelayBindingClaim(s.db.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE relay_binding_id = ?`, relayBindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func (s MariaDBStreamStore) GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (YouTubeRelayBindingClaim, error) {
	if err := ctx.Err(); err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if !isCanonicalUUID(streamID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	claim, err := scanYouTubeRelayBindingClaim(s.db.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE stream_id = ?`, streamID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func (s MariaDBStreamStore) HasStreamYouTubeRelayBindingClaimForOutput(ctx context.Context, youtubeOutputID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	youtubeOutputID = strings.TrimSpace(youtubeOutputID)
	if !isCanonicalUUID(youtubeOutputID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE youtube_output_id = ?)`, youtubeOutputID).Scan(&exists)
	return exists, err
}

func (s MariaDBStreamStore) HasStreamYouTubeRelayBindingClaim(ctx context.Context, relayBindingID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !isValidYouTubeRelayBindingID(relayBindingID) {
		return false, ErrInvalidYouTubeRelayBindingClaim
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE relay_binding_id = ?)`, relayBindingID).Scan(&exists)
	return exists, err
}
