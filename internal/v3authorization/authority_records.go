package v3authorization

import (
	"bytes"
	"encoding/json"
	"strconv"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// These are CP-owned v3 registrations, never v2 permissions/Grants or remote
// Fence/Boot/Drain assertions. Only an explicitly composed v3 management workflow
// may provision them; startup and ordinary authorization never register targets.
type AuthorityRecordKind string

const (
	RealmRecord      AuthorityRecordKind = "realm"
	HostRecord       AuthorityRecordKind = "host"
	ResourceRecord   AuthorityRecordKind = "resource"
	BackendRecord    AuthorityRecordKind = "backend"
	CandidateRecord  AuthorityRecordKind = "candidate"
	CapabilityRecord AuthorityRecordKind = "capability"
	MembershipRecord AuthorityRecordKind = "membership"
	PolicyRecord     AuthorityRecordKind = "policy"
	TrustRecord      AuthorityRecordKind = "trust"
)

type RealmRegistration struct{ Realm, State string }
type HostRegistration struct{ Realm, Host, OwnerDomain, State string }
type ResourceRegistration struct {
	Scope             evidence.Scope
	ReferenceRevision string
	StateVersion      uint64
	State             string
}
type BackendRegistration struct{ Realm, Host, Backend, State string }
type CandidateApproval struct {
	Scope                                           evidence.Scope
	Revision, ContentPin, Approval, Approver, State string
}
type V3Capability struct {
	Principal               string
	Scope                   evidence.Scope
	Action, Revision, State string
	Issue, ReadHistory      bool
}
type DomainMembership struct{ Realm, Principal, Domain, Revision, State string }
type ScopeRule struct {
	Scope                               evidence.Scope
	Action, Revision, State, Revocation string
}

// AuthorityRecord is a closed storage union, NOT a P-A Request/Binding format.
// Revocations/tombstones replace only the current view; every version is retained
// in the append-only CP authority history. UNKNOWN is never an active record.
type AuthorityRecord struct {
	Kind       AuthorityRecordKind
	Realm      *RealmRegistration
	Host       *HostRegistration
	Resource   *ResourceRegistration
	Backend    *BackendRegistration
	Candidate  *CandidateApproval
	Capability *V3Capability
	Membership *DomainMembership
	Rule       *ScopeRule
}
type authorityRecordKey struct {
	kind                 AuthorityRecordKind
	realm, first, second string
}
type ownedRecord struct {
	Record     AuthorityRecord
	Generation uint64
	UpdatedAt  string // Database wall-time metadata only; never qualified Validity.
}
type ResolvedTarget struct {
	Realm      RealmRegistration
	Host       HostRegistration
	Resource   ResourceRegistration
	Backend    BackendRegistration
	Candidate  CandidateApproval
	Revision   string
	ObservedAt string // Database read time, not a qualified clock or Host observation.
}
type ResolvedRule struct {
	Rule       ScopeRule
	Generation uint64
	UpdatedAt  string
}
type ResolvedPolicyTrust struct {
	Policy, Trust ResolvedRule
	ObservedAt    string
}

func ownerState(s string) bool { return s == "ACTIVE" || s == "INACTIVE" || s == "UNKNOWN" }
func ownerIDs(values ...string) bool {
	for _, v := range values {
		if !validID(v) {
			return false
		}
	}
	return true
}
func authorityKey(v AuthorityRecord) (authorityRecordKey, error) {
	count := 0
	for _, yes := range []bool{v.Realm != nil, v.Host != nil, v.Resource != nil, v.Backend != nil, v.Candidate != nil, v.Capability != nil, v.Membership != nil, v.Rule != nil} {
		if yes {
			count++
		}
	}
	if count != 1 {
		return authorityRecordKey{}, ErrInvalid
	}
	var realm, first, second string
	valid := false
	switch v.Kind {
	case RealmRecord:
		if p := v.Realm; p != nil {
			realm, first = p.Realm, p.Realm
			valid = ownerIDs(p.Realm) && ownerState(p.State)
		}
	case HostRecord:
		if p := v.Host; p != nil {
			realm, first = p.Realm, p.Host
			valid = ownerIDs(p.Realm, p.Host, p.OwnerDomain) && ownerState(p.State)
		}
	case ResourceRecord:
		if p := v.Resource; p != nil {
			realm, first = p.Scope.Realm, p.Scope.Resource
			valid = validScope(p.Scope) && validID(p.ReferenceRevision) && p.StateVersion != ^uint64(0) && ownerState(p.State)
		}
	case BackendRecord:
		if p := v.Backend; p != nil {
			realm, first = p.Realm, p.Backend
			valid = ownerIDs(p.Realm, p.Host, p.Backend) && ownerState(p.State)
		}
	case CandidateRecord:
		if p := v.Candidate; p != nil {
			realm, first, second = p.Scope.Realm, p.Scope.Resource, p.Revision
			valid = validScope(p.Scope) && ownerIDs(p.Revision, p.ContentPin) && ownerState(p.State) &&
				(p.Approval == "APPROVED" && validID(p.Approver) || p.Approval == "PENDING" || p.Approval == "REJECTED" || p.Approval == "UNKNOWN")
		}
	case CapabilityRecord:
		if p := v.Capability; p != nil {
			realm, first, second = p.Scope.Realm, p.Principal, p.Scope.Resource
			valid = validScope(p.Scope) && ownerIDs(p.Principal, p.Revision) && p.Action == Action && ownerState(p.State)
		}
	case MembershipRecord:
		if p := v.Membership; p != nil {
			realm, first, second = p.Realm, p.Principal, p.Domain
			valid = ownerIDs(p.Realm, p.Principal, p.Domain, p.Revision) && (p.State == "MEMBER" || p.State == "REVOKED" || p.State == "UNKNOWN")
		}
	case PolicyRecord, TrustRecord:
		if p := v.Rule; p != nil {
			realm, first, second = p.Scope.Realm, p.Scope.Resource, p.Action
			valid = validScope(p.Scope) && validID(p.Revision) && p.Action == Action && ownerState(p.State) && (p.Revocation == "ACTIVE" || p.Revocation == "REVOKED" || p.Revocation == "UNKNOWN")
		}
	}
	if !valid {
		return authorityRecordKey{}, ErrInvalid
	}
	return authorityRecordKey{v.Kind, realm, first, second}, nil
}
func encodeAuthorityRecord(v AuthorityRecord) (authorityRecordKey, []byte, error) {
	key, err := authorityKey(v)
	if err != nil {
		return key, nil, err
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) > 32<<10 {
		return key, nil, ErrInvalid
	}
	return key, body, nil
}
func decodeAuthorityRecord(body []byte, key authorityRecordKey) (AuthorityRecord, error) {
	if len(body) == 0 || len(body) > 32<<10 || sqlDuplicateJSON(body) {
		return AuthorityRecord{}, ErrUnavailable
	}
	var v AuthorityRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil {
		return AuthorityRecord{}, ErrUnavailable
	}
	actual, canonical, err := encodeAuthorityRecord(v)
	if err != nil || actual != key || !bytes.Equal(body, canonical) {
		return AuthorityRecord{}, ErrUnavailable
	}
	return v, nil
}
func ownerRevision(prefix string, generation uint64) string {
	return prefix + "-" + strconv.FormatUint(generation, 10)
}
func requireOwnerActive(state string) error {
	if state == "ACTIVE" {
		return nil
	}
	if state == "UNKNOWN" {
		return ErrUnavailable
	}
	return ErrDenied
}
