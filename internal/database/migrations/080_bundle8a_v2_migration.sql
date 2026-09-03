-- Execution Bundle 8A: additive, replay-safe compatibility data migration.
-- No compatibility column, route, binary, unit, environment key, or fixture is
-- removed here.  Backup tables intentionally retain encrypted values inside
-- MariaDB; proof queries expose counts only.

CREATE TABLE IF NOT EXISTS v2_migration_drive_destinations_backup (
  id CHAR(36) PRIMARY KEY,
  base_path VARCHAR(512) NOT NULL,
  folder_id_fingerprint VARCHAR(32) NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_drive_destinations_backup
  (id, base_path, folder_id_fingerprint, source_fingerprint)
SELECT id, base_path, folder_id_fingerprint,
       SHA2(CONCAT_WS('|', id, base_path, folder_id_fingerprint), 256)
FROM drive_destinations;

CREATE TABLE IF NOT EXISTS v2_migration_archive_artifacts_backup (
  artifact_id CHAR(36) PRIMARY KEY,
  archive_run_id VARCHAR(128) NOT NULL,
  archive_started_at DATETIME(6) NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_archive_artifacts_backup
  (artifact_id, archive_run_id, archive_started_at, source_fingerprint)
SELECT id, archive_run_id, archive_started_at,
       SHA2(CONCAT_WS('|', id, archive_run_id, COALESCE(DATE_FORMAT(archive_started_at, '%Y-%m-%dT%H:%i:%s.%f'), '')), 256)
FROM stream_artifacts
WHERE archive_run_id = '' OR archive_started_at IS NULL;

UPDATE stream_artifacts AS artifact
JOIN v2_migration_archive_artifacts_backup AS backup ON backup.artifact_id = artifact.id
SET artifact.archive_run_id = CASE
      WHEN artifact.archive_run_id = '' THEN CONCAT('legacy-', LOWER(REPLACE(artifact.stream_id, '-', '')))
      ELSE artifact.archive_run_id
    END,
    artifact.archive_started_at = COALESCE(artifact.archive_started_at, artifact.created_at)
WHERE artifact.archive_run_id = '' OR artifact.archive_started_at IS NULL;

CREATE TABLE IF NOT EXISTS v2_migration_stream_key_refs_backup (
  stream_id CHAR(36) PRIMARY KEY,
  stream_key_secret_name VARCHAR(160) NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_stream_key_refs_backup
  (stream_id, stream_key_secret_name, source_fingerprint)
SELECT stream_id, stream_key_secret_name,
       SHA2(CONCAT_WS('|', stream_id, stream_key_secret_name), 256)
FROM stream_youtube_runtimes
WHERE TRIM(stream_key_secret_name) <> '';

CREATE TABLE IF NOT EXISTS v2_migration_service_tokens_backup (
  token_id CHAR(36) PRIMARY KEY,
  service_type VARCHAR(32) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  scopes JSON NOT NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_service_tokens_backup
  (token_id, service_type, token_hash, scopes, revoked_at, created_at, source_fingerprint)
SELECT id, service_type, token_hash, scopes, revoked_at, created_at,
       SHA2(CONCAT_WS('|', id, service_type, token_hash, JSON_COMPACT(scopes),
         COALESCE(DATE_FORMAT(revoked_at, '%Y-%m-%dT%H:%i:%s.%f'), '')),
         256)
FROM service_tokens
WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS v2_migration_oauth_accounts_backup (
  id CHAR(36) PRIMARY KEY,
  provider_id CHAR(36) NOT NULL,
  provider_type VARCHAR(32) NOT NULL,
  account_label VARCHAR(255) NOT NULL,
  subject VARCHAR(255) NULL,
  email VARCHAR(255) NULL,
  scopes JSON NOT NULL,
  refresh_token_ciphertext TEXT NULL,
  refresh_token_nonce VARCHAR(64) NULL,
  token_fingerprint VARCHAR(32) NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_oauth_accounts_backup
  (id, provider_id, provider_type, account_label, subject, email, scopes,
   refresh_token_ciphertext, refresh_token_nonce, token_fingerprint,
   created_at, updated_at, source_fingerprint)
SELECT id, provider_id, provider_type, account_label, subject, email, scopes,
       refresh_token_ciphertext, refresh_token_nonce, token_fingerprint,
       created_at, updated_at,
       SHA2(CONCAT_WS('|', id, provider_id, provider_type, account_label,
         COALESCE(subject, ''), COALESCE(email, ''), JSON_COMPACT(scopes),
         COALESCE(refresh_token_ciphertext, ''), COALESCE(refresh_token_nonce, ''),
         COALESCE(token_fingerprint, '')), 256)
FROM oauth_accounts;

CREATE TABLE IF NOT EXISTS v2_migration_discord_targets_backup (
  stream_id CHAR(36) PRIMARY KEY,
  settings_target_mode VARCHAR(16) NULL,
  settings_target_preset_id CHAR(36) NULL,
  settings_target_preset_revision BIGINT UNSIGNED NULL,
  discord_guild_id VARCHAR(255) NULL,
  discord_text_channel_id VARCHAR(255) NULL,
  discord_voice_channel_id VARCHAR(255) NULL,
  visual_row_existed BOOLEAN NOT NULL,
  visual_target_mode VARCHAR(16) NULL,
  visual_target_preset_id CHAR(36) NULL,
  visual_target_preset_revision BIGINT UNSIGNED NULL,
  visual_guild_id VARCHAR(32) NULL,
  visual_text_channel_id VARCHAR(32) NULL,
  visual_voice_channel_id VARCHAR(32) NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_discord_targets_backup
  (stream_id, settings_target_mode, settings_target_preset_id,
   settings_target_preset_revision, discord_guild_id,
   discord_text_channel_id, discord_voice_channel_id, visual_row_existed,
   visual_target_mode, visual_target_preset_id, visual_target_preset_revision,
   visual_guild_id, visual_text_channel_id, visual_voice_channel_id,
   source_fingerprint)
SELECT settings.stream_id, settings.discord_target_mode,
       settings.discord_target_preset_id, settings.discord_target_preset_revision,
       settings.discord_guild_id, settings.discord_text_channel_id,
       settings.discord_voice_channel_id,
       IF(visual.stream_id IS NULL, FALSE, TRUE), visual.discord_target_mode,
       visual.discord_target_preset_id, visual.discord_target_preset_revision,
       visual.discord_guild_id, visual.discord_text_channel_id,
       visual.discord_voice_channel_id,
       SHA2(CONCAT_WS('|', settings.stream_id,
         COALESCE(settings.discord_target_mode, ''),
         COALESCE(settings.discord_target_preset_id, ''),
         COALESCE(settings.discord_target_preset_revision, 0),
         COALESCE(settings.discord_guild_id, ''),
         COALESCE(settings.discord_text_channel_id, ''),
         COALESCE(settings.discord_voice_channel_id, ''),
         IF(visual.stream_id IS NULL, 0, 1),
         COALESCE(visual.discord_target_mode, ''),
         COALESCE(visual.discord_target_preset_id, ''),
         COALESCE(visual.discord_target_preset_revision, 0),
         COALESCE(visual.discord_guild_id, ''),
         COALESCE(visual.discord_text_channel_id, ''),
         COALESCE(visual.discord_voice_channel_id, '')), 256)
FROM stream_settings AS settings
LEFT JOIN stream_visual_settings AS visual ON visual.stream_id = settings.stream_id
WHERE settings.discord_target_mode IS NULL;

INSERT IGNORE INTO stream_visual_settings
  (stream_id, discord_target_mode, discord_guild_id,
   discord_text_channel_id, discord_voice_channel_id,
   created_at, updated_at)
SELECT settings.stream_id,
       CASE
         WHEN COALESCE(TRIM(settings.discord_guild_id), '') = ''
          AND COALESCE(TRIM(settings.discord_text_channel_id), '') = ''
          AND COALESCE(TRIM(settings.discord_voice_channel_id), '') = '' THEN 'inherit'
         ELSE 'manual'
       END,
       NULLIF(TRIM(settings.discord_guild_id), ''),
       NULLIF(TRIM(settings.discord_text_channel_id), ''),
       NULLIF(TRIM(settings.discord_voice_channel_id), ''),
       settings.updated_at, settings.updated_at
FROM stream_settings AS settings
LEFT JOIN stream_visual_settings AS visual ON visual.stream_id = settings.stream_id
WHERE settings.discord_target_mode IS NULL
  AND visual.stream_id IS NULL
  AND (
    (
      COALESCE(TRIM(settings.discord_guild_id), '') = ''
      AND COALESCE(TRIM(settings.discord_text_channel_id), '') = ''
      AND COALESCE(TRIM(settings.discord_voice_channel_id), '') = ''
    ) OR (
      settings.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND settings.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND settings.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
    )
  );

UPDATE stream_visual_settings AS visual
JOIN stream_settings AS settings ON settings.stream_id = visual.stream_id
SET visual.discord_target_mode = CASE
      WHEN COALESCE(TRIM(settings.discord_guild_id), '') = ''
       AND COALESCE(TRIM(settings.discord_text_channel_id), '') = ''
       AND COALESCE(TRIM(settings.discord_voice_channel_id), '') = '' THEN 'inherit'
      ELSE 'manual'
    END,
    visual.discord_guild_id = NULLIF(TRIM(settings.discord_guild_id), ''),
    visual.discord_text_channel_id = NULLIF(TRIM(settings.discord_text_channel_id), ''),
    visual.discord_voice_channel_id = NULLIF(TRIM(settings.discord_voice_channel_id), ''),
    visual.updated_at = settings.updated_at
WHERE settings.discord_target_mode IS NULL
  AND visual.discord_target_mode IS NULL
  AND (
    (
      COALESCE(TRIM(settings.discord_guild_id), '') = ''
      AND COALESCE(TRIM(settings.discord_text_channel_id), '') = ''
      AND COALESCE(TRIM(settings.discord_voice_channel_id), '') = ''
    ) OR (
      settings.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND settings.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND settings.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
    )
  );

UPDATE stream_settings AS settings
JOIN stream_visual_settings AS visual ON visual.stream_id = settings.stream_id
SET settings.discord_target_mode = visual.discord_target_mode,
    settings.discord_target_preset_id = visual.discord_target_preset_id,
    settings.discord_target_preset_revision = visual.discord_target_preset_revision
WHERE settings.discord_target_mode IS NULL
  AND visual.discord_target_mode IS NOT NULL;

CREATE TABLE IF NOT EXISTS v2_migration_update_hosts_backup (
  execution_host_id VARCHAR(191) PRIMARY KEY,
  transport_mode VARCHAR(16) NOT NULL,
  agent_service_id VARCHAR(128) NOT NULL,
  legacy_agent_service_id VARCHAR(128) NULL,
  ownership_epoch BIGINT NOT NULL,
  policy_revision BIGINT NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  backed_up_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_update_hosts_backup
  (execution_host_id, transport_mode, agent_service_id,
   legacy_agent_service_id, ownership_epoch, policy_revision, source_fingerprint)
SELECT execution_host_id, transport_mode, agent_service_id,
       legacy_agent_service_id, ownership_epoch, policy_revision,
       SHA2(CONCAT_WS('|', execution_host_id, transport_mode, agent_service_id,
         COALESCE(legacy_agent_service_id, ''), ownership_epoch, policy_revision), 256)
FROM system_update_execution_hosts;

CREATE TABLE IF NOT EXISTS v2_migration_legacy_agent_export (
  execution_host_id VARCHAR(191) PRIMARY KEY,
  legacy_agent_service_id VARCHAR(128) NOT NULL,
  source_fingerprint CHAR(64) NOT NULL,
  exported_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
);

INSERT IGNORE INTO v2_migration_legacy_agent_export
  (execution_host_id, legacy_agent_service_id, source_fingerprint)
SELECT execution_host_id, legacy_agent_service_id,
       SHA2(CONCAT_WS('|', execution_host_id, legacy_agent_service_id), 256)
FROM system_update_execution_hosts
WHERE legacy_agent_service_id IS NOT NULL;

CREATE OR REPLACE VIEW v2_migration_bundle8a_counts AS
SELECT 'DEP-CON-0001' AS inventory_id,
       (SELECT COUNT(*) FROM v2_migration_drive_destinations_backup) AS pre_count,
       (SELECT COUNT(*) FROM v2_migration_drive_destinations_backup) AS backup_count,
       (SELECT COUNT(*) FROM v2_migration_drive_destinations_backup AS backup
          JOIN drive_destinations AS current ON current.id = backup.id
         WHERE TRIM(current.folder_id_fingerprint) <> '') AS post_count,
       (SELECT COUNT(*) FROM v2_migration_drive_destinations_backup AS backup
          LEFT JOIN drive_destinations AS current ON current.id = backup.id
         WHERE current.id IS NULL OR TRIM(COALESCE(current.folder_id_fingerprint, '')) = '') AS orphan_count
UNION ALL
SELECT 'DEP-CON-0003',
       (SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup),
       (SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup),
       (SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup AS backup
          JOIN stream_artifacts AS current ON current.id = backup.artifact_id
         WHERE current.archive_run_id <> '' AND current.archive_started_at IS NOT NULL),
       (SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup AS backup
          LEFT JOIN stream_artifacts AS current ON current.id = backup.artifact_id
         WHERE current.id IS NULL OR current.archive_run_id = '' OR current.archive_started_at IS NULL)
UNION ALL
SELECT 'DEP-CON-0005',
       (SELECT COUNT(*) FROM v2_migration_stream_key_refs_backup),
       (SELECT COUNT(*) FROM v2_migration_stream_key_refs_backup),
       (SELECT COUNT(*) FROM v2_migration_stream_key_refs_backup AS backup
          JOIN stream_youtube_runtimes AS current ON current.stream_id = backup.stream_id
         WHERE current.stream_key_secret_name = backup.stream_key_secret_name),
       (SELECT COUNT(*) FROM v2_migration_stream_key_refs_backup AS backup
          LEFT JOIN stream_youtube_runtimes AS current ON current.stream_id = backup.stream_id
         WHERE current.stream_id IS NULL OR current.stream_key_secret_name <> backup.stream_key_secret_name)
UNION ALL
SELECT 'DEP-CON-0012',
       (SELECT COUNT(*) FROM v2_migration_oauth_accounts_backup),
       (SELECT COUNT(*) FROM v2_migration_oauth_accounts_backup),
       (SELECT COUNT(*) FROM v2_migration_oauth_accounts_backup AS backup
          JOIN oauth_accounts AS current ON current.id = backup.id),
       (SELECT COUNT(*) FROM v2_migration_oauth_accounts_backup AS backup
          LEFT JOIN oauth_accounts AS current ON current.id = backup.id
         WHERE current.id IS NULL)
UNION ALL
SELECT 'DEP-CP-0005',
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup AS backup
          JOIN stream_settings AS settings ON settings.stream_id = backup.stream_id
          JOIN stream_visual_settings AS visual ON visual.stream_id = backup.stream_id
         WHERE settings.discord_target_mode = visual.discord_target_mode
           AND (
             (backup.visual_row_existed = TRUE
               AND backup.visual_target_mode IS NOT NULL
               AND visual.discord_target_mode = backup.visual_target_mode
               AND visual.discord_target_preset_id <=> backup.visual_target_preset_id
               AND visual.discord_target_preset_revision <=> backup.visual_target_preset_revision
               AND visual.discord_guild_id <=> backup.visual_guild_id
               AND visual.discord_text_channel_id <=> backup.visual_text_channel_id
               AND visual.discord_voice_channel_id <=> backup.visual_voice_channel_id)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND COALESCE(TRIM(backup.discord_guild_id), '') = ''
               AND COALESCE(TRIM(backup.discord_text_channel_id), '') = ''
               AND COALESCE(TRIM(backup.discord_voice_channel_id), '') = ''
               AND visual.discord_target_mode = 'inherit'
               AND visual.discord_guild_id IS NULL
               AND visual.discord_text_channel_id IS NULL
               AND visual.discord_voice_channel_id IS NULL)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND backup.discord_guild_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
               AND visual.discord_target_mode = 'manual'
               AND visual.discord_guild_id = TRIM(backup.discord_guild_id)
               AND visual.discord_text_channel_id = TRIM(backup.discord_text_channel_id)
               AND visual.discord_voice_channel_id = TRIM(backup.discord_voice_channel_id))
           )),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup AS backup
          LEFT JOIN stream_settings AS settings ON settings.stream_id = backup.stream_id
          LEFT JOIN stream_visual_settings AS visual ON visual.stream_id = backup.stream_id
         WHERE settings.stream_id IS NULL OR visual.stream_id IS NULL
            OR settings.discord_target_mode IS NULL OR visual.discord_target_mode IS NULL OR NOT (
           settings.discord_target_mode = visual.discord_target_mode
           AND (
             (backup.visual_row_existed = TRUE
               AND backup.visual_target_mode IS NOT NULL
               AND visual.discord_target_mode = backup.visual_target_mode
               AND visual.discord_target_preset_id <=> backup.visual_target_preset_id
               AND visual.discord_target_preset_revision <=> backup.visual_target_preset_revision
               AND visual.discord_guild_id <=> backup.visual_guild_id
               AND visual.discord_text_channel_id <=> backup.visual_text_channel_id
               AND visual.discord_voice_channel_id <=> backup.visual_voice_channel_id)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND COALESCE(TRIM(backup.discord_guild_id), '') = ''
               AND COALESCE(TRIM(backup.discord_text_channel_id), '') = ''
               AND COALESCE(TRIM(backup.discord_voice_channel_id), '') = ''
               AND visual.discord_target_mode = 'inherit'
               AND visual.discord_guild_id IS NULL
               AND visual.discord_text_channel_id IS NULL
               AND visual.discord_voice_channel_id IS NULL)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND backup.discord_guild_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
               AND visual.discord_target_mode = 'manual'
               AND visual.discord_guild_id = TRIM(backup.discord_guild_id)
               AND visual.discord_text_channel_id = TRIM(backup.discord_text_channel_id)
               AND visual.discord_voice_channel_id = TRIM(backup.discord_voice_channel_id))
           )))
UNION ALL
SELECT 'DEP-CP-0006',
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup AS backup
          JOIN stream_settings AS settings ON settings.stream_id = backup.stream_id
          JOIN stream_visual_settings AS visual ON visual.stream_id = backup.stream_id
         WHERE settings.discord_target_mode = visual.discord_target_mode
           AND (
             (backup.visual_row_existed = TRUE
               AND backup.visual_target_mode IS NOT NULL
               AND visual.discord_target_mode = backup.visual_target_mode
               AND visual.discord_target_preset_id <=> backup.visual_target_preset_id
               AND visual.discord_target_preset_revision <=> backup.visual_target_preset_revision
               AND visual.discord_guild_id <=> backup.visual_guild_id
               AND visual.discord_text_channel_id <=> backup.visual_text_channel_id
               AND visual.discord_voice_channel_id <=> backup.visual_voice_channel_id)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND COALESCE(TRIM(backup.discord_guild_id), '') = ''
               AND COALESCE(TRIM(backup.discord_text_channel_id), '') = ''
               AND COALESCE(TRIM(backup.discord_voice_channel_id), '') = ''
               AND visual.discord_target_mode = 'inherit'
               AND visual.discord_guild_id IS NULL
               AND visual.discord_text_channel_id IS NULL
               AND visual.discord_voice_channel_id IS NULL)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND backup.discord_guild_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
               AND visual.discord_target_mode = 'manual'
               AND visual.discord_guild_id = TRIM(backup.discord_guild_id)
               AND visual.discord_text_channel_id = TRIM(backup.discord_text_channel_id)
               AND visual.discord_voice_channel_id = TRIM(backup.discord_voice_channel_id))
           )),
       (SELECT COUNT(*) FROM v2_migration_discord_targets_backup AS backup
          LEFT JOIN stream_settings AS settings ON settings.stream_id = backup.stream_id
          LEFT JOIN stream_visual_settings AS visual ON visual.stream_id = backup.stream_id
         WHERE settings.stream_id IS NULL OR visual.stream_id IS NULL
            OR settings.discord_target_mode IS NULL OR visual.discord_target_mode IS NULL OR NOT (
           settings.discord_target_mode = visual.discord_target_mode
           AND (
             (backup.visual_row_existed = TRUE
               AND backup.visual_target_mode IS NOT NULL
               AND visual.discord_target_mode = backup.visual_target_mode
               AND visual.discord_target_preset_id <=> backup.visual_target_preset_id
               AND visual.discord_target_preset_revision <=> backup.visual_target_preset_revision
               AND visual.discord_guild_id <=> backup.visual_guild_id
               AND visual.discord_text_channel_id <=> backup.visual_text_channel_id
               AND visual.discord_voice_channel_id <=> backup.visual_voice_channel_id)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND COALESCE(TRIM(backup.discord_guild_id), '') = ''
               AND COALESCE(TRIM(backup.discord_text_channel_id), '') = ''
               AND COALESCE(TRIM(backup.discord_voice_channel_id), '') = ''
               AND visual.discord_target_mode = 'inherit'
               AND visual.discord_guild_id IS NULL
               AND visual.discord_text_channel_id IS NULL
               AND visual.discord_voice_channel_id IS NULL)
             OR ((backup.visual_row_existed = FALSE OR backup.visual_target_mode IS NULL)
               AND backup.discord_guild_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
               AND backup.discord_voice_channel_id REGEXP '^[0-9]{1,32}$'
               AND visual.discord_target_mode = 'manual'
               AND visual.discord_guild_id = TRIM(backup.discord_guild_id)
               AND visual.discord_text_channel_id = TRIM(backup.discord_text_channel_id)
               AND visual.discord_voice_channel_id = TRIM(backup.discord_voice_channel_id))
           )))
UNION ALL
SELECT 'DEP-CP-0007',
       (SELECT COUNT(*) FROM v2_migration_service_tokens_backup),
       (SELECT COUNT(*) FROM v2_migration_service_tokens_backup),
       (SELECT COUNT(*) FROM v2_migration_service_tokens_backup AS backup
          JOIN service_tokens AS current ON current.id = backup.token_id
         WHERE current.token_hash = backup.token_hash),
       (SELECT COUNT(*) FROM v2_migration_service_tokens_backup AS backup
          LEFT JOIN service_tokens AS current ON current.id = backup.token_id
         WHERE current.id IS NULL OR current.token_hash <> backup.token_hash)
UNION ALL
SELECT 'DEP-CP-0017',
       (SELECT COUNT(*) FROM v2_migration_update_hosts_backup),
       (SELECT COUNT(*) FROM v2_migration_update_hosts_backup),
       (SELECT COUNT(*) FROM v2_migration_update_hosts_backup AS backup
          JOIN system_update_execution_hosts AS current
            ON current.execution_host_id = backup.execution_host_id),
       (SELECT COUNT(*) FROM v2_migration_update_hosts_backup AS backup
          LEFT JOIN system_update_execution_hosts AS current
            ON current.execution_host_id = backup.execution_host_id
         WHERE current.execution_host_id IS NULL)
UNION ALL
SELECT 'DEP-CP-0030',
       (SELECT COUNT(*) FROM v2_migration_legacy_agent_export),
       (SELECT COUNT(*) FROM v2_migration_legacy_agent_export),
       (SELECT COUNT(*) FROM v2_migration_legacy_agent_export AS exported
          JOIN system_update_execution_hosts AS current
            ON current.execution_host_id = exported.execution_host_id
         WHERE current.legacy_agent_service_id = exported.legacy_agent_service_id),
       (SELECT COUNT(*) FROM v2_migration_legacy_agent_export AS exported
          LEFT JOIN system_update_execution_hosts AS current
            ON current.execution_host_id = exported.execution_host_id
         WHERE current.execution_host_id IS NULL
            OR current.legacy_agent_service_id <> exported.legacy_agent_service_id);

CREATE TABLE IF NOT EXISTS v2_migration_bundle8a_gate (
  gate_id TINYINT PRIMARY KEY,
  mismatch_count INT NOT NULL,
  verified_at DATETIME(6) NOT NULL,
  CONSTRAINT chk_v2_migration_bundle8a_zero_mismatch CHECK (mismatch_count = 0)
);

INSERT INTO v2_migration_bundle8a_gate (gate_id, mismatch_count, verified_at)
SELECT 1,
       SUM(CASE
         WHEN pre_count <> backup_count OR pre_count <> post_count OR orphan_count <> 0
         THEN 1 ELSE 0 END),
       CURRENT_TIMESTAMP(6)
FROM v2_migration_bundle8a_counts
ON DUPLICATE KEY UPDATE
  mismatch_count = VALUES(mismatch_count),
  verified_at = VALUES(verified_at);
