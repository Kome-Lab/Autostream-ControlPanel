ALTER TABLE system_update_jobs
  ADD COLUMN IF NOT EXISTS transport_mode VARCHAR(16) NOT NULL DEFAULT 'ssh_v1' AFTER execution_host_id,
  ADD COLUMN IF NOT EXISTS ownership_epoch BIGINT NOT NULL DEFAULT 0 AFTER transport_mode,
  ADD COLUMN IF NOT EXISTS policy_revision BIGINT NOT NULL DEFAULT 0 AFTER ownership_epoch,
  ADD CONSTRAINT ck_system_update_jobs_transport_mode
    CHECK (transport_mode IN ('ssh_v1','pull_v2')),
  ADD CONSTRAINT ck_system_update_jobs_ownership_epoch
    CHECK (ownership_epoch >= 0),
  ADD CONSTRAINT ck_system_update_jobs_policy_revision
    CHECK (policy_revision >= 0);

