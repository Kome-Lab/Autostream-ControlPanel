CREATE TABLE IF NOT EXISTS stream_archive_run_sources (
  stream_id CHAR(36) NOT NULL,
  archive_run_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  archive_started_at DATETIME(6) NOT NULL,
  service_id VARCHAR(128) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id, archive_run_id, archive_started_at),
  UNIQUE KEY uniq_stream_archive_run_source (stream_id, archive_run_id),
  INDEX idx_stream_archive_run_source_service (service_id, stream_id)
);

-- Do not infer a historical source from today's assignment. Existing runs are
-- backfilled only by an authenticated exact report from the current primary.
