// Package bootstrapapproval connects independently verified approval to the
// existing management transaction. It supplies no Origin, Root, clock, quorum,
// signing key, production qualification or public bootstrap entry point.
package bootstrapapproval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
)

var (
	ErrDenied   = errors.New("approval_denied")
	ErrHeld     = errors.New("approval_held")
	ErrUnknown  = errors.New("approval_unknown_inspect_same_attempt")
	ErrConflict = errors.New("approval_conflict")
)

const FixtureProfile = "IU-WS1-SYNTHETIC-ONLY-1"
const FixtureDatabase = "autostream_v3_auth_ci"

// Reference is untrusted input, not an approval or capability. The authenticated
// human session remains checked by ManagementService inside its transaction.
type Reference struct {
	ManifestID string
	AttemptID  string
	Identity   lifecycle.IdentityContext
}

// Request contains nonsecret management metadata only. No raw manifest,
// credential, session/CSRF token or secret-derived commitment is accepted.
type Request struct {
	Reference                Reference
	EventID, CorrelationID   string
	Scope                    evidence.Scope
	Action, Principal, State string
	GrantBinding             string
	ExpectedRevision         uint64
	Operations, Subjects     []string
	ProvisionTargets         bool
}

// Ceiling is explicit and finite. Empty values never mean unlimited/default.
type Ceiling struct {
	Scope                evidence.Scope
	Subject              string
	Operations, Subjects []string
	ProvisionTargets     bool
}

type Qualification struct {
	ProfileID           string
	FixtureOnly         bool
	ProductionQualified bool
}

type Verification struct {
	Request             Request
	Ceiling             Ceiling
	OriginID, ReceiptID string
	OriginRevision      uint64
	Knowledge           lifecycle.Knowledge
	Consumed            bool
}

// Inspection keeps origin consumption and CP commit knowledge separate. Even a
// known local commit never releases a hold or issues fresh approval/authority.
type Inspection struct {
	OriginKnowledge lifecycle.Knowledge
	OriginConsumed  bool
	CPKnowledge     lifecycle.Knowledge
	CPPresent       bool
	Generation      uint64
	Gate            lifecycle.Gate
}

func (Inspection) ProductionQualified() bool { return false }
func (Inspection) AuthorizesMutation() bool  { return false }

func cloneRequest(r Request) Request {
	r.Operations = slices.Clone(r.Operations)
	r.Subjects = slices.Clone(r.Subjects)
	return r
}
func cloneVerification(v Verification) Verification {
	v.Request = cloneRequest(v.Request)
	v.Ceiling.Operations = slices.Clone(v.Ceiling.Operations)
	v.Ceiling.Subjects = slices.Clone(v.Ceiling.Subjects)
	return v
}
func id(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return false
		}
	}
	return true
}
func validSet(values []string, max int) bool {
	if len(values) == 0 || len(values) > max {
		return false
	}
	for i, v := range values {
		if !id(v) || slices.Contains(values[:i], v) {
			return false
		}
	}
	return true
}
func scopeValid(s evidence.Scope) bool {
	return id(s.Realm) && id(s.Host) && id(s.Resource) && id(s.Backend)
}
func validate(r Request, limits lifecycle.Limits) error {
	if _, err := hex.DecodeString(r.GrantBinding); err != nil {
		return ErrDenied
	}
	for _, value := range []string{r.Reference.ManifestID, r.Reference.AttemptID, r.EventID, r.CorrelationID, r.Action, r.Principal, r.Scope.Realm, r.Scope.Host, r.Scope.Resource, r.Scope.Backend} {
		if len(value) > limits.MaxIDBytes {
			return ErrDenied
		}
	}
	if len(r.Operations) > limits.MaxRefs || len(r.Subjects) > limits.MaxRefs {
		return ErrDenied
	}
	for _, values := range [][]string{r.Operations, r.Subjects} {
		for _, value := range values {
			if len(value) > limits.MaxIDBytes {
				return ErrDenied
			}
		}
	}
	if lifecycle.ValidateIdentity(r.Reference.Identity, limits).Kind != lifecycle.StructurallyCoherent ||
		r.Reference.Identity.Class != lifecycle.Human || r.Reference.Identity.PrincipalKind != lifecycle.PrincipalHuman ||
		r.Reference.Identity.Purpose != lifecycle.PurposeApproval || string(r.Reference.Identity.ActorID) != r.Principal ||
		!id(r.Reference.ManifestID) || !id(r.Reference.AttemptID) || r.Reference.AttemptID != r.EventID ||
		!id(r.CorrelationID) || !scopeValid(r.Scope) || !id(r.Action) || r.State != "ACTIVE" ||
		len(r.GrantBinding) != 64 ||
		r.ExpectedRevision == ^uint64(0) || !validSet(r.Operations, 12) || !validSet(r.Subjects, 32) {
		return ErrDenied
	}
	return nil
}
func ceilingAllows(r Request, c Ceiling) bool {
	if c.Scope != r.Scope || c.Subject != r.Principal || !validSet(c.Operations, 12) || !validSet(c.Subjects, 32) || r.ProvisionTargets && !c.ProvisionTargets {
		return false
	}
	for _, v := range r.Operations {
		if !slices.Contains(c.Operations, v) {
			return false
		}
	}
	for _, v := range r.Subjects {
		if !slices.Contains(c.Subjects, v) {
			return false
		}
	}
	return true
}

// Binding is only a nonsecret metadata equality digest; it is not D4 secret
// commitment, signature verification, trust qualification or authority.
func Binding(r Request) string {
	r = cloneRequest(r)
	slices.Sort(r.Operations)
	slices.Sort(r.Subjects)
	b, _ := json.Marshal(r)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
