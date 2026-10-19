package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
)

type cb15Fixture struct {
	stores         CurrentDiscordCredentialStores
	token          ServiceToken
	serviceID      string
	current, other Stream
	profile        Profile
	secret         SecretStatus
	db             *sql.DB
}

func cb15StoreFixture(t *testing.T, db *sql.DB) cb15Fixture {
	t.Helper()
	f := cb15Fixture{serviceID: "cb15-" + newUUID(), db: db}
	if db == nil {
		a, s := NewMemoryAuthStore(), NewMemoryStreamStore()
		a.BindStreamAssignmentGuard(s)
		f.stores = CurrentDiscordCredentialStores{Services: a, Streams: s, Profiles: NewMemoryProfileStore(), Secrets: NewMemorySecretStore(), Leases: NewMemoryRuntimeSecretLeaseStore()}
	} else {
		f.stores = CurrentDiscordCredentialStores{Services: NewMariaDBAuthStore(db), Streams: NewMariaDBStreamStore(db), Profiles: NewMariaDBProfileStore(db), Secrets: NewMariaDBSecretStore(db, "cb15-test-only-encryption-key"), Leases: NewMariaDBRuntimeSecretLeaseStore(db)}
	}
	var err error
	f.token, err = f.stores.Services.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registration := ServiceRegistration{ServiceID: f.serviceID, ServiceType: "discord_bot", ServiceName: f.serviceID, Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com", Capabilities: map[string]any{}}
	if _, err = f.stores.Services.PrecreateService(t.Context(), f.token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = f.stores.Services.RegisterService(t.Context(), f.token, registration); err != nil {
		t.Fatal(err)
	}
	f.secret, err = f.stores.Secrets.UpdateSecret(t.Context(), "discord_bot_token_"+newUUID(), "synthetic-cb15-current-value")
	if err != nil {
		t.Fatal(err)
	}
	f.profile, err = f.stores.Profiles.CreateProfile(t.Context(), ProfileDiscordConfig, f.serviceID, map[string]any{"service_id": f.serviceID, "bot_token_secret_name": f.secret.Name, "bot_token_fingerprint": f.secret.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	f.current, err = f.stores.Streams.CreateStream(t.Context(), "current")
	if err != nil {
		t.Fatal(err)
	}
	f.other, err = f.stores.Streams.CreateStream(t.Context(), "other retained created")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Stream{f.current, f.other} {
		if _, err = f.stores.Streams.UpdateStreamSettings(t.Context(), s.ID, StreamSettings{DiscordConfigID: f.profile.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.stores.Services.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: f.serviceID, StreamID: f.current.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func cb15TestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("real MariaDB not configured; no MariaDB PASS claimed")
	}
	t.Setenv("DATABASE_URL", dsn)
	db, err := database.OpenFromEnv(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = database.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestUI188CB15CurrentBindingMemory(t *testing.T)  { cb15StoreMatrix(t, nil) }
func TestUI188CB15CurrentBindingMariaDB(t *testing.T) { cb15StoreMatrix(t, cb15TestDB(t)) }

func cb15StoreMatrix(t *testing.T, db *sql.DB) {
	t.Helper()
	t.Run("rotation preserves active lease", func(t *testing.T) {
		f := cb15StoreFixture(t, db)
		snapshot, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, lease, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		status, err := f.stores.Secrets.UpdateSecret(t.Context(), f.secret.Name, "synthetic-valid-rotated-current")
		if err != nil {
			t.Fatal(err)
		}
		f.profile.Config["bot_token_fingerprint"] = status.Fingerprint
		cb15SaveProfile(t, &f)
		rotated, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || !rotated.Bindings[0].Ready {
			t.Fatal("normal rotation unavailable", err)
		}
		if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, rotated.Bindings[0], time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
			t.Fatal("rotation bypassed active lease", err)
		}
		if err = f.stores.Leases.ReleaseRuntimeSecretLease(t.Context(), lease); err != nil {
			t.Fatal(err)
		}
		value, _, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, rotated.Bindings[0], time.Minute)
		if err != nil || value != "synthetic-valid-rotated-current" {
			t.Fatal("rotation substituted old value", err)
		}
	})
	t.Run("starting cold cache and unchanged lease key", func(t *testing.T) {
		f := cb15StoreFixture(t, db)
		before, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(before.Bindings) != 1 || !before.Bindings[0].Ready {
			t.Fatal("initial current binding unavailable", err)
		}
		if _, err = f.stores.Streams.UpdateStreamStatus(t.Context(), f.current.ID, "starting"); err != nil {
			t.Fatal(err)
		}
		after, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(after.Bindings) != 1 || after.Bindings[0] != before.Bindings[0] {
			t.Fatal("starting changed current binding", err)
		}
		value, lease, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, after.Bindings[0], time.Minute)
		if err != nil || value != "synthetic-cb15-current-value" || lease.StreamID != "" || lease.ArchiveProfileID != "" {
			t.Fatal("cold current resolver or legacy lease scope changed", err)
		}
		if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, after.Bindings[0], time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
			t.Fatal("active lease guard bypassed", err)
		}
		other, err := f.stores.Streams.GetStream(t.Context(), f.other.ID)
		if err != nil || other.Status != "created" || other.DiscordConfigID != f.profile.ID {
			t.Fatal("other waiting stream changed")
		}
	})
	for _, tc := range []struct {
		name            string
		mutate          func(*testing.T, *cb15Fixture)
		wantUnavailable bool
	}{
		{"foreign owner", func(t *testing.T, f *cb15Fixture) {
			f.profile.Config["service_id"] = "other-owner"
			cb15SaveProfile(t, f)
		}, true},
		{"missing owner", func(t *testing.T, f *cb15Fixture) { delete(f.profile.Config, "service_id"); cb15SaveProfile(t, f) }, true},
		{"missing profile", func(t *testing.T, f *cb15Fixture) {
			if err := f.stores.Profiles.DeleteProfile(t.Context(), ProfileDiscordConfig, f.profile.ID); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"wrong kind", func(t *testing.T, f *cb15Fixture) {
			p, err := f.stores.Profiles.CreateProfile(t.Context(), ProfileEncoder, "wrong-kind-"+f.serviceID, map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.stores.Streams.UpdateStreamSettings(t.Context(), f.current.ID, StreamSettings{DiscordConfigID: p.ID}); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"secret revoked", func(t *testing.T, f *cb15Fixture) {
			if _, err := f.stores.Secrets.UpdateSecret(t.Context(), f.secret.Name, ""); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"other secret kind", func(t *testing.T, f *cb15Fixture) {
			status, err := f.stores.Secrets.UpdateSecret(t.Context(), "youtube_stream_key", "synthetic-other-secret-kind")
			if err != nil {
				t.Fatal(err)
			}
			f.profile.Config["bot_token_secret_name"], f.profile.Config["bot_token_fingerprint"] = status.Name, status.Fingerprint
			cb15SaveProfile(t, f)
		}, true},
		{"fingerprint intermediate mismatch", func(t *testing.T, f *cb15Fixture) {
			if _, err := f.stores.Secrets.UpdateSecret(t.Context(), f.secret.Name, "synthetic-rotated-value"); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"reference changed", func(t *testing.T, f *cb15Fixture) {
			status, err := f.stores.Secrets.UpdateSecret(t.Context(), "discord_bot_token_"+newUUID(), "synthetic-new-reference")
			if err != nil {
				t.Fatal(err)
			}
			f.profile.Config["bot_token_secret_name"], f.profile.Config["bot_token_fingerprint"] = status.Name, status.Fingerprint
			cb15SaveProfile(t, f)
		}, false},
		{"fingerprint changed", func(t *testing.T, f *cb15Fixture) {
			status, err := f.stores.Secrets.UpdateSecret(t.Context(), f.secret.Name, "synthetic-rotated-value")
			if err != nil {
				t.Fatal(err)
			}
			f.profile.Config["bot_token_fingerprint"] = status.Fingerprint
			cb15SaveProfile(t, f)
		}, false},
		{"reassigned current", func(t *testing.T, f *cb15Fixture) {
			if _, err := f.stores.Services.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: f.serviceID, StreamID: f.other.ID, AssignmentRole: "primary"}); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cb15StoreFixture(t, db)
			before, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, &f)
			after, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Bindings) != 1 || after.Bindings[0].Ready == tc.wantUnavailable {
				t.Fatal("changed current availability not explicit")
			}
			if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, before.Bindings[0], time.Minute); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrServiceAssignmentConflict) {
				t.Fatal("old expected binding resolved after mutation", err)
			}
		})
	}
	t.Run("current lost explicit empty", func(t *testing.T) {
		f := cb15StoreFixture(t, db)
		before, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.stores.Services.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: f.serviceID}); err != nil {
			t.Fatal(err)
		}
		after, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || after.Bindings == nil || len(after.Bindings) != 0 || len(after.Assignments) != 0 {
			t.Fatal("no-current omitted or not empty", err)
		}
		if _, _, err = f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, before.Bindings[0], time.Minute); !errors.Is(err, ErrServiceAssignmentConflict) {
			t.Fatal("lost current resolved", err)
		}
	})
	t.Run("revoked and foreign token", func(t *testing.T) {
		f := cb15StoreFixture(t, db)
		if _, err := f.stores.Snapshot(t.Context(), f.serviceID, "wrong-token-id"); !errors.Is(err, ErrForbidden) {
			t.Fatal("foreign token accepted", err)
		}
		if err := f.stores.Services.RevokeServiceToken(t.Context(), f.token.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID); !errors.Is(err, ErrForbidden) {
			t.Fatal("revoked token accepted", err)
		}
	})
	t.Run("old epoch and forged expected tuple", func(t *testing.T) {
		f := cb15StoreFixture(t, db)
		snapshot, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*CurrentDiscordCredentialBinding){func(b *CurrentDiscordCredentialBinding) { b.AssignmentEpoch = b.AssignmentEpoch.Add(-time.Second) }, func(b *CurrentDiscordCredentialBinding) { b.ProfileOwnerServiceID = "other" }, func(b *CurrentDiscordCredentialBinding) { b.Ready = false }, func(b *CurrentDiscordCredentialBinding) { b.ServiceType = "worker" }} {
			b := snapshot.Bindings[0]
			mutate(&b)
			if _, _, err := f.stores.Resolve(t.Context(), f.serviceID, f.token.ID, b, time.Minute); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrServiceAssignmentConflict) {
				t.Fatal("forged binding accepted", err)
			}
		}
	})
}

func cb15SaveProfile(t *testing.T, f *cb15Fixture) {
	t.Helper()
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, f.profile.Config); err != nil {
		t.Fatal(err)
	}
}

func TestUI188CB15MariaDBSnapshotReassignmentLockBoundary(t *testing.T) {
	db := cb15TestDB(t)
	f := cb15StoreFixture(t, db)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, f.token.ID)
	defer blocker.Rollback()
	type outcome struct {
		snapshot CurrentDiscordCredentialSnapshot
		err      error
	}
	read := make(chan outcome, 1)
	go func() { s, e := f.stores.Snapshot(ctx, f.serviceID, f.token.ID); read <- outcome{s, e} }()
	// A real NOWAIT probe proves the snapshot holds the canonical service row
	// while blocked at the existing token phase, rather than guessing by timing.
	waitForMariaDBServiceRowLock(t, ctx, db, f.serviceID)
	mutation := make(chan error, 1)
	go func() {
		_, e := f.stores.Services.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: f.serviceID, StreamID: f.other.ID, AssignmentRole: "primary"})
		mutation <- e
	}()
	select {
	case e := <-mutation:
		t.Fatal("assignment advanced through held current service", e)
	case <-time.After(30 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	var before outcome
	select {
	case before = <-read:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if before.err != nil || len(before.snapshot.Bindings) != 1 || before.snapshot.Bindings[0].StreamID != f.current.ID {
		t.Fatal("snapshot was not wholly before reassignment", before.err)
	}
	select {
	case err := <-mutation:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	after, err := f.stores.Snapshot(ctx, f.serviceID, f.token.ID)
	if err != nil || len(after.Bindings) != 1 || after.Bindings[0].StreamID != f.other.ID || !after.Bindings[0].AssignmentEpoch.Equal(after.Assignments[0].AssignedAt) {
		t.Fatal("reassignment returned hybrid tuple", err)
	}
	if _, _, err = f.stores.Resolve(ctx, f.serviceID, f.token.ID, before.snapshot.Bindings[0], time.Minute); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatal("old snapshot crossed resolver authority boundary", err)
	}
}
