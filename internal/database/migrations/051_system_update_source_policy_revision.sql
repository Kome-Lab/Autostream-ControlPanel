ALTER TABLE system_update_jobs
  ADD COLUMN IF NOT EXISTS expected_source_policy_revision BIGINT NULL
    AFTER expected_config_sha256,
  ADD CONSTRAINT ck_system_update_jobs_source_policy_revision
    CHECK (
      (
        operation = 'software_update' AND
        expected_source_policy_revision IS NULL
      ) OR (
        operation = 'port_reconfigure' AND
        expected_source_policy_revision >= 1
      )
    );

ALTER TABLE system_update_mutation_grants
  ADD COLUMN IF NOT EXISTS expected_source_policy_revision BIGINT NULL
    AFTER expected_config_sha256,
  ADD CONSTRAINT ck_system_update_mutation_grants_source_policy_revision
    CHECK (
      (
        job_operation = 'software_update' AND
        expected_source_policy_revision IS NULL
      ) OR (
        job_operation = 'port_reconfigure' AND
        expected_source_policy_revision >= 1
      )
    );
