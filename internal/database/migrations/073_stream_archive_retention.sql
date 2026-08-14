ALTER TABLE streams
  ADD COLUMN deleted_at DATETIME NULL,
  ADD INDEX idx_streams_deleted_at (deleted_at);

ALTER TABLE stream_artifacts
  ADD COLUMN source_service_id VARCHAR(128) NOT NULL DEFAULT '';
