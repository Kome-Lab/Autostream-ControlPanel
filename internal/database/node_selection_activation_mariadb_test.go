package database

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

const activationFixtureCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func openNodeSelectionActivationFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := openMariaDBMigrationTest(t, false)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var permanentEngine, temporaryEngine string
	if err := db.QueryRowContext(ctx, "SELECT @@default_storage_engine,@@default_tmp_storage_engine").Scan(&permanentEngine, &temporaryEngine); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture engines: permanent=%s temporary=%s; transactional tables explicitly use InnoDB", permanentEngine, temporaryEngine)
	for _, stmt := range []string{
		"CREATE TEMPORARY TABLE streams(id CHAR(36) PRIMARY KEY,deleted_at DATETIME(6) NULL,created_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE stream_settings(stream_id CHAR(36) PRIMARY KEY,discord_config_id CHAR(36) NULL,updated_at DATETIME(6) NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE profiles(id CHAR(36) PRIMARY KEY,kind VARCHAR(40) NOT NULL,config JSON NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE services(service_id VARCHAR(128) PRIMARY KEY,service_type VARCHAR(40) NOT NULL,current_stream_id CHAR(36) NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE stream_service_assignments(id CHAR(36) PRIMARY KEY,stream_id CHAR(36) NOT NULL,service_type VARCHAR(40) NOT NULL,service_id VARCHAR(128) NOT NULL,assignment_role VARCHAR(16) NOT NULL,assigned_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE system_update_jobs(id CHAR(36) PRIMARY KEY,target_id VARCHAR(191) NOT NULL,target_service_type VARCHAR(64) NOT NULL,operation VARCHAR(32) NOT NULL,target_version VARCHAR(128) NOT NULL,status VARCHAR(32) NOT NULL,agent_service_id VARCHAR(191) NULL,execution_host_id VARCHAR(191) NOT NULL,transport_mode VARCHAR(32) NOT NULL,current_version VARCHAR(128) NOT NULL,artifact_digest VARCHAR(192) NOT NULL DEFAULT '',previous_digest VARCHAR(192) NOT NULL DEFAULT '',ownership_epoch BIGINT NOT NULL,completed_at DATETIME(6) NULL,active_target_id VARCHAR(191) GENERATED ALWAYS AS (CASE WHEN status IN ('claimed','installing','starting','health_checking','reconciling','rolling_back') THEN target_id ELSE NULL END) STORED,UNIQUE KEY uq_active(active_target_id)) ENGINE=InnoDB",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"migrations/092_stream_node_selection_activation.sql", "migrations/090_stream_node_selections.sql"} {
		body, err := embeddedMigrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		statements := splitSQLStatements(string(body))
		// Only create the temporary new tables. Seeding/rebuild is exercised below.
		statement := strings.Replace(statements[0], "CREATE TABLE IF NOT EXISTS", "CREATE TEMPORARY TABLE", 1) + " ENGINE=InnoDB"
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO stream_node_selection_activation(singleton_id) VALUES(1)",
		"INSERT INTO streams VALUES('frame',NULL,'2026-11-07 00:00:00.123456')",
		"INSERT INTO services VALUES('encoder-1','encoder_recorder','frame'),('encoder-2','encoder_recorder',NULL),('bot-1','discord_bot',NULL)",
		"INSERT INTO stream_service_assignments VALUES('assignment','frame','encoder_recorder','encoder-1','primary','2026-11-07 00:00:00.123456')",
		"INSERT INTO profiles VALUES('discord','discord_config','{\"service_id\":\"bot-1\"}')",
		"INSERT INTO stream_settings VALUES('frame','discord','2026-11-07 00:00:00.123456')",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx
}

func insertNodeActivationJob(t *testing.T, ctx context.Context, db *sql.DB, id string) {
	t.Helper()
	_, err := db.ExecContext(ctx, "INSERT INTO system_update_jobs(id,target_id,target_service_type,operation,target_version,status,agent_service_id,execution_host_id,transport_mode,current_version,ownership_epoch) VALUES(?,'control-panel','control_panel','software_update','v2.0.7','installing','updater','host','pull_v2','v2.0.6',7)", id)
	if err != nil {
		t.Fatal(err)
	}
}

func finishNodeActivationJob(t *testing.T, ctx context.Context, db *sql.DB, id, status string) {
	t.Helper()
	_, err := db.ExecContext(ctx, "UPDATE system_update_jobs SET status=?,completed_at=UTC_TIMESTAMP(6),artifact_digest=?,previous_digest=? WHERE id=?", status, strings.Repeat("a", 64), strings.Repeat("b", 64), id)
	if err != nil {
		t.Fatal(err)
	}
}

func nodeActivationChoice(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var selected string
	if err := db.QueryRowContext(ctx, "SELECT service_id FROM stream_node_selections WHERE stream_id='frame' AND service_type='encoder_recorder'").Scan(&selected); err != nil {
		t.Fatal(err)
	}
	return selected
}

func nodeActivationRows(t *testing.T, ctx context.Context, db *sql.DB, table string) [][]string {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return nodeSelectionMigrationRows(t, ctx, conn, table)
}

func TestMariaDBNodeSelectionActivationRollbackEditAndRetry(t *testing.T) {
	db, ctx := openNodeSelectionActivationFixture(t)
	insertNodeActivationJob(t, ctx, db, "job-1")
	protected := map[string][][]string{}
	for _, table := range []string{"streams", "stream_settings", "profiles", "services", "stream_service_assignments"} {
		protected[table] = nodeActivationRows(t, ctx, db, table)
	}
	first, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ready() || !first.ProbeReady() || first.UpdateJobID() != "job-1" || nodeActivationChoice(t, ctx, db) != "encoder-1" {
		t.Fatal("first update was not held with prepared legacy choices")
	}
	for table, before := range protected {
		if !reflect.DeepEqual(before, nodeActivationRows(t, ctx, db, table)) {
			t.Fatalf("preparation changed legacy %s", table)
		}
	}
	finishNodeActivationJob(t, ctx, db, "job-1", "rolled_back")
	if ready, healthy, err := first.checkCompletion(ctx); err != nil || ready || healthy {
		t.Fatal("rollback incorrectly activated the candidate")
	}
	// The restored old application changes its own legacy preference.
	if _, err = db.ExecContext(ctx, "UPDATE stream_service_assignments SET service_id='encoder-2',assigned_at='2026-11-07 01:00:00.654321' WHERE id='assignment'"); err != nil {
		t.Fatal(err)
	}
	insertNodeActivationJob(t, ctx, db, "job-2")
	retry, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Ready() || retry.UpdateJobID() != "job-2" || nodeActivationChoice(t, ctx, db) != "encoder-2" {
		t.Fatal("retry retained the failed candidate's stale Node selection")
	}
	finishNodeActivationJob(t, ctx, db, "job-2", "succeeded")
	if ready, healthy, err := retry.checkCompletion(ctx); err != nil || !ready || !healthy {
		t.Fatalf("durable success did not activate: %v", err)
	}
	// Even a restart at the activation boundary must preserve explicit clears and
	// preferences that intentionally differ from execution assignments.
	if _, err = db.ExecContext(ctx, "UPDATE stream_node_selections SET service_id='' WHERE stream_id='frame' AND service_type='encoder_recorder'"); err != nil {
		t.Fatal(err)
	}
	restored, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil || !restored.Ready() || nodeActivationChoice(t, ctx, db) != "" {
		t.Fatalf("activated choices were reimported from legacy state: %v", err)
	}
}

func TestMariaDBNodeSelectionActivationRestartAndTerminalHold(t *testing.T) {
	db, ctx := openNodeSelectionActivationFixture(t)
	insertNodeActivationJob(t, ctx, db, "job-1")
	first, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil {
		t.Fatal(err)
	}
	restart, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil || restart.Ready() || restart.UpdateJobID() != first.UpdateJobID() {
		t.Fatalf("pending restart lost its exact job: %v", err)
	}
	if _, err = PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("b", 40)); err == nil {
		t.Fatal("different candidate commit resumed the held update")
	}
	finishNodeActivationJob(t, ctx, db, "job-1", "failed")
	held, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil || held.Ready() || held.ProbeReady() {
		t.Fatalf("failed update was treated as manual installation: %v", err)
	}
	insertNodeActivationJob(t, ctx, db, "job-2")
	if _, err = PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit); err == nil {
		t.Fatal("replacement job bypassed missing rollback proof")
	}
	if _, err = db.ExecContext(ctx, "DELETE FROM system_update_jobs WHERE id='job-1'"); err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit); err == nil {
		t.Fatal("missing original job was replaced")
	}
}

func TestMariaDBNodeSelectionActivationRejectsChangedAuthority(t *testing.T) {
	for _, mutation := range []string{"agent_service_id='other'", "execution_host_id='other'", "ownership_epoch=8", "target_version='v2.0.8'", "operation='port_reconfigure'", "target_id='worker'"} {
		t.Run(mutation, func(t *testing.T) {
			db, ctx := openNodeSelectionActivationFixture(t)
			insertNodeActivationJob(t, ctx, db, "job-1")
			gate, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, "UPDATE system_update_jobs SET "+mutation+" WHERE id='job-1'"); err != nil {
				t.Fatal(err)
			}
			if ready, healthy, err := gate.checkCompletion(ctx); err == nil || ready || healthy {
				t.Fatal("changed job authority released the hold")
			}
			if _, err = PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit); err == nil {
				t.Fatal("restart accepted changed job authority")
			}
		})
	}
}

func TestMariaDBNodeSelectionActivationPreparationIsAtomic(t *testing.T) {
	db, ctx := openNodeSelectionActivationFixture(t)
	insertNodeActivationJob(t, ctx, db, "job-1")
	if _, err := db.ExecContext(ctx, "INSERT INTO stream_node_selections VALUES('frame','encoder_recorder','original-preference','2026-11-07 00:00:00.111111','2026-11-07 00:00:00.222222')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO stream_service_assignments VALUES('duplicate-primary','frame','encoder_recorder','encoder-2','primary','2026-11-07 00:00:00.123456')"); err != nil {
		t.Fatal(err)
	}
	before := nodeActivationRows(t, ctx, db, "stream_node_selections")
	if _, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit); err == nil {
		t.Fatal("ambiguous legacy primary did not fail preparation")
	}
	if !reflect.DeepEqual(before, nodeActivationRows(t, ctx, db, "stream_node_selections")) {
		t.Fatal("failed preparation partially erased preferences")
	}
	var job sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT update_job_id FROM stream_node_selection_activation WHERE singleton_id=1").Scan(&job); err != nil || job.Valid {
		t.Fatal("failed preparation partially bound a job")
	}
}

func TestMariaDBNodeSelectionActivationManualAndMissingProvenance(t *testing.T) {
	db, ctx := openNodeSelectionActivationFixture(t)
	manual, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil || !manual.Ready() || nodeActivationChoice(t, ctx, db) != "encoder-1" {
		t.Fatalf("fresh manual startup did not prepare choices: %v", err)
	}
	if _, err = db.ExecContext(ctx, "DELETE FROM stream_node_selection_activation"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = prepareNodeSelectionActivationJournal(ctx, db); err == nil {
			t.Fatal("missing provenance was fabricated for existing preferences")
		}
	}
	if nodeActivationChoice(t, ctx, db) != "encoder-1" {
		t.Fatal("unknown preferences changed")
	}
}

func TestMariaDBNodeSelectionActivationSuccessRequiresProof(t *testing.T) {
	db, ctx := openNodeSelectionActivationFixture(t)
	insertNodeActivationJob(t, ctx, db, "job-1")
	gate, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE system_update_jobs SET status='succeeded' WHERE id='job-1'"); err != nil {
		t.Fatal(err)
	}
	if ready, healthy, err := gate.checkCompletion(ctx); err == nil || ready || healthy {
		t.Fatal("status alone released activation")
	}
	finishNodeActivationJob(t, ctx, db, "job-1", "succeeded")
	for i := 0; i < 2; i++ {
		if ready, healthy, err := gate.checkCompletion(ctx); err != nil || !ready || !healthy {
			t.Fatalf("same terminal proof did not converge: %v", err)
		}
	}
	restart, err := PrepareNodeSelectionActivation(ctx, db, "v2.0.7", activationFixtureCommit)
	if err != nil || !restart.Ready() {
		t.Fatalf("activated restart did not converge: %v", err)
	}
}
