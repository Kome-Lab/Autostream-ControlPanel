package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerResourceRoutes() {
	s.registerProfileRoutes("/profiles/encoder", store.ProfileEncoder, "encoder_profiles")
	s.registerProfileRoutes("/profiles/archive", store.ProfileArchive, "archive_profiles")
	s.registerProfileRoutes("/profiles/caption", store.ProfileCaption, "caption_profiles")
	s.registerProfileRoutes("/profiles/overlay", store.ProfileOverlay, "overlay_profiles")
	s.mux.HandleFunc("GET /discord/configs", s.requirePermission("discord_configs.read", s.listDiscordConfigs))
	s.mux.HandleFunc("POST /discord/configs", s.requirePermission("discord_configs.create", s.createDiscordConfig))
	s.mux.HandleFunc("GET /discord/configs/{id}", s.requirePermission("discord_configs.read", s.getDiscordConfig))
	s.mux.HandleFunc("PUT /discord/configs/{id}", s.requirePermission("discord_configs.update", s.updateDiscordConfig))
	s.mux.HandleFunc("DELETE /discord/configs/{id}", s.requirePermission("discord_configs.delete", s.deleteDiscordConfig))
	s.mux.HandleFunc("GET /discord/target-presets", s.requirePermission("discord_target_presets.read", s.listDiscordTargetPresets))
	s.mux.HandleFunc("POST /discord/target-presets", s.requirePermission("discord_target_presets.create", s.createDiscordTargetPreset))
	s.mux.HandleFunc("GET /discord/target-presets/{id}", s.requirePermission("discord_target_presets.read", s.getDiscordTargetPreset))
	s.mux.HandleFunc("PUT /discord/target-presets/{id}", s.requirePermission("discord_target_presets.update", s.updateDiscordTargetPreset))
	s.mux.HandleFunc("DELETE /discord/target-presets/{id}", s.requirePermission("discord_target_presets.delete", s.deleteDiscordTargetPreset))
	s.mux.HandleFunc("GET /youtube/outputs", s.requirePermission("youtube_outputs.read", s.listYouTubeOutputs))
	s.mux.HandleFunc("POST /youtube/outputs", s.requirePermission("youtube_outputs.create", s.createYouTubeOutput))
	s.mux.HandleFunc("GET /youtube/outputs/{id}", s.requirePermission("youtube_outputs.read", s.getYouTubeOutput))
	s.mux.HandleFunc("PUT /youtube/outputs/{id}", s.requirePermission("youtube_outputs.update", s.updateYouTubeOutput))
	s.mux.HandleFunc("DELETE /youtube/outputs/{id}", s.requirePermission("youtube_outputs.delete", s.deleteYouTubeOutput))
}

func (s *Server) registerProfileRoutes(base string, kind store.ProfileKind, permissionPrefix string) {
	s.mux.HandleFunc("GET "+base, s.requirePermission(permissionPrefix+".read", s.listProfiles(kind)))
	s.mux.HandleFunc("POST "+base, s.requirePermission(permissionPrefix+".create", s.createProfile(kind, permissionPrefix+".create")))
	s.mux.HandleFunc("GET "+base+"/{id}", s.requirePermission(permissionPrefix+".read", s.getProfile(kind)))
	s.mux.HandleFunc("PUT "+base+"/{id}", s.requirePermission(permissionPrefix+".update", s.updateProfile(kind, permissionPrefix+".update")))
	s.mux.HandleFunc("DELETE "+base+"/{id}", s.requirePermission(permissionPrefix+".delete", s.deleteProfile(kind, permissionPrefix+".delete")))
}

func (s *Server) listProfiles(kind store.ProfileKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.profiles.ListProfiles(r.Context(), kind)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_profiles_failed"})
			return
		}
		if kind == store.ProfileCaption {
			for i := range items {
				items[i].Config = normalizeProfileConfig(kind, items[i].Config)
			}
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func (s *Server) createProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		body.Config = normalizeProfileConfig(kind, body.Config)
		if !s.validateProfileSecretReferences(w, kind, body.Config) {
			return
		}
		profile, err := s.profiles.CreateProfile(r.Context(), kind, body.Name, body.Config)
		if errors.Is(err, store.ErrProfileRawSecretConfig) {
			writeProfileSecretReferenceRequired(w, kind)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"name": profile.Name}})
		writeJSON(w, http.StatusCreated, profile)
	}
}

func (s *Server) getProfile(kind store.ProfileKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := s.profiles.GetProfile(r.Context(), kind, r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_profile_failed"})
			return
		}
		if kind == store.ProfileCaption {
			profile.Config = normalizeProfileConfig(kind, profile.Config)
		}
		writeJSON(w, http.StatusOK, profile)
	}
}

func (s *Server) updateProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		body.Config = normalizeProfileConfig(kind, body.Config)
		if !s.validateProfileSecretReferences(w, kind, body.Config) {
			return
		}
		profile, err := s.profiles.UpdateProfile(r.Context(), kind, r.PathValue("id"), body.Name, body.Config)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if errors.Is(err, store.ErrProfileRawSecretConfig) {
			writeProfileSecretReferenceRequired(w, kind)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		metadata := map[string]any{"name": profile.Name}
		if kind == store.ProfileCaption {
			results, affected, reason, statusCode := s.applyCaptionProfileToLiveStreams(r.Context(), profile.ID)
			metadata["affected_live_streams"] = affected
			metadata["applied_live"] = affected > 0 && reason == ""
			if len(results) > 0 {
				metadata["dispatch"] = sanitizeDispatchResults(results)
			}
			if reason != "" {
				metadata["profile_saved"] = true
				metadata["reason"] = reason
				s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: profile.ID, Result: "failure", Metadata: metadata})
				writeJSON(w, statusCode, map[string]string{"code": "caption_profile_saved_runtime_apply_failed", "reason": reason})
				return
			}
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: profile.ID, Result: "success", Metadata: metadata})
		writeJSON(w, http.StatusOK, profile)
	}
}

func (s *Server) applyCaptionProfileToLiveStreams(ctx context.Context, profileID string) ([]servicecall.DispatchResult, int, string, int) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || s.streams == nil {
		return nil, 0, "", http.StatusOK
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, 0, "list_streams_failed", http.StatusInternalServerError
	}
	candidates := make([]store.Stream, 0)
	for _, stream := range streams {
		status := strings.ToLower(strings.TrimSpace(stream.Status))
		if stream.CaptionProfileID == profileID && (status == "live" || status == "starting") {
			candidates = append(candidates, stream)
		}
	}
	if len(candidates) == 0 {
		return nil, 0, "", http.StatusOK
	}
	dispatcher, ok := s.dispatcher.(workerCaptionRuntimeSettingsDispatcher)
	if !ok {
		return nil, len(candidates), "worker_caption_runtime_settings_dispatch_not_supported", http.StatusBadGateway
	}

	results := make([]servicecall.DispatchResult, 0, len(candidates))
	affected := 0
	failureReason := ""
	failureStatus := http.StatusBadGateway
	for _, candidate := range candidates {
		unlockLifecycle := s.lockStreamLifecycle(candidate.ID)
		stream, err := s.streams.GetStream(ctx, candidate.ID)
		if err != nil {
			unlockLifecycle()
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if failureReason == "" {
				failureReason = "get_stream_failed"
				failureStatus = http.StatusInternalServerError
			}
			continue
		}
		if strings.ToLower(strings.TrimSpace(stream.Status)) != "live" || stream.CaptionProfileID != profileID {
			unlockLifecycle()
			continue
		}
		assignments, err := s.streamAssignments(ctx, stream.ID)
		if err != nil {
			unlockLifecycle()
			if failureReason == "" {
				failureReason = "list_stream_assignments_failed"
				failureStatus = http.StatusInternalServerError
			}
			continue
		}
		result := dispatcher.UpdateWorkerCaptionRuntimeSettings(ctx, stream, primaryStreamAssignments(assignments), profileID)
		unlockLifecycle()
		affected++
		results = append(results, result)
		if !result.Success && failureReason == "" {
			failureReason = strings.TrimSpace(result.Code)
			if failureReason == "" {
				failureReason = "worker_caption_runtime_settings_apply_failed"
			}
		}
	}
	return results, affected, failureReason, failureStatus
}

func (s *Server) deleteProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		profile, err := s.profiles.GetProfile(r.Context(), kind, id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_profile_failed"})
			return
		}
		if s.writeProfileDeleteBlockedIfInUse(w, r, kind, id) {
			return
		}
		if err := s.profiles.DeleteProfile(r.Context(), kind, id); errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		} else if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: id, Result: "success", Metadata: map[string]any{"name": profile.Name}})
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (s *Server) writeProfileDeleteBlockedIfInUse(w http.ResponseWriter, r *http.Request, kind store.ProfileKind, id string) bool {
	stream, inUse, err := s.streamProfileReferenceInUse(r.Context(), kind, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "profile_reference_check_failed"})
		return true
	}
	if !inUse {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{
		"code":      "profile_in_use",
		"stream_id": stream.ID,
	})
	return true
}

func (s *Server) streamProfileReferenceInUse(ctx context.Context, kind store.ProfileKind, id string) (store.Stream, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || s.streams == nil {
		return store.Stream{}, false, nil
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return store.Stream{}, false, err
	}
	for _, stream := range streams {
		if streamReferencesProfile(stream, kind, id) {
			return stream, true, nil
		}
	}
	return store.Stream{}, false, nil
}

func streamReferencesProfile(stream store.Stream, kind store.ProfileKind, id string) bool {
	switch kind {
	case store.ProfileEncoder:
		return stream.EncoderProfileID == id
	case store.ProfileArchive:
		return stream.ArchiveProfileID == id
	case store.ProfileCaption:
		return stream.CaptionProfileID == id
	case store.ProfileOverlay:
		return stream.OverlayProfileID == id
	case store.ProfileDiscordConfig:
		return stream.DiscordConfigID == id
	case store.ProfileYouTubeOutput:
		return stream.YouTubeOutputID == id
	default:
		return false
	}
}

func normalizeProfileConfig(kind store.ProfileKind, config map[string]any) map[string]any {
	if kind == store.ProfileCaption {
		endpointingMS := configInt(config, "endpointing_ms")
		if endpointingMS < 10 || endpointingMS > 5000 {
			endpointingMS = 300
		}
		captionInt := func(key string, fallback, min, max int) int {
			value := configInt(config, key)
			if value < min || value > max {
				return fallback
			}
			return value
		}
		delayMS := configInt(config, "delay_ms")
		if _, ok := config["delay_ms"]; !ok {
			delayMS = 800
		} else if delayMS < 0 || delayMS > 10000 {
			delayMS = 800
		}
		keyterms := normalizedDeepgramKeyterms(config["keyterms"])
		return map[string]any{
			"provider":                        "deepgram",
			"model":                           "nova-3",
			"language":                        normalizeDeepgramLanguage(configString(config, "language")),
			"api_key_secret_name":             "deepgram_api_key",
			"endpointing_ms":                  endpointingMS,
			"utterance_end_ms":                captionInt("utterance_end_ms", 1000, 100, 10000),
			"local_finalize_ms":               captionInt("local_finalize_ms", 1500, 0, 10000),
			"speaker_idle_close_seconds":      captionInt("speaker_idle_close_seconds", 8, 1, 120),
			"keepalive_interval_seconds":      captionInt("keepalive_interval_seconds", 4, 1, 60),
			"interim_results":                 configBoolDefault(config, "interim_results", true),
			"smart_format":                    configBoolDefault(config, "smart_format", true),
			"keyterms":                        keyterms,
			"mip_opt_out":                     configBoolDefault(config, "mip_opt_out", false),
			"replay_buffer_max_ms":            captionInt("replay_buffer_max_ms", 2000, 0, 10000),
			"delay_ms":                        delayMS,
			"caption_audio_flush_ms":          captionInt("caption_audio_flush_ms", 100, 10, 1000),
			"caption_audio_max_batch_packets": captionInt("caption_audio_max_batch_packets", 5, 1, 100),
			"unresolved_ssrc_buffer_ms":       captionInt("unresolved_ssrc_buffer_ms", 1000, 0, 5000),
			"conversation_max_items":          captionInt("conversation_max_items", 12, 1, 64),
			"conversation_reorder_window_ms":  captionInt("conversation_reorder_window_ms", 500, 0, 5000),
			"voice_interim_ttl_seconds":       captionInt("voice_interim_ttl_seconds", 6, 1, 60),
			"voice_final_ttl_seconds":         captionInt("voice_final_ttl_seconds", 15, 1, 300),
			"show_voice_transcripts":          configBoolDefault(config, "show_voice_transcripts", true),
			"show_legacy_caption_bar":         configBoolDefault(config, "show_legacy_caption_bar", false),
		}
	}
	if kind != store.ProfileOverlay || config == nil {
		return config
	}
	out := make(map[string]any, len(config)+3)
	for key, value := range config {
		out[key] = value
	}
	delete(out, "watermark_position")
	delete(out, "watermark_opacity")
	delete(out, "watermark_width_percent")
	out["watermark_canvas_width"] = 1920
	out["watermark_canvas_height"] = 1080
	out["watermark_fit_mode"] = "scale_to_output"
	return out
}

func normalizedDeepgramKeyterms(value any) []string {
	terms := make([]string, 0, 20)
	appendTerm := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || len([]rune(raw)) > 128 || len(terms) >= 20 {
			return
		}
		for _, existing := range terms {
			if existing == raw {
				return
			}
		}
		terms = append(terms, raw)
	}
	switch typed := value.(type) {
	case []string:
		for _, term := range typed {
			appendTerm(term)
		}
	case []any:
		for _, item := range typed {
			if term, ok := item.(string); ok {
				appendTerm(term)
			}
		}
	}
	return terms
}

func normalizeDeepgramLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en", "en-us", "en-gb":
		return "en"
	default:
		return "ja"
	}
}

func (s *Server) validateProfileSecretReferences(w http.ResponseWriter, kind store.ProfileKind, config map[string]any) bool {
	invalid := invalidProfileSecretReferences(kind, config)
	if len(invalid) == 0 {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"code":                      "profile_secret_reference_not_allowed",
		"message":                   "Profile config contains a secret reference that is not allowed for this profile kind.",
		"invalid_secret_references": invalid,
		"allowed_secret_references": allowedProfileSecretReferences(kind),
	})
	return false
}

func writeProfileSecretReferenceRequired(w http.ResponseWriter, kind store.ProfileKind) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"code":                      "profile_secret_reference_required",
		"message":                   "Profile config must use *_secret_name references; raw secret-like keys and values are not accepted.",
		"allowed_secret_references": allowedProfileSecretReferences(kind),
	})
}

func invalidProfileSecretReferences(kind store.ProfileKind, config map[string]any) []string {
	seen := map[string]bool{}
	for _, name := range profileSecretReferences(config) {
		if !runtimeProfileKindAllowsSecret(kind, name) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func profileSecretReferences(value any) []string {
	var refs []string
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if profileSecretReferenceKey(normalizedKey) {
				if name, ok := nested.(string); ok && strings.TrimSpace(name) != "" {
					refs = append(refs, strings.TrimSpace(name))
				}
			}
			refs = append(refs, profileSecretReferences(nested)...)
		}
	case []any:
		for _, nested := range typed {
			refs = append(refs, profileSecretReferences(nested)...)
		}
	}
	return refs
}

func profileSecretReferenceKey(key string) bool {
	for _, suffix := range []string{"_secret_name", "_secret_ref", "_secret_id"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	canonical := canonicalProfileSecretKey(key)
	for _, suffix := range []string{"secretname", "secretref", "secretid"} {
		if strings.HasSuffix(canonical, suffix) {
			return true
		}
	}
	return false
}

func canonicalProfileSecretKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func allowedProfileSecretReferences(kind store.ProfileKind) []string {
	switch kind {
	case store.ProfileDiscordConfig:
		return []string{"discord_bot_token", "discord_bot_token_<config_id>"}
	case store.ProfileYouTubeOutput:
		return []string{"youtube_stream_key", "youtube_stream_key_<output_id>"}
	case store.ProfileArchive:
		return []string{"google_drive_folder_id", "google_drive_folder_id_<destination_id>", "google_oauth_refresh_token_<account_id>", "drive_destination:<id>:folder_id", "oauth_provider:<id>:client_secret", "oauth_account:<id>:refresh_token"}
	case store.ProfileEncoder:
		return []string{"encoder_runtime_secret_<name>"}
	case store.ProfileCaption:
		return []string{"deepgram_api_key", "deepgram_api_key_<profile_id>"}
	default:
		return []string{}
	}
}
