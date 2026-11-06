package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestDiscordWaitingCredentialMemory(t *testing.T) {
	discordWaitingCredentialMatrix(t, nil)
}

func TestDiscordWaitingCredentialMariaDB(t *testing.T) {
	discordWaitingCredentialMatrix(t, cb15TestDB(t))
}

func discordWaitingCredentialMatrix(t *testing.T, db *sql.DB) {
	t.Helper()
	newFixture := func(t *testing.T) cb15Fixture {
		t.Helper()
		f := cb15StoreFixture(t, db)
		if streams, ok := f.stores.Streams.(*MemoryStreamStore); ok {
			f.stores.Profiles.(*MemoryProfileStore).BindStreamYouTubeRelayBindingClaims(streams)
		}
		selected := f.serviceID
		for _, stream := range []Stream{f.current, f.other} {
			if _, err := f.stores.Streams.UpdateStreamSettings(t.Context(), stream.ID, StreamSettings{
				DiscordConfigID: f.profile.ID, AutoStartTrigger: "discord_voice_join",
				NodeSelections: &StreamNodeSelectionUpdate{DiscordServiceID: &selected},
			}); err != nil {
				t.Fatal(err)
			}
		}
		return f
	}
	t.Run("saved selections do not grant execution ownership", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.stores.Services.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: f.serviceID}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := f.stores.WaitingDiscordSnapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(snapshot.Bindings) != 2 {
			t.Fatalf("waiting snapshot: bindings=%d err=%v", len(snapshot.Bindings), err)
		}
		for _, binding := range snapshot.Bindings {
			if !binding.Ready || binding.SelectionEpoch.IsZero() || binding.ProfileRevision == "" {
				t.Fatal("waiting selection did not produce fresh monitoring authority")
			}
		}
		service, err := f.stores.Services.GetService(t.Context(), f.serviceID)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("monitor snapshot synthesized an execution owner", err)
		}
		current, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(current.Assignments) != 0 || len(current.Bindings) != 0 {
			t.Fatal("monitor snapshot synthesized current bindings", err)
		}
		value, lease, err := f.stores.ResolveWaitingDiscordBinding(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[0], time.Minute)
		if err != nil || value != "synthetic-cb15-current-value" || lease.StreamID != "" || lease.ArchiveProfileID != "" {
			t.Fatal("waiting resolution changed service lease scope", err)
		}
		if _, _, err := f.stores.ResolveWaitingDiscordBinding(t.Context(), f.serviceID, f.token.ID, snapshot.Bindings[1], time.Minute); !errors.Is(err, ErrRuntimeSecretLeaseActive) {
			t.Fatal("another waiting frame bypassed the service-level lease", err)
		}
	})
	t.Run("waiting B does not alter current A or its epoch", func(t *testing.T) {
		f := newFixture(t)
		before, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.stores.Streams.UpdateStreamStatus(t.Context(), f.current.ID, "live"); err != nil {
			t.Fatal(err)
		}
		waiting, err := f.stores.WaitingDiscordSnapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(waiting.Bindings) != 1 || waiting.Bindings[0].StreamID != f.other.ID {
			t.Fatal("active A polluted the waiting projection", err)
		}
		after, err := f.stores.Snapshot(t.Context(), f.serviceID, f.token.ID)
		if err != nil || len(after.Bindings) != 1 || after.Bindings[0] != before.Bindings[0] {
			t.Fatal("waiting projection changed active credential authority", err)
		}
	})
	for _, mutation := range []string{"selection epoch", "selection removed", "profile changed", "secret rotated", "terminal stream"} {
		t.Run("stale "+mutation, func(t *testing.T) {
			f := newFixture(t)
			snapshot, err := f.stores.WaitingDiscordSnapshot(t.Context(), f.serviceID, f.token.ID)
			if err != nil {
				t.Fatal(err)
			}
			var expected DiscordWaitingCredentialBinding
			for _, binding := range snapshot.Bindings {
				if binding.StreamID == f.other.ID {
					expected = binding
				}
			}
			switch mutation {
			case "selection epoch":
				expected.SelectionEpoch = expected.SelectionEpoch.Add(-time.Second)
			case "selection removed":
				clear := ""
				_, err = f.stores.Streams.UpdateStreamSettings(t.Context(), f.other.ID, StreamSettings{
					NodeSelections: &StreamNodeSelectionUpdate{DiscordServiceID: &clear},
				})
			case "profile changed":
				f.profile.Config["voice_channel_id"] = "100000000000000009"
				cb15SaveProfile(t, &f)
			case "secret rotated":
				_, err = f.stores.Secrets.UpdateSecret(t.Context(), f.secret.Name, "synthetic-new-waiting-value")
			case "terminal stream":
				_, err = f.stores.Streams.UpdateStreamStatus(t.Context(), f.other.ID, "failed")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.stores.ResolveWaitingDiscordBinding(t.Context(), f.serviceID, f.token.ID, expected, time.Minute); !errors.Is(err, ErrServiceAssignmentConflict) && !errors.Is(err, ErrForbidden) {
				t.Fatal("stale monitoring binding resolved", err)
			}
		})
	}
}
