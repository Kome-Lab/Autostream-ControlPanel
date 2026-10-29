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
	poolBaseline := db.Stats()
	results := make(chan error, 2)
	for _, name := range []string{"first contender", "second contender"} {
		go func() {
			_, updateErr := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: name, ExpectedSettingsRevision: &baseline})
			results <- updateErr
		}()
	}
	// Bind the actual wait graph to this transaction's one held PRIMARY row.
	// An exclusive waiter can queue behind another waiter on that same row;
	// both direct and queued predecessors must lead back to our held lock.
	// Prepared-statement INFO can retain '?' rather than the stream ID.
	const waitQuery = `WITH RECURSIVE edges AS (
SELECT requesting.trx_mysql_thread_id AS requesting_thread,
       blocking.trx_mysql_thread_id AS blocking_thread,
       requested.lock_space, requested.lock_page, requested.lock_rec
FROM information_schema.INNODB_LOCK_WAITS waits
JOIN information_schema.INNODB_TRX requesting ON requesting.trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_TRX blocking ON blocking.trx_id = waits.blocking_trx_id
JOIN information_schema.INNODB_LOCKS requested ON requested.lock_id = waits.requested_lock_id AND requested.lock_trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_LOCKS held ON held.lock_id = waits.blocking_lock_id AND held.lock_trx_id = waits.blocking_trx_id
WHERE requested.lock_table = ? AND held.lock_table = ?
  AND requested.lock_index = 'PRIMARY' AND held.lock_index = 'PRIMARY'
  AND requested.lock_type = 'RECORD' AND held.lock_type = 'RECORD'
  AND requested.lock_space = held.lock_space
  AND requested.lock_page = held.lock_page AND requested.lock_rec = held.lock_rec
), blocked AS (
SELECT requesting_thread, blocking_thread, lock_space, lock_page, lock_rec
FROM edges WHERE blocking_thread = ?
UNION DISTINCT
SELECT successor_edge.requesting_thread, successor_edge.blocking_thread, successor_edge.lock_space, successor_edge.lock_page, successor_edge.lock_rec
FROM edges successor_edge JOIN blocked predecessor_edge ON successor_edge.blocking_thread = predecessor_edge.requesting_thread
  AND successor_edge.lock_space = predecessor_edge.lock_space
  AND successor_edge.lock_page = predecessor_edge.lock_page AND successor_edge.lock_rec = predecessor_edge.lock_rec
)
SELECT COUNT(DISTINCT requesting_thread),
       COUNT(DISTINCT CASE WHEN blocking_thread = ? THEN requesting_thread END)
FROM blocked WHERE requesting_thread <> ?`
	observeCtx, cancelObservation := context.WithTimeout(ctx, 5*time.Second)
	defer cancelObservation()
	table := "`" + schema + "`.`streams`"
	diagnoseFailure := func() {
		logStreamSettingsLockFailure(t, ctx, db, diagnostic, results, poolBaseline, table)
	}
	waiting, directWaiting := 0, 0
	for {
		if err = diagnostic.QueryRowContext(observeCtx, waitQuery, table, table, blockerID, blockerID, blockerID).Scan(&waiting, &directWaiting); err != nil {
			if observeCtx.Err() != nil {
				diagnoseFailure()
				t.Fatalf("both writers did not actually reach the held stream lock: waiting=%d direct=%d", waiting, directWaiting)
			}
			diagnoseFailure()
			t.Fatal("cannot observe the real competing settings row-lock waits")
		}
		if waiting == 2 {
			t.Log("observed two distinct settings writers waiting on the held stream PRIMARY row")
			break
		}
		select {
		case <-observeCtx.Done():
			diagnoseFailure()
			t.Fatalf("both writers did not actually reach the held stream lock: waiting=%d direct=%d", waiting, directWaiting)
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

func logStreamSettingsLockFailure(t *testing.T, ctx context.Context, application, diagnostic *sql.DB, results <-chan error, baseline sql.DBStats, table string) {
	t.Helper()
	var outcomes [6]int
	for range 2 {
		select {
		case result := <-results:
			switch {
			case result == nil:
				outcomes[0]++
			case errors.Is(result, store.ErrRevisionConflict):
				outcomes[1]++
			case errors.Is(result, context.DeadlineExceeded):
				outcomes[2]++
			case errors.Is(result, context.Canceled):
				outcomes[3]++
			default:
				outcomes[4]++
			}
		default:
			outcomes[5]++
		}
	}
	t.Logf("settings lock diagnostic writers nil=%d revision_conflict=%d deadline=%d cancel=%d other=%d pending=%d", outcomes[0], outcomes[1], outcomes[2], outcomes[3], outcomes[4], outcomes[5])
	current := application.Stats()
	t.Logf("settings lock diagnostic pool before_max_open=%d before_open=%d before_in_use=%d before_idle=%d before_wait_count=%d after_max_open=%d after_open=%d after_in_use=%d after_idle=%d after_wait_count=%d wait_count_delta=%d", baseline.MaxOpenConnections, baseline.OpenConnections, baseline.InUse, baseline.Idle, baseline.WaitCount, current.MaxOpenConnections, current.OpenConnections, current.InUse, current.Idle, current.WaitCount, current.WaitCount-baseline.WaitCount)
	// These categories identify a failed observation without disclosing SQL,
	// connection identities, lock data, schema names or driver error text.
	const lockClassesQuery = `SELECT
COUNT(DISTINCT CASE WHEN index_class=0 AND type_class=0 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=0 AND type_class=1 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=0 AND type_class=2 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=1 AND type_class=0 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=1 AND type_class=1 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=1 AND type_class=2 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=2 AND type_class=0 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=2 AND type_class=1 THEN requesting_trx_id END),
COUNT(DISTINCT CASE WHEN index_class=2 AND type_class=2 THEN requesting_trx_id END)
FROM (
SELECT waits.requesting_trx_id,
       CASE requested.lock_index WHEN 'PRIMARY' THEN 0 WHEN 'idx_streams_deleted_at' THEN 1 ELSE 2 END AS index_class,
       CASE requested.lock_type WHEN 'RECORD' THEN 0 WHEN 'TABLE' THEN 1 ELSE 2 END AS type_class
FROM information_schema.INNODB_LOCK_WAITS waits
JOIN information_schema.INNODB_LOCKS requested ON requested.lock_id = waits.requested_lock_id AND requested.lock_trx_id = waits.requesting_trx_id
WHERE requested.lock_table = ?
) classified_waits`
	diagnosticCtx, cancelDiagnostic := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancelDiagnostic()
	var classes [9]int
	if err := diagnostic.QueryRowContext(diagnosticCtx, lockClassesQuery, table).Scan(&classes[0], &classes[1], &classes[2], &classes[3], &classes[4], &classes[5], &classes[6], &classes[7], &classes[8]); err != nil {
		category := "query_error"
		switch {
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(diagnosticCtx.Err(), context.DeadlineExceeded):
			category = "deadline"
		case errors.Is(err, context.Canceled) || errors.Is(diagnosticCtx.Err(), context.Canceled):
			category = "cancel"
		}
		t.Logf("settings lock diagnostic waits status=%s", category)
		return
	}
	t.Logf("settings lock diagnostic waits status=ok primary_record=%d primary_table=%d primary_other=%d deleted_record=%d deleted_table=%d deleted_other=%d other_record=%d other_table=%d other_other=%d", classes[0], classes[1], classes[2], classes[3], classes[4], classes[5], classes[6], classes[7], classes[8])
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
