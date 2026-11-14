package store

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/go-sql-driver/mysql"
)

// This helper is limited to the schema created by this workflow's disposable
// MariaDB service. A DSN alone never authorizes running it on a developer DB.
func operationalSQLFixture(t *testing.T) *sql.DB {
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
		t.Fatal("S13 SQL tests require their dedicated GitHub Actions loopback schema")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	operationalSQLReset(t, db)
	for _, statement := range []string{
		`Create TABLE users (id CHAR(36) NOT NULL PRIMARY KEY,status VARCHAR(32) NOT NULL,password_hash VARCHAR(255) NOT NULL DEFAULT '') ENGINE=InnoDB`,
		`Create TABLE roles (id CHAR(36) NOT NULL PRIMARY KEY,name VARCHAR(128) NOT NULL UNIQUE) ENGINE=InnoDB`,
		`Create TABLE role_permissions (role_id CHAR(36) NOT NULL,permission VARCHAR(160) NOT NULL,PRIMARY KEY(role_id,permission),FOREIGN KEY(role_id) REFERENCES roles(id)) ENGINE=InnoDB`,
		`Create TABLE user_roles (user_id CHAR(36) NOT NULL,role_id CHAR(36) NOT NULL,PRIMARY KEY(user_id,role_id),FOREIGN KEY(user_id) REFERENCES users(id),FOREIGN KEY(role_id) REFERENCES roles(id)) ENGINE=InnoDB`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func operationalSQLReset(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=1")
	for _, name := range tables {
		if _, err := conn.ExecContext(ctx, "DROP TABLE "+string(rune(96))+strings.ReplaceAll(name, string(rune(96)), strings.Repeat(string(rune(96)), 2))+string(rune(96))); err != nil {
			t.Fatal(err)
		}
	}
}
func operationalSQLExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func operationalSQLSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	operationalSQLExec(t, db, `INSERT INTO users(id,status) VALUES ('human-one','active'),('human-two','active')`)
	operationalSQLExec(t, db, `INSERT INTO roles(id,name) VALUES ('role-one','super_admin'),('role-two','custom')`)
	operationalSQLExec(t, db, `INSERT INTO user_roles(user_id,role_id) VALUES ('human-one','role-one'),('human-two','role-two')`)
}

func TestOperationalSQLSource(t *testing.T) {
	db := operationalSQLFixture(t)
	ctx := t.Context()
	operationalSQLSeed(t, db)
	auth := NewMariaDBAuthStore(db)
	if permissions, err := auth.GetUserPermissions(ctx, "human-one"); err != nil || len(permissions) != 0 {
		t.Fatalf("legacy source before migration: %v %v", permissions, err)
	}
	if _, err := auth.Snapshot(ctx, "human-one"); err == nil {
		t.Fatal("opt-in source accepted missing schema")
	}
	if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	empty, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Grants) != 0 {
		t.Fatal("super_admin name granted without contents")
	}
	for _, permission := range security.DefaultPermissions {
		operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-one',?)`, permission)
	}
	snapshot, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Grants) != 99 || len(snapshot.Roles) != 1 || snapshot.Roles[0].ID != "role-one" || snapshot.Roles[0].Name != "super_admin" {
		t.Fatalf("role expansion: roles=%d grants=%d", len(snapshot.Roles), len(snapshot.Grants))
	}
	var expanded []string
	for _, grant := range snapshot.Grants {
		if grant.PrincipalID != "human-one" || grant.RoleID != "role-one" || string(grant.Scope.Type) != "global" || grant.Scope.ID != "" || grant.Revision != snapshot.Revision {
			t.Fatal("invalid projected grant")
		}
		expanded = append(expanded, grant.Operation)
	}
	legacy, err := auth.GetUserPermissions(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(legacy)
	sort.Strings(expanded)
	if strings.Join(legacy, "\n") != strings.Join(expanded, "\n") {
		t.Fatal("v2/v3 effective permission mismatch")
	}
	operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','streams.read')`)
	operationalSQLExec(t, db, `INSERT INTO user_roles(user_id,role_id) VALUES ('human-one','role-two')`)
	union, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(union.Roles) != 2 || len(union.Grants) != 100 {
		t.Fatal("multiple-role expansion lost")
	}
	operationalSQLExec(t, db, `DELETE FROM role_permissions WHERE role_id='role-one' AND permission='streams.read'`)
	remaining, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, grant := range remaining.Grants {
		if grant.Operation == "streams.read" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("surviving custom role permission lost")
	}
	operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','trust_root.change')`)
	if _, err := auth.Snapshot(ctx, "human-one"); err == nil {
		t.Fatal("unknown authority accepted")
	}
}

func TestOperationalSQLMigration(t *testing.T) {
	t.Run("replay-preserves-source-and-history", func(t *testing.T) {
		db := operationalSQLFixture(t)
		ctx := t.Context()
		operationalSQLSeed(t, db)
		operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','streams.read')`)
		if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
			t.Fatal(err)
		}
		auth := NewMariaDBAuthStore(db)
		before, err := auth.Snapshot(ctx, "human-two")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		after, err := auth.Snapshot(ctx, "human-two")
		if err != nil {
			t.Fatal(err)
		}
		if before.Revision != after.Revision || len(after.Grants) != 1 || after.Grants[0].ID != before.Grants[0].ID {
			t.Fatal("migration replay changed grant/revision")
		}
		if _, err := db.ExecContext(ctx, `UPDATE operational_authorization_history SET source_event='tampered'`); err == nil {
			t.Fatal("history UPDATE accepted")
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM operational_authorization_history`); err == nil {
			t.Fatal("history DELETE accepted")
		}
	})
	t.Run("dirty-checksum-and-missing-trigger-refuse", func(t *testing.T) {
		for _, change := range []string{`UPDATE operational_authorization_migrations SET dirty=TRUE WHERE singleton=1`, `UPDATE operational_authorization_migrations SET checksum=REPEAT('0',64) WHERE singleton=1`, `DROP TRIGGER s13oa_user_roles_ad`} {
			db := operationalSQLFixture(t)
			ctx := t.Context()
			if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
				t.Fatal(err)
			}
			operationalSQLExec(t, db, change)
			if err := oa.ApplyOperationalMigrations(ctx, db); err == nil {
				t.Fatal("migration silently repaired unknown state")
			}
			if _, err := NewMariaDBAuthStore(db).Revision(ctx); err == nil {
				t.Fatal("inconsistent schema accepted")
			}
		}
	})
	t.Run("partial-ddl-remains-dirty", func(t *testing.T) {
		db := operationalSQLFixture(t)
		ctx := t.Context()
		operationalSQLExec(t, db, `DROP TABLE user_roles`)
		operationalSQLExec(t, db, `DROP TABLE users`)
		if err := oa.ApplyOperationalMigrations(ctx, db); err == nil {
			t.Fatal("incomplete source schema accepted")
		}
		var dirty bool
		if err := db.QueryRowContext(ctx, `SELECT dirty FROM operational_authorization_migrations WHERE singleton=1`).Scan(&dirty); err != nil || !dirty {
			t.Fatalf("partial install lost dirty marker: %v", err)
		}
		operationalSQLExec(t, db, `CREATE TABLE users(id CHAR(36) NOT NULL PRIMARY KEY,status VARCHAR(32) NOT NULL) ENGINE=InnoDB`)
		if err := oa.ApplyOperationalMigrations(ctx, db); err == nil {
			t.Fatal("dirty install automatically retried")
		}
	})
	t.Run("invalid-source-engine-remains-dirty", func(t *testing.T) {
		db := operationalSQLFixture(t)
		ctx := t.Context()
		operationalSQLExec(t, db, "DROP TABLE user_roles")
		operationalSQLExec(t, db, "ALTER TABLE users ENGINE=MyISAM")
		operationalSQLExec(t, db, "CREATE TABLE user_roles(user_id CHAR(36),role_id CHAR(36),PRIMARY KEY(user_id,role_id)) ENGINE=InnoDB")
		if err := oa.ApplyOperationalMigrations(ctx, db); err == nil {
			t.Fatal("nontransactional source accepted")
		}
		var dirty bool
		if err := db.QueryRowContext(ctx, "SELECT dirty FROM operational_authorization_migrations WHERE singleton=1").Scan(&dirty); err != nil || !dirty {
			t.Fatal("failed final validation published a clean migration")
		}
		if _, err := NewMariaDBAuthStore(db).Revision(ctx); err == nil {
			t.Fatal("invalid source enabled authorization")
		}
	})
	t.Run("concurrent-installer-single-commit", func(t *testing.T) {
		db := operationalSQLFixture(t)
		ctx := t.Context()
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { results <- oa.ApplyOperationalMigrations(ctx, db) }()
		}
		for i := 0; i < 2; i++ {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		revision, err := NewMariaDBAuthStore(db).Revision(ctx)
		if err != nil || revision != 1 {
			t.Fatalf("concurrent migration: %d %v", revision, err)
		}
	})
}

func TestOperationalSQLMutationRevocation(t *testing.T) {
	db := operationalSQLFixture(t)
	ctx := t.Context()
	operationalSQLSeed(t, db)
	if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	auth := NewMariaDBAuthStore(db)
	operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','streams.read')`)
	before, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	operationalSQLExec(t, db, `DELETE FROM role_permissions WHERE role_id='role-two' AND permission='streams.read'`)
	revoked, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.Grants) != 0 || revoked.Revision <= before.Revision {
		t.Fatal("revocation did not invalidate projection")
	}
	operationalSQLExec(t, db, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','streams.read')`)
	restored, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision <= revoked.Revision || restored.Grants[0].ID != before.Grants[0].ID {
		t.Fatal("ABA reused generation")
	}
	result, err := db.ExecContext(ctx, `UPDATE operational_authorization_epoch SET generation=generation+1 WHERE singleton=1 AND generation=?`, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 0 {
		t.Fatal("stale generation CAS accepted")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions(role_id,permission) VALUES ('role-two','streams.start')`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, blockedErr := auth.Snapshot(blocked, "human-two")
	cancel()
	if blockedErr == nil {
		tx.Rollback()
		t.Fatal("snapshot crossed uncommitted source generation")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Revision != restored.Revision || len(rolledBack.Grants) != 1 {
		t.Fatal("rollback retained source/epoch/history changes")
	}
	operationalSQLExec(t, db, `UPDATE roles SET name=name WHERE id='role-two'`)
	identical, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	if identical.Revision <= rolledBack.Revision {
		t.Fatal("same-value mutation did not advance revision")
	}
	operationalSQLExec(t, db, `DELETE FROM user_roles WHERE user_id='human-two'`)
	unassigned, err := auth.Snapshot(ctx, "human-two")
	if err != nil {
		t.Fatal(err)
	}
	if len(unassigned.Roles) != 0 || len(unassigned.Grants) != 0 || unassigned.Revision <= identical.Revision {
		t.Fatal("assignment removal stale")
	}
	operationalSQLExec(t, db, `UPDATE users SET password_hash='synthetic-test-only-replacement' WHERE id='human-one'`)
	rows, err := db.QueryContext(ctx, `SELECT source_key,change_digest FROM operational_authorization_history WHERE source_table<>'schema'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, digest string
		if err := rows.Scan(&key, &digest); err != nil {
			t.Fatal(err)
		}
		if len(key) != 64 || len(digest) != 64 || strings.Contains(key, "synthetic") || strings.Contains(digest, "synthetic") {
			t.Fatal("history exposed source material")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestOperationalSQLFullLegacySchema(t *testing.T) {
	db := operationalSQLFixture(t)
	operationalSQLReset(t, db)
	ctx := t.Context()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	var migrations int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil || migrations != 94 {
		t.Fatalf("fixed legacy migrations=%d err=%v", migrations, err)
	}
	var legacyMigrations int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE id<>'093_user_admin_guard.sql'").Scan(&legacyMigrations); err != nil || legacyMigrations != 93 {
		t.Fatalf("fixed legacy migration inventory changed: %d err=%v", legacyMigrations, err)
	}
	auth := NewMariaDBAuthStore(db)
	admin, err := auth.CreateFirstAdmin(ctx, "synthetic-s13-admin", "synthetic-only-s13-password", security.DefaultPermissions)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := auth.CreateRole(ctx, "synthetic-custom", []string{"streams.read", "audit_logs.export"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.CreateRole(ctx, "synthetic-second", []string{"streams.read", "roles.read"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.CreateOAuthUser(ctx, "synthetic-member", "synthetic-member@example.test", []string{custom.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetUserPermissions(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(before)
	if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	service := oa.NewService(auth)
	current, operations, err := service.Current(ctx, admin.ID, oa.Scope{Type: oa.ScopeGlobal})
	if err != nil || len(operations) != 99 || current.Principal.ID != admin.ID {
		t.Fatalf("administrator migration: permissions=%d err=%v", len(operations), err)
	}
	_, after, err := service.Current(ctx, member.ID, oa.Scope{Type: oa.ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(after)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatal("full legacy schema custom role union changed")
	}
	if _, err := auth.SetUserStatus(ctx, admin.ID, "disabled"); err != ErrLastSuperAdmin {
		t.Fatalf("last super_admin guard changed: %v", err)
	}
	if _, err := auth.UpdateRole(ctx, custom.ID, "synthetic-custom", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	_, remaining, err := service.Current(ctx, member.ID, oa.Scope{Type: oa.ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(remaining, ",") != "roles.read,streams.read" {
		t.Fatalf("role change did not preserve remaining union: %v", remaining)
	}
}
