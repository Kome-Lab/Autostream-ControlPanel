package store

import (
	"context"
	"database/sql"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/security"
	"time"
)

func (s MariaDBAuthStore) identityCountUsers(ctx context.Context) (int, error) {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return 0, identityErr
	}
	var count int
	err := identityQ.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s MariaDBAuthStore) CreateFirstAdmin(ctx context.Context, username, password string, permissions []string) (User, error) {
	if s.identityGate != nil {
		return User{}, lf.ErrHeld
	}
	if _, bound, err := lf.SQL(ctx, s.db); err != nil || bound {
		return User{}, lf.ErrHeld
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.identityBeginSQL(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	user := User{ID: newUUID(), Username: username, Status: "active", Roles: []string{"super_admin"}, PasswordHash: hash}
	roleID := newUUID()
	user.RoleIDs = []string{roleID}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, email, password_hash, status, created_at, updated_at) VALUES (?, ?, NULL, ?, 'active', ?, ?)`, user.ID, user.Username, user.PasswordHash, now, now); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO roles (id, name, created_at) VALUES (?, 'super_admin', ?)`, roleID, now); err != nil {
		return User{}, err
	}
	for _, permission := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, roleID, permission); err != nil {
			return User{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, user.ID, roleID); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s MariaDBAuthStore) identityFindUserByUsername(ctx context.Context, username string) (User, error) {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return User{}, identityErr
	}
	var user User
	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	err := identityQ.QueryRowContext(ctx, `SELECT id, username, COALESCE(email, ''), password_hash, status, last_login_at, last_login_ip FROM users WHERE username = ?`, username).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Status, &lastLoginAt, &lastLoginIP)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lastLoginIP.Valid {
		user.LastLoginIP = lastLoginIP.String
	}
	user.RoleIDs, user.Roles, err = s.userRoleAssignments(ctx, user.ID)
	if err != nil && s.identityGate != nil {
		return User{}, err
	}
	return user, nil
}

func (s MariaDBAuthStore) identityGetUser(ctx context.Context, id string) (User, error) {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return User{}, identityErr
	}
	var user User
	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	err := identityQ.QueryRowContext(ctx, `SELECT id, username, COALESCE(email, ''), password_hash, status, last_login_at, last_login_ip FROM users WHERE id = ?`, id).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Status, &lastLoginAt, &lastLoginIP)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lastLoginIP.Valid {
		user.LastLoginIP = lastLoginIP.String
	}
	user.RoleIDs, user.Roles, err = s.userRoleAssignments(ctx, user.ID)
	if err != nil && s.identityGate != nil {
		return User{}, err
	}
	return user, nil
}

func (s MariaDBAuthStore) userRoleAssignments(ctx context.Context, userID string) ([]string, []string, error) {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return nil, nil, identityErr
	}
	rows, err := identityQ.QueryContext(ctx, `SELECT r.id, r.name FROM roles r INNER JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = ? ORDER BY r.name`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var roleIDs []string
	var roles []string
	for rows.Next() {
		var roleID string
		var role string
		if err := rows.Scan(&roleID, &role); err != nil {
			return nil, nil, err
		}
		roleIDs = append(roleIDs, roleID)
		roles = append(roles, role)
	}
	return roleIDs, roles, rows.Err()
}

func (s MariaDBAuthStore) identityGetUserPermissions(ctx context.Context, id string) ([]string, error) {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return nil, identityErr
	}
	rows, err := identityQ.QueryContext(ctx, `SELECT DISTINCT rp.permission FROM role_permissions rp INNER JOIN user_roles ur ON ur.role_id = rp.role_id WHERE ur.user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

func (s MariaDBAuthStore) identityChangePassword(ctx context.Context, id, newPassword string) error {
	identityQ, identityErr := s.identitySQL(ctx)
	if identityErr != nil {
		return identityErr
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	result, err := identityQ.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'active', updated_at = ? WHERE id = ?`, hash, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
