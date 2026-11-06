package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBArchiveRunSourceAcceptsLateExactReportAfterNodeReuse(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	streams, auth := store.NewMariaDBStreamStore(db), store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	encoderID, otherEncoderID := "late-source-"+suffix, "new-source-"+suffix
	token := registerMariaDBAssignmentService(t, ctx, auth, encoderID, "encoder_recorder")
	otherToken := registerMariaDBAssignmentService(t, ctx, auth, otherEncoderID, "encoder_recorder")
	owner, err := streams.CreateStream(ctx, "late source A "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	next, err := streams.CreateStream(ctx, "late source B "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: encoderID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 6, 1, 0, 0, 123000, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(ctx, owner.ID, "late-A-"+suffix, started)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, owner.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	artifact := store.StreamArtifact{
		ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: owner.ArchiveStartedAt,
		Kind: "archive", Name: "final.mp4",
		RelativePath: fmt.Sprintf("final/%s/%s/final.mp4", owner.ID, owner.ArchiveRunID), SizeBytes: 1,
	}
	event := store.ServiceStreamEvent{ServiceID: encoderID, StreamID: owner.ID, EventType: "archive.artifacts.reported"}
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	var source string
	var sourceStartedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT service_id, archive_started_at FROM stream_archive_run_sources WHERE stream_id = ? AND archive_run_id = ?`,
		owner.ID, owner.ArchiveRunID).Scan(&source, &sourceStartedAt); err != nil || source != encoderID || !sourceStartedAt.Equal(started) {
		t.Fatalf("exact source backfill: source=%s err=%v", source, err)
	}
	first, err := streams.ListStreamArtifacts(ctx, owner.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("first report count=%d err=%v", len(first), err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: encoderID, StreamID: next.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: otherEncoderID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	for _, current := range []store.Stream{owner, next} {
		if _, err := streams.PrepareStreamArchiveRun(ctx, current.ID, "new-"+current.ID, started.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := streams.UpdateStreamStatus(ctx, current.ID, "completed"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(ctx, otherEncoderID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(ctx, encoderID, next.ID); err != nil {
		t.Fatal(err)
	}
	beforeA, err := streams.GetStream(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeB, err := streams.GetStream(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeService, err := auth.GetService(ctx, encoderID)
	if err != nil {
		t.Fatal(err)
	}
	artifact.SizeBytes = 2
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); err != nil {
		t.Fatalf("late exact report: %v", err)
	}
	afterA, err := streams.GetStream(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterB, err := streams.GetStream(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterService, err := auth.GetService(ctx, encoderID)
	if err != nil {
		t.Fatal(err)
	}
	if afterA.ArchiveRunID != beforeA.ArchiveRunID || afterA.ArchiveReportedAt != nil || !afterA.UpdatedAt.Equal(beforeA.UpdatedAt) ||
		afterB.ArchiveRunID != beforeB.ArchiveRunID || afterB.ArchiveReportedAt != nil || !afterB.UpdatedAt.Equal(beforeB.UpdatedAt) ||
		afterService.CurrentStreamID != next.ID || !afterService.UpdatedAt.Equal(beforeService.UpdatedAt) {
		t.Fatal("late report changed a current stream or Node owner")
	}
	second, err := streams.ListStreamArtifacts(ctx, owner.ID)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID ||
		!second[0].CreatedAt.Equal(first[0].CreatedAt) || second[0].SizeBytes != 2 || second[0].SourceServiceID != encoderID {
		t.Fatalf("late report lost artifact identity: count=%d err=%v", len(second), err)
	}
	// Both newer runs remain guarded after the old run's accepted duplicate.
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: otherEncoderID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("newer A guard error=%v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: encoderID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("current B guard error=%v", err)
	}
	badTime := started.Add(time.Microsecond)
	wrongTime := artifact
	wrongTime.ArchiveStartedAt = &badTime
	badRun := artifact
	badRun.ArchiveRunID = "forged-"+suffix
	badRun.RelativePath = fmt.Sprintf("final/%s/%s/final.mp4", owner.ID, badRun.ArchiveRunID)
	for _, tc := range []struct {
		name     string
		token    store.ServiceToken
		event    store.ServiceStreamEvent
		artifact store.StreamArtifact
	}{
		{"forged run", token, event, badRun},
		{"wrong exact timestamp", token, event, wrongTime},
		{"different source currently owns A", otherToken, store.ServiceStreamEvent{ServiceID: otherEncoderID, StreamID: owner.ID, EventType: event.EventType}, artifact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := streams.WriteStreamArtifactReport(ctx, tc.token, tc.event, []store.StreamArtifact{tc.artifact}); !errors.Is(err, store.ErrForbidden) {
				t.Fatalf("report error=%v, want ErrForbidden", err)
			}
		})
	}
	if _, err := db.ExecContext(ctx, `UPDATE service_tokens SET scopes = '["service.heartbeat"]' WHERE id = ?`, token.ID); err != nil {
		t.Fatal(err)
	}
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("stored scope check error=%v", err)
	}
	if _, err := auth.RotateServiceToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("stale token error=%v", err)
	}
	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_stream_events WHERE stream_id = ? AND event_type = 'archive.artifacts.reported'`, owner.ID).Scan(&eventCount); err != nil || eventCount != 2 {
		t.Fatalf("append-only report history count=%d err=%v", eventCount, err)
	}
}
