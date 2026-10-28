package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

// UpdateSecretTx participates in the caller's configuration transaction. The
// caller owns the matching database authority and commits only after every
// protected settings mutation succeeds.
func (s MariaDBSecretStore) UpdateSecretTx(ctx context.Context, tx *sql.Tx, name, value string, now time.Time) (SecretStatus, error) {
	if tx == nil {
		return SecretStatus{}, ErrAtomicProfileUpdateUnavailable
	}
	if !allowedSecretName(name) {
		return SecretStatus{}, ErrUnknownSecret
	}
	now = now.UTC()
	if value == "" {
		_, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE name = ?", name)
		return SecretStatus{Name: name, Configured: false, UpdatedAt: now.Format(time.RFC3339)}, err
	}
	if s.keyMaterial == "" {
		return SecretStatus{}, ErrSecretKeyRequired
	}
	ciphertext, nonce, err := security.EncryptSecret(value, s.keyMaterial)
	if err != nil {
		return SecretStatus{}, err
	}
	fingerprint := security.SecretFingerprint(value)
	_, err = tx.ExecContext(ctx, `INSERT INTO secrets(name,ciphertext,nonce,value_hash,updated_at)VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE ciphertext=VALUES(ciphertext),nonce=VALUES(nonce),value_hash=VALUES(value_hash),updated_at=VALUES(updated_at)`, name, ciphertext, nonce, fingerprint, now)
	if err != nil {
		return SecretStatus{}, err
	}
	return SecretStatus{Name: name, Configured: true, Fingerprint: fingerprint, UpdatedAt: now.Format(time.RFC3339)}, nil
}

// Requires s.mu and prior name validation. No fallible work follows the first
// write in a coordinator that publishes several settings/secret changes.
func (s *MemorySecretStore) updateSecretLocked(name, value string, now time.Time) SecretStatus {
	status := SecretStatus{Name: name, Configured: value != "", UpdatedAt: now.UTC().Format(time.RFC3339)}
	if status.Configured {
		status.Fingerprint = security.SecretFingerprint(value)
		s.secrets[name], s.values[name] = status, value
	} else {
		delete(s.secrets, name)
		delete(s.values, name)
	}
	return status
}
