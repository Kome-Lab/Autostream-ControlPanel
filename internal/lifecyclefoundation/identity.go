package lifecyclefoundation

import (
	"context"
	"strings"
	"sync/atomic"
)

// IdentityClass is a local, purpose-limited trust context. It never grants
// Operational, Evidence, Management, Bootstrap or Workload authority.
type IdentityClass string

const (
	IdentityHuman   IdentityClass = "HUMAN"
	IdentityPending IdentityClass = "PENDING"
	IdentityPreauth IdentityClass = "PREAUTH"
	IdentitySystem  IdentityClass = "SYSTEM"
)

// IdentityBinding contains references only. ProofReference identifies an already
// verified local proof; neither proof bytes nor secret-derived commitments belong
// in this DTO. Revision is the version that proof/authorization actually checked.
type IdentityBinding struct {
	Class          IdentityClass
	Target         string
	Flow           string
	ProofReference string
	Coverage       string
	Revision       uint64
}

type IdentityCall struct {
	Operation        string
	Target           string
	ContentReference string
}

// ReadOnly describes the exact contract, not a grant or admission decision.
func (c IdentityCall) ReadOnly() bool {
	rule, ok := identityRules[c.Operation]
	return ok && rule.mode == Read
}

// IdentityAuthorizer is an optional trusted composition dependency. There is no
// product constructor implementing it. An HTTP actor/proof is not this interface.
type IdentityAuthorizer interface {
	ResolveIdentity(context.Context, IdentityCall) (IdentityBinding, error)
}

type identityRequestKey struct{}
type identityRequest struct {
	active atomic.Bool
	gate    *Gate
	call    IdentityCall
	binding IdentityBinding
}

// IdentityRequest exposes only the exact request minted by DoIdentity to its
// trusted Authorizer. A caller cannot manufacture this private context key.
func IdentityRequest(ctx context.Context) (IdentityCall, IdentityBinding, error) {
	if ctx == nil || ctx.Err() != nil {
		return IdentityCall{}, IdentityBinding{}, ErrHeld
	}
	r, ok := ctx.Value(identityRequestKey{}).(*identityRequest)
	if !ok || r == nil || r.gate == nil || !r.active.Load() {
		return IdentityCall{}, IdentityBinding{}, ErrHeld
	}
	return r.call, r.binding, nil
}

type identityRule struct {
	mode    Mode
	classes string
}

// Exact operations only: prefixes and unknown operations never inherit a grant.
// h/p/a/s denote Human, pending onboarding, preauth proof, System cleanup.
var identityRules = map[string]identityRule{
	"identity.user.list":            {Read, "h"},
	"identity.user.create":          {Write, "h"},
	"identity.user.create_oauth":    {Write, "a"},
	"identity.user.read":            {Read, "hpa"},
	"identity.user.update":          {Write, "h"},
	"identity.user.status":          {Write, "h"},
	"identity.user.delete":          {Write, "h"},
	"identity.user.reset_password":  {Write, "h"},
	"identity.user.find":            {Read, "a"},
	"identity.user.permissions":     {Read, "hpa"},
	"identity.user.has_role":        {Read, "ha"},
	"identity.user.count":           {Read, "h"},
	"identity.user.count_admins":    {Read, "h"},
	"identity.role.list":            {Read, "h"},
	"identity.role.create":          {Write, "h"},
	"identity.role.read":            {Read, "h"},
	"identity.role.update":          {Write, "h"},
	"identity.role.delete":          {Write, "h"},
	"identity.password.change":      {Write, "hp"},
	"identity.password.change_self": {Write, "hp"},
	"identity.session.create":       {Write, "pa"},
	"identity.session.issue_login":  {Write, "pa"},
	"identity.session.read":         {Read, "hpa"},
	"identity.session.refresh":      {Write, "h"},
	"identity.session.delete":       {Write, "hp"},
	"identity.session.delete_user":  {Write, "hp"},
	"identity.login.success":        {Write, "pa"},
	"identity.login.failure":        {Write, "a"},
	"identity.mfa.read":             {Read, "ha"},
	"identity.mfa.enroll":           {Write, "h"},
	"identity.mfa.confirm":          {Write, "h"},
	"identity.mfa.disable":          {Write, "h"},
	"identity.mfa.regenerate":       {Write, "h"},
	"identity.mfa.consume_recovery": {Write, "ha"},
	"identity.mfa.challenge.create": {Write, "a"},
	"identity.mfa.challenge.read":   {Read, "a"},
	"identity.mfa.challenge.delete": {Write, "a"},
	"identity.cleanup.sessions":     {Write, "s"},
	"identity.cleanup.challenges":   {Write, "s"},
}

func identityClassCode(class IdentityClass) string {
	switch class {
	case IdentityHuman:
		return "h"
	case IdentityPending:
		return "p"
	case IdentityPreauth:
		return "a"
	case IdentitySystem:
		return "s"
	}
	return ""
}

func (b IdentityBinding) validate(intent Intent) error {
	rule, ok := identityRules[intent.Operation]
	class := identityClassCode(b.Class)
	if !ok || class == "" || !strings.Contains(rule.classes, class) || intent.Mode != rule.mode ||
		!validID(b.Target) || !validID(b.Flow) || !validID(b.ProofReference) ||
		b.Coverage != IdentityFixtureCoverage || b.Revision == 0 ||
		intent.Scope != (Scope{Type: "RESOURCE", ID: SyntheticReference(b.Target)}) || intent.Subject != "product" {
		return ErrHeld
	}
	if rule.mode == Read && intent.Purpose != "CURRENT_READ" || rule.mode == Write && intent.Purpose != "IDENTITY_CHANGE" {
		return ErrHeld
	}
	return nil
}

func validateIdentityAuthorization(a Authorization, intent Intent) error {
	if a.Identity == nil || a.Approval != nil || a.Identity.validate(intent) != nil {
		return ErrHeld
	}
	kind := "HUMAN"
	if a.Identity.Class == IdentityPreauth {
		kind = "PREAUTH"
	} else if a.Identity.Class == IdentitySystem {
		kind = "SYSTEM"
	}
	if a.PrincipalKind != kind {
		return ErrHeld
	}
	return nil
}

// DoIdentity uses B0 for every reservation/commit/UNKNOWN/completion boundary.
// The additional capture seam is attached only by _test.go. A fixture reference
// cannot qualify a real secret-state profile or enable a product Gate.
func (g *Gate) DoIdentity(ctx context.Context, call IdentityCall, fn func(context.Context) error) error {
	if g.Ready() != nil || ctx == nil || ctx.Err() != nil || fn == nil ||
		g.identityCapture == nil || !validID(call.Target) ||
		call.ContentReference != "" && !validID(call.ContentReference) || ctx.Value(identityRequestKey{}) != nil {
		return ErrHeld
	}
	rule, ok := identityRules[call.Operation]
	if !ok {
		return ErrHeld
	}
	trusted, ok := g.service.authorizer.(IdentityAuthorizer)
	if !ok || missing(trusted) {
		return ErrHeld
	}
	binding, err := trusted.ResolveIdentity(ctx, call)
	if err != nil || binding.Target != call.Target {
		return ErrHeld
	}
	purpose := "IDENTITY_CHANGE"
	if rule.mode == Read {
		purpose = "CURRENT_READ"
	}
	intent := Intent{Mode: rule.mode, Purpose: purpose, Operation: call.Operation,
		OperationContent: "identity-validation", Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(call.Target)}, Subject: "product"}
	if binding.validate(intent) != nil {
		return ErrHeld
	}
	request := &identityRequest{gate: g, call: call, binding: binding}
	request.active.Store(true)
	defer request.active.Store(false)
	bound := context.WithValue(ctx, identityRequestKey{}, request)
	return g.Do(bound, Call{Operation: call.Operation, Purpose: purpose, Target: call.Target,
		Content: struct {
			Binding IdentityBinding
			Reference string
		}{binding, call.ContentReference}}, func(partCtx context.Context) error {
		p, _ := partCtx.Value(participationKey{}).(*participation)
		if p == nil || p.authorization.Identity == nil || *p.authorization.Identity != binding ||
			p.before.IdentityRevision != binding.Revision {
			return ErrHeld
		}
		q, joined, err := SQL(partCtx, g.service.store.db)
		if err != nil || !joined {
			return ErrHeld
		}
		before, err := readIdentityProjection(partCtx, q, rule.mode == Write)
		if err != nil || before.Revision != binding.Revision || before.Coverage != binding.Coverage ||
			before.OperationalRevision != p.before.OperationalRevision || before.AuthorityEpoch != p.before.AuthorityEpoch {
			return ErrHeld
		}
		captured, err := g.identityCapture(partCtx, q)
		if err != nil || captured != before {
			return ErrHeld
		}
		if err = fn(partCtx); err != nil {
			return err
		}
		if rule.mode == Read {
			return nil
		}
		after, err := g.identityCapture(partCtx, q)
		if err != nil {
			return err
		}
		return advanceIdentityProjection(partCtx, q, before, after, call, binding)
	})
}

func (g *Gate) RequireIdentity(ctx context.Context, operation, target string, contentReference ...string) (bool, error) {
	inside, err := g.Require(ctx, operation)
	if err != nil || !inside {
		return inside, err
	}
	r, _ := ctx.Value(identityRequestKey{}).(*identityRequest)
	if r == nil || !r.active.Load() || r.gate != g || r.call.Operation != operation || r.call.Target != target ||
		len(contentReference) > 1 || len(contentReference) == 1 && r.call.ContentReference != contentReference[0] {
		return true, ErrHeld
	}
	return true, nil
}

func (g *Gate) IdentityBinding(ctx context.Context) (IdentityBinding, error) {
	if g.Ready() != nil || ctx == nil {
		return IdentityBinding{}, ErrHeld
	}
	r, _ := ctx.Value(identityRequestKey{}).(*identityRequest)
	if r == nil || !r.active.Load() || r.gate != g {
		return IdentityBinding{}, ErrHeld
	}
	if admitted, err := g.RequireIdentity(ctx, r.call.Operation, r.call.Target); err != nil || !admitted {
		return IdentityBinding{}, ErrHeld
	}
	return r.binding, nil
}

// Only DoIdentity may enter a product Identity effect. The low-level B0
// Service contract remains backward compatible with its accepted fixtures.
func identityGateCallValid(ctx context.Context, g *Gate, call Call) bool {
	r, _ := ctx.Value(identityRequestKey{}).(*identityRequest)
	identity := strings.HasPrefix(call.Operation, "identity.") || call.Purpose == "IDENTITY_CHANGE"
	if !identity {
		return r == nil
	}
	if r == nil || !r.active.Load() || r.gate != g || r.call.Operation != call.Operation || call.Actor != "" {
		return false
	}
	target, ok := call.Target.(string)
	if !ok || target != r.call.Target {
		return false
	}
	rule, ok := identityRules[call.Operation]
	if !ok || (rule.mode == Read && call.Purpose != "CURRENT_READ") || (rule.mode == Write && call.Purpose != "IDENTITY_CHANGE") {
		return false
	}
	want := struct { Binding IdentityBinding; Reference string }{r.binding, r.call.ContentReference}
	return same(want, call.Content)
}
