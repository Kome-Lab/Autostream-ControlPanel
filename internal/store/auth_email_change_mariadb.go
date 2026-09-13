package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s MariaDBAuthStore) CreateEmailChangeChallenge(ctx context.Context, userID, email string, ttl time.Duration) (EmailChangeChallenge, error) {
	challenge, err := newEmailChangeChallenge(userID, email, ttl)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO email_change_challenges (id, user_id, email, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		challenge.TokenHash, challenge.UserID, challenge.Email, challenge.ExpiresAt, challenge.CreatedAt)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	return challenge, nil
}

func (s MariaDBAuthStore) GetEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error) {
	hash := security.HashToken(strings.TrimSpace(rawToken))
	var challenge EmailChangeChallenge
	challenge.Token = rawToken
	challenge.TokenHash = hash
	err := s.db.QueryRowContext(ctx, `SELECT user_id, email, expires_at, created_at FROM email_change_challenges WHERE id = ?`, hash).Scan(&challenge.UserID, &challenge.Email, &challenge.ExpiresAt, &challenge.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailChangeChallenge{}, ErrNotFound
	}
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	if time.Now().UTC().After(challenge.ExpiresAt) {
		_ = s.deleteEmailChangeChallenge(ctx, rawToken)
		return EmailChangeChallenge{}, ErrNotFound
	}
	return challenge, nil
}

func (s MariaDBAuthStore) ConsumeEmailChangeChallenge(ctx context.Context, rawToken string) (EmailChangeChallenge, error) {
	hash := security.HashToken(strings.TrimSpace(rawToken))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	defer tx.Rollback()
	var challenge EmailChangeChallenge
	challenge.Token = rawToken
	challenge.TokenHash = hash
	err = tx.QueryRowContext(ctx, `SELECT user_id, email, expires_at, created_at FROM email_change_challenges WHERE id = ? FOR UPDATE`, hash).Scan(&challenge.UserID, &challenge.Email, &challenge.ExpiresAt, &challenge.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailChangeChallenge{}, ErrNotFound
	}
	if err != nil {
		return EmailChangeChallenge{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_change_challenges WHERE id = ?`, hash); err != nil {
		return EmailChangeChallenge{}, err
	}
	if err := tx.Commit(); err != nil {
		return EmailChangeChallenge{}, err
	}
	if time.Now().UTC().After(challenge.ExpiresAt) {
		return EmailChangeChallenge{}, ErrNotFound
	}
	return challenge, nil
}

func (s MariaDBAuthStore) deleteEmailChangeChallenge(ctx context.Context, rawToken string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM email_change_challenges WHERE id = ?`, security.HashToken(strings.TrimSpace(rawToken)))
	return err
}
