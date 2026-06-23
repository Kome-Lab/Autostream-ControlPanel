CREATE TABLE IF NOT EXISTS users (
  id CHAR(36) PRIMARY KEY,
  username VARCHAR(128) NOT NULL UNIQUE,
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
  public_url TEXT NOT NULL,
  version VARCHAR(80) NOT NULL,
  status VARCHAR(80) NOT NULL,
  last_heartbeat_at DATETIME NULL,
  current_stream_id CHAR(36) NULL,
  capabilities JSON NOT NULL,
  token_id CHAR(36) NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
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
