package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
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
	results := make(chan error, 2)
	for _, name := range []string{"first contender", "second contender"} {
		go func() {
			_, updateErr := streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: name, ExpectedSettingsRevision: &baseline})
			results <- updateErr
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		pattern := "SELECT id FROM streams WHERE id = %" + stream.ID + "%FOR UPDATE%"
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID<>CONNECTION_ID() AND DB=DATABASE() AND INFO LIKE ?`, pattern).Scan(&waiting); err != nil {
			t.Fatalf("cannot observe the real competing settings reads: %v", err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("both writers did not actually reach the held stream lock: waiting=%d", waiting)
		}
		time.Sleep(10 * time.Millisecond)
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
