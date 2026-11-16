package bootstrapapproval

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

func unitLimits() lifecycle.Limits {
	return lifecycle.Limits{MaxIDBytes: 128, MaxMembers: 16, MaxAxes: 16, MaxRefs: 32, MaxObligations: 16, MaxPartitions: 16, MaxRanges: 16, MaxDepth: 8}
}
func unitRequest() Request {
	return Request{Reference: Reference{ManifestID: "manifest", AttemptID: "attempt", Identity: lifecycle.IdentityContext{Class: lifecycle.Human, PrincipalKind: lifecycle.PrincipalHuman, ActorID: "human", Purpose: lifecycle.PurposeApproval}}, EventID: "attempt", CorrelationID: "correlation", Scope: evidence.Scope{Realm: "realm", Host: "host", Resource: "resource", Backend: "backend"}, Action: "management", Principal: "human", State: "ACTIVE", GrantBinding: strings.Repeat("a", 64), ExpectedRevision: 0, Operations: []string{"read", "administrator.write"}, Subjects: []string{"human"}}
}
func unitVerification(r Request) Verification {
	return Verification{Request: cloneRequest(r), Ceiling: Ceiling{Scope: r.Scope, Subject: r.Principal, Operations: append([]string(nil), r.Operations...), Subjects: append([]string(nil), r.Subjects...)}, OriginID: "fixture-origin", ReceiptID: "fixture-receipt", OriginRevision: 1, Knowledge: lifecycle.Known, Consumed: true}
}

func TestIUWS1ApprovalStructuralBoundary(t *testing.T) {
	r := unitRequest()
	v := unitVerification(r)
	if validate(r, unitLimits()) != nil || !verified(r, v, unitLimits()) {
		t.Fatal("explicit fixture metadata rejected")
	}
	if validate(r, lifecycle.Limits{}) == nil {
		t.Fatal("missing limits defaulted")
	}
	for _, class := range []lifecycle.IdentityClass{lifecycle.PendingIdentity, lifecycle.Preauth, lifecycle.System, "UNRECOGNIZED"} {
		next := cloneRequest(r)
		next.Reference.Identity.Class = class
		if validate(next, unitLimits()) == nil {
			t.Fatal("non-human class accepted", class)
		}
	}
	for _, purpose := range []lifecycle.Purpose{lifecycle.PurposeRecovery, lifecycle.PurposeIngress, lifecycle.PurposeResume, "UNRECOGNIZED"} {
		next := cloneRequest(r)
		next.Reference.Identity.Purpose = purpose
		if validate(next, unitLimits()) == nil {
			t.Fatal("nonapproval purpose accepted", purpose)
		}
	}
	changed := cloneRequest(r)
	changed.ProvisionTargets = true
	if verified(changed, v, unitLimits()) {
		t.Fatal("provisioning exceeded immutable request/ceiling")
	}
	changed = cloneRequest(r)
	changed.Operations = append(changed.Operations, "trust.write")
	if verified(changed, v, unitLimits()) {
		t.Fatal("operation exceeded explicit finite ceiling")
	}
	changed = cloneRequest(r)
	changed.Subjects = append(changed.Subjects, "other-human")
	if verified(changed, v, unitLimits()) {
		t.Fatal("subject exceeded explicit finite ceiling")
	}
	v.Knowledge = lifecycle.KnowledgeUnknown
	if verified(r, v, unitLimits()) {
		t.Fatal("unknown origin treated as consumed authority")
	}
	if (Inspection{}).AuthorizesMutation() || (Inspection{}).ProductionQualified() || (&Core{}).ProductionQualified() {
		t.Fatal("structure/inspection fixture qualified production")
	}
}

func TestIUWS1ApprovalPrivateParticipantRequired(t *testing.T) {
	ctx := context.Background()
	r := unitRequest()
	c := &Core{db: &sql.DB{}, limits: unitLimits()}
	if c.Consume(ctx, c.db, nil, r, 1) == nil {
		t.Fatal("plain DTO/context consumed without private verification")
	}
	view := &approvalView{core: c, verification: unitVerification(r), binding: Binding(r)}
	prepared := context.WithValue(ctx, approvalKey{}, view)
	if c.Consume(prepared, c.db, nil, r, 1) == nil {
		t.Fatal("sealed view accepted missing owner transaction")
	}
	if c.Consume(prepared, &sql.DB{}, nil, r, 1) == nil {
		t.Fatal("sealed view accepted another DB pool")
	}
	changed := cloneRequest(r)
	changed.CorrelationID = "other-correlation"
	if c.Consume(prepared, c.db, nil, changed, 1) == nil {
		t.Fatal("sealed context accepted changed immutable request")
	}
	if _, err := lf.ApprovalSQL(ctx, c.db, (*sql.Tx)(nil), Binding(r)); err == nil {
		t.Fatal("unbound raw SQL transaction accepted")
	}
	if view.used {
		t.Fatal("refusal consumed a private view before participant qualification")
	}
}

func TestIUWS1ApprovalImmutableClone(t *testing.T) {
	r := unitRequest()
	clone := cloneRequest(r)
	clone.Operations[0] = "trust.write"
	clone.Subjects[0] = "other-human"
	if r.Operations[0] != "read" || r.Subjects[0] != "human" {
		t.Fatal("mutable caller alias crossed private-view boundary")
	}
	v := unitVerification(r)
	snapshot := cloneVerification(v)
	snapshot.Request.Operations[0] = "trust.write"
	snapshot.Ceiling.Operations[0] = "trust.write"
	if v.Request.Operations[0] != "read" || v.Ceiling.Operations[0] != "read" {
		t.Fatal("verifier mutable alias survived snapshot")
	}
	reordered := cloneRequest(r)
	reordered.Operations[0], reordered.Operations[1] = reordered.Operations[1], reordered.Operations[0]
	if Binding(r) != Binding(reordered) {
		t.Fatal("canonical set reordering changed immutable content")
	}
	reordered.GrantBinding = strings.Repeat("b", 64)
	if Binding(r) == Binding(reordered) {
		t.Fatal("different canonical management grant had same request binding")
	}
}
