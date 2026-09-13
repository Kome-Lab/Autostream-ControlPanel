package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	streamYouTubeRuntimeSaveAttempts = 2
	streamYouTubeRuntimeRetryDelay   = 100 * time.Millisecond
)

func (s MariaDBStreamStore) SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error {
	if strings.TrimSpace(runtime.Mode) == youtubeRelayBindingClaimStaticRuntimeMode {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	if strings.TrimSpace(runtime.StreamID) == "" {
		return ErrNotFound
	}
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = time.Now().UTC()
	}
	var lastErr error
	for attempt := 0; attempt < streamYouTubeRuntimeSaveAttempts; attempt++ {
		runtime.UpdatedAt = time.Now().UTC()
		lastErr = s.saveStreamYouTubeRuntimeOnce(ctx, runtime)
		if lastErr == nil || attempt+1 == streamYouTubeRuntimeSaveAttempts || !isTransientDatabaseConnectionError(lastErr) {
			return lastErr
		}
		timer := time.NewTimer(streamYouTubeRuntimeRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func (s MariaDBStreamStore) saveStreamYouTubeRuntimeOnce(ctx context.Context, runtime StreamYouTubeRuntime) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, runtime.StreamID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, runtime.StreamID); err != nil {
		return err
	} else if hasClaim {
		return ErrYouTubeRelayBindingClaimActive
	}
	if err := saveStreamYouTubeRuntimeTx(ctx, tx, runtime); err != nil {
		return err
	}
	return tx.Commit()
}

func streamYouTubeRuntimeCompleteLastError(value string) string {
	return truncateString(strings.TrimSpace(value), 255)
}

func (s MariaDBStreamStore) GetStreamYouTubeRuntime(ctx context.Context, streamID string) (StreamYouTubeRuntime, error) {
	var runtime StreamYouTubeRuntime
	err := scanStreamYouTubeRuntime(s.db.QueryRowContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at FROM stream_youtube_runtimes WHERE stream_id = ?`, streamID), &runtime)
	if err == sql.ErrNoRows {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	return runtime, err
}

func (s MariaDBStreamStore) ListStreamYouTubeRuntimes(ctx context.Context) ([]StreamYouTubeRuntime, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at
FROM stream_youtube_runtimes
ORDER BY updated_at DESC, stream_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runtimes []StreamYouTubeRuntime
	for rows.Next() {
		var runtime StreamYouTubeRuntime
		if err := scanStreamYouTubeRuntime(rows, &runtime); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, rows.Err()
}

func (s MariaDBStreamStore) ListDueStreamYouTubeRuntimes(ctx context.Context, now time.Time, limit int) ([]StreamYouTubeRuntime, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `SELECT stream_id, youtube_output, oauth_account_id, mode, broadcast_id, live_stream_id, rtmp_url, stream_key_secret_name, dry_run, complete_on_stop, complete_retry_count, complete_next_retry_at, complete_last_error, created_at, updated_at
FROM stream_youtube_runtimes
WHERE mode IN ('live_api', 'live_api_relay_static') AND complete_next_retry_at IS NOT NULL AND complete_next_retry_at <= ?
ORDER BY complete_next_retry_at ASC LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runtimes []StreamYouTubeRuntime
	for rows.Next() {
		var runtime StreamYouTubeRuntime
		if err := scanStreamYouTubeRuntime(rows, &runtime); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, rows.Err()
}

func (s MariaDBStreamStore) RecordStreamYouTubeRuntimeCompleteFailure(ctx context.Context, streamID, lastError string, nextRetryAt time.Time) (StreamYouTubeRuntime, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE stream_youtube_runtimes
SET complete_retry_count = complete_retry_count + 1, complete_next_retry_at = ?, complete_last_error = ?, updated_at = ?
WHERE stream_id = ?`, nextRetryAt.UTC(), truncateString(strings.TrimSpace(lastError), 255), time.Now().UTC(), streamID)
	if err != nil {
		return StreamYouTubeRuntime{}, err
	}
	return s.GetStreamYouTubeRuntime(ctx, streamID)
}

func (s MariaDBStreamStore) DeleteStreamYouTubeRuntime(ctx context.Context, streamID string) error {
	streamID = strings.TrimSpace(streamID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedStreamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, streamID).Scan(&lockedStreamID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, streamID); err != nil {
			return err
		} else if hasClaim {
			return ErrYouTubeRelayBindingClaimActive
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`, streamID); err != nil {
		return err
	}
	return tx.Commit()
}

type streamYouTubeRuntimeScanner interface {
	Scan(dest ...any) error
}

func scanStreamYouTubeRuntime(scanner streamYouTubeRuntimeScanner, runtime *StreamYouTubeRuntime) error {
	var oauthAccountID sql.NullString
	var broadcastID sql.NullString
	var liveStreamID sql.NullString
	var rtmpURL sql.NullString
	var nextRetryAt sql.NullTime
	var lastError sql.NullString
	err := scanner.Scan(&runtime.StreamID, &runtime.YouTubeOutput, &oauthAccountID, &runtime.Mode, &broadcastID, &liveStreamID, &rtmpURL, &runtime.StreamKeySecretName, &runtime.DryRun, &runtime.CompleteOnStop, &runtime.CompleteRetryCount, &nextRetryAt, &lastError, &runtime.CreatedAt, &runtime.UpdatedAt)
	if oauthAccountID.Valid {
		runtime.OAuthAccountID = oauthAccountID.String
	}
	if broadcastID.Valid {
		runtime.BroadcastID = broadcastID.String
	}
	if liveStreamID.Valid {
		runtime.LiveStreamID = liveStreamID.String
	}
	if rtmpURL.Valid {
		runtime.RTMPURL = rtmpURL.String
	}
	if nextRetryAt.Valid {
		runtime.CompleteNextRetryAt = nextRetryAt.Time
	}
	if lastError.Valid {
		runtime.CompleteLastError = lastError.String
	}
	return err
}
