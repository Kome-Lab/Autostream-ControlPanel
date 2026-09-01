package store

import (
	"context"
	"errors"
	"testing"
)

func TestUserUIPreferenceAllTwelveThemesByThreeModesAndIsolation(t *testing.T) {
	ctx := context.Background()
	preferences := NewMemoryUserUIPreferenceStore()
	expected := uint64(0)
	for _, themeID := range UserThemeIDs {
		for _, mode := range UserColorModes {
			saved, err := preferences.UpdateUserUIPreference(ctx, "user-a", themeID, mode, expected)
			if err != nil {
				t.Fatalf("%s/%s: %v", themeID, mode, err)
			}
			expected++
			if saved.ThemeID != themeID || saved.ColorMode != mode || saved.Revision != expected {
				t.Fatalf("saved=%#v", saved)
			}
		}
	}
	other, err := preferences.GetUserUIPreference(ctx, "user-b")
	if err != nil {
		t.Fatal(err)
	}
	if other.ThemeID != "autostream" || other.ColorMode != "system" || other.Revision != 0 {
		t.Fatalf("second user leaked preference: %#v", other)
	}
	if _, err = preferences.UpdateUserUIPreference(ctx, "user-a", "autostream", "dark", 0); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale error=%v", err)
	}
}

func TestUserUIPreferenceRejectsUnknownAndFallbackDoesNotRewriteAuthority(t *testing.T) {
	preferences := NewMemoryUserUIPreferenceStore()
	if _, err := preferences.UpdateUserUIPreference(context.Background(), "user-a", "unknown", "system", 0); !errors.Is(err, ErrInvalidThemeID) {
		t.Fatalf("theme error=%v", err)
	}
	if _, err := preferences.UpdateUserUIPreference(context.Background(), "user-a", "autostream", "sepia", 0); !errors.Is(err, ErrInvalidColorMode) {
		t.Fatalf("mode error=%v", err)
	}
	stored := UserUIPreference{UserID: "user-a", ThemeID: "removed-theme", ColorMode: "future-mode", Revision: 9}
	preferences.preferences["user-a"] = stored
	raw, _ := preferences.GetUserUIPreference(context.Background(), "user-a")
	safe := SafeUserUIPreference(raw)
	if safe.ThemeID != "autostream" || safe.ColorMode != "system" || !safe.Fallback || safe.Revision != 9 {
		t.Fatalf("safe=%#v", safe)
	}
	unchanged, _ := preferences.GetUserUIPreference(context.Background(), "user-a")
	if unchanged.ThemeID != stored.ThemeID || unchanged.ColorMode != stored.ColorMode {
		t.Fatalf("fallback rewrote DB authority: %#v", unchanged)
	}
}

func TestDiscordTargetPresetCRUDRevisionSoftDeleteAndSnapshotStability(t *testing.T) {
	ctx := context.Background()
	presets := NewMemoryDiscordTargetPresetStore()
	created, err := presets.CreateDiscordTargetPreset(ctx, DiscordTargetPreset{Name: "Production", GuildID: "123", TextChannelID: "456", VoiceChannelID: "789", CreatedByUserID: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := created
	if _, err = presets.CreateDiscordTargetPreset(ctx, DiscordTargetPreset{Name: " production ", GuildID: "1", TextChannelID: "2", VoiceChannelID: "3", CreatedByUserID: "user-a"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("case-insensitive duplicate=%v", err)
	}
	if _, err = presets.UpdateDiscordTargetPreset(ctx, created.ID, DiscordTargetPreset{Name: "Updated", GuildID: "111", TextChannelID: "222", VoiceChannelID: "333", UpdatedByUserID: "user-b"}, 0); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update=%v", err)
	}
	updated, err := presets.UpdateDiscordTargetPreset(ctx, created.ID, DiscordTargetPreset{Name: "Updated", GuildID: "111", TextChannelID: "222", VoiceChannelID: "333", UpdatedByUserID: "user-b"}, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GuildID != "123" || snapshot.TextChannelID != "456" || snapshot.VoiceChannelID != "789" {
		t.Fatalf("existing snapshot mutated: %#v", snapshot)
	}
	deleted, err := presets.DeleteDiscordTargetPreset(ctx, created.ID, "user-b", updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedAt == nil {
		t.Fatal("soft delete missing")
	}
	if _, err = presets.GetDiscordTargetPreset(ctx, created.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted preset selectable: %v", err)
	}
	historical, err := presets.GetDiscordTargetPreset(ctx, created.ID, true)
	if err != nil || historical.Revision != deleted.Revision {
		t.Fatalf("historical snapshot lookup=%#v err=%v", historical, err)
	}
	items, err := presets.ListDiscordTargetPresets(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("active list=%#v err=%v", items, err)
	}
}

func TestDiscordTargetPresetRequiresThreeTrimmedDecimalOpaqueIDs(t *testing.T) {
	for _, preset := range []DiscordTargetPreset{{Name: "x", GuildID: "", TextChannelID: "2", VoiceChannelID: "3"}, {Name: "x", GuildID: "abc", TextChannelID: "2", VoiceChannelID: "3"}, {Name: "x", GuildID: "1", TextChannelID: "2", VoiceChannelID: "3x"}, {Name: "x", GuildID: "123456789012345678901234567890123", TextChannelID: "2", VoiceChannelID: "3"}} {
		if err := ValidateDiscordTargetPreset(preset); err == nil {
			t.Fatalf("accepted invalid preset %#v", preset)
		}
	}
	valid := DiscordTargetPreset{Name: " x ", GuildID: " 001 ", TextChannelID: "002", VoiceChannelID: "003"}
	valid = normalizeDiscordTargetPreset(valid)
	if err := ValidateDiscordTargetPreset(valid); err != nil {
		t.Fatalf("valid preset=%#v error=%v", valid, err)
	}
}
