//go:build lifecycle_identity_integration

package lifecyclefoundation

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

// IdentityTestEnvironment is compiled only into the LF test binary. Every row,
// credential value and reference belongs to the Actions-only disposable fixture.
// It is not a product Provider, secret-commitment profile or deployment approval.
type IdentityTestEnvironment struct {
	*ProductTestEnvironment
	Class         IdentityClass
	FixedRevision uint64
}

var identityProductTables = []string{
	"b2_test_restrict_owner", "email_change_challenges", "webauthn_credentials",
	"webauthn_registration_challenges", "webauthn_ceremony_sessions", "mfa_challenges", "user_mfa", "oauth_user_links",
	"sessions", "user_roles", "role_permissions", "roles", "users", "user_admin_guard",
	"operational_authorization_epoch", "v3_cp_authority_epoch",
	"v3_cp_identity_history", "v3_cp_identity_projection", "v3_cp_identity_schema_history", "v3_cp_identity_schema",
}

type identityTestAuthorizer struct{ env *IdentityTestEnvironment }

func (a identityTestAuthorizer) ResolveIdentity(ctx context.Context, call IdentityCall) (IdentityBinding, error) {
	p, err := ReadIdentityProjection(ctx, a.env.DB)
	if err != nil {
		return IdentityBinding{}, err
	}
	class := a.env.Class
	if class == "" {
		class = IdentityHuman
		switch call.Operation {
		case "identity.user.find", "identity.user.create_oauth", "identity.session.create", "identity.session.issue_login", "identity.login.success", "identity.login.failure", "identity.mfa.challenge.create", "identity.mfa.challenge.read", "identity.mfa.challenge.delete":
			class = IdentityPreauth
		}
	}
	revision := p.Revision
	if a.env.FixedRevision != 0 {
		revision = a.env.FixedRevision
	}
	return IdentityBinding{Class: class, Target: call.Target, Flow: "synthetic-flow", ProofReference: "synthetic-verified-proof", Coverage: IdentityFixtureCoverage, Revision: revision}, nil
}

func (a identityTestAuthorizer) Resolve(ctx context.Context, intent Intent) (Authorization, error) {
	if intent.Operation == "lifecycle.inspect" {
		return Authorization{Actor: "operator-1", PrincipalKind: "HUMAN", Intent: intent}, ctx.Err()
	}
	_, binding, err := IdentityRequest(ctx)
	if err != nil {
		return Authorization{}, err
	}
	kind := "HUMAN"
	if binding.Class == IdentityPreauth {
		kind = "PREAUTH"
	}
	if binding.Class == IdentitySystem {
		kind = "SYSTEM"
	}
	return Authorization{Actor: "operator-1", PrincipalKind: kind, Intent: intent, Identity: &binding}, ctx.Err()
}

func (a identityTestAuthorizer) ValidateTx(ctx context.Context, tx *sql.Tx, auth Authorization, checkpoint Checkpoint) error {
	// These immutable fixture grants require no User lock before last-admin's
	// guard. Actual product authorization/proof composition remains unavailable.
	var state string
	if tx.QueryRowContext(ctx, "SELECT status FROM users WHERE id='operator-1'").Scan(&state) != nil || state != "active" {
		return ErrHeld
	}
	if auth.Identity != nil && auth.Identity.Revision != checkpoint.IdentityRevision {
		return ErrHeld
	}
	return ctx.Err()
}

func requireIdentitySQLFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN") == "" && os.Getenv("AUTOSTREAM_S13_TEST_DSN") == "" {
		t.Fatal("B2 required SQL inventory needs the explicitly disposable Actions-only DSN")
	}
}

func NewIdentityTestEnvironment(t *testing.T) *IdentityTestEnvironment {
	t.Helper()
	requireIdentitySQLFixture(t)
	f := newFixture(t)
	e := &IdentityTestEnvironment{ProductTestEnvironment: &ProductTestEnvironment{DB: f.db, Binding: f.binding, f: f}}
	for _, table := range identityProductTables {
		var count int
		must(t, e.DB.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&count))
		if count != 0 {
			t.Fatal("B2 requires an empty disposable identity namespace: " + table)
		}
	}
	t.Cleanup(func() {
		for _, table := range identityProductTables {
			_, err := e.DB.Exec("DROP TABLE IF EXISTS " + table)
			must(t, err)
		}
	})
	for _, ddl := range []string{
		"CREATE TABLE users(id CHAR(36) PRIMARY KEY,username VARCHAR(96) UNIQUE NOT NULL,email VARCHAR(255) NULL,password_hash VARCHAR(255) NOT NULL,status VARCHAR(32) NOT NULL,failed_login_count INT NOT NULL DEFAULT 0,last_login_at DATETIME(6) NULL,last_login_ip VARCHAR(96) NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE roles(id CHAR(36) PRIMARY KEY,name VARCHAR(96) UNIQUE NOT NULL,created_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE role_permissions(role_id CHAR(36) NOT NULL,permission VARCHAR(96) NOT NULL,PRIMARY KEY(role_id,permission)) ENGINE=InnoDB",
		"CREATE TABLE user_roles(user_id CHAR(36) NOT NULL,role_id CHAR(36) NOT NULL,PRIMARY KEY(user_id,role_id)) ENGINE=InnoDB",
		"CREATE TABLE user_admin_guard(singleton TINYINT PRIMARY KEY) ENGINE=InnoDB",
		"INSERT INTO user_admin_guard VALUES(1)",
		"CREATE TABLE sessions(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL,csrf_token_hash CHAR(64) NOT NULL,idle_expires_at DATETIME(6) NOT NULL,absolute_expires_at DATETIME(6) NOT NULL,created_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE operational_authorization_epoch(singleton TINYINT PRIMARY KEY,generation BIGINT UNSIGNED NOT NULL) ENGINE=InnoDB",
		"INSERT INTO operational_authorization_epoch VALUES(1,1)",
		"CREATE TABLE v3_cp_authority_epoch(singleton TINYINT PRIMARY KEY,generation BIGINT UNSIGNED NOT NULL) ENGINE=InnoDB",
		"INSERT INTO v3_cp_authority_epoch VALUES(1,1)",
		"CREATE TABLE user_mfa(user_id CHAR(36) PRIMARY KEY,enabled BOOLEAN NOT NULL,totp_secret_ciphertext TEXT NULL,totp_secret_nonce TEXT NULL,pending_totp_secret_ciphertext TEXT NULL,pending_totp_secret_nonce TEXT NULL,recovery_code_hashes_json TEXT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE mfa_challenges(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL,expires_at DATETIME(6),created_at DATETIME(6)) ENGINE=InnoDB",
		"CREATE TABLE email_change_challenges(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL,email VARCHAR(255),expires_at DATETIME(6),created_at DATETIME(6),FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE) ENGINE=InnoDB",
		"CREATE TABLE b2_test_restrict_owner(id INT PRIMARY KEY,user_id CHAR(36) NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id)) ENGINE=InnoDB",
		"CREATE TABLE oauth_user_links(id CHAR(36) PRIMARY KEY,user_id CHAR(36) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE webauthn_credentials(id CHAR(36) PRIMARY KEY,user_id CHAR(36) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE webauthn_registration_challenges(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE webauthn_ceremony_sessions(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NULL) ENGINE=InnoDB",
	} {
		_, err := e.DB.Exec(ddl)
		must(t, err)
	}
	// Each DML fires its own actual MariaDB epoch update, including multi-DML
	// Role changes. Tests must observe its resulting value, never assume +1.
	for _, table := range []string{"users", "roles", "role_permissions", "user_roles"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			_, err := e.DB.Exec(fmt.Sprintf("CREATE TRIGGER b2_%s_%s AFTER %s ON %s FOR EACH ROW UPDATE operational_authorization_epoch SET generation=generation+1 WHERE singleton=1", table, strings.ToLower(event), event, table))
			must(t, err)
		}
	}
	for _, sql := range []string{
		"INSERT INTO users(id,username,password_hash,status,created_at,updated_at) VALUES('operator-1','operator','synthetic-password-before','active',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)),('member-1','member','synthetic-member-password','active',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)),('pending-1','pending','synthetic-pending-password','pending_password_change',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
		"INSERT INTO roles VALUES('role-admin','super_admin',UTC_TIMESTAMP(6)),('role-start','starter',UTC_TIMESTAMP(6))",
		"INSERT INTO role_permissions VALUES('role-admin','roles.create'),('role-admin','streams.start'),('role-start','streams.start')",
		"INSERT INTO user_roles VALUES('operator-1','role-admin'),('member-1','role-start')",
	} {
		_, err := e.DB.Exec(sql)
		must(t, err)
	}
	must(t, ApplyIdentityMigrations(context.Background(), e.DB))
	_, err := e.DB.Exec("INSERT INTO v3_cp_identity_projection(singleton,revision,operational_revision,authority_epoch,entity_count,content_reference,history_reference,coverage,qualified) VALUES(1,1,1,1,0,'synthetic-unenrolled','synthetic-history-initial',?,0)", IdentityFixtureCoverage)
	must(t, err)
	projection, err := captureIdentityFixture(context.Background(), e.DB)
	must(t, err)
	_, err = e.DB.Exec("UPDATE v3_cp_identity_projection SET operational_revision=?,authority_epoch=?,entity_count=?,content_reference=? WHERE singleton=1", projection.OperationalRevision, projection.AuthorityEpoch, projection.Count, projection.ContentReference)
	must(t, err)
	e.enroll(t)
	return e
}

// captureIdentityFixture hashes synthetic rows only inside the test binary.
// Its output cannot qualify a real secret checkpoint or trusted clock.
func captureIdentityFixture(ctx context.Context, q Executor) (IdentityProjection, error) {
	p, err := ReadIdentityProjection(ctx, q)
	if err != nil {
		return p, err
	}
	if err = q.QueryRowContext(ctx, "SELECT generation FROM operational_authorization_epoch WHERE singleton=1").Scan(&p.OperationalRevision); err != nil {
		return p, err
	}
	if err = q.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&p.AuthorityEpoch); err != nil {
		return p, err
	}
	var roots []any
	p.Count = 0
	for _, query := range []string{
		"SELECT id,username,email,password_hash,status,failed_login_count,last_login_at,last_login_ip FROM users ORDER BY id",
		"SELECT id,user_id,csrf_token_hash,idle_expires_at,absolute_expires_at FROM sessions ORDER BY id",
		"SELECT id,name FROM roles ORDER BY id",
		"SELECT role_id,permission FROM role_permissions ORDER BY role_id,permission",
		"SELECT user_id,role_id FROM user_roles ORDER BY user_id,role_id",
		"SELECT user_id,enabled,totp_secret_ciphertext,totp_secret_nonce,pending_totp_secret_ciphertext,pending_totp_secret_nonce,recovery_code_hashes_json FROM user_mfa ORDER BY user_id",
		"SELECT id,user_id,expires_at FROM mfa_challenges ORDER BY id",
		"SELECT id,user_id,email,expires_at FROM email_change_challenges ORDER BY id",
		"SELECT id,user_id FROM oauth_user_links ORDER BY id",
		"SELECT id,user_id FROM webauthn_credentials ORDER BY id",
		"SELECT id,user_id FROM webauthn_registration_challenges ORDER BY id",
		"SELECT id,user_id FROM webauthn_ceremony_sessions ORDER BY id",
	} {
		rows, readErr := q.QueryContext(ctx, query)
		if readErr != nil {
			return p, readErr
		}
		columns, readErr := rows.Columns()
		if readErr != nil {
			rows.Close()
			return p, readErr
		}
		for rows.Next() {
			values := make([]sql.RawBytes, len(columns))
			scan := make([]any, len(columns))
			for i := range values {
				scan[i] = &values[i]
			}
			if readErr = rows.Scan(scan...); readErr != nil {
				rows.Close()
				return p, readErr
			}
			entry := make([]string, len(values))
			for i, value := range values {
				if value == nil {
					entry[i] = "fixture-null"
				} else {
					entry[i] = string(value)
				}
			}
			roots = append(roots, entry)
			p.Count++
		}
		readErr = rows.Err()
		rows.Close()
		if readErr != nil {
			return p, readErr
		}
	}
	p.ContentReference = SyntheticReference(roots)
	return p, nil
}

func identityFixtureCheckpoint(ctx context.Context, q Executor) (Checkpoint, error) {
	p, err := captureIdentityFixture(ctx, q)
	return Checkpoint{OperationalRevision: p.OperationalRevision, AuthorityEpoch: p.AuthorityEpoch, IdentityRevision: p.Revision, Count: p.Count, Content: SyntheticReference([]any{p.ContentReference, p.Revision}), HistoryHead: p.HistoryReference}, err
}

func (e *IdentityTestEnvironment) attachIdentity(t *testing.T) {
	service, err := NewService(e.f.cp, productTestPort{e.ProductTestEnvironment}, identityTestAuthorizer{e})
	must(t, err)
	e.f.service = service
	e.Gate = &Gate{service: service, source: identityFixtureCheckpoint, identityCapture: captureIdentityFixture}
}

func (e *IdentityTestEnvironment) enroll(t *testing.T) {
	checkpoint, err := identityFixtureCheckpoint(context.Background(), e.DB)
	must(t, err)
	frame := Frame{Binding: e.Binding, Checkpoint: checkpoint, Phase: Completed}
	_, err = e.DB.Exec("UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", encode(frame), e.Binding.Deployment)
	must(t, err)
	_, err = e.DB.Exec("UPDATE b0_test_ledger_heads SET body=? WHERE deployment=?", encode(Head{Frame: frame, Phase: Completed}), e.Binding.Deployment)
	must(t, err)
	e.attachIdentity(t)
}

// SealFixture permits synthetic setup only before the first admission. It is
// never a repair/reconciliation path after a lifecycle attempt or history exists.
func (e *IdentityTestEnvironment) SealFixture(t *testing.T) {
	t.Helper()
	var attempts, history int
	must(t, e.DB.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_attempts WHERE deployment=?", e.Binding.Deployment).Scan(&attempts))
	must(t, e.DB.QueryRow("SELECT COUNT(*) FROM v3_cp_identity_history").Scan(&history))
	if attempts != 0 || history != 0 {
		t.Fatal("cannot reseal an admitted identity fixture")
	}
	p, err := captureIdentityFixture(context.Background(), e.DB)
	must(t, err)
	_, err = e.DB.Exec("UPDATE v3_cp_identity_projection SET operational_revision=?,authority_epoch=?,entity_count=?,content_reference=? WHERE singleton=1", p.OperationalRevision, p.AuthorityEpoch, p.Count, p.ContentReference)
	must(t, err)
	e.enroll(t)
}

func ResumeIdentityTestEnvironment(t *testing.T, binding Binding) *IdentityTestEnvironment {
	t.Helper()
	requireIdentitySQLFixture(t)
	db := disposableDB(t)
	cp, err := NewSQLStore(db, binding)
	must(t, err)
	f := &fixture{db: db, cp: cp, ledger: &syntheticLedger{db: db}, binding: binding}
	e := &IdentityTestEnvironment{ProductTestEnvironment: &ProductTestEnvironment{DB: db, Binding: binding, f: f}}
	e.attachIdentity(t)
	return e
}
