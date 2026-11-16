package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type manualHoldProducer func() (lifecycle.ProducerRef, error)
type serviceStopOriginKey struct{}
type resumeStartOriginKey struct{}
type autoStartOriginalKey struct{}
type savedStartAdmissionKey struct{}
type startClaimRequestIDKey struct{}

type manualHoldLifecycle struct{ gate *lf.Gate }

// This is an explicit composition seam, like the existing B1 Gate. Normal
// startup supplies no synthetic Gate and cannot enable a production L2 profile.
func WithManualAutoStartLifecycleGate(gate *lf.Gate) ServerOption {
	return func(s *Server) {
		if gate == nil {
			gate = lf.NewRequiredGate()
		}
		s.manualAutoStartGate = &manualHoldLifecycle{gate: gate}
	}
}

func manualHoldCall(req store.AutoStartHoldRequest, stage string) lf.Call {
	return lf.Call{Operation: "streams.auto_start_hold." + stage, Purpose: "OPERATIONAL_CHANGE", Actor: req.ActorUserID, Target: req.StreamID, Content: req}
}

func (s *Server) recheckResumeActor(w http.ResponseWriter, r *http.Request, actorID string) (*http.Request, bool) {
	var fresh *http.Request
	s.requirePermission("streams.start", func(w http.ResponseWriter, checked *http.Request) {
		if currentFromContext(checked.Context()).User.ID != actorID {
			writeManualHoldError(w, store.ErrForbidden)
			return
		}
		fresh = checked
	})(w, r)
	return fresh, fresh != nil
}

func (s *Server) runManualHoldMutation(ctx context.Context, req store.AutoStartHoldRequest, stage string, fn func(context.Context) error) error {
	if s.manualAutoStartGate == nil {
		return fn(ctx)
	}
	result, err := s.manualAutoStartGate.gate.DoExplicit(ctx, req.RequestID+":"+stage, manualHoldCall(req, stage), fn)
	if err != nil {
		return err
	}
	if result.Outcome != "COMPLETE" {
		return lf.ErrUnknown
	}
	return nil
}

type autoStartOriginal struct {
	RequestID                string `json:"request_id"`
	ExpectedHoldRevision     uint64 `json:"expected_hold_revision"`
	ExpectedStreamRevision   string `json:"expected_stream_revision"`
	ExpectedSettingsRevision string `json:"expected_settings_revision"`
	ExpectedRunID            string `json:"expected_run_id"`
}

type manualAutoStartObservation struct {
	Required         bool   `json:"auto_start_hold_required"`
	Known            bool   `json:"auto_start_hold_known"`
	Held             bool   `json:"auto_start_hold"`
	Revision         uint64 `json:"auto_start_hold_revision"`
	StreamRevision   string `json:"expected_stream_revision"`
	SettingsRevision string `json:"expected_settings_revision"`
	RunID            string `json:"expected_run_id"`
}

func (s *Server) runtimeManualAutoStartObservation(ctx context.Context, stream store.Stream) manualAutoStartObservation {
	value := manualAutoStartObservation{Required: true, Held: true, StreamRevision: stream.UpdatedAt.UTC().Format(time.RFC3339Nano), SettingsRevision: stream.SettingsRevision, RunID: stream.ArchiveRunID}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		return value
	}
	hold, err := owner.ObserveAutoStartHold(ctx, stream.ID)
	value.Revision = hold.Revision
	if err == nil && hold.Known && hold.PendingAttemptID == "" {
		value.Known, value.Held = true, hold.Held
	}
	return value
}

func (s *Server) bindManualStopHold(r *http.Request, stream store.Stream, services []store.RegisteredService, operation string) (*http.Request, *store.AutoStartHoldRequest, error) {
	if service, _ := r.Context().Value(serviceStopOriginKey{}).(bool); service {
		return r, nil, nil
	}
	if !s.manualAutoStartManaged() {
		return r, nil, nil
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		return r, nil, manualhold.ErrUnknown
	}
	hold, err := owner.ObserveAutoStartHold(r.Context(), stream.ID)
	if err != nil && !errors.Is(err, lf.ErrUnknown) {
		return r, nil, err
	}
	actor := currentFromContext(r.Context()).User.ID
	if actor == "" {
		return r, nil, store.ErrForbidden
	}
	id := strings.TrimSpace(r.Header.Get("X-AutoStream-Request-ID"))
	if id == "" {
		id, err = newAutoStartRequestID()
		if err != nil {
			return r, nil, err
		}
	}
	if len(id) > 80 {
		return r, nil, manualhold.ErrRefused
	}
	req := store.AutoStartHoldRequest{RequestID: id, StreamID: stream.ID, Operation: operation, ActorUserID: actor, ExpectedHoldRevision: hold.Revision, ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedSettingsRevision: stream.SettingsRevision, ExpectedRunID: stream.ArchiveRunID, Correlation: autoStartCorrelation(id, operation, "close"), RequiredTargets: manualHoldTargets(services, stream.ID)}
	return r.WithContext(store.WithManualStopHold(r.Context(), req)), &req, nil
}

func (s *Server) confirmManualStopHold(w http.ResponseWriter, r *http.Request, stream store.Stream, req *store.AutoStartHoldRequest, services []store.RegisteredService) error {
	if req == nil {
		return nil
	}
	w.Header().Set("X-AutoStream-Manual-Stop-Attempt", req.RequestID)
	owner := s.streams.(store.ManualAutoStartHoldStore)
	attempt, err := owner.InspectAutoStartAttempt(r.Context(), stream.ID, req.RequestID)
	if err != nil {
		return err
	}
	evidence, err := s.closeManualHoldTargets(r.Context(), stream, attempt, services)
	if err != nil {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.Unknown))
		return err
	}
	err = s.runManualHoldMutation(r.Context(), *req, "confirm", func(ctx context.Context) error {
		_, writeErr := owner.ConfirmManualStopHold(ctx, *req, evidence)
		return writeErr
	})
	if err != nil {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.Unknown))
	}
	return err
}

func (s *Server) registerManualAutoStartRoutes() {
	s.mux.HandleFunc("GET /streams/{id}/auto-start-hold", s.requirePermission("streams.read", s.getManualAutoStartHold))
	s.mux.HandleFunc("POST /streams/{id}/auto-start/resume", s.requirePermission("streams.start", s.resumeManualAutoStart))
	s.mux.HandleFunc("GET /streams/{id}/auto-start/resume-attempts/{request_id}", s.requirePermission("streams.start", s.inspectManualAutoStart))
	s.mux.HandleFunc("GET /streams/{id}/auto-start/hold-attempts/{request_id}", s.requirePermission("streams.read", s.inspectManualAutoStart))
	s.mux.HandleFunc("POST /services/streams/{id}/start-admission/inspect", s.inspectServiceStartAdmission)
	s.mux.HandleFunc("GET /services/streams/{id}/auto-start/attempts/{request_id}", s.inspectServiceAutomaticStart)
}

func (s *Server) getManualAutoStartHold(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "manual_hold_unknown"})
		return
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	hold, err := owner.ObserveAutoStartHold(r.Context(), stream.ID)
	if err != nil {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.Unknown))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "manual_hold_unknown", "stream": stream, "hold": hold})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "hold": hold})
}

func writeManualHoldError(w http.ResponseWriter, err error) {
	if errors.Is(err, lf.ErrUnknown) || errors.Is(err, manualhold.ErrUnknown) {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.Unknown))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "manual_hold_unknown"})
		return
	}
	status, code := http.StatusServiceUnavailable, "manual_hold_unknown"
	result := string(lifecycle.Unknown)
	if errors.Is(err, lf.ErrHeld) && !errors.Is(err, lf.ErrUnknown) && !errors.Is(err, manualhold.ErrUnknown) {
		code, result = "manual_hold_held", string(lifecycle.Held)
	}
	if errors.Is(err, lf.ErrConflict) || errors.Is(err, manualhold.ErrRefused) {
		status, code = http.StatusConflict, "manual_hold_revision_changed"
	}
	if errors.Is(err, store.ErrNotFound) {
		status, code = http.StatusNotFound, "not_found"
	}
	if errors.Is(err, store.ErrForbidden) {
		status, code = http.StatusForbidden, "permission_denied"
	}
	if status == http.StatusServiceUnavailable {
		w.Header().Set(lifecycleResultHeader, result)
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) manualHoldAttemptResponse(ctx context.Context, attempt store.AutoStartHoldAttempt) (map[string]any, error) {
	stream, err := s.streams.GetStream(ctx, attempt.Request.StreamID)
	if err != nil {
		return nil, err
	}
	// Preserve the original confirmation snapshot, not today's current revisions.
	encoded, _ := json.Marshal(attempt)
	var projection map[string]any
	_ = json.Unmarshal(encoded, &projection)
	request := projection["request"].(map[string]any)
	request["expected_stream_revision"] = attempt.Request.ExpectedStreamUpdatedAt.UTC().Format(time.RFC3339Nano)
	currentHold, holdErr := s.streams.(store.ManualAutoStartHoldStore).ObserveAutoStartHold(ctx, stream.ID)
	if holdErr != nil {
		currentHold.Known = false
		currentHold.Held = true
	}
	response := map[string]any{"stream": stream, "hold": currentHold, "attempt": projection}
	if s.manualAutoStartGate != nil {
		stages := []string{"begin", "commit"}
		if attempt.Request.Operation != store.AutoStartExplicitResume {
			stages = []string{"stop", "confirm"}
		}
		ledger := map[string]any{}
		for _, stage := range stages {
			inspection, inspectErr := s.manualAutoStartGate.gate.InspectExplicitCall(ctx, attempt.Request.RequestID+":"+stage, manualHoldCall(attempt.Request, stage))
			if inspectErr != nil {
				ledger[stage] = map[string]string{"knowledge": "UNKNOWN"}
				continue
			}
			ledger[stage] = map[string]any{"attempt_id": inspection.Ledger.Reservation.Request.ID, "phase_hint": inspection.Ledger.Phase, "cp_commit_observed": inspection.CP != nil, "receipt_observed": inspection.Receipt != nil, "held": inspection.Quarantined}
		}
		response["lifecycle_inspections"] = ledger
	}
	if attempt.Request.Operation == store.AutoStartExplicitResume {
		if admission, lookupErr := s.streams.(store.ManualAutoStartHoldStore).LookupAutoStartAdmission(ctx, attempt.Request.StreamID, attempt.Request.RequestID+":start"); lookupErr == nil {
			response["start_observations"] = s.inspectStartBoundaries(ctx, admission)
		}
	}
	return response, nil
}

func (s *Server) inspectManualAutoStart(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	attempt, err := owner.InspectAutoStartAttempt(r.Context(), r.PathValue("id"), r.PathValue("request_id"))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	response, err := s.manualHoldAttemptResponse(r.Context(), attempt)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) currentResumeCondition(ctx context.Context, stream store.Stream, hold store.AutoStartHold, services []store.RegisteredService) (lifecycle.Fact[bool], error) {
	unknown := lifecycle.Fact[bool]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: "AUTO_START_CONDITION_NOT_OBSERVED"}}
	observed := func(present bool) lifecycle.Fact[bool] {
		return lifecycle.Fact[bool]{State: lifecycle.Observed, Value: &present, Observation: &lifecycle.ObservationContext{OwnerID: "control-panel", FrameRef: manualHoldReference("condition", stream.ID, fmt.Sprint(hold.Revision)), Scope: lifecycle.ID(stream.ID), SourceRevision: hold.Revision}}
	}
	if stream.AutoStartTrigger != autoStartTriggerDiscordVoiceJoin || !isAutoStartableStreamStatus(stream.Status) {
		return observed(false), nil
	}
	client, ok := s.dispatcher.(manualHoldDispatcher)
	if !ok {
		return unknown, manualhold.ErrUnknown
	}
	for _, service := range services {
		if service.ServiceType != "discord_bot" {
			continue
		}
		monitor, err := s.runtimeDiscordWaitingMonitor(ctx, service, nil)
		if err != nil {
			return unknown, err
		}
		if monitor == nil {
			return unknown, manualhold.ErrUnknown
		}
		for _, binding := range monitor.Bindings {
			if binding.StreamID == stream.ID && binding.Ready {
				epoch := fmt.Sprintf("%s/%d/%s", binding.SelectionEpoch.UTC().Format(time.RFC3339Nano), binding.TargetRevision, binding.ProfileRevision)
				condition, err := client.ObserveAutoStartPresence(ctx, service, stream.ID, epoch)
				if err != nil {
					return unknown, err
				}
				if !condition.Known {
					return unknown, manualhold.ErrUnknown
				}
				if condition.StreamRevision != stream.UpdatedAt.UTC().Format(time.RFC3339Nano) || condition.SettingsRevision != stream.SettingsRevision || condition.RunID != stream.ArchiveRunID {
					return unknown, lf.ErrConflict
				}
				return observed(condition.Present), nil
			}
		}
	}
	return unknown, manualhold.ErrUnknown
}

func (s *Server) resumeManualAutoStart(w http.ResponseWriter, r *http.Request) {
	var original autoStartOriginal
	if decodeOptionalSingleJSON(r, &original) != nil || original.RequestID == "" || len(original.RequestID) > 80 || original.ExpectedStreamRevision == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_resume_request"})
		return
	}
	owner, ok := s.streams.(store.ManualAutoStartHoldStore)
	if !ok {
		writeManualHoldError(w, manualhold.ErrUnknown)
		return
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	// Existing Session/CSRF/scope checks ran in middleware. Re-evaluate the current
	// source immediately before recording this immutable confirmation.
	current, err := s.authenticateIdentity(r)
	if err != nil {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	var allowed bool
	current, allowed = s.operationalCurrent(w, r, current)
	if !allowed {
		return
	}
	if !security.HasPermission(current.Permissions, "streams.start") {
		writeManualHoldError(w, store.ErrForbidden)
		return
	}
	expected, err := time.Parse(time.RFC3339Nano, original.ExpectedStreamRevision)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_resume_request"})
		return
	}
	req := store.AutoStartHoldRequest{RequestID: original.RequestID, StreamID: stream.ID, Operation: store.AutoStartExplicitResume, ActorUserID: current.User.ID, ExpectedHoldRevision: original.ExpectedHoldRevision, ExpectedStreamUpdatedAt: expected, ExpectedSettingsRevision: original.ExpectedSettingsRevision, ExpectedRunID: original.ExpectedRunID, Correlation: autoStartCorrelation(original.RequestID, store.AutoStartExplicitResume, "resume-close")}
	if prior, inspectErr := owner.InspectAutoStartAttempt(r.Context(), stream.ID, req.RequestID); inspectErr == nil {
		if prior.Request.Operation != store.AutoStartExplicitResume || prior.Request.ActorUserID != req.ActorUserID || prior.Request.ExpectedHoldRevision != req.ExpectedHoldRevision || !prior.Request.ExpectedStreamUpdatedAt.Equal(req.ExpectedStreamUpdatedAt) || prior.Request.ExpectedSettingsRevision != req.ExpectedSettingsRevision || prior.Request.ExpectedRunID != req.ExpectedRunID {
			writeManualHoldError(w, lf.ErrConflict)
			return
		}
		response, responseErr := s.manualHoldAttemptResponse(r.Context(), prior)
		if responseErr != nil {
			writeManualHoldError(w, responseErr)
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	} else if !errors.Is(inspectErr, store.ErrNotFound) {
		writeManualHoldError(w, inspectErr)
		return
	}
	hold, err := owner.ObserveAutoStartHold(r.Context(), stream.ID)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	if hold.Revision != original.ExpectedHoldRevision || !stream.UpdatedAt.Equal(expected) || stream.SettingsRevision != original.ExpectedSettingsRevision || stream.ArchiveRunID != original.ExpectedRunID {
		writeManualHoldError(w, lf.ErrConflict)
		return
	}
	services, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	services = primaryStreamAssignments(services)
	req.RequiredTargets = manualHoldTargets(services, stream.ID)
	condition, err := s.currentResumeCondition(r.Context(), stream, hold, services)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	var attempt store.AutoStartHoldAttempt
	var created bool
	err = s.runManualHoldMutation(r.Context(), req, "begin", func(ctx context.Context) error {
		var writeErr error
		attempt, created, writeErr = owner.BeginExplicitResume(ctx, req)
		return writeErr
	})
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	if !created {
		response, _ := s.manualHoldAttemptResponse(r.Context(), attempt)
		writeJSON(w, http.StatusOK, response)
		return
	}
	evidence, err := s.closeManualHoldTargets(r.Context(), stream, attempt, services)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	// Closing a busy target may take time. Re-read actual current presence after
	// the acknowledgements, before the final CAS, with no SQL lock held.
	condition, err = s.currentResumeCondition(r.Context(), stream, hold, services)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	r, allowed = s.recheckResumeActor(w, r, req.ActorUserID)
	if !allowed {
		return
	}
	err = s.runManualHoldMutation(r.Context(), req, "commit", func(ctx context.Context) error {
		var writeErr error
		attempt, writeErr = owner.CommitExplicitResume(ctx, req, condition, evidence)
		return writeErr
	})
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	if condition.Value != nil && *condition.Value {
		r, allowed = s.recheckResumeActor(w, r, req.ActorUserID)
		if !allowed {
			return
		}
		child := r.Clone(context.WithValue(r.Context(), resumeStartOriginKey{}, req.RequestID))
		child = child.WithContext(store.WithAutomaticStartHold(child.Context(), attempt.Hold.Revision))
		childOriginal := autoStartOriginal{RequestID: req.RequestID + ":start", ExpectedHoldRevision: attempt.Hold.Revision, ExpectedStreamRevision: req.ExpectedStreamUpdatedAt.UTC().Format(time.RFC3339Nano), ExpectedSettingsRevision: req.ExpectedSettingsRevision, ExpectedRunID: req.ExpectedRunID}
		child = child.WithContext(context.WithValue(child.Context(), autoStartOriginalKey{}, childOriginal))
		child.Body = io.NopCloser(bytes.NewReader([]byte("{}")))
		capture := newManualStartCapture()
		s.startStreamWithMaterialization(capture, child, nil)
		resource := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: "control-panel", ResourceID: lifecycle.ID(stream.ID)}
		observation := lifecycle.BoundaryObservation{Address: lifecycle.BoundaryAddress{Kind: lifecycle.ProjectionBoundary, OwnerID: "control-panel", Resource: resource, SubBoundary: lifecycle.CPOutcome}, Knowledge: lifecycle.KnowledgeUnknown, Presence: lifecycle.NotObserved, Outcome: lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: "START_RESULT_NOT_ESTABLISHED"}}}
		// A local response cannot prove all remote boundaries. Persist the known
		// dispatch response reference while leaving remote receipt knowledge separate.
		// The child HTTP result alone cannot establish the target apply boundary.
		// Keep its knowledge UNKNOWN until the original target records are read.
		if admission, lookupErr := owner.LookupAutoStartAdmission(r.Context(), stream.ID, req.RequestID+":start"); lookupErr == nil && capture.Code >= 200 && capture.Code < 300 {
			boundaries := s.inspectStartBoundaries(r.Context(), admission)
			accepted := len(boundaries) > 0 && len(boundaries) == len(admission.Records)
			for _, boundary := range boundaries {
				if boundary.Knowledge != lifecycle.Known || boundary.Outcome.Value == nil || *boundary.Outcome.Value != lifecycle.SuccessAtBoundary {
					accepted = false
				}
			}
			if accepted {
				value := lifecycle.SuccessAtBoundary
				observation.Knowledge, observation.Presence = lifecycle.Known, lifecycle.Present
				observation.Outcome = lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Observed, Value: &value, Observation: &lifecycle.ObservationContext{OwnerID: resource.OwnerID, FrameRef: lifecycle.ID(req.RequestID), Scope: lifecycle.ID(stream.ID), SourceRevision: attempt.Hold.Revision}}
			}
		}
		attempt, err = owner.RecordAutoStartStartResult(r.Context(), stream.ID, req.RequestID, observation)
		if err != nil {
			writeManualHoldError(w, err)
			return
		}
	}
	response, err := s.manualHoldAttemptResponse(r.Context(), attempt)
	if err != nil {
		writeManualHoldError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

// Capture only a bounded local response. No testing package enters production,
// and neither a timeout nor a 2xx response is promoted into a remote receipt.
type manualStartCapture struct {
	header http.Header
	Code   int
	body   bytes.Buffer
}

func newManualStartCapture() *manualStartCapture {
	return &manualStartCapture{header: make(http.Header)}
}
func (c *manualStartCapture) Header() http.Header { return c.header }
func (c *manualStartCapture) WriteHeader(code int) {
	if c.Code == 0 {
		c.Code = code
	}
}
func (c *manualStartCapture) Write(data []byte) (int, error) {
	if c.Code == 0 {
		c.Code = http.StatusOK
	}
	if c.body.Len()+len(data) <= 65536 {
		_, _ = c.body.Write(data)
	}
	return len(data), nil
}
