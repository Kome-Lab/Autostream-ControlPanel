ALTER TABLE oauth_login_states
  ADD COLUMN IF NOT EXISTS purpose VARCHAR(32) NOT NULL DEFAULT 'login' AFTER provider_type;
