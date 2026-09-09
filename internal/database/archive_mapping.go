package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var archiveStreamID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type archiveMapping struct {
	id, streamID, name, path, runID     string
	started, created                    sql.NullTime
	backupID, backupRun, backupPath     sql.NullString
	backupStarted                       sql.NullTime
	backupPathValid                     bool
	originalPath, targetPath, targetRun string
}

// 080's immutable backup includes existing runs with a missing timestamp as
// well as genuinely flat files. Only the latter belong to Encoder's manifest.
// This also corrects an already-recorded 081 using its saved original path.
func correctArchiveMapping(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `ALTER TABLE v2_migration_archive_artifacts_backup
 ADD COLUMN IF NOT EXISTS relative_path TEXT NULL AFTER archive_started_at,
 ADD COLUMN IF NOT EXISTS relative_path_fingerprint CHAR(64) NULL AFTER relative_path`); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v2_migration_bundle8b_artifact_gate (
 gate_id TINYINT PRIMARY KEY, mismatch_count INT NOT NULL, verified_at DATETIME(6) NOT NULL,
 CONSTRAINT chk_v2_migration_bundle8b_artifact_zero_mismatch CHECK (mismatch_count=0))`); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var invalidBackup int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM v2_migration_archive_artifacts_backup AS backup
 LEFT JOIN stream_artifacts AS current ON current.id=backup.artifact_id
 WHERE current.id IS NULL OR backup.source_fingerprint <> SHA2(CONCAT_WS('|', backup.artifact_id,
 backup.archive_run_id, COALESCE(DATE_FORMAT(backup.archive_started_at,'%Y-%m-%dT%H:%i:%s.%f'),'')),256)`).Scan(&invalidBackup); err != nil {
		return err
	}
	if invalidBackup != 0 {
		return errors.New("archive original backup mismatch")
	}
	rows, err := tx.QueryContext(ctx, `SELECT current.id,current.stream_id,current.name,current.relative_path,
 current.archive_run_id,current.archive_started_at,current.created_at,
 backup.artifact_id,backup.archive_run_id,backup.archive_started_at,backup.relative_path,
 IF(backup.relative_path IS NULL, backup.relative_path_fingerprint IS NULL,
 COALESCE(backup.relative_path_fingerprint=SHA2(backup.relative_path,256),FALSE))
 FROM stream_artifacts AS current LEFT JOIN v2_migration_archive_artifacts_backup AS backup ON backup.artifact_id=current.id
 ORDER BY current.id FOR UPDATE`)
	if err != nil {
		return err
	}
	var mappings []archiveMapping
	destinations := map[string]bool{}
	for rows.Next() {
		var m archiveMapping
		if err := rows.Scan(&m.id, &m.streamID, &m.name, &m.path, &m.runID, &m.started, &m.created,
			&m.backupID, &m.backupRun, &m.backupStarted, &m.backupPath, &m.backupPathValid); err != nil {
			rows.Close()
			return err
		}
		m.targetPath = m.path
		if m.backupID.Valid {
			if err := prepareArchiveMapping(&m); err != nil {
				rows.Close()
				return fmt.Errorf("archive mapping conflict for %s: %w", m.id, err)
			}
			mappings = append(mappings, m)
		}
		key := strings.ToLower(m.targetPath)
		previousMigration, occupied := destinations[key]
		if occupied && (previousMigration || m.backupID.Valid) {
			rows.Close()
			return errors.New("archive mapping destination collision")
		}
		destinations[key] = previousMigration || m.backupID.Valid
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// No metadata or original backup path changes until the whole mapping is
	// checked, including destinations occupied by rows outside the 080 backup.
	for _, m := range mappings {
		if !m.backupPath.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE v2_migration_archive_artifacts_backup
 SET relative_path=?,relative_path_fingerprint=SHA2(?,256) WHERE artifact_id=? AND relative_path IS NULL`, m.originalPath, m.originalPath, m.id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stream_artifacts SET archive_run_id=?,relative_path=?,
 archive_started_at=COALESCE(archive_started_at,?,created_at) WHERE id=?`, m.targetRun, m.targetPath, m.backupStarted, m.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO v2_migration_bundle8b_artifact_gate (gate_id,mismatch_count,verified_at)
 VALUES (1,0,CURRENT_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE mismatch_count=0,verified_at=VALUES(verified_at)`); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareArchiveMapping(m *archiveMapping) error {
	if !archiveStreamID.MatchString(m.streamID) || !archiveComponent(m.name) || !m.backupPathValid {
		return errors.New("invalid archive path or saved path evidence")
	}
	flat := "final/" + m.streamID + "/" + m.name
	legacyRun := "legacy-" + strings.ToLower(strings.ReplaceAll(m.streamID, "-", ""))
	legacyPath := "final/" + m.streamID + "/" + legacyRun + "/" + m.name
	m.originalPath = m.path
	if m.backupPath.Valid {
		m.originalPath = m.backupPath.String
	}
	m.targetRun = m.backupRun.String
	if m.targetRun == "" {
		if m.originalPath != flat {
			return errors.New("runless archive does not name an Encoder flat manifest entry")
		}
		m.targetRun, m.targetPath = legacyRun, legacyPath
		if m.runID != legacyRun || (m.path != flat && m.path != legacyPath) {
			return errors.New("flat archive changed since 080")
		}
	} else {
		if !archiveComponent(m.targetRun) || m.originalPath != "final/"+m.streamID+"/"+m.targetRun+"/"+m.name {
			return errors.New("existing run path does not match retained run identity")
		}
		m.targetPath = m.originalPath
		preserved := m.runID == m.targetRun && m.path == m.originalPath
		old081 := m.backupPath.Valid && m.runID == legacyRun && m.path == legacyPath
		if !preserved && !old081 {
			return errors.New("existing archive run changed since retained mapping")
		}
	}
	return nil
}

func archiveComponent(value string) bool {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
