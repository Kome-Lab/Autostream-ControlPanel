package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func preparationClaimStores(t *testing.T, maria bool) outputSecretStores {
	t.Helper()
	if !maria {
		a := store.NewMemoryAuthStore()
		return outputSecretStores{a, a, a, store.NewMemoryStreamStore(), store.NewMemoryProfileStore(), store.NewMemorySecretStore(), store.NewMemoryRuntimeSecretLeaseStore()}
	}
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	cfg, e := mysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	if e != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	admin, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "autostream077_claim_" + outputSecretRandom(t)[:16]
	if _, e = admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec("DROP DATABASE `" + name + "`"); e != nil {
			t.Error(e)
		}
	})
	cfg.DBName = name
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	if e = database.RunEmbeddedMigrations(t.Context(), db); e != nil {
		t.Fatal(e)
	}
	key := outputSecretRandom(t)
	a := store.NewMariaDBAuthStoreWithSecretKey(db, key)
	return outputSecretStores{a, a, store.NewMariaDBAuditStore(db), store.NewMariaDBStreamStore(db), store.NewMariaDBProfileStore(db), store.NewMariaDBSecretStore(db, key), store.NewMariaDBRuntimeSecretLeaseStore(db)}
}
func preparationClaimFixture(t *testing.T, maria bool) (*outputSecretFixture, store.ClaimedStreamStart) {
	t.Helper()
	f := newOutputSecretFixture(t, preparationClaimStores(t, maria))
	for _, item := range []struct{ id, kind string }{{"claim-worker", "worker"}, {"claim-bot", "discord_bot"}} {
		f.register(t, item.id, item.kind)
		assignServiceForTest(t, f.handler, f.cookie, f.csrf, item.id, f.stream.ID)
	}
	return f, claimPreparationFixture(t, f)
}
func claimPreparationFixture(t *testing.T, f *outputSecretFixture) store.ClaimedStreamStart {
	t.Helper()
	claim, err := tryClaimPreparationFixture(t, f)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}
func tryClaimPreparationFixture(t *testing.T, f *outputSecretFixture) (store.ClaimedStreamStart, error) {
	t.Helper()
	ctx := t.Context()
	stream, e := f.streams.GetStream(ctx, f.stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.services.ListStreamAssignments(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	startedAt := time.Now().UTC()
	claim, e := f.streams.(store.StreamStartClaimStore).ClaimStreamStart(ctx, store.StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedPrimaryAssignments: a, ArchiveEnabled: true, ArchiveStartedAt: startedAt})
	if e != nil {
		t.Logf("claim_attempt status=%s stored_updated_at=%s attempt_at=%s stored_after_claim_second=%t primary_assignments=%d", stream.Status, stream.UpdatedAt.Format(time.RFC3339Nano), startedAt.Format(time.RFC3339Nano), stream.UpdatedAt.After(startedAt.Truncate(time.Second)), len(a))
	}
	if e == nil {
		if _, maria := f.streams.(store.MariaDBStreamStore); maria {
			t.Logf("claim_precision previous_updated_at=%s accepted_updated_at=%s accepted_archive_at=%s", stream.UpdatedAt.Format(time.RFC3339Nano), claim.Stream.UpdatedAt.Format(time.RFC3339Nano), claim.ArchiveAuthority.StartedAt.Format(time.RFC3339Nano))
		}
	}
	return claim, e
}

type preparationNotificationStore interface {
	TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(context.Context, store.StreamStartOwnershipClaim, store.DiscordYouTubeLiveNotification) (store.Stream, store.DiscordYouTubeLiveNotification, bool, error)
}

func TestStartPreparationClaimMemoryAndMariaDB(t *testing.T) {
	for _, maria := range []bool{false, true} {
		name := "Memory"
		if maria {
			name = "MariaDB"
		}
		t.Run(name, func(t *testing.T) {
			for _, kind := range []string{"complete", "wrong_identity", "wrong_assignment", "new_claim", "protected_changes"} {
				t.Run(kind, func(t *testing.T) {
					f, claimed := preparationClaimFixture(t, maria)
					ctx := t.Context()
					if !f.handler.startPreparationClaimCurrent(ctx, claimed) {
						t.Fatal("fresh real claim not recognized")
					}
					original := claimed.OwnershipClaim
					successorStatus := "starting"
					switch kind {
					case "wrong_identity":
						claimed.OwnershipClaim.StreamIdentity = "stale"
					case "wrong_assignment":
						copied := append([]store.StreamStartAssignmentClaim(nil), claimed.OwnershipClaim.Assignments...)
						copied[0].ServiceID = "not-owned"
						claimed.OwnershipClaim.Assignments = copied
					case "new_claim":
						if _, changed, e := f.streams.(store.StreamStartClaimStore).TransitionClaimedStreamStart(ctx, original, "failed"); e != nil || !changed {
							t.Fatal(e)
						}
						successor, err := tryClaimPreparationFixture(t, f)
						if errors.Is(err, store.ErrServiceAssignmentConflict) {
							// Equal stored authority is rejected before accepting a
							// successor. Keep checking no live/notification side effect.
							successorStatus = "failed"
							t.Log("successor_rejected=ambiguous_clock_conflict")
						} else if err != nil {
							t.Fatal(err)
						} else {
							if !f.handler.startPreparationClaimCurrent(ctx, successor) || f.handler.startPreparationClaimCurrent(ctx, claimed) {
								t.Fatal("accepted successor did not fence old claim")
							}
						}
					case "protected_changes":
						guard := f.services.(store.ServiceAssignmentGuardStore)
						ready := make(chan struct{})
						errs := make(chan error, 8)
						var wg sync.WaitGroup
						for i := 0; i < 8; i++ {
							wg.Add(1)
							go func(i int) {
								defer wg.Done()
								<-ready
								var e error
								if i%2 == 0 {
									_, e = guard.UnassignServiceFromStreamGuarded(ctx, store.ServiceUnassignmentMutation{ServiceID: f.serviceID})
								} else {
									_, e = guard.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: f.serviceID, StreamID: f.otherStream.ID, AssignmentRole: "primary"})
								}
								errs <- e
							}(i)
						}
						close(ready)
						wg.Wait()
						close(errs)
						for e := range errs {
							if !errors.Is(e, store.ErrServiceAssignmentConflict) && !errors.Is(e, store.ErrServiceAssignmentProtectedStream) && !errors.Is(e, store.ErrServiceUnassignProtectedStream) {
								t.Fatalf("active owner reassigned: %v", e)
							}
						}
						if _, e := f.streams.UpdateStreamSettings(ctx, f.stream.ID, store.StreamSettings{Name: "changed", YouTubeOutputID: "other-output"}); e != nil {
							t.Fatal(e)
						}
						if f.handler.startPreparationClaimCurrent(ctx, claimed) {
							t.Fatal("selected profile mutation preserved old start ownership")
						}
					}
					notification := store.DiscordYouTubeLiveNotification{StreamID: f.stream.ID, EventID: "youtube-live-077", WatchURL: "https://www.youtube.com/watch?v=synthetic", DiscordServiceID: "claim-bot", DiscordTextChannelID: "1003", YouTubeMode: "stream_key"}
					stream, queued, changed, e := f.streams.(preparationNotificationStore).TransitionClaimedStartAndEnqueueDiscordYouTubeLiveNotification(ctx, claimed.OwnershipClaim, notification)
					success := kind == "complete"
					if success {
						if e != nil || !changed || stream.Status != "live" || queued.ID == "" {
							t.Fatalf("current owner finalization: changed=%v status=%s err=%v", changed, stream.Status, e)
						}
					} else {
						if changed || (e == nil && successorStatus != "failed") {
							t.Fatal("stale claim completed or enqueued")
						}
						current, e := f.streams.GetStream(ctx, f.stream.ID)
						if e != nil || current.Status != successorStatus {
							t.Fatal("stale result changed successor", e)
						}
						if _, e := f.streams.(store.StreamDiscordYouTubeLiveNotificationStore).GetLatestDiscordYouTubeLiveNotification(ctx, f.stream.ID); e == nil {
							t.Fatal("stale notification enqueued")
						}
					}
				})
			}
		})
	}
}
