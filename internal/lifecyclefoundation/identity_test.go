package lifecyclefoundation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func identityTestAuthorization(operation string, class IdentityClass) Authorization {
	rule := identityRules[operation]
	purpose := "IDENTITY_CHANGE"
	if rule.mode == Read {
		purpose = "CURRENT_READ"
	}
	b := IdentityBinding{Class: class, Target: "user-1", Flow: "flow-1", ProofReference: "verified-reference-1", Coverage: IdentityFixtureCoverage, Revision: 1}
	kind := "HUMAN"
	if class == IdentityPreauth {
		kind = "PREAUTH"
	} else if class == IdentitySystem {
		kind = "SYSTEM"
	}
	intent := Intent{Mode: rule.mode, Purpose: purpose, Operation: operation, OperationContent: "reference-1", Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(b.Target)}, Subject: "product"}
	return Authorization{Actor: "trusted-actor-1", PrincipalKind: kind, Intent: intent, Identity: &b}
}

func TestB2IdentityClassPurposeBoundary(t *testing.T) {
	for operation, rule := range identityRules {
		for _, class := range []IdentityClass{IdentityHuman, IdentityPending, IdentityPreauth, IdentitySystem, "WORKLOAD", ""} {
			t.Run(operation+"-"+string(class), func(t *testing.T) {
				a := identityTestAuthorization(operation, class)
				want := identityClassCode(class) != "" && strings.Contains(rule.classes, identityClassCode(class))
				if (validateAuthorization(a, a.Intent) == nil) != want {
					t.Fatal("exact operation/class contract violated")
				}
			})
		}
	}
}

func TestB2IdentityBindingRejectsStaleOrMissingContext(t *testing.T) {
	for name, mutate := range map[string]func(*Authorization){
		"no-binding": func(a *Authorization) { a.Identity = nil },
		"wrong-kind": func(a *Authorization) { a.PrincipalKind = "HUMAN" },
		"no-proof": func(a *Authorization) { a.Identity.ProofReference = "" },
		"no-flow": func(a *Authorization) { a.Identity.Flow = "" },
		"zero-revision": func(a *Authorization) { a.Identity.Revision = 0 },
		"real-profile": func(a *Authorization) { a.Identity.Coverage = "real-secret-profile" },
		"other-target": func(a *Authorization) { a.Identity.Target = "user-2" },
		"wrong-purpose": func(a *Authorization) { a.Intent.Purpose = "AUTHORITY_CHANGE" },
		"read-mode": func(a *Authorization) { a.Intent.Mode = Read },
		"approval-reuse": func(a *Authorization) { a.Approval = &Approval{Origin: "origin-1", ID: "approval-1", Manifest: "manifest-1", Purpose: a.Intent.Purpose} },
	} {
		t.Run(name, func(t *testing.T) {
			a := identityTestAuthorization("identity.session.issue_login", IdentityPreauth)
			mutate(&a)
			if validateAuthorization(a, a.Intent) == nil {
				t.Fatal("invalid identity trust context accepted")
			}
		})
	}
}

func TestB2IdentityNonHumanCannotAcquireAuthority(t *testing.T) {
	for _, operation := range []string{"authority.save", "management.apply", "evidence.finish", "bootstrap.initialize", "identity.cleanup.root", "identity.user.status.extra"} {
		for _, class := range []IdentityClass{IdentityPreauth, IdentitySystem} {
			a := identityTestAuthorization("identity.cleanup.sessions", class)
			a.Intent.Operation = operation
			if validateAuthorization(a, a.Intent) == nil {
				t.Fatalf("non-Human acquired %s", operation)
			}
		}
	}
	legacy := Authorization{Actor: "human-1", PrincipalKind: "HUMAN", Intent: Intent{Mode: Read, Purpose: "CURRENT_READ", Operation: "authority.read", OperationContent: "ref-1", Scope: Scope{Type: "GLOBAL"}, Subject: "product"}}
	if validateAuthorization(legacy, legacy.Intent) != nil {
		t.Fatal("existing Human admission changed")
	}
}

func TestB2IdentityCanonicalCompatibility(t *testing.T) {
	a := Authorization{Actor: "human-1", PrincipalKind: "HUMAN", Intent: Intent{Mode: Read, Purpose: "CURRENT_READ", Operation: "authority.read", OperationContent: "ref-1", Scope: Scope{Type: "GLOBAL"}, Subject: "product"}}
	old := struct {
		Actor string
		PrincipalKind string
		Intent Intent
		Approval *Approval
	}{a.Actor, a.PrincipalKind, a.Intent, a.Approval}
	if encode(old) != encode(a) || strings.Contains(encode(a), "Identity") {
		t.Fatal("old B0/B1 canonical authorization bytes changed")
	}
	var parsed Authorization
	if decode(encode(old), &parsed) != nil || !same(a, parsed) {
		t.Fatal("old record no longer decodes canonically")
	}
	b := identityTestAuthorization("identity.session.issue_login", IdentityPreauth)
	if decode(encode(b), &parsed) != nil || !same(b, parsed) {
		t.Fatal("Identity binding did not survive canonical freeze")
	}
}

func TestB2IdentityRequiredComposition(t *testing.T) {
	for _, g := range []*Gate{nil, NewRequiredGate(), {}} {
		called := false
		err := g.DoIdentity(context.Background(), IdentityCall{Operation: "identity.user.status", Target: "user-1"}, func(context.Context) error { called = true; return nil })
		if !errors.Is(err, ErrHeld) || called {
			t.Fatal("unconfigured Identity provider executed mutation")
		}
	}
	if ProductStartup("identity-fixture-v1") == nil || ProductStartup("required") == nil || ProductStartup("off") != nil {
		t.Fatal("a setting enabled fixture/product L2")
	}
	if _, _, err := IdentityRequest(context.Background()); !errors.Is(err, ErrHeld) {
		t.Fatal("HTTP context manufactured an Identity proof")
	}
}

func TestB2IdentityCheckpointTransition(t *testing.T) {
	before := Checkpoint{OperationalRevision: 3, AuthorityEpoch: 2, IdentityRevision: 8, Content: "old", HistoryHead: "old-history"}
	after := Checkpoint{OperationalRevision: 10, AuthorityEpoch: 2, IdentityRevision: 9, Content: "new", HistoryHead: "new-history"}
	if !nextCheckpoint(before, after, Intent{Mode: Write, Purpose: "IDENTITY_CHANGE"}) {
		t.Fatal("actual multi-trigger Operational revision was assumed to increment once")
	}
	after.IdentityRevision = before.IdentityRevision
	if nextCheckpoint(before, after, Intent{Mode: Write, Purpose: "IDENTITY_CHANGE"}) {
		t.Fatal("Identity change omitted revision CAS")
	}
	if !nextCheckpoint(before, before, Intent{Mode: Read, Purpose: "CURRENT_READ"}) || nextCheckpoint(before, after, Intent{Mode: Read, Purpose: "CURRENT_READ"}) {
		t.Fatal("read checkpoint advanced or changed state")
	}
}

func TestB2IdentityPrivateRequestLifetime(t *testing.T) {
	g := &Gate{}
	a := identityTestAuthorization("identity.user.status", IdentityHuman)
	request := &identityRequest{gate: g, call: IdentityCall{Operation: a.Intent.Operation, Target: a.Identity.Target, ContentReference: "effect-1"}, binding: *a.Identity}
	request.active.Store(true)
	ctx := context.WithValue(context.Background(), identityRequestKey{}, request)
	call := Call{Operation: request.call.Operation, Purpose: "IDENTITY_CHANGE", Target: request.call.Target,
		Content: struct { Binding IdentityBinding; Reference string }{request.binding, request.call.ContentReference}}
	if !identityGateCallValid(ctx, g, call) {
		t.Fatal("exact private identity call was refused")
	}
	for name, mutate := range map[string]func(*Call){
		"other-operation": func(c *Call) { c.Operation = "identity.user.delete" },
		"authority-reuse": func(c *Call) { c.Operation = "authority.save"; c.Purpose = "AUTHORITY_CHANGE" },
		"other-target": func(c *Call) { c.Target = "user-2" },
		"other-effect": func(c *Call) { c.Content = "other-effect" },
		"actor-input": func(c *Call) { c.Actor = "untrusted-actor" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := call
			mutate(&changed)
			if identityGateCallValid(ctx, g, changed) {
				t.Fatal("a foreign product call reused Identity admission")
			}
		})
	}
	if identityGateCallValid(ctx, &Gate{}, call) || identityGateCallValid(context.Background(), g, call) {
		t.Fatal("Identity admission could be manufactured or moved to a different Gate")
	}
	request.active.Store(false)
	if identityGateCallValid(ctx, g, call) {
		t.Fatal("retained private request survived completion")
	}
	if _, _, err := IdentityRequest(ctx); !errors.Is(err, ErrHeld) {
		t.Fatal("expired request exposed its proof references")
	}
}
