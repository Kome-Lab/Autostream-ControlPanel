package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryServiceAssignmentGuardProtectsLiveOwnerWithoutMutation(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-live-guard", "encoder_recorder")
	owner, err := streams.CreateStream(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: owner.ID, ActorUserID: "admin", AssignmentRole: "primary",
	}); err != nil {
		t.Fatalf("initial assignment: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), owner.ID, "live"); err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: target.ID, ActorUserID: "admin", AssignmentRole: "primary",
	}); !errors.Is(err, ErrServiceAssignmentProtectedStream) {
		t.Fatalf("reassign err = %v, want %v", err, ErrServiceAssignmentProtectedStream)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{
		ServiceID: service.ServiceID, ActorUserID: "admin",
	}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("unassign err = %v, want %v", err, ErrServiceUnassignProtectedStream)
	}
	after, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentStreamID != owner.ID || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rejected mutation changed service: before=%s after=%s", formatSafeRegisteredServiceDiagnostic(before), formatSafeRegisteredServiceDiagnostic(after))
	}
	assertMemoryAssignmentOwner(t, auth, service.ServiceID, owner.ID)

	same, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: owner.ID, ActorUserID: "admin", AssignmentRole: "primary",
	})
	if err != nil {
		t.Fatalf("same-target assignment: %v", err)
	}
	if !same.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("same-target assignment churned updated_at: before=%s after=%s", before.UpdatedAt, same.UpdatedAt)
	}
}

func TestMemoryServiceAssignmentGuardProtectsPendingArchiveAndRetry(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-archive-guard", "encoder_recorder")
	owner, err := streams.CreateStream(t.Context(), "archive owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary",
	}); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "archive-run-guard", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: target.ID, AssignmentRole: "primary",
	}); !errors.Is(err, ErrServiceAssignmentProtectedStream) {
		t.Fatalf("archive reassign err = %v, want %v", err, ErrServiceAssignmentProtectedStream)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("archive unassign err = %v, want %v", err, ErrServiceUnassignProtectedStream)
	}

	reportedAt := time.Now().UTC()
	streams.mu.Lock()
	current := streams.streams[owner.ID]
	current.ArchiveReportedAt = &reportedAt
	streams.streams[owner.ID] = current
	streams.mu.Unlock()
	retry, err := auth.BeginStreamArchiveRetryGuarded(t.Context(), service.ServiceID, owner.ID)
	if err != nil {
		t.Fatalf("begin guarded retry: %v", err)
	}
	if retry.ArchiveRunID != owner.ArchiveRunID || retry.ArchiveStartedAt == nil || !retry.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || retry.ArchiveReportedAt != nil {
		t.Fatalf("retry changed archive identity or did not reopen report: before=%#v after=%#v", owner, retry)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("retry unassign err = %v, want %v", err, ErrServiceUnassignProtectedStream)
	}
}

func TestMemoryArchiveClaimRequiresExactV2ReportAuthority(t *testing.T) {
	streams, auth, owner, service, claimed := memoryArchiveStartClaimFixture(t)
	if claimed.ArchiveAuthority.RunID == "" || claimed.ArchiveAuthority.StartedAt == nil {
		t.Fatalf("v2 claim authority = %#v", claimed.ArchiveAuthority)
	}
	owner, transitioned, err := streams.TransitionClaimedStreamStart(t.Context(), claimed.OwnershipClaim, "completed")
	if err != nil || !transitioned {
		t.Fatalf("complete modern claim: stream=%#v transitioned=%v err=%v", owner, transitioned, err)
	}
	wrongRunStartedAt := claimed.ArchiveAuthority.StartedAt.Add(-time.Minute)
	wrongRun := StreamArtifact{
		StreamID: owner.ID, ArchiveRunID: "stale-run", ArchiveStartedAt: &wrongRunStartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: streamArtifactRelativePath(owner.ID, "stale-run", "final.mp4"), SizeBytes: 1,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), owner.ID, []StreamArtifact{wrongRun}); err != nil {
		t.Fatal(err)
	}
	wrongStartedAt := claimed.ArchiveAuthority.StartedAt.Add(time.Second)
	wrongTime := StreamArtifact{
		StreamID: owner.ID, ArchiveRunID: claimed.ArchiveAuthority.RunID, ArchiveStartedAt: &wrongStartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: streamArtifactRelativePath(owner.ID, claimed.ArchiveAuthority.RunID, "final.mp4"), SizeBytes: 2,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), owner.ID, []StreamArtifact{wrongTime}); err != nil {
		t.Fatal(err)
	}
	if current, getErr := streams.GetStream(t.Context(), owner.ID); getErr != nil || current.ArchiveReportedAt != nil {
		t.Fatalf("stale modern report closed authority: stream=%#v err=%v", current, getErr)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("stale modern reports released ownership: %v", err)
	}
	correct := StreamArtifact{
		StreamID: owner.ID, ArchiveRunID: claimed.ArchiveAuthority.RunID, ArchiveStartedAt: claimed.ArchiveAuthority.StartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: streamArtifactRelativePath(owner.ID, claimed.ArchiveAuthority.RunID, "final.mp4"), SizeBytes: 3,
	}
	if err := streams.UpsertStreamArtifacts(t.Context(), owner.ID, []StreamArtifact{correct}); err != nil {
		t.Fatal(err)
	}
	reported, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || reported.ArchiveReportedAt == nil {
		t.Fatalf("exact modern report did not close authority: stream=%#v err=%v", reported, err)
	}
	firstReportedAt := cloneTimePtr(reported.ArchiveReportedAt)
	if err := streams.UpsertStreamArtifacts(t.Context(), owner.ID, []StreamArtifact{correct}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || duplicate.ArchiveReportedAt == nil || firstReportedAt == nil || !duplicate.ArchiveReportedAt.Equal(*firstReportedAt) {
		t.Fatalf("duplicate modern report was not idempotent: first=%v stream=%#v err=%v", firstReportedAt, duplicate, err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); err != nil {
		t.Fatalf("exact modern report did not release ownership: %v", err)
	}
}

func TestMemoryStartClaimCompensationRequiresExactImmutableToken(t *testing.T) {
	for _, mutation := range []string{"stream_identity", "assignment_identity", "archive_authority"} {
		t.Run(mutation, func(t *testing.T) {
			streams, auth, owner, _, claimed := memoryArchiveStartClaimFixture(t)
			switch mutation {
			case "stream_identity":
				streams.mu.Lock()
				current := streams.streams[owner.ID]
				current.Name = "newer settings with colliding timestamp"
				current.UpdatedAt = claimed.OwnershipClaim.StreamUpdatedAt
				streams.streams[owner.ID] = current
				streams.mu.Unlock()
			case "assignment_identity":
				auth.mu.Lock()
				for key := range auth.assignmentIDs {
					streamID, _, role := assignmentPartsFromKey(key)
					if streamID == owner.ID && normalizeAssignmentRole(role) == "primary" {
						auth.assignmentIDs[key] = newUUID()
						break
					}
				}
				auth.mu.Unlock()
			case "archive_authority":
				streams.mu.Lock()
				current := streams.streams[owner.ID]
				current.ArchiveRunID = "newer-run-authority"
				current.UpdatedAt = claimed.OwnershipClaim.StreamUpdatedAt
				streams.streams[owner.ID] = current
				streams.mu.Unlock()
			}
			current, transitioned, err := streams.TransitionClaimedStreamStart(t.Context(), claimed.OwnershipClaim, "failed")
			if !errors.Is(err, ErrServiceAssignmentConflict) || transitioned || current.Status != "starting" {
				t.Fatalf("stale compensation mutation=%s stream=%#v transitioned=%v err=%v", mutation, current, transitioned, err)
			}
		})
	}
}

func memoryArchiveStartClaimFixture(t *testing.T) (*MemoryStreamStore, *MemoryAuthStore, Stream, RegisteredService, ClaimedStreamStart) {
	t.Helper()
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	encoder := registerMemoryAssignmentService(t, auth, "encoder-archive-claim", "encoder_recorder")
	registerMemoryAssignmentService(t, auth, "worker-archive-claim", "worker")
	registerMemoryAssignmentService(t, auth, "discord-archive-claim", "discord_bot")
	owner, err := streams.CreateStream(t.Context(), "archive claim")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile"})
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{encoder.ServiceID, "worker-archive-claim", "discord-archive-claim"} {
		if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: serviceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
			t.Fatal(err)
		}
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.GetStream(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := streams.ClaimStreamStart(t.Context(), StreamStartClaimRequest{
		StreamID: owner.ID, ExpectedStatus: owner.Status, ExpectedStreamUpdatedAt: owner.UpdatedAt,
		ExpectedPrimaryAssignments: assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Date(2026, 8, 23, 4, 5, 6, 789, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return streams, auth, claimed.Stream, encoder, claimed
}

func TestMemoryServiceAssignmentGuardConcurrentMutationsCannotStealProtectedOwner(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "worker-concurrent-guard", "worker")
	owner, err := streams.CreateStream(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
		ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), owner.ID, "starting"); err != nil {
		t.Fatal(err)
	}

	const attempts = 24
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(unassign bool) {
			defer wg.Done()
			if unassign {
				_, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID})
				errs <- err
				return
			}
			_, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{
				ServiceID: service.ServiceID, StreamID: target.ID, AssignmentRole: "primary",
			})
			errs <- err
		}(index%2 == 0)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrServiceAssignmentProtectedStream) && !errors.Is(err, ErrServiceUnassignProtectedStream) {
			t.Fatalf("concurrent mutation err = %v", err)
		}
	}
	assertMemoryAssignmentOwner(t, auth, service.ServiceID, owner.ID)
}

func TestMemoryServiceAssignmentGuardKeepsOnePrimaryOwner(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	target, err := streams.CreateStream(t.Context(), "primary target")
	if err != nil {
		t.Fatal(err)
	}
	serviceIDs := []string{"worker-primary-a", "worker-primary-b"}
	for _, serviceID := range serviceIDs {
		registerMemoryAssignmentService(t, auth, serviceID, "worker")
	}
	start := make(chan struct{})
	errs := make(chan error, len(serviceIDs))
	var wg sync.WaitGroup
	for _, serviceID := range serviceIDs {
		serviceID := serviceID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: serviceID, StreamID: target.ID, AssignmentRole: "primary"})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent primary assignment err=%v", err)
		}
	}
	primary, err := auth.ListStreamAssignments(t.Context(), target.ID)
	if err != nil || len(primary) != 1 || primary[0].ServiceType != "worker" || primary[0].AssignmentRole != "primary" {
		t.Fatalf("primary assignments=%s err=%v", formatSafeSensitiveCompositeDiagnostic(primary), err)
	}
	for _, serviceID := range serviceIDs {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil {
			t.Fatal(err)
		}
		if serviceID == primary[0].ServiceID && service.CurrentStreamID != target.ID {
			t.Fatalf("primary service current stream=%q", service.CurrentStreamID)
		}
		if serviceID != primary[0].ServiceID && service.CurrentStreamID != "" {
			t.Fatalf("replaced service retained current stream=%q", service.CurrentStreamID)
		}
	}
}

func TestMemoryServiceAssignmentGuardRejectsSplitBrainWithoutRepair(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-split-brain", "encoder_recorder")
	owner, err := streams.CreateStream(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	auth.mu.Lock()
	corrupted := auth.services[service.ServiceID]
	corrupted.CurrentStreamID = target.ID
	auth.services[service.ServiceID] = corrupted
	auth.mu.Unlock()

	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: target.ID, AssignmentRole: "primary"}); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("split-brain assign err=%v, want %v", err, ErrServiceAssignmentConflict)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("split-brain unassign err=%v, want %v", err, ErrServiceAssignmentConflict)
	}
	after, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil || after.CurrentStreamID != target.ID {
		t.Fatalf("split-brain current_stream_id was repaired: service=%s err=%v", formatSafeRegisteredServiceDiagnostic(after), err)
	}
	assignments, err := auth.ListServiceAssignmentsForService(t.Context(), service.ServiceID)
	if err != nil || len(assignments) != 1 || assignments[0].StreamID != owner.ID {
		t.Fatalf("split-brain assignment row was repaired: assignments=%#v err=%v", assignments, err)
	}
}

func TestMemoryServiceAndStreamDeleteCannotBypassProtectedUnassign(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-delete-guard", "encoder_recorder")
	owner, err := streams.CreateStream(t.Context(), "protected owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), owner.ID, "live"); err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.DeleteService(t.Context(), service.ServiceID); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("delete service err=%v, want %v", err, ErrServiceUnassignProtectedStream)
	}
	if err := streams.DeleteStream(t.Context(), owner.ID); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("delete stream err=%v, want %v", err, ErrServiceUnassignProtectedStream)
	}
	after, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil || after.CurrentStreamID != owner.ID || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("protected delete mutated service: before=%s after=%s err=%v", formatSafeRegisteredServiceDiagnostic(before), formatSafeRegisteredServiceDiagnostic(after), err)
	}
	current, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || current.DeletedAt != nil || current.Status != "live" {
		t.Fatalf("protected delete mutated stream: %#v err=%v", current, err)
	}
	assertMemoryAssignmentOwner(t, auth, service.ServiceID, owner.ID)
}

func TestMemoryStreamDeleteCannotClearRetryGuardThroughArtifactBackfill(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "encoder-delete-retry-guard", "encoder_recorder")
	stream, err := streams.CreateStream(t.Context(), "retry-protected delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: stream.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	backfillStartedAt := time.Now().UTC().Add(-time.Minute)
	if err := streams.UpsertStreamArtifacts(t.Context(), stream.ID, []StreamArtifact{{
		ArchiveRunID: "backfill-run", ArchiveStartedAt: &backfillStartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: streamArtifactRelativePath(stream.ID, "backfill-run", "final.mp4"), SizeBytes: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BeginStreamArchiveRetryGuarded(t.Context(), service.ServiceID, stream.ID); err != nil {
		t.Fatal(err)
	}
	before, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("delete retry-protected stream err=%v, want %v", err, ErrServiceUnassignProtectedStream)
	}
	after, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil || after.CurrentStreamID != stream.ID || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("retry-protected delete mutated service: before=%s after=%s err=%v", formatSafeRegisteredServiceDiagnostic(before), formatSafeRegisteredServiceDiagnostic(after), err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), stream.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].SourceServiceID != "" {
		t.Fatalf("rejected delete backfilled artifact source: artifacts=%#v err=%v", artifacts, err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: service.ServiceID}); !errors.Is(err, ErrServiceUnassignProtectedStream) {
		t.Fatalf("rejected delete cleared retry guard: %v", err)
	}
}

func TestMemoryStreamDeleteAtomicallyClearsIdleAssignment(t *testing.T) {
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	service := registerMemoryAssignmentService(t, auth, "worker-delete-idle", "worker")
	stream, err := streams.CreateStream(t.Context(), "idle owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStreamGuarded(t.Context(), ServiceAssignmentMutation{ServiceID: service.ServiceID, StreamID: stream.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatalf("delete idle stream: %v", err)
	}
	after, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil || after.CurrentStreamID != "" || after.Status != "registered" {
		t.Fatalf("deleted stream left service owner: %s err=%v", formatSafeRegisteredServiceDiagnostic(after), err)
	}
	assignments, err := auth.ListServiceAssignmentsForService(t.Context(), service.ServiceID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("deleted stream left assignment rows: %#v err=%v", assignments, err)
	}
}

func registerMemoryAssignmentService(t *testing.T, auth *MemoryAuthStore, serviceID, serviceType string) RegisteredService {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registration := ServiceRegistration{
		ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID,
		PublicURL: "https://" + serviceID + ".example.com", Capabilities: map[string]any{},
	}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	service, err := auth.RegisterService(t.Context(), token, registration)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func assertMemoryAssignmentOwner(t *testing.T, auth *MemoryAuthStore, serviceID, streamID string) {
	t.Helper()
	service, err := auth.GetService(t.Context(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if service.CurrentStreamID != streamID {
		t.Fatalf("current stream = %q, want %q", service.CurrentStreamID, streamID)
	}
	assignments, err := auth.ListServiceAssignmentsForService(t.Context(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].StreamID != streamID || assignments[0].AssignmentRole != "primary" {
		t.Fatalf("assignments = %#v, want one primary assignment to %q", assignments, streamID)
	}
}
