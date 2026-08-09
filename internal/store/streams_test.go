package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("bad uuid: %s", id)
	}
}

func TestYouTubeRuntimeSaveErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "stream not found", err: ErrNotFound, want: "stream_not_found"},
		{name: "driver bad connection", err: driver.ErrBadConn, want: "database_connection_transient"},
		{name: "mysql connection lost", err: &mysql.MySQLError{Number: 2013, Message: "Lost connection"}, want: "database_connection_transient"},
		{name: "connection reset", err: errors.New("write tcp: connection reset by peer"), want: "database_connection_transient"},
		{name: "deadline", err: context.DeadlineExceeded, want: "database_write_failed"},
		{name: "write failure", err: errors.New("duplicate key"), want: "database_write_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := YouTubeRuntimeSaveErrorCode(test.err); got != test.want {
				t.Fatalf("YouTubeRuntimeSaveErrorCode(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}

func TestStreamYouTubeRuntimeCompleteLastErrorUsesEmptyStringWhenUnset(t *testing.T) {
	if got := streamYouTubeRuntimeCompleteLastError("   "); got != "" {
		t.Fatalf("streamYouTubeRuntimeCompleteLastError(unset) = %q, want empty string", got)
	}
	if got := streamYouTubeRuntimeCompleteLastError(" youtube transition failed "); got != "youtube transition failed" {
		t.Fatalf("streamYouTubeRuntimeCompleteLastError(value) = %q", got)
	}
}

func TestMemoryStreamStoreUpdateSettingsRenamesWhenNameIsProvided(t *testing.T) {
	streams := NewMemoryStreamStore()
	created, err := streams.CreateStream(t.Context(), "before")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	updated, err := streams.UpdateStreamSettings(t.Context(), created.ID, StreamSettings{Name: " after "})
	if err != nil {
		t.Fatalf("update stream settings: %v", err)
	}
	if updated.Name != "after" {
		t.Fatalf("updated name = %q, want after", updated.Name)
	}
	unchanged, err := streams.UpdateStreamSettings(t.Context(), created.ID, StreamSettings{})
	if err != nil {
		t.Fatalf("update stream settings without name: %v", err)
	}
	if unchanged.Name != "after" {
		t.Fatalf("name after empty update = %q, want after", unchanged.Name)
	}
}

func TestMemoryStreamStoreTransitionStatusRequiresExpectedCurrentStatus(t *testing.T) {
	streams := NewMemoryStreamStore()
	created, err := streams.CreateStream(t.Context(), "lifecycle")
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), created.ID, "completed"); err != nil {
		t.Fatalf("complete stream: %v", err)
	}
	notTransitioned, transitioned, err := streams.TransitionStreamStatus(t.Context(), created.ID, "starting", "live")
	if err != nil {
		t.Fatalf("conditional live transition: %v", err)
	}
	if transitioned {
		t.Fatal("transition from stale starting status succeeded")
	}
	if notTransitioned.Status != "completed" {
		t.Fatalf("stale transition returned status %q, want completed", notTransitioned.Status)
	}
	stopping, transitioned, err := streams.TransitionStreamStatus(t.Context(), created.ID, "completed", "stopping")
	if err != nil {
		t.Fatalf("conditional stopping transition: %v", err)
	}
	if !transitioned || stopping.Status != "stopping" {
		t.Fatalf("expected completed -> stopping transition, got transitioned=%t stream=%#v", transitioned, stopping)
	}
}
