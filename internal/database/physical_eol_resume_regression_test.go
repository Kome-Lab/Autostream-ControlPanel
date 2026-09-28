package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMariaDBPhysicalEOLResumeAfterViewDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP VIEW IF EXISTS v2_migration_bundle8a_counts")
}
func TestMariaDBPhysicalEOLResumeAfterDiscordForeignKeyDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP FOREIGN KEY IF EXISTS fk_stream_settings_discord_target_preset")
}
func TestMariaDBPhysicalEOLResumeAfterDiscordIndexDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP INDEX IF EXISTS idx_stream_settings_discord_target_preset")
}
func TestMariaDBPhysicalEOLResumeAfterDiscordColumnsDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP COLUMN IF EXISTS discord_target_preset_revision")
}
func TestMariaDBPhysicalEOLResumeAfterHostForeignKeyDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP FOREIGN KEY IF EXISTS fk_system_update_execution_hosts_legacy_agent")
}
func TestMariaDBPhysicalEOLResumeAfterAllDDL(t *testing.T) {
	testPhysicalEOLResume(t, "DROP COLUMN IF EXISTS legacy_agent_service_id")
}

func testPhysicalEOLResume(t *testing.T, boundary string) {
	t.Helper()
	db, ctx := physicalEOLReviewFixture(t)
	physicalEOLInterruptDeliveredSQL(t, ctx, db, boundary)
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("normal runner resume: %v", err)
	}
	assertPhysicalEOLEOLComplete(t, db)
	assertPhysicalEOLArtifactPaths(t, db)
	assertPhysicalEOLDiscordSnapshots(t, db)
	assertPhysicalEOLRollbackRecords(t, db)
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("completed replay: %v", err)
	}
}

func TestMariaDBPhysicalEOLResumeRejectsReplacementMismatch(t *testing.T) {
	db, ctx := physicalEOLReviewFixture(t)
	physicalEOLInterruptDeliveredSQL(t, ctx, db, "DROP COLUMN IF EXISTS discord_target_preset_revision")
	mustPhysicalEOLExec(t, db, `UPDATE stream_visual_settings SET discord_guild_id='9999' WHERE stream_id=?`, physicalEOLManualStreamID)
	if err := RunEmbeddedMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatal("resume accepted replacement mismatch")
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE id='081_bundle8b_physical_eol.sql'`, 0)
}

func TestMariaDBPhysicalEOLResumeRejectsBackupMismatch(t *testing.T) {
	db, ctx := physicalEOLReviewFixture(t)
	physicalEOLInterruptDeliveredSQL(t, ctx, db, "DROP COLUMN IF EXISTS legacy_agent_service_id")
	mustPhysicalEOLExec(t, db, `UPDATE v2_migration_discord_targets_backup SET discord_guild_id='9999' WHERE stream_id=?`, physicalEOLManualStreamID)
	if err := RunEmbeddedMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatal("resume accepted backup mismatch")
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE id='081_bundle8b_physical_eol.sql'`, 0)
}

func TestMariaDBPhysicalEOLResumeCurrentCheckpoint(t *testing.T) {
	db, ctx := physicalEOLCurrentCheckpointFixture(t)
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("resume current checkpoint: %v", err)
	}
	assertPhysicalEOLEOLComplete(t, db)
	assertPhysicalEOLDiscordSnapshots(t, db)
	assertPhysicalEOLRollbackRecords(t, db)
}

func TestMariaDBPhysicalEOLResumeRejectsRetainedDataDrift(t *testing.T) {
	db, ctx := physicalEOLCurrentCheckpointFixture(t)
	mustPhysicalEOLExec(t, db, `UPDATE stream_settings SET updated_at='2030-01-01 00:00:00' WHERE stream_id=?`, physicalEOLManualStreamID)
	if err := RunEmbeddedMigrations(ctx, db); err == nil || !strings.Contains(err.Error(), "changed after EOL gate") {
		t.Fatalf("expected retained data conflict, got %v", err)
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE id='081_bundle8b_physical_eol.sql'`, 0)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM stream_settings WHERE updated_at='2030-01-01 00:00:00'`, 1)
}

func physicalEOLCurrentCheckpointFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := physicalEOLReviewFixture(t)
	body, err := embeddedMigrations.ReadFile("migrations/081_bundle8b_physical_eol.sql")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("\nSIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='fixture stop before record';\n")...)
	source := fstest.MapFS{"migrations/081_bundle8b_physical_eol.sql": &fstest.MapFile{Data: body}}
	if err := runMigrationsFS(ctx, db, source, "migrations"); err == nil || !strings.Contains(err.Error(), "fixture stop before record") {
		t.Fatalf("fault injection did not reach final boundary: %v", err)
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM v2_migration_bundle8b_resume_checkpoint`, 1)
	return db, ctx
}

func physicalEOLReviewFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := openMariaDBMigrationTest(t, false)
	assertPhysicalEOLFreshDatabase(t, db)
	through079 := physicalEOLMigrationFS(t, func(name string) bool { return name <= "079_control_platform_features.sql" })
	if err := runMigrationsFS(ctx, db, through079, "migrations"); err != nil {
		t.Fatal(err)
	}
	seedPhysicalEOLMigrationFixtures(t, db)
	mustPhysicalEOLExec(t, db, `UPDATE stream_artifacts SET name='audio.aac',relative_path=? WHERE id='artifact-audio'`, "final/"+physicalEOLManualStreamID+"/audio.aac")
	mustPhysicalEOLExec(t, db, `UPDATE stream_visual_settings SET discord_guild_id='3001' WHERE stream_id=?`, physicalEOLMismatchID)
	migration080 := physicalEOLMigrationFS(t, func(name string) bool { return name == "080_bundle8a_v2_migration.sql" })
	if err := runMigrationsFS(ctx, db, migration080, "migrations"); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func physicalEOLInterruptDeliveredSQL(t *testing.T, ctx context.Context, db *sql.DB, boundary string) {
	t.Helper()
	body, err := embeddedMigrations.ReadFile("migrations/081_bundle8b_physical_eol.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	found := false
	for _, stmt := range splitSQLStatements(string(body)) {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("delivered SQL interruption fixture: %v", err)
		}
		if strings.Contains(stmt, boundary) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("interruption boundary was not executed")
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE id='081_bundle8b_physical_eol.sql'`, 0)
}
