package store

import (
	"context"
	"testing"
)

func TestOperationalMemorySource(t *testing.T) {
	ctx := context.Background()
	auth := NewMemoryAuthStore()
	if err := auth.AddUser(User{ID: "human-one", Username: "synthetic-user", Status: "active", Roles: []string{"custom"}}, "synthetic-test-only-password", []string{"streams.read", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	before, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Roles) != 1 || len(before.Grants) != 2 {
		t.Fatal("real role expansion missing")
	}
	primary := before.Roles[0]
	second, err := auth.CreateRole(ctx, "second", []string{"roles.read", "streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.UpdateUser(ctx, "human-one", UserPatch{RoleIDs: []string{primary.ID, second.ID}}); err != nil {
		t.Fatal(err)
	}
	union, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(union.Roles) != 2 || len(union.Grants) != 4 || union.Revision <= before.Revision {
		t.Fatal("multiple role union/assignment revision not reflected")
	}
	if _, err := auth.UpdateRole(ctx, primary.ID, "custom", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	changed, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	foundRead, foundRoleRead := false, false
	for _, grant := range changed.Grants {
		if grant.Operation == "streams.start" {
			t.Fatal("revoked operation survived source update")
		}
		foundRead = foundRead || grant.Operation == "streams.read"
		foundRoleRead = foundRoleRead || grant.Operation == "roles.read"
	}
	if !foundRead || !foundRoleRead || changed.Revision <= union.Revision {
		t.Fatal("remaining role operations lost")
	}
	if _, err := auth.UpdateUser(ctx, "human-one", UserPatch{RoleIDs: []string{second.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.DeleteRole(ctx, primary.ID); err != nil {
		t.Fatal(err)
	}
	unassigned, err := auth.Snapshot(ctx, "human-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(unassigned.Roles) != 1 || unassigned.Roles[0].ID != second.ID {
		t.Fatal("role deletion/assignment removal stale")
	}
	if _, err := auth.Snapshot(ctx, "missing-human"); err == nil {
		t.Fatal("missing principal projected")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := auth.Snapshot(cancelled, "human-one"); err == nil {
		t.Fatal("cancelled source accepted")
	}
	if err := auth.AddUser(User{ID: "roleless", Username: "synthetic-roleless", Status: "active"}, "synthetic-test-only-password", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	roleless, err := auth.Snapshot(ctx, "roleless")
	if err != nil {
		t.Fatal(err)
	}
	if len(roleless.Grants) != 0 {
		t.Fatal("fixture cache converted to unassigned role")
	}
}
