package database

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

func TestMariaDBDraftCleanupAndGarbageCollectionUseSessionBeforeAsset(t *testing.T) {
	f := newCoverDeletionFixture(t)
	stream := f.stream()
	for index, contender := range []string{"draft cleanup", "stream claim"} {
		t.Run(contender, func(t *testing.T) {
			now := time.Now().UTC()
			session, err := f.media.CreateUploadSession(f.ctx, f.userID, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			asset, err := f.media.Upload(f.ctx, mediaassets.UploadInput{UserID: f.userID, SessionID: session.ID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 73+index*8, 6, false))})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.media.SoftDeleteDraftAsset(f.ctx, f.userID, asset.ID, session.ID, now.Add(-25*time.Hour)); err != nil {
				t.Fatal(err)
			}
			assetBlocker, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer assetBlocker.Rollback()
			var locked string
			if err := assetBlocker.QueryRowContext(f.ctx, `SELECT id FROM media_assets WHERE id=? FOR UPDATE`, asset.ID).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			gcResult := make(chan error, 1)
			go func() {
				_, err := f.media.GarbageCollect(f.ctx, now, 500)
				gcResult <- err
			}()
			// Hold the exact asset lock and observe the collector's session
			// lock independently. Before the fix GC waited on the asset first,
			// then could deadlock against a session-owning cleanup or claim.
			deadline := time.Now().Add(5 * time.Second)
			sessionLocked := false
			for time.Now().Before(deadline) {
				observer, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				err = observer.QueryRowContext(f.ctx, `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE NOWAIT`, session.ID).Scan(&locked)
				_ = observer.Rollback()
				if err != nil {
					var lockError *mysql.MySQLError
					if !errors.As(err, &lockError) || lockError.Number != 1205 {
						t.Fatalf("GC session lock observer error=%v", err)
					}
					sessionLocked = true
					break
				}
				select {
				case err := <-gcResult:
					t.Fatalf("GC escaped held asset lock: %v", err)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !sessionLocked {
				t.Fatal("GC did not acquire session before waiting for asset")
			}
			contenderResult := make(chan error, 1)
			go func() {
				if contender == "draft cleanup" {
					contenderResult <- f.media.SoftDeleteDraftAsset(f.ctx, f.userID, asset.ID, session.ID, now)
					return
				}
				tx, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					contenderResult <- err
					return
				}
				defer tx.Rollback()
				if err = f.media.ClaimDraftTx(f.ctx, tx, f.userID, session.ID, stream.ID, now); err == nil {
					err = tx.Commit()
				}
				contenderResult <- err
			}()
			deadline = time.Now().Add(5 * time.Second)
			for f.db.Stats().InUse < 3 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if f.db.Stats().InUse < 3 {
				t.Fatal("contender did not enter transaction behind GC session lock")
			}
			if err := assetBlocker.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-gcResult:
				if err != nil {
					t.Fatalf("GC/contender failed to converge: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("GC did not complete after asset lock release")
			}
			select {
			case err := <-contenderResult:
				if contender == "draft cleanup" && !errors.Is(err, mediaassets.ErrNotFound) || contender == "stream claim" && err != nil {
					t.Fatalf("contender result=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("contender did not complete after GC commit")
			}
			var retained int
			if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM media_assets WHERE id=?`, asset.ID).Scan(&retained); err != nil || retained != 0 {
				t.Fatalf("GC asset retained=%d err=%v", retained, err)
			}
			f.file(asset.StorageKey, false)
		})
	}
}
