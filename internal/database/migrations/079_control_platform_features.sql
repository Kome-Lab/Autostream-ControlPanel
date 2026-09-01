-- Execution Bundle 4: additive Control Platform feature authority.
-- This repository uses forward-only migrations. Referenced assets are retained;
-- rollback after first reference requires the matching database/blob backup.

-- Stream Start uses streams.updated_at as its persisted compare-and-swap
-- authority. Preserve every legacy value while widening the column so visual
-- mutations in the same second can still invalidate an already-read Start.
ALTER TABLE streams
  MODIFY COLUMN updated_at DATETIME(6) NOT NULL;

CREATE TABLE IF NOT EXISTS user_ui_preferences (
  user_id CHAR(36) NOT NULL,
  theme_id VARCHAR(32) NOT NULL DEFAULT 'autostream',
  color_mode VARCHAR(16) NOT NULL DEFAULT 'system',
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (user_id),
  CONSTRAINT fk_user_ui_preferences_user
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT chk_user_ui_preferences_mode
    CHECK (color_mode IN ('system','light','dark'))
);

CREATE TABLE IF NOT EXISTS media_upload_sessions (
  id CHAR(36) NOT NULL,
  user_id CHAR(36) NOT NULL,
  owner_type VARCHAR(32) NOT NULL DEFAULT 'upload_draft',
  claimed_stream_id CHAR(36) NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  INDEX idx_media_upload_sessions_expiry (owner_type, expires_at, id),
  INDEX idx_media_upload_sessions_user (user_id, created_at),
  CONSTRAINT fk_media_upload_sessions_user
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_media_upload_sessions_stream
    FOREIGN KEY (claimed_stream_id) REFERENCES streams(id) ON DELETE SET NULL,
  CONSTRAINT chk_media_upload_sessions_owner
    CHECK (owner_type IN ('upload_draft','stream'))
);

CREATE TABLE IF NOT EXISTS media_assets (
  id CHAR(36) NOT NULL,
  owner_user_id CHAR(36) NOT NULL,
  owner_type VARCHAR(32) NOT NULL DEFAULT 'upload_draft',
  owner_id CHAR(36) NOT NULL,
  upload_session_id CHAR(36) NULL,
  usage_type VARCHAR(32) NOT NULL,
  storage_key VARCHAR(255) NOT NULL,
  sha256 CHAR(64) NOT NULL,
  media_type VARCHAR(64) NOT NULL,
  byte_size BIGINT UNSIGNED NOT NULL,
  width INT UNSIGNED NOT NULL,
  height INT UNSIGNED NOT NULL,
  opaque TINYINT(1) NOT NULL DEFAULT 0,
  processor_revision INT UNSIGNED NOT NULL DEFAULT 1,
  deleted_at DATETIME(6) NULL,
  retention_until DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  INDEX idx_media_assets_storage_key (storage_key),
  INDEX idx_media_assets_owner (owner_type, owner_id, deleted_at),
  INDEX idx_media_assets_gc (deleted_at, retention_until, id),
  INDEX idx_media_assets_session (upload_session_id),
  CONSTRAINT fk_media_assets_user
    FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT fk_media_assets_session
    FOREIGN KEY (upload_session_id) REFERENCES media_upload_sessions(id) ON DELETE SET NULL,
  CONSTRAINT chk_media_assets_owner
    CHECK (owner_type IN ('upload_draft','stream','preset')),
  CONSTRAINT chk_media_assets_usage
    CHECK (usage_type IN ('scene_background','video_cover')),
  CONSTRAINT chk_media_assets_dimensions
    CHECK (width BETWEEN 1 AND 8192 AND height BETWEEN 1 AND 8192 AND width * height <= 40000000),
  CONSTRAINT chk_media_assets_byte_size
    CHECK (byte_size BETWEEN 1 AND 20971520)
);

CREATE TABLE IF NOT EXISTS media_asset_variants (
  id CHAR(36) NOT NULL,
  asset_id CHAR(36) NOT NULL,
  target_width INT UNSIGNED NOT NULL,
  target_height INT UNSIGNED NOT NULL,
  crop_mode VARCHAR(32) NOT NULL DEFAULT 'center_crop',
  processor_revision INT UNSIGNED NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'queued',
  storage_key VARCHAR(255) NULL,
  sha256 CHAR(64) NULL,
  media_type VARCHAR(64) NULL,
  byte_size BIGINT UNSIGNED NULL,
  width INT UNSIGNED NULL,
  height INT UNSIGNED NULL,
  opaque TINYINT(1) NOT NULL DEFAULT 0,
  last_error_code VARCHAR(80) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_media_asset_variant_spec
    (asset_id, target_width, target_height, crop_mode, processor_revision),
  INDEX idx_media_asset_variant_storage_key (storage_key),
  INDEX idx_media_asset_variants_status (status, updated_at, id),
  CONSTRAINT fk_media_asset_variants_asset
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
  CONSTRAINT chk_media_asset_variants_status
    CHECK (status IN ('queued','processing','ready','failed')),
  CONSTRAINT chk_media_asset_variants_target
    CHECK (target_width BETWEEN 1 AND 8192 AND target_height BETWEEN 1 AND 8192
      AND target_width * target_height <= 40000000)
);

CREATE TABLE IF NOT EXISTS discord_target_presets (
  id CHAR(36) NOT NULL,
  name VARCHAR(128) NOT NULL,
  deleted_at DATETIME(6) NULL,
  active_name_key VARCHAR(128)
    GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN LOWER(TRIM(name)) ELSE NULL END) STORED,
  guild_id VARCHAR(32) NOT NULL,
  text_channel_id VARCHAR(32) NOT NULL,
  voice_channel_id VARCHAR(32) NOT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_by_user_id CHAR(36) NOT NULL,
  updated_by_user_id CHAR(36) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_discord_target_presets_active_name (active_name_key),
  INDEX idx_discord_target_presets_active (deleted_at, name, id),
  CONSTRAINT fk_discord_target_presets_created_user
    FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT fk_discord_target_presets_updated_user
    FOREIGN KEY (updated_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT chk_discord_target_preset_guild
    CHECK (guild_id REGEXP '^[0-9]{1,32}$'),
  CONSTRAINT chk_discord_target_preset_text
    CHECK (text_channel_id REGEXP '^[0-9]{1,32}$'),
  CONSTRAINT chk_discord_target_preset_voice
    CHECK (voice_channel_id REGEXP '^[0-9]{1,32}$')
);

ALTER TABLE stream_settings
  ADD COLUMN IF NOT EXISTS discord_target_mode VARCHAR(16) NULL
    CHECK (discord_target_mode IS NULL OR discord_target_mode IN ('inherit','preset','manual'))
    AFTER discord_text_channel_id,
  ADD COLUMN IF NOT EXISTS discord_target_preset_id CHAR(36) NULL AFTER discord_target_mode,
  ADD COLUMN IF NOT EXISTS discord_target_preset_revision BIGINT UNSIGNED NULL AFTER discord_target_preset_id;

CREATE INDEX IF NOT EXISTS idx_stream_settings_discord_target_preset
  ON stream_settings (discord_target_preset_id);

ALTER TABLE stream_settings
  ADD CONSTRAINT fk_stream_settings_discord_target_preset
    FOREIGN KEY IF NOT EXISTS (discord_target_preset_id)
      REFERENCES discord_target_presets(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS video_cover_presets (
  id CHAR(36) NOT NULL,
  name VARCHAR(128) NOT NULL,
  deleted_at DATETIME(6) NULL,
  active_name_key VARCHAR(128)
    GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN LOWER(TRIM(name)) ELSE NULL END) STORED,
  asset_id CHAR(36) NOT NULL,
  asset_variant_id CHAR(36) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  system_preset TINYINT(1) NOT NULL DEFAULT 0,
  release_key VARCHAR(128) NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_by_user_id CHAR(36) NULL,
  updated_by_user_id CHAR(36) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_video_cover_presets_active_name (active_name_key),
  UNIQUE KEY uniq_video_cover_presets_release_key (release_key),
  INDEX idx_video_cover_presets_active (deleted_at, enabled, name, id),
  CONSTRAINT fk_video_cover_presets_asset
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE RESTRICT,
  CONSTRAINT fk_video_cover_presets_variant
    FOREIGN KEY (asset_variant_id) REFERENCES media_asset_variants(id) ON DELETE RESTRICT,
  CONSTRAINT fk_video_cover_presets_created_user
    FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_video_cover_presets_updated_user
    FOREIGN KEY (updated_by_user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS stream_visual_settings (
  stream_id CHAR(36) NOT NULL,
  background_mode VARCHAR(16) NOT NULL DEFAULT 'default',
  background_asset_id CHAR(36) NULL,
  background_variant_id CHAR(36) NULL,
  header_title_mode VARCHAR(16) NOT NULL DEFAULT 'default',
  header_title_value VARCHAR(80) NULL,
  discord_target_mode VARCHAR(16) NULL,
  discord_target_preset_id CHAR(36) NULL,
  discord_target_preset_revision BIGINT UNSIGNED NULL,
  discord_snapshot_revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  discord_guild_id VARCHAR(32) NULL,
  discord_text_channel_id VARCHAR(32) NULL,
  discord_voice_channel_id VARCHAR(32) NULL,
  cover_source VARCHAR(16) NOT NULL DEFAULT 'none',
  cover_preset_id CHAR(36) NULL,
  cover_preset_revision BIGINT UNSIGNED NULL,
  cover_asset_id CHAR(36) NULL,
  cover_variant_id CHAR(36) NULL,
  cover_start_active TINYINT(1) NOT NULL DEFAULT 0,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id),
  INDEX idx_stream_visual_background_asset (background_asset_id, background_variant_id),
  INDEX idx_stream_visual_cover_asset (cover_asset_id, cover_variant_id),
  INDEX idx_stream_visual_discord_preset (discord_target_preset_id),
  CONSTRAINT fk_stream_visual_stream
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE,
  CONSTRAINT fk_stream_visual_background_asset
    FOREIGN KEY (background_asset_id) REFERENCES media_assets(id) ON DELETE RESTRICT,
  CONSTRAINT fk_stream_visual_background_variant
    FOREIGN KEY (background_variant_id) REFERENCES media_asset_variants(id) ON DELETE RESTRICT,
  CONSTRAINT fk_stream_visual_discord_preset
    FOREIGN KEY (discord_target_preset_id) REFERENCES discord_target_presets(id) ON DELETE SET NULL,
  CONSTRAINT fk_stream_visual_cover_preset
    FOREIGN KEY (cover_preset_id) REFERENCES video_cover_presets(id) ON DELETE SET NULL,
  CONSTRAINT fk_stream_visual_cover_asset
    FOREIGN KEY (cover_asset_id) REFERENCES media_assets(id) ON DELETE RESTRICT,
  CONSTRAINT fk_stream_visual_cover_variant
    FOREIGN KEY (cover_variant_id) REFERENCES media_asset_variants(id) ON DELETE RESTRICT,
  CONSTRAINT chk_stream_visual_background_mode
    CHECK (background_mode IN ('default','image')),
  CONSTRAINT chk_stream_visual_header_mode
    CHECK (header_title_mode IN ('default','custom')),
  CONSTRAINT chk_stream_visual_discord_mode
    CHECK (discord_target_mode IS NULL OR discord_target_mode IN ('inherit','preset','manual')),
  CONSTRAINT chk_stream_visual_cover_source
    CHECK (cover_source IN ('none','preset','upload'))
);

CREATE TABLE IF NOT EXISTS stream_video_cover_runtime (
  stream_id CHAR(36) NOT NULL,
  job_generation BIGINT UNSIGNED NOT NULL,
  desired_active TINYINT(1) NOT NULL DEFAULT 0,
  desired_revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  applied_active TINYINT(1) NULL,
  applied_revision BIGINT UNSIGNED NULL,
  asset_variant_id CHAR(36) NULL,
  last_error_code VARCHAR(80) NULL,
  reconciliation_status VARCHAR(24) NOT NULL DEFAULT 'idle',
  last_idempotency_key VARCHAR(128) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id, job_generation),
  INDEX idx_stream_video_cover_current (stream_id, job_generation, desired_revision),
  INDEX idx_stream_video_cover_variant (asset_variant_id),
  CONSTRAINT fk_stream_video_cover_runtime_stream
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE,
  CONSTRAINT fk_stream_video_cover_runtime_variant
    FOREIGN KEY (asset_variant_id) REFERENCES media_asset_variants(id) ON DELETE RESTRICT,
  CONSTRAINT chk_stream_video_cover_applied_pair
    CHECK ((applied_active IS NULL AND applied_revision IS NULL)
      OR (applied_active IS NOT NULL AND applied_revision IS NOT NULL)),
  CONSTRAINT chk_stream_video_cover_reconciliation
    CHECK (reconciliation_status IN ('idle','confirming','applied','failed'))
);

CREATE TABLE IF NOT EXISTS stream_video_cover_actions (
  stream_id CHAR(36) NOT NULL,
  job_generation BIGINT UNSIGNED NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  requested_active TINYINT(1) NOT NULL,
  requested_revision BIGINT UNSIGNED NOT NULL,
  request_fingerprint CHAR(64) NOT NULL,
  result_status VARCHAR(24) NOT NULL,
  safe_error_code VARCHAR(80) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stream_id, job_generation, idempotency_key),
  UNIQUE KEY uniq_stream_video_cover_action_revision
    (stream_id, job_generation, requested_revision),
  CONSTRAINT fk_stream_video_cover_action_runtime
    FOREIGN KEY (stream_id, job_generation)
      REFERENCES stream_video_cover_runtime(stream_id, job_generation) ON DELETE CASCADE,
  CONSTRAINT chk_stream_video_cover_action_status
    CHECK (result_status IN ('pending','confirming','applied','failed'))
);

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'discord_target_presets.read' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'discord_target_presets.create' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'discord_target_presets.update' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'discord_target_presets.delete' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'video_cover_presets.read' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'video_cover_presets.create' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'video_cover_presets.update' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'video_cover_presets.delete' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'streams.show_cover' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'streams.hide_cover' FROM roles WHERE name = 'super_admin';
