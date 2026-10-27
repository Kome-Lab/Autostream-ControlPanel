package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func TestMariaDBStreamCoverDeletionLocksRuntimeBeforeCompletedActionReplay(t *testing.T) {
	f := newCoverDeletionFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 25*time.Second)
	defer cancel()
	stream, other := f.stream(), f.stream()
	asset, variants := f.upload(stream.ID, "video_cover", 83)
	foreign, foreignVariants := f.upload(other.ID, "video_cover", 91)
	f.visual(stream.ID, asset, variants[0])
	f.visual(other.ID, foreign, foreignVariants[0])
	covers := videocover.NewMariaDBRepository(f.db)
	now := time.Now().UTC()
	requests := make([]videocover.ActionRequest, 0, 2)
	for generation := uint64(1); generation <= 2; generation++ {
		request := videocover.ActionRequest{Active: true, ExpectedJobGeneration: generation, ExpectedRevision: 1, IdempotencyKey: fmt.Sprintf("delete-replay-%d", generation)}
		requests = append(requests, request)
		if _, err := f.db.ExecContext(ctx, `INSERT INTO stream_video_cover_runtime
		  (stream_id,job_generation,desired_active,desired_revision,applied_active,applied_revision,asset_variant_id,applied_asset_variant_id,reconciliation_status,last_idempotency_key,created_at,updated_at)
		  VALUES(?,?,TRUE,2,TRUE,2,?,?,'applied',?,?,?)`, stream.ID, generation, variants[0].ID, variants[1].ID, request.IdempotencyKey, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `INSERT INTO stream_video_cover_actions
		  (stream_id,job_generation,idempotency_key,requested_active,requested_revision,request_fingerprint,result_status,requested_asset_variant_id,created_at,updated_at)
		  VALUES(?,?,?,TRUE,2,?,'applied',?,?,?)`, stream.ID, generation, request.IdempotencyKey, videocover.RequestFingerprint(request), variants[0].ID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	stopping, err := f.streams.UpdateStreamStatus(ctx, stream.ID, "stopping")
	if err != nil {
		t.Fatal(err)
	}
	if stopped, changed, err := f.streams.CompleteNormalStop(ctx, stream.ID, stopping.UpdatedAt.Format(time.RFC3339Nano), stopping.UpdatedAt); err != nil || !changed || stopped.Status != "completed" {
		t.Fatalf("normal stop did not retain a deletable completed execution: changed=%v err=%v", changed, err)
	}
	latest := requests[1]
	actionBlocker, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer actionBlocker.Rollback()
	var locked string
	if err := actionBlocker.QueryRowContext(ctx, `SELECT idempotency_key FROM stream_video_cover_actions WHERE stream_id=? AND job_generation=? AND idempotency_key=? FOR UPDATE`, stream.ID, latest.ExpectedJobGeneration, latest.IdempotencyKey).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- f.streams.DeleteStream(ctx, stream.ID) }()
	// The external action lock cannot release early. Independently observe
	// both runtime row locks before starting the replay. The old action-first
	// deletion never acquired either runtime and fails this phase, regardless
	// of goroutine scheduling or InnoDB's later deadlock victim selection.
	for _, request := range requests {
		waitForCoverDeletionRuntimeLock(t, f, ctx, stream.ID, request.ExpectedJobGeneration)
	}
	waitForCoverDeletionSQL(t, f, ctx, "UPDATE stream_video_cover_actions SET requested_asset_variant_id=NULL WHERE stream_id=")
	type replayResult struct {
		prepared videocover.PreparedAction
		found    bool
		err      error
	}
	replayed := make(chan replayResult, 1)
	go func() {
		if _, err := covers.GetCurrentState(ctx, stream.ID); err != nil {
			replayed <- replayResult{err: err}
			return
		}
		prepared, found, err := covers.LookupActionReplay(ctx, stream.ID, latest)
		replayed <- replayResult{prepared: prepared, found: found, err: err}
	}()
	// PROCESSLIST exposes this test user's own executing statements without a
	// global PROCESS grant. Observe the real runtime FOR UPDATE on the server,
	// while deletion owns that row and waits on the still-held action blocker.
	waitForCoverDeletionSQL(t, f, ctx, "SELECT stream_id,job_generation,desired_active,", "FOR UPDATE")
	if err := actionBlocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("delete failed while a completed action was replayed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("delete did not converge after the action blocker released")
	}
	select {
	case result := <-replayed:
		if result.err != nil || !result.found || !result.prepared.Replay || result.prepared.Dispatch || result.prepared.Outcome != "applied" || result.prepared.RequestedRevision != 2 || result.prepared.RequestedAssetVariantID != "" {
			t.Fatalf("historical replay lost its immutable result or retained a file reference: found=%v outcome=%q err=%v", result.found, result.prepared.Outcome, result.err)
		}
	case <-ctx.Done():
		t.Fatal("historical replay did not converge after deletion committed")
	}
	for _, request := range requests {
		outcome, found, err := covers.LookupActionOutcome(ctx, stream.ID, request)
		if err != nil || !found || outcome.Outcome != "applied" || outcome.RequestedRevision != 2 || outcome.RequestedAssetVariantID != "" {
			t.Fatalf("generation %d audit outcome changed: found=%v err=%v", request.ExpectedJobGeneration, found, err)
		}
		var fingerprint string
		if err := f.db.QueryRowContext(ctx, `SELECT request_fingerprint FROM stream_video_cover_actions WHERE stream_id=? AND job_generation=? AND idempotency_key=?`, stream.ID, request.ExpectedJobGeneration, request.IdempotencyKey).Scan(&fingerprint); err != nil || fingerprint != videocover.RequestFingerprint(request) {
			t.Fatalf("generation %d audit fingerprint changed: err=%v", request.ExpectedJobGeneration, err)
		}
	}
	var retired int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_video_cover_runtime WHERE stream_id=? AND retired_at IS NOT NULL AND asset_variant_id IS NULL AND applied_asset_variant_id IS NULL`, stream.ID).Scan(&retired); err != nil || retired != 2 {
		t.Fatalf("all completed generations must release file references: rows=%d err=%v", retired, err)
	}
	var tombstones int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM streams WHERE id=? AND deleted_at IS NOT NULL`, stream.ID).Scan(&tombstones); err != nil || tombstones != 1 {
		t.Fatalf("normal deletion did not commit: rows=%d err=%v", tombstones, err)
	}
	if _, err := f.media.GetAsset(ctx, f.userID, asset.ID); !errors.Is(err, mediaassets.ErrNotFound) {
		t.Fatalf("deleted stream upload remained visible: %v", err)
	}
	f.file(asset.StorageKey, true)
	if _, err := f.media.GarbageCollect(ctx, time.Now().UTC(), 100); err != nil {
		t.Fatal(err)
	}
	f.file(asset.StorageKey, false)
	for _, variant := range variants {
		f.file(variant.StorageKey, false)
	}
	f.file(foreign.StorageKey, true)
	for _, variant := range foreignVariants {
		f.file(variant.StorageKey, true)
	}
}

func waitForCoverDeletionRuntimeLock(t *testing.T, f *coverDeletionFixture, ctx context.Context, streamID string, generation uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		observer, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var locked uint64
		err = observer.QueryRowContext(ctx, `SELECT job_generation FROM stream_video_cover_runtime WHERE stream_id=? AND job_generation=? FOR UPDATE NOWAIT`, streamID, generation).Scan(&locked)
		_ = observer.Rollback()
		if err != nil {
			var lockErr *mysql.MySQLError
			if errors.As(err, &lockErr) && (lockErr.Number == 1205 || lockErr.Number == 3572) {
				return
			}
			t.Fatalf("runtime generation %d lock observer failed: %v", generation, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("deletion did not acquire runtime generation %d before waiting on an action", generation)
}

func waitForCoverDeletionSQL(t *testing.T, f *coverDeletionFixture, ctx context.Context, prefix string, required ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	pattern := prefix + "%"
	for _, fragment := range required {
		pattern += fragment + "%"
	}
	for time.Now().Before(deadline) {
		var executing int
		if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID<>CONNECTION_ID() AND DB=DATABASE() AND INFO LIKE ?`, pattern).Scan(&executing); err != nil {
			t.Fatalf("cannot observe the real blocked SQL statement: %v", err)
		}
		if executing == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("real blocked SQL statement did not reach the server: %s", prefix)
}
