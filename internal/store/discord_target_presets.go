package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var discordOpaqueIDPattern = regexp.MustCompile(`^[0-9]{1,32}$`)

type DiscordTargetPreset struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	GuildID         string     `json:"guild_id"`
	TextChannelID   string     `json:"text_channel_id"`
	VoiceChannelID  string     `json:"voice_channel_id"`
	Revision        uint64     `json:"revision"`
	CreatedByUserID string     `json:"created_by_user_id"`
	UpdatedByUserID string     `json:"updated_by_user_id"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type DiscordTargetPresetStore interface {
	ListDiscordTargetPresets(ctx context.Context) ([]DiscordTargetPreset, error)
	GetDiscordTargetPreset(ctx context.Context, id string, includeDeleted bool) (DiscordTargetPreset, error)
	CreateDiscordTargetPreset(ctx context.Context, preset DiscordTargetPreset) (DiscordTargetPreset, error)
	UpdateDiscordTargetPreset(ctx context.Context, id string, preset DiscordTargetPreset, expectedRevision uint64) (DiscordTargetPreset, error)
	DeleteDiscordTargetPreset(ctx context.Context, id, actorUserID string, expectedRevision uint64) (DiscordTargetPreset, error)
}

func ValidateDiscordTargetPreset(preset DiscordTargetPreset) error {
	preset.Name = strings.TrimSpace(preset.Name)
	if preset.Name == "" || len([]rune(preset.Name)) > 128 {
		return errors.New("invalid_name")
	}
	for _, value := range []string{preset.GuildID, preset.TextChannelID, preset.VoiceChannelID} {
		if !discordOpaqueIDPattern.MatchString(strings.TrimSpace(value)) {
			return errors.New("invalid_discord_id")
		}
	}
	return nil
}

func normalizeDiscordTargetPreset(preset DiscordTargetPreset) DiscordTargetPreset {
	preset.Name = strings.TrimSpace(preset.Name)
	preset.GuildID = strings.TrimSpace(preset.GuildID)
	preset.TextChannelID = strings.TrimSpace(preset.TextChannelID)
	preset.VoiceChannelID = strings.TrimSpace(preset.VoiceChannelID)
	return preset
}

type MemoryDiscordTargetPresetStore struct {
	mu      sync.Mutex
	presets map[string]DiscordTargetPreset
	now     func() time.Time
}

func NewMemoryDiscordTargetPresetStore() *MemoryDiscordTargetPresetStore {
	return &MemoryDiscordTargetPresetStore{presets: map[string]DiscordTargetPreset{}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *MemoryDiscordTargetPresetStore) ListDiscordTargetPresets(_ context.Context) ([]DiscordTargetPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []DiscordTargetPreset{}
	for _, preset := range s.presets {
		if preset.DeletedAt == nil {
			result = append(result, preset)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result, nil
}

func (s *MemoryDiscordTargetPresetStore) GetDiscordTargetPreset(_ context.Context, id string, includeDeleted bool) (DiscordTargetPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	preset, ok := s.presets[strings.TrimSpace(id)]
	if !ok || (!includeDeleted && preset.DeletedAt != nil) {
		return DiscordTargetPreset{}, ErrNotFound
	}
	return preset, nil
}

func (s *MemoryDiscordTargetPresetStore) CreateDiscordTargetPreset(_ context.Context, preset DiscordTargetPreset) (DiscordTargetPreset, error) {
	preset = normalizeDiscordTargetPreset(preset)
	if err := ValidateDiscordTargetPreset(preset); err != nil {
		return DiscordTargetPreset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.presets {
		if existing.DeletedAt == nil && strings.EqualFold(existing.Name, preset.Name) {
			return DiscordTargetPreset{}, ErrAlreadyExists
		}
	}
	now := s.now()
	preset.ID = newUUID()
	preset.Revision = 1
	preset.UpdatedByUserID = preset.CreatedByUserID
	preset.CreatedAt = now
	preset.UpdatedAt = now
	s.presets[preset.ID] = preset
	return preset, nil
}

func (s *MemoryDiscordTargetPresetStore) UpdateDiscordTargetPreset(_ context.Context, id string, preset DiscordTargetPreset, expectedRevision uint64) (DiscordTargetPreset, error) {
	preset = normalizeDiscordTargetPreset(preset)
	if err := ValidateDiscordTargetPreset(preset); err != nil {
		return DiscordTargetPreset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.presets[strings.TrimSpace(id)]
	if !ok || current.DeletedAt != nil {
		return DiscordTargetPreset{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return DiscordTargetPreset{}, ErrRevisionConflict
	}
	for otherID, other := range s.presets {
		if otherID != current.ID && other.DeletedAt == nil && strings.EqualFold(other.Name, preset.Name) {
			return DiscordTargetPreset{}, ErrAlreadyExists
		}
	}
	current.Name = preset.Name
	current.GuildID = preset.GuildID
	current.TextChannelID = preset.TextChannelID
	current.VoiceChannelID = preset.VoiceChannelID
	current.UpdatedByUserID = preset.UpdatedByUserID
	current.Revision++
	current.UpdatedAt = s.now()
	s.presets[current.ID] = current
	return current, nil
}

func (s *MemoryDiscordTargetPresetStore) DeleteDiscordTargetPreset(_ context.Context, id, actorUserID string, expectedRevision uint64) (DiscordTargetPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	preset, ok := s.presets[strings.TrimSpace(id)]
	if !ok || preset.DeletedAt != nil {
		return DiscordTargetPreset{}, ErrNotFound
	}
	if preset.Revision != expectedRevision {
		return DiscordTargetPreset{}, ErrRevisionConflict
	}
	now := s.now()
	preset.DeletedAt = &now
	preset.UpdatedAt = now
	preset.UpdatedByUserID = actorUserID
	preset.Revision++
	s.presets[preset.ID] = preset
	return preset, nil
}

type MariaDBDiscordTargetPresetStore struct{ db *sql.DB }

func NewMariaDBDiscordTargetPresetStore(db *sql.DB) MariaDBDiscordTargetPresetStore {
	return MariaDBDiscordTargetPresetStore{db: db}
}

const discordPresetSelect = `SELECT id,name,guild_id,text_channel_id,voice_channel_id,revision,created_by_user_id,updated_by_user_id,deleted_at,created_at,updated_at FROM discord_target_presets`

func scanDiscordTargetPreset(row scanner) (DiscordTargetPreset, error) {
	var preset DiscordTargetPreset
	var deleted sql.NullTime
	err := row.Scan(&preset.ID, &preset.Name, &preset.GuildID, &preset.TextChannelID, &preset.VoiceChannelID, &preset.Revision, &preset.CreatedByUserID, &preset.UpdatedByUserID, &deleted, &preset.CreatedAt, &preset.UpdatedAt)
	if deleted.Valid {
		preset.DeletedAt = &deleted.Time
	}
	return preset, err
}

func (s MariaDBDiscordTargetPresetStore) ListDiscordTargetPresets(ctx context.Context) ([]DiscordTargetPreset, error) {
	rows, err := s.db.QueryContext(ctx, discordPresetSelect+` WHERE deleted_at IS NULL ORDER BY LOWER(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DiscordTargetPreset{}
	for rows.Next() {
		preset, err := scanDiscordTargetPreset(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, preset)
	}
	return result, rows.Err()
}

func (s MariaDBDiscordTargetPresetStore) GetDiscordTargetPreset(ctx context.Context, id string, includeDeleted bool) (DiscordTargetPreset, error) {
	query := discordPresetSelect + ` WHERE id=?`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	preset, err := scanDiscordTargetPreset(s.db.QueryRowContext(ctx, query, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordTargetPreset{}, ErrNotFound
	}
	return preset, err
}

func (s MariaDBDiscordTargetPresetStore) CreateDiscordTargetPreset(ctx context.Context, preset DiscordTargetPreset) (DiscordTargetPreset, error) {
	preset = normalizeDiscordTargetPreset(preset)
	if err := ValidateDiscordTargetPreset(preset); err != nil {
		return DiscordTargetPreset{}, err
	}
	now := time.Now().UTC()
	preset.ID = newUUID()
	preset.Revision = 1
	preset.UpdatedByUserID = preset.CreatedByUserID
	preset.CreatedAt = now
	preset.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO discord_target_presets(id,name,guild_id,text_channel_id,voice_channel_id,revision,created_by_user_id,updated_by_user_id,deleted_at,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,NULL,?,?)`, preset.ID, preset.Name, preset.GuildID, preset.TextChannelID, preset.VoiceChannelID, preset.CreatedByUserID, preset.UpdatedByUserID, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return DiscordTargetPreset{}, ErrAlreadyExists
		}
		return DiscordTargetPreset{}, err
	}
	return preset, nil
}

func (s MariaDBDiscordTargetPresetStore) UpdateDiscordTargetPreset(ctx context.Context, id string, preset DiscordTargetPreset, expectedRevision uint64) (DiscordTargetPreset, error) {
	preset = normalizeDiscordTargetPreset(preset)
	if err := ValidateDiscordTargetPreset(preset); err != nil {
		return DiscordTargetPreset{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE discord_target_presets SET name=?,guild_id=?,text_channel_id=?,voice_channel_id=?,revision=revision+1,updated_by_user_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL AND revision=?`, preset.Name, preset.GuildID, preset.TextChannelID, preset.VoiceChannelID, preset.UpdatedByUserID, now, strings.TrimSpace(id), expectedRevision)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return DiscordTargetPreset{}, ErrAlreadyExists
		}
		return DiscordTargetPreset{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, getErr := s.GetDiscordTargetPreset(ctx, id, false); errors.Is(getErr, ErrNotFound) {
			return DiscordTargetPreset{}, ErrNotFound
		}
		return DiscordTargetPreset{}, ErrRevisionConflict
	}
	return s.GetDiscordTargetPreset(ctx, id, false)
}

func (s MariaDBDiscordTargetPresetStore) DeleteDiscordTargetPreset(ctx context.Context, id, actorUserID string, expectedRevision uint64) (DiscordTargetPreset, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE discord_target_presets SET deleted_at=?,revision=revision+1,updated_by_user_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL AND revision=?`, now, actorUserID, now, strings.TrimSpace(id), expectedRevision)
	if err != nil {
		return DiscordTargetPreset{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, getErr := s.GetDiscordTargetPreset(ctx, id, false); errors.Is(getErr, ErrNotFound) {
			return DiscordTargetPreset{}, ErrNotFound
		}
		return DiscordTargetPreset{}, ErrRevisionConflict
	}
	return s.GetDiscordTargetPreset(ctx, id, true)
}

var _ DiscordTargetPresetStore = (*MemoryDiscordTargetPresetStore)(nil)
var _ DiscordTargetPresetStore = MariaDBDiscordTargetPresetStore{}
