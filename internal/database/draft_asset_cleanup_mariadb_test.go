package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBDraftAssetCleanupSerializesWithClaimAndProtectsSavedAssets(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	userID := controlPlatformTestUUID(t)
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES(?,?,'test-hash','active',?,?)`, userID, "draft-cleanup-"+userID[:8], now, now); err != nil {
		t.Fatal(err)
	}
	stream, err := store.NewMariaDBStreamStore(db).CreateStream(ctx, "Draft cleanup claim target")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM streams WHERE id=?`, stream.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM media_assets WHERE owner_user_id=?`, userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM media_upload_sessions WHERE user_id=?`, userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id=?`, userID)
	})
	media, err := mediaassets.NewMariaDBRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newDraft := func() (mediaassets.UploadSession, mediaassets.Asset) {
		t.Helper()
		session, err := media.CreateUploadSession(ctx, userID, time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 32, 18, false))})
		if err != nil {
			t.Fatal(err)
		}
		return session, asset
	}
	wait := func(result <-chan error) error {
		t.Helper()
		select {
		case err := <-result:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("draft cleanup/claim did not converge")
			return nil
		}
	}

	t.Run("claim commits while stale cleanup waits", func(t *testing.T) {
		session, asset := newDraft()
		claimTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer claimTx.Rollback()
		if err = media.ClaimDraftTx(ctx, claimTx, userID, session.ID, stream.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		// This is the real claim's session/asset lock phase, with the ownership
		// updates still uncommitted when stale cleanup starts.
		cleanupResult := make(chan error, 1)
		go func() {
			cleanupResult <- media.SoftDeleteDraftAsset(ctx, userID, asset.ID, session.ID, time.Now().UTC())
		}()
		deadline := time.Now().Add(5 * time.Second)
		for db.Stats().InUse < 2 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if db.Stats().InUse < 2 {
			t.Fatal("cleanup did not enter its transaction behind the claim lock")
		}
		if err = claimTx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = wait(cleanupResult); !errors.Is(err, mediaassets.ErrDraftClaimed) {
			t.Fatalf("stale cleanup error=%v", err)
		}
		var ownerType, ownerID string
		var deleted sql.NullTime
		if err = db.QueryRowContext(ctx, `SELECT owner_type,owner_id,deleted_at FROM media_assets WHERE id=?`, asset.ID).Scan(&ownerType, &ownerID, &deleted); err != nil || ownerType != "stream" || ownerID != stream.ID || deleted.Valid {
			t.Fatalf("claimed asset owner=%s streamMatch=%t deleted=%t err=%v", ownerType, ownerID == stream.ID, deleted.Valid, err)
		}
	})

	t.Run("cleanup owns session before later claim", func(t *testing.T) {
		session, asset := newDraft()
		assetBlocker, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer assetBlocker.Rollback()
		var locked string
		if err = assetBlocker.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE id=? FOR UPDATE`, asset.ID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		cleanupResult := make(chan error, 1)
		go func() {
			cleanupResult <- media.SoftDeleteDraftAsset(ctx, userID, asset.ID, session.ID, time.Now().UTC())
		}()
		// Cleanup must own the session before waiting on this held asset row.
		// An independent NOWAIT observer proves that lock ordering directly.
		deadline := time.Now().Add(5 * time.Second)
		sessionLocked := false
		for time.Now().Before(deadline) {
			observer, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = observer.QueryRowContext(ctx, `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE NOWAIT`, session.ID).Scan(&locked)
			_ = observer.Rollback()
			if err != nil {
				var lockError *mysql.MySQLError
				if !errors.As(err, &lockError) || lockError.Number != 1205 {
					t.Fatalf("cleanup lock observer error=%v", err)
				}
				sessionLocked = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !sessionLocked {
			t.Fatal("cleanup did not acquire its session lock before the asset lock")
		}
		claimResult := make(chan error, 1)
		go func() {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				claimResult <- err
				return
			}
			defer tx.Rollback()
			if err = media.ClaimDraftTx(ctx, tx, userID, session.ID, stream.ID, time.Now().UTC()); err == nil {
				err = tx.Commit()
			}
			claimResult <- err
		}()
		if err = assetBlocker.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = wait(cleanupResult); err != nil {
			t.Fatalf("cleanup winner error=%v", err)
		}
		if err = wait(claimResult); err != nil {
			t.Fatalf("later claim error=%v", err)
		}
		var ownerType string
		var deleted sql.NullTime
		if err = db.QueryRowContext(ctx, `SELECT owner_type,deleted_at FROM media_assets WHERE id=?`, asset.ID).Scan(&ownerType, &deleted); err != nil || ownerType != "upload_draft" || !deleted.Valid {
			t.Fatalf("cleaned asset was transferred owner=%s deleted=%t err=%v", ownerType, deleted.Valid, err)
		}
	})
}
