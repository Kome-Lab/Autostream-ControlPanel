package store

import (
	"context"
	"strings"
	"sync"
	"time"
)

type MemoryStreamStore struct {
	mu              sync.Mutex
	streams         map[string]Stream
	logs            map[string][]StreamLog
	artifacts       map[string][]StreamArtifact
	youtubeRuntimes map[string]StreamYouTubeRuntime
}

func NewMemoryStreamStore() *MemoryStreamStore {
	return &MemoryStreamStore{streams: map[string]Stream{}, logs: map[string][]StreamLog{}, artifacts: map[string][]StreamArtifact{}, youtubeRuntimes: map[string]StreamYouTubeRuntime{}}
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
	stream.DiscordConfigID = strings.TrimSpace(settings.DiscordConfigID)
	stream.DiscordGuildID = strings.TrimSpace(settings.DiscordGuildID)
	stream.DiscordVoiceID = strings.TrimSpace(settings.DiscordVoiceID)
	stream.DiscordTextID = strings.TrimSpace(settings.DiscordTextID)
	stream.EncoderProfileID = strings.TrimSpace(settings.EncoderProfileID)
	stream.CaptionProfileID = strings.TrimSpace(settings.CaptionProfileID)
	stream.OverlayProfileID = strings.TrimSpace(settings.OverlayProfileID)
	stream.ArchiveProfileID = strings.TrimSpace(settings.ArchiveProfileID)
	stream.YouTubeOutputID = strings.TrimSpace(settings.YouTubeOutputID)
	stream.EncoderInputURL = strings.TrimSpace(settings.EncoderInputURL)
	stream.UpdatedAt = time.Now().UTC()
	s.streams[id] = stream
	return stream, nil
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
