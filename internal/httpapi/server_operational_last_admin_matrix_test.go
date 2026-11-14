package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

var operationalLastAdminOperations = []string{"disable", "lock", "delete", "unassign", "force-password-change", "reset-password"}

func operationalLastAdminServerDB(t *testing.T, db *sql.DB, actor string) operationalLastAdminEndpoint {
	t.Helper()
	auth := store.NewMariaDBAuthStore(db)
	session, err := auth.CreateSession(t.Context(), actor, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = db.QueryRowContext(t.Context(), "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return operationalLastAdminEndpoint{db: db, server: NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(store.NewMariaDBAuditStore(db)), WithOperationalAuthorization(auth)), cookie: &http.Cookie{Name: sessionCookieName, Value: session.Token}, csrf: session.CSRFToken, connection: id}
}
func operationalLastAdminActor(operation, target string) string {
	if operation == "unassign" {
		return "la-manager"
	}
	if target == "la-admin-a" {
		return "la-admin-b"
	}
	return "la-admin-a"
}
func operationalLastAdminRequest(ctx context.Context, t *testing.T, e operationalLastAdminEndpoint, operation, target string) *httptest.ResponseRecorder {
	t.Helper()
	method, path, body := "POST", "/users/"+target+"/"+operation, "{}"
	switch operation {
	case "unassign":
		method, path, body = "PUT", "/users/"+target, "{\"role_ids\":[]}"
	case "delete":
		method, path = "DELETE", "/users/"+target
	case "reset-password":
		body = "{\"temporary_password\":\"Synthetic-Reset-Pass-912!\"}"
	case "add":
		method, path, body = "PUT", "/users/"+target, "{\"role_ids\":[\"la-super\"]}"
	}
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	req.AddCookie(e.cookie)
	req.Header.Set("X-CSRF-Token", e.csrf)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	e.server.ServeHTTP(res, req)
	return res
}

type operationalLastAdminEntryStore struct {
	store.MariaDBAuthStore
	arrived chan<- struct{}
	release <-chan struct{}
}

func (s operationalLastAdminEntryStore) wait(ctx context.Context) error {
	select {
	case s.arrived <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s operationalLastAdminEntryStore) SetUserStatus(ctx context.Context, id, state string) (store.User, error) {
	if e := s.wait(ctx); e != nil {
		return store.User{}, e
	}
	return s.MariaDBAuthStore.SetUserStatus(ctx, id, state)
}
func (s operationalLastAdminEntryStore) DeleteUser(ctx context.Context, id string) error {
	if e := s.wait(ctx); e != nil {
		return e
	}
	return s.MariaDBAuthStore.DeleteUser(ctx, id)
}
func (s operationalLastAdminEntryStore) UpdateUser(ctx context.Context, id string, p store.UserPatch) (store.User, error) {
	if e := s.wait(ctx); e != nil {
		return store.User{}, e
	}
	return s.MariaDBAuthStore.UpdateUser(ctx, id, p)
}
func (s operationalLastAdminEntryStore) ResetPassword(ctx context.Context, id, password string) error {
	if e := s.wait(ctx); e != nil {
		return e
	}
	return s.MariaDBAuthStore.ResetPassword(ctx, id, password)
}

func TestOperationalLastAdminSQLMutationMatrix(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	for _, same := range []bool{false, true} {
		for i, firstOp := range operationalLastAdminOperations {
			for _, secondOp := range operationalLastAdminOperations[i:] {
				t.Run(fmt.Sprintf("same=%v/%s+%s", same, firstOp, secondOp), func(t *testing.T) {
					operationalLastAdminSeed(t, db)
					targets := []string{"la-admin-a", "la-admin-b"}
					if same {
						targets[1] = targets[0]
					}
					ops := []string{firstOp, secondOp}
					arrived, release := make(chan struct{}, 2), make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					defer unblock()
					done := make(chan *httptest.ResponseRecorder, 2)
					ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
					defer cancel()
					endpoints := make([]operationalLastAdminEndpoint, 2)
					for j := range endpoints {
						e := operationalLastAdminServer(t, raw, operationalLastAdminActor(ops[j], targets[j]))
						e.server.users = operationalLastAdminEntryStore{MariaDBAuthStore: store.NewMariaDBAuthStore(e.db), arrived: arrived, release: release}
						endpoints[j] = e
						go func(e operationalLastAdminEndpoint, op, target string) {
							done <- operationalLastAdminRequest(ctx, t, e, op, target)
						}(e, ops[j], targets[j])
					}
					if endpoints[0].connection == endpoints[1].connection {
						t.Fatal("connection isolation lost")
					}
					for range 2 {
						select {
						case <-arrived:
						case r := <-done:
							t.Fatalf("request rejected before Store barrier: %d %s", r.Code, r.Body.String())
						case <-ctx.Done():
							t.Fatal("Store entry barrier not reached")
						}
					}
					unblock()
					successes, conflicts := 0, 0
					for range 2 {
						select {
						case r := <-done:
							if r.Code == 200 {
								successes++
							} else if r.Code == 409 {
								conflicts++
							} else if !(same && r.Code == 404) {
								t.Fatalf("unexpected HTTP=%d %s", r.Code, r.Body.String())
							}
						case <-ctx.Done():
							t.Fatal("competing SQL mutations did not finish")
						}
					}
					count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if count != 1 || successes < 1 || (!same && (successes != 1 || conflicts != 1)) {
						t.Fatalf("count=%d successes=%d conflicts=%d", count, successes, conflicts)
					}
				})
			}
		}
	}
}

func TestOperationalLastAdminSQLSingleAndAbnormalState(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	for _, op := range operationalLastAdminOperations {
		t.Run(op, func(t *testing.T) {
			operationalLastAdminSeed(t, db)
			operationalLastAdminExec(t, db, "DELETE FROM user_roles WHERE user_id='la-admin-b'")
			actor := "la-admin-a"
			if op == "unassign" {
				actor = "la-manager"
			}
			e := operationalLastAdminServer(t, raw, actor)
			before, err := store.NewMariaDBAuthStore(db).GetUser(t.Context(), "la-admin-a")
			if err != nil {
				t.Fatal(err)
			}
			r := operationalLastAdminRequest(t.Context(), t, e, op, "la-admin-a")
			if r.Code != 409 {
				t.Fatalf("sole administrator HTTP=%d %s", r.Code, r.Body.String())
			}
			after, err := store.NewMariaDBAuthStore(db).GetUser(t.Context(), "la-admin-a")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("sole administrator was changed")
			}
			// Deletion has an earlier self-delete HTTP protection; verify the actual
			// MariaDB Store also protects the invariant independently.
			auth := store.NewMariaDBAuthStore(e.db)
			if op == "delete" && !errors.Is(auth.DeleteUser(t.Context(), "la-admin-a"), store.ErrLastSuperAdmin) {
				t.Fatal("SQL deletion lacked last-admin protection")
			}
			if op == "unassign" {
				_, err := auth.UpdateUser(t.Context(), "la-admin-a", store.UserPatch{RoleIDs: []string{}})
				if !errors.Is(err, store.ErrLastSuperAdmin) {
					t.Fatal("SQL assignment update lacked protection")
				}
			}
		})
	}
	t.Run("zero-is-not-repaired", func(t *testing.T) {
		operationalLastAdminSeed(t, db)
		operationalLastAdminExec(t, db, "DELETE FROM user_roles WHERE role_id='la-super'")
		e := operationalLastAdminServer(t, raw, "la-manager")
		r := operationalRequest(t, e.server, e.cookie, e.csrf, "PUT", "/users/la-admin-a", "{\"username\":\"la-renamed\"}")
		if r.Code != 200 {
			t.Fatalf("unrelated profile update=%d", r.Code)
		}
		count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
		if err != nil || count != 0 {
			t.Fatalf("zero state was auto-repaired count=%d err=%v", count, err)
		}
	})
}

func operationalLastAdminMustBeLocked(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, args...)
	if err == nil {
		rows.Close()
		t.Fatal("protected transaction did not retain the expected DB row lock")
	}
	var me *mysql.MySQLError
	if !errors.As(err, &me) || (me.Number != 1205 && me.Number != 3572) {
		t.Fatalf("NOWAIT observation failed: %v", err)
	}
}

func TestOperationalLastAdminSQLSharedGuardAndIsolation(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	for _, op := range operationalLastAdminOperations {
		t.Run(op, func(t *testing.T) {
			operationalLastAdminSeed(t, db)
			paused, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			owner := &operationalLastAdminDriver{beforeEffect: func() { close(paused); <-release }}
			e := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, owner), operationalLastAdminActor(op, "la-admin-a"))
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- operationalLastAdminRequest(ctx, t, e, op, "la-admin-a") }()
			select {
			case <-paused:
			case r := <-done:
				t.Fatalf("mutation did not reach SQL effect: %d %s", r.Code, r.Body.String())
			case <-ctx.Done():
				t.Fatal("effect barrier not reached")
			}
			// These queries use an independent DB connection; a process mutex could not
			// make either NOWAIT probe fail. Both locks must span the actual mutation.
			operationalLastAdminMustBeLocked(t, db, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE NOWAIT")
			operationalLastAdminMustBeLocked(t, db, "SELECT id FROM users WHERE id='la-admin-a' FOR UPDATE NOWAIT")
			// Unrelated profile and Role operations do not acquire the shared guard.
			unrelated := store.NewMariaDBAuthStore(db)
			if _, err := unrelated.UpdateUser(ctx, "la-manager", store.UserPatch{Username: "la-manager-renamed"}); err != nil {
				t.Fatal("unrelated profile operation blocked: ", err)
			}
			role, err := unrelated.CreateRole(ctx, "la-independent", []string{"streams.read"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = unrelated.UpdateRole(ctx, role.ID, "la-independent-renamed", []string{"streams.read"}); err != nil {
				t.Fatal(err)
			}
			if err = unrelated.DeleteRole(ctx, role.ID); err != nil {
				t.Fatal(err)
			}
			unblock()
			select {
			case r := <-done:
				if r.Code != 200 {
					t.Fatalf("protected mutation=%d %s", r.Code, r.Body.String())
				}
			case <-ctx.Done():
				t.Fatal("protected mutation did not finish")
			}
			count, err := unrelated.CountActiveSuperAdmins(t.Context())
			if err != nil || count != 1 {
				t.Fatalf("post-mutation count=%d err=%v", count, err)
			}
		})
	}
}

func TestOperationalLastAdminSQLAdditionRemovalOrder(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	for _, additionFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("addition-first=%v", additionFirst), func(t *testing.T) {
			operationalLastAdminSeed(t, db)
			operationalLastAdminExec(t, db, "DELETE FROM user_roles WHERE user_id='la-admin-b'")
			operationalLastAdminExec(t, db, "INSERT INTO user_roles(user_id,role_id) VALUES ('la-admin-b','la-custom')")
			acquired, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			firstOwner := &operationalLastAdminDriver{afterGuard: func() { close(acquired); <-release }}
			first := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, firstOwner), "la-admin-a")
			attempted := make(chan struct{})
			secondOwner := &operationalLastAdminDriver{beforeGuard: func() { close(attempted) }}
			second := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, secondOwner), "la-admin-a")
			if first.connection == second.connection {
				t.Fatal("shared physical connection")
			}
			firstOp, firstTarget, secondOp, secondTarget := "add", "la-admin-b", "disable", "la-admin-a"
			if !additionFirst {
				firstOp, firstTarget, secondOp, secondTarget = secondOp, secondTarget, firstOp, firstTarget
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			doneFirst, doneSecond := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			go func() { doneFirst <- operationalLastAdminRequest(ctx, t, first, firstOp, firstTarget) }()
			select {
			case <-acquired:
			case r := <-doneFirst:
				t.Fatalf("first guard not acquired HTTP=%d", r.Code)
			case <-ctx.Done():
				t.Fatal("guard acquisition timed out")
			}
			operationalLastAdminMustBeLocked(t, db, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE NOWAIT")
			go func() { doneSecond <- operationalLastAdminRequest(ctx, t, second, secondOp, secondTarget) }()
			select {
			case <-attempted:
			case r := <-doneSecond:
				t.Fatalf("second guard not attempted HTTP=%d %s", r.Code, r.Body.String())
			case <-ctx.Done():
				t.Fatal("second guard attempt timed out")
			}
			unblock()
			var firstResult, secondResult *httptest.ResponseRecorder
			select {
			case firstResult = <-doneFirst:
			case <-ctx.Done():
				t.Fatal("first mutation timed out")
			}
			select {
			case secondResult = <-doneSecond:
			case <-ctx.Done():
				t.Fatal("second mutation timed out")
			}
			if additionFirst {
				if firstResult.Code != 200 || secondResult.Code != 200 {
					t.Fatalf("add then remove=%d,%d", firstResult.Code, secondResult.Code)
				}
			} else {
				if firstResult.Code != 409 || secondResult.Code != 200 {
					t.Fatalf("remove then add=%d,%d", firstResult.Code, secondResult.Code)
				}
			}
			want := 1
			if !additionFirst {
				want = 2
			}
			count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
			if err != nil || count != want {
				t.Fatalf("count=%d want=%d err=%v", count, want, err)
			}
		})
	}
}

func TestOperationalLastAdminSQLAuthenticationLockoutOrder(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	t.Run("lockout-before-management", func(t *testing.T) {
		operationalLastAdminSeed(t, db)
		acquired, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		authOwner := &operationalLastAdminDriver{afterGuard: func() { close(acquired); <-release }}
		authDB := operationalLastAdminWrappedDB(t, raw, authOwner)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		authDone := make(chan error, 1)
		go func() { authDone <- store.NewMariaDBAuthStore(authDB).RecordLoginFailure(ctx, "la-admin-b", 1) }()
		select {
		case <-acquired:
		case err := <-authDone:
			t.Fatalf("automatic lockout missed shared guard: %v", err)
		case <-ctx.Done():
			t.Fatal("lockout guard timed out")
		}
		operationalLastAdminMustBeLocked(t, db, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE NOWAIT")
		attempted := make(chan struct{})
		owner := &operationalLastAdminDriver{beforeGuard: func() { close(attempted) }}
		manager := operationalLastAdminServerDB(t, operationalLastAdminWrappedDB(t, raw, owner), "la-manager")
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- operationalLastAdminRequest(ctx, t, manager, "unassign", "la-admin-a") }()
		select {
		case <-attempted:
		case r := <-done:
			t.Fatalf("management did not attempt the shared guard: %d", r.Code)
		case <-ctx.Done():
			t.Fatal("management guard attempt timed out")
		}
		unblock()
		select {
		case err := <-authDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("automatic lockout did not finish")
		}
		select {
		case r := <-done:
			if r.Code != 409 {
				t.Fatalf("stale count after lockout accepted: %d %s", r.Code, r.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("management did not finish")
		}
		count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
		if err != nil || count != 1 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	})
	t.Run("existing-authentication-policy-preserved", func(t *testing.T) {
		operationalLastAdminSeed(t, db)
		operationalLastAdminExec(t, db, "DELETE FROM user_roles WHERE user_id='la-admin-a'")
		auth := store.NewMariaDBAuthStore(db)
		if err := auth.RecordLoginFailure(t.Context(), "la-admin-b", 1); err != nil {
			t.Fatal(err)
		}
		user, err := auth.GetUser(t.Context(), "la-admin-b")
		if err != nil || user.Status != "locked" {
			t.Fatal("existing authentication lockout policy was changed")
		}
		count, err := auth.CountActiveSuperAdmins(t.Context())
		if err != nil || count != 0 {
			t.Fatal("automatic lockout was silently refused or recovered")
		}
		// This is an authentication policy event, not a management removal. No
		// availability claim or automatic recovery is added for this pre-existing case.
	})
}
