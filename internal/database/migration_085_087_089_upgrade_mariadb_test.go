package database

import (
    "fmt"
    "testing"
)

func TestMariaDBAdditiveDeletionQueueMigrationPreservesExistingQueueRows(t *testing.T) {
    db, ctx := openMariaDBMigrationTest(t, false)
    conn, err := db.Conn(ctx)
    if err != nil { t.Fatal(err) }
    defer conn.Close()
    for _, statement := range []string{
        `CREATE TEMPORARY TABLE media_asset_file_deletions (storage_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, attempts INT UNSIGNED NOT NULL DEFAULT 0, last_error_code VARCHAR(80) NULL, created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, PRIMARY KEY(storage_key), INDEX idx_media_asset_file_deletions_retry(updated_at,storage_key))`,
        `INSERT INTO media_asset_file_deletions(storage_key,attempts,last_error_code,created_at,updated_at) VALUES('existing-key',2,'media_asset_file_remove_failed','2026-11-02 10:00:00','2026-11-02 10:01:00')`,
    } { if _, err := conn.ExecContext(ctx, statement); err != nil { t.Fatal(err) } }
    body, err := embeddedMigrations.ReadFile("migrations/085_media_asset_file_deletions.sql")
    if err != nil { t.Fatal(err) }
    statements := splitSQLStatements(string(body))
    if len(statements) != 1 { t.Fatalf("085 statement count=%d", len(statements)) }
    for attempt := 0; attempt < 2; attempt++ { if _, err := conn.ExecContext(ctx, statements[0]); err != nil { t.Fatalf("085 replay %d: %v", attempt+1, err) } }
    var attempts int
    var errorCode string
    if err := conn.QueryRowContext(ctx, `SELECT attempts,last_error_code FROM media_asset_file_deletions WHERE storage_key='existing-key'`).Scan(&attempts,&errorCode); err != nil || attempts != 2 || errorCode != "media_asset_file_remove_failed" {
        t.Fatalf("085 changed existing queue row: attempts=%d errorCode=%q err=%v", attempts,errorCode,err)
    }
}

func TestMariaDBRevisionPrecisionMigrationsPreserveExistingRows(t *testing.T) {
    db, ctx := openMariaDBMigrationTest(t, false)
    conn, err := db.Conn(ctx)
    if err != nil { t.Fatal(err) }
    defer conn.Close()
    fixtures := []struct { name string; create string; insert string; migration string; table string }{
        {"087", `CREATE TEMPORARY TABLE stream_settings (stream_id CHAR(36) PRIMARY KEY, updated_at DATETIME(0) NOT NULL)`, `INSERT INTO stream_settings VALUES('legacy-stream','2026-11-02 10:11:12')`, "087_stream_settings_revision_precision.sql", "stream_settings"},
        {"088_profiles", `CREATE TEMPORARY TABLE profiles (id CHAR(36) PRIMARY KEY, kind VARCHAR(40) NOT NULL, updated_at DATETIME(0) NOT NULL)`, `INSERT INTO profiles VALUES('legacy-profile','discord','2026-11-02 10:11:12')`, "088_profile_and_drive_revision_precision.sql", "profiles"},
        {"088_drive", `CREATE TEMPORARY TABLE drive_destinations (id CHAR(36) PRIMARY KEY, updated_at DATETIME(0) NOT NULL)`, `INSERT INTO drive_destinations VALUES('legacy-drive','2026-11-02 10:11:12')`, "088_profile_and_drive_revision_precision.sql", "drive_destinations"},
        {"089", `CREATE TEMPORARY TABLE system_settings (name VARCHAR(100) PRIMARY KEY, updated_at DATETIME(0) NOT NULL)`, `INSERT INTO system_settings VALUES('legacy-setting','2026-11-02 10:11:12')`, "089_system_settings_revision_precision.sql", "system_settings"},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            if _, err := conn.ExecContext(ctx, fixture.create); err != nil { t.Fatal(err) }
            if _, err := conn.ExecContext(ctx, fixture.insert); err != nil { t.Fatal(err) }
            body, err := embeddedMigrations.ReadFile("migrations/" + fixture.migration)
            if err != nil { t.Fatal(err) }
            for attempt, statement := range splitSQLStatements(string(body)) {
                if _, err := conn.ExecContext(ctx, statement); err != nil { t.Fatalf("migration statement %d: %v", attempt+1, err) }
            }
            var precision int
            if err := conn.QueryRowContext(ctx, `SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='updated_at'`, fixture.table).Scan(&precision); err != nil || precision != 6 {
                t.Fatalf("%s updated_at precision=%d err=%v", fixture.table,precision,err)
            }
            var preserved string
            query := fmt.Sprintf(`SELECT DATE_FORMAT(updated_at,'%%Y-%%m-%%d %%H:%%i:%%s.%%f') FROM %s`, fixture.table)
            if err := conn.QueryRowContext(ctx, query).Scan(&preserved); err != nil || preserved != "2026-11-02 10:11:12.000000" {
                t.Fatalf("%s legacy timestamp not preserved: %q err=%v", fixture.table,preserved,err)
            }
        })
    }
}
