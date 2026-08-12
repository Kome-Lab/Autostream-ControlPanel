package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s MariaDBStreamStore) SetStreamVideoOverlayBurnIn(ctx context.Context, streamID string, enabled bool) error {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return ErrNotFound
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_media_runtimes (stream_id, video_overlay_burn_in, updated_at)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE video_overlay_burn_in = VALUES(video_overlay_burn_in), updated_at = VALUES(updated_at)`, streamID, enabled, now)
	return err
}

func (s MariaDBStreamStore) GetStreamMediaRuntime(ctx context.Context, streamID string) (StreamMediaRuntime, error) {
	var runtime StreamMediaRuntime
	err := s.db.QueryRowContext(ctx, `SELECT stream_id, video_overlay_burn_in, updated_at
FROM stream_media_runtimes WHERE stream_id = ?`, strings.TrimSpace(streamID)).Scan(
		&runtime.StreamID, &runtime.VideoOverlayBurnIn, &runtime.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return StreamMediaRuntime{}, ErrNotFound
	}
	if err != nil {
		return StreamMediaRuntime{}, err
	}
	return runtime, nil
}
