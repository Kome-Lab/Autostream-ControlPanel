ALTER TABLE oauth_accounts
  ADD COLUMN IF NOT EXISTS access_token_refreshed_at DATETIME NULL AFTER refresh_token_updated_at;
