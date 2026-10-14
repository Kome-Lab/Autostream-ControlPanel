package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
)

func TestMariaDBProfileUpdateZeroChangedReadsExistingRow(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var precision int
	if err = db.QueryRowContext(ctx, `SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='profiles' AND COLUMN_NAME='updated_at'`).Scan(&precision); err != nil || precision != 0 {
		t.Fatalf("saved timestamp precision=%d err=%v", precision, err)
	}
	profiles := NewMariaDBProfileStore(db)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, kind := range []ProfileKind{ProfileEncoder, ProfileArchive, ProfileCaption, ProfileOverlay, ProfileDiscordConfig, ProfileYouTubeOutput} {
		t.Run(string(kind), func(t *testing.T) {
			config := map[string]any{"width": float64(854)}
			created, err := profiles.CreateProfile(ctx, kind, "R3 zero changed", config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := db.ExecContext(context.Background(), `DELETE FROM profiles WHERE id=? AND kind=?`, created.ID, string(kind)); err != nil {
					t.Error(err)
				}
			})
			body, _ := json.Marshal(config)
			before, err := profiles.GetProfile(ctx, kind, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, `UPDATE profiles SET updated_at=? WHERE id=? AND kind=?`, fixed, created.ID, string(kind)); err != nil {
				t.Fatal(err)
			}
			result, err := db.ExecContext(ctx, `UPDATE profiles SET name=?, config=?, updated_at=? WHERE id=? AND kind=?`, created.Name, string(body), fixed, created.ID, string(kind))
			if err != nil {
				t.Fatal(err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 0 {
				t.Fatalf("real changed rows=%d err=%v; zero-change precondition failed", affected, err)
			}
			saved, err := profiles.updateProfileAt(ctx, kind, created.ID, created.Name, config, fixed)
			if err != nil {
				t.Fatalf("same saved value update: %v", err)
			}
			read, err := profiles.GetProfile(ctx, kind, created.ID)
			if err != nil || !reflect.DeepEqual(saved, read) || saved.ID != created.ID || saved.Kind != kind || saved.Name != created.Name || !reflect.DeepEqual(saved.Config, config) {
				t.Fatalf("saved DB result mismatch err=%v", err)
			}
			if kind == ProfileYouTubeOutput && saved.YouTubeRelayBindingRevision != before.YouTubeRelayBindingRevision+1 {
				t.Fatal("YouTube revision increment lost")
			}
			changed, err := profiles.updateProfileAt(ctx, kind, created.ID, "changed", map[string]any{"width": float64(1280)}, fixed)
			if err != nil || changed.Name != "changed" || changed.Config["width"] != float64(1280) {
				t.Fatalf("changed update err=%v", err)
			}
			if _, err = profiles.updateProfileAt(ctx, kind, "00000000-0000-0000-0000-000000000000", "missing", config, fixed); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing err=%v", err)
			}
			wrong := ProfileEncoder
			if kind == wrong {
				wrong = ProfileArchive
			}
			if _, err = profiles.updateProfileAt(ctx, wrong, created.ID, "wrong kind", config, fixed); !errors.Is(err, ErrNotFound) {
				t.Fatalf("wrong kind err=%v", err)
			}
			if err = profiles.DeleteProfile(ctx, wrong, created.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("wrong kind delete err=%v", err)
			}
			if err = profiles.DeleteProfile(ctx, kind, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing delete err=%v", err)
			}
			var count int
			if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE id=?`, created.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("profile identity count=%d err=%v", count, err)
			}
		})
	}
}
