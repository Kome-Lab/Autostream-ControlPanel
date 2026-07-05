ALTER TABLE services ADD COLUMN IF NOT EXISTS description TEXT NULL AFTER service_name;
ALTER TABLE services ADD COLUMN IF NOT EXISTS host VARCHAR(255) NULL AFTER description;
ALTER TABLE services ADD COLUMN IF NOT EXISTS port INT NULL AFTER host;
ALTER TABLE services ADD COLUMN IF NOT EXISTS ssl_enabled BOOLEAN NOT NULL DEFAULT FALSE AFTER port;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_version VARCHAR(80) NOT NULL DEFAULT '' AFTER version;
ALTER TABLE services ADD COLUMN IF NOT EXISTS last_reported_at DATETIME NULL AFTER last_heartbeat_at;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_capabilities JSON NULL AFTER capabilities;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_hostname VARCHAR(255) NOT NULL DEFAULT '' AFTER reported_capabilities;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_os VARCHAR(80) NOT NULL DEFAULT '' AFTER reported_hostname;
ALTER TABLE services ADD COLUMN IF NOT EXISTS reported_arch VARCHAR(80) NOT NULL DEFAULT '' AFTER reported_os;
ALTER TABLE services ADD COLUMN IF NOT EXISTS node_token_ciphertext TEXT NULL AFTER token_id;
ALTER TABLE services ADD COLUMN IF NOT EXISTS node_token_nonce VARCHAR(128) NULL AFTER node_token_ciphertext;
ALTER TABLE services ADD COLUMN IF NOT EXISTS configure_token_hash CHAR(64) NULL AFTER node_token_nonce;
ALTER TABLE services ADD COLUMN IF NOT EXISTS configure_token_expires_at DATETIME NULL AFTER configure_token_hash;
ALTER TABLE services ADD COLUMN IF NOT EXISTS configure_token_used_at DATETIME NULL AFTER configure_token_expires_at;
ALTER TABLE services ADD COLUMN IF NOT EXISTS node_token_rotated_at DATETIME NULL AFTER configure_token_used_at;

UPDATE services SET reported_capabilities = capabilities WHERE reported_capabilities IS NULL;
