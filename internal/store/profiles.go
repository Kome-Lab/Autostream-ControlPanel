package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProfileKind string

const (
	ProfileEncoder       ProfileKind = "encoder"
	ProfileArchive       ProfileKind = "archive"
	ProfileCaption       ProfileKind = "caption"
	ProfileOverlay       ProfileKind = "overlay"
	ProfileDiscordConfig ProfileKind = "discord_config"
	ProfileYouTubeOutput ProfileKind = "youtube_output"
)

type Profile struct {
	ID                          string         `json:"id"`
	Kind                        ProfileKind    `json:"kind"`
	Name                        string         `json:"name"`
	Config                      map[string]any `json:"config"`
	CreatedAt                   time.Time      `json:"created_at"`
	UpdatedAt                   time.Time      `json:"updated_at"`
	YouTubeRelayBindingRevision uint64         `json:"-"`
}

type ProfileStore interface {
	ListProfiles(ctx context.Context, kind ProfileKind) ([]Profile, error)
	CreateProfile(ctx context.Context, kind ProfileKind, name string, config map[string]any) (Profile, error)
	GetProfile(ctx context.Context, kind ProfileKind, id string) (Profile, error)
	UpdateProfile(ctx context.Context, kind ProfileKind, id, name string, config map[string]any) (Profile, error)
	DeleteProfile(ctx context.Context, kind ProfileKind, id string) error
}

var ErrProfileRawSecretConfig = errors.New("profile config must reference secrets by name and must not contain raw secret values")

type MemoryProfileStore struct {
	mu                       sync.Mutex
	profiles                 map[string]Profile
	relayBindingClaimStreams *MemoryStreamStore
}

func NewMemoryProfileStore() *MemoryProfileStore {
	return &MemoryProfileStore{profiles: map[string]Profile{}}
}

// BindStreamYouTubeRelayBindingClaims gives a memory ProfileStore the same
// output-profile fence as MariaDB. Bind it during test/server construction
// before handling requests; the shared lock serializes output mutation with
// relay-static reservation.
func (s *MemoryProfileStore) BindStreamYouTubeRelayBindingClaims(streams *MemoryStreamStore) {
	if streams == nil {
		s.mu.Lock()
		s.relayBindingClaimStreams = nil
		s.mu.Unlock()
		return
	}
	streams.youtubeRelayBindingOutputMu.Lock()
	defer streams.youtubeRelayBindingOutputMu.Unlock()
	s.mu.Lock()
	s.relayBindingClaimStreams = streams
	s.mu.Unlock()
	streams.mu.Lock()
	streams.relayBindingClaimProfiles = s
	streams.mu.Unlock()
}

func (s *MemoryProfileStore) ListProfiles(ctx context.Context, kind ProfileKind) ([]Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateProfileKind(kind); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Profile, 0)
	for _, profile := range s.profiles {
		if profile.Kind == kind {
			items = append(items, copyProfile(profile))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *MemoryProfileStore) CreateProfile(ctx context.Context, kind ProfileKind, name string, config map[string]any) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validateProfileInput(kind, name, config); err != nil {
		return Profile{}, err
	}
	now := time.Now().UTC()
	profile := Profile{ID: newUUID(), Kind: kind, Name: strings.TrimSpace(name), Config: copyConfig(config), CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.profiles {
		if existing.Kind == kind && existing.Name == profile.Name {
			return Profile{}, errors.New("profile name already exists")
		}
	}
	s.profiles[profile.ID] = profile
	return copyProfile(profile), nil
}

func (s *MemoryProfileStore) GetProfile(ctx context.Context, kind ProfileKind, id string) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validateProfileKind(kind); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || profile.Kind != kind {
		return Profile{}, ErrNotFound
	}
	return copyProfile(profile), nil
}

func (s *MemoryProfileStore) UpdateProfile(ctx context.Context, kind ProfileKind, id, name string, config map[string]any) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validateProfileInput(kind, name, config); err != nil {
		return Profile{}, err
	}
	unlockRelayBindingOutput := s.lockRelayBindingOutputMutation(kind)
	defer unlockRelayBindingOutput()
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || profile.Kind != kind {
		return Profile{}, ErrNotFound
	}
	if err := s.requireUnclaimedYouTubeOutputLocked(ctx, kind, id); err != nil {
		return Profile{}, err
	}
	for existingID, existing := range s.profiles {
		if existingID != id && existing.Kind == kind && existing.Name == strings.TrimSpace(name) {
			return Profile{}, errors.New("profile name already exists")
		}
	}
	profile.Name = strings.TrimSpace(name)
	profile.Config = copyConfig(config)
	if kind == ProfileYouTubeOutput {
		profile.YouTubeRelayBindingRevision++
	}
	profile.UpdatedAt = time.Now().UTC()
	s.profiles[id] = profile
	return copyProfile(profile), nil
}

func (s *MemoryProfileStore) DeleteProfile(ctx context.Context, kind ProfileKind, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateProfileKind(kind); err != nil {
		return err
	}
	unlockRelayBindingOutput := s.lockRelayBindingOutputMutation(kind)
	defer unlockRelayBindingOutput()
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || profile.Kind != kind {
		return ErrNotFound
	}
	if err := s.requireUnclaimedYouTubeOutputLocked(ctx, kind, id); err != nil {
		return err
	}
	delete(s.profiles, id)
	return nil
}

func (s *MemoryProfileStore) lockRelayBindingOutputMutation(kind ProfileKind) func() {
	if kind != ProfileYouTubeOutput {
		return func() {}
	}
	s.mu.Lock()
	streams := s.relayBindingClaimStreams
	s.mu.Unlock()
	if streams == nil {
		return func() {}
	}
	streams.youtubeRelayBindingOutputMu.Lock()
	return streams.youtubeRelayBindingOutputMu.Unlock
}

func (s *MemoryProfileStore) requireUnclaimedYouTubeOutputLocked(ctx context.Context, kind ProfileKind, id string) error {
	if kind != ProfileYouTubeOutput || s.relayBindingClaimStreams == nil {
		return nil
	}
	hasClaim, err := s.relayBindingClaimStreams.HasStreamYouTubeRelayBindingClaimForOutput(ctx, id)
	if err != nil {
		return err
	}
	if hasClaim {
		return ErrYouTubeRelayBindingClaimActive
	}
	return nil
}

type MariaDBProfileStore struct {
	db *sql.DB
}

func NewMariaDBProfileStore(db *sql.DB) MariaDBProfileStore {
	return MariaDBProfileStore{db: db}
}

func (s MariaDBProfileStore) ListProfiles(ctx context.Context, kind ProfileKind) ([]Profile, error) {
	if err := validateProfileKind(kind); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, config, created_at, updated_at, youtube_relay_binding_revision FROM profiles WHERE kind = ? ORDER BY name`, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Profile
	for rows.Next() {
		profile, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, profile)
	}
	return items, rows.Err()
}

func (s MariaDBProfileStore) CreateProfile(ctx context.Context, kind ProfileKind, name string, config map[string]any) (Profile, error) {
	if err := validateProfileInput(kind, name, config); err != nil {
		return Profile{}, err
	}
	body, err := json.Marshal(config)
	if err != nil {
		return Profile{}, err
	}
	now := time.Now().UTC()
	profile := Profile{ID: newUUID(), Kind: kind, Name: strings.TrimSpace(name), Config: copyConfig(config), CreatedAt: now, UpdatedAt: now}
	_, err = s.db.ExecContext(ctx, `INSERT INTO profiles (id, kind, name, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, profile.ID, string(profile.Kind), profile.Name, string(body), profile.CreatedAt, profile.UpdatedAt)
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s MariaDBProfileStore) GetProfile(ctx context.Context, kind ProfileKind, id string) (Profile, error) {
	if err := validateProfileKind(kind); err != nil {
		return Profile{}, err
	}
	profile, err := scanProfile(s.db.QueryRowContext(ctx, `SELECT id, kind, name, config, created_at, updated_at, youtube_relay_binding_revision FROM profiles WHERE id = ? AND kind = ?`, id, string(kind)))
	if err == sql.ErrNoRows {
		return Profile{}, ErrNotFound
	}
	return profile, err
}

func (s MariaDBProfileStore) UpdateProfile(ctx context.Context, kind ProfileKind, id, name string, config map[string]any) (Profile, error) {
	if err := validateProfileInput(kind, name, config); err != nil {
		return Profile{}, err
	}
	body, err := json.Marshal(config)
	if err != nil {
		return Profile{}, err
	}
	query := `UPDATE profiles SET name = ?, config = ?, updated_at = ? WHERE id = ? AND kind = ?`
	if kind == ProfileYouTubeOutput {
		query = `UPDATE profiles
SET name = ?, config = ?, updated_at = ?, youtube_relay_binding_revision = youtube_relay_binding_revision + 1
WHERE id = ? AND kind = ?`
	}
	result, err := s.db.ExecContext(ctx, query, strings.TrimSpace(name), string(body), time.Now().UTC(), id, string(kind))
	if err != nil {
		if isYouTubeRelayBindingClaimProfileConstraintError(err) {
			return Profile{}, ErrYouTubeRelayBindingClaimActive
		}
		return Profile{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Profile{}, err
	}
	if affected == 0 {
		return Profile{}, ErrNotFound
	}
	return s.GetProfile(ctx, kind, id)
}

func (s MariaDBProfileStore) DeleteProfile(ctx context.Context, kind ProfileKind, id string) error {
	if err := validateProfileKind(kind); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id = ? AND kind = ?`, id, string(kind))
	if err != nil {
		if isYouTubeRelayBindingClaimProfileConstraintError(err) {
			return ErrYouTubeRelayBindingClaimActive
		}
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type profileScanner interface {
	Scan(dest ...any) error
}

func scanProfile(row profileScanner) (Profile, error) {
	var profile Profile
	var kind string
	var config string
	if err := row.Scan(&profile.ID, &kind, &profile.Name, &config, &profile.CreatedAt, &profile.UpdatedAt, &profile.YouTubeRelayBindingRevision); err != nil {
		return Profile{}, err
	}
	profile.Kind = ProfileKind(kind)
	_ = json.Unmarshal([]byte(config), &profile.Config)
	if profile.Config == nil {
		profile.Config = map[string]any{}
	}
	return profile, nil
}

func validateProfileInput(kind ProfileKind, name string, config map[string]any) error {
	if err := validateProfileKind(kind); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("profile name is required")
	}
	if config == nil {
		return errors.New("profile config is required")
	}
	if containsRawSecretConfig("", config) {
		return ErrProfileRawSecretConfig
	}
	return nil
}

func validateProfileKind(kind ProfileKind) error {
	switch kind {
	case ProfileEncoder, ProfileArchive, ProfileCaption, ProfileOverlay, ProfileDiscordConfig, ProfileYouTubeOutput:
		return nil
	default:
		return errors.New("invalid profile kind")
	}
}

func copyProfile(profile Profile) Profile {
	profile.Config = copyConfig(profile.Config)
	return profile
}

func copyConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		out[key] = value
	}
	return out
}

func containsRawSecretConfig(path string, value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			nestedPath := key
			if path != "" {
				nestedPath = path + "." + key
			}
			if secretLikeKeyRequiresWriteOnly(key) {
				return true
			}
			if containsRawSecretConfig(nestedPath, nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsRawSecretConfig(path, nested) {
				return true
			}
		}
	case string:
		if secretLikeValue(typed) {
			return true
		}
	}
	return false
}

func secretLikeKeyRequiresWriteOnly(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	allowedReferenceSuffixes := []string{"_secret_name", "_secret_ref", "_secret_id", "_secret_status", "_configured", "_fingerprint"}
	for _, suffix := range allowedReferenceSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return false
		}
	}
	canonical := canonicalSecretKey(normalized)
	allowedCanonicalSuffixes := []string{"secretname", "secretref", "secretid", "secretstatus", "configured", "fingerprint"}
	for _, suffix := range allowedCanonicalSuffixes {
		if strings.HasSuffix(canonical, suffix) {
			return false
		}
	}
	secretKeyTokens := []string{"password", "passwd", "token", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "client_secret", "refresh_token", "access_token", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"}
	for _, token := range secretKeyTokens {
		if strings.Contains(normalized, token) || strings.Contains(canonical, canonicalSecretKey(token)) {
			return true
		}
	}
	return false
}

func canonicalSecretKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func secretLikeValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	sensitivePatterns := []string{
		"bearer ",
		"authorization:",
		"password=",
		"token=",
		"access_token=",
		"refresh_token=",
		"discord.com/api/webhooks/",
		"hooks.slack.com/services/",
		"-----begin private key-----",
	}
	for _, pattern := range sensitivePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.User != nil {
		return true
	}
	return false
}
