CREATE TABLE IF NOT EXISTS system_update_host_self_updates (
  id CHAR(36) PRIMARY KEY,
  execution_host_id VARCHAR(191) NOT NULL,
  agent_service_id VARCHAR(128) NOT NULL,
  target_version VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  revision BIGINT NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  intent_sha256 CHAR(64) NOT NULL,
  requested_by_user_id CHAR(36) NOT NULL,
  requested_by_username VARCHAR(255) NOT NULL,
  retry_of_id CHAR(36) NULL,
  attempt_generation CHAR(36) NOT NULL,
  expected_ownership_epoch BIGINT NOT NULL,
  expected_source_policy_revision BIGINT NOT NULL,
  expected_projection_revision BIGINT NOT NULL,
  expected_local_executor_policy_revision BIGINT NOT NULL,
  expected_local_executor_policy_sha256 VARCHAR(71) NOT NULL,
  previous_agent_version VARCHAR(64) NOT NULL,
  previous_executor_version VARCHAR(64) NOT NULL,
  previous_agent_protocol_version INT NOT NULL,
  previous_executor_protocol_version INT NOT NULL,
  previous_mutation_protocol_version INT NOT NULL,
  previous_recovery_protocol_version INT NOT NULL,
  release_tag VARCHAR(64) NOT NULL,
  release_commit CHAR(40) NOT NULL,
  release_published_at DATETIME(6) NOT NULL,
  manifest_asset_id BIGINT NOT NULL,
  manifest_asset_name VARCHAR(255) NOT NULL,
  manifest_sha256 CHAR(64) NOT NULL,
  manifest_checksum_asset_id BIGINT NOT NULL,
  manifest_checksum_sha256 CHAR(64) NOT NULL,
  archive_asset_id BIGINT NOT NULL,
  archive_asset_name VARCHAR(255) NOT NULL,
  archive_size BIGINT NOT NULL,
  archive_sha256 CHAR(64) NOT NULL,
  archive_checksum_asset_id BIGINT NOT NULL,
  archive_checksum_sha256 CHAR(64) NOT NULL,
  artifact_arch VARCHAR(16) NOT NULL,
  agent_protocol_version INT NOT NULL,
  executor_protocol_version INT NOT NULL,
  mutation_protocol_version INT NOT NULL,
  recovery_protocol_version INT NOT NULL,
  minimum_panel_version VARCHAR(64) NOT NULL,
  attestation_verified_at DATETIME(6) NOT NULL,
  issued_at DATETIME(6) NOT NULL,
  observation_state VARCHAR(16) NOT NULL DEFAULT 'unknown',
  reported_phase VARCHAR(32) NULL,
  last_heartbeat_at DATETIME(6) NULL,
  stalled_since DATETIME(6) NULL,
  cancel_requested_at DATETIME(6) NULL,
  started_at DATETIME(6) NULL,
  completed_at DATETIME(6) NULL,
  code VARCHAR(128) NULL,
  message VARCHAR(1024) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  active_execution_host_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN status IN (
        'queued','staging','activating','verifying',
        'rolling_back','cancel_requested'
      ) THEN execution_host_id
      ELSE NULL
    END
  ) STORED,
  CONSTRAINT ck_system_update_host_self_updates_status
    CHECK (status IN (
      'queued','staging','activating','verifying','rolling_back',
      'cancel_requested','succeeded','rolled_back','failed','canceled'
    )),
  CONSTRAINT ck_system_update_host_self_updates_observation
    CHECK (observation_state IN ('known','stalled','unknown')),
  CONSTRAINT ck_system_update_host_self_updates_revision
    CHECK (revision >= 1),
  CONSTRAINT ck_system_update_host_self_updates_fences
    CHECK (
      expected_ownership_epoch >= 1 AND
      expected_source_policy_revision >= 1 AND
      expected_projection_revision >= 1 AND
      expected_local_executor_policy_revision >= 1
    ),
  CONSTRAINT ck_system_update_host_self_updates_asset
    CHECK (
      archive_size > 0 AND
      artifact_arch IN ('amd64','arm64') AND
      previous_agent_protocol_version >= 1 AND
      previous_executor_protocol_version >= 1 AND
      previous_mutation_protocol_version >= 1 AND
      previous_recovery_protocol_version >= 1 AND
      agent_protocol_version >= 1 AND
      executor_protocol_version >= 1 AND
      mutation_protocol_version >= 1
      AND recovery_protocol_version >= 1
    ),
  CONSTRAINT ck_system_update_host_self_updates_cancel
    CHECK (
      (status = 'cancel_requested' AND cancel_requested_at IS NOT NULL) OR
      status <> 'cancel_requested'
    ),
  CONSTRAINT ck_system_update_host_self_updates_terminal
    CHECK (
      (
        status IN ('succeeded','rolled_back','failed','canceled') AND
        completed_at IS NOT NULL
      ) OR (
        status NOT IN ('succeeded','rolled_back','failed','canceled') AND
        completed_at IS NULL
      )
    ),
  CONSTRAINT uq_system_update_host_self_updates_user_idempotency
    UNIQUE (requested_by_user_id, idempotency_key),
  CONSTRAINT uq_system_update_host_self_updates_generation
    UNIQUE (attempt_generation),
  CONSTRAINT uq_system_update_host_self_updates_active_host
    UNIQUE (active_execution_host_id),
  CONSTRAINT fk_system_update_host_self_updates_host
    FOREIGN KEY (execution_host_id)
    REFERENCES system_update_execution_hosts(execution_host_id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_host_self_updates_agent
    FOREIGN KEY (agent_service_id)
    REFERENCES services(service_id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_host_self_updates_user
    FOREIGN KEY (requested_by_user_id)
    REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_host_self_updates_retry
    FOREIGN KEY (retry_of_id)
    REFERENCES system_update_host_self_updates(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_system_update_host_self_updates_host_created
  ON system_update_host_self_updates (execution_host_id, created_at);

CREATE TABLE IF NOT EXISTS system_update_host_self_update_grants (
  id CHAR(36) PRIMARY KEY,
  self_update_id CHAR(36) NOT NULL,
  attempt_generation CHAR(36) NOT NULL,
  operation VARCHAR(16) NOT NULL,
  execution_host_id VARCHAR(191) NOT NULL,
  agent_service_id VARCHAR(128) NOT NULL,
  expected_self_update_revision BIGINT NOT NULL,
  expected_ownership_epoch BIGINT NOT NULL,
  expected_source_policy_revision BIGINT NOT NULL,
  expected_projection_revision BIGINT NOT NULL,
  expected_local_executor_policy_revision BIGINT NOT NULL,
  expected_local_executor_policy_sha256 VARCHAR(71) NOT NULL,
  agent_version VARCHAR(64) NOT NULL,
  executor_version VARCHAR(64) NOT NULL,
  release_commit CHAR(40) NOT NULL,
  artifact_sha256 VARCHAR(71) NOT NULL,
  agent_protocol_version INT NOT NULL,
  executor_protocol_version INT NOT NULL,
  mutation_protocol_version INT NOT NULL,
  recovery_protocol_version INT NOT NULL,
  directive_issued_at DATETIME(6) NOT NULL,
  plan_sha256 CHAR(64) NOT NULL,
  session_id VARCHAR(128) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  revision BIGINT NOT NULL,
  issued_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  consumed_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  CONSTRAINT ck_system_update_host_self_update_grants_operation
    CHECK (operation IN ('stage','reconcile')),
  CONSTRAINT ck_system_update_host_self_update_grants_revision
    CHECK (revision >= 1),
  CONSTRAINT ck_system_update_host_self_update_grants_fences
    CHECK (
      expected_ownership_epoch >= 1 AND
      expected_self_update_revision >= 1 AND
      expected_source_policy_revision >= 1 AND
      expected_projection_revision >= 1 AND
      expected_local_executor_policy_revision >= 1
    ),
  CONSTRAINT ck_system_update_host_self_update_grants_protocols
    CHECK (
      agent_protocol_version >= 1 AND
      executor_protocol_version >= 1 AND
      mutation_protocol_version >= 1
      AND recovery_protocol_version >= 1
    ),
  CONSTRAINT ck_system_update_host_self_update_grants_expiry
    CHECK (expires_at > issued_at),
  CONSTRAINT uq_system_update_host_self_update_grants_operation
    UNIQUE (self_update_id, operation, session_id),
  CONSTRAINT uq_system_update_host_self_update_grants_token
    UNIQUE (token_hash),
  CONSTRAINT fk_system_update_host_self_update_grants_update
    FOREIGN KEY (self_update_id)
    REFERENCES system_update_host_self_updates(id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_host_self_update_grants_host
    FOREIGN KEY (execution_host_id)
    REFERENCES system_update_execution_hosts(execution_host_id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_host_self_update_grants_agent
    FOREIGN KEY (agent_service_id)
    REFERENCES services(service_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_system_update_host_self_update_grants_expiry
  ON system_update_host_self_update_grants (expires_at, consumed_at);
