package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMemoryStreamArtifactRereportPreservesIdentityAndShare(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "artifact identity")
	if err != nil {
		t.Fatal(err)
	}
	artifact := StreamArtifact{
		Kind:         "archive",
		Name:         "final.mp4",
		RelativePath: "final/" + stream.ID + "/final.mp4",
		SizeBytes:    123,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	first, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("first artifact report: artifacts=%#v err=%v", first, err)
	}
	share, err := streams.CreateStreamArtifactShare(t.Context(), StreamArtifactShare{
		StreamID:      stream.ID,
		ArtifactID:    first[0].ID,
		TokenHash:     strings.Repeat("a", 64),
		AllowDownload: true,
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	artifact.SizeBytes = 456
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	second, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(second) != 1 {
		t.Fatalf("second artifact report: artifacts=%#v err=%v", second, err)
	}
	if second[0].ID != first[0].ID || !second[0].CreatedAt.Equal(first[0].CreatedAt) || second[0].SizeBytes != 456 {
		t.Fatalf("artifact identity changed after re-report: before=%#v after=%#v", first[0], second[0])
	}
	shares, err := streams.ListStreamArtifactShares(t.Context(), stream.ID, second[0].ID)
	if err != nil || len(shares) != 1 || shares[0].ID != share.ID {
		t.Fatalf("artifact share was not preserved: shares=%#v err=%v", shares, err)
	}
	resolved, err := streams.GetStreamArtifactShareByTokenHash(t.Context(), share.TokenHash)
	if err != nil || resolved.ArtifactID != second[0].ID {
		t.Fatalf("artifact share no longer resolves: share=%#v err=%v", resolved, err)
	}
}

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("bad uuid: %s", id)
	}
}

func TestMemoryStreamMediaRuntimePersistsNegotiatedBurnIn(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Worker video")
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.SetStreamVideoOverlayBurnIn(t.Context(), stream.ID, true); err != nil {
		t.Fatal(err)
	}
	runtime, err := streams.GetStreamMediaRuntime(t.Context(), stream.ID)
	if err != nil || !runtime.VideoOverlayBurnIn || runtime.StreamID != stream.ID || runtime.UpdatedAt.IsZero() {
		t.Fatalf("unexpected media runtime: runtime=%#v err=%v", runtime, err)
	}
	if err := streams.SetStreamVideoOverlayBurnIn(t.Context(), stream.ID, false); err != nil {
		t.Fatal(err)
	}
	runtime, err = streams.GetStreamMediaRuntime(t.Context(), stream.ID)
	if err != nil || runtime.VideoOverlayBurnIn {
		t.Fatalf("legacy media runtime was not persisted: runtime=%#v err=%v", runtime, err)
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
