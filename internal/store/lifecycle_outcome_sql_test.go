package store

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/videocover"
)

// This root reuses the existing strict Actions-only disposable S13 schema guard.
// It never accepts an arbitrary developer/production DSN.
func TestIUWS2CoverSQLTransaction(t *testing.T) {
	db := operationalSQLFixture(t)
	ctx := t.Context()
	for _, statement := range []string{
		`ALTER TABLE users ADD COLUMN username VARCHAR(128) NOT NULL DEFAULT ''`,
		`CREATE TABLE sessions(id VARCHAR(128) PRIMARY KEY,user_id CHAR(36) NOT NULL,idle_expires_at DATETIME(6) NOT NULL,absolute_expires_at DATETIME(6) NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE streams(id CHAR(36) PRIMARY KEY,deleted_at DATETIME(6) NULL) ENGINE=InnoDB`,
		`CREATE TABLE audit_logs(id CHAR(36) PRIMARY KEY,timestamp DATETIME(6) NOT NULL,actor_user_id CHAR(36),actor_username VARCHAR(128),actor_ip VARCHAR(128),user_agent TEXT,action VARCHAR(160) NOT NULL,resource_type VARCHAR(160) NOT NULL,resource_id VARCHAR(128),result VARCHAR(32) NOT NULL,metadata TEXT NOT NULL,request_id VARCHAR(128) NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE stream_video_cover_runtime(stream_id CHAR(36) NOT NULL,job_generation BIGINT UNSIGNED NOT NULL,desired_active BOOLEAN NOT NULL,desired_revision BIGINT UNSIGNED NOT NULL,applied_active BOOLEAN NULL,applied_revision BIGINT UNSIGNED NULL,asset_variant_id VARCHAR(128) NULL,applied_asset_variant_id VARCHAR(128) NULL,last_error_code VARCHAR(80) NULL,reconciliation_status VARCHAR(24) NOT NULL,last_idempotency_key VARCHAR(128) NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NOT NULL,retired_at DATETIME(6) NULL,PRIMARY KEY(stream_id,job_generation)) ENGINE=InnoDB`,
		`CREATE TABLE stream_video_cover_actions(stream_id CHAR(36) NOT NULL,job_generation BIGINT UNSIGNED NOT NULL,idempotency_key VARCHAR(128) NOT NULL,requested_active BOOLEAN NOT NULL,requested_revision BIGINT UNSIGNED NOT NULL,requested_asset_variant_id VARCHAR(128) NULL,request_fingerprint CHAR(64) NOT NULL,result_status VARCHAR(24) NOT NULL,safe_error_code VARCHAR(80) NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NOT NULL,PRIMARY KEY(stream_id,job_generation,idempotency_key)) ENGINE=InnoDB`,
		`INSERT INTO users(id,status,username) VALUES('test-human','active','test-reader')`,
		`INSERT INTO streams(id) VALUES('test-stream')`,
		`INSERT INTO roles(id,name) VALUES('test-reader-role','test-reader-role'),('test-other-role','test-other-role'),('test-name-only-admin','super_admin')`,
		`INSERT INTO user_roles(user_id,role_id) VALUES('test-human','test-reader-role'),('test-human','test-other-role')`,
		`INSERT INTO role_permissions(role_id,permission) VALUES('test-reader-role','streams.read'),('test-other-role','streams.show_cover')`,
		`INSERT INTO sessions(id,user_id,idle_expires_at,absolute_expires_at) VALUES('test-session-hash','test-human',UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := oa.ApplyOperationalMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	owner := NewMariaDBAuthStore(db)
	audit := NewMariaDBAuditStore(db)
	covers := videocover.NewMariaDBRepository(db)
	if _, err := covers.EnsureGeneration(ctx, "test-stream", 7, "test-variant", false); err != nil {
		t.Fatal(err)
	}
	request := videocover.ActionRequest{Active: true, ExpectedJobGeneration: 7, ExpectedRevision: 1, IdempotencyKey: "test-cover-key"}
	if _, err := covers.PrepareAction(ctx, "test-stream", request); err != nil {
		t.Fatal(err)
	}
	expected, err := covers.RecordAmbiguous(ctx, "test-stream", 7, request.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	actor := CoverRecordActor{UserID: "test-human", SessionHash: "test-session-hash"}
	event := AuditEvent{ActorUserID: actor.UserID, Action: "streams.video_cover.show", ResourceType: "stream", ResourceID: "test-stream", RequestID: "test-request"}
	if _, err = db.ExecContext(ctx, `CREATE TRIGGER test_iu_ws2_audit_fail BEFORE INSERT ON audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic audit failure'`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.RecordCoverObservation(ctx, audit, covers, actor, expected, "applied", "", event); err == nil {
		t.Fatal("critical audit failure accepted")
	}
	state, err := covers.GetCurrentState(ctx, "test-stream")
	if err != nil || state.Status != "confirming" || state.AppliedRevision != nil {
		t.Fatal("audit failure did not roll back local result")
	}
	if _, err = db.ExecContext(ctx, `DROP TRIGGER test_iu_ws2_audit_fail`); err != nil {
		t.Fatal(err)
	}
	second, err := sql.Open("mysql", os.Getenv("AUTOSTREAM_S13_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetMaxOpenConns(2)
	if _, err = owner.RecordCoverObservation(ctx, audit, videocover.NewMariaDBRepository(second), actor, expected, "applied", "", event); err == nil {
		t.Fatal("separate pool joined as same transaction")
	}
	// Two independent owner/pool instances race on the same immutable action.
	otherOwner := NewMariaDBAuthStore(second)
	otherAudit := NewMariaDBAuditStore(second)
	otherCovers := videocover.NewMariaDBRepository(second)
	barrier := make(chan struct{})
	done := make(chan error, 2)
	for _, entry := range []struct {
		owner  MariaDBAuthStore
		audit  MariaDBAuditStore
		covers *videocover.SQLRepository
	}{{owner, audit, covers}, {otherOwner, otherAudit, otherCovers}} {
		go func() {
			<-barrier
			_, e := entry.owner.RecordCoverObservation(ctx, entry.audit, entry.covers, actor, expected, "applied", "", event)
			done <- e
		}()
	}
	close(barrier)
	for range 2 {
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("independent pool race deadlocked")
		}
	}
	var audits int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE action='streams.video_cover.show' AND actor_user_id='test-human' AND actor_username='test-reader'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("outcome and Human audit not exactly once")
	}
	// Restarted owners inspect the committed action, without another mutation.
	restarted := videocover.NewMariaDBRepository(second)
	state, err = restarted.GetCurrentState(ctx, "test-stream")
	if err != nil || state.Status != "applied" {
		t.Fatal("restart lost committed result")
	}
	if _, err = covers.EnsureGeneration(ctx, "test-stream", 8, "test-next-variant", false); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.RecordCoverObservation(ctx, audit, covers, actor, expected, "applied", "", event); err == nil {
		t.Fatal("stale generation accepted")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id='test-reader-role' AND permission='streams.read'`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.RecordCoverObservation(ctx, audit, covers, actor, expected, "applied", "", event); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked read permission accepted")
	}
	// Explicit required Identity/L2 composition never falls back to this v2 Tx.
	closed := NewMariaDBAuthStoreWithLifecycle(db, "", lf.NewRequiredGate())
	if _, err = closed.RecordCoverObservation(ctx, audit, covers, actor, expected, "applied", "", event); !errors.Is(err, lf.ErrHeld) {
		t.Fatal("unqualified Identity path fell back")
	}
}
