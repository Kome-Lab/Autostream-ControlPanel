package database

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

func TestMariaDBCoverUploadLimitCreateEditOmissionLegacyAndConcurrentClaims(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	userID := controlPlatformTestUUID(t)
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES(?,?,'test-hash','active',?,?)`, userID, "cover-quota-"+userID[:8], now, now); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMariaDBStreamStore(db)
	stream, err := streams.CreateStream(ctx, "Cover upload quota")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM streams WHERE id=?`, stream.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM media_assets WHERE owner_user_id=?`, userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM media_upload_sessions WHERE user_id=?`, userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id=?`, userID)
	})
	if _, err = db.ExecContext(ctx, `INSERT INTO stream_visual_settings(stream_id,revision,created_at,updated_at) VALUES(?,1,?,?)`, stream.ID, now, now); err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMariaDBRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	visuals := streamvisual.NewMariaDBRepository(db, media)
	upload := func() (mediaassets.UploadSession, streamvisual.CoverCandidate) {
		t.Helper()
		session, err := media.CreateUploadSession(ctx, userID, time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, false))})
		if err != nil {
			t.Fatal(err)
		}
		variant, err := media.EnsureVariant(ctx, userID, asset.ID, 1920, 1080, true)
		if err != nil {
			t.Fatal(err)
		}
		return session, streamvisual.CoverCandidate{Name: "cover.png", Source: "upload", AssetID: asset.ID, VariantID: variant.ID}
	}
	claim := func(sessionID string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var locked string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id=? FOR UPDATE`, stream.ID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		if err = media.ClaimDraftTx(ctx, tx, userID, sessionID, stream.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var candidates []streamvisual.CoverCandidate
	for range 4 {
		session, candidate := upload()
		claim(session.ID)
		candidates = append(candidates, candidate)
	}
	saved, err := visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: 1, CoverCandidates: &candidates})
	if err != nil || len(saved.CoverCandidates) != 4 {
		t.Fatalf("save four count=%d err=%v", len(saved.CoverCandidates), err)
	}
	fifthSession, fifth := upload()
	five := append(append([]streamvisual.CoverCandidate{}, candidates...), fifth)
	beforeCreate, err := streams.ListStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = visuals.CreateStream(ctx, userID, streamvisual.Create{Name: "Reject five", UploadSessionID: fifthSession.ID, Settings: streamvisual.Update{CoverCandidates: &five}}); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
		t.Fatalf("create five error=%v", err)
	}
	afterCreate, err := streams.ListStreams(ctx)
	if err != nil || len(afterCreate) != len(beforeCreate) {
		t.Fatalf("rejected create leaked stream count before=%d after=%d err=%v", len(beforeCreate), len(afterCreate), err)
	}
	if _, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: saved.Revision, UploadSessionID: fifthSession.ID, CoverCandidates: &five}); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
		t.Fatalf("explicit fifth error=%v", err)
	}
	if _, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{
		ExpectedRevision: saved.Revision, UploadSessionID: fifthSession.ID,
		CoverSource:    streamvisual.OptionalString{Set: true, Valid: true, Value: "upload"},
		CoverAssetID:   streamvisual.OptionalString{Set: true, Valid: true, Value: fifth.AssetID},
		CoverVariantID: streamvisual.OptionalString{Set: true, Valid: true, Value: fifth.VariantID},
	}); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
		t.Fatalf("omitted fifth error=%v", err)
	}
	var ownerType string
	if err = db.QueryRowContext(ctx, `SELECT owner_type FROM media_upload_sessions WHERE id=?`, fifthSession.ID).Scan(&ownerType); err != nil || ownerType != "upload_draft" {
		t.Fatalf("rejected update claimed draft owner=%s err=%v", ownerType, err)
	}
	unchanged, err := visuals.Get(ctx, stream.ID)
	if err != nil || unchanged.Revision != saved.Revision || len(unchanged.CoverCandidates) != 4 {
		t.Fatalf("rejected update changed settings revision=%d count=%d err=%v", unchanged.Revision, len(unchanged.CoverCandidates), err)
	}

	// Seed a legitimate pre-limit collection. Production updates must preserve
	// every legacy candidate, and only an addition/replacement must converge.
	claim(fifthSession.ID)
	if _, err = db.ExecContext(ctx, `INSERT INTO stream_video_cover_candidates(stream_id,variant_id,asset_id,display_name,source,sort_order) VALUES(?,?,?,'legacy fifth','upload',4)`, stream.ID, fifth.VariantID, fifth.AssetID); err != nil {
		t.Fatal(err)
	}
	legacy, err := visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: saved.Revision, HeaderTitleMode: streamvisual.OptionalString{Set: true, Valid: true, Value: "custom"}, HeaderTitleValue: streamvisual.OptionalString{Set: true, Valid: true, Value: "Legacy collection kept"}})
	if err != nil || len(legacy.CoverCandidates) != 5 {
		t.Fatalf("legacy other settings count=%d err=%v", len(legacy.CoverCandidates), err)
	}
	legacy, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: legacy.Revision, CoverSource: streamvisual.OptionalString{Set: true, Valid: true, Value: "upload"}, CoverAssetID: streamvisual.OptionalString{Set: true, Valid: true, Value: fifth.AssetID}, CoverVariantID: streamvisual.OptionalString{Set: true, Valid: true, Value: fifth.VariantID}})
	if err != nil || legacy.CoverAssetID != fifth.AssetID || len(legacy.CoverCandidates) != 5 {
		t.Fatalf("legacy selection count=%d err=%v", len(legacy.CoverCandidates), err)
	}
	legacy, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: legacy.Revision, CoverCandidates: &five})
	if err != nil || len(legacy.CoverCandidates) != 5 {
		t.Fatalf("legacy explicit retention count=%d err=%v", len(legacy.CoverCandidates), err)
	}
	sixthSession, sixth := upload()
	replacement := append([]streamvisual.CoverCandidate{}, five...)
	replacement[0] = sixth
	if _, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: legacy.Revision, UploadSessionID: sixthSession.ID, CoverCandidates: &replacement}); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
		t.Fatalf("legacy replacement above four error=%v", err)
	}
	three := append([]streamvisual.CoverCandidate{}, candidates[:3]...)
	reduced, err := visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: legacy.Revision, CoverSource: streamvisual.OptionalString{Set: true, Valid: true, Value: "none"}, CoverCandidates: &three})
	if err != nil || len(reduced.CoverCandidates) != 3 {
		t.Fatalf("legacy reduction count=%d err=%v", len(reduced.CoverCandidates), err)
	}

	// Hold the draft lock so the first real Update owns the stream lock. An
	// observer NOWAIT proves that exact lock phase before the second request.
	seventhSession, seventh := upload()
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	if err = blocker.QueryRowContext(ctx, `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE`, sixthSession.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	fourA := append(append([]streamvisual.CoverCandidate{}, three...), sixth)
	go func() {
		_, err := visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: reduced.Revision, UploadSessionID: sixthSession.ID, CoverCandidates: &fourA})
		results <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	streamLockObserved := false
	for time.Now().Before(deadline) {
		observer, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = observer.QueryRowContext(ctx, `SELECT id FROM streams WHERE id=? FOR UPDATE NOWAIT`, stream.ID).Scan(&locked)
		_ = observer.Rollback()
		if err != nil {
			var lockError *mysql.MySQLError
			if !errors.As(err, &lockError) || lockError.Number != 1205 {
				t.Fatalf("stream NOWAIT observer returned a non-lock error: %v", err)
			}
			streamLockObserved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !streamLockObserved {
		t.Fatal("first Update did not own the stream lock while waiting for its draft")
	}
	fourB := append(append([]streamvisual.CoverCandidate{}, three...), seventh)
	go func() {
		_, err := visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: reduced.Revision, UploadSessionID: seventhSession.ID, CoverCandidates: &fourB})
		results <- err
	}()
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	successes, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-results:
			switch {
			case err == nil:
				successes++
			case errors.Is(err, streamvisual.ErrRevisionConflict):
				conflicts++
			default:
				t.Fatalf("concurrent visual error=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent visual updates did not converge")
		}
	}
	final, err := visuals.Get(ctx, stream.ID)
	if err != nil || successes != 1 || conflicts != 1 || len(final.CoverCandidates) != 4 || final.Revision != reduced.Revision+1 {
		t.Fatalf("concurrent outcome success=%d conflicts=%d count=%d revision=%d err=%v", successes, conflicts, len(final.CoverCandidates), final.Revision, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT owner_type FROM media_upload_sessions WHERE id=?`, seventhSession.ID).Scan(&ownerType); err != nil || ownerType != "upload_draft" {
		t.Fatalf("losing request claimed draft owner=%s err=%v", ownerType, err)
	}
	if err = streams.DeleteStream(ctx, stream.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = visuals.Update(ctx, stream.ID, userID, streamvisual.Update{ExpectedRevision: final.Revision, UploadSessionID: seventhSession.ID, CoverCandidates: &fourB}); !errors.Is(err, streamvisual.ErrNotFound) {
		t.Fatalf("deleted stream update error=%v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT owner_type FROM media_upload_sessions WHERE id=?`, seventhSession.ID).Scan(&ownerType); err != nil || ownerType != "upload_draft" {
		t.Fatalf("deleted stream claimed new draft owner=%s err=%v", ownerType, err)
	}
	var recreated int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_video_cover_candidates WHERE stream_id=?`, stream.ID).Scan(&recreated); err != nil || recreated != 0 {
		t.Fatalf("deleted stream recreated candidates=%d err=%v", recreated, err)
	}
}

func TestMariaDBCoverUploadSessionConcurrentLimitAndClaimRollback(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	userID := controlPlatformTestUUID(t)
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES(?,?,'test-hash','active',?,?)`, userID, "cover-session-"+userID[:8], now, now); err != nil {
		t.Fatal(err)
	}
	stream, err := store.NewMariaDBStreamStore(db).CreateStream(ctx, "Cover quota claim")
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
	session, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = media.Upload(ctx, mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 32+i, 18, false))}); err != nil {
			t.Fatal(err)
		}
	}
	db.SetMaxOpenConns(3)
	defer db.SetMaxOpenConns(0)
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	if err = blocker.QueryRowContext(ctx, `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE`, session.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	processingReached := make([]chan struct{}, 2)
	processingRelease := make(chan struct{})
	processingReleased := false
	defer func() {
		if !processingReleased {
			close(processingRelease)
		}
	}()
	for i := 0; i < 2; i++ {
		body := controlPlatformPNG(t, 50+i, 18, false)
		processingReached[i] = make(chan struct{})
		reader := &phaseBarrierReader{reader: bytes.NewReader(body), reached: processingReached[i], release: processingRelease}
		go func() {
			_, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: reader})
			results <- err
		}()
	}
	for _, reached := range processingReached {
		select {
		case <-reached:
		case err := <-results:
			t.Fatalf("upload failed before image-processing barrier: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("upload did not reach image-processing barrier")
		}
	}
	close(processingRelease)
	processingReleased = true
	deadline := time.Now().Add(5 * time.Second)
	for db.Stats().InUse != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if db.Stats().InUse != 3 {
		t.Fatal("both uploads did not reach the held session lock")
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	successes, limited := 0, 0
	for range 2 {
		select {
		case err := <-results:
			switch {
			case err == nil:
				successes++
			case errors.Is(err, mediaassets.ErrCoverUploadLimit):
				limited++
			default:
				t.Fatalf("concurrent upload error=%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent uploads did not finish")
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=? AND deleted_at IS NULL`, session.ID).Scan(&count); err != nil || count != 4 || successes != 1 || limited != 1 {
		t.Fatalf("session successes=%d limited=%d count=%d err=%v", successes, limited, count, err)
	}
	// Model an existing pre-limit draft and prove the direct transactional
	// claimer refuses it without transferring any metadata ownership.
	if _, err = db.ExecContext(ctx, `INSERT INTO media_assets(id,owner_user_id,owner_type,owner_id,upload_session_id,usage_type,storage_key,sha256,media_type,byte_size,width,height,opaque,processor_revision,created_at,updated_at) SELECT ?,owner_user_id,owner_type,owner_id,upload_session_id,usage_type,storage_key,sha256,media_type,byte_size,width,height,opaque,processor_revision,created_at,updated_at FROM media_assets WHERE upload_session_id=? LIMIT 1`, controlPlatformTestUUID(t), session.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	claimErr := media.ClaimDraftTx(ctx, tx, userID, session.ID, stream.ID, time.Now().UTC())
	_ = tx.Rollback()
	if !errors.Is(claimErr, mediaassets.ErrCoverUploadLimit) {
		t.Fatalf("oversized draft claim error=%v", claimErr)
	}
	var state string
	if err = db.QueryRowContext(ctx, `SELECT owner_type FROM media_upload_sessions WHERE id=?`, session.ID).Scan(&state); err != nil || state != "upload_draft" {
		t.Fatalf("claim rollback state=%s err=%v", state, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=? AND owner_type='stream'`, session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claim rollback changed assets=%d err=%v", count, err)
	}
}
