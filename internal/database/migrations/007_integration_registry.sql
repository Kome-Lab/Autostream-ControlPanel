CREATE TABLE IF NOT EXISTS oauth_providers (
  id CHAR(36) PRIMARY KEY,
  provider_type ENUM('google','github','discord') NOT NULL,
  name VARCHAR(255) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  client_id VARCHAR(512) NOT NULL,
  client_secret_ciphertext TEXT NULL,
  client_secret_nonce VARCHAR(64) NULL,
  scopes JSON NOT NULL,
  allowed_domains JSON NOT NULL,
  redirect_uri TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uniq_oauth_provider_name (name)
);

CREATE TABLE IF NOT EXISTS oauth_accounts (
  id CHAR(36) PRIMARY KEY,
  provider_id CHAR(36) NOT NULL,
  provider_type ENUM('google','github','discord') NOT NULL,
  account_label VARCHAR(255) NOT NULL,
  subject VARCHAR(255) NULL,
  email VARCHAR(255) NULL,
  scopes JSON NOT NULL,
  refresh_token_ciphertext TEXT NULL,
  refresh_token_nonce VARCHAR(64) NULL,
  token_fingerprint VARCHAR(32) NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  INDEX idx_oauth_accounts_provider_id (provider_id)
);

CREATE TABLE IF NOT EXISTS drive_destinations (
  id CHAR(36) PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  auth_mode ENUM('oauth2','service_account') NOT NULL,
  oauth_account_id CHAR(36) NULL,
  folder_id_ciphertext TEXT NOT NULL,
  folder_id_nonce VARCHAR(64) NOT NULL,
  folder_id_fingerprint VARCHAR(32) NOT NULL,
  masked_folder_id VARCHAR(128) NOT NULL,
  shared_drive BOOLEAN NOT NULL DEFAULT false,
  base_path VARCHAR(512) NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uniq_drive_destination_name (name)
);
