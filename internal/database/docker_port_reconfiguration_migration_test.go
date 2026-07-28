package database

import (
	"strings"
	"testing"
)

func TestDockerPortReconfigurationMigrationBindsRuntimeBaseline(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/054_docker_port_reconfiguration.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ALTER TABLE system_update_jobs",
		"ALTER TABLE system_update_mutation_grants",
		"docker_published_host_ip VARCHAR(45) NULL",
		"docker_old_published_port INT UNSIGNED NULL",
		"docker_new_published_port INT UNSIGNED NULL",
		"docker_old_container_port INT UNSIGNED NULL",
		"docker_new_container_port INT UNSIGNED NULL",
		"docker_old_health_port INT UNSIGNED NULL",
		"docker_new_health_port INT UNSIGNED NULL",
		"docker_approved_compose_config_sha256 CHAR(64) NULL",
		"docker_approved_compose_revision BIGINT NULL",
		"docker_expected_version_env_sha256 CHAR(71) NULL",
		"docker_expected_container_id VARCHAR(64) NULL",
		"docker_expected_image_id CHAR(71) NULL",
		"docker_expected_repository_digest CHAR(71) NULL",
		"DROP CONSTRAINT IF EXISTS ck_system_update_jobs_port_reconfiguration",
		"DROP CONSTRAINT IF EXISTS ck_system_update_mutation_grants_port_reconfiguration",
		"deployment_mode = 'systemd'",
		"deployment_mode = 'docker'",
		"docker_published_host_ip = '127.0.0.1'",
		"old_port <> new_port OR",
		"docker_old_published_port <> docker_new_published_port OR",
		"docker_old_container_port <> docker_new_container_port",
		"docker_approved_compose_revision = expected_executor_policy_revision",
		"docker_expected_container_id REGEXP '^[a-f0-9]{12,64}$'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Docker port migration is missing %q:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{
		"runtime_token",
		"mutation_grant_token",
		"github_token",
		"compose_file",
		"project_dir",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("Docker port migration persists forbidden field %q:\n%s", forbidden, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func TestDockerPortIndependentChangeConstraintIsRepairedForAppliedDatabases(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/056_docker_port_reconfiguration_independent_changes.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"DROP CONSTRAINT IF EXISTS ck_system_update_jobs_port_reconfiguration",
		"DROP CONSTRAINT IF EXISTS ck_system_update_mutation_grants_port_reconfiguration",
	} {
		if count := strings.Count(text, required); count != 1 {
			t.Fatalf(
				"Docker port constraint repair contains %q %d times, want 1:\n%s",
				required,
				count,
				text,
			)
		}
	}
	for _, required := range []string{
		"old_port <> new_port OR",
		"docker_old_published_port <> docker_new_published_port OR",
		"docker_old_container_port <> docker_new_container_port",
	} {
		if count := strings.Count(text, required); count != 2 {
			t.Fatalf(
				"Docker port constraint repair contains %q %d times, want 2:\n%s",
				required,
				count,
				text,
			)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}
