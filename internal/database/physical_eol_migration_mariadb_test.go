package database

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	physicalEOLUserID         = "11111111-1111-4111-8111-111111111111"
	physicalEOLPresetID       = "22222222-2222-4222-8222-222222222222"
	physicalEOLManualStreamID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	physicalEOLPresetStreamID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	physicalEOLMismatchID     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	physicalEOLLegacyStreamID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	physicalEOLAgentServiceID = "bundle8b-update-agent"
	physicalEOLExecutionHost  = "bundle8b-host"
)

func TestPhysicalEOLMigrationMariaDB(t *testing.T) {
	db, ctx := openMariaDBMigrationTest(t, false)
	assertPhysicalEOLFreshDatabase(t, db)

	through079 := physicalEOLMigrationFS(t, func(name string) bool {
		return name <= "079_control_platform_features.sql"
	})
	if err := runMigrationsFS(ctx, db, through079, "migrations"); err != nil {
		t.Fatalf("apply migrations through 079: %v", err)
	}
	seedPhysicalEOLMigrationFixtures(t, db)

	migration080 := physicalEOLMigrationFS(t, func(name string) bool {
		return name == "080_bundle8a_v2_migration.sql"
	})
	if err := runMigrationsFS(ctx, db, migration080, "migrations"); err != nil {
		t.Fatalf("apply migration 080: %v", err)
	}

	migration081 := physicalEOLMigrationFS(t, func(name string) bool {
		return name == "081_bundle8b_physical_eol.sql"
	})
	if err := runMigrationsFS(ctx, db, migration081, "migrations"); err == nil {
		t.Fatal("migration 081 accepted colliding Encoder destinations")
	}
	assertPhysicalEOLEOLBlocked(t, db)

	mustPhysicalEOLExec(t, db,
		`UPDATE stream_artifacts SET name='../unsafe.ts', relative_path=? WHERE id=?`,
		"final/"+physicalEOLManualStreamID+"/unsafe.ts", "artifact-audio")
	if err := runMigrationsFS(ctx, db, migration081, "migrations"); err == nil {
		t.Fatal("migration 081 accepted an unsafe Encoder artifact name")
	}
	assertPhysicalEOLEOLBlocked(t, db)

	mustPhysicalEOLExec(t, db,
		`UPDATE stream_artifacts SET name='audio.aac', relative_path=? WHERE id=?`,
		"final/"+physicalEOLManualStreamID+"/audio.aac", "artifact-audio")
	if err := runMigrationsFS(ctx, db, migration081, "migrations"); err == nil {
		t.Fatal("migration 081 accepted a nonzero Discord snapshot mismatch")
	}
	assertPhysicalEOLEOLBlocked(t, db)
	assertPhysicalEOLArtifactPaths(t, db)

	mustPhysicalEOLExec(t, db, `UPDATE stream_visual_settings
		SET discord_guild_id='3001', discord_text_channel_id='3002', discord_voice_channel_id='3003'
		WHERE stream_id=?`, physicalEOLMismatchID)
	if err := runMigrationsFS(ctx, db, migration081, "migrations"); err != nil {
		t.Fatalf("apply migration 081 after zero mismatch: %v", err)
	}

	assertPhysicalEOLEOLComplete(t, db)
	assertPhysicalEOLArtifactPaths(t, db)
	assertPhysicalEOLDiscordSnapshots(t, db)
	assertPhysicalEOLRollbackRecords(t, db)

	if err := runMigrationsFS(ctx, db, migration081, "migrations"); err != nil {
		t.Fatalf("second migration 081 application: %v", err)
	}
	assertPhysicalEOLArtifactPaths(t, db)
	assertPhysicalEOLEOLComplete(t, db)
}

func physicalEOLMigrationFS(t *testing.T, include func(string) bool) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	selected := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || !include(entry.Name()) {
			continue
		}
		path := "migrations/" + entry.Name()
		body, err := embeddedMigrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		selected[path] = &fstest.MapFile{Data: body, Mode: 0o444}
	}
	if len(selected) == 0 {
		t.Fatal("Bundle 8B migration fixture selected zero files")
	}
	return selected
}

func assertPhysicalEOLFreshDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("Bundle 8B migration witness requires a fresh database, found %d tables", count)
	}
}

func seedPhysicalEOLMigrationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	mustPhysicalEOLExec(t, db, `INSERT INTO users
		(id,username,password_hash,status,created_at,updated_at)
		VALUES (?, 'bundle8b-user', 'not-a-secret', 'active', NOW(6), NOW(6))`, physicalEOLUserID)
	mustPhysicalEOLExec(t, db, `INSERT INTO discord_target_presets
		(id,name,guild_id,text_channel_id,voice_channel_id,revision,created_by_user_id,updated_by_user_id,created_at,updated_at)
		VALUES (?, 'Bundle 8B preset', '2001', '2002', '2003', 7, ?, ?, NOW(6), NOW(6))`,
		physicalEOLPresetID, physicalEOLUserID, physicalEOLUserID)

	for _, fixture := range []struct {
		id   string
		name string
	}{
		{physicalEOLManualStreamID, "manual"},
		{physicalEOLPresetStreamID, "preset"},
		{physicalEOLMismatchID, "mismatch"},
		{physicalEOLLegacyStreamID, "legacy"},
	} {
		mustPhysicalEOLExec(t, db, `INSERT INTO streams
			(id,name,status,created_at,updated_at) VALUES (?,?,'created',NOW(6),NOW(6))`, fixture.id, fixture.name)
	}

	mustPhysicalEOLExec(t, db, `INSERT INTO stream_settings
		(stream_id,discord_target_mode,discord_guild_id,discord_text_channel_id,discord_voice_channel_id,updated_at)
		VALUES (?,'manual','1001','1002','1003',NOW(6))`, physicalEOLManualStreamID)
	mustPhysicalEOLExec(t, db, `INSERT INTO stream_settings
		(stream_id,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,
		 discord_guild_id,discord_text_channel_id,discord_voice_channel_id,updated_at)
		VALUES (?,'preset',?,7,'2001','2002','2003',NOW(6))`, physicalEOLPresetStreamID, physicalEOLPresetID)
	mustPhysicalEOLExec(t, db, `INSERT INTO stream_settings
		(stream_id,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,
		 discord_guild_id,discord_text_channel_id,discord_voice_channel_id,updated_at)
		VALUES (?,'preset',?,7,'3001','3002','3003',NOW(6))`, physicalEOLMismatchID, physicalEOLPresetID)
	mustPhysicalEOLExec(t, db, `INSERT INTO stream_settings
		(stream_id,discord_guild_id,discord_text_channel_id,discord_voice_channel_id,updated_at)
		VALUES (?,'4001','4002','4003',NOW(6))`, physicalEOLLegacyStreamID)
	mustPhysicalEOLExec(t, db, `INSERT INTO stream_visual_settings
		(stream_id,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,
		 discord_snapshot_revision,discord_guild_id,discord_text_channel_id,discord_voice_channel_id,
		 created_at,updated_at)
		VALUES (?,'preset',?,7,1,'9999','3002','3003',NOW(6),NOW(6))`, physicalEOLMismatchID, physicalEOLPresetID)

	mustPhysicalEOLExec(t, db, `INSERT INTO stream_artifacts
		(id,stream_id,kind,name,relative_path,size_bytes,created_at)
		VALUES ('artifact-video',?,'video','capture.ts',?,10,NOW(6))`,
		physicalEOLManualStreamID, "final/"+physicalEOLManualStreamID+"/capture.ts")
	mustPhysicalEOLExec(t, db, `INSERT INTO stream_artifacts
		(id,stream_id,kind,name,relative_path,size_bytes,created_at)
		VALUES ('artifact-audio',?,'audio','capture.ts',?,20,NOW(6))`,
		physicalEOLManualStreamID, "final/"+physicalEOLManualStreamID+"/duplicate-copy.ts")

	mustPhysicalEOLExec(t, db, `INSERT INTO services
		(service_id,service_type,service_name,transport_mode,execution_host_id,ownership_epoch,
		 public_url,version,status,capabilities,metrics,token_id,created_at,updated_at)
		VALUES (?,'update_agent','Bundle 8B updater','pull_v2',?,1,
		 'https://updater.example.com','v2','online',JSON_OBJECT(),JSON_OBJECT(),?,NOW(6),NOW(6))`,
		physicalEOLAgentServiceID, physicalEOLExecutionHost, "33333333-3333-4333-8333-333333333333")
	mustPhysicalEOLExec(t, db, `INSERT INTO system_update_execution_hosts
		(execution_host_id,transport_mode,agent_service_id,legacy_agent_service_id,
		 ownership_epoch,policy_revision,created_at,updated_at)
		VALUES (?,'pull_v2',?,?,1,5,NOW(6),NOW(6))`,
		physicalEOLExecutionHost, physicalEOLAgentServiceID, physicalEOLAgentServiceID)
}

func assertPhysicalEOLEOLBlocked(t *testing.T, db *sql.DB) {
	t.Helper()
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='stream_settings' AND column_name='discord_guild_id'`, 1)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='system_update_execution_hosts' AND column_name='legacy_agent_service_id'`, 1)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.views
		WHERE table_schema=DATABASE() AND table_name='v2_migration_bundle8a_counts'`, 1)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations
		WHERE id='081_bundle8b_physical_eol.sql'`, 0)
}

func assertPhysicalEOLEOLComplete(t *testing.T, db *sql.DB) {
	t.Helper()
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='stream_settings'
		  AND column_name IN ('discord_target_mode','discord_target_preset_id','discord_target_preset_revision',
		                      'discord_guild_id','discord_text_channel_id','discord_voice_channel_id')`, 0)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name='system_update_execution_hosts' AND column_name='legacy_agent_service_id'`, 0)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM information_schema.views
		WHERE table_schema=DATABASE() AND table_name='v2_migration_bundle8a_counts'`, 0)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM schema_migrations
		WHERE id='081_bundle8b_physical_eol.sql'`, 1)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT mismatch_count FROM v2_migration_bundle8b_gate WHERE gate_id=1`, 0)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup`, 2)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup AS backup
		JOIN stream_artifacts AS current ON current.id=backup.artifact_id`, 2)
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup AS backup
		LEFT JOIN stream_artifacts AS current ON current.id=backup.artifact_id WHERE current.id IS NULL`, 0)
}

func assertPhysicalEOLArtifactPaths(t *testing.T, db *sql.DB) {
	t.Helper()
	runID := "legacy-aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa"
	for _, fixture := range []struct {
		id           string
		name         string
		originalPath string
	}{
		{"artifact-video", "capture.ts", "final/" + physicalEOLManualStreamID + "/capture.ts"},
		{"artifact-audio", "audio.aac", "final/" + physicalEOLManualStreamID + "/audio.aac"},
	} {
		var archiveRunID, currentPath, originalPath, fingerprint, calculated string
		err := db.QueryRow(`SELECT current.archive_run_id,current.relative_path,backup.relative_path,
			backup.relative_path_fingerprint,SHA2(backup.relative_path,256)
			FROM stream_artifacts AS current
			JOIN v2_migration_archive_artifacts_backup AS backup ON backup.artifact_id=current.id
			WHERE current.id=?`, fixture.id).Scan(&archiveRunID, &currentPath, &originalPath, &fingerprint, &calculated)
		if err != nil {
			t.Fatal(err)
		}
		wantCurrent := "final/" + physicalEOLManualStreamID + "/" + runID + "/" + fixture.name
		if archiveRunID != runID || currentPath != wantCurrent || originalPath != fixture.originalPath || fingerprint != calculated {
			t.Fatalf("artifact %s migration mismatch: run=%q path=%q original=%q fingerprint_match=%v",
				fixture.id, archiveRunID, currentPath, originalPath, fingerprint == calculated)
		}
		if strings.Count(currentPath, runID) != 1 {
			t.Fatalf("artifact %s path was double-prefixed: %q", fixture.id, currentPath)
		}
	}
}

func assertPhysicalEOLDiscordSnapshots(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, fixture := range []struct {
		streamID       string
		mode           string
		presetID       string
		presetRevision uint64
		guild          string
		text           string
		voice          string
	}{
		{physicalEOLManualStreamID, "manual", "", 0, "1001", "1002", "1003"},
		{physicalEOLPresetStreamID, "preset", physicalEOLPresetID, 7, "2001", "2002", "2003"},
		{physicalEOLMismatchID, "preset", physicalEOLPresetID, 7, "3001", "3002", "3003"},
		{physicalEOLLegacyStreamID, "manual", "", 0, "4001", "4002", "4003"},
	} {
		var mode, presetID, guild, textID, voice string
		var presetRevision uint64
		err := db.QueryRow(`SELECT discord_target_mode,COALESCE(discord_target_preset_id,''),
			COALESCE(discord_target_preset_revision,0),COALESCE(discord_guild_id,''),
			COALESCE(discord_text_channel_id,''),COALESCE(discord_voice_channel_id,'')
			FROM stream_visual_settings WHERE stream_id=?`, fixture.streamID).
			Scan(&mode, &presetID, &presetRevision, &guild, &textID, &voice)
		if err != nil {
			t.Fatal(err)
		}
		if mode != fixture.mode || presetID != fixture.presetID || presetRevision != fixture.presetRevision ||
			guild != fixture.guild || textID != fixture.text || voice != fixture.voice {
			t.Fatalf("Discord v2 snapshot mismatch for %s", fixture.streamID)
		}
	}
}

func assertPhysicalEOLRollbackRecords(t *testing.T, db *sql.DB) {
	t.Helper()
	var mode, presetID, guild, textID, voice string
	var presetRevision uint64
	if err := db.QueryRow(`SELECT settings_target_mode,COALESCE(settings_target_preset_id,''),
		COALESCE(settings_target_preset_revision,0),COALESCE(discord_guild_id,''),
		COALESCE(discord_text_channel_id,''),COALESCE(discord_voice_channel_id,'')
		FROM v2_migration_discord_targets_backup WHERE stream_id=?`, physicalEOLPresetStreamID).
		Scan(&mode, &presetID, &presetRevision, &guild, &textID, &voice); err != nil {
		t.Fatal(err)
	}
	if mode != "preset" || presetID != physicalEOLPresetID || presetRevision != 7 ||
		guild != "2001" || textID != "2002" || voice != "2003" {
		t.Fatal("Discord rollback record did not preserve the exact legacy target")
	}

	var hostID, agentID string
	if err := db.QueryRow(`SELECT execution_host_id,legacy_agent_service_id
		FROM v2_migration_legacy_agent_export WHERE execution_host_id=?`, physicalEOLExecutionHost).
		Scan(&hostID, &agentID); err != nil {
		t.Fatal(err)
	}
	if hostID != physicalEOLExecutionHost || agentID != physicalEOLAgentServiceID {
		t.Fatal("legacy updater owner export is not exact")
	}
	assertPhysicalEOLSchemaObjectCount(t, db, `SELECT COUNT(*) FROM v2_migration_update_hosts_backup
		WHERE execution_host_id='bundle8b-host' AND legacy_agent_service_id='bundle8b-update-agent'`, 1)
}

func assertPhysicalEOLSchemaObjectCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("query result=%d want=%d: %s", got, want, query)
	}
}

func mustPhysicalEOLExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("fixture statement failed: %v\n%s", err, statement)
	}
}
