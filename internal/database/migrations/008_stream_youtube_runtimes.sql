CREATE TABLE IF NOT EXISTS stream_youtube_runtimes (
  stream_id CHAR(36) PRIMARY KEY,
  youtube_output VARCHAR(160) NOT NULL,
  oauth_account_id CHAR(36) NULL,
  mode VARCHAR(80) NOT NULL,
  broadcast_id VARCHAR(255) NULL,
  live_stream_id VARCHAR(255) NULL,
  rtmp_url TEXT NULL,
  stream_key_secret_name VARCHAR(160) NOT NULL DEFAULT '',
  dry_run BOOLEAN NOT NULL DEFAULT FALSE,
  complete_on_stop BOOLEAN NOT NULL DEFAULT TRUE,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
