package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type discordConfigRequest struct {
	Name                 string `json:"name"`
	ServiceID            string `json:"service_id"`
	GuildID              string `json:"guild_id"`
	VoiceChannelID       string `json:"voice_channel_id"`
	TextChannelID        string `json:"text_channel_id"`
	BotToken             string `json:"bot_token"`
	CaptionEnabled       *bool  `json:"caption_enabled"`
	STTProfileID         string `json:"stt_profile_id"`
	ReconnectEnabled     *bool  `json:"reconnect_enabled"`
	ReconnectMaxAttempts int    `json:"reconnect_max_attempts"`
	ReconnectBaseDelay   string `json:"reconnect_base_delay"`
	ReconnectMaxDelay    string `json:"reconnect_max_delay"`
	AudioForwardEnabled  *bool  `json:"audio_forward_enabled"`
	Config               any    `json:"config"`
}

type discordConfigResponse struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	ServiceID            string    `json:"service_id,omitempty"`
	GuildID              string    `json:"guild_id,omitempty"`
	VoiceChannelID       string    `json:"voice_channel_id,omitempty"`
	TextChannelID        string    `json:"text_channel_id,omitempty"`
	BotTokenConfigured   bool      `json:"bot_token_configured,omitempty"`
	BotTokenFingerprint  string    `json:"bot_token_fingerprint,omitempty"`
	CaptionEnabled       bool      `json:"caption_enabled,omitempty"`
	STTProfileID         string    `json:"stt_profile_id,omitempty"`
	ReconnectEnabled     bool      `json:"reconnect_enabled,omitempty"`
	ReconnectMaxAttempts int       `json:"reconnect_max_attempts,omitempty"`
	ReconnectBaseDelay   string    `json:"reconnect_base_delay,omitempty"`
	ReconnectMaxDelay    string    `json:"reconnect_max_delay,omitempty"`
	AudioForwardEnabled  bool      `json:"audio_forward_enabled,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (s *Server) listDiscordConfigs(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.profiles.ListProfiles(r.Context(), store.ProfileDiscordConfig)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_discord_configs_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	out := make([]discordConfigResponse, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, discordConfigFromProfile(profile, statuses))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createDiscordConfig(w http.ResponseWriter, r *http.Request) {
	var body discordConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	config, err := discordConfigFromRequest(body, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_discord_config"})
		return
	}
	if err := s.validateDiscordConfigService(r.Context(), config); err != nil {
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
		return
	}
	profile, err := s.profiles.CreateProfile(r.Context(), store.ProfileDiscordConfig, body.Name, config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_discord_config_failed"})
		return
	}
	if strings.TrimSpace(body.BotToken) != "" {
		secretName := discordBotTokenSecretName(profile.ID)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.BotToken)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_discord_bot_token_failed"})
			return
		}
		config["bot_token_secret_name"] = secretName
		config["bot_token_fingerprint"] = status.Fingerprint
		profile, err = s.profiles.UpdateProfile(r.Context(), store.ProfileDiscordConfig, profile.ID, profile.Name, config)
		if err != nil {
			_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_discord_config_failed"})
			return
		}
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.create", ResourceType: "discord_config", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"service_id": configString(profile.Config, "service_id"), "bot_token_configured": strings.TrimSpace(body.BotToken) != ""}})
	writeJSON(w, http.StatusCreated, discordConfigFromProfile(profile, secretStatusListForConfig(profile, "bot_token_secret_name", "bot_token_fingerprint")))
}

func (s *Server) getDiscordConfig(w http.ResponseWriter, r *http.Request) {
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, discordConfigFromProfile(profile, statuses))
}

func (s *Server) updateDiscordConfig(w http.ResponseWriter, r *http.Request) {
	var body discordConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	config, err := discordConfigFromRequest(body, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_discord_config"})
		return
	}
	if err := s.validateDiscordConfigService(r.Context(), config); err != nil {
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
		return
	}
	if existingSecret := strings.TrimSpace(configString(existing.Config, "bot_token_secret_name")); existingSecret != "" && strings.TrimSpace(body.BotToken) == "" {
		config["bot_token_secret_name"] = existingSecret
		config["bot_token_fingerprint"] = configString(existing.Config, "bot_token_fingerprint")
	}
	if strings.TrimSpace(body.BotToken) != "" {
		secretName := discordBotTokenSecretName(id)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.BotToken)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_discord_bot_token_failed"})
			return
		}
		config["bot_token_secret_name"] = secretName
		config["bot_token_fingerprint"] = status.Fingerprint
	}
	profile, err := s.profiles.UpdateProfile(r.Context(), store.ProfileDiscordConfig, id, body.Name, config)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_discord_config_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.update", ResourceType: "discord_config", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"service_id": configString(profile.Config, "service_id"), "bot_token_updated": strings.TrimSpace(body.BotToken) != ""}})
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, discordConfigFromProfile(profile, statuses))
}

func (s *Server) deleteDiscordConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	if s.writeProfileDeleteBlockedIfInUse(w, r, store.ProfileDiscordConfig, id) {
		return
	}
	if err := s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_discord_config_failed"})
		return
	}
	if secretName := strings.TrimSpace(configString(profile.Config, "bot_token_secret_name")); secretName != "" {
		_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.delete", ResourceType: "discord_config", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func discordConfigFromRequest(body discordConfigRequest, id string) (map[string]any, error) {
	if body.Config != nil {
		return nil, errors.New("discord config must use structured fields")
	}
	if strings.TrimSpace(body.Name) == "" {
		return nil, errors.New("discord config name is required")
	}
	config := map[string]any{}
	if value := strings.TrimSpace(body.ServiceID); value != "" {
		config["service_id"] = value
	}
	if value := strings.TrimSpace(body.STTProfileID); value != "" {
		config["stt_profile_id"] = value
	}
	if body.CaptionEnabled != nil {
		config["caption_enabled"] = *body.CaptionEnabled
	}
	config["reconnect_enabled"] = true
	if body.ReconnectMaxAttempts < 0 {
		return nil, errors.New("reconnect max attempts must be positive")
	}
	if body.ReconnectMaxAttempts > 0 {
		config["reconnect_max_attempts"] = body.ReconnectMaxAttempts
	}
	if value := strings.TrimSpace(body.ReconnectBaseDelay); value != "" {
		if _, err := time.ParseDuration(value); err != nil {
			return nil, errors.New("reconnect base delay must be a duration")
		}
		config["reconnect_base_delay"] = value
	}
	if value := strings.TrimSpace(body.ReconnectMaxDelay); value != "" {
		if _, err := time.ParseDuration(value); err != nil {
			return nil, errors.New("reconnect max delay must be a duration")
		}
		config["reconnect_max_delay"] = value
	}
	config["audio_forward_enabled"] = true
	if id != "" {
		config["bot_token_secret_name"] = discordBotTokenSecretName(id)
	}
	return config, nil
}

func (s *Server) validateDiscordConfigService(ctx context.Context, config map[string]any) error {
	serviceID := strings.TrimSpace(configString(config, "service_id"))
	if serviceID == "" {
		return nil
	}
	service, err := s.services.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return errDiscordConfigServiceMismatch
	}
	if err != nil {
		return err
	}
	if service.ServiceType != "discord_bot" {
		return errDiscordConfigServiceMismatch
	}
	return nil
}

func discordBotTokenSecretName(id string) string {
	return "discord_bot_token_" + strings.ToLower(strings.TrimSpace(id))
}

func discordConfigFromProfile(profile store.Profile, statuses []store.SecretStatus) discordConfigResponse {
	secretName := strings.TrimSpace(configString(profile.Config, "bot_token_secret_name"))
	status := secretStatusByName(statuses, secretName)
	return discordConfigResponse{
		ID:                   profile.ID,
		Name:                 profile.Name,
		ServiceID:            configString(profile.Config, "service_id"),
		GuildID:              configString(profile.Config, "guild_id"),
		VoiceChannelID:       configString(profile.Config, "voice_channel_id"),
		TextChannelID:        configString(profile.Config, "text_channel_id"),
		BotTokenConfigured:   status.Configured,
		BotTokenFingerprint:  status.Fingerprint,
		CaptionEnabled:       configBool(profile.Config, "caption_enabled"),
		STTProfileID:         configString(profile.Config, "stt_profile_id"),
		ReconnectEnabled:     true,
		ReconnectMaxAttempts: configInt(profile.Config, "reconnect_max_attempts"),
		ReconnectBaseDelay:   configString(profile.Config, "reconnect_base_delay"),
		ReconnectMaxDelay:    configString(profile.Config, "reconnect_max_delay"),
		AudioForwardEnabled:  true,
		CreatedAt:            profile.CreatedAt,
		UpdatedAt:            profile.UpdatedAt,
	}
}
