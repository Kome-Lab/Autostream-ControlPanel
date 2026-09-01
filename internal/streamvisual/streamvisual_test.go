package streamvisual

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUpdateDistinguishesOmitFromExplicitClear(t *testing.T) {
	current := Settings{StreamID: "stream-1", BackgroundMode: "image", BackgroundAssetID: "asset-1", BackgroundVariantID: "variant-1", HeaderTitleMode: "custom", HeaderTitleValue: "Operator title", CoverSource: "upload", CoverAssetID: "asset-2", CoverVariantID: "variant-2", CoverStartActive: true, Revision: 4}
	var omitted Update
	if err := json.Unmarshal([]byte(`{"expected_revision":4}`), &omitted); err != nil {
		t.Fatal(err)
	}
	preserved, _, err := applyUpdate(current, omitted)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.BackgroundAssetID != "asset-1" || preserved.HeaderTitleValue != "Operator title" || preserved.CoverAssetID != "asset-2" {
		t.Fatalf("omitted fields were cleared: %#v", preserved)
	}
	var cleared Update
	if err = json.Unmarshal([]byte(`{"expected_revision":4,"background_mode":null,"header_title_mode":null,"cover_source":null}`), &cleared); err != nil {
		t.Fatal(err)
	}
	next, _, err := applyUpdate(current, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if next.BackgroundMode != "default" || next.BackgroundAssetID != "" || next.HeaderTitleMode != "default" || next.HeaderTitleValue != "" || next.CoverSource != "none" || next.CoverAssetID != "" || next.CoverStartActive {
		t.Fatalf("explicit clear failed: %#v", next)
	}
}

func TestPresetModeIgnoresClientEffectiveDiscordIDsAndManualRequiresAllThree(t *testing.T) {
	current := DefaultSettings("stream-1")
	presetRevision := uint64(7)
	presetUpdate := Update{DiscordTargetMode: OptionalString{Set: true, Valid: true, Value: "preset"}, DiscordTargetPresetID: OptionalString{Set: true, Valid: true, Value: "preset-1"}, DiscordTargetPresetRevision: &presetRevision, DiscordGuildID: OptionalString{Set: true, Valid: true, Value: "999"}, DiscordTextChannelID: OptionalString{Set: true, Valid: true, Value: "998"}, DiscordVoiceChannelID: OptionalString{Set: true, Valid: true, Value: "997"}}
	next, changes, err := applyUpdate(current, presetUpdate)
	if err != nil || !changes.discord {
		t.Fatalf("apply preset changed=%t err=%v", changes.discord, err)
	}
	if next.DiscordGuildID != "" || next.DiscordTextChannelID != "" || next.DiscordVoiceChannelID != "" {
		t.Fatalf("client effective IDs were trusted: %#v", next)
	}
	next.DiscordGuildID = "123"
	next.DiscordTextChannelID = "456"
	next.DiscordVoiceChannelID = "789"
	if err = ValidateSettings(next); err != nil {
		t.Fatalf("server snapshot invalid: %v", err)
	}
	manual := Settings{BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "none", DiscordTargetMode: "manual", DiscordGuildID: "1", DiscordTextChannelID: "2"}
	if err = ValidateSettings(manual); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("incomplete manual error=%v", err)
	}
	manualSwitch, _, err := applyUpdate(next, Update{DiscordTargetMode: OptionalString{Set: true, Valid: true, Value: "manual"}})
	if err != nil || ValidateSettings(manualSwitch) == nil {
		t.Fatalf("preset-to-manual switch reused snapshot IDs without active manual payload: %#v err=%v", manualSwitch, err)
	}
}

func TestInactiveModeFieldsCannotMutateOrRefreshSnapshots(t *testing.T) {
	current := DefaultSettings("stream-snapshot")
	current.DiscordTargetMode = "preset"
	current.DiscordTargetPresetID = "discord-preset-1"
	current.DiscordTargetPresetRevision = 8
	current.DiscordGuildID = "101"
	current.DiscordTextChannelID = "102"
	current.DiscordVoiceChannelID = "103"
	current.CoverSource = "preset"
	current.CoverPresetID = "cover-preset-1"
	current.CoverPresetRevision = 5
	current.CoverAssetID = "cover-asset-1"
	current.CoverVariantID = "cover-variant-1"

	next, changes, err := applyUpdate(current, Update{
		DiscordGuildID:        OptionalString{Set: true, Valid: true, Value: "999"},
		DiscordTextChannelID:  OptionalString{Set: true, Valid: true, Value: "998"},
		DiscordVoiceChannelID: OptionalString{Set: true, Valid: true, Value: "997"},
		CoverAssetID:          OptionalString{Set: true, Valid: true, Value: "attacker-asset"},
		CoverVariantID:        OptionalString{Set: true, Valid: true, Value: "attacker-variant"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes.discord || changes.coverSelection {
		t.Fatalf("inactive fields requested snapshot refresh: %#v", changes)
	}
	if next.DiscordGuildID != current.DiscordGuildID || next.DiscordTextChannelID != current.DiscordTextChannelID || next.DiscordVoiceChannelID != current.DiscordVoiceChannelID || next.CoverAssetID != current.CoverAssetID || next.CoverVariantID != current.CoverVariantID {
		t.Fatalf("inactive fields mutated snapshot: %#v", next)
	}
}

func TestHeaderTitleRejectsControlNewlineBidiAndLength(t *testing.T) {
	invalid := []string{"", "line\nbreak", "control\x01", "safe\u202Eevil", strings.Repeat("a", 81)}
	for _, value := range invalid {
		if err := ValidateHeaderTitle(value); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("accepted title %q error=%v", value, err)
		}
	}
	if err := ValidateHeaderTitle("配信タイトル 2026"); err != nil {
		t.Fatalf("valid title=%v", err)
	}
}

func TestReadinessFailsClosedWithoutRuntimeOrVerifiedVariant(t *testing.T) {
	settings := Settings{BackgroundMode: "image", BackgroundAssetID: "asset-bg", BackgroundVariantID: "variant-bg", HeaderTitleMode: "custom", HeaderTitleValue: "Title", CoverSource: "upload", CoverAssetID: "asset-cover", CoverVariantID: "variant-cover", DiscordTargetMode: "manual", DiscordGuildID: "1", DiscordTextChannelID: "2", DiscordVoiceChannelID: "3"}
	issues := EvaluateReadiness(settings, AssetReadiness{}, RuntimeCapabilities{})
	want := map[string]bool{"scene_appearance_capability": true, "scene_background_asset_exists": true, "scene_background_variant_ready": true, "scene_background_hash_verified": true, "video_cover_capability": true, "video_cover_variant_ready": true, "video_cover_action_ready": true, "media_asset_integrity": true, "discord_target_accessible": true}
	for _, issue := range issues {
		delete(want, issue.Code)
		if !issue.Blocking {
			t.Fatalf("issue not blocking: %#v", issue)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing fail-closed issues: %#v", want)
	}
	ready := EvaluateReadiness(settings, AssetReadiness{BackgroundExists: true, BackgroundVariantReady: true, BackgroundHashVerified: true, CoverVariantReady: true, MediaAssetIntegrity: true}, RuntimeCapabilities{SceneAppearance: true, VideoCover: true, VideoCoverAction: true, DiscordTargetAccessible: true})
	if len(ready) != 0 {
		t.Fatalf("ready issues=%#v", ready)
	}
}

func TestReadinessInheritDoesNotRequireSavedDiscordSnapshot(t *testing.T) {
	settings := DefaultSettings("stream-inherit")
	settings.DiscordTargetMode = "inherit"
	issues := EvaluateReadiness(settings, AssetReadiness{}, RuntimeCapabilities{})
	for _, issue := range issues {
		if strings.HasPrefix(issue.Code, "discord_target_") {
			t.Fatalf("inherit mode was treated as an unresolved explicit target: %#v", issues)
		}
	}
}

func TestReadinessCustomHeaderRequiresSceneAppearanceCapabilityWithoutBackground(t *testing.T) {
	settings := DefaultSettings("stream-title")
	settings.HeaderTitleMode = "custom"
	settings.HeaderTitleValue = "Runtime-rendered title"
	issues := EvaluateReadiness(settings, AssetReadiness{}, RuntimeCapabilities{})
	if len(issues) != 1 || issues[0].Code != "scene_appearance_capability" || !issues[0].Blocking {
		t.Fatalf("custom title did not fail closed without Worker capability: %#v", issues)
	}
	if ready := EvaluateReadiness(settings, AssetReadiness{}, RuntimeCapabilities{SceneAppearance: true}); len(ready) != 0 {
		t.Fatalf("capable custom title remained blocked: %#v", ready)
	}
}
