package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

type RemediationExecutionStore interface {
	ClaimRemediationExecution(ctx context.Context, actionID, incidentID, streamID, action string) error
}

func (s MariaDBAuthStore) ClaimRemediationExecution(ctx context.Context, actionID, incidentID, streamID, action string) error {
	actionID = strings.TrimSpace(actionID)
	incidentID = strings.TrimSpace(incidentID)
	streamID = strings.TrimSpace(streamID)
	action = strings.TrimSpace(action)
	if actionID == "" || incidentID == "" || streamID == "" || action == "" {
		return ErrInvalidRemediationExecution
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO service_remediation_executions (action_id, incident_id, stream_id, action, executed_at) VALUES (?, ?, ?, ?, ?)`, actionID, incidentID, streamID, action, time.Now().UTC())
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

type MemoryRemediationExecutionStore struct {
	mu     sync.Mutex
	claims map[string]remediationExecutionClaim
}

type remediationExecutionClaim struct {
	IncidentID string
	StreamID   string
	Action     string
	ExecutedAt time.Time
}

func NewMemoryRemediationExecutionStore() *MemoryRemediationExecutionStore {
	return &MemoryRemediationExecutionStore{claims: map[string]remediationExecutionClaim{}}
}

func (s *MemoryRemediationExecutionStore) ClaimRemediationExecution(ctx context.Context, actionID, incidentID, streamID, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	actionID = strings.TrimSpace(actionID)
	incidentID = strings.TrimSpace(incidentID)
	streamID = strings.TrimSpace(streamID)
	action = strings.TrimSpace(action)
	if actionID == "" || incidentID == "" || streamID == "" || action == "" {
		return ErrInvalidRemediationExecution
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claims == nil {
		s.claims = map[string]remediationExecutionClaim{}
	}
	if _, ok := s.claims[actionID]; ok {
		return ErrAlreadyExists
	}
	s.claims[actionID] = remediationExecutionClaim{IncidentID: incidentID, StreamID: streamID, Action: action, ExecutedAt: time.Now().UTC()}
	return nil
}

var ErrInvalidRemediationExecution = errors.New("invalid remediation execution")

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate") || strings.Contains(text, "unique constraint") || strings.Contains(text, "constraint failed")
}
