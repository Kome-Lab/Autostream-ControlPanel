ALTER TABLE stream_youtube_runtimes
  ADD COLUMN IF NOT EXISTS rtmp_url TEXT NULL AFTER live_stream_id;
