package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

type coverDeletionFixture struct {
	t       *testing.T
	db      *sql.DB
	ctx     context.Context
	media   *mediaassets.SQLRepository
	streams store.MariaDBStreamStore
	root    string
	userID  string
}

func newCoverDeletionFixture(t *testing.T) *coverDeletionFixture {
	t.Helper()
	db, ctx := openMariaDBOAuthMigrationTest(t)
	f := &coverDeletionFixture{t: t, db: db, ctx: ctx, streams: store.NewMariaDBStreamStore(db), root: t.TempDir(), userID: controlPlatformTestUUID(t)}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES(?,?,'test-hash','active',?,?)`, f.userID, "cover-delete-"+f.userID[:8], now, now); err != nil {
		t.Fatal(err)
	}
	var err error
	f.media, err = mediaassets.NewMariaDBRepository(db, f.root)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *coverDeletionFixture) stream() store.Stream {
	f.t.Helper()
	stream, err := f.streams.CreateStream(f.ctx, "cover deletion "+f.userID)
	if err != nil {
		f.t.Fatal(err)
	}
	return stream
}

func (f *coverDeletionFixture) upload(streamID, usage string, width int) (mediaassets.Asset, []mediaassets.Variant) {
	f.t.Helper()
	now := time.Now().UTC()
	session, err := f.media.CreateUploadSession(f.ctx, f.userID, now.Add(time.Hour))
	if err != nil {
		f.t.Fatal(err)
	}
	asset, err := f.media.Upload(f.ctx, mediaassets.UploadInput{UserID: f.userID, SessionID: session.ID, UsageType: usage, Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(f.t, width, 6, false))})
	if err != nil {
		f.t.Fatal(err)
	}
	var variants []mediaassets.Variant
	for _, size := range []int{3, 4} {
		variant, err := f.media.EnsureVariant(f.ctx, f.userID, asset.ID, size, 2, true)
		if err != nil {
			f.t.Fatal(err)
		}
		variants = append(variants, variant)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.media.ClaimDraftTx(f.ctx, tx, f.userID, session.ID, streamID, now); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return asset, variants
}

func (f *coverDeletionFixture) visual(streamID string, asset mediaassets.Asset, variant mediaassets.Variant) {
	f.t.Helper()
	now := time.Now().UTC()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO stream_visual_settings(stream_id,cover_source,cover_asset_id,cover_variant_id,created_at,updated_at) VALUES(?,'upload',?,?,?,?)`, streamID, asset.ID, variant.ID, now, now); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO stream_video_cover_candidates(stream_id,variant_id,asset_id,display_name,source,sort_order) VALUES(?,?,?,'selected','upload',0)`, streamID, variant.ID, asset.ID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *coverDeletionFixture) file(key string, exists bool) {
	f.t.Helper()
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(key)))
	if exists && err != nil || !exists && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("physical file exists=%v expected=%v err=%v", err == nil, exists, err)
	}
}

func TestMariaDBStreamCoverDeletionRetainsSharedFilesAndArchive(t *testing.T) {
	f := newCoverDeletionFixture(t)
	stream, other := f.stream(), f.stream()
	selected, selectedVariants := f.upload(stream.ID, "video_cover", 9)
	excluded, excludedVariants := f.upload(stream.ID, "video_cover", 17)
	background, backgroundVariants := f.upload(stream.ID, "scene_background", 25)
	sharedOwner, sharedVariants := f.upload(stream.ID, "video_cover", 33)
	sharedOther, sharedOtherVariants := f.upload(other.ID, "video_cover", 33)
	foreign, foreignVariants := f.upload(other.ID, "video_cover", 41)
	f.visual(stream.ID, selected, selectedVariants[0])
	f.visual(other.ID, foreign, foreignVariants[0])
	now := time.Now().UTC()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO stream_video_cover_runtime(stream_id,job_generation,asset_variant_id,applied_asset_variant_id,reconciliation_status,created_at,updated_at) VALUES(?,1,?,?,'applied',?,?)`, stream.ID, selectedVariants[0].ID, selectedVariants[1].ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO stream_video_cover_actions(stream_id,job_generation,idempotency_key,requested_active,requested_revision,request_fingerprint,result_status,requested_asset_variant_id,created_at,updated_at) VALUES(?,1,'retained-audit',TRUE,1,?,'applied',?,?,?)`, stream.ID, strings.Repeat("a", 64), selectedVariants[0].ID, now, now); err != nil {
		t.Fatal(err)
	}
	// A system preset is shared authority even if stale ownership metadata
	// incorrectly still points at this stream. It must never be soft deleted.
	presetAsset, presetVariants := f.upload(stream.ID, "video_cover", 49)
	presetID := controlPlatformTestUUID(t)
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO video_cover_presets(id,name,asset_id,asset_variant_id,system_preset,revision,created_at,updated_at) VALUES(?,?,?,?,TRUE,1,?,?)`, presetID, "shared-"+presetID, presetAsset.ID, presetVariants[0].ID, now, now); err != nil {
		t.Fatal(err)
	}
	archiveID := controlPlatformTestUUID(t)
	archivePath := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(archivePath, []byte("recording fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO stream_artifacts(id,stream_id,kind,name,relative_path,size_bytes,created_at) VALUES(?,?,'archive','recording.mp4',?,17,?)`, archiveID, stream.ID, archivePath, now); err != nil {
		t.Fatal(err)
	}
	if err := f.streams.DeleteStream(f.ctx, stream.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.media.GetAsset(f.ctx, f.userID, excluded.ID); !errors.Is(err, mediaassets.ErrNotFound) {
		t.Fatalf("excluded owned upload remained user-visible: %v", err)
	}
	if removed, err := f.media.GarbageCollect(f.ctx, time.Now().UTC(), 100); err != nil || removed != 3 {
		t.Fatalf("collection removed=%d err=%v", removed, err)
	}
	for _, entry := range []struct {
		asset    mediaassets.Asset
		variants []mediaassets.Variant
		exists   bool
	}{{selected, selectedVariants, false}, {excluded, excludedVariants, false}, {background, backgroundVariants, true}, {sharedOwner, sharedVariants, true}, {sharedOther, sharedOtherVariants, true}, {foreign, foreignVariants, true}, {presetAsset, presetVariants, true}} {
		f.file(entry.asset.StorageKey, entry.exists)
		for _, variant := range entry.variants {
			f.file(variant.StorageKey, entry.exists)
		}
	}
	var actionCount, candidateCount, artifactCount int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM stream_video_cover_actions WHERE stream_id=? AND requested_asset_variant_id IS NULL AND request_fingerprint=?`, stream.ID, strings.Repeat("a", 64)).Scan(&actionCount); err != nil || actionCount != 1 {
		t.Fatalf("safe action audit retention count=%d err=%v", actionCount, err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM stream_video_cover_candidates WHERE stream_id=?`, stream.ID).Scan(&candidateCount); err != nil || candidateCount != 0 {
		t.Fatalf("old candidates pin deleted stream count=%d err=%v", candidateCount, err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM stream_artifacts WHERE id=?`, archiveID).Scan(&artifactCount); err != nil || artifactCount != 1 {
		t.Fatalf("recording metadata changed count=%d err=%v", artifactCount, err)
	}
	var cleanupAudit int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM stream_logs WHERE stream_id=? AND message='stream cover asset deletion scheduled' AND JSON_UNQUOTE(JSON_EXTRACT(fields,'$.asset_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(fields,'$.sha256'))=?`, stream.ID, excluded.ID, excluded.SHA256).Scan(&cleanupAudit); err != nil || cleanupAudit != 1 {
		t.Fatalf("deleted asset identity audit missing count=%d err=%v", cleanupAudit, err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("recording file changed: %v", err)
	}
	if err := f.streams.DeleteStream(f.ctx, stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second deletion must remain not found: %v", err)
	}
	if removed, err := f.media.GarbageCollect(f.ctx, time.Now().UTC(), 100); err != nil || removed != 0 {
		t.Fatalf("double collection removed=%d err=%v", removed, err)
	}
}

func TestMariaDBStreamCoverDeletionRollbackAndGuard(t *testing.T) {
	f := newCoverDeletionFixture(t)
	stream := f.stream()
	asset, variants := f.upload(stream.ID, "video_cover", 57)
	f.visual(stream.ID, asset, variants[0])
	if _, err := f.streams.UpdateStreamStatus(f.ctx, stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	if err := f.streams.DeleteStream(f.ctx, stream.ID); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("active deletion bypassed guard: %v", err)
	}
	if _, err := f.streams.UpdateStreamStatus(f.ctx, stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	trigger := "cover_delete_" + strings.ReplaceAll(asset.ID, "-", "")
	if _, err := f.db.ExecContext(f.ctx, `CREATE TRIGGER `+trigger+` BEFORE UPDATE ON media_assets FOR EACH ROW BEGIN IF OLD.id='`+asset.ID+`' AND NEW.deleted_at IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='cover deletion rollback fixture'; END IF; END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.ExecContext(context.WithoutCancel(f.ctx), `DROP TRIGGER IF EXISTS `+trigger) })
	if err := f.streams.DeleteStream(f.ctx, stream.ID); err == nil {
		t.Fatal("injected asset retirement failure did not abort stream deletion")
	}
	if _, err := f.media.GetAsset(f.ctx, f.userID, asset.ID); err != nil {
		t.Fatalf("rollback lost usable asset: %v", err)
	}
	var streams, candidates int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM streams WHERE id=? AND deleted_at IS NULL`, stream.ID).Scan(&streams); err != nil || streams != 1 {
		t.Fatalf("stream tombstone escaped rollback count=%d err=%v", streams, err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM stream_video_cover_candidates WHERE stream_id=?`, stream.ID).Scan(&candidates); err != nil || candidates != 1 {
		t.Fatalf("candidate references escaped rollback count=%d err=%v", candidates, err)
	}
	f.file(asset.StorageKey, true)
	for _, variant := range variants {
		f.file(variant.StorageKey, true)
	}
}

func TestMariaDBStreamCoverPhysicalFailureRetriesAfterRepositoryRestart(t *testing.T) {
	f := newCoverDeletionFixture(t)
	stream := f.stream()
	asset, variants := f.upload(stream.ID, "video_cover", 65)
	f.visual(stream.ID, asset, variants[0])
	// Remove successful publication locks before arranging the retry failure.
	if _, err := f.media.GarbageCollect(f.ctx, time.Now().UTC(), 100); err != nil {
		t.Fatal(err)
	}
	if err := f.streams.DeleteStream(f.ctx, stream.ID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, filepath.FromSlash(asset.StorageKey))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(path, "failure-fixture")
	if err := os.WriteFile(blocker, []byte("retry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := f.media.GarbageCollect(f.ctx, time.Now().UTC(), 100); err == nil || removed != 1 {
		t.Fatalf("physical failure hidden: metadata removed=%d err=%v", removed, err)
	}
	var pending int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM media_asset_file_deletions WHERE storage_key=? AND attempts>0 AND last_error_code='media_asset_file_remove_failed'`, asset.StorageKey).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("durable retry missing count=%d err=%v", pending, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	restarted, err := mediaassets.NewMariaDBRepository(f.db, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := restarted.GarbageCollect(f.ctx, time.Now().UTC(), 100); err != nil || removed != 0 {
		t.Fatalf("restart retry failed metadata removed=%d err=%v", removed, err)
	}
	f.file(asset.StorageKey, false)
	for _, variant := range variants {
		f.file(variant.StorageKey, false)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM media_asset_file_deletions WHERE storage_key=?`, asset.StorageKey).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("retry failed to converge count=%d err=%v", pending, err)
	}
	// Replaying a removal after the file was already removed is idempotent.
	now := time.Now().UTC()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO media_asset_file_deletions(storage_key,created_at,updated_at) VALUES(?,?,?)`, asset.StorageKey, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.GarbageCollect(f.ctx, now, 100); err != nil {
		t.Fatalf("already removed retry: %v", err)
	}
}
