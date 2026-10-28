package store

import (
	"errors"
	"time"
)

var ErrInvalidResourceRevision = errors.New("invalid resource updated_at revision")

func resourceUpdatedAt(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

func validateExpectedResourceUpdatedAt(expected *string) error {
	if expected == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *expected)
	if err != nil || parsed.IsZero() || resourceUpdatedAt(parsed) != *expected {
		return ErrInvalidResourceRevision
	}
	return nil
}

func matchExpectedResourceUpdatedAt(expected *string, current time.Time) error {
	if err := validateExpectedResourceUpdatedAt(expected); err != nil {
		return err
	}
	if expected != nil && *expected != resourceUpdatedAt(current) {
		return ErrRevisionConflict
	}
	return nil
}

func nextResourceUpdatedAt(previous, now time.Time) time.Time {
	previous = previous.UTC().Truncate(time.Microsecond)
	now = now.UTC().Truncate(time.Microsecond)
	if !previous.IsZero() && !now.After(previous) {
		return previous.Add(time.Microsecond)
	}
	return now
}
