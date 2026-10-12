package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NormalStopReceiptStore records only acknowledged normal stops. The source
// version distinguishes a stopped execution from a later waiting/live cycle.
type NormalStopReceiptStore interface {
	MarkNormalStopUnconfirmed(context.Context, string) error
	ClaimNormalStop(context.Context, Stream) (Stream, bool, error)
	ClaimForceStop(context.Context, Stream) (Stream, bool, error)
	FailNormalStop(context.Context, string, time.Time) (Stream, bool, error)
	CompleteConfirmedForceStop(context.Context, string, time.Time) (Stream, bool, error)
	CompleteNormalStop(context.Context, string, string, time.Time) (Stream, bool, error)
	RecordNormalStopReceipt(context.Context, string, string, time.Time) error
	HasNormalStopReceipt(context.Context, string, string) (bool, error)
}

func streamMutationTime(previous time.Time) time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(previous) {
		now = previous.Add(time.Microsecond)
	}
	return now
}

// The terminal transition and its receipt share one durability boundary.
func (s MariaDBStreamStore) CompleteNormalStop(ctx context.Context, id, source string, claim time.Time) (Stream, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, false, err
	}
	defer tx.Rollback()
	var status string
	var updated time.Time
	if err = tx.QueryRowContext(ctx, `SELECT status,updated_at FROM streams WHERE id=? FOR UPDATE`, id).Scan(&status, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return Stream{}, false, err
	}
	transitioned := status == "stopping" && updated.Equal(claim)
	if transitioned {
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err = tx.ExecContext(ctx, `UPDATE streams SET status='completed',updated_at=? WHERE id=?`, now, id); err != nil {
			return Stream{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO stream_normal_stop_receipts(stream_id,source_updated_at,completed_updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE completed_updated_at=VALUES(completed_updated_at)`, id, source, now); err != nil {
			return Stream{}, false, err
		}
	}
	if transitioned {
		if _, err = tx.ExecContext(ctx, `DELETE FROM stream_normal_stop_pending WHERE stream_id=?`, id); err != nil {
			return Stream{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET retired_at=? WHERE stream_id=? AND retired_at IS NULL`, time.Now().UTC(), id); err != nil {
			return Stream{}, false, err
		}
	}
	stream, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id = ?"), id))
	if err != nil {
		return Stream{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Stream{}, false, err
	}
	return stream, transitioned, nil
}
func (s *MemoryStreamStore) CompleteNormalStop(ctx context.Context, id, source string, claim time.Time) (Stream, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, false, ErrNotFound
	}
	if stream.Status != "stopping" || !stream.UpdatedAt.Equal(claim) {
		return stream, false, nil
	}
	delete(s.normalStopPending, id)
	stream.Status = "completed"
	stream.UpdatedAt = streamMutationTime(stream.UpdatedAt)
	if s.normalStopReceipts == nil {
		s.normalStopReceipts = map[string]time.Time{}
	}
	s.normalStopReceipts[id+"\x00"+source] = stream.UpdatedAt
	s.streams[id] = stream
	return stream, true, nil
}

func (s MariaDBStreamStore) RecordNormalStopReceipt(ctx context.Context, id, source string, completed time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_normal_stop_receipts(stream_id,source_updated_at,completed_updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE completed_updated_at=VALUES(completed_updated_at)`, id, source, completed)
	return err
}
func (s MariaDBStreamStore) HasNormalStopReceipt(ctx context.Context, id, source string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_normal_stop_receipts WHERE stream_id=? AND source_updated_at=?)`, id, source).Scan(&found)
	return found, err
}
func (s *MemoryStreamStore) RecordNormalStopReceipt(ctx context.Context, id, source string, completed time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.normalStopReceipts == nil {
		s.normalStopReceipts = map[string]time.Time{}
	}
	s.normalStopReceipts[id+"\x00"+source] = completed
	return nil
}
func (s *MemoryStreamStore) HasNormalStopReceipt(ctx context.Context, id, source string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.normalStopReceipts[id+"\x00"+source]
	return ok, nil
}

func (s MariaDBStreamStore) MarkNormalStopUnconfirmed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stream_normal_stop_pending(stream_id) VALUES(?) ON DUPLICATE KEY UPDATE stream_id=VALUES(stream_id)`, id)
	return err
}
func (s *MemoryStreamStore) MarkNormalStopUnconfirmed(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return ErrNotFound
	}
	if s.normalStopPending == nil {
		s.normalStopPending = map[string]bool{}
	}
	s.normalStopPending[id] = true
	return nil
}

// Claim and deletion/assignment fence commit together across Panel processes.
func (s MariaDBStreamStore) ClaimNormalStop(ctx context.Context, expected Stream) (Stream, bool, error) {
	return s.claimStop(ctx, expected, "stopping")
}
func (s MariaDBStreamStore) ClaimForceStop(ctx context.Context, expected Stream) (Stream, bool, error) {
	return s.claimStop(ctx, expected, "failed")
}
func (s MariaDBStreamStore) claimStop(ctx context.Context, expected Stream, status string) (Stream, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, false, err
	}
	defer tx.Rollback()
	current, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id=?")+" FOR UPDATE", expected.ID))
	if err != nil {
		return Stream{}, false, err
	}
	if current.Status != expected.Status || !current.UpdatedAt.Equal(expected.UpdatedAt) {
		return current, false, nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Microsecond)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE streams SET status=?,updated_at=? WHERE id=?`, status, now, current.ID); err != nil {
		return Stream{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO stream_normal_stop_pending(stream_id) VALUES(?) ON DUPLICATE KEY UPDATE stream_id=VALUES(stream_id)`, current.ID); err != nil {
		return Stream{}, false, err
	}
	current.Status = status
	current.UpdatedAt = now
	if err = tx.Commit(); err != nil {
		return Stream{}, false, err
	}
	return current, true, nil
}
func (s *MemoryStreamStore) ClaimNormalStop(ctx context.Context, expected Stream) (Stream, bool, error) {
	return s.claimStop(ctx, expected, "stopping")
}
func (s *MemoryStreamStore) ClaimForceStop(ctx context.Context, expected Stream) (Stream, bool, error) {
	return s.claimStop(ctx, expected, "failed")
}
func (s *MemoryStreamStore) claimStop(ctx context.Context, expected Stream, status string) (Stream, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.streams[expected.ID]
	if !ok {
		return Stream{}, false, ErrNotFound
	}
	if current.Status != expected.Status || !current.UpdatedAt.Equal(expected.UpdatedAt) {
		return current, false, nil
	}
	current.Status = status
	current.UpdatedAt = streamMutationTime(current.UpdatedAt)
	s.streams[current.ID] = current
	if s.normalStopPending == nil {
		s.normalStopPending = map[string]bool{}
	}
	s.normalStopPending[current.ID] = true
	return current, true, nil
}
func (s MariaDBStreamStore) finishClaimedStop(ctx context.Context, id, expected, status string, claim time.Time, confirmed bool) (Stream, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, false, err
	}
	defer tx.Rollback()
	current, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id=?")+" FOR UPDATE", id))
	if err != nil {
		return Stream{}, false, err
	}
	if current.Status != expected || !current.UpdatedAt.Equal(claim) {
		return current, false, nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Microsecond)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE streams SET status=?,updated_at=? WHERE id=?`, status, now, id); err != nil {
		return Stream{}, false, err
	}
	if confirmed {
		if _, err = tx.ExecContext(ctx, `DELETE FROM stream_normal_stop_pending WHERE stream_id=?`, id); err != nil {
			return Stream{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE stream_video_cover_runtime SET retired_at=? WHERE stream_id=? AND retired_at IS NULL`, now, id); err != nil {
			return Stream{}, false, err
		}
	}
	current.Status = status
	current.UpdatedAt = now
	if err = tx.Commit(); err != nil {
		return Stream{}, false, err
	}
	return current, true, nil
}
func (s *MemoryStreamStore) finishClaimedStop(ctx context.Context, id, expected, status string, claim time.Time, confirmed bool) (Stream, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.streams[id]
	if !ok {
		return Stream{}, false, ErrNotFound
	}
	if current.Status != expected || !current.UpdatedAt.Equal(claim) {
		return current, false, nil
	}
	current.Status = status
	current.UpdatedAt = streamMutationTime(current.UpdatedAt)
	s.streams[id] = current
	if confirmed {
		delete(s.normalStopPending, id)
	}
	return current, true, nil
}
func (s MariaDBStreamStore) FailNormalStop(ctx context.Context, id string, claim time.Time) (Stream, bool, error) {
	return s.finishClaimedStop(ctx, id, "stopping", "failed", claim, false)
}
func (s *MemoryStreamStore) FailNormalStop(ctx context.Context, id string, claim time.Time) (Stream, bool, error) {
	return s.finishClaimedStop(ctx, id, "stopping", "failed", claim, false)
}
func (s MariaDBStreamStore) CompleteConfirmedForceStop(ctx context.Context, id string, claim time.Time) (Stream, bool, error) {
	return s.finishClaimedStop(ctx, id, "failed", "completed", claim, true)
}
func (s *MemoryStreamStore) CompleteConfirmedForceStop(ctx context.Context, id string, claim time.Time) (Stream, bool, error) {
	return s.finishClaimedStop(ctx, id, "failed", "completed", claim, true)
}
