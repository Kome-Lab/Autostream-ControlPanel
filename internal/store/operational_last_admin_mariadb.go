package store

import (
	"context"
	"database/sql"
	"errors"
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
func lockLastAdminUser(ctx context.Context, tx *sql.Tx, id string) (lastAdminUser, error) {
	var singleton int
	if err := tx.QueryRowContext(ctx, "SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE").Scan(&singleton); err != nil {
		return lastAdminUser{}, err
	}
	if singleton != 1 {
		return lastAdminUser{}, ErrUnauthorized
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
