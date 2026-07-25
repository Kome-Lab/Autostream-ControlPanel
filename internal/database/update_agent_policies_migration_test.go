package database

import (
	"strings"
	"testing"
)

func TestUpdateAgentPoliciesMigrationMatchesServiceIdentityAndStoresDeclarativeJSON(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/043_update_agent_policies.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"service_id VARCHAR(128) PRIMARY KEY",
		"revision BIGINT NOT NULL",
		"policy_json JSON NOT NULL",
		"FOREIGN KEY (service_id) REFERENCES services(service_id) ON DELETE CASCADE",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("update agent policy migration is missing %q:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{"github_token", "runtime_token", "private_key", "identity_file", "known_hosts", "command", "argv"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("update agent policy migration must not add raw secret or privileged field %q:\n%s", forbidden, text)
		}
	}
}
