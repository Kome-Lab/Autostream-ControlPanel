package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// Only explicitly disposable autostream_v3_auth_ci is accepted by sqlTestDB.
// SQLAuthority/Resolvers/owner Save/SQLStore are product code. Authentication
// transport and every Integrity/clock/current-applicability hook remain doubles.
type authorityFixture struct {
	*serviceFixture
	db        *sql.DB
	authority *SQLAuthority
	records   []AuthorityRecord
}

func newAuthorityFixture(t *testing.T) *authorityFixture {
	t.Helper()
	db := sqlTestDB(t)
	for _, table := range []string{"v3_cp_authority_history", "v3_cp_authority_records", "v3_cp_authority_epoch", "v3_cp_authority_schema", "v3_authority_deadlock_weight", "user_roles", "role_permissions", "roles", "users"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal("reset disposable authority schema", err)
		}
	}
	// Minimal existing CP user identity shape; no password, token or live identity.
	if _, err := db.Exec("CREATE TABLE users(id CHAR(36) PRIMARY KEY,status ENUM('active','disabled','locked','pending_password_change') NOT NULL) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := ApplyAuthorityMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	f := resolverTestFixture(t, resolverTestCommand())
	i := f.intent
	if _, err := db.Exec("INSERT INTO users(id,status) VALUES(?,'active')", f.principal.ID); err != nil {
		t.Fatal(err)
	}
	records := []AuthorityRecord{
		{Kind: RealmRecord, Realm: &RealmRegistration{Realm: i.Scope.Realm, State: "ACTIVE"}},
		{Kind: HostRecord, Host: &HostRegistration{Realm: i.Scope.Realm, Host: i.Scope.Host, OwnerDomain: "domain-1", State: "ACTIVE"}},
		{Kind: ResourceRecord, Resource: &ResourceRegistration{Scope: i.Scope, ReferenceRevision: i.ExpectedRevision, StateVersion: i.ExpectedStateVersion, State: "ACTIVE"}},
		{Kind: BackendRecord, Backend: &BackendRegistration{Realm: i.Scope.Realm, Host: i.Scope.Host, Backend: i.Scope.Backend, State: "ACTIVE"}},
		{Kind: CandidateRecord, Candidate: &CandidateApproval{Scope: i.Scope, Revision: i.CandidateRevision, ContentPin: i.CandidateContentPin, Approval: "APPROVED", Approver: "synthetic-approver", State: "ACTIVE"}},
		{Kind: CapabilityRecord, Capability: &V3Capability{Principal: f.principal.ID, Scope: i.Scope, Action: Action, Revision: "capability-1", State: "ACTIVE", Issue: true, ReadHistory: true}},
		{Kind: MembershipRecord, Membership: &DomainMembership{Realm: i.Scope.Realm, Principal: f.principal.ID, Domain: "domain-1", Revision: i.Required.Membership, State: "MEMBER"}},
		{Kind: PolicyRecord, Rule: &ScopeRule{Scope: i.Scope, Action: Action, Revision: i.Required.Policy, State: "ACTIVE", Revocation: "ACTIVE"}},
		{Kind: TrustRecord, Rule: &ScopeRule{Scope: i.Scope, Action: Action, Revision: i.Required.Trust, State: "ACTIVE", Revocation: "ACTIVE"}},
	}
	a := NewSQLAuthority(db)
	if generation, err := a.Save(context.Background(), 0, records); err != nil || generation != 1 {
		t.Fatal("save synthetic registrations through real owner", err)
	}
	f.deps.Authority = a
	f.deps.Store = NewSQLStore(db)
	f.service = NewService(f.service.config, f.deps)
	return &authorityFixture{f, db, a, records}
}
func (f *authorityFixture) mutate(t *testing.T, kind AuthorityRecordKind, change func(*AuthorityRecord)) {
	t.Helper()
	var record AuthorityRecord
	found := false
	for _, r := range f.records {
		if r.Kind == kind {
			body, _ := json.Marshal(r)
			if json.Unmarshal(body, &record) != nil {
				t.Fatal("clone fixture")
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing fixture kind")
	}
	change(&record)
	generation, err := f.authority.Generation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.authority.Save(context.Background(), generation, []AuthorityRecord{record}); err != nil {
		t.Fatal("owner mutation", err)
	}
	for n, r := range f.records {
		if r.Kind == kind {
			f.records[n] = record
		}
	}
}
func (f *authorityFixture) remove(t *testing.T, kind AuthorityRecordKind) {
	t.Helper()
	if _, err := f.db.Exec("DELETE FROM v3_cp_authority_records WHERE kind=?", kind); err != nil {
		t.Fatal(err)
	}
}
func requireRefusal(t *testing.T, decision Decision, err error) {
	t.Helper()
	if err == nil || decision.State == "ALLOWED" {
		t.Fatal("unqualified source became ALLOWED", err)
	}
}

func TestSQLAuthorityUnconfiguredFailsClosed(t *testing.T) {
	a := NewSQLAuthority(nil)
	f := resolverTestFixture(t, resolverTestCommand())
	if _, err := a.Read(context.Background(), f.principal, f.intent); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := a.AuthorizeRead(context.Background(), f.principal, f.lookup()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := a.WithCurrent(context.Background(), f.principal, f.intent, Decision{}, func(context.Context, Decision) error { t.Fatal("nil source called callback"); return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestSQLAuthorityReadMatrix(t *testing.T) {
	cases := []string{"normal-principal", "principal-mismatch", "principal-disabled", "capability-missing", "capability-revoked", "scope-mismatch", "action-mismatch", "realm-mismatch", "host-unregistered", "host-inactive", "resource-unregistered", "resource-host-mismatch", "backend-mismatch", "policy-normal", "policy-revision-changed", "policy-revoked", "trust-normal", "trust-revision-changed", "trust-revoked", "membership-revoked", "revision-unavailable", "candidate-normal", "candidate-unapproved", "candidate-pin-mismatch", "resource-changed", "backend-changed", "owner-unknown", "policy-unknown", "candidate-unregistered", "candidate-scope-mismatch", "resource-inactive", "backend-unregistered", "membership-missing", "trust-unknown"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			p, i := f.principal, f.intent
			success := false
			switch name {
			case "normal-principal", "policy-normal", "trust-normal", "candidate-normal":
				success = true
			case "principal-mismatch":
				p.ID = "different-principal"
			case "principal-disabled":
				if _, err := f.db.Exec("UPDATE users SET status='disabled' WHERE id=?", p.ID); err != nil {
					t.Fatal(err)
				}
			case "capability-missing":
				f.remove(t, CapabilityRecord)
			case "capability-revoked":
				f.mutate(t, CapabilityRecord, func(r *AuthorityRecord) { r.Capability.State = "INACTIVE" })
			case "scope-mismatch":
				i.Scope.Resource = "other-resource"
			case "action-mismatch":
				i.Action = "service.restart"
			case "realm-mismatch":
				i.Scope.Realm = "other-realm"
			case "host-unregistered":
				f.remove(t, HostRecord)
			case "host-inactive":
				f.mutate(t, HostRecord, func(r *AuthorityRecord) { r.Host.State = "INACTIVE" })
			case "resource-unregistered":
				f.remove(t, ResourceRecord)
			case "resource-host-mismatch":
				f.mutate(t, ResourceRecord, func(r *AuthorityRecord) { r.Resource.Scope.Host = "other-host" })
			case "backend-mismatch":
				i.Scope.Backend = "other-backend"
			case "policy-revision-changed":
				f.mutate(t, PolicyRecord, func(r *AuthorityRecord) { r.Rule.Revision = "policy-2" })
			case "policy-revoked":
				f.mutate(t, PolicyRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "REVOKED" })
			case "trust-revision-changed":
				f.mutate(t, TrustRecord, func(r *AuthorityRecord) { r.Rule.Revision = "trust-2" })
			case "trust-revoked":
				f.mutate(t, TrustRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "REVOKED" })
			case "membership-revoked":
				f.mutate(t, MembershipRecord, func(r *AuthorityRecord) { r.Membership.State = "REVOKED" })
			case "revision-unavailable":
				if _, err := f.db.Exec("UPDATE v3_cp_authority_records SET record_bytes='{}' WHERE kind='policy'"); err != nil {
					t.Fatal(err)
				}
			case "candidate-unapproved":
				f.mutate(t, CandidateRecord, func(r *AuthorityRecord) { r.Candidate.Approval = "PENDING"; r.Candidate.Approver = "" })
			case "candidate-pin-mismatch":
				i.CandidateContentPin = "different-pin"
			case "resource-changed":
				f.mutate(t, ResourceRecord, func(r *AuthorityRecord) { r.Resource.StateVersion++ })
			case "backend-changed":
				f.mutate(t, BackendRecord, func(r *AuthorityRecord) { r.Backend.Host = "different-host" })
			case "owner-unknown":
				f.mutate(t, HostRecord, func(r *AuthorityRecord) { r.Host.State = "UNKNOWN" })
			case "candidate-unregistered":
				f.remove(t, CandidateRecord)
			case "candidate-scope-mismatch":
				f.mutate(t, CandidateRecord, func(r *AuthorityRecord) { r.Candidate.Scope.Host = "other-host" })
			case "resource-inactive":
				f.mutate(t, ResourceRecord, func(r *AuthorityRecord) { r.Resource.State = "INACTIVE" })
			case "backend-unregistered":
				f.remove(t, BackendRecord)
			case "membership-missing":
				f.remove(t, MembershipRecord)
			case "trust-unknown":
				f.mutate(t, TrustRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "UNKNOWN" })
			case "policy-unknown":
				f.mutate(t, PolicyRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "UNKNOWN" })
			}
			d, err := f.authority.Read(context.Background(), p, i)
			if !success {
				requireRefusal(t, d, err)
				return
			}
			if err != nil || d.Principal != p.ID || d.Scope != i.Scope || d.Action != Action || d.State != "ALLOWED" || d.AuthorityRevision != "authority-1" || d.TargetRevision != "target-1" || d.PolicyRevision != i.Required.Policy || d.TrustRevision != i.Required.Trust {
				t.Fatal("normal source mapping", d, err)
			}
			target, err := f.authority.ResolveTarget(context.Background(), i)
			if err != nil || target.Resource.Scope != i.Scope || target.Candidate.ContentPin != i.CandidateContentPin || target.ObservedAt == "" {
				t.Fatal("actual target resolver", err)
			}
			rules, err := f.authority.ResolvePolicyTrust(context.Background(), i.Scope, i.Action)
			if err != nil || rules.Policy.Rule.Revision != i.Required.Policy || rules.Trust.Rule.Revision != i.Required.Trust || rules.Policy.Generation != 1 || rules.Trust.UpdatedAt == "" || rules.ObservedAt == "" {
				t.Fatal("actual rule resolver", err)
			}
		})
	}
}
func TestSQLAuthorityWithCurrentChanges(t *testing.T) {
	for _, kind := range []AuthorityRecordKind{CapabilityRecord, RealmRecord, HostRecord, ResourceRecord, BackendRecord, PolicyRecord, TrustRecord, MembershipRecord, CandidateRecord} {
		t.Run(string(kind), func(t *testing.T) {
			f := newAuthorityFixture(t)
			ctx := context.Background()
			before, err := f.authority.Read(ctx, f.principal, f.intent)
			if err != nil {
				t.Fatal(err)
			}
			f.mutate(t, kind, func(r *AuthorityRecord) {
				switch kind {
				case CapabilityRecord:
					r.Capability.Issue = false
				case RealmRecord:
					r.Realm.State = "INACTIVE"
				case HostRecord:
					r.Host.State = "INACTIVE"
				case ResourceRecord:
					r.Resource.StateVersion++
				case BackendRecord:
					r.Backend.State = "INACTIVE"
				case PolicyRecord:
					r.Rule.Revision = "changed-policy"
				case TrustRecord:
					r.Rule.Revision = "changed-trust"
				case MembershipRecord:
					r.Membership.State = "REVOKED"
				case CandidateRecord:
					r.Candidate.Approval = "REJECTED"
				}
			})
			calls := 0
			err = f.authority.WithCurrent(ctx, f.principal, f.intent, before, func(context.Context, Decision) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatal("changed owner ran save", err, calls)
			}
		})
	}
	for _, name := range []string{"authority-generation", "target-generation", "principal-disabled"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			ctx := context.Background()
			before, err := f.authority.Read(ctx, f.principal, f.intent)
			if err != nil {
				t.Fatal(err)
			}
			if name == "principal-disabled" {
				_, err = f.db.Exec("UPDATE users SET status='disabled' WHERE id=?", f.principal.ID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				kind := CapabilityRecord
				if name == "target-generation" {
					kind = ResourceRecord
				}
				// Identical values written again still advance generation: an ABA restore
				// cannot reuse the old decision merely because visible revisions match.
				f.mutate(t, kind, func(*AuthorityRecord) {})
			}
			calls := 0
			err = f.authority.WithCurrent(ctx, f.principal, f.intent, before, func(context.Context, Decision) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatal("stale generation reached callback")
			}
		})
	}
}
func TestSQLAuthorityServiceSQLStoreAndHistoricalRead(t *testing.T) {
	f := newAuthorityFixture(t)
	issued := f.issue()
	if issued.Status != Issued || issued.AuthorizesExecution {
		t.Fatal(issued)
	}
	read := f.service.Read(context.Background(), f.principal, f.lookup())
	if read.Status != Replay || !bytes.Equal(issued.Envelope, read.Envelope) {
		t.Fatal(read)
	}
	// Dedicated history permission remains usable after ISSUE permission and
	// Candidate/Policy eligibility are removed. It never grants a new execution.
	f.mutate(t, CapabilityRecord, func(r *AuthorityRecord) { r.Capability.Issue = false })
	f.mutate(t, PolicyRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "REVOKED" })
	f.mutate(t, CandidateRecord, func(r *AuthorityRecord) { r.Candidate.Approval = "REJECTED" })
	read = f.service.Read(context.Background(), f.principal, f.lookup())
	if read.Status != Replay || read.AuthorizesExecution || !bytes.Equal(read.Envelope, issued.Envelope) || f.security.seals != 1 {
		t.Fatal("history renewed or used issue authority", read)
	}
	if result := f.issue(); result.Status == Issued || result.Status == Replay {
		t.Fatal("history permission issued evidence")
	}
	for _, name := range []string{"other-scope", "other-host", "other-execution", "other-principal", "evidence-id-only"} {
		t.Run(name, func(t *testing.T) {
			p, q := f.principal, f.lookup()
			switch name {
			case "other-scope":
				q.Scope.Resource = "other-resource"
			case "other-host":
				q.Scope.Host = "other-host"
			case "other-execution":
				q.Execution.AttemptID = "other-attempt"
			case "other-principal":
				p.ID = "other-user"
			case "evidence-id-only":
				q.Scope = evidence.Scope{}
			}
			if result := f.service.Read(context.Background(), p, q); result.Status == Replay || len(result.Envelope) != 0 {
				t.Fatal("cross-boundary history disclosure", result)
			}
		})
	}
	f.mutate(t, CapabilityRecord, func(r *AuthorityRecord) { r.Capability.ReadHistory = false })
	if result := f.service.Read(context.Background(), f.principal, f.lookup()); result.Status != Denied || len(result.Envelope) != 0 {
		t.Fatal("read permission revocation ignored", result)
	}
}
func TestSQLAuthorityDuplicateAndBindingCollision(t *testing.T) {
	f := newAuthorityFixture(t)
	start := make(chan struct{})
	results := make(chan Result, 8)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- f.issue() }()
	}
	close(start)
	wg.Wait()
	close(results)
	issued := 0
	for r := range results {
		switch r.Status {
		case Issued:
			issued++
		case Pending, Replay:
		default:
			t.Fatal("concurrent issuance result", r)
		}
	}
	if issued != 1 || f.security.seals != 1 {
		t.Fatal("duplicate generation", issued, f.security.seals)
	}
	c := resolverTestCommand()
	c.OriginalBudget = "different-budget"
	_, raw := resolverTestRequest(t, c)
	changed := f.service.Issue(context.Background(), f.principal, IssueRequest{OriginalRequest: raw, Frame: f.request.Frame})
	if changed.Status != Conflict || f.security.seals != 1 {
		t.Fatal("same identity accepted different canonical Binding", changed)
	}
	var count int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authorization_issuances").Scan(&count) != nil || count != 1 {
		t.Fatal("issuance liability grew")
	}
}
func TestSQLAuthorityV2RoleAndGrantDoNotSupplyCapability(t *testing.T) {
	f := newAuthorityFixture(t)
	f.remove(t, CapabilityRecord)
	for _, stmt := range []string{"CREATE TABLE roles(id CHAR(36) PRIMARY KEY,name VARCHAR(128)) ENGINE=InnoDB", "CREATE TABLE user_roles(user_id CHAR(36),role_id CHAR(36)) ENGINE=InnoDB", "CREATE TABLE role_permissions(role_id CHAR(36),permission VARCHAR(160)) ENGINE=InnoDB", "INSERT INTO roles VALUES('synthetic-admin','super_admin')", "INSERT INTO role_permissions VALUES('synthetic-admin','system_updates.execute')"} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec("INSERT INTO user_roles VALUES(?,'synthetic-admin')", f.principal.ID); err != nil {
		t.Fatal(err)
	}
	d, err := f.authority.Read(context.Background(), f.principal, f.intent)
	requireRefusal(t, d, err)
	result := f.service.Issue(context.Background(), f.principal, IssueRequest{OriginalRequest: json.RawMessage("{\"grant_id\":\"synthetic-v2-grant\",\"authorized\":true}"), Frame: f.request.Frame})
	if result.Status != Invalid || f.security.seals != 0 {
		t.Fatal("v2 Grant/role converted to v3 authority", result)
	}
}
func TestSQLAuthorityProductionDependenciesRemainUnavailable(t *testing.T) {
	f := newAuthorityFixture(t)
	service := NewService(f.service.config, Dependencies{Resolver: NewCanonicalBindingResolver(), Authority: f.authority, Store: NewSQLStore(f.db)})
	if result := service.Issue(context.Background(), f.principal, f.request); result.Status != Unavailable || len(result.Envelope) != 0 {
		t.Fatal("real SQL source enabled missing security/time", result)
	}
	var count int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authorization_issuances").Scan(&count) != nil || count != 0 {
		t.Fatal("unconfigured dependencies claimed issuance")
	}
}
func TestSQLAuthorityConcurrentOwnerCAS(t *testing.T) {
	f := newAuthorityFixture(t)
	start := make(chan struct{})
	result := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() {
			<-start
			_, err := f.authority.Save(context.Background(), 1, []AuthorityRecord{f.records[5]})
			result <- err
		}()
	}
	close(start)
	success, conflict := 0, 0
	for n := 0; n < 2; n++ {
		err := <-result
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	generation, err := f.authority.Generation(context.Background())
	if err != nil || success != 1 || conflict != 1 || generation != 2 {
		t.Fatal("concurrent owner CAS", success, conflict, generation, err)
	}
}
func TestSQLAuthorityEmptySourceNeverSelfRegisters(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.db.Exec("DELETE FROM v3_cp_authority_records"); err != nil {
		t.Fatal(err)
	}
	var before int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authority_history").Scan(&before) != nil {
		t.Fatal("history")
	}
	d, err := f.authority.Read(context.Background(), f.principal, f.intent)
	requireRefusal(t, d, err)
	if _, err = f.authority.ResolveTarget(context.Background(), f.intent); err == nil {
		t.Fatal("empty target was registered from the request")
	}
	if _, err = f.authority.ResolvePolicyTrust(context.Background(), f.intent.Scope, f.intent.Action); err == nil {
		t.Fatal("empty policy/trust became active")
	}
	var after, count int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authority_records").Scan(&count) != nil || count != 0 || f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authority_history").Scan(&after) != nil || after != before {
		t.Fatal("read repaired or seeded ownership")
	}
}
func TestSQLAuthorityOtherOwnedExecutionIsPrivate(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.db.Exec("INSERT INTO users(id,status) VALUES('operator-2','active')"); err != nil {
		t.Fatal(err)
	}
	capability := *f.records[5].Capability
	capability.Principal = "operator-2"
	membership := *f.records[6].Membership
	membership.Principal = "operator-2"
	if _, err := f.authority.Save(context.Background(), 1, []AuthorityRecord{{Kind: CapabilityRecord, Capability: &capability}, {Kind: MembershipRecord, Membership: &membership}}); err != nil {
		t.Fatal(err)
	}
	command := resolverTestCommand()
	command.RequestingPrincipal = "operator-2"
	command.Execution = evidence.Execution{RequestID: "request-2", ExecutionKey: "execution-2", OperationID: "operation-2", AttemptID: "attempt-2", StepID: "step-2"}
	_, raw := resolverTestRequest(t, command)
	issued := f.service.Issue(context.Background(), Principal{ID: "operator-2"}, IssueRequest{OriginalRequest: raw, Frame: f.request.Frame})
	if issued.Status != Issued {
		t.Fatal("second qualified principal could not issue", issued)
	}
	envelope, err := evidence.Decode(issued.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	lookup := Lookup{Scope: command.Scope, Execution: command.Execution, EvidenceID: envelope.EvidenceID}
	if result := f.service.Read(context.Background(), f.principal, lookup); result.Status != Denied || len(result.Envelope) != 0 {
		t.Fatal("known valid foreign Execution leaked within the same Scope", result)
	}
	own := f.service.Read(context.Background(), Principal{ID: "operator-2"}, lookup)
	if own.Status != Replay || !bytes.Equal(own.Envelope, issued.Envelope) {
		t.Fatal("legitimate history read failed", own)
	}
}
func TestSQLAuthorityRequiresTransactionalFinish(t *testing.T) {
	t.Run("missing-finish", func(t *testing.T) {
		f := newAuthorityFixture(t)
		pending := authorityPending(t, f)
		calls := 0
		err := f.authority.WithCurrent(context.Background(), f.principal, f.intent, pending.Decision, func(context.Context, Decision) error { calls++; return nil })
		if err == nil || calls != 1 {
			t.Fatal("empty callback became a successful persistence boundary", err)
		}
		record, err := NewSQLStore(f.db).Lookup(context.Background(), f.lookup())
		if err != nil || record.State != "PENDING" || len(record.Envelope) != 0 {
			t.Fatal("missing finish changed liability", err)
		}
	})
	t.Run("unbound-store", func(t *testing.T) {
		f := newAuthorityFixture(t)
		// This existing synthetic Store reports successful Claim/Finish but cannot
		// participate in SQLAuthority's transaction. It must NOT qualify issuance.
		f.deps.Store = f.store
		f.service = NewService(f.service.config, f.deps)
		result := f.issue()
		if result.Status != Unknown || len(result.Envelope) != 0 || result.AuthorizesExecution || f.store.finishes != 1 {
			t.Fatal("nontransactional Store manufactured successful issuance", result)
		}
		var count int
		if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authorization_issuances").Scan(&count) != nil || count != 0 {
			t.Fatal("unbound Store reached authoritative persistence")
		}
	})
}
