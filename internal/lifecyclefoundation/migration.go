package lifecyclefoundation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed migrations/001_lifecycle.sql
var migrationSQL string

const journalDDL = "CREATE TABLE IF NOT EXISTS v3_cp_lifecycle_schema (singleton TINYINT PRIMARY KEY, checksum CHAR(64) NOT NULL, generation BIGINT UNSIGNED NOT NULL, dirty BOOLEAN NOT NULL, definition CHAR(64) NOT NULL) ENGINE=InnoDB"

var tables = []string{
	"v3_cp_lifecycle_schema", "v3_cp_lifecycle_heads", "v3_cp_lifecycle_attempts",
	"v3_cp_lifecycle_consumptions", "v3_cp_lifecycle_receipts", "v3_cp_lifecycle_history",
	"v3_cp_lifecycle_unknowns", "v3_cp_lifecycle_schema_history",
}

type sqlQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func migrationChecksum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(migrationSQL)))
}

func definition(ctx context.Context, q sqlQuery) (string, error) {
	var all strings.Builder
	for _, table := range tables {
		var name, ddl string
		if err := q.QueryRowContext(ctx, "SHOW CREATE TABLE "+table).Scan(&name, &ddl); err != nil {
			return "", held(err)
		}
		if !strings.Contains(ddl, "ENGINE=InnoDB") {
			return "", ErrHeld
		}
		// AUTO_INCREMENT's current counter is data, not schema identity.
		parts := strings.Fields(ddl)
		for _, part := range parts {
			if !strings.HasPrefix(part, "AUTO_INCREMENT=") {
				all.WriteString(part)
				all.WriteByte(' ')
			}
		}
	}
	// Trigger definitions are part of the sealed schema.
	for _, name := range []string{
		"v3_cp_lifecycle_history_no_update", "v3_cp_lifecycle_history_no_delete",
		"v3_cp_lifecycle_schema_history_no_update", "v3_cp_lifecycle_schema_history_no_delete",
		"v3_cp_lifecycle_unknowns_no_update", "v3_cp_lifecycle_unknowns_no_delete",
	} {
		var action, timing, event string
		if err := q.QueryRowContext(ctx, "SELECT ACTION_STATEMENT,ACTION_TIMING,EVENT_MANIPULATION FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND TRIGGER_NAME=?", name).Scan(&action, &timing, &event); err != nil {
			return "", held(err)
		}
		all.WriteString(name + action + timing + event)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(all.String()))), nil
}

func checkSchema(ctx context.Context, q sqlQuery) error {
	var checksum, sealed string
	var generation uint64
	var dirty bool
	var rows int
	if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_schema").Scan(&rows) != nil || rows != 1 {
		return ErrHeld
	}
	if err := q.QueryRowContext(ctx, "SELECT checksum,generation,dirty,definition FROM v3_cp_lifecycle_schema WHERE singleton=1").Scan(&checksum, &generation, &dirty, &sealed); err != nil {
		return held(err)
	}
	if checksum != migrationChecksum() || dirty || generation != 1 {
		return ErrHeld
	}
	current, err := definition(ctx, q)
	if err != nil || current != sealed {
		return ErrHeld
	}
	var recorded string
	if q.QueryRowContext(ctx, "SELECT checksum FROM v3_cp_lifecycle_schema_history WHERE generation=1").Scan(&recorded) != nil || recorded != checksum {
		return ErrHeld
	}
	return nil
}

// ApplyMigrations is explicit and never called by constructors or product startup.
// MariaDB DDL is not transactional. An interrupted installation remains dirty;
// this method refuses repair rather than guessing which DDL was committed.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if ctx == nil || db == nil || ctx.Err() != nil {
		return ErrHeld
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return held(err)
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':s12-b0-migration'),5)").Scan(&locked); err != nil || locked != 1 {
		return ErrHeld
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':s12-b0-migration'))")
	if _, err = conn.ExecContext(ctx, journalDDL); err != nil {
		return held(err)
	}
	var rows int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_schema").Scan(&rows); err != nil {
		return held(err)
	}
	if rows != 0 {
		return checkSchema(ctx, conn)
	}
	for _, table := range tables[1:] {
		var count int
		if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&count); err != nil || count != 0 {
			return ErrHeld
		}
	}
	checksum := migrationChecksum()
	if _, err = conn.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_schema VALUES(1,?,0,1,'')", checksum); err != nil {
		return held(err)
	}
	for _, statement := range strings.Split(migrationSQL, ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			if _, err = conn.ExecContext(ctx, statement); err != nil {
				return held(err)
			}
		}
	}
	sealed, err := definition(ctx, conn)
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return held(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE v3_cp_lifecycle_schema SET generation=1,dirty=0,definition=? WHERE singleton=1 AND generation=0 AND dirty=1 AND checksum=?", sealed, checksum)
	if err != nil {
		return held(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_schema_history VALUES(1,?)", checksum); err != nil {
		return held(err)
	}
	if err = tx.Commit(); err != nil {
		return ErrUnknown
	}
	return checkSchema(ctx, conn)
}
