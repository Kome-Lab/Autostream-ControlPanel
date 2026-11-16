package operationalauthorization

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func operationalFixture(t *testing.T, operations ...string) (Snapshot, *MemoryStore, *Service) {
	t.Helper()
	snapshot, err := Project(Principal{ID: "human-1", Kind: KindHuman, State: StateActive, RoleIDs: []string{"role-1"}}, []Role{{ID: "role-1", Name: "operator", Operations: operations}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, store, NewService(store)
}
func operationalRequest(operation string, scope Scope) Request {
	return Request{PrincipalID: "human-1", PrincipalKind: KindHuman, Operation: operation, Scope: scope}
}
func operationalGlobal() Scope { return Scope{Type: ScopeGlobal} }

type operationalFixedStore struct {
	snapshot Snapshot
	revision uint64
	err      error
}

func (s operationalFixedStore) Snapshot(context.Context, string) (Snapshot, error) {
	return cloneSnapshot(s.snapshot), s.err
}
func (s operationalFixedStore) Revision(context.Context) (uint64, error) { return s.revision, s.err }

func TestOperationalAllPermissionsAndRoleUnion(t *testing.T) {
	operations := AllOperations()
	if len(operations) != 99 {
		t.Fatalf("catalog count=%d", len(operations))
	}
	roles := []Role{{ID: "read-role", Name: "custom-read"}, {ID: "write-role", Name: "custom-write"}}
	for i, operation := range operations {
		roles[i%2].Operations = append(roles[i%2].Operations, operation)
	}
	snapshot, err := Project(Principal{ID: "human-1", Kind: KindHuman, State: StateActive, RoleIDs: []string{"read-role", "write-role"}}, roles, 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			decision, err := service.Authorize(context.Background(), operationalRequest(operation, operationalGlobal()))
			if err != nil || !decision.Allowed {
				t.Fatalf("expected allow: %+v %v", decision, err)
			}
		})
	}
	current, permissions, err := service.Current(context.Background(), "human-1", operationalGlobal())
	if err != nil || len(permissions) != 99 || len(current.Principal.RoleIDs) != 2 {
		t.Fatalf("union/current: %v %v", permissions, err)
	}
	empty, err := Project(Principal{ID: "human-1", Kind: KindHuman, State: StateActive}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if _, err := NewService(operationalFixedStore{snapshot: empty, revision: 1}).Authorize(context.Background(), operationalRequest(operation, operationalGlobal())); err == nil {
			t.Fatalf("unassigned allowed %s", operation)
		}
	}
}

func TestOperationalSuperAdminNameDoesNotGrantOperations(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "streams.read")
	snapshot.Roles[0].Name = "super_admin"
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.start", operationalGlobal())); err == nil {
		t.Fatal("role name granted unassigned operation")
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"evidence.issue", "bootstrap.approve", "trust.update", "recovery.quorum", "mutation_grant.consume", "host_self_update.consume", "*"} {
		if _, err := service.Authorize(context.Background(), operationalRequest(operation, operationalGlobal())); err == nil {
			t.Fatalf("foreign authority allowed %s", operation)
		}
	}
	request := operationalRequest("streams.read", operationalGlobal())
	request.PrincipalKind = "service_token"
	if _, err := service.Authorize(context.Background(), request); err == nil {
		t.Fatal("workload identity allowed")
	}
	snapshot.Principal.Kind = "host_agent"
	if _, err := NewService(operationalFixedStore{snapshot: snapshot, revision: 1}).Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err == nil {
		t.Fatal("workload snapshot allowed")
	}
}

func TestOperationalScopeAndSubject(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "streams.read")
	snapshot.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", Scope{Type: ScopeStream, ID: "stream-a"})); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{operationalGlobal(), {Type: ScopeStream, ID: "stream-b"}, {Type: ScopeHost, ID: "stream-a"}, {Type: ScopeStream, ID: "*"}, {Type: ScopeGlobal, ID: "unexpected"}, {}} {
		if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", scope)); err == nil {
			t.Fatalf("scope mismatch allowed %+v", scope)
		}
	}
	snapshot.Grants[0].Scope = operationalGlobal()
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", Scope{Type: ScopeStream, ID: "other-stream"})); err != nil {
		t.Fatal("legacy global narrowed", err)
	}
	snapshot.Grants[0].Subjects = SubjectConstraints{SelfOnly: true}
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	request := operationalRequest("streams.read", operationalGlobal())
	request.SubjectID = "human-1"
	if _, err := service.Authorize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.SubjectID = "human-2"
	if _, err := service.Authorize(context.Background(), request); err == nil {
		t.Fatal("subject mismatch allowed")
	}
	if permissions, err := service.Permissions(context.Background(), "human-1", operationalGlobal()); err != nil || len(permissions) != 0 {
		t.Fatalf("subject-constrained grant flattened: %v %v", permissions, err)
	}
	snapshot.Grants[0].Subjects = SubjectConstraints{SelfOnly: true, IDs: []string{"human-2"}}
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	request.SubjectID = "human-1"
	if _, err := service.Authorize(context.Background(), request); err == nil {
		t.Fatal("subject AND restriction treated as OR")
	}
}

func TestOperationalUnknownAndSnapshotIntegrity(t *testing.T) {
	base, _, _ := operationalFixture(t, "streams.read")
	cases := map[string]func(*Snapshot){
		"unknown snapshot": func(s *Snapshot) { s.State = StateUnknown },
		"held snapshot":    func(s *Snapshot) { s.State = StateHeld },
		"zero revision":    func(s *Snapshot) { s.Revision = 0 },
		"wrong principal":  func(s *Snapshot) { s.Principal.ID = "another" },
		"unassigned role":  func(s *Snapshot) { s.Principal.RoleIDs = nil },
		"missing role":     func(s *Snapshot) { s.Roles = nil },
		"missing grant":    func(s *Snapshot) { s.Grants = nil },
		"unknown operation": func(s *Snapshot) {
			s.Roles[0].Operations = []string{"trust.update"}
			s.Grants[0].Operation = "trust.update"
		},
		"unknown grant":         func(s *Snapshot) { s.Grants[0].State = StateUnknown },
		"held grant":            func(s *Snapshot) { s.Grants[0].State = StateHeld },
		"future grant":          func(s *Snapshot) { s.Grants[0].Revision = 2 },
		"wrong grant principal": func(s *Snapshot) { s.Grants[0].PrincipalID = "another" },
		"duplicate grant":       func(s *Snapshot) { s.Grants = append(s.Grants, s.Grants[0]) },
		"extra operation":       func(s *Snapshot) { s.Grants[0].Operation = "streams.start" },
		"unknown delegation":    func(s *Snapshot) { s.Grants[0].Delegation.Mode = "unchecked" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := cloneSnapshot(base)
			mutate(&snapshot)
			service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
			if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err == nil {
				t.Fatal("invalid state allowed")
			}
		})
	}
}

func TestOperationalFreshRevisionCancellationAndPending(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "streams.read")
	if _, err := NewService(operationalFixedStore{snapshot: snapshot, revision: 2}).Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale=%v", err)
	}
	request := operationalRequest("streams.read", operationalGlobal())
	request.ExpectedRevision = 2
	if _, err := NewService(operationalFixedStore{snapshot: snapshot, revision: 1}).Authorize(context.Background(), request); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected revision=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewService(operationalFixedStore{snapshot: snapshot, revision: 1}).Authorize(ctx, operationalRequest("streams.read", operationalGlobal())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewService(nil).Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err == nil {
		t.Fatal("nil store allowed")
	}
	if _, err := NewService(operationalFixedStore{snapshot: snapshot, revision: 1}).Authorize(nil, operationalRequest("streams.read", operationalGlobal())); err == nil {
		t.Fatal("nil context allowed")
	}
	snapshot.Principal.State = StatePendingPasswordChange
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err == nil {
		t.Fatal("pending-password principal allowed business operation")
	}
	current, permissions, err := service.Current(context.Background(), "human-1", operationalGlobal())
	if err != nil || current.Principal.State != StatePendingPasswordChange || len(permissions) != 0 {
		t.Fatalf("pending identity projection=%+v %v %v", current, permissions, err)
	}
}

func TestOperationalDelegation(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "roles.assign", "roles.create", "roles.update", "streams.read")
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	child := Grant{PrincipalID: "human-2", Operation: "streams.read", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}
	request := DelegationRequest{PrincipalID: "human-1", Action: "roles.assign", RoleName: "custom", Scope: operationalGlobal(), Grants: []Grant{child}}
	if err := service.ValidateDelegation(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Grants[0].Operation = "streams.start"
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("permission amplification allowed")
	}
	request.Grants[0] = child
	request.RoleName = "super_admin"
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("non-super assigned super_admin")
	}
	request.RoleName = "custom"
	for i := range snapshot.Grants {
		if snapshot.Grants[i].Operation == "streams.read" {
			snapshot.Grants[i].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
		}
	}
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("scoped authority delegated global")
	}
	request.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
	if err := service.ValidateDelegation(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-b"}
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("cross-stream delegated")
	}
	super, _, _ := operationalFixture(t, "roles.assign", "roles.create", "roles.update")
	super.Roles[0].Name = "super_admin"
	service = NewService(operationalFixedStore{snapshot: super, revision: 1})
	request.Grants[0] = Grant{PrincipalID: "human-2", Operation: "streams.start", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}
	if err := service.ValidateDelegation(context.Background(), request); err != nil {
		t.Fatal("existing assignment exception lost", err)
	}
	for _, action := range []string{"roles.create", "roles.update"} {
		request.Action = action
		if err := service.ValidateDelegation(context.Background(), request); err == nil {
			t.Fatalf("assignment exception leaked into %s", action)
		}
	}
	request.Action = "roles.assign"
	request.Grants[0].Operation = "bootstrap.approve"
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("super exception granted trust authority")
	}
	super, _, _ = operationalFixture(t, "streams.read")
	super.Roles[0].Name = "super_admin"
	service = NewService(operationalFixedStore{snapshot: super, revision: 1})
	request.Grants[0] = child
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("role name bypassed required entry operation")
	}
}

func TestOperationalRevocationCASRollbackAndHistory(t *testing.T) {
	snapshot, store, service := operationalFixture(t, "streams.read")
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err != nil {
		t.Fatal(err)
	}
	next, err := store.Revoke(context.Background(), 1, "human-1", snapshot.Grants[0].ID)
	if err != nil || next != 2 {
		t.Fatalf("revoke=%d %v", next, err)
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err == nil {
		t.Fatal("revoked grant allowed")
	}
	if _, err := store.Replace(context.Background(), 1, snapshot); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale replace=%v", err)
	}
	bad := cloneSnapshot(snapshot)
	bad.Grants[0].Scope = Scope{Type: ScopeType("unknown"), ID: "x"}
	if _, err := store.Replace(context.Background(), 2, bad); err == nil {
		t.Fatal("partial invalid replacement committed")
	}
	revision, err := store.Revision(context.Background())
	if err != nil || revision != 2 {
		t.Fatalf("failed mutation advanced generation=%d %v", revision, err)
	}
	history, err := store.History(context.Background())
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%v %v", history, err)
	}
	history[0].Checksum = "tampered"
	again, _ := store.History(context.Background())
	if again[0].Checksum == "tampered" {
		t.Fatal("history exposed mutable backing")
	}
	current, _ := store.Snapshot(context.Background(), "human-1")
	if current.Grants[0].State != StateRevoked {
		t.Fatal("failed replacement changed grant")
	}
	next, err = store.Replace(context.Background(), 2, current)
	if err != nil || next != 3 {
		t.Fatalf("identical-value revision/ABA=%d %v", next, err)
	}
	after, _ := store.History(context.Background())
	if len(after) != 3 || after[2].Generation != 3 {
		t.Fatalf("append history=%v", after)
	}
	current.Principal.RoleIDs[0] = "modified"
	current.Roles[0].Operations[0] = "streams.start"
	current.Grants[0].State = StateActive
	fresh, _ := store.Snapshot(context.Background(), "human-1")
	if fresh.Principal.RoleIDs[0] != "role-1" || fresh.Roles[0].Operations[0] != "streams.read" || fresh.Grants[0].State != StateRevoked {
		t.Fatal("snapshot exposed mutable backing")
	}
}

func TestOperationalConcurrentCAS(t *testing.T) {
	snapshot, store, _ := operationalFixture(t, "streams.read")
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.Replace(context.Background(), 1, snapshot)
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS success=%d conflict=%d", successes, conflicts)
	}
}

func TestOperationalRoleChangeAndDelete(t *testing.T) {
	principal := Principal{ID: "human-1", Kind: KindHuman, State: StateActive, RoleIDs: []string{"role-1", "role-2"}}
	roles := []Role{{ID: "role-1", Name: "reader", Operations: []string{"streams.read"}}, {ID: "role-2", Name: "starter", Operations: []string{"streams.start"}}}
	snapshot, err := Project(principal, roles, 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.start", operationalGlobal())); err != nil {
		t.Fatal(err)
	}
	roles[1].Operations = []string{"streams.stop"}
	replacement, err := Project(principal, roles, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(context.Background(), 1, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.start", operationalGlobal())); err == nil {
		t.Fatal("removed operation remained allowed")
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.stop", operationalGlobal())); err != nil {
		t.Fatal(err)
	}
	principal.RoleIDs = []string{"role-1"}
	replacement, err = Project(principal, roles[:1], 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(context.Background(), 2, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.stop", operationalGlobal())); err == nil {
		t.Fatal("deleted role operation remained allowed")
	}
	if _, err := service.Authorize(context.Background(), operationalRequest("streams.read", operationalGlobal())); err != nil {
		t.Fatal(err)
	}
}

func TestOperationalDelegationDisabledEntry(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "roles.assign", "streams.read")
	snapshot.Roles[0].Name = "super_admin"
	for i := range snapshot.Grants {
		if snapshot.Grants[i].Operation == "roles.assign" {
			snapshot.Grants[i].Delegation.Mode = DelegationNone
		}
	}
	request := DelegationRequest{PrincipalID: "human-1", Action: "roles.assign", RoleName: "custom", Scope: operationalGlobal(), Grants: []Grant{{PrincipalID: "human-2", Operation: "streams.read", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}}}
	if err := NewService(operationalFixedStore{snapshot: snapshot, revision: 1}).ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("disabled delegation was bypassed by super_admin")
	}
}

func TestOperationalDelegationScopeBoundary(t *testing.T) {
	snapshot, _, _ := operationalFixture(t, "roles.assign")
	snapshot.Roles[0].Name = "super_admin"
	snapshot.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
	service := NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	child := Grant{PrincipalID: "human-2", Operation: "streams.start", Scope: operationalGlobal(), Delegation: DelegationConstraints{Mode: DelegationSubset}}
	request := DelegationRequest{PrincipalID: "human-1", Action: "roles.assign", RoleName: "custom", Scope: Scope{Type: ScopeStream, ID: "stream-a"}, SubjectID: "human-2", Grants: []Grant{child}}
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("scoped roles.assign expanded a Global grant through the super_admin exception")
	}
	request.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
	if err := service.ValidateDelegation(context.Background(), request); err != nil {
		t.Fatal("same-scope operational assignment exception lost", err)
	}
	request.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-b"}
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("cross-stream delegation allowed")
	}
	request.Grants[0].Scope = Scope{Type: ScopeStream, ID: "stream-a"}
	request.Grants[0].PrincipalID = "human-3"
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("delegated Principal differed from checked Subject")
	}
	snapshot.Grants[0].Subjects = SubjectConstraints{SelfOnly: true}
	service = NewService(operationalFixedStore{snapshot: snapshot, revision: 1})
	request.SubjectID = "human-1"
	request.Grants[0].PrincipalID = "human-2"
	if err := service.ValidateDelegation(context.Background(), request); err == nil {
		t.Fatal("self-only assignment delegated to another Human")
	}
}
