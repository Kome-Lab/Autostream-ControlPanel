ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_previous_token_id CHAR(36) NULL AFTER node_token_nonce;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_id CHAR(36) NULL AFTER staged_node_previous_token_id;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_hash CHAR(64) NULL AFTER staged_node_token_id;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_scopes JSON NULL AFTER staged_node_token_hash;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_ciphertext TEXT NULL AFTER staged_node_token_scopes;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_nonce VARCHAR(128) NULL AFTER staged_node_token_ciphertext;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_activation_token_hash CHAR(64) NULL AFTER staged_node_token_nonce;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS staged_node_token_at DATETIME(6) NULL AFTER staged_node_activation_token_hash;
