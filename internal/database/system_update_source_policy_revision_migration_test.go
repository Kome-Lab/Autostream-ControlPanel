package database

import (
	"strings"
	"testing"
)

func TestSystemUpdateSourcePolicyRevisionMigrationFencesJobsAndGrants(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/051_system_update_source_policy_revision.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, table := range []string{"system_update_jobs", "system_update_mutation_grants"} {
		if !strings.Contains(text, "alter table "+table) {
			t.Fatalf("source-policy revision migration omits %s:\n%s", table, string(body))
		}
	}
	for _, required := range []string{
		"add column if not exists expected_source_policy_revision bigint null",
		"operation = 'software_update'",
		"job_operation = 'software_update'",
		"operation = 'port_reconfigure'",
		"job_operation = 'port_reconfigure'",
		"expected_source_policy_revision is null",
		"expected_source_policy_revision >= 1",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("source-policy revision migration is missing %q:\n%s", required, string(body))
		}
	}
}
