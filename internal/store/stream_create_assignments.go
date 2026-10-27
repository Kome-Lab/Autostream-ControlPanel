package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// StreamCreateAssignments reserves the existing single-owner service roles.
// Normal creation never transfers an existing assignment; edits retain the
// separate guarded transfer contract.
type StreamCreateAssignments struct {
	EncoderServiceID string
	WorkerServiceID  string
	DiscordServiceID string
	DiscordConfigID  string
}

func (a StreamCreateAssignments) serviceIDs() []string {
	return sortedUniqueStrings([]string{a.EncoderServiceID, a.WorkerServiceID, a.DiscordServiceID})
}

func (a StreamCreateAssignments) valid() bool {
	count := 2
	if strings.TrimSpace(a.DiscordConfigID) != "" {
		if strings.TrimSpace(a.DiscordServiceID) == "" {
			return false
		}
		count++
	} else if strings.TrimSpace(a.DiscordServiceID) != "" {
		return false
	}
	return strings.TrimSpace(a.EncoderServiceID) != "" && strings.TrimSpace(a.WorkerServiceID) != "" && len(a.serviceIDs()) == count
}

func (a StreamCreateAssignments) serviceType(id string) string {
	switch id {
	case strings.TrimSpace(a.EncoderServiceID):
		return "encoder_recorder"
	case strings.TrimSpace(a.DiscordServiceID):
		return "discord_bot"
	default:
		return "worker"
	}
}

func AssignNewStreamServicesTx(ctx context.Context, tx *sql.Tx, streamID, actorID string, a StreamCreateAssignments, now time.Time) error {
	if !a.valid() {
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
		if service.ServiceType != a.serviceType(id) {
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
	if a.DiscordConfigID != "" {
		profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, a.DiscordConfigID, string(ProfileDiscordConfig)))
		if err != nil || currentDiscordConfigString(profile.Config, "service_id") != a.DiscordServiceID {
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
	// Current credential snapshots already use profiles -> registry -> streams.
	// Recheck the selected authentication profile under that same authority;
	// the HTTP observation alone must not survive a concurrent owner change.
	if a.DiscordConfigID != "" {
		profiles := s.relayBindingClaimProfiles
		if profiles == nil {
			return Stream{}, ErrServiceAssignmentGuardUnavailable
		}
		profiles.mu.Lock()
		defer profiles.mu.Unlock()
		profile := profiles.profiles[a.DiscordConfigID]
		if settings.DiscordConfigID != a.DiscordConfigID || profile.Kind != ProfileDiscordConfig || currentDiscordConfigString(profile.Config, "service_id") != a.DiscordServiceID {
			return Stream{}, ErrServiceAssignmentConflict
		}
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !a.valid() {
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
		if service.ServiceType != a.serviceType(id) {
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
