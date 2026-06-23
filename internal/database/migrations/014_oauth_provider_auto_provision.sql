ALTER TABLE oauth_providers
  ADD COLUMN IF NOT EXISTS auto_provision BOOLEAN NOT NULL DEFAULT false AFTER allowed_domains,
  ADD COLUMN IF NOT EXISTS default_role_ids JSON NULL AFTER auto_provision;
