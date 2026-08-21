package database

import (
	"strings"
	"testing"
)

func TestStreamLogHistoryMigrationBackfillsAuditHistoryWithoutDeletingLogs(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/077_stream_log_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"insert ignore into stream_logs",
		"from audit_logs",
		"resource_type = 'stream'",
		"idx_stream_logs_created_stream",
		"idx_stream_logs_stream_created",
		"audit_logs.action = 'streams.retry_upload'",
		"audit_logs.result = 'success'",
		"create trigger prevent_stream_log_delete",
		"stream log history is append-only",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("stream log migration missing %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, "delete from stream_logs") {
		t.Fatalf("stream log history migration must not delete retained logs:\n%s", text)
	}
}
