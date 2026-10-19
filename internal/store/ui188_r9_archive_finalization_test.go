package store

import (
	"errors"
	"testing"
	"time"
)

func TestUI188R9ArchiveProtectionAuthority(t *testing.T) {
	started := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	zero := time.Time{}
	reported := started.Add(time.Minute)
	for _, tc := range []struct {
		name      string
		state     streamAssignmentProtection
		protected bool
	}{
		{"modern ready without profile", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveRunID: "modern", ArchiveStartedAt: &started}}, true},
		{"modern completed without profile", streamAssignmentProtection{Stream: Stream{Status: "completed", ArchiveRunID: "modern", ArchiveStartedAt: &started}}, true},
		{"modern reported", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveRunID: "modern", ArchiveStartedAt: &started, ArchiveReportedAt: &reported}}, false},
		{"explicit no archive", streamAssignmentProtection{Stream: Stream{Status: "ready"}}, false},
		{"never started", streamAssignmentProtection{Stream: Stream{Status: "created"}}, false},
		{"missing run", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveStartedAt: &started}}, false},
		{"missing started at", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveRunID: "modern"}}, false},
		{"zero started at", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveRunID: "modern", ArchiveStartedAt: &zero}}, false},
		{"legacy configured archive", streamAssignmentProtection{Stream: Stream{Status: "completed", ArchiveProfileID: "legacy-profile", ArchiveStartedAt: &started}}, true},
		{"legacy without run start", streamAssignmentProtection{Stream: Stream{Status: "completed", ArchiveProfileID: "legacy-profile"}}, false},
		{"live", streamAssignmentProtection{Stream: Stream{Status: "live"}}, true},
		{"starting", streamAssignmentProtection{Stream: Stream{Status: "starting"}}, true},
		{"stopping", streamAssignmentProtection{Stream: Stream{Status: "stopping"}}, true},
		{"normal stop pending", streamAssignmentProtection{Stream: Stream{Status: "failed"}, NormalStopPending: true}, true},
		{"archive retry pending", streamAssignmentProtection{Stream: Stream{Status: "failed"}, ArchiveRetryPending: true}, true},
		{"failed without durable pending", streamAssignmentProtection{Stream: Stream{Status: "failed", ArchiveRunID: "modern", ArchiveStartedAt: &started}}, false},
		{"old report does not close current run", streamAssignmentProtection{Stream: Stream{Status: "ready", ArchiveRunID: "modern", ArchiveStartedAt: &started}, HasArchiveReport: true, HasRecordingArtifact: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.protected(); got != tc.protected {
				t.Fatalf("protected=%v, want %v", got, tc.protected)
			}
		})
	}
}

func TestUI188R9MemoryModernArchiveWithoutProfileBarrier(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-r9", "encoder_recorder")
	owner, err := streams.CreateStream(t.Context(), "R9 profile-less archive")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "other run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "r9-modern", started)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if owner.ArchiveProfileID != "" || owner.ArchiveReportedAt != nil {
		t.Fatal("fixture must be profile-less with an unreported real run")
	}
	before, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	// The producer cannot report until all three mutations have reached the guard.
	arrived, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		close(arrived)
		<-release
		items := []StreamArtifact{}
		for _, item := range []struct{ kind, name string }{{"archive", "final.mp4"}, {"metadata", "metadata.json"}, {"logs", "logs.jsonl"}} {
			items = append(items, StreamArtifact{StreamID: owner.ID, ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: &started, Kind: item.kind, Name: item.name, RelativePath: streamArtifactRelativePath(owner.ID, owner.ArchiveRunID, item.name), SizeBytes: 1, SourceServiceID: service.ServiceID})
		}
		done <- streams.UpsertStreamArtifacts(t.Context(), owner.ID, items)
	}()
	<-arrived
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if err := streams.DeleteStream(t.Context(), owner.ID); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("pre-report delete=%v", err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("pre-report unassign=%v", err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: target.ID, AssignmentRole: "primary"}); !errors.Is(err, ErrServiceAssignmentProtectedStream) {
		t.Fatalf("pre-report reassign=%v", err)
	}
	current, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil || current.CurrentStreamID != owner.ID || !current.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("rejected mutation changed the current owner")
	}
	assertMemoryAssignmentOwner(t, auth, service.ServiceID, owner.ID)
	pending, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != owner.ID {
		t.Fatalf("pending archive inventory count=%d err=%v", len(pending), err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	items, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(items) != 3 {
		t.Fatalf("artifact count=%d err=%v", len(items), err)
	}
	for _, item := range items {
		if item.SourceServiceID != service.ServiceID || item.ArchiveRunID != owner.ArchiveRunID || item.ArchiveStartedAt == nil || !item.ArchiveStartedAt.Equal(started) {
			t.Fatal("artifact lost exact service/run authority")
		}
	}
	if current, err := streams.GetStream(t.Context(), owner.ID); err != nil || current.ArchiveReportedAt == nil {
		t.Fatal("report did not finalize the exact run")
	}
	if pending, err := streams.ListArchiveProcessingStreams(t.Context()); err != nil || len(pending) != 0 {
		t.Fatal("finalized run still listed as processing")
	}
	if err := streams.DeleteStream(t.Context(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := streams.GetStream(t.Context(), owner.ID); err != nil || current.DeletedAt == nil {
		t.Fatal("normal delete did not retain a tombstone")
	}
	if items, err := streams.ListStreamArtifacts(t.Context(), owner.ID); err != nil || len(items) != 3 {
		t.Fatal("delete lost the finalized artifact catalog")
	}
}
