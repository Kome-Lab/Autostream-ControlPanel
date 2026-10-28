package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// UpdateProfileRevision keeps the profile compare-and-swap and its secret
// writes under one memory authority. Failed validation never changes either.
func (s *MemoryProfileStore) UpdateProfileRevision(ctx context.Context, kind ProfileKind, id string, input ProfileUpdate, secrets SecretStore) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := validateExpectedResourceUpdatedAt(input.ExpectedUpdatedAt); err != nil {
		return Profile{}, err
	}
	if err := validateProfileInput(kind, input.Name, input.Config); err != nil {
		return Profile{}, err
	}
	if err := validateProfileSecretWrites(input.SecretChanges); err != nil {
		return Profile{}, err
	}
	var memorySecrets *MemorySecretStore
	if len(input.SecretChanges) != 0 {
		var ok bool
		memorySecrets, ok = secrets.(*MemorySecretStore)
		if !ok || memorySecrets == nil {
			return Profile{}, ErrAtomicProfileUpdateUnavailable
		}
	}
	// Relay reservations already take output -> profile -> stream. Acquire
	// that same fence before the profile lock, and check it before secrets.
	unlockOutput := s.lockRelayBindingOutputMutation(kind)
	defer unlockOutput()
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || profile.Kind != kind {
		return Profile{}, ErrNotFound
	}
	if err := matchExpectedResourceUpdatedAt(input.ExpectedUpdatedAt, profile.UpdatedAt); err != nil {
		return Profile{}, err
	}
	if err := s.requireUnclaimedYouTubeOutputLocked(ctx, kind, id); err != nil {
		return Profile{}, err
	}
	for existingID, existing := range s.profiles {
		if existingID != id && existing.Kind == kind && existing.Name == strings.TrimSpace(input.Name) {
			return Profile{}, errors.New("profile name already exists")
		}
	}
	if memorySecrets != nil {
		// Current Discord binding snapshots use profile -> auth -> stream ->
		// secrets. This writer never acquires auth/stream after secrets.
		memorySecrets.mu.Lock()
		defer memorySecrets.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	now := nextResourceUpdatedAt(profile.UpdatedAt, time.Now().UTC())
	for _, change := range input.SecretChanges {
		memorySecrets.updateSecretLocked(change.Name, change.Value, now)
	}
	profile.Name = strings.TrimSpace(input.Name)
	profile.Config = copyConfig(input.Config)
	profile.UpdatedAt = now
	if kind == ProfileYouTubeOutput {
		profile.YouTubeRelayBindingRevision++
	}
	s.profiles[id] = profile
	return copyProfile(profile), nil
}
