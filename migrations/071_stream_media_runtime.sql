-- Persist the media presentation contract selected by a successful start.
-- The value contains no credentials and must not be inferred later from
-- mutable service capability advertisements.
CREATE TABLE IF NOT EXISTS stream_media_runtimes (
  stream_id CHAR(36) NOT NULL,
  video_overlay_burn_in TINYINT(1) NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id),
  CONSTRAINT fk_stream_media_runtime_stream
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE
);
