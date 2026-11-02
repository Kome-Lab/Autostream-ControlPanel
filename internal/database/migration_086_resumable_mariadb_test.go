package database

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/go-sql-driver/mysql"
)

func TestMariaDBUploadOperationMigrationResumesAfterPartialDDL(t *testing.T) {
    db, ctx := openMariaDBMigrationTest(t, false)
    conn, err := db.Conn(ctx)
    if err != nil { t.Fatal(err) }
    defer conn.Close()

    // Temporary tables isolate this migration witness from the shared CI schema.
    for _, statement := range []string{
        `CREATE TEMPORARY TABLE media_assets (id CHAR(36) PRIMARY KEY, upload_session_id VARCHAR(64) NULL)`,
        `CREATE TEMPORARY TABLE stream_video_cover_candidates (stream_id CHAR(36) NOT NULL, variant_id CHAR(36) NOT NULL, display_name VARCHAR(128) NOT NULL, PRIMARY KEY(stream_id,variant_id))`,
        `INSERT INTO media_assets(id,upload_session_id) VALUES ('legacy-null','legacy-session')`,
        `INSERT INTO media_assets(id,upload_session_id) VALUES ('legacy-null-operation',NULL)`,
        `INSERT INTO stream_video_cover_candidates(stream_id,variant_id,display_name) VALUES ('legacy-stream','legacy-variant','legacy name')`,
    } {
        if _, err := conn.ExecContext(ctx, statement); err != nil { t.Fatalf("prepare isolated migration fixture: %v", err) }
    }

    body, err := embeddedMigrations.ReadFile("migrations/086_upload_operation_and_candidate_names.sql")
    if err != nil { t.Fatal(err) }
    statements := splitSQLStatements(string(body))
    if len(statements) != 2 { t.Fatalf("migration 086 statement count=%d, want 2", len(statements)) }
    statements[0] = strings.ReplaceAll(statements[0], "media_assets", "media_assets")
    statements[1] = strings.ReplaceAll(statements[1], "stream_video_cover_candidates", "stream_video_cover_candidates")

    // Simulate interruption after DDL statement 1 committed, before statement 2.
    if _, err := conn.ExecContext(ctx, statements[0]); err != nil { t.Fatalf("initial first DDL: %v", err) }
    // A restarted migration runner replays statement 1 because schema_migrations
    // is only recorded after the whole migration completes.
    if _, err := conn.ExecContext(ctx, statements[0]); err != nil { t.Fatalf("replay first DDL after partial application: %v", err) }
    if _, err := conn.ExecContext(ctx, statements[1]); err != nil { t.Fatalf("apply second DDL: %v", err) }
    if _, err := conn.ExecContext(ctx, statements[1]); err != nil { t.Fatalf("replay second DDL: %v", err) }

    var legacyCount int
    if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE id IN ('legacy-null','legacy-null-operation')`).Scan(&legacyCount); err != nil || legacyCount != 2 {
        t.Fatalf("legacy media rows were not preserved: count=%d err=%v", legacyCount, err)
    }
    var legacyCandidate string
    if err := conn.QueryRowContext(ctx, `SELECT display_name FROM stream_video_cover_candidates WHERE stream_id='legacy-stream' AND variant_id='legacy-variant'`).Scan(&legacyCandidate); err != nil || legacyCandidate != "legacy name" {
        t.Fatalf("legacy cover candidate changed: name=%q err=%v", legacyCandidate, err)
    }
    longName := strings.Repeat("x", 255)
    if _, err := conn.ExecContext(ctx, `UPDATE stream_video_cover_candidates SET display_name=? WHERE stream_id='legacy-stream' AND variant_id='legacy-variant'`, longName); err != nil {
        t.Fatalf("255-character display name write failed: %v", err)
    }
    var gotName string
    if err := conn.QueryRowContext(ctx, `SELECT display_name FROM stream_video_cover_candidates WHERE stream_id='legacy-stream' AND variant_id='legacy-variant'`).Scan(&gotName); err != nil || gotName != longName {
        t.Fatalf("255-character display name round trip failed: length=%d err=%v", len(gotName), err)
    }
    if _, err := conn.ExecContext(ctx, `INSERT INTO media_assets(id,upload_session_id,client_operation_session_id,client_operation_id) VALUES ('op-one','s1','session-1','operation-1')`); err != nil {
        t.Fatalf("first operation identity insert failed: %v", err)
    }
    if _, err := conn.ExecContext(ctx, `INSERT INTO media_assets(id,upload_session_id,client_operation_session_id,client_operation_id) VALUES ('op-two','s2','session-1','operation-1')`); err == nil {
        t.Fatal("duplicate non-NULL operation identity was accepted")
    }
    if _, err := conn.ExecContext(ctx, `INSERT INTO media_assets(id,upload_session_id,client_operation_session_id,client_operation_id) VALUES ('op-null','s3',NULL,NULL)`); err != nil {
        t.Fatalf("legacy NULL operation identity must remain allowed: %v", err)
    }
    var duplicate mysql.MySQLError
    _ = errors.As(nil, &duplicate) // Keep the MySQL driver type available for actionable failure decoding if needed.
}
