package database

import (
	"strings"
	"testing"
)

func TestObservabilityEmailScopeMigrationUpgradesOnlyActiveObservabilityTokens(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/076_observability_email_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"update service_tokens",
		"service_type = 'observability'",
		"revoked_at is null",
		"notifications.email.send",
		"json_contains",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("email scope migration missing %q:\n%s", required, text)
		}
	}
}
