package operationalauthorization

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

//go:embed testdata/v2_server_users_roles.go.txt
var operationalFrozenRoleSource []byte

const operationalFrozenRoleSHA256 = "e3ee915884ddf576e46f37c350877871b678fc9e5143177181e3301f25d28e80"

// Replay only the seven reviewed delegation hooks over the immutable v2 file.
// The entire resulting file is compared: no handler or protection is excluded.
func operationalApprovedRoleSource(frozen []byte) ([]byte, error) {
	source := strings.ReplaceAll(string(frozen), "\r\n", "\n")
	changes := [][2]string{
		{"\tif !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {\n\t\treturn\n\t}\n\tif len(body.RoleIDs) > 0 && !security.HasPermission(currentFromContext(r.Context()).Permissions, \"roles.assign\") {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_denied\"})\n\t\treturn\n\t}\n", "\tif !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {\n\t\treturn\n\t}\n\tif len(body.RoleIDs) > 0 && !s.hasRoleAssignmentPermission(r.Context(), \"\") {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_denied\"})\n\t\treturn\n\t}\n"},
		{"\t\twriteJSON(w, http.StatusBadRequest, map[string]string{\"code\": \"bad_request\"})\n\t\treturn\n\t}\n\tif body.RoleIDs != nil && !security.HasPermission(currentFromContext(r.Context()).Permissions, \"roles.assign\") {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_denied\"})\n\t\treturn\n\t}\n", "\t\twriteJSON(w, http.StatusBadRequest, map[string]string{\"code\": \"bad_request\"})\n\t\treturn\n\t}\n\tif body.RoleIDs != nil && !s.hasRoleAssignmentPermission(r.Context(), r.PathValue(\"id\")) {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_denied\"})\n\t\treturn\n\t}\n"},
		{"\tif err != nil {\n\t\treturn err\n\t}\n\tif err := s.validateRoleAssignments(ctx, roleIDs); err != nil {\n\t\treturn err\n\t}\n\tif !userHasRoleName(target, \"super_admin\") || target.Status != \"active\" {\n", "\tif err != nil {\n\t\treturn err\n\t}\n\tif err := s.validateRoleAssignmentsForSubject(ctx, roleIDs, target.ID); err != nil {\n\t\treturn err\n\t}\n\tif !userHasRoleName(target, \"super_admin\") || target.Status != \"active\" {\n"},
		{"}\n\nfunc (s *Server) validateRoleAssignments(ctx context.Context, roleIDs []string) error {\n\troles := make([]store.Role, 0, len(roleIDs))\n\tfor _, roleID := range roleIDs {\n\t\trole, err := s.roles.GetRole(ctx, roleID)\n", "}\n\nfunc (s *Server) validateRoleAssignments(ctx context.Context, roleIDs []string) error {\n\treturn s.validateRoleAssignmentsForSubject(ctx, roleIDs, \"\")\n}\n\nfunc (s *Server) validateRoleAssignmentsForSubject(ctx context.Context, roleIDs []string, subjectID string) error {\n\troles := make([]store.Role, 0, len(roleIDs))\n\tfor _, roleID := range roleIDs {\n\t\trole, err := s.roles.GetRole(ctx, roleID)\n"},
		{"\tif err := store.ValidateRoleAssignment(current.User, roles); err != nil {\n\t\treturn err\n\t}\n\tif userHasRoleName(current.User, \"super_admin\") {\n\t\treturn nil\n\t}\n\tfor _, role := range roles {\n\t\tif err := store.ValidateRolePermissions(current.Permissions, role.Permissions); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", "\tif err := store.ValidateRoleAssignment(current.User, roles); err != nil {\n\t\treturn err\n\t}\n\tif !userHasRoleName(current.User, \"super_admin\") {\n\t\tfor _, role := range roles {\n\t\t\tif err := store.ValidateRolePermissions(current.Permissions, role.Permissions); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t}\n\tif len(roles) == 0 {\n\t\treturn s.validateOperationalRoleDelegation(ctx, \"roles.assign\", store.Role{}, subjectID)\n\t}\n\tfor _, role := range roles {\n\t\tif err := s.validateOperationalRoleDelegation(ctx, \"roles.assign\", role, subjectID); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n"},
		{"\t\twriteJSON(w, status, map[string]string{\"code\": code})\n\t\treturn\n\t}\n\trole, err := s.roles.CreateRole(r.Context(), strings.TrimSpace(body.Name), body.Permissions)\n\tif err != nil {\n\t\twriteJSON(w, http.StatusBadRequest, map[string]string{\"code\": \"create_role_failed\"})\n", "\t\twriteJSON(w, status, map[string]string{\"code\": code})\n\t\treturn\n\t}\n\tif err := s.validateOperationalRoleDelegation(r.Context(), \"roles.create\", store.Role{Name: strings.TrimSpace(body.Name), Permissions: body.Permissions}, \"\"); err != nil {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_escalation\"})\n\t\treturn\n\t}\n\trole, err := s.roles.CreateRole(r.Context(), strings.TrimSpace(body.Name), body.Permissions)\n\tif err != nil {\n\t\twriteJSON(w, http.StatusBadRequest, map[string]string{\"code\": \"create_role_failed\"})\n"},
		{"\t\twriteJSON(w, status, map[string]string{\"code\": code})\n\t\treturn\n\t}\n\trole, err := s.roles.UpdateRole(r.Context(), id, strings.TrimSpace(body.Name), body.Permissions)\n\tif errors.Is(err, store.ErrNotFound) {\n\t\twriteJSON(w, http.StatusNotFound, map[string]string{\"code\": \"not_found\"})\n", "\t\twriteJSON(w, status, map[string]string{\"code\": code})\n\t\treturn\n\t}\n\tif err := s.validateOperationalRoleDelegation(r.Context(), \"roles.update\", store.Role{ID: existing.ID, Name: strings.TrimSpace(body.Name), Permissions: body.Permissions}, \"\"); err != nil {\n\t\twriteJSON(w, http.StatusForbidden, map[string]string{\"code\": \"permission_escalation\"})\n\t\treturn\n\t}\n\trole, err := s.roles.UpdateRole(r.Context(), id, strings.TrimSpace(body.Name), body.Permissions)\n\tif errors.Is(err, store.ErrNotFound) {\n\t\twriteJSON(w, http.StatusNotFound, map[string]string{\"code\": \"not_found\"})\n"},
	}
	for i, change := range changes {
		if strings.Count(source, change[0]) != 1 {
			return nil, fmt.Errorf("approved hook %d has no unique v2 match", i)
		}
		source = strings.Replace(source, change[0], change[1], 1)
	}
	return format.Source([]byte(source))
}

func operationalRoleSourceMatches(expected, actual []byte) bool {
	formatted, err := format.Source(actual)
	return err == nil && bytes.Equal(expected, formatted)
}

func TestOperationalHTTPRoleSourceBoundary(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	// Match Git blob bytes even when Windows checkout converts text to CRLF.
	frozen := []byte(strings.ReplaceAll(string(operationalFrozenRoleSource), "\r\n", "\n"))
	digest := sha256.Sum256(frozen)
	if hex.EncodeToString(digest[:]) != operationalFrozenRoleSHA256 {
		t.Fatal("fixed v2 fixture checksum mismatch")
	}
	expected, err := operationalApprovedRoleSource(frozen)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "internal/httpapi/server_users_roles.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !operationalRoleSourceMatches(expected, actual) {
		t.Fatal("HTTP Role source contains changes beyond the seven reviewed delegation hooks")
	}
	for _, change := range [][2]string{
		{"count <= 1", "count < 1"},
		{"targetUserID == current.User.ID", "targetUserID != current.User.ID"},
		{"protected_super_admin_role", "unprotected_role"},
		{"s.validateOperationalRoleDelegation(r.Context(), \"roles.create\"", "s.bypassDelegation(r.Context(), \"roles.create\""},
		{"s.requirePermission(\"roles.create\"", "s.requirePermission(\"roles.read\""},
	} {
		before, after := change[0], change[1]
		if !strings.Contains(string(actual), before) {
			t.Fatalf("tamper fixture not anchored: %s", before)
		}
		tampered := []byte(strings.Replace(string(actual), before, after, 1))
		if operationalRoleSourceMatches(expected, tampered) {
			t.Fatalf("unauthorized change passed: %s", before)
		}
	}
}
