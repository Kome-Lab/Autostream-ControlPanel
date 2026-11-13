package v3authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// Every dependency in this file is synthetic. These are not deployable keys,
// qualified clocks, v3 capability stores, public Binding profiles or Continuity.
type syntheticResolver struct {
	Intent   Intent
	Err      error
	Expected json.RawMessage
}

func (r syntheticResolver) Resolve(ctx context.Context, raw json.RawMessage) (Intent, error) {
	if ctx.Err() != nil {
		return Intent{}, ctx.Err()
	}
	if r.Err != nil {
		return Intent{}, r.Err
	}
	if r.Expected != nil && !bytes.Equal(r.Expected, raw) {
		return Intent{}, ErrConflict
	}
	return r.Intent, nil
}

type syntheticAuthority struct {
	mu        sync.Mutex
	decision  Decision
	denyRead  bool
	readErr   error
	readCount int
	change    bool
	guardErr  error
	onGuard   func()
	observed  Intent
}

func (a *syntheticAuthority) Read(ctx context.Context, p Principal, i Intent) (Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observed = i
	return a.decision, ctx.Err()
}
func (a *syntheticAuthority) WithCurrent(ctx context.Context, p Principal, i Intent, expected Decision, fn func(Decision) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.guardErr != nil {
		return a.guardErr
	}
	if a.change {
		a.decision.TrustRevision = "trust-changed"
	}
	if a.onGuard != nil {
		a.onGuard()
	}
	return fn(a.decision)
}
func (a *syntheticAuthority) AuthorizeRead(ctx context.Context, p Principal, l Lookup) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.readCount++
	if a.readErr != nil {
		return a.readErr
	}
	if a.denyRead || p.ID != a.decision.Principal || l.Scope != a.decision.Scope {
		return ErrDenied
	}
	return ctx.Err()
}

type syntheticSecurity struct {
	mu                                         sync.Mutex
	issued                                     map[string][]byte
	seals, verifies, inspects                  int
	prepareErr, sealErr, verifyErr, inspectErr error
	validityUnknown, mutate, lateDeny          bool
	current                                    Applicability
	onSeal                                     func()
	boot                                       string
}

func (s *syntheticSecurity) Prepare(ctx context.Context, i Intent, d Decision) (evidence.Validity, error) {
	if s.prepareErr != nil {
		return evidence.Validity{}, s.prepareErr
	}
	if s.validityUnknown {
		return evidence.Validity{Status: "UNKNOWN"}, nil
	}
	return evidence.Validity{Status: "SPECIFIED", Issued: &evidence.TimePoint{TimeBase: "synthetic-contract-clock", BootID: i.Required.BootID, Tick: 10}, Expiry: &evidence.TimePoint{TimeBase: "synthetic-contract-clock", BootID: i.Required.BootID, Tick: 100}, QualificationProof: "synthetic-clock-proof"}, ctx.Err()
}
func (s *syntheticSecurity) Seal(ctx context.Context, e evidence.Envelope) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seals++
	if s.onSeal != nil {
		s.onSeal()
	}
	if s.sealErr != nil {
		return nil, s.sealErr
	}
	if s.mutate {
		e.Scope.Host = "tampered-host"
	}
	e.Integrity = evidence.Integrity{ProfileID: "synthetic-only", KeyID: "synthetic-key", KeyRevision: "synthetic-revision", Proof: "synthetic-authenticated-bytes"}
	raw, err := evidence.Encode(e)
	if err == nil {
		s.issued[e.EvidenceID] = append([]byte(nil), raw...)
	}
	return raw, err
}
func (s *syntheticSecurity) Verify(ctx context.Context, e evidence.Envelope, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifies++
	if s.verifyErr != nil {
		return s.verifyErr
	}
	if !bytes.Equal(s.issued[e.EvidenceID], raw) {
		return ErrUnavailable
	}
	decoded, err := evidence.Decode(raw)
	if err != nil || !reflect.DeepEqual(decoded, e) {
		return ErrUnavailable
	}
	return ctx.Err()
}
func (s *syntheticSecurity) Inspect(ctx context.Context, e evidence.Envelope, raw []byte) (Applicability, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inspects++
	return s.current, s.inspectErr
}

type syntheticStore struct {
	mu                             sync.Mutex
	records                        map[string]Record
	claims, finishes, lookups      int
	claimErr, finishErr, lookupErr error
	loseAck                        bool
}

func key(r Record) string { return r.Intent.Scope.Realm + ":" + r.Intent.Execution.RequestID }
func (s *syntheticStore) Claim(ctx context.Context, r Record) (Record, ClaimStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.claimErr != nil {
		return Record{}, "", s.claimErr
	}
	for _, old := range s.records {
		if old.Intent.Scope.Realm == r.Intent.Scope.Realm && (old.Intent.Execution.RequestID == r.Intent.Execution.RequestID || old.Intent.Execution.ExecutionKey == r.Intent.Execution.ExecutionKey || (old.Intent.Execution.OperationID == r.Intent.Execution.OperationID && old.Intent.Execution.AttemptID == r.Intent.Execution.AttemptID && old.Intent.Execution.StepID == r.Intent.Execution.StepID)) {
			if !bytes.Equal(old.Immutable, r.Immutable) {
				return cloneRecord(old), Collided, nil
			}
			return cloneRecord(old), Existing, nil
		}
	}
	s.records[key(r)] = cloneRecord(r)
	return cloneRecord(r), Claimed, nil
}
func (s *syntheticStore) Finish(ctx context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes++
	if s.finishErr != nil {
		return s.finishErr
	}
	old, ok := s.records[key(r)]
	if !ok || old.State != "PENDING" || old.ClaimID != r.ClaimID || !bytes.Equal(old.Immutable, r.Immutable) {
		return ErrConflict
	}
	s.records[key(r)] = cloneRecord(r)
	if s.loseAck {
		s.loseAck = false
		return ErrStoreUnknown
	}
	return ctx.Err()
}
func (s *syntheticStore) Lookup(ctx context.Context, l Lookup) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++
	if s.lookupErr != nil {
		return Record{}, s.lookupErr
	}
	r, ok := s.records[l.Scope.Realm+":"+l.Execution.RequestID]
	if !ok {
		return Record{}, ErrNotFound
	}
	return cloneRecord(r), ctx.Err()
}

type serviceFixture struct {
	service   *Service
	deps      Dependencies
	authority *syntheticAuthority
	security  *syntheticSecurity
	store     *syntheticStore
	intent    Intent
	request   IssueRequest
	principal Principal
}

func makeTestFixture(t *testing.T) *serviceFixture {
	t.Helper()
	i := Intent{Scope: evidence.Scope{Realm: "realm", Host: "host", Resource: "resource", Backend: "backend"}, Execution: evidence.Execution{RequestID: "request", ExecutionKey: "execution", OperationID: "operation", AttemptID: "attempt", StepID: "step"}, Binding: "synthetic-fixture-binding-not-a-production-profile", Action: Action, RequestingPrincipal: "operator", AuthorizationIssuer: "cp", CandidateRevision: "candidate", CandidateContentPin: "content", ExpectedRevision: "previous", ExpectedStateVersion: 3, Required: GuardRequirement{Policy: "policy", Trust: "trust", Membership: "membership", OwnershipEpoch: "owner", FenceEpoch: "fence", Generation: "generation", BootID: "boot", ProcessIncarnation: "process"}, TimeBase: "synthetic-contract-clock", DeadlineTick: 90, OriginalBudget: "budget"}
	f := &serviceFixture{authority: &syntheticAuthority{}, security: &syntheticSecurity{issued: map[string][]byte{}, current: Current}, store: &syntheticStore{records: map[string]Record{}}, intent: i, request: IssueRequest{OriginalRequest: json.RawMessage(`{"synthetic":true}`), Frame: evidence.BackendReadFrame{Issuer: "backend", ServingIncarnation: "incarnation", Generation: "generation", SnapshotID: "snapshot", ObservationProof: "observation"}}}
	configureIntent(f, i)
	return f
}
func configureIntent(f *serviceFixture, i Intent) {
	f.intent = i
	f.principal = Principal{i.RequestingPrincipal}
	f.authority.decision = Decision{Principal: i.RequestingPrincipal, Scope: i.Scope, Action: i.Action, PolicyRevision: i.Required.Policy, TrustRevision: i.Required.Trust, AuthorityRevision: "cp-authority-1", TargetRevision: "target-1", State: "ALLOWED"}
	f.deps = Dependencies{Resolver: syntheticResolver{Intent: i, Expected: append([]byte(nil), f.request.OriginalRequest...)}, Authority: f.authority, Validity: f.security, Integrity: f.security, Applicability: f.security, Store: f.store}
	f.service = NewService(Config{Issuer: i.AuthorizationIssuer, Audience: "executor"}, f.deps)
}
func (f *serviceFixture) issue() Result {
	return f.service.Issue(context.Background(), f.principal, f.request)
}
func (f *serviceFixture) lookup() Lookup {
	return Lookup{Scope: f.intent.Scope, Execution: f.intent.Execution}
}

func TestIssueSaveReadExactBytes(t *testing.T) {
	f := makeTestFixture(t)
	r := f.issue()
	if r.Status != Issued {
		t.Fatal(r)
	}
	if r.AuthorizesExecution {
		t.Fatal("issue authorizes mutation")
	}
	env, e := evidence.Decode(r.Envelope)
	if e != nil || env.CPAuthorization == nil || env.CPAuthorization.Decision != "AUTHORIZED" {
		t.Fatal("not canonical CP authorization")
	}
	if !reflect.DeepEqual(f.authority.observed, f.intent) {
		t.Fatal("business authorization lost original semantic content")
	}
	for n := 0; n < 3; n++ {
		got := f.service.Read(context.Background(), f.principal, f.lookup())
		if got.Status != Replay || !bytes.Equal(got.Envelope, r.Envelope) || got.AuthorizesExecution {
			t.Fatal(got)
		}
	}
	again := f.issue()
	if again.Status != Replay || !bytes.Equal(again.Envelope, r.Envelope) || f.security.seals != 1 || f.store.finishes != 1 {
		t.Fatal("replay regenerated or wrote")
	}
	r.Envelope[0] = 'x'
	if f.service.Read(context.Background(), f.principal, f.lookup()).Status != Replay {
		t.Fatal("caller mutated stored bytes")
	}
}
func TestConcurrentIssuanceAndCollisions(t *testing.T) {
	f := makeTestFixture(t)
	var wg sync.WaitGroup
	results := make(chan Result, 24)
	for n := 0; n < 24; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- f.issue() }()
	}
	wg.Wait()
	close(results)
	issued := 0
	for r := range results {
		if r.Status == Issued {
			issued++
		} else if r.Status != Replay && r.Status != Pending {
			t.Fatal(r)
		}
	}
	if issued != 1 || f.security.seals != 1 || f.store.finishes != 1 {
		t.Fatal("additional issuance")
	}
	for _, mutate := range []func(*Intent){func(i *Intent) { i.CandidateRevision = "different" }, func(i *Intent) { i.Execution.RequestID = "alias" }, func(i *Intent) { i.Execution.ExecutionKey = "other" }} {
		i := f.intent
		mutate(&i)
		f.deps.Resolver = syntheticResolver{Intent: i}
		svc := NewService(f.service.config, f.deps)
		if r := svc.Issue(context.Background(), f.principal, f.request); r.Status != Conflict {
			t.Fatal(r)
		}
	}
	if f.security.seals != 1 {
		t.Fatal("collision generated evidence")
	}
}
func TestIssueRejectsSemanticAndAuthorityMismatch(t *testing.T) {
	cases := map[string]func(*serviceFixture){
		"principal": func(f *serviceFixture) { f.principal.ID = "attacker" },
		"action": func(f *serviceFixture) {
			i := f.intent
			i.Action = "shell"
			f.deps.Resolver = syntheticResolver{Intent: i}
		},
		"issuer": func(f *serviceFixture) {
			i := f.intent
			i.AuthorizationIssuer = "another-cp"
			f.deps.Resolver = syntheticResolver{Intent: i}
		},
		"binding":            func(f *serviceFixture) { f.deps.Resolver = syntheticResolver{Err: ErrConflict} },
		"scope":              func(f *serviceFixture) { f.authority.decision.Scope.Host = "another" },
		"policy":             func(f *serviceFixture) { f.authority.decision.PolicyRevision = "other" },
		"trust":              func(f *serviceFixture) { f.authority.decision.TrustRevision = "other" },
		"policy-missing":     func(f *serviceFixture) { f.authority.decision.PolicyRevision = "" },
		"target-missing":     func(f *serviceFixture) { f.authority.decision.TargetRevision = "" },
		"permission-v2-only": func(f *serviceFixture) { f.authority.decision.State = "DENIED" },
		"revoked":            func(f *serviceFixture) { f.authority.decision.State = "REVOKED" },
		"deadline-zero": func(f *serviceFixture) {
			i := f.intent
			i.DeadlineTick = 0
			f.deps.Resolver = syntheticResolver{Intent: i}
		},
		"state-overflow": func(f *serviceFixture) {
			i := f.intent
			i.ExpectedStateVersion = ^uint64(0)
			f.deps.Resolver = syntheticResolver{Intent: i}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			f := makeTestFixture(t)
			fn(f)
			f.service = NewService(f.service.config, f.deps)
			r := f.issue()
			if r.Status == Issued || r.Status == Replay || f.security.seals != 0 || f.store.claims != 0 {
				t.Fatal(r)
			}
		})
	}
}
func TestMissingDependenciesFailClosed(t *testing.T) {
	for _, name := range []string{"resolver", "authority", "validity", "integrity", "applicability", "store", "nil-service"} {
		t.Run(name, func(t *testing.T) {
			f := makeTestFixture(t)
			switch name {
			case "resolver":
				f.deps.Resolver = nil
			case "authority":
				f.deps.Authority = nil
			case "validity":
				f.deps.Validity = nil
			case "integrity":
				f.deps.Integrity = nil
			case "applicability":
				f.deps.Applicability = nil
			case "store":
				f.deps.Store = nil
			}
			f.service = NewService(f.service.config, f.deps)
			if name == "nil-service" {
				f.service = nil
			}
			if r := f.issue(); r.Status != Unavailable {
				t.Fatal(r)
			}
			if f.store.claims != 0 {
				t.Fatal("unconfigured wrote")
			}
		})
	}
}
func TestRevisionRaceAndEvidenceGenerationFailure(t *testing.T) {
	for _, name := range []string{"policy-trust-race", "target-race", "unknown-time", "clock-error", "seal-error", "wire-modified", "revocation-unknown", "expired", "revoked", "integrity-verification", "dependency-raw-error"} {
		t.Run(name, func(t *testing.T) {
			f := makeTestFixture(t)
			switch name {
			case "policy-trust-race":
				f.authority.change = true
			case "target-race":
				f.security.onSeal = func() {
					f.authority.mu.Lock()
					f.authority.decision.TargetRevision = "new-target"
					f.authority.mu.Unlock()
				}
			case "unknown-time":
				f.security.validityUnknown = true
			case "clock-error":
				f.security.prepareErr = errors.New("private path /secret/db")
			case "seal-error":
				f.security.sealErr = ErrUnavailable
			case "wire-modified":
				f.security.mutate = true
			case "revocation-unknown":
				f.security.current = ApplicabilityUnknown
			case "expired":
				f.security.current = Expired
			case "revoked":
				f.security.current = Revoked
			case "integrity-verification":
				f.security.verifyErr = ErrUnavailable
			case "dependency-raw-error":
				f.authority.guardErr = errors.New("SELECT password FROM private")
			}
			r := f.issue()
			if r.Status == Issued || len(r.Envelope) != 0 || bytes.Contains([]byte(r.Code), []byte("private")) {
				t.Fatal(r)
			}
			for _, rec := range f.store.records {
				if rec.State == "ISSUED" || len(rec.Envelope) != 0 {
					t.Fatal("unsafe saved evidence")
				}
			}
		})
	}
}
func TestSaveUnknownReconcilesSameLiability(t *testing.T) {
	f := makeTestFixture(t)
	f.store.loseAck = true
	if r := f.issue(); r.Status != Unknown {
		t.Fatal(r)
	}
	read := f.service.Read(context.Background(), f.principal, f.lookup())
	if read.Status != Replay {
		t.Fatal(read)
	}
	again := f.issue()
	if again.Status != Replay || !bytes.Equal(read.Envelope, again.Envelope) || f.security.seals != 1 {
		t.Fatal("ACK loss regenerated")
	}
	f = makeTestFixture(t)
	f.store.finishErr = ErrStoreUnknown
	if r := f.issue(); r.Status != Unknown {
		t.Fatal(r)
	}
	f.store.finishErr = nil
	if r := f.issue(); r.Status != Pending {
		t.Fatal(r)
	}
	if f.security.seals != 1 {
		t.Fatal("pending was taken over")
	}
}
func TestReadCurrentPermissionHistoryTamperAndNoWrites(t *testing.T) {
	f := makeTestFixture(t)
	r := f.issue()
	if r.Status != Issued {
		t.Fatal(r)
	}
	writes := f.store.finishes
	seals := f.security.seals
	for _, status := range []Applicability{Expired, Revoked, ApplicabilityUnknown} {
		f.security.current = status
		read := f.service.Read(context.Background(), f.principal, f.lookup())
		if read.Status != Replay || read.Applicability != status || !bytes.Equal(read.Envelope, r.Envelope) || read.AuthorizesExecution {
			t.Fatal(read)
		}
	}
	if f.store.finishes != writes || f.security.seals != seals {
		t.Fatal("read wrote or renewed")
	}
	f.authority.denyRead = true
	if got := f.service.Read(context.Background(), f.principal, f.lookup()); got.Status != Denied || len(got.Envelope) != 0 {
		t.Fatal(got)
	}
	f.authority.denyRead = false
	l := f.lookup()
	l.Scope.Host = "other"
	if got := f.service.Read(context.Background(), f.principal, l); got.Status != Denied {
		t.Fatal(got)
	}
	l = f.lookup()
	l.Execution.StepID = "other"
	if got := f.service.Read(context.Background(), f.principal, l); got.Status != Conflict {
		t.Fatal(got)
	}
	for key, r := range f.store.records {
		r.Envelope = bytes.Replace(r.Envelope, []byte(`"AUTHORIZED"`), []byte(`"DENIED"`), 1)
		f.store.records[key] = r
	}
	if got := f.service.Read(context.Background(), f.principal, f.lookup()); got.Status != Conflict {
		t.Fatal(got)
	}
}
func TestCancellationMissingAndDependencyErrors(t *testing.T) {
	f := makeTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := f.service.Issue(ctx, f.principal, f.request); r.Status != Unavailable || f.store.claims != 0 {
		t.Fatal(r)
	}
	if r := f.service.Read(context.Background(), f.principal, f.lookup()); r.Status != Unknown {
		t.Fatal("absence became non-issuance", r)
	}
	f.store.claimErr = fmt.Errorf("private dependency failure at /private/db")
	r := f.issue()
	if r.Status != Unknown || r.Code != ErrStoreUnknown.Code {
		t.Fatal(r)
	}
}

func TestStrictHTTPDecode(t *testing.T) {
	f := makeTestFixture(t)
	raw, _ := json.Marshal(f.request)
	if _, e := DecodeIssueRequest(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{append(raw, []byte(` {}`)...), bytes.Replace(raw, []byte(`"original_request"`), []byte(`"Original_Request"`), 1), bytes.Replace(raw, []byte(`"original_request":`), []byte(`"original_request":{},"original_request":`), 1), bytes.Replace(raw, []byte(`"snapshot_id"`), []byte(`"unknown"`), 1), []byte(`{"original_request":null,"backend_read_frame":{}}`), []byte(`{"original_request":{"x":1,"x":2},"backend_read_frame":{}}`)} {
		if _, e := DecodeIssueRequest(bad); e == nil {
			t.Fatal("accepted ambiguous carrier")
		}
	}
	l, _ := json.Marshal(f.lookup())
	if _, e := DecodeLookup(l); e != nil {
		t.Fatal(e)
	}
	if _, e := DecodeLookup(bytes.Replace(l, []byte(`"scope"`), []byte(`"Scope"`), 1)); e == nil {
		t.Fatal("accepted case alias")
	}
}

func TestCancellationDuringGenerationDoesNotSaveEvidence(t *testing.T) {
	f := makeTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.security.onSeal = cancel
	result := f.service.Issue(ctx, f.principal, f.request)
	if result.Status == Issued || len(result.Envelope) != 0 {
		t.Fatal("canceled generation issued", result)
	}
	for _, r := range f.store.records {
		if r.State == "ISSUED" || len(r.Envelope) != 0 {
			t.Fatal("canceled operation persisted evidence")
		}
	}
	if f.service.Issue(context.Background(), f.principal, f.request).Status == Issued || f.security.seals != 1 {
		t.Fatal("canceled liability silently retried")
	}
}
func TestStoredIssuerAndDecisionCannotDriftFromBinding(t *testing.T) {
	for _, name := range []string{"issuer", "decision-principal", "decision-policy", "decision-target"} {
		t.Run(name, func(t *testing.T) {
			f := makeTestFixture(t)
			if f.issue().Status != Issued {
				t.Fatal("fixture")
			}
			for k, r := range f.store.records {
				switch name {
				case "issuer":
					r.Issuer = "another-issuer"
				case "decision-principal":
					r.Decision.Principal = "another"
				case "decision-policy":
					r.Decision.PolicyRevision = "another"
				case "decision-target":
					r.Decision.Scope.Host = "another"
				}
				r.Immutable, _ = immutable(r)
				f.store.records[k] = r
			}
			result := f.service.Read(context.Background(), f.principal, f.lookup())
			if result.Status != Conflict || len(result.Envelope) != 0 {
				t.Fatal("incoherent historical record returned", result)
			}
		})
	}
}

func TestStoredWireBytesTamperRejected(t *testing.T) {
	f := makeTestFixture(t)
	if f.issue().Status != Issued {
		t.Fatal("fixture")
	}
	writes := f.store.finishes
	for k, r := range f.store.records {
		r.Envelope = append(r.Envelope, ' ')
		f.store.records[k] = r
	}
	// Same decoded payload, different exact bytes: syntax/semantic checks alone
	// would accept this. The actual configured Integrity hook must reject it.
	result := f.service.Read(context.Background(), f.principal, f.lookup())
	if result.Status != Unavailable || len(result.Envelope) != 0 || f.store.finishes != writes {
		t.Fatal("altered authenticated bytes were returned or repaired", result)
	}
}
