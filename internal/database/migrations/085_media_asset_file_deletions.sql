-- Durable, bounded retry state for content-addressed original/variant removal.
-- No existing asset is selected or deleted by this additive migration.
CREATE TABLE IF NOT EXISTS media_asset_file_deletions (
  storage_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  last_error_code VARCHAR(80) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (storage_key),
  INDEX idx_media_asset_file_deletions_retry (updated_at, storage_key)
);
