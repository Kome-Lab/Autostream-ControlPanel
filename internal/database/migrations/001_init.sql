CREATE TABLE IF NOT EXISTS users (
  id CHAR(36) PRIMARY KEY,
  username VARCHAR(128) NOT NULL UNIQUE,
  email VARCHAR(255) NULL,
  password_hash TEXT NOT NULL,
  status ENUM('active','disabled','locked','pending_password_change') NOT NULL,
  failed_login_count INT NOT NULL DEFAULT 0,
  last_login_at DATETIME NULL,
  last_login_ip VARCHAR(64) NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS roles (
  id CHAR(36) PRIMARY KEY,
  name VARCHAR(128) NOT NULL UNIQUE,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id CHAR(36) NOT NULL,
  permission VARCHAR(160) NOT NULL,
  PRIMARY KEY (role_id, permission)
);

CREATE TABLE IF NOT EXISTS user_roles (
  user_id CHAR(36) NOT NULL,
  role_id CHAR(36) NOT NULL,
  PRIMARY KEY (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS sessions (
  id CHAR(64) PRIMARY KEY,
  user_id CHAR(36) NOT NULL,
  csrf_token_hash CHAR(64) NOT NULL,
  idle_expires_at DATETIME NOT NULL,
  absolute_expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id CHAR(36) PRIMARY KEY,
  timestamp DATETIME NOT NULL,
  actor_user_id CHAR(36) NULL,
  actor_username VARCHAR(128) NULL,
  actor_ip VARCHAR(64) NULL,
  user_agent TEXT NULL,
  action VARCHAR(160) NOT NULL,
  resource_type VARCHAR(80) NOT NULL,
  resource_id VARCHAR(160) NULL,
  result ENUM('success','failure') NOT NULL,
  metadata JSON NOT NULL,
  request_id CHAR(36) NOT NULL
);

CREATE TABLE IF NOT EXISTS service_tokens (
  id CHAR(36) PRIMARY KEY,
  service_type ENUM('discord_bot','encoder_recorder','worker','observability') NOT NULL,
  token_hash CHAR(64) NOT NULL,
  scopes JSON NOT NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS services (
  service_id VARCHAR(128) PRIMARY KEY,
  service_type ENUM('discord_bot','encoder_recorder','worker','observability') NOT NULL,
  service_name VARCHAR(255) NOT NULL,
  description TEXT NULL,
  host VARCHAR(255) NULL,
  port INT NULL,
  ssl_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  public_url TEXT NOT NULL,
  version VARCHAR(80) NOT NULL,
  reported_version VARCHAR(80) NOT NULL DEFAULT '',
  reported_commit VARCHAR(80) NOT NULL DEFAULT '',
  reported_build_date VARCHAR(80) NOT NULL DEFAULT '',
  status VARCHAR(80) NOT NULL,
  last_heartbeat_at DATETIME NULL,
  last_reported_at DATETIME NULL,
  current_stream_id CHAR(36) NULL,
  capabilities JSON NOT NULL,
  reported_capabilities JSON NULL,
  reported_hostname VARCHAR(255) NOT NULL DEFAULT '',
  reported_os VARCHAR(80) NOT NULL DEFAULT '',
  reported_arch VARCHAR(80) NOT NULL DEFAULT '',
    token_id CHAR(36) NOT NULL,
    node_token_ciphertext TEXT NULL,
    node_token_nonce VARCHAR(128) NULL,
    configure_token_hash CHAR(64) NULL,
  configure_token_expires_at DATETIME NULL,
  configure_token_used_at DATETIME NULL,
  node_token_rotated_at DATETIME NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS service_metric_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  service_id VARCHAR(128) NOT NULL,
  service_type ENUM('discord_bot','encoder_recorder','worker','observability') NOT NULL,
  metric_name VARCHAR(255) NOT NULL,
  status VARCHAR(80) NOT NULL DEFAULT '',
  value DOUBLE NOT NULL,
  observed_at DATETIME NOT NULL,
  INDEX idx_service_metric_snapshots_observed_at (observed_at),
  INDEX idx_service_metric_snapshots_service_observed (service_id, observed_at)
);

CREATE TABLE IF NOT EXISTS stream_service_assignments (
  id CHAR(36) PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  service_id VARCHAR(128) NOT NULL,
  service_type ENUM('discord_bot','encoder_recorder','worker','observability') NOT NULL,
  assigned_by_user_id CHAR(36) NULL,
  assigned_at DATETIME NOT NULL,
  UNIQUE KEY uniq_stream_service_type (stream_id, service_type)
);

CREATE TABLE IF NOT EXISTS service_stream_events (
  id CHAR(36) PRIMARY KEY,
  service_id VARCHAR(128) NOT NULL,
  stream_id CHAR(36) NOT NULL,
  event_type VARCHAR(160) NOT NULL,
  payload JSON NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS streams (
  id CHAR(36) PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  status ENUM('created','starting','live','stopping','completed','failed') NOT NULL,
  scheduled_start_at DATETIME NULL,
  scheduled_end_at DATETIME NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS stream_logs (
  id CHAR(36) PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  level ENUM('debug','info','warning','error') NOT NULL,
  message TEXT NOT NULL,
  fields JSON NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS stream_artifacts (
  id CHAR(36) PRIMARY KEY,
  stream_id CHAR(36) NOT NULL,
  kind VARCHAR(80) NOT NULL,
  name VARCHAR(255) NOT NULL,
  relative_path TEXT NOT NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL
);

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
  complete_retry_count INT NOT NULL DEFAULT 0,
  complete_next_retry_at DATETIME(6) NULL,
  complete_last_error VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_login_states (
  state_hash CHAR(64) PRIMARY KEY,
  provider_id CHAR(36) NOT NULL,
  provider_type ENUM('google','github','discord') NOT NULL,
  purpose VARCHAR(32) NOT NULL DEFAULT 'login',
  nonce VARCHAR(160) NOT NULL,
  redirect_after TEXT NULL,
  account_label VARCHAR(255) NULL,
  requested_scopes TEXT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  INDEX idx_oauth_login_states_expires_at (expires_at)
);

CREATE TABLE IF NOT EXISTS oauth_user_links (
  id CHAR(36) PRIMARY KEY,
  user_id CHAR(36) NOT NULL,
  provider_id CHAR(36) NOT NULL,
  provider_type ENUM('google','github','discord') NOT NULL,
  subject VARCHAR(255) NOT NULL,
  email VARCHAR(255) NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uniq_oauth_user_link_provider_subject (provider_id, subject),
  UNIQUE KEY uniq_oauth_user_link_user_provider_subject (user_id, provider_id, subject),
  INDEX idx_oauth_user_links_user_id (user_id)
);
