ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS refresh_token_updated_at DATETIME NULL AFTER token_fingerprint;
