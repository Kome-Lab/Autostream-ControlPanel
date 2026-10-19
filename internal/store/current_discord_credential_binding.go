package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

// CurrentDiscordCredentialBinding is local CP/Bot authority, not a waiting
// auto-start candidate. It contains references and fingerprints, never a value.
type CurrentDiscordCredentialBinding struct {
	StreamID              string    `json:"stream_id"`
	ServiceID             string    `json:"service_id"`
	ServiceType           string    `json:"service_type"`
	AssignmentRole        string    `json:"assignment_role"`
	AssignmentEpoch       time.Time `json:"assignment_epoch"`
	ProfileID             string    `json:"profile_id"`
	ProfileOwnerServiceID string    `json:"profile_owner_service_id,omitempty"`
	SecretName            string    `json:"secret_name,omitempty"`
	Fingerprint           string    `json:"fingerprint,omitempty"`
	Ready                 bool      `json:"ready"`
	ReadinessCode         string    `json:"readiness_code,omitempty"`
}

type CurrentDiscordCredentialSnapshot struct {
	Service     RegisteredService
	Assignments []StreamServiceAssignment
	Bindings    []CurrentDiscordCredentialBinding
	Profiles    []Profile
}

// All stores must share the existing Memory authority or the same MariaDB DB.
// Unsupported wrappers are explicit failures, never an omitted allow signal.
type CurrentDiscordCredentialStores struct {
	Services ServiceRegistryStore
	Streams  StreamStore
	Profiles ProfileStore
	Secrets  SecretStore
	Leases   RuntimeSecretLeaseStore
}

var ErrCurrentDiscordBindingUnavailable = errors.New("current Discord credential binding unavailable")

func (stores CurrentDiscordCredentialStores) Snapshot(ctx context.Context, serviceID, tokenID string) (CurrentDiscordCredentialSnapshot, error) {
	return readFreshCurrentDiscordSnapshot(ctx, func(ctx context.Context) (CurrentDiscordCredentialSnapshot, error) {
		snapshot, _, _, err := stores.currentDiscordOperation(ctx, serviceID, tokenID, nil, 0)
		return snapshot, err
	})
}

// Only a read-only Snapshot may re-enter discovery after a typed assignment
// conflict. The prior operation has returned, including its rollback/unlocks.
// Resolve and lease acquisition remain a single operation below.
func readFreshCurrentDiscordSnapshot(ctx context.Context, read func(context.Context) (CurrentDiscordCredentialSnapshot, error)) (CurrentDiscordCredentialSnapshot, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return CurrentDiscordCredentialSnapshot{}, err
		}
		snapshot, err := read(ctx)
		if err == nil {
			return snapshot, nil
		}
		if ctx.Err() != nil {
			return CurrentDiscordCredentialSnapshot{}, ctx.Err()
		}
		if attempt == 1 || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrServiceAssignmentConflict) {
			return CurrentDiscordCredentialSnapshot{}, err
		}
	}
	return CurrentDiscordCredentialSnapshot{}, ErrServiceAssignmentConflict
}

func (stores CurrentDiscordCredentialStores) Resolve(ctx context.Context, serviceID, tokenID string, expected CurrentDiscordCredentialBinding, ttl time.Duration) (string, RuntimeSecretLease, error) {
	_, value, lease, err := stores.currentDiscordOperation(ctx, serviceID, tokenID, &expected, ttl)
	return value, lease, err
}

func (stores CurrentDiscordCredentialStores) currentDiscordOperation(ctx context.Context, serviceID, tokenID string, expected *CurrentDiscordCredentialBinding, ttl time.Duration) (CurrentDiscordCredentialSnapshot, string, RuntimeSecretLease, error) {
	if err := ctx.Err(); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	switch auth := stores.Services.(type) {
	case *MemoryAuthStore:
		return stores.currentDiscordMemory(ctx, auth, serviceID, tokenID, expected, ttl)
	case MariaDBAuthStore:
		return stores.currentDiscordMariaDB(ctx, auth, serviceID, tokenID, expected, ttl)
	case *MariaDBAuthStore:
		return stores.currentDiscordMariaDB(ctx, *auth, serviceID, tokenID, expected, ttl)
	default:
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
	}
}

func (stores CurrentDiscordCredentialStores) currentDiscordMemory(ctx context.Context, auth *MemoryAuthStore, serviceID, tokenID string, expected *CurrentDiscordCredentialBinding, ttl time.Duration) (CurrentDiscordCredentialSnapshot, string, RuntimeSecretLease, error) {
	streams, streamsOK := stores.Streams.(*MemoryStreamStore)
	profiles, profilesOK := stores.Profiles.(*MemoryProfileStore)
	secrets, secretsOK := stores.Secrets.(*MemorySecretStore)
	if !streamsOK || !profilesOK || !secretsOK || streams == nil || profiles == nil || secrets == nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
	}
	// YouTube profile mutations already use profiles -> streams. Keep that
	// order as well as the existing assignment order auth -> streams.
	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	streams.mu.Lock()
	defer streams.mu.Unlock()
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	if auth.streamAssignmentGuard != streams || streams.serviceAssignmentGuard != auth {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
	}
	service, ok := auth.services[serviceID]
	token, tokenOK := auth.serviceTokens[tokenID]
	if !ok || !tokenOK || !currentDiscordServiceTokenValid(service, token, serviceID, tokenID, expected != nil) {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrForbidden
	}
	snapshot := CurrentDiscordCredentialSnapshot{Service: service, Assignments: []StreamServiceAssignment{}, Bindings: []CurrentDiscordCredentialBinding{}}
	snapshot.Service.Capabilities = copyConfig(service.Capabilities)
	snapshot.Service.Metrics = copyConfig(service.Metrics)
	for key, assignedServiceID := range auth.assignments {
		if assignedServiceID != serviceID {
			continue
		}
		streamID, serviceType, role := assignmentPartsFromKey(key)
		snapshot.Assignments = append(snapshot.Assignments, StreamServiceAssignment{StreamID: streamID, ServiceID: serviceID, ServiceType: serviceType, AssignmentRole: role, AssignedAt: service.UpdatedAt})
	}
	if err := validateCurrentDiscordAssignments(service, snapshot.Assignments); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	for _, assignment := range snapshot.Assignments {
		if assignment.AssignmentRole != "primary" {
			continue
		}
		stream, exists := streams.streams[assignment.StreamID]
		if !exists || stream.DeletedAt != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrServiceAssignmentConflict
		}
		profile := profiles.profiles[stream.DiscordConfigID]
		secret := secrets.secrets[currentDiscordConfigString(profile.Config, "bot_token_secret_name")]
		binding := currentDiscordBinding(assignment, stream.DiscordConfigID, profile, secret)
		snapshot.Bindings = append(snapshot.Bindings, binding)
		if binding.Ready {
			snapshot.Profiles = append(snapshot.Profiles, copyProfile(profile))
		}
	}
	if expected == nil {
		return snapshot, "", RuntimeSecretLease{}, nil
	}
	if err := validateExpectedCurrentDiscordBinding(snapshot, *expected); err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	leases, ok := stores.Leases.(*MemoryRuntimeSecretLeaseStore)
	if !ok {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrCurrentDiscordBindingUnavailable
	}
	// The existing service-level lease key and guard are retained. It is not
	// changed to a per-stream key merely because the request carries a binding.
	lease, err := leases.ClaimRuntimeSecretLease(ctx, RuntimeSecretLease{ServiceID: serviceID, TokenID: tokenID, SecretName: expected.SecretName}, ttl)
	if err != nil {
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
	}
	value := secrets.values[expected.SecretName]
	if value == "" || security.SecretFingerprint(value) != expected.Fingerprint {
		if err := leases.ReleaseRuntimeSecretLease(ctx, lease); err != nil {
			return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, err
		}
		return CurrentDiscordCredentialSnapshot{}, "", RuntimeSecretLease{}, ErrNotFound
	}
	return snapshot, value, lease, nil
}

func currentDiscordServiceTokenValid(service RegisteredService, token ServiceToken, serviceID, tokenID string, resolving bool) bool {
	return serviceID != "" && tokenID != "" && service.ServiceID == serviceID && service.ServiceType == "discord_bot" &&
		service.TokenID == tokenID && token.ID == tokenID && token.ServiceType == service.ServiceType && token.RevokedAt == nil &&
		(!resolving || hasString(token.Scopes, "service.secret.resolve"))
}

func validateCurrentDiscordAssignments(service RegisteredService, assignments []StreamServiceAssignment) error {
	// Preserve the existing CP's single-current invariant; the Bot wire
	// selector separately supports several explicit bindings to one profile.
	if len(assignments) > 1 || (len(assignments) == 0) != (service.CurrentStreamID == "") {
		return ErrServiceAssignmentConflict
	}
	if len(assignments) == 0 {
		return nil
	}
	a := assignments[0]
	if a.ServiceID != service.ServiceID || a.ServiceType != service.ServiceType || a.StreamID != service.CurrentStreamID ||
		a.AssignedAt.IsZero() || (a.AssignmentRole != "primary" && a.AssignmentRole != "standby") {
		return ErrServiceAssignmentConflict
	}
	return nil
}

func currentDiscordBinding(a StreamServiceAssignment, profileID string, profile Profile, secret SecretStatus) CurrentDiscordCredentialBinding {
	binding := CurrentDiscordCredentialBinding{StreamID: a.StreamID, ServiceID: a.ServiceID, ServiceType: a.ServiceType,
		AssignmentRole: a.AssignmentRole, AssignmentEpoch: a.AssignedAt, ProfileID: profileID, ReadinessCode: "current_profile_unavailable"}
	owner := currentDiscordConfigString(profile.Config, "service_id")
	reference := currentDiscordConfigString(profile.Config, "bot_token_secret_name")
	fingerprint := currentDiscordConfigString(profile.Config, "bot_token_fingerprint")
	if profileID == "" || profile.ID != profileID || profile.Kind != ProfileDiscordConfig || owner != a.ServiceID ||
		reference == "" || fingerprint == "" || !currentDiscordSecretName(reference) || !secret.Configured ||
		secret.Name != reference || secret.Fingerprint != fingerprint {
		return binding
	}
	binding.ProfileOwnerServiceID, binding.SecretName, binding.Fingerprint = owner, reference, fingerprint
	binding.Ready, binding.ReadinessCode = true, ""
	return binding
}

func currentDiscordSecretName(name string) bool {
	return allowedSecretName(name) && (name == "discord_bot_token" || strings.HasPrefix(name, "discord_bot_token_"))
}

func currentDiscordConfigString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func validateExpectedCurrentDiscordBinding(snapshot CurrentDiscordCredentialSnapshot, expected CurrentDiscordCredentialBinding) error {
	if !expected.Ready || expected.ReadinessCode != "" || expected.AssignmentRole != "primary" || expected.AssignmentEpoch.IsZero() ||
		expected.StreamID == "" || expected.ServiceID == "" || expected.ProfileID == "" || expected.SecretName == "" || expected.Fingerprint == "" {
		return ErrForbidden
	}
	matches := 0
	for _, actual := range snapshot.Bindings {
		if !actual.Ready {
			return ErrForbidden
		}
		if actual.StreamID == expected.StreamID && actual.ServiceID == expected.ServiceID && actual.ServiceType == expected.ServiceType &&
			actual.AssignmentRole == expected.AssignmentRole && actual.AssignmentEpoch.Equal(expected.AssignmentEpoch) &&
			actual.ProfileID == expected.ProfileID && actual.ProfileOwnerServiceID == expected.ProfileOwnerServiceID &&
			actual.SecretName == expected.SecretName && actual.Fingerprint == expected.Fingerprint {
			matches++
		}
	}
	if matches != 1 {
		return ErrServiceAssignmentConflict
	}
	return nil
}

func sortCurrentDiscordAssignments(assignments []StreamServiceAssignment) {
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].StreamID < assignments[j].StreamID })
}
