package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSplitSQLStatements(t *testing.T) {
	got := splitSQLStatements("CREATE TABLE a (id INT);\n\nCREATE TABLE b (id INT);")
	if len(got) != 2 {
		t.Fatalf("got %d statements: %#v", len(got), got)
	}
}

func TestSplitSQLStatementsKeepsSemicolonsInCommentsAndQuotedText(t *testing.T) {
	got := splitSQLStatements("-- release note; do not split here\nUPDATE services SET name = 'semi;colon';\n/* another; comment */ UPDATE services SET name = `quoted;name`;")
	if len(got) != 2 {
		t.Fatalf("got %d statements: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "release note; do not split here") || !strings.Contains(got[0], "'semi;colon'") {
		t.Fatalf("first statement lost protected semicolon content: %q", got[0])
	}
	if !strings.Contains(got[1], "another; comment") || !strings.Contains(got[1], "`quoted;name`") {
		t.Fatalf("second statement lost protected semicolon content: %q", got[1])
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

func TestArchiveProcessingEventLookupMigrationIndexesStreamAndEventType(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/074_archive_processing_event_lookup.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"create index if not exists idx_service_stream_events_stream_type",
		"on service_stream_events (stream_id, event_type)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("archive processing event lookup migration is missing %q:\n%s", required, string(body))
		}
	}
}

func TestStreamArchiveRunsMigrationScopesArtifactUniquenessByRun(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/075_stream_archive_runs.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{
		"archive_run_id varchar(128) not null default ''",
		"archive_started_at datetime(6) null",
		"archive_reported_at datetime(6) null",
		"drop index if exists uniq_stream_artifacts_stream_kind_name",
		"uniq_stream_artifacts_stream_run_kind_name",
		"(stream_id, archive_run_id, kind, name)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("stream archive run migration is missing %q:\n%s", required, string(body))
		}
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

func TestOAuthLoginStateTargetAccountMigrationIsIdempotent(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/062_oauth_login_state_target_account.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		normalized := strings.ToUpper(stmt)
		if strings.Contains(normalized, "ADD COLUMN TARGET_ACCOUNT_ID") && !strings.Contains(normalized, "ADD COLUMN IF NOT EXISTS TARGET_ACCOUNT_ID") {
			t.Fatalf("oauth login state target account migration must tolerate partially upgraded oauth_login_states tables:\n%s", stmt)
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

func TestServiceEndpointStateMigrationSeparatesDesiredAppliedAndReported(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/044_service_endpoint_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"transport_mode VARCHAR(16)",
		"execution_host_id VARCHAR(191)",
		"ownership_epoch BIGINT",
		"desired_host VARCHAR(255)",
		"desired_port INT",
		"desired_ssl_enabled BOOLEAN",
		"desired_public_url TEXT",
		"reported_api_host VARCHAR(255)",
		"reported_api_port INT",
		"reported_api_ssl_enabled BOOLEAN",
		"reported_api_public_url TEXT",
		"endpoint_revision BIGINT",
		"endpoint_status VARCHAR(32)",
		"SET desired_host = host",
		"SET transport_mode = 'ssh_v1'",
		"uq_services_execution_host",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("service endpoint state migration is missing %q:\n%s", required, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "runtime_token") {
		t.Fatalf("service endpoint state migration must not persist runtime tokens:\n%s", text)
	}
}

func TestBundle8AV2MigrationIsAdditiveAndBackupFirst(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/080_bundle8a_v2_migration.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, table := range []string{
		"v2_migration_drive_destinations_backup",
		"v2_migration_archive_artifacts_backup",
		"v2_migration_stream_key_refs_backup",
		"v2_migration_service_tokens_backup",
		"v2_migration_oauth_accounts_backup",
		"v2_migration_discord_targets_backup",
		"v2_migration_update_hosts_backup",
		"v2_migration_legacy_agent_export",
		"v2_migration_bundle8a_gate",
	} {
		if !strings.Contains(text, "create table if not exists "+table) {
			t.Fatalf("migration is missing additive backup/export table %s", table)
		}
	}
	for _, id := range []string{
		"dep-con-0001", "dep-con-0003", "dep-con-0005", "dep-con-0012",
		"dep-cp-0005", "dep-cp-0006", "dep-cp-0007", "dep-cp-0017", "dep-cp-0030",
	} {
		if !strings.Contains(text, "'"+id+"'") {
			t.Fatalf("migration count view is missing %s", id)
		}
	}
	for _, statement := range splitSQLStatements(string(body)) {
		normalized := strings.ToLower(strings.TrimSpace(statement))
		for _, forbidden := range []string{"drop table", "drop column", "truncate table", "delete from"} {
			if strings.Contains(normalized, forbidden) {
				t.Fatalf("Bundle 8A migration contains physical deletion %q:\n%s", forbidden, statement)
			}
		}
	}
	archiveBackup := strings.Index(text, "insert ignore into v2_migration_archive_artifacts_backup")
	archiveTransform := strings.Index(text, "update stream_artifacts as artifact")
	discordBackup := strings.Index(text, "insert ignore into v2_migration_discord_targets_backup")
	discordTransform := strings.Index(text, "insert ignore into stream_visual_settings")
	if archiveBackup < 0 || archiveTransform < 0 || archiveBackup >= archiveTransform ||
		discordBackup < 0 || discordTransform < 0 || discordBackup >= discordTransform {
		t.Fatal("persistent data transforms must occur after their immutable backup statements")
	}
	for _, preserved := range []string{"base_path", "legacy_agent_service_id", "stream_key_secret_name"} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("migration does not represent retained compatibility data %s", preserved)
		}
	}
	if !strings.Contains(text, "chk_v2_migration_bundle8a_zero_mismatch") || !strings.Contains(text, "orphan_count <> 0") {
		t.Fatal("migration must fail closed when pre/post/backup counts or orphan checks mismatch")
	}
}

func TestMariaDBBundle8AV2MigrationRehearsal(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	now := time.Now().UTC()
	streamID := controlPlatformTestUUID(t)
	artifactID := controlPlatformTestUUID(t)
	driveID := controlPlatformTestUUID(t)
	providerID := controlPlatformTestUUID(t)
	oauthID := controlPlatformTestUUID(t)
	tokenID := controlPlatformTestUUID(t)
	agentID := "bundle8a-agent-" + streamID[:8]
	hostID := "bundle8a-host-" + streamID[:8]

	t.Cleanup(func() {
		cleanup := []struct {
			query string
			args  []any
		}{
			{"DELETE FROM v2_migration_legacy_agent_export WHERE execution_host_id=?", []any{hostID}},
			{"DELETE FROM v2_migration_update_hosts_backup WHERE execution_host_id=?", []any{hostID}},
			{"DELETE FROM v2_migration_discord_targets_backup WHERE stream_id=?", []any{streamID}},
			{"DELETE FROM v2_migration_oauth_accounts_backup WHERE id=?", []any{oauthID}},
			{"DELETE FROM v2_migration_stream_key_refs_backup WHERE stream_id=?", []any{streamID}},
			{"DELETE FROM v2_migration_service_tokens_backup WHERE token_id=?", []any{tokenID}},
			{"DELETE FROM v2_migration_archive_artifacts_backup WHERE artifact_id=?", []any{artifactID}},
			{"DELETE FROM v2_migration_drive_destinations_backup WHERE id=?", []any{driveID}},
			{"DELETE FROM stream_visual_settings WHERE stream_id=?", []any{streamID}},
			{"DELETE FROM stream_artifacts WHERE id=?", []any{artifactID}},
			{"DELETE FROM stream_youtube_runtimes WHERE stream_id=?", []any{streamID}},
			{"DELETE FROM stream_settings WHERE stream_id=?", []any{streamID}},
			{"DELETE FROM streams WHERE id=?", []any{streamID}},
			{"DELETE FROM oauth_accounts WHERE id=?", []any{oauthID}},
			{"DELETE FROM oauth_providers WHERE id=?", []any{providerID}},
			{"DELETE FROM drive_destinations WHERE id=?", []any{driveID}},
			{"DELETE FROM system_update_execution_hosts WHERE execution_host_id=?", []any{hostID}},
			{"DELETE FROM services WHERE service_id=?", []any{agentID}},
			{"DELETE FROM service_tokens WHERE id=?", []any{tokenID}},
		}
		for _, current := range cleanup {
			_, _ = db.ExecContext(ctx, current.query, current.args...)
		}
	})

	mustExecBundle8A(t, db, ctx, `INSERT INTO streams(id,name,status,created_at,updated_at) VALUES(?,?,'created',?,?)`, streamID, "Bundle 8A rehearsal", now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO stream_settings(stream_id,discord_guild_id,discord_voice_channel_id,discord_text_channel_id,updated_at) VALUES(?,?,?,?,?)`, streamID, "100000000000000001", "100000000000000002", "100000000000000003", now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO stream_artifacts(id,stream_id,archive_run_id,archive_started_at,kind,name,relative_path,size_bytes,created_at) VALUES(?,?,'',NULL,'archive','final.mp4',?,7,?)`, artifactID, streamID, "final/"+streamID+"/final.mp4", now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO stream_youtube_runtimes(stream_id,youtube_output,mode,stream_key_secret_name,created_at,updated_at) VALUES(?,'primary','manual',?,?,?)`, streamID, "stream:"+streamID+":youtube", now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO drive_destinations(id,name,auth_mode,folder_id_ciphertext,folder_id_nonce,folder_id_fingerprint,masked_folder_id,shared_drive,base_path,created_at,updated_at) VALUES(?,?,'oauth2','encrypted','nonce','abcdef0123456789','folder-masked',FALSE,'AutoStream/Archives',?,?)`, driveID, "Bundle 8A "+driveID[:8], now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO oauth_providers(id,provider_type,name,enabled,client_id,scopes,allowed_domains,redirect_uri,created_at,updated_at) VALUES(?,'google',?,TRUE,'client','[]','[]','https://example.com/callback',?,?)`, providerID, "Bundle 8A "+providerID[:8], now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO oauth_accounts(id,provider_id,provider_type,account_label,subject,email,scopes,refresh_token_ciphertext,refresh_token_nonce,token_fingerprint,created_at,updated_at) VALUES(?,?,'google','linked','subject','ops@example.com','[]','encrypted','nonce','abcdef0123456789',?,?)`, oauthID, providerID, now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO service_tokens(id,service_type,token_hash,scopes,created_at) VALUES(?,'update_agent',?,'["system_updates.execute"]',?)`, tokenID, strings.Repeat("a", 64), now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO services(service_id,service_type,service_name,public_url,version,status,capabilities,token_id,transport_mode,execution_host_id,ownership_epoch,created_at,updated_at) VALUES(?,'update_agent',?,'https://agent.example.com','v2','healthy','[]',?,'ssh_v1',?,1,?,?)`, agentID, "Bundle 8A agent", tokenID, hostID, now, now)
	mustExecBundle8A(t, db, ctx, `INSERT INTO system_update_execution_hosts(execution_host_id,transport_mode,agent_service_id,legacy_agent_service_id,ownership_epoch,policy_revision,created_at,updated_at) VALUES(?,'ssh_v1',?,?,1,1,?,?)`, hostID, agentID, agentID, now, now)

	replayEmbeddedMariaDBMigration(t, ctx, db, "migrations/080_bundle8a_v2_migration.sql")
	assertBundle8ACounts(t, db, ctx, 1)

	// Replaying models both an interrupted migration before schema_migrations is
	// recorded and a normal idempotent operator rehearsal.
	replayEmbeddedMariaDBMigration(t, ctx, db, "migrations/080_bundle8a_v2_migration.sql")
	assertBundle8ACounts(t, db, ctx, 1)
	mustExecBundle8A(t, db, ctx, `UPDATE stream_visual_settings SET discord_guild_id='999' WHERE stream_id=?`, streamID)
	var discordPostCount, discordOrphanCount int
	if err := db.QueryRowContext(ctx, `SELECT post_count,orphan_count FROM v2_migration_bundle8a_counts WHERE inventory_id='DEP-CP-0005'`).Scan(&discordPostCount, &discordOrphanCount); err != nil {
		t.Fatal(err)
	}
	if discordPostCount != 0 || discordOrphanCount != 1 {
		t.Fatalf("Discord orphan fixture post=%d orphan=%d, want 0/1", discordPostCount, discordOrphanCount)
	}
	mustExecBundle8A(t, db, ctx, `UPDATE stream_visual_settings SET discord_guild_id='100000000000000001' WHERE stream_id=?`, streamID)
	assertBundle8ACounts(t, db, ctx, 1)

	// Force a post-state mismatch.  The count view must reject it, then the
	// backup-backed restore must recover the exact pre-state.
	mustExecBundle8A(t, db, ctx, `UPDATE stream_artifacts SET archive_run_id='' WHERE id=?`, artifactID)
	var postCount, orphanCount int
	if err := db.QueryRowContext(ctx, `SELECT post_count,orphan_count FROM v2_migration_bundle8a_counts WHERE inventory_id='DEP-CON-0003'`).Scan(&postCount, &orphanCount); err != nil {
		t.Fatal(err)
	}
	if postCount != 0 || orphanCount != 1 {
		t.Fatalf("negative count fixture post=%d orphan=%d, want 0/1", postCount, orphanCount)
	}
	restoreBundle8ARehearsal(t, db, ctx, streamID, artifactID)
	assertBundle8APreState(t, db, ctx, streamID, artifactID)

	// The migration remains executable after restore and returns to a complete,
	// orphan-free state.
	replayEmbeddedMariaDBMigration(t, ctx, db, "migrations/080_bundle8a_v2_migration.sql")
	assertBundle8ACounts(t, db, ctx, 1)
}

func mustExecBundle8A(t *testing.T, db *sql.DB, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("Bundle 8A rehearsal SQL failed: %v\n%s", err, query)
	}
}

func assertBundle8ACounts(t *testing.T, db *sql.DB, ctx context.Context, want int) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT inventory_id,pre_count,backup_count,post_count,orphan_count FROM v2_migration_bundle8a_counts ORDER BY inventory_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id string
		var pre, backup, post, orphan int
		if err := rows.Scan(&id, &pre, &backup, &post, &orphan); err != nil {
			t.Fatal(err)
		}
		if pre != want || backup != want || post != want || orphan != 0 {
			t.Fatalf("%s counts=%d/%d/%d orphan=%d, want %d/%d/%d orphan=0", id, pre, backup, post, orphan, want, want, want)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 9 {
		t.Fatalf("migration count row denominator=%d, want 9", seen)
	}
}

func restoreBundle8ARehearsal(t *testing.T, db *sql.DB, ctx context.Context, streamID, artifactID string) {
	t.Helper()
	mustExecBundle8A(t, db, ctx, `UPDATE stream_artifacts AS current JOIN v2_migration_archive_artifacts_backup AS backup ON backup.artifact_id=current.id SET current.archive_run_id=backup.archive_run_id,current.archive_started_at=backup.archive_started_at WHERE current.id=?`, artifactID)
	mustExecBundle8A(t, db, ctx, `UPDATE stream_settings AS current JOIN v2_migration_discord_targets_backup AS backup ON backup.stream_id=current.stream_id SET current.discord_target_mode=backup.settings_target_mode,current.discord_target_preset_id=backup.settings_target_preset_id,current.discord_target_preset_revision=backup.settings_target_preset_revision,current.discord_guild_id=backup.discord_guild_id,current.discord_text_channel_id=backup.discord_text_channel_id,current.discord_voice_channel_id=backup.discord_voice_channel_id WHERE current.stream_id=?`, streamID)
	mustExecBundle8A(t, db, ctx, `DELETE visual FROM stream_visual_settings AS visual JOIN v2_migration_discord_targets_backup AS backup ON backup.stream_id=visual.stream_id WHERE backup.visual_row_existed=FALSE AND visual.stream_id=?`, streamID)
}

func assertBundle8APreState(t *testing.T, db *sql.DB, ctx context.Context, streamID, artifactID string) {
	t.Helper()
	var runID string
	var startedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT archive_run_id,archive_started_at FROM stream_artifacts WHERE id=?`, artifactID).Scan(&runID, &startedAt); err != nil {
		t.Fatal(err)
	}
	var mode sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT discord_target_mode FROM stream_settings WHERE stream_id=?`, streamID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	var visualCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_visual_settings WHERE stream_id=?`, streamID).Scan(&visualCount); err != nil {
		t.Fatal(err)
	}
	if runID != "" || startedAt.Valid || mode.Valid || visualCount != 0 {
		t.Fatalf("restore did not reproduce pre-state: run=%q started=%v mode=%v visual=%d", runID, startedAt, mode, visualCount)
	}
}
