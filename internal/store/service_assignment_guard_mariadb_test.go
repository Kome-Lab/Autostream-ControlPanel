package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBServiceAssignmentGuardParityAndConcurrency(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	owner, err := streams.CreateStream(ctx, "assignment owner "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(ctx, "assignment target "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	serviceID := "encoder-assignment-" + suffix
	token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "encoder_recorder")
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, owner.ID, "live"); err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetService(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 12
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(unassign bool) {
			defer wg.Done()
			if unassign {
				_, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: serviceID})
				errs <- err
				return
			}
			_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"})
			errs <- err
		}(index%2 == 0)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, store.ErrServiceAssignmentProtectedStream) && !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
			t.Fatalf("protected concurrent mutation err = %v", err)
		}
	}
	same, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: owner.ID, AssignmentRole: "primary"})
	if err != nil {
		t.Fatalf("same-target assignment: %v", err)
	}
	if !same.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("same-target assignment churned updated_at: before=%s after=%s", before.UpdatedAt, same.UpdatedAt)
	}
	after, err := auth.GetService(ctx, serviceID)
	if err != nil || after.CurrentStreamID != owner.ID || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("protected mutation changed service: before=%s after=%s err=%v", formatSafeRegisteredServiceDiagnostic(before), formatSafeRegisteredServiceDiagnostic(after), err)
	}
	assignments, err := auth.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil || len(assignments) != 1 || assignments[0].StreamID != owner.ID || assignments[0].AssignmentRole != "primary" {
		t.Fatalf("protected owner assignments=%#v err=%v", assignments, err)
	}

	owner, err = streams.UpdateStreamSettings(ctx, owner.ID, store.StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile", ArchiveFileName: "recording.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	owner, err = streams.PrepareStreamArchiveRun(ctx, owner.ID, "archive-run-"+suffix, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(ctx, owner.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"}); !errors.Is(err, store.ErrServiceAssignmentProtectedStream) {
		t.Fatalf("archive reassign err=%v", err)
	}
	artifactPath := fmt.Sprintf("final/%s/%s/final.mp4", owner.ID, owner.ArchiveRunID)
	if err := streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: serviceID, StreamID: owner.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
		ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: owner.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: artifactPath, SizeBytes: 42,
	}}); err != nil {
		t.Fatalf("artifact report after rejected theft: %v", err)
	}
	reported, err := streams.GetStream(ctx, owner.ID)
	if err != nil || reported.ArchiveRunID != owner.ArchiveRunID || reported.ArchiveStartedAt == nil || !reported.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || reported.ArchiveReportedAt == nil || reported.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("archive identity changed: before=%#v after=%#v err=%v", owner, reported, err)
	}
	retry, err := auth.BeginStreamArchiveRetryGuarded(ctx, serviceID, owner.ID)
	if err != nil {
		t.Fatalf("begin guarded retry: %v", err)
	}
	if retry.ArchiveRunID != owner.ArchiveRunID || retry.ArchiveStartedAt == nil || !retry.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || retry.ArchiveReportedAt != nil || retry.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("retry changed archive identity: before=%#v retry=%#v", owner, retry)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"}); !errors.Is(err, store.ErrServiceAssignmentProtectedStream) {
		t.Fatalf("retry reassign err=%v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: serviceID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("retry unassign err=%v", err)
	}
	if err := streams.DeleteStream(ctx, owner.ID); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("retry delete stream err=%v", err)
	}
	protectedStream, err := streams.GetStream(ctx, owner.ID)
	if err != nil || protectedStream.DeletedAt != nil {
		t.Fatalf("retry delete mutated stream=%#v err=%v", protectedStream, err)
	}
	protectedService, err := auth.GetService(ctx, serviceID)
	if err != nil || protectedService.CurrentStreamID != owner.ID {
		t.Fatalf("retry delete mutated service=%s err=%v", formatSafeRegisteredServiceDiagnostic(protectedService), err)
	}
	if err := streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: serviceID, StreamID: owner.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
		ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: owner.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: artifactPath, SizeBytes: 84,
	}}); err != nil {
		t.Fatalf("artifact report after guarded retry: %v", err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatalf("idle reassign after retry report: %v", err)
	}
	assignments, err = auth.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil || len(assignments) != 1 || assignments[0].StreamID != target.ID {
		t.Fatalf("post-report assignments=%#v err=%v", assignments, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE services SET current_stream_id = ? WHERE service_id = ?`, owner.ID, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: serviceID}); !errors.Is(err, store.ErrServiceAssignmentConflict) {
		t.Fatalf("split-brain unassign err=%v", err)
	}
	corrupted, err := auth.GetService(ctx, serviceID)
	if err != nil || corrupted.CurrentStreamID != owner.ID {
		t.Fatalf("split-brain current_stream_id was repaired: service=%s err=%v", formatSafeRegisteredServiceDiagnostic(corrupted), err)
	}
	assignments, err = auth.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil || len(assignments) != 1 || assignments[0].StreamID != target.ID {
		t.Fatalf("split-brain assignment row was repaired: assignments=%#v err=%v", assignments, err)
	}

	legacy, err := streams.CreateStream(ctx, "legacy retry "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	legacyServiceID := "encoder-legacy-retry-" + suffix
	legacyToken := registerMariaDBAssignmentService(t, ctx, auth, legacyServiceID, "encoder_recorder")
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: legacyServiceID, StreamID: legacy.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(ctx, legacyServiceID, legacy.ID); err != nil {
		t.Fatalf("begin legacy retry: %v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: legacyServiceID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("legacy retry unassign err=%v", err)
	}
	staleStartedAt := time.Now().UTC().Add(-time.Hour)
	stalePath := fmt.Sprintf("final/%s/stale-run/final.mp4", legacy.ID)
	if err := streams.WriteStreamArtifactReport(ctx, legacyToken, store.ServiceStreamEvent{ServiceID: legacyServiceID, StreamID: legacy.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
		ArchiveRunID: "stale-run", ArchiveStartedAt: &staleStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: stalePath, SizeBytes: 20,
	}}); err != nil {
		t.Fatalf("stale legacy retry artifact report: %v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: legacyServiceID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("stale report cleared legacy retry guard: %v", err)
	}
	legacyPath := fmt.Sprintf("final/%s/final.mp4", legacy.ID)
	if err := streams.WriteStreamArtifactReport(ctx, legacyToken, store.ServiceStreamEvent{ServiceID: legacyServiceID, StreamID: legacy.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
		Kind: "archive", Name: "final.mp4", RelativePath: legacyPath, SizeBytes: 21,
	}}); err != nil {
		t.Fatalf("legacy retry artifact report: %v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: legacyServiceID}); err != nil {
		t.Fatalf("unassign after legacy retry report: %v", err)
	}
}

func TestMariaDBArchiveAuthorityCapabilityParity(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)

	for _, modern := range []bool{false, true} {
		name := "legacy"
		if modern {
			name = "modern"
		}
		t.Run(name, func(t *testing.T) {
			suffix := name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			stream, encoderID, encoderToken, claimed := claimMariaDBArchiveStart(t, ctx, streams, auth, suffix, modern)
			if modern {
				if claimed.ArchiveAuthority.Legacy || claimed.ArchiveAuthority.RunID == "" || claimed.ArchiveAuthority.StartedAt == nil {
					t.Fatalf("modern authority=%#v", claimed.ArchiveAuthority)
				}
			} else if !claimed.ArchiveAuthority.Legacy || claimed.ArchiveAuthority.RunID != "" || claimed.ArchiveAuthority.StartedAt != nil || claimed.Stream.ArchiveStartedAt != nil {
				t.Fatalf("legacy authority=%#v stream=%#v", claimed.ArchiveAuthority, claimed.Stream)
			}
			completed, transitioned, err := streams.TransitionClaimedStreamStart(ctx, claimed.OwnershipClaim, "completed")
			if err != nil || !transitioned {
				t.Fatalf("complete claim: stream=%#v transitioned=%v err=%v", completed, transitioned, err)
			}
			if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: encoderID}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
				t.Fatalf("pre-report unassign err=%v", err)
			}

			wrongStream, err := streams.CreateStream(ctx, "wrong archive target "+suffix)
			if err != nil {
				t.Fatal(err)
			}
			wrongPath := fmt.Sprintf("final/%s/final.mp4", wrongStream.ID)
			if err := streams.WriteStreamArtifactReport(ctx, encoderToken, store.ServiceStreamEvent{ServiceID: encoderID, StreamID: wrongStream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
				Kind: "archive", Name: "final.mp4", RelativePath: wrongPath, SizeBytes: 1,
			}}); !errors.Is(err, store.ErrForbidden) {
				t.Fatalf("wrong-stream report err=%v", err)
			}

			if modern {
				staleStartedAt := claimed.ArchiveAuthority.StartedAt.Add(-time.Minute)
				staleRunID := "stale-run-" + suffix
				stalePath := fmt.Sprintf("final/%s/%s/final.mp4", stream.ID, staleRunID)
				if err := streams.WriteStreamArtifactReport(ctx, encoderToken, store.ServiceStreamEvent{ServiceID: encoderID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
					ArchiveRunID: staleRunID, ArchiveStartedAt: &staleStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: stalePath, SizeBytes: 2,
				}}); err != nil {
					t.Fatal(err)
				}
				wrongStartedAt := claimed.ArchiveAuthority.StartedAt.Add(time.Second)
				currentPath := fmt.Sprintf("final/%s/%s/final.mp4", stream.ID, claimed.ArchiveAuthority.RunID)
				if err := streams.WriteStreamArtifactReport(ctx, encoderToken, store.ServiceStreamEvent{ServiceID: encoderID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{{
					ArchiveRunID: claimed.ArchiveAuthority.RunID, ArchiveStartedAt: &wrongStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: currentPath, SizeBytes: 3,
				}}); err != nil {
					t.Fatal(err)
				}
				if current, getErr := streams.GetStream(ctx, stream.ID); getErr != nil || current.ArchiveReportedAt != nil {
					t.Fatalf("stale modern report closed authority: stream=%#v err=%v", current, getErr)
				}
			}

			artifact := store.StreamArtifact{Kind: "archive", Name: "final.mp4", SizeBytes: 4}
			if modern {
				artifact.ArchiveRunID = claimed.ArchiveAuthority.RunID
				artifact.ArchiveStartedAt = claimed.ArchiveAuthority.StartedAt
				artifact.RelativePath = fmt.Sprintf("final/%s/%s/final.mp4", stream.ID, claimed.ArchiveAuthority.RunID)
			} else {
				artifact.RelativePath = fmt.Sprintf("final/%s/final.mp4", stream.ID)
			}
			if err := streams.WriteStreamArtifactReport(ctx, encoderToken, store.ServiceStreamEvent{ServiceID: encoderID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
			reported, err := streams.GetStream(ctx, stream.ID)
			if err != nil || reported.ArchiveReportedAt == nil {
				t.Fatalf("matching report did not close authority: stream=%#v err=%v", reported, err)
			}
			firstReportedAt := *reported.ArchiveReportedAt
			if err := streams.WriteStreamArtifactReport(ctx, encoderToken, store.ServiceStreamEvent{ServiceID: encoderID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
			duplicate, err := streams.GetStream(ctx, stream.ID)
			if err != nil || duplicate.ArchiveReportedAt == nil || !duplicate.ArchiveReportedAt.Equal(firstReportedAt) {
				t.Fatalf("duplicate report was not idempotent: first=%s stream=%#v err=%v", firstReportedAt, duplicate, err)
			}
			if !modern {
				var pendingMarkers, closedMarkers int
				if err := db.QueryRowContext(ctx, `SELECT
COUNT(CASE WHEN message = 'legacy archive assignment guard pending' THEN 1 END),
COUNT(CASE WHEN message = 'legacy archive assignment guard closed' THEN 1 END)
FROM stream_logs WHERE stream_id = ?`, stream.ID).Scan(&pendingMarkers, &closedMarkers); err != nil {
					t.Fatal(err)
				}
				if pendingMarkers != 1 || closedMarkers != 1 {
					t.Fatalf("legacy guard did not use one append-only pending/closure pair: pending=%d closed=%d", pendingMarkers, closedMarkers)
				}
			}
			if _, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: encoderID}); err != nil {
				t.Fatalf("post-report unassign: %v", err)
			}
			if err := streams.DeleteStream(ctx, stream.ID); err != nil {
				t.Fatalf("post-report delete: %v", err)
			}
		})
	}
}

func claimMariaDBArchiveStart(t *testing.T, ctx context.Context, streams store.MariaDBStreamStore, auth store.MariaDBAuthStore, suffix string, modern bool) (store.Stream, string, store.ServiceToken, store.ClaimedStreamStart) {
	t.Helper()
	stream, err := streams.CreateStream(ctx, "archive claim "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{Name: stream.Name, ArchiveProfileID: "archive-profile"})
	if err != nil {
		t.Fatal(err)
	}
	encoderID := "encoder-claim-" + suffix
	capabilities := map[string]any{}
	if modern {
		capabilities["archive_runs"] = true
	}
	encoderToken := registerMariaDBAssignmentServiceWithCapabilities(t, ctx, auth, encoderID, "encoder_recorder", capabilities)
	serviceIDs := []string{encoderID, "worker-claim-" + suffix, "discord-claim-" + suffix}
	registerMariaDBAssignmentService(t, ctx, auth, serviceIDs[1], "worker")
	registerMariaDBAssignmentService(t, ctx, auth, serviceIDs[2], "discord_bot")
	for _, serviceID := range serviceIDs {
		if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary"}); err != nil {
			t.Fatal(err)
		}
	}
	assignments, err := auth.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := streams.ClaimStreamStart(ctx, store.StreamStartClaimRequest{
		StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt,
		ExpectedPrimaryAssignments: assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Date(2026, 8, 23, 5, 6, 7, 123456000, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream, encoderID, encoderToken, claimed
}

func TestMariaDBHeartbeatCannotRestoreCurrentStreamAfterConcurrentUnassign(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	stream, err := streams.CreateStream(ctx, "heartbeat assignment race "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	serviceID := "worker-heartbeat-race-" + suffix
	token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "worker")
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}

	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var lockedServiceID string
	if err := blocker.QueryRowContext(ctx, `SELECT service_id FROM services WHERE service_id = ? FOR UPDATE`, serviceID).Scan(&lockedServiceID); err != nil {
		t.Fatal(err)
	}
	heartbeatDone := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", CurrentStreamID: stream.ID})
		heartbeatDone <- err
	}()
	select {
	case err := <-heartbeatDone:
		t.Fatalf("heartbeat did not wait for the service assignment lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := blocker.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE service_id = ?`, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL WHERE service_id = ?`, serviceID); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-heartbeatDone; !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("heartbeat after concurrent unassign err=%v, want forbidden", err)
	}
	service, err := auth.GetService(ctx, serviceID)
	if err != nil || service.CurrentStreamID != "" {
		t.Fatalf("heartbeat restored current_stream_id: service=%s err=%v", formatSafeRegisteredServiceDiagnostic(service), err)
	}
	assignments, err := auth.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("assignments after concurrent unassign=%#v err=%v", assignments, err)
	}
}

func TestMariaDBServiceAssignmentGuardKeepsOnePrimaryOwner(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	target, err := streams.CreateStream(ctx, "primary race "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	serviceIDs := []string{"worker-primary-a-" + suffix, "worker-primary-b-" + suffix}
	for _, serviceID := range serviceIDs {
		registerMariaDBAssignmentService(t, ctx, auth, serviceID, "worker")
	}
	start := make(chan struct{})
	errs := make(chan error, len(serviceIDs))
	var wg sync.WaitGroup
	for _, serviceID := range serviceIDs {
		serviceID := serviceID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, store.ErrServiceAssignmentConflict) {
			t.Fatalf("concurrent primary assignment err=%v", err)
		}
	}
	primary, err := auth.ListStreamAssignments(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(primary) != 1 || primary[0].AssignmentRole != "primary" || primary[0].ServiceType != "worker" {
		t.Fatalf("primary assignments=%s, want exactly one worker primary", formatSafeSensitiveCompositeDiagnostic(primary))
	}
	for _, serviceID := range serviceIDs {
		service, err := auth.GetService(ctx, serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if service.ServiceID == primary[0].ServiceID && service.CurrentStreamID != target.ID {
			t.Fatalf("primary service current_stream_id=%q", service.CurrentStreamID)
		}
		if service.ServiceID != primary[0].ServiceID && service.CurrentStreamID != "" {
			t.Fatalf("replaced service retained current_stream_id=%q", service.CurrentStreamID)
		}
	}
}

func registerMariaDBAssignmentService(t *testing.T, ctx context.Context, auth store.MariaDBAuthStore, serviceID, serviceType string) store.ServiceToken {
	return registerMariaDBAssignmentServiceWithCapabilities(t, ctx, auth, serviceID, serviceType, nil)
}

func registerMariaDBAssignmentServiceWithCapabilities(t *testing.T, ctx context.Context, auth store.MariaDBAuthStore, serviceID, serviceType string, capabilities map[string]any) store.ServiceToken {
	t.Helper()
	scopes := []string{"service.register", "service.heartbeat"}
	if serviceType == "encoder_recorder" {
		scopes = append(scopes, "encoder.status.write")
	}
	token, err := auth.CreateServiceToken(ctx, serviceType, scopes)
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Capabilities: capabilities}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	return token
}
