package store

import (
	"testing"
	"time"
)

func TestMariaDBServiceTokenFixtureCleanupLeavesNoResidue(t *testing.T) {
	db, ctx := openMariaDBServiceTokenTest(t)
	var fixture *mariaDBServiceTokenCleanup
	t.Run("fixture", func(t *testing.T) {
		fixture = newMariaDBServiceTokenCleanup(t, ctx, db)
		auth := NewMariaDBAuthStore(db)
		streams := NewMariaDBStreamStore(db)
		serviceID := fixture.prefix + "encoder"
		token := createMariaDBServiceTokenPairService(t, ctx, auth, serviceID, "encoder_recorder", nil, fixture)
		stream, err := streams.CreateStream(ctx, fixture.prefix+"cleanup")
		if err != nil {
			t.Fatal(err)
		}
		fixture.trackStreamID(stream.ID)
		if _, err := auth.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{
			ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary",
		}); err != nil {
			t.Fatal(err)
		}
		var assignmentID string
		if err := db.QueryRowContext(ctx, `SELECT id FROM stream_service_assignments
WHERE service_id = ? AND stream_id = ?`, serviceID, stream.ID).Scan(&assignmentID); err != nil {
			t.Fatal(err)
		}
		fixture.trackAssignmentID(assignmentID)
		archiveStartedAt := time.Now().UTC()
		archiveRunID := "cleanup-" + stream.ID
		if err := streams.WriteStreamArtifactReport(
			ctx,
			token,
			ServiceStreamEvent{
				ServiceID: serviceID,
				StreamID:  stream.ID,
				EventType: "archive.artifacts.reported",
			},
			[]StreamArtifact{{
				ArchiveRunID: archiveRunID, ArchiveStartedAt: &archiveStartedAt,
				Kind: "archive", Name: "final.mp4",
				RelativePath: "final/" + stream.ID + "/" + archiveRunID + "/final.mp4",
				SizeBytes:    1,
			}},
		); err != nil {
			t.Fatal(err)
		}
		artifacts, err := streams.ListStreamArtifacts(ctx, stream.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, artifact := range artifacts {
			fixture.trackArtifactID(artifact.ID)
		}
		eventIDs, err := queryMariaDBServiceTokenStringsByIDs(
			ctx, db, "service_stream_events", "id", "stream_id", []string{stream.ID},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, eventID := range eventIDs {
			fixture.trackEventID(eventID)
		}
	})
	if fixture == nil {
		t.Fatal("FIX-006 cleanup fixture was not created")
	}
	if count, err := fixture.residueCount(ctx); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("FIX-006 cleanup left %d fixture rows", count)
	}
}
