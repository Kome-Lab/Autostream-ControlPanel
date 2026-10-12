package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// StreamCreateAssignments reserves both existing single-owner service roles.
// Normal creation never transfers an existing assignment; edits retain the
// separate guarded transfer contract.
type StreamCreateAssignments struct {
	EncoderServiceID string
	WorkerServiceID  string
}

func (a StreamCreateAssignments) serviceIDs() []string {
	return sortedUniqueStrings([]string{a.EncoderServiceID, a.WorkerServiceID})
}

func AssignNewStreamServicesTx(ctx context.Context, tx *sql.Tx, streamID, actorID string, a StreamCreateAssignments, now time.Time) error {
	if strings.TrimSpace(a.EncoderServiceID) == "" || strings.TrimSpace(a.WorkerServiceID) == "" || len(a.serviceIDs()) != 2 {
		return ErrInvalidServiceAssignment
	}
	// The caller has inserted/locked its new stream. Acquire sorted services
	// before any asset/preset references. The new ID is invisible to competing
	// assignment callers until commit, and occupied services are never moved.
	services, err := lockMariaDBServicesSorted(ctx, tx, a.serviceIDs())
	if err != nil {
		return err
	}
	for _, id := range a.serviceIDs() {
		service := services[id]
		expectedType := "worker"
		if id == strings.TrimSpace(a.EncoderServiceID) {
			expectedType = "encoder_recorder"
		}
		if service.ServiceType != expectedType {
			return ErrInvalidServiceAssignment
		}
		owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
		if err != nil {
			return err
		}
		if owner != "" {
			return ErrServiceAssignmentConflict
		}
	}
	for _, id := range a.serviceIDs() {
		service := services[id]
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_service_assignments(id,stream_id,service_id,service_type,assignment_role,assigned_by_user_id,assigned_at) VALUES(?,?,?,?,'primary',NULLIF(?,''),?)`, newUUID(), streamID, id, service.ServiceType, actorID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE services SET current_stream_id=?,status='assigned',updated_at=? WHERE service_id=?`, streamID, now, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStreamStore) CreateAssignedStream(ctx context.Context, streamID, name, actorID string, settings StreamSettings, a StreamCreateAssignments) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	auth := s.serviceAssignmentGuard
	if auth == nil {
		return Stream{}, ErrServiceAssignmentGuardUnavailable
	}
	// Match the existing memory assignment lock order: registry, then streams.
	auth.mu.Lock()
	defer auth.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(a.EncoderServiceID) == "" || strings.TrimSpace(a.WorkerServiceID) == "" || len(a.serviceIDs()) != 2 {
		return Stream{}, ErrInvalidServiceAssignment
	}
	if _, exists := s.streams[streamID]; exists {
		return Stream{}, ErrServiceAssignmentConflict
	}
	for _, id := range a.serviceIDs() {
		service, exists := auth.services[id]
		if !exists {
			return Stream{}, ErrNotFound
		}
		expectedType := "worker"
		if id == strings.TrimSpace(a.EncoderServiceID) {
			expectedType = "encoder_recorder"
		}
		if service.ServiceType != expectedType {
			return Stream{}, ErrInvalidServiceAssignment
		}
		owner, _, err := auth.consistentServiceAssignmentLocked(service)
		if err != nil {
			return Stream{}, err
		}
		if owner != "" {
			return Stream{}, ErrServiceAssignmentConflict
		}
	}
	now := time.Now().UTC()
	stream := Stream{ID: streamID, Name: name, Status: "created", CreatedAt: now, UpdatedAt: now}
	stream = applyMemoryStreamSettings(stream, settings)
	stream.AssignedEncoderID, stream.AssignedWorkerID = strings.TrimSpace(a.EncoderServiceID), strings.TrimSpace(a.WorkerServiceID)
	for _, id := range a.serviceIDs() {
		service := auth.services[id]
		key := assignmentKey(streamID, service.ServiceType, "primary", id)
		auth.assignments[key], auth.assignmentIDs[key] = id, newUUID()
		service.CurrentStreamID, service.Status, service.AssignmentRole, service.UpdatedAt = streamID, "assigned", "primary", now
		auth.services[id] = service
	}
	s.streams[streamID] = stream
	return stream, nil
}
