package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMariaDBNodeSelectionMigrationPreservesOwnersAndBackfillsConfiguredBot(t *testing.T) {
	conn, ctx := openNodeSelectionMigrationConnection(t)
	for _, statement := range []string{
		`INSERT INTO streams(id,status,archive_run_id,archive_started_at,archive_reported_at,deleted_at,updated_at) VALUES
('live-owner','live',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('stop-owner','failed',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('archive-owner','ready','archive-run','2026-11-05 23:00:00.123456',NULL,NULL,'2026-11-06 00:00:00.123456'),
('configured-owner','ready',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('deleted-owner','completed',NULL,NULL,NULL,'2026-11-06 00:00:00.123456','2026-11-06 00:00:00.123456'),
('wrong-type-owner','created',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('missing-node-owner','created',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('wrong-kind-owner','created',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('empty-node-owner','created',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456'),
('preserved-owner','created',NULL,NULL,NULL,NULL,'2026-11-06 00:00:00.123456')`,
		`INSERT INTO services(service_id,service_type,current_stream_id,status,token_id,node_token_ciphertext,node_token_nonce,updated_at) VALUES
('encoder-live','encoder_recorder','live-owner','assigned','token-encoder','cipher-encoder','nonce-encoder','2026-11-06 00:00:00.123456'),
('worker-live','worker','live-owner','assigned','token-worker','cipher-worker','nonce-worker','2026-11-06 00:00:00.123456'),
('bot-live','discord_bot','live-owner','assigned','token-bot','cipher-bot','nonce-bot','2026-11-06 00:00:00.123456'),
('bot-other','discord_bot',NULL,'registered','token-other',NULL,NULL,'2026-11-06 00:00:00.123456'),
('encoder-stop','encoder_recorder','stop-owner','assigned','token-stop',NULL,NULL,'2026-11-06 00:00:00.123456'),
('encoder-archive','encoder_recorder','archive-owner','assigned','token-archive',NULL,NULL,'2026-11-06 00:00:00.123456'),
('encoder-configured','encoder_recorder','configured-owner','assigned','token-configured',NULL,NULL,'2026-11-06 00:00:00.123456'),
('worker-configured','worker','configured-owner','assigned','token-configured-worker',NULL,NULL,'2026-11-06 00:00:00.123456'),
('encoder-deleted','encoder_recorder','deleted-owner','assigned','token-deleted',NULL,NULL,'2026-11-06 00:00:00.123456'),
('worker-preserved','worker','preserved-owner','assigned','token-preserved',NULL,NULL,'2026-11-06 00:00:00.123456')`,
		`INSERT INTO profiles(id,kind,name,config,created_at,updated_at) VALUES
('profile-live','discord_config','live profile','{"service_id":"bot-other"}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321'),
('profile-configured','discord_config','configured profile','{"service_id":"bot-live"}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321'),
('profile-wrong-type','discord_config','wrong type','{"service_id":"worker-live"}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321'),
('profile-missing','discord_config','missing Node','{"service_id":"bot-missing"}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321'),
('profile-wrong-kind','encoder','wrong kind','{"service_id":"bot-live"}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321'),
('profile-empty','discord_config','empty Node','{"service_id":"  "}','2026-11-06 00:00:00.123456','2026-11-06 00:00:00.654321')`,
		`INSERT INTO stream_settings(stream_id,discord_config_id,updated_at) VALUES
('live-owner','profile-live','2026-11-06 00:00:00.654321'),
('configured-owner','profile-configured','2026-11-06 00:00:00.654321'),
('deleted-owner','profile-configured','2026-11-06 00:00:00.654321'),
('wrong-type-owner','profile-wrong-type','2026-11-06 00:00:00.654321'),
('missing-node-owner','profile-missing','2026-11-06 00:00:00.654321'),
('wrong-kind-owner','profile-wrong-kind','2026-11-06 00:00:00.654321'),
('empty-node-owner','profile-empty','2026-11-06 00:00:00.654321'),
('preserved-owner','profile-configured','2026-11-06 00:00:00.654321')`,
		`INSERT INTO stream_service_assignments(id,stream_id,service_id,service_type,assignment_role,assigned_by_user_id,assigned_at) VALUES
('assignment-encoder-live','live-owner','encoder-live','encoder_recorder','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-worker-live','live-owner','worker-live','worker','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-bot-live','live-owner','bot-live','discord_bot','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-stop','stop-owner','encoder-stop','encoder_recorder','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-archive','archive-owner','encoder-archive','encoder_recorder','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-configured-encoder','configured-owner','encoder-configured','encoder_recorder','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-configured-worker','configured-owner','worker-configured','worker','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-deleted','deleted-owner','encoder-deleted','encoder_recorder','primary','original-actor','2026-11-05 23:59:00.111111'),
('assignment-preserved','preserved-owner','worker-preserved','worker','primary','original-actor','2026-11-05 23:59:00.111111')`,
		`INSERT INTO stream_normal_stop_pending(stream_id,created_at) VALUES('stop-owner','2026-11-06 00:00:00.222222')`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate an interrupted migration after explicit clears were persisted.
	// Replaying migration090 must not reconstitute execution-derived choices.
	if _, err := conn.ExecContext(ctx, nodeSelectionMigrationTemporaryCreate(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO stream_node_selections(stream_id,service_type,service_id,created_at,updated_at) VALUES
('preserved-owner','worker','','2026-11-06 00:00:00.111111','2026-11-06 00:00:00.222222'),
('preserved-owner','discord_bot','','2026-11-06 00:00:00.111111','2026-11-06 00:00:00.222222')`); err != nil {
		t.Fatal(err)
	}
	protectedTables := []string{"streams", "stream_settings", "services", "stream_service_assignments", "profiles", "stream_normal_stop_pending"}
	before := make(map[string][][]string)
	for _, table := range protectedTables {
		before[table] = nodeSelectionMigrationRows(t, ctx, conn, table)
	}
	if err := replayNodeSelectionMigration(ctx, conn, t); err != nil {
		t.Fatalf("upgrade legacy Node selections: %v", err)
	}
	choices := nodeSelectionMigrationRows(t, ctx, conn, "stream_node_selections")
	got := make(map[string]map[string]string)
	for _, row := range choices {
		if got[row[0]] == nil {
			got[row[0]] = make(map[string]string)
		}
		got[row[0]][row[1]] = row[2]
	}
	want := map[string]map[string]string{
		"live-owner":       {"encoder_recorder": "encoder-live", "worker": "worker-live", "discord_bot": "bot-live"},
		"stop-owner":       {"encoder_recorder": "encoder-stop"},
		"archive-owner":    {"encoder_recorder": "encoder-archive"},
		"configured-owner": {"encoder_recorder": "encoder-configured", "worker": "worker-configured", "discord_bot": "bot-live"},
		"preserved-owner":  {"worker": "", "discord_bot": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy choices were not preserved or validated: got=%#v want=%#v", got, want)
	}
	var originalCreatedAt, originalUpdatedAt time.Time
	if err := conn.QueryRowContext(ctx, `SELECT created_at,updated_at FROM stream_node_selections WHERE stream_id='live-owner' AND service_type='encoder_recorder'`).Scan(&originalCreatedAt, &originalUpdatedAt); err != nil {
		t.Fatal(err)
	}
	assignedAt := time.Date(2026, 11, 5, 23, 59, 0, 111111000, time.UTC)
	if !originalCreatedAt.Equal(assignedAt) || !originalUpdatedAt.Equal(assignedAt) {
		t.Fatal("primary-assignment selection backfill changed its original timestamp")
	}
	if err := replayNodeSelectionMigration(ctx, conn, t); err != nil {
		t.Fatalf("replay migration090: %v", err)
	}
	if !reflect.DeepEqual(nodeSelectionMigrationRows(t, ctx, conn, "stream_node_selections"), choices) {
		t.Fatal("migration replay rewrote saved choices or their timestamps")
	}
	for _, table := range protectedTables {
		if !reflect.DeepEqual(nodeSelectionMigrationRows(t, ctx, conn, table), before[table]) {
			// Rows include synthetic credential storage; never print snapshots.
			t.Fatalf("selection migration changed protected %s rows", table)
		}
	}
}

func TestMariaDBNodeSelectionMigrationRejectsAmbiguousPrimaryChoice(t *testing.T) {
	conn, ctx := openNodeSelectionMigrationConnection(t)
	for _, statement := range []string{
		`INSERT INTO streams(id,status,updated_at) VALUES('ambiguous-owner','ready','2026-11-06 00:00:00.123456')`,
		`INSERT INTO services(service_id,service_type,current_stream_id,status,token_id,updated_at) VALUES
('worker-first','worker','ambiguous-owner','assigned','token-first','2026-11-06 00:00:00.123456'),
('worker-second','worker','ambiguous-owner','assigned','token-second','2026-11-06 00:00:00.123456')`,
		`INSERT INTO stream_service_assignments(id,stream_id,service_id,service_type,assignment_role,assigned_at) VALUES
('first-primary','ambiguous-owner','worker-first','worker','primary','2026-11-06 00:00:00.111111'),
('second-primary','ambiguous-owner','worker-second','worker','primary','2026-11-06 00:00:00.222222')`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	beforeAssignments := nodeSelectionMigrationRows(t, ctx, conn, "stream_service_assignments")
	beforeServices := nodeSelectionMigrationRows(t, ctx, conn, "services")
	err := replayNodeSelectionMigration(ctx, conn, t)
	var duplicate *mysql.MySQLError
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
		t.Fatalf("ambiguous primaries did not fail with a duplicate-role conflict: %v", err)
	}
	if len(nodeSelectionMigrationRows(t, ctx, conn, "stream_node_selections")) != 0 {
		t.Fatal("ambiguous legacy primary role was silently reduced to one choice")
	}
	if !reflect.DeepEqual(nodeSelectionMigrationRows(t, ctx, conn, "stream_service_assignments"), beforeAssignments) ||
		!reflect.DeepEqual(nodeSelectionMigrationRows(t, ctx, conn, "services"), beforeServices) {
		t.Fatal("failed backfill changed the old execution ownership")
	}
}

func openNodeSelectionMigrationConnection(t *testing.T) (*sql.Conn, context.Context) {
	t.Helper()
	db, ctx := openMariaDBMigrationTest(t, false)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, statement := range []string{
		`CREATE TEMPORARY TABLE streams (
id CHAR(36) PRIMARY KEY,status VARCHAR(80) NOT NULL,archive_run_id VARCHAR(128) NULL,
archive_started_at DATETIME(6) NULL,archive_reported_at DATETIME(6) NULL,
deleted_at DATETIME(6) NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stream_settings (
stream_id CHAR(36) PRIMARY KEY,discord_config_id CHAR(36) NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE profiles (
id CHAR(36) PRIMARY KEY,kind VARCHAR(40) NOT NULL,name VARCHAR(255) NOT NULL,
config JSON NOT NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE services (
service_id VARCHAR(128) PRIMARY KEY,service_type VARCHAR(40) NOT NULL,current_stream_id CHAR(36) NULL,
status VARCHAR(80) NOT NULL,token_id CHAR(36) NOT NULL,node_token_ciphertext TEXT NULL,
node_token_nonce VARCHAR(128) NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stream_service_assignments (
id CHAR(36) PRIMARY KEY,stream_id CHAR(36) NOT NULL,service_id VARCHAR(128) NOT NULL,
service_type VARCHAR(40) NOT NULL,assignment_role VARCHAR(20) NOT NULL,
assigned_by_user_id CHAR(36) NULL,assigned_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE stream_normal_stop_pending (
stream_id CHAR(36) PRIMARY KEY,created_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	return conn, ctx
}

func nodeSelectionMigrationTemporaryCreate(t *testing.T) string {
	t.Helper()
	body, err := embeddedMigrations.ReadFile("migrations/090_stream_node_selections.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range splitSQLStatements(string(body)) {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS stream_node_selections") {
			return strings.Replace(statement, "CREATE TABLE IF NOT EXISTS stream_node_selections", "CREATE TEMPORARY TABLE IF NOT EXISTS stream_node_selections", 1)
		}
	}
	t.Fatal("migration090 is missing its additive selection table")
	return ""
}

func replayNodeSelectionMigration(ctx context.Context, conn *sql.Conn, t *testing.T) error {
	t.Helper()
	body, err := embeddedMigrations.ReadFile("migrations/090_stream_node_selections.sql")
	if err != nil {
		return err
	}
	for _, statement := range splitSQLStatements(string(body)) {
		// Execute the real migration SQL against this connection's isolated
		// fixture tables. Only the target table's temporary scope is changed.
		statement = strings.Replace(statement, "CREATE TABLE IF NOT EXISTS stream_node_selections", "CREATE TEMPORARY TABLE IF NOT EXISTS stream_node_selections", 1)
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func nodeSelectionMigrationRows(t *testing.T, ctx context.Context, conn *sql.Conn, table string) [][]string {
	t.Helper()
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s ORDER BY 1,2", table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		record := make([]string, len(values))
		for index, value := range values {
			switch value := value.(type) {
			case nil:
				record[index] = "<NULL>"
			case []byte:
				record[index] = string(value)
			case time.Time:
				record[index] = value.UTC().Format(time.RFC3339Nano)
			default:
				record[index] = fmt.Sprint(value)
			}
		}
		snapshot = append(snapshot, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
