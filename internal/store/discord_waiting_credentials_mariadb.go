package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type discordWaitingQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func discordWaitingSelections(ctx context.Context, q discordWaitingQueryer, serviceID string) ([]StreamNodeSelectionBinding, error) {
	rows, err := q.QueryContext(ctx, `SELECT n.stream_id,n.service_id,n.service_type,COALESCE(ss.updated_at,n.updated_at)
FROM stream_node_selections n
JOIN streams s ON s.id=n.stream_id AND s.deleted_at IS NULL
LEFT JOIN stream_settings ss ON ss.stream_id=s.id
WHERE n.service_id=? AND n.service_type='discord_bot'
ORDER BY n.stream_id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []StreamNodeSelectionBinding
	for rows.Next() {
		var selection StreamNodeSelectionBinding
		if err := rows.Scan(&selection.StreamID, &selection.ServiceID, &selection.ServiceType, &selection.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, selection)
	}
	return result, rows.Err()
}

func equalDiscordWaitingSelections(a, b []StreamNodeSelectionBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].StreamID != b[i].StreamID || a[i].ServiceID != b[i].ServiceID ||
			a[i].ServiceType != b[i].ServiceType || !a[i].UpdatedAt.Equal(b[i].UpdatedAt) {
			return false
		}
	}
	return true
}

func (stores CurrentDiscordCredentialStores) discordWaitingMariaDB(ctx context.Context, auth MariaDBAuthStore, serviceID, tokenID string, expected *DiscordWaitingCredentialBinding, ttl time.Duration) (DiscordWaitingCredentialSnapshot, string, RuntimeSecretLease, error) {
	streams, streamsOK := stores.Streams.(MariaDBStreamStore)
	profiles, profilesOK := stores.Profiles.(MariaDBProfileStore)
	secrets, secretsOK := stores.Secrets.(MariaDBSecretStore)
	if !streamsOK || !profilesOK || !secretsOK || auth.db != streams.db || auth.db != profiles.db || auth.db != secrets.db {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
	}
	if expected != nil {
		leases, ok := stores.Leases.(MariaDBRuntimeSecretLeaseStore)
		if !ok || leases.db != auth.db {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
		}
	}
	discoveredService, err := auth.getService(ctx, serviceID)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	discoveredAssignments, err := discoverMariaDBAssignmentsForService(ctx, auth.db, serviceID)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	discoveredSelections, err := discordWaitingSelections(ctx, auth.db, serviceID)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	streamIDs := []string{discoveredService.CurrentStreamID}
	for _, assignment := range discoveredAssignments {
		streamIDs = append(streamIDs, assignment.StreamID)
	}
	for _, selection := range discoveredSelections {
		streamIDs = append(streamIDs, selection.StreamID)
	}
	tx, err := auth.db.BeginTx(ctx, nil)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{serviceID})
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	service, exists := lockedServices[serviceID]
	if !exists || service.CurrentStreamID != discoveredService.CurrentStreamID || service.ServiceType != "discord_bot" {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredAssignments); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	actualAssignments, err := discoverMariaDBAssignmentsForService(ctx, tx, serviceID)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if !mariaDBAssignmentRowsEqual(actualAssignments, discoveredAssignments) {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
	}
	if _, _, err := consistentMariaDBServiceAssignment(ctx, tx, service); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	selections, err := discordWaitingSelections(ctx, tx, serviceID)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	// A new selection is never locked after the registry row. Retry discovery
	// instead of inverting the stream -> service lock order.
	if !equalDiscordWaitingSelections(discoveredSelections, selections) {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
	}
	var eligible []StreamNodeSelectionBinding
	var profileIDs []string
	resolvedStreams := make(map[string]Stream, len(selections))
	for _, selection := range selections {
		if _, exists := lockedStreams[selection.StreamID]; !exists {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
		}
		stream, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id = ?"), selection.StreamID))
		if err != nil {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		resolvedStreams[selection.StreamID] = stream
		if !discordWaitingStreamEligible(stream, serviceID) {
			continue
		}
		eligible = append(eligible, selection)
		profileIDs = append(profileIDs, stream.DiscordConfigID)
	}
	profilesByID := map[string]Profile{}
	for _, id := range sortedUniqueStrings(profileIDs) {
		profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, id, string(ProfileDiscordConfig)))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		if err == nil {
			profilesByID[id] = profile
		}
	}
	type waitingSecret struct {
		status     SecretStatus
		ciphertext string
		nonce      string
	}
	materials := map[string]waitingSecret{}
	var secretNames []string
	for _, profile := range profilesByID {
		name := currentDiscordConfigString(profile.Config, "bot_token_secret_name")
		if currentDiscordConfigString(profile.Config, "service_id") == serviceID && currentDiscordSecretName(name) {
			secretNames = append(secretNames, name)
		}
	}
	for _, name := range sortedUniqueStrings(secretNames) {
		var material waitingSecret
		err := tx.QueryRowContext(ctx, `SELECT value_hash,ciphertext,nonce FROM secrets WHERE name=? FOR UPDATE`, name).Scan(&material.status.Fingerprint, &material.ciphertext, &material.nonce)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		if err == nil && material.ciphertext != "" && material.nonce != "" {
			material.status.Name, material.status.Configured = name, true
			materials[name] = material
		}
	}
	token, err := selectActiveServiceTokenForUpdate(ctx, tx, tokenID)
	if errors.Is(err, ErrNotFound) {
		err = ErrForbidden
	}
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if !currentDiscordServiceTokenValid(service, token, serviceID, tokenID, expected != nil) {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
	}
	snapshot := DiscordWaitingCredentialSnapshot{Service: service, Bindings: []DiscordWaitingCredentialBinding{}}
	seenProfiles := map[string]bool{}
	for _, selection := range eligible {
		stream := resolvedStreams[selection.StreamID]
		profile := profilesByID[stream.DiscordConfigID]
		material := materials[currentDiscordConfigString(profile.Config, "bot_token_secret_name")]
		binding := discordWaitingBinding(stream, serviceID, selection.UpdatedAt, profile, material.status)
		snapshot.Bindings = append(snapshot.Bindings, binding)
		if binding.Ready && !seenProfiles[profile.ID] {
			snapshot.Profiles = append(snapshot.Profiles, copyProfile(profile))
			seenProfiles[profile.ID] = true
		}
	}
	sort.Slice(snapshot.Bindings, func(i, j int) bool { return snapshot.Bindings[i].StreamID < snapshot.Bindings[j].StreamID })
	var value string
	var lease RuntimeSecretLease
	if expected != nil {
		if err := validateExpectedDiscordWaitingBinding(snapshot.Bindings, *expected); err != nil {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		material, exists := materials[expected.SecretName]
		if !exists {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrNotFound
		}
		lease, err = claimRuntimeSecretLeaseTx(ctx, tx, RuntimeSecretLease{ServiceID: serviceID, TokenID: tokenID, SecretName: expected.SecretName}, ttl)
		if err != nil {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		value, err = security.DecryptSecret(material.ciphertext, material.nonce, secrets.keyMaterial)
		if err != nil {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		if value == "" || security.SecretFingerprint(value) != expected.Fingerprint {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
		}
	}
	if err := tx.Commit(); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	return snapshot, value, lease, nil
}
