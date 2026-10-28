ALTER TABLE media_assets
  ADD COLUMN client_operation_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER upload_session_id,
  ADD COLUMN client_operation_session_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER client_operation_id,
  ADD UNIQUE KEY uq_media_upload_operation (client_operation_session_id, client_operation_id);

-- Preserve complete ordinary filenames across save/reload; no existing row is removed.
ALTER TABLE stream_video_cover_candidates MODIFY COLUMN display_name VARCHAR(255) NOT NULL;
