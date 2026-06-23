ALTER TABLE stream_youtube_runtimes
  ADD COLUMN IF NOT EXISTS oauth_account_id CHAR(36) NULL AFTER youtube_output;
