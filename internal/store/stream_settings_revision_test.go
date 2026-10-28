package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStreamSettingsRevisionCanonicalAndMonotonic(t *testing.T) {
	previous := time.Date(2026, 10, 28, 0, 0, 0, 123456000, time.UTC)
	for _, now := range []time.Time{previous, previous.Add(999 * time.Nanosecond), previous.Add(-time.Second)} {
		if next := nextStreamSettingsTime(previous, now); !next.Equal(previous.Add(time.Microsecond)) {
			t.Fatalf("same database precision must advance once: previous=%s next=%s", previous, next)
		}
	}
	if got := StreamSettingsRevisionFromTime(previous.In(time.FixedZone("offset", 9*60*60))); got != "2026-10-28T00:00:00.123456Z" {
		t.Fatalf("revision must be canonical UTC: %q", got)
	}
	for _, valid := range []string{UnconfiguredStreamSettingsRevision, "2026-10-28T00:00:00Z", "2026-10-28T00:00:00.123456Z"} {
		if !ValidStreamSettingsRevision(valid) {
			t.Fatalf("canonical revision rejected: %q", valid)
		}
	}
	for _, invalid := range []string{"", " unconfigured", "2026-10-28T00:00:00+00:00", "2026-10-28T00:00:00.1234567Z", "2026-10-28T00:00:00.000000Z", "latest"} {
		if ValidStreamSettingsRevision(invalid) {
			t.Fatalf("noncanonical revision accepted: %q", invalid)
		}
	}
}

func TestMemoryStreamSettingsRevisionSurvivesLifecycleAndRejectsStaleSettings(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if stream.SettingsRevision != UnconfiguredStreamSettingsRevision {
		t.Fatalf("legacy settings baseline = %q", stream.SettingsRevision)
	}
	if _, err = streams.UpdateStreamStatus(t.Context(), stream.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	baseline := stream.SettingsRevision
	first, err := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{Name: "first", ExpectedSettingsRevision: &baseline})
	if err != nil || first.SettingsRevision == baseline || !ValidStreamSettingsRevision(first.SettingsRevision) {
		t.Fatalf("initial compare-and-swap: revision=%q error=%v", first.SettingsRevision, err)
	}
	if _, err = streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{Name: "stale overwrite", ExpectedSettingsRevision: &baseline}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("old settings baseline accepted: %v", err)
	}
	changed, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed")
	if err != nil || changed.SettingsRevision != first.SettingsRevision {
		t.Fatalf("lifecycle changed settings authority: %q error=%v", changed.SettingsRevision, err)
	}
	current := first.SettingsRevision
	runtime, err := streams.UpdateStreamEncoderRuntimeSettingsRevision(t.Context(), stream.ID, -3, "", &current)
	if err != nil || runtime.SettingsRevision == current {
		t.Fatalf("runtime settings did not consume settings revision: %q error=%v", runtime.SettingsRevision, err)
	}
	if _, err = streams.UpdateStreamEncoderRuntimeSettingsRevision(t.Context(), stream.ID, 6, "", &current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale runtime settings accepted: %v", err)
	}
	after, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || after.Name != "first" || after.EncoderAudioGainDB != -3 || after.SettingsRevision != runtime.SettingsRevision {
		t.Fatalf("rejected writes changed data: name=%q gain=%v revision=%q error=%v", after.Name, after.EncoderAudioGainDB, after.SettingsRevision, err)
	}
}

func TestMemoryStreamSettingsRevisionConcurrentWritersHaveOneWinner(t *testing.T) {
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, name := range []string{"one", "two"} {
		go func() {
			ready.Done()
			<-start
			_, updateErr := streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{Name: name, ExpectedSettingsRevision: &stream.SettingsRevision})
			results <- updateErr
		}()
	}
	ready.Wait()
	close(start)
	passed, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			passed++
		case errors.Is(err, ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing writer error: %v", err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatalf("writers: success=%d revision_conflict=%d", passed, conflicts)
	}
}
