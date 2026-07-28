CREATE TABLE IF NOT EXISTS system_update_runtime_token_rotations (
  id CHAR(36) PRIMARY KEY,
  service_id VARCHAR(128) NOT NULL,
  execution_host_id VARCHAR(191) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  intent_sha256 CHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  revision BIGINT NOT NULL,
  expected_ownership_epoch BIGINT NOT NULL,
  expected_source_policy_revision BIGINT NOT NULL,
  expected_projection_revision BIGINT NOT NULL,
  expected_local_executor_policy_revision BIGINT NOT NULL,
  previous_token_id CHAR(36) NOT NULL,
  staged_token_id CHAR(36) NOT NULL,
  staged_token_hash CHAR(64) NOT NULL,
  staged_token_scopes JSON NOT NULL,
  staged_token_ciphertext TEXT NULL,
  staged_token_nonce VARCHAR(128) NULL,
  local_stage_receipt_id VARCHAR(128) NULL,
  credential_claim_id_sha256 CHAR(64) NULL,
  credential_claim_revision BIGINT NULL,
  credential_claimed_at DATETIME(6) NULL,
  local_stage_acknowledged_at DATETIME(6) NULL,
  local_staged_at DATETIME(6) NULL,
  heartbeat_proved_at DATETIME(6) NULL,
  activated_at DATETIME(6) NULL,
  cancel_requested_at DATETIME(6) NULL,
  cancel_acknowledged_at DATETIME(6) NULL,
  canceled_at DATETIME(6) NULL,
  emergency_revoked_token_id CHAR(36) NULL,
  emergency_revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  active_execution_host_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('staged','local_staged','heartbeat_proved','cancel_requested') THEN execution_host_id
      ELSE NULL
    END
  ) STORED,
  CONSTRAINT ck_system_update_runtime_token_rotations_status
    CHECK (status IN ('staged','local_staged','heartbeat_proved','activated','cancel_requested','canceled')),
  CONSTRAINT ck_system_update_runtime_token_rotations_revision
    CHECK (revision >= 1),
  CONSTRAINT ck_system_update_runtime_token_rotations_revisions
    CHECK (
      expected_ownership_epoch >= 1 AND
      expected_source_policy_revision >= 1 AND
      expected_projection_revision >= 1 AND
      expected_local_executor_policy_revision >= 1
    ),
  CONSTRAINT ck_system_update_runtime_token_rotations_distinct_tokens
    CHECK (previous_token_id <> staged_token_id),
  CONSTRAINT ck_system_update_runtime_token_rotations_credential_claim
    CHECK (
      (
        credential_claim_id_sha256 IS NULL AND
        credential_claim_revision IS NULL AND
        (
          credential_claimed_at IS NULL OR
          status IN ('activated','canceled')
        )
      ) OR (
        credential_claim_id_sha256 IS NOT NULL AND
        credential_claim_revision >= 1 AND
        credential_claimed_at IS NOT NULL
      )
    ),
  CONSTRAINT ck_system_update_runtime_token_rotations_secret_material
    CHECK (
      (
        status IN ('staged','local_staged','heartbeat_proved','cancel_requested') AND
        staged_token_ciphertext IS NOT NULL AND
        staged_token_nonce IS NOT NULL
      ) OR (
        status IN ('activated','canceled') AND
        staged_token_ciphertext IS NULL AND
        staged_token_nonce IS NULL AND
        credential_claim_id_sha256 IS NULL AND
        credential_claim_revision IS NULL
      )
    ),
  CONSTRAINT ck_system_update_runtime_token_rotations_local_stage
    CHECK (
      (
        status = 'staged' AND
        local_stage_receipt_id IS NULL AND
        local_stage_acknowledged_at IS NULL AND
        local_staged_at IS NULL AND
        heartbeat_proved_at IS NULL AND
        activated_at IS NULL
      ) OR (
        status = 'local_staged' AND
        credential_claimed_at IS NOT NULL AND
        local_stage_receipt_id IS NOT NULL AND
        local_stage_acknowledged_at IS NOT NULL AND
        local_staged_at IS NOT NULL AND
        heartbeat_proved_at IS NULL AND
        activated_at IS NULL
      ) OR (
        status = 'heartbeat_proved' AND
        credential_claimed_at IS NOT NULL AND
        local_stage_receipt_id IS NOT NULL AND
        local_stage_acknowledged_at IS NOT NULL AND
        local_staged_at IS NOT NULL AND
        heartbeat_proved_at IS NOT NULL AND
        activated_at IS NULL
      ) OR (
        status = 'activated' AND
        credential_claimed_at IS NOT NULL AND
        local_stage_receipt_id IS NOT NULL AND
        local_stage_acknowledged_at IS NOT NULL AND
        local_staged_at IS NOT NULL AND
        heartbeat_proved_at IS NOT NULL AND
        activated_at IS NOT NULL
      ) OR status IN ('cancel_requested','canceled')
    ),
  CONSTRAINT ck_system_update_runtime_token_rotations_cancel
    CHECK (
      (
        status = 'cancel_requested' AND
        cancel_requested_at IS NOT NULL AND
        cancel_acknowledged_at IS NULL AND
        canceled_at IS NULL
      ) OR (
        status = 'canceled' AND
        canceled_at IS NOT NULL AND
        (
          (
            cancel_requested_at IS NULL AND
            cancel_acknowledged_at IS NULL
          ) OR (
            cancel_requested_at IS NOT NULL AND
            cancel_acknowledged_at IS NOT NULL
          )
        )
      ) OR (
        status NOT IN ('cancel_requested','canceled') AND
        cancel_requested_at IS NULL AND
        cancel_acknowledged_at IS NULL AND
        canceled_at IS NULL
      )
    ),
  CONSTRAINT ck_system_update_runtime_token_rotations_emergency
    CHECK (
      (emergency_revoked_token_id IS NULL AND emergency_revoked_at IS NULL) OR
      (emergency_revoked_token_id IS NOT NULL AND emergency_revoked_at IS NOT NULL)
    ),
  CONSTRAINT uq_system_update_runtime_token_rotations_service_idempotency
    UNIQUE (service_id, idempotency_key),
  CONSTRAINT uq_system_update_runtime_token_rotations_active_host
    UNIQUE (active_execution_host_id),
  CONSTRAINT uq_system_update_runtime_token_rotations_staged_hash
    UNIQUE (staged_token_hash),
  CONSTRAINT fk_system_update_runtime_token_rotations_service
    FOREIGN KEY (service_id) REFERENCES services(service_id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_runtime_token_rotations_host
    FOREIGN KEY (execution_host_id) REFERENCES system_update_execution_hosts(execution_host_id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_runtime_token_rotations_previous_token
    FOREIGN KEY (previous_token_id) REFERENCES service_tokens(id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_runtime_token_rotations_staged_token
    FOREIGN KEY (staged_token_id) REFERENCES service_tokens(id) ON DELETE RESTRICT,
  CONSTRAINT fk_system_update_runtime_token_rotations_emergency_token
    FOREIGN KEY (emergency_revoked_token_id) REFERENCES service_tokens(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_system_update_runtime_token_rotations_host_created
  ON system_update_runtime_token_rotations (execution_host_id, created_at);
