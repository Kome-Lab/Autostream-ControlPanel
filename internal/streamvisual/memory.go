package streamvisual

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

// MemoryRepository is the v2 visual-settings authority used with the in-memory
// stream store. It mirrors one canonical visual row per stream and never reads
// or writes removed flat stream settings.
type MemoryRepository struct {
	mu       sync.Mutex
	streams  store.StreamStore
	settings map[string]Settings
}

func NewMemoryRepository(streams store.StreamStore) *MemoryRepository {
	return &MemoryRepository{streams: streams, settings: map[string]Settings{}}
}

func (r *MemoryRepository) Get(ctx context.Context, streamID string) (Settings, error) {
	if err := ctx.Err(); err != nil {
		return Settings{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return Settings{}, ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if settings, ok := r.settings[streamID]; ok {
		return settings, nil
	}
	if r.streams == nil {
		return Settings{}, ErrNotFound
	}
	if _, err := r.streams.GetStream(ctx, streamID); err != nil {
		return Settings{}, ErrNotFound
	}
	now := time.Now().UTC()
	settings := DefaultSettings(streamID)
	settings.DiscordTargetMode = "inherit"
	settings.Revision = 1
	settings.DiscordSnapshotRevision = 1
	settings.CreatedAt = now
	settings.UpdatedAt = now
	r.settings[streamID] = settings
	return settings, nil
}

func (r *MemoryRepository) Update(ctx context.Context, streamID, _ string, update Update) (Settings, error) {
	current, err := r.Get(ctx, streamID)
	if err != nil {
		return Settings{}, err
	}
	if current.Revision != update.ExpectedRevision {
		return Settings{}, ErrRevisionConflict
	}
	next, _, err := applyUpdate(current, update)
	if err != nil {
		return Settings{}, err
	}
	next.Revision++
	next.UpdatedAt = time.Now().UTC()
	r.mu.Lock()
	r.settings[next.StreamID] = next
	r.mu.Unlock()
	return next, nil
}

func (r *MemoryRepository) CreateStream(ctx context.Context, _ string, input Create) (store.Stream, Settings, error) {
	if r.streams == nil || strings.TrimSpace(input.Name) == "" || input.Settings.ExpectedRevision != 0 {
		return store.Stream{}, Settings{}, ErrInvalidSettings
	}
	stream, err := r.streams.CreateStream(ctx, strings.TrimSpace(input.Name))
	if err != nil {
		return store.Stream{}, Settings{}, err
	}
	settings, _, err := applyUpdate(DefaultSettings(stream.ID), input.Settings)
	if err != nil {
		_ = r.streams.DeleteStream(ctx, stream.ID)
		return store.Stream{}, Settings{}, err
	}
	if strings.TrimSpace(settings.DiscordTargetMode) == "" {
		settings.DiscordTargetMode = "inherit"
	}
	if strings.TrimSpace(input.StreamSettings.AutoStartTrigger) == "discord_voice_join" &&
		(strings.TrimSpace(input.StreamSettings.DiscordConfigID) == "" || strings.TrimSpace(settings.DiscordGuildID) == "" || strings.TrimSpace(settings.DiscordVoiceChannelID) == "") {
		_ = r.streams.DeleteStream(ctx, stream.ID)
		return store.Stream{}, Settings{}, ErrInvalidSettings
	}
	stream, err = r.streams.UpdateStreamSettings(ctx, stream.ID, input.StreamSettings)
	if err != nil {
		_ = r.streams.DeleteStream(ctx, stream.ID)
		return store.Stream{}, Settings{}, err
	}
	now := time.Now().UTC()
	settings.Revision = 1
	settings.DiscordSnapshotRevision = 1
	settings.CreatedAt = now
	settings.UpdatedAt = now
	r.mu.Lock()
	r.settings[stream.ID] = settings
	r.mu.Unlock()
	return stream, settings, nil
}

func (r *MemoryRepository) InspectAssets(context.Context, Settings) (AssetReadiness, error) {
	return AssetReadiness{BackgroundExists: true, BackgroundVariantReady: true, BackgroundHashVerified: true, CoverVariantReady: true, MediaAssetIntegrity: true}, nil
}
