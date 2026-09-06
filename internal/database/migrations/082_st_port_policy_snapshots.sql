-- Unknown historical applied endpoint revisions require an authenticated exact
-- baseline. Do not infer them from desired endpoint revisions during migration.
ALTER TABLE services ADD COLUMN applied_endpoint_revision BIGINT NULL;
ALTER TABLE services ADD CONSTRAINT ck_services_applied_endpoint_revision CHECK (
  applied_endpoint_revision IS NULL OR (applied_endpoint_revision >= 1 AND applied_endpoint_revision <= endpoint_revision));

ALTER TABLE system_update_jobs ADD COLUMN port_contract_version INT NULL;
ALTER TABLE system_update_jobs DROP CONSTRAINT ck_system_update_jobs_port_reconfiguration;
ALTER TABLE system_update_jobs ADD CONSTRAINT ck_system_update_jobs_port_reconfiguration CHECK ((port_contract_version IS NULL AND (
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
        port_plan_sha256 IS NULL AND
        docker_published_host_ip IS NULL AND
        docker_old_published_port IS NULL AND
        docker_new_published_port IS NULL AND
        docker_old_container_port IS NULL AND
        docker_new_container_port IS NULL AND
        docker_old_health_port IS NULL AND
        docker_new_health_port IS NULL AND
        docker_approved_compose_config_sha256 IS NULL AND
        docker_approved_compose_revision IS NULL AND
        docker_expected_version_env_sha256 IS NULL AND
        docker_expected_container_id IS NULL AND
        docker_expected_image_id IS NULL AND
        docker_expected_repository_digest IS NULL
      ) OR (
        operation = 'port_reconfigure' AND
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
        protocol = 'tcp' AND
        old_port BETWEEN 1 AND 65535 AND
        new_port BETWEEN 1 AND 65535 AND
        expected_endpoint_revision >= 1 AND
        target_endpoint_revision = expected_endpoint_revision + 1 AND
        expected_config_revision >= 1 AND
        target_config_revision = expected_config_revision + 1 AND
        expected_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        target_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        expected_updater_policy_revision >= 1 AND
        expected_executor_policy_revision >= 1 AND
        expected_executor_policy_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        port_plan_sha256 REGEXP '^[a-f0-9]{64}$' AND
        (
          (
            deployment_mode = 'systemd' AND
            old_port BETWEEN 1024 AND 65535 AND
            new_port BETWEEN 1024 AND 65535 AND
            old_port <> new_port AND
            docker_published_host_ip IS NULL AND
            docker_old_published_port IS NULL AND
            docker_new_published_port IS NULL AND
            docker_old_container_port IS NULL AND
            docker_new_container_port IS NULL AND
            docker_old_health_port IS NULL AND
            docker_new_health_port IS NULL AND
            docker_approved_compose_config_sha256 IS NULL AND
            docker_approved_compose_revision IS NULL AND
            docker_expected_version_env_sha256 IS NULL AND
            docker_expected_container_id IS NULL AND
            docker_expected_image_id IS NULL AND
            docker_expected_repository_digest IS NULL
          ) OR (
            deployment_mode = 'docker' AND
            docker_published_host_ip = '127.0.0.1' AND
            docker_old_published_port BETWEEN 1024 AND 65535 AND
            docker_new_published_port BETWEEN 1024 AND 65535 AND
            docker_old_container_port BETWEEN 1024 AND 65535 AND
            docker_new_container_port BETWEEN 1024 AND 65535 AND
            (
              old_port <> new_port OR
              docker_old_published_port <> docker_new_published_port OR
              docker_old_container_port <> docker_new_container_port
            ) AND
            docker_old_health_port = docker_old_published_port AND
            docker_new_health_port = docker_new_published_port AND
            docker_approved_compose_config_sha256 REGEXP '^[a-f0-9]{64}$' AND
            docker_approved_compose_revision = expected_executor_policy_revision AND
            docker_expected_version_env_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
            docker_expected_container_id REGEXP '^[a-f0-9]{12,64}$' AND
            docker_expected_image_id REGEXP '^sha256:[a-f0-9]{64}$' AND
            docker_expected_repository_digest REGEXP '^sha256:[a-f0-9]{64}$'
          )
        )
      )
)) OR (port_contract_version = 2 AND operation = 'port_reconfigure' AND network_namespace IS NULL AND protocol IS NULL AND old_port IS NULL AND new_port IS NULL AND expected_endpoint_revision IS NULL AND target_endpoint_revision IS NULL AND expected_config_revision IS NULL AND target_config_revision IS NULL AND expected_config_sha256 IS NULL AND expected_source_policy_revision IS NULL AND target_config_sha256 IS NULL AND expected_updater_policy_revision IS NULL AND expected_executor_policy_revision IS NULL AND expected_executor_policy_sha256 IS NULL AND port_plan_sha256 IS NULL AND docker_published_host_ip IS NULL AND docker_old_published_port IS NULL AND docker_new_published_port IS NULL AND docker_old_container_port IS NULL AND docker_new_container_port IS NULL AND docker_old_health_port IS NULL AND docker_new_health_port IS NULL AND docker_approved_compose_config_sha256 IS NULL AND docker_approved_compose_revision IS NULL AND docker_expected_version_env_sha256 IS NULL AND docker_expected_container_id IS NULL AND docker_expected_image_id IS NULL AND docker_expected_repository_digest IS NULL)));
ALTER TABLE system_update_mutation_grants ADD COLUMN port_contract_version INT NULL;
ALTER TABLE system_update_mutation_grants DROP CONSTRAINT ck_system_update_mutation_grants_port_reconfiguration;
ALTER TABLE system_update_mutation_grants ADD CONSTRAINT ck_system_update_mutation_grants_port_reconfiguration CHECK ((port_contract_version IS NULL AND (
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
        port_plan_sha256 IS NULL AND
        docker_published_host_ip IS NULL AND
        docker_old_published_port IS NULL AND
        docker_new_published_port IS NULL AND
        docker_old_container_port IS NULL AND
        docker_new_container_port IS NULL AND
        docker_old_health_port IS NULL AND
        docker_new_health_port IS NULL AND
        docker_approved_compose_config_sha256 IS NULL AND
        docker_approved_compose_revision IS NULL AND
        docker_expected_version_env_sha256 IS NULL AND
        docker_expected_container_id IS NULL AND
        docker_expected_image_id IS NULL AND
        docker_expected_repository_digest IS NULL
      ) OR (
        job_operation = 'port_reconfigure' AND
        operation IN ('port_reconfigure','port_reconfigure_reconcile') AND
        transport_mode = 'pull_v2' AND
        target_service_type IN ('worker','encoder_recorder','discord_bot','observability') AND
        network_namespace IS NOT NULL AND
        protocol = 'tcp' AND
        old_port BETWEEN 1 AND 65535 AND
        new_port BETWEEN 1 AND 65535 AND
        expected_endpoint_revision >= 1 AND
        target_endpoint_revision = expected_endpoint_revision + 1 AND
        expected_config_revision >= 1 AND
        target_config_revision = expected_config_revision + 1 AND
        expected_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        target_config_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        expected_updater_policy_revision >= 1 AND
        expected_executor_policy_revision >= 1 AND
        expected_executor_policy_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
        port_plan_sha256 REGEXP '^[a-f0-9]{64}$' AND
        (
          (
            deployment_mode = 'systemd' AND
            old_port BETWEEN 1024 AND 65535 AND
            new_port BETWEEN 1024 AND 65535 AND
            old_port <> new_port AND
            docker_published_host_ip IS NULL AND
            docker_old_published_port IS NULL AND
            docker_new_published_port IS NULL AND
            docker_old_container_port IS NULL AND
            docker_new_container_port IS NULL AND
            docker_old_health_port IS NULL AND
            docker_new_health_port IS NULL AND
            docker_approved_compose_config_sha256 IS NULL AND
            docker_approved_compose_revision IS NULL AND
            docker_expected_version_env_sha256 IS NULL AND
            docker_expected_container_id IS NULL AND
            docker_expected_image_id IS NULL AND
            docker_expected_repository_digest IS NULL
          ) OR (
            deployment_mode = 'docker' AND
            docker_published_host_ip = '127.0.0.1' AND
            docker_old_published_port BETWEEN 1024 AND 65535 AND
            docker_new_published_port BETWEEN 1024 AND 65535 AND
            docker_old_container_port BETWEEN 1024 AND 65535 AND
            docker_new_container_port BETWEEN 1024 AND 65535 AND
            (
              old_port <> new_port OR
              docker_old_published_port <> docker_new_published_port OR
              docker_old_container_port <> docker_new_container_port
            ) AND
            docker_old_health_port = docker_old_published_port AND
            docker_new_health_port = docker_new_published_port AND
            docker_approved_compose_config_sha256 REGEXP '^[a-f0-9]{64}$' AND
            docker_approved_compose_revision = expected_executor_policy_revision AND
            docker_expected_version_env_sha256 REGEXP '^sha256:[a-f0-9]{64}$' AND
            docker_expected_container_id REGEXP '^[a-f0-9]{12,64}$' AND
            docker_expected_image_id REGEXP '^sha256:[a-f0-9]{64}$' AND
            docker_expected_repository_digest REGEXP '^sha256:[a-f0-9]{64}$'
          )
        )
      )
)) OR (port_contract_version = 2 AND job_operation = 'port_reconfigure' AND network_namespace IS NULL AND protocol IS NULL AND old_port IS NULL AND new_port IS NULL AND expected_endpoint_revision IS NULL AND target_endpoint_revision IS NULL AND expected_config_revision IS NULL AND target_config_revision IS NULL AND expected_config_sha256 IS NULL AND expected_source_policy_revision IS NULL AND target_config_sha256 IS NULL AND expected_updater_policy_revision IS NULL AND expected_executor_policy_revision IS NULL AND expected_executor_policy_sha256 IS NULL AND port_plan_sha256 IS NULL AND docker_published_host_ip IS NULL AND docker_old_published_port IS NULL AND docker_new_published_port IS NULL AND docker_old_container_port IS NULL AND docker_new_container_port IS NULL AND docker_old_health_port IS NULL AND docker_new_health_port IS NULL AND docker_approved_compose_config_sha256 IS NULL AND docker_approved_compose_revision IS NULL AND docker_expected_version_env_sha256 IS NULL AND docker_expected_container_id IS NULL AND docker_expected_image_id IS NULL AND docker_expected_repository_digest IS NULL)));

CREATE TABLE system_update_port_transactions (
    job_id CHAR(36) NOT NULL PRIMARY KEY,
    execution_host_id VARCHAR(191) NOT NULL,
    updater_service_id VARCHAR(191) NOT NULL,
    target_id VARCHAR(191) NOT NULL,
    ownership_epoch BIGINT NOT NULL,
    job_policy_revision BIGINT NOT NULL,
    port_contract_version INT NOT NULL,
    mode VARCHAR(32) NOT NULL,
    request_sha256 CHAR(64) NOT NULL,
    intent_sha256 CHAR(64) NOT NULL,
    plan_json LONGTEXT NOT NULL,
    before_json LONGTEXT NOT NULL,
    target_json LONGTEXT NOT NULL,
    rollback_json LONGTEXT NOT NULL,
    cancel_endpoint_revision BIGINT NOT NULL,
    phase VARCHAR(32) NOT NULL,
    recovery_required BOOLEAN NOT NULL DEFAULT FALSE,
    accepted_result_json LONGTEXT NULL,
    last_recovery_observation_json LONGTEXT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT fk_port_transaction_job FOREIGN KEY (job_id) REFERENCES system_update_jobs(id) ON DELETE RESTRICT,
    INDEX idx_port_transaction_host_hold (execution_host_id, recovery_required),
    CONSTRAINT chk_port_transaction_version CHECK (port_contract_version = 2),
    CONSTRAINT chk_port_transaction_mode CHECK (mode IN ('local_only', 'local_and_advertised')),
    CONSTRAINT chk_port_transaction_phase CHECK (
      phase IN ('created','consumed','canceled','premutation_failed','rollback_latched','accepted') AND
      recovery_required = (phase = 'rollback_latched') AND
      (accepted_result_json IS NOT NULL) = (phase = 'accepted') AND
      (phase <> 'rollback_latched' OR last_recovery_observation_json IS NOT NULL)),
    CONSTRAINT chk_port_transaction_json CHECK (
      JSON_VALID(plan_json) AND JSON_VALID(before_json) AND JSON_VALID(target_json) AND JSON_VALID(rollback_json) AND
      (accepted_result_json IS NULL OR JSON_VALID(accepted_result_json)) AND
      (last_recovery_observation_json IS NULL OR JSON_VALID(last_recovery_observation_json))),
    CONSTRAINT chk_port_transaction_size CHECK (
      OCTET_LENGTH(before_json) <= 1048576 AND OCTET_LENGTH(target_json) <= 1048576 AND
      OCTET_LENGTH(rollback_json) <= 1048576 AND
      OCTET_LENGTH(before_json) + OCTET_LENGTH(target_json) + OCTET_LENGTH(rollback_json) + OCTET_LENGTH(plan_json) <= 4194304)
);
