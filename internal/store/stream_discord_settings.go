package store

import (
	"context"
	"database/sql"
	"strings"
)

// Called only after the stream row is locked, matching the existing current
// credential snapshot order: stream -> service -> assignment -> profile.
func validateExpectedDiscordSettingsTx(ctx context.Context, tx *sql.Tx, streamID string, settings StreamSettings) error {
	if settings.ExpectedDiscordServiceID == nil {
		return nil
	}
	desired := strings.TrimSpace(*settings.ExpectedDiscordServiceID)
	if desired != "" {
		services, err := lockMariaDBServicesSorted(ctx, tx, []string{desired})
		if err != nil {
			return err
		}
		service, ok := services[desired]
		if !ok || service.ServiceType != "discord_bot" {
			return ErrServiceAssignmentConflict
		}
		owner, role, err := consistentMariaDBServiceAssignment(ctx, tx, service)
		if err != nil || owner != streamID || role != "primary" {
			return ErrServiceAssignmentConflict
		}
		profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, settings.DiscordConfigID, string(ProfileDiscordConfig)))
		if err != nil || currentDiscordConfigString(profile.Config, "service_id") != desired {
			return ErrServiceAssignmentConflict
		}
	} else {
		if settings.DiscordConfigID != "" {
			return ErrServiceAssignmentConflict
		}
		rows, err := discoverMariaDBAssignmentsForStream(ctx, tx, streamID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ServiceType == "discord_bot" {
				return ErrServiceAssignmentConflict
			}
		}
	}
	return ctx.Err()
}

// The memory counterpart uses the existing shared profile authority already
// bound during server construction. Keep profiles -> registry -> stream order
// used by CurrentDiscordCredentialStores; the caller takes the stream last.
func (s *MemoryStreamStore) lockExpectedDiscordSettings(ctx context.Context, streamID string, settings StreamSettings) (func(), error) {
	if settings.ExpectedDiscordServiceID == nil {
		return func() {}, nil
	}
	desired := strings.TrimSpace(*settings.ExpectedDiscordServiceID)
	unlockProfile := func() {}
	if desired != "" {
		profiles := s.relayBindingClaimProfiles
		if profiles == nil {
			return nil, ErrServiceAssignmentGuardUnavailable
		}
		profiles.mu.Lock()
		unlockProfile = profiles.mu.Unlock
		profile := profiles.profiles[settings.DiscordConfigID]
		if profile.Kind != ProfileDiscordConfig || currentDiscordConfigString(profile.Config, "service_id") != desired {
			unlockProfile()
			return nil, ErrServiceAssignmentConflict
		}
	} else if settings.DiscordConfigID != "" {
		return nil, ErrServiceAssignmentConflict
	}
	auth := s.serviceAssignmentGuard
	if auth == nil {
		unlockProfile()
		return nil, ErrServiceAssignmentGuardUnavailable
	}
	auth.mu.Lock()
	unlock := func() { auth.mu.Unlock(); unlockProfile() }
	if desired != "" {
		service, ok := auth.services[desired]
		owner, role, err := auth.consistentServiceAssignmentLocked(service)
		if !ok || service.ServiceType != "discord_bot" || err != nil || owner != streamID || role != "primary" {
			unlock()
			return nil, ErrServiceAssignmentConflict
		}
	} else {
		for key := range auth.assignments {
			owner, serviceType, _ := assignmentPartsFromKey(key)
			if owner == streamID && serviceType == "discord_bot" {
				unlock()
				return nil, ErrServiceAssignmentConflict
			}
		}
	}
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
