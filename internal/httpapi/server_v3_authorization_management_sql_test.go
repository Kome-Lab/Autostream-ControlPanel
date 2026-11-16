//go:build v3_management_http_sql

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/v3authorization"
	"github.com/go-sql-driver/mysql"
)

// Transport identity and the independent bootstrap origin are synthetic.
// Handler, session recheck, ManagementService, SQLAuthority.Save and audit are
// the product path on an explicitly disposable MariaDB schema.
type v3ManagementSQLBootstrap struct{}

func (v3ManagementSQLBootstrap) AuthorizeBootstrap(_ context.Context, actor v3authorization.ManagementActor, _ v3authorization.ManagementMetadata, _ v3authorization.ManagementGrant) error {
	if actor.SessionHash != "" {
		return v3authorization.ErrDenied
	}
	return nil
}

type v3ManagementSQLTransport struct {
	*v3ManagementHTTPSession
	db   *sql.DB
	mode string
	once sync.Once
}

func (s *v3ManagementSQLTransport) GetUser(ctx context.Context, id string) (store.User, error) {
	user, err := s.v3ManagementHTTPSession.GetUser(ctx, id)
	if err != nil {
		return user, err
	}
	var changeErr error
	s.once.Do(func() {
		switch s.mode {
		case "session-deleted":
			_, changeErr = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", security.HashToken("synthetic-session"))
		case "user-disabled":
			_, changeErr = s.db.ExecContext(ctx, "UPDATE users SET status='disabled' WHERE id=?", user.ID)
		}
	})
	return user, changeErr
}

func v3ManagementSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN")
	config, err := mysql.ParseDSN(raw)
	if raw == "" || err != nil || config.DBName != "autostream_v3_auth_ci" {
		t.Fatal("tagged HTTP SQL tests require explicit disposable autostream_v3_auth_ci")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, table := range []string{
		"v3_cp_management_grant_history", "v3_cp_management_audit", "v3_cp_management_grants", "v3_cp_management_bootstrap", "v3_cp_management_schema",
		"v3_cp_authority_history", "v3_cp_authority_records", "v3_cp_authority_epoch", "v3_cp_authority_schema",
		"v3_cp_authorization_issuances", "v3_cp_authorization_schema", "sessions", "users",
	} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"CREATE TABLE users(id CHAR(36) PRIMARY KEY,status VARCHAR(32) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE sessions(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL,idle_expires_at DATETIME(6) NOT NULL,absolute_expires_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"INSERT INTO users VALUES('bootstrap-user','active'),('synthetic-user','active')",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][2]string{{strings.Repeat("b", 64), "bootstrap-user"}, {security.HashToken("synthetic-session"), "synthetic-user"}} {
		if _, err := db.Exec("INSERT INTO sessions VALUES(?,?,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, migrate := range []func(context.Context, *sql.DB) error{v3authorization.ApplyMigrations, v3authorization.ApplyAuthorityMigrations, v3authorization.ApplyManagementMigrations} {
		if err := migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestV3AuthorizationManagementHTTPSQL(t *testing.T) {
	for _, name := range []string{"applied-and-inspected", "v2-admin-only", "other-realm", "revision-conflict", "session-deleted", "user-disabled", "administrator-revoked", "uninitialized"} {
		t.Run(name, func(t *testing.T) {
			db := v3ManagementSQLDB(t)
			authority := v3authorization.NewSQLAuthority(db)
			manager := v3authorization.NewManagementService(authority, v3ManagementSQLBootstrap{})
			ctx := context.Background()
			scope := v3ManagementHTTPScope()
			generation := func() uint64 {
				t.Helper()
				value, err := authority.Generation(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			metadata := func(event string) v3authorization.ManagementMetadata {
				return v3authorization.ManagementMetadata{EventID: event, CorrelationID: event, ExpectedRevision: generation(), Scope: scope, Action: v3authorization.Action}
			}
			ops := []v3authorization.ManagementOperation{v3authorization.ManagementRead, v3authorization.ManagementRealm, v3authorization.ManagementAdministrator, v3authorization.ManagementDelegateSubjects}
			root := v3authorization.ManagementActor{Principal: v3authorization.Principal{ID: "bootstrap-user"}, SessionHash: strings.Repeat("b", 64)}
			rootGrant := v3authorization.ManagementGrant{Principal: root.Principal.ID, Scope: scope, Action: v3authorization.Action, State: "ACTIVE", Operations: ops, Subjects: []string{root.Principal.ID}}
			grant := v3authorization.ManagementGrant{Principal: "synthetic-user", Scope: scope, Action: v3authorization.Action, State: "ACTIVE",
				Operations: []v3authorization.ManagementOperation{v3authorization.ManagementRead, v3authorization.ManagementRealm}, Subjects: []string{"synthetic-user"}}
			if name != "uninitialized" {
				if result := manager.Bootstrap(ctx, root, metadata("bootstrap"), rootGrant); result.Status != v3authorization.ManagementApplied {
					t.Fatal("synthetic trust origin failed", result)
				}
				if name != "v2-admin-only" {
					if result := manager.Apply(ctx, root, metadata("grant"), nil, []v3authorization.ManagementGrant{grant}); result.Status != v3authorization.ManagementApplied {
						t.Fatal("explicit scoped management grant failed", result)
					}
				}
			}
			if name == "administrator-revoked" {
				grant.State = "REVOKED"
				if result := manager.Apply(ctx, root, metadata("revoke"), nil, []v3authorization.ManagementGrant{grant}); result.Status != v3authorization.ManagementApplied {
					t.Fatal(result)
				}
			}
			meta := metadata("http-management")
			if name == "other-realm" {
				meta.Scope.Realm = "another-realm"
			}
			if name == "revision-conflict" {
				meta.ExpectedRevision++
			}
			body := map[string]any{"event_id": meta.EventID, "correlation_id": meta.CorrelationID, "expected_revision": meta.ExpectedRevision,
				"scope": meta.Scope, "action": meta.Action, "records": []v3authorization.RealmRegistration{{Realm: scope.Realm, State: "ACTIVE"}}}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			transport := &v3ManagementSQLTransport{v3ManagementHTTPSession: &v3ManagementHTTPSession{status: "active", role: "super_admin"}, db: db, mode: name}
			handler := (&Server{auth: transport}).V3AuthorityManagementHandler(manager)
			before := generation()
			want := http.StatusForbidden
			switch name {
			case "applied-and-inspected":
				want = http.StatusOK
			case "revision-conflict":
				want = http.StatusConflict
			case "uninitialized":
				want = http.StatusServiceUnavailable
			}
			v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/realms", string(raw)), want)
			if name == "applied-and-inspected" {
				if generation() != before+1 {
					t.Fatal("HTTP update did not advance the same authority epoch")
				}
				meta = metadata("inspect")
				raw, err = json.Marshal(meta)
				if err != nil {
					t.Fatal(err)
				}
				response := v3ManagementHTTPResponse(t, handler, v3ManagementHTTPRequest(http.MethodPost, "/v3/authority-management/inspect", string(raw)), http.StatusOK)
				if !strings.Contains(response.Body.String(), scope.Realm) || generation() != before+1 {
					t.Fatal("HTTP inspection lost registered state or changed authority")
				}
			} else if generation() != before {
				t.Fatal("HTTP rejection changed authority")
			}
			var audits int
			if err := db.QueryRow("SELECT COUNT(*) FROM v3_cp_management_audit WHERE event_id=?", meta.EventID).Scan(&audits); err != nil || audits == 0 {
				t.Fatal("real HTTP management result was not audited", err)
			}
		})
	}
}
