package store

import (
	"context"
	"reflect"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
)

func (s *MemoryStreamStore) initAutoStartHoldMapsLocked() {
	if s.autoStartHolds == nil {
		s.autoStartHolds = map[string]AutoStartHold{}
	}
	if s.autoStartHoldHistory == nil {
		s.autoStartHoldHistory = map[string][]AutoStartHoldHistory{}
	}
	if s.autoStartHoldAttempts == nil {
		s.autoStartHoldAttempts = map[string]AutoStartHoldAttempt{}
	}
	if s.autoStartAdmissions == nil {
		s.autoStartAdmissions = map[string]AutoStartAdmission{}
	}
}

func (s *MemoryStreamStore) observeAutoStartHoldLocked(id string) (AutoStartHold, error) {
	hold, ok := s.autoStartHolds[id]
	history := s.autoStartHoldHistory[id]
	if !ok || len(history) == 0 || !reflect.DeepEqual(history[len(history)-1].Hold, hold) {
		return unknownAutoStartHold(id), lf.ErrUnknown
	}
	if hold.PendingAttemptID != "" {
		a, exists := s.autoStartHoldAttempts[hold.PendingAttemptID]
		if !exists || !reflect.DeepEqual(a.Hold, hold) {
			return unknownAutoStartHold(id), lf.ErrUnknown
		}
	}
	return cloneAutoStart(hold), nil
}

func (s *MemoryStreamStore) ObserveAutoStartHold(ctx context.Context, id string) (AutoStartHold, error) {
	if err := ctx.Err(); err != nil {
		return unknownAutoStartHold(id), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return AutoStartHold{}, ErrNotFound
	}
	return s.observeAutoStartHoldLocked(id)
}

func (s *MemoryStreamStore) persistAutoStartHoldLocked(before, hold AutoStartHold, requestID, operation string) {
	s.initAutoStartHoldMapsLocked()
	history := AutoStartHoldHistory{Sequence: uint64(len(s.autoStartHoldHistory[hold.StreamID]) + 1), StreamID: hold.StreamID, RequestID: requestID, Operation: operation, BeforeRevision: before.Revision, Hold: cloneAutoStart(hold), AuditID: newUUID(), CreatedAt: hold.UpdatedAt}
	s.autoStartHolds[hold.StreamID] = cloneAutoStart(hold)
	s.autoStartHoldHistory[hold.StreamID] = append(s.autoStartHoldHistory[hold.StreamID], history)
}

func (s *MemoryStreamStore) initializeAutoStartHoldLocked(stream Stream) {
	s.persistAutoStartHoldLocked(AutoStartHold{}, AutoStartHold{StreamID: stream.ID, Known: true, Revision: 1, Reason: "new_stream", CreatedAt: stream.CreatedAt, UpdatedAt: stream.CreatedAt}, "", "new_stream")
}

func (s *MemoryStreamStore) beginManualStopHoldLocked(ctx context.Context, stream Stream) error {
	req, bound := ctx.Value(manualStopHoldKey{}).(AutoStartHoldRequest)
	if !bound {
		return nil
	}
	if err := validateHoldRequest(req); err != nil {
		return err
	}
	if req.Operation != AutoStartManualStop && req.Operation != AutoStartForceStop {
		return lf.ErrHeld
	}
	if err := checkHoldRequestStream(req, stream); err != nil {
		return err
	}
	s.initAutoStartHoldMapsLocked()
	if _, exists := s.autoStartHoldAttempts[req.RequestID]; exists {
		return lf.ErrConflict
	}
	before, err := s.observeAutoStartHoldLocked(stream.ID)
	if err != nil {
		if _, exists := s.autoStartHolds[stream.ID]; exists {
			return err
		}
		before = unknownAutoStartHold(stream.ID)
	}
	if before.Revision != req.ExpectedHoldRevision || before.PendingAttemptID != "" {
		return lf.ErrConflict
	}
	hold, err := nextHold(before, req, true)
	if err != nil {
		return err
	}
	hold.Known, hold.Held, hold.Reason, hold.OriginalAttemptID = true, true, req.Operation, req.RequestID
	s.persistAutoStartHoldLocked(before, hold, req.RequestID, "intent")
	s.autoStartHoldAttempts[req.RequestID] = newHoldAttempt(req, hold)
	return nil
}

func (s *MemoryStreamStore) checkAutomaticStartHoldLocked(ctx context.Context, streamID string) error {
	expected, automatic := ctx.Value(automaticStartHoldKey{}).(uint64)
	manualRevision, manual := ctx.Value(manualStartHoldKey{}).(uint64)
	if automatic && manual {
		return lf.ErrHeld
	}
	if !automatic && !manual {
		return nil
	}
	if manual {
		expected = manualRevision
	}
	hold, err := s.observeAutoStartHoldLocked(streamID)
	if err != nil {
		return err
	}
	if !hold.Known || hold.Revision == 0 {
		return lf.ErrUnknown
	}
	if (automatic && hold.Held) || hold.PendingAttemptID != "" || hold.Revision != expected {
		return lf.ErrHeld
	}
	return nil
}

func (s *MemoryStreamStore) BeginExplicitResume(ctx context.Context, req AutoStartHoldRequest) (AutoStartHoldAttempt, bool, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	if err := validateHoldRequest(req); err != nil || req.Operation != AutoStartExplicitResume {
		return AutoStartHoldAttempt{}, false, lf.ErrHeld
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initAutoStartHoldMapsLocked()
	stream, exists := s.streams[req.StreamID]
	if !exists {
		return AutoStartHoldAttempt{}, false, ErrNotFound
	}
	if prior, ok := s.autoStartHoldAttempts[req.RequestID]; ok {
		if !sameHoldRequest(prior.Request, req) {
			return AutoStartHoldAttempt{}, false, lf.ErrConflict
		}
		return cloneAutoStart(prior), false, nil
	}
	if err := checkHoldRequestStream(req, stream); err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	before, err := s.observeAutoStartHoldLocked(req.StreamID)
	if err != nil {
		if _, ok := s.autoStartHolds[req.StreamID]; ok {
			return AutoStartHoldAttempt{}, false, err
		}
		before = unknownAutoStartHold(req.StreamID)
	}
	if before.Revision != req.ExpectedHoldRevision || before.PendingAttemptID != "" {
		return AutoStartHoldAttempt{}, false, lf.ErrConflict
	}
	hold, err := nextHold(before, req, true)
	if err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	hold.OriginalAttemptID = req.RequestID
	a := newHoldAttempt(req, hold)
	s.persistAutoStartHoldLocked(before, hold, req.RequestID, "resume_intent")
	s.autoStartHoldAttempts[req.RequestID] = cloneAutoStart(a)
	return cloneAutoStart(a), true, nil
}

func (s *MemoryStreamStore) finishAutoStartHold(ctx context.Context, req AutoStartHoldRequest, condition lifecycle.Fact[bool], evidence []lifecycle.Record, resume bool) (AutoStartHoldAttempt, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if err := validateHoldRequest(req); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if resume && validateHoldCondition(condition) != nil {
		return AutoStartHoldAttempt{}, lf.ErrUnknown
	}
	if resume && string(condition.Observation.Scope) != req.StreamID {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, exists := s.streams[req.StreamID]
	if !exists {
		return AutoStartHoldAttempt{}, ErrNotFound
	}
	a, exists := s.autoStartHoldAttempts[req.RequestID]
	if !exists {
		return AutoStartHoldAttempt{}, ErrNotFound
	}
	if !sameHoldRequest(a.Request, req) || a.Final {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if resume {
		if req.Operation != AutoStartExplicitResume {
			return AutoStartHoldAttempt{}, lf.ErrHeld
		}
		if err := checkHoldRequestStream(req, stream); err != nil {
			return AutoStartHoldAttempt{}, err
		}
	} else if req.Operation != AutoStartManualStop && req.Operation != AutoStartForceStop {
		return AutoStartHoldAttempt{}, lf.ErrHeld
	}
	if !resume && (stream.SettingsRevision != req.ExpectedSettingsRevision || stream.ArchiveRunID != req.ExpectedRunID) {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	before, err := s.observeAutoStartHoldLocked(req.StreamID)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if before.PendingAttemptID != req.RequestID || before.Revision != a.Hold.Revision {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if err = holdFenceEvidence(req, before, evidence); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	hold, err := nextHold(before, req, false)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	operation := "confirmed"
	if resume {
		hold.Known, hold.Held, hold.Reason, operation = true, false, AutoStartExplicitResume, "resumed"
	}
	a.Hold, a.FenceEvidence, a.UpdatedAt, a.Final = hold, cloneAutoStart(evidence), hold.UpdatedAt, true
	a.HoldCommit, a.Gate = knownHoldBoundary(hold), lifecycle.NotQualified
	if resume {
		a.Condition = cloneAutoStart(condition)
	}
	s.persistAutoStartHoldLocked(before, hold, req.RequestID, operation)
	s.autoStartHoldAttempts[req.RequestID] = cloneAutoStart(a)
	return cloneAutoStart(a), nil
}

func (s *MemoryStreamStore) CommitExplicitResume(ctx context.Context, req AutoStartHoldRequest, condition lifecycle.Fact[bool], evidence []lifecycle.Record) (AutoStartHoldAttempt, error) {
	return s.finishAutoStartHold(ctx, req, condition, evidence, true)
}
func (s *MemoryStreamStore) ConfirmManualStopHold(ctx context.Context, req AutoStartHoldRequest, evidence []lifecycle.Record) (AutoStartHoldAttempt, error) {
	return s.finishAutoStartHold(ctx, req, lifecycle.Fact[bool]{}, evidence, false)
}

func (s *MemoryStreamStore) InspectAutoStartAttempt(ctx context.Context, streamID, attemptID string) (AutoStartHoldAttempt, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.observeAutoStartHoldLocked(streamID); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	a, ok := s.autoStartHoldAttempts[attemptID]
	if !ok {
		return AutoStartHoldAttempt{}, ErrNotFound
	}
	if a.Request.StreamID != streamID {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	return cloneAutoStart(a), nil
}

func (s *MemoryStreamStore) ListHoldHistory(ctx context.Context, streamID string) ([]AutoStartHoldHistory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAutoStart(s.autoStartHoldHistory[streamID]), nil
}

func (s *MemoryStreamStore) RecordAutoStartStartResult(ctx context.Context, streamID, attemptID string, result lifecycle.BoundaryObservation) (AutoStartHoldAttempt, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if err := validateStartBoundary(result, streamID); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.autoStartHoldAttempts[attemptID]
	if !ok {
		return AutoStartHoldAttempt{}, ErrNotFound
	}
	if a.Request.StreamID != streamID || !a.Final || a.Request.Operation != AutoStartExplicitResume || string(result.Address.Resource.ResourceID) != streamID {
		return AutoStartHoldAttempt{}, lf.ErrHeld
	}
	if a.StartResultRecorded {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	a.StartResult, a.StartResultRecorded, a.UpdatedAt = cloneAutoStart(result), true, time.Now().UTC()
	s.autoStartHoldAttempts[attemptID] = cloneAutoStart(a)
	return cloneAutoStart(a), nil
}

func validateAutoStartAdmission(a AutoStartAdmission) error {
	if !validHoldID(a.RequestID) || len(a.RequestID) > 96 || !validHoldID(a.StreamID) || a.StreamUpdatedAt.IsZero() || a.SettingsRevision == "" || len(a.Records) == 0 || len(a.Records) > 16 {
		return lf.ErrHeld
	}
	seen := map[lifecycle.ID]bool{}
	for _, record := range a.Records {
		resource, err := manualhold.Resource(record)
		if err != nil || manualhold.Validate(record, a.StreamID) != nil || record.Header.RecordRevision != a.HoldRevision || seen[resource.OwnerID] {
			return lf.ErrHeld
		}
		seen[resource.OwnerID] = true
		if string(record.Header.ContractRef.ModuleCommitRef) != manualHoldModuleCommit || string(record.Header.Correlation.RequestID) != a.RequestID {
			return lf.ErrHeld
		}
	}
	return nil
}

func checkAutoStartAdmission(a AutoStartAdmission, stream Stream, hold AutoStartHold) error {
	if a.StreamID != stream.ID || stream.DeletedAt != nil || !a.StreamUpdatedAt.Equal(stream.UpdatedAt) || a.SettingsRevision != stream.SettingsRevision || a.RunID != stream.ArchiveRunID || a.HoldRevision != hold.Revision {
		return lf.ErrConflict
	}
	if hold.PendingAttemptID != "" {
		return lf.ErrHeld
	}
	if a.Automatic && (!hold.Known || hold.Held) {
		if !hold.Known {
			return lf.ErrUnknown
		}
		return lf.ErrHeld
	}
	return nil
}

func (s *MemoryStreamStore) SaveAutoStartAdmission(ctx context.Context, a AutoStartAdmission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAutoStartAdmission(a); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initAutoStartHoldMapsLocked()
	stream, exists := s.streams[a.StreamID]
	if !exists {
		return ErrNotFound
	}
	if _, exists := s.autoStartAdmissions[a.RequestID]; exists {
		return lf.ErrConflict
	}
	hold, err := s.observeAutoStartHoldLocked(a.StreamID)
	if err != nil {
		return err
	}
	if err := checkAutoStartAdmission(a, stream, hold); err != nil {
		return err
	}
	s.autoStartAdmissions[a.RequestID] = cloneAutoStart(a)
	return nil
}

func (s *MemoryStreamStore) InspectAutoStartAdmission(ctx context.Context, streamID, requestID string) (AutoStartAdmission, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartAdmission{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, exists := s.autoStartAdmissions[requestID]
	if !exists {
		return AutoStartAdmission{}, ErrNotFound
	}
	if a.StreamID != streamID {
		return AutoStartAdmission{}, lf.ErrConflict
	}
	stream, exists := s.streams[streamID]
	if !exists {
		return AutoStartAdmission{}, ErrNotFound
	}
	hold, err := s.observeAutoStartHoldLocked(streamID)
	if err != nil {
		return AutoStartAdmission{}, err
	}
	if err := checkAutoStartAdmission(a, stream, hold); err != nil {
		return AutoStartAdmission{}, err
	}
	return cloneAutoStart(a), nil
}

// LookupAutoStartAdmission reads the saved original without qualifying its
// present Stream/Hold frame. Only InspectAutoStartAdmission is a current fence.
func (s *MemoryStreamStore) LookupAutoStartAdmission(ctx context.Context, streamID, requestID string) (AutoStartAdmission, error) {
	if err := ctx.Err(); err != nil {
		return AutoStartAdmission{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	admission, exists := s.autoStartAdmissions[requestID]
	if !exists || admission.StreamID != streamID {
		return AutoStartAdmission{}, ErrNotFound
	}
	if validateAutoStartAdmission(admission) != nil {
		return AutoStartAdmission{}, lf.ErrUnknown
	}
	return cloneAutoStart(admission), nil
}
