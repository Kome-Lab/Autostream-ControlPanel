package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s MariaDBProfileStore) UpdateProfileRevision(ctx context.Context, kind ProfileKind, id string, input ProfileUpdate, secrets SecretStore) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validateProfileInput(kind, input.Name, input.Config); err != nil {
		return Profile{}, err
	}
	if err := validateExpectedResourceUpdatedAt(input.ExpectedUpdatedAt); err != nil {
		return Profile{}, err
	}
	if err := validateProfileSecretWrites(input.SecretChanges); err != nil {
		return Profile{}, err
	}
	var secretStore MariaDBSecretStore
	if len(input.SecretChanges) > 0 {
		var ok bool
		secretStore, ok = secrets.(MariaDBSecretStore)
		if !ok || secretStore.db != s.db {
			return Profile{}, ErrAtomicProfileUpdateUnavailable
		}
	}
	body, err := json.Marshal(input.Config)
	if err != nil {
		return Profile{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, id, string(kind)))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	if err = matchExpectedResourceUpdatedAt(input.ExpectedUpdatedAt, profile.UpdatedAt); err != nil {
		return Profile{}, err
	}
	now := nextResourceUpdatedAt(profile.UpdatedAt, time.Now().UTC())
	query := `UPDATE profiles SET name=?,config=?,updated_at=? WHERE id=? AND kind=?`
	if kind == ProfileYouTubeOutput {
		// The composite claim FK remains the final guard even on another CP
		// process. It rejects a revision change while that output is claimed.
		query = `UPDATE profiles SET name=?,config=?,updated_at=?,youtube_relay_binding_revision=youtube_relay_binding_revision+1 WHERE id=? AND kind=?`
	}
	if _, err = tx.ExecContext(ctx, query, strings.TrimSpace(input.Name), string(body), now, id, string(kind)); err != nil {
		if isYouTubeRelayBindingClaimProfileConstraintError(err) {
			return Profile{}, ErrYouTubeRelayBindingClaimActive
		}
		return Profile{}, err
	}
	for _, change := range input.SecretChanges {
		if _, err = secretStore.UpdateSecretTx(ctx, tx, change.Name, change.Value, now); err != nil {
			return Profile{}, err
		}
	}
	// Return the database row under the same transaction, not an input echo or
	// a post-commit read that could observe a later writer.
	saved, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=?`, id, string(kind)))
	if err != nil {
		return Profile{}, err
	}
	if err = tx.Commit(); err != nil {
		return Profile{}, err
	}
	return saved, nil
}
