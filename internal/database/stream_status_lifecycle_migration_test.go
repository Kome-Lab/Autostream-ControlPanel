package database

import (
	"strings"
	"testing"
)

func TestStreamStatusLifecycleMigrationIncludesAllControlPanelStatuses(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/070_stream_status_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	if len(splitSQLStatements(string(body))) != 1 {
		t.Fatalf("stream status lifecycle migration must remain one executable statement:\n%s", body)
	}
	for _, status := range []string{"created", "draft", "scheduled", "ready", "starting", "live", "stopping", "completed", "failed", "stopped"} {
		if !strings.Contains(text, "'"+status+"'") {
			t.Fatalf("stream status lifecycle migration is missing %q:\n%s", status, body)
		}
	}
	if !strings.Contains(text, "alter table streams") || !strings.Contains(text, "modify column status enum") || !strings.Contains(text, "not null") {
		t.Fatalf("stream status lifecycle migration must alter streams.status as a non-null enum:\n%s", body)
	}
}
