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

func TestMariaDBUI188R9ProfilelessArchiveReportBarrier(t *testing.T) {
	if os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN") == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"))
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
	for _, order := range []string{"mutation-before-report", "report-before-mutation"} {
		t.Run(order, func(t *testing.T) {
			suffix := "r9-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			owner, err := streams.CreateStream(ctx, suffix)
			if err != nil {
				t.Fatal(err)
			}
			target, err := streams.CreateStream(ctx, "other "+suffix)
			if err != nil {
				t.Fatal(err)
			}
			encoder := "encoder-" + suffix
			token := registerMariaDBAssignmentService(t, ctx, auth, encoder, "encoder_recorder")
			for _, service := range []struct{ id, kind string }{{encoder, "encoder_recorder"}, {"worker-" + suffix, "worker"}, {"bot-" + suffix, "discord_bot"}} {
				if service.id != encoder {
					registerMariaDBAssignmentService(t, ctx, auth, service.id, service.kind)
				}
				if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: service.id, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
					t.Fatal(err)
				}
			}
			assignments, err := auth.ListStreamAssignments(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			owner, err = streams.GetStream(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := streams.ClaimStreamStart(ctx, store.StreamStartClaimRequest{StreamID: owner.ID, ExpectedStatus: owner.Status, ExpectedStreamUpdatedAt: owner.UpdatedAt, ExpectedPrimaryAssignments: assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Date(2026, 10, 15, 14, 0, 0, 123000, time.UTC)})
			if err != nil {
				t.Fatal(err)
			}
			owner, transitioned, err := streams.TransitionClaimedStreamStart(ctx, claimed.OwnershipClaim, "completed")
			if err != nil || !transitioned || owner.ArchiveProfileID != "" || owner.ArchiveRunID == "" || owner.ArchiveStartedAt == nil {
				t.Fatalf("profile-less completed claim failed: transitioned=%v err=%v", transitioned, err)
			}
			artifacts := []store.StreamArtifact{}
			for _, item := range []struct{ kind, name string }{{"archive", "final.mp4"}, {"metadata", "metadata.json"}, {"logs", "logs.jsonl"}} {
				artifacts = append(artifacts, store.StreamArtifact{ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: owner.ArchiveStartedAt, Kind: item.kind, Name: item.name, RelativePath: fmt.Sprintf("final/%s/%s/%s", owner.ID, owner.ArchiveRunID, item.name), SizeBytes: 1})
			}
			arrived, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				close(arrived)
				<-release
				done <- streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: encoder, StreamID: owner.ID, EventType: "archive.artifacts.reported"}, artifacts)
			}()
			<-arrived
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			if order == "mutation-before-report" {
				before, err := auth.GetService(ctx, encoder)
				if err != nil {
					t.Fatal(err)
				}
				if err := streams.DeleteStream(ctx, owner.ID); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
					t.Fatalf("pre-report delete=%v", err)
				}
				if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: encoder}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
					t.Fatalf("pre-report unassign=%v", err)
				}
				if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: encoder, StreamID: target.ID, AssignmentRole: "primary"}); !errors.Is(err, store.ErrServiceAssignmentProtectedStream) {
					t.Fatalf("pre-report reassign=%v", err)
				}
				after, err := auth.GetService(ctx, encoder)
				if err != nil || after.CurrentStreamID != owner.ID || !after.UpdatedAt.Equal(before.UpdatedAt) {
					t.Fatal("rejected mutation changed owner")
				}
				pending, err := streams.ListArchiveProcessingStreams(ctx)
				found := false
				for _, stream := range pending {
					found = found || stream.ID == owner.ID
				}
				if err != nil || !found {
					t.Fatalf("profile-less pending run not listed: %v", err)
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			current, err := streams.GetStream(ctx, owner.ID)
			if err != nil || current.ArchiveReportedAt == nil || current.ArchiveRunID != owner.ArchiveRunID {
				t.Fatal("exact report failed to finalize run")
			}
			if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: encoder, StreamID: target.ID, AssignmentRole: "primary"}); err != nil {
				t.Fatalf("finalized reassign=%v", err)
			}
			if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: encoder, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
				t.Fatal(err)
			}
			if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: encoder}); err != nil {
				t.Fatalf("finalized unassign=%v", err)
			}
			if err := streams.DeleteStream(ctx, owner.ID); err != nil {
				t.Fatalf("finalized delete=%v", err)
			}
			rows, err := streams.ListStreamArtifacts(ctx, owner.ID)
			if err != nil || len(rows) != 3 {
				t.Fatalf("retained artifact count=%d err=%v", len(rows), err)
			}
			for _, row := range rows {
				if row.SourceServiceID != encoder || row.ArchiveRunID != owner.ArchiveRunID || row.ArchiveStartedAt == nil || !row.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) {
					t.Fatal("artifact lost exact source/run authority")
				}
			}
			if err := streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: encoder, StreamID: owner.ID, EventType: "archive.artifacts.reported"}, artifacts); !errors.Is(err, store.ErrForbidden) {
				t.Fatalf("deleted stream accepted a late report: %v", err)
			}
		})
	}
}
