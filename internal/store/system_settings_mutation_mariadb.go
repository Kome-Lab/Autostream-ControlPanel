package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

// READ COMMITTED is local to this settings transaction: a missing named row
// does not retain a gap lock that can deadlock simultaneous first INSERTs.
// Existing rows still use FOR UPDATE. The only retry is a legacy caller's
// duplicate initial INSERT after the winner has committed.
func beginSystemSettingsMutation(ctx context.Context, db *sql.DB, name, initialBody string, expected *string) (*sql.Tx, time.Time, error) {
	if err := validateExpectedSystemSettingsRevision(expected); err != nil {
		return nil, time.Time{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return nil, time.Time{}, err
		}
		var previous time.Time
		err = tx.QueryRowContext(ctx, `SELECT updated_at FROM system_settings WHERE name=? FOR UPDATE`, name).Scan(&previous)
		if errors.Is(err, sql.ErrNoRows) {
			if err = matchExpectedSystemSettingsRevision(expected, UnconfiguredSystemSettingsRevision); err != nil {
				_ = tx.Rollback()
				return nil, time.Time{}, err
			}
			previous = time.Now().UTC().Truncate(time.Microsecond)
			_, err = tx.ExecContext(ctx, `INSERT INTO system_settings(name,value_json,updated_at)VALUES(?,?,?)`, name, initialBody, previous)
			var duplicate *mysql.MySQLError
			if errors.As(err, &duplicate) && duplicate.Number == 1062 {
				_ = tx.Rollback()
				if expected == nil && attempt == 0 {
					continue
				}
				return nil, time.Time{}, ErrRevisionConflict
			}
			if err != nil {
				_ = tx.Rollback()
				return nil, time.Time{}, err
			}
			return tx, previous, nil
		}
		if err == nil {
			err = matchExpectedSystemSettingsRevision(expected, resourceUpdatedAt(previous))
		}
		if err != nil {
			_ = tx.Rollback()
			return nil, time.Time{}, err
		}
		return tx, previous, nil
	}
	return nil, time.Time{}, ErrRevisionConflict
}

func appSecretStatusTx(ctx context.Context, tx *sql.Tx, settings *AppSettings) error {
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM secrets WHERE name=?),EXISTS(SELECT 1 FROM secrets WHERE name=?)`, AppSMTPPasswordSecretName, AppTurnstileSecretName).Scan(&settings.SMTPPasswordConfigured, &settings.TurnstileConfigured)
}
