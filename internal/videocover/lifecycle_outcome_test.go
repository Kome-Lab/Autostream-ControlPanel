package videocover

import (
	"errors"
	"testing"
)

func TestIUWS2CoverObservationAuditRollback(t *testing.T) {
	m := NewMemoryRepository()
	if _, err := m.EnsureGeneration(t.Context(), "test-stream", 7, "test-variant", false); err != nil {
		t.Fatal(err)
	}
	request := ActionRequest{Active: true, ExpectedJobGeneration: 7, ExpectedRevision: 1, IdempotencyKey: "test-cover-key"}
	if _, err := m.PrepareAction(t.Context(), "test-stream", request); err != nil {
		t.Fatal(err)
	}
	expected, err := m.RecordAmbiguous(t.Context(), "test-stream", 7, request.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	auditFailure := errors.New("synthetic critical audit failure")
	if _, err = m.RecordObservation(t.Context(), expected, "applied", "", func() error { return auditFailure }); !errors.Is(err, auditFailure) {
		t.Fatal("audit failure lost")
	}
	state, err := m.GetCurrentState(t.Context(), "test-stream")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "confirming" || state.AppliedRevision != nil {
		t.Fatal("audit rollback changed runtime")
	}
	action, found, err := m.LookupActionOutcome(t.Context(), "test-stream", request)
	if err != nil || !found || action.Outcome != "confirming" {
		t.Fatal("audit rollback changed immutable action")
	}
	wrong := expected
	wrong.DesiredActive = false
	if _, err = m.RecordObservation(t.Context(), wrong, "applied", "", func() error { t.Fatal("refusal called audit"); return nil }); err == nil {
		t.Fatal("same key different contents accepted")
	}
	audits := 0
	committed, err := m.RecordObservation(t.Context(), expected, "applied", "", func() error { audits++; return nil })
	if err != nil || committed.Status != "applied" || audits != 1 {
		t.Fatal("matching observation did not commit")
	}
	if _, err = m.RecordObservation(t.Context(), expected, "applied", "", func() error { audits++; return nil }); err != nil || audits != 2 {
		t.Fatal("terminal observation skipped critical audit participant")
	}
	if _, err = m.EnsureGeneration(t.Context(), "test-stream", 8, "test-next-variant", false); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecordObservation(t.Context(), expected, "applied", "", func() error { t.Fatal("stale generation called audit"); return nil }); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("old generation accepted")
	}
}
