package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerUserRoleRoutes() {
	s.mux.HandleFunc("GET /permissions", s.requirePermission("roles.read", s.listPermissions))
	s.mux.HandleFunc("GET /users", s.requirePermission("users.read", s.listUsers))
	s.mux.HandleFunc("POST /users", s.requirePermission("users.create", s.createUser))
	s.mux.HandleFunc("GET /users/{id}", s.requirePermission("users.read", s.getUser))
	s.mux.HandleFunc("PUT /users/{id}", s.requirePermission("users.update", s.updateUser))
	s.mux.HandleFunc("POST /users/{id}/disable", s.requirePermission("users.disable", s.disableUser))
	s.mux.HandleFunc("POST /users/{id}/lock", s.requirePermission("users.disable", s.lockUser))
	s.mux.HandleFunc("POST /users/{id}/unlock", s.requirePermission("users.update", s.unlockUser))
	s.mux.HandleFunc("POST /users/{id}/reset-password", s.requirePermission("users.reset_password", s.resetPassword))
	s.mux.HandleFunc("POST /users/{id}/force-password-change", s.requirePermission("users.reset_password", s.forcePasswordChange))
	s.mux.HandleFunc("DELETE /users/{id}", s.requirePermission("users.delete", s.deleteUser))
	s.mux.HandleFunc("GET /users/{id}/oauth-links", s.requirePermission("users.read", s.listUserOAuthLinks))
	s.mux.HandleFunc("POST /users/{id}/oauth-links", s.requirePermission("users.manage_mfa", s.createUserOAuthLink))
	s.mux.HandleFunc("DELETE /users/{id}/oauth-links/{link_id}", s.requirePermission("users.manage_mfa", s.deleteUserOAuthLink))
	s.mux.HandleFunc("GET /roles", s.requirePermission("roles.read", s.listRoles))
	s.mux.HandleFunc("POST /roles", s.requirePermission("roles.create", s.createRole))
	s.mux.HandleFunc("GET /roles/{id}", s.requirePermission("roles.read", s.getRole))
	s.mux.HandleFunc("PUT /roles/{id}", s.requirePermission("roles.update", s.updateRole))
	s.mux.HandleFunc("DELETE /roles/{id}", s.requirePermission("roles.delete", s.deleteRole))
}

func (s *Server) listPermissions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, security.DefaultPermissions)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_users_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicUsers(users))
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username          string   `json:"username"`
		Email             string   `json:"email"`
		TemporaryPassword string   `json:"temporary_password"`
		RoleIDs           []string `json:"role_ids"`
		SendWelcomeEmail  bool     `json:"send_welcome_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "username_required"})
		return
	}
	email, ok := normalizeSMTPTestRecipient(body.Email)
	if !ok {
		code := "invalid_email"
		if strings.TrimSpace(body.Email) == "" {
			code = "email_required"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {
		return
	}
	if len(body.RoleIDs) > 0 && !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	if len(body.RoleIDs) > 0 {
		if err := s.validateRoleAssignments(r.Context(), body.RoleIDs); err != nil {
			writeRoleAssignmentError(w, err)
			return
		}
	}
	user, err := s.users.CreateUser(r.Context(), username, email, body.TemporaryPassword, body.RoleIDs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	emailSent := false
	if body.SendWelcomeEmail && user.Email != "" {
		if err := s.sendUserWelcomeEmail(r, user); err != nil {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.email_welcome", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": safeErrorCode(err)}})
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "welcome_email_failed"})
			return
		}
		emailSent = true
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.create", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"username": user.Username, "email_present": user.Email != "", "welcome_email_sent": emailSent}})
	writeJSON(w, http.StatusCreated, publicUser(user))
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string   `json:"username"`
		Email    *string  `json:"email"`
		RoleIDs  []string `json:"role_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.RoleIDs != nil && !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	if body.RoleIDs != nil {
		if err := s.validateUserRoleUpdate(r.Context(), r.PathValue("id"), body.RoleIDs); err != nil {
			status := http.StatusInternalServerError
			code := "validate_user_roles_failed"
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
				code = "not_found"
			}
			if errors.Is(err, errCannotUpdateOwnRoles) {
				status = http.StatusForbidden
				code = "cannot_update_own_roles"
			}
			if errors.Is(err, store.ErrLastSuperAdmin) {
				status = http.StatusConflict
				code = "last_super_admin"
			}
			if errors.Is(err, store.ErrSuperAdminAssignmentForbidden) {
				status = http.StatusForbidden
				code = "cannot_assign_super_admin"
			}
			if errors.Is(err, store.ErrPermissionEscalation) {
				status = http.StatusForbidden
				code = "permission_escalation"
			}
			if errors.Is(err, store.ErrUnknownPermission) {
				status = http.StatusBadRequest
				code = "invalid_permissions"
			}
			if errors.Is(err, errInvalidRoleAssignment) {
				status = http.StatusBadRequest
				code = "invalid_role_assignment"
			}
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
	}
	user, err := s.users.UpdateUser(r.Context(), r.PathValue("id"), store.UserPatch{Username: strings.TrimSpace(body.Username), Email: body.Email, RoleIDs: body.RoleIDs})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.update", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	writeJSON(w, http.StatusOK, publicUser(user))
}

var (
	errCannotUpdateOwnRoles  = errors.New("cannot update own role assignments")
	errInvalidRoleAssignment = errors.New("invalid role assignment")
)

func (s *Server) validateUserRoleUpdate(ctx context.Context, targetUserID string, roleIDs []string) error {
	current := currentFromContext(ctx)
	if targetUserID == current.User.ID {
		return errCannotUpdateOwnRoles
	}
	target, err := s.users.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}
	if err := s.validateRoleAssignments(ctx, roleIDs); err != nil {
		return err
	}
	if !userHasRoleName(target, "super_admin") || target.Status != "active" {
		return nil
	}
	includesSuperAdmin, err := s.roleIDsIncludeRoleName(ctx, roleIDs, "super_admin")
	if err != nil {
		return err
	}
	if includesSuperAdmin {
		return nil
	}
	count, err := s.users.CountActiveSuperAdmins(ctx)
	if err != nil {
		return err
	}
	if count <= 1 {
		return store.ErrLastSuperAdmin
	}
	return nil
}

func (s *Server) validateRoleAssignments(ctx context.Context, roleIDs []string) error {
	roles := make([]store.Role, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		role, err := s.roles.GetRole(ctx, roleID)
		if errors.Is(err, store.ErrNotFound) {
			return errInvalidRoleAssignment
		}
		if err != nil {
			return err
		}
		roles = append(roles, role)
	}
	current := currentFromContext(ctx)
	if err := store.ValidateRoleAssignment(current.User, roles); err != nil {
		return err
	}
	if userHasRoleName(current.User, "super_admin") {
		return nil
	}
	for _, role := range roles {
		if err := store.ValidateRolePermissions(current.Permissions, role.Permissions); err != nil {
			return err
		}
	}
	return nil
}

func writeRoleAssignmentError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "validate_user_roles_failed"
	if errors.Is(err, store.ErrSuperAdminAssignmentForbidden) {
		status = http.StatusForbidden
		code = "cannot_assign_super_admin"
	} else if errors.Is(err, store.ErrPermissionEscalation) {
		status = http.StatusForbidden
		code = "permission_escalation"
	} else if errors.Is(err, store.ErrUnknownPermission) {
		status = http.StatusBadRequest
		code = "invalid_permissions"
	} else if errors.Is(err, errInvalidRoleAssignment) {
		status = http.StatusBadRequest
		code = "invalid_role_assignment"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) roleIDsIncludeRoleName(ctx context.Context, roleIDs []string, roleName string) (bool, error) {
	for _, roleID := range roleIDs {
		role, err := s.roles.GetRole(ctx, roleID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if role.Name == roleName {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "disabled", "users.disable")
}

func (s *Server) lockUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "locked", "users.lock")
}

func (s *Server) unlockUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "active", "users.unlock")
}

func (s *Server) forcePasswordChange(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "pending_password_change", "users.force_password_change")
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidateUserStatusActor(current.User, target); errors.Is(err, store.ErrSuperAdminStatusForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_change_super_admin_status"})
		return
	}
	user, err := s.users.SetUserStatus(r.Context(), target.ID, status)
	if errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "set_user_status_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"status": status}})
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if target.ID == current.User.ID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "cannot_delete_self"})
		return
	}
	if err := store.ValidateUserStatusActor(current.User, target); errors.Is(err, store.ErrSuperAdminStatusForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_delete_super_admin"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") {
		targetPermissions, err := s.auth.GetUserPermissions(r.Context(), target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_permissions_failed"})
			return
		}
		if err := store.ValidateRolePermissions(current.Permissions, targetPermissions); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
	}
	if err := s.users.DeleteUser(r.Context(), target.ID); errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_user_failed"})
		return
	}
	_ = s.auth.DeleteUserSessions(r.Context(), target.ID)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.delete", ResourceType: "user", ResourceID: target.ID, Result: "success", Metadata: map[string]any{"username": target.Username, "roles": target.Roles}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidatePasswordResetActor(current.User, target); err != nil {
		code := "permission_escalation"
		if errors.Is(err, store.ErrSuperAdminPasswordResetForbidden) {
			code = "cannot_reset_super_admin_password"
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"code": code})
		return
	}
	if !userHasRoleName(current.User, "super_admin") {
		targetPermissions, err := s.auth.GetUserPermissions(r.Context(), target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_permissions_failed"})
			return
		}
		if err := store.ValidateRolePermissions(current.Permissions, targetPermissions); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {
		return
	}
	if err := s.users.ResetPassword(r.Context(), target.ID, body.TemporaryPassword); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "reset_password_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.reset_password", ResourceType: "user", ResourceID: target.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.roles.ListRoles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_roles_failed"})
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidateRolePermissions(current.Permissions, body.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	role, err := s.roles.CreateRole(r.Context(), strings.TrimSpace(body.Name), body.Permissions)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.create", ResourceType: "role", ResourceID: role.ID, Result: "success", Metadata: map[string]any{"name": role.Name}})
	writeJSON(w, http.StatusCreated, role)
}

func (s *Server) getRole(w http.ResponseWriter, r *http.Request) {
	role, err := s.roles.GetRole(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.roles.GetRole(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if existing.Name == "super_admin" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "protected_super_admin_role"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") && userHasRoleName(current.User, existing.Name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_update_own_role"})
		return
	}
	if err := store.ValidateRolePermissions(current.Permissions, body.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	role, err := s.roles.UpdateRole(r.Context(), id, strings.TrimSpace(body.Name), body.Permissions)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.update", ResourceType: "role", ResourceID: role.ID, Result: "success"})
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.roles.GetRole(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if existing.Name == "super_admin" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "protected_super_admin_role"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") && userHasRoleName(current.User, existing.Name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_delete_own_role"})
		return
	}
	if err := store.ValidateRolePermissions(current.Permissions, existing.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	err = s.roles.DeleteRole(r.Context(), id)
	if errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.delete", ResourceType: "role", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func publicUser(user store.User) map[string]any {
	roles := append([]string{}, user.Roles...)
	roleIDs := append([]string{}, user.RoleIDs...)
	return map[string]any{"id": user.ID, "username": user.Username, "email": user.Email, "status": user.Status, "roles": roles, "role_ids": roleIDs, "last_login_at": user.LastLoginAt, "last_login_ip": user.LastLoginIP}
}

func publicUsers(users []store.User) []map[string]any {
	out := make([]map[string]any, 0, len(users))
	for _, user := range users {
		out = append(out, publicUser(user))
	}
	return out
}
