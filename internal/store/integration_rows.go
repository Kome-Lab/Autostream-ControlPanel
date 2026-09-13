package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

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
