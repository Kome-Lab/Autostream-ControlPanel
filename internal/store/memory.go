package store

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"
)

type MemoryStreamStore struct {
	mu              sync.Mutex
	streams         map[string]Stream
	logs            map[string][]StreamLog
	artifacts       map[string][]StreamArtifact
	artifactShares  map[string]StreamArtifactShare
	youtubeRuntimes map[string]StreamYouTubeRuntime
}

func NewMemoryStreamStore() *MemoryStreamStore {
	return &MemoryStreamStore{streams: map[string]Stream{}, logs: map[string][]StreamLog{}, artifacts: map[string][]StreamArtifact{}, artifactShares: map[string]StreamArtifactShare{}, youtubeRuntimes: map[string]StreamYouTubeRuntime{}}
}

func (s *MemoryStreamStore) ListStreams(ctx context.Context) ([]Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		items = append(items, stream)
	}
	return items, nil
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

func (s *MemoryStreamStore) SaveStreamYouTubeRuntime(ctx context.Context, runtime StreamYouTubeRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[runtime.StreamID]; !ok {
		return ErrNotFound
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
		if runtime.Mode != "live_api" || runtime.CompleteNextRetryAt.IsZero() || runtime.CompleteNextRetryAt.After(now) {
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
	delete(s.youtubeRuntimes, streamID)
	return nil
}

func (s *MemoryStreamStore) RetryArchiveUpload(ctx context.Context, id, actorUserID string) (StreamLog, error) {
	if err := ctx.Err(); err != nil {
		return StreamLog{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[id]; !ok {
		return StreamLog{}, ErrNotFound
	}
	log := StreamLog{ID: newUUID(), StreamID: id, Level: "info", Message: "archive upload retry requested", Fields: map[string]any{"actor_user_id": actorUserID}, CreatedAt: time.Now().UTC()}
	s.logs[id] = append(s.logs[id], log)
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
	return append([]StreamLog(nil), s.logs[id]...), nil
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
	for _, artifact := range NormalizeStreamArtifacts(id, artifacts) {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		filtered := current[:0]
		for _, existing := range current {
			if existing.Kind == artifact.Kind && existing.Name == artifact.Name {
				continue
			}
			filtered = append(filtered, existing)
		}
		current = append(filtered, artifact)
	}
	s.artifacts[id] = current
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
	for _, artifact := range s.artifacts[streamID] {
		if artifact.ID != artifactID && artifact.Name == name {
			return StreamArtifact{}, ErrAlreadyExists
		}
	}
	for index, artifact := range s.artifacts[streamID] {
		if artifact.ID != artifactID {
			continue
		}
		artifact.Name = name
		artifact.RelativePath = path.Join("final", streamID, name)
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
