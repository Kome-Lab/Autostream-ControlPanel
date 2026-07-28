ALTER TABLE services
  ADD COLUMN IF NOT EXISTS applied_config_revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS applied_config_sha256 CHAR(71) NULL,
  ADD CONSTRAINT ck_services_applied_config_revision
    CHECK (applied_config_revision >= 1),
  ADD CONSTRAINT ck_services_applied_config_sha256
    CHECK (
      applied_config_sha256 IS NULL OR
      applied_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$'
    );

ALTER TABLE update_agent_policies
  ADD COLUMN IF NOT EXISTS projection_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS local_executor_policy_revision BIGINT NOT NULL DEFAULT 0;

UPDATE update_agent_policies
SET projection_revision = revision
WHERE projection_revision IS NULL;

ALTER TABLE update_agent_policies
  MODIFY COLUMN projection_revision BIGINT NOT NULL DEFAULT 1,
  ADD CONSTRAINT ck_update_agent_policies_projection_revision
    CHECK (projection_revision >= 1),
  ADD CONSTRAINT ck_update_agent_policies_local_executor_policy_revision
    CHECK (local_executor_policy_revision >= 0);

ALTER TABLE system_update_jobs
  ADD COLUMN IF NOT EXISTS operation VARCHAR(32) NOT NULL DEFAULT 'software_update',
  ADD COLUMN IF NOT EXISTS network_namespace VARCHAR(128) NULL,
  ADD COLUMN IF NOT EXISTS protocol VARCHAR(3) NULL,
  ADD COLUMN IF NOT EXISTS old_port INT UNSIGNED NULL,
  ADD COLUMN IF NOT EXISTS new_port INT UNSIGNED NULL,
  ADD COLUMN IF NOT EXISTS expected_endpoint_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS target_endpoint_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_config_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS target_config_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_config_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS target_config_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS expected_updater_policy_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_executor_policy_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_executor_policy_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS port_plan_sha256 CHAR(64) NULL,
  ADD CONSTRAINT ck_system_update_jobs_operation
    CHECK (operation IN ('software_update','port_reconfigure')),
  ADD CONSTRAINT ck_system_update_jobs_port_reconfiguration
    CHECK (
      (
        operation = 'software_update' AND
        network_namespace IS NULL AND
        protocol IS NULL AND
        old_port IS NULL AND
        new_port IS NULL AND
        expected_endpoint_revision IS NULL AND
        target_endpoint_revision IS NULL AND
        expected_config_revision IS NULL AND
        target_config_revision IS NULL AND
        expected_config_sha256 IS NULL AND
        target_config_sha256 IS NULL AND
        expected_updater_policy_revision IS NULL AND
        expected_executor_policy_revision IS NULL AND
        expected_executor_policy_sha256 IS NULL AND
        port_plan_sha256 IS NULL
      ) OR (
        operation = 'port_reconfigure' AND
        deployment_mode = 'systemd' AND
        network_namespace IS NOT NULL AND
        protocol IS NOT NULL AND
        old_port IS NOT NULL AND
        new_port IS NOT NULL AND
        expected_endpoint_revision IS NOT NULL AND
        target_endpoint_revision IS NOT NULL AND
        expected_config_revision IS NOT NULL AND
        target_config_revision IS NOT NULL AND
        expected_config_sha256 IS NOT NULL AND
        target_config_sha256 IS NOT NULL AND
        expected_updater_policy_revision IS NOT NULL AND
        expected_executor_policy_revision IS NOT NULL AND
        expected_executor_policy_sha256 IS NOT NULL AND
        port_plan_sha256 IS NOT NULL AND
        network_namespace = LOWER(network_namespace) AND
        network_namespace REGEXP '^[a-z0-9][a-z0-9._:-]{0,127}$' AND
        protocol IN ('tcp','udp') AND
        old_port BETWEEN 1024 AND 65535 AND
        new_port BETWEEN 1024 AND 65535 AND
        old_port <> new_port AND
        expected_endpoint_revision >= 1 AND
        target_endpoint_revision > expected_endpoint_revision AND
        expected_config_revision >= 1 AND
        target_config_revision > expected_config_revision AND
        expected_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        target_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        expected_updater_policy_revision >= 1 AND
        expected_executor_policy_revision >= 1 AND
        expected_executor_policy_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        port_plan_sha256 REGEXP '^[a-f0-9]{64}$'
      )
    );

ALTER TABLE system_update_mutation_grants
  MODIFY COLUMN operation VARCHAR(32) NOT NULL,
  ADD COLUMN IF NOT EXISTS job_operation VARCHAR(32) NOT NULL DEFAULT 'software_update',
  ADD COLUMN IF NOT EXISTS target_service_type VARCHAR(64) NULL,
  ADD COLUMN IF NOT EXISTS network_namespace VARCHAR(128) NULL,
  ADD COLUMN IF NOT EXISTS protocol VARCHAR(3) NULL,
  ADD COLUMN IF NOT EXISTS old_port INT UNSIGNED NULL,
  ADD COLUMN IF NOT EXISTS new_port INT UNSIGNED NULL,
  ADD COLUMN IF NOT EXISTS expected_endpoint_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS target_endpoint_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_config_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS target_config_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_config_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS target_config_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS expected_updater_policy_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_executor_policy_revision BIGINT NULL,
  ADD COLUMN IF NOT EXISTS expected_executor_policy_sha256 CHAR(71) NULL,
  ADD COLUMN IF NOT EXISTS port_plan_sha256 CHAR(64) NULL,
  ADD CONSTRAINT ck_system_update_mutation_grants_job_operation
    CHECK (job_operation IN ('software_update','port_reconfigure')),
  ADD CONSTRAINT ck_system_update_mutation_grants_operation
    CHECK (operation IN ('apply','reconcile','port_reconfigure','port_reconfigure_reconcile')),
  ADD CONSTRAINT ck_system_update_mutation_grants_port_reconfiguration
    CHECK (
      (
        job_operation = 'software_update' AND
        operation IN ('apply','reconcile') AND
        network_namespace IS NULL AND
        protocol IS NULL AND
        old_port IS NULL AND
        new_port IS NULL AND
        expected_endpoint_revision IS NULL AND
        target_endpoint_revision IS NULL AND
        expected_config_revision IS NULL AND
        target_config_revision IS NULL AND
        expected_config_sha256 IS NULL AND
        target_config_sha256 IS NULL AND
        expected_updater_policy_revision IS NULL AND
        expected_executor_policy_revision IS NULL AND
        expected_executor_policy_sha256 IS NULL AND
        port_plan_sha256 IS NULL
      ) OR (
        job_operation = 'port_reconfigure' AND
        operation IN ('port_reconfigure','port_reconfigure_reconcile') AND
        deployment_mode = 'systemd' AND
        target_service_type IN ('worker','encoder_recorder','discord_bot','observability') AND
        network_namespace IS NOT NULL AND
        protocol IS NOT NULL AND
        old_port IS NOT NULL AND
        new_port IS NOT NULL AND
        expected_endpoint_revision IS NOT NULL AND
        target_endpoint_revision IS NOT NULL AND
        expected_config_revision IS NOT NULL AND
        target_config_revision IS NOT NULL AND
        expected_config_sha256 IS NOT NULL AND
        target_config_sha256 IS NOT NULL AND
        expected_updater_policy_revision IS NOT NULL AND
        expected_executor_policy_revision IS NOT NULL AND
        expected_executor_policy_sha256 IS NOT NULL AND
        port_plan_sha256 IS NOT NULL AND
        network_namespace = LOWER(network_namespace) AND
        network_namespace REGEXP '^[a-z0-9][a-z0-9._:-]{0,127}$' AND
        protocol IN ('tcp','udp') AND
        old_port BETWEEN 1024 AND 65535 AND
        new_port BETWEEN 1024 AND 65535 AND
        old_port <> new_port AND
        expected_endpoint_revision >= 1 AND
        target_endpoint_revision > expected_endpoint_revision AND
        expected_config_revision >= 1 AND
        target_config_revision > expected_config_revision AND
        expected_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        target_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        expected_updater_policy_revision >= 1 AND
        expected_executor_policy_revision >= 1 AND
        expected_executor_policy_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        port_plan_sha256 REGEXP '^[a-f0-9]{64}$'
      )
    );

CREATE INDEX IF NOT EXISTS idx_service_port_reservations_service_id
  ON service_port_reservations (service_id);
