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
