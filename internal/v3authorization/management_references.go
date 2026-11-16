package v3authorization

import (
	"database/sql"
	"errors"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

func managementRecordInScope(r AuthorityRecord, s evidence.Scope, g ManagementGrant, domain string) bool {
	switch r.Kind {
	case RealmRecord:
		return r.Realm != nil && r.Realm.Realm == s.Realm
	case HostRecord:
		return r.Host != nil && r.Host.Realm == s.Realm && r.Host.Host == s.Host
	case BackendRecord:
		return r.Backend != nil && r.Backend.Realm == s.Realm && r.Backend.Host == s.Host && r.Backend.Backend == s.Backend
	case ResourceRecord:
		return r.Resource != nil && r.Resource.Scope == s
	case CandidateRecord:
		return r.Candidate != nil && r.Candidate.Scope == s
	case CapabilityRecord:
		return r.Capability != nil && r.Capability.Scope == s && r.Capability.Action == Action && managementSubject(g, r.Capability.Principal)
	case MembershipRecord:
		return r.Membership != nil && r.Membership.Realm == s.Realm && r.Membership.Domain == domain && managementSubject(g, r.Membership.Principal)
	case PolicyRecord, TrustRecord:
		return r.Rule != nil && r.Rule.Scope == s && r.Rule.Action == Action
	}
	return false
}
func managementValidateRecords(v *authorityView, a ManagementActor, m ManagementMetadata, g ManagementGrant, records []AuthorityRecord) error {
	pending := map[authorityRecordKey]AuthorityRecord{}
	for _, r := range records {
		key, _, e := encodeAuthorityRecord(r)
		if e != nil {
			return e
		}
		if _, duplicate := pending[key]; duplicate {
			return ErrInvalid
		}
		pending[key] = r
		if !hasManagementOperation(g, managementOperation(r.Kind)) {
			return ErrDenied
		}
	}
	get := func(kind AuthorityRecordKind, realm, first, second string) (AuthorityRecord, error) {
		key := authorityRecordKey{kind, realm, first, second}
		if r, ok := pending[key]; ok {
			return r, nil
		}
		r, e := v.get(kind, realm, first, second)
		return r.Record, e
	}
	domain := ""
	if host, e := get(HostRecord, m.Scope.Realm, m.Scope.Host, ""); e == nil && host.Host != nil {
		domain = host.Host.OwnerDomain
	}
	for _, r := range records {
		if !managementRecordInScope(r, m.Scope, g, domain) {
			return ErrDenied
		}
		key, _ := authorityKey(r)
		var existing []byte
		e := v.tx.QueryRowContext(v.ctx, "SELECT record_bytes FROM v3_cp_authority_records WHERE kind=? AND realm=? AND first_id=? AND second_id=? FOR UPDATE", key.kind, key.realm, key.first, key.second).Scan(&existing)
		if e == nil {
			old, e := decodeAuthorityRecord(existing, key)
			if e != nil {
				return e
			}
			if !managementRecordInScope(old, m.Scope, g, domain) {
				return ErrDenied
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return ErrUnavailable
		}
		active := false
		switch r.Kind {
		case RealmRecord:
			continue
		case HostRecord:
			active = r.Host.State == "ACTIVE"
		case BackendRecord:
			active = r.Backend.State == "ACTIVE"
		case ResourceRecord:
			active = r.Resource.State == "ACTIVE"
		case CandidateRecord:
			active = r.Candidate.State == "ACTIVE"
			if r.Candidate.Approval == "APPROVED" && r.Candidate.Approver != a.Principal.ID {
				return ErrDenied
			}
			if r.Candidate.Approval != "APPROVED" && r.Candidate.Approver != "" {
				return ErrInvalid
			}
		case CapabilityRecord:
			active = r.Capability.State == "ACTIVE"
			if active && v.principal(Principal{r.Capability.Principal}) != nil {
				return ErrDenied
			}
		case MembershipRecord:
			active = r.Membership.State == "MEMBER"
			if active && v.principal(Principal{r.Membership.Principal}) != nil {
				return ErrDenied
			}
		case PolicyRecord, TrustRecord:
			active = r.Rule.State == "ACTIVE"
		}
		realm, e := get(RealmRecord, m.Scope.Realm, m.Scope.Realm, "")
		if e != nil || realm.Realm == nil {
			return ErrUnavailable
		}
		if active {
			if e = requireOwnerActive(realm.Realm.State); e != nil {
				return e
			}
		}
		if r.Kind == HostRecord {
			continue
		}
		host, e := get(HostRecord, m.Scope.Realm, m.Scope.Host, "")
		if e != nil || host.Host == nil || host.Host.Host != m.Scope.Host || host.Host.Realm != m.Scope.Realm {
			return ErrUnavailable
		}
		if active {
			if e = requireOwnerActive(host.Host.State); e != nil {
				return e
			}
		}
		if r.Kind == BackendRecord || r.Kind == MembershipRecord {
			continue
		}
		backend, e := get(BackendRecord, m.Scope.Realm, m.Scope.Backend, "")
		if e != nil || backend.Backend == nil || backend.Backend.Host != m.Scope.Host || backend.Backend.Realm != m.Scope.Realm {
			return ErrConflict
		}
		if active {
			if e = requireOwnerActive(backend.Backend.State); e != nil {
				return e
			}
		}
		if r.Kind == ResourceRecord {
			continue
		}
		resource, e := get(ResourceRecord, m.Scope.Realm, m.Scope.Resource, "")
		if e != nil || resource.Resource == nil || resource.Resource.Scope != m.Scope {
			return ErrConflict
		}
		if active {
			if e = requireOwnerActive(resource.Resource.State); e != nil {
				return e
			}
		}
	}
	return nil
}
