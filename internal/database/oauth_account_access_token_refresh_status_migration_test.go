package database

import (
	"strings"
	"testing"
)

func TestOAuthAccountAccessTokenRefreshStatusMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/064_oauth_account_access_token_refresh_status.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"alter table oauth_accounts",
		"add column if not exists token_revision bigint unsigned not null default 1 after token_fingerprint",
		"add column if not exists access_token_refresh_attempted_at datetime null after access_token_refreshed_at",
		"add column if not exists access_token_refresh_failed_at datetime null after access_token_refresh_attempted_at",
		"add column if not exists access_token_refresh_failure_code varchar(64) null after access_token_refresh_failed_at",
		"add column if not exists access_token_refresh_relink_required boolean not null default false after access_token_refresh_failure_code",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("oauth account access token refresh status migration is missing %q:\n%s", required, string(body))
		}
	}
	assertMigrationContainsNoRawSecrets(t, string(body))
}
