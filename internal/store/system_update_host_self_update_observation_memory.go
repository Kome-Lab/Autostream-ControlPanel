package store

import (
	"context"
	"strings"
	"time"
)

func (s *MemorySystemUpdateStore) CancelSystemUpdateHostSelfUpdate(
	ctx context.Context,
	id, actorUserID string,
	expectedRevision int64,
	_ bool,
	now time.Time,
) (SystemUpdateHostSelfUpdate, error) {
	id = strings.TrimSpace(id)
	actorUserID = strings.TrimSpace(actorUserID)
	now = now.UTC()
	if !serviceIDPattern.MatchString(id) ||
		!serviceIDPattern.MatchString(actorUserID) ||
		expectedRevision < 1 || now.IsZero() {
		return SystemUpdateHostSelfUpdate{},
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, err
	}
	update, ok := s.hostSelfUpdates[id]
	if !ok {
		return SystemUpdateHostSelfUpdate{}, ErrNotFound
	}
	if update.Revision != expectedRevision {
		return SystemUpdateHostSelfUpdate{}, ErrSystemUpdateHostSelfUpdateStale
	}
	switch {
	case update.Status == SystemUpdateHostSelfUpdateQueued:
		update.Status = SystemUpdateHostSelfUpdateCanceled
		update.Code = "canceled_by_admin"
		update.CompletedAt = cloneTimePtr(&now)
	default:
		return SystemUpdateHostSelfUpdate{}, ErrSystemUpdateHostSelfUpdateCancel
	}
	update.Revision++
	update.UpdatedAt = now
	s.hostSelfUpdates[id] = update
	return publicSystemUpdateHostSelfUpdate(update), nil
}

func (s *MemorySystemUpdateStore) ObserveSystemUpdateHostSelfUpdate(
	ctx context.Context,
	observation SystemUpdateHostSelfUpdateObservation,
) (SystemUpdateHostSelfUpdate, bool, error) {
	observation.ExecutionHostID = strings.TrimSpace(observation.ExecutionHostID)
	observation.AgentServiceID = strings.TrimSpace(observation.AgentServiceID)
	observation.Now = observation.Now.UTC()
	observation.HeartbeatAt = observation.HeartbeatAt.UTC()
	if !executionHostIDPattern.MatchString(observation.ExecutionHostID) ||
		!serviceIDPattern.MatchString(observation.AgentServiceID) ||
		observation.ExpectedRevision < 1 || observation.Now.IsZero() {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrInvalidSystemUpdateHostSelfUpdate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	update, ok := activeMemorySystemUpdateHostSelfUpdateForHostLocked(
		s, observation.ExecutionHostID,
	)
	if !ok {
		return SystemUpdateHostSelfUpdate{}, false, ErrNotFound
	}
	if update.AgentServiceID != observation.AgentServiceID {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateOwnershipConflict
	}
	if update.Revision != observation.ExpectedRevision {
		return SystemUpdateHostSelfUpdate{}, false,
			ErrSystemUpdateHostSelfUpdateStale
	}
	next, changed, err := reconcileSystemUpdateHostSelfUpdateObservation(
		update, observation,
	)
	if err != nil {
		return SystemUpdateHostSelfUpdate{}, false, err
	}
	s.hostSelfUpdates[next.ID] = next
	return publicSystemUpdateHostSelfUpdate(next), changed, nil
}

func reconcileSystemUpdateHostSelfUpdateObservation(
	update SystemUpdateHostSelfUpdate,
	observation SystemUpdateHostSelfUpdateObservation,
) (SystemUpdateHostSelfUpdate, bool, error) {
	beforeStatus := update.Status
	beforeObservation := update.ObservationState
	beforePhase := update.ReportedPhase
	if observation.HeartbeatAt.IsZero() {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationUnknown
		update.ReportedPhase = ""
		update.StalledSince = nil
	} else if observation.Now.Sub(observation.HeartbeatAt) >
		systemUpdateHostSelfUpdateHeartbeatStallAfter {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationStalled
		if update.StalledSince == nil {
			stalled := observation.HeartbeatAt.Add(
				systemUpdateHostSelfUpdateHeartbeatStallAfter,
			)
			update.StalledSince = cloneTimePtr(&stalled)
		}
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
	} else if observation.RecoveryPending {
		update.ObservationState = SystemUpdateHostSelfUpdateObservationUnknown
		update.ReportedPhase = ""
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
		update.StalledSince = nil
	} else {
		phase := strings.ToLower(strings.TrimSpace(observation.Phase))
		if phase != "" && phase != "stable" &&
			observation.PendingGeneration != update.AttemptGeneration {
			return SystemUpdateHostSelfUpdate{}, false,
				ErrSystemUpdateHostSelfUpdateStale
		}
		if phase == "stable" &&
			observation.HeartbeatGeneration != "" &&
			observation.HeartbeatGeneration != update.AttemptGeneration &&
			observation.FailedGeneration != update.AttemptGeneration {
			return SystemUpdateHostSelfUpdate{}, false,
				ErrSystemUpdateHostSelfUpdateStale
		}
		update.ObservationState = SystemUpdateHostSelfUpdateObservationKnown
		update.ReportedPhase = phase
		update.LastHeartbeatAt = cloneTimePtr(&observation.HeartbeatAt)
		update.StalledSince = nil
		if err := advanceSystemUpdateHostSelfUpdateFromObservation(
			&update, observation,
		); err != nil {
			return SystemUpdateHostSelfUpdate{}, false, err
		}
	}
	changed := update.Status != beforeStatus ||
		update.ObservationState != beforeObservation ||
		update.ReportedPhase != beforePhase
	if changed {
		update.Revision++
		update.UpdatedAt = observation.Now
	}
	return update, changed, nil
}

func advanceSystemUpdateHostSelfUpdateFromObservation(
	update *SystemUpdateHostSelfUpdate,
	observation SystemUpdateHostSelfUpdateObservation,
) error {
	phase := strings.ToLower(strings.TrimSpace(observation.Phase))
	if phase != "stable" &&
		observation.PendingGeneration != update.AttemptGeneration {
		return nil
	}
	switch phase {
	case "staged":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateStaging, observation.Now,
		)
	case "activating":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateActivating, observation.Now,
		)
	case "verifying":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateVerifying, observation.Now,
		)
	case "rolling_back":
		setSystemUpdateHostSelfUpdateStatus(
			update, SystemUpdateHostSelfUpdateRollingBack, observation.Now,
		)
	case "stable":
		if strictSystemUpdateHostSelfUpdateSuccess(update, observation) {
			setSystemUpdateHostSelfUpdateTerminal(
				update, SystemUpdateHostSelfUpdateSucceeded,
				"succeeded", observation.Now,
			)
		} else if strictSystemUpdateHostSelfUpdateRollback(update, observation) {
			setSystemUpdateHostSelfUpdateTerminal(
				update, SystemUpdateHostSelfUpdateRolledBack,
				"rolled_back", observation.Now,
			)
		}
	case "":
		return nil
	default:
		return nil
	}
	return nil
}

func setSystemUpdateHostSelfUpdateStatus(
	update *SystemUpdateHostSelfUpdate,
	status string,
	now time.Time,
) {
	rank := map[string]int{
		SystemUpdateHostSelfUpdateQueued:          0,
		SystemUpdateHostSelfUpdateStaging:         1,
		SystemUpdateHostSelfUpdateActivating:      2,
		SystemUpdateHostSelfUpdateVerifying:       3,
		SystemUpdateHostSelfUpdateRollingBack:     4,
		SystemUpdateHostSelfUpdateCancelRequested: 5,
	}
	if isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) ||
		rank[status] < rank[update.Status] {
		return
	}
	update.Status = status
	if update.StartedAt == nil && status != SystemUpdateHostSelfUpdateQueued {
		update.StartedAt = cloneTimePtr(&now)
	}
}

func setSystemUpdateHostSelfUpdateTerminal(
	update *SystemUpdateHostSelfUpdate,
	status, code string,
	now time.Time,
) {
	if isTerminalSystemUpdateHostSelfUpdateStatus(update.Status) {
		return
	}
	update.Status = status
	update.Code = code
	update.CompletedAt = cloneTimePtr(&now)
}

func strictSystemUpdateHostSelfUpdateSuccess(
	update *SystemUpdateHostSelfUpdate,
	o SystemUpdateHostSelfUpdateObservation,
) bool {
	return update.Status != SystemUpdateHostSelfUpdateQueued &&
		o.HeartbeatGeneration == update.AttemptGeneration &&
		o.AgentVersion == update.TargetVersion &&
		o.ActiveAgentVersion == update.TargetVersion &&
		o.ExecutorVersion == update.TargetVersion &&
		o.ActiveExecutorVersion == update.TargetVersion &&
		o.AgentProtocolVersion == update.Release.AgentProtocolVersion &&
		o.ExecutorProtocolVersion == update.Release.ExecutorProtocolVersion &&
		o.MutationProtocolVersion == update.Release.MutationProtocolVersion &&
		o.RecoveryProtocolVersion == update.Release.RecoveryProtocolVersion
}

func strictSystemUpdateHostSelfUpdateRollback(
	update *SystemUpdateHostSelfUpdate,
	o SystemUpdateHostSelfUpdateObservation,
) bool {
	return o.FailedGeneration == update.AttemptGeneration &&
		o.AgentVersion == update.PreviousAgentVersion &&
		o.ActiveAgentVersion == update.PreviousAgentVersion &&
		o.ExecutorVersion == update.PreviousExecutorVersion &&
		o.ActiveExecutorVersion == update.PreviousExecutorVersion &&
		o.AgentProtocolVersion == update.PreviousAgentProtocolVersion &&
		o.ExecutorProtocolVersion == update.PreviousExecutorProtocolVersion &&
		o.MutationProtocolVersion == update.PreviousMutationProtocolVersion &&
		o.RecoveryProtocolVersion == update.PreviousRecoveryProtocolVersion
}
