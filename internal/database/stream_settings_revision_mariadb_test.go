package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

func TestMariaDBStreamSettingsRevisionConcurrentWritersAndLifecycleIsolation(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	var precision int
	if err := db.QueryRowContext(ctx, `SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stream_settings' AND COLUMN_NAME='updated_at'`).Scan(&precision); err != nil || precision != 6 {
		t.Fatalf("settings revision needs retained microseconds: precision=%d error=%v", precision, err)
	}
	streams := store.NewMariaDBStreamStore(db)
	stream, err := streams.CreateStream(ctx, "settings revision")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM streams WHERE id=?`, stream.ID) })
	if stream.SettingsRevision != store.UnconfiguredStreamSettingsRevision {
		t.Fatalf("settings without a row have no stable sentinel: %q", stream.SettingsRevision)
	}
	legacy := stream.SettingsRevision
	stream, err = streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: "configured", ExpectedSettingsRevision: &legacy})
	if err != nil {
		t.Fatal(err)
	}
	// This isolated fixture places the settings clock ahead of the wall clock.
	// Every subsequent writer must consume exactly one persisted microsecond;
	// actual clock resolution cannot accidentally make this regression pass.
	previous := time.Date(2099, 10, 28, 0, 0, 0, 123456000, time.UTC)
	if _, err = db.ExecContext(ctx, `UPDATE stream_settings SET updated_at=? WHERE stream_id=?`, previous, stream.ID); err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil || stream.SettingsRevision != previous.Format(time.RFC3339Nano) {
		t.Fatalf("database did not retain exact revision: %q error=%v", stream.SettingsRevision, err)
	}
	baseline := stream.SettingsRevision
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	if err = blocker.QueryRowContext(ctx, `SELECT id FROM streams WHERE id=? FOR UPDATE`, stream.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	var blockerID uint64
	var schema string
	if err = blocker.QueryRowContext(ctx, `SELECT CONNECTION_ID(),DATABASE()`).Scan(&blockerID, &schema); err != nil || blockerID == 0 || schema == "" {
		t.Fatal("held stream lock has no verified connection/database identity")
	}
	diagnostic, closeDiagnostic := streamSettingsLockDiagnosticPool(t, db, schema)
	defer closeDiagnostic()
	results := make(chan error, 2)
	for _, name := range []string{"first contender", "second contender"} {
		go func() {
			_, updateErr := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: name, ExpectedSettingsRevision: &baseline})
			results <- updateErr
		}()
	}
	// Bind the actual wait graph to this transaction's one held PRIMARY row.
	// Prepared-statement INFO can retain '?' rather than the stream ID.
	const waitQuery = `SELECT COUNT(DISTINCT requesting.trx_mysql_thread_id)
FROM information_schema.INNODB_LOCK_WAITS waits
JOIN information_schema.INNODB_TRX requesting ON requesting.trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_TRX blocking ON blocking.trx_id = waits.blocking_trx_id
JOIN information_schema.INNODB_LOCKS requested ON requested.lock_id = waits.requested_lock_id AND requested.lock_trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_LOCKS held ON held.lock_id = waits.blocking_lock_id AND held.lock_trx_id = waits.blocking_trx_id
WHERE blocking.trx_mysql_thread_id = ? AND requesting.trx_mysql_thread_id <> ?
  AND requested.lock_table = ? AND held.lock_table = ?
  AND requested.lock_index = 'PRIMARY' AND held.lock_index = 'PRIMARY'
  AND requested.lock_type = 'RECORD' AND held.lock_type = 'RECORD'
  AND requested.lock_space = held.lock_space
  AND requested.lock_page = held.lock_page AND requested.lock_rec = held.lock_rec`
	observeCtx, cancelObservation := context.WithTimeout(ctx, 5*time.Second)
	defer cancelObservation()
	table := "`" + schema + "`.`streams`"
	waiting := 0
	for {
		if err = diagnostic.QueryRowContext(observeCtx, waitQuery, blockerID, blockerID, table, table).Scan(&waiting); err != nil {
			if observeCtx.Err() != nil {
				t.Fatalf("both writers did not actually reach the held stream lock: waiting=%d", waiting)
			}
			t.Fatal("cannot observe the real competing settings row-lock waits")
		}
		if waiting == 2 {
			t.Log("observed two distinct settings writers waiting on the held stream PRIMARY row")
			break
		}
		select {
		case <-observeCtx.Done():
			t.Fatalf("both writers did not actually reach the held stream lock: waiting=%d", waiting)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	passed, conflicts := 0, 0
	for range 2 {
		select {
		case err = <-results:
			if err == nil {
				passed++
			} else if errors.Is(err, store.ErrRevisionConflict) {
				conflicts++
			} else {
				t.Fatalf("unexpected writer result: %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil || passed != 1 || conflicts != 1 || !strings.HasSuffix(stream.Name, "contender") || stream.SettingsRevision != previous.Add(time.Microsecond).Format(time.RFC3339Nano) {
		t.Fatalf("competing writers: success=%d conflict=%d name=%q revision=%q error=%v", passed, conflicts, stream.Name, stream.SettingsRevision, err)
	}
	current := stream.SettingsRevision
	if _, err = streams.UpdateStreamStatus(ctx, stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil || stream.SettingsRevision != current {
		t.Fatalf("lifecycle-only write changed settings baseline: %q error=%v", stream.SettingsRevision, err)
	}
	stream, err = streams.UpdateStreamEncoderRuntimeSettingsRevision(ctx, stream.ID, -3, "", &current)
	if err != nil || stream.SettingsRevision != previous.Add(2*time.Microsecond).Format(time.RFC3339Nano) {
		t.Fatalf("runtime settings lost precision: %q error=%v", stream.SettingsRevision, err)
	}
	if _, err = streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: "stale overwrite", ExpectedSettingsRevision: &current}); !errors.Is(err, store.ErrRevisionConflict) {
		t.Fatalf("runtime settings did not invalidate an older settings writer: %v", err)
	}
	after, err := streams.GetStream(ctx, stream.ID)
	if err != nil || after.Name != stream.Name || after.EncoderAudioGainDB != -3 || after.SettingsRevision != stream.SettingsRevision {
		t.Fatalf("rejected writer changed retained settings: gain=%v revision=%q error=%v", after.EncoderAudioGainDB, after.SettingsRevision, err)
	}
}

func streamSettingsLockDiagnosticPool(t *testing.T, application *sql.DB, schema string) (*sql.DB, func()) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_LOCK_DIAGNOSTIC_DSN"))
	if dsn == "" {
		// Local fixtures may already have PROCESS authority. Missing authority
		// still fails the observation; it never skips or weakens the proof.
		return application, func() {}
	}
	management, managementErr := mysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	mutation, mutationErr := mysql.ParseDSN(strings.TrimPrefix(strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")), "mysql://"))
	if managementErr != nil || mutationErr != nil || management.Net != mutation.Net || management.Addr != mutation.Addr || management.DBName != schema || mutation.DBName != schema {
		t.Fatal("settings lock diagnostic endpoint/database authority does not match the application")
	}
	management.Logger = &mysql.NopLogger{}
	for _, duration := range []*time.Duration{&management.Timeout, &management.ReadTimeout, &management.WriteTimeout} {
		if *duration <= 0 || *duration > 2*time.Second {
			*duration = 2 * time.Second
		}
	}
	connector, err := mysql.NewConnector(management)
	if err != nil {
		t.Fatal("cannot open the settings lock diagnostic connection")
	}
	diagnostic := sql.OpenDB(connector)
	diagnostic.SetMaxOpenConns(1)
	diagnostic.SetMaxIdleConns(1)
	return diagnostic, func() { _ = diagnostic.Close() }
}
