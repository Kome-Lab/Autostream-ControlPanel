package v3authorization

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

func managementUnitBody(t *testing.T, kind AuthorityRecordKind, record any) []byte {
	t.Helper()
	raw, e := json.Marshal(map[string]any{"event_id": "event-1", "correlation_id": "correlation-1", "expected_revision": uint64(1),
		"scope": evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"}, "action": Action, "records": []any{record}})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestSQLAuthorityManagementClosedCodec(t *testing.T) {
	body := managementUnitBody(t, RealmRecord, RealmRegistration{Realm: "realm-1", State: "ACTIVE"})
	meta, records, e := DecodeManagementRecords(body, RealmRecord)
	if e != nil || len(records) != 1 || meta.ExpectedRevision != 1 || records[0].Realm.Realm != "realm-1" {
		t.Fatal(e)
	}
	cases := map[string]string{
		"extra":         strings.Replace(string(body), `"event_id":`, `"Actor":"forged","event_id":`, 1),
		"case":          strings.Replace(string(body), `"event_id":`, `"Event_id":`, 1),
		"duplicate":     strings.Replace(string(body), `"event_id":"event-1"`, `"event_id":"event-1","\u0065vent_id":"event-1"`, 1),
		"null":          strings.Replace(string(body), `"expected_revision":1`, `"expected_revision":null`, 1),
		"missing":       strings.Replace(string(body), `"expected_revision":1,`, "", 1),
		"fraction":      strings.Replace(string(body), `"expected_revision":1`, `"expected_revision":1.0`, 1),
		"exponent":      strings.Replace(string(body), `"expected_revision":1`, `"expected_revision":1e0`, 1),
		"negative":      strings.Replace(string(body), `"expected_revision":1`, `"expected_revision":-0`, 1),
		"overflow":      strings.Replace(string(body), `"expected_revision":1`, `"expected_revision":18446744073709551616`, 1),
		"payload-extra": strings.Replace(string(body), `"State":"ACTIVE"`, `"State":"ACTIVE","password":"secret"`, 1),
		"trailing":      string(body) + "{}", "utf8": string([]byte{0xff}), "size": strings.Repeat(" ", (64<<10)+1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if raw == string(body) {
				t.Fatal("fixture mutation failed")
			}
			if _, _, e := DecodeManagementRecords([]byte(raw), RealmRecord); e == nil {
				t.Fatal("invalid body accepted")
			}
		})
	}
	if _, _, e := DecodeManagementRecords(body, CapabilityRecord); e == nil {
		t.Fatal("route type bypass")
	}
}
func TestSQLAuthorityManagementGrantBounds(t *testing.T) {
	g := ManagementGrant{Principal: "user-1", Scope: evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"},
		Action: Action, State: "ACTIVE", Operations: []ManagementOperation{ManagementRead, ManagementRealm}, Subjects: []string{"user-1"}}
	body, e := encodeManagementGrant(g)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeManagementGrant(body)
	if e != nil || decoded.Principal != g.Principal {
		t.Fatal(e)
	}
	if managementSubject(g, "user-2") {
		t.Fatal("implicit other-principal permission")
	}
	g.Operations = append(g.Operations, ManagementDelegateSubjects)
	if !managementSubject(g, "user-2") {
		t.Fatal("explicit subject delegation lost")
	}
	for _, change := range []func(*ManagementGrant){
		func(g *ManagementGrant) { g.Operations = []ManagementOperation{"*"} },
		func(g *ManagementGrant) { g.Operations = []ManagementOperation{ManagementRead, ManagementRead} },
		func(g *ManagementGrant) { g.Subjects = []string{"user-1", "user-1"} },
		func(g *ManagementGrant) { g.Scope.Realm = "*" },
		func(g *ManagementGrant) { g.Action = "service.restart" },
		func(g *ManagementGrant) { g.State = "super_admin" },
	} {
		bad := g
		change(&bad)
		if _, e := encodeManagementGrant(bad); e == nil {
			t.Fatal("invalid grant accepted")
		}
	}
}
func TestSQLAuthorityManagementUnconfigured(t *testing.T) {
	s := NewManagementService(nil, nil)
	ctx := context.Background()
	meta := ManagementMetadata{EventID: "event-1", CorrelationID: "correlation-1", Action: Action}
	actor := ManagementActor{}
	for _, result := range []ManagementResult{s.Bootstrap(ctx, actor, meta, ManagementGrant{}), s.Apply(ctx, actor, meta, []AuthorityRecord{{Kind: RealmRecord, Realm: &RealmRegistration{Realm: "realm-1", State: "ACTIVE"}}}, nil), s.Inspect(ctx, actor, meta)} {
		if result.Status == ManagementApplied || result.Status == ManagementInspected {
			t.Fatal("unconfigured default accepted")
		}
	}
	if s.RecordTransportDenial(ctx, Principal{}, "raw-secret-error") == nil {
		t.Fatal("arbitrary audit code")
	}
}
func TestSQLAuthorityManagementSaveBindingGuard(t *testing.T) {
	a := NewSQLAuthority(nil)
	records := []AuthorityRecord{{Kind: RealmRecord, Realm: &RealmRegistration{Realm: "realm-1", State: "ACTIVE"}}}
	for _, binding := range []*managementSaveTransaction{{active: false}, {active: true, used: true}, {active: true, expected: 2}} {
		ctx := context.WithValue(context.Background(), managementSaveKey{}, binding)
		if _, e := a.Save(ctx, 1, records); e != ErrConflict {
			t.Fatal("retained/reused/mismatched context accepted", e)
		}
	}
}

func TestSQLAuthorityManagementQuarantineGate(t *testing.T) {
	s := NewManagementService(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := s.enter(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	done := make(chan error, 1)
	queued := managementObservedContext{Context: ctx, observed: waiting, once: &sync.Once{}}
	go func() {
		unlock, err := s.enter(queued, true)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("mutation never reached the gate")
	}
	s.uncertain.Store(true)
	release()
	if err := <-done; err != ErrStoreUnknown {
		t.Fatal("queued mutation bypassed commit uncertainty", err)
	}
	unlock, err := s.enter(ctx, false)
	if err != nil {
		t.Fatal("inspection cannot observe quarantined state", err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if release, err := s.enter(cancelled, false); err == nil || release != nil {
		t.Fatal("cancelled gate acquired successfully")
	}
	unlock()
	if _, err := (&ManagementService{}).enter(ctx, false); err == nil {
		t.Fatal("zero-value gate accepted")
	}
}

func TestSQLAuthorityManagementDelegationCannotAmplify(t *testing.T) {
	g := ManagementGrant{Principal: "user-1", Scope: evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"},
		Action: Action, State: "ACTIVE", Operations: []ManagementOperation{ManagementRead, ManagementAdministrator}, Subjects: []string{"user-1"}}
	next := g
	if !managementGrantDelegationAllowed(g, next, next.Scope, Action) {
		t.Fatal("same explicit permissions cannot be delegated")
	}
	next.ProvisionTargets = true
	if managementGrantDelegationAllowed(g, next, next.Scope, Action) {
		t.Fatal("target provisioning power was amplified")
	}
	g.ProvisionTargets = true
	if !managementGrantDelegationAllowed(g, next, next.Scope, Action) {
		t.Fatal("explicit target provisioning power was lost")
	}
	next.Operations = append([]ManagementOperation(nil), next.Operations...)
	next.Operations = append(next.Operations, ManagementPolicy)
	if managementGrantDelegationAllowed(g, next, next.Scope, Action) {
		t.Fatal("operation power was amplified")
	}
}
