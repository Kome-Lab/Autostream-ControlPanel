package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryAuthStore) AssignServiceToStream(ctx context.Context, serviceID, streamID, actorUserID string) (RegisteredService, error) {
	return s.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, "primary")
}

func (s *MemoryAuthStore) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	guardBound := s.streamAssignmentGuard != nil
	s.mu.Unlock()
	if guardBound {
		return s.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: serviceID, StreamID: streamID, ActorUserID: actorUserID, AssignmentRole: assignmentRole})
	}
	assignmentRole = normalizeAssignmentRole(assignmentRole)
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if !streamAssignableServiceType(svc.ServiceType) {
		return RegisteredService{}, ErrInvalidServiceAssignment
	}
	targetKey := assignmentKey(streamID, svc.ServiceType, assignmentRole, serviceID)
	replacedServiceIDs := make(map[string]bool)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID || (assignmentRole == "primary" && assignmentKeyMatchesPrimary(key, streamID, svc.ServiceType)) || key == targetKey {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
			if assignedServiceID != serviceID {
				replacedServiceIDs[assignedServiceID] = true
			}
		}
	}
	for replacedServiceID := range replacedServiceIDs {
		replaced := s.services[replacedServiceID]
		replaced.CurrentStreamID = ""
		if replaced.Status == "assigned" {
			replaced.Status = "registered"
		}
		replaced.UpdatedAt = time.Now().UTC()
		s.services[replacedServiceID] = replaced
	}
	s.assignments[targetKey] = serviceID
	s.assignmentIDs[targetKey] = newUUID()
	svc.CurrentStreamID = streamID
	svc.Status = "assigned"
	svc.AssignmentRole = assignmentRole
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) UnassignServiceFromStream(ctx context.Context, serviceID, actorUserID string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	guardBound := s.streamAssignmentGuard != nil
	s.mu.Unlock()
	if guardBound {
		return s.UnassignServiceFromStreamGuarded(ctx, ServiceUnassignmentMutation{ServiceID: serviceID, ActorUserID: actorUserID})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	svc.CurrentStreamID = ""
	if svc.Status == "assigned" {
		svc.Status = "registered"
	}
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) AssignServiceToStreamGuarded(ctx context.Context, mutation ServiceAssignmentMutation) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	mutation.StreamID = strings.TrimSpace(mutation.StreamID)
	mutation.AssignmentRole = normalizeAssignmentRole(mutation.AssignmentRole)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return RegisteredService{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()

	svc, ok := s.services[mutation.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	if !streamAssignableServiceType(svc.ServiceType) {
		return RegisteredService{}, ErrInvalidServiceAssignment
	}
	target, ok := streams.streams[mutation.StreamID]
	if !ok || target.DeletedAt != nil {
		return RegisteredService{}, ErrNotFound
	}
	currentStreamID, currentRole, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == mutation.StreamID && currentRole == mutation.AssignmentRole {
		svc.AssignmentRole = currentRole
		return svc, nil
	}
	if currentStreamID != "" {
		source, ok := streams.streams[currentStreamID]
		if !ok || source.DeletedAt != nil {
			return RegisteredService{}, ErrServiceAssignmentConflict
		}
		if memoryStreamAssignmentProtectionLocked(streams, source).protected() {
			return RegisteredService{}, ErrServiceAssignmentProtectedStream
		}
	}
	if memoryStreamAssignmentProtectionLocked(streams, target).protected() {
		return RegisteredService{}, ErrServiceAssignmentProtectedStream
	}

	targetKey := assignmentKey(mutation.StreamID, svc.ServiceType, mutation.AssignmentRole, mutation.ServiceID)
	replacedServiceIDs := make(map[string]bool)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID ||
			(mutation.AssignmentRole == "primary" && assignmentKeyMatchesPrimary(key, mutation.StreamID, svc.ServiceType)) ||
			key == targetKey {
			if assignedServiceID != mutation.ServiceID {
				replaced, ok := s.services[assignedServiceID]
				if !ok {
					return RegisteredService{}, ErrServiceAssignmentConflict
				}
				replacedOwner, _, err := s.consistentServiceAssignmentLocked(replaced)
				if err != nil || replacedOwner != mutation.StreamID {
					return RegisteredService{}, ErrServiceAssignmentConflict
				}
				replacedServiceIDs[assignedServiceID] = true
			}
		}
	}

	now := time.Now().UTC()
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID ||
			(mutation.AssignmentRole == "primary" && assignmentKeyMatchesPrimary(key, mutation.StreamID, svc.ServiceType)) ||
			key == targetKey {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	for replacedServiceID := range replacedServiceIDs {
		replaced := s.services[replacedServiceID]
		replaced.CurrentStreamID = ""
		if replaced.Status == "assigned" {
			replaced.Status = "registered"
		}
		replaced.UpdatedAt = now
		s.services[replacedServiceID] = replaced
	}
	s.assignments[targetKey] = mutation.ServiceID
	s.assignmentIDs[targetKey] = newUUID()
	svc.CurrentStreamID = mutation.StreamID
	svc.Status = "assigned"
	svc.AssignmentRole = mutation.AssignmentRole
	svc.UpdatedAt = now
	s.services[mutation.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) UnassignServiceFromStreamGuarded(ctx context.Context, mutation ServiceUnassignmentMutation) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	mutation.ServiceID = strings.TrimSpace(mutation.ServiceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return RegisteredService{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()

	svc, ok := s.services[mutation.ServiceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	currentStreamID, currentRole, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return RegisteredService{}, err
	}
	if mutation.ExpectedCurrentStreamID != nil && currentStreamID != strings.TrimSpace(*mutation.ExpectedCurrentStreamID) {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if currentStreamID == "" {
		svc.AssignmentRole = currentRole
		return svc, nil
	}
	owner, ok := streams.streams[currentStreamID]
	if !ok || owner.DeletedAt != nil {
		return RegisteredService{}, ErrServiceAssignmentConflict
	}
	if memoryStreamAssignmentProtectionLocked(streams, owner).protected() {
		return RegisteredService{}, ErrServiceUnassignProtectedStream
	}
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == mutation.ServiceID {
			delete(s.assignments, key)
			delete(s.assignmentIDs, key)
		}
	}
	svc.CurrentStreamID = ""
	if svc.Status == "assigned" {
		svc.Status = "registered"
	}
	svc.AssignmentRole = ""
	svc.UpdatedAt = time.Now().UTC()
	s.services[mutation.ServiceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) BeginStreamArchiveRetryGuarded(ctx context.Context, serviceID, streamID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	serviceID = strings.TrimSpace(serviceID)
	streamID = strings.TrimSpace(streamID)
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streamAssignmentGuard
	if streams == nil {
		return Stream{}, ErrServiceAssignmentGuardUnavailable
	}
	streams.mu.Lock()
	defer streams.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return Stream{}, ErrNotFound
	}
	if svc.ServiceType != "encoder_recorder" {
		return Stream{}, ErrInvalidServiceAssignment
	}
	owner, _, err := s.consistentServiceAssignmentLocked(svc)
	if err != nil {
		return Stream{}, err
	}
	if owner != streamID {
		return Stream{}, ErrServiceAssignmentConflict
	}
	stream, ok := streams.streams[streamID]
	if !ok || stream.DeletedAt != nil {
		return Stream{}, ErrNotFound
	}
	if streams.archiveRetryPending[streamID] {
		return stream, nil
	}
	streams.archiveRetryPending[streamID] = true
	if stream.ArchiveReportedAt != nil {
		stream.ArchiveReportedAt = nil
		stream.UpdatedAt = time.Now().UTC()
		streams.streams[streamID] = stream
		streams.artifactReports[streamID] = false
	}
	return stream, nil
}

func (s *MemoryAuthStore) consistentServiceAssignmentLocked(service RegisteredService) (string, string, error) {
	owner := ""
	role := ""
	count := 0
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID != service.ServiceID {
			continue
		}
		streamID, serviceType, assignmentRole := assignmentPartsFromKey(key)
		if serviceType != service.ServiceType || streamID == "" {
			return "", "", ErrServiceAssignmentConflict
		}
		owner = streamID
		role = assignmentRole
		count++
	}
	if count > 1 || (count == 0) != (strings.TrimSpace(service.CurrentStreamID) == "") {
		return "", "", ErrServiceAssignmentConflict
	}
	if count == 1 && owner != strings.TrimSpace(service.CurrentStreamID) {
		return "", "", ErrServiceAssignmentConflict
	}
	return owner, role, nil
}

func memoryStreamAssignmentProtectionLocked(streams *MemoryStreamStore, stream Stream) streamAssignmentProtection {
	state := streamAssignmentProtection{Stream: stream, ArchiveRetryPending: streams.archiveRetryPending[stream.ID], HasArchiveReport: streams.artifactReports[stream.ID]}
	for _, artifact := range streams.artifacts[stream.ID] {
		if isArchiveRecordingArtifact(artifact) {
			state.HasRecordingArtifact = true
			break
		}
	}
	return state
}

func (s *MemoryAuthStore) ListStreamAssignments(ctx context.Context, streamID string) ([]RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	services := make([]RegisteredService, 0)
	for key, serviceID := range s.assignments {
		if !strings.HasPrefix(key, streamID+"\x00") {
			continue
		}
		if svc, ok := s.services[serviceID]; ok {
			svc.AssignmentRole = assignmentRoleFromKey(key)
			services = append(services, svc)
		}
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].ServiceType == services[j].ServiceType {
			if services[i].AssignmentRole != services[j].AssignmentRole {
				return services[i].AssignmentRole == "primary"
			}
			return services[i].ServiceName < services[j].ServiceName
		}
		return services[i].ServiceType < services[j].ServiceType
	})
	return services, nil
}

func (s *MemoryAuthStore) ListServiceAssignmentsForService(ctx context.Context, serviceID string) ([]StreamServiceAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := make([]StreamServiceAssignment, 0)
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID != serviceID {
			continue
		}
		streamID, serviceType, role := assignmentPartsFromKey(key)
		assignments = append(assignments, StreamServiceAssignment{
			StreamID:       streamID,
			ServiceID:      serviceID,
			ServiceType:    serviceType,
			AssignmentRole: role,
			AssignedAt:     s.services[serviceID].UpdatedAt,
		})
	}
	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].StreamID == assignments[j].StreamID {
			return assignments[i].AssignmentRole < assignments[j].AssignmentRole
		}
		return assignments[i].StreamID < assignments[j].StreamID
	})
	return assignments, nil
}

func (s *MemoryAuthStore) RequestServiceRestart(ctx context.Context, serviceID string) (RegisteredService, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredService{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[serviceID]
	if !ok {
		return RegisteredService{}, ErrNotFound
	}
	svc.Status = "restart_requested"
	svc.UpdatedAt = time.Now().UTC()
	s.services[serviceID] = svc
	return svc, nil
}

func (s *MemoryAuthStore) WriteStreamEvent(ctx context.Context, token ServiceToken, event ServiceStreamEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[event.ServiceID]
	if !ok {
		return ErrNotFound
	}
	if svc.TokenID != token.ID {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(svc.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	if !s.isAssignedLocked(event.ServiceID, event.StreamID) {
		return ErrForbidden
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	s.streamEvents = append(s.streamEvents, event)
	return nil
}

func (s *MemoryAuthStore) isAssignedLocked(serviceID, streamID string) bool {
	for key, assignedServiceID := range s.assignments {
		if assignedServiceID == serviceID && strings.HasPrefix(key, streamID+"\x00") {
			return true
		}
	}
	return false
}

func assignmentKey(streamID, serviceType, assignmentRole, serviceID string) string {
	if assignmentRole == "standby" {
		return streamID + "\x00standby\x00" + serviceType + "\x00" + serviceID
	}
	return streamID + "\x00primary\x00" + serviceType
}

func assignmentKeyMatchesPrimary(key, streamID, serviceType string) bool {
	return key == assignmentKey(streamID, serviceType, "primary", "")
}

func assignmentRoleFromKey(key string) string {
	parts := strings.Split(key, "\x00")
	if len(parts) >= 2 && parts[1] == "standby" {
		return "standby"
	}
	return "primary"
}

func assignmentPartsFromKey(key string) (streamID, serviceType, assignmentRole string) {
	parts := strings.Split(key, "\x00")
	if len(parts) >= 3 {
		streamID = parts[0]
		assignmentRole = parts[1]
		serviceType = parts[2]
		if assignmentRole != "standby" {
			assignmentRole = "primary"
		}
		return streamID, serviceType, assignmentRole
	}
	return "", "", "primary"
}
