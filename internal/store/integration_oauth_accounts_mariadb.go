package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

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
