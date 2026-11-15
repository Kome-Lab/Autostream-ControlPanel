package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/videocover"
)

const lifecycleOutcomeHeader = "X-AutoStream-Lifecycle-Outcome"
const lifecycleResultHeader = "X-AutoStream-Lifecycle-Result"
const maxLifecycleOutcomeHeaderBytes = 32768

// LifecycleOutcomeBinding is an explicit owner composition seam. Original must
// come from the persisted original request, not from client query parameters.
// Frames name actual owner reads of that exact request/snapshot. Profiles,
// limits, missing axes, original Attempt and Binding IDs have no defaults.
type LifecycleOutcomeBinding struct {
	Original       lifecycle.Record
	Profile        lifecycle.ComparisonProfile
	CurrentVector  lifecycle.RevisionVector
	OriginalTarget lifecycle.QueryTarget
	Query          lifecycle.QueryTarget
	Frames         []lifecycle.SourceFrame
	Gate           lifecycle.Gate
	CoverExpected  *videocover.State
}

type LifecycleOutcomeResolver interface {
	ResolveStop(context.Context, store.NormalStopObservation) (LifecycleOutcomeBinding, error)
	ResolveCover(context.Context, videocover.State) (LifecycleOutcomeBinding, error)
}

// LifecycleOutcomeBridge exposes a bounded, opt-in synthetic transport using
// the shared CC08 DTO directly. It issues no authority or production wire
// qualification. Normal startup deliberately supplies no synthetic resolver.
type LifecycleOutcomeBridge struct{ Resolver LifecycleOutcomeResolver }

func WithLifecycleOutcomeBridge(bridge *LifecycleOutcomeBridge) ServerOption {
	return func(s *Server) { s.lifecycleOutcomes = bridge }
}

func wantsLifecycleOutcome(r *http.Request) bool {
	return r.URL.Query().Get("lifecycle_outcome") == "1"
}

func unknownOutcome(address lifecycle.BoundaryAddress, code lifecycle.ID) lifecycle.BoundaryObservation {
	return lifecycle.BoundaryObservation{Address: address, Knowledge: lifecycle.KnowledgeUnknown, Presence: lifecycle.NotObserved, Outcome: lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: code}}}
}

func knownOutcome(address lifecycle.BoundaryAddress, frame lifecycle.SourceFrame, value lifecycle.Outcome) lifecycle.BoundaryObservation {
	return lifecycle.BoundaryObservation{Address: address, Knowledge: lifecycle.Known, Presence: lifecycle.Present, Outcome: lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Observed, Value: &value, Observation: &lifecycle.ObservationContext{OwnerID: frame.OwnerID, FrameRef: frame.FrameRef, Scope: frame.Scope, SourceRevision: frame.SourceRevision}}}
}

func outcomeFrame(binding LifecycleOutcomeBinding, owner lifecycle.ID) (lifecycle.SourceFrame, bool) {
	for _, frame := range binding.Frames {
		if frame.OwnerID == owner && frame.Scope == binding.Query.Scope && frame.SourceRevision > 0 && frame.FrameRef != "" {
			return frame, true
		}
	}
	return lifecycle.SourceFrame{}, false
}

func boundVectorValue(binding LifecycleOutcomeBinding, axis lifecycle.Axis, streamID string, literal string, number uint64) bool {
	if binding.Original.Effect == nil {
		return false
	}
	for _, entry := range binding.CurrentVector {
		if entry.Key.Axis != axis || entry.Key.Resource.ResourceID != lifecycle.ID(streamID) || entry.Key.Resource != binding.Original.Effect.Target {
			continue
		}
		if literal != "" {
			return entry.Value.Kind == lifecycle.PresentLiteral && entry.Value.Literal != nil && *entry.Value.Literal == lifecycle.ID(literal)
		}
		return entry.Value.Kind == lifecycle.PresentUint64 && entry.Value.Uint64 != nil && *entry.Value.Uint64 == number
	}
	return false
}

func validOutcomeBinding(binding LifecycleOutcomeBinding, streamID string, profile lifecycle.ProfileID) bool {
	if binding.Profile.Ref.ProfileID != profile || lifecycle.ValidateProfile(binding.Profile).Kind != lifecycle.StructurallyCoherent ||
		binding.Original.Header.ContractRef.ContractID != lifecycle.CC07 || binding.Original.Effect == nil ||
		lifecycle.ValidateRecord(binding.Original, binding.Profile).Kind != lifecycle.StructurallyCoherent ||
		lifecycle.CompareVectors(binding.Original.Effect.SourceVector, binding.CurrentVector, binding.Profile).Kind != lifecycle.StructurallyCoherent {
		return false
	}
	if binding.Gate != lifecycle.Held && binding.Gate != lifecycle.NotQualified && binding.Gate != lifecycle.VerifiedForProfile {
		return false
	}
	if binding.Original.Effect.Target.Kind != lifecycle.Stream || binding.Original.Effect.Target.ResourceID != lifecycle.ID(streamID) || binding.Query != binding.OriginalTarget ||
		binding.OriginalTarget.Scope != lifecycle.ID(streamID) || binding.OriginalTarget.Correlation != binding.Original.Header.Correlation ||
		binding.OriginalTarget.ExecutionKey != binding.Original.Header.Correlation.ExecutionKey || binding.OriginalTarget.TargetRecordRef != binding.Original.Header.RecordKey.RecordID || binding.OriginalTarget.Purpose != lifecycle.PurposeLookup {
		return false
	}
	// CC02 validates the identical owner vector; no RUN literal is converted
	// into a clock, numeric revision, or today's current value.
	vector := lifecycle.Record{Header: binding.Original.Header, Vector: &lifecycle.VectorRecord{Vector: binding.Original.Effect.SourceVector}}
	vector.Header.ContractRef.ContractID = lifecycle.CC02
	vector.Header.RecordKey.ContractRef = vector.Header.ContractRef
	return lifecycle.ValidateRecord(vector, binding.Profile).Kind == lifecycle.StructurallyCoherent
}

func writeLifecycleInspection(w http.ResponseWriter, binding LifecycleOutcomeBinding, observations []lifecycle.BoundaryObservation) {
	set := lifecycle.ObservationSet{Observations: observations, Gate: binding.Gate}
	_, assessment := lifecycle.InspectSameAttempt(binding.OriginalTarget, binding.Query, set, binding.Profile.Limits)
	if assessment.Kind != lifecycle.StructurallyCoherent && assessment.Kind != lifecycle.Unknown && assessment.Kind != lifecycle.NeedsEvidence && assessment.Kind != lifecycle.PendingObligation {
		w.Header().Set(lifecycleResultHeader, string(assessment.Kind))
		return
	}
	frame, ok := outcomeFrame(binding, binding.Original.Header.RecordKey.OwnerID)
	if !ok {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.NeedsEvidence))
		return
	}
	header := binding.Original.Header
	header.ContractRef.ContractID = lifecycle.CC08
	header.RecordKey = lifecycle.RecordKey{ContractRef: header.ContractRef, OwnerID: frame.OwnerID, RecordID: frame.FrameRef}
	header.RecordRevision = frame.SourceRevision
	coverage := lifecycle.ID("SCOPED_RESULT_LOOKUP")
	record := lifecycle.Record{Header: header, Inspection: &lifecycle.InspectionRecord{
		Target: binding.Query, Observations: set, ObservedScopes: []lifecycle.ID{binding.Query.Scope}, SourceFrames: binding.Frames,
		Coverage:       lifecycle.Fact[lifecycle.ID]{State: lifecycle.Observed, Value: &coverage, Observation: &lifecycle.ObservationContext{OwnerID: frame.OwnerID, FrameRef: frame.FrameRef, Scope: frame.Scope, SourceRevision: frame.SourceRevision}},
		HistoricalRefs: []lifecycle.ID{binding.Original.Header.RecordKey.RecordID},
	}}
	validation := lifecycle.ValidateRecord(record, binding.Profile)
	w.Header().Set(lifecycleResultHeader, string(validation.Kind))
	if validation.Kind != lifecycle.StructurallyCoherent && validation.Kind != lifecycle.Unknown && validation.Kind != lifecycle.NeedsEvidence && validation.Kind != lifecycle.PendingObligation {
		return
	}
	body, err := json.Marshal(record)
	if err != nil || base64.RawURLEncoding.EncodedLen(len(body)) > maxLifecycleOutcomeHeaderBytes {
		w.Header().Set(lifecycleResultHeader, string(lifecycle.NeedsEvidence))
		return
	}
	w.Header().Set(lifecycleOutcomeHeader, base64.RawURLEncoding.EncodeToString(body))
}

func (s *Server) writeStopLifecycleOutcome(w http.ResponseWriter, r *http.Request, snapshot store.NormalStopObservation) {
	if !wantsLifecycleOutcome(r) {
		return
	}
	w.Header().Set(lifecycleResultHeader, string(lifecycle.NeedsEvidence))
	if s.lifecycleOutcomes == nil || s.lifecycleOutcomes.Resolver == nil {
		return
	}
	binding, err := s.lifecycleOutcomes.Resolver.ResolveStop(r.Context(), snapshot)
	if err != nil || !validOutcomeBinding(binding, snapshot.Stream.ID, lifecycle.StopLookup) || !boundVectorValue(binding, lifecycle.RunAxis, snapshot.Stream.ID, snapshot.SourceVersion, 0) {
		return
	}
	target := binding.Original.Effect.Target
	cp := lifecycle.BoundaryAddress{Kind: lifecycle.CPLifecycle, OwnerID: target.OwnerID, Resource: target, SubBoundary: lifecycle.ReceiptSave}
	observations := []lifecycle.BoundaryObservation{unknownOutcome(cp, "EXACT_STOP_RECEIPT_NOT_OBSERVED")}
	if snapshot.ReceiptFound {
		if frame, ok := outcomeFrame(binding, cp.OwnerID); ok {
			observations[0] = knownOutcome(cp, frame, lifecycle.SuccessAtBoundary)
		}
	}
	for _, boundary := range binding.Profile.RequiredBoundaries {
		if boundary != cp {
			observations = append(observations, unknownOutcome(boundary, "BOUNDARY_NOT_ESTABLISHED_BY_CP_RECEIPT"))
		}
	}
	writeLifecycleInspection(w, binding, observations)
}

func (s *Server) writeCoverLifecycleOutcome(w http.ResponseWriter, r *http.Request, expected videocover.State, encoder store.RegisteredService, result servicecall.VideoCoverDispatchResult, localKnown bool) {
	if !wantsLifecycleOutcome(r) {
		return
	}
	w.Header().Set(lifecycleResultHeader, string(lifecycle.NeedsEvidence))
	if s.lifecycleOutcomes == nil || s.lifecycleOutcomes.Resolver == nil {
		return
	}
	binding, ok := s.coverLifecycleBinding(r.Context(), expected)
	if !ok {
		return
	}
	target := binding.Original.Effect.Target
	local := lifecycle.BoundaryAddress{Kind: lifecycle.LocalPersistence, OwnerID: target.OwnerID, Resource: target, SubBoundary: lifecycle.LocalDBCommit}
	var backend lifecycle.BoundaryAddress
	backend, _ = coverBackendBoundary(binding, expected.StreamID, encoder.ServiceID)
	if backend.OwnerID == "" {
		return
	}
	observations := []lifecycle.BoundaryObservation{unknownOutcome(local, "LOCAL_RECORD_NOT_CONFIRMED"), unknownOutcome(backend, "BACKEND_RESULT_UNKNOWN")}
	if localKnown {
		if frame, ok := outcomeFrame(binding, local.OwnerID); ok {
			observations[0] = knownOutcome(local, frame, lifecycle.SuccessAtBoundary)
		}
	}
	if !result.Ambiguous && (result.Applied || result.SafeErrorCode != "") {
		if frame, ok := outcomeFrame(binding, backend.OwnerID); ok {
			outcome := lifecycle.FailureAtBoundary
			if result.Applied {
				outcome = lifecycle.SuccessAtBoundary
			}
			observations[1] = knownOutcome(backend, frame, outcome)
		}
	}
	for _, boundary := range binding.Profile.RequiredBoundaries {
		if boundary != local && boundary != backend {
			observations = append(observations, unknownOutcome(boundary, "OTHER_BOUNDARY_UNOBSERVED"))
		}
	}
	writeLifecycleInspection(w, binding, observations)
}

func (s *Server) coverLifecycleBinding(ctx context.Context, expected videocover.State) (LifecycleOutcomeBinding, bool) {
	if s.lifecycleOutcomes == nil || s.lifecycleOutcomes.Resolver == nil {
		return LifecycleOutcomeBinding{}, false
	}
	binding, err := s.lifecycleOutcomes.Resolver.ResolveCover(ctx, expected)
	if err != nil || !validOutcomeBinding(binding, expected.StreamID, lifecycle.EffectOutcome) || binding.Original.Effect.EffectID != lifecycle.ID(expected.LastIdempotencyKey) ||
		!boundVectorValue(binding, lifecycle.JobAxis, expected.StreamID, "", expected.JobGeneration) || !boundVectorValue(binding, lifecycle.Cover, expected.StreamID, "", expected.DesiredRevision) || binding.CoverExpected == nil || binding.Gate != lifecycle.VerifiedForProfile {
		return LifecycleOutcomeBinding{}, false
	}
	original := binding.CoverExpected
	if original.StreamID != expected.StreamID || original.JobGeneration != expected.JobGeneration || original.DesiredRevision != expected.DesiredRevision || original.DesiredActive != expected.DesiredActive || original.AssetVariantID != expected.AssetVariantID || original.LastIdempotencyKey != expected.LastIdempotencyKey {
		return LifecycleOutcomeBinding{}, false
	}
	return binding, true
}

func coverBackendBoundary(binding LifecycleOutcomeBinding, streamID, encoderID string) (lifecycle.BoundaryAddress, bool) {
	for _, boundary := range binding.Profile.RequiredBoundaries {
		if boundary.Kind != lifecycle.BackendMutation || boundary.SubBoundary != lifecycle.ServiceApply || boundary.OwnerID != lifecycle.ID(encoderID) || boundary.Resource.OwnerID != lifecycle.ID(encoderID) || boundary.Resource.Kind != lifecycle.Stream || boundary.Resource.ResourceID != lifecycle.ID(streamID) {
			continue
		}
		for _, resource := range binding.Original.Header.DomainRef.Resources {
			if resource == boundary.Resource {
				return boundary, true
			}
		}
	}
	return lifecycle.BoundaryAddress{}, false
}

func (s *Server) recordObservedCover(r *http.Request, expected videocover.State, result servicecall.VideoCoverDispatchResult) (videocover.State, error) {
	owner, ok := s.auth.(store.CoverOutcomeStore)
	if !ok || s.identityLifecycleRequired() {
		return videocover.State{}, lf.ErrHeld
	}
	if result.Ambiguous || !result.Applied && result.SafeErrorCode == "" {
		return expected, nil
	}
	status, resultText := "applied", "success"
	if !result.Applied {
		status, resultText = "failed", "failure"
	}
	action := "streams.video_cover.show"
	if !expected.DesiredActive {
		action = "streams.video_cover.hide"
	}
	current := currentFromContext(r.Context())
	event := store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, ActorIP: clientIP(r), UserAgent: r.UserAgent(), Action: action, ResourceType: "stream", ResourceID: expected.StreamID, Result: resultText, RequestID: requestID()}
	state, err := owner.RecordCoverObservation(r.Context(), s.audit, s.videoCovers, store.CoverRecordActor{UserID: current.User.ID, SessionHash: current.Session.TokenHash, OperationalRevision: current.OperationalRevision}, expected, status, result.SafeErrorCode, event)
	if err == nil && state.RecordedAction != nil {
		// Preserve the legacy noncritical announcements after the critical
		// record/audit commit. Never write a second audit, hold SQL locks during
		// notification, or announce an uncertain Commit as successful.
		event.Timestamp = state.UpdatedAt
		event.Metadata = map[string]any{"status": status, "reconciled": true, "desired_revision": expected.DesiredRevision, "job_generation": expected.JobGeneration}
		if status == "failed" {
			event.Metadata["error_code"] = state.LastErrorCode
		}
		s.appendStreamAuditLog(r.Context(), event)
		s.notifyAdminAuditEvent(event)
	}
	return state, err
}

func writeCoverObservationFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrUnauthorized):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
	case errors.Is(err, lf.ErrUnknown):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "video_cover_record_unknown"})
	case errors.Is(err, lf.ErrHeld):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "video_cover_record_held"})
	case errors.Is(err, videocover.ErrStaleGeneration), errors.Is(err, videocover.ErrRevisionConflict), errors.Is(err, videocover.ErrIdempotencyConflict):
		writeVideoCoverError(w, err)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_record_failed"})
	}
}
