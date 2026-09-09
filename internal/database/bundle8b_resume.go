package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var bundle8BDiscordColumns = []string{"discord_target_mode", "discord_target_preset_id", "discord_target_preset_revision", "discord_guild_id", "discord_text_channel_id", "discord_voice_channel_id"}

// Preserve the distributed SQL as history. Its data-copy statements execute
// against durable source snapshots, so restart never selects deleted columns.
// The original destructive DDL and its ordering remain in the runner path.
func applyBundle8BPhysicalEOL(ctx context.Context, conn *sql.Conn, body string) error {
	if err := bundle8BCheckpoint(ctx, conn, false); err != nil {
		return err
	}
	if err := correctArchiveMapping(ctx, conn); err != nil {
		return err
	}
	discordCount, err := bundle8BColumnCount(ctx, conn, "stream_settings", bundle8BDiscordColumns)
	if err != nil {
		return err
	}
	hostCount, err := bundle8BColumnCount(ctx, conn, "system_update_execution_hosts", []string{"legacy_agent_service_id"})
	if err != nil {
		return err
	}
	if discordCount != 0 && discordCount != len(bundle8BDiscordColumns) {
		return errors.New("partial Discord source schema")
	}
	if discordCount == 0 || hostCount == 0 {
		if err := requireBundle8BZeroGate(ctx, conn, "v2_migration_bundle8b_gate"); err != nil {
			return err
		}
	}
	if err := prepareBundle8BSources(ctx, conn, discordCount != 0, hostCount != 0); err != nil {
		return err
	}
	// For an old-runner interruption, missing replacements cannot be recreated
	// from a deleted source and then counted as a successful resume.
	if discordCount == 0 || hostCount == 0 {
		if err := validateBundle8BReplacement(ctx, conn); err != nil {
			return err
		}
	}
	for _, original := range splitSQLStatements(body) {
		stmt := trimMigrationComments(original)
		switch {
		case strings.HasPrefix(stmt, "ALTER TABLE v2_migration_archive_artifacts_backup"),
			strings.HasPrefix(stmt, "CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_artifact_gate"),
			strings.HasPrefix(stmt, "INSERT INTO v2_migration_bundle8b_artifact_gate"),
			strings.HasPrefix(stmt, "UPDATE v2_migration_archive_artifacts_backup"),
			strings.HasPrefix(stmt, "UPDATE stream_artifacts"):
			continue // Replaced by the checked, transactional archive mapping above.
		case strings.HasPrefix(stmt, "INSERT INTO v2_migration_bundle8b_gate"):
			if err := validateBundle8BReplacement(ctx, conn); err != nil {
				return err
			}
			if err := requireBundle8BZeroGate(ctx, conn, "v2_migration_bundle8a_gate"); err != nil {
				return err
			}
			if err := requireBundle8BZeroGate(ctx, conn, "v2_migration_bundle8b_artifact_gate"); err != nil {
				return err
			}
			stmt = `INSERT INTO v2_migration_bundle8b_gate (gate_id,mismatch_count,verified_at)
 VALUES (1,0,CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE mismatch_count=0,verified_at=VALUES(verified_at)`
		case strings.HasPrefix(stmt, "INSERT INTO v2_migration_legacy_agent_export"):
			stmt, _, _ = strings.Cut(stmt, "ON DUPLICATE KEY UPDATE")
			stmt = strings.Replace(stmt, "INSERT INTO", "INSERT IGNORE INTO", 1)
			stmt = strings.ReplaceAll(stmt, "FROM system_update_execution_hosts", "FROM v2_migration_bundle8b_host_source")
		}
		stmt = strings.ReplaceAll(stmt, "FROM stream_settings AS legacy", "FROM v2_migration_bundle8b_discord_source AS legacy")
		stmt = strings.ReplaceAll(stmt, "JOIN stream_settings AS legacy", "JOIN v2_migration_bundle8b_discord_source AS legacy")
		if strings.HasPrefix(stmt, "DROP VIEW") {
			if err := bundle8BCheckpoint(ctx, conn, true); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := validateBundle8BReplacement(ctx, conn); err != nil {
		return err
	}
	if err := bundle8BCheckpoint(ctx, conn, false); err != nil {
		return err
	}
	for table, columns := range map[string][]string{"stream_settings": bundle8BDiscordColumns, "system_update_execution_hosts": {"legacy_agent_service_id"}} {
		count, err := bundle8BColumnCount(ctx, conn, table, columns)
		if err != nil {
			return err
		}
		if count != 0 {
			return errors.New("Bundle 8B final schema mismatch")
		}
	}
	if err := bundle8BNoMismatch(ctx, conn, `SELECT
 (SELECT COUNT(*) FROM information_schema.views WHERE table_schema=DATABASE() AND table_name='v2_migration_bundle8a_counts') +
 (SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE()
 AND constraint_name IN ('fk_stream_settings_discord_target_preset','fk_system_update_execution_hosts_legacy_agent')) +
 (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='stream_settings'
 AND index_name='idx_stream_settings_discord_target_preset')`); err != nil {
		return err
	}
	return nil
}

func trimMigrationComments(stmt string) string {
	for strings.HasPrefix(strings.TrimSpace(stmt), "--") {
		_, stmt, _ = strings.Cut(strings.TrimSpace(stmt), "\n")
	}
	return strings.TrimSpace(stmt)
}

func bundle8BColumnCount(ctx context.Context, conn *sql.Conn, table string, columns []string) (int, error) {
	args := []any{table}
	placeholders := make([]string, len(columns))
	for i, c := range columns {
		args = append(args, c)
		placeholders[i] = "?"
	}
	var count int
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE()
 AND table_name=? AND column_name IN (`+strings.Join(placeholders, ",")+`)`, args...).Scan(&count)
	return count, err
}

func requireBundle8BZeroGate(ctx context.Context, conn *sql.Conn, table string) error {
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE gate_id=1 AND mismatch_count=0").Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("Bundle 8B prerequisite gate missing")
	}
	return nil
}

func prepareBundle8BSources(ctx context.Context, conn *sql.Conn, discordPresent, hostPresent bool) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_discord_source (
 stream_id CHAR(36) PRIMARY KEY, discord_target_mode VARCHAR(16) NULL,discord_target_preset_id CHAR(36) NULL,
 discord_target_preset_revision BIGINT UNSIGNED NULL,discord_guild_id VARCHAR(255) NULL,
 discord_text_channel_id VARCHAR(255) NULL,discord_voice_channel_id VARCHAR(255) NULL,updated_at DATETIME NOT NULL)`); err != nil {
		return err
	}
	discordSelect := `SELECT stream_id,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,
 discord_guild_id,discord_text_channel_id,discord_voice_channel_id,updated_at FROM stream_settings`
	if !discordPresent {
		// Recover only from retained pre-transform values; an arbitrary current
		// replacement never supplies its own expected IDs or preset revision.
		discordSelect = `SELECT backup.stream_id,
 CASE WHEN backup.settings_target_mode IS NOT NULL THEN backup.settings_target_mode
 WHEN backup.visual_target_mode IS NOT NULL THEN backup.visual_target_mode
 WHEN COALESCE(TRIM(backup.discord_guild_id),'')<>'' THEN 'manual'
 WHEN v2.stream_id IS NOT NULL THEN 'inherit' ELSE NULL END,
 CASE WHEN backup.settings_target_mode IS NOT NULL THEN backup.settings_target_preset_id ELSE backup.visual_target_preset_id END,
 CASE WHEN backup.settings_target_mode IS NOT NULL THEN backup.settings_target_preset_revision ELSE backup.visual_target_preset_revision END,
 CASE WHEN backup.settings_target_mode IS NULL AND backup.visual_target_mode IS NOT NULL THEN backup.visual_guild_id ELSE NULLIF(TRIM(backup.discord_guild_id),'') END,
 CASE WHEN backup.settings_target_mode IS NULL AND backup.visual_target_mode IS NOT NULL THEN backup.visual_text_channel_id ELSE NULLIF(TRIM(backup.discord_text_channel_id),'') END,
 CASE WHEN backup.settings_target_mode IS NULL AND backup.visual_target_mode IS NOT NULL THEN backup.visual_voice_channel_id ELSE NULLIF(TRIM(backup.discord_voice_channel_id),'') END,
 current.updated_at FROM v2_migration_discord_targets_backup AS backup
 JOIN stream_settings AS current ON current.stream_id=backup.stream_id
 LEFT JOIN stream_visual_settings AS v2 ON v2.stream_id=backup.stream_id`
	}
	if _, err := conn.ExecContext(ctx, `INSERT IGNORE INTO v2_migration_bundle8b_discord_source `+discordSelect); err != nil {
		return err
	}
	if discordPresent {
		if err := bundle8BNoMismatch(ctx, conn, `SELECT COUNT(*) FROM stream_settings AS current
 LEFT JOIN v2_migration_bundle8b_discord_source AS saved ON saved.stream_id=current.stream_id
 WHERE saved.stream_id IS NULL OR NOT(current.discord_target_mode <=> saved.discord_target_mode)
 OR NOT(current.discord_target_preset_id <=> saved.discord_target_preset_id)
 OR NOT(current.discord_target_preset_revision <=> saved.discord_target_preset_revision)
 OR NOT(current.discord_guild_id <=> saved.discord_guild_id)
 OR NOT(current.discord_text_channel_id <=> saved.discord_text_channel_id)
 OR NOT(current.discord_voice_channel_id <=> saved.discord_voice_channel_id)`); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_host_source (
 execution_host_id VARCHAR(191) PRIMARY KEY,transport_mode VARCHAR(16) NOT NULL,agent_service_id VARCHAR(128) NOT NULL,
 legacy_agent_service_id VARCHAR(128) NULL,ownership_epoch BIGINT NOT NULL,policy_revision BIGINT NOT NULL)`); err != nil {
		return err
	}
	hostSelect := `SELECT execution_host_id,transport_mode,agent_service_id,legacy_agent_service_id,ownership_epoch,policy_revision FROM system_update_execution_hosts`
	if !hostPresent {
		hostSelect = `SELECT current.execution_host_id,current.transport_mode,current.agent_service_id,exported.legacy_agent_service_id,
 current.ownership_epoch,current.policy_revision FROM system_update_execution_hosts AS current
 LEFT JOIN v2_migration_legacy_agent_export AS exported ON exported.execution_host_id=current.execution_host_id`
	}
	if _, err := conn.ExecContext(ctx, `INSERT IGNORE INTO v2_migration_bundle8b_host_source `+hostSelect); err != nil {
		return err
	}
	if hostPresent {
		if err := bundle8BNoMismatch(ctx, conn, `SELECT COUNT(*) FROM system_update_execution_hosts AS current
 JOIN v2_migration_bundle8b_host_source AS saved ON saved.execution_host_id=current.execution_host_id
 WHERE NOT(current.legacy_agent_service_id <=> saved.legacy_agent_service_id)`); err != nil {
			return err
		}
	}
	return nil
}

func bundle8BNoMismatch(ctx context.Context, conn *sql.Conn, query string) error {
	var count int
	if err := conn.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("Bundle 8B retained source or replacement mismatch")
	}
	return nil
}

func validateBundle8BReplacement(ctx context.Context, conn *sql.Conn) error {
	for _, query := range []string{
		`SELECT COUNT(*) FROM stream_settings AS current LEFT JOIN v2_migration_bundle8b_discord_source AS saved ON saved.stream_id=current.stream_id WHERE saved.stream_id IS NULL`,
		`SELECT COUNT(*) FROM v2_migration_bundle8b_discord_source AS saved LEFT JOIN stream_settings AS current ON current.stream_id=saved.stream_id
 LEFT JOIN stream_visual_settings AS v2 ON v2.stream_id=saved.stream_id
 WHERE current.stream_id IS NULL OR ((saved.discord_target_mode IS NOT NULL OR saved.discord_target_preset_id IS NOT NULL
 OR saved.discord_target_preset_revision IS NOT NULL OR saved.discord_guild_id IS NOT NULL
 OR saved.discord_text_channel_id IS NOT NULL OR saved.discord_voice_channel_id IS NOT NULL) AND
 (v2.stream_id IS NULL OR NOT(saved.discord_target_mode <=> v2.discord_target_mode)
 OR NOT(saved.discord_target_preset_id <=> v2.discord_target_preset_id)
 OR NOT(saved.discord_target_preset_revision <=> v2.discord_target_preset_revision)
 OR COALESCE(TRIM(saved.discord_guild_id),'')<>COALESCE(v2.discord_guild_id,'')
 OR COALESCE(TRIM(saved.discord_text_channel_id),'')<>COALESCE(v2.discord_text_channel_id,'')
 OR COALESCE(TRIM(saved.discord_voice_channel_id),'')<>COALESCE(v2.discord_voice_channel_id,'')))`,
		`SELECT COUNT(*) FROM v2_migration_discord_targets_backup AS backup WHERE source_fingerprint<>SHA2(CONCAT_WS('|',stream_id,
 COALESCE(settings_target_mode,''),COALESCE(settings_target_preset_id,''),COALESCE(settings_target_preset_revision,0),
 COALESCE(discord_guild_id,''),COALESCE(discord_text_channel_id,''),COALESCE(discord_voice_channel_id,''),IF(visual_row_existed,1,0),
 COALESCE(visual_target_mode,''),COALESCE(visual_target_preset_id,''),COALESCE(visual_target_preset_revision,0),
 COALESCE(visual_guild_id,''),COALESCE(visual_text_channel_id,''),COALESCE(visual_voice_channel_id,'')),256)`,
		`SELECT COUNT(*) FROM system_update_execution_hosts AS current
 LEFT JOIN v2_migration_bundle8b_host_source AS saved ON saved.execution_host_id=current.execution_host_id
 LEFT JOIN v2_migration_legacy_agent_export AS exported ON exported.execution_host_id=current.execution_host_id
 WHERE saved.execution_host_id IS NULL OR current.transport_mode<>'pull_v2' OR TRIM(current.agent_service_id)=''
 OR current.transport_mode<>saved.transport_mode OR current.agent_service_id<>saved.agent_service_id
 OR current.ownership_epoch<>saved.ownership_epoch OR current.policy_revision<>saved.policy_revision
 OR (saved.legacy_agent_service_id IS NOT NULL AND (exported.execution_host_id IS NULL
 OR NOT(saved.legacy_agent_service_id <=> exported.legacy_agent_service_id)))`,
		`SELECT COUNT(*) FROM v2_migration_legacy_agent_export AS exported LEFT JOIN system_update_execution_hosts AS current ON current.execution_host_id=exported.execution_host_id
 WHERE current.execution_host_id IS NULL OR exported.source_fingerprint<>SHA2(CONCAT_WS('|',exported.execution_host_id,exported.legacy_agent_service_id),256)`,
		`SELECT COUNT(*) FROM v2_migration_update_hosts_backup AS backup
 LEFT JOIN v2_migration_legacy_agent_export AS exported ON exported.execution_host_id=backup.execution_host_id
 WHERE (backup.legacy_agent_service_id IS NOT NULL AND exported.execution_host_id IS NULL)
 OR backup.source_fingerprint<>SHA2(CONCAT_WS('|',backup.execution_host_id,backup.transport_mode,backup.agent_service_id,
 COALESCE(backup.legacy_agent_service_id,''),backup.ownership_epoch,backup.policy_revision),256)`,
	} {
		if err := bundle8BNoMismatch(ctx, conn, query); err != nil {
			return err
		}
	}
	return nil
}

func bundle8BCheckpoint(ctx context.Context, conn *sql.Conn, create bool) error {
	var exists int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='v2_migration_bundle8b_resume_checkpoint'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 && !create {
		return nil
	}
	h := sha256.New()
	for _, table := range []string{"stream_settings", "stream_visual_settings", "system_update_execution_hosts", "stream_artifacts", "drive_destinations",
		"v2_migration_discord_targets_backup", "v2_migration_archive_artifacts_backup", "v2_migration_legacy_agent_export",
		"v2_migration_bundle8b_discord_source", "v2_migration_bundle8b_host_source"} {
		rows, err := conn.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY 1")
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		if _, err := h.Write([]byte(table + "\n")); err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			args := make([]any, len(columns))
			for i := range args {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				rows.Close()
				return err
			}
			retained := make([]any, 0, len(columns))
			for i, column := range columns {
				if table == "system_update_execution_hosts" && column == "legacy_agent_service_id" {
					continue
				}
				legacy := false
				if table == "stream_settings" {
					for _, c := range bundle8BDiscordColumns {
						if column == c {
							legacy = true
							break
						}
					}
				}
				if !legacy {
					retained = append(retained, values[i])
				}
			}
			if err := json.NewEncoder(h).Encode(retained); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if create {
		if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_resume_checkpoint (gate_id TINYINT PRIMARY KEY,state_digest CHAR(64) NOT NULL)`); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT IGNORE INTO v2_migration_bundle8b_resume_checkpoint VALUES (1,?)`, digest); err != nil {
			return err
		}
	}
	var matches bool
	if err := conn.QueryRowContext(ctx, `SELECT state_digest=? FROM v2_migration_bundle8b_resume_checkpoint WHERE gate_id=1`, digest).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("Bundle 8B retained state changed after EOL gate")
	}
	return nil
}
