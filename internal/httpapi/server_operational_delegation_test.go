package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/store"
)

func operationalDelegationFixture(t *testing.T, operations []string, nonDelegable ...string) (*store.MemoryAuthStore, *oa.MemoryStore, *Server, *http.Cookie, string) {
	t.Helper()
	auth, _, cookie, csrf := operationalHTTPFixture(t, operations, "delegating-operator")
	snapshot, err := auth.Snapshot(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Grants {
		if slices.Contains(nonDelegable, snapshot.Grants[i].Operation) {
			snapshot.Grants[i].Delegation.Mode = oa.DelegationNone
		}
	}
	source, err := oa.NewMemoryStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(source))
	return auth, source, server, cookie, csrf
}

func operationalPlannedGrant(operation, principal string) oa.Grant {
	return oa.Grant{Operation: operation, PrincipalID: principal, Scope: oa.Scope{Type: oa.ScopeGlobal}, Delegation: oa.DelegationConstraints{Mode: oa.DelegationSubset}, State: oa.StateActive, Revision: 1}
}

func TestOperationalHTTPDelegationCreateRejectsNonDelegableOperation(t *testing.T) {
	auth, source, server, cookie, csrf := operationalDelegationFixture(t, []string{"roles.create", "streams.start"}, "streams.start")
	service := oa.NewService(source)
	typed := service.ValidateDelegation(t.Context(), oa.DelegationRequest{PrincipalID: "human-s13", Action: "roles.create", RoleName: "delegated-start", Scope: oa.Scope{Type: oa.ScopeGlobal}, Grants: []oa.Grant{operationalPlannedGrant("streams.start", "")}})
	if !errors.Is(typed, oa.ErrDenied) {
		t.Fatalf("typed rejection not reproduced: %v", typed)
	}
	before, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	res := operationalRequest(t, server, cookie, csrf, "POST", "/roles", `{"name":"delegated-start","permissions":["streams.start"]}`)
	after, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("typed_denied=%v HTTP=%d roles_before=%d roles_after=%d", errors.Is(typed, oa.ErrDenied), res.Code, len(before), len(after))
	if res.Code != http.StatusForbidden {
		t.Errorf("nondelegable operation reached Role save: status=%d", res.Code)
	}
	if len(after) != len(before) {
		t.Error("Role persisted despite nondelegable operation")
	}
	if _, err := service.Authorize(t.Context(), oa.Request{PrincipalID: "human-s13", Operation: "streams.start", Scope: oa.Scope{Type: oa.ScopeGlobal}}); err != nil {
		t.Errorf("own use was removed: %v", err)
	}
	permissions, err := service.Permissions(t.Context(), "human-s13", oa.Scope{Type: oa.ScopeGlobal})
	if err != nil || !slices.Contains(permissions, "streams.start") {
		t.Error("None grant disappeared from ordinary permissions")
	}
}

func TestOperationalHTTPDelegationAssignRejectsNonDelegableEntry(t *testing.T) {
	auth, source, server, cookie, csrf := operationalDelegationFixture(t, []string{"users.update", "roles.assign", "streams.start"}, "roles.assign")
	role, err := auth.CreateRole(t.Context(), "start-role", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := auth.CreateOAuthUser(t.Context(), "delegation-target", "delegation-target@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	typed := oa.NewService(source).ValidateDelegation(t.Context(), oa.DelegationRequest{PrincipalID: "human-s13", Action: "roles.assign", RoleName: role.Name, SubjectID: target.ID, Scope: oa.Scope{Type: oa.ScopeGlobal}, Grants: []oa.Grant{operationalPlannedGrant("streams.start", target.ID)}})
	if !errors.Is(typed, oa.ErrDenied) {
		t.Fatalf("typed entry rejection not reproduced: %v", typed)
	}
	body, _ := json.Marshal(map[string]any{"role_ids": []string{role.ID}})
	res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
	after, err := auth.GetUser(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("typed_denied=%v HTTP=%d assigned_roles_before=%d assigned_roles_after=%d", errors.Is(typed, oa.ErrDenied), res.Code, len(target.RoleIDs), len(after.RoleIDs))
	if res.Code != http.StatusForbidden {
		t.Errorf("nondelegable roles.assign reached User save: status=%d", res.Code)
	}
	if len(after.RoleIDs) != 0 {
		t.Error("User Role assignment persisted despite nondelegable entry")
	}
}

var operationalDelegationOperations = []string{"roles.create", "roles.update", "roles.assign", "roles.read", "users.create", "users.update", "integrations.create", "integrations.update", "streams.start"}

// Capture complete records to detect partial mutations in registered HTTP paths.
func operationalDelegationState(t *testing.T, auth *store.MemoryAuthStore, server *Server) string {
	t.Helper()
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	providers, err := server.integrations.ListOAuthProviders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal([]any{roles, users, providers})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func operationalDelegationMutation(t *testing.T, auth *store.MemoryAuthStore, server *Server, route string) (string, string, string, int) {
	t.Helper()
	role, err := auth.CreateRole(t.Context(), "existing-start", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	method, target, status := "POST", "/roles", 201
	body := map[string]any{"name": "delegated-start", "permissions": []string{"streams.start"}}
	switch route {
	case "role-update":
		method, target, status = "PUT", "/roles/"+role.ID, 200
	case "user-create":
		target = "/users"
		body = map[string]any{"username": "new-target", "email": "new-target@example.test", "temporary_password": "Synthetic-Strong-Pass-921!", "role_ids": []string{role.ID}}
	case "user-update":
		user, err := auth.CreateOAuthUser(t.Context(), "existing-target", "existing-target@example.test", nil)
		if err != nil {
			t.Fatal(err)
		}
		method, target, status = "PUT", "/users/"+user.ID, 200
		body = map[string]any{"username": "changed-target", "email": "changed-target@example.test", "role_ids": []string{role.ID}}
	case "provider-create", "provider-update":
		target = "/integrations/oauth-providers"
		body = map[string]any{"provider_type": "google", "name": "delegation-provider", "enabled": true, "client_id": "synthetic-client", "client_secret": "synthetic-only", "scopes": []string{"openid", "email"}, "auto_provision": true, "default_role_ids": []string{role.ID}, "redirect_uri": "https://control.example.test/auth/oauth/callback"}
		if route == "provider-update" {
			p, err := server.integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{ProviderType: "google", Name: "existing-provider", ClientID: "synthetic-client", RedirectURI: "https://control.example.test/auth/oauth/callback"})
			if err != nil {
				t.Fatal(err)
			}
			method, target, status = "PUT", target+"/"+p.ID, 200
		}
	}
	// Untrusted claims cannot narrow the Global/Subset effect stored by v2.
	body["actor"] = "super_admin"
	body["principal_id"] = "forged"
	body["scope_type"] = "stream"
	body["scope_id"] = "stream-a"
	body["subject_id"] = "forged"
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return method, target, string(data), status
}

func TestOperationalHTTPDelegationMutationMatrix(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.test")
	for _, route := range []string{"role-create", "role-update", "user-create", "user-update", "provider-create", "provider-update"} {
		for _, mode := range []string{"subset", "operation-none", "entry-none"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				var none []string
				if mode == "operation-none" {
					none = []string{"streams.start"}
				}
				if mode == "entry-none" {
					entry := "roles.assign"
					if route == "role-create" {
						entry = "roles.create"
					}
					if route == "role-update" {
						entry = "roles.update"
					}
					none = []string{entry}
				}
				auth, _, server, cookie, csrf := operationalDelegationFixture(t, operationalDelegationOperations, none...)
				server.integrations = store.NewMemoryIntegrationStore()
				method, target, body, status := operationalDelegationMutation(t, auth, server, route)
				before := operationalDelegationState(t, auth, server)
				res := operationalRequest(t, server, cookie, csrf, method, target, body)
				after := operationalDelegationState(t, auth, server)
				if mode != "subset" {
					status = 403
					if before != after {
						t.Error("denied request changed persisted records")
					}
				}
				if res.Code != status {
					t.Fatalf("HTTP=%d want=%d body=%s", res.Code, status, res.Body.String())
				}
				if mode == "subset" && before == after {
					t.Fatal("allowed mutation was not saved")
				}
			})
		}
	}
}

func TestOperationalHTTPDelegationOwnUseAndRoleUnion(t *testing.T) {
	auth, source, server, cookie, csrf := operationalDelegationFixture(t, []string{"roles.create", "streams.start"}, "streams.start")
	// The actual start Handler returns its missing-target error, with no dispatcher.
	res := operationalRequest(t, server, cookie, csrf, "POST", "/streams/missing-synthetic-stream/start", "{}")
	if res.Code != 404 {
		t.Fatalf("ordinary start no longer reaches Handler: %d %s", res.Code, res.Body.String())
	}
	role, err := auth.CreateRole(t.Context(), "other-delegator", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.GetUser(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.UpdateUser(t.Context(), actor.ID, store.UserPatch{RoleIDs: append(actor.RoleIDs, role.ID)}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := auth.Snapshot(t.Context(), actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Grants {
		if snapshot.Grants[i].RoleID != role.ID && snapshot.Grants[i].Operation == "streams.start" {
			snapshot.Grants[i].Delegation.Mode = oa.DelegationNone
		}
	}
	if _, err = source.Replace(t.Context(), 1, snapshot); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"name": "union-start", "permissions": []string{"streams.start"}})
	res = operationalRequest(t, server, cookie, csrf, "POST", "/roles", string(body))
	if res.Code != 201 {
		t.Fatalf("legitimate grant in second Role lost: %d %s", res.Code, res.Body.String())
	}
}

func TestOperationalHTTPDelegationScopeAndSubject(t *testing.T) {
	for _, mode := range []string{"scoped-operation", "split-scopes", "subject-operation", "self-operation", "scoped-entry", "subject-entry", "split-subjects"} {
		t.Run(mode, func(t *testing.T) {
			auth, source, server, cookie, csrf := operationalDelegationFixture(t, operationalDelegationOperations)
			snapshot, err := source.Snapshot(t.Context(), "human-s13")
			if err != nil {
				t.Fatal(err)
			}
			for i := range snapshot.Grants {
				g := &snapshot.Grants[i]
				if g.Operation == "streams.start" {
					switch mode {
					case "scoped-operation", "split-scopes":
						g.Scope = oa.Scope{Type: oa.ScopeStream, ID: "stream-a"}
					case "subject-operation", "split-subjects":
						g.Subjects = oa.SubjectConstraints{IDs: []string{"allowed-subject"}}
					case "self-operation":
						g.Subjects.SelfOnly = true
					}
				}
				if g.Operation == "roles.assign" {
					if mode == "scoped-entry" {
						g.Scope = oa.Scope{Type: oa.ScopeHost, ID: "host-a"}
					}
					if mode == "subject-entry" {
						g.Subjects = oa.SubjectConstraints{IDs: []string{"allowed-subject"}}
					}
				}
			}
			if mode == "split-scopes" || mode == "split-subjects" {
				for _, g := range snapshot.Grants {
					if g.Operation == "streams.start" {
						g.ID += "-second"
						if mode == "split-scopes" {
							g.Scope.ID = "stream-b"
						} else {
							g.Subjects.IDs = []string{"other-subject"}
						}
						snapshot.Grants = append(snapshot.Grants, g)
						break
					}
				}
			}
			if _, err = source.Replace(t.Context(), 1, snapshot); err != nil {
				t.Fatal(err)
			}
			server.integrations = store.NewMemoryIntegrationStore()
			method, target, body, _ := operationalDelegationMutation(t, auth, server, "user-update")
			before := operationalDelegationState(t, auth, server)
			res := operationalRequest(t, server, cookie, csrf, method, target, body)
			if res.Code != 403 {
				t.Fatalf("range widened: %d %s", res.Code, res.Body.String())
			}
			if before != operationalDelegationState(t, auth, server) {
				t.Fatal("scope/subject rejection saved data")
			}
		})
	}
	// A subject-restricted entry may assign a complete Global Role to that User.
	auth, source, server, cookie, csrf := operationalDelegationFixture(t, operationalDelegationOperations)
	role, err := auth.CreateRole(t.Context(), "start-role", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := auth.CreateOAuthUser(t.Context(), "allowed", "allowed@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.CreateOAuthUser(t.Context(), "other", "other@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Grants {
		if snapshot.Grants[i].Operation == "roles.assign" {
			snapshot.Grants[i].Subjects = oa.SubjectConstraints{IDs: []string{allowed.ID}}
		}
	}
	if _, err = source.Replace(t.Context(), 1, snapshot); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"role_ids": []string{role.ID}, "subject_id": allowed.ID})
	for _, target := range []store.User{other, allowed} {
		res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
		want := 403
		if target.ID == allowed.ID {
			want = 200
		}
		if res.Code != want {
			t.Fatalf("subject target=%s HTTP=%d want=%d body=%s", target.Username, res.Code, want, res.Body.String())
		}
		saved, err := auth.GetUser(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (len(saved.RoleIDs) > 0) != (want == 200) {
			t.Fatal("subject assignment save mismatch")
		}
	}
	// The store allocates a new User ID after validation; claims cannot supply it.
	createBody, _ := json.Marshal(map[string]any{"username": "future", "email": "future@example.test", "temporary_password": "Synthetic-Strong-Pass-921!", "role_ids": []string{role.ID}, "subject_id": allowed.ID})
	before, err := auth.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	res := operationalRequest(t, server, cookie, csrf, "POST", "/users", string(createBody))
	after, err := auth.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 403 || len(before) != len(after) {
		t.Fatalf("future subject forged: HTTP=%d users=%d/%d", res.Code, len(before), len(after))
	}
}

type operationalDelegationFaultSource struct {
	oa.Store
	mu                   sync.Mutex
	snapshots, revisions int
	mode                 string
}

func (s *operationalDelegationFaultSource) Snapshot(ctx context.Context, principal string) (oa.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots++
	snapshot, err := s.Store.Snapshot(ctx, principal)
	if err != nil {
		return snapshot, err
	}
	if s.mode == "source-error" || (s.mode == "late-source-error" && s.snapshots > 1) {
		return oa.Snapshot{}, oa.ErrUnavailable
	}
	if s.snapshots > 1 {
		switch s.mode {
		case "changed-generation":
			snapshot.Revision++
		case "unknown":
			snapshot.State = oa.StateUnknown
		case "held":
			snapshot.State = oa.StateHeld
		case "wrong-principal":
			snapshot.Principal.ID = "forged"
		case "workload":
			snapshot.Principal.Kind = "host_agent"
		case "inconsistent-role":
			snapshot.Principal.RoleIDs = nil
		case "unknown-operation":
			snapshot.Grants[0].Operation = "bootstrap.approve"
		case "unknown-delegation":
			snapshot.Grants[0].Delegation.Mode = "unknown"
		case "revoked":
			for i := range snapshot.Grants {
				snapshot.Grants[i].State = oa.StateRevoked
			}
		}
	}
	return snapshot, nil
}
func (s *operationalDelegationFaultSource) Revision(ctx context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revisions++
	revision, err := s.Store.Revision(ctx)
	if s.mode == "revision-error" || (s.mode == "late-revision-error" && s.revisions > 1) {
		return 0, oa.ErrUnavailable
	}
	if s.mode == "late-revision-change" && s.revisions > 1 {
		revision++
	}
	return revision, err
}

func TestOperationalHTTPDelegationSourceFailures(t *testing.T) {
	for _, route := range []string{"role-create", "role-update", "user-create", "user-update"} {
		for _, mode := range []string{"nil-source", "source-error", "late-source-error", "revision-error", "late-revision-error", "changed-generation", "late-revision-change", "unknown", "held", "wrong-principal", "workload", "inconsistent-role", "unknown-operation", "unknown-delegation", "revoked"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				auth, source, server, cookie, csrf := operationalDelegationFixture(t, operationalDelegationOperations)
				if mode == "nil-source" {
					server.operational = nil
				} else {
					server.operational = oa.NewService(&operationalDelegationFaultSource{Store: source, mode: mode})
				}
				server.integrations = store.NewMemoryIntegrationStore()
				method, target, body, _ := operationalDelegationMutation(t, auth, server, route)
				before := operationalDelegationState(t, auth, server)
				res := operationalRequest(t, server, cookie, csrf, method, target, body)
				if res.Code != 403 {
					t.Fatalf("source failure allowed: HTTP=%d body=%s", res.Code, res.Body.String())
				}
				if before != operationalDelegationState(t, auth, server) {
					t.Fatal("source failure changed saved records")
				}
			})
		}
	}
}

func TestOperationalHTTPDelegationLegacyConfiguration(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, route := range []string{"role-create", "role-update", "user-create", "user-update", "provider-create", "provider-update"} {
			t.Run(fmt.Sprintf("configured=%v/%s", configured, route), func(t *testing.T) {
				t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.test")
				auth, _, cookie, csrf := operationalHTTPFixture(t, operationalDelegationOperations, "legacy-custom")
				integrations := store.NewMemoryIntegrationStore()
				options := []ServerOption{WithAuthStore(auth), WithIntegrationStore(integrations)}
				if configured {
					options = append(options, WithOperationalAuthorization(auth))
				}
				server := NewServer(store.NewMemoryStreamStore(), options...)
				method, target, body, status := operationalDelegationMutation(t, auth, server, route)
				res := operationalRequest(t, server, cookie, csrf, method, target, body)
				if res.Code != status {
					t.Fatalf("legacy compatibility HTTP=%d want=%d body=%s", res.Code, status, res.Body.String())
				}
			})
		}
	}
}

func TestOperationalHTTPDelegationSuperAdminCompatibility(t *testing.T) {
	for _, mode := range []string{"legacy-unheld", "none-entry", "none-operation", "scoped-entry", "subject-entry", "scoped-operation", "subject-operation", "revoked-operation"} {
		t.Run(mode, func(t *testing.T) {
			operations := []string{"users.update", "roles.assign", "roles.create", "roles.update"}
			if strings.HasSuffix(mode, "operation") {
				operations = append(operations, "streams.start")
			}
			auth, _, cookie, csrf := operationalHTTPFixture(t, operations, "super_admin")
			role, err := auth.CreateRole(t.Context(), "custom-start", []string{"streams.start"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := auth.CreateOAuthUser(t.Context(), "target", "target@example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := auth.Snapshot(t.Context(), "human-s13")
			if err != nil {
				t.Fatal(err)
			}
			for i := range snapshot.Grants {
				g := &snapshot.Grants[i]
				switch {
				case g.Operation == "roles.assign" && mode == "none-entry":
					g.Delegation.Mode = oa.DelegationNone
				case g.Operation == "roles.assign" && mode == "scoped-entry":
					g.Scope = oa.Scope{Type: oa.ScopeHost, ID: "host-a"}
				case g.Operation == "roles.assign" && mode == "subject-entry":
					g.Subjects = oa.SubjectConstraints{IDs: []string{"other-user"}}
				case g.Operation == "streams.start" && mode == "none-operation":
					g.Delegation.Mode = oa.DelegationNone
				case g.Operation == "streams.start" && mode == "scoped-operation":
					g.Scope = oa.Scope{Type: oa.ScopeStream, ID: "stream-a"}
				case g.Operation == "streams.start" && mode == "subject-operation":
					g.Subjects = oa.SubjectConstraints{IDs: []string{target.ID}}
				case g.Operation == "streams.start" && mode == "revoked-operation":
					g.State = oa.StateRevoked
				}
			}
			source, err := oa.NewMemoryStore(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(source), WithIntegrationStore(store.NewMemoryIntegrationStore()))
			body, _ := json.Marshal(map[string]any{"role_ids": []string{role.ID}})
			before := operationalDelegationState(t, auth, server)
			res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
			want := 403
			if mode == "legacy-unheld" {
				want = 200
			}
			if res.Code != want {
				t.Fatalf("assignment exception HTTP=%d want=%d body=%s", res.Code, want, res.Body.String())
			}
			saved, err := auth.GetUser(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (len(saved.RoleIDs) > 0) != (want == 200) {
				t.Fatal("assignment save mismatch")
			}
			if want == 403 && before != operationalDelegationState(t, auth, server) {
				t.Fatal("restricted super_admin changed records")
			}
			if mode == "legacy-unheld" {
				actor, err := auth.GetUser(t.Context(), "human-s13")
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(map[string]any{"role_ids": actor.RoleIDs})
				if err != nil {
					t.Fatal(err)
				}
				res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
				saved, err := auth.GetUser(t.Context(), target.ID)
				if err != nil {
					t.Fatal(err)
				}
				if res.Code != 200 || !userHasRoleName(saved, "super_admin") {
					t.Fatalf("legacy super_admin Role assignment lost: %d %s", res.Code, res.Body.String())
				}
				// Assignment's legacy exception does not remove create/update subset checks.
				for _, req := range []struct{ method, path string }{{"POST", "/roles"}, {"PUT", "/roles/" + role.ID}} {
					body, _ := json.Marshal(map[string]any{"name": "changed", "permissions": []string{"streams.start"}})
					before := operationalDelegationState(t, auth, server)
					res := operationalRequest(t, server, cookie, csrf, req.method, req.path, string(body))
					if res.Code != 403 || before != operationalDelegationState(t, auth, server) {
						t.Fatal("assignment exception leaked into Role definition")
					}
				}
			}
		})
	}
}

func TestOperationalHTTPDelegationProtections(t *testing.T) {
	auth, server, cookie, csrf := operationalHTTPFixture(t, operationalDelegationOperations, "custom-manager")
	actor, err := auth.GetUser(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.AddUser(store.User{ID: "last-admin", Username: "last-admin", Roles: []string{"super_admin"}}, "synthetic-only-password", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.GetUser(t.Context(), "last-admin")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, path, body, code string
		status                   int
	}{
		{"PUT", "/users/human-s13", "{\"role_ids\":[]}", "cannot_update_own_roles", 403},
		{"PUT", "/roles/" + actor.RoleIDs[0], "{\"name\":\"custom-manager\",\"permissions\":[]}", "cannot_update_own_role", 403},
		{"PUT", "/roles/" + admin.RoleIDs[0], "{\"name\":\"super_admin\",\"permissions\":[]}", "protected_super_admin_role", 409},
		{"DELETE", "/roles/" + admin.RoleIDs[0], "", "permission_denied", 403},
		{"PUT", "/users/last-admin", "{\"role_ids\":[]}", "last_super_admin", 409},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			res := operationalRequest(t, server, cookie, csrf, tc.method, tc.path, tc.body)
			if res.Code != tc.status || !strings.Contains(res.Body.String(), tc.code) {
				t.Fatalf("HTTP=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
	body, _ := json.Marshal(map[string]any{"role_ids": admin.RoleIDs})
	target, err := auth.CreateOAuthUser(t.Context(), "new-target", "new-target@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
	if res.Code != 403 || !strings.Contains(res.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("custom assigned super_admin: %d %s", res.Code, res.Body.String())
	}
	// Preserve session, CSRF and live User-state checks before delegation.
	for _, mode := range []string{"anonymous", "csrf", "disabled"} {
		cookieForRequest, token := cookie, csrf
		if mode == "anonymous" {
			cookieForRequest = nil
		}
		if mode == "csrf" {
			token = "wrong"
		}
		if mode == "disabled" {
			if _, err = auth.SetUserStatus(t.Context(), actor.ID, "disabled"); err != nil {
				t.Fatal(err)
			}
		}
		rolesBefore, err := auth.ListRoles(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		res := operationalRequest(t, server, cookieForRequest, token, "POST", "/roles", "{\"name\":\"blocked\",\"permissions\":[]}")
		rolesAfter, err := auth.ListRoles(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want := 403
		if mode != "csrf" {
			want = 401
		}
		if res.Code != want || !reflect.DeepEqual(rolesBefore, rolesAfter) {
			t.Fatalf("auth boundary %s HTTP=%d", mode, res.Code)
		}
	}
}

func TestOperationalHTTPDelegationAllRolesBeforeSave(t *testing.T) {
	for _, route := range []string{"user-create", "user-update", "provider-create", "provider-update"} {
		t.Run(route, func(t *testing.T) {
			t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.test")
			operations := append(append([]string{}, operationalDelegationOperations...), "streams.stop")
			auth, _, server, cookie, csrf := operationalDelegationFixture(t, operations, "streams.stop")
			server.integrations = store.NewMemoryIntegrationStore()
			method, target, body, _ := operationalDelegationMutation(t, auth, server, route)
			second, err := auth.CreateRole(t.Context(), "nondelegable-stop", []string{"streams.stop"})
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err = json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			key := "role_ids"
			if strings.HasPrefix(route, "provider-") {
				key = "default_role_ids"
			}
			payload[key] = append(payload[key].([]any), second.ID)
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			before := operationalDelegationState(t, auth, server)
			res := operationalRequest(t, server, cookie, csrf, method, target, string(encoded))
			if res.Code != 403 || before != operationalDelegationState(t, auth, server) {
				t.Fatalf("partial Role assignment: HTTP=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}
