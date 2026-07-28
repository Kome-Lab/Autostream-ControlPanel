package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSystemUpdateHostSelfUpdateMigrationUsesDedicatedLifecycleAndGrant(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/053_system_update_host_self_updates.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"system_update_host_self_updates",
		"system_update_host_self_update_grants",
		"uq_system_update_host_self_updates_active_host",
		"uq_system_update_host_self_updates_user_idempotency",
		"attempt_generation CHAR(36)",
		"release_published_at DATETIME(6)",
		"issued_at DATETIME(6)",
		"expected_local_executor_policy_sha256 VARCHAR(71)",
		"operation IN ('stage','reconcile')",
		"UNIQUE (self_update_id, operation, session_id)",
		"UNIQUE (token_hash)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("self-update migration is missing %q:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{
		"release_url",
		"download_url",
		"github_token",
		"release_token",
		"raw_token",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("self-update migration stores forbidden field %q", forbidden)
		}
	}
}

func TestHostSelfUpdateGrantReleaseBindingMigrationMariaDBQuarantinesLegacyRevisionDrift(
	t *testing.T,
) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	for _, statement := range []string{
		`CREATE TEMPORARY TABLE system_update_host_self_updates (
			id CHAR(36) PRIMARY KEY,
			status VARCHAR(32) NOT NULL,
			revision BIGINT NOT NULL,
			release_tag VARCHAR(128) NOT NULL,
			release_commit CHAR(40) NOT NULL,
			release_published_at DATETIME(6) NOT NULL,
			manifest_asset_id BIGINT NOT NULL,
			manifest_asset_name VARCHAR(255) NOT NULL,
			manifest_sha256 CHAR(64) NOT NULL,
			manifest_checksum_asset_id BIGINT NOT NULL,
			manifest_checksum_sha256 CHAR(64) NOT NULL,
			archive_asset_id BIGINT NOT NULL,
			archive_asset_name VARCHAR(255) NOT NULL,
			archive_size BIGINT NOT NULL,
			archive_sha256 CHAR(64) NOT NULL,
			archive_checksum_asset_id BIGINT NOT NULL,
			archive_checksum_sha256 CHAR(64) NOT NULL,
			artifact_arch VARCHAR(32) NOT NULL,
			agent_protocol_version INT NOT NULL,
			executor_protocol_version INT NOT NULL,
			mutation_protocol_version INT NOT NULL,
			recovery_protocol_version INT NOT NULL,
			minimum_panel_version VARCHAR(64) NOT NULL
		)`,
		`CREATE TEMPORARY TABLE system_update_host_self_update_grants (
			id CHAR(36) PRIMARY KEY,
			self_update_id CHAR(36) NOT NULL,
			operation VARCHAR(16) NOT NULL,
			token_hash CHAR(64) NOT NULL,
			expected_self_update_revision BIGINT NOT NULL,
			artifact_sha256 CHAR(71) NOT NULL,
			consumed_at DATETIME(6) NULL
		)`,
	} {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	publishedAt := time.Date(2026, 7, 27, 9, 0, 0, 123456000, time.UTC)
	for _, row := range []struct {
		id       string
		status   string
		revision int64
	}{
		{"legacy-queued-revision-drift", "queued", 2},
		{"legacy-canceled", "canceled", 1},
		{"legacy-staging", "staging", 2},
	} {
		if _, err := connection.ExecContext(
			ctx,
			`INSERT INTO system_update_host_self_updates (
				id, status, revision, release_tag, release_commit,
				release_published_at, manifest_asset_id,
				manifest_asset_name, manifest_sha256,
				manifest_checksum_asset_id,
				manifest_checksum_sha256, archive_asset_id,
				archive_asset_name, archive_size, archive_sha256,
				archive_checksum_asset_id,
				archive_checksum_sha256, artifact_arch,
				agent_protocol_version, executor_protocol_version,
				mutation_protocol_version, recovery_protocol_version,
				minimum_panel_version
			) VALUES (
				?, ?, ?, 'v1.8.0', ?, ?, 1, 'manifest.json', ?,
				2, ?, 3, 'host-agent.tar.gz', 1024, ?, 4, ?,
				'amd64', 2, 2, 2, 1, 'v1.7.8'
			)`,
			row.id,
			row.status,
			row.revision,
			strings.Repeat("a", 40),
			publishedAt,
			strings.Repeat("1", 64),
			strings.Repeat("2", 64),
			strings.Repeat("3", 64),
			strings.Repeat("4", 64),
		); err != nil {
			t.Fatal(err)
		}
	}
	consumedAt := time.Date(2026, 7, 28, 9, 0, 1, 654321000, time.UTC)
	for _, id := range []string{
		"legacy-queued-revision-drift",
		"legacy-canceled",
		"legacy-staging",
	} {
		if _, err := connection.ExecContext(
			ctx,
			`INSERT INTO system_update_host_self_update_grants (
				id, self_update_id, operation, token_hash,
				expected_self_update_revision, artifact_sha256,
				consumed_at
			) VALUES (?, ?, 'stage', ?, 1, ?, ?)`,
			"grant-"+id,
			id,
			legacyHostSelfUpdateGrantTokenHash(id),
			"sha256:"+strings.Repeat("3", 64),
			consumedAt,
		); err != nil {
			t.Fatal(err)
		}
	}

	body, err := embeddedMigrations.ReadFile(
		"migrations/057_host_self_update_grant_release_binding.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	runMigration := func() {
		t.Helper()
		for _, statement := range splitSQLStatements(string(body)) {
			if _, err := connection.ExecContext(ctx, statement); err != nil {
				t.Fatalf("execute 057 statement: %v\n%s", err, statement)
			}
		}
	}
	runMigration()
	assertLegacyHostSelfUpdateGrantQuarantined(
		t,
		ctx,
		connection,
		"legacy-queued-revision-drift",
		"queued",
		2,
	)
	assertLegacyHostSelfUpdateGrantQuarantined(
		t,
		ctx,
		connection,
		"legacy-canceled",
		"canceled",
		1,
	)
	var (
		stagingConsumedAt time.Time
		stagingClaim      int64
		stagingClaimedAt  time.Time
	)
	if err := connection.QueryRowContext(
		ctx,
		`SELECT consumed_at, stage_claim_revision, stage_claimed_at
		 FROM system_update_host_self_update_grants
		 WHERE id = 'grant-legacy-staging'`,
	).Scan(
		&stagingConsumedAt,
		&stagingClaim,
		&stagingClaimedAt,
	); err != nil {
		t.Fatal(err)
	}
	if stagingClaim != 2 ||
		!stagingConsumedAt.Equal(consumedAt) ||
		!stagingClaimedAt.Equal(consumedAt) {
		t.Fatalf(
			"reconstructible staging grant was not backfilled: consumed=%v claim=%d claimed=%v",
			stagingConsumedAt,
			stagingClaim,
			stagingClaimedAt,
		)
	}

	// A second execution must be a no-op, including the deterministic
	// quarantine token hash and the claim constraint.
	runMigration()
	assertLegacyHostSelfUpdateGrantQuarantined(
		t,
		ctx,
		connection,
		"legacy-queued-revision-drift",
		"queued",
		2,
	)
	if _, err := connection.ExecContext(
		ctx,
		`UPDATE system_update_host_self_update_grants
		 SET consumed_at = ?, stage_claim_revision = NULL,
		     stage_claimed_at = NULL
		 WHERE id = 'grant-legacy-queued-revision-drift'`,
		consumedAt,
	); err == nil {
		t.Fatal("057 stage-claim constraint accepted an incomplete receipt")
	}
}

func legacyHostSelfUpdateGrantTokenHash(id string) string {
	return strings.Repeat(
		string('a'+rune(len(id)%20)),
		64,
	)
}

func assertLegacyHostSelfUpdateGrantQuarantined(
	t *testing.T,
	ctx context.Context,
	connection *sql.Conn,
	id, wantStatus string,
	wantRevision int64,
) {
	t.Helper()
	var (
		status         string
		revision       int64
		tokenHash      string
		consumedAt     sql.NullTime
		claimRevision  sql.NullInt64
		claimedAt      sql.NullTime
		releaseBinding []byte
	)
	if err := connection.QueryRowContext(
		ctx,
		`SELECT update_row.status, update_row.revision,
		        grant_row.token_hash, grant_row.consumed_at,
		        grant_row.stage_claim_revision,
		        grant_row.stage_claimed_at, grant_row.release_binding
		 FROM system_update_host_self_updates AS update_row
		 INNER JOIN system_update_host_self_update_grants AS grant_row
		   ON grant_row.self_update_id = update_row.id
		 WHERE update_row.id = ?`,
		id,
	).Scan(
		&status,
		&revision,
		&tokenHash,
		&consumedAt,
		&claimRevision,
		&claimedAt,
		&releaseBinding,
	); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus ||
		revision != wantRevision ||
		tokenHash == legacyHostSelfUpdateGrantTokenHash(id) ||
		consumedAt.Valid ||
		claimRevision.Valid ||
		claimedAt.Valid ||
		len(releaseBinding) == 0 {
		t.Fatalf(
			"legacy grant was not quarantined: status=%q rev=%d token=%q consumed=%#v claim=%#v claimed=%#v release=%s",
			status,
			revision,
			tokenHash,
			consumedAt,
			claimRevision,
			claimedAt,
			releaseBinding,
		)
	}
	var oldTokenMatches int
	if err := connection.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM system_update_host_self_update_grants
		 WHERE token_hash = ?`,
		legacyHostSelfUpdateGrantTokenHash(id),
	).Scan(&oldTokenMatches); err != nil {
		t.Fatal(err)
	}
	if oldTokenMatches != 0 {
		t.Fatalf("legacy token hash still matches %d grant(s)", oldTokenMatches)
	}
}

func TestHostSelfUpdateGrantReleaseBindingMigrationBackfillsExistingRows(t *testing.T) {
	body, err := embeddedMigrations.ReadFile(
		"migrations/057_host_self_update_grant_release_binding.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	baseBody, err := embeddedMigrations.ReadFile(
		"migrations/053_system_update_host_self_updates.sql",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, additiveOnly := range []string{
		"release_binding",
		"stage_claim_revision",
		"stage_claimed_at",
	} {
		if strings.Contains(string(baseBody), additiveOnly) {
			t.Fatalf(
				"base migration 053 was rewritten with additive field %q",
				additiveOnly,
			)
		}
	}
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS release_binding JSON NULL",
		"ADD COLUMN IF NOT EXISTS stage_claim_revision BIGINT NULL",
		"ADD COLUMN IF NOT EXISTS stage_claimed_at DATETIME(6) NULL",
		"INNER JOIN system_update_host_self_updates",
		"WHERE grant_row.release_binding IS NULL",
		"'manifest_asset_id', update_row.manifest_asset_id",
		"'manifest_checksum_asset_id', update_row.manifest_checksum_asset_id",
		"'archive_asset_id', update_row.archive_asset_id",
		"'archive_checksum_asset_id', update_row.archive_checksum_asset_id",
		"'minimum_panel_version', update_row.minimum_panel_version",
		"update_row.status IN ('queued', 'canceled')",
		"grant_row.token_hash = SHA2(",
		"grant_row.consumed_at = NULL",
		"stage_claim_revision = expected_self_update_revision + 1",
		"ck_system_update_host_self_update_grants_stage_claim",
		"MODIFY COLUMN release_binding JSON NOT NULL",
		"DROP CONSTRAINT IF EXISTS ck_system_update_host_self_update_grants_stage_claim",
		"stage_claim_revision IS NOT NULL",
		"stage_claimed_at IS NOT NULL",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("release binding migration is missing %q:\n%s", required, text)
		}
	}
	quarantineAt := strings.Index(
		text,
		"grant_row.token_hash = SHA2(",
	)
	backfillAt := strings.Index(
		text,
		"stage_claim_revision = expected_self_update_revision + 1",
	)
	if quarantineAt < 0 || backfillAt < 0 || quarantineAt >= backfillAt {
		t.Fatalf(
			"legacy queued or canceled grants must be quarantined before stage claim backfill:\n%s",
			text,
		)
	}
	if strings.Contains(text, "update_row.status = 'staging'") {
		t.Fatalf(
			"legacy queued grants must be quarantined, not reconstructed from a drifted revision:\n%s",
			text,
		)
	}
	for _, forbidden := range []string{
		"download_url",
		"github_token",
		"release_token",
		"attestation_verified_at",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf(
				"release binding migration stores forbidden field %q",
				forbidden,
			)
		}
	}
}
