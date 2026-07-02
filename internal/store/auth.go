package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type User struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	LastLoginIP string     `json:"last_login_ip,omitempty"`

	PasswordHash string `json:"-"`
}

type Session struct {
	Token             string
	TokenHash         string
	CSRFToken         string
	CSRFTokenHash     string
	UserID            string
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type MFAConfig struct {
	UserID             string
	Enabled            bool
	TOTPSecret         string
	PendingTOTPSecret  string
	RecoveryCodeHashes []string
	UpdatedAt          time.Time
}

type MFAChallenge struct {
	Token     string
	TokenHash string
	UserID    string
	ExpiresAt time.Time
}

type PasskeyRegistrationChallenge struct {
	Token           string    `json:"-"`
	TokenHash       string    `json:"token_hash,omitempty"`
	UserID          string    `json:"user_id"`
	Challenge       string    `json:"challenge"`
	UserHandle      string    `json:"user_handle"`
	RPID            string    `json:"rp_id"`
	RPName          string    `json:"rp_name"`
	UserName        string    `json:"user_name"`
	UserDisplayName string    `json:"user_display_name"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type PasskeyCeremonySession struct {
	Token       string    `json:"-"`
	TokenHash   string    `json:"token_hash,omitempty"`
	UserID      string    `json:"user_id"`
	Ceremony    string    `json:"ceremony"`
	SessionJSON []byte    `json:"-"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type PasskeyCredential struct {
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	Name             string     `json:"name"`
	CredentialID     []byte     `json:"-"`
	CredentialIDHash string     `json:"credential_id_hash,omitempty"`
	PublicKeyCBOR    []byte     `json:"-"`
	SignCount        uint32     `json:"sign_count"`
	Transports       []string   `json:"transports,omitempty"`
	AAGUID           string     `json:"aaguid,omitempty"`
	BackupEligible   bool       `json:"backup_eligible"`
	BackedUp         bool       `json:"backed_up"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
}

type AuditEvent struct {
	ID            string         `json:"id"`
	Timestamp     time.Time      `json:"timestamp"`
	ActorUserID   string         `json:"actor_user_id,omitempty"`
	ActorUsername string         `json:"actor_username,omitempty"`
	ActorIP       string         `json:"actor_ip,omitempty"`
	UserAgent     string         `json:"user_agent,omitempty"`
	Action        string         `json:"action"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id,omitempty"`
	Result        string         `json:"result"`
	Metadata      map[string]any `json:"metadata"`
	RequestID     string         `json:"request_id"`
}

type AuditFilter struct {
	Limit   int
	Actions []string
	Result  string
	Query   string
	From    time.Time
	To      time.Time
}

type AuthStore interface {
	CountUsers(ctx context.Context) (int, error)
	CreateFirstAdmin(ctx context.Context, username, password string, permissions []string) (User, error)
	FindUserByUsername(ctx context.Context, username string) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	GetUserPermissions(ctx context.Context, id string) ([]string, error)
	ChangePassword(ctx context.Context, id, newPassword string) error
	CreateSession(ctx context.Context, userID string, idleTTL, absoluteTTL time.Duration) (Session, error)
	GetSession(ctx context.Context, rawToken string) (Session, error)
	DeleteSession(ctx context.Context, rawToken string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	RecordLoginSuccess(ctx context.Context, userID, ip string) error
	RecordLoginFailure(ctx context.Context, username string, lockoutThreshold int) error
}

type MFAStore interface {
	GetMFAConfig(ctx context.Context, userID string) (MFAConfig, error)
	StartTOTPEnrollment(ctx context.Context, userID, secret string, recoveryCodeHashes []string) error
	ConfirmTOTPEnrollment(ctx context.Context, userID string) error
	DisableMFA(ctx context.Context, userID string) error
	RegenerateRecoveryCodes(ctx context.Context, userID string, recoveryCodeHashes []string) error
	ConsumeRecoveryCode(ctx context.Context, userID, recoveryCodeHash string) error
	CreateMFAChallenge(ctx context.Context, userID string, ttl time.Duration) (MFAChallenge, error)
	GetMFAChallenge(ctx context.Context, rawToken string) (MFAChallenge, error)
	DeleteMFAChallenge(ctx context.Context, rawToken string) error
}

type PasskeyStore interface {
	ListPasskeyCredentials(ctx context.Context, userID string) ([]PasskeyCredential, error)
	ListPasskeyCredentialsForVerification(ctx context.Context, userID string) ([]PasskeyCredential, error)
	CreatePasskeyCredential(ctx context.Context, credential PasskeyCredential) (PasskeyCredential, error)
	FindPasskeyCredentialByCredentialID(ctx context.Context, credentialID []byte) (PasskeyCredential, error)
	UpdatePasskeySignCount(ctx context.Context, id string, signCount uint32) error
	DeletePasskeyCredential(ctx context.Context, userID, id string) error
	CreatePasskeyRegistrationChallenge(ctx context.Context, userID, rpID, rpName, userName, userDisplayName string, ttl time.Duration) (PasskeyRegistrationChallenge, error)
	GetPasskeyRegistrationChallenge(ctx context.Context, rawToken string) (PasskeyRegistrationChallenge, error)
	DeletePasskeyRegistrationChallenge(ctx context.Context, rawToken string) error
	CreatePasskeyCeremonySession(ctx context.Context, userID, ceremony string, sessionJSON []byte, ttl time.Duration) (PasskeyCeremonySession, error)
	GetPasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error)
	ConsumePasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error)
	DeletePasskeyCeremonySession(ctx context.Context, rawToken string) error
}

type AuditStore interface {
	WriteAudit(ctx context.Context, event AuditEvent) error
	ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEvent, error)
}

type MariaDBAuthStore struct {
	db                *sql.DB
	secretKeyMaterial string
}

func NewMariaDBAuthStore(db *sql.DB) MariaDBAuthStore {
	return MariaDBAuthStore{db: db}
}

func NewMariaDBAuthStoreWithSecretKey(db *sql.DB, keyMaterial string) MariaDBAuthStore {
	return MariaDBAuthStore{db: db, secretKeyMaterial: keyMaterial}
}

func (s MariaDBAuthStore) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s MariaDBAuthStore) CreateFirstAdmin(ctx context.Context, username, password string, permissions []string) (User, error) {
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	user := User{ID: newUUID(), Username: username, Status: "active", Roles: []string{"super_admin"}, PasswordHash: hash}
	roleID := newUUID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)`, user.ID, user.Username, user.PasswordHash, now, now); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO roles (id, name, created_at) VALUES (?, 'super_admin', ?)`, roleID, now); err != nil {
		return User{}, err
	}
	for _, permission := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`, roleID, permission); err != nil {
			return User{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, user.ID, roleID); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s MariaDBAuthStore) FindUserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, status, last_login_at, last_login_ip FROM users WHERE username = ?`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Status, &lastLoginAt, &lastLoginIP)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lastLoginIP.Valid {
		user.LastLoginIP = lastLoginIP.String
	}
	user.Roles, _ = s.userRoles(ctx, user.ID)
	return user, nil
}

func (s MariaDBAuthStore) GetUser(ctx context.Context, id string) (User, error) {
	var user User
	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, status, last_login_at, last_login_ip FROM users WHERE id = ?`, id).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Status, &lastLoginAt, &lastLoginIP)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lastLoginIP.Valid {
		user.LastLoginIP = lastLoginIP.String
	}
	user.Roles, _ = s.userRoles(ctx, user.ID)
	return user, nil
}

func (s MariaDBAuthStore) userRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.name FROM roles r INNER JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = ? ORDER BY r.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s MariaDBAuthStore) GetUserPermissions(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT rp.permission FROM role_permissions rp INNER JOIN user_roles ur ON ur.role_id = rp.role_id WHERE ur.user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

func (s MariaDBAuthStore) ChangePassword(ctx context.Context, id, newPassword string) error {
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'active', updated_at = ? WHERE id = ?`, hash, time.Now().UTC(), id)
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

func (s MariaDBAuthStore) CreateSession(ctx context.Context, userID string, idleTTL, absoluteTTL time.Duration) (Session, error) {
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrfToken, err := security.RandomToken(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	session := Session{
		Token: rawToken, TokenHash: security.HashToken(rawToken),
		CSRFToken: csrfToken, CSRFTokenHash: security.HashToken(csrfToken),
		UserID: userID, IdleExpiresAt: now.Add(idleTTL), AbsoluteExpiresAt: now.Add(absoluteTTL),
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, csrf_token_hash, idle_expires_at, absolute_expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`, session.TokenHash, session.UserID, session.CSRFTokenHash, session.IdleExpiresAt, session.AbsoluteExpiresAt, now)
	return session, err
}

func (s MariaDBAuthStore) GetSession(ctx context.Context, rawToken string) (Session, error) {
	hash := security.HashToken(rawToken)
	var session Session
	session.Token = rawToken
	session.TokenHash = hash
	err := s.db.QueryRowContext(ctx, `SELECT user_id, csrf_token_hash, idle_expires_at, absolute_expires_at FROM sessions WHERE id = ?`, hash).Scan(&session.UserID, &session.CSRFTokenHash, &session.IdleExpiresAt, &session.AbsoluteExpiresAt)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if time.Now().UTC().After(session.IdleExpiresAt) || time.Now().UTC().After(session.AbsoluteExpiresAt) {
		_ = s.DeleteSession(ctx, rawToken)
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s MariaDBAuthStore) DeleteSession(ctx context.Context, rawToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, security.HashToken(rawToken))
	return err
}

func (s MariaDBAuthStore) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s MariaDBAuthStore) RecordLoginSuccess(ctx context.Context, userID, ip string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET failed_login_count = 0, last_login_at = ?, last_login_ip = ? WHERE id = ?`, time.Now().UTC(), ip, userID)
	return err
}

func (s MariaDBAuthStore) RecordLoginFailure(ctx context.Context, username string, lockoutThreshold int) error {
	if lockoutThreshold < 1 {
		lockoutThreshold = defaultSecurityConfig.LoginLockoutThreshold
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET failed_login_count = failed_login_count + 1, status = IF(failed_login_count + 1 >= ?, 'locked', status), updated_at = ? WHERE username = ?`, lockoutThreshold, time.Now().UTC(), username)
	return err
}

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

func (s MariaDBAuthStore) decryptMFASecret(ciphertext, nonce string) (string, error) {
	if s.secretKeyMaterial == "" {
		return "", ErrSecretKeyRequired
	}
	return security.DecryptSecret(ciphertext, nonce, s.secretKeyMaterial)
}

type publicPasskeyScanner interface {
	Scan(dest ...any) error
}

func scanPublicPasskeyCredential(scanner publicPasskeyScanner) (PasskeyCredential, error) {
	var credential PasskeyCredential
	var transportsRaw string
	var aaguid sql.NullString
	var lastUsed sql.NullTime
	if err := scanner.Scan(&credential.ID, &credential.UserID, &credential.Name, &credential.CredentialIDHash, &credential.SignCount, &transportsRaw, &aaguid, &credential.BackupEligible, &credential.BackedUp, &credential.CreatedAt, &credential.UpdatedAt, &lastUsed); err != nil {
		return PasskeyCredential{}, err
	}
	_ = json.Unmarshal([]byte(transportsRaw), &credential.Transports)
	credential.AAGUID = aaguid.String
	if lastUsed.Valid {
		credential.LastUsedAt = &lastUsed.Time
	}
	return credential, nil
}

func normalizePasskeyCredential(credential PasskeyCredential) (PasskeyCredential, error) {
	credential.UserID = strings.TrimSpace(credential.UserID)
	credential.Name = strings.TrimSpace(credential.Name)
	credential.AAGUID = strings.TrimSpace(credential.AAGUID)
	if credential.UserID == "" || len(credential.CredentialID) == 0 || len(credential.PublicKeyCBOR) == 0 {
		return PasskeyCredential{}, errors.New("passkey credential user, credential id, and public key are required")
	}
	if credential.ID == "" {
		credential.ID = newUUID()
	}
	if credential.Name == "" {
		credential.Name = "Passkey"
	}
	credential.CredentialIDHash = passkeyCredentialIDHash(credential.CredentialID)
	credential.Transports = cleanPasskeyStringSlice(credential.Transports)
	now := time.Now().UTC()
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now
	}
	credential.UpdatedAt = now
	return credential, nil
}

func publicPasskeyCredential(credential PasskeyCredential) PasskeyCredential {
	credential.CredentialID = nil
	credential.PublicKeyCBOR = nil
	return credential
}

func passkeyCredentialIDHash(credentialID []byte) string {
	sum := sha256.Sum256(credentialID)
	return hex.EncodeToString(sum[:])
}

func newPasskeyRegistrationChallenge(userID, rpID, rpName, userName, userDisplayName string, ttl time.Duration) (PasskeyRegistrationChallenge, error) {
	userID = strings.TrimSpace(userID)
	rpID = strings.TrimSpace(rpID)
	rpName = strings.TrimSpace(rpName)
	userName = strings.TrimSpace(userName)
	userDisplayName = strings.TrimSpace(userDisplayName)
	if userID == "" || rpID == "" || rpName == "" || userName == "" {
		return PasskeyRegistrationChallenge{}, errors.New("passkey registration user and relying party are required")
	}
	if userDisplayName == "" {
		userDisplayName = userName
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	rawChallenge, err := security.RandomToken(32)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	now := time.Now().UTC()
	return PasskeyRegistrationChallenge{
		Token: rawToken, TokenHash: security.HashToken(rawToken), UserID: userID,
		Challenge: rawChallenge, UserHandle: base64.RawURLEncoding.EncodeToString([]byte(userID)),
		RPID: rpID, RPName: rpName, UserName: userName, UserDisplayName: userDisplayName,
		ExpiresAt: now.Add(ttl), CreatedAt: now,
	}, nil
}

func newPasskeyCeremonySession(userID, ceremony string, sessionJSON []byte, ttl time.Duration) (PasskeyCeremonySession, error) {
	userID = strings.TrimSpace(userID)
	ceremony = strings.TrimSpace(ceremony)
	if ceremony == "" || len(sessionJSON) == 0 {
		return PasskeyCeremonySession{}, errors.New("passkey ceremony session user, ceremony, and data are required")
	}
	switch ceremony {
	case "registration", "login":
	default:
		return PasskeyCeremonySession{}, errors.New("invalid passkey ceremony")
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	now := time.Now().UTC()
	return PasskeyCeremonySession{
		Token:       rawToken,
		TokenHash:   security.HashToken(rawToken),
		UserID:      userID,
		Ceremony:    ceremony,
		SessionJSON: append([]byte(nil), sessionJSON...),
		ExpiresAt:   now.Add(ttl),
		CreatedAt:   now,
	}, nil
}

func cleanPasskeyStringSlice(values []string) []string {
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

type MariaDBAuditStore struct {
	db *sql.DB
}

func NewMariaDBAuditStore(db *sql.DB) MariaDBAuditStore {
	return MariaDBAuditStore{db: db}
}

func (s MariaDBAuditStore) WriteAudit(ctx context.Context, event AuditEvent) error {
	event = redactedAuditEvent(event)
	if event.ID == "" {
		event.ID = newUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	body, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_logs (id, timestamp, actor_user_id, actor_username, actor_ip, user_agent, action, resource_type, resource_id, result, metadata, request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.Timestamp, nullString(event.ActorUserID), nullString(event.ActorUsername), nullString(event.ActorIP), nullString(event.UserAgent), event.Action, event.ResourceType, nullString(event.ResourceID), event.Result, string(body), event.RequestID)
	return err
}

func (s MariaDBAuditStore) ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	filter = normalizeAuditFilter(filter)
	where := make([]string, 0, 3)
	args := make([]any, 0, len(filter.Actions)+3)
	if len(filter.Actions) > 0 {
		placeholders := make([]string, 0, len(filter.Actions))
		for _, action := range filter.Actions {
			placeholders = append(placeholders, "?")
			args = append(args, action)
		}
		where = append(where, "action IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Result != "" {
		where = append(where, "result = ?")
		args = append(args, filter.Result)
	}
	if !filter.From.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		where = append(where, "timestamp < ?")
		args = append(args, filter.To)
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		where = append(where, "(action LIKE ? OR actor_username LIKE ? OR resource_type LIKE ? OR resource_id LIKE ? OR result LIKE ? OR metadata LIKE ?)")
		args = append(args, like, like, like, like, like, like)
	}
	query := `SELECT id, timestamp, actor_user_id, actor_username, actor_ip, user_agent, action, resource_type, resource_id, result, metadata, request_id FROM audit_logs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY timestamp DESC LIMIT ?"
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var actorUserID, actorUsername, actorIP, userAgent, resourceID sql.NullString
		var metadata string
		if err := rows.Scan(&event.ID, &event.Timestamp, &actorUserID, &actorUsername, &actorIP, &userAgent, &event.Action, &event.ResourceType, &resourceID, &event.Result, &metadata, &event.RequestID); err != nil {
			return nil, err
		}
		event.ActorUserID = actorUserID.String
		event.ActorUsername = actorUsername.String
		event.ActorIP = actorIP.String
		event.UserAgent = userAgent.String
		event.ResourceID = resourceID.String
		event.Metadata = map[string]any{}
		_ = json.Unmarshal([]byte(metadata), &event.Metadata)
		event = redactedAuditEvent(event)
		events = append(events, event)
	}
	return events, rows.Err()
}

func normalizeAuditFilter(filter AuditFilter) AuditFilter {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	filter.Result = strings.TrimSpace(filter.Result)
	if filter.Result != "success" && filter.Result != "failure" {
		filter.Result = ""
	}
	filter.Query = strings.TrimSpace(filter.Query)
	actions := make([]string, 0, len(filter.Actions))
	seen := map[string]bool{}
	for _, action := range filter.Actions {
		action = strings.TrimSpace(action)
		if action == "" || seen[action] {
			continue
		}
		seen[action] = true
		actions = append(actions, action)
	}
	filter.Actions = actions
	return filter
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}

var ErrUnauthorized = errors.New("unauthorized")
