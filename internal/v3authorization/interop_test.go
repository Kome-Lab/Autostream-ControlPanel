package v3authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// These records decode ONLY the synthetic request exported by the actual pinned
// C10 fixture. They are not a new public P-A wire profile or a production copy of
// C10 CanonicalBinding. A shared public binding contract remains unconnected.
type cpInteropCommand struct {
	Schema uint32
	Operation string
	Scope struct { Realm, Host, Resource, Backend string }
	Execution struct { RequestID, ExecutionKey, OperationID, AttemptID, StepID string }
	RequestingPrincipal, AuthorizationIssuer string
	CandidateRevision, CandidateContentPin string
	Expected struct { Revision string; StateVersion uint64 }
	RequiredGuard GuardRequirement
	Timing struct { TimeBase string; DeadlineTick uint64 }
	OriginalBudget string
}
type cpInteropRequest struct { Command cpInteropCommand; Binding string }
type cpInteropInput struct {
	Request json.RawMessage `json:"request"`
	Scope evidence.Scope `json:"scope"`
	Execution evidence.Execution `json:"execution"`
	Frame evidence.BackendReadFrame `json:"backend_read_frame"`
	Issuer string `json:"issuer"`
	Audience string `json:"audience"`
}

type cpInteropResolver struct { original json.RawMessage; request cpInteropRequest; intent Intent }

func (r cpInteropResolver) Resolve(ctx context.Context, raw json.RawMessage) (Intent, error) {
	if err := ctx.Err(); err != nil { return Intent{}, err }
	var request cpInteropRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !reflect.DeepEqual(request, r.request) || request.Binding != r.request.Binding {
		return Intent{}, ErrConflict
	}
	// Exact original bytes make the exported fixture the only accepted input.
	// No local serialization is being claimed as the canonical Binding algorithm.
	if !bytes.Equal(raw, r.original) { return Intent{}, ErrConflict }
	return r.intent, nil
}

type cpInteropAuthority struct { intent Intent; principal Principal; decision Decision }

func (a cpInteropAuthority) Read(ctx context.Context, principal Principal, intent Intent) (Decision, error) {
	if err := ctx.Err(); err != nil { return Decision{}, err }
	if principal != a.principal || intent != a.intent { return Decision{}, ErrDenied }
	return a.decision, nil
}
func (a cpInteropAuthority) WithCurrent(ctx context.Context, principal Principal, intent Intent, decision Decision, save func(Decision) error) error {
	current, err := a.Read(ctx, principal, intent)
	if err != nil { return err }
	if current != decision { return ErrConflict }
	// This immutable single-test authority is a DOUBLE. It does not implement
	// production CP policy serialization or target-side Barrier completion.
	return save(current)
}
func (a cpInteropAuthority) AuthorizeRead(ctx context.Context, principal Principal, lookup Lookup) error {
	if err := ctx.Err(); err != nil { return err }
	if principal != a.principal || lookup.Scope != a.intent.Scope || lookup.Execution != a.intent.Execution { return ErrDenied }
	return nil
}

type cpInteropSecurity struct {
	mu sync.Mutex
	issued map[string][]byte
	boot string
	seals int
}

func (s *cpInteropSecurity) Prepare(ctx context.Context, intent Intent, _ Decision) (evidence.Validity, error) {
	if err := ctx.Err(); err != nil { return evidence.Validity{}, err }
	if intent.Required.BootID != s.boot { return evidence.Validity{}, ErrUnavailable }
	return evidence.Validity{Status:"SPECIFIED", Issued:&evidence.TimePoint{TimeBase:"synthetic-contract-clock", BootID:s.boot, Tick:10},
		Expiry:&evidence.TimePoint{TimeBase:"synthetic-contract-clock", BootID:s.boot, Tick:100}, QualificationProof:"synthetic-clock-proof"}, nil
}
func (s *cpInteropSecurity) Seal(ctx context.Context, envelope evidence.Envelope) ([]byte, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	// An issued-byte registry DOUBLE, never a production signature or key.
	envelope.Integrity = evidence.Integrity{ProfileID:"synthetic-cp-only", KeyID:"synthetic-key", KeyRevision:"synthetic-key-revision", Proof:"synthetic-issued-byte-registration"}
	raw, err := evidence.Encode(envelope)
	if err != nil { return nil, err }
	s.mu.Lock()
	s.issued[envelope.EvidenceID] = append([]byte(nil), raw...)
	s.seals++
	s.mu.Unlock()
	return raw, nil
}
func (s *cpInteropSecurity) Verify(ctx context.Context, envelope evidence.Envelope, raw []byte) error {
	if err := ctx.Err(); err != nil { return err }
	decoded, err := evidence.Decode(raw)
	if err != nil || !reflect.DeepEqual(decoded, envelope) { return ErrUnavailable }
	s.mu.Lock()
	defer s.mu.Unlock()
	if !bytes.Equal(s.issued[envelope.EvidenceID], raw) { return ErrUnavailable }
	return nil
}
func (s *cpInteropSecurity) Inspect(ctx context.Context, envelope evidence.Envelope, raw []byte) (Applicability, error) {
	if err := s.Verify(ctx, envelope, raw); err != nil { return ApplicabilityUnknown, err }
	v := envelope.Validity
	if v.Status != "SPECIFIED" || v.Issued == nil || v.Expiry == nil || v.Issued.TimeBase != "synthetic-contract-clock" ||
		v.Expiry.TimeBase != "synthetic-contract-clock" || v.Issued.BootID != s.boot || v.Expiry.BootID != s.boot ||
		v.Issued.Tick > 50 || v.Expiry.Tick <= 50 || v.QualificationProof != "synthetic-clock-proof" { return ApplicabilityUnknown, ErrUnavailable }
	// Current tick and active applicability are explicitly synthetic. No real
	// clock, effective revocation or independent Continuity is qualified here.
	return Current, nil
}

// The interop test exercises the real Service and Codec; this store is a
// synthetic collaborator. SQL Store/Migration behavior has separate DB tests.
type cpInteropStore struct { mu sync.Mutex; record *Record; writes int }

func (s *cpInteropStore) Claim(ctx context.Context, record Record) (Record, ClaimStatus, error) {
	if err := ctx.Err(); err != nil { return Record{}, "", err }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record != nil {
		if !bytes.Equal(s.record.Immutable, record.Immutable) { return cloneRecord(*s.record), Collided, nil }
		return cloneRecord(*s.record), Existing, nil
	}
	copy := cloneRecord(record)
	s.record = &copy
	s.writes++
	return cloneRecord(copy), Claimed, nil
}
func (s *cpInteropStore) Finish(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil { return err }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.State != "PENDING" || s.record.ClaimID != record.ClaimID || !bytes.Equal(s.record.Immutable, record.Immutable) { return ErrConflict }
	copy := cloneRecord(record)
	s.record = &copy
	s.writes++
	return nil
}
func (s *cpInteropStore) Lookup(ctx context.Context, lookup Lookup) (Record, error) {
	if err := ctx.Err(); err != nil { return Record{}, err }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.Intent.Scope != lookup.Scope || s.record.Intent.Execution != lookup.Execution ||
		(lookup.EvidenceID != "" && lookup.EvidenceID != s.record.EvidenceID) { return Record{}, ErrNotFound }
	return cloneRecord(*s.record), nil
}

func TestCPProducerInterop(t *testing.T) {
	directory := os.Getenv("CP_INTEROP_DIR")
	if directory == "" { t.Skip("explicit disposable C10 interop fixture is not configured") }
	if !filepath.IsAbs(directory) { t.Fatal("interop directory must be explicitly absolute") }
	raw, err := os.ReadFile(filepath.Join(directory,"c10-fixture.json"))
	if err != nil { t.Fatal("cannot read disposable C10 fixture",err) }
	var fixture cpInteropInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil { t.Fatal("invalid disposable C10 fixture",err) }
	var request cpInteropRequest
	decoder = json.NewDecoder(bytes.NewReader(fixture.Request))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil { t.Fatal("invalid C10 request fixture",err) }
	c := request.Command
	intent := Intent{
		Scope:evidence.Scope{Realm:c.Scope.Realm, Host:c.Scope.Host, Resource:c.Scope.Resource, Backend:c.Scope.Backend},
		Execution:evidence.Execution{RequestID:c.Execution.RequestID, ExecutionKey:c.Execution.ExecutionKey, OperationID:c.Execution.OperationID, AttemptID:c.Execution.AttemptID, StepID:c.Execution.StepID},
		Binding:request.Binding, Action:c.Operation, RequestingPrincipal:c.RequestingPrincipal, AuthorizationIssuer:c.AuthorizationIssuer,
		CandidateRevision:c.CandidateRevision, CandidateContentPin:c.CandidateContentPin, ExpectedRevision:c.Expected.Revision,
		ExpectedStateVersion:c.Expected.StateVersion, Required:c.RequiredGuard, TimeBase:c.Timing.TimeBase, DeadlineTick:c.Timing.DeadlineTick, OriginalBudget:c.OriginalBudget,
	}
	if c.Schema != 1 || intent.Scope != fixture.Scope || intent.Execution != fixture.Execution || intent.AuthorizationIssuer != fixture.Issuer ||
		intent.Required.Generation != fixture.Frame.Generation || !validIntent(intent) { t.Fatal("C10 fixture semantic correlation failed") }
	principal := Principal{ID:intent.RequestingPrincipal}
	authority := cpInteropAuthority{intent:intent, principal:principal, decision:Decision{Principal:principal.ID, Scope:intent.Scope, Action:Action,
		PolicyRevision:intent.Required.Policy, TrustRevision:intent.Required.Trust, AuthorityRevision:"synthetic-authority-revision", TargetRevision:"synthetic-target-revision", State:"ALLOWED"}}
	security := &cpInteropSecurity{issued:make(map[string][]byte), boot:intent.Required.BootID}
	store := &cpInteropStore{}
	service := NewService(Config{Issuer:fixture.Issuer, Audience:fixture.Audience}, Dependencies{
		Resolver:cpInteropResolver{original:append(json.RawMessage(nil),fixture.Request...), request:request, intent:intent},
		Authority:authority, Validity:security, Integrity:security, Applicability:security, Store:store,
	})
	issued := service.Issue(context.Background(), principal, IssueRequest{OriginalRequest:fixture.Request, Frame:fixture.Frame})
	if issued.Status != Issued || issued.Applicability != Current || issued.AuthorizesExecution { t.Fatalf("CP issue result=%s/%s",issued.Status,issued.Code) }
	envelope, err := evidence.Decode(issued.Envelope)
	if err != nil || envelope.Kind != evidence.CPAuthorizationKind || envelope.CPAuthorization == nil || envelope.CPAuthorization.Decision != "AUTHORIZED" { t.Fatal("CP service did not issue canonical authorization") }
	lookup := Lookup{Scope:intent.Scope, Execution:intent.Execution, EvidenceID:envelope.EvidenceID}
	for count:=0; count<2; count++ {
		read := service.Read(context.Background(), principal, lookup)
		if read.Status != Replay || read.Applicability != Current || read.AuthorizesExecution || !bytes.Equal(read.Envelope,issued.Envelope) {
			t.Fatalf("CP read changed issued bytes or semantics: %s/%s",read.Status,read.Code)
		}
	}
	if store.writes != 2 || security.seals != 1 { t.Fatal("CP read repeated issuance or changed stored evidence") }
	if err := os.WriteFile(filepath.Join(directory,"cp-envelope.json"), issued.Envelope,0600); err != nil { t.Fatal("cannot retain CP service output",err) }
	t.Log("actual CP Issue/Store/Read produced exact canonical envelope; authority, binding qualification, integrity, clock and store are synthetic collaborators; no production issuance enabled")
}
