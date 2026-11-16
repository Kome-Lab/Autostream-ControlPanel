package bootstrapapproval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"strings"
)

//go:embed migrations/001_fixture_approval.sql
var fixtureMigration string

func checksum() string { s := sha256.Sum256([]byte(fixtureMigration)); return hex.EncodeToString(s[:]) }

type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (c *Core) schema(ctx context.Context, q rowReader) error {
	var db, profile, sum string
	var version uint64
	var dirty bool
	if q.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&db) != nil || db != FixtureDatabase {
		return ErrHeld
	}
	if q.QueryRowContext(ctx, "SELECT version,profile,checksum,dirty FROM v3_cp_approval_schema WHERE singleton=1").Scan(&version, &profile, &sum, &dirty) != nil || version != 1 || profile != FixtureProfile || sum != checksum() || dirty {
		return ErrHeld
	}
	var n int
	if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('v3_cp_approval_schema','v3_cp_approval_checkpoint','v3_cp_approval_consumptions','v3_cp_approval_history') AND ENGINE='InnoDB'").Scan(&n) != nil || n != 4 {
		return ErrHeld
	}
	if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND TRIGGER_NAME IN ('v3_approval_consumption_no_update','v3_approval_consumption_no_delete','v3_approval_history_no_update','v3_approval_history_no_delete') AND ACTION_TIMING='BEFORE'").Scan(&n) != nil || n != 4 {
		return ErrHeld
	}
	return nil
}

// ApplyFixtureMigrations requires both explicit opt-in and the dedicated CI
// schema name. No normal database migration/startup path imports or calls it.
func ApplyFixtureMigrations(ctx context.Context, db *sql.DB, optIn bool) error {
	if !optIn || ctx == nil || db == nil {
		return ErrHeld
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return ErrHeld
	}
	defer conn.Close()
	var name string
	if conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name) != nil || name != FixtureDatabase {
		return ErrHeld
	}
	var locked int
	if conn.QueryRowContext(ctx, "SELECT GET_LOCK('autostream-v3-cp-fixture-approval-migration',0)").Scan(&locked) != nil || locked != 1 {
		return ErrHeld
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK('autostream-v3-cp-fixture-approval-migration')")
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS v3_cp_approval_schema(singleton TINYINT NOT NULL PRIMARY KEY,version BIGINT UNSIGNED NOT NULL,profile VARBINARY(128) NOT NULL,checksum VARBINARY(64) NOT NULL,dirty BOOLEAN NOT NULL,CHECK(singleton=1)) ENGINE=InnoDB"); err != nil {
		return ErrHeld
	}
	var version uint64
	var profile, sum string
	var dirty bool
	err = conn.QueryRowContext(ctx, "SELECT version,profile,checksum,dirty FROM v3_cp_approval_schema WHERE singleton=1").Scan(&version, &profile, &sum, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('v3_cp_approval_checkpoint','v3_cp_approval_consumptions','v3_cp_approval_history')").Scan(&count) != nil || count != 0 {
			return ErrHeld
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO v3_cp_approval_schema VALUES(1,0,?,'',0)", FixtureProfile); err != nil {
			return ErrHeld
		}
		profile = FixtureProfile
	} else if err != nil {
		return ErrHeld
	}
	if dirty || profile != FixtureProfile {
		return ErrHeld
	}
	if version == 1 {
		if sum != checksum() {
			return ErrHeld
		}
		return (&Core{}).schema(ctx, conn)
	}
	if version != 0 || sum != "" {
		return ErrHeld
	}
	if _, err = conn.ExecContext(ctx, "UPDATE v3_cp_approval_schema SET dirty=1 WHERE singleton=1 AND version=0 AND dirty=0"); err != nil {
		return ErrHeld
	}
	for _, statement := range strings.Split(fixtureMigration, "\n-- statement\n") {
		if _, err = conn.ExecContext(ctx, strings.TrimSpace(statement)); err != nil {
			return ErrHeld
		}
	}
	if _, err = conn.ExecContext(ctx, "UPDATE v3_cp_approval_schema SET version=1,checksum=?,dirty=0 WHERE singleton=1 AND dirty=1", checksum()); err != nil {
		return ErrHeld
	}
	return (&Core{}).schema(ctx, conn)
}
