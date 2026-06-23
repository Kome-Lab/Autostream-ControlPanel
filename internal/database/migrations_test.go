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
