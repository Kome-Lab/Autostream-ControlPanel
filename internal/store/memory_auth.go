package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type MemoryAuthStore struct {
	mu            sync.Mutex
	users         map[string]User
	byUsername    map[string]string
	roles         map[string]Role
	permissions   map[string][]string
	sessions      map[string]Session
	serviceTokens map[string]ServiceToken
	services      map[string]RegisteredService
	metricHistory []ServiceMetricSnapshot
	assignments   map[string]string
	failedLogins  map[string]int
	mfaConfigs    map[string]MFAConfig
	mfaChallenges map[string]MFAChallenge
	emailChanges  map[string]EmailChangeChallenge
	passkeys      map[string]PasskeyCredential
	passkeyReg    map[string]PasskeyRegistrationChallenge
	passkeySess   map[string]PasskeyCeremonySession
	streamEvents  []ServiceStreamEvent
	auditEvents   []AuditEvent
}

func NewMemoryAuthStore() *MemoryAuthStore {
	return &MemoryAuthStore{
		users: map[string]User{}, byUsername: map[string]string{}, roles: map[string]Role{}, permissions: map[string][]string{}, sessions: map[string]Session{}, serviceTokens: map[string]ServiceToken{}, services: map[string]RegisteredService{}, assignments: map[string]string{}, failedLogins: map[string]int{}, mfaConfigs: map[string]MFAConfig{}, mfaChallenges: map[string]MFAChallenge{}, emailChanges: map[string]EmailChangeChallenge{}, passkeys: map[string]PasskeyCredential{}, passkeyReg: map[string]PasskeyRegistrationChallenge{}, passkeySess: map[string]PasskeyCeremonySession{},
	}
}

func (s *MemoryAuthStore) CountUsers(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users), nil
}

func (s *MemoryAuthStore) CreateFirstAdmin(ctx context.Context, username, password string, permissions []string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	user := User{Username: username, Status: "active", Roles: []string{"super_admin"}}
	if err := s.AddUser(user, password, permissions); err != nil {
		return User{}, err
	}
	return s.FindUserByUsername(ctx, username)
}

func (s *MemoryAuthStore) AddUser(user User, password string, permissions []string) error {
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	if user.ID == "" {
		user.ID = newUUID()
	}
	if user.Status == "" {
		user.Status = "active"
	}
	user.PasswordHash = hash
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user.ID] = user
	s.byUsername[user.Username] = user.ID
	s.permissions[user.ID] = append([]string(nil), permissions...)
	for _, roleName := range user.Roles {
		role := Role{ID: newUUID(), Name: roleName, Permissions: append([]string(nil), permissions...), CreatedAt: time.Now().UTC()}
		s.roles[role.ID] = role
	}
	return nil
}

func (s *MemoryAuthStore) FindUserByUsername(ctx context.Context, username string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byUsername[username]
	if !ok {
		return User{}, ErrNotFound
	}
	return s.users[id], nil
}

func (s *MemoryAuthStore) GetUser(ctx context.Context, id string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (s *MemoryAuthStore) GetUserPermissions(ctx context.Context, id string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.permissions[id]...), nil
}

func (s *MemoryAuthStore) ChangePassword(ctx context.Context, id, newPassword string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = hash
	user.Status = "active"
	s.users[id] = user
	return nil
}

func (s *MemoryAuthStore) CreateSession(ctx context.Context, userID string, idleTTL, absoluteTTL time.Duration) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := security.RandomToken(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	session := Session{Token: raw, TokenHash: security.HashToken(raw), CSRFToken: csrf, CSRFTokenHash: security.HashToken(csrf), UserID: userID, IdleExpiresAt: now.Add(idleTTL), AbsoluteExpiresAt: now.Add(absoluteTTL)}
	s.mu.Lock()
	s.sessions[session.TokenHash] = session
	s.mu.Unlock()
	return session, nil
}

func (s *MemoryAuthStore) GetSession(ctx context.Context, rawToken string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[security.HashToken(rawToken)]
	if !ok || time.Now().UTC().After(session.IdleExpiresAt) || time.Now().UTC().After(session.AbsoluteExpiresAt) {
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryAuthStore) DeleteSession(ctx context.Context, rawToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessions, security.HashToken(rawToken))
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) DeleteUserSessions(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, hash)
		}
	}
	return nil
}

func (s *MemoryAuthStore) RecordLoginSuccess(ctx context.Context, userID, ip string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	user := s.users[userID]
	now := time.Now().UTC()
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	s.users[userID] = user
	delete(s.failedLogins, user.Username)
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) RecordLoginFailure(ctx context.Context, username string, lockoutThreshold int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lockoutThreshold < 1 {
		lockoutThreshold = defaultSecurityConfig.LoginLockoutThreshold
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byUsername[username]
	if !ok {
		return nil
	}
	s.failedLogins[username]++
	if s.failedLogins[username] >= lockoutThreshold {
		user := s.users[id]
		if user.Status == "active" {
			user.Status = "locked"
			s.users[id] = user
		}
	}
	return nil
}

func (s *MemoryAuthStore) GetMFAConfig(ctx context.Context, userID string) (MFAConfig, error) {
	if err := ctx.Err(); err != nil {
		return MFAConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.mfaConfigs[userID]
	cfg.UserID = userID
	cfg.RecoveryCodeHashes = append([]string(nil), cfg.RecoveryCodeHashes...)
	return cfg, nil
}

func (s *MemoryAuthStore) StartTOTPEnrollment(ctx context.Context, userID, secret string, recoveryCodeHashes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.mfaConfigs[userID]
	cfg.UserID = userID
	cfg.PendingTOTPSecret = secret
	cfg.RecoveryCodeHashes = append([]string(nil), recoveryCodeHashes...)
	cfg.UpdatedAt = time.Now().UTC()
	s.mfaConfigs[userID] = cfg
	return nil
}

func (s *MemoryAuthStore) ConfirmTOTPEnrollment(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.mfaConfigs[userID]
	if cfg.PendingTOTPSecret == "" {
		return ErrNotFound
	}
	cfg.UserID = userID
	cfg.TOTPSecret = cfg.PendingTOTPSecret
	cfg.PendingTOTPSecret = ""
	cfg.Enabled = true
	cfg.UpdatedAt = time.Now().UTC()
	s.mfaConfigs[userID] = cfg
	return nil
}

func (s *MemoryAuthStore) DisableMFA(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.mfaConfigs, userID)
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) RegenerateRecoveryCodes(ctx context.Context, userID string, recoveryCodeHashes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.mfaConfigs[userID]
	if !cfg.Enabled {
		return ErrNotFound
	}
	cfg.RecoveryCodeHashes = append([]string(nil), recoveryCodeHashes...)
	cfg.UpdatedAt = time.Now().UTC()
	s.mfaConfigs[userID] = cfg
	return nil
}

func (s *MemoryAuthStore) ConsumeRecoveryCode(ctx context.Context, userID, recoveryCodeHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.mfaConfigs[userID]
	next := make([]string, 0, len(cfg.RecoveryCodeHashes))
	found := false
	for _, hash := range cfg.RecoveryCodeHashes {
		if hash == recoveryCodeHash {
			found = true
			continue
		}
		next = append(next, hash)
	}
	if !found {
		return ErrUnauthorized
	}
	cfg.RecoveryCodeHashes = next
	cfg.UpdatedAt = time.Now().UTC()
	s.mfaConfigs[userID] = cfg
	return nil
}

func (s *MemoryAuthStore) CreateMFAChallenge(ctx context.Context, userID string, ttl time.Duration) (MFAChallenge, error) {
	if err := ctx.Err(); err != nil {
		return MFAChallenge{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return MFAChallenge{}, err
	}
	challenge := MFAChallenge{Token: raw, TokenHash: security.HashToken(raw), UserID: userID, ExpiresAt: time.Now().UTC().Add(ttl)}
	s.mu.Lock()
	s.mfaChallenges[challenge.TokenHash] = challenge
	s.mu.Unlock()
	return challenge, nil
}

func (s *MemoryAuthStore) GetMFAChallenge(ctx context.Context, rawToken string) (MFAChallenge, error) {
	if err := ctx.Err(); err != nil {
		return MFAChallenge{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.mfaChallenges[security.HashToken(rawToken)]
	if !ok || time.Now().UTC().After(challenge.ExpiresAt) {
		return MFAChallenge{}, ErrNotFound
	}
	challenge.Token = rawToken
	return challenge, nil
}

func (s *MemoryAuthStore) DeleteMFAChallenge(ctx context.Context, rawToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.mfaChallenges, security.HashToken(rawToken))
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) CreateEmailChangeChallenge(ctx context.Context, userID, email string, ttl time.Duration) (EmailChangeChallenge, error) {
	if err := ctx.Err(); err != nil {
		return EmailChangeChallenge{}, err
	}
	challenge, err := newEmailChangeChallenge(userID, email, ttl)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[challenge.UserID]; !ok {
		return EmailChangeChallenge{}, ErrNotFound
	}
	for hash, existing := range s.emailChanges {
		if existing.UserID == challenge.UserID {
			delete(s.emailChanges, hash)
		}
	}
	s.emailChanges[challenge.TokenHash] = challenge
	return challenge, nil
}

func (s *MemoryAuthStore) GetEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error) {
	if err := ctx.Err(); err != nil {
		return EmailChangeChallenge{}, err
	}
	hash := security.HashToken(strings.TrimSpace(rawToken))
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.emailChanges[hash]
	if !ok || time.Now().UTC().After(challenge.ExpiresAt) {
		delete(s.emailChanges, hash)
		return EmailChangeChallenge{}, ErrNotFound
	}
	challenge.Token = rawToken
	return challenge, nil
}

func (s *MemoryAuthStore) ConsumeEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error) {
	if err := ctx.Err(); err != nil {
		return EmailChangeChallenge{}, err
	}
	hash := security.HashToken(strings.TrimSpace(rawToken))
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.emailChanges[hash]
	delete(s.emailChanges, hash)
	if !ok || time.Now().UTC().After(challenge.ExpiresAt) {
		return EmailChangeChallenge{}, ErrNotFound
	}
	challenge.Token = rawToken
	return challenge, nil
}

func (s *MemoryAuthStore) CreatePasskeyRegistrationChallenge(ctx context.Context, userID, rpID, rpName, userName, userDisplayName string, ttl time.Duration) (PasskeyRegistrationChallenge, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	challenge, err := newPasskeyRegistrationChallenge(userID, rpID, rpName, userName, userDisplayName, ttl)
	if err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	s.mu.Lock()
	s.passkeyReg[challenge.TokenHash] = challenge
	s.mu.Unlock()
	return challenge, nil
}

func (s *MemoryAuthStore) GetPasskeyRegistrationChallenge(ctx context.Context, rawToken string) (PasskeyRegistrationChallenge, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyRegistrationChallenge{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.passkeyReg[security.HashToken(strings.TrimSpace(rawToken))]
	if !ok || time.Now().UTC().After(challenge.ExpiresAt) {
		return PasskeyRegistrationChallenge{}, ErrNotFound
	}
	challenge.Token = rawToken
	return challenge, nil
}

func (s *MemoryAuthStore) DeletePasskeyRegistrationChallenge(ctx context.Context, rawToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.passkeyReg, security.HashToken(strings.TrimSpace(rawToken)))
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) CreatePasskeyCeremonySession(ctx context.Context, userID, ceremony string, sessionJSON []byte, ttl time.Duration) (PasskeyCeremonySession, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyCeremonySession{}, err
	}
	session, err := newPasskeyCeremonySession(userID, ceremony, sessionJSON, ttl)
	if err != nil {
		return PasskeyCeremonySession{}, err
	}
	s.mu.Lock()
	s.passkeySess[session.TokenHash] = session
	s.mu.Unlock()
	return session, nil
}

func (s *MemoryAuthStore) GetPasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyCeremonySession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.passkeySess[security.HashToken(strings.TrimSpace(rawToken))]
	if !ok || time.Now().UTC().After(session.ExpiresAt) || strings.TrimSpace(session.Ceremony) != strings.TrimSpace(ceremony) {
		return PasskeyCeremonySession{}, ErrNotFound
	}
	session.Token = rawToken
	return session, nil
}

func (s *MemoryAuthStore) ConsumePasskeyCeremonySession(ctx context.Context, rawToken, ceremony string) (PasskeyCeremonySession, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyCeremonySession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := security.HashToken(strings.TrimSpace(rawToken))
	session, ok := s.passkeySess[hash]
	if !ok {
		return PasskeyCeremonySession{}, ErrNotFound
	}
	delete(s.passkeySess, hash)
	if time.Now().UTC().After(session.ExpiresAt) || strings.TrimSpace(session.Ceremony) != strings.TrimSpace(ceremony) {
		return PasskeyCeremonySession{}, ErrNotFound
	}
	session.Token = rawToken
	return session, nil
}

func (s *MemoryAuthStore) DeletePasskeyCeremonySession(ctx context.Context, rawToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.passkeySess, security.HashToken(strings.TrimSpace(rawToken)))
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) ListPasskeyCredentials(ctx context.Context, userID string) ([]PasskeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PasskeyCredential{}
	for _, credential := range s.passkeys {
		if credential.UserID == userID {
			out = append(out, publicPasskeyCredential(credential))
		}
	}
	return out, nil
}

func (s *MemoryAuthStore) ListPasskeyCredentialsForVerification(ctx context.Context, userID string) ([]PasskeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PasskeyCredential{}
	for _, credential := range s.passkeys {
		if credential.UserID == userID {
			out = append(out, credential)
		}
	}
	return out, nil
}

func (s *MemoryAuthStore) CreatePasskeyCredential(ctx context.Context, credential PasskeyCredential) (PasskeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyCredential{}, err
	}
	credential, err := normalizePasskeyCredential(credential)
	if err != nil {
		return PasskeyCredential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.passkeys {
		if existing.CredentialIDHash == credential.CredentialIDHash {
			return PasskeyCredential{}, errors.New("passkey credential already exists")
		}
	}
	s.passkeys[credential.ID] = credential
	return publicPasskeyCredential(credential), nil
}

func (s *MemoryAuthStore) FindPasskeyCredentialByCredentialID(ctx context.Context, credentialID []byte) (PasskeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return PasskeyCredential{}, err
	}
	hash := passkeyCredentialIDHash(credentialID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, credential := range s.passkeys {
		if credential.CredentialIDHash == hash {
			return credential, nil
		}
	}
	return PasskeyCredential{}, ErrNotFound
}

func (s *MemoryAuthStore) UpdatePasskeySignCount(ctx context.Context, id string, signCount uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.passkeys[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	credential.SignCount = signCount
	credential.LastUsedAt = &now
	credential.UpdatedAt = now
	s.passkeys[id] = credential
	return nil
}

func (s *MemoryAuthStore) DeletePasskeyCredential(ctx context.Context, userID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.passkeys[id]
	if !ok || credential.UserID != userID {
		return ErrNotFound
	}
	delete(s.passkeys, id)
	return nil
}

func (s *MemoryAuthStore) WriteAudit(ctx context.Context, event AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event = redactedAuditEvent(event)
	if event.ID == "" {
		event.ID = newUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.mu.Lock()
	s.auditEvents = append(s.auditEvents, event)
	s.mu.Unlock()
	return nil
}

func (s *MemoryAuthStore) AuditEvents() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, 0, len(s.auditEvents))
	for _, event := range s.auditEvents {
		out = append(out, redactedAuditEvent(event))
	}
	return out
}

func (s *MemoryAuthStore) ListAudit(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	filter = normalizeAuditFilter(filter)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, 0, len(s.auditEvents))
	for i := len(s.auditEvents) - 1; i >= 0 && len(out) < filter.Limit; i-- {
		if auditEventMatchesFilter(s.auditEvents[i], filter) {
			out = append(out, redactedAuditEvent(s.auditEvents[i]))
		}
	}
	return out, nil
}

func auditEventMatchesFilter(event AuditEvent, filter AuditFilter) bool {
	if len(filter.Actions) > 0 {
		matched := false
		for _, action := range filter.Actions {
			if event.Action == action {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if filter.Result != "" && event.Result != filter.Result {
		return false
	}
	if !filter.From.IsZero() && event.Timestamp.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && !event.Timestamp.Before(filter.To) {
		return false
	}
	if filter.Query == "" {
		return true
	}
	metadata, _ := json.Marshal(event.Metadata)
	haystack := strings.ToLower(strings.Join([]string{
		event.Action,
		event.ActorUsername,
		event.ResourceType,
		event.ResourceID,
		event.Result,
		string(metadata),
	}, " "))
	return strings.Contains(haystack, strings.ToLower(filter.Query))
}
