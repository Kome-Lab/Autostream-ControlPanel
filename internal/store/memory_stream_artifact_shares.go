package store

import (
	"context"
	"strings"
	"time"
)

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
