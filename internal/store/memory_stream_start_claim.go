package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

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
	if !validArchiveRunID(archiveRunID) || startedAt.IsZero() {
		return Stream{}, ErrInvalidStreamArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[id]
	if !ok {
		return Stream{}, ErrNotFound
	}
	stream.ArchiveRunID = archiveRunID
	value := startedAt.UTC()
	stream.ArchiveStartedAt = &value
	stream.ArchiveReportedAt = nil
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	s.artifactReports[id] = false
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
	if request.ArchiveEnabled {
		startedAt := request.ArchiveStartedAt.UTC()
		if startedAt.IsZero() {
			startedAt = now
		}
		stream.ArchiveRunID = StreamArchiveRunIDForStart(startedAt)
		stream.ArchiveStartedAt = cloneTimePtr(&startedAt)
		authority.RunID = stream.ArchiveRunID
		authority.StartedAt = cloneTimePtr(stream.ArchiveStartedAt)
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
	if strings.TrimSpace(claim.StreamIdentity) == "" || streamStartOwnershipIdentity(stream) != claim.StreamIdentity || !stream.UpdatedAt.Equal(claim.StreamUpdatedAt) || !archiveAuthorityMatchesClaim(stream, claim.Archive) {
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
