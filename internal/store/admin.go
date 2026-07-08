package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

type UserPatch struct {
	Username string
	Email    *string
	RoleIDs  []string
}

type UserAdminStore interface {
	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, username, email, temporaryPassword string, roleIDs []string) (User, error)
	CreateOAuthUser(ctx context.Context, username, email string, roleIDs []string) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error)
	SetUserStatus(ctx context.Context, id, status string) (User, error)
	DeleteUser(ctx context.Context, id string) error
	ResetPassword(ctx context.Context, id, temporaryPassword string) error
	CountActiveSuperAdmins(ctx context.Context) (int, error)
	UserHasRole(ctx context.Context, userID, roleName string) (bool, error)
}

type RoleStore interface {
	ListRoles(ctx context.Context) ([]Role, error)
	CreateRole(ctx context.Context, name string, permissions []string) (Role, error)
	GetRole(ctx context.Context, id string) (Role, error)
	UpdateRole(ctx context.Context, id, name string, permissions []string) (Role, error)
	DeleteRole(ctx context.Context, id string) error
}

var (
	ErrLastSuperAdmin                   = errors.New("cannot modify last active super_admin")
	ErrPermissionEscalation             = errors.New("requested permissions exceed actor permissions")
	ErrSuperAdminAssignmentForbidden    = errors.New("only super_admin may assign the super_admin role")
	ErrSuperAdminPasswordResetForbidden = errors.New("only super_admin may reset a super_admin password")
	ErrSuperAdminStatusForbidden        = errors.New("only super_admin may change a super_admin status")
	ErrUnknownPermission                = errors.New("unknown permission")
)

func ValidateRoleAssignment(actor User, roles []Role) error {
	if hasRole(actor, "super_admin") {
		return nil
	}
	for _, role := range roles {
		if role.Name == "super_admin" {
			return ErrSuperAdminAssignmentForbidden
		}
	}
	return nil
}

func ValidateRolePermissions(actorPermissions, requestedPermissions []string) error {
	if err := validatePermissions(requestedPermissions); err != nil {
		return err
	}
	granted := make(map[string]struct{}, len(actorPermissions))
	for _, permission := range actorPermissions {
		granted[permission] = struct{}{}
	}
	for _, permission := range requestedPermissions {
		if _, ok := granted[permission]; !ok {
			return ErrPermissionEscalation
		}
	}
	return nil
}

func ValidatePasswordResetActor(actor, target User) error {
	if hasRole(target, "super_admin") && !hasRole(actor, "super_admin") {
		return ErrSuperAdminPasswordResetForbidden
	}
	return nil
}

func ValidateUserStatusActor(actor, target User) error {
	if hasRole(target, "super_admin") && !hasRole(actor, "super_admin") {
		return ErrSuperAdminStatusForbidden
	}
	return nil
}

func (s MariaDBAuthStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, COALESCE(email, ''), password_hash, status, last_login_at, last_login_ip FROM users ORDER BY username`)
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
		user.Roles, _ = s.userRoles(ctx, user.ID)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s MariaDBAuthStore) CreateUser(ctx context.Context, username, email, temporaryPassword string, roleIDs []string) (User, error) {
	hash, err := security.HashPassword(temporaryPassword)
	if err != nil {
		return User{}, err
	}
	email, err = normalizeUserEmail(email)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	user := User{ID: newUUID(), Username: username, Email: email, Status: "pending_password_change", PasswordHash: hash}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, email, password_hash, status, created_at, updated_at) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, user.ID, user.Username, user.Email, user.PasswordHash, user.Status, now, now); err != nil {
		return User{}, err
	}
	for _, roleID := range roleIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, user.ID, roleID); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, user.ID)
}

func (s MariaDBAuthStore) CreateOAuthUser(ctx context.Context, username, email string, roleIDs []string) (User, error) {
	email, err := normalizeUserEmail(email)
	if err != nil {
		return User{}, err
	}
	password, err := security.RandomToken(48)
	if err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	user := User{ID: newUUID(), Username: username, Email: email, Status: "active", PasswordHash: hash}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, email, password_hash, status, created_at, updated_at) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, user.ID, user.Username, user.Email, user.PasswordHash, user.Status, now, now); err != nil {
		return User{}, err
	}
	for _, roleID := range roleIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, user.ID, roleID); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, user.ID)
}

func (s MariaDBAuthStore) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if patch.Username != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, patch.Username, time.Now().UTC(), id); err != nil {
			return User{}, err
		}
	}
	if patch.Email != nil {
		email, err := normalizeUserEmail(*patch.Email)
		if err != nil {
			return User{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET email = NULLIF(?, ''), updated_at = ? WHERE id = ?`, email, time.Now().UTC(), id); err != nil {
			return User{}, err
		}
	}
	if patch.RoleIDs != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, id); err != nil {
			return User{}, err
		}
		for _, roleID := range patch.RoleIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, id, roleID); err != nil {
				return User{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, id)
}

func (s MariaDBAuthStore) SetUserStatus(ctx context.Context, id, status string) (User, error) {
	hasSuperAdmin, err := s.UserHasRole(ctx, id, "super_admin")
	if err != nil {
		return User{}, err
	}
	if hasSuperAdmin && (status == "disabled" || status == "locked") {
		count, err := s.CountActiveSuperAdmins(ctx)
		if err != nil {
			return User{}, err
		}
		if count <= 1 {
			return User{}, ErrLastSuperAdmin
		}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC(), id)
	if err != nil {
		return User{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if affected == 0 {
		return User{}, ErrNotFound
	}
	return s.GetUser(ctx, id)
}

func (s MariaDBAuthStore) DeleteUser(ctx context.Context, id string) error {
	user, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if hasRole(user, "super_admin") && user.Status == "active" {
		count, err := s.CountActiveSuperAdmins(ctx)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrLastSuperAdmin
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM oauth_user_links WHERE user_id = ?`,
		`DELETE FROM user_roles WHERE user_id = ?`,
		`DELETE FROM user_mfa WHERE user_id = ?`,
		`DELETE FROM mfa_challenges WHERE user_id = ?`,
		`DELETE FROM webauthn_ceremony_sessions WHERE user_id = ?`,
		`DELETE FROM webauthn_registration_challenges WHERE user_id = ?`,
		`DELETE FROM webauthn_credentials WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
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
	return tx.Commit()
}

func (s MariaDBAuthStore) ResetPassword(ctx context.Context, id, temporaryPassword string) error {
	hash, err := security.HashPassword(temporaryPassword)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'pending_password_change', updated_at = ? WHERE id = ?`, hash, time.Now().UTC(), id)
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

func (s MariaDBAuthStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u INNER JOIN user_roles ur ON ur.user_id = u.id INNER JOIN roles r ON r.id = ur.role_id WHERE r.name = 'super_admin' AND u.status = 'active'`).Scan(&count)
	return count, err
}

func (s MariaDBAuthStore) UserHasRole(ctx context.Context, userID, roleName string) (bool, error) {
	var got string
	err := s.db.QueryRowContext(ctx, `SELECT r.name FROM roles r INNER JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = ? AND r.name = ?`, userID, roleName).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s MariaDBAuthStore) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM roles ORDER BY name`)
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
		role.Permissions, _ = s.rolePermissions(ctx, role.ID)
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s MariaDBAuthStore) CreateRole(ctx context.Context, name string, permissions []string) (Role, error) {
	if err := validatePermissions(permissions); err != nil {
		return Role{}, err
	}
	now := time.Now().UTC()
	role := Role{ID: newUUID(), Name: name, Permissions: permissions, CreatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Role{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO roles (id, name, created_at) VALUES (?, ?, ?)`, role.ID, role.Name, role.CreatedAt); err != nil {
		return Role{}, err
	}
	for _, permission := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, role.ID, permission); err != nil {
			return Role{}, err
		}
	}
	return role, tx.Commit()
}

func (s MariaDBAuthStore) GetRole(ctx context.Context, id string) (Role, error) {
	var role Role
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM roles WHERE id = ?`, id).Scan(&role.ID, &role.Name, &role.CreatedAt)
	if err == sql.ErrNoRows {
		return Role{}, ErrNotFound
	}
	if err != nil {
		return Role{}, err
	}
	role.Permissions, _ = s.rolePermissions(ctx, role.ID)
	return role, nil
}

func (s MariaDBAuthStore) UpdateRole(ctx context.Context, id, name string, permissions []string) (Role, error) {
	if err := validatePermissions(permissions); err != nil {
		return Role{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Role{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE roles SET name = ? WHERE id = ?`, name, id); err != nil {
		return Role{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, id); err != nil {
		return Role{}, err
	}
	for _, permission := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, id, permission); err != nil {
			return Role{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Role{}, err
	}
	return s.GetRole(ctx, id)
}

func (s MariaDBAuthStore) DeleteRole(ctx context.Context, id string) error {
	role, err := s.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if role.Name == "super_admin" {
		return ErrLastSuperAdmin
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE role_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s MariaDBAuthStore) rolePermissions(ctx context.Context, roleID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT permission FROM role_permissions WHERE role_id = ? ORDER BY permission`, roleID)
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

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (User, error) {
	var user User
	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	err := row.Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Status, &lastLoginAt, &lastLoginIP)
	if err != nil {
		return User{}, err
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lastLoginIP.Valid {
		user.LastLoginIP = lastLoginIP.String
	}
	return user, nil
}

func normalizeUserEmail(value string) (string, error) {
	email := strings.TrimSpace(value)
	if email == "" {
		return "", nil
	}
	if len(email) > 255 || strings.ContainsAny(email, "\r\n\t\x00") {
		return "", ErrInvalidSettings
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address == "" || !strings.EqualFold(address.Address, email) {
		return "", ErrInvalidSettings
	}
	return address.Address, nil
}

func validatePermissions(permissions []string) error {
	allowed := map[string]bool{}
	for _, permission := range security.DefaultPermissions {
		allowed[permission] = true
	}
	for _, permission := range permissions {
		if !allowed[permission] {
			return ErrUnknownPermission
		}
	}
	return nil
}

func marshalMetadata(metadata map[string]any) (string, error) {
	body, err := json.Marshal(metadata)
	return string(body), err
}
