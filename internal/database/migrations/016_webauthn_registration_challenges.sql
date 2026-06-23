CREATE TABLE IF NOT EXISTS webauthn_registration_challenges (
  id CHAR(64) PRIMARY KEY,
  user_id CHAR(36) NOT NULL,
  challenge VARCHAR(128) NOT NULL,
  user_handle VARCHAR(512) NOT NULL,
  rp_id VARCHAR(255) NOT NULL,
  rp_name VARCHAR(255) NOT NULL,
  user_name VARCHAR(255) NOT NULL,
  user_display_name VARCHAR(255) NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  INDEX idx_webauthn_registration_user_id (user_id),
  INDEX idx_webauthn_registration_expires_at (expires_at),
  CONSTRAINT fk_webauthn_registration_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
