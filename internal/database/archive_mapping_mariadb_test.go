package database

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMariaDBBundle8BArchiveExistingRuns(t *testing.T) {
	testBundle8BArchiveMapping(t, "existing", false)
}
func TestMariaDBBundle8BArchiveFlat(t *testing.T) { testBundle8BArchiveMapping(t, "flat", false) }
func TestMariaDBBundle8BArchiveMixedSameName(t *testing.T) {
	testBundle8BArchiveMapping(t, "mixed", false)
}
func TestMariaDBBundle8BArchiveForwardCorrection(t *testing.T) {
	testBundle8BArchiveMapping(t, "forward", true)
}

type archiveFixtureRow struct{ id, run, name, path, body string }

func testBundle8BArchiveMapping(t *testing.T, scenario string, recorded081 bool) {
	t.Helper()
	db, ctx := openMariaDBMigrationTest(t, false)
	assertBundle8BFreshDatabase(t, db)
	binary := os.Getenv("AUTOSTREAM_ARCHIVE_MIGRATOR_BINARY")
	if binary == "" {
		t.Fatal("filesystem witness requires AUTOSTREAM_ARCHIVE_MIGRATOR_BINARY")
	}
	through079 := bundle8BMigrationFS(t, func(name string) bool { return name <= "079_control_platform_features.sql" })
	if err := runMigrationsFS(ctx, db, through079, "migrations"); err != nil {
		t.Fatal(err)
	}
	seedBundle8BMigrationFixtures(t, db)
	mustBundle8BExec(t, db, `UPDATE stream_visual_settings SET discord_guild_id='3001' WHERE stream_id=?`, bundle8BMismatchID)
	mustBundle8BExec(t, db, `DELETE FROM stream_artifacts`)
	var fixtures []archiveFixtureRow
	if scenario != "flat" {
		fixtures = append(fixtures, archiveFixtureRow{id: "existing-a", run: "run-a", name: "capture.ts", body: "existing-run-a"})
	}
	if scenario == "existing" || scenario == "mixed" {
		fixtures = append(fixtures, archiveFixtureRow{id: "existing-b", run: "run-b", name: "capture.ts", body: "existing-run-b"})
	}
	if scenario != "existing" {
		name := "capture.ts"
		if scenario == "forward" {
			name = "flat.ts"
		}
		fixtures = append(fixtures, archiveFixtureRow{id: "flat", name: name, body: "true-flat-legacy"})
	}
	root := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backup")
	flatCount := 0
	for i := range fixtures {
		row := &fixtures[i]
		row.path = "final/" + bundle8BManualStreamID + "/"
		if row.run != "" {
			row.path += row.run + "/"
		} else {
			flatCount++
		}
		row.path += row.name
		file := filepath.Join(root, filepath.FromSlash(row.path))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(row.body), 0o640); err != nil {
			t.Fatal(err)
		}
		mustBundle8BExec(t, db, `INSERT INTO stream_artifacts(id,stream_id,archive_run_id,archive_started_at,kind,name,relative_path,size_bytes,created_at)
 VALUES (?,?,?,NULL,'video',?,?,?,'2026-02-03 04:05:06')`, row.id, bundle8BManualStreamID, row.run, row.name, row.path, len(row.body))
	}
	manifestPaths := map[string]string{}
	if flatCount > 0 {
		runArchiveFixtureCommand(t, binary, root, backup, "prepare")
		body, err := os.ReadFile(filepath.Join(backup, "archive-v2-migration-manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Entries []struct {
				Source      string `json:"source_relative"`
				Destination string `json:"destination_relative"`
			}
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatal(err)
		}
		if len(manifest.Entries) != flatCount {
			t.Fatalf("physical manifest rows=%d, flat rows=%d", len(manifest.Entries), flatCount)
		}
		for _, entry := range manifest.Entries {
			manifestPaths[filepath.ToSlash(entry.Source)] = filepath.ToSlash(entry.Destination)
		}
	}
	migration080 := bundle8BMigrationFS(t, func(name string) bool { return name == "080_bundle8a_v2_migration.sql" })
	if err := runMigrationsFS(ctx, db, migration080, "migrations"); err != nil {
		t.Fatal(err)
	}
	if recorded081 {
		bundle8BInterruptDeliveredSQL(t, ctx, db, "DROP COLUMN IF EXISTS legacy_agent_service_id")
		// Complete the old-runner fixture, then exercise only the normal forward
		// migration path. This record belongs to this disposable test DB alone.
		mustBundle8BExec(t, db, `INSERT INTO schema_migrations(id) VALUES ('081_bundle8b_physical_eol.sql')`)
	}
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("archive migration: %v", err)
	}
	if flatCount > 0 {
		runArchiveFixtureCommand(t, binary, root, backup, "apply")
		runArchiveFixtureCommand(t, binary, root, backup, "apply")
		runArchiveFixtureCommand(t, binary, root, backup, "verify")
	}
	for _, row := range fixtures {
		var run, path string
		var started sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT archive_run_id,relative_path,archive_started_at FROM stream_artifacts WHERE id=?`, row.id).Scan(&run, &path, &started); err != nil {
			t.Fatal(err)
		}
		wantRun, wantPath := row.run, row.path
		if row.run == "" {
			wantRun = "legacy-" + strings.ReplaceAll(bundle8BManualStreamID, "-", "")
			var ok bool
			wantPath, ok = manifestPaths[row.path]
			if !ok {
				t.Fatal("DB flat row has no physical manifest entry")
			}
		}
		if run != wantRun || path != wantPath || !started.Valid || started.Time.Format("2006-01-02 15:04:05") != "2026-02-03 04:05:06" {
			t.Fatalf("archive authority mismatch for %s", row.id)
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("DB path has no physical artifact for %s: %v", row.id, err)
		}
		if string(body) != row.body {
			t.Fatalf("wrong run content for %s", row.id)
		}
		var originalRun, originalPath string
		var originalStarted sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT archive_run_id,archive_started_at,relative_path FROM v2_migration_archive_artifacts_backup WHERE artifact_id=?`, row.id).Scan(&originalRun, &originalStarted, &originalPath); err != nil {
			t.Fatal(err)
		}
		if originalRun != row.run || originalStarted.Valid || originalPath != row.path {
			t.Fatalf("original backup changed for %s", row.id)
		}
	}
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("mapping replay: %v", err)
	}
}

func runArchiveFixtureCommand(t *testing.T, binary, root, backup, operation string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, "archive-v2-migrate", "--operation", operation, "--archive-root", root, "--backup-dir", backup)
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("archive %s: %v (%s)", operation, err, strings.TrimSpace(string(body)))
	}
}
