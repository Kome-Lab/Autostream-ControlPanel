//go:build lifecycle_product_integration

package lifecyclefoundation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	ev "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	va "github.com/example/autostream-control-panel/internal/v3authorization"
)

var productTables = []string{
	"v3_cp_management_grant_history", "v3_cp_management_audit", "v3_cp_management_grants", "v3_cp_management_bootstrap", "v3_cp_management_schema",
	"v3_cp_authority_history", "v3_cp_authority_records", "v3_cp_authority_epoch", "v3_cp_authority_schema",
	"v3_cp_authorization_issuances", "v3_cp_authorization_schema", "sessions", "users", "user_admin_guard",
}

type productFixture struct {
	env      *lf.ProductTestEnvironment
	db       *sql.DB
	a        *va.SQLAuthority
	store    *va.SQLStore
	manager  *va.ManagementService
	service  *va.Service
	intent   va.Intent
	request  va.IssueRequest
	records  []va.AuthorityRecord
	actor    va.ManagementActor
	security *productSecurity
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func check(t *testing.T, yes bool, message string) {
	t.Helper()
	if !yes {
		t.Fatal(message)
	}
}

func productCheckpoint(ctx context.Context, q lf.Executor) (lf.Checkpoint, error) {
	cp := lf.Checkpoint{OperationalRevision: 1, IdentityRevision: 1}
	if err := q.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&cp.AuthorityEpoch); err != nil {
		return cp, err
	}
	var roots []string
	// This test-only coverage intentionally excludes identity/session data and
	// audit insert metadata. It qualifies neither B2 nor a real commitment profile.
	for _, query := range []string{
		"SELECT record_bytes FROM v3_cp_authority_records ORDER BY kind,realm,first_id,second_id",
		"SELECT record_bytes FROM v3_cp_authority_history ORDER BY generation,ordinal",
		"SELECT grant_bytes FROM v3_cp_management_grants ORDER BY principal,realm,host,resource,backend,action",
		"SELECT grant_bytes FROM v3_cp_management_grant_history ORDER BY generation,ordinal",
		"SELECT record_bytes FROM v3_cp_authorization_issuances ORDER BY realm,request_id",
	} {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return cp, err
		}
		for rows.Next() {
			var body string
			if err = rows.Scan(&body); err != nil {
				rows.Close()
				return cp, err
			}
			roots = append(roots, body)
			cp.Count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return cp, err
		}
	}
	cp.Content = lf.SyntheticReference(roots)
	cp.HistoryHead = lf.SyntheticReference(struct {
		Generation uint64
		Roots      []string
	}{cp.AuthorityEpoch, roots})
	return cp, nil
}

func newProductFixture(t *testing.T) *productFixture {
	t.Helper()
	env := lf.NewProductTestEnvironment(t)
	db := env.DB
	// The tagged required inventory runs before legacy product fixtures. Refuse
	// rather than overwrite even another test's pre-existing product namespace.
	for _, table := range productTables {
		var count int
		require(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&count))
		check(t, count == 0, "B1 requires an empty explicitly disposable product namespace: "+table)
	}
	t.Cleanup(func() {
		for _, table := range productTables {
			_, err := db.Exec("DROP TABLE IF EXISTS " + table)
			require(t, err)
		}
	})
	for _, ddl := range []string{
		"CREATE TABLE users(id CHAR(36) PRIMARY KEY,status VARCHAR(32) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE sessions(id CHAR(64) PRIMARY KEY,user_id CHAR(36),idle_expires_at DATETIME(6),absolute_expires_at DATETIME(6)) ENGINE=InnoDB",
		"CREATE TABLE user_admin_guard(singleton TINYINT PRIMARY KEY) ENGINE=InnoDB",
		"INSERT INTO user_admin_guard VALUES(1)",
		"INSERT INTO users VALUES('operator-1','active')",
	} {
		_, err := db.Exec(ddl)
		require(t, err)
	}
	require(t, va.ApplyMigrations(context.Background(), db))
	require(t, va.ApplyAuthorityMigrations(context.Background(), db))
	require(t, va.ApplyManagementMigrations(context.Background(), db))
	scope := ev.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"}
	command := ev.Command{Schema: ev.CanonicalRequestVersion, Operation: ev.AuthorizationAction, Scope: scope,
		Execution:           ev.Execution{RequestID: "request-1", ExecutionKey: "execution-1", OperationID: "operation-1", AttemptID: "attempt-1", StepID: "step-1"},
		RequestingPrincipal: "operator-1", AuthorizationIssuer: "cp-1", CandidateRevision: "candidate-1", CandidateContentPin: "content-1",
		Expected:      ev.ExpectedReference{Revision: "previous-1", StateVersion: 3},
		RequiredGuard: ev.RequiredGuard{Policy: "policy-1", Trust: "trust-1", Membership: "member-1", OwnershipEpoch: "owner-1", FenceEpoch: "fence-1", Generation: "generation-1", BootID: "boot-1", ProcessIncarnation: "process-1"},
		Timing:        ev.CommandTiming{TimeBase: "test-clock", DeadlineTick: 90}, OriginalBudget: "budget-1"}
	binding, err := ev.ComputeBinding(command)
	require(t, err)
	raw, err := ev.EncodeRequest(ev.Request{Command: command, Binding: binding})
	require(t, err)
	intent, err := va.NewCanonicalBindingResolver().Resolve(context.Background(), raw)
	require(t, err)
	records := []va.AuthorityRecord{
		{Kind: va.RealmRecord, Realm: &va.RealmRegistration{Realm: scope.Realm, State: "ACTIVE"}},
		{Kind: va.HostRecord, Host: &va.HostRegistration{Realm: scope.Realm, Host: scope.Host, OwnerDomain: "domain-1", State: "ACTIVE"}},
		{Kind: va.ResourceRecord, Resource: &va.ResourceRegistration{Scope: scope, ReferenceRevision: "previous-1", StateVersion: 3, State: "ACTIVE"}},
		{Kind: va.BackendRecord, Backend: &va.BackendRegistration{Realm: scope.Realm, Host: scope.Host, Backend: scope.Backend, State: "ACTIVE"}},
		{Kind: va.CandidateRecord, Candidate: &va.CandidateApproval{Scope: scope, Revision: "candidate-1", ContentPin: "content-1", Approval: "APPROVED", Approver: "test-approver", State: "ACTIVE"}},
		{Kind: va.CapabilityRecord, Capability: &va.V3Capability{Principal: "operator-1", Scope: scope, Action: va.Action, Revision: "capability-1", State: "ACTIVE", Issue: true, ReadHistory: true}},
		{Kind: va.MembershipRecord, Membership: &va.DomainMembership{Realm: scope.Realm, Principal: "operator-1", Domain: "domain-1", Revision: "member-1", State: "MEMBER"}},
		{Kind: va.PolicyRecord, Rule: &va.ScopeRule{Scope: scope, Action: va.Action, Revision: "policy-1", State: "ACTIVE", Revocation: "ACTIVE"}},
		{Kind: va.TrustRecord, Rule: &va.ScopeRule{Scope: scope, Action: va.Action, Revision: "trust-1", State: "ACTIVE", Revocation: "ACTIVE"}},
	}
	_, err = va.NewSQLAuthority(db).Save(context.Background(), 0, records)
	require(t, err)
	hash := strings.Repeat("a", 64)
	_, err = db.Exec("INSERT INTO sessions VALUES(?,?,UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)", hash, "operator-1")
	require(t, err)
	grant := va.ManagementGrant{Principal: "operator-1", Scope: scope, Action: va.Action, State: "ACTIVE",
		Operations: []va.ManagementOperation{va.ManagementAdministrator, va.ManagementHost, va.ManagementRead}, Subjects: []string{"operator-1"}}
	body, err := json.Marshal(grant)
	require(t, err)
	_, err = db.Exec("UPDATE v3_cp_management_bootstrap SET state='INITIALIZED',event_id='fixture-marker',principal='operator-1',generation=1 WHERE singleton=1")
	require(t, err)
	_, err = db.Exec("INSERT INTO v3_cp_management_grants(realm,host,resource,backend,action,principal,generation,grant_bytes) VALUES(?,?,?,?,?,?,1,?)", scope.Realm, scope.Host, scope.Resource, scope.Backend, va.Action, "operator-1", body)
	require(t, err)
	env.Configure(t, "operator-1", productCheckpoint)
	a := va.NewSQLAuthority(db).WithLifecycleGate(env.Gate)
	store := va.NewSQLStore(db).WithLifecycleGate(env.Gate)
	security := &productSecurity{db: db, issued: map[string][]byte{}}
	service := va.NewService(va.Config{Issuer: "cp-1", Audience: "executor"}, va.Dependencies{Resolver: va.NewCanonicalBindingResolver(), Authority: a, Store: store, Validity: security, Integrity: security, Applicability: security}).WithLifecycleGate(env.Gate)
	return &productFixture{env: env, db: db, a: a, store: store, manager: va.NewManagementService(a, nil), service: service, intent: intent,
		request: va.IssueRequest{OriginalRequest: raw, Frame: ev.BackendReadFrame{Issuer: "backend-1", ServingIncarnation: "serving-1", Generation: "generation-1", SnapshotID: "snapshot-1", ObservationProof: "synthetic-observation"}},
		records: records, actor: va.ManagementActor{Principal: va.Principal{ID: "operator-1"}, SessionHash: hash}, security: security}
}
func (f *productFixture) generation(t *testing.T) uint64 {
	var generation uint64
	require(t, f.db.QueryRow("SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&generation))
	return generation
}
func (f *productFixture) metadata(id string) va.ManagementMetadata {
	return va.ManagementMetadata{EventID: id, CorrelationID: id, ExpectedRevision: 1, Scope: f.intent.Scope, Action: va.Action}
}
func (f *productFixture) changedHost() va.AuthorityRecord {
	return va.AuthorityRecord{Kind: va.HostRecord, Host: &va.HostRegistration{Realm: "realm-1", Host: "host-1", OwnerDomain: "domain-2", State: "ACTIVE"}}
}
func (f *productFixture) attempts(t *testing.T, operation string) int {
	var count int
	require(t, f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_attempts WHERE deployment=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.Reservation.Request.Intent.Operation'))=?", f.env.Binding.Deployment, operation).Scan(&count))
	return count
}

type productSecurity struct {
	db          *sql.DB
	mu          sync.Mutex
	issued      map[string][]byte
	seals       int
	onSeal      func()
	failPrepare bool
}

func (s *productSecurity) Prepare(ctx context.Context, i va.Intent, _ va.Decision) (ev.Validity, error) {
	if s.failPrepare {
		return ev.Validity{}, va.ErrUnavailable
	}
	return ev.Validity{Status: "SPECIFIED", Issued: &ev.TimePoint{TimeBase: "test-clock", BootID: i.Required.BootID, Tick: 10}, Expiry: &ev.TimePoint{TimeBase: "test-clock", BootID: i.Required.BootID, Tick: 100}, QualificationProof: "test-only"}, ctx.Err()
}
func (s *productSecurity) Seal(ctx context.Context, e ev.Envelope) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seals++
	if s.onSeal != nil {
		s.onSeal()
	}
	e.Integrity = ev.Integrity{ProfileID: "synthetic-only", KeyID: "synthetic-key", KeyRevision: "synthetic-revision", Proof: "synthetic-proof"}
	body, err := ev.Encode(e)
	if err == nil {
		s.issued[e.EvidenceID] = append([]byte(nil), body...)
	}
	return body, err
}
func (s *productSecurity) Verify(ctx context.Context, e ev.Envelope, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !bytes.Equal(s.issued[e.EvidenceID], body) {
		return va.ErrUnavailable
	}
	return ctx.Err()
}
func (s *productSecurity) Inspect(ctx context.Context, _ ev.Envelope, _ []byte) (va.Applicability, error) {
	return va.Current, ctx.Err()
}
