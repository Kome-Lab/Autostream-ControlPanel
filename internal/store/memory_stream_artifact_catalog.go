package store

import (
	"context"
	"strings"
	"time"
)

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
	reportMatchesAuthority := streamArtifactReportMatchesArchiveAuthority(stream, normalized)
	if reportMatchesAuthority {
		delete(s.archiveRetryPending, id)
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
