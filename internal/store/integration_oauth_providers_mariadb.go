package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"time"
)

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
