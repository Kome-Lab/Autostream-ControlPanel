CREATE TABLE IF NOT EXISTS webauthn_ceremony_sessions (
  id VARCHAR(128) PRIMARY KEY,
  user_id CHAR(36) NULL,
  ceremony VARCHAR(32) NOT NULL,
  session_json LONGBLOB NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  INDEX idx_webauthn_ceremony_sessions_user (user_id),
  INDEX idx_webauthn_ceremony_sessions_expires (expires_at),
  CONSTRAINT fk_webauthn_ceremony_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;
