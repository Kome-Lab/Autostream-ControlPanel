// Package operationalauthorization owns Panel Human Operational Authorization.
// It has no Evidence, Trust Management or Workload credential authority.
package operationalauthorization

import "context"

type ScopeType string

const (
	ScopeGlobal      ScopeType = "global"
	ScopeRealm       ScopeType = "realm"
	ScopeHost        ScopeType = "host"
	ScopeResource    ScopeType = "resource"
	ScopeStream      ScopeType = "stream"
	ScopeProfile     ScopeType = "profile"
	ScopeIntegration ScopeType = "integration"
	ScopeSecret      ScopeType = "secret"
	ScopeIncident    ScopeType = "incident"
)
const (
	KindHuman                  = "human"
	StateActive                = "active"
	StateRevoked               = "revoked"
	StateUnknown               = "unknown"
	StateHeld                  = "held"
	StatePendingPasswordChange = "pending_password_change"
)

type Scope struct {
	Type ScopeType
	ID   string
}
type Principal struct {
	ID      string
	Kind    string
	State   string
	RoleIDs []string
}
type Role struct {
	ID         string
	Name       string
	Operations []string
}
type SubjectConstraints struct {
	SelfOnly bool
	IDs      []string
}
type DelegationConstraints struct{ Mode string }

const (
	DelegationNone                 = "none"
	DelegationSubset               = "permission_subset"
	DelegationSuperAdminAssignment = "super_admin_assignment"
)

type Grant struct {
	ID          string
	PrincipalID string
	RoleID      string
	Operation   string
	Scope       Scope
	Subjects    SubjectConstraints
	Delegation  DelegationConstraints
	Revision    uint64
	State       string
}
type Snapshot struct {
	Principal Principal
	Roles     []Role
	Grants    []Grant
	Revision  uint64
	State     string
}
type Store interface {
	Snapshot(context.Context, string) (Snapshot, error)
	Revision(context.Context) (uint64, error)
}
type Request struct {
	PrincipalID      string
	PrincipalKind    string
	Operation        string
	Scope            Scope
	SubjectID        string
	ExpectedRevision uint64
}
type Decision struct {
	Allowed  bool
	Revision uint64
	GrantIDs []string
	Reason   string
}
type DelegationRequest struct {
	PrincipalID      string
	Action           string
	RoleName         string
	Grants           []Grant
	Scope            Scope
	SubjectID        string
	ExpectedRevision uint64
}
