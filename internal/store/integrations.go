package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type OAuthProvider struct {
	ID                     string   `json:"id"`
	ProviderType           string   `json:"provider_type"`
	Name                   string   `json:"name"`
	Enabled                bool     `json:"enabled"`
	ClientID               string   `json:"client_id"`
	ClientSecret           string   `json:"-"`
	ClientSecretConfigured bool     `json:"client_secret_configured"`
	Scopes                 []string `json:"scopes"`
	AllowedDomains         []string `json:"allowed_domains"`
	AutoProvision          bool     `json:"auto_provision"`
	DefaultRoleIDs         []string `json:"default_role_ids,omitempty"`
	RedirectURI            string   `json:"redirect_uri"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              string   `json:"updated_at"`
}

type OAuthAccount struct {
	ID                               string   `json:"id"`
	ProviderID                       string   `json:"provider_id"`
	ProviderType                     string   `json:"provider_type"`
	ProviderName                     string   `json:"provider_name,omitempty"`
	AccountLabel                     string   `json:"account_label"`
	AccountPurpose                   string   `json:"account_purpose"`
	DisplayName                      string   `json:"display_name,omitempty"`
	Subject                          string   `json:"subject,omitempty"`
	Email                            string   `json:"email,omitempty"`
	Scopes                           []string `json:"scopes"`
	RefreshToken                     string   `json:"-"`
	RefreshTokenConfigured           bool     `json:"refresh_token_configured"`
	TokenFingerprint                 string   `json:"token_fingerprint,omitempty"`
	TokenRevision                    uint64   `json:"-"`
	RefreshTokenUpdatedAt            string   `json:"refresh_token_updated_at"`
	AccessTokenRefreshedAt           string   `json:"access_token_refreshed_at"`
	AccessTokenRefreshAttemptedAt    string   `json:"access_token_refresh_attempted_at"`
	AccessTokenRefreshFailedAt       string   `json:"access_token_refresh_failed_at"`
	AccessTokenRefreshFailureCode    string   `json:"access_token_refresh_failure_code"`
	AccessTokenRefreshRelinkRequired bool     `json:"access_token_refresh_relink_required"`
	CreatedAt                        string   `json:"created_at"`
	UpdatedAt                        string   `json:"updated_at"`
}

// OAuthTokenRefreshFailureCode is intentionally a bounded operational class.
// It must never be populated from a provider error string because those can
// include tokens or other provider-side detail.
const (
	OAuthTokenRefreshFailureUnknown                    = "unknown"
	OAuthTokenRefreshFailureCredentialsUnavailable     = "credentials_unavailable"
	OAuthTokenRefreshFailureProviderNotReady           = "provider_not_ready"
	OAuthTokenRefreshFailureProviderUnavailable        = "provider_unavailable"
	OAuthTokenRefreshFailureProviderCredentialsInvalid = "provider_credentials_invalid"
	OAuthTokenRefreshFailureReauthorizationRequired    = "reauthorization_required"
	OAuthTokenRefreshFailureTimedOut                   = "timeout"
	OAuthTokenRefreshFailureInvalidResponse            = "invalid_response"
)

type DriveDestination struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	AuthMode            string `json:"auth_mode"`
	OAuthAccountID      string `json:"oauth_account_id,omitempty"`
	FolderID            string `json:"-"`
	FolderIDConfigured  bool   `json:"folder_id_configured"`
	FolderIDFingerprint string `json:"folder_id_fingerprint,omitempty"`
	MaskedFolderID      string `json:"masked_folder_id,omitempty"`
	SharedDrive         bool   `json:"shared_drive"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

type IntegrationStore interface {
	ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error)
	CreateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error)
	GetOAuthProvider(ctx context.Context, id string) (OAuthProvider, error)
	GetOAuthProviderForDispatch(ctx context.Context, id string) (OAuthProvider, error)
	UpdateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error)
	DeleteOAuthProvider(ctx context.Context, id string) error

	ListOAuthAccounts(ctx context.Context) ([]OAuthAccount, error)
	CreateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error)
	GetOAuthAccount(ctx context.Context, id string) (OAuthAccount, error)
	GetOAuthAccountForDispatch(ctx context.Context, id string) (OAuthAccount, error)
	UpdateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error)
	RecordOAuthAccountTokenRefreshAttempt(ctx context.Context, id string, expectedTokenRevision uint64, attemptedAt time.Time) (OAuthAccount, error)
	RecordOAuthAccountTokenRefreshFailure(ctx context.Context, id string, expectedTokenRevision uint64, failureCode string, relinkRequired bool, failedAt time.Time) (OAuthAccount, error)
	RecordOAuthAccountTokenRefresh(ctx context.Context, id string, expectedTokenRevision uint64, rotatedRefreshToken string, refreshedAt time.Time) (OAuthAccount, error)
	DeleteOAuthAccount(ctx context.Context, id string) error

	ListDriveDestinations(ctx context.Context) ([]DriveDestination, error)
	CreateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error)
	GetDriveDestination(ctx context.Context, id string) (DriveDestination, error)
	GetDriveDestinationForDispatch(ctx context.Context, id string) (DriveDestination, error)
	UpdateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error)
	DeleteDriveDestination(ctx context.Context, id string) error
}

type MemoryIntegrationStore struct {
	mu           sync.Mutex
	providers    map[string]OAuthProvider
	accounts     map[string]OAuthAccount
	destinations map[string]DriveDestination
}

func NewMemoryIntegrationStore() *MemoryIntegrationStore {
	return &MemoryIntegrationStore{
		providers:    map[string]OAuthProvider{},
		accounts:     map[string]OAuthAccount{},
		destinations: map[string]DriveDestination{},
	}
}

func (s *MemoryIntegrationStore) ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OAuthProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		out = append(out, publicOAuthProvider(provider))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryIntegrationStore) CreateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	provider, err := normalizeOAuthProvider(provider, true)
	if err != nil {
		return OAuthProvider{}, err
	}
	provider.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	provider.CreatedAt, provider.UpdatedAt = now, now
	provider.ClientSecretConfigured = provider.ClientSecret != ""
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.providers {
		if strings.EqualFold(existing.Name, provider.Name) {
			return OAuthProvider{}, errors.New("oauth provider name already exists")
		}
	}
	s.providers[provider.ID] = provider
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) GetOAuthProvider(ctx context.Context, id string) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) GetOAuthProviderForDispatch(ctx context.Context, id string) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	return provider, nil
}

func (s *MemoryIntegrationStore) UpdateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	if err := ctx.Err(); err != nil {
		return OAuthProvider{}, err
	}
	provider, err := normalizeOAuthProvider(provider, false)
	if err != nil {
		return OAuthProvider{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.providers[provider.ID]
	if !ok {
		return OAuthProvider{}, ErrNotFound
	}
	for id, item := range s.providers {
		if id != provider.ID && strings.EqualFold(item.Name, provider.Name) {
			return OAuthProvider{}, errors.New("oauth provider name already exists")
		}
	}
	if provider.ClientSecret == "" {
		provider.ClientSecret = existing.ClientSecret
		provider.ClientSecretConfigured = existing.ClientSecretConfigured
	} else {
		provider.ClientSecretConfigured = true
	}
	provider.CreatedAt = existing.CreatedAt
	provider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.providers[provider.ID] = provider
	return publicOAuthProvider(provider), nil
}

func (s *MemoryIntegrationStore) DeleteOAuthProvider(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return ErrNotFound
	}
	delete(s.providers, id)
	return nil
}

func (s *MemoryIntegrationStore) ListOAuthAccounts(ctx context.Context) ([]OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OAuthAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		out = append(out, s.publicOAuthAccountLocked(account))
	}
	sortOAuthAccounts(out)
	return out, nil
}

func (s *MemoryIntegrationStore) CreateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	account, err := normalizeOAuthAccount(account, true)
	if err != nil {
		return OAuthAccount{}, err
	}
	account.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	account.CreatedAt, account.UpdatedAt = now, now
	account.RefreshTokenConfigured = account.RefreshToken != ""
	account.TokenRevision = 1
	if account.RefreshToken != "" {
		account.TokenFingerprint = security.SecretFingerprint(account.RefreshToken)
		account.RefreshTokenUpdatedAt = now
	}
	s.mu.Lock()
	s.accounts[account.ID] = account
	response := s.publicOAuthAccountLocked(account)
	s.mu.Unlock()
	return response, nil
}

func (s *MemoryIntegrationStore) GetOAuthAccount(ctx context.Context, id string) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) GetOAuthAccountForDispatch(ctx context.Context, id string) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	return account, nil
}

func (s *MemoryIntegrationStore) UpdateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	account, err := normalizeOAuthAccount(account, false)
	if err != nil {
		return OAuthAccount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.accounts[account.ID]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if account.RefreshToken == "" {
		account.RefreshToken = existing.RefreshToken
		account.RefreshTokenConfigured = existing.RefreshTokenConfigured
		account.TokenFingerprint = existing.TokenFingerprint
		account.TokenRevision = existing.TokenRevision
		account.RefreshTokenUpdatedAt = existing.RefreshTokenUpdatedAt
		account.AccessTokenRefreshedAt = existing.AccessTokenRefreshedAt
		account.AccessTokenRefreshAttemptedAt = existing.AccessTokenRefreshAttemptedAt
		account.AccessTokenRefreshFailedAt = existing.AccessTokenRefreshFailedAt
		account.AccessTokenRefreshFailureCode = existing.AccessTokenRefreshFailureCode
		account.AccessTokenRefreshRelinkRequired = existing.AccessTokenRefreshRelinkRequired
	} else {
		account.RefreshTokenConfigured = true
		account.TokenFingerprint = security.SecretFingerprint(account.RefreshToken)
		account.RefreshTokenUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		// A successful re-link replaces the credentials that caused any prior
		// refresh failure. The next automatic refresh records fresh metadata.
		account.AccessTokenRefreshedAt = ""
		account.AccessTokenRefreshAttemptedAt = ""
		account.AccessTokenRefreshFailedAt = ""
		account.AccessTokenRefreshFailureCode = ""
		account.AccessTokenRefreshRelinkRequired = false
		account.TokenRevision = nextOAuthAccountTokenRevision(existing.TokenRevision)
	}
	account.CreatedAt = existing.CreatedAt
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefreshAttempt(ctx context.Context, id string, expectedTokenRevision uint64, attemptedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	attemptedAt = normalizedOAuthRefreshTime(attemptedAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[strings.TrimSpace(id)]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	account.AccessTokenRefreshAttemptedAt = attemptedAt.Format(time.RFC3339)
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefreshFailure(ctx context.Context, id string, expectedTokenRevision uint64, failureCode string, relinkRequired bool, failedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	failedAt = normalizedOAuthRefreshTime(failedAt)
	failureCode, relinkRequired = normalizedOAuthTokenRefreshFailure(failureCode, relinkRequired)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[strings.TrimSpace(id)]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	account.AccessTokenRefreshAttemptedAt = failedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailedAt = failedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailureCode = failureCode
	account.AccessTokenRefreshRelinkRequired = relinkRequired
	s.accounts[account.ID] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) RecordOAuthAccountTokenRefresh(ctx context.Context, id string, expectedTokenRevision uint64, rotatedRefreshToken string, refreshedAt time.Time) (OAuthAccount, error) {
	if err := ctx.Err(); err != nil {
		return OAuthAccount{}, err
	}
	refreshedAt = normalizedOAuthRefreshTime(refreshedAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return OAuthAccount{}, ErrNotFound
	}
	if !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	if rotatedRefreshToken = strings.TrimSpace(rotatedRefreshToken); rotatedRefreshToken != "" && rotatedRefreshToken != account.RefreshToken {
		account.RefreshToken = rotatedRefreshToken
		account.RefreshTokenConfigured = true
		account.TokenFingerprint = security.SecretFingerprint(rotatedRefreshToken)
		account.TokenRevision = nextOAuthAccountTokenRevision(account.TokenRevision)
		account.RefreshTokenUpdatedAt = refreshedAt.Format(time.RFC3339)
	}
	account.AccessTokenRefreshAttemptedAt = refreshedAt.Format(time.RFC3339)
	account.AccessTokenRefreshedAt = refreshedAt.Format(time.RFC3339)
	account.AccessTokenRefreshFailedAt = ""
	account.AccessTokenRefreshFailureCode = ""
	account.AccessTokenRefreshRelinkRequired = false
	s.accounts[id] = account
	return s.publicOAuthAccountLocked(account), nil
}

func (s *MemoryIntegrationStore) publicOAuthAccountLocked(account OAuthAccount) OAuthAccount {
	if provider, ok := s.providers[account.ProviderID]; ok {
		account.ProviderName = provider.Name
	}
	return publicOAuthAccount(account)
}

func (s *MemoryIntegrationStore) DeleteOAuthAccount(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(s.accounts, id)
	return nil
}

func (s *MemoryIntegrationStore) ListDriveDestinations(ctx context.Context) ([]DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DriveDestination, 0, len(s.destinations))
	for _, destination := range s.destinations {
		out = append(out, publicDriveDestination(destination))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryIntegrationStore) CreateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	destination, err := normalizeDriveDestination(destination, true)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.ID = newUUID()
	now := time.Now().UTC().Format(time.RFC3339)
	destination.CreatedAt, destination.UpdatedAt = now, now
	destination.FolderIDConfigured = destination.FolderID != ""
	destination.FolderIDFingerprint = security.SecretFingerprint(destination.FolderID)
	destination.MaskedFolderID = maskIdentifier(destination.FolderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.destinations {
		if strings.EqualFold(existing.Name, destination.Name) {
			return DriveDestination{}, errors.New("drive destination name already exists")
		}
	}
	s.destinations[destination.ID] = destination
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) GetDriveDestination(ctx context.Context, id string) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	destination, ok := s.destinations[id]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) GetDriveDestinationForDispatch(ctx context.Context, id string) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	destination, ok := s.destinations[id]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	return destination, nil
}

func (s *MemoryIntegrationStore) UpdateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	if err := ctx.Err(); err != nil {
		return DriveDestination{}, err
	}
	destination, err := normalizeDriveDestination(destination, false)
	if err != nil {
		return DriveDestination{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.destinations[destination.ID]
	if !ok {
		return DriveDestination{}, ErrNotFound
	}
	for id, item := range s.destinations {
		if id != destination.ID && strings.EqualFold(item.Name, destination.Name) {
			return DriveDestination{}, errors.New("drive destination name already exists")
		}
	}
	if destination.FolderID == "" {
		destination.FolderID = existing.FolderID
		destination.FolderIDConfigured = existing.FolderIDConfigured
		destination.FolderIDFingerprint = existing.FolderIDFingerprint
		destination.MaskedFolderID = existing.MaskedFolderID
	} else {
		destination.FolderIDConfigured = true
		destination.FolderIDFingerprint = security.SecretFingerprint(destination.FolderID)
		destination.MaskedFolderID = maskIdentifier(destination.FolderID)
	}
	destination.CreatedAt = existing.CreatedAt
	destination.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.destinations[destination.ID] = destination
	return publicDriveDestination(destination), nil
}

func (s *MemoryIntegrationStore) DeleteDriveDestination(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.destinations[id]; !ok {
		return ErrNotFound
	}
	delete(s.destinations, id)
	return nil
}

type MariaDBIntegrationStore struct {
	db          *sql.DB
	keyMaterial string
}

func NewMariaDBIntegrationStore(db *sql.DB, keyMaterial string) MariaDBIntegrationStore {
	return MariaDBIntegrationStore{db: db, keyMaterial: keyMaterial}
}

func (s MariaDBIntegrationStore) ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, provider_type, name, enabled, client_id, client_secret_ciphertext, scopes, allowed_domains, auto_provision, default_role_ids, redirect_uri, created_at, updated_at FROM oauth_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OAuthProvider
	for rows.Next() {
		provider, err := scanOAuthProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, rows.Err()
}

func (s MariaDBIntegrationStore) CreateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	provider, err := normalizeOAuthProvider(provider, true)
	if err != nil {
		return OAuthProvider{}, err
	}
	provider.ID = newUUID()
	now := time.Now().UTC()
	secretCiphertext, secretNonce, configured, err := s.encryptOptional(provider.ClientSecret)
	if err != nil {
		return OAuthProvider{}, err
	}
	scopes, allowedDomains, err := marshalStringSlices(provider.Scopes, provider.AllowedDomains)
	if err != nil {
		return OAuthProvider{}, err
	}
	defaultRoleIDs, err := marshalStringSlice(provider.DefaultRoleIDs)
	if err != nil {
		return OAuthProvider{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_providers (id, provider_type, name, enabled, client_id, client_secret_ciphertext, client_secret_nonce, scopes, allowed_domains, auto_provision, default_role_ids, redirect_uri, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, provider.ID, provider.ProviderType, provider.Name, provider.Enabled, provider.ClientID, nullableString(secretCiphertext), nullableString(secretNonce), scopes, allowedDomains, provider.AutoProvision, nullableString(defaultRoleIDs), provider.RedirectURI, now, now)
	if err != nil {
		return OAuthProvider{}, err
	}
	provider.ClientSecret = ""
	provider.ClientSecretConfigured = configured
	provider.CreatedAt = now.Format(time.RFC3339)
	provider.UpdatedAt = now.Format(time.RFC3339)
	return provider, nil
}

func (s MariaDBIntegrationStore) GetOAuthProvider(ctx context.Context, id string) (OAuthProvider, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider_type, name, enabled, client_id, client_secret_ciphertext, scopes, allowed_domains, auto_provision, default_role_ids, redirect_uri, created_at, updated_at FROM oauth_providers WHERE id = ?`, id)
	provider, err := scanOAuthProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthProvider{}, ErrNotFound
	}
	return provider, err
}

func (s MariaDBIntegrationStore) GetOAuthProviderForDispatch(ctx context.Context, id string) (OAuthProvider, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider_type, name, enabled, client_id, client_secret_ciphertext, client_secret_nonce, scopes, allowed_domains, auto_provision, default_role_ids, redirect_uri, created_at, updated_at FROM oauth_providers WHERE id = ?`, id)
	var provider OAuthProvider
	var scopes, domains string
	var defaultRoleIDs sql.NullString
	var secretCiphertext, secretNonce sql.NullString
	var createdAt, updatedAt time.Time
	if err := row.Scan(&provider.ID, &provider.ProviderType, &provider.Name, &provider.Enabled, &provider.ClientID, &secretCiphertext, &secretNonce, &scopes, &domains, &provider.AutoProvision, &defaultRoleIDs, &provider.RedirectURI, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return OAuthProvider{}, ErrNotFound
	} else if err != nil {
		return OAuthProvider{}, err
	}
	if secretCiphertext.Valid && secretCiphertext.String != "" {
		if s.keyMaterial == "" {
			return OAuthProvider{}, ErrSecretKeyRequired
		}
		value, err := security.DecryptSecret(secretCiphertext.String, secretNonce.String, s.keyMaterial)
		if err != nil {
			return OAuthProvider{}, err
		}
		provider.ClientSecret = value
		provider.ClientSecretConfigured = value != ""
	}
	_ = json.Unmarshal([]byte(scopes), &provider.Scopes)
	_ = json.Unmarshal([]byte(domains), &provider.AllowedDomains)
	if defaultRoleIDs.Valid && defaultRoleIDs.String != "" {
		_ = json.Unmarshal([]byte(defaultRoleIDs.String), &provider.DefaultRoleIDs)
	}
	provider.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	provider.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return provider, nil
}

func (s MariaDBIntegrationStore) UpdateOAuthProvider(ctx context.Context, provider OAuthProvider) (OAuthProvider, error) {
	provider, err := normalizeOAuthProvider(provider, false)
	if err != nil {
		return OAuthProvider{}, err
	}
	scopes, allowedDomains, err := marshalStringSlices(provider.Scopes, provider.AllowedDomains)
	if err != nil {
		return OAuthProvider{}, err
	}
	defaultRoleIDs, err := marshalStringSlice(provider.DefaultRoleIDs)
	if err != nil {
		return OAuthProvider{}, err
	}
	now := time.Now().UTC()
	if provider.ClientSecret != "" {
		secretCiphertext, secretNonce, _, err := s.encryptOptional(provider.ClientSecret)
		if err != nil {
			return OAuthProvider{}, err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE oauth_providers SET provider_type = ?, name = ?, enabled = ?, client_id = ?, client_secret_ciphertext = ?, client_secret_nonce = ?, scopes = ?, allowed_domains = ?, auto_provision = ?, default_role_ids = ?, redirect_uri = ?, updated_at = ? WHERE id = ?`, provider.ProviderType, provider.Name, provider.Enabled, provider.ClientID, nullableString(secretCiphertext), nullableString(secretNonce), scopes, allowedDomains, provider.AutoProvision, nullableString(defaultRoleIDs), provider.RedirectURI, now, provider.ID)
		if err != nil {
			return OAuthProvider{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return OAuthProvider{}, ErrNotFound
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE oauth_providers SET provider_type = ?, name = ?, enabled = ?, client_id = ?, scopes = ?, allowed_domains = ?, auto_provision = ?, default_role_ids = ?, redirect_uri = ?, updated_at = ? WHERE id = ?`, provider.ProviderType, provider.Name, provider.Enabled, provider.ClientID, scopes, allowedDomains, provider.AutoProvision, nullableString(defaultRoleIDs), provider.RedirectURI, now, provider.ID)
		if err != nil {
			return OAuthProvider{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return OAuthProvider{}, ErrNotFound
		}
	}
	return s.GetOAuthProvider(ctx, provider.ID)
}

func (s MariaDBIntegrationStore) DeleteOAuthProvider(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM oauth_providers WHERE id = ?`, id)
	return notFoundOnNoRows(result, err)
}

func (s MariaDBIntegrationStore) ListOAuthAccounts(ctx context.Context) ([]OAuthAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.provider_id, a.provider_type, a.account_label, a.subject, a.email, a.scopes, a.refresh_token_ciphertext, a.token_fingerprint, a.token_revision, a.refresh_token_updated_at, a.access_token_refreshed_at, a.access_token_refresh_attempted_at, a.access_token_refresh_failed_at, a.access_token_refresh_failure_code, a.access_token_refresh_relink_required, a.created_at, a.updated_at, p.name FROM oauth_accounts a LEFT JOIN oauth_providers p ON p.id = a.provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OAuthAccount
	for rows.Next() {
		account, err := scanOAuthAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, publicOAuthAccount(account))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortOAuthAccounts(out)
	return out, nil
}

func (s MariaDBIntegrationStore) CreateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	account, err := normalizeOAuthAccount(account, true)
	if err != nil {
		return OAuthAccount{}, err
	}
	account.ID = newUUID()
	now := time.Now().UTC()
	ciphertext, nonce, configured, err := s.encryptOptional(account.RefreshToken)
	if err != nil {
		return OAuthAccount{}, err
	}
	fingerprint := ""
	if configured {
		fingerprint = security.SecretFingerprint(account.RefreshToken)
	}
	scopes, err := marshalStringSlice(account.Scopes)
	if err != nil {
		return OAuthAccount{}, err
	}
	var refreshTokenUpdatedAt any
	if configured {
		refreshTokenUpdatedAt = now
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_accounts (id, provider_id, provider_type, account_label, subject, email, scopes, refresh_token_ciphertext, refresh_token_nonce, token_fingerprint, token_revision, refresh_token_updated_at, access_token_refreshed_at, created_at, updated_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), 1, ?, ?, ?, ?)`, account.ID, account.ProviderID, account.ProviderType, account.AccountLabel, account.Subject, account.Email, scopes, nullableString(ciphertext), nullableString(nonce), fingerprint, refreshTokenUpdatedAt, nil, now, now)
	if err != nil {
		return OAuthAccount{}, err
	}
	account.RefreshToken = ""
	account.RefreshTokenConfigured = configured
	account.TokenFingerprint = fingerprint
	account.TokenRevision = 1
	if configured {
		account.RefreshTokenUpdatedAt = now.Format(time.RFC3339)
	}
	account.CreatedAt = now.Format(time.RFC3339)
	account.UpdatedAt = now.Format(time.RFC3339)
	if provider, providerErr := s.GetOAuthProvider(ctx, account.ProviderID); providerErr == nil {
		account.ProviderName = provider.Name
	}
	return publicOAuthAccount(account), nil
}

func (s MariaDBIntegrationStore) GetOAuthAccount(ctx context.Context, id string) (OAuthAccount, error) {
	row := s.db.QueryRowContext(ctx, `SELECT a.id, a.provider_id, a.provider_type, a.account_label, a.subject, a.email, a.scopes, a.refresh_token_ciphertext, a.token_fingerprint, a.token_revision, a.refresh_token_updated_at, a.access_token_refreshed_at, a.access_token_refresh_attempted_at, a.access_token_refresh_failed_at, a.access_token_refresh_failure_code, a.access_token_refresh_relink_required, a.created_at, a.updated_at, p.name FROM oauth_accounts a LEFT JOIN oauth_providers p ON p.id = a.provider_id WHERE a.id = ?`, id)
	account, err := scanOAuthAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAccount{}, ErrNotFound
	}
	if err != nil {
		return OAuthAccount{}, err
	}
	return publicOAuthAccount(account), nil
}

func (s MariaDBIntegrationStore) GetOAuthAccountForDispatch(ctx context.Context, id string) (OAuthAccount, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider_id, provider_type, account_label, subject, email, scopes, refresh_token_ciphertext, refresh_token_nonce, token_fingerprint, token_revision, refresh_token_updated_at, access_token_refreshed_at, access_token_refresh_attempted_at, access_token_refresh_failed_at, access_token_refresh_failure_code, access_token_refresh_relink_required, created_at, updated_at FROM oauth_accounts WHERE id = ?`, id)
	var account OAuthAccount
	var scopes string
	var subject, email, refreshCiphertext, refreshNonce, tokenFingerprint, refreshFailureCode sql.NullString
	var tokenRevision uint64
	var refreshTokenUpdatedAt, accessTokenRefreshedAt, refreshAttemptedAt, refreshFailedAt sql.NullTime
	var refreshRelinkRequired sql.NullBool
	var createdAt, updatedAt time.Time
	if err := row.Scan(&account.ID, &account.ProviderID, &account.ProviderType, &account.AccountLabel, &subject, &email, &scopes, &refreshCiphertext, &refreshNonce, &tokenFingerprint, &tokenRevision, &refreshTokenUpdatedAt, &accessTokenRefreshedAt, &refreshAttemptedAt, &refreshFailedAt, &refreshFailureCode, &refreshRelinkRequired, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return OAuthAccount{}, ErrNotFound
	} else if err != nil {
		return OAuthAccount{}, err
	}
	if refreshCiphertext.Valid && refreshCiphertext.String != "" {
		if s.keyMaterial == "" {
			return OAuthAccount{}, ErrSecretKeyRequired
		}
		value, err := security.DecryptSecret(refreshCiphertext.String, refreshNonce.String, s.keyMaterial)
		if err != nil {
			return OAuthAccount{}, err
		}
		account.RefreshToken = value
		account.RefreshTokenConfigured = value != ""
	}
	account.Subject = subject.String
	account.Email = email.String
	account.TokenFingerprint = tokenFingerprint.String
	account.TokenRevision = tokenRevision
	if refreshTokenUpdatedAt.Valid {
		account.RefreshTokenUpdatedAt = refreshTokenUpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	if accessTokenRefreshedAt.Valid {
		account.AccessTokenRefreshedAt = accessTokenRefreshedAt.Time.UTC().Format(time.RFC3339)
	}
	if refreshAttemptedAt.Valid {
		account.AccessTokenRefreshAttemptedAt = refreshAttemptedAt.Time.UTC().Format(time.RFC3339)
	}
	if refreshFailedAt.Valid {
		account.AccessTokenRefreshFailedAt = refreshFailedAt.Time.UTC().Format(time.RFC3339)
	}
	account.AccessTokenRefreshFailureCode = refreshFailureCode.String
	account.AccessTokenRefreshRelinkRequired = refreshRelinkRequired.Bool
	_ = json.Unmarshal([]byte(scopes), &account.Scopes)
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	account.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	account.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return account, nil
}

func (s MariaDBIntegrationStore) UpdateOAuthAccount(ctx context.Context, account OAuthAccount) (OAuthAccount, error) {
	account, err := normalizeOAuthAccount(account, false)
	if err != nil {
		return OAuthAccount{}, err
	}
	scopes, err := marshalStringSlice(account.Scopes)
	if err != nil {
		return OAuthAccount{}, err
	}
	now := time.Now().UTC()
	if account.RefreshToken != "" {
		ciphertext, nonce, _, err := s.encryptOptional(account.RefreshToken)
		if err != nil {
			return OAuthAccount{}, err
		}
		fingerprint := security.SecretFingerprint(account.RefreshToken)
		result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET provider_id = ?, provider_type = ?, account_label = ?, subject = NULLIF(?, ''), email = NULLIF(?, ''), scopes = ?, refresh_token_ciphertext = ?, refresh_token_nonce = ?, token_fingerprint = ?, token_revision = token_revision + 1, refresh_token_updated_at = ?, access_token_refreshed_at = NULL, access_token_refresh_attempted_at = NULL, access_token_refresh_failed_at = NULL, access_token_refresh_failure_code = NULL, access_token_refresh_relink_required = FALSE, updated_at = ? WHERE id = ?`, account.ProviderID, account.ProviderType, account.AccountLabel, account.Subject, account.Email, scopes, nullableString(ciphertext), nullableString(nonce), fingerprint, now, now, account.ID)
		if err != nil {
			return OAuthAccount{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return OAuthAccount{}, ErrNotFound
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET provider_id = ?, provider_type = ?, account_label = ?, subject = NULLIF(?, ''), email = NULLIF(?, ''), scopes = ?, updated_at = ? WHERE id = ?`, account.ProviderID, account.ProviderType, account.AccountLabel, account.Subject, account.Email, scopes, now, account.ID)
		if err != nil {
			return OAuthAccount{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return OAuthAccount{}, ErrNotFound
		}
	}
	return s.GetOAuthAccount(ctx, account.ID)
}

func (s MariaDBIntegrationStore) RecordOAuthAccountTokenRefresh(ctx context.Context, id string, expectedTokenRevision uint64, rotatedRefreshToken string, refreshedAt time.Time) (OAuthAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthAccount{}, ErrNotFound
	}
	if expectedTokenRevision == 0 {
		return OAuthAccount{}, ErrConflict
	}
	refreshedAt = normalizedOAuthRefreshTime(refreshedAt)
	rotatedRefreshToken = strings.TrimSpace(rotatedRefreshToken)
	if rotatedRefreshToken == "" {
		result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET access_token_refresh_attempted_at = ?, access_token_refreshed_at = ?, access_token_refresh_failed_at = NULL, access_token_refresh_failure_code = NULL, access_token_refresh_relink_required = FALSE WHERE id = ? AND token_revision = ?`, refreshedAt, refreshedAt, id, expectedTokenRevision)
		if err != nil {
			return OAuthAccount{}, err
		}
		return s.oauthAccountTokenRefreshWriteResult(ctx, id, expectedTokenRevision, result)
	}
	ciphertext, nonce, _, err := s.encryptOptional(rotatedRefreshToken)
	if err != nil {
		return OAuthAccount{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET refresh_token_ciphertext = ?, refresh_token_nonce = ?, token_fingerprint = ?, token_revision = token_revision + 1, refresh_token_updated_at = ?, access_token_refresh_attempted_at = ?, access_token_refreshed_at = ?, access_token_refresh_failed_at = NULL, access_token_refresh_failure_code = NULL, access_token_refresh_relink_required = FALSE WHERE id = ? AND token_revision = ?`, nullableString(ciphertext), nullableString(nonce), security.SecretFingerprint(rotatedRefreshToken), refreshedAt, refreshedAt, refreshedAt, id, expectedTokenRevision)
	if err != nil {
		return OAuthAccount{}, err
	}
	return s.oauthAccountTokenRefreshWriteResult(ctx, id, expectedTokenRevision, result)
}

func (s MariaDBIntegrationStore) RecordOAuthAccountTokenRefreshAttempt(ctx context.Context, id string, expectedTokenRevision uint64, attemptedAt time.Time) (OAuthAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthAccount{}, ErrNotFound
	}
	if expectedTokenRevision == 0 {
		return OAuthAccount{}, ErrConflict
	}
	attemptedAt = normalizedOAuthRefreshTime(attemptedAt)
	result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET access_token_refresh_attempted_at = ? WHERE id = ? AND token_revision = ?`, attemptedAt, id, expectedTokenRevision)
	if err != nil {
		return OAuthAccount{}, err
	}
	return s.oauthAccountTokenRefreshWriteResult(ctx, id, expectedTokenRevision, result)
}

func (s MariaDBIntegrationStore) RecordOAuthAccountTokenRefreshFailure(ctx context.Context, id string, expectedTokenRevision uint64, failureCode string, relinkRequired bool, failedAt time.Time) (OAuthAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthAccount{}, ErrNotFound
	}
	if expectedTokenRevision == 0 {
		return OAuthAccount{}, ErrConflict
	}
	failedAt = normalizedOAuthRefreshTime(failedAt)
	failureCode, relinkRequired = normalizedOAuthTokenRefreshFailure(failureCode, relinkRequired)
	result, err := s.db.ExecContext(ctx, `UPDATE oauth_accounts SET access_token_refresh_attempted_at = ?, access_token_refresh_failed_at = ?, access_token_refresh_failure_code = ?, access_token_refresh_relink_required = ? WHERE id = ? AND token_revision = ?`, failedAt, failedAt, failureCode, relinkRequired, id, expectedTokenRevision)
	if err != nil {
		return OAuthAccount{}, err
	}
	return s.oauthAccountTokenRefreshWriteResult(ctx, id, expectedTokenRevision, result)
}

func (s MariaDBIntegrationStore) oauthAccountTokenRefreshWriteResult(ctx context.Context, id string, expectedTokenRevision uint64, result sql.Result) (OAuthAccount, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return OAuthAccount{}, err
	}
	account, err := s.GetOAuthAccount(ctx, id)
	if err != nil {
		return OAuthAccount{}, err
	}
	if affected == 0 && !oauthAccountTokenRevisionMatches(account.TokenRevision, expectedTokenRevision) {
		return OAuthAccount{}, ErrConflict
	}
	return account, nil
}

func (s MariaDBIntegrationStore) DeleteOAuthAccount(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM oauth_accounts WHERE id = ?`, id)
	return notFoundOnNoRows(result, err)
}

func (s MariaDBIntegrationStore) ListDriveDestinations(ctx context.Context) ([]DriveDestination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveDestination
	for rows.Next() {
		destination, err := scanDriveDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, destination)
	}
	return out, rows.Err()
}

func (s MariaDBIntegrationStore) CreateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	destination, err := normalizeDriveDestination(destination, true)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.ID = newUUID()
	now := time.Now().UTC()
	ciphertext, nonce, _, err := s.encryptRequired(destination.FolderID)
	if err != nil {
		return DriveDestination{}, err
	}
	fingerprint := security.SecretFingerprint(destination.FolderID)
	masked := maskIdentifier(destination.FolderID)
	_, err = s.db.ExecContext(ctx, `INSERT INTO drive_destinations (id, name, auth_mode, oauth_account_id, folder_id_ciphertext, folder_id_nonce, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`, destination.ID, destination.Name, destination.AuthMode, destination.OAuthAccountID, ciphertext, nonce, fingerprint, masked, destination.SharedDrive, now, now)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.FolderID = ""
	destination.FolderIDConfigured = true
	destination.FolderIDFingerprint = fingerprint
	destination.MaskedFolderID = masked
	destination.CreatedAt = now.Format(time.RFC3339)
	destination.UpdatedAt = now.Format(time.RFC3339)
	return destination, nil
}

func (s MariaDBIntegrationStore) GetDriveDestination(ctx context.Context, id string) (DriveDestination, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations WHERE id = ?`, id)
	destination, err := scanDriveDestination(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DriveDestination{}, ErrNotFound
	}
	return destination, err
}

func (s MariaDBIntegrationStore) GetDriveDestinationForDispatch(ctx context.Context, id string) (DriveDestination, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_ciphertext, folder_id_nonce, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations WHERE id = ?`, id)
	var destination DriveDestination
	var oauthAccountID sql.NullString
	var ciphertext, nonce string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&destination.ID, &destination.Name, &destination.AuthMode, &oauthAccountID, &ciphertext, &nonce, &destination.FolderIDFingerprint, &destination.MaskedFolderID, &destination.SharedDrive, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return DriveDestination{}, ErrNotFound
	} else if err != nil {
		return DriveDestination{}, err
	}
	if s.keyMaterial == "" {
		return DriveDestination{}, ErrSecretKeyRequired
	}
	folderID, err := security.DecryptSecret(ciphertext, nonce, s.keyMaterial)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.OAuthAccountID = oauthAccountID.String
	destination.FolderID = folderID
	destination.FolderIDConfigured = folderID != ""
	destination.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	destination.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return destination, nil
}

func (s MariaDBIntegrationStore) UpdateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	destination, err := normalizeDriveDestination(destination, false)
	if err != nil {
		return DriveDestination{}, err
	}
	now := time.Now().UTC()
	if destination.FolderID != "" {
		ciphertext, nonce, _, err := s.encryptRequired(destination.FolderID)
		if err != nil {
			return DriveDestination{}, err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE drive_destinations SET name = ?, auth_mode = ?, oauth_account_id = NULLIF(?, ''), folder_id_ciphertext = ?, folder_id_nonce = ?, folder_id_fingerprint = ?, masked_folder_id = ?, shared_drive = ?, updated_at = ? WHERE id = ?`, destination.Name, destination.AuthMode, destination.OAuthAccountID, ciphertext, nonce, security.SecretFingerprint(destination.FolderID), maskIdentifier(destination.FolderID), destination.SharedDrive, now, destination.ID)
		if err != nil {
			return DriveDestination{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return DriveDestination{}, ErrNotFound
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE drive_destinations SET name = ?, auth_mode = ?, oauth_account_id = NULLIF(?, ''), shared_drive = ?, updated_at = ? WHERE id = ?`, destination.Name, destination.AuthMode, destination.OAuthAccountID, destination.SharedDrive, now, destination.ID)
		if err != nil {
			return DriveDestination{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return DriveDestination{}, ErrNotFound
		}
	}
	return s.GetDriveDestination(ctx, destination.ID)
}

func (s MariaDBIntegrationStore) DeleteDriveDestination(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM drive_destinations WHERE id = ?`, id)
	return notFoundOnNoRows(result, err)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOAuthProvider(row scanner) (OAuthProvider, error) {
	var provider OAuthProvider
	var scopes, domains string
	var createdAt, updatedAt time.Time
	var secretCiphertext, defaultRoleIDs sql.NullString
	if err := row.Scan(&provider.ID, &provider.ProviderType, &provider.Name, &provider.Enabled, &provider.ClientID, &secretCiphertext, &scopes, &domains, &provider.AutoProvision, &defaultRoleIDs, &provider.RedirectURI, &createdAt, &updatedAt); err != nil {
		return OAuthProvider{}, err
	}
	_ = json.Unmarshal([]byte(scopes), &provider.Scopes)
	_ = json.Unmarshal([]byte(domains), &provider.AllowedDomains)
	if defaultRoleIDs.Valid && defaultRoleIDs.String != "" {
		_ = json.Unmarshal([]byte(defaultRoleIDs.String), &provider.DefaultRoleIDs)
	}
	provider.ClientSecretConfigured = secretCiphertext.Valid && secretCiphertext.String != ""
	provider.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	provider.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return provider, nil
}

func scanOAuthAccount(row scanner) (OAuthAccount, error) {
	var account OAuthAccount
	var scopes string
	var subject, email, tokenFingerprint, refreshCiphertext, providerName, refreshFailureCode sql.NullString
	var tokenRevision uint64
	var refreshTokenUpdatedAt, accessTokenRefreshedAt, refreshAttemptedAt, refreshFailedAt sql.NullTime
	var refreshRelinkRequired sql.NullBool
	var createdAt, updatedAt time.Time
	if err := row.Scan(&account.ID, &account.ProviderID, &account.ProviderType, &account.AccountLabel, &subject, &email, &scopes, &refreshCiphertext, &tokenFingerprint, &tokenRevision, &refreshTokenUpdatedAt, &accessTokenRefreshedAt, &refreshAttemptedAt, &refreshFailedAt, &refreshFailureCode, &refreshRelinkRequired, &createdAt, &updatedAt, &providerName); err != nil {
		return OAuthAccount{}, err
	}
	account.ProviderName = providerName.String
	account.Subject = subject.String
	account.Email = email.String
	account.TokenFingerprint = tokenFingerprint.String
	account.TokenRevision = tokenRevision
	if refreshTokenUpdatedAt.Valid {
		account.RefreshTokenUpdatedAt = refreshTokenUpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	if accessTokenRefreshedAt.Valid {
		account.AccessTokenRefreshedAt = accessTokenRefreshedAt.Time.UTC().Format(time.RFC3339)
	}
	if refreshAttemptedAt.Valid {
		account.AccessTokenRefreshAttemptedAt = refreshAttemptedAt.Time.UTC().Format(time.RFC3339)
	}
	if refreshFailedAt.Valid {
		account.AccessTokenRefreshFailedAt = refreshFailedAt.Time.UTC().Format(time.RFC3339)
	}
	account.AccessTokenRefreshFailureCode = refreshFailureCode.String
	account.AccessTokenRefreshRelinkRequired = refreshRelinkRequired.Bool
	account.RefreshTokenConfigured = refreshCiphertext.Valid && refreshCiphertext.String != ""
	_ = json.Unmarshal([]byte(scopes), &account.Scopes)
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	account.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	account.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return account, nil
}

func scanDriveDestination(row scanner) (DriveDestination, error) {
	var destination DriveDestination
	var oauthAccountID sql.NullString
	var createdAt, updatedAt time.Time
	if err := row.Scan(&destination.ID, &destination.Name, &destination.AuthMode, &oauthAccountID, &destination.FolderIDFingerprint, &destination.MaskedFolderID, &destination.SharedDrive, &createdAt, &updatedAt); err != nil {
		return DriveDestination{}, err
	}
	destination.OAuthAccountID = oauthAccountID.String
	destination.FolderIDConfigured = true
	destination.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	destination.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return destination, nil
}

func (s MariaDBIntegrationStore) encryptOptional(value string) (string, string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, nil
	}
	ciphertext, nonce, _, err := s.encryptRequired(value)
	return ciphertext, nonce, true, err
}

func (s MariaDBIntegrationStore) encryptRequired(value string) (string, string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, errors.New("secret value is required")
	}
	if s.keyMaterial == "" {
		return "", "", false, ErrSecretKeyRequired
	}
	ciphertext, nonce, err := security.EncryptSecret(value, s.keyMaterial)
	if err != nil {
		return "", "", false, err
	}
	return ciphertext, nonce, true, nil
}

func normalizeOAuthProvider(provider OAuthProvider, creating bool) (OAuthProvider, error) {
	provider.ProviderType = strings.TrimSpace(strings.ToLower(provider.ProviderType))
	provider.Name = strings.TrimSpace(provider.Name)
	provider.ClientID = strings.TrimSpace(provider.ClientID)
	provider.RedirectURI = strings.TrimSpace(provider.RedirectURI)
	provider.ClientSecret = strings.TrimSpace(provider.ClientSecret)
	provider.Scopes = cleanStringSlice(provider.Scopes)
	provider.AllowedDomains = cleanStringSlice(provider.AllowedDomains)
	provider.DefaultRoleIDs = cleanStringSlice(provider.DefaultRoleIDs)
	if !creating && strings.TrimSpace(provider.ID) == "" {
		return OAuthProvider{}, errors.New("oauth provider id is required")
	}
	if provider.ProviderType != "google" && provider.ProviderType != "github" && provider.ProviderType != "discord" {
		return OAuthProvider{}, errors.New("invalid oauth provider type")
	}
	if provider.Name == "" || provider.ClientID == "" || provider.RedirectURI == "" {
		return OAuthProvider{}, errors.New("oauth provider name, client_id, and redirect_uri are required")
	}
	return provider, nil
}

func normalizeOAuthAccount(account OAuthAccount, creating bool) (OAuthAccount, error) {
	account.ProviderID = strings.TrimSpace(account.ProviderID)
	account.ProviderType = strings.TrimSpace(strings.ToLower(account.ProviderType))
	account.AccountLabel = strings.TrimSpace(account.AccountLabel)
	account.Subject = strings.TrimSpace(account.Subject)
	account.Email = strings.TrimSpace(account.Email)
	account.RefreshToken = strings.TrimSpace(account.RefreshToken)
	account.Scopes = cleanStringSlice(account.Scopes)
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	if !creating && strings.TrimSpace(account.ID) == "" {
		return OAuthAccount{}, errors.New("oauth account id is required")
	}
	if account.ProviderID == "" || account.AccountLabel == "" {
		return OAuthAccount{}, errors.New("oauth account provider_id and account_label are required")
	}
	if account.ProviderType != "google" && account.ProviderType != "github" && account.ProviderType != "discord" {
		return OAuthAccount{}, errors.New("invalid oauth account provider type")
	}
	return account, nil
}

func normalizeDriveDestination(destination DriveDestination, creating bool) (DriveDestination, error) {
	destination.ID = strings.TrimSpace(destination.ID)
	destination.Name = strings.TrimSpace(destination.Name)
	destination.AuthMode = strings.TrimSpace(strings.ToLower(destination.AuthMode))
	destination.OAuthAccountID = strings.TrimSpace(destination.OAuthAccountID)
	destination.FolderID = strings.TrimSpace(destination.FolderID)
	if !creating && destination.ID == "" {
		return DriveDestination{}, errors.New("drive destination id is required")
	}
	if destination.Name == "" {
		return DriveDestination{}, errors.New("drive destination name is required")
	}
	if destination.AuthMode == "" {
		destination.AuthMode = "oauth2"
	}
	if destination.AuthMode != "oauth2" {
		return DriveDestination{}, errors.New("invalid drive destination auth_mode")
	}
	if destination.OAuthAccountID == "" {
		return DriveDestination{}, errors.New("oauth2 drive destination requires oauth_account_id")
	}
	if creating && destination.FolderID == "" {
		return DriveDestination{}, errors.New("drive destination folder_id is required")
	}
	return destination, nil
}

func publicOAuthProvider(provider OAuthProvider) OAuthProvider {
	provider.ClientSecret = ""
	return provider
}

func publicOAuthAccount(account OAuthAccount) OAuthAccount {
	account.RefreshToken = ""
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	account.DisplayName = oauthAccountDisplayName(account)
	return account
}

const (
	OAuthAccountPurposeDrive        = "drive"
	OAuthAccountPurposeYouTube      = "youtube"
	OAuthAccountPurposeDriveYouTube = "drive_youtube"
	OAuthAccountPurposeUnknown      = "unknown"
)

func OAuthAccountPurposeFromScopes(scopes []string) string {
	drive, youtube := false, false
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive":
			drive = true
		case "https://www.googleapis.com/auth/youtube",
			"https://www.googleapis.com/auth/youtube.force-ssl",
			"https://www.googleapis.com/auth/youtube.upload":
			youtube = true
		}
	}
	switch {
	case drive && youtube:
		return OAuthAccountPurposeDriveYouTube
	case drive:
		return OAuthAccountPurposeDrive
	case youtube:
		return OAuthAccountPurposeYouTube
	default:
		return OAuthAccountPurposeUnknown
	}
}

func OAuthAccountAllowsPurpose(account OAuthAccount, purpose string) bool {
	actual := OAuthAccountPurposeFromScopes(account.Scopes)
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case OAuthAccountPurposeDrive:
		return actual == OAuthAccountPurposeDrive || actual == OAuthAccountPurposeDriveYouTube
	case OAuthAccountPurposeYouTube:
		return actual == OAuthAccountPurposeYouTube || actual == OAuthAccountPurposeDriveYouTube
	default:
		return false
	}
}

func oauthAccountDisplayName(account OAuthAccount) string {
	email := strings.TrimSpace(strings.ToLower(account.Email))
	if label := strings.TrimSpace(account.AccountLabel); label != "" {
		if strings.ToLower(label) != email && !generatedOAuthAccountLabel(label, account) {
			return label
		}
	}
	base := strings.TrimSpace(account.ProviderName)
	if base == "" || genericOAuthProviderName(base, account.ProviderType) {
		if providerLabel := oauthAccountProviderLabel(account.ProviderType); providerLabel != "" {
			base = providerLabel + "アカウント"
		} else {
			base = "OAuthアカウント"
		}
	}
	if reference := oauthAccountDisplayReference(account.ID); reference != "" {
		return base + " (" + reference + ")"
	}
	return base + " (表示名未設定)"
}

func oauthAccountDisplayReference(id string) string {
	id = strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func generatedOAuthAccountLabel(label string, account OAuthAccount) bool {
	normalized := compactOAuthLabel(label)
	for _, base := range []string{oauthAccountProviderLabel(account.ProviderType), account.ProviderName} {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		if normalized == compactOAuthLabel(base) || normalized == compactOAuthLabel(base+" 接続アカウント") || normalized == compactOAuthLabel(base+" connected account") {
			return true
		}
	}
	return false
}

func genericOAuthProviderName(name, providerType string) bool {
	base := oauthAccountProviderLabel(providerType)
	if base == "" {
		return false
	}
	normalized := compactOAuthLabel(name)
	return normalized == compactOAuthLabel(base) || normalized == compactOAuthLabel(base+" 接続アカウント") || normalized == compactOAuthLabel(base+" connected account")
}

func compactOAuthLabel(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
}

func sortOAuthAccounts(accounts []OAuthAccount) {
	sort.SliceStable(accounts, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(accounts[i].DisplayName))
		right := strings.ToLower(strings.TrimSpace(accounts[j].DisplayName))
		if left == right {
			return accounts[i].ID < accounts[j].ID
		}
		return left < right
	})
}

func oauthAccountProviderLabel(providerType string) string {
	switch strings.TrimSpace(strings.ToLower(providerType)) {
	case "google":
		return "Google"
	case "github":
		return "GitHub"
	case "discord":
		return "Discord"
	default:
		return strings.TrimSpace(providerType)
	}
}

func publicDriveDestination(destination DriveDestination) DriveDestination {
	destination.FolderID = ""
	return destination
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func marshalStringSlice(values []string) (string, error) {
	body, err := json.Marshal(cleanStringSlice(values))
	return string(body), err
}

func marshalStringSlices(first, second []string) (string, string, error) {
	a, err := marshalStringSlice(first)
	if err != nil {
		return "", "", err
	}
	b, err := marshalStringSlice(second)
	if err != nil {
		return "", "", err
	}
	return a, b, nil
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func normalizedOAuthRefreshTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func oauthAccountTokenRevisionMatches(actual, expected uint64) bool {
	return actual != 0 && expected != 0 && actual == expected
}

func nextOAuthAccountTokenRevision(current uint64) uint64 {
	if current == 0 {
		return 1
	}
	return current + 1
}

func normalizedOAuthTokenRefreshFailure(code string, relinkRequired bool) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(code)) {
	case OAuthTokenRefreshFailureCredentialsUnavailable,
		OAuthTokenRefreshFailureProviderNotReady,
		OAuthTokenRefreshFailureProviderUnavailable,
		OAuthTokenRefreshFailureProviderCredentialsInvalid,
		OAuthTokenRefreshFailureTimedOut,
		OAuthTokenRefreshFailureInvalidResponse:
		return strings.TrimSpace(strings.ToLower(code)), false
	case OAuthTokenRefreshFailureReauthorizationRequired:
		return OAuthTokenRefreshFailureReauthorizationRequired, relinkRequired
	default:
		return OAuthTokenRefreshFailureUnknown, false
	}
}

func notFoundOnNoRows(result sql.Result, err error) error {
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

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 10 {
		return "<configured>"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
