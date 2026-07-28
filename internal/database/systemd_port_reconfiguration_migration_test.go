package database

import (
	"strings"
	"testing"
)

func TestSystemdPortReconfigurationMigrationAddsProjectionAndImmutableFences(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/049_systemd_port_reconfiguration.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, required := range []string{
		"ALTER TABLE services",
		"applied_config_revision BIGINT NOT NULL DEFAULT 1",
		"applied_config_sha256 CHAR(71) NULL",
		"ALTER TABLE update_agent_policies",
		"projection_revision BIGINT NULL",
		"local_executor_policy_revision BIGINT NOT NULL DEFAULT 0",
		"SET projection_revision = revision",
		"MODIFY COLUMN projection_revision BIGINT NOT NULL",
		"ALTER TABLE system_update_jobs",
		"operation VARCHAR(32) NOT NULL DEFAULT 'software_update'",
		"network_namespace VARCHAR(128) NULL",
		"protocol VARCHAR(3) NULL",
		"old_port INT UNSIGNED NULL",
		"new_port INT UNSIGNED NULL",
		"expected_endpoint_revision BIGINT NULL",
		"target_endpoint_revision BIGINT NULL",
		"expected_config_revision BIGINT NULL",
		"target_config_revision BIGINT NULL",
		"expected_config_sha256 CHAR(71) NULL",
		"target_config_sha256 CHAR(71) NULL",
		"expected_updater_policy_revision BIGINT NULL",
		"expected_executor_policy_revision BIGINT NULL",
		"expected_executor_policy_sha256 CHAR(71) NULL",
		"port_plan_sha256 CHAR(64) NULL",
		"ALTER TABLE system_update_mutation_grants",
		"job_operation VARCHAR(32) NOT NULL DEFAULT 'software_update'",
		"target_service_type VARCHAR(64) NULL",
		"MODIFY COLUMN operation VARCHAR(32) NOT NULL",
		"CHECK (operation IN ('apply','reconcile','port_reconfigure','port_reconfigure_reconcile'))",
		"target_service_type IN ('worker','encoder_recorder','discord_bot','observability')",
		"CREATE INDEX IF NOT EXISTS idx_service_port_reservations_service_id",
		"ON service_port_reservations (service_id)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("systemd port-reconfiguration migration is missing %q:\n%s", required, text)
		}
	}

	backfill := strings.Index(text, "SET projection_revision = revision")
	notNull := strings.Index(text, "MODIFY COLUMN projection_revision BIGINT NOT NULL")
	if backfill < 0 || notNull <= backfill {
		t.Fatal("projection_revision must be copied from the existing source revision before NOT NULL is enforced")
	}
	if strings.Contains(text, "projection_revision BIGINT NOT NULL DEFAULT 0") {
		t.Fatal("projection_revision must not initialize existing policies to a blind zero")
	}
	for _, forbidden := range []string{
		"applied_config_sha256 CHAR(64)",
		"expected_config_sha256 CHAR(64)",
		"target_config_sha256 CHAR(64)",
		"expected_executor_policy_sha256 CHAR(64)",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("canonical sha256: digests require CHAR(71), found %q", forbidden)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func TestSystemdPortReconfigurationMigrationKeepsSoftwareRowsCompatible(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/049_systemd_port_reconfiguration.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"operation = 'software_update'",
		"job_operation = 'software_update'",
		"network_namespace IS NULL",
		"port_plan_sha256 IS NULL",
		"operation = 'port_reconfigure'",
		"job_operation = 'port_reconfigure'",
		"network_namespace IS NOT NULL",
		"port_plan_sha256 IS NOT NULL",
		"old_port <> new_port",
		"deployment_mode = 'systemd'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("systemd port-reconfiguration compatibility fence is missing %q:\n%s", required, text)
		}
	}
}
