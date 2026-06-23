ALTER TABLE stream_youtube_runtimes
  ADD COLUMN IF NOT EXISTS stream_key_secret_name VARCHAR(160) NOT NULL DEFAULT '' AFTER live_stream_id;
