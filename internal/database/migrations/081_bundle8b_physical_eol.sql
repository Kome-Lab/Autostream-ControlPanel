-- Execution Bundle 8B consumes the immutable 8A backups and revalidates the
-- current source rows immediately before physical EOL. Retained-data columns
-- stay in place; runtime code no longer reads or writes them.

-- base_path is retained as archived data. New v2 writes omit it, so the
-- database supplies an empty inert value without exposing a runtime fallback.
ALTER TABLE drive_destinations
  MODIFY COLUMN base_path VARCHAR(512) NOT NULL DEFAULT '';

-- Migration 080 assigned archive identity but deliberately did not move the
-- filesystem. Preserve the original path as rollback evidence, then make the
-- database path exactly match Encoder's physical migration convention.
ALTER TABLE v2_migration_archive_artifacts_backup
  ADD COLUMN IF NOT EXISTS relative_path TEXT NULL AFTER archive_started_at,
  ADD COLUMN IF NOT EXISTS relative_path_fingerprint CHAR(64) NULL AFTER relative_path;

-- Encoder migrates only direct regular-file children into
-- final/<canonical-stream-uuid>/legacy-<uuid-without-hyphens>/<name>. Reject
-- database rows that cannot name that exact physical source or that would
-- collapse onto one destination before changing any artifact metadata.
CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_artifact_gate (
  gate_id TINYINT PRIMARY KEY,
  mismatch_count INT NOT NULL,
  verified_at DATETIME(6) NOT NULL,
  CONSTRAINT chk_v2_migration_bundle8b_artifact_zero_mismatch CHECK (mismatch_count = 0)
);

INSERT INTO v2_migration_bundle8b_artifact_gate (gate_id, mismatch_count, verified_at)
SELECT 1,
       (
         SELECT COUNT(*)
         FROM stream_artifacts AS current
         JOIN v2_migration_archive_artifacts_backup AS backup
           ON backup.artifact_id = current.id
         WHERE LOWER(TRIM(current.stream_id)) NOT REGEXP
                 '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            OR TRIM(current.name) = ''
            OR TRIM(current.name) IN ('.', '..')
            OR INSTR(current.name, '/') > 0
            OR INSTR(current.name, CHAR(92)) > 0
            OR current.name REGEXP '[[:cntrl:]]'
       ) + (
         SELECT COALESCE(SUM(destination_count - 1), 0)
         FROM (
           SELECT COUNT(*) AS destination_count
           FROM stream_artifacts AS current
           JOIN v2_migration_archive_artifacts_backup AS backup
             ON backup.artifact_id = current.id
           GROUP BY LOWER(CONCAT(
             'final/', current.stream_id, '/legacy-',
             REPLACE(LOWER(TRIM(current.stream_id)), '-', ''), '/', current.name
           ))
           HAVING COUNT(*) > 1
         ) AS collisions
       ),
       CURRENT_TIMESTAMP(6)
ON DUPLICATE KEY UPDATE
  mismatch_count = VALUES(mismatch_count),
  verified_at = VALUES(verified_at);

UPDATE v2_migration_archive_artifacts_backup AS backup
JOIN stream_artifacts AS current ON current.id = backup.artifact_id
SET backup.relative_path = COALESCE(backup.relative_path, current.relative_path),
    backup.relative_path_fingerprint = COALESCE(
      backup.relative_path_fingerprint,
      SHA2(current.relative_path, 256)
    );

UPDATE stream_artifacts AS current
JOIN v2_migration_archive_artifacts_backup AS backup ON backup.artifact_id = current.id
SET current.archive_run_id = CONCAT('legacy-', LOWER(REPLACE(current.stream_id, '-', ''))),
    current.archive_started_at = COALESCE(current.archive_started_at, current.created_at),
    current.relative_path = CONCAT(
      'final/', current.stream_id, '/legacy-',
      LOWER(REPLACE(current.stream_id, '-', '')), '/', current.name
    );

-- Migration 080 copied legacy rows that predated normalized target modes. Add
-- any later dual-written rows to the immutable rollback set, then fill only a
-- missing v2 snapshot. A conflicting non-null v2 snapshot is never overwritten
-- and is rejected by the final zero-mismatch gate below.
INSERT IGNORE INTO v2_migration_discord_targets_backup
  (stream_id, settings_target_mode, settings_target_preset_id,
   settings_target_preset_revision, discord_guild_id,
   discord_text_channel_id, discord_voice_channel_id, visual_row_existed,
   visual_target_mode, visual_target_preset_id, visual_target_preset_revision,
   visual_guild_id, visual_text_channel_id, visual_voice_channel_id,
   source_fingerprint)
SELECT legacy.stream_id, legacy.discord_target_mode,
       legacy.discord_target_preset_id, legacy.discord_target_preset_revision,
       legacy.discord_guild_id, legacy.discord_text_channel_id,
       legacy.discord_voice_channel_id,
       IF(v2.stream_id IS NULL, FALSE, TRUE), v2.discord_target_mode,
       v2.discord_target_preset_id, v2.discord_target_preset_revision,
       v2.discord_guild_id, v2.discord_text_channel_id,
       v2.discord_voice_channel_id,
       SHA2(CONCAT_WS('|', legacy.stream_id,
         COALESCE(legacy.discord_target_mode, ''),
         COALESCE(legacy.discord_target_preset_id, ''),
         COALESCE(legacy.discord_target_preset_revision, 0),
         COALESCE(legacy.discord_guild_id, ''),
         COALESCE(legacy.discord_text_channel_id, ''),
         COALESCE(legacy.discord_voice_channel_id, ''),
         IF(v2.stream_id IS NULL, 0, 1),
         COALESCE(v2.discord_target_mode, ''),
         COALESCE(v2.discord_target_preset_id, ''),
         COALESCE(v2.discord_target_preset_revision, 0),
         COALESCE(v2.discord_guild_id, ''),
         COALESCE(v2.discord_text_channel_id, ''),
         COALESCE(v2.discord_voice_channel_id, '')), 256)
FROM stream_settings AS legacy
LEFT JOIN stream_visual_settings AS v2 ON v2.stream_id = legacy.stream_id;

INSERT INTO stream_visual_settings
  (stream_id, discord_target_mode, discord_target_preset_id,
   discord_target_preset_revision, discord_guild_id,
   discord_text_channel_id, discord_voice_channel_id, created_at, updated_at)
SELECT legacy.stream_id, legacy.discord_target_mode,
       legacy.discord_target_preset_id, legacy.discord_target_preset_revision,
       NULLIF(TRIM(legacy.discord_guild_id), ''),
       NULLIF(TRIM(legacy.discord_text_channel_id), ''),
       NULLIF(TRIM(legacy.discord_voice_channel_id), ''),
       legacy.updated_at, legacy.updated_at
FROM stream_settings AS legacy
LEFT JOIN stream_visual_settings AS v2 ON v2.stream_id = legacy.stream_id
WHERE v2.stream_id IS NULL
  AND legacy.discord_target_mode IS NOT NULL
  AND (
    (legacy.discord_target_mode = 'inherit'
      AND legacy.discord_target_preset_id IS NULL
      AND legacy.discord_target_preset_revision IS NULL
      AND COALESCE(TRIM(legacy.discord_guild_id), '') = ''
      AND COALESCE(TRIM(legacy.discord_text_channel_id), '') = ''
      AND COALESCE(TRIM(legacy.discord_voice_channel_id), '') = '')
    OR (legacy.discord_target_mode = 'manual'
      AND legacy.discord_target_preset_id IS NULL
      AND legacy.discord_target_preset_revision IS NULL
      AND legacy.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_voice_channel_id REGEXP '^[0-9]{1,32}$')
    OR (legacy.discord_target_mode = 'preset'
      AND legacy.discord_target_preset_id IS NOT NULL
      AND legacy.discord_target_preset_revision IS NOT NULL
      AND legacy.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_voice_channel_id REGEXP '^[0-9]{1,32}$')
  );

UPDATE stream_visual_settings AS v2
JOIN stream_settings AS legacy ON legacy.stream_id = v2.stream_id
SET v2.discord_target_mode = legacy.discord_target_mode,
    v2.discord_target_preset_id = legacy.discord_target_preset_id,
    v2.discord_target_preset_revision = legacy.discord_target_preset_revision,
    v2.discord_guild_id = NULLIF(TRIM(legacy.discord_guild_id), ''),
    v2.discord_text_channel_id = NULLIF(TRIM(legacy.discord_text_channel_id), ''),
    v2.discord_voice_channel_id = NULLIF(TRIM(legacy.discord_voice_channel_id), ''),
    v2.updated_at = legacy.updated_at
WHERE v2.discord_target_mode IS NULL
  AND legacy.discord_target_mode IS NOT NULL
  AND (
    (legacy.discord_target_mode = 'inherit'
      AND legacy.discord_target_preset_id IS NULL
      AND legacy.discord_target_preset_revision IS NULL
      AND COALESCE(TRIM(legacy.discord_guild_id), '') = ''
      AND COALESCE(TRIM(legacy.discord_text_channel_id), '') = ''
      AND COALESCE(TRIM(legacy.discord_voice_channel_id), '') = '')
    OR (legacy.discord_target_mode = 'manual'
      AND legacy.discord_target_preset_id IS NULL
      AND legacy.discord_target_preset_revision IS NULL
      AND legacy.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_voice_channel_id REGEXP '^[0-9]{1,32}$')
    OR (legacy.discord_target_mode = 'preset'
      AND legacy.discord_target_preset_id IS NOT NULL
      AND legacy.discord_target_preset_revision IS NOT NULL
      AND legacy.discord_guild_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_text_channel_id REGEXP '^[0-9]{1,32}$'
      AND legacy.discord_voice_channel_id REGEXP '^[0-9]{1,32}$')
  );

-- Refresh the export from the current row, rather than trusting the point-in-
-- time 8A INSERT IGNORE snapshot. Historical rows remain retained.
INSERT INTO v2_migration_legacy_agent_export
  (execution_host_id, legacy_agent_service_id, source_fingerprint, exported_at)
SELECT execution_host_id, legacy_agent_service_id,
       SHA2(CONCAT_WS('|', execution_host_id, legacy_agent_service_id), 256),
       CURRENT_TIMESTAMP(6)
FROM system_update_execution_hosts
WHERE legacy_agent_service_id IS NOT NULL
ON DUPLICATE KEY UPDATE
  legacy_agent_service_id = VALUES(legacy_agent_service_id),
  source_fingerprint = VALUES(source_fingerprint),
  exported_at = VALUES(exported_at);

CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_gate (
  gate_id TINYINT PRIMARY KEY,
  mismatch_count INT NOT NULL,
  verified_at DATETIME(6) NOT NULL,
  CONSTRAINT chk_v2_migration_bundle8b_zero_mismatch CHECK (mismatch_count = 0)
);

INSERT INTO v2_migration_bundle8b_gate (gate_id, mismatch_count, verified_at)
SELECT 1,
       (
         CASE WHEN EXISTS (
           SELECT 1
           FROM v2_migration_bundle8a_gate
           WHERE gate_id = 1 AND mismatch_count = 0
         ) THEN 0 ELSE 1 END
       ) + (
         SELECT COUNT(*)
         FROM v2_migration_archive_artifacts_backup AS backup
         LEFT JOIN stream_artifacts AS current ON current.id = backup.artifact_id
         WHERE current.id IS NULL
            OR current.archive_run_id <> CONCAT('legacy-', LOWER(REPLACE(current.stream_id, '-', '')))
            OR current.archive_started_at IS NULL
            OR current.relative_path <> CONCAT(
              'final/', current.stream_id, '/legacy-',
              LOWER(REPLACE(current.stream_id, '-', '')), '/', current.name
            )
            OR backup.relative_path IS NULL
            OR backup.relative_path_fingerprint <> SHA2(backup.relative_path, 256)
       ) + (
         SELECT COUNT(*)
         FROM stream_artifacts AS current
         JOIN v2_migration_archive_artifacts_backup AS backup
           ON backup.artifact_id = current.id
         WHERE LOWER(TRIM(current.stream_id)) NOT REGEXP
                 '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            OR TRIM(current.name) = ''
            OR TRIM(current.name) IN ('.', '..')
            OR INSTR(current.name, '/') > 0
            OR INSTR(current.name, CHAR(92)) > 0
            OR current.name REGEXP '[[:cntrl:]]'
       ) + (
         SELECT COALESCE(SUM(destination_count - 1), 0)
         FROM (
           SELECT COUNT(*) AS destination_count
           FROM stream_artifacts AS current
           JOIN v2_migration_archive_artifacts_backup AS backup
             ON backup.artifact_id = current.id
           GROUP BY LOWER(CONCAT(
             'final/', current.stream_id, '/legacy-',
             REPLACE(LOWER(TRIM(current.stream_id)), '-', ''), '/', current.name
           ))
           HAVING COUNT(*) > 1
         ) AS collisions
       ) + (
         SELECT COUNT(*)
         FROM stream_settings AS legacy
         LEFT JOIN stream_visual_settings AS v2
           ON v2.stream_id = legacy.stream_id
         WHERE (
           legacy.discord_target_mode IS NOT NULL
           OR legacy.discord_target_preset_id IS NOT NULL
           OR legacy.discord_target_preset_revision IS NOT NULL
           OR legacy.discord_guild_id IS NOT NULL
           OR legacy.discord_text_channel_id IS NOT NULL
           OR legacy.discord_voice_channel_id IS NOT NULL
         ) AND (
           v2.stream_id IS NULL
           OR NOT (legacy.discord_target_mode <=> v2.discord_target_mode)
           OR NOT (legacy.discord_target_preset_id <=> v2.discord_target_preset_id)
           OR NOT (legacy.discord_target_preset_revision <=> v2.discord_target_preset_revision)
           OR COALESCE(TRIM(legacy.discord_guild_id), '') <> COALESCE(v2.discord_guild_id, '')
           OR COALESCE(TRIM(legacy.discord_text_channel_id), '') <> COALESCE(v2.discord_text_channel_id, '')
           OR COALESCE(TRIM(legacy.discord_voice_channel_id), '') <> COALESCE(v2.discord_voice_channel_id, '')
         )
       ) + (
         SELECT COUNT(*)
         FROM system_update_execution_hosts AS current
         LEFT JOIN v2_migration_legacy_agent_export AS exported
           ON exported.execution_host_id = current.execution_host_id
          AND exported.legacy_agent_service_id = current.legacy_agent_service_id
          AND exported.source_fingerprint = SHA2(CONCAT_WS('|', current.execution_host_id, current.legacy_agent_service_id), 256)
         WHERE current.transport_mode <> 'pull_v2'
            OR TRIM(current.agent_service_id) = ''
            OR (current.legacy_agent_service_id IS NOT NULL AND exported.execution_host_id IS NULL)
       ),
       CURRENT_TIMESTAMP(6)
ON DUPLICATE KEY UPDATE
  mismatch_count = VALUES(mismatch_count),
  verified_at = VALUES(verified_at);

-- The 8A view still names compatibility columns removed below. The 8B gate is
-- now the durable proof record, so close that dependency deterministically.
DROP VIEW IF EXISTS v2_migration_bundle8a_counts;

-- The normalized target and its resolved snapshot now have one owner:
-- stream_visual_settings. Migration 080 retained the pre-transform rows in
-- v2_migration_discord_targets_backup; the gate above rejects any newer
-- dual-written row that does not exactly map to that v2 authority.
ALTER TABLE stream_settings
  DROP FOREIGN KEY IF EXISTS fk_stream_settings_discord_target_preset;

DROP INDEX IF EXISTS idx_stream_settings_discord_target_preset
  ON stream_settings;

ALTER TABLE stream_settings
  DROP COLUMN IF EXISTS discord_target_preset_revision,
  DROP COLUMN IF EXISTS discord_target_preset_id,
  DROP COLUMN IF EXISTS discord_target_mode,
  DROP COLUMN IF EXISTS discord_text_channel_id,
  DROP COLUMN IF EXISTS discord_voice_channel_id,
  DROP COLUMN IF EXISTS discord_guild_id;

ALTER TABLE system_update_execution_hosts
  DROP FOREIGN KEY IF EXISTS fk_system_update_execution_hosts_legacy_agent;

ALTER TABLE system_update_execution_hosts
  DROP COLUMN IF EXISTS legacy_agent_service_id;
