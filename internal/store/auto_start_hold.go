package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
)

const (
	AutoStartManualStop     = "manual_stop"
	AutoStartForceStop      = "manual_force_stop"
	AutoStartExplicitResume = "explicit_resume"
	manualHoldModuleCommit  = "d32ea3514b1c1010391caae0348b0ff9e751f447"
)

// AutoStartHold is CP-owned product metadata. Revision is a record CAS, not a
// replacement for a Run, admission epoch, Workload identity or execution fence.
// Known=false must never be interpreted as an observed unheld value.
type AutoStartHold struct {
	StreamID          string    `json:"stream_id"`
	Known             bool      `json:"known"`
	Held              bool      `json:"held"`
	Revision          uint64    `json:"revision"`
	Reason            string    `json:"reason"`
	ActorUserID       string    `json:"actor_user_id,omitempty"`
	OriginalAttemptID string    `json:"original_attempt_id,omitempty"`
	PendingAttemptID  string    `json:"pending_attempt_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// AutoStartHoldRequest contains only immutable, non-secret references. HTTP
// callers must authenticate/authorize the actual Human separately; this type
// neither grants authority nor converts Human rights into a Service identity.
type AutoStartHoldRequest struct {
	RequestID                string                  `json:"request_id"`
	StreamID                 string                  `json:"stream_id"`
	Operation                string                  `json:"operation"`
	ActorUserID              string                  `json:"actor_user_id"`
	ExpectedHoldRevision     uint64                  `json:"expected_hold_revision"`
	ExpectedStreamUpdatedAt  time.Time               `json:"expected_stream_updated_at"`
	ExpectedSettingsRevision string                  `json:"expected_settings_revision"`
	ExpectedRunID            string                  `json:"expected_run_id"`
	Correlation              lifecycle.Correlation   `json:"correlation"`
	RequiredTargets          []lifecycle.ResourceRef `json:"required_targets"`
	OriginalMetadata         map[string]string       `json:"original_metadata,omitempty"`
}

type AutoStartHoldAttempt struct {
	Request             AutoStartHoldRequest          `json:"request"`
	Hold                AutoStartHold                 `json:"hold"`
	Condition           lifecycle.Fact[bool]          `json:"condition"`
	FenceEvidence       []lifecycle.Record            `json:"fence_evidence,omitempty"`
	HoldCommit          lifecycle.BoundaryObservation `json:"hold_commit"`
	StartResult         lifecycle.BoundaryObservation `json:"start_result"`
	Gate                lifecycle.Gate                `json:"gate"`
	Final               bool                          `json:"final"`
	StartResultRecorded bool                          `json:"start_result_recorded"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
}

type AutoStartHoldHistory struct {
	Sequence       uint64        `json:"sequence"`
	StreamID       string        `json:"stream_id"`
	RequestID      string        `json:"request_id,omitempty"`
	Operation      string        `json:"operation"`
	BeforeRevision uint64        `json:"before_revision"`
	Hold           AutoStartHold `json:"hold"`
	AuditID        string        `json:"audit_id"`
	CreatedAt      time.Time     `json:"created_at"`
}

type ManualAutoStartHoldStore interface {
	ObserveAutoStartHold(context.Context, string) (AutoStartHold, error)
	BeginExplicitResume(context.Context, AutoStartHoldRequest) (AutoStartHoldAttempt, bool, error)
	CommitExplicitResume(context.Context, AutoStartHoldRequest, lifecycle.Fact[bool], []lifecycle.Record) (AutoStartHoldAttempt, error)
	ConfirmManualStopHold(context.Context, AutoStartHoldRequest, []lifecycle.Record) (AutoStartHoldAttempt, error)
	InspectAutoStartAttempt(context.Context, string, string) (AutoStartHoldAttempt, error)
	ListHoldHistory(context.Context, string) ([]AutoStartHoldHistory, error)
	RecordAutoStartStartResult(context.Context, string, string, lifecycle.BoundaryObservation) (AutoStartHoldAttempt, error)
	SaveAutoStartAdmission(context.Context, AutoStartAdmission) error
	InspectAutoStartAdmission(context.Context, string, string) (AutoStartAdmission, error)
	LookupAutoStartAdmission(context.Context, string, string) (AutoStartAdmission, error)
	LookupStartClaimRequest(context.Context, string, string) (StartClaimRequestOriginal, error)
}

// AutoStartAdmission is a persisted product correlation with the existing G0
// records, not an Authority constructor, a new generation or a fence schema.
type AutoStartAdmission struct {
	RequestID        string             `json:"request_id"`
	StreamID         string             `json:"stream_id"`
	Automatic        bool               `json:"automatic"`
	HoldRevision     uint64             `json:"hold_revision"`
	StreamUpdatedAt  time.Time          `json:"stream_updated_at"`
	SettingsRevision string             `json:"settings_revision"`
	RunID            string             `json:"run_id"`
	Records          []lifecycle.Record `json:"records"`
}

type manualStopHoldKey struct{}
type automaticStartHoldKey struct{}
type manualStartHoldKey struct{}

// WithManualStopHold is installed only by an authenticated operator Stop path.
// Service Stop, cleanup and failure convergence must not receive this binding.
func WithManualStopHold(ctx context.Context, req AutoStartHoldRequest) context.Context {
	return context.WithValue(ctx, manualStopHoldKey{}, cloneAutoStart(req))
}

// WithAutomaticStartHold carries an observation, never permission. The Store
// compares it under the same Stream lock used by ClaimStreamStart.
func WithAutomaticStartHold(ctx context.Context, expectedHoldRevision uint64) context.Context {
	return context.WithValue(ctx, automaticStartHoldKey{}, expectedHoldRevision)
}

// WithManualStartHold binds only a current record observation. It permits a
// confirmed held lane, never a pending operation, and cannot clear the Hold.
func WithManualStartHold(ctx context.Context, expectedHoldRevision uint64) context.Context {
	return context.WithValue(ctx, manualStartHoldKey{}, expectedHoldRevision)
}

func validHoldID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.:", ch)) {
			return false
		}
	}
	return true
}

func validateHoldRequest(req AutoStartHoldRequest) error {
	if !validHoldID(req.RequestID) || len(req.RequestID) > 96 || !validHoldID(req.StreamID) || !validHoldID(req.ActorUserID) ||
		req.ExpectedStreamUpdatedAt.IsZero() || req.ExpectedSettingsRevision == "" || len(req.ExpectedSettingsRevision) > 128 || len(req.ExpectedRunID) > 128 {
		return lf.ErrHeld
	}
	if req.Operation != AutoStartManualStop && req.Operation != AutoStartForceStop && req.Operation != AutoStartExplicitResume {
		return lf.ErrHeld
	}
	if string(req.Correlation.RequestID) != req.RequestID || string(req.Correlation.AttemptID) != req.RequestID ||
		!validHoldID(string(req.Correlation.OperationID)) || !validHoldID(string(req.Correlation.StepID)) || !validHoldID(string(req.Correlation.ExecutionKey)) {
		return lf.ErrHeld
	}
	if len(req.RequiredTargets) == 0 || len(req.RequiredTargets) > 16 {
		return lf.ErrHeld
	}
	seen := map[lifecycle.ResourceRef]bool{}
	for _, target := range req.RequiredTargets {
		if target.Kind != lifecycle.Stream || string(target.ResourceID) != req.StreamID || !validHoldID(string(target.OwnerID)) || seen[target] {
			return lf.ErrHeld
		}
		seen[target] = true
	}
	if len(req.OriginalMetadata) > 16 {
		return lf.ErrHeld
	}
	for key, value := range req.OriginalMetadata {
		switch key {
		case "source_ref", "generation_ref", "run_ref", "intent_ref", "frame_ref", "profile_ref":
		default:
			return lf.ErrHeld
		}
		if !validHoldID(value) {
			return lf.ErrHeld
		}
	}
	return nil
}

func checkHoldRequestStream(req AutoStartHoldRequest, stream Stream) error {
	if stream.ID != req.StreamID || stream.DeletedAt != nil || !stream.UpdatedAt.Equal(req.ExpectedStreamUpdatedAt) ||
		stream.SettingsRevision != req.ExpectedSettingsRevision || stream.ArchiveRunID != req.ExpectedRunID {
		return lf.ErrConflict
	}
	return nil
}

func unknownAutoStartHold(streamID string) AutoStartHold {
	return AutoStartHold{StreamID: streamID, Held: true}
}

func cloneAutoStart[T any](value T) T {
	var out T
	encoded, err := json.Marshal(value)
	if err != nil || json.Unmarshal(encoded, &out) != nil {
		panic("non-serializable automatic-start metadata")
	}
	return out
}

func sameHoldRequest(a, b AutoStartHoldRequest) bool {
	return reflect.DeepEqual(cloneAutoStart(a), cloneAutoStart(b))
}

func nextHold(hold AutoStartHold, req AutoStartHoldRequest, pending bool) (AutoStartHold, error) {
	if hold.Revision == ^uint64(0) {
		return AutoStartHold{}, lf.ErrConflict
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(hold.UpdatedAt) {
		now = hold.UpdatedAt.Add(time.Microsecond)
	}
	if hold.CreatedAt.IsZero() {
		hold.CreatedAt = now
	}
	hold.StreamID, hold.ActorUserID, hold.UpdatedAt = req.StreamID, req.ActorUserID, now
	hold.Revision++
	if pending {
		hold.PendingAttemptID = req.RequestID
	} else {
		hold.PendingAttemptID = ""
	}
	return hold, nil
}

func unknownHoldBoundary(streamID string, sub lifecycle.SubBoundary, reason string) lifecycle.BoundaryObservation {
	return lifecycle.BoundaryObservation{Address: lifecycle.BoundaryAddress{Kind: lifecycle.CPLifecycle, OwnerID: "control_panel", Resource: lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: "control_panel", ResourceID: lifecycle.ID(streamID)}, SubBoundary: sub}, Knowledge: lifecycle.KnowledgeUnknown, Presence: lifecycle.NotObserved, Outcome: lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: lifecycle.ID(reason)}}}
}

func knownHoldBoundary(hold AutoStartHold) lifecycle.BoundaryObservation {
	b := unknownHoldBoundary(hold.StreamID, lifecycle.ReceiptSave, "HOLD_COMMIT_NOT_OBSERVED")
	value := lifecycle.SuccessAtBoundary
	b.Knowledge, b.Presence = lifecycle.Known, lifecycle.Present
	b.Outcome = lifecycle.Fact[lifecycle.Outcome]{State: lifecycle.Observed, Value: &value, Observation: &lifecycle.ObservationContext{OwnerID: "control_panel", FrameRef: lifecycle.ID(hold.OriginalAttemptID), Scope: lifecycle.ID(hold.StreamID), SourceRevision: hold.Revision}}
	return b
}

// StartResult is the CP-owned projection of the required target observations.
// It must not impersonate a Provider or a CP transaction result.
func unknownStartProjection(streamID string) lifecycle.BoundaryObservation {
	result := unknownHoldBoundary(streamID, lifecycle.CPOutcome, "START_NOT_OBSERVED")
	result.Address.Kind = lifecycle.ProjectionBoundary
	return result
}

func newHoldAttempt(req AutoStartHoldRequest, hold AutoStartHold) AutoStartHoldAttempt {
	return AutoStartHoldAttempt{Request: cloneAutoStart(req), Hold: hold, Gate: lifecycle.Held,
		Condition:   lifecycle.Fact[bool]{State: lifecycle.Unavailable, Unavailable: &lifecycle.UnavailableReason{Code: "CONDITION_NOT_OBSERVED"}},
		HoldCommit:  unknownHoldBoundary(req.StreamID, lifecycle.ReceiptSave, "TARGET_GUARDS_NOT_CONFIRMED"),
		StartResult: unknownStartProjection(req.StreamID), CreatedAt: hold.UpdatedAt, UpdatedAt: hold.UpdatedAt}
}

func holdFenceEvidence(req AutoStartHoldRequest, hold AutoStartHold, evidence []lifecycle.Record) error {
	if len(evidence) != len(req.RequiredTargets) {
		return lf.ErrHeld
	}
	seen := map[lifecycle.ResourceRef]bool{}
	for _, record := range evidence {
		if manualhold.Validate(record, req.StreamID) != nil {
			return lf.ErrHeld
		}
		if string(record.Header.ContractRef.ModuleCommitRef) != manualHoldModuleCommit || record.Header.ContractRef.LogicalRevision != 1 ||
			record.Header.ProfileRef.ProfileID != lifecycle.ExplicitResume || record.Header.ProfileRef.Revision != 1 ||
			record.Header.Correlation != req.Correlation || record.Header.RecordRevision != hold.Revision {
			return lf.ErrHeld
		}
		found := false
		for _, target := range req.RequiredTargets {
			if record.Header.RecordKey.OwnerID != target.OwnerID || record.Header.ProducerRef.ServiceID != target.OwnerID || (record.Header.ProducerRef.ComponentID != "C04" && record.Header.ProducerRef.ComponentID != "C06") {
				continue
			}
			for _, resource := range record.Header.DomainRef.Resources {
				if resource == target && !seen[target] {
					seen[target], found = true, true
				}
			}
		}
		if !found {
			return lf.ErrHeld
		}
	}
	return nil
}

func validateHoldCondition(condition lifecycle.Fact[bool]) error {
	if condition.State != lifecycle.Observed || condition.Value == nil || condition.Observation == nil || condition.Unavailable != nil ||
		!validHoldID(string(condition.Observation.OwnerID)) || !validHoldID(string(condition.Observation.FrameRef)) || condition.Observation.SourceRevision == 0 {
		return lf.ErrUnknown
	}
	return nil
}

func validateStartBoundary(result lifecycle.BoundaryObservation, streamID string) error {
	if result.Address.Kind != lifecycle.ProjectionBoundary || result.Address.SubBoundary != lifecycle.CPOutcome || result.Address.Resource.Kind != lifecycle.Stream || string(result.Address.Resource.ResourceID) != streamID || !validHoldID(string(result.Address.OwnerID)) {
		return lf.ErrHeld
	}
	if result.Knowledge == lifecycle.Known {
		if result.Presence != lifecycle.Present || result.Outcome.State != lifecycle.Observed || result.Outcome.Value == nil || result.Outcome.Observation == nil || result.Outcome.Unavailable != nil || result.Outcome.Observation.SourceRevision == 0 {
			return lf.ErrHeld
		}
		switch *result.Outcome.Value {
		case lifecycle.SuccessAtBoundary, lifecycle.FailureAtBoundary, lifecycle.NoEffectProven, lifecycle.Pending:
		default:
			return lf.ErrHeld
		}
	} else if result.Knowledge == lifecycle.KnowledgeUnknown {
		if result.Presence != lifecycle.NotObserved || result.Outcome.State != lifecycle.Unavailable || result.Outcome.Value != nil || result.Outcome.Observation != nil || result.Outcome.Unavailable == nil {
			return lf.ErrHeld
		}
	} else {
		return lf.ErrHeld
	}
	return nil
}

func holdAttemptError(err error) error {
	if err == nil || errors.Is(err, lf.ErrConflict) || errors.Is(err, lf.ErrHeld) || errors.Is(err, ErrNotFound) {
		return err
	}
	return errors.Join(lf.ErrUnknown, err)
}
