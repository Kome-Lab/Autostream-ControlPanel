package database

import (
	"strings"
	"testing"
)

func TestSplitSQLStatements(t *testing.T) {
	got := splitSQLStatements("CREATE TABLE a (id INT);\n\nCREATE TABLE b (id INT);")
	if len(got) != 2 {
		t.Fatalf("got %d statements: %#v", len(got), got)
	}
}

func TestStreamArtifactUniqueMigrationDeduplicatesBeforeIndex(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/005_stream_artifacts_unique_key.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	dedupeAt := strings.Index(text, "DELETE stale_artifacts")
	indexAt := strings.Index(text, "uniq_stream_artifacts_stream_kind_name")
	if dedupeAt < 0 {
		t.Fatalf("stream artifact unique migration must remove historical duplicates before adding the unique key:\n%s", text)
	}
	if indexAt < 0 {
		t.Fatalf("stream artifact unique migration must add the expected unique key:\n%s", text)
	}
	if dedupeAt > indexAt {
		t.Fatalf("stream artifact unique migration must deduplicate before adding the unique key:\n%s", text)
	}
}

func TestPasskeyCeremonySessionUserForeignKeyMatchesUsersTable(t *testing.T) {
	initBody, err := embeddedMigrations.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	initText := string(initBody)
	if !strings.Contains(initText, "id CHAR(36) PRIMARY KEY") {
		t.Fatalf("users.id type changed; passkey ceremony FK compatibility test must be updated:\n%s", initText)
	}

	createBody, err := embeddedMigrations.ReadFile("migrations/017_webauthn_ceremony_sessions.sql")
	if err != nil {
		t.Fatal(err)
	}
	createText := string(createBody)
	if !strings.Contains(createText, "user_id CHAR(36) NULL") {
		t.Fatalf("webauthn ceremony user_id must match users.id for MariaDB FK compatibility:\n%s", createText)
	}
	if strings.Contains(createText, "DEFAULT CHARSET") {
		t.Fatalf("webauthn ceremony table must inherit the database charset/collation used by users:\n%s", createText)
	}
	if strings.Contains(createText, "ENGINE=") {
		t.Fatalf("webauthn ceremony table must inherit the database storage engine used by users:\n%s", createText)
	}

	alterBody, err := embeddedMigrations.ReadFile("migrations/018_webauthn_ceremony_sessions_nullable_user.sql")
	if err != nil {
		t.Fatal(err)
	}
	alterText := string(alterBody)
	dropAt := strings.Index(alterText, "DROP FOREIGN KEY fk_webauthn_ceremony_sessions_user")
	modifyAt := strings.Index(alterText, "MODIFY user_id CHAR(36) NULL")
	addAt := strings.Index(alterText, "ADD CONSTRAINT fk_webauthn_ceremony_sessions_user")
	if dropAt < 0 || modifyAt < 0 || addAt < 0 || !(dropAt < modifyAt && modifyAt < addAt) {
		t.Fatalf("migration 018 must rebuild the FK around the compatible user_id type:\n%s", alterText)
	}
}

func TestNodeAgentRegistrationMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/025_node_agent_registration.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		normalized := strings.ToUpper(stmt)
		if strings.HasPrefix(normalized, "ALTER TABLE SERVICES ADD COLUMN ") && !strings.Contains(normalized, "ADD COLUMN IF NOT EXISTS ") {
			t.Fatalf("node agent registration migration must tolerate partially upgraded services tables:\n%s", stmt)
		}
	}
}

func TestStreamArchiveDirectSettingsMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/026_stream_archive_direct_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		normalized := strings.ToUpper(stmt)
		if strings.HasPrefix(normalized, "ALTER TABLE STREAM_SETTINGS ADD COLUMN ") && !strings.Contains(normalized, "ADD COLUMN IF NOT EXISTS ") {
			t.Fatalf("stream archive direct settings migration must tolerate partially upgraded stream_settings tables:\n%s", stmt)
		}
	}
}

func TestOAuthLoginStateRequestedScopesMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/027_oauth_login_state_requested_scopes.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		normalized := strings.ToUpper(stmt)
		if strings.Contains(normalized, "ADD COLUMN REQUESTED_SCOPES") && !strings.Contains(normalized, "ADD COLUMN IF NOT EXISTS REQUESTED_SCOPES") {
			t.Fatalf("oauth login state requested scopes migration must tolerate partially upgraded oauth_login_states tables:\n%s", stmt)
		}
	}
}

func TestOAuthLoginStatePurposeMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/028_oauth_login_state_purpose.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		normalized := strings.ToUpper(stmt)
		if strings.Contains(normalized, "ADD COLUMN PURPOSE") && !strings.Contains(normalized, "ADD COLUMN IF NOT EXISTS PURPOSE") {
			t.Fatalf("oauth login state purpose migration must tolerate partially upgraded oauth_login_states tables:\n%s", stmt)
		}
	}
}

func TestUserAvatarMigrationUsesBoundedBinaryStorageAndUserCascade(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/037_user_avatars.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"user_id CHAR(36) PRIMARY KEY", "image_data MEDIUMBLOB NOT NULL", "fingerprint CHAR(64) NOT NULL", "REFERENCES users(id) ON DELETE CASCADE"} {
		if !strings.Contains(text, required) {
			t.Fatalf("avatar migration is missing %q:\n%s", required, text)
		}
	}
}

func TestAuditResultMigrationAcceptsNonBinaryAuditOutcomes(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/038_audit_result.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToUpper(string(body))
	if !strings.Contains(text, "ALTER TABLE AUDIT_LOGS") || !strings.Contains(text, "MODIFY COLUMN RESULT VARCHAR(32) NOT NULL") {
		t.Fatalf("audit result migration must replace the success/failure enum with a bounded string column:\n%s", string(body))
	}
}

func TestSystemUpdateJobsMigrationEnforcesSingleActiveTargetAndPermissions(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/039_system_update_jobs.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"ALTER TABLE service_tokens", "ALTER TABLE services", "ALTER TABLE service_metric_snapshots", "'update_agent'", "active_target_id", "uq_system_update_jobs_active_target", "executing_agent_service_id", "uq_system_update_jobs_executing_agent", "uq_system_update_jobs_idempotency", "lease_generation", "reconciling", "lease_token_hash", "lease_expires_at", "system_updates.read", "system_updates.execute"} {
		if !strings.Contains(text, required) {
			t.Fatalf("system update migration is missing %q:\n%s", required, text)
		}
	}
}

func TestSystemUpdateHostSlotMigrationReplacesAgentWideExecutionUniqueness(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/040_system_update_host_slots.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"execution_host_id VARCHAR(191)",
		"COALESCE(NULLIF(TRIM(agent_service_id), ''), target_id)",
		"MODIFY COLUMN execution_host_id VARCHAR(191) NOT NULL",
		"DROP INDEX IF EXISTS uq_system_update_jobs_executing_agent",
		"executing_host_id VARCHAR(191) GENERATED ALWAYS AS",
		"uq_system_update_jobs_executing_agent_host",
		"(executing_agent_service_id, executing_host_id)",
		"idx_system_update_jobs_agent_host_claim",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("system update host slot migration is missing %q:\n%s", required, text)
		}
	}
	backfillAt := strings.Index(text, "SET execution_host_id")
	notNullAt := strings.Index(text, "MODIFY COLUMN execution_host_id")
	dropAgentUniqueAt := strings.Index(text, "DROP INDEX IF EXISTS uq_system_update_jobs_executing_agent")
	hostUniqueAt := strings.Index(text, "CREATE UNIQUE INDEX IF NOT EXISTS uq_system_update_jobs_executing_agent_host")
	if backfillAt < 0 || notNullAt < 0 || dropAgentUniqueAt < 0 || hostUniqueAt < 0 || !(backfillAt < notNullAt && notNullAt < dropAgentUniqueAt && dropAgentUniqueAt < hostUniqueAt) {
		t.Fatalf("system update host migration must backfill before NOT NULL and replace the old unique key before creating the host lane key:\n%s", text)
	}
}

func TestSystemUpdateMutationGrantMigrationStoresOnlyHashedSingleUseTokens(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/041_system_update_mutation_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS system_update_mutation_grants",
		"token_hash CHAR(64) NOT NULL",
		"UNIQUE KEY uq_system_update_mutation_grants_token_hash",
		"lease_generation BIGINT NOT NULL",
		"host_id VARCHAR(191) NOT NULL",
		"operation VARCHAR(16) NOT NULL",
		"plan_sha256 CHAR(64) NOT NULL",
		"session_id VARCHAR(128) NOT NULL",
		"expires_at DATETIME(6) NOT NULL",
		"consumed_at DATETIME(6) NULL",
		"REFERENCES system_update_jobs(id) ON DELETE CASCADE",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("system update mutation grant migration is missing %q:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{"grant_token", "lease_token VARCHAR", "runtime_token"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("system update mutation grant migration must not persist raw secret column %q:\n%s", forbidden, text)
		}
	}
}

func TestUpdateAgentStagedTokenMigrationStoresNoRawSecrets(t *testing.T) {
	initBody, err := embeddedMigrations.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(initBody), "staged_node_token_id") {
		t.Fatal("staged updater columns must be introduced by migration 042 so the real upgrade path is exercised")
	}

	body, err := embeddedMigrations.ReadFile("migrations/042_update_agent_staged_token.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"staged_node_previous_token_id",
		"staged_node_token_id",
		"staged_node_token_hash CHAR(64)",
		"staged_node_token_scopes JSON",
		"staged_node_token_ciphertext TEXT",
		"staged_node_token_nonce VARCHAR(128)",
		"staged_node_activation_token_hash CHAR(64)",
		"staged_node_token_at DATETIME(6)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("staged token migration missing %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{"runtime_token TEXT", "activation_token TEXT", "configure_token TEXT"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("staged token migration persists raw secret column %q: %s", forbidden, text)
		}
	}
}
