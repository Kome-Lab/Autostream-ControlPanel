package database

import (
	"strings"
	"testing"
)

func TestSystemUpdateActiveHostIndexMigrationLeadsWithExecutionHost(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/050_system_update_active_host_index.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	if !strings.Contains(text, "create index if not exists idx_system_update_jobs_execution_host_status_created") {
		t.Fatalf("active-host index migration must be idempotent:\n%s", string(body))
	}
	if !strings.Contains(text, "on system_update_jobs (execution_host_id, status, created_at)") {
		t.Fatalf("active-host lookup index must lead with execution_host_id:\n%s", string(body))
	}
}
