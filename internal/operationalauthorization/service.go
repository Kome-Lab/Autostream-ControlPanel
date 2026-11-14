package operationalauthorization

import "context"

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) load(ctx context.Context, principalID string) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if s == nil || s.store == nil {
		return Snapshot{}, ErrUnavailable
	}
	snapshot, err := s.store.Snapshot(ctx, principalID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateSnapshot(snapshot, principalID); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
func (s *Service) fresh(ctx context.Context, snapshot Snapshot) error {
	if ctx == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	revision, err := s.store.Revision(ctx)
	if err != nil {
		return err
	}
	if revision == 0 || revision != snapshot.Revision {
		return ErrStaleRevision
	}
	return ctx.Err()
}

// Current supplies Role identity and entry permissions from one fresh generation.
// The HTTP Handler still evaluates existing target/state and Subject policies.
func (s *Service) Current(ctx context.Context, principalID string, scope Scope) (Snapshot, []string, error) {
	if !validScope(scope) {
		return Snapshot{}, nil, ErrDenied
	}
	snapshot, err := s.load(ctx, principalID)
	if err != nil {
		return Snapshot{}, nil, err
	}
	granted := make(map[string]bool)
	if snapshot.Principal.State == StateActive {
		for _, grant := range snapshot.Grants {
			if grant.State == StateActive && scopeContains(grant.Scope, scope) && !grant.Subjects.SelfOnly && len(grant.Subjects.IDs) == 0 {
				granted[grant.Operation] = true
			}
		}
	}
	permissions := make([]string, 0, len(granted))
	for _, operation := range AllOperations() {
		if granted[operation] {
			permissions = append(permissions, operation)
		}
	}
	if err := s.fresh(ctx, snapshot); err != nil {
		return Snapshot{}, nil, err
	}
	return cloneSnapshot(snapshot), permissions, nil
}
func (s *Service) Permissions(ctx context.Context, principalID string, scope Scope) ([]string, error) {
	_, permissions, err := s.Current(ctx, principalID, scope)
	return permissions, err
}
func (s *Service) Authorize(ctx context.Context, request Request) (Decision, error) {
	if request.PrincipalKind != "" && request.PrincipalKind != KindHuman {
		return Decision{}, ErrDenied
	}
	if !KnownOperation(request.Operation) {
		return Decision{}, ErrUnknownOperation
	}
	if !validScope(request.Scope) {
		return Decision{}, ErrDenied
	}
	snapshot, err := s.load(ctx, request.PrincipalID)
	if err != nil {
		return Decision{}, err
	}
	if snapshot.Principal.State != StateActive {
		return Decision{}, ErrDenied
	}
	if request.ExpectedRevision != 0 && request.ExpectedRevision != snapshot.Revision {
		return Decision{}, ErrStaleRevision
	}
	decision := decide(snapshot, request)
	if !decision.Allowed {
		return decision, ErrDenied
	}
	if err := s.fresh(ctx, snapshot); err != nil {
		return Decision{}, err
	}
	return decision, nil
}
func decide(snapshot Snapshot, request Request) Decision {
	result := Decision{Revision: snapshot.Revision, Reason: "operation_not_granted"}
	for _, grant := range snapshot.Grants {
		if grant.State == StateActive && grant.Operation == request.Operation && scopeContains(grant.Scope, request.Scope) && subjectMatches(grant.Subjects, snapshot.Principal.ID, request.SubjectID) {
			result.GrantIDs = append(result.GrantIDs, grant.ID)
		}
	}
	result.Allowed = len(result.GrantIDs) > 0
	if result.Allowed {
		result.Reason = "entry_granted"
	}
	return result
}

// ValidateDelegation keeps the legacy assignment exception separate from Role
// creation/update and cannot introduce non-operational authority.
func (s *Service) ValidateDelegation(ctx context.Context, request DelegationRequest) error {
	switch request.Action {
	case "roles.assign", "roles.create", "roles.update":
	default:
		return ErrDenied
	}
	if !validScope(request.Scope) {
		return ErrDenied
	}
	snapshot, err := s.load(ctx, request.PrincipalID)
	if err != nil {
		return err
	}
	if snapshot.Principal.State != StateActive {
		return ErrDenied
	}
	if request.ExpectedRevision != 0 && request.ExpectedRevision != snapshot.Revision {
		return ErrStaleRevision
	}
	entry := decide(snapshot, Request{PrincipalID: request.PrincipalID, Operation: request.Action, Scope: request.Scope, SubjectID: request.SubjectID})
	if !entry.Allowed {
		return ErrDenied
	}
	entryDelegates := false
	for _, grant := range snapshot.Grants {
		for _, id := range entry.GrantIDs {
			if grant.ID == id && grant.Delegation.Mode != DelegationNone {
				entryDelegates = true
			}
		}
	}
	if !entryDelegates {
		return ErrDenied
	}
	superAdmin := false
	for _, role := range snapshot.Roles {
		if role.Name == "super_admin" {
			superAdmin = true
		}
	}
	if request.RoleName == "super_admin" && (request.Action != "roles.assign" || !superAdmin) {
		return ErrDenied
	}
	assignmentException := superAdmin && request.Action == "roles.assign"
	for _, child := range request.Grants {
		if !KnownOperation(child.Operation) || !validScope(child.Scope) || !validSubjects(child.Subjects) {
			return ErrDenied
		}
		if child.Delegation.Mode != DelegationNone && child.Delegation.Mode != DelegationSubset {
			return ErrDenied
		}
		if request.Action == "roles.assign" && request.SubjectID != "" && child.PrincipalID != request.SubjectID {
			return ErrDenied
		}
		entryContains := false
		for _, parent := range snapshot.Grants {
			for _, id := range entry.GrantIDs {
				if parent.ID == id && parent.Delegation.Mode != DelegationNone && scopeContains(parent.Scope, child.Scope) && subjectMatches(parent.Subjects, snapshot.Principal.ID, child.PrincipalID) {
					entryContains = true
				}
			}
		}
		if !entryContains {
			return ErrDenied
		}
		// The legacy super_admin assignment exception covers unheld operations.
		// It cannot erase explicit typed constraints on a represented operation.
		represented, contained := false, false
		for _, parent := range snapshot.Grants {
			if parent.Operation == child.Operation {
				represented = true
			}
			if parent.State != StateActive || parent.Operation != child.Operation || parent.Delegation.Mode != DelegationSubset || !scopeContains(parent.Scope, child.Scope) {
				continue
			}
			if subjectsContain(parent.Subjects, snapshot.Principal.ID, child.Subjects, child.PrincipalID) {
				contained = true
				break
			}
		}
		if !contained && !(assignmentException && !represented) {
			return ErrDenied
		}
	}
	return s.fresh(ctx, snapshot)
}
func subjectsContain(parent SubjectConstraints, parentPrincipal string, child SubjectConstraints, childPrincipal string) bool {
	if !parent.SelfOnly && len(parent.IDs) == 0 {
		return true
	}
	if !child.SelfOnly && len(child.IDs) == 0 {
		return false
	}
	var ids []string
	if child.SelfOnly {
		if !validID(childPrincipal) {
			return false
		}
		if len(child.IDs) == 0 {
			ids = []string{childPrincipal}
		} else {
			for _, id := range child.IDs {
				if id == childPrincipal {
					ids = []string{id}
					break
				}
			}
		}
	} else {
		ids = child.IDs
	}
	for _, id := range ids {
		if !subjectMatches(parent, parentPrincipal, id) {
			return false
		}
	}
	return true
}
