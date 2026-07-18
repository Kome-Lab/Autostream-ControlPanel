ALTER TABLE system_update_jobs
  ADD COLUMN IF NOT EXISTS execution_host_id VARCHAR(191) NULL AFTER agent_service_id;

UPDATE system_update_jobs
SET execution_host_id = COALESCE(NULLIF(TRIM(agent_service_id), ''), target_id)
WHERE execution_host_id IS NULL OR execution_host_id = '';

ALTER TABLE system_update_jobs
  MODIFY COLUMN execution_host_id VARCHAR(191) NOT NULL;

DROP INDEX IF EXISTS uq_system_update_jobs_executing_agent ON system_update_jobs;

ALTER TABLE system_update_jobs
  ADD COLUMN IF NOT EXISTS executing_host_id VARCHAR(191) GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('claimed','downloading','verifying','staging','stopping','installing','starting','health_checking','rolling_back','reconciling') THEN execution_host_id
      ELSE NULL
    END
  ) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS uq_system_update_jobs_executing_agent_host
  ON system_update_jobs (executing_agent_service_id, executing_host_id);

CREATE INDEX IF NOT EXISTS idx_system_update_jobs_agent_host_claim
  ON system_update_jobs (agent_service_id, execution_host_id, status, created_at);
