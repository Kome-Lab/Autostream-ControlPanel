package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// NewMariaDBAuthStoreWithLifecycle is an explicit composition seam. No product
// startup path supplies a configured Gate, and an empty Gate remains closed.
func NewMariaDBAuthStoreWithLifecycle(db *sql.DB, key string, gate *lf.Gate) MariaDBAuthStore {
	if gate == nil {
		gate = lf.NewRequiredGate()
	}
	return MariaDBAuthStore{db: db, secretKeyMaterial: key, identityGate: gate}
}

func (s MariaDBAuthStore) IdentityLifecycleRequired() bool { return s.identityGate != nil }

// identityValue withholds even locally generated secrets until the owning B0
// participation has completed. An error never returns a captured partial value.
func identityValue[T any](ctx context.Context, gate *lf.Gate, operation, target string, fn func(context.Context) (T, error)) (T, error) {
	return identityValueContent(ctx, gate, operation, target, "", fn)
}

func identityValueContent[T any](ctx context.Context, gate *lf.Gate, operation, target, contentReference string, fn func(context.Context) (T, error)) (T, error) {
	if gate == nil {
		return fn(ctx)
	}
	var zero T
	joined, err := gate.RequireIdentity(ctx, operation, target, contentReference)
	if err != nil {
		return zero, err
	}
	if joined {
		return fn(ctx)
	}
	var value T
	var businessErr error
	err = gate.DoIdentity(ctx, lf.IdentityCall{Operation: operation, Target: target, ContentReference: contentReference}, func(partCtx context.Context) error {
		var callErr error
		value, callErr = fn(partCtx)
		if callErr != nil && (lf.IdentityCall{Operation: operation}).ReadOnly() &&
			!errors.Is(callErr, lf.ErrUnknown) && !errors.Is(callErr, lf.ErrHeld) && !errors.Is(callErr, lf.ErrConflict) &&
			(errors.Is(callErr, ErrNotFound) || errors.Is(callErr, ErrUnauthorized)) {
			// A confirmed denial from the SQL read-only owner completes the
			// unchanged admission before the business result is exposed. Any
			// uncertain Complete still wins and withholds this captured result.
			businessErr = callErr
			return nil
		}
		return callErr
	})
	if err != nil {
		return zero, err
	}
	if businessErr != nil {
		return zero, businessErr
	}
	return value, nil
}

func (s MariaDBAuthStore) identityRun(ctx context.Context, operation, target string, fn func(context.Context) error) error {
	_, err := identityValue(ctx, s.identityGate, operation, target, func(partCtx context.Context) (struct{}, error) {
		return struct{}{}, fn(partCtx)
	})
	return err
}

func (s MariaDBAuthStore) ListUsers(ctx context.Context) ([]User, error) {
	return identityValue(ctx, s.identityGate, "identity.user.list", "users", s.identityListUsers)
}

func (s MariaDBAuthStore) CreateUser(ctx context.Context, username, email, temporaryPassword string, roleIDs []string) (User, error) {
	content := lf.SyntheticReference(struct { Username, Email string; RoleIDs []string }{username, email, roleIDs})
	return identityValueContent(ctx, s.identityGate, "identity.user.create", "users", content, func(partCtx context.Context) (User, error) {
		return s.identityCreateUser(partCtx, username, email, temporaryPassword, roleIDs)
	})
}

func (s MariaDBAuthStore) CreateOAuthUser(ctx context.Context, username, email string, roleIDs []string) (User, error) {
	// Provisioning needs an adopted Provider/Subject/version contract. A generic
	// admin-shaped call must not turn an OAuth proof into a Human permission.
	if s.identityGate != nil {
		return User{}, lf.ErrHeld
	}
	return s.identityCreateOAuthUser(ctx, username, email, roleIDs)
}

func (s MariaDBAuthStore) GetUser(ctx context.Context, id string) (User, error) {
	return identityValue(ctx, s.identityGate, "identity.user.read", id, func(partCtx context.Context) (User, error) {
		return s.identityGetUser(partCtx, id)
	})
}

func (s MariaDBAuthStore) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {
	content := lf.SyntheticReference(struct { Username string; Email *string; RoleIDs []string }{patch.Username, patch.Email, patch.RoleIDs})
	return identityValueContent(ctx, s.identityGate, "identity.user.update", id, content, func(partCtx context.Context) (User, error) {
		return s.identityUpdateUser(partCtx, id, patch)
	})
}

func (s MariaDBAuthStore) SetUserStatus(ctx context.Context, id, status string) (User, error) {
	return identityValueContent(ctx, s.identityGate, "identity.user.status", id, lf.SyntheticReference(status), func(partCtx context.Context) (User, error) {
		return s.identitySetUserStatus(partCtx, id, status)
	})
}

func (s MariaDBAuthStore) DeleteUser(ctx context.Context, id string) error {
	return s.identityRun(ctx, "identity.user.delete", id, func(partCtx context.Context) error {
		return s.identityDeleteUser(partCtx, id)
	})
}

func (s MariaDBAuthStore) ResetPassword(ctx context.Context, id, temporaryPassword string) error {
	return s.identityRun(ctx, "identity.user.reset_password", id, func(partCtx context.Context) error {
		return s.identityResetPassword(partCtx, id, temporaryPassword)
	})
}

func (s MariaDBAuthStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	return identityValue(ctx, s.identityGate, "identity.user.count_admins", "users", s.identityCountActiveSuperAdmins)
}

func (s MariaDBAuthStore) UserHasRole(ctx context.Context, userID, roleName string) (bool, error) {
	return identityValueContent(ctx, s.identityGate, "identity.user.has_role", userID, lf.SyntheticReference(roleName), func(partCtx context.Context) (bool, error) {
		return s.identityUserHasRole(partCtx, userID, roleName)
	})
}

func (s MariaDBAuthStore) ListRoles(ctx context.Context) ([]Role, error) {
	return identityValue(ctx, s.identityGate, "identity.role.list", "roles", s.identityListRoles)
}

func (s MariaDBAuthStore) CreateRole(ctx context.Context, name string, permissions []string) (Role, error) {
	content := lf.SyntheticReference(struct { Name string; Permissions []string }{name, permissions})
	return identityValueContent(ctx, s.identityGate, "identity.role.create", "roles", content, func(partCtx context.Context) (Role, error) {
		return s.identityCreateRole(partCtx, name, permissions)
	})
}

func (s MariaDBAuthStore) GetRole(ctx context.Context, id string) (Role, error) {
	return identityValue(ctx, s.identityGate, "identity.role.read", id, func(partCtx context.Context) (Role, error) {
		return s.identityGetRole(partCtx, id)
	})
}

func (s MariaDBAuthStore) UpdateRole(ctx context.Context, id, name string, permissions []string) (Role, error) {
	content := lf.SyntheticReference(struct { Name string; Permissions []string }{name, permissions})
	return identityValueContent(ctx, s.identityGate, "identity.role.update", id, content, func(partCtx context.Context) (Role, error) {
		return s.identityUpdateRole(partCtx, id, name, permissions)
	})
}

func (s MariaDBAuthStore) DeleteRole(ctx context.Context, id string) error {
	return s.identityRun(ctx, "identity.role.delete", id, func(partCtx context.Context) error {
		return s.identityDeleteRole(partCtx, id)
	})
}

func (s MariaDBAuthStore) CountUsers(ctx context.Context) (int, error) {
	return identityValue(ctx, s.identityGate, "identity.user.count", "users", s.identityCountUsers)
}

func (s MariaDBAuthStore) FindUserByUsername(ctx context.Context, username string) (User, error) {
	return identityValueContent(ctx, s.identityGate, "identity.user.find", "users", lf.SyntheticReference(username), func(partCtx context.Context) (User, error) {
		return s.identityFindUserByUsername(partCtx, username)
	})
}

func (s MariaDBAuthStore) GetUserPermissions(ctx context.Context, id string) ([]string, error) {
	return identityValue(ctx, s.identityGate, "identity.user.permissions", id, func(partCtx context.Context) ([]string, error) {
		return s.identityGetUserPermissions(partCtx, id)
	})
}

func (s MariaDBAuthStore) ChangePassword(ctx context.Context, id, newPassword string) error {
	if s.identityGate != nil {
		// The old signature carries no verified Password/User snapshot.
		return lf.ErrHeld
	}
	return s.identityChangePassword(ctx, id, newPassword)
}

func (s MariaDBAuthStore) CreateSession(ctx context.Context, userID string, idleTTL, absoluteTTL time.Duration) (Session, error) {
	return identityValue(ctx, s.identityGate, "identity.session.create", userID, func(partCtx context.Context) (Session, error) {
		if s.identityGate != nil {
			if _, err := s.identityLockCurrentUser(partCtx, userID); err != nil {
				return Session{}, err
			}
		}
		return s.identityCreateSession(partCtx, userID, idleTTL, absoluteTTL)
	})
}

func (s MariaDBAuthStore) GetSession(ctx context.Context, rawToken string) (Session, error) {
	return identityValue(ctx, s.identityGate, "identity.session.read", "sessions", func(partCtx context.Context) (Session, error) {
		return s.identityGetSession(partCtx, rawToken)
	})
}

func (s MariaDBAuthStore) RefreshSession(ctx context.Context, rawToken string, idleTTL time.Duration) (Session, error) {
	return identityValue(ctx, s.identityGate, "identity.session.refresh", "sessions", func(partCtx context.Context) (Session, error) {
		if s.identityGate != nil {
			current, err := s.identityGetSession(partCtx, rawToken)
			if err != nil {
				return Session{}, err
			}
			if _, err := s.identityLockCurrentUser(partCtx, current.UserID); err != nil {
				return Session{}, err
			}
		}
		return s.identityRefreshSession(partCtx, rawToken, idleTTL)
	})
}

func (s MariaDBAuthStore) DeleteSession(ctx context.Context, rawToken string) error {
	return s.identityRun(ctx, "identity.session.delete", "sessions", func(partCtx context.Context) error {
		return s.identityDeleteSession(partCtx, rawToken)
	})
}

func (s MariaDBAuthStore) DeleteUserSessions(ctx context.Context, userID string) error {
	return s.identityRun(ctx, "identity.session.delete_user", userID, func(partCtx context.Context) error {
		return s.identityDeleteUserSessions(partCtx, userID)
	})
}

func (s MariaDBAuthStore) RecordLoginSuccess(ctx context.Context, userID, ip string) error {
	return s.identityRun(ctx, "identity.login.success", userID, func(partCtx context.Context) error {
		if s.identityGate != nil {
			q, err := s.identitySQL(partCtx)
			if err != nil {
				return err
			}
			if err := lockUserAdministration(partCtx, q); err != nil {
				return err
			}
			if _, err := s.identityLockCurrentUser(partCtx, userID); err != nil {
				return err
			}
		}
		return s.identityRecordLoginSuccess(partCtx, userID, ip)
	})
}

func (s MariaDBAuthStore) RecordLoginFailure(ctx context.Context, username string, lockoutThreshold int) error {
	return s.identityRun(ctx, "identity.login.failure", "users", func(partCtx context.Context) error {
		return s.identityRecordLoginFailure(partCtx, username, lockoutThreshold)
	})
}

// CompleteIdentityLogin compares the password-verified local snapshot after
// acquiring guard -> User. The snapshot and fresh tokens never enter the ledger.
func (s MariaDBAuthStore) CompleteIdentityLogin(ctx context.Context, snapshot User, ip string, idleTTL, absoluteTTL time.Duration) (Session, error) {
	if s.identityGate == nil {
		return Session{}, lf.ErrHeld
	}
	return identityValue(ctx, s.identityGate, "identity.session.issue_login", snapshot.ID, func(partCtx context.Context) (Session, error) {
		q, err := s.identitySQL(partCtx)
		if err != nil {
			return Session{}, err
		}
		if err := lockUserAdministration(partCtx, q); err != nil {
			return Session{}, err
		}
		current, err := s.identityLockCurrentUser(partCtx, snapshot.ID)
		if err != nil {
			return Session{}, err
		}
		if current.Status != snapshot.Status || current.PasswordHash != snapshot.PasswordHash {
			return Session{}, ErrUnauthorized
		}
		if err := s.identityRecordLoginSuccess(partCtx, snapshot.ID, ip); err != nil {
			return Session{}, err
		}
		return s.identityCreateSession(partCtx, snapshot.ID, idleTTL, absoluteTTL)
	})
}

// ChangeIdentityPassword uses the already verified snapshot only locally and
// shares password mutation and the existing session revocation in one owner.
func (s MariaDBAuthStore) ChangeIdentityPassword(ctx context.Context, snapshot User, newPassword string) error {
	if s.identityGate == nil {
		return lf.ErrHeld
	}
	return s.identityRun(ctx, "identity.password.change", snapshot.ID, func(partCtx context.Context) error {
		q, err := s.identitySQL(partCtx)
		if err != nil {
			return err
		}
		if err := lockUserAdministration(partCtx, q); err != nil {
			return err
		}
		current, err := s.identityLockCurrentUser(partCtx, snapshot.ID)
		if err != nil {
			return err
		}
		if current.Status != snapshot.Status || current.PasswordHash != snapshot.PasswordHash {
			return ErrUnauthorized
		}
		if err := s.identityChangePassword(partCtx, snapshot.ID, newPassword); err != nil {
			return err
		}
		return s.identityDeleteUserSessions(partCtx, snapshot.ID)
	})
}
