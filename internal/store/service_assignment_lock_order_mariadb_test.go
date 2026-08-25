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
	"github.com/go-sql-driver/mysql"
)

func TestMariaDBCanonicalAssignmentLockOrderConcurrency(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
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

	t.Run("stream delete vs heartbeat", func(t *testing.T) {
		suffix := mariaDBLockSuffix("dh")
		stream := createMariaDBLockStream(t, ctx, streams, suffix)
		serviceID := "worker-" + suffix
		token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "worker")
		assignMariaDBLockService(t, ctx, auth, serviceID, stream.ID)
		deleteErr, heartbeatErr := runMariaDBBarrierPair(t, ctx,
			func() error { return streams.DeleteStream(ctx, stream.ID) },
			func() error {
				_, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", CurrentStreamID: stream.ID})
				return err
			},
		)
		assertMariaDBConcurrentError(t, "stream delete", deleteErr)
		assertMariaDBConcurrentError(t, "heartbeat", heartbeatErr, store.ErrForbidden)
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{serviceID})
	})

	t.Run("stream delete vs artifact report", func(t *testing.T) {
		suffix := mariaDBLockSuffix("dr")
		stream := createMariaDBLockStream(t, ctx, streams, suffix)
		serviceID := "encoder-" + suffix
		token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "encoder_recorder")
		assignMariaDBLockService(t, ctx, auth, serviceID, stream.ID)
		artifact := mariaDBLegacyArtifact(stream.ID, 10)
		deleteErr, reportErr := runMariaDBBarrierPair(t, ctx,
			func() error { return streams.DeleteStream(ctx, stream.ID) },
			func() error {
				return streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: serviceID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{artifact})
			},
		)
		assertMariaDBConcurrentError(t, "stream delete", deleteErr)
		assertMariaDBConcurrentError(t, "artifact report", reportErr, store.ErrForbidden)
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{serviceID})
	})

	t.Run("stream delete vs guarded assign", func(t *testing.T) {
		suffix := mariaDBLockSuffix("da")
		stream := createMariaDBLockStream(t, ctx, streams, suffix)
		serviceID := "worker-" + suffix
		registerMariaDBAssignmentService(t, ctx, auth, serviceID, "worker")
		deleteErr, assignErr := runMariaDBBarrierPair(t, ctx,
			func() error { return streams.DeleteStream(ctx, stream.ID) },
			func() error {
				_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary"})
				return err
			},
		)
		assertMariaDBConcurrentError(t, "stream delete", deleteErr)
		assertMariaDBConcurrentError(t, "guarded assign", assignErr, store.ErrServiceAssignmentConflict, store.ErrNotFound)
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{serviceID})
	})

	t.Run("guarded assign vs heartbeat", func(t *testing.T) {
		suffix := mariaDBLockSuffix("ah")
		source := createMariaDBLockStream(t, ctx, streams, suffix+"-source")
		target := createMariaDBLockStream(t, ctx, streams, suffix+"-target")
		serviceID := "worker-" + suffix
		token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "worker")
		assignMariaDBLockService(t, ctx, auth, serviceID, source.ID)
		assignErr, heartbeatErr := runMariaDBBarrierPair(t, ctx,
			func() error {
				_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"})
				return err
			},
			func() error {
				_, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", CurrentStreamID: source.ID})
				return err
			},
		)
		assertMariaDBConcurrentError(t, "guarded assign", assignErr)
		assertMariaDBConcurrentError(t, "heartbeat", heartbeatErr, store.ErrForbidden)
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{source.ID, target.ID}, []string{serviceID})
	})

	t.Run("guarded unassign vs artifact report", func(t *testing.T) {
		suffix := mariaDBLockSuffix("ur")
		stream := createMariaDBLockStream(t, ctx, streams, suffix)
		serviceID := "encoder-" + suffix
		token := registerMariaDBAssignmentService(t, ctx, auth, serviceID, "encoder_recorder")
		assignMariaDBLockService(t, ctx, auth, serviceID, stream.ID)
		artifact := mariaDBLegacyArtifact(stream.ID, 11)
		unassignErr, reportErr := runMariaDBBarrierPair(t, ctx,
			func() error {
				_, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: serviceID})
				return err
			},
			func() error {
				return streams.WriteStreamArtifactReport(ctx, token, store.ServiceStreamEvent{ServiceID: serviceID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}, []store.StreamArtifact{artifact})
			},
		)
		assertMariaDBConcurrentError(t, "guarded unassign", unassignErr)
		assertMariaDBConcurrentError(t, "artifact report", reportErr, store.ErrForbidden)
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{serviceID})
	})

	t.Run("primary replacement vs service delete", func(t *testing.T) {
		suffix := mariaDBLockSuffix("pd")
		stream := createMariaDBLockStream(t, ctx, streams, suffix)
		oldID := "worker-old-" + suffix
		newID := "worker-new-" + suffix
		registerMariaDBAssignmentService(t, ctx, auth, oldID, "worker")
		registerMariaDBAssignmentService(t, ctx, auth, newID, "worker")
		assignMariaDBLockService(t, ctx, auth, oldID, stream.ID)
		replaceErr, deleteErr := runMariaDBBarrierPair(t, ctx,
			func() error {
				_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: newID, StreamID: stream.ID, AssignmentRole: "primary"})
				return err
			},
			func() error { return auth.DeleteService(ctx, oldID) },
		)
		assertMariaDBConcurrentError(t, "primary replacement", replaceErr, store.ErrServiceAssignmentConflict)
		assertMariaDBConcurrentError(t, "service delete", deleteErr, store.ErrServiceAssignmentConflict)
		if replaceErr != nil && deleteErr != nil {
			t.Fatalf("replacement and delete both failed: replace=%v delete=%v", replaceErr, deleteErr)
		}
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{oldID, newID})
	})

	for _, pair := range []string{"start vs guarded assign", "start vs guarded unassign", "start vs service delete", "start vs stream delete"} {
		pair := pair
		t.Run(pair, func(t *testing.T) {
			fixture := newMariaDBStartRaceFixture(t, ctx, streams, auth, mariaDBLockSuffix("sc"))
			claimErr, mutationErr := runMariaDBBarrierPair(t, ctx,
				func() error {
					_, err := streams.ClaimStreamStart(ctx, fixture.claim)
					return err
				},
				func() error {
					switch pair {
					case "start vs guarded assign":
						_, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: fixture.extraWorkerID, StreamID: fixture.stream.ID, AssignmentRole: "primary"})
						return err
					case "start vs guarded unassign":
						_, err := auth.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: fixture.encoderID})
						return err
					case "start vs service delete":
						return auth.DeleteService(ctx, fixture.encoderID)
					default:
						return streams.DeleteStream(ctx, fixture.stream.ID)
					}
				},
			)
			assertMariaDBConcurrentError(t, "start claim", claimErr, store.ErrServiceAssignmentConflict)
			assertMariaDBConcurrentError(t, pair, mutationErr, store.ErrServiceAssignmentConflict, store.ErrServiceAssignmentProtectedStream, store.ErrServiceUnassignProtectedStream)
			if (claimErr == nil) == (mutationErr == nil) {
				t.Fatalf("exactly one operation must acquire ownership: claim=%v mutation=%v", claimErr, mutationErr)
			}
			assertMariaDBAssignmentConsistency(t, ctx, auth, []string{fixture.stream.ID}, fixture.serviceIDs())
		})
	}

	t.Run("archive retry begin vs artifact report", func(t *testing.T) {
		suffix := mariaDBLockSuffix("rr")
		stream, encoderID, token, claimed := claimMariaDBArchiveStart(t, ctx, streams, auth, suffix, true)
		if _, transitioned, err := streams.TransitionClaimedStreamStart(ctx, claimed.OwnershipClaim, "completed"); err != nil || !transitioned {
			t.Fatalf("complete archive claim: transitioned=%v err=%v", transitioned, err)
		}
		artifact := store.StreamArtifact{
			ArchiveRunID: claimed.ArchiveAuthority.RunID, ArchiveStartedAt: claimed.ArchiveAuthority.StartedAt,
			Kind: "archive", Name: "final.mp4", RelativePath: fmt.Sprintf("final/%s/%s/final.mp4", stream.ID, claimed.ArchiveAuthority.RunID), SizeBytes: 12,
		}
		event := store.ServiceStreamEvent{ServiceID: encoderID, StreamID: stream.ID, EventType: "archive.artifacts.reported"}
		if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); err != nil {
			t.Fatal(err)
		}
		retryErr, reportErr := runMariaDBBarrierPair(t, ctx,
			func() error {
				_, err := auth.BeginStreamArchiveRetryGuarded(ctx, encoderID, stream.ID)
				return err
			},
			func() error {
				return streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact})
			},
		)
		assertMariaDBConcurrentError(t, "archive retry begin", retryErr)
		assertMariaDBConcurrentError(t, "artifact report", reportErr)
		current, err := streams.GetStream(ctx, stream.ID)
		if err != nil {
			t.Fatal(err)
		}
		processing, err := streams.ListArchiveProcessingStreams(ctx)
		if err != nil {
			t.Fatal(err)
		}
		isProcessing := false
		for _, candidate := range processing {
			if candidate.ID == stream.ID {
				isProcessing = true
			}
		}
		if (current.ArchiveReportedAt == nil) != isProcessing {
			t.Fatalf("retry/report partial state: stream=%#v processing=%v", current, isProcessing)
		}
		assertMariaDBAssignmentConsistency(t, ctx, auth, []string{stream.ID}, []string{encoderID, "worker-claim-" + suffix, "discord-claim-" + suffix})
	})
}

type mariaDBStartRaceFixture struct {
	stream        store.Stream
	claim         store.StreamStartClaimRequest
	encoderID     string
	workerID      string
	discordID     string
	extraWorkerID string
}

func newMariaDBStartRaceFixture(t *testing.T, ctx context.Context, streams store.MariaDBStreamStore, auth store.MariaDBAuthStore, suffix string) mariaDBStartRaceFixture {
	t.Helper()
	stream := createMariaDBLockStream(t, ctx, streams, suffix)
	fixture := mariaDBStartRaceFixture{
		stream: stream, encoderID: "encoder-" + suffix, workerID: "worker-" + suffix,
		discordID: "discord-" + suffix, extraWorkerID: "worker-extra-" + suffix,
	}
	for serviceID, serviceType := range map[string]string{
		fixture.encoderID: "encoder_recorder", fixture.workerID: "worker", fixture.discordID: "discord_bot", fixture.extraWorkerID: "worker",
	} {
		registerMariaDBAssignmentService(t, ctx, auth, serviceID, serviceType)
	}
	for _, serviceID := range []string{fixture.encoderID, fixture.workerID, fixture.discordID} {
		assignMariaDBLockService(t, ctx, auth, serviceID, stream.ID)
	}
	assignments, err := auth.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.stream = stream
	fixture.claim = store.StreamStartClaimRequest{
		StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt,
		ExpectedPrimaryAssignments: assignments,
	}
	return fixture
}

func (fixture mariaDBStartRaceFixture) serviceIDs() []string {
	return []string{fixture.encoderID, fixture.workerID, fixture.discordID, fixture.extraWorkerID}
}

func createMariaDBLockStream(t *testing.T, ctx context.Context, streams store.MariaDBStreamStore, suffix string) store.Stream {
	t.Helper()
	stream, err := streams.CreateStream(ctx, "lock order "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func assignMariaDBLockService(t *testing.T, ctx context.Context, auth store.MariaDBAuthStore, serviceID, streamID string) {
	t.Helper()
	if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
}

func mariaDBLegacyArtifact(streamID string, size int64) store.StreamArtifact {
	return store.StreamArtifact{Kind: "archive", Name: "final.mp4", RelativePath: fmt.Sprintf("final/%s/final.mp4", streamID), SizeBytes: size}
}

func mariaDBLockSuffix(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func runMariaDBBarrierPair(t *testing.T, ctx context.Context, left, right func() error) (error, error) {
	t.Helper()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	leftResult := make(chan error, 1)
	rightResult := make(chan error, 1)
	run := func(operation func() error, result chan<- error) {
		ready <- struct{}{}
		select {
		case <-start:
			result <- operation()
		case <-ctx.Done():
			result <- ctx.Err()
		}
	}
	go run(left, leftResult)
	go run(right, rightResult)
	for range 2 {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent operation did not reach the start barrier")
		}
	}
	close(start)
	receive := func(label string, result <-chan error) error {
		select {
		case err := <-result:
			return err
		case <-time.After(15 * time.Second):
			t.Fatalf("%s did not finish before the lock-order timeout", label)
			return nil
		}
	}
	return receive("left operation", leftResult), receive("right operation", rightResult)
}

func assertMariaDBConcurrentError(t *testing.T, label string, err error, allowed ...error) {
	t.Helper()
	if err == nil {
		return
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205) {
		t.Fatalf("%s hit MariaDB lock failure %d: %v", label, mysqlErr.Number, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("%s timed out: %v", label, err)
	}
	for _, allowedErr := range allowed {
		if errors.Is(err, allowedErr) {
			return
		}
	}
	t.Fatalf("%s returned unexpected store error (HTTP 500 candidate): %v", label, err)
}

func assertMariaDBAssignmentConsistency(t *testing.T, ctx context.Context, auth store.MariaDBAuthStore, streamIDs, serviceIDs []string) {
	t.Helper()
	for _, serviceID := range serviceIDs {
		service, err := auth.GetService(ctx, serviceID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		assignments, err := auth.ListServiceAssignmentsForService(ctx, serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if service.CurrentStreamID == "" {
			if len(assignments) != 0 {
				t.Fatalf("service %s has no current_stream_id but assignments=%#v", serviceID, assignments)
			}
			continue
		}
		if len(assignments) != 1 || assignments[0].StreamID != service.CurrentStreamID || assignments[0].ServiceType != service.ServiceType {
			t.Fatalf("service %s split ownership: service=%s assignments=%s", serviceID, formatSafeRegisteredServiceDiagnostic(service), formatSafeSensitiveCompositeDiagnostic(assignments))
		}
	}
	for _, streamID := range streamIDs {
		assignments, err := auth.ListStreamAssignments(ctx, streamID)
		if err != nil {
			t.Fatal(err)
		}
		primaryByType := make(map[string]string)
		for _, assignment := range assignments {
			if assignment.AssignmentRole == "primary" {
				if prior := primaryByType[assignment.ServiceType]; prior != "" && prior != assignment.ServiceID {
					t.Fatalf("stream %s has multiple %s primaries: %s and %s", streamID, assignment.ServiceType, prior, assignment.ServiceID)
				}
				primaryByType[assignment.ServiceType] = assignment.ServiceID
			}
			service, err := auth.GetService(ctx, assignment.ServiceID)
			if err != nil || service.CurrentStreamID != streamID {
				t.Fatalf("stream %s assignment disagrees with service: assignment=%#v service=%s err=%v", streamID, assignment, formatSafeRegisteredServiceDiagnostic(service), err)
			}
		}
	}
}
