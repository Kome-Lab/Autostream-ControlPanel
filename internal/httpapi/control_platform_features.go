package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func WithMediaAssetRepository(repository mediaassets.Repository) ServerOption {
	return func(s *Server) { s.mediaAssets = repository }
}
func WithUserUIPreferenceStore(preferences store.UserUIPreferenceStore) ServerOption {
	return func(s *Server) { s.uiPreferences = preferences }
}
func WithDiscordTargetPresetStore(presets store.DiscordTargetPresetStore) ServerOption {
	return func(s *Server) { s.discordTargetPresets = presets }
}
func WithStreamVisualRepository(repository streamvisual.Repository) ServerOption {
	return func(s *Server) { s.streamVisual = repository }
}
func WithVideoCoverRepository(repository videocover.Repository) ServerOption {
	return func(s *Server) { s.videoCovers = repository }
}
func WithVideoCoverDispatcher(dispatcher servicecall.VideoCoverDispatcher) ServerOption {
	return func(s *Server) { s.videoCoverDispatcher = dispatcher }
}

func decodeSingleJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeOptionalSingleJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeSingleJSONWithRequiredField(r *http.Request, destination any, required string) error {
	var fields map[string]json.RawMessage
	if err := decodeSingleJSON(r, &fields); err != nil {
		return err
	}
	raw, ok := fields[required]
	trimmed := bytes.TrimSpace(raw)
	if !ok || len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("required field is missing")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func (s *Server) getUIPreference(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	preference, err := s.uiPreferences.GetUserUIPreference(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_ui_preference_failed"})
		return
	}
	writeJSON(w, http.StatusOK, store.SafeUserUIPreference(preference))
}
func (s *Server) updateUIPreference(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThemeID          string  `json:"theme_id"`
		ColorMode        string  `json:"color_mode"`
		ExpectedRevision *uint64 `json:"expected_revision"`
	}
	if err := decodeSingleJSONWithRequiredField(r, &body, "expected_revision"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preference, err := s.uiPreferences.UpdateUserUIPreference(r.Context(), current.User.ID, body.ThemeID, body.ColorMode, *body.ExpectedRevision)
	if err != nil {
		status, code := http.StatusInternalServerError, "update_ui_preference_failed"
		switch {
		case errors.Is(err, store.ErrInvalidThemeID):
			status, code = http.StatusBadRequest, "invalid_theme_id"
		case errors.Is(err, store.ErrInvalidColorMode):
			status, code = http.StatusBadRequest, "invalid_color_mode"
		case errors.Is(err, store.ErrRevisionConflict):
			status, code = http.StatusConflict, "revision_conflict"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeAudit(r, store.AuditEvent{Action: "account.ui_preference.update", ResourceType: "user", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"theme_id": preference.ThemeID, "color_mode": preference.ColorMode, "revision": preference.Revision}})
	writeJSON(w, http.StatusOK, preference)
}

type discordPresetRequest struct {
	Name             string `json:"name"`
	GuildID          string `json:"guild_id"`
	TextChannelID    string `json:"text_channel_id"`
	VoiceChannelID   string `json:"voice_channel_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

func (s *Server) listDiscordTargetPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.discordTargetPresets.ListDiscordTargetPresets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_discord_target_presets_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": presets})
}
func (s *Server) getDiscordTargetPreset(w http.ResponseWriter, r *http.Request) {
	preset, err := s.discordTargetPresets.GetDiscordTargetPreset(r.Context(), r.PathValue("id"), false)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_target_preset_failed"})
		return
	}
	writeJSON(w, http.StatusOK, preset)
}
func (s *Server) createDiscordTargetPreset(w http.ResponseWriter, r *http.Request) {
	var body discordPresetRequest
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.discordTargetPresets.CreateDiscordTargetPreset(r.Context(), store.DiscordTargetPreset{Name: body.Name, GuildID: body.GuildID, TextChannelID: body.TextChannelID, VoiceChannelID: body.VoiceChannelID, CreatedByUserID: current.User.ID})
	if err != nil {
		writeDiscordPresetError(w, err)
		return
	}
	s.writeDiscordPresetAudit(r, "discord_target_presets.create", preset, []string{"name", "guild_id", "text_channel_id", "voice_channel_id"})
	writeJSON(w, http.StatusCreated, preset)
}
func (s *Server) updateDiscordTargetPreset(w http.ResponseWriter, r *http.Request) {
	var body discordPresetRequest
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.discordTargetPresets.UpdateDiscordTargetPreset(r.Context(), r.PathValue("id"), store.DiscordTargetPreset{Name: body.Name, GuildID: body.GuildID, TextChannelID: body.TextChannelID, VoiceChannelID: body.VoiceChannelID, UpdatedByUserID: current.User.ID}, body.ExpectedRevision)
	if err != nil {
		writeDiscordPresetError(w, err)
		return
	}
	s.writeDiscordPresetAudit(r, "discord_target_presets.update", preset, []string{"name", "guild_id", "text_channel_id", "voice_channel_id"})
	writeJSON(w, http.StatusOK, preset)
}
func (s *Server) deleteDiscordTargetPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.discordTargetPresets.DeleteDiscordTargetPreset(r.Context(), r.PathValue("id"), current.User.ID, body.ExpectedRevision)
	if err != nil {
		writeDiscordPresetError(w, err)
		return
	}
	s.writeDiscordPresetAudit(r, "discord_target_presets.delete", preset, []string{"deleted_at"})
	writeJSON(w, http.StatusOK, preset)
}
func writeDiscordPresetError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_discord_target_preset"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, store.ErrRevisionConflict):
		status, code = http.StatusConflict, "revision_conflict"
	case errors.Is(err, store.ErrAlreadyExists):
		status, code = http.StatusConflict, "name_conflict"
	}
	writeJSON(w, status, map[string]string{"code": code})
}
func (s *Server) writeDiscordPresetAudit(r *http.Request, action string, preset store.DiscordTargetPreset, changed []string) {
	s.writeAudit(r, store.AuditEvent{Action: action, ResourceType: "discord_target_preset", ResourceID: preset.ID, Result: "success", Metadata: map[string]any{"preset_id": preset.ID, "revision": preset.Revision, "changed_fields": changed}})
}

func (s *Server) createMediaUploadSession(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	var body struct {
		ExpiresInSeconds int64 `json:"expires_in_seconds,omitempty"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	expires := time.Now().UTC().Add(mediaassets.DraftRetention)
	if body.ExpiresInSeconds > 0 {
		expires = time.Now().UTC().Add(time.Duration(body.ExpiresInSeconds) * time.Second)
	}
	current := currentFromContext(r.Context())
	session, err := s.mediaAssets.CreateUploadSession(r.Context(), current.User.ID, expires)
	if err != nil {
		writeMediaAssetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) uploadMediaAsset(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "multipart_required"})
		return
	}
	var sessionID, usage string
	var asset mediaassets.Asset
	uploaded := false
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		switch part.FormName() {
		case "session_id":
			raw, _ := io.ReadAll(io.LimitReader(part, 129))
			sessionID = strings.TrimSpace(string(raw))
		case "usage_type":
			raw, _ := io.ReadAll(io.LimitReader(part, 65))
			usage = strings.TrimSpace(string(raw))
		case "file":
			if uploaded || sessionID == "" || usage == "" {
				_ = part.Close()
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "upload_metadata_must_precede_file"})
				return
			}
			current := currentFromContext(r.Context())
			asset, err = s.mediaAssets.Upload(r.Context(), mediaassets.UploadInput{SessionID: sessionID, UserID: current.User.ID, UsageType: usage, Filename: part.FileName(), ContentType: part.Header.Get("Content-Type"), Body: part})
			uploaded = true
			_ = part.Close()
			if err != nil {
				writeMediaAssetError(w, err)
				return
			}
		default:
			_ = part.Close()
		}
	}
	if !uploaded {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "file_required"})
		return
	}
	s.writeAudit(r, store.AuditEvent{Action: "media_assets.upload", ResourceType: "media_asset", ResourceID: asset.ID, Result: "success", Metadata: map[string]any{"asset_id": asset.ID, "usage_type": asset.UsageType, "media_type": asset.MediaType, "byte_size": asset.ByteSize, "width": asset.Width, "height": asset.Height}})
	writeJSON(w, http.StatusCreated, asset)
}
func (s *Server) getMediaAsset(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	current := currentFromContext(r.Context())
	asset, err := s.mediaAssets.GetAsset(r.Context(), current.User.ID, r.PathValue("id"))
	if err != nil {
		writeMediaAssetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}
func (s *Server) createMediaAssetVariant(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	var body struct {
		Width  int  `json:"width"`
		Height int  `json:"height"`
		Opaque bool `json:"opaque"`
	}
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	variant, err := s.mediaAssets.EnsureVariant(r.Context(), current.User.ID, r.PathValue("id"), body.Width, body.Height, body.Opaque)
	if err != nil {
		writeMediaAssetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, variant)
}
func (s *Server) deleteMediaAsset(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	current := currentFromContext(r.Context())
	if err := s.mediaAssets.SoftDeleteAsset(r.Context(), current.User.ID, r.PathValue("id"), time.Now().UTC()); err != nil {
		writeMediaAssetError(w, err)
		return
	}
	s.writeAudit(r, store.AuditEvent{Action: "media_assets.delete", ResourceType: "media_asset", ResourceID: r.PathValue("id"), Result: "success", Metadata: map[string]any{"asset_id": r.PathValue("id")}})
	w.WriteHeader(http.StatusNoContent)
}

func writeMediaAssetError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_image"
	switch {
	case errors.Is(err, mediaassets.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, mediaassets.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, mediaassets.ErrUploadTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "upload_too_large"
	case errors.Is(err, mediaassets.ErrImageDimensions):
		code = "invalid_image_dimensions"
	case errors.Is(err, mediaassets.ErrContentTypeMismatch):
		code = "content_type_mismatch"
	case errors.Is(err, mediaassets.ErrAnimatedImage):
		code = "animated_image_unsupported"
	case errors.Is(err, mediaassets.ErrUnsupportedImage):
		code = "unsupported_image"
	case errors.Is(err, mediaassets.ErrDraftExpired):
		status, code = http.StatusConflict, "upload_session_expired"
	case errors.Is(err, mediaassets.ErrDraftClaimed):
		status, code = http.StatusConflict, "upload_session_claimed"
	case errors.Is(err, mediaassets.ErrIntegrity):
		status, code = http.StatusConflict, "media_asset_integrity"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) internalMediaAsset(w http.ResponseWriter, r *http.Request) {
	if s.mediaAssets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	if token.ServiceType != "worker" && token.ServiceType != "encoder_recorder" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_forbidden"})
		return
	}
	assignments, err := s.services.ListStreamAssignments(r.Context(), r.PathValue("stream_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assignment_check_failed"})
		return
	}
	assigned := false
	for _, service := range assignments {
		if service.TokenID == token.ID && service.ServiceType == token.ServiceType {
			assigned = true
			break
		}
	}
	if !assigned {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "stream_assignment_forbidden"})
		return
	}
	internal, err := s.mediaAssets.OpenInternalVariant(r.Context(), r.PathValue("stream_id"), r.PathValue("variant_id"))
	if err != nil {
		writeMediaAssetError(w, err)
		return
	}
	defer internal.Reader.Close()
	digestBytes, err := hex.DecodeString(internal.Variant.SHA256)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "media_asset_integrity"})
		return
	}
	w.Header().Set("Content-Type", internal.Variant.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(internal.Variant.ByteSize, 10))
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digestBytes))
	w.Header().Set("X-AutoStream-Asset-ID", internal.Asset.ID)
	w.Header().Set("X-AutoStream-Variant-ID", internal.Variant.ID)
	w.Header().Set("X-AutoStream-Width", strconv.Itoa(internal.Variant.Width))
	w.Header().Set("X-AutoStream-Height", strconv.Itoa(internal.Variant.Height))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, internal.Reader)
}

func (s *Server) RunMediaAssetGarbageCollector(ctx context.Context, interval time.Duration) {
	if s.mediaAssets == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			removed, err := s.mediaAssets.GarbageCollect(ctx, now.UTC(), 100)
			if err != nil {
				s.writeSystemAudit(ctx, store.AuditEvent{Action: "media_assets.gc", ResourceType: "media_asset", Result: "failure", Metadata: map[string]any{"error_code": "media_asset_gc_failed", "limit": 100}})
				continue
			}
			if removed > 0 {
				s.writeSystemAudit(ctx, store.AuditEvent{Action: "media_assets.gc", ResourceType: "media_asset", Result: "success", Metadata: map[string]any{"removed_count": removed, "limit": 100}})
			}
		}
	}
}
