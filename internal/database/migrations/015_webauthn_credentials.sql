CREATE TABLE IF NOT EXISTS webauthn_credentials (
  id CHAR(36) PRIMARY KEY,
  user_id CHAR(36) NOT NULL,
  name VARCHAR(255) NOT NULL,
  credential_id VARBINARY(1024) NOT NULL,
  credential_id_hash CHAR(64) NOT NULL,
  public_key_cbor MEDIUMBLOB NOT NULL,
  sign_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  transports_json JSON NOT NULL,
  aaguid VARCHAR(64) NULL,
  backup_eligible BOOLEAN NOT NULL DEFAULT false,
  backed_up BOOLEAN NOT NULL DEFAULT false,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  last_used_at DATETIME NULL,
  UNIQUE KEY uniq_webauthn_credential_id_hash (credential_id_hash),
  INDEX idx_webauthn_credentials_user_id (user_id),
  CONSTRAINT fk_webauthn_credentials_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
