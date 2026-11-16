package store

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

func manualHoldTestRequest(t *testing.T, s *MemoryStreamStore, id, operation string) AutoStartHoldRequest {
	t.Helper()
	stream, err := s.GetStream(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := s.ObserveAutoStartHold(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newUUID()
	return AutoStartHoldRequest{RequestID: attempt, StreamID: id, Operation: operation, ActorUserID: "human-test", ExpectedHoldRevision: hold.Revision, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedSettingsRevision: stream.SettingsRevision, ExpectedRunID: stream.ArchiveRunID,
		Correlation:     lifecycle.Correlation{RequestID: lifecycle.ID(attempt), AttemptID: lifecycle.ID(attempt), OperationID: "manual-hold", StepID: "hold-record", ExecutionKey: "test-execution"},
		RequiredTargets: []lifecycle.ResourceRef{{Kind: lifecycle.Stream, OwnerID: "encoder-test", ResourceID: lifecycle.ID(id)}, {Kind: lifecycle.Stream, OwnerID: "worker-test", ResourceID: lifecycle.ID(id)}}}
}

func manualHoldTestEvidence(req AutoStartHoldRequest, revision uint64) []lifecycle.Record {
	out := []lifecycle.Record{}
	for _, target := range req.RequiredTargets {
		cr := lifecycle.ContractRef{ContractID: lifecycle.CC02, LogicalRevision: 1, SpecRef: lifecycle.SpecRef, ModuleCommitRef: manualHoldModuleCommit}
		header := lifecycle.Header{ContractRef: cr, RecordKey: lifecycle.RecordKey{ContractRef: cr, OwnerID: target.OwnerID, RecordID: "synthetic-target-ack"}, Correlation: req.Correlation, RecordRevision: revision, DomainRef: lifecycle.DomainRef{DeploymentID: "synthetic-deployment", Realm: "synthetic-realm", DomainID: lifecycle.ID(req.StreamID), MembershipRevision: 1, SnapshotRef: "synthetic-membership", Resources: []lifecycle.ResourceRef{target}}, ProducerRef: lifecycle.ProducerRef{ComponentID: "C04", ServiceID: target.OwnerID, HostID: "synthetic-host", BootID: "synthetic-boot", ProcessIncarnation: "synthetic-process", WriterID: "synthetic-writer"}, ProfileRef: lifecycle.ProfileRef{ProfileID: lifecycle.ExplicitResume, Revision: 1}}
		vector := lifecycle.RevisionVector{}
		membership := uint64(1)
		vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: lifecycle.Membership, OwnerID: target.OwnerID, Resource: target}, Incarnation: lifecycle.ID(req.StreamID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentUint64, Uint64: &membership}, SourceFrameRef: "synthetic-frame"})
		for _, axis := range []lifecycle.Axis{lifecycle.Admission, lifecycle.Ownership, lifecycle.Fence, lifecycle.Boot, lifecycle.Process, lifecycle.RunAxis, lifecycle.ConfigAxis} {
			literal := lifecycle.ID("synthetic-" + string(axis))
			vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: axis, OwnerID: target.OwnerID, Resource: target}, Incarnation: lifecycle.ID(req.StreamID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentLiteral, Literal: &literal}, SourceFrameRef: "synthetic-frame"})
		}
		out = append(out, lifecycle.Record{Header: header, Vector: &lifecycle.VectorRecord{Vector: vector}})
	}
	return out
}

func manualHoldTestCondition(streamID string, value bool) lifecycle.Fact[bool] {
	return lifecycle.Fact[bool]{State: lifecycle.Observed, Value: &value, Observation: &lifecycle.ObservationContext{OwnerID: "bot-test", FrameRef: "presence-observation", Scope: lifecycle.ID(streamID), SourceRevision: 1}}
}

func manualHeldStream(t *testing.T) (*MemoryStreamStore, Stream, AutoStartHoldRequest) {
	t.Helper()
	s := NewMemoryStreamStore()
	stream, err := s.CreateStream(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stream, err = s.UpdateStreamStatus(context.Background(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	req := manualHoldTestRequest(t, s, stream.ID, AutoStartManualStop)
	claimed, changed, err := s.ClaimNormalStop(WithManualStopHold(context.Background(), req), stream)
	if err != nil || !changed {
		t.Fatalf("claim changed=%v err=%v", changed, err)
	}
	return s, claimed, req
}

func TestManualAutoStartHoldSeparatesOperatorAndServiceStop(t *testing.T) {
	s := NewMemoryStreamStore()
	stream, _ := s.CreateStream(context.Background(), "service")
	stream, _ = s.UpdateStreamStatus(context.Background(), stream.ID, "live")
	before, _ := s.ObserveAutoStartHold(context.Background(), stream.ID)
	if _, ok, err := s.ClaimNormalStop(context.Background(), stream); err != nil || !ok {
		t.Fatalf("service stop %v %v", ok, err)
	}
	after, _ := s.ObserveAutoStartHold(context.Background(), stream.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("service stop mutated Human hold")
	}

	manual, stream, req := manualHeldStream(t)
	hold, err := manual.ObserveAutoStartHold(context.Background(), stream.ID)
	if err != nil || !hold.Known || !hold.Held || hold.PendingAttemptID != req.RequestID {
		t.Fatalf("manual hold: %+v %v", hold, err)
	}
	if len(manual.autoStartHoldHistory[stream.ID]) != 2 || manual.autoStartHoldHistory[stream.ID][1].AuditID == "" {
		t.Fatal("hold, history and metadata audit must share the claim")
	}
}

func TestManualAutoStartHoldRequiresEveryTargetACK(t *testing.T) {
	t.Run("manual_start_claim_excludes_pending_resume", manualStartClaimPendingResume)
	s, stream, req := manualHeldStream(t)
	a, _ := s.InspectAutoStartAttempt(context.Background(), stream.ID, req.RequestID)
	evidence := manualHoldTestEvidence(req, a.Hold.Revision)
	if _, err := s.ConfirmManualStopHold(context.Background(), req, evidence[:1]); !errors.Is(err, lf.ErrHeld) {
		t.Fatalf("missing target: %v", err)
	}
	if got, _ := s.ObserveAutoStartHold(context.Background(), stream.ID); got != a.Hold {
		t.Fatal("refusal changed hold")
	}
	confirmed, err := s.ConfirmManualStopHold(context.Background(), req, evidence)
	if err != nil || !confirmed.Final || !confirmed.Hold.Held || confirmed.Hold.PendingAttemptID != "" || confirmed.HoldCommit.Knowledge != lifecycle.Known {
		t.Fatalf("confirm %+v %v", confirmed, err)
	}
	if confirmed.Gate != lifecycle.NotQualified {
		t.Fatal("metadata must not qualify real lifecycle authority")
	}
	if _, err := s.ConfirmManualStopHold(context.Background(), req, evidence); !errors.Is(err, lf.ErrConflict) {
		t.Fatal("same Attempt must be inspected, not executed again")
	}
}

func TestManualAutoStartResumeBindsFrozenRequestAndKnownCondition(t *testing.T) {
	s, stream, stopReq := manualHeldStream(t)
	a, _ := s.InspectAutoStartAttempt(context.Background(), stream.ID, stopReq.RequestID)
	if _, err := s.ConfirmManualStopHold(context.Background(), stopReq, manualHoldTestEvidence(stopReq, a.Hold.Revision)); err != nil {
		t.Fatal(err)
	}
	req := manualHoldTestRequest(t, s, stream.ID, AutoStartExplicitResume)
	started, created, err := s.BeginExplicitResume(context.Background(), req)
	if err != nil || !created {
		t.Fatalf("begin %v %v", created, err)
	}
	if duplicate, created, err := s.BeginExplicitResume(context.Background(), req); err != nil || created || !reflect.DeepEqual(duplicate, started) {
		t.Fatal("same immutable request was not correlated")
	}
	different := cloneAutoStart(req)
	different.ActorUserID = "another-human"
	if _, _, err := s.BeginExplicitResume(context.Background(), different); !errors.Is(err, lf.ErrConflict) {
		t.Fatal("same ID different content accepted")
	}
	if _, err := s.CommitExplicitResume(context.Background(), req, lifecycle.Fact[bool]{}, manualHoldTestEvidence(req, started.Hold.Revision)); !errors.Is(err, lf.ErrUnknown) {
		t.Fatalf("unknown condition: %v", err)
	}
	if current, _ := s.ObserveAutoStartHold(context.Background(), stream.ID); !current.Held {
		t.Fatal("unknown condition cleared hold")
	}
	resumed, err := s.CommitExplicitResume(context.Background(), req, manualHoldTestCondition(stream.ID, false), manualHoldTestEvidence(req, started.Hold.Revision))
	if err != nil || resumed.Hold.Held || !resumed.Hold.Known || resumed.Condition.Value == nil || *resumed.Condition.Value || resumed.StartResult.Knowledge != lifecycle.KnowledgeUnknown {
		t.Fatalf("resume false-condition %+v %v", resumed, err)
	}
	if resumed.HoldCommit.Outcome.Observation == nil || resumed.HoldCommit.Outcome.Observation.SourceRevision == 0 {
		t.Fatal("missing committed hold revision")
	}
}

func TestManualAutoStartResumeRejectsChangedStream(t *testing.T) {
	s := NewMemoryStreamStore()
	stream, _ := s.CreateStream(context.Background(), "test")
	req := manualHoldTestRequest(t, s, stream.ID, AutoStartExplicitResume)
	a, _, err := s.BeginExplicitResume(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateStreamSettings(context.Background(), stream.ID, StreamSettings{Name: "changed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitExplicitResume(context.Background(), req, manualHoldTestCondition(stream.ID, true), manualHoldTestEvidence(req, a.Hold.Revision)); !errors.Is(err, lf.ErrConflict) {
		t.Fatalf("stale settings: %v", err)
	}
	if hold, _ := s.ObserveAutoStartHold(context.Background(), stream.ID); hold.PendingAttemptID != req.RequestID {
		t.Fatal("stale confirmation changed pending intent")
	}
}

func TestManualAutoStartHoldPartialRestoreAndInspectionArePure(t *testing.T) {
	s, stream, req := manualHeldStream(t)
	before, _ := s.InspectAutoStartAttempt(context.Background(), stream.ID, req.RequestID)
	history, _ := s.ListHoldHistory(context.Background(), stream.ID)
	for i := 0; i < 3; i++ {
		if _, err := s.InspectAutoStartAttempt(context.Background(), stream.ID, req.RequestID); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.autoStartHoldHistory[stream.ID]) != len(history) {
		t.Fatal("Inspect wrote history")
	}
	s.mu.Lock()
	damaged := s.autoStartHolds[stream.ID]
	damaged.Held = false
	s.autoStartHolds[stream.ID] = damaged
	s.mu.Unlock()
	if _, err := s.ObserveAutoStartHold(context.Background(), stream.ID); !errors.Is(err, lf.ErrUnknown) {
		t.Fatalf("partial restore not unknown: %v", err)
	}
	if _, err := s.InspectAutoStartAttempt(context.Background(), stream.ID, req.RequestID); !errors.Is(err, lf.ErrUnknown) {
		t.Fatal("inspection repaired inconsistent projection")
	}
	if before.Hold.Held == false {
		t.Fatal("returned historic observation mutated")
	}
}

func TestManualAutoStartAdmissionIsImmutableAndCurrent(t *testing.T) {
	t.Run("claim_request_uniqueness_before_mutation", manualStartRequestUniqueness)
	s := NewMemoryStreamStore()
	stream, _ := s.CreateStream(context.Background(), "test")
	hold, _ := s.ObserveAutoStartHold(context.Background(), stream.ID)
	request := manualHoldTestRequest(t, s, stream.ID, AutoStartExplicitResume)
	request.RequestID = "start-request"
	request.Correlation.RequestID, request.Correlation.AttemptID = "start-request", "start-request"
	a := AutoStartAdmission{RequestID: "start-request", StreamID: stream.ID, Automatic: true, HoldRevision: hold.Revision, StreamUpdatedAt: stream.UpdatedAt, SettingsRevision: stream.SettingsRevision, Records: manualHoldTestEvidence(request, hold.Revision)}
	if err := s.SaveAutoStartAdmission(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectAutoStartAdmission(context.Background(), stream.ID, a.RequestID); err != nil {
		t.Fatal(err)
	}
	a.Automatic = false
	if err := s.SaveAutoStartAdmission(context.Background(), a); !errors.Is(err, lf.ErrConflict) {
		t.Fatal("same ID different content accepted")
	}
	if _, err := s.UpdateStreamStatus(context.Background(), stream.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectAutoStartAdmission(context.Background(), stream.ID, a.RequestID); !errors.Is(err, lf.ErrConflict) {
		t.Fatal("old admission returned as current")
	}
}

func TestManualAutoStartHoldRejectsSecretMetadata(t *testing.T) {
	s := NewMemoryStreamStore()
	stream, _ := s.CreateStream(context.Background(), "test")
	req := manualHoldTestRequest(t, s, stream.ID, AutoStartExplicitResume)
	req.OriginalMetadata = map[string]string{"password": "must-not-be-recorded"}
	if _, _, err := s.BeginExplicitResume(context.Background(), req); !errors.Is(err, lf.ErrHeld) {
		t.Fatal("raw secret field accepted")
	}
	if len(s.autoStartHoldAttempts) != 0 {
		t.Fatal("rejected payload was stored")
	}
}

func manualStartClaimPendingResume(t *testing.T) {
	ctx := t.Context()
	streams, stopping, stopRequest := manualHeldStream(t)
	pending, e := streams.InspectAutoStartAttempt(ctx, stopping.ID, stopRequest.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = streams.ConfirmManualStopHold(ctx, stopRequest, manualHoldTestEvidence(stopRequest, pending.Hold.Revision)); e != nil {
		t.Fatal(e)
	}
	completed, changed, e := streams.CompleteNormalStop(ctx, stopping.ID, stopRequest.ExpectedStreamUpdatedAt.UTC().Format(time.RFC3339Nano), stopping.UpdatedAt)
	if e != nil || !changed {
		t.Fatal("complete synthetic Stop:", e)
	}
	stream, e := streams.UpdateStreamStatus(ctx, completed.ID, "ready")
	if e != nil {
		t.Fatal(e)
	}
	services := NewMemoryAuthStore()
	services.BindStreamAssignmentGuard(streams)
	for _, kind := range []string{"encoder_recorder", "worker", "discord_bot"} {
		id := newUUID()
		token, e := services.CreateServiceToken(ctx, kind, []string{"service.register"})
		if e != nil {
			t.Fatal(e)
		}
		registration := ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: kind, PublicURL: "https://" + id + ".example.com"}
		if _, e = services.PrecreateService(ctx, token, registration); e != nil {
			t.Fatal(e)
		}
		if _, e = services.RegisterService(ctx, token, registration); e != nil {
			t.Fatal(e)
		}
		if _, e = services.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: id, StreamID: stream.ID, AssignmentRole: "primary"}); e != nil {
			t.Fatal(e)
		}
	}
	assignments, e := services.ListStreamAssignments(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	hold, e := streams.ObserveAutoStartHold(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	stream, e = streams.GetStream(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	request := StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedPrimaryAssignments: assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()}
	// Native Windows time resolution can leave the existing mutation timestamp
	// briefly ahead of wall time. Do not weaken that production clock fence.
	deadline := time.Now().Add(time.Second)
	for time.Now().UTC().Before(stream.UpdatedAt) {
		if time.Now().After(deadline) {
			t.Fatal("native clock did not reach source snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	claimed, e := streams.ClaimStreamStart(WithManualStartHold(ctx, hold.Revision), request)
	if e != nil {
		t.Fatalf("confirmed held lane refused manual claim: %v status=%s protected=%+v assignments=%+v pending=%v", e, stream.Status, memoryStreamAssignmentProtectionLocked(streams, stream), assignments, streams.normalStopPending[stream.ID])
	}
	if got, _ := streams.ObserveAutoStartHold(ctx, stream.ID); got != hold {
		t.Fatal("manual claim changed Hold")
	}
	if _, changed, e = streams.TransitionClaimedStreamStart(ctx, claimed.OwnershipClaim, "failed"); e != nil || !changed {
		t.Fatal(e)
	}
	stream, e = streams.UpdateStreamStatus(ctx, stream.ID, "ready")
	if e != nil {
		t.Fatal(e)
	}
	resume := manualHoldTestRequest(t, streams, stream.ID, AutoStartExplicitResume)
	attempt, created, e := streams.BeginExplicitResume(ctx, resume)
	if e != nil || !created {
		t.Fatal(e)
	}
	before, e := streams.GetStream(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	request.ExpectedStatus, request.ExpectedStreamUpdatedAt = before.Status, before.UpdatedAt
	request.ArchiveStartedAt = time.Now().UTC()
	barrier := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		<-barrier
		_, err := streams.ClaimStreamStart(WithManualStartHold(ctx, attempt.Hold.Revision), request)
		result <- err
	}()
	close(barrier)
	if e = <-result; !errors.Is(e, lf.ErrHeld) {
		t.Fatalf("manual claim crossed pending Resume: %v", e)
	}
	after, e := streams.GetStream(ctx, stream.ID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected manual claim changed Stream/Run")
	}
	if got, _ := streams.ObserveAutoStartHold(ctx, stream.ID); got != attempt.Hold {
		t.Fatal("rejected manual claim changed pending Hold")
	}
	both := WithAutomaticStartHold(WithManualStartHold(ctx, attempt.Hold.Revision), attempt.Hold.Revision)
	if _, e = streams.ClaimStreamStart(both, request); !errors.Is(e, lf.ErrHeld) {
		t.Fatal("conflicting Start origin bindings accepted")
	}
}

type manualClaimSnapshot struct {
	owner                      *MemoryStreamStore
	services                   map[string]RegisteredService
	assignments, assignmentIDs map[string]string
}

func manualCaptureClaimOwner(streams *MemoryStreamStore, services *MemoryAuthStore) manualClaimSnapshot {
	services.mu.Lock()
	defer services.mu.Unlock()
	streams.mu.Lock()
	defer streams.mu.Unlock()
	return manualClaimSnapshot{owner: &MemoryStreamStore{autoStartHolds: cloneAutoStart(streams.autoStartHolds), autoStartHoldHistory: cloneAutoStart(streams.autoStartHoldHistory), autoStartHoldAttempts: cloneAutoStart(streams.autoStartHoldAttempts), autoStartAdmissions: cloneAutoStart(streams.autoStartAdmissions), autoStartClaimRequests: maps.Clone(streams.autoStartClaimRequests), serviceAssignmentGuard: streams.serviceAssignmentGuard, streams: maps.Clone(streams.streams), archiveRunSources: maps.Clone(streams.archiveRunSources), normalStopPending: maps.Clone(streams.normalStopPending), normalStopReceipts: maps.Clone(streams.normalStopReceipts), logs: cloneAutoStart(streams.logs), artifacts: cloneAutoStart(streams.artifacts), artifactReports: maps.Clone(streams.artifactReports), archiveRetryPending: maps.Clone(streams.archiveRetryPending), artifactShares: cloneAutoStart(streams.artifactShares), mediaRuntimes: cloneAutoStart(streams.mediaRuntimes), youtubeRuntimes: cloneAutoStart(streams.youtubeRuntimes), youtubeRelayBindingClaims: cloneAutoStart(streams.youtubeRelayBindingClaims), discordYouTubeLiveNotifications: cloneAutoStart(streams.discordYouTubeLiveNotifications), discordYouTubeLiveNotificationEvents: maps.Clone(streams.discordYouTubeLiveNotificationEvents), discordYouTubeLiveNotificationLeases: maps.Clone(streams.discordYouTubeLiveNotificationLeases), discordYouTubeLiveNotificationLeaseExpires: maps.Clone(streams.discordYouTubeLiveNotificationLeaseExpires), relayBindingClaimProfiles: streams.relayBindingClaimProfiles}, services: maps.Clone(services.services), assignments: maps.Clone(services.assignments), assignmentIDs: maps.Clone(services.assignmentIDs)}
}
func manualClaimFixture(t *testing.T, streams *MemoryStreamStore, services *MemoryAuthStore, name string) (Stream, []RegisteredService) {
	t.Helper()
	ctx := t.Context()
	stream, e := streams.CreateStream(ctx, name)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"encoder_recorder", "worker", "discord_bot"} {
		id := newUUID()
		token, e := services.CreateServiceToken(ctx, kind, []string{"service.register"})
		if e != nil {
			t.Fatal(e)
		}
		registration := ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: kind, PublicURL: "https://" + id + ".example.com"}
		if _, e = services.PrecreateService(ctx, token, registration); e != nil {
			t.Fatal(e)
		}
		if _, e = services.RegisterService(ctx, token, registration); e != nil {
			t.Fatal(e)
		}
		if _, e = services.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: id, StreamID: stream.ID, AssignmentRole: "primary"}); e != nil {
			t.Fatal(e)
		}
	}
	stream, e = streams.GetStream(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	assignments, e := services.ListStreamAssignments(ctx, stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	return stream, assignments
}
func manualClaimOriginal(id string, stream Stream) StartClaimRequestOriginal {
	return StartClaimRequestOriginal{RequestID: id, StreamID: stream.ID, ActorUserID: "synthetic-human", Operation: "manual-start", ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedSettingsRevision: stream.SettingsRevision, ExpectedRunID: stream.ArchiveRunID}
}
func manualClaimRequest(t *testing.T, stream Stream, assignments []RegisteredService) StreamStartClaimRequest {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().UTC().Before(stream.UpdatedAt) {
		if time.Now().After(deadline) {
			t.Fatal("native source clock did not advance")
		}
		time.Sleep(time.Millisecond)
	}
	return StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedPrimaryAssignments: assignments, ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC()}
}
func manualStartRequestUniqueness(t *testing.T) {
	ctx := t.Context()
	streams := NewMemoryStreamStore()
	services := NewMemoryAuthStore()
	services.BindStreamAssignmentGuard(streams)
	first, firstAssignments := manualClaimFixture(t, streams, services, "first")
	id := "original-start-" + newUUID()
	original := manualClaimOriginal(id, first)
	firstClaim, e := streams.ClaimStreamStart(WithStartClaimRequest(WithManualStartHold(ctx, 1), original), manualClaimRequest(t, first, firstAssignments))
	if e != nil {
		t.Fatal("first original claim:", e)
	}
	saved, e := streams.LookupStartClaimRequest(ctx, first.ID, id)
	if e != nil || saved != original {
		t.Fatal("original immutable claim correlation lost")
	}
	if _, changed, e := streams.TransitionClaimedStreamStart(ctx, firstClaim.OwnershipClaim, "failed"); e != nil || !changed {
		t.Fatal(e)
	}
	first, e = streams.UpdateStreamStatus(ctx, first.ID, "ready")
	if e != nil {
		t.Fatal(e)
	}
	fresh := manualClaimOriginal(id, first)
	fresh.ActorUserID = "different-human"
	before := manualCaptureClaimOwner(streams, services)
	if _, e = streams.ClaimStreamStart(WithStartClaimRequest(WithManualStartHold(ctx, 1), fresh), manualClaimRequest(t, first, firstAssignments)); !errors.Is(e, lf.ErrConflict) {
		t.Fatalf("same ID different fresh content: %v", e)
	}
	if !reflect.DeepEqual(before, manualCaptureClaimOwner(streams, services)) {
		t.Fatal("duplicate request changed CP owner fields, Run, history or assignments")
	}
	second, secondAssignments := manualClaimFixture(t, streams, services, "different Stream")
	before = manualCaptureClaimOwner(streams, services)
	if _, e = streams.ClaimStreamStart(WithStartClaimRequest(WithManualStartHold(ctx, 1), manualClaimOriginal(id, second)), manualClaimRequest(t, second, secondAssignments)); !errors.Is(e, lf.ErrConflict) {
		t.Fatal("cross Stream reused request accepted", e)
	}
	if !reflect.DeepEqual(before, manualCaptureClaimOwner(streams, services)) {
		t.Fatal("cross Stream duplicate changed owner state")
	}
	left, leftAssignments := manualClaimFixture(t, streams, services, "left")
	right, rightAssignments := manualClaimFixture(t, streams, services, "right")
	requests := []StreamStartClaimRequest{manualClaimRequest(t, left, leftAssignments), manualClaimRequest(t, right, rightAssignments)}
	originals := []StartClaimRequestOriginal{manualClaimOriginal("cross-"+id, left), manualClaimOriginal("cross-"+id, right)}
	before = manualCaptureClaimOwner(streams, services)
	barrier := make(chan struct{})
	type outcome struct {
		index int
		claim ClaimedStreamStart
		err   error
	}
	results := make(chan outcome, 2)
	for i := range originals {
		go func(i int) {
			<-barrier
			claim, e := streams.ClaimStreamStart(WithStartClaimRequest(WithManualStartHold(ctx, 1), originals[i]), requests[i])
			results <- outcome{i, claim, e}
		}(i)
	}
	close(barrier)
	winner := ""
	successes := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			successes++
			winner = result.claim.Stream.ID
		} else if !errors.Is(result.err, lf.ErrConflict) {
			t.Fatal("cross Stream concurrency unexpected error", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("global request uniqueness winners=%d", successes)
	}
	after := manualCaptureClaimOwner(streams, services)
	expected := before
	expected.owner.streams[winner] = after.owner.streams[winner]
	expected.owner.artifactReports[winner] = after.owner.artifactReports[winner]
	for key, value := range after.owner.archiveRunSources {
		if key.streamID == winner {
			expected.owner.archiveRunSources[key] = value
		}
	}
	expected.owner.autoStartClaimRequests[originals[0].RequestID] = after.owner.autoStartClaimRequests[originals[0].RequestID]
	if !reflect.DeepEqual(expected, after) {
		t.Fatal("losing cross Stream claim changed unrelated owner fields or created extra markers")
	}
}
