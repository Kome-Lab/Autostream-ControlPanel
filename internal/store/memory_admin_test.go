package store

import (
	"errors"
	"testing"
)

func TestDeleteUserPreservesLastActiveSuperAdmin(t *testing.T) {
	auth := NewMemoryAuthStore()
	if err := auth.AddUser(User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.DeleteUser(t.Context(), "admin-id"); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("expected ErrLastSuperAdmin, got %v", err)
	}
	if _, err := auth.GetUser(t.Context(), "admin-id"); err != nil {
		t.Fatalf("last active super_admin should remain: %v", err)
	}
}

func TestDeleteUserAllowsSuperAdminWhenAnotherActiveSuperAdminExists(t *testing.T) {
	auth := NewMemoryAuthStore()
	if err := auth.AddUser(User{ID: "admin-1", Username: "admin1", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(User{ID: "admin-2", Username: "admin2", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.DeleteUser(t.Context(), "admin-2"); err != nil {
		t.Fatalf("second active super_admin should be deletable: %v", err)
	}
	if _, err := auth.GetUser(t.Context(), "admin-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted super_admin should be gone, got %v", err)
	}
}
