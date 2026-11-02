-- Keep this migration resumable across MariaDB's independently committed DDL.
-- The migration runner records the migration only after every statement succeeds.
-- A process interruption after the first ALTER must therefore be safe to replay.
ALTER TABLE media_assets
  ADD COLUMN IF NOT EXISTS client_operation_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER upload_session_id,
  ADD COLUMN IF NOT EXISTS client_operation_session_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER client_operation_id,
  ADD UNIQUE INDEX IF NOT EXISTS uq_media_upload_operation (client_operation_session_id, client_operation_id);

-- MODIFY to the same definition is safe to replay after a partial application.
-- Preserve complete ordinary filenames across save/reload; no existing row is removed.
ALTER TABLE stream_video_cover_candidates MODIFY COLUMN display_name VARCHAR(255) NOT NULL;
