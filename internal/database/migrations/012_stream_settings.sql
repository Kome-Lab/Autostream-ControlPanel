CREATE TABLE IF NOT EXISTS stream_settings (
  stream_id CHAR(36) PRIMARY KEY,
  discord_config_id CHAR(36) NULL,
  encoder_profile_id CHAR(36) NULL,
  caption_profile_id CHAR(36) NULL,
  overlay_profile_id CHAR(36) NULL,
  archive_profile_id CHAR(36) NULL,
  youtube_output_id CHAR(36) NULL,
  encoder_input_url TEXT NULL,
  updated_at DATETIME NOT NULL,
  FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE
);
