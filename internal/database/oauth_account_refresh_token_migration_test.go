package database

import (
	"strings"
	"testing"
)

func TestOAuthAccountRefreshTokenUpdatedAtMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/061_oauth_account_refresh_token_updated_at.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"alter table oauth_accounts",
		"add column if not exists refresh_token_updated_at datetime null after token_fingerprint",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("oauth account token timestamp migration is missing %q:\n%s", required, string(body))
		}
	}
	assertMigrationContainsNoRawSecrets(t, string(body))
}
