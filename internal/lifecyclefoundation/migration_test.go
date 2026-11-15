package lifecyclefoundation

import (
	"context"
	"strings"
	"testing"
)

func TestLifecycleMigrationReplayAndTamper(t *testing.T) {
	f := newFixture(t)
	must(t, ApplyMigrations(context.Background(), f.db))
	for _, corruption := range []string{
		"UPDATE v3_cp_lifecycle_schema SET dirty=1 WHERE singleton=1",
		"UPDATE v3_cp_lifecycle_schema SET generation=2 WHERE singleton=1",
		"UPDATE v3_cp_lifecycle_schema SET checksum=REPEAT('0',64) WHERE singleton=1",
	} {
		must(t, func() error { _, err := f.db.Exec(corruption); return err }())
		expect(t, ApplyMigrations(context.Background(), f.db) != nil, "dirty/unknown migration auto-repaired")
		_, err := f.cp.current(context.Background())
		expect(t, err != nil, "invalid migration allowed admission")
		_, err = f.db.Exec("UPDATE v3_cp_lifecycle_schema SET dirty=0,generation=1,checksum=? WHERE singleton=1", migrationChecksum())
		must(t, err)
	}
	_, err := f.db.Exec("ALTER TABLE v3_cp_lifecycle_heads ADD COLUMN unexpected INT NULL")
	must(t, err)
	_, err = f.cp.current(context.Background())
	expect(t, err != nil, "unexpected schema accepted")
	_, err = f.db.Exec("ALTER TABLE v3_cp_lifecycle_heads DROP COLUMN unexpected")
	must(t, err)
	must(t, ApplyMigrations(context.Background(), f.db))
	_, err = f.service.Run(context.Background(), writeRequest(), f.effect)
	must(t, err)
	for _, statement := range []string{
		"UPDATE v3_cp_lifecycle_history SET phase='FORGED' WHERE deployment=?",
		"DELETE FROM v3_cp_lifecycle_history WHERE deployment=?",
	} {
		_, err = f.db.Exec(statement, f.binding.Deployment)
		expect(t, err != nil, "append-only history modified")
	}
	var generation, count int
	must(t, f.db.QueryRow("SELECT generation FROM v3_cp_lifecycle_schema WHERE singleton=1").Scan(&generation))
	must(t, f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_schema_history").Scan(&count))
	expect(t, generation == 1 && count == 1, "migration replay duplicated history or generation")
}

func TestLifecycleMigrationPartialFailure(t *testing.T) {
	db := disposableDB(t)
	drop := func() {
		for i := len(tables)-1; i >= 0; i-- {
			_, err := db.Exec("DROP TABLE IF EXISTS " + tables[i])
			must(t, err)
		}
	}
	// Only new B0 tables in the explicitly disposable schema are reset.
	// Existing v2/v3 product tables and existing migrations are never touched.
	drop()
	original := migrationSQL
	defer func() {
		migrationSQL = original
		drop()
		must(t, ApplyMigrations(context.Background(), db))
	}()
	migrationSQL += "\nNOT VALID DDL;\n"
	expect(t, ApplyMigrations(context.Background(), db) != nil, "partial DDL fixture did not fail")
	var dirty bool
	var generation uint64
	must(t, db.QueryRow("SELECT dirty,generation FROM v3_cp_lifecycle_schema WHERE singleton=1").Scan(&dirty, &generation))
	expect(t, dirty && generation == 0, "partial DDL published clean completion")
	migrationSQL = original
	expect(t, ApplyMigrations(context.Background(), db) != nil, "dirty installation was automatically repaired")
	var existing int
	must(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='v3_cp_lifecycle_heads'").Scan(&existing))
	expect(t, existing == 1, "partial DDL evidence discarded")
	expect(t, strings.Contains(original, "CREATE TRIGGER"), "append-only constraints missing")
}
