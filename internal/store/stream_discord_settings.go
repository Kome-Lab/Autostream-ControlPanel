package store

import (
	"context"
	"database/sql"
)

// Settings validate saved choices and their authentication profile. Current
// execution ownership belongs exclusively to start/stop and runtime authority.
func validateExpectedDiscordSettingsTx(ctx context.Context, tx *sql.Tx, streamID string, settings StreamSettings) error {
	return validateStreamNodeSelectionsTx(ctx, tx, streamID, settings)
}

// Memory credential snapshots use profiles -> registry -> stream order.
// The caller acquires the stream last, then revalidates the effective choices.
func (s *MemoryStreamStore) lockExpectedDiscordSettings(ctx context.Context, streamID string, settings StreamSettings) (func(), error) {
	if !streamNodeSelectionsChanged(settings) {
		return func() {}, nil
	}
	unlockProfile := func() {}
	if profiles := s.relayBindingClaimProfiles; profiles != nil {
		profiles.mu.Lock()
		unlockProfile = profiles.mu.Unlock
	}
	auth := s.serviceAssignmentGuard
	if auth == nil {
		unlockProfile()
		return nil, ErrServiceAssignmentGuardUnavailable
	}
	auth.mu.Lock()
	unlock := func() { auth.mu.Unlock(); unlockProfile() }
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
