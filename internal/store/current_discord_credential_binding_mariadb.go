package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func (stores CurrentDiscordCredentialStores) currentDiscordMariaDB(ctx context.Context, auth MariaDBAuthStore, serviceID, tokenID string, expected *CurrentDiscordCredentialBinding, ttl time.Duration) (CurrentDiscordCredentialSnapshot, string, RuntimeSecretLease, error) {
	streams, streamsOK := stores.Streams.(MariaDBStreamStore)
	profiles, profilesOK := stores.Profiles.(MariaDBProfileStore)
	secrets, secretsOK := stores.Secrets.(MariaDBSecretStore)
	if !streamsOK || !profilesOK || !secretsOK || auth.db != streams.db || auth.db != profiles.db || auth.db != secrets.db {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
	}
	if expected != nil {
		leases, ok := stores.Leases.(MariaDBRuntimeSecretLeaseStore)
		if !ok || leases.db != auth.db {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
		}
	}
	discoveredService, err := auth.getService(ctx, serviceID)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, auth.db, serviceID)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	streamIDs := []string{discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := auth.db.BeginTx(ctx, nil)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{serviceID})
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	service, ok := lockedServices[serviceID]
	if !ok || service.CurrentStreamID != discoveredService.CurrentStreamID || service.ServiceType != discoveredService.ServiceType {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	rows, err := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if !mariaDBAssignmentRowsEqual(rows, discoveredRows) {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
	}
	if _, _, err := consistentMariaDBServiceAssignment(ctx, tx, service); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	snapshot := CurrentDiscordCredentialSnapshot{Service: service, Assignments: []StreamServiceAssignment{}, Bindings: []CurrentDiscordCredentialBinding{}}
	for _, row := range rows {
		snapshot.Assignments = append(snapshot.Assignments, StreamServiceAssignment{StreamID: row.StreamID, ServiceID: row.ServiceID, ServiceType: row.ServiceType, AssignmentRole: row.AssignmentRole, AssignedAt: row.AssignedAt})
	}
	sortCurrentDiscordAssignments(snapshot.Assignments)
	if err := validateCurrentDiscordAssignments(service, snapshot.Assignments); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if len(snapshot.Assignments) != 0 {
		snapshot.Service.AssignmentRole = snapshot.Assignments[0].AssignmentRole
	}
	type secretRow struct{ ciphertext, nonce string }
	values := map[string]secretRow{}
	for _, assignment := range snapshot.Assignments {
		if assignment.AssignmentRole != "primary" {
			continue
		}
		stream, ok := lockedStreams[assignment.StreamID]
		if !ok || stream.DeletedAt != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
		}
		// UpdateStreamSettings takes this same stream row first. The subsequent
		// settings/profile/secret reads therefore belong to this assignment view.
		var profileID string
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(discord_config_id, '') FROM stream_settings WHERE stream_id = ? FOR UPDATE`, assignment.StreamID).Scan(&profileID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		profile := Profile{}
		if profileID != "" {
			profile, err = scanProfile(tx.QueryRowContext(ctx, `SELECT id, kind, name, config, created_at, updated_at, youtube_relay_binding_revision FROM profiles WHERE id = ? AND kind = ? FOR UPDATE`, profileID, string(ProfileDiscordConfig)))
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
			}
		}
		status := SecretStatus{}
		reference := currentDiscordConfigString(profile.Config, "bot_token_secret_name")
		if reference != "" && currentDiscordSecretName(reference) && currentDiscordConfigString(profile.Config, "service_id") == serviceID {
			var material secretRow
			err = tx.QueryRowContext(ctx, `SELECT value_hash, ciphertext, nonce FROM secrets WHERE name = ? FOR UPDATE`, reference).Scan(&status.Fingerprint, &material.ciphertext, &material.nonce)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
			}
			if err == nil && material.ciphertext != "" && material.nonce != "" {
				status.Name, status.Configured = reference, true
				values[reference] = material
			}
		}
		binding := currentDiscordBinding(assignment, strings.TrimSpace(profileID), profile, status)
		snapshot.Bindings = append(snapshot.Bindings, binding)
		if binding.Ready {
			snapshot.Profiles = append(snapshot.Profiles, copyProfile(profile))
		}
	}
	// Service-token rotation/revocation also locks service before token. Do not
	// use the token observed by the HTTP handler as a current store authority.
	token, err := selectActiveServiceTokenForUpdate(ctx, tx, tokenID)
	if errors.Is(err, ErrNotFound) {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
	}
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if !currentDiscordServiceTokenValid(service, token, serviceID, tokenID, expected != nil) {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
	}
	var value string
	var lease RuntimeSecretLease
	if expected != nil {
		if err := validateExpectedCurrentDiscordBinding(snapshot, *expected); err != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		material, ok := values[expected.SecretName]
		if !ok {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrNotFound
		}
		lease, err = claimRuntimeSecretLeaseTx(ctx, tx, RuntimeSecretLease{ServiceID: serviceID, TokenID: tokenID, SecretName: expected.SecretName}, ttl)
		if err != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		value, err = security.DecryptSecret(material.ciphertext, material.nonce, secrets.keyMaterial)
		if err != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		if value == "" || security.SecretFingerprint(value) != expected.Fingerprint {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
		}
	}
	if err := tx.Commit(); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	return snapshot, value, lease, nil
}
