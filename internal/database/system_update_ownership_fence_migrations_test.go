package database

import (
	"strings"
	"testing"
)

func TestSystemUpdateJobOwnershipFenceMigrationAddsLegacyCompatibleSnapshot(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/047_system_update_job_ownership_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ALTER TABLE system_update_jobs",
		"transport_mode VARCHAR(16) NOT NULL DEFAULT 'ssh_v1'",
		"ownership_epoch BIGINT NOT NULL DEFAULT 0",
		"policy_revision BIGINT NOT NULL DEFAULT 0",
		"CHECK (transport_mode IN ('ssh_v1','pull_v2'))",
		"CHECK (ownership_epoch >= 0)",
		"CHECK (policy_revision >= 0)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("system update ownership fence migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func TestSystemUpdateMutationGrantOwnershipFenceMigrationAddsBindingSnapshot(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/048_system_update_mutation_grant_ownership_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ALTER TABLE system_update_mutation_grants",
		"transport_mode VARCHAR(16) NOT NULL DEFAULT 'ssh_v1'",
		"ownership_epoch BIGINT NOT NULL DEFAULT 0",
		"policy_revision BIGINT NOT NULL DEFAULT 0",
		"CHECK (transport_mode IN ('ssh_v1','pull_v2'))",
		"CHECK (ownership_epoch >= 0)",
		"CHECK (policy_revision >= 0)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("mutation grant ownership fence migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}
