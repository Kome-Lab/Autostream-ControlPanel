package store

import (
	"context"
	"strings"
	"sync"
	"time"
)

type MemoryStreamStore struct {
	mu                                         sync.Mutex
	serviceAssignmentGuard                     *MemoryAuthStore
	youtubeRelayBindingOutputMu                sync.Mutex
	streams                                    map[string]Stream
	logs                                       map[string][]StreamLog
	artifacts                                  map[string][]StreamArtifact
	artifactReports                            map[string]bool
	archiveRetryPending                        map[string]bool
	artifactShares                             map[string]StreamArtifactShare
	mediaRuntimes                              map[string]StreamMediaRuntime
	youtubeRuntimes                            map[string]StreamYouTubeRuntime
	youtubeRelayBindingClaims                  map[string]YouTubeRelayBindingClaim
	discordYouTubeLiveNotifications            map[string]DiscordYouTubeLiveNotification
	discordYouTubeLiveNotificationEvents       map[string]string
	discordYouTubeLiveNotificationLeases       map[string]string
	discordYouTubeLiveNotificationLeaseExpires map[string]time.Time
	relayBindingClaimProfiles                  *MemoryProfileStore
}

func NewMemoryStreamStore() *MemoryStreamStore {
	return &MemoryStreamStore{
		streams:                              map[string]Stream{},
		logs:                                 map[string][]StreamLog{},
		artifacts:                            map[string][]StreamArtifact{},
		artifactReports:                      map[string]bool{},
		archiveRetryPending:                  map[string]bool{},
		artifactShares:                       map[string]StreamArtifactShare{},
		mediaRuntimes:                        map[string]StreamMediaRuntime{},
		youtubeRuntimes:                      map[string]StreamYouTubeRuntime{},
		youtubeRelayBindingClaims:            map[string]YouTubeRelayBindingClaim{},
		discordYouTubeLiveNotifications:      map[string]DiscordYouTubeLiveNotification{},
		discordYouTubeLiveNotificationEvents: map[string]string{},
		discordYouTubeLiveNotificationLeases: map[string]string{},
		discordYouTubeLiveNotificationLeaseExpires: map[string]time.Time{},
	}
}

// AssignmentGuardMemoryStore exposes the shared in-memory lock authority to
// wrappers used by focused HTTP tests without exposing its maps or mutexes.
func (s *MemoryStreamStore) AssignmentGuardMemoryStore() *MemoryStreamStore {
	return s
}

func (s *MemoryStreamStore) ListStreams(ctx context.Context) ([]Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		if stream.DeletedAt != nil {
			continue
		}
		items = append(items, stream)
	}
	return items, nil
}

func (s *MemoryStreamStore) ListArchiveStreams(ctx context.Context) ([]Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		hasRecording := false
		for _, artifact := range s.artifacts[stream.ID] {
			if isArchiveRecordingArtifact(artifact) {
				hasRecording = true
				break
			}
		}
		if !hasRecording {
			continue
		}
		items = append(items, stream)
	}
	return items, nil
}

func (s *MemoryStreamStore) ListArchiveProcessingStreams(ctx context.Context) ([]Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		status := strings.ToLower(strings.TrimSpace(stream.Status))
		if stream.DeletedAt != nil {
			continue
		}
		if s.archiveRetryPending[stream.ID] {
			items = append(items, stream)
			continue
		}
		if strings.TrimSpace(stream.ArchiveProfileID) == "" {
			continue
		}
		if stream.ArchiveStartedAt != nil {
			if stream.ArchiveReportedAt == nil && (status == "stopping" || status == "completed" || status == "ready") {
				items = append(items, stream)
			}
			continue
		}
	}
	return items, nil
}

func (s *MemoryStreamStore) HasActiveStream(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range s.streams {
		switch strings.ToLower(strings.TrimSpace(stream.Status)) {
		case "starting", "live", "stopping":
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStreamStore) CreateStream(ctx context.Context, name string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	now := time.Now().UTC()
	stream := Stream{ID: newUUID(), Name: name, Status: "created", CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	s.streams[stream.ID] = stream
	s.mu.Unlock()
	return stream, nil
}

func (s *MemoryStreamStore) GetStream(ctx context.Context, id string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, ErrNotFound
	}
	return stream, nil
}

func (s *MemoryStreamStore) DeleteStream(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if services := s.serviceAssignmentGuard; services != nil {
		services.mu.Lock()
		defer services.mu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.deleteStreamLocked(id, services)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteStreamLocked(id, nil)
}

func (s *MemoryStreamStore) deleteStreamLocked(id string, services *MemoryAuthStore) error {
	if _, ok := s.streams[id]; !ok {
		return ErrNotFound
	}
	protected := memoryStreamAssignmentProtectionLocked(s, s.streams[id]).protected()
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == id {
			return ErrYouTubeRelayBindingClaimActive
		}
	}
	if protected {
		return ErrServiceUnassignProtectedStream
	}
	now := time.Now().UTC()
	if services != nil {
		serviceIDs := make(map[string]struct{})
		archiveEncoderID := ""
		archiveEncoderIsPrimary := false
		for key, serviceID := range services.assignments {
			streamID, serviceType, assignmentRole := assignmentPartsFromKey(key)
			if streamID == id {
				serviceIDs[serviceID] = struct{}{}
				isPrimary := assignmentRole == "primary"
				if serviceType == "encoder_recorder" && (archiveEncoderID == "" || (!archiveEncoderIsPrimary && isPrimary)) {
					archiveEncoderID = serviceID
					archiveEncoderIsPrimary = isPrimary
				}
			}
		}
		currentServiceIDs := make(map[string]struct{})
		for serviceID, service := range services.services {
			if strings.TrimSpace(service.CurrentStreamID) == id {
				currentServiceIDs[serviceID] = struct{}{}
			}
		}
		if len(currentServiceIDs) != len(serviceIDs) {
			return ErrServiceAssignmentConflict
		}
		for serviceID := range serviceIDs {
			if _, ok := currentServiceIDs[serviceID]; !ok {
				return ErrServiceAssignmentConflict
			}
		}
		for serviceID := range serviceIDs {
			service, ok := services.services[serviceID]
			if !ok {
				return ErrServiceAssignmentConflict
			}
			owner, _, err := services.consistentServiceAssignmentLocked(service)
			if err != nil || owner != id {
				return ErrServiceAssignmentConflict
			}
		}
		if archiveEncoderID != "" {
			artifacts := s.artifacts[id]
			for index := range artifacts {
				if strings.TrimSpace(artifacts[index].SourceServiceID) == "" {
					artifacts[index].SourceServiceID = archiveEncoderID
				}
			}
			s.artifacts[id] = artifacts
		}
		for key, serviceID := range services.assignments {
			streamID, _, _ := assignmentPartsFromKey(key)
			if streamID != id {
				continue
			}
			delete(services.assignments, key)
			delete(services.assignmentIDs, key)
			service := services.services[serviceID]
			service.CurrentStreamID = ""
			if service.Status == "assigned" {
				service.Status = "registered"
			}
			service.UpdatedAt = now
			services.services[serviceID] = service
		}
	}
	stream := s.streams[id]
	stream.Status = "completed"
	if stream.DeletedAt == nil {
		deletedAt := now
		stream.DeletedAt = &deletedAt
	}
	stream.UpdatedAt = now
	s.streams[id] = stream
	delete(s.mediaRuntimes, id)
	delete(s.youtubeRuntimes, id)
	for notificationID, notification := range s.discordYouTubeLiveNotifications {
		if notification.StreamID != id {
			continue
		}
		delete(s.discordYouTubeLiveNotificationEvents, notification.EventID)
		delete(s.discordYouTubeLiveNotificationLeases, notificationID)
		delete(s.discordYouTubeLiveNotificationLeaseExpires, notificationID)
		delete(s.discordYouTubeLiveNotifications, notificationID)
	}
	return nil
}
