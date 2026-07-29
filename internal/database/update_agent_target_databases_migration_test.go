package database

import (
	"strings"
	"testing"
)

func TestUpdateAgentTargetDatabasesMigrationKeepsBindingsRevisionFenced(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/059_update_agent_target_databases.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS update_agent_target_databases",
		"updater_service_id VARCHAR(128) NOT NULL",
		"target_id VARCHAR(128) NOT NULL",
		"binding_policy_revision BIGINT NOT NULL",
		"database_name VARCHAR(64) NOT NULL",
		"PRIMARY KEY (updater_service_id, target_id)",
		"REFERENCES update_agent_policies(service_id)",
		"ON DELETE CASCADE",
		"CHECK (binding_policy_revision >= 1)",
		"CHECK (database_name REGEXP '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$')",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("target database migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}
