package store

import (
	"strings"
	"time"
)

// A failed start may be reclaimed only with distinguishable stored authority.
// Reject clock rollback rather than writing an earlier business timestamp. At
// equal update time, archive authority must advance at the store's precision;
// otherwise an old immutable claim could finalize or clean up the successor.
// This check runs under the existing stream/assignment locks, before writes.
func streamStartClockConflict(previous Stream, now time.Time, next StreamArchiveAuthority) bool {
	if now.Before(previous.UpdatedAt) {
		return true
	}
	if now.After(previous.UpdatedAt) || !strings.EqualFold(strings.TrimSpace(previous.Status), "failed") {
		return false
	}
	return next.StartedAt == nil || (previous.ArchiveStartedAt != nil && !next.StartedAt.After(*previous.ArchiveStartedAt))
}
