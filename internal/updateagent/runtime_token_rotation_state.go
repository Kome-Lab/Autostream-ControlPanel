package updateagent

import (
	"errors"
	"strings"
	"time"
)

const (
	RuntimeTokenRotationStaged    = "staged"
	RuntimeTokenRotationProved    = "heartbeat_proved"
	RuntimeTokenRotationActivated = "activated"
)

const runtimeTokenRotationStateSchemaVersion = 1

// RuntimeTokenRotationRequest carries token record identifiers only. The raw
// runtime credential is returned once by the transport layer and must never be
// placed in this durable state, argv, environment variables, logs, or journal.
type RuntimeTokenRotationRequest struct {
	RotationID      string    `json:"rotation_id"`
	NodeID          string    `json:"node_id"`
	PreviousTokenID string    `json:"previous_token_id"`
	StagedTokenID   string    `json:"staged_token_id"`
	StagedAt        time.Time `json:"staged_at"`
}

type RuntimeTokenRotationState struct {
	SchemaVersion           int        `json:"schema_version"`
	Phase                   string     `json:"phase"`
	RotationID              string     `json:"rotation_id"`
	NodeID                  string     `json:"node_id"`
	PreviousTokenID         string     `json:"previous_token_id"`
	StagedTokenID           string     `json:"staged_token_id"`
	StagedAt                time.Time  `json:"staged_at"`
	HeartbeatProvedAt       *time.Time `json:"heartbeat_proved_at,omitempty"`
	ActivatedAt             *time.Time `json:"activated_at,omitempty"`
	EmergencyRevokedTokenID string     `json:"emergency_revoked_token_id,omitempty"`
	EmergencyRevokedAt      *time.Time `json:"emergency_revoked_at,omitempty"`
}

type RuntimeTokenRotationResult struct {
	State                         string `json:"state"`
	AlreadyActivated              bool   `json:"already_activated"`
	RevokeTokenID                 string `json:"revoke_token_id,omitempty"`
	AllowConsumedMutationRollback bool   `json:"allow_consumed_mutation_rollback,omitempty"`
}

func StageRuntimeTokenRotation(
	request RuntimeTokenRotationRequest,
	blockers HostLifecycleBlockers,
) (RuntimeTokenRotationState, error) {
	if blockers.mutationBlocked() {
		return RuntimeTokenRotationState{}, errors.New("host lifecycle mutation is active")
	}
	if !identifierPattern.MatchString(strings.TrimSpace(request.RotationID)) ||
		!identifierPattern.MatchString(strings.TrimSpace(request.NodeID)) ||
		!identifierPattern.MatchString(strings.TrimSpace(request.PreviousTokenID)) ||
		!identifierPattern.MatchString(strings.TrimSpace(request.StagedTokenID)) ||
		request.PreviousTokenID == request.StagedTokenID ||
		request.StagedAt.IsZero() ||
		request.StagedAt.Location() != time.UTC {
		return RuntimeTokenRotationState{}, errors.New("runtime token rotation request is invalid")
	}
	if request.RotationID != strings.TrimSpace(request.RotationID) ||
		request.NodeID != strings.TrimSpace(request.NodeID) ||
		request.PreviousTokenID != strings.TrimSpace(request.PreviousTokenID) ||
		request.StagedTokenID != strings.TrimSpace(request.StagedTokenID) {
		return RuntimeTokenRotationState{}, errors.New("runtime token rotation request is not canonical")
	}
	return RuntimeTokenRotationState{
		SchemaVersion:   runtimeTokenRotationStateSchemaVersion,
		Phase:           RuntimeTokenRotationStaged,
		RotationID:      request.RotationID,
		NodeID:          request.NodeID,
		PreviousTokenID: request.PreviousTokenID,
		StagedTokenID:   request.StagedTokenID,
		StagedAt:        request.StagedAt,
	}, nil
}

func ProveRuntimeTokenHeartbeat(
	state RuntimeTokenRotationState,
	authenticatedTokenID string,
	heartbeatAt time.Time,
) (RuntimeTokenRotationState, error) {
	if err := state.validate(); err != nil {
		return RuntimeTokenRotationState{}, err
	}
	if authenticatedTokenID != state.StagedTokenID ||
		heartbeatAt.IsZero() ||
		heartbeatAt.Location() != time.UTC ||
		heartbeatAt.Before(state.StagedAt) {
		return RuntimeTokenRotationState{}, errors.New("staged runtime token heartbeat proof is invalid")
	}
	switch state.Phase {
	case RuntimeTokenRotationStaged:
		provedAt := heartbeatAt
		state.Phase = RuntimeTokenRotationProved
		state.HeartbeatProvedAt = &provedAt
		return state, nil
	case RuntimeTokenRotationProved, RuntimeTokenRotationActivated:
		return state, nil
	default:
		return RuntimeTokenRotationState{}, errors.New("runtime token rotation phase is invalid")
	}
}

func ActivateRuntimeTokenRotation(
	state RuntimeTokenRotationState,
	authenticatedTokenID string,
	now time.Time,
) (RuntimeTokenRotationState, RuntimeTokenRotationResult, error) {
	if err := state.validate(); err != nil {
		return RuntimeTokenRotationState{}, RuntimeTokenRotationResult{}, err
	}
	if authenticatedTokenID != state.StagedTokenID ||
		now.IsZero() ||
		now.Location() != time.UTC ||
		now.Before(state.StagedAt) {
		return RuntimeTokenRotationState{}, RuntimeTokenRotationResult{}, errors.New("runtime token activation authorization is invalid")
	}
	if state.Phase == RuntimeTokenRotationActivated {
		return state, RuntimeTokenRotationResult{
			State:            RuntimeTokenRotationActivated,
			AlreadyActivated: true,
			RevokeTokenID:    state.PreviousTokenID,
		}, nil
	}
	if state.Phase != RuntimeTokenRotationProved ||
		state.HeartbeatProvedAt == nil ||
		now.Before(*state.HeartbeatProvedAt) {
		return RuntimeTokenRotationState{}, RuntimeTokenRotationResult{}, errors.New("new-token heartbeat proof is required before activation")
	}
	activatedAt := now
	state.Phase = RuntimeTokenRotationActivated
	state.ActivatedAt = &activatedAt
	return state, RuntimeTokenRotationResult{
		State:         RuntimeTokenRotationActivated,
		RevokeTokenID: state.PreviousTokenID,
	}, nil
}

func EmergencyRevokeRuntimeToken(
	state RuntimeTokenRotationState,
	tokenID string,
	consumedMutationRollbackPending bool,
	now time.Time,
) (RuntimeTokenRotationState, RuntimeTokenRotationResult, error) {
	if err := state.validate(); err != nil {
		return RuntimeTokenRotationState{}, RuntimeTokenRotationResult{}, err
	}
	if (tokenID != state.PreviousTokenID && tokenID != state.StagedTokenID) ||
		now.IsZero() ||
		now.Location() != time.UTC {
		return RuntimeTokenRotationState{}, RuntimeTokenRotationResult{}, errors.New("emergency runtime token revoke request is invalid")
	}
	revokedAt := now
	state.EmergencyRevokedTokenID = tokenID
	state.EmergencyRevokedAt = &revokedAt
	return state, RuntimeTokenRotationResult{
		State:                         state.Phase,
		RevokeTokenID:                 tokenID,
		AllowConsumedMutationRollback: consumedMutationRollbackPending,
	}, nil
}

func (s RuntimeTokenRotationState) validate() error {
	if s.SchemaVersion != runtimeTokenRotationStateSchemaVersion ||
		!identifierPattern.MatchString(s.RotationID) ||
		!identifierPattern.MatchString(s.NodeID) ||
		!identifierPattern.MatchString(s.PreviousTokenID) ||
		!identifierPattern.MatchString(s.StagedTokenID) ||
		s.PreviousTokenID == s.StagedTokenID ||
		s.StagedAt.IsZero() ||
		s.StagedAt.Location() != time.UTC {
		return errors.New("runtime token rotation state is invalid")
	}
	switch s.Phase {
	case RuntimeTokenRotationStaged:
		if s.HeartbeatProvedAt != nil || s.ActivatedAt != nil {
			return errors.New("staged runtime token rotation contains later proof")
		}
	case RuntimeTokenRotationProved:
		if s.HeartbeatProvedAt == nil ||
			s.HeartbeatProvedAt.Before(s.StagedAt) ||
			s.ActivatedAt != nil {
			return errors.New("runtime token heartbeat proof is invalid")
		}
	case RuntimeTokenRotationActivated:
		if s.HeartbeatProvedAt == nil ||
			s.ActivatedAt == nil ||
			s.HeartbeatProvedAt.Before(s.StagedAt) ||
			s.ActivatedAt.Before(*s.HeartbeatProvedAt) {
			return errors.New("activated runtime token rotation proof is invalid")
		}
	default:
		return errors.New("runtime token rotation phase is invalid")
	}
	return nil
}
