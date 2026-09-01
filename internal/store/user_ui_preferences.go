package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidThemeID   = errors.New("invalid_theme_id")
	ErrInvalidColorMode = errors.New("invalid_color_mode")
	ErrRevisionConflict = errors.New("revision_conflict")
)

var UserThemeIDs = []string{
	"autostream", "slate", "ocean", "cyan", "indigo", "violet",
	"magenta", "rose", "crimson", "amber", "emerald", "monochrome",
}

var UserColorModes = []string{"system", "light", "dark"}

type UserUIPreference struct {
	UserID    string    `json:"-"`
	ThemeID   string    `json:"theme_id"`
	ColorMode string    `json:"color_mode"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Fallback  bool      `json:"fallback,omitempty"`
}

type UserUIPreferenceStore interface {
	GetUserUIPreference(ctx context.Context, userID string) (UserUIPreference, error)
	UpdateUserUIPreference(ctx context.Context, userID, themeID, colorMode string, expectedRevision uint64) (UserUIPreference, error)
}

func ValidateUserUIPreference(themeID, colorMode string) error {
	themeID, colorMode = strings.TrimSpace(themeID), strings.TrimSpace(colorMode)
	if !stringInSet(themeID, UserThemeIDs) {
		return ErrInvalidThemeID
	}
	if !stringInSet(colorMode, UserColorModes) {
		return ErrInvalidColorMode
	}
	return nil
}

func SafeUserUIPreference(preference UserUIPreference) UserUIPreference {
	if ValidateUserUIPreference(preference.ThemeID, preference.ColorMode) == nil {
		return preference
	}
	preference.ThemeID = "autostream"
	preference.ColorMode = "system"
	preference.Fallback = true
	return preference
}

func stringInSet(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

type MemoryUserUIPreferenceStore struct {
	mu          sync.Mutex
	preferences map[string]UserUIPreference
	now         func() time.Time
}

func NewMemoryUserUIPreferenceStore() *MemoryUserUIPreferenceStore {
	return &MemoryUserUIPreferenceStore{preferences: map[string]UserUIPreference{}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *MemoryUserUIPreferenceStore) GetUserUIPreference(_ context.Context, userID string) (UserUIPreference, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserUIPreference{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if preference, ok := s.preferences[userID]; ok {
		return preference, nil
	}
	return UserUIPreference{UserID: userID, ThemeID: "autostream", ColorMode: "system", Revision: 0}, nil
}

func (s *MemoryUserUIPreferenceStore) UpdateUserUIPreference(_ context.Context, userID, themeID, colorMode string, expectedRevision uint64) (UserUIPreference, error) {
	userID = strings.TrimSpace(userID)
	themeID = strings.TrimSpace(themeID)
	colorMode = strings.TrimSpace(colorMode)
	if userID == "" {
		return UserUIPreference{}, ErrNotFound
	}
	if err := ValidateUserUIPreference(themeID, colorMode); err != nil {
		return UserUIPreference{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.preferences[userID]
	if !ok {
		current = UserUIPreference{UserID: userID, Revision: 0}
	}
	if current.Revision != expectedRevision {
		return UserUIPreference{}, ErrRevisionConflict
	}
	current.ThemeID = themeID
	current.ColorMode = colorMode
	current.Revision++
	current.UpdatedAt = s.now()
	s.preferences[userID] = current
	return current, nil
}

type MariaDBUserUIPreferenceStore struct{ db *sql.DB }

func NewMariaDBUserUIPreferenceStore(db *sql.DB) MariaDBUserUIPreferenceStore {
	return MariaDBUserUIPreferenceStore{db: db}
}

func (s MariaDBUserUIPreferenceStore) GetUserUIPreference(ctx context.Context, userID string) (UserUIPreference, error) {
	userID = strings.TrimSpace(userID)
	var preference UserUIPreference
	preference.UserID = userID
	err := s.db.QueryRowContext(ctx, `SELECT theme_id,color_mode,revision,updated_at FROM user_ui_preferences WHERE user_id=?`, userID).Scan(&preference.ThemeID, &preference.ColorMode, &preference.Revision, &preference.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserUIPreference{UserID: userID, ThemeID: "autostream", ColorMode: "system", Revision: 0}, nil
	}
	return preference, err
}

func (s MariaDBUserUIPreferenceStore) UpdateUserUIPreference(ctx context.Context, userID, themeID, colorMode string, expectedRevision uint64) (UserUIPreference, error) {
	userID = strings.TrimSpace(userID)
	themeID = strings.TrimSpace(themeID)
	colorMode = strings.TrimSpace(colorMode)
	if userID == "" {
		return UserUIPreference{}, ErrNotFound
	}
	if err := ValidateUserUIPreference(themeID, colorMode); err != nil {
		return UserUIPreference{}, err
	}
	if expectedRevision == ^uint64(0) {
		return UserUIPreference{}, ErrRevisionConflict
	}
	now := time.Now().UTC()
	next := expectedRevision + 1
	if expectedRevision == 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO user_ui_preferences (user_id,theme_id,color_mode,revision,updated_at) VALUES (?,?,?,?,?)`, userID, themeID, colorMode, next, now); err != nil {
			if isDuplicateKeyError(err) {
				return UserUIPreference{}, ErrRevisionConflict
			}
			return UserUIPreference{}, err
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE user_ui_preferences SET theme_id=?,color_mode=?,revision=?,updated_at=? WHERE user_id=? AND revision=?`, themeID, colorMode, next, now, userID, expectedRevision)
		if err != nil {
			return UserUIPreference{}, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return UserUIPreference{}, ErrRevisionConflict
		}
	}
	return UserUIPreference{UserID: userID, ThemeID: themeID, ColorMode: colorMode, Revision: next, UpdatedAt: now}, nil
}

var _ UserUIPreferenceStore = (*MemoryUserUIPreferenceStore)(nil)
var _ UserUIPreferenceStore = MariaDBUserUIPreferenceStore{}
