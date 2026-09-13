package store

import (
	"context"
	"strings"
	"time"
)

func (s *MemoryStreamStore) SetStreamVideoOverlayBurnIn(ctx context.Context, streamID string, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	streamID = strings.TrimSpace(streamID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[streamID]; !ok {
		return ErrNotFound
	}
	s.mediaRuntimes[streamID] = StreamMediaRuntime{
		StreamID: streamID, VideoOverlayBurnIn: enabled, UpdatedAt: time.Now().UTC(),
	}
	return nil
}

func (s *MemoryStreamStore) GetStreamMediaRuntime(ctx context.Context, streamID string) (StreamMediaRuntime, error) {
	if err := ctx.Err(); err != nil {
		return StreamMediaRuntime{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.mediaRuntimes[strings.TrimSpace(streamID)]
	if !ok {
		return StreamMediaRuntime{}, ErrNotFound
	}
	return runtime, nil
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
	for _, claim := range s.youtubeRelayBindingClaims {
		if claim.StreamID == id && claim.YouTubeOutputID != strings.TrimSpace(settings.YouTubeOutputID) {
			return Stream{}, ErrYouTubeRelayBindingClaimActive
		}
	}
	if name := strings.TrimSpace(settings.Name); name != "" {
		stream.Name = name
	}
	stream.ScheduledStartAt = cloneTimePtr(settings.ScheduledStartAt)
	stream.ScheduledEndAt = cloneTimePtr(settings.ScheduledEndAt)
	stream.DiscordConfigID = strings.TrimSpace(settings.DiscordConfigID)
	stream.AutoStartTrigger = strings.TrimSpace(settings.AutoStartTrigger)
	stream.EncoderProfileID = strings.TrimSpace(settings.EncoderProfileID)
	stream.CaptionProfileID = strings.TrimSpace(settings.CaptionProfileID)
	stream.OverlayProfileID = strings.TrimSpace(settings.OverlayProfileID)
	stream.EncoderAudioGainDB = settings.EncoderAudioGainDB
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

func (s *MemoryStreamStore) UpdateStreamEncoderRuntimeSettings(ctx context.Context, id string, audioGainDB float64, overlayProfileID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[strings.TrimSpace(id)]
	if !ok {
		return Stream{}, ErrNotFound
	}
	stream.EncoderAudioGainDB = audioGainDB
	stream.OverlayProfileID = strings.TrimSpace(overlayProfileID)
	stream.UpdatedAt = time.Now().UTC()
	s.streams[stream.ID] = stream
	return stream, nil
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}
