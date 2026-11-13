package v3authorization

import (
 "bytes"
 "context"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "errors"
 "math"
 "reflect"
 "unicode/utf8"

 evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

type Service struct { config Config; deps Dependencies }

// NewService installs no test signer, clock, v2 permission bridge or default
// resolver. Unconfigured services/handlers are explicitly fail-closed.
func NewService(config Config, deps Dependencies) *Service { return &Service{config, deps} }
func (s *Service) configured() bool {
 return s != nil && validID(s.config.Issuer) && validID(s.config.Audience) &&
  s.deps.Resolver != nil && s.deps.Authority != nil && s.deps.Validity != nil &&
  s.deps.Integrity != nil && s.deps.Applicability != nil && s.deps.Store != nil
}
func failure(status Status, code string) Result { return Result{Status: status, Code: code, Applicability: ApplicabilityUnknown} }
func dependencyFailure(err error) Result {
 if errors.Is(err, ErrDenied) { return failure(Denied, ErrDenied.Code) }
 if errors.Is(err, ErrConflict) { return failure(Conflict, ErrConflict.Code) }
 if errors.Is(err, ErrInvalid) { return failure(Invalid, ErrInvalid.Code) }
 return failure(Unavailable, ErrUnavailable.Code)
}

func (s *Service) Issue(ctx context.Context, principal Principal, request IssueRequest) Result {
 if !validID(principal.ID) { return failure(Denied, ErrDenied.Code) }
 if !s.configured() || ctx.Err() != nil { return failure(Unavailable, ErrUnavailable.Code) }
 // Snapshot caller-owned bytes before crossing any dependency boundary.
 raw := append(json.RawMessage(nil), request.OriginalRequest...)
 if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || !validFrame(request.Frame) { return failure(Invalid, ErrInvalid.Code) }
 intent, err := s.deps.Resolver.Resolve(ctx, raw)
 if err != nil { return dependencyFailure(err) }
 if !validIntent(intent) { return failure(Invalid, ErrInvalid.Code) }
 if request.Frame.Generation != intent.Required.Generation { return failure(Conflict, ErrConflict.Code) }
 if intent.RequestingPrincipal != principal.ID || intent.AuthorizationIssuer != s.config.Issuer { return failure(Denied, ErrDenied.Code) }
 decision, err := s.deps.Authority.Read(ctx, principal, intent)
 if err != nil { return dependencyFailure(err) }
 if err := validateDecision(principal, intent, decision); err != nil { return dependencyFailure(err) }
 record := Record{Principal: principal.ID, Intent: intent, Frame: request.Frame, Issuer: s.config.Issuer, Audience: s.config.Audience, State: "PENDING", Decision: decision}
 record.Immutable, err = immutable(record)
 if err != nil { return failure(Invalid, ErrInvalid.Code) }
 record.ClaimID, err = randomID()
 if err != nil { return failure(Unavailable, ErrUnavailable.Code) }
 stored, claim, err := s.deps.Store.Claim(ctx, cloneRecord(record))
 if err != nil { return failure(Unknown, ErrStoreUnknown.Code) }
 if claim == Collided || !bytes.Equal(stored.Immutable, record.Immutable) { return failure(Conflict, ErrConflict.Code) }
 if claim == Existing {
  // A replay is a read, not an independent authorization/expiry generation.
  return s.readRecord(ctx, principal, Lookup{Scope: intent.Scope, Execution: intent.Execution}, stored)
 }
 if claim != Claimed || stored.ClaimID != record.ClaimID || stored.State != "PENDING" { return failure(Unknown, ErrStoreUnknown.Code) }
 record = cloneRecord(stored)
 // A persisted claim makes a crashed/uncertain generation visible. No second
 // caller takes it over, extends its deadline, or fabricates a new execution.
 validity, err := s.deps.Validity.Prepare(ctx, intent, decision)
 if err != nil || validity.Status != "SPECIFIED" { return s.closeUnissued(ctx, record, Unavailable) }
 id, err := randomID()
 if err != nil { return s.closeUnissued(ctx, record, Unavailable) }
 base := evidence.Envelope{ProtocolVersion: evidence.ProtocolVersion, EvidenceID: id, Kind: evidence.CPAuthorizationKind,
  Issuer: s.config.Issuer, Audience: s.config.Audience, Scope: intent.Scope, Execution: intent.Execution,
  Binding: intent.Binding, Frame: record.Frame, Validity: validity,
  CPAuthorization: &evidence.CPAuthorizationPayload{Action: Action, Decision: "AUTHORIZED", PolicyRevision: decision.PolicyRevision, TrustRevision: decision.TrustRevision}}
 wire, err := s.deps.Integrity.Seal(ctx, evidence.Clone(base))
 if err != nil { return s.closeUnissued(ctx, record, Unavailable) }
 wire = append([]byte(nil), wire...)
 sealed, err := evidence.Decode(wire)
 if err != nil || !sameUnsigned(base, sealed) { return s.closeUnissued(ctx, record, Conflict) }
 canonical, err := evidence.Encode(sealed)
 if err != nil || !bytes.Equal(canonical, wire) { return s.closeUnissued(ctx, record, Conflict) }
 if err = s.deps.Integrity.Verify(ctx, evidence.Clone(sealed), append([]byte(nil), wire...)); err != nil { return s.closeUnissued(ctx, record, Unavailable) }
 // WithCurrent pins relevant CP-owned state through the conditional save. It
 // cannot assert target Boot, Drain, SQLite snapshot or independent freshness.
 callbackRan := false
 saved := false
 saveUnknown := false
 err = s.deps.Authority.WithCurrent(ctx, principal, intent, decision, func(current Decision) error {
  if callbackRan { return ErrConflict }; callbackRan = true
  if validateDecision(principal, intent, current) != nil || current != decision { return ErrConflict }
  applicability, e := s.deps.Applicability.Inspect(ctx, evidence.Clone(sealed), append([]byte(nil), wire...))
  if e != nil || applicability != Current || ctx.Err() != nil { return ErrUnavailable }
  record.State, record.Code, record.EvidenceID, record.Envelope = "ISSUED", "", sealed.EvidenceID, wire
  if e := s.deps.Store.Finish(ctx, cloneRecord(record)); e != nil { saveUnknown = true; return ErrStoreUnknown }
  saved = true
  return nil
 })
 if saveUnknown || (saved && err != nil) { return failure(Unknown, ErrStoreUnknown.Code) }
 if err != nil || !callbackRan || !saved {
  if errors.Is(err, ErrConflict) { return s.closeUnissued(ctx, record, Conflict) }
  return s.closeUnissued(ctx, record, Unavailable)
 }
 return Result{Status: Issued, Envelope: append([]byte(nil), wire...), Applicability: Current}
}

func (s *Service) closeUnissued(ctx context.Context, record Record, status Status) Result {
 record.State, record.Envelope, record.EvidenceID = "CLOSED", nil, ""
 record.Code = string(status)
 if s.deps.Store.Finish(ctx, cloneRecord(record)) != nil { return failure(Unknown, ErrStoreUnknown.Code) }
 if status == Conflict { return failure(Conflict, ErrConflict.Code) }
 return failure(Unavailable, ErrUnavailable.Code)
}

func (s *Service) Read(ctx context.Context, principal Principal, lookup Lookup) Result {
 if !validID(principal.ID) { return failure(Denied, ErrDenied.Code) }
 // A read requires present query authority and current evidence qualification,
 // but never a signing/generation dependency or a current issue permission.
 if s == nil || s.deps.Authority == nil || s.deps.Store == nil || s.deps.Integrity == nil || s.deps.Applicability == nil || ctx.Err() != nil { return failure(Unavailable, ErrUnavailable.Code) }
 if !validLookup(lookup) { return failure(Invalid, ErrInvalid.Code) }
 if err := s.deps.Authority.AuthorizeRead(ctx, principal, lookup); err != nil { return dependencyFailure(err) }
 record, err := s.deps.Store.Lookup(ctx, lookup)
 if err != nil { return failure(Unknown, ErrStoreUnknown.Code) }
 return s.readRecord(ctx, principal, lookup, record)
}
func (s *Service) readRecord(ctx context.Context, principal Principal, lookup Lookup, record Record) Result {
 if err := s.deps.Authority.AuthorizeRead(ctx, principal, lookup); err != nil { return dependencyFailure(err) }
 if !validRecord(record) || record.Intent.Scope != lookup.Scope || record.Intent.Execution != lookup.Execution || (lookup.EvidenceID != "" && record.EvidenceID != lookup.EvidenceID) { return failure(Conflict, ErrConflict.Code) }
 switch record.State {
 case "PENDING": return failure(Pending, "v3_authorization_pending")
 case "CLOSED":
  if record.Code == string(Conflict) { return failure(Conflict, ErrConflict.Code) }
  return failure(Unavailable, ErrUnavailable.Code)
 case "ISSUED":
 default: return failure(Unknown, ErrStoreUnknown.Code)
 }
 envelope, err := evidence.Decode(record.Envelope)
 if err != nil || envelope.EvidenceID != record.EvidenceID || envelope.Kind != evidence.CPAuthorizationKind || envelope.Issuer != record.Issuer || envelope.Audience != record.Audience || envelope.Scope != record.Intent.Scope || envelope.Execution != record.Intent.Execution || envelope.Binding != record.Intent.Binding || envelope.Frame != record.Frame || envelope.CPAuthorization == nil || envelope.CPAuthorization.Action != Action || envelope.CPAuthorization.Decision != "AUTHORIZED" || envelope.CPAuthorization.PolicyRevision != record.Decision.PolicyRevision || envelope.CPAuthorization.TrustRevision != record.Decision.TrustRevision { return failure(Conflict, ErrConflict.Code) }
 if err := s.deps.Integrity.Verify(ctx, evidence.Clone(envelope), append([]byte(nil), record.Envelope...)); err != nil { return failure(Unavailable, ErrUnavailable.Code) }
 current, err := s.deps.Applicability.Inspect(ctx, evidence.Clone(envelope), append([]byte(nil), record.Envelope...))
 if err != nil || (current != Current && current != Expired && current != Revoked) { current = ApplicabilityUnknown }
 // Current read permission is checked again after provider calls; reads do not
 // repair projections, renew evidence, consume authority or replay effects.
 if err := s.deps.Authority.AuthorizeRead(ctx, principal, lookup); err != nil { return dependencyFailure(err) }
 if ctx.Err() != nil { return failure(Unavailable, ErrUnavailable.Code) }
 return Result{Status: Replay, Envelope: append([]byte(nil), record.Envelope...), Applicability: current}
}

func immutable(r Record) ([]byte, error) {
 return json.Marshal(struct{ Principal string; Intent Intent; Frame evidence.BackendReadFrame; Issuer, Audience string }{r.Principal,r.Intent,r.Frame,r.Issuer,r.Audience})
}
func cloneRecord(r Record) Record { r.Immutable = append([]byte(nil),r.Immutable...); r.Envelope = append([]byte(nil),r.Envelope...); return r }
func sameUnsigned(a,b evidence.Envelope) bool { a.Integrity = evidence.Integrity{}; b.Integrity = evidence.Integrity{}; return reflect.DeepEqual(a,b) }
func randomID() (string,error) { var b [16]byte; if _,err:=rand.Read(b[:]);err!=nil{return "",err};return "cp-auth-"+hex.EncodeToString(b[:]),nil }
func validID(s string) bool {
 if len(s)==0 || len(s)>128 {return false};for n,c:=range []byte(s){if (c>='a'&&c<='z')||(c>='0'&&c<='9'){continue};if n==0||(c!='.'&&c!='_'&&c!=':'&&c!='-'){return false}};return true
}
func validScope(s evidence.Scope) bool {return validID(s.Realm)&&validID(s.Host)&&validID(s.Resource)&&validID(s.Backend)}
func validExecution(e evidence.Execution) bool {return validID(e.RequestID)&&validID(e.ExecutionKey)&&validID(e.OperationID)&&validID(e.AttemptID)&&validID(e.StepID)}
func validFrame(f evidence.BackendReadFrame) bool {return validID(f.Issuer)&&validID(f.ServingIncarnation)&&validID(f.Generation)&&validID(f.SnapshotID)&&validID(f.ObservationProof)}
func validLookup(l Lookup) bool {return validScope(l.Scope)&&validExecution(l.Execution)&&(l.EvidenceID==""||validID(l.EvidenceID))}
func validIntent(i Intent) bool {
 if !validScope(i.Scope)||!validExecution(i.Execution)||i.Action!=Action||i.ExpectedStateVersion==math.MaxUint64||i.DeadlineTick==0||len(i.Binding)==0||len(i.Binding)>64<<10||!utf8.ValidString(i.Binding){return false}
 for _,s:=range []string{i.RequestingPrincipal,i.AuthorizationIssuer,i.CandidateRevision,i.CandidateContentPin,i.ExpectedRevision,i.Required.Policy,i.Required.Trust,i.Required.Membership,i.Required.OwnershipEpoch,i.Required.FenceEpoch,i.Required.Generation,i.Required.BootID,i.Required.ProcessIncarnation,i.TimeBase,i.OriginalBudget}{if !validID(s){return false}};return true
}
func validateDecision(p Principal,i Intent,d Decision) error {
 if d.State=="DENIED" {return ErrDenied}
 if d.State!="ALLOWED"||!validID(d.AuthorityRevision)||!validID(d.TargetRevision)||!validID(d.PolicyRevision)||!validID(d.TrustRevision){return ErrUnavailable}
 if d.Principal!=p.ID||d.Scope!=i.Scope||d.Action!=i.Action||d.PolicyRevision!=i.Required.Policy||d.TrustRevision!=i.Required.Trust{return ErrConflict};return nil
}
func validRecord(r Record) bool {
 if r.Issuer!=r.Intent.AuthorizationIssuer||validateDecision(Principal{r.Principal},r.Intent,r.Decision)!=nil{return false}
 if !validIntent(r.Intent)||!validID(r.Principal)||r.Principal!=r.Intent.RequestingPrincipal||!validFrame(r.Frame)||!validID(r.ClaimID)||!validID(r.Issuer)||!validID(r.Audience){return false}
 b,e:=immutable(r);return e==nil&&bytes.Equal(b,r.Immutable)
}
