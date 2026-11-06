package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// WriteStreamArtifactReport commits the event, source and catalog under the
// same locks used by start claims and assignment changes.
func (s *MemoryStreamStore) WriteStreamArtifactReport(ctx context.Context, token ServiceToken, event ServiceStreamEvent, artifacts []StreamArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.ServiceID == "" || event.StreamID == "" || event.EventType == "" {
		return errors.New("missing required stream event field")
	}
	if err := ValidateStreamArtifactReport(event.StreamID, artifacts); err != nil {
		return err
	}
	normalized := NormalizeStreamArtifacts(event.StreamID, artifacts)
	services := s.serviceAssignmentGuard
	if services == nil {
		return ErrServiceAssignmentGuardUnavailable
	}
	services.mu.Lock()
	defer services.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	service, ok := services.services[event.ServiceID]
	if !ok {
		return ErrNotFound
	}
	activeToken, ok := services.serviceTokens[token.ID]
	if !ok || service.TokenID != token.ID || service.ServiceType != "encoder_recorder" ||
		!streamArtifactReportTokenAllowed(activeToken) {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(service.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	authority, ok := s.streams[event.StreamID]
	if !ok {
		return ErrNotFound
	}
	if authority.DeletedAt != nil {
		return ErrForbidden
	}
	owner, role, err := services.consistentServiceAssignmentLocked(service)
	if err != nil {
		return ErrForbidden
	}
	source, recorded, err := streamArchiveRunSourceForReportLocked(s, event.StreamID, normalized)
	if err != nil {
		return err
	}
	backfill := false
	if recorded {
		if source != event.ServiceID {
			return ErrForbidden
		}
	} else {
		if owner != event.StreamID {
			return ErrForbidden
		}
		if strings.TrimSpace(authority.ArchiveRunID) != "" {
			if role != "primary" || !streamArtifactReportMatchesArchiveAuthority(authority, normalized) {
				return ErrForbidden
			}
			backfill = true
		}
	}
	current := append([]StreamArtifact(nil), s.artifacts[event.StreamID]...)
	if recorded || backfill {
		for _, artifact := range normalized {
			for _, existing := range current {
				if existing.ArchiveRunID == artifact.ArchiveRunID && existing.Kind == artifact.Kind &&
					existing.Name == artifact.Name && streamArtifactReportHasConflictingIdentity(existing, artifact, event.ServiceID) {
					return ErrForbidden
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backfill {
		if err := memoryRecordStreamArchiveRunSourceLocked(s, authority, event.ServiceID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	for _, artifact := range normalized {
		artifact.SourceServiceID = event.ServiceID
		updated := false
		for index, existing := range current {
			if existing.ArchiveRunID != artifact.ArchiveRunID || existing.Kind != artifact.Kind || existing.Name != artifact.Name {
				continue
			}
			existing.RelativePath = artifact.RelativePath
			existing.SizeBytes = artifact.SizeBytes
			existing.SourceServiceID = artifact.SourceServiceID
			current[index] = existing
			updated = true
			break
		}
		if !updated {
			artifact.ID = newUUID()
			artifact.CreatedAt = now
			current = append(current, artifact)
		}
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	services.streamEvents = append(services.streamEvents, event)
	s.artifacts[event.StreamID] = current
	reportMatchesAuthority := streamArtifactReportMatchesArchiveAuthority(authority, normalized)
	if reportMatchesAuthority || strings.TrimSpace(authority.ArchiveRunID) == "" {
		s.artifactReports[event.StreamID] = true
	}
	if reportMatchesAuthority {
		delete(s.archiveRetryPending, event.StreamID)
		if authority.ArchiveReportedAt == nil {
			authority.ArchiveReportedAt = &now
			authority.UpdatedAt = now
			s.streams[event.StreamID] = authority
		}
	}
	return nil
}
