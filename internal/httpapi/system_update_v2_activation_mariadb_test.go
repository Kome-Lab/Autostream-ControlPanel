package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/database"
	_ "github.com/go-sql-driver/mysql"
)

func openActivationWireFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"))
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { cancel(); _ = db.Close() })
	for _, stmt := range []string{
		"CREATE TEMPORARY TABLE streams(id CHAR(36) PRIMARY KEY,deleted_at DATETIME(6) NULL,created_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE stream_settings(stream_id CHAR(36) PRIMARY KEY,discord_config_id CHAR(36) NULL,updated_at DATETIME(6) NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE profiles(id CHAR(36) PRIMARY KEY,kind VARCHAR(40) NOT NULL,config JSON NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE services(service_id VARCHAR(128) PRIMARY KEY,service_type VARCHAR(40) NOT NULL,current_stream_id CHAR(36) NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE stream_service_assignments(id CHAR(36) PRIMARY KEY,stream_id CHAR(36) NOT NULL,service_type VARCHAR(40) NOT NULL,service_id VARCHAR(128) NOT NULL,assignment_role VARCHAR(16) NOT NULL,assigned_at DATETIME(6) NOT NULL) ENGINE=InnoDB",
		"CREATE TEMPORARY TABLE system_update_jobs(id CHAR(36) PRIMARY KEY,target_id VARCHAR(191) NOT NULL,target_service_type VARCHAR(64) NOT NULL,operation VARCHAR(32) NOT NULL,target_version VARCHAR(128) NOT NULL,status VARCHAR(32) NOT NULL,agent_service_id VARCHAR(191) NULL,execution_host_id VARCHAR(191) NOT NULL,transport_mode VARCHAR(32) NOT NULL,current_version VARCHAR(128) NOT NULL,artifact_digest VARCHAR(192) NOT NULL DEFAULT '',previous_digest VARCHAR(192) NOT NULL DEFAULT '',ownership_epoch BIGINT NOT NULL,completed_at DATETIME(6) NULL,active_target_id VARCHAR(191) GENERATED ALWAYS AS (CASE WHEN status IN ('claimed','installing','starting','health_checking','reconciling','rolling_back') THEN target_id ELSE NULL END) STORED,UNIQUE KEY uq_active(active_target_id)) ENGINE=InnoDB",
	} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"090_stream_node_selections.sql", "092_stream_node_selection_activation.sql"} {
		body, readErr := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := string(body)
		start := strings.Index(text, "CREATE TABLE IF NOT EXISTS")
		if start < 0 {
			t.Fatal("missing fixture table migration")
		}
		end := strings.Index(text[start:], ";")
		if end < 0 {
			t.Fatal("unterminated fixture table migration")
		}
		ddl := strings.Replace(text[start:start+end], "CREATE TABLE IF NOT EXISTS", "CREATE TEMPORARY TABLE", 1) + " ENGINE=InnoDB"
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO stream_node_selection_activation(singleton_id) VALUES(1)",
		"INSERT INTO streams VALUES('frame',NULL,'2026-11-07 00:00:00.123456')",
		"INSERT INTO services VALUES('encoder-1','encoder_recorder','frame'),('encoder-2','encoder_recorder',NULL)",
		"INSERT INTO stream_service_assignments VALUES('assignment','frame','encoder_recorder','encoder-1','primary','2026-11-07 00:00:00.123456')",
	} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx
}

func insertActivationWireJob(t *testing.T, ctx context.Context, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "INSERT INTO system_update_jobs(id,target_id,target_service_type,operation,target_version,status,agent_service_id,execution_host_id,transport_mode,current_version,ownership_epoch) VALUES(?,'control-panel','control_panel','software_update','v2.0.7','installing','updater','host','pull_v2','v2.0.6',7)", id); err != nil {
		t.Fatal(err)
	}
}

// Exercise the production JSON evidence adapter before persisting the exact
// report fields read by activation. Lease/auth validation has separate tests.
func persistActivationWireResult(t *testing.T, ctx context.Context, db *sql.DB, id string, rolledBack bool) {
	t.Helper()
	result := contracts.UpdaterResultEnvelope{ProtocolVersion: 2, Status: contracts.SystemUpdateStatus("succeeded"), Outcome: contracts.UpdaterOutcomeSucceeded, Evidence: []contracts.UpdaterEvidence{{EvidenceCode: "application_probe_verified", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), ObservedAt: time.Now().UTC(), ObservedRevision: 1}}}
	if rolledBack {
		result.Status = contracts.SystemUpdateStatus("rolled_back")
		result.Outcome = contracts.UpdaterOutcomeRolledBack
		result.Evidence[0].EvidenceCode = "rollback_verified"
		result.Evidence[0].ArtifactDigest = "sha256:" + strings.Repeat("b", 64)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded contracts.UpdaterResultEnvelope
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	artifact, previous := systemUpdateV2EvidenceDigests(decoded)
	if (!rolledBack && (artifact == "" || previous != "")) || (rolledBack && (artifact != "" || previous == "")) {
		t.Fatal("canonical result is not outcome-specific evidence")
	}
	if _, err = db.ExecContext(ctx, "UPDATE system_update_jobs SET status=?,completed_at=UTC_TIMESTAMP(6),artifact_digest=?,previous_digest=? WHERE id=?", string(decoded.Status), artifact, previous, id); err != nil {
		t.Fatal(err)
	}
}

func awaitActivationWireReady(t *testing.T, ctx context.Context, gate *database.NodeSelectionActivation) {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go gate.Run(runCtx)
	select {
	case <-gate.ReadySignal():
		if !gate.Ready() || !gate.ProbeReady() {
			t.Fatal("activated gate is not ready")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canonical v2 success did not release activation")
	}
}

func TestMariaDBNodeSelectionActivationV2WireResults(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		db, ctx := openActivationWireFixture(t)
		insertActivationWireJob(t, ctx, db, "job-1")
		gate, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		if gate.Ready() {
			t.Fatal("installing update activated before a result")
		}
		persistActivationWireResult(t, ctx, db, "job-1", false)
		awaitActivationWireReady(t, ctx, gate)
	})
	t.Run("rollback_and_retry", func(t *testing.T) {
		db, ctx := openActivationWireFixture(t)
		insertActivationWireJob(t, ctx, db, "job-1")
		if _, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("a", 40)); err != nil {
			t.Fatal(err)
		}
		persistActivationWireResult(t, ctx, db, "job-1", true)
		held, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("a", 40))
		if err != nil || held.Ready() || held.ProbeReady() {
			t.Fatalf("rollback activated the candidate: %v", err)
		}
		if _, err = db.ExecContext(ctx, "UPDATE stream_service_assignments SET service_id='encoder-2' WHERE id='assignment'"); err != nil {
			t.Fatal(err)
		}
		insertActivationWireJob(t, ctx, db, "job-2")
		retried, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		var selected string
		if err = db.QueryRowContext(ctx, "SELECT service_id FROM stream_node_selections WHERE stream_id='frame' AND service_type='encoder_recorder'").Scan(&selected); err != nil || selected != "encoder-2" {
			t.Fatalf("retry lost legacy edit: %s %v", selected, err)
		}
		persistActivationWireResult(t, ctx, db, "job-2", false)
		awaitActivationWireReady(t, ctx, retried)
	})
	t.Run("completed_predecessor", func(t *testing.T) {
		db, ctx := openActivationWireFixture(t)
		insertActivationWireJob(t, ctx, db, "job-1")
		if _, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.7", strings.Repeat("a", 40)); err != nil {
			t.Fatal(err)
		}
		persistActivationWireResult(t, ctx, db, "job-1", false)
		recovered, err := database.PrepareNodeSelectionActivation(ctx, db, "v2.0.8", strings.Repeat("c", 40))
		if err != nil || !recovered.Ready() {
			t.Fatalf("fixed release did not finalize accepted predecessor: %v", err)
		}
		var originalVersion, originalCommit string
		if err = db.QueryRowContext(ctx, "SELECT target_version,build_commit FROM stream_node_selection_activation WHERE singleton_id=1").Scan(&originalVersion, &originalCommit); err != nil || originalVersion != "v2.0.7" || originalCommit != strings.Repeat("a", 40) {
			t.Fatal("recovery relabeled the original update")
		}
	})
}
