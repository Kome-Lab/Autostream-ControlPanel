ALTER TABLE system_update_host_self_update_grants
  ADD COLUMN IF NOT EXISTS release_binding JSON NULL AFTER artifact_sha256,
  ADD COLUMN IF NOT EXISTS stage_claim_revision BIGINT NULL AFTER consumed_at,
  ADD COLUMN IF NOT EXISTS stage_claimed_at DATETIME(6) NULL AFTER stage_claim_revision;

UPDATE system_update_host_self_update_grants AS grant_row
INNER JOIN system_update_host_self_updates AS update_row
  ON update_row.id = grant_row.self_update_id
SET grant_row.release_binding = JSON_OBJECT(
  'tag', update_row.release_tag,
  'commit', update_row.release_commit,
  'published_at', DATE_FORMAT(
    update_row.release_published_at,
    '%Y-%m-%dT%H:%i:%s.%fZ'
  ),
  'manifest_asset_id', update_row.manifest_asset_id,
  'manifest_asset_name', update_row.manifest_asset_name,
  'manifest_sha256', update_row.manifest_sha256,
  'manifest_checksum_asset_id', update_row.manifest_checksum_asset_id,
  'manifest_checksum_sha256', update_row.manifest_checksum_sha256,
  'archive_asset_id', update_row.archive_asset_id,
  'archive_asset_name', update_row.archive_asset_name,
  'archive_size', update_row.archive_size,
  'archive_sha256', update_row.archive_sha256,
  'archive_checksum_asset_id', update_row.archive_checksum_asset_id,
  'archive_checksum_sha256', update_row.archive_checksum_sha256,
  'arch', update_row.artifact_arch,
  'agent_protocol_version', update_row.agent_protocol_version,
  'executor_protocol_version', update_row.executor_protocol_version,
  'mutation_protocol_version', update_row.mutation_protocol_version,
  'recovery_protocol_version', update_row.recovery_protocol_version,
  'minimum_panel_version', update_row.minimum_panel_version
)
WHERE grant_row.release_binding IS NULL;

UPDATE system_update_host_self_update_grants AS grant_row
INNER JOIN system_update_host_self_updates AS update_row
  ON update_row.id = grant_row.self_update_id
SET
  grant_row.token_hash = SHA2(
    CONCAT('quarantined-host-self-update-grant:', grant_row.id, ':057'),
    256
  ),
  grant_row.consumed_at = NULL,
  grant_row.stage_claim_revision = NULL,
  grant_row.stage_claimed_at = NULL
WHERE grant_row.operation = 'stage'
  AND grant_row.consumed_at IS NOT NULL
  AND update_row.status IN ('queued', 'canceled');

UPDATE system_update_host_self_update_grants
SET
  stage_claim_revision = expected_self_update_revision + 1,
  stage_claimed_at = consumed_at
WHERE operation = 'stage'
  AND consumed_at IS NOT NULL
  AND stage_claim_revision IS NULL
  AND stage_claimed_at IS NULL;

ALTER TABLE system_update_host_self_update_grants
  MODIFY COLUMN release_binding JSON NOT NULL;

ALTER TABLE system_update_host_self_update_grants
  DROP CONSTRAINT IF EXISTS ck_system_update_host_self_update_grants_stage_claim;

ALTER TABLE system_update_host_self_update_grants
  ADD CONSTRAINT ck_system_update_host_self_update_grants_stage_claim
    CHECK (
      (
        operation = 'stage' AND consumed_at IS NOT NULL AND
        stage_claim_revision IS NOT NULL AND
        stage_claimed_at IS NOT NULL AND
        stage_claim_revision = expected_self_update_revision + 1 AND
        stage_claimed_at = consumed_at
      ) OR (
        (
          operation <> 'stage' OR consumed_at IS NULL
        ) AND stage_claim_revision IS NULL AND stage_claimed_at IS NULL
      )
    );
