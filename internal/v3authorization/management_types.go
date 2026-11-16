package v3authorization

import (
	"context"
	"encoding/json"
	"sort"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

type ManagementOperation string

const (
	ManagementRead             ManagementOperation = "read"
	ManagementRealm            ManagementOperation = "realm.write"
	ManagementHost             ManagementOperation = "host.write"
	ManagementResource         ManagementOperation = "resource.write"
	ManagementBackend          ManagementOperation = "backend.write"
	ManagementCandidate        ManagementOperation = "candidate.write"
	ManagementCapability       ManagementOperation = "capability.write"
	ManagementPolicy           ManagementOperation = "policy.write"
	ManagementTrust            ManagementOperation = "trust.write"
	ManagementMembership       ManagementOperation = "membership.write"
	ManagementAdministrator    ManagementOperation = "administrator.write"
	ManagementDelegateSubjects ManagementOperation = "subjects.delegate"
	ManagementApplied          Status              = "APPLIED"
	ManagementInspected        Status              = "INSPECTED"
)

// Management is an independent v3 permission, never an Issue/ReadHistory bit.
type ManagementGrant struct {
	ProvisionTargets bool `json:"provision_targets,omitempty"`
	Principal        string
	Scope            evidence.Scope
	Action, State    string
	Operations       []ManagementOperation
	Subjects         []string
}
type ManagementActor struct {
	Principal   Principal
	SessionHash string // trusted server authentication context only, never body/audit
}
type ManagementMetadata struct {
	EventID          string         `json:"event_id"`
	CorrelationID    string         `json:"correlation_id"`
	ExpectedRevision uint64         `json:"expected_revision"`
	Scope            evidence.Scope `json:"scope"`
	Action           string         `json:"action"`
}
type ManagementResult struct {
	Status         Status
	Code           string
	Generation     uint64
	Initialized    bool
	Records        []AuthorityRecord
	Administrators []ManagementGrant
}
type ManagementAPI interface {
	Apply(context.Context, ManagementActor, ManagementMetadata, []AuthorityRecord, []ManagementGrant) ManagementResult
	Inspect(context.Context, ManagementActor, ManagementMetadata) ManagementResult
	RecordTransportDenial(context.Context, Principal, string) error
}

// BootstrapAuthorizer is deliberately unconfigured in product composition.
// Its independent authenticated, one-time approval and restore/replay trust
// origin must be selected by the operator, not inferred from v2 identity/roles.
type BootstrapAuthorizer interface {
	AuthorizeBootstrap(context.Context, ManagementActor, ManagementMetadata, ManagementGrant) error
}

var ErrManagementUninitialized = SafeError{"v3_management_uninitialized"}

func managementOperations() []ManagementOperation {
	return []ManagementOperation{ManagementRead, ManagementRealm, ManagementHost, ManagementResource, ManagementBackend,
		ManagementCandidate, ManagementCapability, ManagementPolicy, ManagementTrust, ManagementMembership, ManagementAdministrator, ManagementDelegateSubjects}
}
func managementOperation(kind AuthorityRecordKind) ManagementOperation {
	switch kind {
	case RealmRecord:
		return ManagementRealm
	case HostRecord:
		return ManagementHost
	case ResourceRecord:
		return ManagementResource
	case BackendRecord:
		return ManagementBackend
	case CandidateRecord:
		return ManagementCandidate
	case CapabilityRecord:
		return ManagementCapability
	case PolicyRecord:
		return ManagementPolicy
	case TrustRecord:
		return ManagementTrust
	case MembershipRecord:
		return ManagementMembership
	}
	return ""
}
func hasManagementOperation(g ManagementGrant, op ManagementOperation) bool {
	for _, v := range g.Operations {
		if v == op {
			return true
		}
	}
	return false
}
func managementSubject(g ManagementGrant, p string) bool {
	if hasManagementOperation(g, ManagementDelegateSubjects) {
		return true
	}
	for _, v := range g.Subjects {
		if v == p {
			return true
		}
	}
	return false
}
func encodeManagementGrant(g ManagementGrant) ([]byte, error) {
	if !validID(g.Principal) || !validScope(g.Scope) || g.Action != Action ||
		(g.State != "ACTIVE" && g.State != "REVOKED" && g.State != "UNKNOWN") ||
		len(g.Operations) == 0 || len(g.Operations) > 12 || len(g.Subjects) == 0 || len(g.Subjects) > 32 {
		return nil, ErrInvalid
	}
	ops := map[ManagementOperation]bool{}
	for _, op := range g.Operations {
		valid := false
		for _, allowed := range managementOperations() {
			if op == allowed {
				valid = true
			}
		}
		if !valid || ops[op] {
			return nil, ErrInvalid
		}
		ops[op] = true
	}
	subjects := map[string]bool{}
	for _, p := range g.Subjects {
		if !validID(p) || subjects[p] {
			return nil, ErrInvalid
		}
		subjects[p] = true
	}
	g.Operations = append([]ManagementOperation(nil), g.Operations...)
	g.Subjects = append([]string(nil), g.Subjects...)
	sort.Slice(g.Operations, func(i, j int) bool { return g.Operations[i] < g.Operations[j] })
	sort.Strings(g.Subjects)
	return json.Marshal(g)
}
func validManagementMetadata(m ManagementMetadata) bool {
	return validID(m.EventID) && validID(m.CorrelationID) && validScope(m.Scope) && m.Action == Action && m.ExpectedRevision != ^uint64(0)
}
func managementFailure(err error) ManagementResult {
	switch err {
	case nil:
		return ManagementResult{Status: Unavailable, Code: "v3_management_unavailable"}
	case ErrDenied:
		return ManagementResult{Status: Denied, Code: "v3_management_denied"}
	case ErrConflict:
		return ManagementResult{Status: Conflict, Code: "v3_management_conflict"}
	case ErrInvalid:
		return ManagementResult{Status: Invalid, Code: "v3_management_invalid"}
	case ErrStoreUnknown:
		return ManagementResult{Status: Unknown, Code: "v3_management_storage_unknown"}
	case ErrManagementUninitialized:
		return ManagementResult{Status: Unavailable, Code: "v3_management_uninitialized"}
	}
	return ManagementResult{Status: Unavailable, Code: "v3_management_unavailable"}
}
