package operationalauthorization

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

//go:embed migrations/001_operational.sql
var operationalSchema string

var ErrOperationalSchema = errors.New("operational authorization schema unavailable or inconsistent")

type OperationalQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ApplyOperationalMigrations is an explicit installer operation.
// DDL is not transactional in MariaDB: an interrupted install remains dirty.
func ApplyOperationalMigrations(ctx context.Context, db *sql.DB) error {
	if ctx == nil || db == nil {
		return ErrOperationalSchema
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var databaseName string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil || databaseName == "" {
		return ErrOperationalSchema
	}
	lockDigest := sha256.Sum256([]byte(databaseName))
	lockName := "s13oa:" + hex.EncodeToString(lockDigest[:])[:48]
	var locked int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&locked); err != nil || locked != 1 {
		return ErrOperationalSchema
	}
	defer func() {
		var released sql.NullInt64
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
	}()
	_, err = conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS operational_authorization_migrations (
 singleton TINYINT UNSIGNED NOT NULL PRIMARY KEY, version INT UNSIGNED NOT NULL,
 checksum CHAR(64) NOT NULL, dirty BOOLEAN NOT NULL,
 CONSTRAINT operational_migration_singleton CHECK (singleton=1)
) ENGINE=InnoDB`)
	if err != nil {
		return err
	}
	checksum := OperationalMigrationChecksum()
	var version uint64
	var stored string
	var dirty bool
	err = conn.QueryRowContext(ctx, `SELECT version, checksum, dirty FROM operational_authorization_migrations WHERE singleton=1`).Scan(&version, &stored, &dirty)
	if err == nil {
		if dirty || version != 1 || stored != checksum {
			return ErrOperationalSchema
		}
		return CheckOperationalSchema(ctx, conn)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var owned int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('operational_authorization_epoch','operational_authorization_history')`).Scan(&owned); err != nil {
		return err
	}
	var existingTriggers int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND LEFT(TRIGGER_NAME,6)='s13oa_'`).Scan(&existingTriggers); err != nil {
		return err
	}
	if owned != 0 || existingTriggers != 0 {
		return ErrOperationalSchema
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO operational_authorization_migrations (singleton,version,checksum,dirty) VALUES (1,0,?,TRUE)`, checksum); err != nil {
		return err
	}
	var sourceTables int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('users','roles','role_permissions','user_roles') AND ENGINE='InnoDB'").Scan(&sourceTables); err != nil || sourceTables != 4 {
		return ErrOperationalSchema
	}
	for _, statement := range operationalStatements() {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation uint64
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM operational_authorization_epoch WHERE singleton=1 FOR UPDATE").Scan(&generation); err != nil || generation == 0 {
		return ErrOperationalSchema
	}
	if err := checkOperationalObjects(ctx, tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE operational_authorization_migrations SET version=1,dirty=FALSE WHERE singleton=1 AND version=0 AND checksum=? AND dirty=TRUE`, checksum)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrOperationalSchema
	}
	return tx.Commit()
}

func OperationalMigrationChecksum() string {
	sum := sha256.Sum256([]byte(strings.Join(operationalStatements(), "\n-- statement\n")))
	return hex.EncodeToString(sum[:])
}

func operationalStatements() []string {
	var out []string
	for _, part := range strings.Split(strings.ReplaceAll(operationalSchema, "\r\n", "\n"), "\n-- statement\n") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	type source struct{ table, keys, values string }
	sources := []source{
		{"users", "id", "id,status"}, {"roles", "id", "id,name"},
		{"role_permissions", "role_id,permission", "role_id,permission"}, {"user_roles", "user_id,role_id", "user_id,role_id"},
	}
	for _, source := range sources {
		for _, event := range []struct{ suffix, sql, row string }{{"ai", "INSERT", "NEW"}, {"au", "UPDATE", "NEW"}, {"ad", "DELETE", "OLD"}} {
			prefix := func(fields, row string) string {
				parts := strings.Split(fields, ",")
				for i := range parts {
					parts[i] = row + "." + parts[i]
				}
				return strings.Join(parts, ",")
			}
			keys := prefix(source.keys, event.row)
			values := prefix(source.values, event.row)
			if event.sql == "UPDATE" {
				values = prefix(source.values, "OLD") + "," + values
			}
			out = append(out, fmt.Sprintf(`CREATE TRIGGER s13oa_%s_%s AFTER %s ON %s FOR EACH ROW BEGIN
UPDATE operational_authorization_epoch SET generation=generation+1 WHERE singleton=1 AND generation<18446744073709551615;
IF ROW_COUNT()<>1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='operational generation unavailable'; END IF;
INSERT INTO operational_authorization_history (generation,source_table,source_event,source_key,change_digest)
SELECT generation,'%s','%s',SHA2(JSON_ARRAY(%s),256),SHA2(JSON_ARRAY(%s),256) FROM operational_authorization_epoch WHERE singleton=1;
END`, source.table, event.suffix, event.sql, source.table, source.table, event.sql, keys, values))
		}
	}
	for _, event := range []struct{ suffix, sql string }{{"bu", "UPDATE"}, {"bd", "DELETE"}} {
		out = append(out, fmt.Sprintf(`CREATE TRIGGER s13oa_history_%s BEFORE %s ON operational_authorization_history FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='operational history is append only'`, event.suffix, event.sql))
	}
	return out
}

func CheckOperationalSchema(ctx context.Context, q OperationalQueryer) error {
	if ctx == nil || q == nil {
		return ErrOperationalSchema
	}
	var version uint64
	var checksum string
	var dirty bool
	if err := q.QueryRowContext(ctx, `SELECT version,checksum,dirty FROM operational_authorization_migrations WHERE singleton=1`).Scan(&version, &checksum, &dirty); err != nil {
		return ErrOperationalSchema
	}
	if dirty || version != 1 || checksum != OperationalMigrationChecksum() {
		return ErrOperationalSchema
	}
	return checkOperationalObjects(ctx, q)
}

func checkOperationalObjects(ctx context.Context, q OperationalQueryer) error {
	var tables int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('users','roles','role_permissions','user_roles','operational_authorization_epoch','operational_authorization_history','operational_authorization_migrations') AND ENGINE='InnoDB'`).Scan(&tables); err != nil || tables != 7 {
		return ErrOperationalSchema
	}
	var triggers int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND ((EVENT_OBJECT_TABLE IN ('users','roles','role_permissions','user_roles') AND ACTION_TIMING='AFTER' AND TRIGGER_NAME IN ('s13oa_users_ai','s13oa_users_au','s13oa_users_ad','s13oa_roles_ai','s13oa_roles_au','s13oa_roles_ad','s13oa_role_permissions_ai','s13oa_role_permissions_au','s13oa_role_permissions_ad','s13oa_user_roles_ai','s13oa_user_roles_au','s13oa_user_roles_ad')) OR (EVENT_OBJECT_TABLE='operational_authorization_history' AND ACTION_TIMING='BEFORE' AND TRIGGER_NAME IN ('s13oa_history_bu','s13oa_history_bd')))`).Scan(&triggers); err != nil || triggers != 14 {
		return ErrOperationalSchema
	}
	var generation, count, min, max uint64
	if err := q.QueryRowContext(ctx, `SELECT e.generation, COUNT(h.generation), COALESCE(MIN(h.generation),0), COALESCE(MAX(h.generation),0) FROM operational_authorization_epoch e LEFT JOIN operational_authorization_history h ON TRUE WHERE e.singleton=1 GROUP BY e.generation`).Scan(&generation, &count, &min, &max); err != nil || generation == 0 || count != generation || min != 1 || max != generation {
		return ErrOperationalSchema
	}
	return nil
}
