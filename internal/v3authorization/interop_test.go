package v3authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// The outer fixture is exported by the actual pinned C10 checkout. Its Request
// is decoded ONLY by Contracts and the CP product CanonicalBindingResolver.
type cpInteropInput struct {
	Request   json.RawMessage           `json:"request"`
	Scope     evidence.Scope            `json:"scope"`
	Execution evidence.Execution        `json:"execution"`
	Frame     evidence.BackendReadFrame `json:"backend_read_frame"`
	Issuer    string                    `json:"issuer"`
	Audience  string                    `json:"audience"`
}

type cpInteropAuthority struct {
	intent    Intent
	principal Principal
	decision  Decision
}

func (a cpInteropAuthority) Read(ctx context.Context, principal Principal, intent Intent) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if principal != a.principal || intent != a.intent {
		return Decision{}, ErrDenied
	}
	return a.decision, nil
}
func (a cpInteropAuthority) WithCurrent(ctx context.Context, principal Principal, intent Intent, decision Decision, save func(Decision) error) error {
	current, err := a.Read(ctx, principal, intent)
	if err != nil {
		return err
	}
	if current != decision {
		return ErrConflict
	}
	// This immutable single-test authority is a DOUBLE. It does not implement
	// production CP policy serialization or target-side Barrier completion.
	return save(current)
}
func (a cpInteropAuthority) AuthorizeRead(ctx context.Context, principal Principal, lookup Lookup) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if principal != a.principal || lookup.Scope != a.intent.Scope || lookup.Execution != a.intent.Execution {
		return ErrDenied
	}
	return nil
}

type cpInteropSecurity struct {
	mu     sync.Mutex
	issued map[string][]byte
	boot   string
	seals  int
}

func (s *cpInteropSecurity) Prepare(ctx context.Context, intent Intent, _ Decision) (evidence.Validity, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Validity{}, err
	}
	if intent.Required.BootID != s.boot {
		return evidence.Validity{}, ErrUnavailable
	}
	return evidence.Validity{Status: "SPECIFIED", Issued: &evidence.TimePoint{TimeBase: "synthetic-contract-clock", BootID: s.boot, Tick: 10},
		Expiry: &evidence.TimePoint{TimeBase: "synthetic-contract-clock", BootID: s.boot, Tick: 100}, QualificationProof: "synthetic-clock-proof"}, nil
}
func (s *cpInteropSecurity) Seal(ctx context.Context, envelope evidence.Envelope) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// An issued-byte registry DOUBLE, never a production signature or key.
	envelope.Integrity = evidence.Integrity{ProfileID: "synthetic-cp-only", KeyID: "synthetic-key", KeyRevision: "synthetic-key-revision", Proof: "synthetic-issued-byte-registration"}
	raw, err := evidence.Encode(envelope)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.issued[envelope.EvidenceID] = append([]byte(nil), raw...)
	s.seals++
	s.mu.Unlock()
	return raw, nil
}
func (s *cpInteropSecurity) Verify(ctx context.Context, envelope evidence.Envelope, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	decoded, err := evidence.Decode(raw)
	if err != nil || !reflect.DeepEqual(decoded, envelope) {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !bytes.Equal(s.issued[envelope.EvidenceID], raw) {
		return ErrUnavailable
	}
	return nil
}
func (s *cpInteropSecurity) Inspect(ctx context.Context, envelope evidence.Envelope, raw []byte) (Applicability, error) {
	if err := s.Verify(ctx, envelope, raw); err != nil {
		return ApplicabilityUnknown, err
	}
	v := envelope.Validity
	if v.Status != "SPECIFIED" || v.Issued == nil || v.Expiry == nil || v.Issued.TimeBase != "synthetic-contract-clock" ||
		v.Expiry.TimeBase != "synthetic-contract-clock" || v.Issued.BootID != s.boot || v.Expiry.BootID != s.boot ||
		v.Issued.Tick > 50 || v.Expiry.Tick <= 50 || v.QualificationProof != "synthetic-clock-proof" {
		return ApplicabilityUnknown, ErrUnavailable
	}
	// Current tick and active applicability are explicitly synthetic. No real
	// clock, effective revocation or independent Continuity is qualified here.
	return Current, nil
}

// The interop test exercises the real Service and Codec; this store is a
// synthetic collaborator. SQL Store/Migration behavior has separate DB tests.
type cpInteropStore struct {
	mu     sync.Mutex
	record *Record
	writes int
}

func (s *cpInteropStore) Claim(ctx context.Context, record Record) (Record, ClaimStatus, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record != nil {
		if !bytes.Equal(s.record.Immutable, record.Immutable) {
			return cloneRecord(*s.record), Collided, nil
		}
		return cloneRecord(*s.record), Existing, nil
	}
	copy := cloneRecord(record)
	s.record = &copy
	s.writes++
	return cloneRecord(copy), Claimed, nil
}
func (s *cpInteropStore) Finish(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.State != "PENDING" || s.record.ClaimID != record.ClaimID || !bytes.Equal(s.record.Immutable, record.Immutable) {
		return ErrConflict
	}
	copy := cloneRecord(record)
	s.record = &copy
	s.writes++
	return nil
}
func (s *cpInteropStore) Lookup(ctx context.Context, lookup Lookup) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.Intent.Scope != lookup.Scope || s.record.Intent.Execution != lookup.Execution ||
		(lookup.EvidenceID != "" && lookup.EvidenceID != s.record.EvidenceID) {
		return Record{}, ErrNotFound
	}
	return cloneRecord(*s.record), nil
}

func TestCPProducerInterop(t *testing.T) {
	directory := os.Getenv("CP_INTEROP_DIR")
	if directory == "" {
		t.Skip("explicit disposable C10 interop fixture is not configured")
	}
	if !filepath.IsAbs(directory) {
		t.Fatal("interop directory must be explicitly absolute")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "c10-fixture.json"))
	if err != nil {
		t.Fatal("cannot read disposable C10 fixture", err)
	}
	var fixture cpInteropInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal("invalid disposable C10 fixture", err)
	}
	request, err := evidence.DecodeRequest(fixture.Request)
	if err != nil {
		t.Fatal("shared Decoder rejected the actual C10 request", err)
	}
	if err := evidence.ValidateRequestFor(request, fixture.Scope, fixture.Execution); err != nil {
		t.Fatal("shared request Scope/Execution differs from the exported target", err)
	}
	canonical, err := evidence.CanonicalBytes(request.Command)
	if err != nil || string(canonical) != request.Binding {
		t.Fatal("actual C10 request has different canonical bytes", err)
	}
	binding, err := evidence.ComputeBinding(request.Command)
	if err != nil || binding != request.Binding || evidence.VerifyBinding(request.Command, request.Binding) != nil {
		t.Fatal("shared Binding differs from the actual C10 request", err)
	}
	encoded, err := evidence.EncodeRequest(request)
	if err != nil || !bytes.Equal(encoded, fixture.Request) {
		t.Fatal("shared Encoder changed the actual C10 request wire", err)
	}
	resolver := NewCanonicalBindingResolver()
	intent, err := resolver.Resolve(context.Background(), fixture.Request)
	if err != nil {
		t.Fatal("product CanonicalBindingResolver rejected actual C10 request", err)
	}
	c := request.Command
	// Independent complete semantic oracle: no local binding generation or
	// duplicate Request wire shape is introduced into the CP consumer.
	expected := Intent{
		Scope: c.Scope, Execution: c.Execution, Binding: request.Binding, Action: c.Operation,
		RequestingPrincipal: c.RequestingPrincipal, AuthorizationIssuer: c.AuthorizationIssuer,
		CandidateRevision: c.CandidateRevision, CandidateContentPin: c.CandidateContentPin,
		ExpectedRevision: c.Expected.Revision, ExpectedStateVersion: c.Expected.StateVersion,
		Required: GuardRequirement{Policy: c.RequiredGuard.Policy, Trust: c.RequiredGuard.Trust,
			Membership: c.RequiredGuard.Membership, OwnershipEpoch: c.RequiredGuard.OwnershipEpoch,
			FenceEpoch: c.RequiredGuard.FenceEpoch, Generation: c.RequiredGuard.Generation,
			BootID: c.RequiredGuard.BootID, ProcessIncarnation: c.RequiredGuard.ProcessIncarnation},
		TimeBase: c.Timing.TimeBase, DeadlineTick: c.Timing.DeadlineTick, OriginalBudget: c.OriginalBudget,
	}
	if intent != expected || c.Schema != evidence.CanonicalRequestVersion || intent.Scope != fixture.Scope ||
		intent.Execution != fixture.Execution || intent.AuthorizationIssuer != fixture.Issuer ||
		intent.Required.Generation != fixture.Frame.Generation || !validIntent(intent) {
		t.Fatal("product Resolver lost or changed a C10 semantic field")
	}
	principal := Principal{ID: intent.RequestingPrincipal}
	authority := cpInteropAuthority{intent: intent, principal: principal, decision: Decision{Principal: principal.ID, Scope: intent.Scope, Action: Action,
		PolicyRevision: intent.Required.Policy, TrustRevision: intent.Required.Trust, AuthorityRevision: "synthetic-authority-revision", TargetRevision: "synthetic-target-revision", State: "ALLOWED"}}
	security := &cpInteropSecurity{issued: make(map[string][]byte), boot: intent.Required.BootID}
	store := &cpInteropStore{}
	service := NewService(Config{Issuer: fixture.Issuer, Audience: fixture.Audience}, Dependencies{
		Resolver:  resolver,
		Authority: authority, Validity: security, Integrity: security, Applicability: security, Store: store,
	})
	issued := service.Issue(context.Background(), principal, IssueRequest{OriginalRequest: fixture.Request, Frame: fixture.Frame})
	if issued.Status != Issued || issued.Applicability != Current || issued.AuthorizesExecution {
		t.Fatalf("CP issue result=%s/%s", issued.Status, issued.Code)
	}
	envelope, err := evidence.Decode(issued.Envelope)
	if err != nil || envelope.Kind != evidence.CPAuthorizationKind || envelope.CPAuthorization == nil || envelope.CPAuthorization.Decision != "AUTHORIZED" {
		t.Fatal("CP service did not issue canonical authorization")
	}
	lookup := Lookup{Scope: intent.Scope, Execution: intent.Execution, EvidenceID: envelope.EvidenceID}
	for count := 0; count < 2; count++ {
		read := service.Read(context.Background(), principal, lookup)
		if read.Status != Replay || read.Applicability != Current || read.AuthorizesExecution || !bytes.Equal(read.Envelope, issued.Envelope) {
			t.Fatalf("CP read changed issued bytes or semantics: %s/%s", read.Status, read.Code)
		}
	}
	if store.writes != 2 || security.seals != 1 {
		t.Fatal("CP read repeated issuance or changed stored evidence")
	}
	// Reuse the ACTUAL C10-generated request for refusal paths too. Stale
	// Binding must fail equality, while a legitimately recomputed Binding still
	// does not grant business permission for different target/content/state.
	for name, change := range map[string]func(*evidence.Command){
		"scope":          func(c *evidence.Command) { c.Scope.Host = "other-host" },
		"execution":      func(c *evidence.Command) { c.Execution.AttemptID = "other-attempt" },
		"principal":      func(c *evidence.Command) { c.RequestingPrincipal = "other-principal" },
		"candidate-pin":  func(c *evidence.Command) { c.CandidateContentPin = "other-content-pin" },
		"expected-state": func(c *evidence.Command) { c.Expected.StateVersion++ },
		"required-guard": func(c *evidence.Command) { c.RequiredGuard.Trust = "other-trust" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			change(&changed.Command)
			commandBytes, err := evidence.CanonicalBytes(changed.Command)
			if err != nil {
				t.Fatal(err)
			}
			// Invalid test proposal: retain the ORIGINAL Binding without asking
			// EncodeRequest to accept a mismatched immutable value.
			bindingJSON, err := json.Marshal(request.Binding)
			if err != nil {
				t.Fatal(err)
			}
			stale := append([]byte(`{"Command":`), commandBytes...)
			stale = append(stale, `,"Binding":`...)
			stale = append(stale, bindingJSON...)
			stale = append(stale, '}')
			refused := service.Issue(context.Background(), principal, IssueRequest{OriginalRequest: stale, Frame: fixture.Frame})
			if refused.Status == Issued || refused.Status == Replay || len(refused.Envelope) != 0 {
				t.Fatal("stale C10 binding was accepted", refused.Status)
			}
			changed.Binding, err = evidence.ComputeBinding(changed.Command)
			if err != nil {
				t.Fatal(err)
			}
			valid, err := evidence.EncodeRequest(changed)
			if err != nil {
				t.Fatal(err)
			}
			refused = service.Issue(context.Background(), principal, IssueRequest{OriginalRequest: valid, Frame: fixture.Frame})
			if refused.Status != Denied || len(refused.Envelope) != 0 {
				t.Fatal("new valid Binding bypassed CP business authority", refused.Status)
			}
			if store.writes != 2 || security.seals != 1 {
				t.Fatal("refusal created an additional issuance")
			}
		})
	}
	for name, malformed := range map[string][]byte{
		"unknown-version": []byte(strings.Replace(string(fixture.Request), `"Schema":1`, `"Schema":2`, 1)),
		"duplicate":       []byte(strings.Replace(string(fixture.Request), `"Schema":1`, `"Schema":1,"Schema":1`, 1)),
		"extra":           []byte(strings.Replace(string(fixture.Request), `"Schema":1`, `"Schema":1,"NotAllowed":1`, 1)),
		"null":            []byte(strings.Replace(string(fixture.Request), `"Schema":1`, `"Schema":null`, 1)),
		"missing-guard":   []byte(strings.Replace(string(fixture.Request), `"RequiredGuard":`, `"UnknownGuard":`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(malformed, fixture.Request) {
				t.Fatal("malformed proposal did not change actual C10 wire")
			}
			refused := service.Issue(context.Background(), principal, IssueRequest{OriginalRequest: malformed, Frame: fixture.Frame})
			if refused.Status == Issued || refused.Status == Replay || len(refused.Envelope) != 0 || store.writes != 2 || security.seals != 1 {
				t.Fatal("invalid C10 proposal produced an additional issuance", refused.Status)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(directory, "cp-envelope.json"), issued.Envelope, 0600); err != nil {
		t.Fatal("cannot retain CP service output", err)
	}
	t.Log("actual product CanonicalBindingResolver + CP Issue/Store/Read retained exact C10 Binding and envelope bytes; authority, integrity, clock and store are synthetic collaborators; real issuance remains disabled")
}
