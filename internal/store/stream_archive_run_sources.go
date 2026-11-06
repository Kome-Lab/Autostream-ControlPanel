package store

import (
	"strings"
	"time"
)

// A run source survives current assignment changes. It is written only from
// the locked start claim or an authenticated current primary's exact report.
type streamArchiveRunSourceKey struct {
	streamID         string
	archiveRunID     string
	archiveStartedAt time.Time
}

func streamArchiveRunSourceKeyFor(stream Stream) (streamArchiveRunSourceKey, error) {
	if strings.TrimSpace(stream.ID) == "" || !validArchiveRunID(stream.ArchiveRunID) ||
		stream.ArchiveStartedAt == nil || stream.ArchiveStartedAt.IsZero() {
		return streamArchiveRunSourceKey{}, ErrInvalidStreamArtifact
	}
	return streamArchiveRunSourceKey{
		streamID: stream.ID, archiveRunID: strings.TrimSpace(stream.ArchiveRunID),
		archiveStartedAt: stream.ArchiveStartedAt.UTC().Round(0),
	}, nil
}

// Caller holds streams.mu and the service-assignment authority lock.
func memoryRecordStreamArchiveRunSourceLocked(streams *MemoryStreamStore, stream Stream, serviceID string) error {
	if strings.TrimSpace(stream.ArchiveRunID) == "" && stream.ArchiveStartedAt == nil {
		return nil
	}
	key, err := streamArchiveRunSourceKeyFor(stream)
	if err != nil {
		return err
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ErrServiceAssignmentConflict
	}
	for existing, source := range streams.archiveRunSources {
		if existing.streamID == key.streamID && existing.archiveRunID == key.archiveRunID &&
			(existing != key || source != serviceID) {
			return ErrServiceAssignmentConflict
		}
	}
	if streams.archiveRunSources == nil {
		streams.archiveRunSources = make(map[streamArchiveRunSourceKey]string)
	}
	streams.archiveRunSources[key] = serviceID
	return nil
}

func streamArchiveRunSourceForReportLocked(streams *MemoryStreamStore, streamID string, artifacts []StreamArtifact) (string, bool, error) {
	source := ""
	found := false
	for _, artifact := range artifacts {
		key, err := streamArchiveRunSourceKeyFor(Stream{
			ID: streamID, ArchiveRunID: artifact.ArchiveRunID, ArchiveStartedAt: artifact.ArchiveStartedAt,
		})
		if err != nil {
			return "", false, err
		}
		currentSource, exists := streams.archiveRunSources[key]
		if !exists {
			for existing := range streams.archiveRunSources {
				if existing.streamID == key.streamID && existing.archiveRunID == key.archiveRunID {
					return "", false, ErrForbidden
				}
			}
			if found {
				return "", false, ErrForbidden
			}
			continue
		}
		if currentSource == "" || (found && source != currentSource) {
			return "", false, ErrForbidden
		}
		source, found = currentSource, true
	}
	return source, found, nil
}

func streamArtifactReportHasConflictingIdentity(existing, report StreamArtifact, serviceID string) bool {
	return !sameOptionalTime(existing.ArchiveStartedAt, report.ArchiveStartedAt) ||
		(strings.TrimSpace(existing.SourceServiceID) != "" && existing.SourceServiceID != serviceID)
}

func streamArtifactReportTokenAllowed(token ServiceToken) bool {
	return token.RevokedAt == nil && token.ServiceType == "encoder_recorder" &&
		validateServiceScopes(token.Scopes) == nil && hasString(token.Scopes, "encoder.status.write")
}
