package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/go-sql-driver/mysql"
)

// The journal must be durable before migration090 can materialize preferences.
// An interrupted migration can then be retried after an old-release rollback.
// A pre-existing preference table without that provenance is never guessed at
// or discarded (including private candidates that predate this boundary).
func prepareNodeSelectionActivationJournal(ctx context.Context, db *sql.DB) error {
	body, err := embeddedMigrations.ReadFile("migrations/092_stream_node_selection_activation.sql")
	if err != nil {
		return err
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	var existing int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM stream_node_selection_activation WHERE singleton_id=1").Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, "SELECT 1 FROM stream_node_selections LIMIT 0")
	if err == nil {
		_ = rows.Close()
		return errors.New("saved Node choices have no activation provenance; verified recovery is required")
	}
	var missing *mysql.MySQLError
	if !errors.As(err, &missing) || missing.Number != 1146 {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT IGNORE INTO stream_node_selection_activation(singleton_id) VALUES(1)")
	return err
}
