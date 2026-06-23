ALTER TABLE stream_youtube_runtimes
  ADD COLUMN IF NOT EXISTS complete_on_stop BOOLEAN NOT NULL DEFAULT TRUE AFTER dry_run;
