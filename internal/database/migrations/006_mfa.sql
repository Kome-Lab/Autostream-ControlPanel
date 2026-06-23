CREATE TABLE IF NOT EXISTS user_mfa (
  user_id VARCHAR(64) PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT false,
  totp_secret_ciphertext TEXT NULL,
  totp_secret_nonce VARCHAR(64) NULL,
  pending_totp_secret_ciphertext TEXT NULL,
  pending_totp_secret_nonce VARCHAR(64) NULL,
  recovery_code_hashes_json JSON NULL,
  updated_at DATETIME NOT NULL,
  CONSTRAINT fk_user_mfa_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS mfa_challenges (
  id VARCHAR(64) PRIMARY KEY,
  user_id VARCHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  INDEX idx_mfa_challenges_user_id (user_id),
  INDEX idx_mfa_challenges_expires_at (expires_at),
  CONSTRAINT fk_mfa_challenges_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
