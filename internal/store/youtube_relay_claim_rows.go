package store

import (
	"context"
	"database/sql"
	"errors"
)

const youtubeRelayBindingClaimSelect = `SELECT relay_binding_id, reservation_token, stream_id, youtube_output_id, youtube_output_revision, oauth_account_id,
  reusable_live_stream_id, COALESCE(broadcast_id, ''), state, prepare_state, dispatch_state, encoder_stop_confirmed_at, last_error, created_at, updated_at
FROM stream_youtube_relay_binding_claims`

type youtubeRelayBindingClaimScanner interface {
	Scan(dest ...any) error
}

func getYouTubeRelayBindingClaimTx(ctx context.Context, tx *sql.Tx, relayBindingID string) (YouTubeRelayBindingClaim, error) {
	claim, err := scanYouTubeRelayBindingClaim(tx.QueryRowContext(ctx, youtubeRelayBindingClaimSelect+` WHERE relay_binding_id = ? FOR UPDATE`, relayBindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return YouTubeRelayBindingClaim{}, ErrNotFound
	}
	return claim, err
}

func hasStreamYouTubeRelayBindingClaimForStreamTx(ctx context.Context, tx *sql.Tx, streamID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_youtube_relay_binding_claims WHERE stream_id = ?)`, streamID).Scan(&exists)
	return exists, err
}

func scanYouTubeRelayBindingClaim(scanner youtubeRelayBindingClaimScanner) (YouTubeRelayBindingClaim, error) {
	var claim YouTubeRelayBindingClaim
	var encoderStopConfirmedAt sql.NullTime
	err := scanner.Scan(&claim.RelayBindingID, &claim.ReservationToken, &claim.StreamID, &claim.YouTubeOutputID, &claim.YouTubeOutputRevision, &claim.OAuthAccountID,
		&claim.ReusableLiveStreamID, &claim.BroadcastID, &claim.State, &claim.PrepareState, &claim.DispatchState, &encoderStopConfirmedAt, &claim.LastError, &claim.CreatedAt, &claim.UpdatedAt)
	if err != nil {
		return YouTubeRelayBindingClaim{}, err
	}
	if !isValidYouTubeRelayBindingID(claim.RelayBindingID) {
		return YouTubeRelayBindingClaim{}, ErrInvalidYouTubeRelayBindingClaim
	}
	if encoderStopConfirmedAt.Valid {
		claim.EncoderStopConfirmedAt = encoderStopConfirmedAt.Time.UTC()
	}
	return claim, nil
}

func getStreamYouTubeRuntimeTx(ctx context.Context, tx *sql.Tx, streamID string) (StreamYouTubeRuntime, error) {
	var runtime StreamYouTubeRuntime
	err := scanStreamYouTubeRuntime(tx.QueryRowContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at FROM stream_youtube_runtimes WHERE stream_id = ? FOR UPDATE`, streamID), &runtime)
	if errors.Is(err, sql.ErrNoRows) {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	return runtime, err
}

func saveStreamYouTubeRuntimeTx(ctx context.Context, tx *sql.Tx, runtime StreamYouTubeRuntime) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_youtube_runtimes (stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE youtube_output = VALUES(youtube_output), oauth_account_id = VALUES(oauth_account_id), mode = VALUES(mode), broadcast_id = VALUES(broadcast_id), live_stream_id = VALUES(live_stream_id), rtmp_url = VALUES(rtmp_url), stream_key_secret_name = VALUES(stream_key_secret_name), dry_run = VALUES(dry_run), complete_on_stop = VALUES(complete_on_stop), complete_retry_count = VALUES(complete_retry_count), complete_next_retry_at = VALUES(complete_next_retry_at), complete_last_error = VALUES(complete_last_error), updated_at = VALUES(updated_at)`,
		runtime.StreamID, runtime.YouTubeOutput, runtime.OAuthAccountID, runtime.Mode, runtime.BroadcastID, runtime.LiveStreamID, runtime.RTMPURL, runtime.StreamKeySecretName, runtime.DryRun, runtime.CompleteOnStop, runtime.CompleteRetryCount, nullTime(runtime.CompleteNextRetryAt), streamYouTubeRuntimeCompleteLastError(runtime.CompleteLastError), runtime.CreatedAt, runtime.UpdatedAt)
	return err
}
