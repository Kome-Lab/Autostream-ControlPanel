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

func TestMemoryStreamArtifactsPreserveRunsAndFenceProcessingCompletion(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive history")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{ArchiveProfileID: "archive-01"}); err != nil {
		t.Fatal(err)
	}
	runOneStarted := time.Date(2026, 8, 18, 5, 6, 29, 1, time.UTC)
	runTwoStarted := runOneStarted.Add(time.Hour)
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-01", runOneStarted); err != nil {
		t.Fatal(err)
	}
	runOne := StreamArtifact{
		ArchiveRunID: "run-01", ArchiveStartedAt: &runOneStarted,
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-01/final.mp4", SizeBytes: 123,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{runOne}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-02", runTwoStarted); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}

	// A delayed report from the previous run may still be catalogued, but must
	// not complete the currently processing run.
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{runOne}); err != nil {
		t.Fatal(err)
	}
	processing, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 1 || processing[0].ArchiveRunID != "run-02" || processing[0].ArchiveReportedAt != nil {
		t.Fatalf("stale run report completed current run: processing=%#v err=%v", processing, err)
	}

	runTwo := StreamArtifact{
		ArchiveRunID: "run-02", ArchiveStartedAt: &runTwoStarted,
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-02/final.mp4", SizeBytes: 456,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{runTwo}); err != nil {
		t.Fatal(err)
	}
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 0 {
		t.Fatalf("matching run report did not complete processing: processing=%#v err=%v", processing, err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("archive run history = %#v err=%v", artifacts, err)
	}
	if artifacts[0].ID == artifacts[1].ID || artifacts[0].ArchiveRunID == artifacts[1].ArchiveRunID {
		t.Fatalf("archive runs were overwritten: %#v", artifacts)
	}
}

func TestMemoryArchiveProcessingStreamsKeepRearmedPendingRunVisible(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "rearmed archive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{ArchiveProfileID: "archive-01"}); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 20, 2, 15, 57, 0, time.UTC)
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "run-01", startedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "ready"); err != nil {
		t.Fatal(err)
	}

	processing, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 1 || processing[0].ID != stream.ID || processing[0].ArchiveRunID != "run-01" {
		t.Fatalf("rearmed pending archive disappeared: processing=%#v err=%v", processing, err)
	}

	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{{
		ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/run-01/final.mp4", SizeBytes: 456,
	}}); err != nil {
		t.Fatal(err)
	}
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 0 {
		t.Fatalf("reported rearmed archive remained in processing: processing=%#v err=%v", processing, err)
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

func TestMemoryDeleteStreamRetainsLogsAndListsHistoricalLogs(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "deleted stream log history")
	if err != nil {
		t.Fatal(err)
	}
	created, err := streams.AppendStreamLog(t.Context(), StreamLog{
		StreamID: stream.ID,
		Level:    "warning",
		Message:  "encoder input recovered",
		Fields:   map[string]any{"event_type": "encoder.input.recovered"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}

	logs, err := streams.ListStreamLogs(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ID != created.ID {
		t.Fatalf("deleted stream logs = %#v, want retained log %q", logs, created.ID)
	}
	history, err := streams.ListStreamLogHistory(t.Context(), 100, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].StreamName != stream.Name || history[0].StreamDeletedAt == nil {
		t.Fatalf("historical stream logs = %#v, want deleted stream context", history)
	}
}

func TestMemoryStreamLogHistoryUsesStableCursor(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "paged history")
	if err != nil {
		t.Fatal(err)
	}
	newest := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	for _, entry := range []StreamLog{
		{ID: "log-c", StreamID: stream.ID, Message: "c", CreatedAt: newest},
		{ID: "log-b", StreamID: stream.ID, Message: "b", CreatedAt: newest},
		{ID: "log-a", StreamID: stream.ID, Message: "a", CreatedAt: newest.Add(-time.Second)},
	} {
		if _, err := streams.AppendStreamLog(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	first, err := streams.ListStreamLogHistory(t.Context(), 2, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != "log-c" || first[1].ID != "log-b" {
		t.Fatalf("unexpected first stream-log page: %#v", first)
	}
	second, err := streams.ListStreamLogHistory(t.Context(), 2, first[1].CreatedAt, first[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != "log-a" {
		t.Fatalf("unexpected second stream-log page: %#v", second)
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

func TestMemoryArchiveProcessingStreamsStopWaitingAfterArtifactReport(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive-processing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{ArchiveProfileID: "archive-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	processing, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 0 {
		t.Fatalf("never-started legacy stream became archive-processing: streams=%#v err=%v", processing, err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "created"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), stream.ID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}

	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 1 || processing[0].ID != stream.ID {
		t.Fatalf("processing streams before artifact report = %#v err=%v", processing, err)
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{{
		Kind: "archive", Name: "final.mp4", RelativePath: "final/" + stream.ID + "/final.mp4", SizeBytes: 10,
	}}); err != nil {
		t.Fatal(err)
	}
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 0 {
		t.Fatalf("processing streams after artifact report = %#v err=%v", processing, err)
	}

	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("reported artifacts = %#v err=%v", artifacts, err)
	}
	if err := streams.DeleteStreamArtifact(t.Context(), stream.ID, artifacts[0].ID); err != nil {
		t.Fatal(err)
	}
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(processing) != 0 {
		t.Fatalf("deleted reported artifact returned to processing = %#v err=%v", processing, err)
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
