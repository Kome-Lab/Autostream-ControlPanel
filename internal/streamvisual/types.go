package streamvisual

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/example/autostream-control-panel/internal/store"
)

var (
	ErrNotFound          = errors.New("stream_visual_settings_not_found")
	ErrRevisionConflict  = errors.New("revision_conflict")
	ErrStreamStateLocked = errors.New("stream_state_locked")
	ErrInvalidSettings   = errors.New("invalid_visual_settings")
	ErrAssetClaim        = errors.New("asset_claim_failed")
)

type OptionalString struct {
	Set   bool
	Valid bool
	Value string
}

func (o *OptionalString) UnmarshalJSON(raw []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		o.Valid = false
		o.Value = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	o.Valid = true
	o.Value = value
	return nil
}

func (o OptionalString) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

type Settings struct {
	StreamID                    string    `json:"stream_id"`
	BackgroundMode              string    `json:"background_mode"`
	BackgroundAssetID           string    `json:"background_asset_id,omitempty"`
	BackgroundVariantID         string    `json:"background_variant_id,omitempty"`
	HeaderTitleMode             string    `json:"header_title_mode"`
	HeaderTitleValue            string    `json:"header_title_value,omitempty"`
	DiscordTargetMode           string    `json:"discord_target_mode,omitempty"`
	DiscordTargetPresetID       string    `json:"discord_target_preset_id,omitempty"`
	DiscordTargetPresetRevision uint64    `json:"discord_target_preset_revision,omitempty"`
	DiscordSnapshotRevision     uint64    `json:"discord_snapshot_revision"`
	DiscordGuildID              string    `json:"discord_guild_id,omitempty"`
	DiscordTextChannelID        string    `json:"discord_text_channel_id,omitempty"`
	DiscordVoiceChannelID       string    `json:"discord_voice_channel_id,omitempty"`
	DiscordPresetDeleted        bool      `json:"discord_preset_deleted,omitempty"`
	CoverSource                 string    `json:"cover_source"`
	CoverPresetID               string    `json:"cover_preset_id,omitempty"`
	CoverPresetRevision         uint64    `json:"cover_preset_revision,omitempty"`
	CoverAssetID                string    `json:"cover_asset_id,omitempty"`
	CoverVariantID              string    `json:"cover_variant_id,omitempty"`
	CoverStartActive            bool      `json:"cover_start_active"`
	Revision                    uint64    `json:"revision"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

type Update struct {
	ExpectedRevision            uint64         `json:"expected_revision"`
	UploadSessionID             string         `json:"upload_session_id,omitempty"`
	BackgroundMode              OptionalString `json:"background_mode"`
	BackgroundAssetID           OptionalString `json:"background_asset_id"`
	BackgroundVariantID         OptionalString `json:"background_variant_id"`
	HeaderTitleMode             OptionalString `json:"header_title_mode"`
	HeaderTitleValue            OptionalString `json:"header_title_value"`
	DiscordTargetMode           OptionalString `json:"discord_target_mode"`
	DiscordTargetPresetID       OptionalString `json:"discord_target_preset_id"`
	DiscordTargetPresetRevision *uint64        `json:"discord_target_preset_revision,omitempty"`
	DiscordGuildID              OptionalString `json:"discord_guild_id"`
	DiscordTextChannelID        OptionalString `json:"discord_text_channel_id"`
	DiscordVoiceChannelID       OptionalString `json:"discord_voice_channel_id"`
	CoverSource                 OptionalString `json:"cover_source"`
	CoverPresetID               OptionalString `json:"cover_preset_id"`
	CoverAssetID                OptionalString `json:"cover_asset_id"`
	CoverVariantID              OptionalString `json:"cover_variant_id"`
	CoverStartActive            *bool          `json:"cover_start_active,omitempty"`
}

type Create struct {
	Name            string               `json:"name"`
	UploadSessionID string               `json:"upload_session_id,omitempty"`
	Settings        Update               `json:"visual_settings"`
	LegacySettings  store.StreamSettings `json:"-"`
}

type AssetReadiness struct {
	BackgroundExists       bool
	BackgroundVariantReady bool
	BackgroundHashVerified bool
	CoverVariantReady      bool
	MediaAssetIntegrity    bool
}

type RuntimeCapabilities struct {
	SceneAppearance         bool
	VideoCover              bool
	VideoCoverAction        bool
	DiscordTargetAccessible bool
}

type ReadinessIssue struct {
	Code     string `json:"code"`
	Blocking bool   `json:"blocking"`
	Message  string `json:"message"`
}

func ValidateHeaderTitle(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 80 {
		return ErrInvalidSettings
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\n' || r == '\r' || isBidiControl(r) {
			return ErrInvalidSettings
		}
	}
	return nil
}

func isBidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func ValidateSettings(settings Settings) error {
	if settings.BackgroundMode != "default" && settings.BackgroundMode != "image" {
		return ErrInvalidSettings
	}
	if settings.BackgroundMode == "image" && (strings.TrimSpace(settings.BackgroundAssetID) == "" || strings.TrimSpace(settings.BackgroundVariantID) == "") {
		return ErrInvalidSettings
	}
	if settings.HeaderTitleMode != "default" && settings.HeaderTitleMode != "custom" {
		return ErrInvalidSettings
	}
	if settings.HeaderTitleMode == "custom" {
		if err := ValidateHeaderTitle(settings.HeaderTitleValue); err != nil {
			return err
		}
	}
	if settings.DiscordTargetMode != "" && settings.DiscordTargetMode != "inherit" && settings.DiscordTargetMode != "preset" && settings.DiscordTargetMode != "manual" {
		return ErrInvalidSettings
	}
	if settings.DiscordTargetMode == "manual" {
		for _, value := range []string{settings.DiscordGuildID, settings.DiscordTextChannelID, settings.DiscordVoiceChannelID} {
			if !validDiscordID(value) {
				return ErrInvalidSettings
			}
		}
	}
	if settings.DiscordTargetMode == "preset" && (settings.DiscordTargetPresetID == "" || settings.DiscordTargetPresetRevision == 0) {
		return ErrInvalidSettings
	}
	if settings.CoverSource != "none" && settings.CoverSource != "preset" && settings.CoverSource != "upload" {
		return ErrInvalidSettings
	}
	if settings.CoverSource == "preset" && (settings.CoverPresetID == "" || settings.CoverPresetRevision == 0 || settings.CoverAssetID == "" || settings.CoverVariantID == "") {
		return ErrInvalidSettings
	}
	if settings.CoverSource == "upload" && (settings.CoverAssetID == "" || settings.CoverVariantID == "") {
		return ErrInvalidSettings
	}
	return nil
}

func validDiscordID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func EvaluateReadiness(settings Settings, assets AssetReadiness, capabilities RuntimeCapabilities) []ReadinessIssue {
	issues := []ReadinessIssue{}
	add := func(code, message string) {
		issues = append(issues, ReadinessIssue{Code: code, Blocking: true, Message: message})
	}
	if (settings.BackgroundMode == "image" || settings.HeaderTitleMode == "custom") && !capabilities.SceneAppearance {
		add("scene_appearance_capability", "Worker scene appearance capability is unavailable")
	}
	if settings.BackgroundMode == "image" {
		if !assets.BackgroundExists {
			add("scene_background_asset_exists", "Background asset is unavailable")
		}
		if !assets.BackgroundVariantReady {
			add("scene_background_variant_ready", "Background variant is not ready")
		}
		if !assets.BackgroundHashVerified {
			add("scene_background_hash_verified", "Background asset integrity is not verified")
		}
	}
	if settings.HeaderTitleMode == "custom" && ValidateHeaderTitle(settings.HeaderTitleValue) != nil {
		add("scene_header_title_valid", "Header title is invalid")
	}
	if settings.CoverSource != "none" {
		if !capabilities.VideoCover {
			add("video_cover_capability", "Encoder video cover capability is unavailable")
		}
		if !assets.CoverVariantReady {
			add("video_cover_variant_ready", "Video cover variant is not ready")
		}
		if !assets.MediaAssetIntegrity {
			add("media_asset_integrity", "Video cover asset integrity is not verified")
		}
		if !capabilities.VideoCoverAction {
			add("video_cover_action_ready", "Video cover action is unavailable")
		}
	}
	if settings.DiscordTargetMode == "manual" || settings.DiscordTargetMode == "preset" {
		if !validDiscordID(settings.DiscordGuildID) || !validDiscordID(settings.DiscordTextChannelID) || !validDiscordID(settings.DiscordVoiceChannelID) {
			add("discord_target_resolved", "Discord target is unresolved")
		} else if !capabilities.DiscordTargetAccessible {
			add("discord_target_accessible", "Discord target accessibility is not confirmed")
		}
	}
	return issues
}
