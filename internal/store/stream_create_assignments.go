package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// StreamCreateAssignments names the saved service choices for a new stream.
// The historical name is retained for the atomic visual-creation API; saving
// choices never acquires, transfers, or releases execution ownership.
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

func (a StreamCreateAssignments) selections() StreamNodeSelections {
	return StreamNodeSelections{
		EncoderServiceID: strings.TrimSpace(a.EncoderServiceID),
		WorkerServiceID:  strings.TrimSpace(a.WorkerServiceID),
		DiscordServiceID: strings.TrimSpace(a.DiscordServiceID),
	}
}

func AssignNewStreamServicesTx(ctx context.Context, tx *sql.Tx, streamID, actorID string, a StreamCreateAssignments, now time.Time) error {
	if !a.valid() {
		return ErrInvalidServiceAssignment
	}
	// The caller inserted/locked its new stream. Services and the selected
	// profile are read under the existing stream -> registry -> profile order.
	services, err := lockMariaDBServicesSorted(ctx, tx, a.serviceIDs())
	if err != nil {
		return err
	}
	for _, id := range a.serviceIDs() {
		if service := services[id]; service.ServiceType != a.serviceType(id) {
			return ErrInvalidServiceAssignment
		}
	}
	if strings.TrimSpace(a.DiscordConfigID) != "" {
		profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, strings.TrimSpace(a.DiscordConfigID), string(ProfileDiscordConfig)))
		if err != nil || currentDiscordConfigString(profile.Config, "service_id") != strings.TrimSpace(a.DiscordServiceID) {
			return ErrServiceAssignmentConflict
		}
	}
	return saveStreamNodeSelectionsTx(ctx, tx, streamID, a.selections(), now)
}

func (s *MemoryStreamStore) CreateAssignedStream(ctx context.Context, streamID, name, actorID string, settings StreamSettings, a StreamCreateAssignments) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	if !a.valid() {
		return Stream{}, ErrInvalidServiceAssignment
	}
	if strings.TrimSpace(settings.DiscordConfigID) != strings.TrimSpace(a.DiscordConfigID) {
		return Stream{}, ErrServiceAssignmentConflict
	}
	if err := validateExpectedDiscordNodeSelection(settings, a.selections()); err != nil {
		return Stream{}, err
	}
	auth := s.serviceAssignmentGuard
	if auth == nil {
		return Stream{}, ErrServiceAssignmentGuardUnavailable
	}
	// Current credential snapshots already use profiles -> registry -> streams.
	if strings.TrimSpace(a.DiscordConfigID) != "" {
		profiles := s.relayBindingClaimProfiles
		if profiles == nil {
			return Stream{}, ErrServiceAssignmentGuardUnavailable
		}
		profiles.mu.Lock()
		defer profiles.mu.Unlock()
		profile := profiles.profiles[strings.TrimSpace(a.DiscordConfigID)]
		if profile.Kind != ProfileDiscordConfig || currentDiscordConfigString(profile.Config, "service_id") != strings.TrimSpace(a.DiscordServiceID) {
			return Stream{}, ErrServiceAssignmentConflict
		}
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
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
	}
	now := time.Now().UTC()
	stream := Stream{ID: streamID, Name: name, Status: "created", CreatedAt: now, UpdatedAt: now}
	// Creation takes canonical choices from its atomic Assignments input.
	settings.NodeSelections = nil
	stream = applyMemoryStreamSettings(stream, settings)
	applyStreamNodeSelections(&stream, a.selections())
	s.streams[streamID] = stream
	return stream, nil
}
