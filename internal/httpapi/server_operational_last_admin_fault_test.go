package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/v3authorization"
)

type operationalLastAdminErrorStore struct {
	store.MariaDBAuthStore
	reported chan<- error
}

func (s operationalLastAdminErrorStore) SetUserStatus(ctx context.Context, id, state string) (store.User, error) {
	u, e := s.MariaDBAuthStore.SetUserStatus(ctx, id, state)
	s.reported <- e
	return u, e
}
func (s operationalLastAdminErrorStore) DeleteUser(ctx context.Context, id string) error {
	e := s.MariaDBAuthStore.DeleteUser(ctx, id)
	s.reported <- e
	return e
}
func (s operationalLastAdminErrorStore) UpdateUser(ctx context.Context, id string, p store.UserPatch) (store.User, error) {
	u, e := s.MariaDBAuthStore.UpdateUser(ctx, id, p)
	s.reported <- e
	return u, e
}
func (s operationalLastAdminErrorStore) ResetPassword(ctx context.Context, id, password string) error {
	e := s.MariaDBAuthStore.ResetPassword(ctx, id, password)
	s.reported <- e
	return e
}

type operationalLastAdminState struct {
	user                                  store.User
	generation, history, sessions, audits int64
}

func operationalLastAdminCapture(t *testing.T, db *sql.DB) operationalLastAdminState {
	t.Helper()
	var state operationalLastAdminState
	var err error
	state.user, err = store.NewMariaDBAuthStore(db).GetUser(t.Context(), "la-admin-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		query string
		value *int64
	}{
		{"SELECT generation FROM operational_authorization_epoch WHERE singleton=1", &state.generation},
		{"SELECT COUNT(*) FROM operational_authorization_history", &state.history},
		{"SELECT COUNT(*) FROM sessions WHERE user_id='la-admin-a'", &state.sessions},
		{"SELECT COUNT(*) FROM audit_logs", &state.audits},
	} {
		if err = db.QueryRowContext(t.Context(), q.query).Scan(q.value); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func TestOperationalLastAdminSQLRollbackAndCommitUnknown(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	for _, op := range operationalLastAdminOperations {
		for _, mode := range []string{"rollback-after-write", "disconnect-before-commit", "commit-ack-lost"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				operationalLastAdminSeed(t, db)
				// Ensure deletion's first partial statement really removes a Session before
				// the injected response failure; this Session must return on rollback.
				if _, err := store.NewMariaDBAuthStore(db).CreateSession(t.Context(), "la-admin-a", time.Hour, time.Hour); err != nil {
					t.Fatal(err)
				}
				owner := &operationalLastAdminDriver{mode: mode}
				e := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, owner), operationalLastAdminActor(op, "la-admin-a"))
				reported := make(chan error, 1)
				e.server.users = operationalLastAdminErrorStore{MariaDBAuthStore: store.NewMariaDBAuthStore(e.db), reported: reported}
				before := operationalLastAdminCapture(t, db)
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				r := operationalLastAdminRequest(ctx, t, e, op, "la-admin-a")
				if r.Code >= 200 && r.Code < 300 {
					t.Fatalf("uncertain or failed write reported success: HTTP=%d", r.Code)
				}
				var err error
				select {
				case err = <-reported:
				case <-ctx.Done():
					t.Fatal("Store did not return its failure")
				}
				if err == nil || !owner.faulted.Load() {
					t.Fatal("fault did not reach the actual mutation")
				}
				if mode == "rollback-after-write" {
					if owner.commits.Load() != 0 || errors.Is(err, store.ErrUserAdministrationUnknown) {
						t.Fatal("pre-commit failure misclassified")
					}
				} else if owner.commits.Load() != 1 || !errors.Is(err, store.ErrUserAdministrationUnknown) {
					t.Fatalf("unknown commit replayed or misclassified: commits=%d", owner.commits.Load())
				}
				var audits int64
				if err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs").Scan(&audits); err != nil {
					t.Fatal(err)
				}
				if audits != before.audits {
					t.Fatal("failure emitted a success audit")
				}
				count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if mode == "commit-ack-lost" {
					if count != 1 {
						t.Fatalf("committed-but-unacknowledged state count=%d", count)
					}
					// A fresh independent request re-reads the canonical state; it cannot remove
					// the remaining administrator after the lost acknowledgement.
					peer := operationalLastAdminServer(t, raw, "la-manager")
					denied := operationalLastAdminRequest(ctx, t, peer, "unassign", "la-admin-b")
					if denied.Code != 409 {
						t.Fatalf("fresh last-admin request after unknown commit=%d", denied.Code)
					}
				} else {
					if count != 2 || !reflect.DeepEqual(before, operationalLastAdminCapture(t, db)) {
						t.Fatal("rollback did not preserve User/Role/Session/generation/history")
					}
				}
			})
		}
	}
}

func TestOperationalLastAdminSQLMigrationAndMissingGuard(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	operationalLastAdminSeed(t, db)
	before := operationalLastAdminCapture(t, db)
	for range 2 {
		if err := database.RunEmbeddedMigrations(t.Context(), db); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, operationalLastAdminCapture(t, db)) {
		t.Fatal("migration replay changed authorization source data")
	}
	var guards, migrations int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM user_admin_guard WHERE singleton=1").Scan(&guards); err != nil || guards != 1 {
		t.Fatalf("guard rows=%d err=%v", guards, err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil || migrations != 94 {
		t.Fatalf("migration rows=%d err=%v", migrations, err)
	}
	operationalLastAdminExec(t, db, "DELETE FROM user_admin_guard")
	for _, op := range operationalLastAdminOperations {
		t.Run(op, func(t *testing.T) {
			e := operationalLastAdminServer(t, raw, operationalLastAdminActor(op, "la-admin-a"))
			current := operationalLastAdminCapture(t, db)
			r := operationalLastAdminRequest(t.Context(), t, e, op, "la-admin-a")
			if r.Code >= 200 && r.Code < 300 {
				t.Fatal("missing guard became successful mutation")
			}
			if !reflect.DeepEqual(current, operationalLastAdminCapture(t, db)) {
				t.Fatal("missing guard changed authorization data")
			}
		})
	}
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM user_admin_guard").Scan(&guards); err != nil || guards != 0 {
		t.Fatal("runtime silently recreated the guard")
	}
}

func TestOperationalLastAdminSQLAuthorityLockOrder(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	if err := v3authorization.ApplyMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := v3authorization.ApplyAuthorityMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	// Model the exact existing C lock order on the shared base rows: authority
	// epoch -> User -> Session. No authority data or management grants are changed.
	// The unchanged C workflow separately runs its actual SQL-owner HTTP tests.
	for _, op := range operationalLastAdminOperations {
		t.Run(op, func(t *testing.T) {
			operationalLastAdminSeed(t, db)
			session, err := store.NewMariaDBAuthStore(db).CreateSession(t.Context(), "la-admin-a", time.Hour, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			authorityTx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer authorityTx.Rollback()
			// Hold the existing independent Authority epoch, then the common
			// User/Session rows in the current product's order.
			var generation uint64
			if err = authorityTx.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&generation); err != nil {
				t.Fatal(err)
			}
			var id string
			if err = authorityTx.QueryRowContext(ctx, "SELECT id FROM users WHERE id='la-admin-a' FOR UPDATE").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err = authorityTx.QueryRowContext(ctx, "SELECT user_id FROM sessions WHERE id=? FOR UPDATE", session.TokenHash).Scan(&id); err != nil {
				t.Fatal(err)
			}
			attempted := make(chan struct{})
			owner := &operationalLastAdminDriver{afterGuard: func() { close(attempted) }}
			e := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, owner), operationalLastAdminActor(op, "la-admin-a"))
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- operationalLastAdminRequest(ctx, t, e, op, "la-admin-a") }()
			select {
			case <-attempted:
			case r := <-done:
				t.Fatalf("guard was not reached HTTP=%d", r.Code)
			case <-ctx.Done():
				t.Fatal("guard attempt timed out")
			}
			// The last-admin transaction owns its guard but cannot pass the User lock.
			operationalLastAdminMustBeLocked(t, db, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE NOWAIT")
			if err = authorityTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			select {
			case r := <-done:
				if r.Code != 200 {
					t.Fatalf("shared User/Session lock path=%d %s", r.Code, r.Body.String())
				}
			case <-ctx.Done():
				t.Fatal("User/Session lock order did not progress")
			}
		})
	}
}
