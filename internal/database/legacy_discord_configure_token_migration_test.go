package database

import (
	"strings"
	"testing"
)

func TestLegacyDiscordConfigureTokenMigrationRevokesOnlyUnscopedPendingTokens(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/065_revoke_legacy_discord_configure_tokens.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	if statements := splitSQLStatements(string(body)); len(statements) != 1 || !strings.Contains(strings.ToLower(statements[0]), "update services as s") {
		t.Fatalf("legacy Discord Configure Token migration must remain one executable statement: %#v", statements)
	}
	for _, required := range []string{
		"update services as s",
		"join service_tokens as t on t.id = s.token_id",
		"set s.configure_token_hash = null",
		"s.configure_token_expires_at = null",
		"where s.service_type = 'discord_bot'",
		"s.configure_token_hash is not null",
		"s.configure_token_used_at is null",
		"json_contains(t.scopes, json_quote('streams.start'))",
		"not json_contains(t.scopes, json_quote('streams.stop'))",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("legacy Discord Configure Token migration is missing %q:\n%s", required, string(body))
		}
	}
	if strings.Contains(text, "node_token_") || strings.Contains(text, "token_hash = null") && !strings.Contains(text, "configure_token_hash = null") {
		t.Fatalf("legacy Discord Configure Token migration must not rewrite active Node Runtime Tokens:\n%s", string(body))
	}
	for _, forbidden := range []string{
		"insert into service_tokens",
		"update service_tokens set",
		"node_token_ciphertext",
		"node_token_nonce",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("legacy Discord Configure Token migration must not rewrite runtime credentials via %q:\n%s", forbidden, string(body))
		}
	}
}
