package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A commit error cannot prove rollback. Callers must return failure and must
// never replay the transaction automatically, including after connection loss.
var ErrUserAdministrationUnknown = errors.New("user administration commit outcome unknown")

type lastAdminUser struct {
	status string
	super  bool
}

// The shared DB guard is acquired before User/session/assignment locks. It has
// no foreign keys to those rows or to the Operational/Authority epochs.
// The canonical User/Role rows, not this lock row, determine administrator count.
func lockUserAdministration(ctx context.Context, tx *sql.Tx) error {
	var singleton int
	if err := tx.QueryRowContext(ctx, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE").Scan(&singleton); err != nil {
		return err
	}
	if singleton != 1 {
		return ErrUnauthorized
	}
	return nil
}

func lockLastAdminUser(ctx context.Context, tx *sql.Tx, id string) (lastAdminUser, error) {
	if err := lockUserAdministration(ctx, tx); err != nil {
		return lastAdminUser{}, err
	}
	var target lastAdminUser
	err := tx.QueryRowContext(ctx, "SELECT status FROM users WHERE id=? FOR UPDATE", id).Scan(&target.status)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrNotFound
	}
	if err != nil {
		return target, err
	}
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles ur INNER JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=? AND r.name='super_admin')", id).Scan(&target.super)
	return target, err
}

func requireAnotherActiveSuperAdmin(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users u INNER JOIN user_roles ur ON ur.user_id=u.id INNER JOIN roles r ON r.id=ur.role_id WHERE r.name='super_admin' AND u.status='active'").Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastSuperAdmin
	}
	return nil
}

func commitUserAdministration(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return errors.Join(ErrUserAdministrationUnknown, err)
	}
	return nil
}

// Authentication lockout keeps its original threshold and status policy. It
// participates in the same lock order so a management transaction cannot use
// a count read before a concurrent automatic lockout commits.
func (s MariaDBAuthStore) recordLoginFailureGuarded(ctx context.Context, username string, threshold int) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockUserAdministration(ctx, tx); err != nil {
		return err
	}
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE username=? FOR UPDATE", username).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET failed_login_count = failed_login_count + 1, status = IF(failed_login_count + 1 >= ?, 'locked', status), updated_at = ? WHERE username = ?`, threshold, time.Now().UTC(), username); err != nil {
		return err
	}
	return commitUserAdministration(tx)
}
