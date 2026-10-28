package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A stream without a settings row has one stable revision, independent of
// lifecycle/heartbeat changes to streams.updated_at.
const UnconfiguredStreamSettingsRevision = "unconfigured"

var ErrInvalidStreamSettingsRevision = errors.New("invalid stream settings revision")

func StreamSettingsRevisionFromTime(value time.Time) string {
	if value.IsZero() {
		return UnconfiguredStreamSettingsRevision
	}
	return value.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

func ValidStreamSettingsRevision(value string) bool {
	if value == UnconfiguredStreamSettingsRevision {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !parsed.IsZero() && StreamSettingsRevisionFromTime(parsed) == value
}

func MatchStreamSettingsRevision(expected *string, current string) error {
	if expected == nil {
		return nil // Older callers retain the optional-field contract.
	}
	if !ValidStreamSettingsRevision(*expected) {
		return ErrInvalidStreamSettingsRevision
	}
	if current == "" {
		current = UnconfiguredStreamSettingsRevision
	}
	if *expected != current {
		return ErrRevisionConflict
	}
	return nil
}

func nextStreamSettingsTime(previous, now time.Time) time.Time {
	previous = previous.UTC().Truncate(time.Microsecond)
	now = now.UTC().Truncate(time.Microsecond)
	if !previous.IsZero() && !now.After(previous) {
		return previous.Add(time.Microsecond)
	}
	return now
}

// The caller holds the stream row first, so both existing and missing settings
// rows have a consistent stream -> settings lock order.
func streamSettingsTimeTx(ctx context.Context, tx *sql.Tx, streamID string) (time.Time, error) {
	var value sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT updated_at FROM stream_settings WHERE stream_id = ? FOR UPDATE`, streamID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if !value.Valid {
		return time.Time{}, nil
	}
	return value.Time.UTC().Truncate(time.Microsecond), nil
}

// Keeps the original runtime-settings API available to older integrations while
// allowing the current Panel to require an atomic expected settings revision.
type StreamRuntimeSettingsRevisionStore interface {
	UpdateStreamEncoderRuntimeSettingsRevision(context.Context, string, float64, string, *string) (Stream, error)
}
