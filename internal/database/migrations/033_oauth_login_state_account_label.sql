ALTER TABLE oauth_login_states
  ADD COLUMN IF NOT EXISTS account_label VARCHAR(255) NULL AFTER redirect_after;
