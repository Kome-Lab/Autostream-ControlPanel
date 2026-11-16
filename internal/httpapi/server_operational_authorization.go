package httpapi

import (
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/operationalauthorization"
)

// WithOperationalAuthorization explicitly composes Human Operational Authorization
// into the existing permission-bearing Panel routes. Session-only authentication,
// Evidence Authorization, Authority Management and workload authentication remain
// independent. A missing or unhealthy configured source never falls back to v2.
// Production SQL schemas are applied only by the separate explicit migrator.
func WithOperationalAuthorization(source operationalauthorization.Store) ServerOption {
	return func(s *Server) {
		s.operationalConfigured = true
		s.operational = operationalauthorization.NewService(source)
	}
}

func (s *Server) operationalCurrent(w http.ResponseWriter, r *http.Request, current currentUser) (currentUser, bool) {
	if !s.operationalConfigured {
		return current, true
	}
	if s.operational == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return currentUser{}, false
	}
	snapshot, permissions, err := s.operational.Current(r.Context(), current.User.ID, operationalRequestScope(r))
	if err != nil || snapshot.Principal.ID != current.User.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return currentUser{}, false
	}
	// Role-name exceptions must use the same fresh source as the operations.
	// A stale pre-projection super_admin name must not survive revocation.
	current.User.Status = string(snapshot.Principal.State)
	current.User.Roles = nil
	current.User.RoleIDs = nil
	for _, role := range snapshot.Roles {
		current.User.Roles = append(current.User.Roles, role.Name)
		current.User.RoleIDs = append(current.User.RoleIDs, role.ID)
	}
	current.Permissions = permissions
	current.OperationalRevision = snapshot.Revision
	return current, true
}

// Only registered, single-target path parameters establish a scope. Collection,
// body-selected and unclassified targets require Global grants. An arbitrary URL
// or client-provided scope header cannot select a larger authorization boundary.
// Existing Handler ownership, assignment and state checks still run afterwards.
func operationalRequestScope(r *http.Request) operationalauthorization.Scope {
	_, pattern, _ := strings.Cut(r.Pattern, " ")
	if strings.HasPrefix(pattern, "/streams/{id}") && r.PathValue("id") != "" {
		return operationalauthorization.Scope{Type: "stream", ID: r.PathValue("id")}
	}
	for _, prefix := range []string{"/profiles/encoder", "/profiles/archive", "/profiles/caption", "/profiles/overlay", "/discord/target-presets", "/video-cover-presets", "/youtube/outputs"} {
		if pattern == prefix+"/{id}" && r.PathValue("id") != "" {
			return operationalauthorization.Scope{Type: "profile", ID: strings.TrimPrefix(prefix, "/") + "/" + r.PathValue("id")}
		}
	}
	for _, prefix := range []string{"/integrations/oauth-providers", "/integrations/oauth-accounts"} {
		if pattern == prefix+"/{id}" && r.PathValue("id") != "" {
			return operationalauthorization.Scope{Type: "integration", ID: strings.TrimPrefix(prefix, "/integrations/") + "/" + r.PathValue("id")}
		}
	}
	if pattern == "/secrets/{name}" && r.PathValue("name") != "" {
		return operationalauthorization.Scope{Type: "secret", ID: r.PathValue("name")}
	}
	if strings.HasPrefix(pattern, "/system-updates/hosts/{host_id}/") && r.PathValue("host_id") != "" {
		return operationalauthorization.Scope{Type: "host", ID: r.PathValue("host_id")}
	}
	return operationalauthorization.Scope{Type: "global"}
}

// This legacy Session-only route selects its required operation inside the Handler.
// Evidence routes also use an empty permission and are deliberately unaffected.
func operationalHandlerChecksPermission(r *http.Request) bool {
	return r.Pattern == "PUT /streams/{id}/video-cover-state"
}
