package database

import (
	"strings"
	"testing"
)

func TestOAuthAccountAccessTokenRefreshedAtMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/063_oauth_account_access_token_refreshed_at.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"alter table oauth_accounts",
		"add column if not exists access_token_refreshed_at datetime null after refresh_token_updated_at",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("oauth account access token migration is missing %q:\n%s", required, string(body))
		}
	}
	assertMigrationContainsNoRawSecrets(t, string(body))
}
