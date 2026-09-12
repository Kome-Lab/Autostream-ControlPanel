package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

const defaultArchiveRetentionDays = 30

const maxArchiveRetentionDays = 3650

func (s *Server) updateStreamSettings(w http.ResponseWriter, r *http.Request) {
	var body streamSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.Name != "" && strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "name_required"})
		return
	}
	settings, code := streamSettingsFromRequest(body)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if err := s.validateStreamSettingsReferences(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	current := currentFromContext(r.Context())
	if code, status := s.validateStreamServiceAssignmentRequest(r.Context(), body, current.Permissions); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	streamID := r.PathValue("id")
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()
	existing, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if isActiveStreamStatus(existing.Status) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_status_not_editable"})
		return
	}
	if err := s.validateStreamAutoStartTarget(r.Context(), streamID, settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	preserveOmittedLegacyArchiveSettings(&settings, existing, body)
	if strings.TrimSpace(existing.YouTubeOutputID) != strings.TrimSpace(settings.YouTubeOutputID) {
		if claimed, err := s.youtubeRelayBindingStreamClaimActive(r.Context(), existing.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "youtube_relay_binding_claim_check_failed"})
			return
		} else if claimed {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
			return
		}
	}
	assignments, code, status := s.applyStreamServiceAssignments(r, streamID, body, current)
	if code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if streamArchiveDirectRequested(body) {
		settings, err = s.materializeStreamArchiveSettings(r.Context(), existing, settings, body)
		if err != nil {
			if rollbackErr := assignments.Rollback(r.Context(), s.services, current.User.ID); rollbackErr != nil {
				log.Printf("stream settings assignment rollback failed after archive settings error: stream_id=%s error=%v", streamID, rollbackErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_service_rollback_failed"})
				return
			}
			writeJSON(w, streamArchiveSettingsStatus(err), map[string]string{"code": streamArchiveSettingsCode(err)})
			return
		}
	}
	stream, err := s.streams.UpdateStreamSettings(r.Context(), streamID, settings)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrYouTubeRelayBindingClaimActive) {
		if rollbackErr := assignments.Rollback(r.Context(), s.services, current.User.ID); rollbackErr != nil {
			log.Printf("stream settings assignment rollback failed after relay binding claim error: stream_id=%s error=%v", streamID, rollbackErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_service_rollback_failed"})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
		return
	}
	if err != nil {
		if rollbackErr := assignments.Rollback(r.Context(), s.services, current.User.ID); rollbackErr != nil {
			log.Printf("stream settings assignment rollback failed after settings update error: stream_id=%s error=%v", streamID, rollbackErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_service_rollback_failed"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_settings_failed"})
		return
	}
	assignments.WriteAudit(r, s, current)
	stream, err = s.streamWithAssignedNodes(r.Context(), stream)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_settings", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: streamSettingsAuditMetadata(stream)})
	writeJSON(w, http.StatusOK, stream)
}

func streamSettingsFromRequest(body streamSettingsRequest) (store.StreamSettings, string) {
	if math.IsNaN(body.EncoderAudioGainDB) || math.IsInf(body.EncoderAudioGainDB, 0) || body.EncoderAudioGainDB < -60 || body.EncoderAudioGainDB > 24 {
		return store.StreamSettings{}, "invalid_encoder_audio_gain_db"
	}
	scheduledStart, code := parseStreamScheduleTime(body.ScheduledStartAt)
	if code != "" {
		return store.StreamSettings{}, code
	}
	scheduledEnd, code := parseStreamScheduleTime(body.ScheduledEndAt)
	if code != "" {
		return store.StreamSettings{}, code
	}
	if scheduledStart != nil && scheduledEnd != nil && !scheduledEnd.After(*scheduledStart) {
		return store.StreamSettings{}, "schedule_end_before_start"
	}
	return store.StreamSettings{
		Name:                  strings.TrimSpace(body.Name),
		ScheduledStartAt:      scheduledStart,
		ScheduledEndAt:        scheduledEnd,
		DiscordConfigID:       strings.TrimSpace(body.DiscordConfigID),
		AutoStartTrigger:      normalizeAutoStartTrigger(body.AutoStartTrigger),
		EncoderProfileID:      strings.TrimSpace(body.EncoderProfileID),
		CaptionProfileID:      strings.TrimSpace(body.CaptionProfileID),
		OverlayProfileID:      strings.TrimSpace(body.OverlayProfileID),
		EncoderAudioGainDB:    body.EncoderAudioGainDB,
		ArchiveProfileID:      strings.TrimSpace(body.ArchiveProfileID),
		ArchiveOAuthAccountID: archiveRequestString(body.ArchiveOAuthAccountID),
		ArchiveSharedDrive:    archiveRequestBool(body.ArchiveSharedDrive),
		ArchiveSharedDriveID:  archiveRequestString(body.ArchiveSharedDriveID),
		ArchiveFileName:       archiveSafeFileName(archiveRequestString(body.ArchiveFileName)),
		YouTubeOutputID:       strings.TrimSpace(body.YouTubeOutputID),
		EncoderInputURL:       strings.TrimSpace(body.EncoderInputURL),
	}, ""
}

type streamRuntimeSettingsRequest struct {
	EncoderAudioGainDB float64 `json:"encoder_audio_gain_db"`
	OverlayProfileID   string  `json:"overlay_profile_id"`
}

func (s *Server) updateStreamRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	var body streamRuntimeSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if math.IsNaN(body.EncoderAudioGainDB) || math.IsInf(body.EncoderAudioGainDB, 0) || body.EncoderAudioGainDB < -60 || body.EncoderAudioGainDB > 24 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_encoder_audio_gain_db"})
		return
	}
	body.OverlayProfileID = strings.TrimSpace(body.OverlayProfileID)
	if err := s.validateStreamSettingsReferences(r.Context(), store.StreamSettings{OverlayProfileID: body.OverlayProfileID, EncoderAudioGainDB: body.EncoderAudioGainDB}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	streamID := r.PathValue("id")
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()
	existing, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	status := strings.ToLower(strings.TrimSpace(existing.Status))
	if status == "starting" || status == "stopping" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_runtime_settings_transition_in_progress"})
		return
	}
	updated, err := s.streams.UpdateStreamEncoderRuntimeSettings(r.Context(), streamID, body.EncoderAudioGainDB, body.OverlayProfileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_runtime_settings_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"encoder_audio_gain_db": body.EncoderAudioGainDB, "overlay_profile_id": body.OverlayProfileID, "applied_live": false}
	if status == "live" {
		dispatcher, ok := s.dispatcher.(encoderRuntimeSettingsDispatcher)
		if !ok {
			metadata["reason"] = "encoder_runtime_settings_dispatch_not_supported"
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_runtime_settings", ResourceType: "stream", ResourceID: streamID, Result: "failure", Metadata: metadata})
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "encoder_runtime_settings_dispatch_not_supported"})
			return
		}
		assignments, err := s.streamAssignments(r.Context(), streamID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		result := dispatcher.UpdateEncoderRuntimeSettings(r.Context(), updated, primaryStreamAssignments(assignments), body.EncoderAudioGainDB, body.OverlayProfileID)
		metadata["dispatch"] = sanitizeDispatchResults([]servicecall.DispatchResult{result})
		if !result.Success {
			metadata["reason"] = result.Code
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_runtime_settings", ResourceType: "stream", ResourceID: streamID, Result: "failure", Metadata: metadata})
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "encoder_runtime_settings_apply_failed"})
			return
		}
		metadata["applied_live"] = true
	}
	updated, err = s.streamWithAssignedNodes(r.Context(), updated)
	if err != nil {
		metadata["reason"] = "stream_assignment_resolution_failed"
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_runtime_settings", ResourceType: "stream", ResourceID: streamID, Result: "failure", Metadata: metadata})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_assignment_resolution_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_runtime_settings", ResourceType: "stream", ResourceID: streamID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, updated)
}

func parseStreamScheduleTime(value string) (*time.Time, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, "schedule_time_invalid"
	}
	utc := parsed.UTC()
	return &utc, ""
}

func normalizeAutoStartTrigger(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func streamArchiveDirectRequested(body streamSettingsRequest) bool {
	return archiveRequestString(body.ArchiveOAuthAccountID) != "" ||
		archiveRequestString(body.ArchiveFolderID) != "" ||
		archiveRequestBool(body.ArchiveSharedDrive) ||
		archiveRequestString(body.ArchiveSharedDriveID) != "" ||
		archiveRequestString(body.ArchiveFileName) != "" ||
		archiveRequestInt(body.ArchiveRetentionDays) > 0
}

func preserveOmittedLegacyArchiveSettings(settings *store.StreamSettings, existing store.Stream, body streamSettingsRequest) {
	if settings == nil {
		return
	}
	// A stream settings form intentionally selects a managed archive profile and
	// does not expose direct OAuth/folder settings. Preserve those legacy fields
	// when every direct setting is absent rather than treating absence as a
	// clear operation. Supplying any direct setting is an explicit request to
	// manage (including clear) that legacy configuration as a unit.
	if streamArchiveDirectFieldsPresent(body) {
		return
	}
	settings.ArchiveDriveDestinationID = existing.ArchiveDriveDestinationID
	settings.ArchiveOAuthAccountID = existing.ArchiveOAuthAccountID
	settings.ArchiveSharedDrive = existing.ArchiveSharedDrive
	settings.ArchiveSharedDriveID = existing.ArchiveSharedDriveID
	settings.ArchiveFileName = existing.ArchiveFileName
}

func streamArchiveDirectFieldsPresent(body streamSettingsRequest) bool {
	return body.ArchiveOAuthAccountID != nil ||
		body.ArchiveFolderID != nil ||
		body.ArchiveSharedDrive != nil ||
		body.ArchiveSharedDriveID != nil ||
		body.ArchiveFileName != nil ||
		body.ArchiveRetentionDays != nil
}

func (s *Server) materializeStreamArchiveSettings(ctx context.Context, stream store.Stream, settings store.StreamSettings, body streamSettingsRequest) (store.StreamSettings, error) {
	if s.profiles == nil {
		return settings, errArchiveSettingsStoreUnavailable
	}
	oauthAccountID := archiveRequestString(body.ArchiveOAuthAccountID)
	folderID := archiveRequestString(body.ArchiveFolderID)
	destinationID := strings.TrimSpace(stream.ArchiveDriveDestinationID)
	sharedDrive := archiveRequestBool(body.ArchiveSharedDrive)
	sharedDriveID := archiveRequestString(body.ArchiveSharedDriveID)
	retentionDays := normalizeArchiveRetentionDays(archiveRequestInt(body.ArchiveRetentionDays))
	driveRequested := oauthAccountID != "" || folderID != "" || sharedDrive || sharedDriveID != "" || archiveRequestString(body.ArchiveFileName) != ""
	fileName := archiveSafeFileName(archiveRequestString(body.ArchiveFileName))
	if driveRequested {
		if s.integrations == nil {
			return settings, errArchiveSettingsStoreUnavailable
		}
		if oauthAccountID == "" {
			return settings, errArchiveOAuthAccountRequired
		}
		if destinationID == "" && folderID == "" {
			return settings, errArchiveFolderIDRequired
		}
		if sharedDrive && sharedDriveID == "" {
			return settings, errArchiveSharedDriveIDRequired
		}
		if err := s.validateDriveOAuthReadiness(ctx, store.DriveDestination{AuthMode: "oauth2", OAuthAccountID: oauthAccountID}); err != nil {
			return settings, err
		}
		destination, err := s.upsertStreamArchiveDriveDestination(ctx, stream, destinationID, oauthAccountID, folderID, sharedDrive)
		if err != nil {
			return settings, err
		}
		if fileName == "" {
			fileName = defaultArchiveFileName(stream.Name, time.Now())
		}
		destinationID = destination.ID
		settings.ArchiveDriveDestinationID = destination.ID
		settings.ArchiveOAuthAccountID = oauthAccountID
		settings.ArchiveSharedDrive = sharedDrive
		settings.ArchiveSharedDriveID = sharedDriveID
		settings.ArchiveFileName = fileName
	} else {
		destinationID = ""
		fileName = ""
	}
	profileID, err := s.upsertStreamArchiveProfile(ctx, stream, destinationID, fileName, sharedDrive && driveRequested, sharedDriveID, retentionDays)
	if err != nil {
		return settings, err
	}
	settings.ArchiveProfileID = profileID
	return settings, nil
}

func (s *Server) upsertStreamArchiveDriveDestination(ctx context.Context, stream store.Stream, destinationID, oauthAccountID, folderID string, sharedDrive bool) (store.DriveDestination, error) {
	destination := store.DriveDestination{
		ID:             strings.TrimSpace(destinationID),
		Name:           streamArchiveDestinationName(stream),
		AuthMode:       "oauth2",
		OAuthAccountID: oauthAccountID,
		FolderID:       folderID,
		SharedDrive:    sharedDrive,
	}
	if destination.ID != "" {
		updated, err := s.integrations.UpdateDriveDestination(ctx, destination)
		if errors.Is(err, store.ErrNotFound) {
			destination.ID = ""
		} else {
			return updated, err
		}
	}
	return s.integrations.CreateDriveDestination(ctx, destination)
}

func (s *Server) upsertStreamArchiveProfile(ctx context.Context, stream store.Stream, destinationID, fileName string, sharedDrive bool, sharedDriveID string, retentionDays int) (string, error) {
	config := map[string]any{
		"stream_archive_direct": true,
		"retention_days":        normalizeArchiveRetentionDays(retentionDays),
	}
	if strings.TrimSpace(destinationID) != "" {
		config["drive_destination_id"] = strings.TrimSpace(destinationID)
	}
	if safeFileName := archiveSafeFileName(fileName); safeFileName != "" {
		config["archive_file_name"] = safeFileName
	}
	if sharedDrive {
		config["shared_drive"] = true
	}
	if sharedDriveID != "" {
		config["shared_drive_id"] = sharedDriveID
	}
	name := streamArchiveProfileName(stream)
	if profileID, ok := s.directArchiveProfileID(ctx, stream); ok {
		profile, err := s.profiles.UpdateProfile(ctx, store.ProfileArchive, profileID, name, config)
		if err != nil {
			return "", err
		}
		return profile.ID, nil
	}
	profile, err := s.profiles.CreateProfile(ctx, store.ProfileArchive, name, config)
	if err != nil {
		fallbackName := name + "-" + strconv.FormatInt(time.Now().Unix(), 10)
		profile, err = s.profiles.CreateProfile(ctx, store.ProfileArchive, fallbackName, config)
	}
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

func (s *Server) directArchiveProfileID(ctx context.Context, stream store.Stream) (string, bool) {
	profileID := strings.TrimSpace(stream.ArchiveProfileID)
	if profileID == "" {
		return "", false
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if err != nil {
		return "", false
	}
	return profile.ID, configBool(profile.Config, "stream_archive_direct")
}

func streamArchiveDestinationName(stream store.Stream) string {
	return strings.TrimSpace(stream.Name) + " archive drive " + shortID(stream.ID)
}

func streamArchiveProfileName(stream store.Stream) string {
	return strings.TrimSpace(stream.Name) + " archive " + shortID(stream.ID)
}

func normalizeArchiveRetentionDays(value int) int {
	if value <= 0 {
		return defaultArchiveRetentionDays
	}
	if value > maxArchiveRetentionDays {
		return maxArchiveRetentionDays
	}
	return value
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "stream"
	}
	return id
}

func defaultArchiveFileName(streamName string, now time.Time) string {
	base := archiveSafeFileName(streamName)
	if base == "" {
		base = "archive"
	}
	if strings.HasSuffix(strings.ToLower(base), ".mp4") {
		base = base[:len(base)-4]
	}
	return archiveSafeFileName(base + "-" + now.Format("20060102") + ".mp4")
}

func archiveSafeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, value)
	value = strings.Trim(value, " .")
	if value == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(value), ".mp4") {
		value += ".mp4"
	}
	return value
}

func streamArchiveSettingsCode(err error) string {
	switch {
	case errors.Is(err, errArchiveOAuthAccountRequired):
		return "archive_oauth_account_required"
	case errors.Is(err, errArchiveFolderIDRequired):
		return "archive_folder_id_required"
	case errors.Is(err, errArchiveSharedDriveIDRequired):
		return "archive_shared_drive_id_required"
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "drive_oauth_account_unavailable"
	case errors.Is(err, store.ErrSecretKeyRequired):
		return "secret_encryption_key_required"
	case errors.Is(err, errArchiveSettingsStoreUnavailable):
		return "archive_settings_store_unavailable"
	default:
		return "archive_settings_failed"
	}
}

func streamArchiveSettingsStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrSecretKeyRequired), errors.Is(err, errArchiveSettingsStoreUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func streamSettingsAuditMetadata(stream store.Stream) map[string]any {
	return map[string]any{
		"name":                               stream.Name,
		"scheduled_start_at":                 stream.ScheduledStartAt,
		"scheduled_end_at":                   stream.ScheduledEndAt,
		"discord_config_id":                  stream.DiscordConfigID,
		"auto_start_trigger":                 stream.AutoStartTrigger,
		"youtube_output_id":                  stream.YouTubeOutputID,
		"encoder_audio_gain_db":              stream.EncoderAudioGainDB,
		"overlay_profile_id":                 stream.OverlayProfileID,
		"archive_profile_id":                 stream.ArchiveProfileID,
		"archive_drive_destination_id":       stream.ArchiveDriveDestinationID,
		"archive_oauth_account_id":           stream.ArchiveOAuthAccountID,
		"archive_folder_id_configured":       stream.ArchiveFolderIDConfigured,
		"archive_shared_drive":               stream.ArchiveSharedDrive,
		"archive_shared_drive_id_configured": stream.ArchiveSharedDriveID != "",
		"archive_file_name":                  stream.ArchiveFileName,
	}
}

func (s *Server) validateStreamSettingsReferences(ctx context.Context, settings store.StreamSettings) error {
	discordConfigID := strings.TrimSpace(settings.DiscordConfigID)
	switch strings.TrimSpace(settings.AutoStartTrigger) {
	case "":
	case autoStartTriggerDiscordVoiceJoin:
		if discordConfigID == "" {
			return errAutoStartDiscordRequired
		}
	default:
		return errAutoStartTriggerInvalid
	}
	if discordConfigID != "" {
		if _, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, settings.DiscordConfigID); errors.Is(err, store.ErrNotFound) {
			return errDiscordConfigNotFound
		} else if err != nil {
			return err
		}
	}
	if err := s.validateProfileReference(ctx, settings.YouTubeOutputID, store.ProfileYouTubeOutput, errYouTubeOutputNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.EncoderProfileID, store.ProfileEncoder, errEncoderProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.CaptionProfileID, store.ProfileCaption, errCaptionProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.OverlayProfileID, store.ProfileOverlay, errOverlayProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.ArchiveProfileID, store.ProfileArchive, errArchiveProfileNotFound); err != nil {
		return err
	}
	if strings.TrimSpace(settings.ArchiveOAuthAccountID) != "" {
		account, err := s.integrations.GetOAuthAccount(ctx, settings.ArchiveOAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return errDriveOAuthAccountUnavailable
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(account.ProviderType, "google") || !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
			return errDriveOAuthAccountUnavailable
		}
	}
	if err := validateEncoderInputURL(settings.EncoderInputURL); err != nil {
		return err
	}
	return nil
}

func (s *Server) validateStreamAutoStartTarget(ctx context.Context, streamID string, settings store.StreamSettings) error {
	if strings.TrimSpace(settings.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		return nil
	}
	if s.streamVisual == nil {
		return errAutoStartDiscordRequired
	}
	visual, err := s.streamVisual.Get(ctx, strings.TrimSpace(streamID))
	if err != nil {
		return err
	}
	if strings.TrimSpace(visual.DiscordGuildID) == "" || strings.TrimSpace(visual.DiscordVoiceChannelID) == "" {
		return errAutoStartDiscordRequired
	}
	return nil
}

func (s *Server) validateProfileReference(ctx context.Context, id string, kind store.ProfileKind, notFound error) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if _, err := s.profiles.GetProfile(ctx, kind, id); errors.Is(err, store.ErrNotFound) {
		return notFound
	} else if err != nil {
		return err
	}
	return nil
}
