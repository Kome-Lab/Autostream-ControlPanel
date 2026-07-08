package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func (s *MemoryAuthStore) ListUsers(ctx context.Context) ([]User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, publicUserCopy(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, nil
}

func (s *MemoryAuthStore) CreateUser(ctx context.Context, username, email, temporaryPassword string, roleIDs []string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(temporaryPassword)
	if err != nil {
		return User{}, err
	}
	email, err = normalizeUserEmail(email)
	if err != nil {
		return User{}, err
	}
	user := User{ID: newUUID(), Username: username, Email: email, Status: "pending_password_change", PasswordHash: hash}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byUsername[username]; exists {
		return User{}, errors.New("username already exists")
	}
	user.Roles = s.roleNamesLocked(roleIDs)
	s.users[user.ID] = user
	s.byUsername[user.Username] = user.ID
	return publicUserCopy(user), nil
}

func (s *MemoryAuthStore) CreateOAuthUser(ctx context.Context, username, email string, roleIDs []string) (User, error) {
	if err := ctx.Err(); err != nil {
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
	email, err = normalizeUserEmail(email)
	if err != nil {
		return User{}, err
	}
	user := User{ID: newUUID(), Username: username, Email: email, Status: "active", PasswordHash: hash}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byUsername[username]; exists {
		return User{}, errors.New("username already exists")
	}
	user.Roles = s.roleNamesLocked(roleIDs)
	s.users[user.ID] = user
	s.byUsername[user.Username] = user.ID
	return publicUserCopy(user), nil
}

func (s *MemoryAuthStore) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if patch.Username != "" && patch.Username != user.Username {
		delete(s.byUsername, user.Username)
		user.Username = patch.Username
		s.byUsername[user.Username] = user.ID
	}
	if patch.Email != nil {
		email, err := normalizeUserEmail(*patch.Email)
		if err != nil {
			return User{}, err
		}
		user.Email = email
	}
	if patch.RoleIDs != nil {
		user.Roles = s.roleNamesLocked(patch.RoleIDs)
	}
	s.users[id] = user
	return publicUserCopy(user), nil
}

func (s *MemoryAuthStore) SetUserStatus(ctx context.Context, id, status string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if hasRole(user, "super_admin") && (status == "disabled" || status == "locked") && s.countActiveSuperAdminsLocked() <= 1 {
		return User{}, ErrLastSuperAdmin
	}
	user.Status = status
	s.users[id] = user
	return publicUserCopy(user), nil
}

func (s *MemoryAuthStore) DeleteUser(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	if hasRole(user, "super_admin") && user.Status == "active" && s.countActiveSuperAdminsLocked() <= 1 {
		return ErrLastSuperAdmin
	}
	delete(s.users, id)
	delete(s.byUsername, user.Username)
	delete(s.permissions, id)
	delete(s.mfaConfigs, id)
	for challengeID, challenge := range s.mfaChallenges {
		if challenge.UserID == id {
			delete(s.mfaChallenges, challengeID)
		}
	}
	for hash, session := range s.sessions {
		if session.UserID == id {
			delete(s.sessions, hash)
		}
	}
	for credentialID, credential := range s.passkeys {
		if credential.UserID == id {
			delete(s.passkeys, credentialID)
		}
	}
	for challengeID, challenge := range s.passkeyReg {
		if challenge.UserID == id {
			delete(s.passkeyReg, challengeID)
		}
	}
	for sessionID, session := range s.passkeySess {
		if session.UserID == id {
			delete(s.passkeySess, sessionID)
		}
	}
	return nil
}

func (s *MemoryAuthStore) ResetPassword(ctx context.Context, id, temporaryPassword string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash, err := security.HashPassword(temporaryPassword)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = hash
	user.Status = "pending_password_change"
	s.users[id] = user
	return nil
}

func (s *MemoryAuthStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countActiveSuperAdminsLocked(), nil
}

func (s *MemoryAuthStore) UserHasRole(ctx context.Context, userID, roleName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return hasRole(s.users[userID], roleName), nil
}

func (s *MemoryAuthStore) ListRoles(ctx context.Context) ([]Role, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	roles := make([]Role, 0, len(s.roles))
	for _, role := range s.roles {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
	return roles, nil
}

func (s *MemoryAuthStore) CreateRole(ctx context.Context, name string, permissions []string) (Role, error) {
	if err := ctx.Err(); err != nil {
		return Role{}, err
	}
	if err := validatePermissions(permissions); err != nil {
		return Role{}, err
	}
	role := Role{ID: newUUID(), Name: name, Permissions: append([]string(nil), permissions...), CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roles[role.ID] = role
	return role, nil
}

func (s *MemoryAuthStore) GetRole(ctx context.Context, id string) (Role, error) {
	if err := ctx.Err(); err != nil {
		return Role{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	role, ok := s.roles[id]
	if !ok {
		return Role{}, ErrNotFound
	}
	return role, nil
}

func (s *MemoryAuthStore) UpdateRole(ctx context.Context, id, name string, permissions []string) (Role, error) {
	if err := ctx.Err(); err != nil {
		return Role{}, err
	}
	if err := validatePermissions(permissions); err != nil {
		return Role{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	role, ok := s.roles[id]
	if !ok {
		return Role{}, ErrNotFound
	}
	role.Name = name
	role.Permissions = append([]string(nil), permissions...)
	s.roles[id] = role
	return role, nil
}

func (s *MemoryAuthStore) DeleteRole(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	role, ok := s.roles[id]
	if !ok {
		return ErrNotFound
	}
	if role.Name == "super_admin" {
		return ErrLastSuperAdmin
	}
	delete(s.roles, id)
	for userID, user := range s.users {
		filtered := user.Roles[:0]
		for _, roleName := range user.Roles {
			if roleName != role.Name {
				filtered = append(filtered, roleName)
			}
		}
		user.Roles = filtered
		s.users[userID] = user
	}
	return nil
}

func (s *MemoryAuthStore) roleNamesLocked(roleIDs []string) []string {
	names := make([]string, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		if role, ok := s.roles[roleID]; ok {
			names = append(names, role.Name)
		}
	}
	return names
}

func (s *MemoryAuthStore) countActiveSuperAdminsLocked() int {
	count := 0
	for _, user := range s.users {
		if user.Status == "active" && hasRole(user, "super_admin") {
			count++
		}
	}
	return count
}

func hasRole(user User, roleName string) bool {
	for _, role := range user.Roles {
		if role == roleName {
			return true
		}
	}
	return false
}

func publicUserCopy(user User) User {
	user.PasswordHash = ""
	return user
}
