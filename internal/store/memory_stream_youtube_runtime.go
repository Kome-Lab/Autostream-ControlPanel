package store

import (
	"context"
	"strings"
	"time"
)

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
