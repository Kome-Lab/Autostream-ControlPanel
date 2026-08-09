ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS token_revision BIGINT UNSIGNED NOT NULL DEFAULT 1 AFTER token_fingerprint;

ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS access_token_refresh_attempted_at DATETIME NULL AFTER access_token_refreshed_at;

ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS access_token_refresh_failed_at DATETIME NULL AFTER access_token_refresh_attempted_at;

ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS access_token_refresh_failure_code VARCHAR(64) NULL AFTER access_token_refresh_failed_at;

ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS access_token_refresh_relink_required BOOLEAN NOT NULL DEFAULT FALSE AFTER access_token_refresh_failure_code;
