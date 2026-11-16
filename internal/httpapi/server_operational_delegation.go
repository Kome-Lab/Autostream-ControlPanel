package httpapi

import (
	"context"

	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

// Role management stores legacy Global/Subset Role permissions. The planned
// effect is therefore Global and unconstrained; client scope/actor claims cannot
// attenuate this descriptor to pass validation and then save broader rights.
// An empty subject denotes a Role definition or an unknown future principal.
func (s *Server) validateOperationalRoleDelegation(ctx context.Context, action string, role store.Role, subjectID string) error {
	if !s.operationalConfigured {
		return nil
	}
	current := currentFromContext(ctx)
	if s.operational == nil || current.OperationalRevision == 0 {
		return store.ErrPermissionEscalation
	}
	grants := make([]oa.Grant, 0, len(role.Permissions))
	for _, operation := range role.Permissions {
		grants = append(grants, oa.Grant{
			PrincipalID: subjectID, RoleID: role.ID, Operation: operation,
			Scope:      oa.Scope{Type: oa.ScopeGlobal},
			Delegation: oa.DelegationConstraints{Mode: oa.DelegationSubset},
			Revision:   current.OperationalRevision, State: oa.StateActive,
		})
	}
	err := s.operational.ValidateDelegation(ctx, oa.DelegationRequest{
		PrincipalID: current.User.ID, Action: action, RoleName: role.Name, Grants: grants,
		Scope: oa.Scope{Type: oa.ScopeGlobal}, SubjectID: subjectID,
		ExpectedRevision: current.OperationalRevision,
	})
	if err != nil {
		return store.ErrPermissionEscalation
	}
	return nil
}

// The legacy permission gate remains unchanged when A is not configured.
// A subject-bound roles.assign Grant can be checked for a known User target
// without flattening it into unrestricted permissions. Delegation itself is
// checked separately against the complete Role effect immediately before save.
func (s *Server) hasRoleAssignmentPermission(ctx context.Context, subjectID string) bool {
	current := currentFromContext(ctx)
	if !s.operationalConfigured {
		return security.HasPermission(current.Permissions, "roles.assign")
	}
	if s.operational == nil || current.OperationalRevision == 0 {
		return false
	}
	_, err := s.operational.Authorize(ctx, oa.Request{
		PrincipalID: current.User.ID, PrincipalKind: oa.KindHuman,
		Operation: "roles.assign", Scope: oa.Scope{Type: oa.ScopeGlobal},
		SubjectID: subjectID, ExpectedRevision: current.OperationalRevision,
	})
	return err == nil
}
