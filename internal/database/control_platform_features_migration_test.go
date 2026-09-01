package database

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func TestControlPlatformMigrationContract(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/079_control_platform_features.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := splitSQLStatements(string(body))
	if len(statements) < 20 {
		t.Fatalf("migration statement denominator unexpectedly small: %d", len(statements))
	}
	requiredTables := []string{
		"user_ui_preferences", "media_upload_sessions", "media_assets", "media_asset_variants",
		"discord_target_presets", "video_cover_presets", "stream_visual_settings",
		"stream_video_cover_runtime", "stream_video_cover_actions",
	}
	normalized := strings.ToLower(string(body))
	for _, table := range requiredTables {
		if !strings.Contains(normalized, "create table if not exists "+table) {
			t.Fatalf("missing additive table contract %s", table)
		}
	}
	for _, statement := range statements {
		trimmed := strings.ToLower(strings.TrimSpace(statement))
		if strings.HasPrefix(trimmed, "drop ") || strings.HasPrefix(trimmed, "truncate ") || strings.HasPrefix(trimmed, "delete ") || strings.HasPrefix(trimmed, "update streams ") {
			t.Fatalf("destructive or bulk legacy rewrite in forward migration: %s", trimmed)
		}
	}
	for _, permission := range []string{
		"discord_target_presets.read", "discord_target_presets.create", "discord_target_presets.update", "discord_target_presets.delete",
		"video_cover_presets.read", "video_cover_presets.create", "video_cover_presets.update", "video_cover_presets.delete",
		"streams.show_cover", "streams.hide_cover",
	} {
		if !strings.Contains(normalized, "'"+permission+"'") {
			t.Fatalf("permission seed missing: %s", permission)
		}
	}
	for _, legacy := range []string{"discord_guild_id", "discord_text_channel_id", "discord_voice_channel_id"} {
		if !strings.Contains(normalized, legacy) {
			t.Fatalf("legacy Discord snapshot column omitted: %s", legacy)
		}
	}
}

func TestMariaDBControlPlatformFeatureMigrationAndPersistence(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)

	for _, table := range []string{
		"user_ui_preferences", "media_upload_sessions", "media_assets", "media_asset_variants",
		"discord_target_presets", "video_cover_presets", "stream_visual_settings",
		"stream_video_cover_runtime", "stream_video_cover_actions",
	} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	var streamUpdatedAtPrecision int
	if err := db.QueryRowContext(ctx, `SELECT DATETIME_PRECISION FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='streams' AND column_name='updated_at'`).Scan(&streamUpdatedAtPrecision); err != nil || streamUpdatedAtPrecision != 6 {
		t.Fatalf("streams.updated_at precision=%v err=%v", streamUpdatedAtPrecision, err)
	}
	// Replaying the exact migration models a crash after DDL but before the
	// schema_migrations record. It must remain retry-safe.
	replayEmbeddedMariaDBMigration(t, ctx, db, "migrations/079_control_platform_features.sql")
	replayEmbeddedMariaDBMigration(t, ctx, db, "migrations/079_control_platform_features.sql")

	userID := controlPlatformTestUUID(t)
	secondUserID := controlPlatformTestUUID(t)
	concurrentUserID := controlPlatformTestUUID(t)
	now := time.Now().UTC()
	for index, id := range []string{userID, secondUserID, concurrentUserID} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,email,password_hash,status,created_at,updated_at) VALUES(?,?,NULL,'test-hash','active',?,?)`, id, "bundle4-"+string(rune('a'+index))+"-"+id[:8], now, now); err != nil {
			t.Fatal(err)
		}
	}

	themes := store.NewMariaDBUserUIPreferenceStore(db)
	preference, err := themes.UpdateUserUIPreference(ctx, userID, "violet", "dark", 0)
	if err != nil || preference.Revision != 1 {
		t.Fatalf("theme create=%#v err=%v", preference, err)
	}
	if _, err := themes.UpdateUserUIPreference(ctx, userID, "ocean", "light", 0); !errors.Is(err, store.ErrRevisionConflict) {
		t.Fatalf("theme stale CAS=%v", err)
	}
	other, err := themes.GetUserUIPreference(ctx, secondUserID)
	if err != nil || other.Revision != 0 || other.ThemeID != "autostream" || other.ColorMode != "system" {
		t.Fatalf("theme isolation=%#v err=%v", other, err)
	}
	startConcurrentPreference := make(chan struct{})
	concurrentPreferenceResults := make(chan error, 2)
	for _, themeID := range []string{"ocean", "violet"} {
		go func(themeID string) {
			<-startConcurrentPreference
			_, updateErr := themes.UpdateUserUIPreference(ctx, concurrentUserID, themeID, "dark", 0)
			concurrentPreferenceResults <- updateErr
		}(themeID)
	}
	close(startConcurrentPreference)
	successes, conflicts := 0, 0
	for range 2 {
		updateErr := <-concurrentPreferenceResults
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, store.ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent first-write returned unsafe error: %v", updateErr)
		}
	}
	concurrentPreference, err := themes.GetUserUIPreference(ctx, concurrentUserID)
	if successes != 1 || conflicts != 1 || err != nil || concurrentPreference.Revision != 1 {
		t.Fatalf("concurrent first-write successes=%d conflicts=%d preference=%#v err=%v", successes, conflicts, concurrentPreference, err)
	}

	streamStore := store.NewMariaDBStreamStore(db)
	legacy, err := streamStore.CreateStream(ctx, "Bundle 4 legacy target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streamStore.UpdateStreamSettings(ctx, legacy.ID, store.StreamSettings{DiscordGuildID: "1001", DiscordTextID: "1002", DiscordVoiceID: "1003"}); err != nil {
		t.Fatal(err)
	}
	legacySettings, err := streamvisual.NewMariaDBRepository(db, nil).Get(ctx, legacy.ID)
	if err != nil || legacySettings.DiscordTargetMode != "manual" || legacySettings.DiscordGuildID != "1001" || legacySettings.DiscordTextChannelID != "1002" || legacySettings.DiscordVoiceChannelID != "1003" {
		t.Fatalf("legacy Discord migration=%#v err=%v", legacySettings, err)
	}

	storageRoot := t.TempDir()
	media, err := mediaassets.NewMariaDBRepository(db, storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	lockedSession, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	defer db.SetMaxOpenConns(0)
	uploadReachedProcessing := make(chan struct{})
	uploadProcessingRelease := make(chan struct{})
	uploadProcessingReleased := false
	defer func() {
		if !uploadProcessingReleased {
			close(uploadProcessingRelease)
		}
	}()
	uploadResult := make(chan error, 1)
	latePNG := controlPlatformPNG(t, 32, 18, false)
	go func() {
		body := &phaseBarrierReader{reader: bytes.NewReader(latePNG), reached: uploadReachedProcessing, release: uploadProcessingRelease}
		_, uploadErr := media.Upload(ctx, mediaassets.UploadInput{SessionID: lockedSession.ID, UserID: userID, UsageType: "scene_background", Filename: "late.png", ContentType: "image/png", Body: body})
		uploadResult <- uploadErr
	}()
	// ProcessUpload cannot read the body until the initial draft validation has
	// succeeded. This barrier therefore proves the upload observed a draft
	// before the competing claim transaction begins.
	select {
	case <-uploadReachedProcessing:
	case uploadErr := <-uploadResult:
		t.Fatalf("upload failed before the processing barrier: %v", uploadErr)
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach the processing barrier")
	}
	claimTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedSessionID string
	if err = claimTx.QueryRowContext(ctx, `SELECT id FROM media_upload_sessions WHERE id=? FOR UPDATE`, lockedSession.ID).Scan(&lockedSessionID); err != nil {
		_ = claimTx.Rollback()
		t.Fatal(err)
	}
	close(uploadProcessingRelease)
	uploadProcessingReleased = true
	// With the pool bounded to two connections, InUse=2 proves that the
	// upload has left image processing and entered its final transaction while
	// claimTx owns the session row lock. A goroutine-start barrier is not enough
	// to establish this lock phase.
	phaseDeadline := time.Now().Add(5 * time.Second)
	for db.Stats().InUse != 2 && time.Now().Before(phaseDeadline) {
		select {
		case uploadErr := <-uploadResult:
			_ = claimTx.Rollback()
			t.Fatalf("upload bypassed the final claim lock phase: %v", uploadErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if db.Stats().InUse != 2 {
		_ = claimTx.Rollback()
		t.Fatal("upload did not reach its final database transaction")
	}
	if _, err = claimTx.ExecContext(ctx, `UPDATE media_upload_sessions SET owner_type='stream',claimed_stream_id=?,updated_at=? WHERE id=?`, legacy.ID, now, lockedSession.ID); err != nil {
		_ = claimTx.Rollback()
		t.Fatal(err)
	}
	if err = claimTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case uploadErr := <-uploadResult:
		if !errors.Is(uploadErr, mediaassets.ErrDraftClaimed) {
			t.Fatalf("late upload error=%v", uploadErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late upload did not finish after claim commit")
	}
	var lateAssets int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE upload_session_id=?`, lockedSession.ID).Scan(&lateAssets); err != nil || lateAssets != 0 {
		t.Fatalf("late upload rows=%d err=%v", lateAssets, err)
	}
	session, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	background, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: "scene_background", Filename: "background.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, true))})
	if err != nil {
		t.Fatal(err)
	}
	backgroundVariant, err := media.EnsureVariant(ctx, userID, background.ID, 1280, 720, true)
	if err != nil {
		t.Fatal(err)
	}
	visuals := streamvisual.NewMariaDBRepository(db, media)
	if _, _, err := visuals.CreateStream(ctx, userID, streamvisual.Create{Name: "unselected draft claim", UploadSessionID: session.ID}); !errors.Is(err, streamvisual.ErrInvalidSettings) {
		t.Fatalf("unselected draft was claimable: %v", err)
	}
	unselectedAsset, err := media.GetAsset(ctx, userID, background.ID)
	if err != nil || unselectedAsset.OwnerType != "upload_draft" || unselectedAsset.OwnerID != session.ID {
		t.Fatalf("unselected draft claim rollback=%#v err=%v", unselectedAsset, err)
	}
	invalid := streamvisual.Update{ExpectedRevision: 0, BackgroundMode: setVisualString("image"), BackgroundAssetID: setVisualString(background.ID), BackgroundVariantID: setVisualString(backgroundVariant.ID), HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("unsafe\u202etitle")}
	if _, _, err := visuals.CreateStream(ctx, userID, streamvisual.Create{Name: "invalid draft claim", UploadSessionID: session.ID, Settings: invalid}); !errors.Is(err, streamvisual.ErrInvalidSettings) {
		t.Fatalf("invalid create=%v", err)
	}
	assetAfterRollback, err := media.GetAsset(ctx, userID, background.ID)
	if err != nil || assetAfterRollback.OwnerType != "upload_draft" || assetAfterRollback.OwnerID != session.ID {
		t.Fatalf("failed create claimed draft=%#v err=%v", assetAfterRollback, err)
	}
	// streams.scheduled_start_at is an existing DATETIME column, so persistence
	// intentionally has second precision even though the new Bundle 4 tables use
	// DATETIME(6). Derive the oracle from that schema authority.
	scheduledStart := now.Add(2 * time.Hour).Truncate(time.Second)
	createdStream, createdSettings, err := visuals.CreateStream(ctx, userID, streamvisual.Create{
		Name:            "Bundle 4 visual stream",
		UploadSessionID: session.ID,
		Settings:        streamvisual.Update{ExpectedRevision: 0, BackgroundMode: setVisualString("image"), BackgroundAssetID: setVisualString(background.ID), BackgroundVariantID: setVisualString(backgroundVariant.ID), HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("Program title")},
		LegacySettings:  store.StreamSettings{ScheduledStartAt: &scheduledStart, AutoStartTrigger: "discord_voice_join", EncoderInputURL: "rtmp://127.0.0.1/live/input", DiscordGuildID: "4001", DiscordTextID: "4002", DiscordVoiceID: "4003"},
	})
	if err != nil || createdSettings.Revision != 1 || createdSettings.DiscordTargetMode != "manual" || createdSettings.DiscordGuildID != "4001" || createdSettings.DiscordTextChannelID != "4002" || createdSettings.DiscordVoiceChannelID != "4003" {
		t.Fatalf("valid create settings=%#v err=%v", createdSettings, err)
	}
	persistedStream, err := streamStore.GetStream(ctx, createdStream.ID)
	if err != nil || persistedStream.ScheduledStartAt == nil || !persistedStream.ScheduledStartAt.Equal(scheduledStart) || persistedStream.AutoStartTrigger != "discord_voice_join" || persistedStream.EncoderInputURL != "rtmp://127.0.0.1/live/input" || persistedStream.DiscordGuildID != "4001" || persistedStream.DiscordTextID != "4002" || persistedStream.DiscordVoiceID != "4003" {
		t.Fatalf("atomic legacy snapshot=%#v err=%v", persistedStream, err)
	}
	claimed, err := media.GetAsset(ctx, userID, background.ID)
	if err != nil || claimed.OwnerType != "stream" || claimed.OwnerID != createdStream.ID {
		t.Fatalf("draft claim=%#v err=%v", claimed, err)
	}
	extraneousSession, err := media.CreateUploadSession(ctx, userID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	extraneousAsset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: extraneousSession.ID, UserID: userID, UsageType: "scene_background", Filename: "extraneous.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 32, 18, false))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: createdSettings.Revision, UploadSessionID: extraneousSession.ID, HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("No unrelated claim")}); !errors.Is(err, streamvisual.ErrInvalidSettings) {
		t.Fatalf("unrelated draft session was claimable during update: %v", err)
	}
	extraneousAfterRollback, err := media.GetAsset(ctx, userID, extraneousAsset.ID)
	if err != nil || extraneousAfterRollback.OwnerType != "upload_draft" || extraneousAfterRollback.OwnerID != extraneousSession.ID {
		t.Fatalf("unrelated draft update rollback=%#v err=%v", extraneousAfterRollback, err)
	}

	presets := store.NewMariaDBDiscordTargetPresetStore(db)
	discordPreset, err := presets.CreateDiscordTargetPreset(ctx, store.DiscordTargetPreset{Name: "Main Target " + userID[:8], GuildID: "2001", TextChannelID: "2002", VoiceChannelID: "2003", CreatedByUserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	streamBeforeVisualUpdate, err := streamStore.GetStream(ctx, createdStream.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: createdSettings.Revision, DiscordTargetMode: setVisualString("preset"), DiscordTargetPresetID: setVisualString(discordPreset.ID), DiscordTargetPresetRevision: &discordPreset.Revision})
	if err != nil || selected.DiscordGuildID != "2001" || selected.DiscordTextChannelID != "2002" || selected.DiscordVoiceChannelID != "2003" {
		t.Fatalf("preset snapshot=%#v err=%v", selected, err)
	}
	legacyOverwrite, err := streamStore.UpdateStreamSettings(ctx, createdStream.ID, store.StreamSettings{Name: createdStream.Name, DiscordGuildID: "9001", DiscordTextID: "9002", DiscordVoiceID: "9003"})
	if err != nil || legacyOverwrite.DiscordGuildID != "2001" || legacyOverwrite.DiscordTextID != "2002" || legacyOverwrite.DiscordVoiceID != "2003" {
		t.Fatalf("legacy client overwrote server Discord snapshot=%#v err=%v", legacyOverwrite, err)
	}
	streamAfterVisualUpdate, err := streamStore.GetStream(ctx, createdStream.ID)
	if err != nil || !streamAfterVisualUpdate.UpdatedAt.After(streamBeforeVisualUpdate.UpdatedAt) {
		t.Fatalf("visual mutation did not advance stream Start CAS: before=%s after=%s err=%v", streamBeforeVisualUpdate.UpdatedAt, streamAfterVisualUpdate.UpdatedAt, err)
	}
	updatedPreset, err := presets.UpdateDiscordTargetPreset(ctx, discordPreset.ID, store.DiscordTargetPreset{Name: discordPreset.Name, GuildID: "3001", TextChannelID: "3002", VoiceChannelID: "3003", UpdatedByUserID: userID}, discordPreset.Revision)
	if err != nil {
		t.Fatal(err)
	}
	afterPresetUpdate, err := visuals.Get(ctx, createdStream.ID)
	if err != nil || afterPresetUpdate.DiscordGuildID != "2001" || afterPresetUpdate.DiscordTargetPresetRevision != discordPreset.Revision {
		t.Fatalf("preset update mutated snapshot=%#v err=%v", afterPresetUpdate, err)
	}
	if _, err := presets.DeleteDiscordTargetPreset(ctx, discordPreset.ID, userID, updatedPreset.Revision); err != nil {
		t.Fatal(err)
	}
	afterPresetDelete, err := visuals.Get(ctx, createdStream.ID)
	if err != nil || !afterPresetDelete.DiscordPresetDeleted || afterPresetDelete.DiscordGuildID != "2001" {
		t.Fatalf("preset delete broke snapshot=%#v err=%v", afterPresetDelete, err)
	}
	afterDeletedDiscordUpdate, err := visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: afterPresetDelete.Revision, HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("Snapshot remains immutable")})
	if err != nil || afterDeletedDiscordUpdate.DiscordGuildID != "2001" || afterDeletedDiscordUpdate.DiscordTargetPresetRevision != discordPreset.Revision || !afterDeletedDiscordUpdate.DiscordPresetDeleted {
		t.Fatalf("unrelated edit re-resolved deleted Discord snapshot=%#v err=%v", afterDeletedDiscordUpdate, err)
	}

	covers := videocover.NewMariaDBRepository(db)
	expiredPresetSession, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	expiredPresetAsset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: expiredPresetSession.ID, UserID: userID, UsageType: "video_cover", Filename: "expired-cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, false))})
	if err != nil {
		t.Fatal(err)
	}
	expiredPresetVariant, err := media.EnsureVariant(ctx, userID, expiredPresetAsset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE media_upload_sessions SET expires_at=? WHERE id=?`, now.Add(-time.Hour), expiredPresetSession.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = covers.CreatePreset(ctx, videocover.Preset{Name: "Expired " + userID[:8], AssetID: expiredPresetAsset.ID, AssetVariantID: expiredPresetVariant.ID, Enabled: true, CreatedByUserID: userID}); !errors.Is(err, videocover.ErrInvalidRequest) {
		t.Fatalf("expired draft became a cover preset: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE media_upload_sessions SET expires_at=? WHERE id=?`, now.Add(time.Hour), expiredPresetSession.ID); err != nil {
		t.Fatal(err)
	}
	expiredClaimTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = media.ClaimDraftTx(ctx, expiredClaimTx, userID, expiredPresetSession.ID, legacy.ID, now); err != nil {
		_ = expiredClaimTx.Rollback()
		t.Fatal(err)
	}
	if err = expiredClaimTx.Commit(); err != nil {
		t.Fatal(err)
	}

	coverSession, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	coverAsset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: coverSession.ID, UserID: userID, UsageType: "video_cover", Filename: "cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, false))})
	if err != nil {
		t.Fatal(err)
	}
	coverVariant, err := media.EnsureVariant(ctx, userID, coverAsset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSession, err := media.CreateUploadSession(ctx, userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	runtimeAsset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: runtimeSession.ID, UserID: userID, UsageType: "video_cover", Filename: "runtime-cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, false))})
	if err != nil {
		t.Fatal(err)
	}
	runtimeVariant, err := media.EnsureVariant(ctx, userID, runtimeAsset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = covers.EnsureGeneration(ctx, createdStream.ID, 99, runtimeVariant.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = media.SoftDeleteAsset(ctx, userID, runtimeAsset.ID, now); err != nil {
		t.Fatal(err)
	}
	if removed, gcErr := media.GarbageCollect(ctx, now.Add(25*time.Hour), 10); gcErr != nil || removed != 0 {
		t.Fatalf("runtime-referenced asset GC removed=%d err=%v", removed, gcErr)
	}
	var claimedSessionRows int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_upload_sessions WHERE id=?`, session.ID).Scan(&claimedSessionRows); err != nil || claimedSessionRows != 0 {
		t.Fatalf("expired claimed upload session remained: count=%d err=%v", claimedSessionRows, err)
	}
	coverPreset, err := covers.CreatePreset(ctx, videocover.Preset{Name: "Release " + userID[:8], AssetID: coverAsset.ID, AssetVariantID: coverVariant.ID, Enabled: true, CreatedByUserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	var coverOwnerType, coverOwnerID string
	if err := db.QueryRowContext(ctx, `SELECT owner_type,owner_id FROM media_assets WHERE id=?`, coverAsset.ID).Scan(&coverOwnerType, &coverOwnerID); err != nil || coverOwnerType != "preset" || coverOwnerID != coverPreset.ID {
		t.Fatalf("cover preset claim owner=%s/%s err=%v", coverOwnerType, coverOwnerID, err)
	}
	withCover, err := visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: afterDeletedDiscordUpdate.Revision, CoverSource: setVisualString("preset"), CoverPresetID: setVisualString(coverPreset.ID)})
	if err != nil || withCover.CoverAssetID != coverAsset.ID || withCover.CoverVariantID != coverVariant.ID || withCover.CoverPresetRevision != coverPreset.Revision {
		t.Fatalf("cover snapshot=%#v err=%v", withCover, err)
	}
	updatedCoverPreset, err := covers.UpdatePreset(ctx, coverPreset.ID, videocover.Preset{Name: coverPreset.Name + " updated", AssetID: coverAsset.ID, AssetVariantID: coverVariant.ID, Enabled: true, UpdatedByUserID: userID}, coverPreset.Revision)
	if err != nil {
		t.Fatal(err)
	}
	afterCoverPresetUpdate, err := visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: withCover.Revision, HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("Cover snapshot unchanged")})
	if err != nil || afterCoverPresetUpdate.CoverPresetRevision != coverPreset.Revision || afterCoverPresetUpdate.CoverAssetID != coverAsset.ID || afterCoverPresetUpdate.CoverVariantID != coverVariant.ID {
		t.Fatalf("unrelated edit re-resolved updated cover snapshot=%#v err=%v", afterCoverPresetUpdate, err)
	}
	if _, err = covers.DeletePreset(ctx, coverPreset.ID, userID, updatedCoverPreset.Revision); err != nil {
		t.Fatal(err)
	}
	afterDeletedCoverUpdate, err := visuals.Update(ctx, createdStream.ID, userID, streamvisual.Update{ExpectedRevision: afterCoverPresetUpdate.Revision, HeaderTitleMode: setVisualString("custom"), HeaderTitleValue: setVisualString("Deleted cover snapshot retained")})
	if err != nil || afterDeletedCoverUpdate.CoverPresetRevision != coverPreset.Revision || afterDeletedCoverUpdate.CoverAssetID != coverAsset.ID || afterDeletedCoverUpdate.CoverVariantID != coverVariant.ID {
		t.Fatalf("unrelated edit re-resolved deleted cover snapshot=%#v err=%v", afterDeletedCoverUpdate, err)
	}

	state, err := covers.EnsureGeneration(ctx, createdStream.ID, 1, coverVariant.ID, false)
	if err != nil || state.AppliedRevision != nil {
		t.Fatalf("cover generation=%#v err=%v", state, err)
	}
	action := videocover.ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: state.DesiredRevision, IdempotencyKey: "mariadb-show"}
	prepared, err := covers.PrepareAction(ctx, createdStream.ID, action)
	if err != nil || !prepared.Dispatch {
		t.Fatalf("cover prepare=%#v err=%v", prepared, err)
	}
	confirming, err := covers.RecordAmbiguous(ctx, createdStream.ID, 1, action.IdempotencyKey)
	if err != nil || confirming.Status != "confirming" || confirming.AppliedRevision != nil {
		t.Fatalf("cover ambiguous=%#v err=%v", confirming, err)
	}
	replay, err := covers.PrepareAction(ctx, createdStream.ID, action)
	if err != nil || replay.Dispatch || !replay.Replay {
		t.Fatalf("cover ambiguous auto-resend=%#v err=%v", replay, err)
	}
	applied, err := covers.RecordApplied(ctx, createdStream.ID, 1, action.IdempotencyKey, true, prepared.RequestedRevision)
	if err != nil || applied.AppliedRevision == nil || *applied.AppliedRevision != prepared.RequestedRevision {
		t.Fatalf("cover apply=%#v err=%v", applied, err)
	}

	gcSession, err := media.CreateUploadSession(ctx, userID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	gcAsset, err := media.Upload(ctx, mediaassets.UploadInput{SessionID: gcSession.ID, UserID: userID, UsageType: "video_cover", Filename: "gc-cover.png", ContentType: "image/png", Body: bytes.NewReader(controlPlatformPNG(t, 160, 90, false))})
	if err != nil {
		t.Fatal(err)
	}
	gcVariant, err := media.EnsureVariant(ctx, userID, gcAsset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	gcPreset, err := covers.CreatePreset(ctx, videocover.Preset{Name: "GC " + userID[:8], AssetID: gcAsset.ID, AssetVariantID: gcVariant.ID, Enabled: true, CreatedByUserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = covers.DeletePreset(ctx, gcPreset.ID, userID, gcPreset.Revision); err != nil {
		t.Fatal(err)
	}
	removed, err := media.GarbageCollect(ctx, time.Now().UTC().Add(25*time.Hour), 10)
	if err != nil || removed != 1 {
		t.Fatalf("deleted unreferenced cover preset asset GC removed=%d err=%v", removed, err)
	}
	var remainingGCAsset int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets WHERE id=?`, gcAsset.ID).Scan(&remainingGCAsset); err != nil || remainingGCAsset != 0 {
		t.Fatalf("deleted cover preset asset remained after retention: count=%d err=%v", remainingGCAsset, err)
	}
}

func setVisualString(value string) streamvisual.OptionalString {
	return streamvisual.OptionalString{Set: true, Valid: true, Value: value}
}

type phaseBarrierReader struct {
	reader  *bytes.Reader
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *phaseBarrierReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.reached)
		<-r.release
	})
	return r.reader.Read(p)
}

func controlPlatformTestUUID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func controlPlatformPNG(t *testing.T, width, height int, transparent bool) []byte {
	t.Helper()
	imageValue := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if transparent && (x+y)%7 == 0 {
				alpha = 96
			}
			imageValue.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 251), G: uint8(y % 251), B: 180, A: alpha})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, imageValue); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
