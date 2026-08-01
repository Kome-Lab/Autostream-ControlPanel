package database

import (
	"strings"
	"testing"
)

func TestUpdateAgentTargetLocalListenersMigrationKeepsBindingsRevisionFenced(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/060_update_agent_target_local_listeners.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS update_agent_target_local_listeners",
		"updater_service_id VARCHAR(128) NOT NULL",
		"target_id VARCHAR(128) NOT NULL",
		"binding_policy_revision BIGINT NOT NULL",
		"local_listen_port INT NOT NULL",
		"PRIMARY KEY (updater_service_id, target_id)",
		"REFERENCES update_agent_policies(service_id)",
		"ON DELETE CASCADE",
		"CHECK (binding_policy_revision >= 1)",
		"CHECK (local_listen_port BETWEEN 1024 AND 65535)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("target local listener migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}
