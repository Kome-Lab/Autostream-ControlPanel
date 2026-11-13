package v3authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

// Requests and every authority/security/time dependency in these tests are
// synthetic. The Resolver, Service and SQLStore themselves are product code.
func resolverTestCommand() evidence.Command {
	return evidence.Command{
		Schema: evidence.CanonicalRequestVersion, Operation: evidence.AuthorizationAction,
		Scope:               evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"},
		Execution:           evidence.Execution{RequestID: "request-1", ExecutionKey: "execution-1", OperationID: "operation-1", AttemptID: "attempt-1", StepID: "step-1"},
		RequestingPrincipal: "operator-1", AuthorizationIssuer: "cp-1", CandidateRevision: "candidate-1", CandidateContentPin: "content-pin-1",
		Expected:      evidence.ExpectedReference{Revision: "expected-1", StateVersion: 1<<63 + 11},
		RequiredGuard: evidence.RequiredGuard{Policy: "policy-1", Trust: "trust-1", Membership: "membership-1", OwnershipEpoch: "owner-1", FenceEpoch: "fence-1", Generation: "generation-1", BootID: "boot-1", ProcessIncarnation: "incarnation-1"},
		Timing:        evidence.CommandTiming{TimeBase: "clock-1", DeadlineTick: 1<<63 + 19}, OriginalBudget: "budget-1",
	}
}

// The independent expected view enumerates every semantic field. Its values are
// deliberately distinct, so swapped guard/identity/revision fields are visible.
func resolverTestExpectedIntent() Intent {
	return Intent{
		Scope:     evidence.Scope{Realm: "realm-1", Host: "host-1", Resource: "resource-1", Backend: "backend-1"},
		Execution: evidence.Execution{RequestID: "request-1", ExecutionKey: "execution-1", OperationID: "operation-1", AttemptID: "attempt-1", StepID: "step-1"},
		Action:    "registry.reference_only", RequestingPrincipal: "operator-1", AuthorizationIssuer: "cp-1", CandidateRevision: "candidate-1", CandidateContentPin: "content-pin-1",
		ExpectedRevision: "expected-1", ExpectedStateVersion: 1<<63 + 11,
		Required: GuardRequirement{Policy: "policy-1", Trust: "trust-1", Membership: "membership-1", OwnershipEpoch: "owner-1", FenceEpoch: "fence-1", Generation: "generation-1", BootID: "boot-1", ProcessIncarnation: "incarnation-1"},
		TimeBase: "clock-1", DeadlineTick: 1<<63 + 19, OriginalBudget: "budget-1",
	}
}

func resolverTestRequest(t *testing.T, c evidence.Command) (evidence.Request, json.RawMessage) {
	t.Helper()
	binding, err := evidence.ComputeBinding(c)
	if err != nil {
		t.Fatal(err)
	}
	request := evidence.Request{Command: c, Binding: binding}
	wire, err := evidence.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return request, json.RawMessage(wire)
}

func resolverTestFixture(t *testing.T, c evidence.Command) *serviceFixture {
	t.Helper()
	request, raw := resolverTestRequest(t, c)
	intent, err := NewCanonicalBindingResolver().Resolve(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Binding != request.Binding {
		t.Fatal("product resolver changed shared Binding")
	}
	f := makeTestFixture(t)
	f.request = IssueRequest{OriginalRequest: raw, Frame: evidence.BackendReadFrame{Issuer: c.Scope.Backend, ServingIncarnation: "backend-process-1", Generation: c.RequiredGuard.Generation, SnapshotID: "snapshot-1", ObservationProof: "observation-1"}}
	configureIntent(f, intent)
	f.deps.Resolver = NewCanonicalBindingResolver()
	f.service = NewService(Config{Issuer: c.AuthorizationIssuer, Audience: "executor"}, f.deps)
	return f
}

func TestCanonicalResolverMapsCompleteSharedRequest(t *testing.T) {
	c := resolverTestCommand()
	request, wire := resolverTestRequest(t, c)
	got, err := NewCanonicalBindingResolver().Resolve(context.Background(), wire)
	if err != nil {
		t.Fatal(err)
	}
	expected := resolverTestExpectedIntent()
	expected.Binding = request.Binding
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("product resolver lost, swapped or reinterpreted a semantic field")
	}
	canonical, err := evidence.CanonicalBytes(c)
	if err != nil || got.Binding != string(canonical) {
		t.Fatal("product resolver changed the exact shared canonical bytes", err)
	}
	if err := evidence.VerifyBinding(c, got.Binding); err != nil {
		t.Fatal(err)
	}
	// Resolver retains no dependency on mutable caller byte storage.
	wire[0] = 'x'
	if got != expected {
		t.Fatal("caller changed resolved semantic intent")
	}
}

type resolverFieldMapping struct{ command, intent string }

func resolverTestFieldMappings() []resolverFieldMapping {
	return []resolverFieldMapping{
		{"Scope.Realm", "Scope.Realm"}, {"Scope.Host", "Scope.Host"}, {"Scope.Resource", "Scope.Resource"}, {"Scope.Backend", "Scope.Backend"},
		{"Execution.RequestID", "Execution.RequestID"}, {"Execution.ExecutionKey", "Execution.ExecutionKey"}, {"Execution.OperationID", "Execution.OperationID"}, {"Execution.AttemptID", "Execution.AttemptID"}, {"Execution.StepID", "Execution.StepID"},
		{"RequestingPrincipal", "RequestingPrincipal"}, {"AuthorizationIssuer", "AuthorizationIssuer"}, {"CandidateRevision", "CandidateRevision"}, {"CandidateContentPin", "CandidateContentPin"},
		{"Expected.Revision", "ExpectedRevision"}, {"Expected.StateVersion", "ExpectedStateVersion"},
		{"RequiredGuard.Policy", "Required.Policy"}, {"RequiredGuard.Trust", "Required.Trust"}, {"RequiredGuard.Membership", "Required.Membership"},
		{"RequiredGuard.OwnershipEpoch", "Required.OwnershipEpoch"}, {"RequiredGuard.FenceEpoch", "Required.FenceEpoch"}, {"RequiredGuard.Generation", "Required.Generation"},
		{"RequiredGuard.BootID", "Required.BootID"}, {"RequiredGuard.ProcessIncarnation", "Required.ProcessIncarnation"},
		{"Timing.TimeBase", "TimeBase"}, {"Timing.DeadlineTick", "DeadlineTick"}, {"OriginalBudget", "OriginalBudget"},
	}
}
func resolverTestField(v reflect.Value, path string) reflect.Value {
	for _, name := range strings.Split(path, ".") {
		v = v.FieldByName(name)
	}
	return v
}

func TestCanonicalResolverEveryMeaningFieldMapping(t *testing.T) {
	mappings := resolverTestFieldMappings()
	if len(mappings) != 26 {
		t.Fatal("semantic field denominator changed")
	}
	for _, mapping := range mappings {
		t.Run(mapping.command, func(t *testing.T) {
			c := resolverTestCommand()
			want := resolverTestExpectedIntent()
			source := resolverTestField(reflect.ValueOf(&c).Elem(), mapping.command)
			target := resolverTestField(reflect.ValueOf(&want).Elem(), mapping.intent)
			if !source.IsValid() || !target.IsValid() {
				t.Fatal("mapping refers to nonexistent field")
			}
			switch source.Kind() {
			case reflect.String:
				source.SetString(source.String() + "-changed")
				target.SetString(source.String())
			case reflect.Uint64:
				source.SetUint(source.Uint() + 1)
				target.SetUint(source.Uint())
			default:
				t.Fatal("new scalar kind needs an explicit mapping")
			}
			request, wire := resolverTestRequest(t, c)
			want.Binding = request.Binding
			got, err := NewCanonicalBindingResolver().Resolve(context.Background(), wire)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("changed canonical meaning was dropped or transformed", err)
			}
		})
	}
}

func TestCanonicalResolverNumericBoundaries(t *testing.T) {
	for _, value := range []uint64{0, 1, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64 - 1} {
		c := resolverTestCommand()
		c.Expected.StateVersion = value
		c.Timing.DeadlineTick = math.MaxUint64
		_, wire := resolverTestRequest(t, c)
		intent, err := NewCanonicalBindingResolver().Resolve(context.Background(), wire)
		if err != nil || intent.ExpectedStateVersion != value || intent.DeadlineTick != math.MaxUint64 {
			t.Fatal("resolver rounded or narrowed a uint64", err)
		}
	}
}

func TestCanonicalResolverRejectsStrictShapeAndStaleBinding(t *testing.T) {
	request, raw := resolverTestRequest(t, resolverTestCommand())
	wire := []byte(raw)
	cases := map[string][]byte{
		"unknown-version":        bytes.Replace(wire, []byte(`"Schema":1`), []byte(`"Schema":2`), 1),
		"duplicate":              bytes.Replace(wire, []byte(`"Schema":1`), []byte(`"Schema":1,"Schema":1`), 1),
		"escaped-duplicate":      bytes.Replace(wire, []byte(`"Schema":1`), []byte(`"Schema":1,"\u0053chema":1`), 1),
		"extra":                  bytes.Replace(wire, []byte(`"Command":{`), []byte(`"Command":{"Extra":"not-allowed",`), 1),
		"null":                   bytes.Replace(wire, []byte(`"Schema":1`), []byte(`"Schema":null`), 1),
		"missing":                bytes.Replace(wire, []byte(`"Schema":1,`), nil, 1),
		"wrong-case":             bytes.Replace(wire, []byte(`"Command":`), []byte(`"command":`), 1),
		"fraction":               bytes.Replace(wire, []byte(`"Schema":1`), []byte(`"Schema":1.0`), 1),
		"overflow":               bytes.Replace(wire, []byte(`"DeadlineTick":9223372036854775827`), []byte(`"DeadlineTick":18446744073709551616`), 1),
		"binding":                bytes.Replace(wire, []byte(`"Binding":"`), []byte(`"Binding":"tampered-`), 1),
		"candidate-content":      bytes.Replace(wire, []byte(`"CandidateContentPin":"content-pin-1"`), []byte(`"CandidateContentPin":"content-pin-2"`), 1),
		"expected-state":         bytes.Replace(wire, []byte(`"StateVersion":9223372036854775819`), []byte(`"StateVersion":8`), 1),
		"required-guard-missing": bytes.Replace(wire, []byte(`"Policy":"policy-1",`), nil, 1),
		"required-guard-changed": bytes.Replace(wire, []byte(`"Trust":"trust-1"`), []byte(`"Trust":"trust-2"`), 1),
		"scope":                  bytes.Replace(wire, []byte(`"Host":"host-1"`), []byte(`"Host":"host-2"`), 1),
		"execution":              bytes.Replace(wire, []byte(`"AttemptID":"attempt-1"`), []byte(`"AttemptID":"attempt-2"`), 1),
		"trailing":               append(bytes.Clone(wire), []byte(` {}`)...),
		"empty":                  nil,
		"invalid-utf8":           bytes.Replace(wire, []byte(`"cp-1"`), []byte{'"', 0xff, '"'}, 1),
	}
	for name, proposal := range cases {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(proposal, wire) {
				t.Fatal("invalid fixture failed to change source bytes")
			}
			got, err := NewCanonicalBindingResolver().Resolve(context.Background(), proposal)
			if err == nil || got != (Intent{}) {
				t.Fatal("unqualified input produced a semantic intent")
			}
		})
	}
	// Different input ordering/ASCII escape spelling is accepted by the shared
	// Decoder but never creates a different binding or a CP-specific encoding.
	alternate := bytes.Replace(wire, []byte(`"Scope":`), []byte(`"Scop\u0065":`), 1)
	got, err := NewCanonicalBindingResolver().Resolve(context.Background(), alternate)
	if err != nil || got.Binding != request.Binding {
		t.Fatal("shared-compatible ASCII escape changed binding", err)
	}
}

func TestCanonicalResolverCancellationAndParallelPurity(t *testing.T) {
	_, raw := resolverTestRequest(t, resolverTestCommand())
	resolver := NewCanonicalBindingResolver()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if intent, err := resolver.Resolve(canceled, raw); err == nil || intent != (Intent{}) {
		t.Fatal("canceled caller obtained intent")
	}
	var wg sync.WaitGroup
	failures := make(chan string, 16)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 10; iteration++ {
				got, err := resolver.Resolve(context.Background(), raw)
				if err != nil || got.Binding == "" {
					failures <- "parallel resolver changed results"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

func TestCanonicalResolverServiceIssueSaveReadAndReplay(t *testing.T) {
	f := resolverTestFixture(t, resolverTestCommand())
	issued := f.issue()
	if issued.Status != Issued || issued.AuthorizesExecution {
		t.Fatal(issued)
	}
	if f.authority.observed != f.intent {
		t.Fatal("business authority did not receive the complete shared semantic intent")
	}
	envelope, err := evidence.Decode(issued.Envelope)
	if err != nil || envelope.Binding != f.intent.Binding {
		t.Fatal("saved evidence lost canonical binding", err)
	}
	for n := 0; n < 3; n++ {
		read := f.service.Read(context.Background(), f.principal, f.lookup())
		if read.Status != Replay || !bytes.Equal(read.Envelope, issued.Envelope) {
			t.Fatal("read changed saved bytes", read)
		}
	}
	replay := f.issue()
	if replay.Status != Replay || !bytes.Equal(replay.Envelope, issued.Envelope) || f.security.seals != 1 || f.store.finishes != 1 {
		t.Fatal("replay reissued or renewed evidence")
	}
}

func TestCanonicalResolverServiceRequiresAuthenticationAndBusinessAuthority(t *testing.T) {
	for _, name := range []string{"principal", "issuer", "scope", "execution", "candidate", "expected-state", "policy", "trust", "binding", "business-denied"} {
		t.Run(name, func(t *testing.T) {
			c := resolverTestCommand()
			f := resolverTestFixture(t, c)
			switch name {
			case "principal":
				c.RequestingPrincipal = "different-principal"
			case "issuer":
				c.AuthorizationIssuer = "different-issuer"
			case "scope":
				c.Scope.Host = "different-host"
			case "execution":
				c.Execution.AttemptID = "different-attempt"
				f.authority.decision.State = "DENIED"
			case "candidate":
				c.CandidateContentPin = "different-content"
				f.authority.decision.State = "DENIED"
			case "expected-state":
				c.Expected.StateVersion++
				f.authority.decision.State = "DENIED"
			case "policy":
				c.RequiredGuard.Policy = "different-policy"
			case "trust":
				c.RequiredGuard.Trust = "different-trust"
			case "business-denied":
				f.authority.decision.State = "DENIED"
			}
			_, wire := resolverTestRequest(t, c)
			if name == "binding" {
				wire = bytes.Replace(wire, []byte(`"Binding":"`), []byte(`"Binding":"invalid-`), 1)
			}
			result := f.service.Issue(context.Background(), f.principal, IssueRequest{OriginalRequest: wire, Frame: f.request.Frame})
			if result.Status == Issued || result.Status == Replay || len(result.Envelope) != 0 || f.security.seals != 0 || f.store.claims != 0 {
				t.Fatal("canonical validation bypassed business or principal checks", result)
			}
		})
	}
}

func TestCanonicalResolverDoesNotEnableMissingProductionDependencies(t *testing.T) {
	_, raw := resolverTestRequest(t, resolverTestCommand())
	f := resolverTestFixture(t, resolverTestCommand())
	for _, name := range []string{"authority", "integrity", "validity", "applicability"} {
		t.Run(name, func(t *testing.T) {
			deps := f.deps
			switch name {
			case "authority":
				deps.Authority = nil
			case "integrity":
				deps.Integrity = nil
			case "validity":
				deps.Validity = nil
			case "applicability":
				deps.Applicability = nil
			}
			service := NewService(f.service.config, deps)
			result := service.Issue(context.Background(), f.principal, IssueRequest{OriginalRequest: raw, Frame: f.request.Frame})
			if result.Status != Unavailable || len(result.Envelope) != 0 || f.store.claims != 0 {
				t.Fatal("shared resolver enabled unconfigured production issuance", result)
			}
		})
	}
}

func TestCanonicalResolverSQLStoreServiceVerticalSlice(t *testing.T) {
	db := sqlTestDB(t)
	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	f := resolverTestFixture(t, resolverTestCommand())
	f.deps.Store = NewSQLStore(db)
	f.service = NewService(f.service.config, f.deps)
	issued := f.issue()
	if issued.Status != Issued {
		t.Fatal(issued)
	}
	var before []byte
	if err := db.QueryRow(`SELECT record_bytes FROM v3_cp_authorization_issuances`).Scan(&before); err != nil {
		t.Fatal("read synthetic durable issuance")
	}
	for n := 0; n < 3; n++ {
		read := f.service.Read(context.Background(), f.principal, f.lookup())
		if read.Status != Replay || !bytes.Equal(read.Envelope, issued.Envelope) {
			t.Fatal("real SQLStore read changed original canonical evidence", read)
		}
	}
	replay := f.issue()
	if replay.Status != Replay || !bytes.Equal(replay.Envelope, issued.Envelope) || f.security.seals != 1 {
		t.Fatal("SQL replay regenerated canonical evidence")
	}
	var after []byte
	if err := db.QueryRow(`SELECT record_bytes FROM v3_cp_authorization_issuances`).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("read/replay modified durable original bytes")
	}
	wrong := f.lookup()
	wrong.Scope.Resource = "different-resource"
	if result := f.service.Read(context.Background(), f.principal, wrong); result.Status != Denied || len(result.Envelope) != 0 {
		t.Fatal("evidence identity allowed cross-scope read", result)
	}
}

func TestCanonicalResolverInputSizeBounds(t *testing.T) {
	_, raw := resolverTestRequest(t, resolverTestCommand())
	atLimit := append([]byte(strings.Repeat(" ", evidence.MaxRequestBytes-len(raw))), raw...)
	resolver := NewCanonicalBindingResolver()
	if _, err := resolver.Resolve(context.Background(), atLimit); err != nil {
		t.Fatal("shared exact size boundary was narrowed", err)
	}
	if intent, err := resolver.Resolve(context.Background(), append(atLimit, ' ')); err == nil || intent != (Intent{}) {
		t.Fatal("oversized input produced an intent")
	}
	if intent, err := resolver.Resolve(context.Background(), nil); err == nil || intent != (Intent{}) {
		t.Fatal("empty input produced an intent")
	}
}
