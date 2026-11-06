package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStreamStore) GetStreamNodeSelections(ctx context.Context, streamID string) (StreamNodeSelections, error) {
	if err := ctx.Err(); err != nil {
		return StreamNodeSelections{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, exists := s.streams[strings.TrimSpace(streamID)]
	if !exists || stream.DeletedAt != nil {
		return StreamNodeSelections{}, ErrNotFound
	}
	return streamNodeSelections(stream), nil
}

func (s *MemoryStreamStore) ListStreamNodeSelectionsForService(ctx context.Context, serviceID string) ([]StreamNodeSelectionBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	serviceID = strings.TrimSpace(serviceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []StreamNodeSelectionBinding
	for _, stream := range s.streams {
		if stream.DeletedAt != nil {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, stream.SettingsRevision)
		if err != nil {
			updatedAt = stream.CreatedAt
		}
		for _, binding := range streamNodeSelections(stream).bindings(stream.ID, updatedAt) {
			if binding.ServiceID == serviceID {
				result = append(result, binding)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StreamID == result[j].StreamID {
			return result[i].ServiceType < result[j].ServiceType
		}
		return result[i].StreamID < result[j].StreamID
	})
	return result, nil
}

// Caller holds profiles (when available), registry, and stream locks.
func (s *MemoryStreamStore) validateStreamNodeSelectionsLocked(stream Stream, settings StreamSettings) error {
	if !streamNodeSelectionsChanged(settings) {
		return nil
	}
	selected := mergeStreamNodeSelections(streamNodeSelections(stream), settings.NodeSelections)
	if err := validateExpectedDiscordNodeSelection(settings, selected); err != nil {
		return err
	}
	auth := s.serviceAssignmentGuard
	if auth == nil {
		return ErrServiceAssignmentGuardUnavailable
	}
	for _, binding := range selected.bindings(stream.ID, time.Time{}) {
		service, exists := auth.services[binding.ServiceID]
		if !exists {
			return ErrNotFound
		}
		if service.ServiceType != binding.ServiceType {
			return ErrInvalidServiceAssignment
		}
	}
	if selected.DiscordServiceID != "" {
		profiles := s.relayBindingClaimProfiles
		if profiles == nil {
			return ErrServiceAssignmentGuardUnavailable
		}
		profile := profiles.profiles[strings.TrimSpace(settings.DiscordConfigID)]
		if profile.Kind != ProfileDiscordConfig || currentDiscordConfigString(profile.Config, "service_id") != selected.DiscordServiceID {
			return ErrServiceAssignmentConflict
		}
	}
	return nil
}
