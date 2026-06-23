CREATE TABLE IF NOT EXISTS runtime_secret_leases (
  id VARCHAR(64) PRIMARY KEY,
  service_id VARCHAR(128) NOT NULL,
  token_id VARCHAR(64) NOT NULL,
  stream_id VARCHAR(64) NOT NULL DEFAULT '',
  archive_profile_id VARCHAR(64) NOT NULL DEFAULT '',
  secret_name VARCHAR(255) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  UNIQUE KEY uniq_runtime_secret_lease_context (service_id, stream_id, archive_profile_id, secret_name),
  INDEX idx_runtime_secret_leases_expires_at (expires_at)
);
