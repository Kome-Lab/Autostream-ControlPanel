-- A Discord notification is not a side effect of the start HTTP request.  The
-- record survives a Control Panel restart and carries only public provider and
-- Discord identifiers; OAuth/Discord tokens and stream keys never belong here.
CREATE TABLE IF NOT EXISTS stream_discord_youtube_live_notifications (
  id CHAR(36) NOT NULL,
  stream_id CHAR(36) NOT NULL,
  event_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  watch_url VARCHAR(1900) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  discord_service_id CHAR(36) NOT NULL,
  discord_text_channel_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  youtube_mode VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  youtube_oauth_account_id CHAR(36) NULL,
  youtube_broadcast_id VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lifecycle_status VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  -- dispatching is an internally reclaimable pre-Bot lease. bot_dispatching
  -- is a durable external side-effect fence: expiry is delivery_unknown, never
  -- an automatic retry of the same Discord event.
  state ENUM('awaiting_youtube_live','dispatch_pending','dispatching','bot_dispatching','delivered','delivery_unknown','suppressed') NOT NULL,
  attempt_count INT NOT NULL DEFAULT 0,
  dispatch_attempt_count INT NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(6) NULL,
  lease_token_hash CHAR(64) NULL,
  lease_expires_at DATETIME(6) NULL,
  last_error VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  discord_message_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  delivered_at DATETIME(6) NULL,
  recovery_of_id CHAR(36) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_stream_discord_youtube_live_notification_event (event_id),
  UNIQUE KEY uq_stream_discord_youtube_live_notification_recovery (recovery_of_id),
  KEY idx_stream_discord_youtube_live_notification_due (state, next_attempt_at),
  KEY idx_stream_discord_youtube_live_notification_lease (state, lease_expires_at),
  KEY idx_stream_discord_youtube_live_notification_stream (stream_id, created_at),
  CONSTRAINT fk_stream_discord_youtube_live_notification_stream
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE
);
