package store

import (
	"errors"
	"testing"
)

func TestValidateRoleAssignment(t *testing.T) {
	superAdminRole := Role{Name: "super_admin"}

	if err := ValidateRoleAssignment(User{Roles: []string{"admin"}}, []Role{superAdminRole}); !errors.Is(err, ErrSuperAdminAssignmentForbidden) {
		t.Fatalf("non-super_admin assignment error = %v", err)
	}
	if err := ValidateRoleAssignment(User{Roles: []string{"super_admin"}}, []Role{superAdminRole}); err != nil {
		t.Fatalf("super_admin assignment error = %v", err)
	}
	if err := ValidateRoleAssignment(User{Roles: []string{"admin"}}, []Role{{Name: "viewer"}}); err != nil {
		t.Fatalf("ordinary role assignment error = %v", err)
	}
}

func TestValidateRolePermissions(t *testing.T) {
	actorPermissions := []string{"roles.create", "roles.update", "streams.read"}

	if err := ValidateRolePermissions(actorPermissions, []string{"streams.read"}); err != nil {
		t.Fatalf("subset permissions error = %v", err)
	}
	if err := ValidateRolePermissions(actorPermissions, []string{"streams.start"}); !errors.Is(err, ErrPermissionEscalation) {
		t.Fatalf("excess permissions error = %v", err)
	}
	if err := ValidateRolePermissions(actorPermissions, []string{"unknown.permission"}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("unknown permissions error = %v", err)
	}
}

func TestValidatePasswordResetActor(t *testing.T) {
	target := User{Roles: []string{"super_admin"}}

	if err := ValidatePasswordResetActor(User{Roles: []string{"admin"}}, target); !errors.Is(err, ErrSuperAdminPasswordResetForbidden) {
		t.Fatalf("non-super_admin reset error = %v", err)
	}
	if err := ValidatePasswordResetActor(User{Roles: []string{"super_admin"}}, target); err != nil {
		t.Fatalf("super_admin reset error = %v", err)
	}
	if err := ValidatePasswordResetActor(User{Roles: []string{"admin"}}, User{Roles: []string{"viewer"}}); err != nil {
		t.Fatalf("ordinary user reset error = %v", err)
	}
}
