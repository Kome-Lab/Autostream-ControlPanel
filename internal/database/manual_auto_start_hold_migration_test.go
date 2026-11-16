package database

import (
	"strings"
	"testing"
)

func TestManualAutoStartHoldMigrationIsExplicitOnly(t *testing.T) {
	body, err := ManualAutoStartHoldMigrationSQL()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stream_auto_start_holds", "stream_auto_start_hold_history", "stream_auto_start_attempts", "stream_auto_start_admissions", "stream_auto_start_claim_requests"} {
		if !strings.Contains(body, "CREATE TABLE "+table) {
			t.Fatalf("missing explicit table %s", table)
		}
	}
	if strings.Contains(strings.ToUpper(body), "UPDATE STREAMS") || strings.Contains(strings.ToUpper(body), "INSERT INTO STREAM_AUTO_START_HOLDS") {
		t.Fatal("historical hold proof must not be manufactured by backfill")
	}
	if !strings.Contains(body, "ON DELETE RESTRICT") {
		t.Fatal("historical hold metadata must not silently cascade away")
	}
}
