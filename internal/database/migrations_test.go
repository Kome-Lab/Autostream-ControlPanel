package database

import (
	"strings"
	"testing"
)

func TestSplitSQLStatements(t *testing.T) {
	got := splitSQLStatements("CREATE TABLE a (id INT);\n\nCREATE TABLE b (id INT);")
	if len(got) != 2 {
		t.Fatalf("got %d statements: %#v", len(got), got)
	}
}

func TestStreamArtifactUniqueMigrationDeduplicatesBeforeIndex(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/005_stream_artifacts_unique_key.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	dedupeAt := strings.Index(text, "DELETE stale_artifacts")
	indexAt := strings.Index(text, "uniq_stream_artifacts_stream_kind_name")
	if dedupeAt < 0 {
		t.Fatalf("stream artifact unique migration must remove historical duplicates before adding the unique key:\n%s", text)
	}
	if indexAt < 0 {
		t.Fatalf("stream artifact unique migration must add the expected unique key:\n%s", text)
	}
	if dedupeAt > indexAt {
		t.Fatalf("stream artifact unique migration must deduplicate before adding the unique key:\n%s", text)
	}
}

func TestPasskeyCeremonySessionUserForeignKeyMatchesUsersTable(t *testing.T) {
	initBody, err := embeddedMigrations.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	initText := string(initBody)
	if !strings.Contains(initText, "id CHAR(36) PRIMARY KEY") {
		t.Fatalf("users.id type changed; passkey ceremony FK compatibility test must be updated:\n%s", initText)
	}

	createBody, err := embeddedMigrations.ReadFile("migrations/017_webauthn_ceremony_sessions.sql")
	if err != nil {
		t.Fatal(err)
	}
	createText := string(createBody)
	if !strings.Contains(createText, "user_id CHAR(36) NULL") {
		t.Fatalf("webauthn ceremony user_id must match users.id for MariaDB FK compatibility:\n%s", createText)
	}
	if strings.Contains(createText, "DEFAULT CHARSET") {
		t.Fatalf("webauthn ceremony table must inherit the database charset/collation used by users:\n%s", createText)
	}
	if strings.Contains(createText, "ENGINE=") {
		t.Fatalf("webauthn ceremony table must inherit the database storage engine used by users:\n%s", createText)
	}

	alterBody, err := embeddedMigrations.ReadFile("migrations/018_webauthn_ceremony_sessions_nullable_user.sql")
	if err != nil {
		t.Fatal(err)
	}
	alterText := string(alterBody)
	dropAt := strings.Index(alterText, "DROP FOREIGN KEY fk_webauthn_ceremony_sessions_user")
	modifyAt := strings.Index(alterText, "MODIFY user_id CHAR(36) NULL")
	addAt := strings.Index(alterText, "ADD CONSTRAINT fk_webauthn_ceremony_sessions_user")
	if dropAt < 0 || modifyAt < 0 || addAt < 0 || !(dropAt < modifyAt && modifyAt < addAt) {
		t.Fatalf("migration 018 must rebuild the FK around the compatible user_id type:\n%s", alterText)
	}
}
