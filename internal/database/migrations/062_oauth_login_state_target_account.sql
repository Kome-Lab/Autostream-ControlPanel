ALTER TABLE oauth_login_states
  ADD COLUMN IF NOT EXISTS target_account_id CHAR(36) NULL AFTER account_label;
