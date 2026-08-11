package database

import (
	"strings"
	"testing"
)

func TestDiscordStreamStopScopeMigrationPairsExistingBotTokens(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/069_pair_discord_stream_stop_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	statements := splitSQLStatements(string(body))
	if len(statements) != 1 {
		t.Fatalf("Discord stop-scope migration must remain one executable statement: %#v", statements)
	}
	for _, required := range []string{
		"update service_tokens",
		"set scopes = json_array_append(scopes, '$', 'streams.stop')",
		"where service_type = 'discord_bot'",
		"json_contains(scopes, json_quote('streams.start'))",
		"not json_contains(scopes, json_quote('streams.stop'))",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Discord stop-scope migration is missing %q:\n%s", required, string(body))
		}
	}
	if strings.Contains(text, "service_type <> 'discord_bot'") || strings.Contains(text, "update services") {
		t.Fatalf("Discord stop-scope migration must not modify services or other service types:\n%s", string(body))
	}
}
