package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

// DiscordWaitingCredentialBinding authorizes monitoring and a start intent.
// It never supplies execution, stop, artifact, or assignment authority.
type DiscordWaitingCredentialBinding struct {
	StreamID              string    `json:"stream_id"`
	ServiceID             string    `json:"service_id"`
	ServiceType           string    `json:"service_type"`
	SelectionEpoch        time.Time `json:"selection_epoch"`
	ProfileID             string    `json:"profile_id"`
	ProfileRevision       string    `json:"profile_revision"`
	ProfileOwnerServiceID string    `json:"profile_owner_service_id,omitempty"`
	SecretName            string    `json:"secret_name,omitempty"`
	Fingerprint           string    `json:"fingerprint,omitempty"`
	Ready                 bool      `json:"ready"`
	ReadinessCode         string    `json:"readiness_code,omitempty"`
}

type DiscordWaitingCredentialSnapshot struct {
	Service         RegisteredService
	ExecutionActive bool
	Bindings        []DiscordWaitingCredentialBinding
	Profiles        []Profile
}

var ErrDiscordWaitingBindingUnavailable = errors.New("waiting Discord credential binding unavailable")

func (stores CurrentDiscordCredentialStores) WaitingDiscordSnapshot(ctx context.Context, serviceID, tokenID string) (DiscordWaitingCredentialSnapshot, error) {
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, _, _, err := stores.discordWaitingOperation(ctx, serviceID, tokenID, nil, 0)
		if err == nil || attempt == 1 || ctx.Err() != nil || !errors.Is(err, ErrServiceAssignmentConflict) {
			return snapshot, err
		}
	}
	return DiscordWaitingCredentialSnapshot{}, ErrServiceAssignmentConflict
}

func (stores CurrentDiscordCredentialStores) ResolveWaitingDiscordBinding(ctx context.Context, serviceID, tokenID string, expected DiscordWaitingCredentialBinding, ttl time.Duration) (string, RuntimeSecretLease, error) {
	_, value, lease, err := stores.discordWaitingOperation(ctx, serviceID, tokenID, &expected, ttl)
	return value, lease, err
}

func (stores CurrentDiscordCredentialStores) discordWaitingOperation(ctx context.Context, serviceID, tokenID string, expected *DiscordWaitingCredentialBinding, ttl time.Duration) (DiscordWaitingCredentialSnapshot, string, RuntimeSecretLease, error) {
	if err := ctx.Err(); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	switch auth := stores.Services.(type) {
	case *MemoryAuthStore:
		return stores.discordWaitingMemory(ctx, auth, serviceID, tokenID, expected, ttl)
	case MariaDBAuthStore:
		return stores.discordWaitingMariaDB(ctx, auth, serviceID, tokenID, expected, ttl)
	case *MariaDBAuthStore:
		return stores.discordWaitingMariaDB(ctx, *auth, serviceID, tokenID, expected, ttl)
	default:
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
	}
}

func discordWaitingExecutionActive(status string, known, stopPending bool) bool {
	if !known || stopPending {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "draft", "scheduled", "ready", "completed", "failed":
		return false
	default:
		return true
	}
}

func discordWaitingStreamEligible(stream Stream, serviceID string) bool {
	if stream.DeletedAt != nil || strings.TrimSpace(stream.SelectedDiscordID) != serviceID ||
		strings.TrimSpace(stream.AutoStartTrigger) != "discord_voice_join" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(stream.Status)) {
	case "created", "draft", "scheduled", "ready":
		return true
	default:
		return false
	}
}

func discordWaitingSelectionEpoch(stream Stream) time.Time {
	epoch, err := time.Parse(time.RFC3339Nano, stream.SettingsRevision)
	if err != nil {
		epoch = stream.CreatedAt
	}
	return epoch.UTC()
}

func discordWaitingProfileRevision(profile Profile) string {
	data, err := json.Marshal(struct {
		ID        string
		Kind      ProfileKind
		Config    map[string]any
		UpdatedAt time.Time
	}{profile.ID, profile.Kind, profile.Config, profile.UpdatedAt})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func discordWaitingBinding(stream Stream, serviceID string, epoch time.Time, profile Profile, secret SecretStatus) DiscordWaitingCredentialBinding {
	binding := DiscordWaitingCredentialBinding{
		StreamID: stream.ID, ServiceID: serviceID, ServiceType: "discord_bot",
		SelectionEpoch: epoch.UTC(), ProfileID: strings.TrimSpace(stream.DiscordConfigID),
		ProfileRevision: discordWaitingProfileRevision(profile), ReadinessCode: "waiting_discord_profile_unavailable",
	}
	owner := currentDiscordConfigString(profile.Config, "service_id")
	reference := currentDiscordConfigString(profile.Config, "bot_token_secret_name")
	fingerprint := currentDiscordConfigString(profile.Config, "bot_token_fingerprint")
	if !discordWaitingStreamEligible(stream, serviceID) || epoch.IsZero() ||
		binding.ProfileID == "" || profile.ID != binding.ProfileID || profile.Kind != ProfileDiscordConfig ||
		binding.ProfileRevision == "" || owner != serviceID || reference == "" || fingerprint == "" ||
		!currentDiscordSecretName(reference) || !secret.Configured || secret.Name != reference || secret.Fingerprint != fingerprint {
		return binding
	}
	binding.ProfileOwnerServiceID, binding.SecretName, binding.Fingerprint = owner, reference, fingerprint
	binding.Ready, binding.ReadinessCode = true, ""
	return binding
}

func validateExpectedDiscordWaitingBinding(bindings []DiscordWaitingCredentialBinding, expected DiscordWaitingCredentialBinding) error {
	if !expected.Ready || expected.ReadinessCode != "" || expected.ServiceType != "discord_bot" ||
		expected.StreamID == "" || expected.ServiceID == "" || expected.ProfileID == "" || expected.SelectionEpoch.IsZero() ||
		expected.ProfileOwnerServiceID != expected.ServiceID || expected.ProfileRevision == "" ||
		!currentDiscordSecretName(expected.SecretName) || expected.Fingerprint == "" {
		return ErrForbidden
	}
	for _, actual := range bindings {
		if actual.Ready && actual.StreamID == expected.StreamID && actual.ServiceID == expected.ServiceID &&
			actual.ServiceType == expected.ServiceType && actual.SelectionEpoch.Equal(expected.SelectionEpoch) &&
			actual.ProfileID == expected.ProfileID && actual.ProfileRevision == expected.ProfileRevision &&
			actual.ProfileOwnerServiceID == expected.ProfileOwnerServiceID && actual.SecretName == expected.SecretName &&
			actual.Fingerprint == expected.Fingerprint {
			return nil
		}
	}
	return ErrServiceAssignmentConflict
}

func (stores CurrentDiscordCredentialStores) discordWaitingMemory(ctx context.Context, auth *MemoryAuthStore, serviceID, tokenID string, expected *DiscordWaitingCredentialBinding, ttl time.Duration) (DiscordWaitingCredentialSnapshot, string, RuntimeSecretLease, error) {
	streams, streamsOK := stores.Streams.(*MemoryStreamStore)
	profiles, profilesOK := stores.Profiles.(*MemoryProfileStore)
	secrets, secretsOK := stores.Secrets.(*MemorySecretStore)
	if !streamsOK || !profilesOK || !secretsOK || streams == nil || profiles == nil || secrets == nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
	}
	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	streams.mu.Lock()
	defer streams.mu.Unlock()
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if auth.streamAssignmentGuard != streams || streams.serviceAssignmentGuard != auth {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
	}
	service, exists := auth.services[serviceID]
	token, tokenExists := auth.serviceTokens[tokenID]
	if !exists || !tokenExists || !currentDiscordServiceTokenValid(service, token, serviceID, tokenID, expected != nil) {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
	}
	snapshot := DiscordWaitingCredentialSnapshot{Service: service, Bindings: []DiscordWaitingCredentialBinding{}}
	if ownerID := strings.TrimSpace(service.CurrentStreamID); ownerID != "" {
		owner, known := streams.streams[ownerID]
		snapshot.ExecutionActive = discordWaitingExecutionActive(owner.Status, known && owner.DeletedAt == nil, streams.normalStopPending[ownerID])
	}
	var streamIDs []string
	for id, stream := range streams.streams {
		if discordWaitingStreamEligible(stream, serviceID) {
			streamIDs = append(streamIDs, id)
		}
	}
	sort.Strings(streamIDs)
	seenProfiles := map[string]bool{}
	for _, id := range streamIDs {
		stream := streams.streams[id]
		profile := profiles.profiles[stream.DiscordConfigID]
		secret := secrets.secrets[currentDiscordConfigString(profile.Config, "bot_token_secret_name")]
		binding := discordWaitingBinding(stream, serviceID, discordWaitingSelectionEpoch(stream), profile, secret)
		snapshot.Bindings = append(snapshot.Bindings, binding)
		if binding.Ready && !seenProfiles[profile.ID] {
			snapshot.Profiles = append(snapshot.Profiles, copyProfile(profile))
			seenProfiles[profile.ID] = true
		}
	}
	if expected == nil {
		return snapshot, "", RuntimeSecretLease{}, nil
	}
	if err := validateExpectedDiscordWaitingBinding(snapshot.Bindings, *expected); err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	leases, ok := stores.Leases.(*MemoryRuntimeSecretLeaseStore)
	if !ok {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrDiscordWaitingBindingUnavailable
	}
	lease, err := leases.ClaimRuntimeSecretLease(ctx, RuntimeSecretLease{ServiceID: serviceID, TokenID: tokenID, SecretName: expected.SecretName}, ttl)
	if err != nil {
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	value := secrets.values[expected.SecretName]
	if value == "" || security.SecretFingerprint(value) != expected.Fingerprint {
		if releaseErr := leases.ReleaseRuntimeSecretLease(ctx, lease); releaseErr != nil {
			return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, releaseErr
		}
		return DiscordWaitingCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrNotFound
	}
	return snapshot, value, lease, nil
}
