CREATE TABLE IF NOT EXISTS system_update_mutation_grants (
  id CHAR(36) PRIMARY KEY,
  job_id CHAR(36) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  agent_service_id VARCHAR(191) NOT NULL,
  lease_generation BIGINT NOT NULL,
  host_id VARCHAR(191) NOT NULL,
  target_id VARCHAR(191) NOT NULL,
  target_version VARCHAR(128) NOT NULL,
  deployment_mode VARCHAR(16) NOT NULL,
  operation VARCHAR(16) NOT NULL,
  plan_sha256 CHAR(64) NOT NULL,
  session_id VARCHAR(128) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  consumed_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_system_update_mutation_grants_job
    FOREIGN KEY (job_id) REFERENCES system_update_jobs(id) ON DELETE CASCADE,
  UNIQUE KEY uq_system_update_mutation_grants_token_hash (token_hash),
  KEY idx_system_update_mutation_grants_job (job_id, operation, lease_generation),
  KEY idx_system_update_mutation_grants_expiry (expires_at)
);
