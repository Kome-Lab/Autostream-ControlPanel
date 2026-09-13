package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"time"
)

func (s MariaDBAuthStore) GetMFAConfig(ctx context.Context, userID string) (MFAConfig, error) {
	var (
		cfg                     MFAConfig
		secretCiphertext        sql.NullString
		secretNonce             sql.NullString
		pendingSecretCiphertext sql.NullString
		pendingSecretNonce      sql.NullString
		recoveryJSON            sql.NullString
		updatedAt               time.Time
	)
	err := s.db.QueryRowContext(ctx, `SELECT user_id, enabled, totp_secret_ciphertext, totp_secret_nonce, pending_totp_secret_ciphertext, pending_totp_secret_nonce, recovery_code_hashes_json, updated_at FROM user_mfa WHERE user_id = ?`, userID).Scan(&cfg.UserID, &cfg.Enabled, &secretCiphertext, &secretNonce, &pendingSecretCiphertext, &pendingSecretNonce, &recoveryJSON, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MFAConfig{UserID: userID}, nil
	}
	if err != nil {
		return MFAConfig{}, err
	}
	cfg.UpdatedAt = updatedAt
	if recoveryJSON.Valid && recoveryJSON.String != "" {
		_ = json.Unmarshal([]byte(recoveryJSON.String), &cfg.RecoveryCodeHashes)
	}
	if secretCiphertext.Valid && secretNonce.Valid {
		secret, err := s.decryptMFASecret(secretCiphertext.String, secretNonce.String)
		if err != nil {
			return MFAConfig{}, err
		}
		cfg.TOTPSecret = secret
	}
	if pendingSecretCiphertext.Valid && pendingSecretNonce.Valid {
		secret, err := s.decryptMFASecret(pendingSecretCiphertext.String, pendingSecretNonce.String)
		if err != nil {
			return MFAConfig{}, err
		}
		cfg.PendingTOTPSecret = secret
	}
	return cfg, nil
}

func (s MariaDBAuthStore) StartTOTPEnrollment(ctx context.Context, userID, secret string, recoveryCodeHashes []string) error {
	if s.secretKeyMaterial == "" {
		return ErrSecretKeyRequired
	}
	ciphertext, nonce, err := security.EncryptSecret(secret, s.secretKeyMaterial)
	if err != nil {
		return err
	}
	recoveryJSON, err := json.Marshal(recoveryCodeHashes)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_mfa (user_id, enabled, pending_totp_secret_ciphertext, pending_totp_secret_nonce, recovery_code_hashes_json, updated_at) VALUES (?, false, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE pending_totp_secret_ciphertext = VALUES(pending_totp_secret_ciphertext), pending_totp_secret_nonce = VALUES(pending_totp_secret_nonce), recovery_code_hashes_json = VALUES(recovery_code_hashes_json), updated_at = VALUES(updated_at)`, userID, ciphertext, nonce, string(recoveryJSON), now)
	return err
}

func (s MariaDBAuthStore) ConfirmTOTPEnrollment(ctx context.Context, userID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE user_mfa SET enabled = true, totp_secret_ciphertext = pending_totp_secret_ciphertext, totp_secret_nonce = pending_totp_secret_nonce, pending_totp_secret_ciphertext = NULL, pending_totp_secret_nonce = NULL, updated_at = ? WHERE user_id = ? AND pending_totp_secret_ciphertext IS NOT NULL`, time.Now().UTC(), userID)
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

func (s MariaDBAuthStore) DisableMFA(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_mfa WHERE user_id = ?`, userID)
	return err
}

func (s MariaDBAuthStore) RegenerateRecoveryCodes(ctx context.Context, userID string, recoveryCodeHashes []string) error {
	recoveryJSON, err := json.Marshal(recoveryCodeHashes)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE user_mfa SET recovery_code_hashes_json = ?, updated_at = ? WHERE user_id = ? AND enabled = true`, string(recoveryJSON), time.Now().UTC(), userID)
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

func (s MariaDBAuthStore) ConsumeRecoveryCode(ctx context.Context, userID, recoveryCodeHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var recoveryJSON sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT recovery_code_hashes_json FROM user_mfa WHERE user_id = ? AND enabled = true FOR UPDATE`, userID).Scan(&recoveryJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	var recoveryCodeHashes []string
	if recoveryJSON.Valid && recoveryJSON.String != "" {
		if err := json.Unmarshal([]byte(recoveryJSON.String), &recoveryCodeHashes); err != nil {
			return err
		}
	}
	next := make([]string, 0, len(recoveryCodeHashes))
	found := false
	for _, hash := range recoveryCodeHashes {
		if hash == recoveryCodeHash {
			found = true
			continue
		}
		next = append(next, hash)
	}
	if !found {
		return ErrUnauthorized
	}
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_mfa SET recovery_code_hashes_json = ?, updated_at = ? WHERE user_id = ?`, string(body), time.Now().UTC(), userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s MariaDBAuthStore) CreateMFAChallenge(ctx context.Context, userID string, ttl time.Duration) (MFAChallenge, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return MFAChallenge{}, err
	}
	challenge := MFAChallenge{Token: raw, TokenHash: security.HashToken(raw), UserID: userID, ExpiresAt: time.Now().UTC().Add(ttl)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO mfa_challenges (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, challenge.TokenHash, challenge.UserID, challenge.ExpiresAt, time.Now().UTC())
	return challenge, err
}

func (s MariaDBAuthStore) GetMFAChallenge(ctx context.Context, rawToken string) (MFAChallenge, error) {
	hash := security.HashToken(rawToken)
	var challenge MFAChallenge
	challenge.Token = rawToken
	challenge.TokenHash = hash
	err := s.db.QueryRowContext(ctx, `SELECT user_id, expires_at FROM mfa_challenges WHERE id = ?`, hash).Scan(&challenge.UserID, &challenge.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MFAChallenge{}, ErrNotFound
	}
	if err != nil {
		return MFAChallenge{}, err
	}
	if time.Now().UTC().After(challenge.ExpiresAt) {
		_ = s.DeleteMFAChallenge(ctx, rawToken)
		return MFAChallenge{}, ErrNotFound
	}
	return challenge, nil
}

func (s MariaDBAuthStore) DeleteMFAChallenge(ctx context.Context, rawToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mfa_challenges WHERE id = ?`, security.HashToken(rawToken))
	return err
}
