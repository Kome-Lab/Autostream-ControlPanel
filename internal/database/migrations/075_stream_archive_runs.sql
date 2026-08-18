ALTER TABLE streams
  ADD COLUMN IF NOT EXISTS archive_run_id VARCHAR(128) NOT NULL DEFAULT '' AFTER status,
  ADD COLUMN IF NOT EXISTS archive_started_at DATETIME(6) NULL AFTER archive_run_id,
  ADD COLUMN IF NOT EXISTS archive_reported_at DATETIME(6) NULL AFTER archive_started_at;

ALTER TABLE stream_artifacts
  ADD COLUMN IF NOT EXISTS archive_run_id VARCHAR(128) NOT NULL DEFAULT '' AFTER stream_id,
  ADD COLUMN IF NOT EXISTS archive_started_at DATETIME(6) NULL AFTER archive_run_id;

ALTER TABLE stream_artifacts
  DROP INDEX IF EXISTS uniq_stream_artifacts_stream_kind_name;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_stream_artifacts_stream_run_kind_name
  ON stream_artifacts (stream_id, archive_run_id, kind, name);

CREATE INDEX IF NOT EXISTS idx_stream_artifacts_stream_run_started
  ON stream_artifacts (stream_id, archive_started_at, created_at);
