package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	approval "github.com/example/autostream-control-panel/internal/bootstrapapproval"
)

// This origin exists only in the explicitly disposable CI namespace. Its pool
// is independent from every management pool and its consumption autocommits
// before CP starts; local rollback must never reverse that separate boundary.
type iuApprovalOrigin struct {
	db                     *sql.DB
	qualification          approval.Qualification
	verifyCalls            atomic.Int32
	inspectCalls           atomic.Int32
	mutate                 func(*approval.Verification)
	arrive                 chan<- struct{}
	release                <-chan struct{}
	allowDuplicateResponse bool
	lockCheck              sync.Mutex
}

func (v *iuApprovalOrigin) Qualification() approval.Qualification { return v.qualification }
func (v *iuApprovalOrigin) VerifyAndConsume(ctx context.Context, r approval.Request) (approval.Verification, error) {
	v.verifyCalls.Add(1)
	// NOWAIT is a barrier assertion, not a scheduling sleep: external work must
	// be able to acquire C epoch and human User before CP owns either SQL lock.
	v.lockCheck.Lock()
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		v.lockCheck.Unlock()
		return approval.Verification{}, approval.ErrUnknown
	}
	var generation uint64
	var actor string
	err = tx.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&generation)
	if err == nil {
		err = tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE NOWAIT", r.Principal).Scan(&actor)
	}
	tx.Rollback()
	v.lockCheck.Unlock()
	if err != nil {
		return approval.Verification{}, approval.ErrUnknown
	}
	if v.arrive != nil {
		select {
		case v.arrive <- struct{}{}:
		case <-ctx.Done():
			return approval.Verification{}, approval.ErrUnknown
		}
	}
	if v.release != nil {
		select {
		case <-v.release:
		case <-ctx.Done():
			return approval.Verification{}, approval.ErrUnknown
		}
	}
	_, err = v.db.ExecContext(ctx, "INSERT INTO iu_ws1_test_origin(manifest_id,attempt_id,request_binding) VALUES(?,?,?)", r.Reference.ManifestID, r.Reference.AttemptID, approval.Binding(r))
	if err != nil {
		if !v.allowDuplicateResponse {
			return approval.Verification{}, approval.ErrUnknown
		}
		var manifest, binding string
		if v.db.QueryRowContext(ctx, "SELECT manifest_id,request_binding FROM iu_ws1_test_origin WHERE attempt_id=?", r.Reference.AttemptID).Scan(&manifest, &binding) != nil || manifest != r.Reference.ManifestID || binding != approval.Binding(r) {
			return approval.Verification{}, approval.ErrUnknown
		}
	}
	out := iuVerification(r)
	if v.mutate != nil {
		v.mutate(&out)
	}
	return out, nil
}
func (v *iuApprovalOrigin) Inspect(ctx context.Context, r approval.Request) (approval.Verification, error) {
	v.inspectCalls.Add(1)
	var manifest, binding string
	err := v.db.QueryRowContext(ctx, "SELECT manifest_id,request_binding FROM iu_ws1_test_origin WHERE attempt_id=? OR manifest_id=?", r.Reference.AttemptID, r.Reference.ManifestID).Scan(&manifest, &binding)
	if err == sql.ErrNoRows {
		out := iuVerification(r)
		out.Consumed = false
		out.ReceiptID = "fixture-known-unused-observation"
		return out, nil
	}
	if err != nil || manifest != r.Reference.ManifestID || binding != approval.Binding(r) {
		return approval.Verification{}, approval.ErrUnknown
	}
	return iuVerification(r), nil
}
func iuVerification(r approval.Request) approval.Verification {
	return approval.Verification{Request: r, Ceiling: approval.Ceiling{Scope: r.Scope, Subject: r.Principal, Operations: append([]string(nil), r.Operations...), Subjects: append([]string(nil), r.Subjects...), ProvisionTargets: r.ProvisionTargets}, OriginID: "fixture-independent-origin", ReceiptID: "fixture-receipt-" + r.Reference.ManifestID, OriginRevision: 1, Knowledge: lifecycle.Known, Consumed: true}
}
func iuApprovalLimits() lifecycle.Limits {
	return lifecycle.Limits{MaxIDBytes: 128, MaxMembers: 64, MaxAxes: 32, MaxRefs: 128, MaxObligations: 64, MaxPartitions: 64, MaxRanges: 64, MaxDepth: 8}
}

type iuApprovalFixture struct {
	*managementFixture
	origin   *iuApprovalOrigin
	core     *approval.Core
	approved *ApprovalManagementService
	meta     ManagementMetadata
	ref      approval.Reference
}

func newIUApprovalFixture(t *testing.T) *iuApprovalFixture {
	t.Helper()
	f := newManagementFixture(t)
	for _, table := range []string{"v3_cp_approval_history", "v3_cp_approval_consumptions", "v3_cp_approval_checkpoint", "v3_cp_approval_schema", "iu_ws1_test_origin"} {
		if _, err := f.db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal("reset explicitly disposable approval fixture", err)
		}
	}
	if err := approval.ApplyFixtureMigrations(context.Background(), f.db, true); err != nil {
		t.Fatal("explicit fixture-only migration", err)
	}
	if _, err := f.db.Exec("CREATE TABLE iu_ws1_test_origin(manifest_id VARBINARY(128) PRIMARY KEY,attempt_id VARBINARY(128) NOT NULL UNIQUE,request_binding VARBINARY(64) NOT NULL) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	originDB := sql.OpenDB(authorityConnector(t))
	t.Cleanup(func() { originDB.Close() })
	origin := &iuApprovalOrigin{db: originDB, qualification: approval.Qualification{ProfileID: approval.FixtureProfile, FixtureOnly: true}}
	core, err := approval.NewFixtureCore(context.Background(), f.db, origin, iuApprovalLimits())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewApprovalManagementService(f.authority, core)
	if err != nil {
		t.Fatal(err)
	}
	meta := f.metadata(t)
	ref := approval.Reference{ManifestID: "fixture-manifest-1", AttemptID: meta.EventID, Identity: lifecycle.IdentityContext{Class: lifecycle.Human, PrincipalKind: lifecycle.PrincipalHuman, ActorID: lifecycle.ID(f.actor.Principal.ID), Purpose: lifecycle.PurposeApproval, FlowRef: "fixture-bootstrap-flow"}}
	return &iuApprovalFixture{managementFixture: f, origin: origin, core: core, approved: manager, meta: meta, ref: ref}
}
func iuLocalCounts(t *testing.T, db *sql.DB) []int {
	t.Helper()
	result := []int{}
	for _, table := range []string{"v3_cp_approval_consumptions", "v3_cp_approval_history", "v3_cp_management_grants", "v3_cp_management_grant_history"} {
		result = append(result, managementCount(t, db, table))
	}
	return result
}
func iuAssertEmpty(t *testing.T, f *iuApprovalFixture) {
	t.Helper()
	for _, n := range iuLocalCounts(t, f.db) {
		if n != 0 {
			t.Fatal("refusal/rollback partially changed management", iuLocalCounts(t, f.db))
		}
	}
	var revision uint64
	if f.db.QueryRow("SELECT revision FROM v3_cp_approval_checkpoint WHERE singleton=1").Scan(&revision) != nil || revision != 0 {
		t.Fatal("rollback advanced checkpoint", revision)
	}
	var state string
	if f.db.QueryRow("SELECT state FROM v3_cp_management_bootstrap WHERE singleton=1").Scan(&state) != nil || state != "UNINITIALIZED" {
		t.Fatal("rollback initialized marker", state)
	}
	if f.generation(t) != f.meta.ExpectedRevision {
		t.Fatal("refusal advanced C generation")
	}
}

// Test-only transport calls actual product Core/ManagementSQL over a real HTTP
// listener. No product HTTP/bootstrap/startup surface is added by this test.
func iuApprovalServer(t *testing.T, service *ApprovalManagementService, f *iuApprovalFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ref approval.Reference
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&ref) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		result := service.Bootstrap(r.Context(), f.actor, f.meta, f.grant, ref)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(server.Close)
	return server
}
func iuHTTPBootstrap(t *testing.T, url string, ref approval.Reference) ManagementResult {
	t.Helper()
	b, _ := json.Marshal(ref)
	response, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result ManagementResult
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("real HTTP bootstrap fixture response", response.StatusCode)
	}
	return result
}

func TestIUWS1ApprovalSameTransactionHTTP(t *testing.T) {
	f := newIUApprovalFixture(t)
	server := iuApprovalServer(t, f.approved, f)
	result := iuHTTPBootstrap(t, server.URL, f.ref)
	managementApplied(t, result, f.meta.ExpectedRevision)
	for _, n := range iuLocalCounts(t, f.db) {
		if n != 1 {
			t.Fatal("atomic consumption/grant/history not all committed", iuLocalCounts(t, f.db))
		}
	}
	var revision, generation uint64
	if f.db.QueryRow("SELECT revision,generation FROM v3_cp_approval_checkpoint WHERE singleton=1").Scan(&revision, &generation) != nil || revision != 1 || generation != result.Generation {
		t.Fatal("checkpoint did not follow actual CP commit", revision, generation)
	}
	if f.core.ProductionQualified() || f.origin.verifyCalls.Load() != 1 {
		t.Fatal("fixture qualified production or verifier not called once")
	}
	beforeAudit := managementCount(t, f.db, "v3_cp_management_audit")
	inspection, err := f.approved.Inspect(context.Background(), f.actor, f.meta, f.grant, f.ref)
	if err != nil || inspection.OriginKnowledge != lifecycle.Known || !inspection.OriginConsumed || inspection.CPKnowledge != lifecycle.Known || !inspection.CPPresent || inspection.Generation != result.Generation || inspection.Gate != lifecycle.Held || inspection.AuthorizesMutation() || inspection.ProductionQualified() {
		t.Fatal("same-attempt observation confused authority/boundaries", inspection, err)
	}
	if beforeAudit != managementCount(t, f.db, "v3_cp_management_audit") || f.origin.verifyCalls.Load() != 1 || f.origin.inspectCalls.Load() != 2 {
		t.Fatal("Inspect wrote audit or reconsumed approval")
	}
	managementRefused(t, iuHTTPBootstrap(t, server.URL, f.ref))
	if managementCount(t, f.db, "v3_cp_approval_consumptions") != 1 || f.generation(t) != result.Generation {
		t.Fatal("manifest replay changed state")
	}
	t.Log("SC-APP-01: real HTTP; independent origin; C epoch/User lock-free verifier; same CP transaction consumption/ceiling/grant/marker/history/checkpoint/critical audit")
}

func TestIUWS1ApprovalBindingAndCeilingRejections(t *testing.T) {
	cases := []struct {
		name         string
		ref          func(*approval.Reference)
		verification func(*approval.Verification)
	}{
		{name: "wrong-class", ref: func(r *approval.Reference) {
			r.Identity.Class = lifecycle.System
			r.Identity.PrincipalKind = lifecycle.PrincipalSystem
		}},
		{name: "wrong-purpose", ref: func(r *approval.Reference) { r.Identity.Purpose = lifecycle.PurposeRecovery }},
		{name: "wrong-actor", ref: func(r *approval.Reference) { r.Identity.ActorID = "other-human" }},
		{name: "wrong-attempt", ref: func(r *approval.Reference) { r.AttemptID = "another-attempt" }},
		{name: "unknown-origin", verification: func(v *approval.Verification) { v.Knowledge = lifecycle.KnowledgeUnknown }},
		{name: "wrong-subject", verification: func(v *approval.Verification) { v.Ceiling.Subject = "other-human" }},
		{name: "wrong-scope", verification: func(v *approval.Verification) { v.Ceiling.Scope.Resource = "different-resource" }},
		{name: "ceiling-no-administrator", verification: func(v *approval.Verification) { v.Ceiling.Operations = []string{string(ManagementRead)} }},
		{name: "ceiling-no-subject", verification: func(v *approval.Verification) { v.Ceiling.Subjects = []string{"other-human"} }},
		{name: "immutable-manifest-mismatch", verification: func(v *approval.Verification) { v.Request.Reference.ManifestID = "changed-manifest" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newIUApprovalFixture(t)
			ref := f.ref
			if test.ref != nil {
				test.ref(&ref)
			}
			f.origin.mutate = test.verification
			managementRefused(t, f.approved.Bootstrap(context.Background(), f.actor, f.meta, f.grant, ref))
			iuAssertEmpty(t, f)
		})
	}
	t.Run("nil-unqualified-verifier-and-limits", func(t *testing.T) {
		f := newIUApprovalFixture(t)
		if _, err := approval.NewFixtureCore(context.Background(), f.db, nil, iuApprovalLimits()); err == nil {
			t.Fatal("nil verifier configured")
		}
		f.origin.qualification.FixtureOnly = false
		if _, err := approval.NewFixtureCore(context.Background(), f.db, f.origin, iuApprovalLimits()); err == nil {
			t.Fatal("unqualified verifier configured")
		}
		managementRefused(t, f.approved.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref))
		f.origin.qualification.FixtureOnly = true
		if _, err := approval.NewFixtureCore(context.Background(), f.db, f.origin, lifecycle.Limits{}); err == nil {
			t.Fatal("missing limits defaulted")
		}
		if err := approval.ApplyFixtureMigrations(context.Background(), f.db, false); err == nil {
			t.Fatal("migration opted in implicitly")
		}
		iuAssertEmpty(t, f)
	})
	t.Log("SC-APP-02: class/purpose/actor/attempt/subject/scope/ceiling/immutable binding/unknown/nil verifier/missing profile limits all preserve local state")
}

func TestIUWS1ApprovalTwoServersDoubleConsumption(t *testing.T) {
	f := newIUApprovalFixture(t)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	f.origin.arrive = arrived
	f.origin.release = release
	f.origin.allowDuplicateResponse = true
	otherDB := sql.OpenDB(authorityConnector(t))
	defer otherDB.Close()
	otherCore, err := approval.NewFixtureCore(context.Background(), otherDB, f.origin, iuApprovalLimits())
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewApprovalManagementService(NewSQLAuthority(otherDB), otherCore)
	if err != nil {
		t.Fatal(err)
	}
	server1, server2 := iuApprovalServer(t, f.approved, f), iuApprovalServer(t, other, f)
	results := make(chan ManagementResult, 2)
	var group sync.WaitGroup
	for _, url := range []string{server1.URL, server2.URL} {
		group.Add(1)
		go func(u string) {
			defer group.Done()
			body, _ := json.Marshal(f.ref)
			response, err := http.Post(u, "application/json", bytes.NewReader(body))
			if err != nil {
				results <- ManagementResult{Status: Unavailable, Code: "synthetic_http_transport_failed"}
				return
			}
			defer response.Body.Close()
			var result ManagementResult
			if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
				results <- ManagementResult{Status: Unavailable, Code: "synthetic_http_response_failed"}
				return
			}
			results <- result
		}(url)
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("two independent servers did not reach external pretransaction barrier")
		}
	}
	close(release)
	released = true
	group.Wait()
	close(results)
	applied := 0
	for result := range results {
		if result.Status == ManagementApplied {
			applied++
		} else {
			if result.Status != Conflict {
				t.Fatal("second independently prepared view did not reach local conflict", result)
			}
		}
	}
	if applied != 1 || f.generation(t) != f.meta.ExpectedRevision+1 {
		t.Fatal("double consumption did not serialize once", applied)
	}
	for _, n := range iuLocalCounts(t, f.db) {
		if n != 1 {
			t.Fatal("duplicate receipt split atomic outcome")
		}
	}
	if f.origin.verifyCalls.Load() != 2 {
		t.Fatal("fixture did not exercise both independently prepared views")
	}
	t.Log("SC-APP-03: two real HTTP servers/two independent ManagementSQL stores/two CP pools; barrier before Tx; duplicate external receipt has one local commit")
}

func TestIUWS1ApprovalRollbackAndCommitUnknown(t *testing.T) {
	t.Run("critical-audit-rollback", func(t *testing.T) {
		f := newIUApprovalFixture(t)
		if _, err := f.db.Exec("CREATE TRIGGER iu_ws1_fail_critical_audit BEFORE INSERT ON v3_cp_management_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic critical audit failure'"); err != nil {
			t.Fatal(err)
		}
		defer f.db.Exec("DROP TRIGGER IF EXISTS iu_ws1_fail_critical_audit")
		managementRefused(t, f.approved.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref))
		iuAssertEmpty(t, f)
		if managementCount(t, f.origin.db, "iu_ws1_test_origin") != 1 {
			t.Fatal("CP rollback unconsumed external approval")
		}
	})
	for _, mode := range []int32{1, 2} {
		name := "commit-ack-lost"
		if mode == 2 {
			name = "rollback-ack-lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newIUApprovalFixture(t)
			fault := &atomic.Int32{}
			db := sql.OpenDB(authorityFaultConnector{authorityConnector(t), fault})
			defer db.Close()
			core, err := approval.NewFixtureCore(context.Background(), db, f.origin, iuApprovalLimits())
			if err != nil {
				t.Fatal(err)
			}
			manager, err := NewApprovalManagementService(NewSQLAuthority(db), core)
			if err != nil {
				t.Fatal(err)
			}
			fault.Store(mode)
			result := manager.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref)
			if result.Status != Unknown {
				t.Fatal("lost Commit ACK became definite", result)
			}
			want := 0
			if mode == 1 {
				want = 1
			}
			for _, n := range iuLocalCounts(t, f.db) {
				if n != want {
					t.Fatal("unknown response did not preserve physical atomic outcome", iuLocalCounts(t, f.db), want)
				}
			}
			if want == 0 {
				iuAssertEmpty(t, f)
			}
			calls := f.origin.verifyCalls.Load()
			managementRefused(t, manager.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref))
			if calls != f.origin.verifyCalls.Load() {
				t.Fatal("uncertain owner automatically reissued/reconsumed")
			}
			// A new service and DB pool model a restarted owner. Durable external
			// consumption remains consumed; only pure original-attempt Inspect runs.
			restartDB := sql.OpenDB(authorityConnector(t))
			defer restartDB.Close()
			restartCore, err := approval.NewFixtureCore(context.Background(), restartDB, f.origin, iuApprovalLimits())
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := NewApprovalManagementService(NewSQLAuthority(restartDB), restartCore)
			if err != nil {
				t.Fatal(err)
			}
			audits := managementCount(t, f.db, "v3_cp_management_audit")
			inspection, err := restarted.Inspect(context.Background(), f.actor, f.meta, f.grant, f.ref)
			if err != nil || inspection.OriginKnowledge != lifecycle.Known || !inspection.OriginConsumed || inspection.CPKnowledge != lifecycle.Known || inspection.CPPresent != (mode == 1) || inspection.Gate != lifecycle.Held {
				t.Fatal("restart confused origin consumption with CP outcome", inspection, err)
			}
			if calls != f.origin.verifyCalls.Load() || audits != managementCount(t, f.db, "v3_cp_management_audit") {
				t.Fatal("Inspect mutated or released uncertain attempt")
			}
			managementRefused(t, restarted.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref))
			if managementCount(t, f.origin.db, "iu_ws1_test_origin") != 1 || f.origin.verifyCalls.Load() != calls {
				t.Fatal("restart issued a new external consumption")
			}
		})
	}
	t.Run("partial-restore-preserves-unknown", func(t *testing.T) {
		f := newIUApprovalFixture(t)
		managementApplied(t, f.approved.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref), f.meta.ExpectedRevision)
		// This deliberate loss occurs solely inside the explicitly disposable
		// fixture. TRUNCATE preserves the schema/triggers while losing one table.
		if _, err := f.db.Exec("TRUNCATE TABLE v3_cp_approval_consumptions"); err != nil {
			t.Fatal(err)
		}
		inspection, err := f.approved.Inspect(context.Background(), f.actor, f.meta, f.grant, f.ref)
		if err == nil || inspection.CPKnowledge != lifecycle.KnowledgeUnknown || inspection.CPPresent || inspection.Gate != lifecycle.Held || f.origin.verifyCalls.Load() != 1 {
			t.Fatal("partial restore converted missing local row to known no-effect", inspection, err)
		}
	})
	t.Log("SC-APP-04: critical audit rollback; physically committed/rolled-back ACK loss remain UNKNOWN; restart same-attempt pure Inspect; partial restore UNKNOWN; HELD retained")
}

func TestIUWS1ApprovalNoSecretTransport(t *testing.T) {
	f := newIUApprovalFixture(t)
	r := approvalRequest(f.actor, f.meta, f.grant, f.ref)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(f.actor.SessionHash)) || strings.Contains(string(b), "SessionHash") {
		t.Fatal("session material crossed independent verifier DTO boundary")
	}
	managementApplied(t, f.approved.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref), f.meta.ExpectedRevision)
	var ceiling []byte
	if f.db.QueryRow("SELECT ceiling_bytes FROM v3_cp_approval_consumptions").Scan(&ceiling) != nil || bytes.Contains(ceiling, []byte(f.actor.SessionHash)) {
		t.Fatal("session material entered local approval history")
	}
}
