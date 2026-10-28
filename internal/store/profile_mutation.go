package store

import (
	"context"
	"errors"
)

var (
	ErrAtomicProfileUpdateUnavailable = errors.New("atomic profile update authority unavailable")
	ErrInvalidProfileSecretMutation   = errors.New("invalid profile secret mutation")
)

// SecretWrite is transient mutation input, never a read response, log or audit.
type SecretWrite struct{ Name, Value string }

type ProfileUpdate struct {
	Name              string
	Config            map[string]any
	ExpectedUpdatedAt *string
	SecretChanges     []SecretWrite
}

type ProfileRevisionStore interface {
	UpdateProfileRevision(context.Context, ProfileKind, string, ProfileUpdate, SecretStore) (Profile, error)
}

// Older test/adaptor stores retain their no-CAS, no-secret operation. A request
// requiring atomic authority never falls back to independent secret/profile writes.
func UpdateProfileWithRevision(ctx context.Context, profiles ProfileStore, secrets SecretStore, kind ProfileKind, id string, input ProfileUpdate) (Profile, error) {
	if authority, ok := profiles.(ProfileRevisionStore); ok {
		return authority.UpdateProfileRevision(ctx, kind, id, input, secrets)
	}
	if input.ExpectedUpdatedAt != nil || len(input.SecretChanges) > 0 {
		return Profile{}, ErrAtomicProfileUpdateUnavailable
	}
	return profiles.UpdateProfile(ctx, kind, id, input.Name, input.Config)
}

func validateProfileSecretWrites(changes []SecretWrite) error {
	seen := map[string]bool{}
	for _, change := range changes {
		if !allowedSecretName(change.Name) {
			return ErrUnknownSecret
		}
		if seen[change.Name] {
			return ErrInvalidProfileSecretMutation
		}
		seen[change.Name] = true
	}
	return nil
}
