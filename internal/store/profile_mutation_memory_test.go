package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

type memoryProfileMutationFixture struct {
	profiles   *MemoryProfileStore
	secrets    *MemorySecretStore
	profile    Profile
	secretName string
}

func newMemoryProfileMutationFixture(t *testing.T) memoryProfileMutationFixture {
	t.Helper()
	f := memoryProfileMutationFixture{profiles: NewMemoryProfileStore(), secrets: NewMemorySecretStore(), secretName: "discord_bot_token_memory_cas"}
	status, err := f.secrets.UpdateSecret(t.Context(), f.secretName, "synthetic-initial-value")
	if err != nil {
		t.Fatal(err)
	}
	f.profile, err = f.profiles.CreateProfile(t.Context(), ProfileDiscordConfig, "initial profile", map[string]any{"service_id": "memory-bot", "bot_token_secret_name": f.secretName, "bot_token_fingerprint": status.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f memoryProfileMutationFixture) input(name, value string) ProfileUpdate {
	expected := f.profile.UpdatedAt.UTC().Format(time.RFC3339Nano)
	return ProfileUpdate{Name: name, ExpectedUpdatedAt: &expected, Config: map[string]any{"service_id": "memory-bot", "bot_token_secret_name": f.secretName, "bot_token_fingerprint": security.SecretFingerprint(value)}, SecretChanges: []SecretWrite{{Name: f.secretName, Value: value}}}
}

func TestMemoryProfileRevisionPublishesProfileAndSecretTogether(t *testing.T) {
	f := newMemoryProfileMutationFixture(t)
	saved, err := f.profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, f.profile.ID, f.input("accepted profile", "synthetic-accepted-value"), f.secrets)
	if err != nil || saved.Name != "accepted profile" || !saved.UpdatedAt.After(f.profile.UpdatedAt) {
		t.Fatal("current profile and secret update was not accepted")
	}
	stale := f.input("stale profile", "synthetic-stale-value")
	if _, err = f.profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, f.profile.ID, stale, f.secrets); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale profile revision was not rejected")
	}
	current, err := f.profiles.GetProfile(t.Context(), ProfileDiscordConfig, f.profile.ID)
	if err != nil || !reflect.DeepEqual(current, saved) {
		t.Fatal("stale request changed accepted profile state")
	}
	value, err := f.secrets.GetSecretValue(t.Context(), f.secretName)
	if err != nil || value != "synthetic-accepted-value" {
		t.Fatal("stale request changed the accepted credential")
	}
	statuses, err := f.secrets.ListSecretStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, status := range statuses {
		if status.Name == f.secretName {
			matched = true
			if status.Fingerprint != current.Config["bot_token_fingerprint"] {
				t.Fatal("published profile and credential fingerprints disagree")
			}
		}
	}
	if !matched {
		t.Fatal("published credential metadata is missing")
	}
}

type wrappedMemoryProfileSecretStore struct{ SecretStore }

func TestMemoryProfileRevisionRejectsEveryPreconditionBeforeSecretWrites(t *testing.T) {
	for _, name := range []string{"malformed revision", "unknown secret", "duplicate secret", "unsupported authority", "missing profile", "wrong kind", "duplicate name", "canceled"} {
		t.Run(name, func(t *testing.T) {
			f := newMemoryProfileMutationFixture(t)
			input := f.input("attempted profile", "synthetic-attempted-value")
			kind, id, secretStore, ctx := ProfileDiscordConfig, f.profile.ID, SecretStore(f.secrets), t.Context()
			var want error
			switch name {
			case "malformed revision":
				invalid := "invalid"
				input.ExpectedUpdatedAt = &invalid
				want = ErrInvalidResourceRevision
			case "unknown secret":
				input.SecretChanges[0].Name = "unrecognized"
				want = ErrUnknownSecret
			case "duplicate secret":
				input.SecretChanges = append(input.SecretChanges, input.SecretChanges[0])
				want = ErrInvalidProfileSecretMutation
			case "unsupported authority":
				secretStore = wrappedMemoryProfileSecretStore{f.secrets}
				want = ErrAtomicProfileUpdateUnavailable
			case "missing profile":
				id = "missing"
				want = ErrNotFound
			case "wrong kind":
				kind = ProfileEncoder
				want = ErrNotFound
			case "duplicate name":
				if _, err := f.profiles.CreateProfile(t.Context(), ProfileDiscordConfig, input.Name, map[string]any{}); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if _, err := f.profiles.UpdateProfileRevision(ctx, kind, id, input, secretStore); err == nil || want != nil && !errors.Is(err, want) {
				t.Fatal("invalid precondition was not rejected")
			}
			current, err := f.profiles.GetProfile(t.Context(), ProfileDiscordConfig, f.profile.ID)
			if err != nil || !reflect.DeepEqual(current, f.profile) {
				t.Fatal("rejected mutation changed the profile")
			}
			value, err := f.secrets.GetSecretValue(t.Context(), f.secretName)
			if err != nil || value != "synthetic-initial-value" {
				t.Fatal("rejected mutation changed the credential")
			}
		})
	}
}

func TestMemoryProfileRevisionConcurrentWritersUseOneAtomicCAS(t *testing.T) {
	f := newMemoryProfileMutationFixture(t)
	type result struct {
		profile Profile
		err     error
		value   string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, name := range []string{"first", "second"} {
		go func(name string) {
			<-start
			value := "synthetic-" + name + "-value"
			profile, err := f.profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, f.profile.ID, f.input(name, value), f.secrets)
			results <- result{profile: profile, err: err, value: value}
		}(name)
	}
	close(start)
	var winner result
	successes, conflicts := 0, 0
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				successes++
				winner = result
			} else if errors.Is(result.err, ErrRevisionConflict) {
				conflicts++
			} else {
				t.Fatal("concurrent update returned an unexpected error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent profile update did not finish")
		}
	}
	value, err := f.secrets.GetSecretValue(t.Context(), f.secretName)
	if successes != 1 || conflicts != 1 || err != nil || value != winner.value {
		t.Fatal("profile CAS admitted more than one writer or published the losing credential")
	}
	current, err := f.profiles.GetProfile(t.Context(), ProfileDiscordConfig, f.profile.ID)
	if err != nil || !reflect.DeepEqual(current, winner.profile) {
		t.Fatal("winning profile and credential did not remain paired")
	}
}

func TestMemoryProfileRevisionPreservesRelayClaimSecretFence(t *testing.T) {
	streams, profiles, secrets := NewMemoryStreamStore(), NewMemoryProfileStore(), NewMemorySecretStore()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	secretName := "youtube_stream_key_memory_cas"
	if _, err := secrets.UpdateSecret(t.Context(), secretName, "synthetic-initial-value"); err != nil {
		t.Fatal(err)
	}
	output, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "claimed output", map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "claimed output stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{YouTubeOutputID: output.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = streams.UpdateStreamStatus(t.Context(), stream.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	claim := testYouTubeRelayBindingClaim(stream.ID)
	claim.YouTubeOutputID = output.ID
	setExpectedYouTubeRelayBindingProfileRevision(&claim, output.YouTubeRelayBindingRevision)
	if _, err = streams.ReserveStreamYouTubeRelayBindingClaim(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	expected := output.UpdatedAt.UTC().Format(time.RFC3339Nano)
	input := ProfileUpdate{Name: "attempted output", Config: map[string]any{"mode": youtubeRelayBindingClaimStaticRuntimeMode}, ExpectedUpdatedAt: &expected, SecretChanges: []SecretWrite{{Name: secretName, Value: "synthetic-attempted-value"}}}
	if _, err = profiles.UpdateProfileRevision(t.Context(), ProfileYouTubeOutput, output.ID, input, secrets); !errors.Is(err, ErrYouTubeRelayBindingClaimActive) {
		t.Fatal("claimed output mutation bypassed the relay fence")
	}
	current, err := profiles.GetProfile(t.Context(), ProfileYouTubeOutput, output.ID)
	if err != nil || !reflect.DeepEqual(current, output) {
		t.Fatal("claimed output changed after rejection")
	}
	value, err := secrets.GetSecretValue(t.Context(), secretName)
	if err != nil || value != "synthetic-initial-value" {
		t.Fatal("relay fence rejection changed its credential")
	}
}
