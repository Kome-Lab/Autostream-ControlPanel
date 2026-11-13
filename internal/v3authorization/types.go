// Package v3authorization implements CP-owned issuance liabilities, not v2 Grants,
// target-side Mutation permission, D1 decisions, or independent Continuity.
package v3authorization

import (
 "context"
 "encoding/json"

 evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

const Action = "registry.reference_only"

type Status string
const (
 Issued Status = "ISSUED"
 Replay Status = "REPLAY"
 Denied Status = "DENIED"
 Conflict Status = "CONFLICT"
 Unavailable Status = "UNAVAILABLE"
 Pending Status = "PENDING"
 Unknown Status = "UNKNOWN"
 Invalid Status = "INVALID"
)

// Principal is established by the server authentication boundary. It is never
// decoded from an HTTP body. Current v3 permission is checked independently.
type Principal struct { ID string }

// Intent is a CP semantic view, NOT another public P-A wire/binding profile.
// A qualified resolver must verify EVERY field against the original canonical
// request. Contracts currently carries opaque Binding only; its public P-A
// Command/CanonicalBinding contract and a production resolver remain unconnected.
type Intent struct {
 Scope evidence.Scope
 Execution evidence.Execution
 Binding string
 Action string
 RequestingPrincipal string
 AuthorizationIssuer string
 CandidateRevision string
 CandidateContentPin string
 ExpectedRevision string
 ExpectedStateVersion uint64
 Required GuardRequirement
 TimeBase string
 DeadlineTick uint64
 OriginalBudget string
}
type GuardRequirement struct {
 Policy, Trust, Membership, OwnershipEpoch, FenceEpoch string
 Generation, BootID, ProcessIncarnation string
}
type IssueRequest struct {
 OriginalRequest json.RawMessage `json:"original_request"`
 Frame evidence.BackendReadFrame `json:"backend_read_frame"`
}
type Lookup struct {
 Scope evidence.Scope `json:"scope"`
 Execution evidence.Execution `json:"execution"`
 EvidenceID string `json:"evidence_id,omitempty"`
}
type BindingResolver interface { Resolve(context.Context, json.RawMessage) (Intent, error) }

// Decision is CP business authorization, not a target-side Guard/Drain proof.
// Authority must own the v3 capability, canonical target mapping and revisions.
type Decision struct {
 Principal string
 Scope evidence.Scope
 Action string
 PolicyRevision, TrustRevision, AuthorityRevision, TargetRevision string
 State string // ALLOWED or DENIED; missing/other states are not permission.
}
type Authority interface {
 Read(context.Context, Principal, Intent) (Decision, error)
 // WithCurrent must serialize relevant CP permission, target, Policy and Trust
 // changes through fn's conditional Store save. It calls fn synchronously
// at most once before returning; it never retains fn for delayed execution. An ordinary re-read/local copy
 // is NOT an implementation. CP transactions do not fence remote Host commits.
 WithCurrent(context.Context, Principal, Intent, Decision, func(Decision) error) error
 AuthorizeRead(context.Context, Principal, Lookup) error
}
type ValidityProvider interface {
 Prepare(context.Context, Intent, Decision) (evidence.Validity, error)
}
type IntegrityProvider interface {
 // Seal returns final canonical envelope bytes, including integrity metadata.
 // Verify authenticates the complete payload and those EXACT saved/wire bytes.
 // No algorithm, signing canonicalization, key product or Token is selected here.
 Seal(context.Context, evidence.Envelope) ([]byte, error)
 Verify(context.Context, evidence.Envelope, []byte) error
}
type Applicability string
const (
 Current Applicability = "CURRENT"
 Expired Applicability = "EXPIRED"
 Revoked Applicability = "REVOKED"
 ApplicabilityUnknown Applicability = "UNKNOWN"
)
type ApplicabilityReader interface {
 // Current validity/clock qualification and effective revocation, not merely
 // issuance-time Validity nor Context.Deadline, must be assessed separately.
 Inspect(context.Context, evidence.Envelope, []byte) (Applicability, error)
}
type Config struct { Issuer, Audience string }
type Dependencies struct {
 Resolver BindingResolver
 Authority Authority
 Validity ValidityProvider
 Integrity IntegrityProvider
 Applicability ApplicabilityReader
 Store Store
}
type Result struct {
 Status Status
 Code string
 Envelope []byte
 Applicability Applicability
 // Evidence read/replay is historical information, never a new execution grant.
 AuthorizesExecution bool
}

// Record identity does not grow when later receipts/observations arrive.
// Pending is durable before generation; no second generator automatically takes
// over a pending/unknown claim. Missing restored rows are not non-issuance proof.
type Record struct {
 Principal string
 Intent Intent
 Frame evidence.BackendReadFrame
 Issuer, Audience string
 Immutable []byte
 ClaimID string
 State string // PENDING, ISSUED, CLOSED (no issued negative Evidence)
 Code string
 EvidenceID string
 Envelope []byte
 Decision Decision
}
type ClaimStatus string
const (
 Claimed ClaimStatus = "CLAIMED"
 Existing ClaimStatus = "EXISTING"
 Collided ClaimStatus = "COLLIDED"
)
// Store operations are parameterized and own no arbitrary DSN/path/API selected
// by a request. NewSQLStore accepts an already-owned CP database connection.
type Store interface {
 Claim(context.Context, Record) (Record, ClaimStatus, error)
 Finish(context.Context, Record) error
 Lookup(context.Context, Lookup) (Record, error)
}

type SafeError struct { Code string }
func (e SafeError) Error() string { return e.Code }
var (
 ErrDenied = SafeError{"v3_authorization_denied"}
 ErrUnavailable = SafeError{"v3_authorization_unavailable"}
 ErrConflict = SafeError{"v3_authorization_conflict"}
 ErrNotFound = SafeError{"v3_authorization_not_found"}
 ErrStoreUnknown = SafeError{"v3_authorization_storage_unknown"}
 ErrInvalid = SafeError{"v3_authorization_invalid"}
)
