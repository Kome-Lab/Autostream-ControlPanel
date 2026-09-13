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

func (s MariaDBAuthStore) CreatePasskeyRegistrationChallenge(ctx context.Context, userID, rpID, rpName, userName, userDisplayName string, ttl time.Duration) (PasskeyRegistrationChallenge, error) {
	challenge, err := newPasskeyRegistrationChallenge(userID, rpID, rpName, userName, userDisplayName, ttl)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO webauthn_registration_challenges (id, user_id, challenge, user_handle, rp_id, rp_name, user_name, user_display_name, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		challenge.TokenHash, challenge.UserID, challenge.Challenge, challenge.UserHandle, challenge.RPID, challenge.RPName, challenge.UserName, challenge.UserDisplayName, challenge.ExpiresAt, challenge.CreatedAt)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	return challenge, nil
}

func (s MariaDBAuthStore) GetPasskeyRegistrationChallenge(ctx context.Context, rawToken string) (PasskeyRegistrationChallenge, error) {
	hash := security.HashToken(strings.TrimSpace(rawToken))
	var challenge PasskeyRegistrationChallenge
	challenge.Token = rawToken
	challenge.TokenHash = hash
	err := s.db.QueryRowContext(ctx, `SELECT user_id, challenge, user_handle, rp_id, rp_name, user_name, user_display_name, expires_at, created_at FROM webauthn_registration_challenges WHERE id = ?`, hash).Scan(&challenge.UserID, &challenge.Challenge, &challenge.UserHandle, &challenge.RPID, &challenge.RPName, &challenge.UserName, &challenge.UserDisplayName, &challenge.ExpiresAt, &challenge.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyRegistrationChallenge{}, ErrNotFound
	}
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	if time.Now().UTC().After(challenge.ExpiresAt) {
		_ = s.DeletePasskeyRegistrationChallenge(ctx, rawToken)
		return PasskeyRegistrationChallenge{}, ErrNotFound
	}
	return challenge, nil
}

func (s MariaDBAuthStore) DeletePasskeyRegistrationChallenge(ctx context.Context, rawToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_registration_challenges WHERE id = ?`, security.HashToken(strings.TrimSpace(rawToken)))
	return err
}

func (s MariaDBAuthStore) CreatePasskeyCeremonySession(ctx context.Context, userID, ceremony string, sessionJSON []byte, ttl time.Duration) (PasskeyCeremonySession, error) {
	session, err := newPasskeyCeremonySession(userID, ceremony, sessionJSON, ttl)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	var dbUserID any
	if session.UserID != "" {
		dbUserID = session.UserID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO webauthn_ceremony_sessions (id, user_id, ceremony, session_json, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		session.TokenHash, dbUserID, session.Ceremony, session.SessionJSON, session.ExpiresAt, session.CreatedAt)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	return session, nil
}

func (s MariaDBAuthStore) GetPasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error) {
	hash := security.HashToken(strings.TrimSpace(rawToken))
	var session PasskeyCeremonySession
	var userID sql.NullString
	session.Token = rawToken
	session.TokenHash = hash
	err := s.db.QueryRowContext(ctx, `SELECT user_id, ceremony, session_json, expires_at, created_at FROM webauthn_ceremony_sessions WHERE id = ?`, hash).Scan(&userID, &session.Ceremony, &session.SessionJSON, &session.ExpiresAt, &session.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyCeremonySession{}, ErrNotFound
	}
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	if userID.Valid {
		session.UserID = userID.String
	}
	if strings.TrimSpace(session.Ceremony) != strings.TrimSpace(ceremony) || time.Now().UTC().After(session.ExpiresAt) {
		_ = s.DeletePasskeyCeremonySession(ctx, rawToken)
		return PasskeyCeremonySession{}, ErrNotFound
	}
	return session, nil
}

func (s MariaDBAuthStore) ConsumePasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error) {
	hash := security.HashToken(strings.TrimSpace(rawToken))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	defer tx.Rollback()
	var session PasskeyCeremonySession
	var userID sql.NullString
	session.Token = rawToken
	session.TokenHash = hash
	err = tx.QueryRowContext(ctx, `SELECT user_id, ceremony, session_json, expires_at, created_at FROM webauthn_ceremony_sessions WHERE id = ? FOR UPDATE`, hash).Scan(&userID, &session.Ceremony, &session.SessionJSON, &session.ExpiresAt, &session.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyCeremonySession{}, ErrNotFound
	}
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	if userID.Valid {
		session.UserID = userID.String
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM webauthn_ceremony_sessions WHERE id = ?`, hash); err != nil {
		return PasskeyCeremonySession{}, err
	}
	if strings.TrimSpace(session.Ceremony) != strings.TrimSpace(ceremony) || time.Now().UTC().After(session.ExpiresAt) {
		if err := tx.Commit(); err != nil {
			return PasskeyCeremonySession{}, err
		}
		return PasskeyCeremonySession{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return PasskeyCeremonySession{}, err
	}
	return session, nil
}

func (s MariaDBAuthStore) DeletePasskeyCeremonySession(ctx context.Context, rawToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_ceremony_sessions WHERE id = ?`, security.HashToken(strings.TrimSpace(rawToken)))
	return err
}

func (s MariaDBAuthStore) ListPasskeyCredentials(ctx context.Context, userID string) ([]PasskeyCredential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, credential_id_hash, sign_count, transports_json, aaguid, backup_eligible, backed_up, created_at, updated_at, last_used_at FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PasskeyCredential
	for rows.Next() {
		credential, err := scanPublicPasskeyCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, credential)
	}
	return out, rows.Err()
}

func (s MariaDBAuthStore) ListPasskeyCredentialsForVerification(ctx context.Context, userID string) ([]PasskeyCredential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, credential_id, credential_id_hash, public_key_cbor, sign_count, transports_json, aaguid, backup_eligible, backed_up, created_at, updated_at, last_used_at FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PasskeyCredential
	for rows.Next() {
		var credential PasskeyCredential
		var transportsRaw string
		var aaguid sql.NullString
		var lastUsed sql.NullTime
		if err := rows.Scan(&credential.ID, &credential.UserID, &credential.Name, &credential.CredentialID, &credential.CredentialIDHash, &credential.PublicKeyCBOR, &credential.SignCount, &transportsRaw, &aaguid, &credential.BackupEligible, &credential.BackedUp, &credential.CreatedAt, &credential.UpdatedAt, &lastUsed); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(transportsRaw), &credential.Transports)
		credential.AAGUID = aaguid.String
		if lastUsed.Valid {
			credential.LastUsedAt = &lastUsed.Time
		}
		out = append(out, credential)
	}
	return out, rows.Err()
}

func (s MariaDBAuthStore) CreatePasskeyCredential(ctx context.Context, credential PasskeyCredential) (PasskeyCredential, error) {
	credential, err := normalizePasskeyCredential(credential)
	if err != nil {
		return PasskeyCredential{}, err
	}
	transports, err := json.Marshal(credential.Transports)
	if err != nil {
		return PasskeyCredential{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO webauthn_credentials (id, user_id, name, credential_id, credential_id_hash, public_key_cbor, sign_count, transports_json, aaguid, backup_eligible, backed_up, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`,
		credential.ID, credential.UserID, credential.Name, credential.CredentialID, credential.CredentialIDHash, credential.PublicKeyCBOR, credential.SignCount, string(transports), credential.AAGUID, credential.BackupEligible, credential.BackedUp, credential.CreatedAt, credential.UpdatedAt)
	if err != nil {
		return PasskeyCredential{}, err
	}
	return publicPasskeyCredential(credential), nil
}

func (s MariaDBAuthStore) FindPasskeyCredentialByCredentialID(ctx context.Context, credentialID []byte) (PasskeyCredential, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, user_id, name, credential_id, credential_id_hash, public_key_cbor, sign_count, transports_json, aaguid, backup_eligible, backed_up, created_at, updated_at, last_used_at FROM webauthn_credentials WHERE credential_id_hash = ?`, passkeyCredentialIDHash(credentialID))
	var credential PasskeyCredential
	var transportsRaw string
	var aaguid sql.NullString
	var lastUsed sql.NullTime
	err := row.Scan(&credential.ID, &credential.UserID, &credential.Name, &credential.CredentialID, &credential.CredentialIDHash, &credential.PublicKeyCBOR, &credential.SignCount, &transportsRaw, &aaguid, &credential.BackupEligible, &credential.BackedUp, &credential.CreatedAt, &credential.UpdatedAt, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyCredential{}, ErrNotFound
	}
	if err != nil {
		return PasskeyCredential{}, err
	}
	_ = json.Unmarshal([]byte(transportsRaw), &credential.Transports)
	credential.AAGUID = aaguid.String
	if lastUsed.Valid {
		credential.LastUsedAt = &lastUsed.Time
	}
	return credential, nil
}

func (s MariaDBAuthStore) UpdatePasskeySignCount(ctx context.Context, id string, signCount uint32) error {
	result, err := s.db.ExecContext(ctx, `UPDATE webauthn_credentials SET sign_count = ?, last_used_at = ?, updated_at = ? WHERE id = ?`, signCount, time.Now().UTC(), time.Now().UTC(), id)
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

func (s MariaDBAuthStore) DeletePasskeyCredential(ctx context.Context, userID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE user_id = ? AND id = ?`, userID, id)
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
