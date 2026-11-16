package store

import (
	"context"
	"database/sql"
	"errors"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// identitySQL cannot silently open another connection during an admission.
// Required composition and the callback's same-DB private owner must both exist.
func (s MariaDBAuthStore) identitySQL(ctx context.Context) (lf.Executor, error) {
	q, joined, err := lf.SQL(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if joined {
		if s.identityGate == nil {
			return nil, lf.ErrHeld
		}
		if _, err := s.identityGate.IdentityBinding(ctx); err != nil {
			return nil, err
		}
		return q, nil
	}
	if s.identityGate != nil || s.db == nil {
		return nil, lf.ErrHeld
	}
	return s.db, nil
}

// identityBeginSQL checks composition before joining the private SQL owner.
func (s MariaDBAuthStore) identityBeginSQL(ctx context.Context, options *sql.TxOptions) (lf.SQLTransaction, error) {
	if _, err := s.identitySQL(ctx); err != nil {
		return nil, err
	}
	return lf.BeginSQL(ctx, s.db, options)
}

func (s MariaDBAuthStore) identityLockCurrentUser(ctx context.Context, id string) (User, error) {
	q, err := s.identitySQL(ctx)
	if err != nil {
		return User{}, err
	}
	var status string
	if err := q.QueryRowContext(ctx, "SELECT status FROM users WHERE id=? FOR UPDATE", id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if status != "active" && status != "pending_password_change" {
		return User{}, ErrUnauthorized
	}
	current, err := s.identityGetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if current.Status != status {
		return User{}, lf.ErrHeld
	}
	return current, nil
}

// Buffer the parent rows before reading children: one joined SQL connection
// cannot run a second query while its first result stream remains open.
func (s MariaDBAuthStore) identityListUsersJoined(ctx context.Context) ([]User, error) {
	q, err := s.identitySQL(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id, username, COALESCE(email, ''), password_hash, status, last_login_at, last_login_ip FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range users {
		users[i].RoleIDs, users[i].Roles, err = s.userRoleAssignments(ctx, users[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (s MariaDBAuthStore) identityListRolesJoined(ctx context.Context) ([]Role, error) {
	q, err := s.identitySQL(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id, name, created_at FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []Role
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Name, &role.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range roles {
		roles[i].Permissions, err = s.rolePermissions(ctx, roles[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return roles, nil
}
