package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

// This fixture can destroy only the explicitly named disposable CI schema.
func operationalLastAdminDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	raw := os.Getenv("AUTOSTREAM_S13_TEST_DSN")
	if raw == "" {
		t.Skip("disposable S13 database not configured")
	}
	cfg, err := mysql.ParseDSN(raw)
	if err != nil {
		t.Fatal("invalid disposable S13 DSN")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || cfg.DBName != "autostream_s13_ci" || cfg.User != "autostream_s13_ci" || cfg.Net != "tcp" || (cfg.Addr != "127.0.0.1:3306" && cfg.Addr != "localhost:3306") {
		t.Fatal("last-admin tests require their dedicated Actions loopback database")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		if _, err = conn.ExecContext(ctx, "DROP TABLE "+string(rune(96))+strings.ReplaceAll(name, string(rune(96)), strings.Repeat(string(rune(96)), 2))+string(rune(96))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
		t.Fatal(err)
	}
	if err = database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db, raw
}

func operationalLastAdminExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func operationalLastAdminSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"sessions", "user_roles", "role_permissions", "users", "roles"} {
		operationalLastAdminExec(t, db, "DELETE FROM "+table)
	}
	operationalLastAdminExec(t, db, "INSERT INTO roles(id,name,created_at) VALUES ('la-super','super_admin',UTC_TIMESTAMP()),('la-custom','la-custom',UTC_TIMESTAMP())")
	for _, op := range security.DefaultPermissions {
		operationalLastAdminExec(t, db, "INSERT INTO role_permissions(role_id,permission) VALUES ('la-super',?)", op)
	}
	for _, op := range []string{"users.update", "roles.assign", "roles.read", "users.read"} {
		operationalLastAdminExec(t, db, "INSERT INTO role_permissions(role_id,permission) VALUES ('la-custom',?)", op)
	}
	for _, id := range []string{"la-admin-a", "la-admin-b", "la-manager"} {
		operationalLastAdminExec(t, db, "INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES (?,?,'synthetic-noncredential','active',UTC_TIMESTAMP(),UTC_TIMESTAMP())", id, id)
		role := "la-super"
		if id == "la-manager" {
			role = "la-custom"
		}
		operationalLastAdminExec(t, db, "INSERT INTO user_roles(user_id,role_id) VALUES (?,?)", id, role)
	}
}

type operationalLastAdminEndpoint struct {
	db         *sql.DB
	server     *Server
	cookie     *http.Cookie
	csrf       string
	connection int64
}

func operationalLastAdminServer(t *testing.T, raw, actor string) operationalLastAdminEndpoint {
	t.Helper()
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	// One stable physical connection per independent Store/Server, not one shared
	// process mutex or connection. The observer and fixture use a third pool.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	auth := store.NewMariaDBAuthStore(db)
	session, err := auth.CreateSession(t.Context(), actor, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = db.QueryRowContext(t.Context(), "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return operationalLastAdminEndpoint{db: db, server: NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(auth)), cookie: &http.Cookie{Name: sessionCookieName, Value: session.Token}, csrf: session.CSRFToken, connection: id}
}

type operationalLastAdminCountStore struct {
	store.MariaDBAuthStore
	arrived chan<- int
	release <-chan struct{}
}

func (s operationalLastAdminCountStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	count, err := s.MariaDBAuthStore.CountActiveSuperAdmins(ctx)
	if err != nil {
		return count, err
	}
	select {
	case s.arrived <- count:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case <-s.release:
		return count, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestOperationalLastAdminSQLConcurrentRemoval(t *testing.T) {
	db, raw := operationalLastAdminDatabase(t)
	operationalLastAdminSeed(t, db)
	first := operationalLastAdminServer(t, raw, "la-manager")
	second := operationalLastAdminServer(t, raw, "la-manager")
	if first.connection == second.connection {
		t.Fatal("fixture reused a physical database connection")
	}
	arrived := make(chan int, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	for _, endpoint := range []operationalLastAdminEndpoint{first, second} {
		auth := store.NewMariaDBAuthStore(endpoint.db)
		endpoint.server.users = operationalLastAdminCountStore{MariaDBAuthStore: auth, arrived: arrived, release: release}
		probe := operationalRequest(t, endpoint.server, endpoint.cookie, endpoint.csrf, "GET", "/users/la-admin-a", "")
		if probe.Code != 200 {
			t.Fatalf("SQL fixture HTTP read: %d %s", probe.Code, probe.Body.String())
		}
	}
	type result struct {
		status int
		body   string
	}
	done := make(chan result, 2)
	for i, endpoint := range []operationalLastAdminEndpoint{first, second} {
		target := fmt.Sprintf("la-admin-%c", 'a'+i)
		go func(e operationalLastAdminEndpoint, target string) {
			res := operationalRequest(t, e.server, e.cookie, e.csrf, "PUT", "/users/"+target, "{\"role_ids\":[]}")
			done <- result{res.Code, res.Body.String()}
		}(endpoint, target)
	}
	for range 2 {
		select {
		case count := <-arrived:
			if count != 2 {
				t.Fatalf("controlled pre-update SQL count=%d want=2", count)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("both real HTTP prechecks did not complete")
		}
	}
	t.Log("two independent Server/Store/connection HTTP prechecks read active super_admin=2 before either mutation")
	unblock()
	results := []result{<-done, <-done}
	count, err := store.NewMariaDBAuthStore(db).CountActiveSuperAdmins(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HTTP statuses=%d,%d; active super_admin=%d", results[0].status, results[1].status, count)
	if count < 1 {
		t.Fatalf("concurrent management requests removed every active super_admin (HTTP %d,%d)", results[0].status, results[1].status)
	}
	for _, r := range results {
		if r.status != 200 && r.status != 409 {
			t.Fatalf("unexpected mutation result: %d %s", r.status, r.body)
		}
	}
}
