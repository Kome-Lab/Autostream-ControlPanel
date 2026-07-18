ALTER TABLE service_tokens
  MODIFY COLUMN service_type ENUM('discord_bot','encoder_recorder','worker','observability','update_agent') NOT NULL;

ALTER TABLE services
  MODIFY COLUMN service_type ENUM('discord_bot','encoder_recorder','worker','observability','update_agent') NOT NULL;

ALTER TABLE service_metric_snapshots
  MODIFY COLUMN service_type ENUM('discord_bot','encoder_recorder','worker','observability','update_agent') NOT NULL;

CREATE TABLE IF NOT EXISTS system_update_jobs (
  id CHAR(36) PRIMARY KEY,
  target_id VARCHAR(191) NOT NULL,
  target_service_type VARCHAR(64) NOT NULL,
  deployment_mode VARCHAR(16) NOT NULL,
  current_version VARCHAR(128) NOT NULL DEFAULT '',
  target_version VARCHAR(128) NOT NULL,
  strategy VARCHAR(32) NOT NULL,
  status VARCHAR(32) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  requested_by_user_id VARCHAR(64) NOT NULL,
  requested_by_username VARCHAR(255) NOT NULL DEFAULT '',
  agent_service_id VARCHAR(191) NULL,
  lease_generation BIGINT NOT NULL DEFAULT 0,
  lease_token_hash CHAR(64) NULL,
  lease_expires_at DATETIME(6) NULL,
  sequence BIGINT NOT NULL DEFAULT 0,
  progress SMALLINT NOT NULL DEFAULT 0,
  code VARCHAR(128) NOT NULL DEFAULT '',
  message VARCHAR(500) NOT NULL DEFAULT '',
  artifact_digest VARCHAR(192) NOT NULL DEFAULT '',
  previous_digest VARCHAR(192) NOT NULL DEFAULT '',
  claimed_at DATETIME(6) NULL,
  completed_at DATETIME(6) NULL,
  cancelled_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  active_target_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('queued','claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') THEN target_id
      ELSE NULL
    END
  ) STORED,
  executing_agent_service_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') THEN agent_service_id
      ELSE NULL
    END
  ) STORED,
  UNIQUE KEY uq_system_update_jobs_idempotency (requested_by_user_id, idempotency_key),
  UNIQUE KEY uq_system_update_jobs_active_target (active_target_id),
  UNIQUE KEY uq_system_update_jobs_executing_agent (executing_agent_service_id),
  KEY idx_system_update_jobs_claim (status, created_at),
  KEY idx_system_update_jobs_agent_lease (agent_service_id, lease_expires_at),
  KEY idx_system_update_jobs_target_created (target_id, created_at)
);

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'system_updates.read' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'system_updates.execute' FROM roles WHERE name = 'super_admin';
