package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
)

func TestMariaDBSessionRefreshZeroChangedPreservesExpiryAndRevocation(t *testing.T) {
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
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var precision int
	if err = db.QueryRowContext(ctx, `SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='sessions' AND COLUMN_NAME='idle_expires_at'`).Scan(&precision); err != nil || precision != 0 {
		t.Fatalf("precision=%d err=%v", precision, err)
	}
	auth := NewMariaDBAuthStore(db)
	var userID string
	if err = db.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		user, createErr := auth.CreateFirstAdmin(ctx, "r3-session-regression", "test-only-password-188", nil)
		if createErr != nil {
			t.Fatal(createErr)
		}
		userID = user.ID
	}
	session, err := auth.CreateSession(ctx, userID, 10*time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := auth.DeleteSession(context.Background(), session.Token); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC().Truncate(time.Second)
	if _, err = db.ExecContext(ctx, `UPDATE sessions SET idle_expires_at=?, absolute_expires_at=? WHERE id=?`, now.Add(10*time.Minute), now.Add(time.Hour), session.TokenHash); err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetSession(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, `UPDATE sessions SET idle_expires_at=? WHERE id=? AND idle_expires_at>? AND absolute_expires_at>?`, now.Add(10*time.Minute), session.TokenHash, now, now)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 0 {
		t.Fatalf("zero changed prerequisite=%d err=%v", changed, err)
	}
	current, err := auth.refreshSessionAt(ctx, session.Token, 10*time.Minute, now)
	if err != nil || !reflect.DeepEqual(before, current) {
		t.Fatalf("same expiry must return DB session, err=%v", err)
	}
	capped, err := auth.refreshSessionAt(ctx, session.Token, 2*time.Hour, now)
	if err != nil || !capped.IdleExpiresAt.Equal(before.AbsoluteExpiresAt) {
		t.Fatalf("absolute cap err=%v", err)
	}
	cappedAgain, err := auth.refreshSessionAt(ctx, session.Token, 2*time.Hour, now)
	if err != nil || !cappedAgain.IdleExpiresAt.Equal(before.AbsoluteExpiresAt) {
		t.Fatalf("zero changed at absolute cap err=%v", err)
	}
	if _, err = auth.refreshSessionAt(ctx, session.Token, time.Minute, before.AbsoluteExpiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("strict absolute boundary=%v", err)
	}
	if err = auth.DeleteSession(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.RefreshSession(ctx, session.Token, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked=%v", err)
	}
	if _, err = auth.RefreshSession(ctx, "unknown-token", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown=%v", err)
	}
	for _, ttl := range []struct{ idle, absolute time.Duration }{{-time.Second, time.Hour}, {time.Hour, -time.Second}} {
		expired, createErr := auth.CreateSession(ctx, userID, ttl.idle, ttl.absolute)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err = auth.RefreshSession(ctx, expired.Token, time.Minute); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired accepted: %v", err)
		}
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err = auth.RefreshSession(canceled, "unknown-token", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled=%v", err)
	}
}
