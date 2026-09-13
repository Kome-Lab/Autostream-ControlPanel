package store

import (
	"context"
	"sync"
	"time"
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
