//go:build cp_producer_interop

// This fixture bridge is copied ONLY into a disposable checkout of the pinned
// Updater source. It is not part of the ControlPanel or Updater product build.
// All identities, databases, other twelve evidence kinds, integrity checks,
// clocks, current applicability and independent Continuity here are SYNTHETIC.
package registry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

type cpProducerFixture struct {
	Request   Request                   `json:"request"`
	Scope     evidence.Scope            `json:"scope"`
	Execution evidence.Execution        `json:"execution"`
	Frame     evidence.BackendReadFrame `json:"backend_read_frame"`
	Issuer    string                    `json:"issuer"`
	Audience  string                    `json:"audience"`
}

func cpProducerDirectory(t *testing.T) string {
	t.Helper()
	directory := os.Getenv("CP_INTEROP_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("synthetic interop directory must be explicitly configured")
	}
	return directory
}

func cpProducerExportValue(t *testing.T, f *contractsTestFixture) cpProducerFixture {
	t.Helper()
	if err := ValidateRequest(f.request); err != nil {
		t.Fatal("C10 fixture request does not validate", err)
	}
	legacy, err := CanonicalBinding(f.request.Command)
	if err != nil { t.Fatal("actual C10 canonical binding failed", err) }
	wire, err := json.Marshal(f.request)
	if err != nil { t.Fatal("cannot encode actual C10 request", err) }
	shared, err := evidence.DecodeRequest(wire)
	if err != nil { t.Fatal("shared Decoder rejected actual C10 request", err) }
	canonical, err := evidence.CanonicalBytes(shared.Command)
	if err != nil || string(canonical) != string(legacy) || shared.Binding != string(legacy) {
		t.Fatal("actual C10 and shared canonical bytes differ", err)
	}
	computed, err := evidence.ComputeBinding(shared.Command)
	if err != nil || computed != string(legacy) || evidence.VerifyBinding(shared.Command, string(legacy)) != nil {
		t.Fatal("actual C10 and shared binding validation differ", err)
	}
	encoded, err := evidence.EncodeRequest(shared)
	if err != nil || !bytes.Equal(encoded, wire) {
		t.Fatal("shared canonical Request wire differs from actual C10", err)
	}
	back, err := DecodeRequest(encoded)
	if err != nil || !reflect.DeepEqual(back, f.request) {
		t.Fatal("shared Request failed actual C10 validation or lost a field", err)
	}
	for _, raw := range f.delivery.encoded {
		envelope, err := evidence.Decode(raw)
		if err != nil {
			t.Fatal("invalid synthetic envelope", err)
		}
		if envelope.Kind == evidence.CPAuthorizationKind {
			return cpProducerFixture{Request: f.request, Scope: envelope.Scope, Execution: envelope.Execution,
				Frame: envelope.Frame, Issuer: envelope.Issuer, Audience: envelope.Audience}
		}
	}
	t.Fatal("synthetic C10 fixture has no CP authorization")
	return cpProducerFixture{}
}

// TestCPProducerExport obtains the request from the REAL C10 CanonicalBinding
// implementation. The CP package does not import the Updater internal package or
// treat this synthetic fixture as a production Binding authority.
func TestCPProducerExport(t *testing.T) {
	f := contractsTestNew(t)
	fixture := cpProducerExportValue(t, f)
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal("cannot serialize synthetic fixture", err)
	}
	if err := os.WriteFile(filepath.Join(cpProducerDirectory(t), "c10-fixture.json"), encoded, 0600); err != nil {
		t.Fatal("cannot write synthetic interop input", err)
	}
	t.Log("exported actual C10 request/CanonicalBinding with byte-exact shared Codec round-trip and synthetic target frame; no product authority established")
}

// TestCPProducerImport consumes the actual CP Service's persisted/read original
// envelope bytes. Its other evidence and verification dependencies remain test
// doubles. A green result proves this limited contract connection only.
func TestCPProducerImport(t *testing.T) {
	directory := cpProducerDirectory(t)
	encodedFixture, err := os.ReadFile(filepath.Join(directory, "c10-fixture.json"))
	if err != nil {
		t.Fatal("cannot read synthetic interop input", err)
	}
	var exported cpProducerFixture
	if err := json.Unmarshal(encodedFixture, &exported); err != nil {
		t.Fatal("invalid synthetic interop input", err)
	}
	f := contractsTestNew(t)
	if !reflect.DeepEqual(exported, cpProducerExportValue(t, f)) {
		t.Fatal("C10 input changed between fixture export and CP evidence import")
	}
	requestJSON, err := json.Marshal(exported.Request)
	if err != nil {
		t.Fatal(err)
	}
	if request, err := DecodeRequest(requestJSON); err != nil || !reflect.DeepEqual(request, f.request) {
		t.Fatal("CP flow did not retain the exact original C10 request")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "cp-envelope.json"))
	if err != nil {
		t.Fatal("cannot read CP Service output", err)
	}
	cp, err := evidence.Decode(raw)
	if err != nil {
		t.Fatal("CP Service output is not a canonical envelope", err)
	}
	if cp.Kind != evidence.CPAuthorizationKind || cp.CPAuthorization == nil || cp.CPAuthorization.Decision != "AUTHORIZED" ||
		cp.Issuer != exported.Issuer || cp.Audience != exported.Audience || cp.Scope != exported.Scope ||
		cp.Execution != exported.Execution || cp.Frame != exported.Frame || cp.Binding != string(f.request.Binding) {
		t.Fatal("CP Service output changed the original target, issuer, audience, execution, binding or frame")
	}
	canonical, err := evidence.Encode(cp)
	if err != nil || !bytes.Equal(canonical, raw) {
		t.Fatal("CP Service did not preserve canonical issued bytes")
	}
	if cp.CPAuthorization.PolicyRevision != string(f.request.Command.RequiredGuard.Policy) ||
		cp.CPAuthorization.TrustRevision != string(f.request.Command.RequiredGuard.Trust) {
		t.Fatal("CP authorization revisions differ from the original request")
	}
	replaced := false
	for index, previous := range f.delivery.encoded {
		envelope, err := evidence.Decode(previous)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Kind == evidence.CPAuthorizationKind {
			f.delivery.encoded[index] = append([]byte(nil), raw...)
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("synthetic C10 delivery lacks CP authorization")
	}
	// These are synthetic policy/trust Producers, not CP claims of their own
	// authority. Correlate them to the actual CP-issued Evidence ID.
	f.change(t, evidence.PolicyKind, func(e *evidence.Envelope) { e.Policy.SubjectEvidenceID = cp.EvidenceID })
	f.change(t, evidence.TrustKind, func(e *evidence.Envelope) { e.Trust.SubjectEvidenceID = cp.EvidenceID })
	f.issue(t, true)
	f.open(t)
	if result := f.check(t); result.Status != PreflightEligible {
		t.Fatalf("actual CP Service bytes failed C10 boundary: %s/%s", result.Status, result.Reason)
	}
	f.verification.mu.Lock()
	integrity, validity, revocation := f.verification.integrityCalls, f.verification.validityCalls, f.verification.revocationCalls
	f.verification.mu.Unlock()
	if integrity != 13 || validity != 13 || revocation != 13 {
		t.Fatal("C10 did not verify all thirteen complete envelopes through separate hooks")
	}
	if f.port.owner.writerConfigured || f.port.owner.writePool != nil {
		t.Fatal("interop read unexpectedly admitted a Writer")
	}
	// Mutate the CP payload WITHOUT registering new issued/authenticated bytes.
	// The Codec still accepts this shape, so rejection must occur at the actual
	// complete-payload authentication boundary rather than syntax validation.
	f.change(t, evidence.CPAuthorizationKind, func(e *evidence.Envelope) { e.CPAuthorization.Decision = "DENIED" })
	if result := f.check(t); result.Status != PreflightUnknown {
		t.Fatalf("modified CP payload became %s/%s instead of UNKNOWN", result.Status, result.Reason)
	}
	t.Log("actual CP issue/store/read bytes accepted; modified payload rejected; other 12 Producers/integrity/time/revocation/Continuity are synthetic only")
}
