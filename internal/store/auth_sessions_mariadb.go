package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"time"
)

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

func (s MariaDBAuthStore) RefreshSession(ctx context.Context, rawToken string, idleTTL time.Duration) (Session, error) {
	session, err := s.GetSession(ctx, rawToken)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	nextIdleExpiry := now.Add(idleTTL)
	if nextIdleExpiry.After(session.AbsoluteExpiresAt) {
		nextIdleExpiry = session.AbsoluteExpiresAt
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET idle_expires_at = ? WHERE id = ? AND idle_expires_at > ? AND absolute_expires_at > ?`, nextIdleExpiry, session.TokenHash, now, now)
	if err != nil {
		return Session{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Session{}, err
	}
	if affected == 0 {
		return Session{}, ErrNotFound
	}
	session.IdleExpiresAt = nextIdleExpiry
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

func (s MariaDBAuthStore) GetUserAvatar(ctx context.Context, userID string) (UserAvatar, error) {
	var avatar UserAvatar
	err := s.db.QueryRowContext(ctx, `SELECT user_id, content_type, image_data, fingerprint, updated_at FROM user_avatars WHERE user_id = ?`, userID).
		Scan(&avatar.UserID, &avatar.ContentType, &avatar.Data, &avatar.Fingerprint, &avatar.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserAvatar{}, ErrNotFound
	}
	if err != nil {
		return UserAvatar{}, err
	}
	return avatar, nil
}

func (s MariaDBAuthStore) GetUserAvatarInfo(ctx context.Context, userID string) (UserAvatarInfo, error) {
	var info UserAvatarInfo
	err := s.db.QueryRowContext(ctx, `SELECT user_id, content_type, OCTET_LENGTH(image_data), fingerprint, updated_at FROM user_avatars WHERE user_id = ?`, userID).
		Scan(&info.UserID, &info.ContentType, &info.SizeBytes, &info.Fingerprint, &info.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserAvatarInfo{}, ErrNotFound
	}
	if err != nil {
		return UserAvatarInfo{}, err
	}
	return info, nil
}

func (s MariaDBAuthStore) UpsertUserAvatar(ctx context.Context, avatar UserAvatar) (UserAvatarInfo, error) {
	if avatar.UpdatedAt.IsZero() {
		avatar.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_avatars (user_id, content_type, image_data, fingerprint, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE content_type = VALUES(content_type), image_data = VALUES(image_data), fingerprint = VALUES(fingerprint), updated_at = VALUES(updated_at)`, avatar.UserID, avatar.ContentType, avatar.Data, avatar.Fingerprint, avatar.UpdatedAt)
	if err != nil {
		return UserAvatarInfo{}, err
	}
	return UserAvatarInfo{UserID: avatar.UserID, ContentType: avatar.ContentType, SizeBytes: int64(len(avatar.Data)), Fingerprint: avatar.Fingerprint, UpdatedAt: avatar.UpdatedAt}, nil
}

func (s MariaDBAuthStore) DeleteUserAvatar(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_avatars WHERE user_id = ?`, userID)
	return err
}
