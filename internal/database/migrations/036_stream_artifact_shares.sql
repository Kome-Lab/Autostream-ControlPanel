CREATE TABLE IF NOT EXISTS stream_artifact_shares (
  id CHAR(36) PRIMARY KEY,
  token_hash CHAR(64) NOT NULL UNIQUE,
  stream_id CHAR(36) NOT NULL,
  artifact_id CHAR(36) NOT NULL,
  created_by_user_id CHAR(36) NULL,
  allow_download BOOLEAN NOT NULL DEFAULT TRUE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  revoked_at DATETIME NULL,
  INDEX idx_stream_artifact_shares_artifact (stream_id, artifact_id),
  INDEX idx_stream_artifact_shares_expires_at (expires_at)
);
