ALTER TABLE oauth_login_states
  ADD COLUMN IF NOT EXISTS requested_scopes TEXT NULL AFTER redirect_after;
