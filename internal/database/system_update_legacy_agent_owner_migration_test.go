package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSystemUpdateLegacyAgentOwnerMigrationPreservesSSHBridgeOwner(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/058_system_update_legacy_agent_owner.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS legacy_agent_service_id VARCHAR(128) NULL",
		"SET legacy_agent_service_id = agent_service_id",
		"WHERE transport_mode = 'ssh_v1'",
		"AND legacy_agent_service_id IS NULL",
		"ADD FOREIGN KEY IF NOT EXISTS fk_system_update_execution_hosts_legacy_agent",
		"(legacy_agent_service_id)",
		"REFERENCES services(service_id)",
		"ON DELETE RESTRICT",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("legacy owner migration is missing %q:\n%s", required, text)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

func TestSystemUpdateLegacyAgentOwnerMigrationMariaDBIsPartialApplyRetrySafe(
	t *testing.T,
) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	body, err := embeddedMigrations.ReadFile(
		"migrations/058_system_update_legacy_agent_owner.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	runMigration := func() {
		t.Helper()
		for _, statement := range splitSQLStatements(string(body)) {
			if _, err := connection.ExecContext(ctx, statement); err != nil {
				t.Fatalf("execute 058 statement: %v\n%s", err, statement)
			}
		}
	}

	// OpenFromEnv has already applied 058. Replaying it twice models both a
	// retry after schema_migrations recording failed and a later full retry.
	runMigration()
	runMigration()
}
