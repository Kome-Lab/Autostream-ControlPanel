ALTER TABLE system_update_jobs
  DROP CONSTRAINT IF EXISTS ck_system_update_jobs_port_reconfiguration;

ALTER TABLE system_update_jobs
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
    );

ALTER TABLE system_update_mutation_grants
  DROP CONSTRAINT IF EXISTS ck_system_update_mutation_grants_port_reconfiguration;

ALTER TABLE system_update_mutation_grants
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
    );
