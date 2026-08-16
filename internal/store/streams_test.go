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

func TestMemoryStreamEncoderRuntimeSettingsPreserveOtherSettings(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "runtime settings")
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{EncoderProfileID: "encoder-01", CaptionProfileID: "caption-01", OverlayProfileID: "overlay-old"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := streams.UpdateStreamEncoderRuntimeSettings(t.Context(), stream.ID, 7.5, "overlay-new")
	if err != nil {
		t.Fatal(err)
	}
	if updated.EncoderAudioGainDB != 7.5 || updated.OverlayProfileID != "overlay-new" {
		t.Fatalf("runtime settings not persisted: %#v", updated)
	}
	if updated.EncoderProfileID != "encoder-01" || updated.CaptionProfileID != "caption-01" {
		t.Fatalf("unrelated settings changed: before=%#v after=%#v", stream, updated)
	}
}

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

func TestMemoryDeleteStreamRetainsArchiveAndHidesOperationalStream(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-protected")
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{{
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/final.mp4", SizeBytes: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatalf("DeleteStream error = %v", err)
	}
	deleted, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatalf("retained stream lookup error: %v", err)
	}
	if deleted.DeletedAt == nil || deleted.Status != "completed" {
		t.Fatalf("retained stream lifecycle = status=%q deleted_at=%v", deleted.Status, deleted.DeletedAt)
	}
	operational, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(operational) != 0 {
		t.Fatalf("deleted stream remains in operational list: %#v", operational)
	}
	archives, err := streams.ListArchiveStreams(t.Context())
	if err != nil || len(archives) != 1 || archives[0].ID != stream.ID {
		t.Fatalf("archive stream lookup = %#v err=%v", archives, err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("archive catalog was not retained: artifacts=%#v err=%v", artifacts, err)
	}
}

func TestMemoryArchiveStreamsHideDeletedStreamAfterLastRecordingArtifactIsDeleted(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-with-sidecars")
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{
		{Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/final.mp4", SizeBytes: 10},
		{Kind: "metadata", Name: "metadata.json", RelativePath: "final/" + stream.ID + "/metadata.json", SizeBytes: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}

	archives, err := streams.ListArchiveStreams(t.Context())
	if err != nil || len(archives) != 1 || archives[0].ID != stream.ID {
		t.Fatalf("archive stream before recording deletion = %#v err=%v", archives, err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if artifact.Kind == "archive" {
			if err := streams.DeleteStreamArtifact(t.Context(), stream.ID, artifact.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	archives, err = streams.ListArchiveStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 0 {
		t.Fatalf("deleted stream without recording remains in archive selector: %#v", archives)
	}
	artifacts, err = streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Kind != "metadata" {
		t.Fatalf("non-recording sidecar retention = %#v err=%v", artifacts, err)
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
