package store

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStreamStore) claimSelectedStreamStart(ctx context.Context, request StreamStartClaimRequest) (ClaimedStreamStart, error) {
	if err := ctx.Err(); err != nil {
		return ClaimedStreamStart{}, err
	}
	services := s.serviceAssignmentGuard
	if services == nil {
		return ClaimedStreamStart{}, ErrServiceAssignmentGuardUnavailable
	}
	services.mu.Lock()
	defer services.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if services.streamAssignmentGuard != s {
		return ClaimedStreamStart{}, ErrServiceAssignmentGuardUnavailable
	}
	stream, ok := s.streams[strings.TrimSpace(request.StreamID)]
	if !ok || stream.DeletedAt != nil || !streamStartClaimStatus(stream.Status) ||
		!strings.EqualFold(strings.TrimSpace(stream.Status), strings.TrimSpace(request.ExpectedStatus)) ||
		!stream.UpdatedAt.Equal(request.ExpectedStreamUpdatedAt) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	if memoryStreamAssignmentProtectionLocked(s, stream).protected() {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	expected, err := validateSelectedStartClaim(request, stream)
	if err != nil {
		return ClaimedStreamStart{}, err
	}
	wanted := make(map[string]RegisteredService, 3)
	for serviceType, observed := range expected {
		service, exists := services.services[observed.ServiceID]
		if !exists || service.ServiceType != serviceType || service.CurrentStreamID != observed.CurrentStreamID {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		owner, _, consistencyErr := services.consistentServiceAssignmentLocked(service)
		if consistencyErr != nil {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if owner != "" && owner != stream.ID {
			previous, exists := s.streams[owner]
			if !exists || previous.DeletedAt != nil {
				return ClaimedStreamStart{}, ErrServiceAssignmentConflict
			}
			if memoryStreamAssignmentProtectionLocked(s, previous).protected() {
				return ClaimedStreamStart{}, ErrNodeExecutionBusy
			}
		}
		wanted[service.ServiceID] = service
	}
	// Validate the whole removal set before the first mutation. A changed
	// selection may leave an inactive execution assignment on this frame.
	remove := make([]string, 0, 6)
	keep := make(map[string]bool, 3)
	for key, serviceID := range services.assignments {
		owner, serviceType, role := assignmentPartsFromKey(key)
		_, selected := wanted[serviceID]
		if owner != stream.ID && !selected {
			continue
		}
		service, exists := services.services[serviceID]
		if !exists || service.ServiceType != serviceType || strings.TrimSpace(services.assignmentIDs[key]) == "" {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		actualOwner, actualRole, consistencyErr := services.consistentServiceAssignmentLocked(service)
		if consistencyErr != nil || actualOwner != owner || actualRole != normalizeAssignmentRole(role) {
			return ClaimedStreamStart{}, ErrServiceAssignmentConflict
		}
		if owner == stream.ID && selected && normalizeAssignmentRole(role) == "primary" {
			keep[serviceID] = true
			continue
		}
		remove = append(remove, key)
	}
	now := time.Now().UTC()
	authority := StreamArchiveAuthority{}
	if request.ArchiveEnabled {
		startedAt := request.ArchiveStartedAt.UTC()
		if startedAt.IsZero() {
			startedAt = now
		}
		authority.RunID = StreamArchiveRunIDForStart(startedAt)
		authority.StartedAt = cloneTimePtr(&startedAt)
	}
	if streamStartClockConflict(stream, now, authority) {
		return ClaimedStreamStart{}, ErrServiceAssignmentConflict
	}
	stream.Status = "starting"
	stream.ArchiveRunID = authority.RunID
	stream.ArchiveStartedAt = cloneTimePtr(authority.StartedAt)
	stream.ArchiveReportedAt = nil
	stream.UpdatedAt = now
	if err := memoryRecordStreamArchiveRunSourceLocked(s, stream, expected["encoder_recorder"].ServiceID); err != nil {
		return ClaimedStreamStart{}, err
	}
	for _, key := range remove {
		serviceID := services.assignments[key]
		delete(services.assignments, key)
		delete(services.assignmentIDs, key)
		if _, selected := wanted[serviceID]; !selected {
			released := services.services[serviceID]
			released.CurrentStreamID = ""
			if released.Status == "assigned" {
				released.Status = "registered"
			}
			released.AssignmentRole = ""
			released.UpdatedAt = now
			services.services[serviceID] = released
		}
	}
	for serviceID, service := range wanted {
		if keep[serviceID] {
			continue
		}
		key := assignmentKey(stream.ID, service.ServiceType, "primary", serviceID)
		services.assignments[key] = serviceID
		services.assignmentIDs[key] = newUUID()
		service.CurrentStreamID = stream.ID
		service.AssignmentRole = "primary"
		service.Status = "assigned"
		service.UpdatedAt = now
		services.services[serviceID] = service
	}
	s.artifactReports[stream.ID] = false
	s.streams[stream.ID] = stream
	primary := make([]RegisteredService, 0, 3)
	claims := make([]StreamStartAssignmentClaim, 0, 3)
	for serviceID := range wanted {
		service := services.services[serviceID]
		service.AssignmentRole = "primary"
		primary = append(primary, service)
		key := assignmentKey(stream.ID, service.ServiceType, "primary", serviceID)
		claims = append(claims, StreamStartAssignmentClaim{AssignmentID: services.assignmentIDs[key], ServiceID: serviceID, ServiceType: service.ServiceType, Role: "primary"})
	}
	sort.Slice(primary, func(i, j int) bool { return primary[i].ServiceType < primary[j].ServiceType })
	sort.Slice(claims, func(i, j int) bool { return claims[i].AssignmentID < claims[j].AssignmentID })
	ownership := StreamStartOwnershipClaim{StreamID: stream.ID, StreamUpdatedAt: now,
		StreamIdentity: streamStartOwnershipIdentity(stream), Assignments: claims, Archive: authority}
	return ClaimedStreamStart{Stream: stream, PrimaryAssignments: primary, ArchiveAuthority: authority, OwnershipClaim: ownership}, nil
}
