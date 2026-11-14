package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func operationalHTTPFixture(t *testing.T, permissions []string, roleName string) (*store.MemoryAuthStore, *Server, *http.Cookie, string) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "human-s13", Username: "s13-user", Roles: []string{roleName}}, "synthetic-password-s13", permissions); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(t.Context(), "human-s13", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(auth))
	return auth, server, &http.Cookie{Name: sessionCookieName, Value: session.Token}, session.CSRFToken
}

func operationalRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestOperationalHTTPAllPermissions(t *testing.T) {
	_, server, cookie, csrf := operationalHTTPFixture(t, security.DefaultPermissions, "super_admin")
	for _, permission := range security.DefaultPermissions {
		t.Run(permission, func(t *testing.T) {
			called := false
			handler := server.requirePermission(permission, func(w http.ResponseWriter, r *http.Request) {
				called = true
				if !slices.Contains(currentFromContext(r.Context()).User.Roles, "super_admin") {
					t.Error("current Role identity lost")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			res := operationalRequest(t, handler, cookie, csrf, http.MethodPost, "/synthetic-operational-entry", "{}")
			if !called || res.Code != http.StatusNoContent {
				t.Fatalf("permission %s: status=%d called=%v", permission, res.Code, called)
			}
			denied := operationalRequest(t, handler, nil, csrf, http.MethodPost, "/synthetic-operational-entry", "{}")
			if denied.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous status=%d", denied.Code)
			}
		})
	}
	unknown := server.requirePermission("trust.root.replace", func(w http.ResponseWriter, _ *http.Request) { t.Error("Trust management entered"); w.WriteHeader(204) })
	if res := operationalRequest(t, unknown, cookie, csrf, http.MethodPost, "/synthetic", "{}"); res.Code != http.StatusForbidden {
		t.Fatalf("unknown operation status=%d", res.Code)
	}
}

func TestOperationalHTTPAdminContinuity(t *testing.T) {
	auth, server, cookie, csrf := operationalHTTPFixture(t, security.DefaultPermissions, "super_admin")
	actor, err := auth.GetUser(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"catalog", "GET", "/permissions", "", 200, ""},
		{"users", "GET", "/users", "", 200, ""},
		{"roles", "GET", "/roles", "", 200, ""},
		{"security", "GET", "/security/settings", "", 200, ""},
		{"self-role", "PUT", "/users/human-s13", `{"role_ids":[]}`, 403, "cannot_update_own_roles"},
		{"last-admin", "POST", "/users/human-s13/disable", `{}`, 409, "last_super_admin"},
		{"self-delete", "DELETE", "/users/human-s13", "", 409, "cannot_delete_self"},
		{"immutable-role", "PUT", "/roles/" + actor.RoleIDs[0], `{"name":"super_admin","permissions":[]}`, 409, "protected_super_admin_role"},
		{"undeletable-role", "DELETE", "/roles/" + actor.RoleIDs[0], "", 409, "protected_super_admin_role"},
		{"csrf", "POST", "/roles", `{"name":"bad","permissions":[]}`, 403, "csrf_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := csrf
			if tc.name == "csrf" {
				token = "wrong-synthetic-token"
			}
			res := operationalRequest(t, server, cookie, token, tc.method, tc.path, tc.body)
			if res.Code != tc.status || (tc.code != "" && !strings.Contains(res.Body.String(), tc.code)) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
	created := operationalRequest(t, server, cookie, csrf, "POST", "/roles", `{"name":"s13-custom","permissions":["streams.read","audit_logs.export"]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create Role status=%d body=%s", created.Code, created.Body.String())
	}
	var role store.Role
	if err := json.Unmarshal(created.Body.Bytes(), &role); err != nil {
		t.Fatal(err)
	}
	if role.Name != "s13-custom" || !slices.Contains(role.Permissions, "audit_logs.export") {
		t.Fatal("custom Role semantics changed")
	}
}

func TestOperationalHTTPLiveRevocationAndAnyPermission(t *testing.T) {
	auth, server, cookie, csrf := operationalHTTPFixture(t, []string{"streams.read", "roles.read"}, "custom-reader")
	any := server.requireAnyPermission([]string{"streams.update", "streams.read"}, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	if res := operationalRequest(t, any, cookie, csrf, "GET", "/synthetic", ""); res.Code != 204 {
		t.Fatalf("union status=%d", res.Code)
	}
	actor, _ := auth.GetUser(t.Context(), "human-s13")
	if _, err := auth.UpdateRole(t.Context(), actor.RoleIDs[0], "custom-reader", []string{"roles.read"}); err != nil {
		t.Fatal(err)
	}
	if res := operationalRequest(t, any, cookie, csrf, "GET", "/synthetic", ""); res.Code != 403 {
		t.Fatalf("revoked operation status=%d", res.Code)
	}
	if res := operationalRequest(t, server, cookie, csrf, "GET", "/roles", ""); res.Code != 200 {
		t.Fatalf("remaining permission status=%d", res.Code)
	}
	if _, err := auth.SetUserStatus(t.Context(), "human-s13", "disabled"); err != nil {
		t.Fatal(err)
	}
	if res := operationalRequest(t, server, cookie, csrf, "GET", "/roles", ""); res.Code != 401 {
		t.Fatalf("disabled status=%d", res.Code)
	}
}

func TestOperationalHTTPMissingSourceDoesNotAffectEvidenceSessionBoundary(t *testing.T) {
	auth, _, cookie, csrf := operationalHTTPFixture(t, []string{"roles.read"}, "reader")
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(nil))
	if res := operationalRequest(t, server, cookie, csrf, "GET", "/roles", ""); res.Code != 403 {
		t.Fatalf("missing source status=%d", res.Code)
	}
	if res := operationalRequest(t, server, cookie, csrf, "GET", "/version", ""); res.Code != 200 {
		t.Fatalf("session-only version status=%d", res.Code)
	}
	res := operationalRequest(t, server.V3AuthorizationHandler(nil), cookie, csrf, "POST", "/v3/cp-authorizations", "{}")
	if res.Code != 400 || !strings.Contains(res.Body.String(), "v3_authorization_invalid") {
		t.Fatalf("Evidence identity boundary changed: %d %s", res.Code, res.Body.String())
	}
}

type operationalStaleRoleAuth struct{ *store.MemoryAuthStore }

func (s operationalStaleRoleAuth) GetUser(ctx context.Context, id string) (store.User, error) {
	user, err := s.MemoryAuthStore.GetUser(ctx, id)
	user.Roles = []string{"super_admin"}
	return user, err
}

func TestOperationalHTTPCurrentRoleIdentityReplacesStaleSuperAdmin(t *testing.T) {
	auth, _, cookie, csrf := operationalHTTPFixture(t, []string{"users.update", "roles.assign"}, "operator")
	super, err := auth.CreateRole(t.Context(), "super_admin", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := auth.CreateOAuthUser(t.Context(), "s13-target", "s13-target@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(operationalStaleRoleAuth{auth}), WithOperationalAuthorization(auth))
	body, _ := json.Marshal(map[string]any{"role_ids": []string{super.ID}})
	res := operationalRequest(t, server, cookie, csrf, "PUT", "/users/"+target.ID, string(body))
	if res.Code != 403 || !strings.Contains(res.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("stale Role exception survived: %d %s", res.Code, res.Body.String())
	}
}

func TestOperationalHTTPPathScopeBindings(t *testing.T) {
	cases := []struct{ pattern, method, path, scopeType, id string }{
		{"GET /streams/{id}", "GET", "/streams/stream-a", "stream", "stream-a"},
		{"GET /streams/{id}", "HEAD", "/streams/stream-a", "stream", "stream-a"},
		{"POST /streams/{id}/services", "POST", "/streams/stream-a/services", "stream", "stream-a"},
		{"GET /streams", "GET", "/streams?scope_id=stream-a", "global", ""},
		{"GET /profiles/encoder/{id}", "GET", "/profiles/encoder/profile-a", "profile", "profiles/encoder/profile-a"},
		{"GET /profiles/archive/{id}", "GET", "/profiles/archive/profile-a", "profile", "profiles/archive/profile-a"},
		{"PUT /secrets/{name}", "PUT", "/secrets/synthetic-name", "secret", "synthetic-name"},
		{"POST /system-updates/hosts/{host_id}/self-updates", "POST", "/system-updates/hosts/host-a/self-updates", "host", "host-a"},
		{"GET /integrations/oauth-accounts/{id}", "GET", "/integrations/oauth-accounts/account-a", "integration", "oauth-accounts/account-a"},
		{"GET /integrations/oauth-providers/{id}", "GET", "/integrations/oauth-providers/account-a", "integration", "oauth-providers/account-a"},
		{"PUT /users/{id}", "PUT", "/users/user-a", "global", ""},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.method, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(tc.pattern, func(w http.ResponseWriter, r *http.Request) {
				scope := operationalRequestScope(r)
				if string(scope.Type) != tc.scopeType || scope.ID != tc.id {
					t.Fatalf("scope=%+v", scope)
				}
				w.WriteHeader(204)
			})
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"scope_type":"global","scope_id":"forged"}`))
			req.Header.Set("X-Operational-Scope", "global")
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != 204 {
				t.Fatalf("status=%d", res.Code)
			}
		})
	}
}

func TestOperationalHTTPHandlerSelectedPermissionProjection(t *testing.T) {
	auth, server, cookie, csrf := operationalHTTPFixture(t, []string{"streams.show_cover", "streams.hide_cover"}, "cover-operator")
	actor, err := auth.GetUser(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.UpdateRole(t.Context(), actor.RoleIDs[0], "cover-operator", []string{"streams.hide_cover"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /streams/{id}/video-cover-state", server.requirePermission("", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Active bool }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		operation := "streams.hide_cover"
		if body.Active {
			operation = "streams.show_cover"
		}
		if !slices.Contains(currentFromContext(r.Context()).Permissions, operation) {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	for _, tc := range []struct {
		active bool
		status int
	}{{true, 403}, {false, 204}} {
		body, _ := json.Marshal(map[string]bool{"active": tc.active})
		if res := operationalRequest(t, mux, cookie, csrf, "PUT", "/streams/stream-a/video-cover-state", string(body)); res.Code != tc.status {
			t.Fatalf("active=%v status=%d", tc.active, res.Code)
		}
	}
}

func TestOperationalHTTPHybridScope(t *testing.T) {
	auth, _, cookie, csrf := operationalHTTPFixture(t, []string{"streams.read"}, "reader")
	streams := store.NewMemoryStreamStore()
	first, err := streams.CreateStream(t.Context(), "synthetic-first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := streams.CreateStream(t.Context(), "synthetic-second")
	if err != nil {
		t.Fatal(err)
	}
	legacy := NewServer(streams, WithAuthStore(auth))
	global := NewServer(streams, WithAuthStore(auth), WithOperationalAuthorization(auth))
	for _, target := range []string{"/streams/" + first.ID, "/streams/" + second.ID, "/streams"} {
		before := operationalRequest(t, legacy, cookie, csrf, "GET", target, "")
		after := operationalRequest(t, global, cookie, csrf, "GET", target, "")
		if before.Code != 200 || after.Code != before.Code {
			t.Fatalf("Global compatibility %s: %d/%d", target, before.Code, after.Code)
		}
	}
	// A separately constructed scoped fixture; existing SQL roles are never
	// automatically narrowed from Global during compatibility projection.
	snapshot, err := auth.Snapshot(t.Context(), "human-s13")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Grants[0].Scope = operationalauthorization.Scope{Type: operationalauthorization.ScopeStream, ID: first.ID}
	source, err := operationalauthorization.NewMemoryStore(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams, WithAuthStore(auth), WithOperationalAuthorization(source))
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"GET", "/streams/" + first.ID, 200}, {"HEAD", "/streams/" + first.ID, 200},
		{"GET", "/streams/" + second.ID, 403}, {"GET", "/streams", 403},
	} {
		if res := operationalRequest(t, server, cookie, csrf, tc.method, tc.target, ""); res.Code != tc.status {
			t.Fatalf("Scoped %s %s status=%d", tc.method, tc.target, res.Code)
		}
	}
	if _, err := source.Revoke(t.Context(), 1, "human-s13", snapshot.Grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if res := operationalRequest(t, server, cookie, csrf, "GET", "/streams/"+first.ID, ""); res.Code != 403 {
		t.Fatal("configured source revocation fell back to legacy Global permissions")
	}
}
