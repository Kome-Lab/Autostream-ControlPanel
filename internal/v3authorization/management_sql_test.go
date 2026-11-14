package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Every identity, session and bootstrap authorization in this file is synthetic.
// The bootstrap double is never a production trust origin or one-shot provider.
type managementBootstrapDouble struct {
	err        error
	hashLeaked atomic.Bool
}

func (b *managementBootstrapDouble) AuthorizeBootstrap(ctx context.Context, actor ManagementActor, _ ManagementMetadata, _ ManagementGrant) error {
	if actor.SessionHash != "" {
		b.hashLeaked.Store(true)
		return ErrDenied
	}
	if ctx == nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	return b.err
}

type managementFixture struct {
	*authorityFixture
	manager *ManagementService
	anchor  *managementBootstrapDouble
	actor   ManagementActor
	grant   ManagementGrant
	serial  int
}

func newManagementFixture(t *testing.T) *managementFixture {
	t.Helper()
	db := sqlTestDB(t)
	// Only the explicitly disposable CI schema is accepted by sqlTestDB.
	for _, table := range []string{"v3_cp_management_grant_history", "v3_cp_management_audit", "v3_cp_management_grants", "v3_cp_management_bootstrap", "v3_cp_management_schema", "sessions"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal("reset disposable management fixture", err)
		}
	}
	base := newAuthorityFixture(t)
	if _, err := base.db.Exec("CREATE TABLE sessions(id CHAR(64) PRIMARY KEY,user_id CHAR(36) NOT NULL,idle_expires_at DATETIME(6) NOT NULL,absolute_expires_at DATETIME(6) NOT NULL) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	actor := ManagementActor{Principal: base.principal, SessionHash: strings.Repeat("a", 64)}
	if _, err := base.db.Exec("INSERT INTO sessions VALUES(?,?,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)", actor.SessionHash, actor.Principal.ID); err != nil {
		t.Fatal(err)
	}
	if err := ApplyManagementMigrations(context.Background(), base.db); err != nil {
		t.Fatal("explicit empty management migration", err)
	}
	anchor := &managementBootstrapDouble{}
	return &managementFixture{
		authorityFixture: base,
		manager:          NewManagementService(base.authority, anchor),
		anchor:           anchor,
		actor:            actor,
		grant: ManagementGrant{Principal: actor.Principal.ID, Scope: base.intent.Scope, Action: Action, State: "ACTIVE",
			Operations: append([]ManagementOperation(nil), managementOperations()...), Subjects: []string{actor.Principal.ID}},
	}
}

func (f *managementFixture) generation(t *testing.T) uint64 {
	t.Helper()
	g, err := f.authority.Generation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func (f *managementFixture) metadata(t *testing.T) ManagementMetadata {
	t.Helper()
	f.serial++
	return ManagementMetadata{EventID: "management-event-" + strconv.Itoa(f.serial), CorrelationID: "management-correlation-" + strconv.Itoa(f.serial),
		ExpectedRevision: f.generation(t), Scope: f.intent.Scope, Action: Action}
}
func (f *managementFixture) bootstrap(t *testing.T) {
	t.Helper()
	before := f.generation(t)
	result := f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant)
	if result.Status != ManagementApplied || result.Generation != before+1 || !result.Initialized || f.anchor.hashLeaked.Load() {
		t.Fatal("synthetic bootstrap failed or leaked session hash", result)
	}
}
func (f *managementFixture) record(t *testing.T, kind AuthorityRecordKind) AuthorityRecord {
	t.Helper()
	for _, record := range f.records {
		if record.Kind == kind {
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var cloned AuthorityRecord
			if json.Unmarshal(raw, &cloned) != nil {
				t.Fatal("clone canonical registration")
			}
			return cloned
		}
	}
	t.Fatal("fixture record missing", kind)
	return AuthorityRecord{}
}
func (f *managementFixture) user(t *testing.T, id, hashByte string) ManagementActor {
	t.Helper()
	actor := ManagementActor{Principal: Principal{ID: id}, SessionHash: strings.Repeat(hashByte, 64)}
	if _, err := f.db.Exec("INSERT INTO users(id,status) VALUES(?,'active')", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("INSERT INTO sessions VALUES(?,?,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)", actor.SessionHash, id); err != nil {
		t.Fatal(err)
	}
	return actor
}
func managementCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	// Every caller supplies one fixed synthetic table name, never request input.
	var count int
	if db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count) != nil {
		t.Fatal("count synthetic fixture", table)
	}
	return count
}
func managementRefused(t *testing.T, result ManagementResult) {
	t.Helper()
	switch result.Status {
	case Denied, Invalid, Conflict, Unavailable, Unknown:
	default:
		t.Fatal("unqualified management became success", result)
	}
	if result.Code == "" {
		t.Fatal("refusal has no fixed reason", result)
	}
}
func managementApplied(t *testing.T, result ManagementResult, before uint64) {
	t.Helper()
	if result.Status != ManagementApplied || result.Generation != before+1 {
		t.Fatal("management mutation did not advance one authority generation", result, before)
	}
}

func TestSQLAuthorityManagementBootstrapAndUnknownState(t *testing.T) {
	t.Run("unknown-independent-approval-is-not-retried", func(t *testing.T) {
		f := newManagementFixture(t)
		f.anchor.err = ErrStoreUnknown
		before := f.generation(t)
		meta := f.metadata(t)
		result := f.manager.Bootstrap(context.Background(), f.actor, meta, f.grant)
		if result.Status != Unknown || !f.manager.uncertain.Load() {
			t.Fatal("unknown independent approval was made definite", result)
		}
		var observed int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_management_audit WHERE event_id=? AND operation='bootstrap' AND target_type='administrator' AND result='UNKNOWN' AND reason_code='v3_management_storage_unknown'", meta.EventID).Scan(&observed); err != nil || observed != 1 {
			t.Fatal("unknown approval attempt was not safely audited", err, observed)
		}
		var state string
		if err := f.db.QueryRow("SELECT state FROM v3_cp_management_bootstrap WHERE singleton=1").Scan(&state); err != nil || state != "UNINITIALIZED" {
			t.Fatal("unknown approval changed the bootstrap marker", err, state)
		}
		if f.generation(t) != before || managementCount(t, f.db, "v3_cp_management_grants") != 0 {
			t.Fatal("unknown independent approval registered authority")
		}
		f.anchor.err = nil
		if retry := f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant); retry.Status != Unknown {
			t.Fatal("resubmission cleared independent-approval uncertainty", retry)
		}
		if f.generation(t) != before || managementCount(t, f.db, "v3_cp_management_grants") != 0 {
			t.Fatal("uncertain independent approval automatically regranted")
		}
	})
	t.Run("unconfigured-root-is-not-v2-or-first-login", func(t *testing.T) {
		f := newManagementFixture(t)
		before := f.generation(t)
		m := NewManagementService(f.authority, nil)
		managementRefused(t, m.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
		managementRefused(t, m.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{f.record(t, PolicyRecord)}, nil))
		if f.generation(t) != before || managementCount(t, f.db, "v3_cp_management_grants") != 0 {
			t.Fatal("unconfigured trust origin seeded authority")
		}
	})
	t.Run("one-time-and-replay", func(t *testing.T) {
		f := newManagementFixture(t)
		meta := f.metadata(t)
		before := f.generation(t)
		result := f.manager.Bootstrap(context.Background(), f.actor, meta, f.grant)
		managementApplied(t, result, before)
		if !result.Initialized || f.anchor.hashLeaked.Load() {
			t.Fatal("bootstrap state or trust-port redaction", result)
		}
		managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, meta, f.grant))
		managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
		if f.generation(t) != before+1 || managementCount(t, f.db, "v3_cp_management_grants") != 1 {
			t.Fatal("bootstrap replay changed authority")
		}
	})
	t.Run("missing-state-is-not-uninitialized", func(t *testing.T) {
		f := newManagementFixture(t)
		before := f.generation(t)
		if _, err := f.db.Exec("DROP TABLE v3_cp_management_bootstrap"); err != nil {
			t.Fatal(err)
		}
		managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
		if f.generation(t) != before {
			t.Fatal("unknown bootstrap state was repaired")
		}
	})
	t.Run("root-refusal-does-not-register", func(t *testing.T) {
		f := newManagementFixture(t)
		f.anchor.err = ErrDenied
		before := f.generation(t)
		managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
		if f.generation(t) != before || managementCount(t, f.db, "v3_cp_management_grants") != 0 {
			t.Fatal("refused trust origin registered an administrator")
		}
	})
	for _, name := range []string{"no-principal", "no-session", "another-users-session"} {
		t.Run("bootstrap-authentication-"+name, func(t *testing.T) {
			f := newManagementFixture(t)
			actor := f.actor
			switch name {
			case "no-principal":
				actor = ManagementActor{}
			case "no-session":
				actor.SessionHash = ""
			case "another-users-session":
				actor.SessionHash = f.user(t, "another-session-owner", "b").SessionHash
			}
			before := f.generation(t)
			managementRefused(t, f.manager.Bootstrap(context.Background(), actor, f.metadata(t), f.grant))
			if f.generation(t) != before || managementCount(t, f.db, "v3_cp_management_grants") != 0 {
				t.Fatal("unauthenticated/mismatched bootstrap registered authority")
			}
		})
	}
	t.Run("bootstrap-does-not-accept-another-principal", func(t *testing.T) {
		f := newManagementFixture(t)
		grant := f.grant
		grant.Principal = f.user(t, "other-bootstrap-user", "b").Principal.ID
		managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), grant))
		if managementCount(t, f.db, "v3_cp_management_grants") != 0 {
			t.Fatal("bootstrap accepted a different principal")
		}
	})
}

func TestSQLAuthorityManagementAuthenticationAndScopedAuthorization(t *testing.T) {
	for _, name := range []string{"unauthenticated", "member", "v2-admin-only", "missing-session", "expired-session", "absolute-expired-session", "session-subject-mismatch", "disabled-principal", "other-realm", "other-resource", "wrong-action"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			f.bootstrap(t)
			actor, meta := f.actor, f.metadata(t)
			switch name {
			case "unauthenticated":
				actor = ManagementActor{}
			case "member", "v2-admin-only":
				actor = f.user(t, "ordinary-user", "b")
				if name == "v2-admin-only" {
					for _, q := range []string{
						"CREATE TABLE roles(id CHAR(36) PRIMARY KEY,name VARCHAR(64) NOT NULL) ENGINE=InnoDB",
						"CREATE TABLE user_roles(user_id CHAR(36),role_id CHAR(36),PRIMARY KEY(user_id,role_id)) ENGINE=InnoDB",
						"CREATE TABLE role_permissions(role_id CHAR(36),permission_key VARCHAR(128),PRIMARY KEY(role_id,permission_key)) ENGINE=InnoDB",
						"INSERT INTO roles VALUES('legacy-admin','admin')",
						"INSERT INTO user_roles VALUES('ordinary-user','legacy-admin')",
						"INSERT INTO role_permissions VALUES('legacy-admin','all')",
					} {
						if _, err := f.db.Exec(q); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "missing-session":
				actor.SessionHash = strings.Repeat("f", 64)
			case "expired-session":
				if _, err := f.db.Exec("UPDATE sessions SET idle_expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", actor.SessionHash); err != nil {
					t.Fatal(err)
				}
			case "absolute-expired-session":
				if _, err := f.db.Exec("UPDATE sessions SET absolute_expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", actor.SessionHash); err != nil {
					t.Fatal(err)
				}
			case "session-subject-mismatch":
				actor.SessionHash = f.user(t, "another-session-owner", "b").SessionHash
			case "disabled-principal":
				if _, err := f.db.Exec("UPDATE users SET status='disabled' WHERE id=?", actor.Principal.ID); err != nil {
					t.Fatal(err)
				}
			case "other-realm":
				meta.Scope.Realm = "another-realm"
			case "other-resource":
				meta.Scope.Resource = "another-resource"
			case "wrong-action":
				meta.Action = "other.action"
			}
			before := f.generation(t)
			result := f.manager.Apply(context.Background(), actor, meta, []AuthorityRecord{f.record(t, PolicyRecord)}, nil)
			managementRefused(t, result)
			if f.generation(t) != before {
				t.Fatal("refused actor changed authority")
			}
		})
	}
	t.Run("v3-read-is-explicit-and-scope-confined", func(t *testing.T) {
		f := newManagementFixture(t)
		f.bootstrap(t)
		other := AuthorityRecord{Kind: HostRecord, Host: &HostRegistration{Realm: f.intent.Scope.Realm, Host: "private-other-host", OwnerDomain: "domain-1", State: "ACTIVE"}}
		if _, err := f.authority.Save(context.Background(), f.generation(t), []AuthorityRecord{other}); err != nil {
			t.Fatal(err)
		}
		meta := f.metadata(t)
		meta.ExpectedRevision = 0 // Inspection must report current state, not CAS.
		result := f.manager.Inspect(context.Background(), f.actor, meta)
		if result.Status != ManagementInspected || !result.Initialized || result.Generation != f.generation(t) || len(result.Administrators) != 1 || result.Administrators[0].Principal != f.actor.Principal.ID {
			t.Fatal("scoped management read", result)
		}
		for _, r := range result.Records {
			if r.Kind == HostRecord && r.Host != nil && r.Host.Host != f.intent.Scope.Host {
				t.Fatal("inspection disclosed another Host")
			}
		}
	})
}

func TestSQLAuthorityManagementGrantDelegationAndRevocation(t *testing.T) {
	f := newManagementFixture(t)
	f.bootstrap(t)
	secondary := f.user(t, "secondary-manager", "b")
	limited := ManagementGrant{Principal: secondary.Principal.ID, Scope: f.intent.Scope, Action: Action, State: "ACTIVE",
		Operations: []ManagementOperation{ManagementRead, ManagementPolicy, ManagementCapability}, Subjects: []string{secondary.Principal.ID}}
	before := f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), nil, []ManagementGrant{limited}), before)
	read := f.manager.Inspect(context.Background(), secondary, f.metadata(t))
	if read.Status != ManagementInspected || len(read.Administrators) != 1 || read.Administrators[0].Principal != secondary.Principal.ID {
		t.Fatal("dedicated v3 grant was not read, or another grant leaked", read)
	}
	policy := f.record(t, PolicyRecord)
	policy.Rule.Revision = "policy-limited-manager"
	before = f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), secondary, f.metadata(t), []AuthorityRecord{policy}, nil), before)

	// Capability write is delegated, but changing another Principal is not.
	capability := f.record(t, CapabilityRecord)
	capability.Capability.Revision = "unauthorized-other-subject"
	before = f.generation(t)
	managementRefused(t, f.manager.Apply(context.Background(), secondary, f.metadata(t), []AuthorityRecord{capability}, nil))
	escalated := limited
	escalated.Operations = append([]ManagementOperation(nil), managementOperations()...)
	managementRefused(t, f.manager.Apply(context.Background(), secondary, f.metadata(t), nil, []ManagementGrant{escalated}))
	if f.generation(t) != before {
		t.Fatal("limited manager escalated or changed another user")
	}
	var deniedOperation, deniedType, deniedTarget string
	if err := f.db.QueryRow("SELECT operation,target_type,target_id FROM v3_cp_management_audit WHERE actor=? AND result='DENIED' AND target_type='capability' ORDER BY observed_at DESC LIMIT 1", secondary.Principal.ID).Scan(&deniedOperation, &deniedType, &deniedTarget); err != nil ||
		deniedOperation != string(ManagementCapability) || deniedType != "capability" || deniedTarget != capability.Capability.Principal+":"+capability.Capability.Scope.Resource {
		t.Fatal("refusal audit lost the validated operation/target", err, deniedOperation, deniedType, deniedTarget)
	}
	limited.State = "REVOKED"
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), nil, []ManagementGrant{limited}), before)
	before = f.generation(t)
	managementRefused(t, f.manager.Inspect(context.Background(), secondary, f.metadata(t)))
	managementRefused(t, f.manager.Apply(context.Background(), secondary, f.metadata(t), []AuthorityRecord{policy}, nil))
	if f.generation(t) != before {
		t.Fatal("cached management permission survived revocation")
	}
	uncertain := f.grant
	uncertain.State = "UNKNOWN"
	before = f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), nil, []ManagementGrant{uncertain}), before)
	before = f.generation(t)
	managementRefused(t, f.manager.Inspect(context.Background(), f.actor, f.metadata(t)))
	managementRefused(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{policy}, nil))
	managementRefused(t, f.manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
	if f.generation(t) != before {
		t.Fatal("UNKNOWN administrator state was promoted or reinitialized")
	}
}

func TestSQLAuthorityManagementRegistrationCASAndBatch(t *testing.T) {
	for _, kind := range []AuthorityRecordKind{RealmRecord, HostRecord, ResourceRecord, BackendRecord, CandidateRecord, CapabilityRecord, MembershipRecord, PolicyRecord, TrustRecord} {
		t.Run("management-revokes-"+string(kind), func(t *testing.T) {
			f := newManagementFixture(t)
			f.bootstrap(t)
			if decision, err := f.authority.Read(context.Background(), f.principal, f.intent); err != nil || decision.State != "ALLOWED" {
				t.Fatal("revocation baseline was not allowed", err, decision)
			}
			record := f.record(t, kind)
			switch kind {
			case RealmRecord:
				record.Realm.State = "INACTIVE"
			case HostRecord:
				record.Host.State = "INACTIVE"
			case ResourceRecord:
				record.Resource.State = "INACTIVE"
			case BackendRecord:
				record.Backend.State = "INACTIVE"
			case CandidateRecord:
				record.Candidate.Approval, record.Candidate.Approver = "REJECTED", ""
			case CapabilityRecord:
				record.Capability.State = "INACTIVE"
			case MembershipRecord:
				record.Membership.State = "REVOKED"
			case PolicyRecord, TrustRecord:
				record.Rule.Revocation = "REVOKED"
			}
			before, history := f.generation(t), managementCount(t, f.db, "v3_cp_authority_history")
			managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{record}, nil), before)
			if managementCount(t, f.db, "v3_cp_authority_history") != history+1 {
				t.Fatal("management revocation did not append exactly one owner record")
			}
			decision, err := f.authority.Read(context.Background(), f.principal, f.intent)
			requireRefusal(t, decision, err)
		})
	}
	t.Run("single-owned-connection", func(t *testing.T) {
		f := newManagementFixture(t)
		f.bootstrap(t)
		meta, before := f.metadata(t), f.generation(t)
		f.db.SetMaxOpenConns(1)
		defer f.db.SetMaxOpenConns(8)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		managementApplied(t, f.manager.Apply(ctx, f.actor, meta, []AuthorityRecord{f.record(t, PolicyRecord)}, nil), before)
		if ctx.Err() != nil {
			t.Fatal("management Save/audit borrowed another connection")
		}
	})
	f := newManagementFixture(t)
	f.bootstrap(t)
	var batch []AuthorityRecord
	for _, kind := range []AuthorityRecordKind{RealmRecord, HostRecord, ResourceRecord, BackendRecord, CandidateRecord, CapabilityRecord, MembershipRecord, PolicyRecord, TrustRecord} {
		r := f.record(t, kind)
		if r.Candidate != nil {
			r.Candidate.Approver = f.actor.Principal.ID
		}
		batch = append(batch, r)
	}
	before, history := f.generation(t), managementCount(t, f.db, "v3_cp_authority_history")
	stale := f.metadata(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), batch, nil), before)
	if managementCount(t, f.db, "v3_cp_authority_history") != history+len(batch) {
		t.Fatal("batch history was not complete")
	}
	managementRefused(t, f.manager.Apply(context.Background(), f.actor, stale, []AuthorityRecord{f.record(t, PolicyRecord)}, nil))
	decision, err := f.authority.Read(context.Background(), f.principal, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	original := f.record(t, PolicyRecord)
	for _, revision := range []string{original.Rule.Revision, "policy-aba-intermediate", original.Rule.Revision} {
		r := f.record(t, PolicyRecord)
		r.Rule.Revision = revision
		before = f.generation(t)
		managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{r}, nil), before)
	}
	calls := 0
	if err := f.authority.WithCurrent(context.Background(), f.principal, f.intent, decision, func(context.Context, Decision) error { calls++; return nil }); err == nil || calls != 0 {
		t.Fatal("same-value/ABA management changes reused an old Decision", err, calls)
	}

	for _, name := range []string{"cross-resource", "wrong-backend-host", "unowned-membership", "candidate-approver", "policy-action", "trust-scope"} {
		t.Run(name, func(t *testing.T) {
			var bad AuthorityRecord
			switch name {
			case "cross-resource":
				bad = f.record(t, ResourceRecord)
				bad.Resource.Scope.Resource = "another-resource"
			case "wrong-backend-host":
				bad = f.record(t, BackendRecord)
				bad.Backend.Host = "another-host"
			case "unowned-membership":
				bad = f.record(t, MembershipRecord)
				bad.Membership.Domain = "unowned-domain"
			case "candidate-approver":
				bad = f.record(t, CandidateRecord)
				bad.Candidate.Approver = "someone-else"
			case "policy-action":
				bad = f.record(t, PolicyRecord)
				bad.Rule.Action = "another.action"
			case "trust-scope":
				bad = f.record(t, TrustRecord)
				bad.Rule.Scope.Host = "another-host"
			}
			good := f.record(t, PolicyRecord)
			good.Rule.Revision = "must-not-partially-save"
			generation, count := f.generation(t), managementCount(t, f.db, "v3_cp_authority_history")
			managementRefused(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{good, bad}, nil))
			if f.generation(t) != generation || managementCount(t, f.db, "v3_cp_authority_history") != count {
				t.Fatal("invalid batch partly changed authority")
			}
		})
	}
	candidate := f.record(t, CandidateRecord)
	candidate.Candidate.Approval, candidate.Candidate.Approver = "PENDING", ""
	before = f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{candidate}, nil), before)
	d, err := f.authority.Read(context.Background(), f.principal, f.intent)
	requireRefusal(t, d, err)
	candidate.Candidate.Approval, candidate.Candidate.Approver = "APPROVED", f.actor.Principal.ID
	before = f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{candidate}, nil), before)
	if d, err = f.authority.Read(context.Background(), f.principal, f.intent); err != nil || d.State != "ALLOWED" {
		t.Fatal("approved canonical candidate failed the existing Authority", err)
	}
}

func TestSQLAuthorityManagementAuditAtomicityAndPreservation(t *testing.T) {
	f := newManagementFixture(t)
	f.bootstrap(t)
	policy := f.record(t, PolicyRecord)
	policy.Rule.Revision = "private-policy-revision-not-in-audit"
	meta := f.metadata(t)
	before := f.generation(t)
	managementApplied(t, f.manager.Apply(context.Background(), f.actor, meta, []AuthorityRecord{policy}, nil), before)
	var actor, qualification, result, reason string
	var previous, next uint64
	if err := f.db.QueryRow("SELECT actor,time_qualification,before_revision,after_revision,result,reason_code FROM v3_cp_management_audit WHERE event_id=? AND result='APPLIED'", meta.EventID).Scan(&actor, &qualification, &previous, &next, &result, &reason); err != nil {
		t.Fatal("missing atomic success audit", err)
	}
	if actor != f.actor.Principal.ID || qualification != "UNQUALIFIED_DB_OBSERVATION" || previous != before || next != before+1 || result != "APPLIED" {
		t.Fatal("audit asserted a qualified time or wrong mutation", actor, qualification, previous, next)
	}
	// A stable EventID is not automatic replay/permission.
	before = f.generation(t)
	managementRefused(t, f.manager.Apply(context.Background(), f.actor, meta, []AuthorityRecord{policy}, nil))
	if f.generation(t) != before {
		t.Fatal("duplicate EventID re-executed a change")
	}
	var denied int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_management_audit WHERE result='DENIED' AND reason_code<>''").Scan(&denied) != nil || denied == 0 {
		t.Fatal("refused management request has no fixed-code audit")
	}
	rows, err := f.db.Query("SELECT CONCAT_WS('|',actor,operation,target_type,target_id,realm,host,resource,backend,action,result,reason_code,correlation_id) FROM v3_cp_management_audit")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var text string
		if rows.Scan(&text) != nil || strings.Contains(text, policy.Rule.Revision) || strings.Contains(text, f.actor.SessionHash) {
			t.Fatal("audit recorded request payload/session material")
		}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()

	count := managementCount(t, f.db, "v3_cp_management_audit")
	for _, q := range []string{
		"UPDATE v3_cp_management_audit SET event_id=event_id",
		"DELETE FROM v3_cp_management_audit",
		"DELETE FROM v3_cp_management_grant_history",
		"DELETE FROM v3_cp_management_bootstrap",
	} {
		if _, err := f.db.Exec(q); err == nil {
			t.Fatal("append-only management history/marker was mutable", q)
		}
	}
	if managementCount(t, f.db, "v3_cp_management_audit") != count {
		t.Fatal("audit history changed")
	}
	if _, err := f.db.Exec("CREATE TRIGGER v3_management_test_audit_failure BEFORE INSERT ON v3_cp_management_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic_audit_failure'"); err != nil {
		t.Fatal(err)
	}
	defer f.db.Exec("DROP TRIGGER IF EXISTS v3_management_test_audit_failure")
	before, history := f.generation(t), managementCount(t, f.db, "v3_cp_authority_history")
	policy.Rule.Revision = "must-rollback-with-audit"
	trust := f.record(t, TrustRecord)
	trust.Rule.Revision = "trust-must-rollback-with-audit"
	managementRefused(t, f.manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{policy, trust}, nil))
	if f.generation(t) != before || managementCount(t, f.db, "v3_cp_authority_history") != history || managementCount(t, f.db, "v3_cp_management_audit") != count {
		t.Fatal("authority update escaped failed audit transaction")
	}
}

func TestSQLAuthorityManagementCommitAcknowledgementUnknown(t *testing.T) {
	for _, stage := range []string{"bootstrap", "apply"} {
		for _, mode := range []int32{1, 2} {
			t.Run(stage+"-"+strconv.Itoa(int(mode)), func(t *testing.T) {
				f := newManagementFixture(t)
				if stage == "apply" {
					f.bootstrap(t)
				}
				fault := &atomic.Int32{}
				db := sql.OpenDB(authorityFaultConnector{authorityConnector(t), fault})
				defer db.Close()
				manager := NewManagementService(NewSQLAuthority(db), f.anchor)
				meta, before := f.metadata(t), f.generation(t)
				policy := f.record(t, PolicyRecord)
				policy.Rule.Revision = "policy-uncertain-commit"
				fault.Store(mode)
				var result ManagementResult
				if stage == "bootstrap" {
					result = manager.Bootstrap(context.Background(), f.actor, meta, f.grant)
				} else {
					result = manager.Apply(context.Background(), f.actor, meta, []AuthorityRecord{policy}, nil)
				}
				if result.Status != Unknown {
					t.Fatal("lost commit acknowledgement became a definite result", result)
				}
				expected := before
				if mode == 1 {
					expected++
				}
				if f.generation(t) != expected {
					t.Fatal("connector failed to exercise the physical outcome")
				}
				if stage == "bootstrap" {
					want := 0
					if mode == 1 {
						want = 1
					}
					if managementCount(t, f.db, "v3_cp_management_grants") != want {
						t.Fatal("unknown bootstrap outcome was repaired")
					}
				}
				// Observe current physical state without clearing process-local poison.
				observed := manager.Inspect(context.Background(), f.actor, f.metadata(t))
				if stage == "apply" || mode == 1 {
					if observed.Status != ManagementInspected || observed.Generation != expected {
						t.Fatal("unknown-state inspection did not observe actual DB", observed)
					}
				} else {
					managementRefused(t, observed)
				}
				retry := manager.Apply(context.Background(), f.actor, f.metadata(t), []AuthorityRecord{policy}, nil)
				if retry.Status == ManagementApplied || f.generation(t) != expected {
					t.Fatal("inspection cleared uncertainty or auto-retried mutation", retry)
				}
				if stage == "bootstrap" {
					managementRefused(t, manager.Bootstrap(context.Background(), f.actor, f.metadata(t), f.grant))
					if f.generation(t) != expected {
						t.Fatal("uncertain bootstrap automatically regranted")
					}
				}
				// Durable reapproval across a new process is intentionally NOT tested
				// as a SQL guarantee: the independent real trust origin is unconnected.
			})
		}
	}
}

func TestSQLAuthorityManagementConcurrencyAndCancellation(t *testing.T) {
	t.Run("two-bootstrap-transactions", func(t *testing.T) {
		f := newManagementFixture(t)
		otherDB := sql.OpenDB(authorityConnector(t))
		defer otherDB.Close()
		other := NewManagementService(NewSQLAuthority(otherDB), &managementBootstrapDouble{})
		a, b := f.metadata(t), f.metadata(t)
		before := f.generation(t)
		start, done := make(chan struct{}), make(chan ManagementResult, 2)
		go func() { <-start; done <- f.manager.Bootstrap(context.Background(), f.actor, a, f.grant) }()
		go func() { <-start; done <- other.Bootstrap(context.Background(), f.actor, b, f.grant) }()
		close(start)
		success := 0
		for n := 0; n < 2; n++ {
			r := <-done
			if r.Status == ManagementApplied {
				success++
			} else {
				managementRefused(t, r)
			}
		}
		if success != 1 || f.generation(t) != before+1 || managementCount(t, f.db, "v3_cp_management_grants") != 1 {
			t.Fatal("concurrent bootstrap was not one-shot")
		}
	})
	t.Run("same-authority-cas", func(t *testing.T) {
		f := newManagementFixture(t)
		f.bootstrap(t)
		before := f.generation(t)
		a, b := f.metadata(t), f.metadata(t)
		first, second := f.record(t, PolicyRecord), f.record(t, PolicyRecord)
		first.Rule.Revision, second.Rule.Revision = "policy-race-a", "policy-race-b"
		start, done := make(chan struct{}), make(chan ManagementResult, 2)
		go func() {
			<-start
			done <- f.manager.Apply(context.Background(), f.actor, a, []AuthorityRecord{first}, nil)
		}()
		go func() {
			<-start
			done <- f.manager.Apply(context.Background(), f.actor, b, []AuthorityRecord{second}, nil)
		}()
		close(start)
		success, conflicts := 0, 0
		for n := 0; n < 2; n++ {
			r := <-done
			if r.Status == ManagementApplied {
				success++
			} else if r.Status == Conflict {
				conflicts++
			} else {
				t.Fatal("unexpected concurrent CAS outcome", r)
			}
		}
		if success != 1 || conflicts != 1 || f.generation(t) != before+1 {
			t.Fatal("same revision mutated twice", success, conflicts)
		}
	})
	t.Run("cancelled-before-mutation", func(t *testing.T) {
		f := newManagementFixture(t)
		f.bootstrap(t)
		meta, before := f.metadata(t), f.generation(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		managementRefused(t, f.manager.Apply(ctx, f.actor, meta, []AuthorityRecord{f.record(t, PolicyRecord)}, nil))
		if f.generation(t) != before {
			t.Fatal("cancellation committed a change")
		}
	})
	for _, name := range []string{"native-lock-timeout", "context-lock-timeout"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			f.bootstrap(t)
			meta, before := f.metadata(t), f.generation(t)
			blocker, err := f.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var locked uint64
			if blocker.QueryRow("SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&locked) != nil {
				t.Fatal("lock fixture")
			}
			config, err := mysql.ParseDSN(os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			if config.Params == nil {
				config.Params = map[string]string{}
			}
			config.Params["innodb_lock_wait_timeout"] = "1"
			connector, err := mysql.NewConnector(config)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			manager := NewManagementService(NewSQLAuthority(db), nil)
			budget := 5 * time.Second
			if name == "context-lock-timeout" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			started := time.Now()
			result := manager.Apply(ctx, f.actor, meta, []AuthorityRecord{f.record(t, PolicyRecord)}, nil)
			managementRefused(t, result)
			if name == "native-lock-timeout" && (time.Since(started) < 900*time.Millisecond || ctx.Err() != nil) {
				t.Fatal("native lock timeout was not exercised")
			}
			if name == "context-lock-timeout" && ctx.Err() == nil {
				t.Fatal("context did not cancel the lock wait")
			}
			if f.generation(t) != before {
				t.Fatal("lock failure advanced authority")
			}
		})
	}
	t.Run("grant-revocation-linearizes-after-active-management", managementRevocationSerializes)
}

// The connector only pauses before the real audit INSERT. It preserves every
// actual MariaDB result and exposes an in-transaction scheduling barrier.
type managementPauseConnector struct {
	base    driver.Connector
	once    *sync.Once
	entered chan struct{}
	release <-chan struct{}
}

func (c managementPauseConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &managementPauseConn{Conn: conn, boundary: c}, nil
}
func (c managementPauseConnector) Driver() driver.Driver { return c.base.Driver() }

type managementPauseConn struct {
	driver.Conn
	boundary managementPauseConnector
}

func (c *managementPauseConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *managementPauseConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(strings.ToLower(q), "insert into v3_cp_management_audit") {
		var wait bool
		c.boundary.once.Do(func() { wait = true; close(c.boundary.entered) })
		if wait {
			select {
			case <-c.boundary.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *managementPauseConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *managementPauseConn) CheckNamedValue(v *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func managementRevocationSerializes(t *testing.T) {
	f := newManagementFixture(t)
	f.bootstrap(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	db := sql.OpenDB(managementPauseConnector{base: authorityConnector(t), once: &sync.Once{}, entered: entered, release: release})
	defer db.Close()
	defer releaseOnce.Do(func() { close(release) })
	manager := NewManagementService(NewSQLAuthority(db), nil)
	meta, revokeMeta := f.metadata(t), f.metadata(t)
	policy := f.record(t, PolicyRecord)
	policy.Rule.Revision = "policy-before-revocation"
	done := make(chan ManagementResult, 1)
	go func() { done <- manager.Apply(ctx, f.actor, meta, []AuthorityRecord{policy}, nil) }()
	select {
	case <-entered:
	case r := <-done:
		t.Fatal("audit boundary not reached", r)
	case <-ctx.Done():
		t.Fatal("audit boundary timeout")
	}
	authorityAssertRowLocked(t, f.db, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1")
	grant := f.grant
	grant.State = "REVOKED"
	revoked := make(chan ManagementResult, 1)
	go func() { revoked <- f.manager.Apply(ctx, f.actor, revokeMeta, nil, []ManagementGrant{grant}) }()
	select {
	case r := <-revoked:
		t.Fatal("revocation crossed management transaction", r)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	managementApplied(t, <-done, meta.ExpectedRevision)
	r := <-revoked
	if r.Status != Conflict {
		t.Fatal("stale concurrent revocation bypassed CAS", r)
	}
	before := f.generation(t)
	managementApplied(t, f.manager.Apply(ctx, f.actor, f.metadata(t), nil, []ManagementGrant{grant}), before)
	before = f.generation(t)
	managementRefused(t, manager.Apply(ctx, f.actor, f.metadata(t), []AuthorityRecord{policy}, nil))
	if f.generation(t) != before {
		t.Fatal("retained service used revoked permission")
	}
}

func TestSQLAuthorityManagementWithCurrentSerialization(t *testing.T) {
	f := newManagementFixture(t)
	f.bootstrap(t)
	pending := authorityPending(t, f.authorityFixture)
	issued := sqlTestIssued(t, pending)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		done <- f.authority.WithCurrent(ctx, f.principal, f.intent, pending.Decision, func(txctx context.Context, _ Decision) error {
			if err := NewSQLStore(f.db).Finish(txctx, issued); err != nil {
				return err
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("issuance boundary not reached", err)
	case <-ctx.Done():
		t.Fatal("issuance boundary timeout")
	}
	authorityAssertRowLocked(t, f.db, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1")
	outside, err := NewSQLStore(f.db).Lookup(ctx, f.lookup())
	if err != nil || outside.State != "PENDING" {
		t.Fatal("issuance escaped its transaction", err)
	}
	policy := f.record(t, PolicyRecord)
	policy.Rule.Revision = "policy-after-issued"
	meta := f.metadata(t)
	changed := make(chan ManagementResult, 1)
	go func() { changed <- f.manager.Apply(ctx, f.actor, meta, []AuthorityRecord{policy}, nil) }()
	select {
	case r := <-changed:
		t.Fatal("management crossed WithCurrent callback", r)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal("original same-Tx issuance failed", err)
	}
	managementApplied(t, <-changed, meta.ExpectedRevision)
	stored, err := NewSQLStore(f.db).Lookup(ctx, f.lookup())
	if err != nil || stored.State != "ISSUED" || !bytes.Equal(stored.Envelope, issued.Envelope) {
		t.Fatal("management changed stored evidence bytes", err)
	}
	d, err := f.authority.Read(ctx, f.principal, f.intent)
	requireRefusal(t, d, err)
}
