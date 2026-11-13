package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	"github.com/go-sql-driver/mysql"
)

func sqlTestDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN")
	if raw == "" {
		t.Skip("disposable MariaDB integration service not configured")
	}
	config, err := mysql.ParseDSN(raw)
	if err != nil || config.DBName != "autostream_v3_auth_ci" {
		t.Fatal("integration tests require the explicitly disposable autostream_v3_auth_ci schema")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal("open synthetic database")
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(8)
	if db.PingContext(context.Background()) != nil {
		t.Fatal("synthetic database unavailable")
	}
	// These tests never enumerate, modify or migrate any existing CP/v2 table.
	for _, table := range []string{"v3_cp_authorization_issuances", "v3_cp_authorization_schema"} {
		if _, err = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal("reset synthetic v3 fixture")
		}
	}
	return db
}
func sqlTestRecord() Record {
	r := Record{Principal: "user-1", Issuer: "cp-1", Audience: "executor-1", ClaimID: "claim-1", State: "PENDING", Frame: evidence.BackendReadFrame{Issuer: "backend-1", ServingIncarnation: "process-1", Generation: "generation-1", SnapshotID: "snapshot-1", ObservationProof: "observation-1"}}
	r.Intent = Intent{Scope: evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"}, Execution: evidence.Execution{RequestID: "request-1", ExecutionKey: "execution-1", OperationID: "operation-1", AttemptID: "attempt-1", StepID: "step-1"}, Binding: "synthetic-binding", Action: Action, RequestingPrincipal: "user-1", AuthorizationIssuer: "cp-1"}
	r.Decision = Decision{Principal: "user-1", Scope: r.Intent.Scope, Action: Action, PolicyRevision: "policy-1", TrustRevision: "trust-1", AuthorityRevision: "authority-1", TargetRevision: "target-1", State: "ALLOWED"}
	r.Immutable = []byte("immutable synthetic fixture")
	return r
}
func sqlTestIssued(t *testing.T, r Record) Record {
	t.Helper()
	r.State = "ISSUED"
	r.EvidenceID = "evidence-1"
	env := evidence.Envelope{ProtocolVersion: 3, EvidenceID: r.EvidenceID, Kind: evidence.CPAuthorizationKind, Issuer: r.Issuer, Audience: r.Audience, Scope: r.Intent.Scope, Execution: r.Intent.Execution, Binding: r.Intent.Binding, Frame: r.Frame, Validity: evidence.Validity{Status: "SPECIFIED", Issued: &evidence.TimePoint{TimeBase: "synthetic-clock", BootID: "boot-1", Tick: 1}, Expiry: &evidence.TimePoint{TimeBase: "synthetic-clock", BootID: "boot-1", Tick: 2}, QualificationProof: "clock-1"}, Integrity: evidence.Integrity{ProfileID: "synthetic-profile", KeyID: "synthetic-key", KeyRevision: "revision-1", Proof: "synthetic-proof"}, CPAuthorization: &evidence.CPAuthorizationPayload{Action: Action, Decision: "AUTHORIZED", PolicyRevision: r.Decision.PolicyRevision, TrustRevision: r.Decision.TrustRevision}}
	var err error
	r.Envelope, err = evidence.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSQLStoreExplicitMigrationAndSchemaFailures(t *testing.T) {
	db := sqlTestDB(t)
	ctx := context.Background()
	s := NewSQLStore(db)
	if _, err := s.Lookup(ctx, Lookup{Scope: sqlTestRecord().Intent.Scope, Execution: sqlTestRecord().Intent.Execution}); !errors.Is(err, errSchemaUnavailable) {
		t.Fatal("normal read must not migrate")
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	for _, c := range []struct {
		name      string
		statement string
		args      []any
	}{
		{"version", `UPDATE v3_cp_authorization_schema SET version=2`, nil},
		{"checksum", `UPDATE v3_cp_authorization_schema SET checksum='not-original'`, nil},
		{"dirty", `UPDATE v3_cp_authorization_schema SET dirty=1`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, c.statement, c.args...); err != nil {
				t.Fatal("synthetic corruption failed")
			}
			if s.schema(ctx) == nil || ApplyMigrations(ctx, db) == nil {
				t.Fatal("schema uncertainty accepted")
			}
			if _, err := db.ExecContext(ctx, `UPDATE v3_cp_authorization_schema SET version=1,checksum=?,dirty=0`, issuanceChecksum()); err != nil {
				t.Fatal("reset schema fixture")
			}
		})
	}
}
func TestSQLStoreClaimFinishExactBytesAndReadOnlyLookup(t *testing.T) {
	db := sqlTestDB(t)
	ctx := context.Background()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(db)
	r := sqlTestRecord()
	got, state, err := s.Claim(ctx, r)
	if err != nil || state != Claimed || got.ClaimID != r.ClaimID {
		t.Fatal(state, err)
	}
	duplicate := r
	duplicate.ClaimID = "claim-2"
	got, state, err = s.Claim(ctx, duplicate)
	if err != nil || state != Existing || got.ClaimID != r.ClaimID {
		t.Fatal("duplicate claim replaced owner", state, err)
	}
	r = sqlTestIssued(t, r)
	if err = s.Finish(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, r); err != nil {
		t.Fatal("same durable result must be idempotent", err)
	}
	var before []byte
	if db.QueryRowContext(ctx, `SELECT record_bytes FROM v3_cp_authorization_issuances`).Scan(&before) != nil {
		t.Fatal("fixture record")
	}
	for n := 0; n < 3; n++ {
		got, err = s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution, EvidenceID: r.EvidenceID})
		if err != nil || !bytes.Equal(got.Envelope, r.Envelope) {
			t.Fatal("lookup changed original wire bytes", err)
		}
	}
	var after []byte
	if db.QueryRowContext(ctx, `SELECT record_bytes FROM v3_cp_authorization_issuances`).Scan(&after) != nil || !bytes.Equal(before, after) {
		t.Fatal("lookup changed durable record")
	}
	r.State = "CLOSED"
	r.EvidenceID = ""
	r.Envelope = nil
	if err = s.Finish(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal issued record overwritten", err)
	}
}
func TestSQLStoreConcurrentClaimsAndIdentifierCollisions(t *testing.T) {
	db := sqlTestDB(t)
	ctx := context.Background()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(db)
	r := sqlTestRecord()
	const n = 8
	results := make(chan ClaimStatus, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, state, err := s.Claim(ctx, r); results <- state; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for state := range results {
		if state == Claimed {
			count++
		} else if state != Existing {
			t.Fatal("unexpected duplicate state", state)
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent claim failure", err)
		}
	}
	if count != 1 {
		t.Fatal("generator owner not unique", count)
	}
	variants := []Record{r, r, r}
	variants[0].Immutable = []byte("different contents")
	variants[1].Intent.Execution.RequestID = "request-2"
	variants[1].Immutable = []byte("same execution alias")
	variants[2].Intent.Execution.ExecutionKey = "execution-2"
	variants[2].Immutable = []byte("same request different execution")
	for _, v := range variants {
		_, state, err := s.Claim(ctx, v)
		if err != nil || state != Collided {
			t.Fatal("identifier collision accepted", state, err)
		}
	}
	var rows int
	if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM v3_cp_authorization_issuances`).Scan(&rows) != nil || rows != 1 {
		t.Fatal("collision created extra liability")
	}
}
func TestSQLStoreClosedLiabilityAndUnknownPersistence(t *testing.T) {
	db := sqlTestDB(t)
	ctx := context.Background()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(db)
	r := sqlTestRecord()
	if _, _, err := s.Claim(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.State = "CLOSED"
	r.Code = "v3_authorization_denied"
	if err := s.Finish(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution})
	if err != nil || got.State != "CLOSED" || len(got.Envelope) != 0 {
		t.Fatal("HTTP denial became signed Evidence", err)
	}
	missing := r.Intent.Execution
	missing.RequestID = "request-missing"
	if _, err = s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: missing}); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing row classification", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = s.Claim(canceled, sqlTestRecord()); err == nil {
		t.Fatal("canceled operation admitted")
	}
	if err = db.Close(); err != nil {
		t.Fatal("close synthetic pool")
	}
	if _, err = s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution}); err == nil || strings.Contains(err.Error(), "sql:") {
		t.Fatal("closed pool exposed driver error", err)
	}
}
func TestSQLStoreMalformedRowsAndScopeMismatch(t *testing.T) {
	db := sqlTestDB(t)
	ctx := context.Background()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(db)
	r := sqlTestRecord()
	if _, _, err := s.Claim(ctx, r); err != nil {
		t.Fatal(err)
	}
	wrong := Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution}
	wrong.Scope.Resource = "other-resource"
	if _, err := s.Lookup(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("scope mismatch accepted", err)
	}
	original, _ := json.Marshal(r)
	for _, body := range [][]byte{[]byte(`{"State":"PENDING","State":"ISSUED"}`), []byte(`{"unknown":"value"}`), []byte(`null`), []byte(`not json`)} {
		if _, err := db.ExecContext(ctx, `UPDATE v3_cp_authorization_issuances SET record_bytes=?`, body); err != nil {
			t.Fatal("corrupt synthetic row")
		}
		if _, err := s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution}); !errors.Is(err, ErrConflict) {
			t.Fatal("malformed row accepted", err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE v3_cp_authorization_issuances SET record_bytes=?,execution_key='different-key'`, original); err != nil {
		t.Fatal("cross-column corruption")
	}
	if _, err := s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution}); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-column binding mismatch accepted", err)
	}
}
func TestSQLStoreMigrationIsNotRuntimeAndNilFailsClosed(t *testing.T) {
	s := NewSQLStore(nil)
	if _, _, err := s.Claim(context.Background(), sqlTestRecord()); err == nil {
		t.Fatal("nil store admitted")
	}
	if err := ApplyMigrations(context.Background(), nil); err == nil {
		t.Fatal("nil administrative DB accepted")
	}
	if !sqlDuplicateJSON([]byte(`{"a":{"b":1,"b":2}}`)) || !sqlDuplicateJSON([]byte(`{"a":1,"\u0061":2}`)) || sqlDuplicateJSON([]byte(`{"a":[1,{"b":2}]}`)) {
		t.Fatal("stored JSON duplicate checker")
	}
}

// This is the actual product Service -> real MariaDB Store -> read Service
// path. Only authority, binding, integrity, validity and applicability are doubles.
func TestSQLStoreServiceVerticalSlice(t *testing.T) {
	db := sqlTestDB(t)
	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	f := makeTestFixture(t)
	f.deps.Store = NewSQLStore(db)
	f.service = NewService(f.service.config, f.deps)
	issued := f.issue()
	if issued.Status != Issued {
		t.Fatal(issued)
	}
	replay := f.issue()
	if replay.Status != Replay || !bytes.Equal(replay.Envelope, issued.Envelope) {
		t.Fatal(replay)
	}
	for n := 0; n < 3; n++ {
		read := f.service.Read(context.Background(), f.principal, f.lookup())
		if read.Status != Replay || !bytes.Equal(read.Envelope, issued.Envelope) {
			t.Fatal(read)
		}
	}
	if f.security.seals != 1 {
		t.Fatal("durable replay regenerated evidence")
	}
	var rows, final int
	if db.QueryRow(`SELECT COUNT(*),SUM(state='ISSUED') FROM v3_cp_authorization_issuances`).Scan(&rows, &final) != nil || rows != 1 || final != 1 {
		t.Fatal("durable liability not terminal")
	}
}
