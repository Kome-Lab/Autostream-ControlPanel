package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestUI188CB16SnapshotRetryPolicy(t *testing.T) {
	unknown := errors.New("synthetic unknown read error")
	for _, tc := range []struct {
		name             string
		errors           []error
		wantCalls        int
		wantErr          error
		cancelAfterFirst bool
	}{
		{"stable", []error{nil}, 1, nil, false},
		{"one typed conflict", []error{ErrServiceAssignmentConflict, nil}, 2, nil, false},
		{"wrapped typed conflict", []error{fmt.Errorf("typed: %w", ErrServiceAssignmentConflict), nil}, 2, nil, false},
		{"persistent conflict", []error{ErrServiceAssignmentConflict, ErrServiceAssignmentConflict}, 2, ErrServiceAssignmentConflict, false},
		{"forbidden", []error{ErrForbidden}, 1, ErrForbidden, false},
		{"unknown read", []error{unknown}, 1, unknown, false},
		{"unavailable store", []error{ErrCurrentDiscordBindingUnavailable}, 1, ErrCurrentDiscordBindingUnavailable, false},
		{"canceled error", []error{context.Canceled}, 1, context.Canceled, false},
		{"deadline error", []error{context.DeadlineExceeded}, 1, context.DeadlineExceeded, false},
		{"joined cancellation", []error{errors.Join(ErrServiceAssignmentConflict, context.Canceled)}, 1, context.Canceled, false},
		{"joined deadline", []error{errors.Join(ErrServiceAssignmentConflict, context.DeadlineExceeded)}, 1, context.DeadlineExceeded, false},
		{"context canceled between attempts", []error{ErrServiceAssignmentConflict}, 1, context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			calls := 0
			got, err := readFreshCurrentDiscordSnapshot(ctx, func(actual context.Context) (CurrentDiscordCredentialSnapshot, error) {
				if actual != ctx {
					t.Fatal("request context replaced")
				}
				if calls >= len(tc.errors) {
					t.Fatal("unbounded read")
				}
				e := tc.errors[calls]
				calls++
				if tc.cancelAfterFirst {
					cancel()
				}
				// Failed reads deliberately carry a stale tuple; it must be discarded.
				id := "stale-A"
				if e == nil {
					id = "fresh-B"
				}
				return CurrentDiscordCredentialSnapshot{Service: RegisteredService{ServiceID: id}}, e
			})
			if calls != tc.wantCalls || tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("policy calls=%d err_type=%T", calls, err)
			}
			if err == nil && got.Service.ServiceID != "fresh-B" || err != nil && got.Service.ServiceID != "" {
				t.Fatal("failed read tuple leaked into result")
			}
		})
	}
	t.Run("pre-canceled request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0
		_, err := readFreshCurrentDiscordSnapshot(ctx, func(context.Context) (CurrentDiscordCredentialSnapshot, error) {
			calls++
			return CurrentDiscordCredentialSnapshot{}, nil
		})
		if calls != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled read executed")
		}
	})
}

func TestUI188CB16PersistentMemoryConflictFailsClosed(t *testing.T) {
	f := cb15StoreFixture(t, nil)
	streams := f.stores.Streams.(*MemoryStreamStore)
	streams.mu.Lock()
	delete(streams.streams, f.current.ID)
	streams.mu.Unlock()
	snapshot, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
	if !errors.Is(err, ErrServiceAssignmentConflict) || snapshot.Service.ServiceID != "" || snapshot.Bindings != nil {
		t.Fatal("persistent corrupt authority returned a usable snapshot")
	}
}
