package videocover

import "testing"

func TestUI188RecordedActionIsImmutableAndHistorical(t *testing.T) {
	m := NewMemoryRepository()
	state, err := m.EnsureGeneration(t.Context(), "stream", 1, "A", false)
	if err != nil {
		t.Fatal(err)
	}
	request := ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: state.DesiredRevision, IdempotencyKey: "old"}
	prepared, err := m.PrepareAction(t.Context(), "stream", request)
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := m.RecordAmbiguous(t.Context(), "stream", 1, "old")
	if err != nil || ambiguous.RecordedAction == nil || ambiguous.RecordedAction.Outcome != "confirming" {
		t.Fatalf("ambiguous: %+v %v", ambiguous, err)
	}
	repeated, err := m.RecordAmbiguous(t.Context(), "stream", 1, "old")
	if err != nil || repeated.RecordedAction != nil {
		t.Fatalf("duplicate phase: %+v %v", repeated, err)
	}
	// A delayed historical acknowledgement must not mutate a newer runtime.
	m.mu.Lock()
	newer := m.states["stream"][1]
	newer.LastIdempotencyKey = "new"
	newer.DesiredRevision++
	newer.Status = "confirming"
	m.states["stream"][1] = newer
	m.mu.Unlock()
	confirmed, err := m.RecordApplied(t.Context(), "stream", 1, "old", true, prepared.RequestedRevision)
	if err != nil || confirmed.RecordedAction == nil || confirmed.RecordedAction.Outcome != "applied" || confirmed.RecordedAction.RequestedRevision != prepared.RequestedRevision {
		t.Fatalf("historical: %+v %v", confirmed, err)
	}
	if confirmed.LastIdempotencyKey != "new" || confirmed.Status != "confirming" || ambiguous.RecordedAction.Outcome != "confirming" {
		t.Fatal("runtime or immutable prior phase changed")
	}
	duplicate, err := m.RecordApplied(t.Context(), "stream", 1, "old", true, prepared.RequestedRevision)
	if err != nil || duplicate.RecordedAction != nil {
		t.Fatalf("duplicate terminal: %+v %v", duplicate, err)
	}
}
