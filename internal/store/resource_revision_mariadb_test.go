package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func revisionFixtureCleanup(t *testing.T, run func(context.Context)) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		run(ctx)
	})
}

func TestMariaDBProfileRevisionAtomicSecretAndMonotonicAuthority(t *testing.T) {
	db := cb15TestDB(t)
	for _, table := range []string{"profiles", "drive_destinations"} {
		var precision int
		if err := db.QueryRowContext(t.Context(), "SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='updated_at'", table).Scan(&precision); err != nil || precision != 6 {
			t.Fatalf("%s revision precision=%d", table, precision)
		}
	}
	profiles := NewMariaDBProfileStore(db)
	secrets := NewMariaDBSecretStore(db, "synthetic-profile-cas-key")
	name := "discord_bot_token_cas_" + strings.ReplaceAll(newUUID(), "-", "")[:12]
	initial := "synthetic-initial-value"
	status, err := secrets.UpdateSecret(t.Context(), name, initial)
	if err != nil {
		t.Fatal(err)
	}
	revisionFixtureCleanup(t, func(ctx context.Context) { _, _ = secrets.UpdateSecret(ctx, name, "") })
	profile, err := profiles.CreateProfile(t.Context(), ProfileDiscordConfig, "SQL profile "+newUUID(), map[string]any{"service_id": "selected-bot", "bot_token_secret_name": name, "bot_token_fingerprint": status.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	revisionFixtureCleanup(t, func(ctx context.Context) { _ = profiles.DeleteProfile(ctx, ProfileDiscordConfig, profile.ID) })
	baseline := resourceUpdatedAt(profile.UpdatedAt)
	fresh := "synthetic-fresh-value"
	input := ProfileUpdate{Name: profile.Name, Config: map[string]any{"service_id": "selected-bot", "bot_token_secret_name": name, "bot_token_fingerprint": security.SecretFingerprint(fresh)}, ExpectedUpdatedAt: &baseline, SecretChanges: []SecretWrite{{Name: name, Value: fresh}}}
	saved, err := profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, profile.ID, input, secrets)
	if err != nil || !saved.UpdatedAt.After(profile.UpdatedAt) {
		t.Fatal("fresh profile CAS did not commit")
	}
	assertPair := func(want Profile, wantSecret string) {
		t.Helper()
		row, err := profiles.GetProfile(t.Context(), ProfileDiscordConfig, profile.ID)
		value, secretErr := secrets.GetSecretValue(t.Context(), name)
		if err != nil || secretErr != nil || row.Name != want.Name || !row.UpdatedAt.Equal(want.UpdatedAt) || value != wantSecret || row.Config["bot_token_fingerprint"] != security.SecretFingerprint(wantSecret) {
			t.Fatal("profile/secret pair became partial")
		}
	}
	stale := input
	stale.Name = "stale profile"
	stale.SecretChanges = []SecretWrite{{Name: name, Value: "synthetic-stale-value"}}
	if _, err = profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, profile.ID, stale, secrets); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale baseline was not rejected")
	}
	assertPair(saved, fresh)
	current := resourceUpdatedAt(saved.UpdatedAt)
	invalidKey := NewMariaDBSecretStore(db, "")
	rollback := input
	rollback.Name = "must roll back"
	rollback.ExpectedUpdatedAt = &current
	if _, err = profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, profile.ID, rollback, invalidKey); !errors.Is(err, ErrSecretKeyRequired) {
		t.Fatal("missing encryption authority did not fail")
	}
	assertPair(saved, fresh)
	type result struct {
		profile Profile
		err     error
	}
	outcomes := make(chan result, 2)
	for _, label := range []string{"a", "b"} {
		label := label
		go func() {
			value := "synthetic-concurrent-" + label
			mutation := ProfileUpdate{Name: "winner-" + label + "-" + profile.ID, Config: map[string]any{"service_id": "selected-bot", "bot_token_secret_name": name, "bot_token_fingerprint": security.SecretFingerprint(value)}, ExpectedUpdatedAt: &current, SecretChanges: []SecretWrite{{Name: name, Value: value}}}
			row, err := profiles.UpdateProfileRevision(t.Context(), ProfileDiscordConfig, profile.ID, mutation, secrets)
			outcomes <- result{row, err}
		}()
	}
	wins, conflicts := 0, 0
	var winner Profile
	for range 2 {
		outcome := <-outcomes
		if outcome.err == nil {
			wins++
			winner = outcome.profile
		} else if errors.Is(outcome.err, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal("unexpected concurrent mutation failure")
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("same baseline allowed multiple writers")
	}
	value, err := secrets.GetSecretValue(t.Context(), name)
	if err != nil || winner.Config["bot_token_fingerprint"] != security.SecretFingerprint(value) {
		t.Fatal("concurrent winner did not own the matching secret")
	}
	prior := winner.UpdatedAt
	legacy, err := profiles.updateProfileAt(t.Context(), ProfileDiscordConfig, profile.ID, winner.Name, winner.Config, prior.Add(-time.Hour))
	if err != nil || !legacy.UpdatedAt.After(prior) {
		t.Fatal("legacy writer moved the CAS clock backwards")
	}
}

func TestMariaDBDriveDestinationRevisionKeepsEncryptedFolderAtomic(t *testing.T) {
	db := cb15TestDB(t)
	integrations := NewMariaDBIntegrationStore(db, "synthetic-drive-cas-key")
	provider, err := integrations.CreateOAuthProvider(t.Context(), OAuthProvider{ProviderType: "google", Name: "SQL Drive CAS " + newUUID(), Enabled: true, ClientID: "synthetic-drive-client", RedirectURI: "https://control.example.com/auth/oauth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	revisionFixtureCleanup(t, func(ctx context.Context) { _ = integrations.DeleteOAuthProvider(ctx, provider.ID) })
	account, err := integrations.CreateOAuthAccount(t.Context(), OAuthAccount{ProviderID: provider.ID, ProviderType: provider.ProviderType, AccountLabel: "SQL Drive CAS account", Subject: newUUID(), Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, RefreshToken: "synthetic-drive-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	revisionFixtureCleanup(t, func(ctx context.Context) { _ = integrations.DeleteOAuthAccount(ctx, account.ID) })
	created, err := integrations.CreateDriveDestination(t.Context(), DriveDestination{Name: "SQL destination " + newUUID(), AuthMode: "oauth2", OAuthAccountID: account.ID, FolderID: "synthetic-initial-folder"})
	if err != nil {
		t.Fatal(err)
	}
	revisionFixtureCleanup(t, func(ctx context.Context) { _ = integrations.DeleteDriveDestination(ctx, created.ID) })
	baseline := created.UpdatedAt
	mutation := DriveDestination{ID: created.ID, Name: created.Name, AuthMode: created.AuthMode, OAuthAccountID: created.OAuthAccountID, FolderID: "synthetic-fresh-folder", ExpectedUpdatedAt: &baseline}
	saved, err := integrations.UpdateDriveDestination(t.Context(), mutation)
	if err != nil || saved.UpdatedAt == baseline {
		t.Fatal("fresh Drive CAS did not advance")
	}
	mutation.Name = "stale destination"
	mutation.FolderID = "synthetic-stale-folder"
	if _, err = integrations.UpdateDriveDestination(t.Context(), mutation); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale Drive baseline was not rejected")
	}
	dispatched, err := integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || dispatched.Name != saved.Name || dispatched.FolderID != "synthetic-fresh-folder" || dispatched.UpdatedAt != saved.UpdatedAt {
		t.Fatal("stale Drive update changed encrypted configuration")
	}
	current := saved.UpdatedAt
	mutation.ExpectedUpdatedAt = &current
	invalidKey := NewMariaDBIntegrationStore(db, "")
	if _, err = invalidKey.UpdateDriveDestination(t.Context(), mutation); !errors.Is(err, ErrSecretKeyRequired) {
		t.Fatal("Drive encryption failure did not reject before commit")
	}
	dispatched, err = integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || dispatched.Name != saved.Name || dispatched.FolderID != "synthetic-fresh-folder" || dispatched.UpdatedAt != saved.UpdatedAt {
		t.Fatal("failed Drive update changed encrypted configuration")
	}
	mutation.FolderID = ""
	mutation.Name = saved.Name
	result, err := integrations.UpdateDriveDestination(t.Context(), mutation)
	if err != nil || result.UpdatedAt == current {
		t.Fatal("blank-folder update did not retain the protected value and advance")
	}
	dispatched, err = integrations.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || dispatched.FolderID != "synthetic-fresh-folder" {
		t.Fatal("blank folder input erased the existing encrypted value")
	}
}
