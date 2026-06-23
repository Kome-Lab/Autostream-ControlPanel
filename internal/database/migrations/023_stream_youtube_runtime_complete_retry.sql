ALTER TABLE stream_youtube_runtimes
  ADD COLUMN IF NOT EXISTS complete_retry_count INT NOT NULL DEFAULT 0 AFTER complete_on_stop,
  ADD COLUMN IF NOT EXISTS complete_next_retry_at DATETIME(6) NULL AFTER complete_retry_count,
  ADD COLUMN IF NOT EXISTS complete_last_error VARCHAR(255) NOT NULL DEFAULT '' AFTER complete_next_retry_at;

CREATE INDEX IF NOT EXISTS idx_stream_youtube_runtimes_complete_next_retry_at
  ON stream_youtube_runtimes (complete_next_retry_at);
