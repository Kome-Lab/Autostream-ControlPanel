package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NormalStopObservation is a consistent product read, not an execution receipt
// for some other stream/version and not proof of a Qualified Clock.
type NormalStopObservation struct {
	Stream           Stream
	SourceVersion    string
	ReceiptFound     bool
	CompletedVersion time.Time
}

type NormalStopOutcomeStore interface {
	ObserveNormalStop(context.Context, string, string) (NormalStopObservation, error)
}

func (s *MemoryStreamStore) ObserveNormalStop(ctx context.Context, id, source string) (NormalStopObservation, error) {
	if err := ctx.Err(); err != nil {
		return NormalStopObservation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return NormalStopObservation{}, ErrNotFound
	}
	completed, found := s.normalStopReceipts[id+"\x00"+source]
	return NormalStopObservation{Stream: stream, SourceVersion: source, ReceiptFound: found, CompletedVersion: completed}, nil
}

func (s MariaDBStreamStore) ObserveNormalStop(ctx context.Context, id, source string) (NormalStopObservation, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return NormalStopObservation{}, err
	}
	defer tx.Rollback()
	stream, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id=?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return NormalStopObservation{}, ErrNotFound
	}
	if err != nil {
		return NormalStopObservation{}, err
	}
	var completed time.Time
	err = tx.QueryRowContext(ctx, `SELECT completed_updated_at FROM stream_normal_stop_receipts WHERE stream_id=? AND source_updated_at=?`, id, source).Scan(&completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return NormalStopObservation{}, err
	}
	found := err == nil
	if err = tx.Commit(); err != nil {
		return NormalStopObservation{}, err
	}
	return NormalStopObservation{Stream: stream, SourceVersion: source, ReceiptFound: found, CompletedVersion: completed}, nil
}
