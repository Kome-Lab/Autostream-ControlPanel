package store

import (
	"context"
	"sort"
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
	legacyArchivePending                       map[string]bool
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
		legacyArchivePending:                 map[string]bool{},
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
		if s.legacyArchivePending[stream.ID] {
			if stream.ArchiveReportedAt == nil && (status == "stopping" || status == "completed" || status == "ready") {
				items = append(items, stream)
			}
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

func (s *MemoryStreamStore) SetStreamVideoOverlayBurnIn(ctx context.Context, streamID string, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	streamID = strings.TrimSpace(streamID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return ErrNotFound
	}
	s.mediaRuntimes[streamID] = StreamMediaRuntime{
		StreamID: streamID, VideoOverlayBurnIn: enabled, UpdatedAt: time.Now().UTC(),
	}
	return nil
}

func (s *MemoryStreamStore) GetStreamMediaRuntime(ctx context.Context, streamID string) (StreamMediaRuntime, error) {
	if err := ctx.Err(); err != nil {
		return StreamMediaRuntime{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.mediaRuntimes[strings.TrimSpace(streamID)]
	if !ok {
		return StreamMediaRuntime{}, ErrNotFound
	}
	return runtime, nil
}

func (s *MemoryStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, ErrNotFound
	}
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == id && claim.YouTubeOutputID != strings.TrimSpace(settings.YouTubeOutputID) {
			return Stream{}, ErrYouTubeRelayBindingClaimActive
		}
	}
	if name := strings.TrimSpace(settings.Name); name != "" {
		stream.Name = name
	}
	stream.ScheduledStartAt = cloneTimePtr(settings.ScheduledStartAt)
	stream.ScheduledEndAt = cloneTimePtr(settings.ScheduledEndAt)
	stream.DiscordConfigID = strings.TrimSpace(settings.DiscordConfigID)
	stream.DiscordGuildID = strings.TrimSpace(settings.DiscordGuildID)
	stream.DiscordVoiceID = strings.TrimSpace(settings.DiscordVoiceID)
	stream.DiscordTextID = strings.TrimSpace(settings.DiscordTextID)
	stream.AutoStartTrigger = strings.TrimSpace(settings.AutoStartTrigger)
	stream.EncoderProfileID = strings.TrimSpace(settings.EncoderProfileID)
	stream.CaptionProfileID = strings.TrimSpace(settings.CaptionProfileID)
	stream.OverlayProfileID = strings.TrimSpace(settings.OverlayProfileID)
	stream.EncoderAudioGainDB = settings.EncoderAudioGainDB
	stream.ArchiveProfileID = strings.TrimSpace(settings.ArchiveProfileID)
	stream.ArchiveDriveDestinationID = strings.TrimSpace(settings.ArchiveDriveDestinationID)
	stream.ArchiveOAuthAccountID = strings.TrimSpace(settings.ArchiveOAuthAccountID)
	stream.ArchiveSharedDrive = settings.ArchiveSharedDrive
	stream.ArchiveSharedDriveID = strings.TrimSpace(settings.ArchiveSharedDriveID)
	stream.ArchiveFileName = strings.TrimSpace(settings.ArchiveFileName)
	stream.ArchiveFolderIDConfigured = stream.ArchiveDriveDestinationID != ""
	stream.YouTubeOutputID = strings.TrimSpace(settings.YouTubeOutputID)
	stream.EncoderInputURL = strings.TrimSpace(settings.EncoderInputURL)
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	return stream, nil
}

func (s *MemoryStreamStore) UpdateStreamEncoderRuntimeSettings(ctx context.Context, id string, audioGainDB float64, overlayProfileID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[strings.TrimSpace(id)]
	if !ok {
		return Stream{}, ErrNotFound
	}
	stream.EncoderAudioGainDB = audioGainDB
	stream.OverlayProfileID = strings.TrimSpace(overlayProfileID)
	stream.UpdatedAt = time.Now().UTC()
	s.streams[stream.ID] = stream
	return stream, nil
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func (s *MemoryStreamStore) UpdateStreamStatus(ctx context.Context, id, status string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, ErrNotFound
	}
	stream.Status = status
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	return stream, nil
}

func (s *MemoryStreamStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (Stream, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, false, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), strings.TrimSpace(expectedStatus)) {
		return stream, false, nil
	}
	stream.Status = status
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	return stream, true, nil
}

func (s *MemoryStreamStore) PrepareStreamArchiveRun(ctx context.Context, id, archiveRunID string, startedAt time.Time) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	archiveRunID = strings.TrimSpace(archiveRunID)
	if archiveRunID != "" && !validArchiveRunID(archiveRunID) {
		return Stream{}, ErrInvalidStreamArtifact
	}
	if archiveRunID == "" {
		startedAt = time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, ErrNotFound
	}
	stream.ArchiveRunID = archiveRunID
	stream.ArchiveStartedAt = nil
	if !startedAt.IsZero() {
		value := startedAt.UTC()
		stream.ArchiveStartedAt = &value
	}
	stream.ArchiveReportedAt = nil
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	s.artifactReports[id] = false
	if archiveRunID == "" && strings.TrimSpace(stream.ArchiveProfileID) != "" {
		s.legacyArchivePending[id] = true
	} else {
		delete(s.legacyArchivePending, id)
	}
	return stream, nil
}

func (s *MemoryStreamStore) ClaimStreamStart(ctx context.Context, request StreamStartClaimRequest) (ClaimedStreamStart, error) {
	if err := ctx.Err(); err != nil {
		return ClaimedStreamStart{}, err
	}
	request.StreamID = strings.TrimSpace(request.StreamID)
	request.MaterializeServiceID = strings.TrimSpace(request.MaterializeServiceID)
	services := s.serviceAssignmentGuard
	if services == nil {
		return ClaimedStreamStart{}, ErrServiceAssignmentGuardUnavailable
	}
	services.mu.Lock()
	defer services.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	stream, ok := s.streams[request.StreamID]
	if !ok || stream.DeletedAt != nil {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	if !streamStartClaimStatus(stream.Status) ||
		!strings.EqualFold(strings.TrimSpace(stream.Status), strings.TrimSpace(request.ExpectedStatus)) ||
		!stream.UpdatedAt.Equal(request.ExpectedStreamUpdatedAt) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	if memoryStreamAssignmentProtectionLocked(s, stream).protected() {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}

	expected, err := expectedPrimaryStartAssignments(request.ExpectedPrimaryAssignments)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	actual := make(map[string]RegisteredService, len(expected))
	for key, serviceID := range services.assignments {
		streamID, serviceType, role := assignmentPartsFromKey(key)
		if streamID != stream.ID || normalizeAssignmentRole(role) != "primary" {
			continue
		}
		service, exists := services.services[serviceID]
		if !exists || strings.TrimSpace(services.assignmentIDs[key]) == "" {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, currentRole, consistencyErr := services.consistentServiceAssignmentLocked(service)
		if consistencyErr != nil || owner != stream.ID || currentRole != "primary" || service.ServiceType != serviceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = "primary"
		if _, duplicate := actual[serviceType]; duplicate {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		actual[serviceType] = service
	}

	var materialized *RegisteredService
	var materializePreviousKey string
	if request.MaterializeServiceID != "" {
		candidate, exists := services.services[request.MaterializeServiceID]
		if !exists {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		expectedCandidate, exists := expected[candidate.ServiceType]
		if !exists || expectedCandidate.ServiceID != candidate.ServiceID || normalizeAssignmentRole(expectedCandidate.AssignmentRole) != "primary" {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if current, exists := actual[candidate.ServiceType]; exists {
			// The preflight observed a missing primary and requested atomic
			// materialization. A concurrently created primary is a changed
			// assignment even when it names the same service.
			_ = current
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := services.consistentServiceAssignmentLocked(candidate)
		if consistencyErr != nil || (owner != "" && owner != stream.ID) || (owner == stream.ID && role == "primary") {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if owner == stream.ID {
			materializePreviousKey = assignmentKey(stream.ID, candidate.ServiceType, role, candidate.ServiceID)
		}
		candidate.AssignmentRole = "primary"
		actual[candidate.ServiceType] = candidate
		materialized = &candidate
	}
	if len(actual) != len(expected) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	for serviceType, expectedService := range expected {
		current, exists := actual[serviceType]
		if !exists || current.ServiceID != expectedService.ServiceID || current.ServiceType != expectedService.ServiceType {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
	}
	for _, requiredType := range []string{"encoder_recorder", "worker", "discord_bot"} {
		if _, exists := actual[requiredType]; !exists {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
	}

	now := time.Now().UTC()
	if materialized != nil {
		if materializePreviousKey != "" {
			delete(services.assignments, materializePreviousKey)
			delete(services.assignmentIDs, materializePreviousKey)
		}
		key := assignmentKey(stream.ID, materialized.ServiceType, "primary", materialized.ServiceID)
		services.assignments[key] = materialized.ServiceID
		services.assignmentIDs[key] = newUUID()
		materialized.CurrentStreamID = stream.ID
		materialized.Status = "assigned"
		materialized.AssignmentRole = "primary"
		materialized.UpdatedAt = now
		services.services[materialized.ServiceID] = *materialized
		actual[materialized.ServiceType] = *materialized
	}

	authority := StreamArchiveAuthority{}
	stream.ArchiveRunID = ""
	stream.ArchiveStartedAt = nil
	stream.ArchiveReportedAt = nil
	delete(s.legacyArchivePending, stream.ID)
	if request.ArchiveEnabled {
		encoder := actual["encoder_recorder"]
		if capabilityTrue(encoder.ReportedCapabilities, "archive_runs") {
			startedAt := request.ArchiveStartedAt.UTC()
			if startedAt.IsZero() {
				startedAt = now
			}
			stream.ArchiveRunID = StreamArchiveRunIDForStart(startedAt)
			stream.ArchiveStartedAt = cloneTimePtr(&startedAt)
			authority.RunID = stream.ArchiveRunID
			authority.StartedAt = cloneTimePtr(stream.ArchiveStartedAt)
		} else {
			s.legacyArchivePending[stream.ID] = true
			authority.Legacy = true
		}
	}
	s.artifactReports[stream.ID] = false
	stream.Status = "starting"
	stream.UpdatedAt = now
	s.streams[stream.ID] = stream

	primaryAssignments, assignmentClaims, err := memoryClaimedPrimaryAssignments(services, stream.ID)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	ownership := StreamStartOwnershipClaim{
		StreamID: stream.ID, StreamUpdatedAt: stream.UpdatedAt,
		StreamIdentity: streamStartOwnershipIdentity(stream), Assignments: assignmentClaims, Archive: authority,
	}
	return ClaimedStreamStart{
		Stream: stream, PrimaryAssignments: primaryAssignments,
		ArchiveAuthority: authority, OwnershipClaim: ownership,
		Materialized: materialized,
	}, nil
}

func (s *MemoryStreamStore) TransitionClaimedStreamStart(ctx context.Context, claim StreamStartOwnershipClaim, status string) (Stream, bool, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, false, err
	}
	services := s.serviceAssignmentGuard
	if services == nil {
		return Stream{}, false, ErrServiceAssignmentGuardUnavailable
	}
	services.mu.Lock()
	defer services.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[strings.TrimSpace(claim.StreamID)]
	if !ok || stream.DeletedAt != nil {
		return Stream{}, false, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(stream.Status), "starting") {
		return stream, false, nil
	}
	if strings.TrimSpace(claim.StreamIdentity) == "" || streamStartOwnershipIdentity(stream) != claim.StreamIdentity || !stream.UpdatedAt.Equal(claim.StreamUpdatedAt) || !archiveAuthorityMatchesClaim(stream, claim.Archive) || s.legacyArchivePending[stream.ID] != claim.Archive.Legacy {
		return stream, false, ErrServiceAssignmentConflict
	}
	_, currentClaims, err := memoryClaimedPrimaryAssignments(services, stream.ID)
	if err != nil || !startAssignmentClaimsEqual(currentClaims, claim.Assignments) {
		return stream, false, ErrServiceAssignmentConflict
	}
	stream.Status = strings.TrimSpace(status)
	stream.UpdatedAt = time.Now().UTC()
	s.streams[stream.ID] = stream
	return stream, true, nil
}

func expectedPrimaryStartAssignments(assignments []RegisteredService) (map[string]RegisteredService, error) {
	expected := make(map[string]RegisteredService, len(assignments))
	for _, service := range assignments {
		service.ServiceID = strings.TrimSpace(service.ServiceID)
		service.ServiceType = strings.ToLower(strings.TrimSpace(service.ServiceType))
		if service.ServiceID == "" || service.ServiceType == "" || normalizeAssignmentRole(service.AssignmentRole) != "primary" {
			return nil, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = "primary"
		if _, duplicate := expected[service.ServiceType]; duplicate {
			return nil, ErrServiceAssignmentConflict
		}
		expected[service.ServiceType] = service
	}
	return expected, nil
}

func memoryClaimedPrimaryAssignments(services *MemoryAuthStore, streamID string) ([]RegisteredService, []StreamStartAssignmentClaim, error) {
	primary := make([]RegisteredService, 0, 3)
	claims := make([]StreamStartAssignmentClaim, 0, 3)
	for key, serviceID := range services.assignments {
		owner, serviceType, role := assignmentPartsFromKey(key)
		if owner != streamID || normalizeAssignmentRole(role) != "primary" {
			continue
		}
		service, ok := services.services[serviceID]
		assignmentID := strings.TrimSpace(services.assignmentIDs[key])
		if !ok || assignmentID == "" || service.ServiceType != serviceType || strings.TrimSpace(service.CurrentStreamID) != streamID {
			return nil, nil, ErrServiceAssignmentConflict
		}
		service.AssignmentRole = "primary"
		primary = append(primary, service)
		claims = append(claims, StreamStartAssignmentClaim{AssignmentID: assignmentID, ServiceID: serviceID, ServiceType: serviceType, Role: "primary"})
	}
	sort.Slice(primary, func(i, j int) bool {
		if primary[i].ServiceType == primary[j].ServiceType {
			return primary[i].ServiceID < primary[j].ServiceID
		}
		return primary[i].ServiceType < primary[j].ServiceType
	})
	sort.Slice(claims, func(i, j int) bool { return claims[i].AssignmentID < claims[j].AssignmentID })
	return primary, claims, nil
}

func streamStartClaimStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "draft", "scheduled", "ready", "failed":
		return true
	default:
		return false
	}
}

func capabilityTrue(capabilities map[string]any, name string) bool {
	value, ok := capabilities[name].(bool)
	return ok && value
}

func archiveAuthorityMatchesClaim(stream Stream, authority StreamArchiveAuthority) bool {
	if strings.TrimSpace(stream.ArchiveRunID) != strings.TrimSpace(authority.RunID) {
		return false
	}
	if (stream.ArchiveStartedAt == nil) != (authority.StartedAt == nil) {
		return false
	}
	return stream.ArchiveStartedAt == nil || stream.ArchiveStartedAt.UTC().Equal(authority.StartedAt.UTC())
}

func startAssignmentClaimsEqual(left, right []StreamStartAssignmentClaim) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]StreamStartAssignmentClaim(nil), left...)
	right = append([]StreamStartAssignmentClaim(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i].AssignmentID < left[j].AssignmentID })
	sort.Slice(right, func(i, j int) bool { return right[i].AssignmentID < right[j].AssignmentID })
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *MemoryStreamStore) SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(runtime.Mode) == youtubeRelayBindingClaimStaticRuntimeMode {
		return ErrInvalidYouTubeRelayBindingClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[runtime.StreamID]; !ok {
		return ErrNotFound
	}
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == runtime.StreamID {
			return ErrYouTubeRelayBindingClaimActive
		}
	}
	now := time.Now().UTC()
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}
	runtime.UpdatedAt = now
	s.youtubeRuntimes[runtime.StreamID] = runtime
	return nil
}

func (s *MemoryStreamStore) GetStreamYouTubeRuntime(ctx context.Context, streamID string) (StreamYouTubeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return StreamYouTubeRuntime{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	runtime, ok := s.youtubeRuntimes[streamID]
	if !ok {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	return runtime, nil
}

func (s *MemoryStreamStore) ListStreamYouTubeRuntimes(ctx context.Context) ([]StreamYouTubeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtimes := make([]StreamYouTubeRuntime, 0, len(s.youtubeRuntimes))
	for _, runtime := range s.youtubeRuntimes {
		runtimes = append(runtimes, runtime)
	}
	return runtimes, nil
}

func (s *MemoryStreamStore) ListDueStreamYouTubeRuntimes(ctx context.Context, now time.Time, limit int) ([]StreamYouTubeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtimes := make([]StreamYouTubeRuntime, 0)
	for _, runtime := range s.youtubeRuntimes {
		if (runtime.Mode != "live_api" && runtime.Mode != youtubeRelayBindingClaimStaticRuntimeMode) || runtime.CompleteNextRetryAt.IsZero() || runtime.CompleteNextRetryAt.After(now) {
			continue
		}
		runtimes = append(runtimes, runtime)
		if len(runtimes) >= limit {
			break
		}
	}
	return runtimes, nil
}

func (s *MemoryStreamStore) RecordStreamYouTubeRuntimeCompleteFailure(ctx context.Context, streamID, lastError string, nextRetryAt time.Time) (StreamYouTubeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return StreamYouTubeRuntime{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.youtubeRuntimes[streamID]
	if !ok {
		return StreamYouTubeRuntime{}, ErrNotFound
	}
	runtime.CompleteRetryCount++
	runtime.CompleteNextRetryAt = nextRetryAt.UTC()
	runtime.CompleteLastError = truncateString(strings.TrimSpace(lastError), 255)
	runtime.UpdatedAt = time.Now().UTC()
	s.youtubeRuntimes[streamID] = runtime
	return runtime, nil
}

func (s *MemoryStreamStore) DeleteStreamYouTubeRuntime(ctx context.Context, streamID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == strings.TrimSpace(streamID) {
			return ErrYouTubeRelayBindingClaimActive
		}
	}
	delete(s.youtubeRuntimes, streamID)
	return nil
}

func (s *MemoryStreamStore) RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error) {
	return s.AppendStreamLog(ctx, StreamLog{StreamID: id, Level: "info", Message: "archive upload retry requested", Fields: map[string]any{"actor_user_id": actorUserID}})
}

func (s *MemoryStreamStore) AppendStreamLog(ctx context.Context, log StreamLog) (StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return StreamLog{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	log = normalizeStreamLog(log)
	if _, ok := s.streams[log.StreamID]; !ok {
		return StreamLog{}, ErrNotFound
	}
	s.logs[log.StreamID] = append(s.logs[log.StreamID], log)
	return log, nil
}

func (s *MemoryStreamStore) ListStreamLogs(ctx context.Context, id string) ([]StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return nil, ErrNotFound
	}
	logs := append([]StreamLog(nil), s.logs[id]...)
	for index := range logs {
		logs[index] = enrichMemoryStreamLog(logs[index], s.streams[id])
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })
	return logs, nil
}

func (s *MemoryStreamStore) ListStreamLogHistory(ctx context.Context, limit int, before time.Time, beforeID string) ([]StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := make([]StreamLog, 0)
	for streamID, streamLogs := range s.logs {
		stream := s.streams[streamID]
		for _, log := range streamLogs {
			if !before.IsZero() && (log.CreatedAt.After(before) || (log.CreatedAt.Equal(before) && log.ID >= beforeID)) {
				continue
			}
			logs = append(logs, enrichMemoryStreamLog(log, stream))
		}
	}
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].CreatedAt.Equal(logs[j].CreatedAt) {
			return logs[i].ID > logs[j].ID
		}
		return logs[i].CreatedAt.After(logs[j].CreatedAt)
	})
	limit = boundedStreamLogLimit(limit)
	if len(logs) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

func enrichMemoryStreamLog(log StreamLog, stream Stream) StreamLog {
	log.StreamName = stream.Name
	if stream.DeletedAt != nil {
		deletedAt := *stream.DeletedAt
		log.StreamDeletedAt = &deletedAt
	}
	return log
}

func (s *MemoryStreamStore) ListStreamArtifacts(ctx context.Context, id string) ([]StreamArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return nil, ErrNotFound
	}
	artifacts := make([]StreamArtifact, 0, len(s.artifacts[id]))
	for _, artifact := range s.artifacts[id] {
		if isSafeRelativePath(artifact.RelativePath) {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, nil
}

func (s *MemoryStreamStore) AddArtifact(ctx context.Context, artifact StreamArtifact) error {
	return s.UpsertStreamArtifacts(ctx, artifact.StreamID, []StreamArtifact{artifact})
}

func (s *MemoryStreamStore) UpsertStreamArtifacts(ctx context.Context, id string, artifacts []StreamArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return ErrNotFound
	}
	if err := ValidateStreamArtifactReport(id, artifacts); err != nil {
		return err
	}
	current := append([]StreamArtifact(nil), s.artifacts[id]...)
	normalized := NormalizeStreamArtifacts(id, artifacts)
	for _, artifact := range normalized {
		updated := false
		for index, existing := range current {
			if existing.ArchiveRunID != artifact.ArchiveRunID || existing.Kind != artifact.Kind || existing.Name != artifact.Name {
				continue
			}
			existing.RelativePath = artifact.RelativePath
			existing.SizeBytes = artifact.SizeBytes
			if sourceServiceID := strings.TrimSpace(artifact.SourceServiceID); sourceServiceID != "" {
				existing.SourceServiceID = sourceServiceID
			}
			current[index] = existing
			updated = true
			break
		}
		if updated {
			continue
		}
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		current = append(current, artifact)
	}
	s.artifacts[id] = current
	s.artifactReports[id] = true
	stream := s.streams[id]
	legacyAuthority := s.legacyArchivePending[id] || s.archiveRetryPending[id] || (stream.ArchiveReportedAt != nil && legacyArchiveReportStatus(stream))
	reportMatchesAuthority := streamArtifactReportMatchesArchiveAuthority(stream, normalized, legacyAuthority)
	if reportMatchesAuthority {
		delete(s.archiveRetryPending, id)
		delete(s.legacyArchivePending, id)
	}
	if reportMatchesAuthority && stream.ArchiveReportedAt == nil {
		reportedAt := time.Now().UTC()
		stream.ArchiveReportedAt = &reportedAt
		stream.UpdatedAt = reportedAt
		s.streams[id] = stream
	}
	return nil
}

func (s *MemoryStreamStore) DeleteStreamArtifact(ctx context.Context, streamID, artifactID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return ErrNotFound
	}
	current := s.artifacts[streamID]
	filtered := current[:0]
	deleted := false
	for _, artifact := range current {
		if artifact.ID == artifactID {
			deleted = true
			continue
		}
		filtered = append(filtered, artifact)
	}
	if !deleted {
		return ErrNotFound
	}
	s.artifacts[streamID] = filtered
	return nil
}

func (s *MemoryStreamStore) RenameStreamArtifact(ctx context.Context, streamID, artifactID, name string) (StreamArtifact, error) {
	if err := ctx.Err(); err != nil {
		return StreamArtifact{}, err
	}
	if !isSafeArtifactFileName(name) {
		return StreamArtifact{}, ErrInvalidStreamArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return StreamArtifact{}, ErrNotFound
	}
	targetRunID := ""
	targetFound := false
	for _, artifact := range s.artifacts[streamID] {
		if artifact.ID == artifactID {
			targetRunID = artifact.ArchiveRunID
			targetFound = true
			break
		}
	}
	if !targetFound {
		return StreamArtifact{}, ErrNotFound
	}
	for _, artifact := range s.artifacts[streamID] {
		if artifact.ID != artifactID && artifact.ArchiveRunID == targetRunID && artifact.Name == name {
			return StreamArtifact{}, ErrAlreadyExists
		}
	}
	for index, artifact := range s.artifacts[streamID] {
		if artifact.ID != artifactID {
			continue
		}
		artifact.Name = name
		artifact.RelativePath = streamArtifactRelativePath(streamID, artifact.ArchiveRunID, name)
		if !isSafeRelativePath(artifact.RelativePath) {
			return StreamArtifact{}, ErrInvalidStreamArtifact
		}
		s.artifacts[streamID][index] = artifact
		return artifact, nil
	}
	return StreamArtifact{}, ErrNotFound
}

func (s *MemoryStreamStore) CreateStreamArtifactShare(ctx context.Context, share StreamArtifactShare) (StreamArtifactShare, error) {
	if err := ctx.Err(); err != nil {
		return StreamArtifactShare{}, err
	}
	share.StreamID = strings.TrimSpace(share.StreamID)
	share.ArtifactID = strings.TrimSpace(share.ArtifactID)
	share.TokenHash = strings.TrimSpace(share.TokenHash)
	share.CreatedByUserID = strings.TrimSpace(share.CreatedByUserID)
	if share.StreamID == "" || share.ArtifactID == "" || share.TokenHash == "" || !share.ExpiresAt.After(time.Now().UTC()) {
		return StreamArtifactShare{}, ErrInvalidStreamArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[share.StreamID]; !ok {
		return StreamArtifactShare{}, ErrNotFound
	}
	if _, ok := memoryArtifactByID(s.artifacts[share.StreamID], share.ArtifactID); !ok {
		return StreamArtifactShare{}, ErrNotFound
	}
	now := time.Now().UTC()
	share.ID = newUUID()
	share.ExpiresAt = share.ExpiresAt.UTC()
	share.CreatedAt = now
	s.artifactShares[share.ID] = share
	return share, nil
}

func (s *MemoryStreamStore) ListStreamArtifactShares(ctx context.Context, streamID, artifactID string) ([]StreamArtifactShare, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return nil, ErrNotFound
	}
	if _, ok := memoryArtifactByID(s.artifacts[streamID], artifactID); !ok {
		return nil, ErrNotFound
	}
	shares := make([]StreamArtifactShare, 0)
	for _, share := range s.artifactShares {
		if share.StreamID == streamID && share.ArtifactID == artifactID {
			shares = append(shares, share)
		}
	}
	return shares, nil
}

func (s *MemoryStreamStore) GetStreamArtifactShareByTokenHash(ctx context.Context, tokenHash string) (StreamArtifactShare, error) {
	if err := ctx.Err(); err != nil {
		return StreamArtifactShare{}, err
	}
	tokenHash = strings.TrimSpace(tokenHash)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, share := range s.artifactShares {
		if share.TokenHash == tokenHash {
			return share, nil
		}
	}
	return StreamArtifactShare{}, ErrNotFound
}

func (s *MemoryStreamStore) RevokeStreamArtifactShare(ctx context.Context, streamID, artifactID, shareID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	share, ok := s.artifactShares[shareID]
	if !ok || share.StreamID != streamID || share.ArtifactID != artifactID {
		return ErrNotFound
	}
	now := time.Now().UTC()
	share.RevokedAt = &now
	s.artifactShares[shareID] = share
	return nil
}

func memoryArtifactByID(artifacts []StreamArtifact, id string) (StreamArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return StreamArtifact{}, false
}
