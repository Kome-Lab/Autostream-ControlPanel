package lifecyclefoundation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed migrations/002_identity.sql
var identityMigrationSQL string

const identityJournalDDL = "CREATE TABLE IF NOT EXISTS v3_cp_identity_schema (singleton TINYINT PRIMARY KEY, checksum CHAR(64) NOT NULL, generation BIGINT UNSIGNED NOT NULL, dirty BOOLEAN NOT NULL, definition CHAR(64) NOT NULL) ENGINE=InnoDB"

var identityTables = []string{
	"v3_cp_identity_schema", "v3_cp_identity_projection", "v3_cp_identity_history", "v3_cp_identity_schema_history",
}

func identityMigrationChecksum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identityMigrationSQL)))
}

func identityDefinition(ctx context.Context, q sqlQuery) (string, error) {
	var all strings.Builder
	for _, table := range identityTables {
		var name, ddl string
		if err := q.QueryRowContext(ctx, "SHOW CREATE TABLE "+table).Scan(&name, &ddl); err != nil {
			return "", held(err)
		}
		if name != table || !strings.Contains(ddl, "ENGINE=InnoDB") {
			return "", ErrHeld
		}
		for _, part := range strings.Fields(ddl) {
			if !strings.HasPrefix(part, "AUTO_INCREMENT=") {
				all.WriteString(part)
				all.WriteByte(' ')
			}
		}
	}
	for _, name := range []string{
		"v3_cp_identity_history_no_update", "v3_cp_identity_history_no_delete",
		"v3_cp_identity_schema_history_no_update", "v3_cp_identity_schema_history_no_delete",
	} {
		var action, timing, event string
		if err := q.QueryRowContext(ctx, "SELECT ACTION_STATEMENT,ACTION_TIMING,EVENT_MANIPULATION FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND TRIGGER_NAME=?", name).Scan(&action, &timing, &event); err != nil {
			return "", held(err)
		}
		all.WriteString(name + action + timing + event)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(all.String()))), nil
}

func checkIdentitySchema(ctx context.Context, q sqlQuery) error {
	var checksum, sealed string
	var generation uint64
	var dirty bool
	var rows int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_identity_schema").Scan(&rows); err != nil || rows != 1 {
		return ErrHeld
	}
	if err := q.QueryRowContext(ctx, "SELECT checksum,generation,dirty,definition FROM v3_cp_identity_schema WHERE singleton=1").Scan(&checksum, &generation, &dirty, &sealed); err != nil {
		return held(err)
	}
	if checksum != identityMigrationChecksum() || generation != 1 || dirty {
		return ErrHeld
	}
	current, err := identityDefinition(ctx, q)
	if err != nil || current != sealed {
		return ErrHeld
	}
	var recorded string
	if err = q.QueryRowContext(ctx, "SELECT checksum FROM v3_cp_identity_schema_history WHERE generation=1").Scan(&recorded); err != nil || recorded != checksum {
		return ErrHeld
	}
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_identity_schema_history").Scan(&rows); err != nil || rows != 1 {
		return ErrHeld
	}
	return nil
}

// ApplyIdentityMigrations is an explicit schema-only operation. Neither startup
// nor a constructor calls it. It installs no projection, identity, credential,
// approval, Gate or Provider. Tests may explicitly seed a disposable fixture.
// Interrupted MariaDB DDL remains dirty and is never repaired automatically.
func ApplyIdentityMigrations(ctx context.Context, db *sql.DB) error {
	if ctx == nil || db == nil || ctx.Err() != nil {
		return ErrHeld
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return held(err)
	}
	defer conn.Close()
	if err = checkSchema(ctx, conn); err != nil {
		return err
	}
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':s12-b2-migration'),5)").Scan(&locked); err != nil || locked != 1 {
		return ErrHeld
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':s12-b2-migration'))")
	if _, err = conn.ExecContext(ctx, identityJournalDDL); err != nil {
		return held(err)
	}
	var rows int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_identity_schema").Scan(&rows); err != nil {
		return held(err)
	}
	if rows != 0 {
		return checkIdentitySchema(ctx, conn)
	}
	for _, table := range identityTables[1:] {
		if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&rows); err != nil || rows != 0 {
			return ErrHeld
		}
	}
	checksum := identityMigrationChecksum()
	if _, err = conn.ExecContext(ctx, "INSERT INTO v3_cp_identity_schema VALUES(1,?,0,1,'')", checksum); err != nil {
		return held(err)
	}
	for _, statement := range strings.Split(identityMigrationSQL, ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			if _, err = conn.ExecContext(ctx, statement); err != nil {
				return held(err)
			}
		}
	}
	sealed, err := identityDefinition(ctx, conn)
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return held(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE v3_cp_identity_schema SET generation=1,dirty=0,definition=? WHERE singleton=1 AND generation=0 AND dirty=1 AND checksum=?", sealed, checksum)
	if err != nil {
		return held(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_identity_schema_history VALUES(1,?)", checksum); err != nil {
		return held(err)
	}
	if err = tx.Commit(); err != nil {
		return ErrUnknown
	}
	return checkIdentitySchema(ctx, conn)
}
