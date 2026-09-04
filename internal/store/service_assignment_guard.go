package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ServiceAssignmentMutation is the store-authoritative assignment contract.
// ExpectedCurrentStreamID is optional. When present, the mutation is applied
// only if the service still has that owner (the empty string means unassigned).
type ServiceAssignmentMutation struct {
	ServiceID               string
	StreamID                string
	ActorUserID             string
	AssignmentRole          string
	ExpectedCurrentStreamID *string
}

// ServiceUnassignmentMutation is the store-authoritative unassignment
// contract. ExpectedCurrentStreamID provides the same CAS fence as assignment.
type ServiceUnassignmentMutation struct {
	ServiceID               string
	ActorUserID             string
	ExpectedCurrentStreamID *string
}

// StreamStartClaimRequest describes the exact preflight observation that must
// still hold when a start acquires lifecycle ownership. A configured Discord
// service may be materialized only inside the same critical section.
type StreamStartClaimRequest struct {
	StreamID                   string
	ExpectedStatus             string
	ExpectedStreamUpdatedAt    time.Time
	ExpectedPrimaryAssignments []RegisteredService
	MaterializeServiceID       string
	MaterializeActorUserID     string
	ArchiveEnabled             bool
	ArchiveStartedAt           time.Time
}

type StreamStartAssignmentClaim struct {
	AssignmentID string
	ServiceID    string
	ServiceType  string
	Role         string
}

type StreamArchiveAuthority struct {
	RunID     string
	StartedAt *time.Time
}

type StreamStartOwnershipClaim struct {
	StreamID        string
	StreamUpdatedAt time.Time
	StreamIdentity  string
	Assignments     []StreamStartAssignmentClaim
	Archive         StreamArchiveAuthority
}

func streamStartOwnershipIdentity(stream Stream) string {
	encoded, err := json.Marshal(stream)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type ClaimedStreamStart struct {
	Stream             Stream
	PrimaryAssignments []RegisteredService
	ArchiveAuthority   StreamArchiveAuthority
	OwnershipClaim     StreamStartOwnershipClaim
	Materialized       *RegisteredService
}

type StreamStartClaimStore interface {
	ClaimStreamStart(ctx context.Context, request StreamStartClaimRequest) (ClaimedStreamStart, error)
	TransitionClaimedStreamStart(ctx context.Context, claim StreamStartOwnershipClaim, status string) (stream Stream, transitioned bool, err error)
}

type ServiceAssignmentGuardStore interface {
	AssignServiceToStreamGuarded(ctx context.Context, mutation ServiceAssignmentMutation) (RegisteredService, error)
	UnassignServiceFromStreamGuarded(ctx context.Context, mutation ServiceUnassignmentMutation) (RegisteredService, error)
	BeginStreamArchiveRetryGuarded(ctx context.Context, serviceID, streamID string) (Stream, error)
}

var (
	ErrServiceAssignmentConflict         = errors.New("service assignment conflict")
	ErrServiceAssignmentProtectedStream  = errors.New("service assignment protected stream")
	ErrServiceUnassignProtectedStream    = errors.New("service unassign protected stream")
	ErrServiceAssignmentGuardUnavailable = errors.New("service assignment guard unavailable")
)

type streamAssignmentProtection struct {
	Stream
	ArchiveRetryPending  bool
	HasArchiveReport     bool
	HasRecordingArtifact bool
}

const archiveRetryAssignmentGuardLogMessage = "archive retry assignment guard pending"
const archiveRetryAssignmentGuardClosedLogMessage = "archive retry assignment guard closed"

// streamLogGuardPendingCondition treats stream_logs as the append-only history
// enforced by migration 077. A pending marker remains authoritative until a
// later closure row explicitly references that marker ID in fields.pending_id.
const streamLogGuardPendingCondition = `EXISTS (
  SELECT 1
  FROM stream_logs guard_pending
  WHERE guard_pending.stream_id = s.id
    AND guard_pending.message = ?
    AND NOT EXISTS (
      SELECT 1
      FROM stream_logs guard_closed
      WHERE guard_closed.stream_id = guard_pending.stream_id
        AND guard_closed.message = ?
        AND JSON_UNQUOTE(JSON_EXTRACT(guard_closed.fields, '$.pending_id')) = guard_pending.id
    )
)`

// protected reports the same archive-processing authority used by
// ListArchiveProcessingStreams, in addition to the active lifecycle states.
// Archive work is deliberately not inferred from stream.status alone.
func (state streamAssignmentProtection) protected() bool {
	status := strings.ToLower(strings.TrimSpace(state.Status))
	switch status {
	case "starting", "live", "stopping":
		return true
	}
	if state.ArchiveRetryPending {
		return true
	}
	if strings.TrimSpace(state.ArchiveProfileID) == "" {
		return false
	}
	if state.ArchiveStartedAt != nil {
		return state.ArchiveReportedAt == nil && (status == "completed" || status == "ready")
	}
	return false
}
