package streamvisual

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

type Repository interface {
	Get(ctx context.Context, streamID string) (Settings, error)
	Update(ctx context.Context, streamID, userID string, update Update) (Settings, error)
	InspectAssets(ctx context.Context, settings Settings) (AssetReadiness, error)
}

type AtomicCreator interface {
	CreateStream(ctx context.Context, userID string, input Create) (store.Stream, Settings, error)
}

type SQLRepository struct {
	db           *sql.DB
	draftClaimer mediaassets.DraftClaimer
	integrity    mediaassets.IntegrityInspector
	now          func() time.Time
}

func NewMariaDBRepository(db *sql.DB, media *mediaassets.SQLRepository) *SQLRepository {
	return &SQLRepository{db: db, draftClaimer: media, integrity: media, now: func() time.Time { return time.Now().UTC() }}
}

func DefaultSettings(streamID string) Settings {
	return Settings{StreamID: streamID, BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "none", DiscordSnapshotRevision: 1, Revision: 0}
}

func (r *SQLRepository) Get(ctx context.Context, streamID string) (Settings, error) {
	settings, err := getSettingsWith(ctx, r.db, strings.TrimSpace(streamID), false)
	if errors.Is(err, sql.ErrNoRows) {
		return getLegacySettings(ctx, r.db, strings.TrimSpace(streamID))
	}
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const visualSelect = `SELECT sv.stream_id,sv.background_mode,COALESCE(sv.background_asset_id,''),COALESCE(sv.background_variant_id,''),
  sv.header_title_mode,COALESCE(sv.header_title_value,''),COALESCE(sv.discord_target_mode,''),COALESCE(sv.discord_target_preset_id,''),
  COALESCE(sv.discord_target_preset_revision,0),sv.discord_snapshot_revision,COALESCE(sv.discord_guild_id,''),COALESCE(sv.discord_text_channel_id,''),
  COALESCE(sv.discord_voice_channel_id,''),sv.cover_source,COALESCE(sv.cover_preset_id,''),COALESCE(sv.cover_preset_revision,0),
  COALESCE(sv.cover_asset_id,''),COALESCE(sv.cover_variant_id,''),sv.cover_start_active,sv.revision,sv.created_at,sv.updated_at,
  EXISTS(SELECT 1 FROM discord_target_presets dp WHERE dp.id=sv.discord_target_preset_id AND dp.deleted_at IS NOT NULL)
  FROM stream_visual_settings sv`

func getSettingsWith(ctx context.Context, q queryer, streamID string, lock bool) (Settings, error) {
	query := visualSelect + ` WHERE sv.stream_id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanSettings(q.QueryRowContext(ctx, query, streamID))
}

type scanner interface{ Scan(...any) error }

func scanSettings(row scanner) (Settings, error) {
	var settings Settings
	err := row.Scan(&settings.StreamID, &settings.BackgroundMode, &settings.BackgroundAssetID, &settings.BackgroundVariantID, &settings.HeaderTitleMode, &settings.HeaderTitleValue, &settings.DiscordTargetMode, &settings.DiscordTargetPresetID, &settings.DiscordTargetPresetRevision, &settings.DiscordSnapshotRevision, &settings.DiscordGuildID, &settings.DiscordTextChannelID, &settings.DiscordVoiceChannelID, &settings.CoverSource, &settings.CoverPresetID, &settings.CoverPresetRevision, &settings.CoverAssetID, &settings.CoverVariantID, &settings.CoverStartActive, &settings.Revision, &settings.CreatedAt, &settings.UpdatedAt, &settings.DiscordPresetDeleted)
	return settings, err
}

func getLegacySettings(ctx context.Context, q queryer, streamID string) (Settings, error) {
	settings := DefaultSettings(streamID)
	var guild, text, voice sql.NullString
	err := q.QueryRowContext(ctx, `SELECT s.id,ss.discord_guild_id,ss.discord_text_channel_id,ss.discord_voice_channel_id FROM streams s LEFT JOIN stream_settings ss ON ss.stream_id=s.id WHERE s.id=?`, streamID).Scan(&settings.StreamID, &guild, &text, &voice)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrNotFound
	}
	if err != nil {
		return Settings{}, err
	}
	settings.DiscordGuildID = strings.TrimSpace(guild.String)
	settings.DiscordTextChannelID = strings.TrimSpace(text.String)
	settings.DiscordVoiceChannelID = strings.TrimSpace(voice.String)
	if settings.DiscordGuildID != "" || settings.DiscordTextChannelID != "" || settings.DiscordVoiceChannelID != "" {
		settings.DiscordTargetMode = "manual"
	}
	return settings, nil
}

func (r *SQLRepository) Update(ctx context.Context, streamID, userID string, update Update) (Settings, error) {
	streamID, userID = strings.TrimSpace(streamID), strings.TrimSpace(userID)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM streams WHERE id=? FOR UPDATE`, streamID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrNotFound
	} else if err != nil {
		return Settings{}, err
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return Settings{}, ErrStreamStateLocked
	}
	current, err := getSettingsWith(ctx, tx, streamID, true)
	if errors.Is(err, sql.ErrNoRows) {
		current, err = getLegacySettings(ctx, tx, streamID)
	}
	if err != nil {
		return Settings{}, err
	}
	if current.Revision != update.ExpectedRevision {
		return Settings{}, ErrRevisionConflict
	}
	next, changes, err := applyUpdate(current, update)
	if err != nil {
		return Settings{}, err
	}
	if sessionID := strings.TrimSpace(update.UploadSessionID); sessionID != "" {
		selectedAssetIDs := selectedUploadAssetIDs(next, changes)
		if len(selectedAssetIDs) == 0 {
			return Settings{}, ErrInvalidSettings
		}
		if r.draftClaimer == nil {
			return Settings{}, ErrAssetClaim
		}
		if err := r.draftClaimer.ClaimDraftTx(ctx, tx, userID, sessionID, streamID, r.now()); err != nil {
			return Settings{}, fmt.Errorf("%w: %v", ErrAssetClaim, err)
		}
		if err := validateClaimedDraftSelection(ctx, tx, userID, sessionID, streamID, selectedAssetIDs); err != nil {
			return Settings{}, err
		}
	}
	if err := r.resolveAndValidate(ctx, tx, userID, streamID, &next, changes); err != nil {
		return Settings{}, err
	}
	now := r.now()
	next.Revision = current.Revision + 1
	next.UpdatedAt = now
	if next.CreatedAt.IsZero() {
		next.CreatedAt = now
	}
	if err := upsertSettings(ctx, tx, next, changes.discord, now); err != nil {
		return Settings{}, err
	}
	// Stream Start owns a database CAS over streams.updated_at. Advance that
	// authority in the same transaction as every visual mutation so a Start
	// that read readiness on another Panel process cannot claim and dispatch a
	// stale appearance snapshot after this commit.
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET updated_at=IF(updated_at>=?,TIMESTAMPADD(MICROSECOND,1,updated_at),?) WHERE id=?`, now, now, streamID); err != nil {
		return Settings{}, err
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, err
	}
	return r.Get(ctx, streamID)
}

func (r *SQLRepository) CreateStream(ctx context.Context, userID string, input Create) (store.Stream, Settings, error) {
	userID = strings.TrimSpace(userID)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 255 {
		return store.Stream{}, Settings{}, ErrInvalidSettings
	}
	if input.Settings.ExpectedRevision != 0 {
		return store.Stream{}, Settings{}, ErrRevisionConflict
	}
	now := r.now()
	streamID, err := newID()
	if err != nil {
		return store.Stream{}, Settings{}, err
	}
	stream := store.Stream{ID: streamID, Name: input.Name, Status: "created", CreatedAt: now, UpdatedAt: now}
	settingsBase := DefaultSettings(stream.ID)
	settingsBase.DiscordGuildID = strings.TrimSpace(input.LegacySettings.DiscordGuildID)
	settingsBase.DiscordTextChannelID = strings.TrimSpace(input.LegacySettings.DiscordTextID)
	settingsBase.DiscordVoiceChannelID = strings.TrimSpace(input.LegacySettings.DiscordVoiceID)
	if settingsBase.DiscordGuildID != "" || settingsBase.DiscordTextChannelID != "" || settingsBase.DiscordVoiceChannelID != "" {
		settingsBase.DiscordTargetMode = "manual"
	}
	settings, changes, err := applyUpdate(settingsBase, input.Settings)
	if err != nil {
		return store.Stream{}, Settings{}, err
	}
	// Creation has no prior persisted snapshot. Any selected media must therefore
	// be resolved and validated even when it came from the legacy/default base.
	changes.backgroundSelection = settings.BackgroundMode == "image"
	changes.coverSelection = settings.CoverSource != "none"
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return store.Stream{}, Settings{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO streams(id,name,status,created_at,updated_at)VALUES(?,?,'created',?,?)`, stream.ID, stream.Name, now, now); err != nil {
		return store.Stream{}, Settings{}, err
	}
	if sessionID := strings.TrimSpace(input.UploadSessionID); sessionID != "" {
		selectedAssetIDs := selectedUploadAssetIDs(settings, changes)
		if len(selectedAssetIDs) == 0 {
			return store.Stream{}, Settings{}, ErrInvalidSettings
		}
		if r.draftClaimer == nil {
			return store.Stream{}, Settings{}, ErrAssetClaim
		}
		if err = r.draftClaimer.ClaimDraftTx(ctx, tx, userID, sessionID, stream.ID, now); err != nil {
			return store.Stream{}, Settings{}, fmt.Errorf("%w: %v", ErrAssetClaim, err)
		}
		if err = validateClaimedDraftSelection(ctx, tx, userID, sessionID, stream.ID, selectedAssetIDs); err != nil {
			return store.Stream{}, Settings{}, err
		}
	}
	if err = r.resolveAndValidate(ctx, tx, userID, stream.ID, &settings, changes); err != nil {
		return store.Stream{}, Settings{}, err
	}
	settings.Revision = 1
	settings.CreatedAt = now
	settings.UpdatedAt = now
	if err = upsertSettings(ctx, tx, settings, true, now); err != nil {
		return store.Stream{}, Settings{}, err
	}
	legacy := input.LegacySettings
	legacy.Name = stream.Name
	legacy.DiscordGuildID = settings.DiscordGuildID
	legacy.DiscordTextID = settings.DiscordTextChannelID
	legacy.DiscordVoiceID = settings.DiscordVoiceChannelID
	if err = store.UpsertStreamSettingsTx(ctx, tx, stream.ID, legacy, now); err != nil {
		return store.Stream{}, Settings{}, err
	}
	if err = tx.Commit(); err != nil {
		return store.Stream{}, Settings{}, err
	}
	applyLegacySettings(&stream, legacy)
	return stream, settings, nil
}

func selectedUploadAssetIDs(settings Settings, changes updateChanges) []string {
	selected := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			selected = append(selected, value)
		}
	}
	if changes.backgroundSelection && settings.BackgroundMode == "image" {
		add(settings.BackgroundAssetID)
	}
	if changes.coverSelection && settings.CoverSource == "upload" {
		add(settings.CoverAssetID)
	}
	return selected
}

func validateClaimedDraftSelection(ctx context.Context, tx *sql.Tx, userID, sessionID, streamID string, expectedIDs []string) error {
	expected := make(map[string]bool, len(expectedIDs))
	for _, id := range expectedIDs {
		expected[strings.TrimSpace(id)] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,owner_user_id,owner_type,owner_id FROM media_assets WHERE upload_session_id=? FOR UPDATE`, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	observed := map[string]bool{}
	for rows.Next() {
		var id, ownerUserID, ownerType, ownerID string
		if err := rows.Scan(&id, &ownerUserID, &ownerType, &ownerID); err != nil {
			return err
		}
		if ownerUserID != userID || ownerType != "stream" || ownerID != streamID || !expected[id] {
			return ErrAssetClaim
		}
		observed[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(observed) != len(expected) {
		return ErrAssetClaim
	}
	return nil
}

func applyLegacySettings(stream *store.Stream, settings store.StreamSettings) {
	if stream == nil {
		return
	}
	stream.Name = strings.TrimSpace(settings.Name)
	stream.ScheduledStartAt = settings.ScheduledStartAt
	stream.ScheduledEndAt = settings.ScheduledEndAt
	stream.DiscordConfigID = strings.TrimSpace(settings.DiscordConfigID)
	stream.DiscordGuildID = strings.TrimSpace(settings.DiscordGuildID)
	stream.DiscordVoiceID = strings.TrimSpace(settings.DiscordVoiceID)
	stream.DiscordTextID = strings.TrimSpace(settings.DiscordTextID)
	stream.AutoStartTrigger = strings.TrimSpace(settings.AutoStartTrigger)
	stream.EncoderProfileID = strings.TrimSpace(settings.EncoderProfileID)
	stream.CaptionProfileID = strings.TrimSpace(settings.CaptionProfileID)
	stream.OverlayProfileID = strings.TrimSpace(settings.OverlayProfileID)
	stream.EncoderAudioGainDB = settings.EncoderAudioGainDB
	stream.ArchiveProfileID = strings.TrimSpace(settings.ArchiveProfileID)
	stream.ArchiveDriveDestinationID = strings.TrimSpace(settings.ArchiveDriveDestinationID)
	stream.ArchiveOAuthAccountID = strings.TrimSpace(settings.ArchiveOAuthAccountID)
	stream.ArchiveSharedDrive = settings.ArchiveSharedDrive
	stream.ArchiveSharedDriveID = strings.TrimSpace(settings.ArchiveSharedDriveID)
	stream.ArchiveFileName = strings.TrimSpace(settings.ArchiveFileName)
	stream.YouTubeOutputID = strings.TrimSpace(settings.YouTubeOutputID)
	stream.EncoderInputURL = strings.TrimSpace(settings.EncoderInputURL)
}

type updateChanges struct {
	discord             bool
	backgroundSelection bool
	coverSelection      bool
}

func applyUpdate(current Settings, update Update) (Settings, updateChanges, error) {
	next := current
	changes := updateChanges{
		backgroundSelection: update.BackgroundMode.Set || update.BackgroundAssetID.Set || update.BackgroundVariantID.Set,
	}
	if update.BackgroundMode.Set {
		if !update.BackgroundMode.Valid || strings.TrimSpace(update.BackgroundMode.Value) == "" {
			next.BackgroundMode = "default"
			next.BackgroundAssetID = ""
			next.BackgroundVariantID = ""
		} else {
			next.BackgroundMode = strings.TrimSpace(update.BackgroundMode.Value)
		}
	}
	if update.BackgroundAssetID.Set {
		if update.BackgroundAssetID.Valid {
			next.BackgroundAssetID = strings.TrimSpace(update.BackgroundAssetID.Value)
		} else {
			next.BackgroundAssetID = ""
		}
	}
	if update.BackgroundVariantID.Set {
		if update.BackgroundVariantID.Valid {
			next.BackgroundVariantID = strings.TrimSpace(update.BackgroundVariantID.Value)
		} else {
			next.BackgroundVariantID = ""
		}
	}
	if next.BackgroundMode == "default" {
		next.BackgroundAssetID = ""
		next.BackgroundVariantID = ""
	}
	if update.HeaderTitleMode.Set {
		if !update.HeaderTitleMode.Valid || strings.TrimSpace(update.HeaderTitleMode.Value) == "" {
			next.HeaderTitleMode = "default"
			next.HeaderTitleValue = ""
		} else {
			next.HeaderTitleMode = strings.TrimSpace(update.HeaderTitleMode.Value)
		}
	}
	if update.HeaderTitleValue.Set {
		if update.HeaderTitleValue.Valid {
			next.HeaderTitleValue = strings.TrimSpace(update.HeaderTitleValue.Value)
		} else {
			next.HeaderTitleValue = ""
		}
	}
	if next.HeaderTitleMode == "default" {
		next.HeaderTitleValue = ""
	}
	if update.DiscordTargetMode.Set {
		if !update.DiscordTargetMode.Valid || strings.TrimSpace(update.DiscordTargetMode.Value) == "" {
			next.DiscordTargetMode = "inherit"
		} else {
			next.DiscordTargetMode = strings.TrimSpace(update.DiscordTargetMode.Value)
		}
	}
	discordPresetInput := update.DiscordTargetPresetID.Set || update.DiscordTargetPresetRevision != nil
	discordManualInput := update.DiscordGuildID.Set || update.DiscordTextChannelID.Set || update.DiscordVoiceChannelID.Set
	if next.DiscordTargetMode == "preset" {
		if update.DiscordTargetMode.Set && current.DiscordTargetMode != "preset" {
			next.DiscordTargetPresetID = ""
			next.DiscordTargetPresetRevision = 0
		}
		if update.DiscordTargetPresetID.Set {
			next.DiscordTargetPresetID = optionalValue(update.DiscordTargetPresetID)
		}
		if update.DiscordTargetPresetRevision != nil {
			next.DiscordTargetPresetRevision = *update.DiscordTargetPresetRevision
		}
	}
	if next.DiscordTargetMode == "manual" {
		if update.DiscordTargetMode.Set && current.DiscordTargetMode != "manual" {
			next.DiscordGuildID = ""
			next.DiscordTextChannelID = ""
			next.DiscordVoiceChannelID = ""
		}
		next.DiscordTargetPresetID = ""
		next.DiscordTargetPresetRevision = 0
		if update.DiscordGuildID.Set {
			next.DiscordGuildID = optionalValue(update.DiscordGuildID)
		}
		if update.DiscordTextChannelID.Set {
			next.DiscordTextChannelID = optionalValue(update.DiscordTextChannelID)
		}
		if update.DiscordVoiceChannelID.Set {
			next.DiscordVoiceChannelID = optionalValue(update.DiscordVoiceChannelID)
		}
	}
	if next.DiscordTargetMode == "inherit" {
		next.DiscordTargetPresetID = ""
		next.DiscordTargetPresetRevision = 0
		next.DiscordGuildID = ""
		next.DiscordTextChannelID = ""
		next.DiscordVoiceChannelID = ""
	}
	changes.discord = update.DiscordTargetMode.Set || (next.DiscordTargetMode == "preset" && discordPresetInput) || (next.DiscordTargetMode == "manual" && discordManualInput)
	if update.CoverSource.Set {
		if !update.CoverSource.Valid || strings.TrimSpace(update.CoverSource.Value) == "" {
			next.CoverSource = "none"
		} else {
			next.CoverSource = strings.TrimSpace(update.CoverSource.Value)
		}
	}
	coverPresetInput := update.CoverPresetID.Set
	coverUploadInput := update.CoverAssetID.Set || update.CoverVariantID.Set
	if next.CoverSource == "preset" {
		if update.CoverSource.Set && current.CoverSource != "preset" {
			next.CoverPresetID = ""
			next.CoverPresetRevision = 0
			next.CoverAssetID = ""
			next.CoverVariantID = ""
		}
		if update.CoverPresetID.Set {
			next.CoverPresetID = optionalValue(update.CoverPresetID)
		}
	}
	if next.CoverSource == "upload" {
		if update.CoverSource.Set && current.CoverSource != "upload" {
			next.CoverAssetID = ""
			next.CoverVariantID = ""
		}
		next.CoverPresetID = ""
		next.CoverPresetRevision = 0
		if update.CoverAssetID.Set {
			next.CoverAssetID = optionalValue(update.CoverAssetID)
		}
		if update.CoverVariantID.Set {
			next.CoverVariantID = optionalValue(update.CoverVariantID)
		}
	}
	if update.CoverStartActive != nil {
		next.CoverStartActive = *update.CoverStartActive
	}
	if next.CoverSource == "none" {
		next.CoverPresetID = ""
		next.CoverPresetRevision = 0
		next.CoverAssetID = ""
		next.CoverVariantID = ""
		next.CoverStartActive = false
	}
	changes.coverSelection = update.CoverSource.Set || (next.CoverSource == "preset" && coverPresetInput) || (next.CoverSource == "upload" && coverUploadInput)
	return next, changes, nil
}

func optionalValue(value OptionalString) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func (r *SQLRepository) resolveAndValidate(ctx context.Context, tx *sql.Tx, userID, streamID string, settings *Settings, changes updateChanges) error {
	if changes.discord && settings.DiscordTargetMode == "preset" {
		requestedRevision := settings.DiscordTargetPresetRevision
		if requestedRevision == 0 {
			return ErrInvalidSettings
		}
		var deleted sql.NullTime
		var revision uint64
		err := tx.QueryRowContext(ctx, `SELECT revision,guild_id,text_channel_id,voice_channel_id,deleted_at FROM discord_target_presets WHERE id=? FOR UPDATE`, settings.DiscordTargetPresetID).Scan(&revision, &settings.DiscordGuildID, &settings.DiscordTextChannelID, &settings.DiscordVoiceChannelID, &deleted)
		if errors.Is(err, sql.ErrNoRows) || deleted.Valid {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if requestedRevision != revision {
			return ErrRevisionConflict
		}
		settings.DiscordTargetPresetRevision = revision
		settings.DiscordPresetDeleted = false
	}
	if changes.coverSelection && settings.CoverSource == "preset" {
		var enabled bool
		var deleted sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT revision,asset_id,asset_variant_id,enabled,deleted_at FROM video_cover_presets WHERE id=? FOR UPDATE`, settings.CoverPresetID).Scan(&settings.CoverPresetRevision, &settings.CoverAssetID, &settings.CoverVariantID, &enabled, &deleted)
		if errors.Is(err, sql.ErrNoRows) || deleted.Valid || !enabled {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	if err := ValidateSettings(*settings); err != nil {
		return err
	}
	if changes.backgroundSelection && settings.BackgroundMode == "image" {
		if err := validateOwnedVariant(ctx, tx, userID, streamID, settings.BackgroundAssetID, settings.BackgroundVariantID, "scene_background"); err != nil {
			return err
		}
	}
	if changes.coverSelection && settings.CoverSource != "none" {
		if settings.CoverSource == "preset" {
			if err := validateReferencedVariant(ctx, tx, settings.CoverAssetID, settings.CoverVariantID, "video_cover"); err != nil {
				return err
			}
		} else if err := validateOwnedVariant(ctx, tx, userID, streamID, settings.CoverAssetID, settings.CoverVariantID, "video_cover"); err != nil {
			return err
		}
	}
	if changes.discord {
		settings.DiscordSnapshotRevision++
		if settings.DiscordSnapshotRevision == 0 {
			settings.DiscordSnapshotRevision = 1
		}
	}
	return nil
}

func validateOwnedVariant(ctx context.Context, tx *sql.Tx, userID, streamID, assetID, variantID, usage string) error {
	var status string
	var ownerUser, ownerType, ownerID, actualUsage string
	var sourceWidth, sourceHeight, targetWidth, targetHeight int
	var opaque bool
	err := tx.QueryRowContext(ctx, `SELECT v.status,a.owner_user_id,a.owner_type,a.owner_id,a.usage_type,a.width,a.height,v.width,v.height,v.opaque FROM media_asset_variants v JOIN media_assets a ON a.id=v.asset_id WHERE a.id=? AND v.id=? AND a.deleted_at IS NULL`, assetID, variantID).Scan(&status, &ownerUser, &ownerType, &ownerID, &actualUsage, &sourceWidth, &sourceHeight, &targetWidth, &targetHeight, &opaque)
	if errors.Is(err, sql.ErrNoRows) {
		return mediaassets.ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerUser != userID || ownerType != "stream" || ownerID != streamID || actualUsage != usage {
		return mediaassets.ErrForbidden
	}
	if status != "ready" {
		return mediaassets.ErrIntegrity
	}
	if err := validateVariantPresentation(actualUsage, sourceWidth, sourceHeight, targetWidth, targetHeight, opaque); err != nil {
		return err
	}
	return nil
}

// A preset ID is the only client-controlled selector. The server has already
// resolved its immutable asset snapshot, which may be system-owned or belong
// to a different stream, so destination-stream ownership is intentionally not
// applied here.
func validateReferencedVariant(ctx context.Context, tx *sql.Tx, assetID, variantID, usage string) error {
	var status, actualUsage string
	var sourceWidth, sourceHeight, targetWidth, targetHeight int
	var opaque bool
	err := tx.QueryRowContext(ctx, `SELECT v.status,a.usage_type,a.width,a.height,v.width,v.height,v.opaque FROM media_asset_variants v JOIN media_assets a ON a.id=v.asset_id WHERE a.id=? AND v.id=? AND a.deleted_at IS NULL`, assetID, variantID).Scan(&status, &actualUsage, &sourceWidth, &sourceHeight, &targetWidth, &targetHeight, &opaque)
	if errors.Is(err, sql.ErrNoRows) {
		return mediaassets.ErrNotFound
	}
	if err != nil {
		return err
	}
	if actualUsage != usage {
		return mediaassets.ErrForbidden
	}
	if status != "ready" {
		return mediaassets.ErrIntegrity
	}
	if err := validateVariantPresentation(actualUsage, sourceWidth, sourceHeight, targetWidth, targetHeight, opaque); err != nil {
		return err
	}
	return nil
}

func validateVariantPresentation(usage string, sourceWidth, sourceHeight, targetWidth, targetHeight int, opaque bool) error {
	if !opaque {
		return ErrInvalidSettings
	}
	if usage == "video_cover" {
		if targetWidth != 1920 || targetHeight != 1080 || sourceHeight == 0 {
			return ErrInvalidSettings
		}
		ratioDelta := float64(sourceWidth)/float64(sourceHeight)/(16.0/9.0) - 1
		if ratioDelta < -0.001 || ratioDelta > 0.001 {
			return ErrInvalidSettings
		}
	}
	return nil
}

func upsertSettings(ctx context.Context, tx *sql.Tx, settings Settings, discordChanged bool, now time.Time) error {
	if discordChanged {
		_, err := tx.ExecContext(ctx, `INSERT INTO stream_settings(stream_id,discord_guild_id,discord_text_channel_id,discord_voice_channel_id,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE discord_guild_id=VALUES(discord_guild_id),discord_text_channel_id=VALUES(discord_text_channel_id),discord_voice_channel_id=VALUES(discord_voice_channel_id),discord_target_mode=VALUES(discord_target_mode),discord_target_preset_id=VALUES(discord_target_preset_id),discord_target_preset_revision=VALUES(discord_target_preset_revision),updated_at=VALUES(updated_at)`, settings.StreamID, nullString(settings.DiscordGuildID), nullString(settings.DiscordTextChannelID), nullString(settings.DiscordVoiceChannelID), nullString(settings.DiscordTargetMode), nullString(settings.DiscordTargetPresetID), nullUint(settings.DiscordTargetPresetRevision), now)
		if err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_visual_settings(stream_id,background_mode,background_asset_id,background_variant_id,header_title_mode,header_title_value,discord_target_mode,discord_target_preset_id,discord_target_preset_revision,discord_snapshot_revision,discord_guild_id,discord_text_channel_id,discord_voice_channel_id,cover_source,cover_preset_id,cover_preset_revision,cover_asset_id,cover_variant_id,cover_start_active,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE background_mode=VALUES(background_mode),background_asset_id=VALUES(background_asset_id),background_variant_id=VALUES(background_variant_id),header_title_mode=VALUES(header_title_mode),header_title_value=VALUES(header_title_value),discord_target_mode=VALUES(discord_target_mode),discord_target_preset_id=VALUES(discord_target_preset_id),discord_target_preset_revision=VALUES(discord_target_preset_revision),discord_snapshot_revision=VALUES(discord_snapshot_revision),discord_guild_id=VALUES(discord_guild_id),discord_text_channel_id=VALUES(discord_text_channel_id),discord_voice_channel_id=VALUES(discord_voice_channel_id),cover_source=VALUES(cover_source),cover_preset_id=VALUES(cover_preset_id),cover_preset_revision=VALUES(cover_preset_revision),cover_asset_id=VALUES(cover_asset_id),cover_variant_id=VALUES(cover_variant_id),cover_start_active=VALUES(cover_start_active),revision=VALUES(revision),updated_at=VALUES(updated_at)`, settings.StreamID, settings.BackgroundMode, nullString(settings.BackgroundAssetID), nullString(settings.BackgroundVariantID), settings.HeaderTitleMode, nullString(settings.HeaderTitleValue), nullString(settings.DiscordTargetMode), nullString(settings.DiscordTargetPresetID), nullUint(settings.DiscordTargetPresetRevision), settings.DiscordSnapshotRevision, nullString(settings.DiscordGuildID), nullString(settings.DiscordTextChannelID), nullString(settings.DiscordVoiceChannelID), settings.CoverSource, nullString(settings.CoverPresetID), nullUint(settings.CoverPresetRevision), nullString(settings.CoverAssetID), nullString(settings.CoverVariantID), settings.CoverStartActive, settings.Revision, settings.CreatedAt, settings.UpdatedAt)
	return err
}

func (r *SQLRepository) InspectAssets(ctx context.Context, settings Settings) (AssetReadiness, error) {
	result := AssetReadiness{BackgroundExists: settings.BackgroundMode != "image", BackgroundVariantReady: settings.BackgroundMode != "image", BackgroundHashVerified: settings.BackgroundMode != "image", CoverVariantReady: settings.CoverSource == "none", MediaAssetIntegrity: settings.CoverSource == "none"}
	if settings.BackgroundMode == "image" {
		var status string
		err := r.db.QueryRowContext(ctx, `SELECT v.status FROM media_assets a JOIN media_asset_variants v ON v.asset_id=a.id WHERE a.id=? AND v.id=?`, settings.BackgroundAssetID, settings.BackgroundVariantID).Scan(&status)
		if err == nil {
			result.BackgroundExists = true
			result.BackgroundVariantReady = status == "ready"
			if status == "ready" && r.integrity != nil && r.integrity.VerifyVariant(ctx, settings.BackgroundAssetID, settings.BackgroundVariantID) == nil {
				result.BackgroundHashVerified = true
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	if settings.CoverSource != "none" {
		var status string
		err := r.db.QueryRowContext(ctx, `SELECT v.status FROM media_assets a JOIN media_asset_variants v ON v.asset_id=a.id WHERE a.id=? AND v.id=?`, settings.CoverAssetID, settings.CoverVariantID).Scan(&status)
		if err == nil {
			result.CoverVariantReady = status == "ready"
			if status == "ready" && r.integrity != nil && r.integrity.VerifyVariant(ctx, settings.CoverAssetID, settings.CoverVariantID) == nil {
				result.MediaAssetIntegrity = true
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	return result, nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
func nullUint(value uint64) any {
	if value == 0 {
		return nil
	}
	return value
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	e := hex.EncodeToString(b[:])
	return e[:8] + "-" + e[8:12] + "-" + e[12:16] + "-" + e[16:20] + "-" + e[20:], nil
}

var _ Repository = (*SQLRepository)(nil)
var _ AtomicCreator = (*SQLRepository)(nil)
