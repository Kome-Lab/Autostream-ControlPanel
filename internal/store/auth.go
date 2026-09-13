package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Email       string     `json:"email,omitempty"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	RoleIDs     []string   `json:"-"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	LastLoginIP string     `json:"last_login_ip,omitempty"`

	PasswordHash string `json:"-"`
}

type UserAvatar struct {
	UserID      string
	ContentType string
	Data        []byte
	Fingerprint string
	UpdatedAt   time.Time
}

type UserAvatarInfo struct {
	UserID      string    `json:"user_id"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
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

type EmailChangeChallenge struct {
	Token     string    `json:"-"`
	TokenHash string    `json:"token_hash,omitempty"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
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
	Limit           int
	Actions         []string
	ExcludedActions []string
	Result          string
	Query           string
	From            time.Time
	To              time.Time
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
	RefreshSession(ctx context.Context, rawToken string, idleTTL time.Duration) (Session, error)
	DeleteSession(ctx context.Context, rawToken string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	RecordLoginSuccess(ctx context.Context, userID, ip string) error
	RecordLoginFailure(ctx context.Context, username string, lockoutThreshold int) error
}

type UserAvatarStore interface {
	GetUserAvatar(ctx context.Context, userID string) (UserAvatar, error)
	GetUserAvatarInfo(ctx context.Context, userID string) (UserAvatarInfo, error)
	UpsertUserAvatar(ctx context.Context, avatar UserAvatar) (UserAvatarInfo, error)
	DeleteUserAvatar(ctx context.Context, userID string) error
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

type EmailChangeStore interface {
	CreateEmailChangeChallenge(ctx context.Context, userID, email string, ttl time.Duration) (EmailChangeChallenge, error)
	GetEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error)
	ConsumeEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error)
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

var ErrUnauthorized = errors.New("unauthorized")
