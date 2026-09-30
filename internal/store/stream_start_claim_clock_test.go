package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/go-sql-driver/mysql"
)

type clockClaimStore interface {
	StreamStore
	StreamStartClaimStore
	StreamDiscordYouTubeLiveNotificationStore
	TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(context.Context, StreamStartOwnershipClaim, DiscordYouTubeLiveNotification) (Stream, DiscordYouTubeLiveNotification, bool, error)
}

func TestMemoryStartClaimClock(t *testing.T) { runStartClaimClockCases(t, false) }

// Existing CI discovers TestMariaDB* with its ordinary disposable database user.
func TestMariaDBStartClaimClock(t *testing.T) { runStartClaimClockCases(t, true) }

func runStartClaimClockCases(t *testing.T, maria bool) {
	if maria && os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN") == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	// Freeze the actual time.Now calls, without a production clock hook,
	// sleeps, host clock changes or replacing either store implementation.
	for _, name := range []string{"fresh", "same_clock_archive", "same_clock_no_archive", "same_clock_archive_notification", "submicro_archive", "successor_distinct_archive", "backward_stored_clock", "backward_compensation"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				streams, services, setFuture := newClockClaimStores(t, maria)
				ctx := t.Context()
				stream, err := streams.CreateStream(ctx, "claim clock "+name)
				if err != nil {
					t.Fatal(err)
				}
				serviceIDs := []string{}
				tokenIDs := []string{}
				t.Cleanup(func() { cleanupClockClaimFixture(t, streams, stream.ID, serviceIDs, tokenIDs) })
				for _, kind := range []string{"encoder_recorder", "worker", "discord_bot"} {
					id := newUUID()
					serviceIDs = append(serviceIDs, id)
					token, err := services.CreateServiceToken(ctx, kind, []string{"service.register", "service.heartbeat"})
					if err != nil {
						t.Fatal(err)
					}
					tokenIDs = append(tokenIDs, token.ID)
					registration := ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: kind, PublicURL: "https://" + id + ".example.com", Capabilities: map[string]any{}}
					if _, err = services.PrecreateService(ctx, token, registration); err != nil {
						t.Fatal(err)
					}
					if _, err = services.RegisterService(ctx, token, registration); err != nil {
						t.Fatal(err)
					}
					if _, err = services.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: id, StreamID: stream.ID, AssignmentRole: "primary"}); err != nil {
						t.Fatal(err)
					}
				}
				assignments, err := services.ListStreamAssignments(ctx, stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				archive := name != "same_clock_no_archive"
				makeClaim := func(start time.Time) (ClaimedStreamStart, error) {
					current, e := streams.GetStream(ctx, stream.ID)
					if e != nil {
						return ClaimedStreamStart{}, e
					}
					return streams.ClaimStreamStart(ctx, StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: current.Status, ExpectedStreamUpdatedAt: current.UpdatedAt, ExpectedPrimaryAssignments: assignments, ArchiveEnabled: archive, ArchiveStartedAt: start})
				}
				start := time.Now().UTC()
				first, err := makeClaim(start)
				if err != nil {
					t.Fatal("fresh claim:", err)
				}
				current, err := streams.GetStream(ctx, stream.ID)
				if err != nil || streamStartOwnershipIdentity(current) != first.OwnershipClaim.StreamIdentity {
					t.Fatal("fresh persisted identity mismatch", err)
				}
				if _, err = makeClaim(start); !errors.Is(err, ErrServiceAssignmentConflict) {
					t.Fatal("duplicate active claim was accepted", err)
				}
				notification := func() DiscordYouTubeLiveNotification {
					bot := ""
					for _, a := range assignments {
						if a.ServiceType == "discord_bot" {
							bot = a.ServiceID
						}
					}
					return DiscordYouTubeLiveNotification{StreamID: stream.ID, EventID: newUUID(), WatchURL: "https://www.youtube.com/watch?v=synthetic", DiscordServiceID: bot, DiscordTextChannelID: "1003", YouTubeMode: "stream_key"}
				}
				if name == "fresh" {
					current, queued, changed, e := streams.TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(ctx, first.OwnershipClaim, notification())
					if e != nil || !changed || current.Status != "live" || queued.ID == "" {
						t.Fatal("fresh finalization failed", changed, current.Status, e)
					}
					return
				}
				if name == "backward_compensation" {
					// A valid persisted owner can predate a wall-clock rollback.
					setFuture(t, stream.ID, start.Add(time.Second))
					ahead, e := streams.GetStream(ctx, stream.ID)
					if e != nil {
						t.Fatal(e)
					}
					owner := first.OwnershipClaim
					owner.StreamUpdatedAt = ahead.UpdatedAt
					owner.StreamIdentity = streamStartOwnershipIdentity(ahead)
					if _, changed, e := streams.TransitionClaimedStreamStart(ctx, owner, "failed"); changed || !errors.Is(e, ErrServiceAssignmentConflict) {
						t.Fatal("rollback compensation rewrote authority", changed, e)
					}
					after, _ := streams.GetStream(ctx, stream.ID)
					if !reflect.DeepEqual(ahead, after) {
						t.Fatal("rejected rollback mutated stream")
					}
					return
				}
				if _, changed, e := streams.TransitionClaimedStreamStart(ctx, first.OwnershipClaim, "failed"); e != nil || !changed {
					t.Fatal("first compensation failed", e)
				}
				failed, _ := streams.GetStream(ctx, stream.ID)
				nextStart := start
				if name == "successor_distinct_archive" {
					nextStart = start.Add(time.Microsecond)
				}
				if name == "submicro_archive" {
					nextStart = start.Add(999 * time.Nanosecond)
				}
				if name == "backward_stored_clock" {
					setFuture(t, stream.ID, start.Add(time.Second))
					failed, _ = streams.GetStream(ctx, stream.ID)
				}
				second, e := makeClaim(nextStart)
				if name != "successor_distinct_archive" && !(name == "submicro_archive" && !maria) {
					if e == nil {
						same := reflect.DeepEqual(first.OwnershipClaim, second.OwnershipClaim)
						if name == "same_clock_archive_notification" {
							_, queued, changed, notifyErr := streams.TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(ctx, first.OwnershipClaim, notification())
							t.Fatalf("ambiguous reclaim accepted: same_immutable_claim=%t stale_live_changed=%t notification_enqueued=%t err=%v", same, changed, queued.ID != "", notifyErr)
						}
						_, changed, cleanupErr := streams.TransitionClaimedStreamStart(ctx, first.OwnershipClaim, "failed")
						t.Fatalf("ambiguous or backward reclaim accepted: same_immutable_claim=%t stale_cleanup_changed=%t stale_cleanup_err=%v", same, changed, cleanupErr)
					}
					if !errors.Is(e, ErrServiceAssignmentConflict) {
						t.Fatal(e)
					}
					after, _ := streams.GetStream(ctx, stream.ID)
					if !reflect.DeepEqual(failed, after) {
						t.Fatal("rejected reclaim mutated stream")
					}
				} else {
					if e != nil {
						t.Fatal("distinguishable successor rejected", e)
					}
					if reflect.DeepEqual(first.OwnershipClaim, second.OwnershipClaim) {
						t.Fatal("successor reused immutable claim")
					}
					if _, _, changed, e := streams.TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(ctx, first.OwnershipClaim, notification()); changed || !errors.Is(e, ErrServiceAssignmentConflict) {
						t.Fatal("old claim finalized successor", changed, e)
					}
					if _, changed, e := streams.TransitionClaimedStreamStart(ctx, first.OwnershipClaim, "failed"); changed || !errors.Is(e, ErrServiceAssignmentConflict) {
						t.Fatal("old cleanup changed successor", changed, e)
					}
					current, _ := streams.GetStream(ctx, stream.ID)
					if current.Status != "starting" || streamStartOwnershipIdentity(current) != second.OwnershipClaim.StreamIdentity {
						t.Fatal("stale attempt changed successor")
					}
					if _, e := streams.GetLatestDiscordYouTubeLiveNotification(ctx, stream.ID); !errors.Is(e, ErrNotFound) {
						t.Fatal("stale attempt enqueued notification", e)
					}
					current, queued, changed, e := streams.TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(ctx, second.OwnershipClaim, notification())
					if e != nil || !changed || current.Status != "live" || queued.ID == "" {
						t.Fatal("new owner failed finalization", e)
					}
				}
				afterAssignments, _ := services.ListStreamAssignments(ctx, stream.ID)
				if !reflect.DeepEqual(assignments, afterAssignments) {
					t.Fatal("claim fixture or rejection changed assignments")
				}
				t.Logf("fixed_clock=%s archive_storage_precision=%s", start.Format(time.RFC3339Nano), map[bool]string{false: "nanoseconds", true: "microseconds"}[maria])
			})
		})
	}
}

func newClockClaimStores(t *testing.T, maria bool) (clockClaimStore, ServiceRegistryStore, func(*testing.T, string, time.Time)) {
	t.Helper()
	if !maria {
		streams := NewMemoryStreamStore()
		auth := NewMemoryAuthStore()
		auth.BindStreamAssignmentGuard(streams)
		return streams, auth, func(t *testing.T, id string, at time.Time) {
			streams.mu.Lock()
			defer streams.mu.Unlock()
			row := streams.streams[id]
			row.UpdatedAt = at
			streams.streams[id] = row
		}
	}
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"), "mysql://"))
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if cfg.DBName == "" {
		t.Fatal("test DSN must name its disposable database")
	}
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = database.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var precision int
	if err := db.QueryRowContext(t.Context(), "SELECT DATETIME_PRECISION FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='streams' AND COLUMN_NAME='updated_at'").Scan(&precision); err != nil {
		t.Fatal(err)
	}
	if precision != 6 {
		t.Fatalf("existing migrated start authority precision=%d, want 6", precision)
	}
	t.Log("actual_streams_updated_at_datetime_precision=6")
	return NewMariaDBStreamStore(db), NewMariaDBAuthStore(db), func(t *testing.T, id string, at time.Time) {
		if _, e := db.ExecContext(t.Context(), "UPDATE streams SET updated_at=? WHERE id=?", at, id); e != nil {
			t.Fatal(e)
		}
	}
}

// Remove only rows created by this case; the CI database and other fixtures stay.
func cleanupClockClaimFixture(t *testing.T, streams clockClaimStore, streamID string, serviceIDs, tokenIDs []string) {
	t.Helper()
	maria, ok := streams.(MariaDBStreamStore)
	if !ok {
		return
	}
	for _, query := range []string{
		"DELETE FROM stream_discord_youtube_live_notifications WHERE stream_id=?",
		"DELETE FROM stream_service_assignments WHERE stream_id=?",
		"UPDATE services SET current_stream_id=NULL WHERE current_stream_id=?",
		"DELETE FROM streams WHERE id=?",
	} {
		if _, err := maria.db.Exec(query, streamID); err != nil {
			t.Error(err)
		}
	}
	for _, id := range serviceIDs {
		if _, err := maria.db.Exec("DELETE FROM services WHERE service_id=?", id); err != nil {
			t.Error(err)
		}
	}
	for _, id := range tokenIDs {
		if _, err := maria.db.Exec("DELETE FROM service_tokens WHERE id=?", id); err != nil {
			t.Error(err)
		}
	}
}
