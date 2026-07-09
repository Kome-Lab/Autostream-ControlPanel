package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type OAuthLoginState struct {
	StateToken      string    `json:"-"`
	StateHash       string    `json:"-"`
	Purpose         string    `json:"purpose,omitempty"`
	ProviderID      string    `json:"provider_id"`
	ProviderType    string    `json:"provider_type"`
	Nonce           string    `json:"nonce"`
	RedirectAfter   string    `json:"redirect_after,omitempty"`
	RequestedScopes []string  `json:"requested_scopes,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type OAuthUserLink struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	ProviderID   string    `json:"provider_id"`
	ProviderType string    `json:"provider_type"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type OAuthLoginStore interface {
	CreateOAuthLoginState(ctx context.Context, state OAuthLoginState, ttl time.Duration) (OAuthLoginState, error)
	GetOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error)
	ConsumeOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error)
	LinkOAuthUser(ctx context.Context, link OAuthUserLink) (OAuthUserLink, error)
	FindOAuthUserLink(ctx context.Context, providerID, subject string) (OAuthUserLink, error)
	ListOAuthUserLinks(ctx context.Context, userID string) ([]OAuthUserLink, error)
	DeleteOAuthUserLink(ctx context.Context, id, userID string) error
}

type MemoryOAuthLoginStore struct {
	mu     sync.Mutex
	states map[string]OAuthLoginState
	links  map[string]OAuthUserLink
}

func NewMemoryOAuthLoginStore() *MemoryOAuthLoginStore {
	return &MemoryOAuthLoginStore{states: map[string]OAuthLoginState{}, links: map[string]OAuthUserLink{}}
}

func (s *MemoryOAuthLoginStore) CreateOAuthLoginState(ctx context.Context, state OAuthLoginState, ttl time.Duration) (OAuthLoginState, error) {
	if err := ctx.Err(); err != nil {
		return OAuthLoginState{}, err
	}
	state.ProviderID = strings.TrimSpace(state.ProviderID)
	state.ProviderType = strings.TrimSpace(state.ProviderType)
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	state.RequestedScopes = cleanStringSlice(state.RequestedScopes)
	if state.ProviderID == "" || state.ProviderType == "" {
		return OAuthLoginState{}, errors.New("oauth state provider is required")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return OAuthLoginState{}, err
	}
	nonce, err := security.RandomToken(24)
	if err != nil {
		return OAuthLoginState{}, err
	}
	now := time.Now().UTC()
	state.StateToken = "ast_oauth_" + raw
	state.StateHash = security.HashToken(state.StateToken)
	state.Nonce = nonce
	state.CreatedAt = now
	state.ExpiresAt = now.Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state.StateHash] = state
	return state, nil
}

func (s *MemoryOAuthLoginStore) ConsumeOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error) {
	if err := ctx.Err(); err != nil {
		return OAuthLoginState{}, err
	}
	hash := security.HashToken(strings.TrimSpace(stateToken))
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[hash]
	if !ok {
		return OAuthLoginState{}, ErrNotFound
	}
	delete(s.states, hash)
	if time.Now().UTC().After(state.ExpiresAt) {
		return OAuthLoginState{}, ErrNotFound
	}
	state.StateToken = stateToken
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	return state, nil
}

func (s *MemoryOAuthLoginStore) GetOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error) {
	if err := ctx.Err(); err != nil {
		return OAuthLoginState{}, err
	}
	hash := security.HashToken(strings.TrimSpace(stateToken))
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[hash]
	if !ok || time.Now().UTC().After(state.ExpiresAt) {
		return OAuthLoginState{}, ErrNotFound
	}
	state.StateToken = stateToken
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	return state, nil
}

func (s *MemoryOAuthLoginStore) LinkOAuthUser(ctx context.Context, link OAuthUserLink) (OAuthUserLink, error) {
	if err := ctx.Err(); err != nil {
		return OAuthUserLink{}, err
	}
	link, err := normalizeOAuthUserLink(link)
	if err != nil {
		return OAuthUserLink{}, err
	}
	now := time.Now().UTC()
	if link.ID == "" {
		link.ID = newUUID()
	}
	link.CreatedAt = now
	link.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.links {
		if existing.ProviderID == link.ProviderID && existing.Subject == link.Subject && existing.ID != link.ID {
			return OAuthUserLink{}, errors.New("oauth user link already exists")
		}
	}
	s.links[link.ID] = link
	return link, nil
}

func (s *MemoryOAuthLoginStore) FindOAuthUserLink(ctx context.Context, providerID, subject string) (OAuthUserLink, error) {
	if err := ctx.Err(); err != nil {
		return OAuthUserLink{}, err
	}
	providerID = strings.TrimSpace(providerID)
	subject = strings.TrimSpace(subject)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, link := range s.links {
		if link.ProviderID == providerID && link.Subject == subject {
			return link, nil
		}
	}
	return OAuthUserLink{}, ErrNotFound
}

func (s *MemoryOAuthLoginStore) ListOAuthUserLinks(ctx context.Context, userID string) ([]OAuthUserLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []OAuthUserLink{}
	for _, link := range s.links {
		if link.UserID == userID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (s *MemoryOAuthLoginStore) DeleteOAuthUserLink(ctx context.Context, id, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	userID = strings.TrimSpace(userID)
	s.mu.Lock()
	defer s.mu.Unlock()
	link, ok := s.links[id]
	if !ok || link.UserID != userID {
		return ErrNotFound
	}
	delete(s.links, id)
	return nil
}

type MariaDBOAuthLoginStore struct {
	db *sql.DB
}

func NewMariaDBOAuthLoginStore(db *sql.DB) MariaDBOAuthLoginStore {
	return MariaDBOAuthLoginStore{db: db}
}

func (s MariaDBOAuthLoginStore) CreateOAuthLoginState(ctx context.Context, state OAuthLoginState, ttl time.Duration) (OAuthLoginState, error) {
	state.ProviderID = strings.TrimSpace(state.ProviderID)
	state.ProviderType = strings.TrimSpace(state.ProviderType)
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	state.RequestedScopes = cleanStringSlice(state.RequestedScopes)
	if state.ProviderID == "" || state.ProviderType == "" {
		return OAuthLoginState{}, errors.New("oauth state provider is required")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return OAuthLoginState{}, err
	}
	nonce, err := security.RandomToken(24)
	if err != nil {
		return OAuthLoginState{}, err
	}
	now := time.Now().UTC()
	state.StateToken = "ast_oauth_" + raw
	state.StateHash = security.HashToken(state.StateToken)
	state.Nonce = nonce
	state.CreatedAt = now
	state.ExpiresAt = now.Add(ttl)
	requestedScopes, err := marshalStringSlice(state.RequestedScopes)
	if err != nil {
		return OAuthLoginState{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_login_states (state_hash, provider_id, provider_type, purpose, nonce, redirect_after, requested_scopes, expires_at, created_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, '[]'), ?, ?)`, state.StateHash, state.ProviderID, state.ProviderType, state.Purpose, state.Nonce, state.RedirectAfter, requestedScopes, state.ExpiresAt, state.CreatedAt)
	if err != nil {
		return OAuthLoginState{}, err
	}
	return state, nil
}

func (s MariaDBOAuthLoginStore) ConsumeOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error) {
	hash := security.HashToken(strings.TrimSpace(stateToken))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthLoginState{}, err
	}
	defer tx.Rollback()
	var state OAuthLoginState
	var redirectAfter, requestedScopes sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state_hash, provider_id, provider_type, purpose, nonce, redirect_after, requested_scopes, expires_at, created_at FROM oauth_login_states WHERE state_hash = ? FOR UPDATE`, hash).Scan(&state.StateHash, &state.ProviderID, &state.ProviderType, &state.Purpose, &state.Nonce, &redirectAfter, &requestedScopes, &state.ExpiresAt, &state.CreatedAt)
	if err == sql.ErrNoRows {
		return OAuthLoginState{}, ErrNotFound
	}
	if err != nil {
		return OAuthLoginState{}, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM oauth_login_states WHERE state_hash = ?`, hash)
	if err != nil {
		return OAuthLoginState{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthLoginState{}, err
	}
	if time.Now().UTC().After(state.ExpiresAt) {
		return OAuthLoginState{}, ErrNotFound
	}
	state.StateToken = stateToken
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	state.RedirectAfter = redirectAfter.String
	if requestedScopes.Valid {
		_ = json.Unmarshal([]byte(requestedScopes.String), &state.RequestedScopes)
		state.RequestedScopes = cleanStringSlice(state.RequestedScopes)
	}
	return state, nil
}

func (s MariaDBOAuthLoginStore) GetOAuthLoginState(ctx context.Context, stateToken string) (OAuthLoginState, error) {
	hash := security.HashToken(strings.TrimSpace(stateToken))
	var state OAuthLoginState
	var redirectAfter, requestedScopes sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT state_hash, provider_id, provider_type, purpose, nonce, redirect_after, requested_scopes, expires_at, created_at FROM oauth_login_states WHERE state_hash = ?`, hash).Scan(&state.StateHash, &state.ProviderID, &state.ProviderType, &state.Purpose, &state.Nonce, &redirectAfter, &requestedScopes, &state.ExpiresAt, &state.CreatedAt)
	if err == sql.ErrNoRows {
		return OAuthLoginState{}, ErrNotFound
	}
	if err != nil {
		return OAuthLoginState{}, err
	}
	if time.Now().UTC().After(state.ExpiresAt) {
		return OAuthLoginState{}, ErrNotFound
	}
	state.StateToken = stateToken
	state.Purpose = normalizeOAuthStatePurpose(state.Purpose)
	state.RedirectAfter = redirectAfter.String
	if requestedScopes.Valid {
		_ = json.Unmarshal([]byte(requestedScopes.String), &state.RequestedScopes)
		state.RequestedScopes = cleanStringSlice(state.RequestedScopes)
	}
	return state, nil
}

func (s MariaDBOAuthLoginStore) LinkOAuthUser(ctx context.Context, link OAuthUserLink) (OAuthUserLink, error) {
	link, err := normalizeOAuthUserLink(link)
	if err != nil {
		return OAuthUserLink{}, err
	}
	now := time.Now().UTC()
	if link.ID == "" {
		link.ID = newUUID()
	}
	link.CreatedAt = now
	link.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_user_links (id, user_id, provider_id, provider_type, subject, email, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`, link.ID, link.UserID, link.ProviderID, link.ProviderType, link.Subject, link.Email, link.CreatedAt, link.UpdatedAt)
	if err != nil {
		return OAuthUserLink{}, err
	}
	return link, nil
}

func (s MariaDBOAuthLoginStore) FindOAuthUserLink(ctx context.Context, providerID, subject string) (OAuthUserLink, error) {
	var link OAuthUserLink
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, provider_id, provider_type, subject, email, created_at, updated_at FROM oauth_user_links WHERE provider_id = ? AND subject = ?`, strings.TrimSpace(providerID), strings.TrimSpace(subject)).Scan(&link.ID, &link.UserID, &link.ProviderID, &link.ProviderType, &link.Subject, &email, &link.CreatedAt, &link.UpdatedAt)
	if err == sql.ErrNoRows {
		return OAuthUserLink{}, ErrNotFound
	}
	if err != nil {
		return OAuthUserLink{}, err
	}
	link.Email = email.String
	return link, nil
}

func (s MariaDBOAuthLoginStore) ListOAuthUserLinks(ctx context.Context, userID string) ([]OAuthUserLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, provider_id, provider_type, subject, email, created_at, updated_at FROM oauth_user_links WHERE user_id = ? ORDER BY provider_type, email, subject`, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OAuthUserLink
	for rows.Next() {
		var link OAuthUserLink
		var email sql.NullString
		if err := rows.Scan(&link.ID, &link.UserID, &link.ProviderID, &link.ProviderType, &link.Subject, &email, &link.CreatedAt, &link.UpdatedAt); err != nil {
			return nil, err
		}
		link.Email = email.String
		out = append(out, link)
	}
	return out, rows.Err()
}

func (s MariaDBOAuthLoginStore) DeleteOAuthUserLink(ctx context.Context, id, userID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM oauth_user_links WHERE id = ? AND user_id = ?`, strings.TrimSpace(id), strings.TrimSpace(userID))
	if err != nil {
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

func normalizeOAuthUserLink(link OAuthUserLink) (OAuthUserLink, error) {
	link.UserID = strings.TrimSpace(link.UserID)
	link.ProviderID = strings.TrimSpace(link.ProviderID)
	link.ProviderType = strings.TrimSpace(link.ProviderType)
	link.Subject = strings.TrimSpace(link.Subject)
	link.Email = strings.TrimSpace(link.Email)
	if link.UserID == "" || link.ProviderID == "" || link.ProviderType == "" || link.Subject == "" {
		return OAuthUserLink{}, errors.New("oauth user link user, provider, and subject are required")
	}
	if !validOAuthLoginProviderType(link.ProviderType) {
		return OAuthUserLink{}, errors.New("invalid oauth provider type")
	}
	return link, nil
}

func validOAuthLoginProviderType(providerType string) bool {
	switch strings.TrimSpace(strings.ToLower(providerType)) {
	case "google", "github", "discord":
		return true
	default:
		return false
	}
}

func normalizeOAuthStatePurpose(value string) string {
	switch strings.TrimSpace(value) {
	case "account_link", "connected_account":
		return strings.TrimSpace(value)
	default:
		return "login"
	}
}
