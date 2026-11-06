package store

import (
	"errors"
	"testing"
	"time"
)

func registerArchiveSourceMemoryEncoder(t *testing.T, auth *MemoryAuthStore, serviceID string) (RegisteredService, ServiceToken) {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.heartbeat", "encoder.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registration := ServiceRegistration{ServiceID: serviceID, ServiceType: "encoder_recorder", ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com"}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	service, err := auth.RegisterService(t.Context(), token, registration)
	if err != nil {
		t.Fatal(err)
	}
	return service, token
}

func archiveSourceMemoryArtifact(stream Stream, size int64) StreamArtifact {
	return StreamArtifact{
		ArchiveRunID: stream.ArchiveRunID, ArchiveStartedAt: cloneTimePtr(stream.ArchiveStartedAt),
		Kind: "archive", Name: "final.mp4",
		RelativePath: streamArtifactRelativePath(stream.ID, stream.ArchiveRunID, "final.mp4"), SizeBytes: size,
	}
}

func TestMemoryArchiveRunSourceAcceptsLateExactReportAfterNodeReuse(t *testing.T) {
	streams, auth := NewMemoryStreamStore(), NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	encoder, token := registerArchiveSourceMemoryEncoder(t, auth, "late-source-encoder")
	otherEncoder, _ := registerArchiveSourceMemoryEncoder(t, auth, "late-source-new-encoder")
	owner, err := streams.CreateStream(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	next, err := streams.CreateStream(t.Context(), "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: encoder.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 6, 1, 0, 0, 123000, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "late-A", started)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), owner.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	event := ServiceStreamEvent{ServiceID: encoder.ServiceID, StreamID: owner.ID, EventType: "archive.artifacts.reported"}
	artifact := archiveSourceMemoryArtifact(owner, 1)
	// Authenticated primary reporting backfills the existing exact run.
	if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	first, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("first report: count=%d err=%v", len(first), err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: encoder.ServiceID, StreamID: next.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: otherEncoder.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	for _, current := range []Stream{owner, next} {
		if _, err := streams.PrepareStreamArchiveRun(t.Context(), current.ID, "new-"+current.ID, started.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := streams.UpdateStreamStatus(t.Context(), current.ID, "completed"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(t.Context(), otherEncoder.ServiceID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(t.Context(), encoder.ServiceID, next.ID); err != nil {
		t.Fatal(err)
	}
	beforeA, _ := streams.GetStream(t.Context(), owner.ID)
	beforeB, _ := streams.GetStream(t.Context(), next.ID)
	beforeService, _ := auth.GetService(t.Context(), encoder.ServiceID)
	artifact.SizeBytes = 2
	if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); err != nil {
		t.Fatalf("late exact report: %v", err)
	}
	afterA, _ := streams.GetStream(t.Context(), owner.ID)
	afterB, _ := streams.GetStream(t.Context(), next.ID)
	afterService, _ := auth.GetService(t.Context(), encoder.ServiceID)
	if afterA.ArchiveRunID != beforeA.ArchiveRunID || afterA.ArchiveReportedAt != nil || !afterA.UpdatedAt.Equal(beforeA.UpdatedAt) ||
		afterB.ArchiveRunID != beforeB.ArchiveRunID || afterB.ArchiveReportedAt != nil || !afterB.UpdatedAt.Equal(beforeB.UpdatedAt) ||
		afterService.CurrentStreamID != next.ID || !afterService.UpdatedAt.Equal(beforeService.UpdatedAt) {
		t.Fatal("late report changed a current stream or Node owner")
	}
	if !streams.archiveRetryPending[owner.ID] || !streams.archiveRetryPending[next.ID] {
		t.Fatal("late report closed a current run's retry guard")
	}
	second, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID ||
		!second[0].CreatedAt.Equal(first[0].CreatedAt) || second[0].SizeBytes != 2 || second[0].SourceServiceID != encoder.ServiceID {
		t.Fatalf("late report lost artifact identity: count=%d err=%v", len(second), err)
	}
	if len(auth.streamEvents) != 2 {
		t.Fatalf("report events=%d, want two append-only rows", len(auth.streamEvents))
	}

	otherToken := auth.serviceTokens[otherEncoder.TokenID]
	badTime := started.Add(time.Microsecond)
	badRun := artifact
	badRun.ArchiveRunID = "forged-A"
	badRun.RelativePath = streamArtifactRelativePath(owner.ID, badRun.ArchiveRunID, badRun.Name)
	wrongTime := artifact
	wrongTime.ArchiveStartedAt = &badTime
	for _, tc := range []struct {
		name     string
		token    ServiceToken
		event    ServiceStreamEvent
		artifact StreamArtifact
	}{
		{"forged run", token, event, badRun},
		{"wrong exact timestamp", token, event, wrongTime},
		{"different source currently owns A", otherToken, ServiceStreamEvent{ServiceID: otherEncoder.ServiceID, StreamID: owner.ID, EventType: event.EventType}, artifact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := streams.WriteStreamArtifactReport(t.Context(), tc.token, tc.event, []StreamArtifact{tc.artifact}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("report error=%v, want ErrForbidden", err)
			}
			if len(auth.streamEvents) != 2 || len(streams.artifacts[owner.ID]) != 1 || streams.artifacts[owner.ID][0].SizeBytes != 2 {
				t.Fatal("rejected report changed event or artifact history")
			}
		})
	}
	if _, err := auth.RotateServiceToken(t.Context(), token.ID); err != nil {
		t.Fatal(err)
	}
	if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale token error=%v", err)
	}
}

func TestMemoryArchiveRunSourcePreservesLegacyOwnerBoundaryAndTokenScopes(t *testing.T) {
	streams, auth := NewMemoryStreamStore(), NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	encoder, token := registerArchiveSourceMemoryEncoder(t, auth, "legacy-source-encoder")
	owner, err := streams.CreateStream(t.Context(), "legacy owner")
	if err != nil {
		t.Fatal(err)
	}
	next, err := streams.CreateStream(t.Context(), "next owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: encoder.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	reportRun := owner
	reportRun.ArchiveRunID, reportRun.ArchiveStartedAt = "legacy-existing", &started
	artifact := archiveSourceMemoryArtifact(reportRun, 1)
	event := ServiceStreamEvent{ServiceID: encoder.ServiceID, StreamID: owner.ID, EventType: "archive.artifacts.reported"}
	for _, change := range []func(*ServiceToken){
		func(stored *ServiceToken) { stored.Scopes = []string{"service.heartbeat"} },
		func(stored *ServiceToken) { stored.ServiceType = "worker" },
	} {
		stored := auth.serviceTokens[token.ID]
		change(&stored)
		auth.serviceTokens[token.ID] = stored
		if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("stored token authority error=%v", err)
		}
		auth.serviceTokens[token.ID] = token
	}
	if len(auth.streamEvents) != 0 || len(streams.artifacts[owner.ID]) != 0 {
		t.Fatal("unauthorized legacy report changed history")
	}
	if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); err != nil {
		t.Fatalf("current-owner legacy report: %v", err)
	}
	if len(streams.archiveRunSources) != 0 {
		t.Fatal("legacy report invented historical source authority")
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: encoder.ServiceID, StreamID: next.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if err := streams.WriteStreamArtifactReport(t.Context(), token, event, []StreamArtifact{artifact}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unrecorded legacy late report error=%v", err)
	}
}

func TestMemoryArchiveRunSourceIsImmutable(t *testing.T) {
	streams := NewMemoryStreamStore()
	started := time.Date(2026, 11, 6, 3, 0, 0, 123000, time.UTC)
	stream := Stream{ID: "stream", ArchiveRunID: "immutable", ArchiveStartedAt: &started}
	if err := memoryRecordStreamArchiveRunSourceLocked(streams, stream, "encoder-A"); err != nil {
		t.Fatal(err)
	}
	if err := memoryRecordStreamArchiveRunSourceLocked(streams, stream, "encoder-A"); err != nil {
		t.Fatal(err)
	}
	if err := memoryRecordStreamArchiveRunSourceLocked(streams, stream, "encoder-B"); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("replacement source error=%v", err)
	}
	later := started.Add(time.Microsecond)
	stream.ArchiveStartedAt = &later
	if err := memoryRecordStreamArchiveRunSourceLocked(streams, stream, "encoder-A"); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("replacement timestamp error=%v", err)
	}
	if len(streams.archiveRunSources) != 1 {
		t.Fatal("source conflict changed immutable history")
	}
}
