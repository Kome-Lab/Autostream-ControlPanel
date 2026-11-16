package operationalauthorization

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

var (
	ErrDenied           = errors.New("operational authorization denied")
	ErrInvalidSnapshot  = errors.New("invalid operational authorization snapshot")
	ErrStaleRevision    = errors.New("operational authorization revision changed")
	ErrConflict         = errors.New("operational authorization generation conflict")
	ErrUnavailable      = errors.New("operational authorization unavailable")
	ErrUnknownOperation = errors.New("unknown operational operation")
)

func validID(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}
func validScope(scope Scope) bool {
	if scope.Type == ScopeGlobal {
		return scope.ID == ""
	}
	switch scope.Type {
	case ScopeRealm, ScopeHost, ScopeResource, ScopeStream, ScopeProfile, ScopeIntegration, ScopeSecret, ScopeIncident:
		return validID(scope.ID) && scope.ID != "*"
	default:
		return false
	}
}
func scopeContains(parent, child Scope) bool {
	return validScope(parent) && validScope(child) && (parent.Type == ScopeGlobal || parent == child)
}
func validSubjects(subjects SubjectConstraints) bool {
	seen := make(map[string]bool, len(subjects.IDs))
	for _, id := range subjects.IDs {
		if !validID(id) || id == "*" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func subjectMatches(subjects SubjectConstraints, principalID, subjectID string) bool {
	if subjects.SelfOnly && (subjectID == "" || subjectID != principalID) {
		return false
	}
	if len(subjects.IDs) == 0 {
		return true
	}
	for _, id := range subjects.IDs {
		if subjectID == id {
			return true
		}
	}
	return false
}
func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Principal.RoleIDs = append([]string(nil), in.Principal.RoleIDs...)
	out.Roles = append([]Role(nil), in.Roles...)
	for i := range out.Roles {
		out.Roles[i].Operations = append([]string(nil), in.Roles[i].Operations...)
	}
	out.Grants = append([]Grant(nil), in.Grants...)
	for i := range out.Grants {
		out.Grants[i].Subjects.IDs = append([]string(nil), in.Grants[i].Subjects.IDs...)
	}
	return out
}

// Project expands actual role contents and assignments, without a role-name grant.
func Project(principal Principal, roles []Role, revision uint64) (Snapshot, error) {
	out := Snapshot{Principal: principal, Roles: append([]Role(nil), roles...), Revision: revision, State: StateActive}
	out.Principal.RoleIDs = append([]string(nil), principal.RoleIDs...)
	sort.Strings(out.Principal.RoleIDs)
	for i := range out.Roles {
		seen := make(map[string]bool)
		operations := make([]string, 0, len(roles[i].Operations))
		for _, operation := range roles[i].Operations {
			if !KnownOperation(operation) {
				return Snapshot{}, ErrUnknownOperation
			}
			if !seen[operation] {
				operations = append(operations, operation)
				seen[operation] = true
			}
		}
		sort.Strings(operations)
		out.Roles[i].Operations = operations
		for _, operation := range operations {
			sum := sha256.Sum256([]byte(principal.ID + "\x00" + roles[i].ID + "\x00" + operation))
			out.Grants = append(out.Grants, Grant{ID: "legacy-" + hex.EncodeToString(sum[:]), PrincipalID: principal.ID, RoleID: roles[i].ID, Operation: operation, Scope: Scope{Type: ScopeGlobal}, Delegation: DelegationConstraints{Mode: DelegationSubset}, Revision: revision, State: StateActive})
		}
	}
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i].ID < out.Roles[j].ID })
	sort.Slice(out.Grants, func(i, j int) bool { return out.Grants[i].ID < out.Grants[j].ID })
	if err := validateSnapshot(out, principal.ID); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

func validateSnapshot(snapshot Snapshot, principalID string) error {
	if snapshot.State != StateActive || snapshot.Revision == 0 || !validID(principalID) || snapshot.Principal.ID != principalID || snapshot.Principal.Kind != KindHuman {
		return ErrInvalidSnapshot
	}
	switch snapshot.Principal.State {
	case StateActive, StatePendingPasswordChange:
	default:
		return ErrDenied
	}
	assignments := make(map[string]bool, len(snapshot.Principal.RoleIDs))
	for _, id := range snapshot.Principal.RoleIDs {
		if !validID(id) || assignments[id] {
			return ErrInvalidSnapshot
		}
		assignments[id] = true
	}
	roles := make(map[string]Role, len(snapshot.Roles))
	operations := make(map[string]map[string]bool, len(snapshot.Roles))
	for _, role := range snapshot.Roles {
		if !validID(role.ID) || role.Name == "" || !assignments[role.ID] {
			return ErrInvalidSnapshot
		}
		if _, exists := roles[role.ID]; exists {
			return ErrInvalidSnapshot
		}
		roles[role.ID] = role
		operations[role.ID] = make(map[string]bool, len(role.Operations))
		for _, operation := range role.Operations {
			if !KnownOperation(operation) {
				return ErrUnknownOperation
			}
			if operations[role.ID][operation] {
				return ErrInvalidSnapshot
			}
			operations[role.ID][operation] = true
		}
	}
	if len(roles) != len(assignments) {
		return ErrInvalidSnapshot
	}
	ids := make(map[string]bool, len(snapshot.Grants))
	represented := make(map[string]map[string]bool, len(roles))
	for _, grant := range snapshot.Grants {
		if !validID(grant.ID) || ids[grant.ID] || grant.PrincipalID != principalID || !operations[grant.RoleID][grant.Operation] || !validScope(grant.Scope) || !validSubjects(grant.Subjects) || grant.Revision == 0 || grant.Revision > snapshot.Revision {
			return ErrInvalidSnapshot
		}
		ids[grant.ID] = true
		if grant.State != StateActive && grant.State != StateRevoked {
			return ErrInvalidSnapshot
		}
		switch grant.Delegation.Mode {
		case DelegationNone, DelegationSubset:
		case DelegationSuperAdminAssignment:
			if grant.Operation != "roles.assign" || roles[grant.RoleID].Name != "super_admin" {
				return ErrInvalidSnapshot
			}
		default:
			return ErrInvalidSnapshot
		}
		if represented[grant.RoleID] == nil {
			represented[grant.RoleID] = make(map[string]bool)
		}
		represented[grant.RoleID][grant.Operation] = true
	}
	for roleID, permissions := range operations {
		for operation := range permissions {
			if !represented[roleID][operation] {
				return ErrInvalidSnapshot
			}
		}
	}
	return nil
}
