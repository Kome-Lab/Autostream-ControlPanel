package store

import (
	"testing"
	"time"
)

func TestUI188NormalStopClaimCannotCompleteNewExecution(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "stop epochs")
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamStatus(t.Context(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	source := stream.UpdatedAt.Format(time.RFC3339Nano)
	old, claimed, err := streams.ClaimNormalStop(t.Context(), stream)
	if err != nil || !claimed {
		t.Fatal("old claim failed")
	}
	forced, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := streams.CompleteConfirmedForceStop(t.Context(), stream.ID, forced.UpdatedAt); err != nil || !ok {
		t.Fatal("confirmed force stop failed")
	}
	next, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	current, claimed, err := streams.ClaimNormalStop(t.Context(), next)
	if err != nil || !claimed {
		t.Fatal("new claim failed")
	}
	if _, ok, err := streams.CompleteNormalStop(t.Context(), stream.ID, source, old.UpdatedAt); err != nil || ok {
		t.Fatal("old stop completed new execution")
	}
	if _, ok, err := streams.FailNormalStop(t.Context(), stream.ID, old.UpdatedAt); err != nil || ok {
		t.Fatal("old failure changed new execution")
	}
	saved, _ := streams.GetStream(t.Context(), stream.ID)
	if saved.Status != "stopping" || !saved.UpdatedAt.Equal(current.UpdatedAt) || !streams.normalStopPending[stream.ID] {
		t.Fatal("stale result removed current ownership fence")
	}
	if found, _ := streams.HasNormalStopReceipt(t.Context(), stream.ID, source); found {
		t.Fatal("stale stop forged positive receipt")
	}
	if _, ok, err := streams.CompleteNormalStop(t.Context(), stream.ID, next.UpdatedAt.Format(time.RFC3339Nano), current.UpdatedAt); err != nil || !ok || streams.normalStopPending[stream.ID] {
		t.Fatal("current confirmed stop did not clear only its fence")
	}
}
