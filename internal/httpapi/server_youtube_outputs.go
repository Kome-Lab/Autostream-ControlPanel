package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type youtubeOutputRequest struct {
	Name                   string `json:"name"`
	Mode                   string `json:"mode"`
	RTMPURL                string `json:"rtmp_url"`
	StreamKey              string `json:"stream_key"`
	WatchURL               string `json:"watch_url"`
	OAuthAccountID         string `json:"oauth_account_id"`
	UseConfiguredStreamKey bool   `json:"use_configured_stream_key"`
	RelayBindingID         string `json:"relay_binding_id"`
	ReusableLiveStreamID   string `json:"reusable_live_stream_id"`
	BroadcastTitleTemplate string `json:"broadcast_title_template"`
	BroadcastDescription   string `json:"broadcast_description"`
	PrivacyStatus          string `json:"privacy_status"`
	LatencyPreference      string `json:"latency_preference"`
	EnableAutoStart        *bool  `json:"enable_auto_start"`
	EnableAutoStop         *bool  `json:"enable_auto_stop"`
	CompleteOnStop         *bool  `json:"complete_on_stop"`
	Config                 any    `json:"config"`
}

type youtubeOutputResponse struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Mode                   string    `json:"mode"`
	RTMPURL                string    `json:"rtmp_url,omitempty"`
	StreamKeyConfigured    bool      `json:"stream_key_configured,omitempty"`
	StreamKeyFingerprint   string    `json:"stream_key_fingerprint,omitempty"`
	WatchURL               string    `json:"watch_url,omitempty"`
	OAuthAccountID         string    `json:"oauth_account_id,omitempty"`
	UseConfiguredStreamKey bool      `json:"use_configured_stream_key,omitempty"`
	RelayBindingID         string    `json:"relay_binding_id,omitempty"`
	ReusableLiveStreamID   string    `json:"reusable_live_stream_id,omitempty"`
	BroadcastTitleTemplate string    `json:"broadcast_title_template,omitempty"`
	BroadcastDescription   string    `json:"broadcast_description,omitempty"`
	PrivacyStatus          string    `json:"privacy_status,omitempty"`
	LatencyPreference      string    `json:"latency_preference,omitempty"`
	EnableAutoStart        bool      `json:"enable_auto_start,omitempty"`
	EnableAutoStop         bool      `json:"enable_auto_stop,omitempty"`
	CompleteOnStop         bool      `json:"complete_on_stop"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (s *Server) listYouTubeOutputs(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.profiles.ListProfiles(r.Context(), store.ProfileYouTubeOutput)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_youtube_outputs_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	out := make([]youtubeOutputResponse, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, youtubeOutputFromProfile(profile, statuses))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	var body youtubeOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	config, err := youtubeOutputConfigFromRequest(body, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_youtube_output"})
		return
	}
	if code, status := s.validateYouTubeOutputOAuthAccount(r.Context(), config); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	profile, err := s.profiles.CreateProfile(r.Context(), store.ProfileYouTubeOutput, body.Name, config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_youtube_output_failed"})
		return
	}
	if strings.TrimSpace(body.StreamKey) != "" {
		secretName := youtubeOutputSecretName(profile.ID)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.StreamKey)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_youtube_stream_key_failed"})
			return
		}
		config["stream_key_secret_name"] = secretName
		profile, err = s.profiles.UpdateProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID, profile.Name, config)
		if err != nil {
			_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_youtube_output_failed"})
			return
		}
		profile.Config["stream_key_fingerprint"] = status.Fingerprint
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.create", ResourceType: "youtube_output", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"mode": normalizedYouTubeOutputMode(body.Mode), "stream_key_configured": strings.TrimSpace(body.StreamKey) != ""}})
	writeJSON(w, http.StatusCreated, youtubeOutputFromProfile(profile, secretStatusListForConfig(profile, "stream_key_secret_name", "stream_key_fingerprint")))
}

func (s *Server) getYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, youtubeOutputFromProfile(profile, statuses))
}

func (s *Server) updateYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	var body youtubeOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	if blocked, err := s.youtubeRelayBindingOutputClaimActive(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "youtube_relay_binding_claim_check_failed"})
		return
	} else if blocked {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.update", ResourceType: "youtube_output", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "youtube_relay_binding_release_pending"}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
		return
	}
	config, err := youtubeOutputConfigFromRequest(body, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_youtube_output"})
		return
	}
	if code, status := s.validateYouTubeOutputOAuthAccount(r.Context(), config); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	existingSecret := strings.TrimSpace(configString(existing.Config, "stream_key_secret_name"))
	staticRelayMode := normalizedYouTubeOutputMode(configString(config, "mode")) == "live_api_relay_static"
	if existingSecret != "" && !staticRelayMode && strings.TrimSpace(body.StreamKey) == "" {
		config["stream_key_secret_name"] = existingSecret
	}
	if strings.TrimSpace(body.StreamKey) != "" {
		secretName := youtubeOutputSecretName(id)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.StreamKey)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_youtube_stream_key_failed"})
			return
		}
		config["stream_key_secret_name"] = secretName
		config["stream_key_fingerprint"] = status.Fingerprint
	}
	profile, err := s.profiles.UpdateProfile(r.Context(), store.ProfileYouTubeOutput, id, body.Name, config)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_youtube_output_failed"})
		return
	}
	if staticRelayMode && existingSecret != "" {
		_, _ = s.secrets.UpdateSecret(r.Context(), existingSecret, "")
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.update", ResourceType: "youtube_output", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"mode": normalizedYouTubeOutputMode(body.Mode), "stream_key_updated": strings.TrimSpace(body.StreamKey) != ""}})
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, youtubeOutputFromProfile(profile, statuses))
}

func (s *Server) deleteYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	if blocked, err := s.youtubeRelayBindingOutputClaimActive(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "youtube_relay_binding_claim_check_failed"})
		return
	} else if blocked {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.delete", ResourceType: "youtube_output", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "youtube_relay_binding_release_pending"}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
		return
	}
	if s.writeProfileDeleteBlockedIfInUse(w, r, store.ProfileYouTubeOutput, id) {
		return
	}
	if err := s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_youtube_output_failed"})
		return
	}
	if secretName := strings.TrimSpace(configString(profile.Config, "stream_key_secret_name")); secretName != "" {
		_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.delete", ResourceType: "youtube_output", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func youtubeOutputConfigFromRequest(body youtubeOutputRequest, id string) (map[string]any, error) {
	if body.Config != nil {
		return nil, errors.New("youtube output config must use structured fields")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return nil, errors.New("youtube output name is required")
	}
	mode := normalizedYouTubeOutputMode(body.Mode)
	if mode == "" {
		return nil, errors.New("invalid youtube output mode")
	}
	rtmpURL := strings.TrimSpace(body.RTMPURL)
	if mode == "live_api_relay_static" && (rtmpURL != "" || strings.TrimSpace(body.StreamKey) != "" || strings.TrimSpace(body.WatchURL) != "") {
		return nil, errors.New("relay static output cannot include ingest settings")
	}
	if mode == "stream_key" && rtmpURL == "" {
		rtmpURL = "rtmps://a.rtmps.youtube.com/live2"
	}
	if rtmpURL != "" {
		parsed, err := url.Parse(rtmpURL)
		if err != nil || parsed.User != nil || parsed.Scheme != "rtmps" {
			return nil, errors.New("invalid rtmp_url")
		}
	}
	config := map[string]any{
		"mode": mode,
	}
	if body.UseConfiguredStreamKey {
		if mode != "live_api" {
			return nil, errors.New("configured stream key reuse requires live_api mode")
		}
		config["use_configured_stream_key"] = true
	}
	if rtmpURL != "" {
		config["rtmp_url"] = rtmpURL
	}
	if mode == "stream_key" && id != "" {
		config["stream_key_secret_name"] = youtubeOutputSecretName(id)
	}
	if mode == "live_api_relay_static" {
		relayBindingID := body.RelayBindingID
		reusableLiveStreamID := strings.TrimSpace(body.ReusableLiveStreamID)
		if !validYouTubeRelayStaticBindingID(relayBindingID) || !validYouTubeRelayStaticExternalID(reusableLiveStreamID, 255) {
			return nil, errors.New("relay static output requires valid binding and reusable stream ids")
		}
		config["relay_binding_id"] = relayBindingID
		config["reusable_live_stream_id"] = reusableLiveStreamID
	}
	if value := strings.TrimSpace(body.WatchURL); value != "" {
		normalized, ok := normalizeYouTubeWatchURL(value)
		if !ok {
			return nil, errors.New("invalid watch_url")
		}
		config["watch_url"] = normalized
	}
	if value := strings.TrimSpace(body.OAuthAccountID); value != "" {
		config["oauth_account_id"] = value
	}
	if value := strings.TrimSpace(body.BroadcastTitleTemplate); value != "" {
		config["broadcast_title"] = value
		config["broadcast_title_template"] = value
	}
	if value := strings.TrimSpace(body.BroadcastDescription); value != "" {
		config["broadcast_description"] = value
	}
	if value := strings.TrimSpace(body.PrivacyStatus); value != "" {
		if value != "private" && value != "unlisted" && value != "public" {
			return nil, errors.New("invalid privacy_status")
		}
		config["privacy_status"] = value
	}
	if value := strings.TrimSpace(body.LatencyPreference); value != "" {
		if value != "normal" && value != "low" && value != "ultra_low" {
			return nil, errors.New("invalid latency_preference")
		}
		config["latency_preference"] = value
	}
	if body.EnableAutoStart != nil {
		config["enable_auto_start"] = *body.EnableAutoStart
	} else if youtubeOutputModeDefaultsAutoStart(mode) {
		config["enable_auto_start"] = true
	}
	if body.EnableAutoStop != nil {
		config["enable_auto_stop"] = *body.EnableAutoStop
	}
	if mode == "live_api_relay_static" {
		config["complete_on_stop"] = true
	} else if body.CompleteOnStop != nil {
		config["complete_on_stop"] = *body.CompleteOnStop
	}
	if (mode == "live_api" || mode == "live_api_dry_run" || mode == "live_api_relay_static") && strings.TrimSpace(body.OAuthAccountID) == "" {
		return nil, errors.New("live_api requires oauth_account_id")
	}
	return config, nil
}

func normalizedYouTubeOutputMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "stream_key", "existing_stream_key", "rtmps_stream_key":
		return "stream_key"
	case "live_api_dry_run", "dry_run_live_api", "youtube_live_api_dry_run":
		return "live_api_dry_run"
	case "live_api", "youtube_live_api":
		return "live_api"
	case "live_api_relay_static", "youtube_live_api_relay_static":
		return "live_api_relay_static"
	default:
		return ""
	}
}

func youtubeOutputModeDefaultsAutoStart(mode string) bool {
	return mode == "live_api" || mode == "live_api_relay_static"
}

func youtubeOutputAutoStartEnabled(config map[string]any) bool {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(config, "mode"), configString(config, "output_mode")))
	return configBoolDefault(config, "enable_auto_start", youtubeOutputModeDefaultsAutoStart(mode))
}

func validYouTubeRelayStaticBindingID(value string) bool {
	const prefix = "relay-"
	if len(value) != len(prefix)+36 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for index := len(prefix); index < len(value); index++ {
		char := value[index]
		switch index - len(prefix) {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return true
}

func validYouTubeRelayStaticExternalID(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func youtubeOutputSecretName(id string) string {
	return "youtube_stream_key_" + strings.ToLower(strings.TrimSpace(id))
}

func youtubeOutputFromProfile(profile store.Profile, statuses []store.SecretStatus) youtubeOutputResponse {
	mode := normalizedYouTubeOutputMode(configString(profile.Config, "mode"))
	if mode == "" {
		mode = "stream_key"
	}
	secretName := strings.TrimSpace(configString(profile.Config, "stream_key_secret_name"))
	status := secretStatusByName(statuses, secretName)
	relayBindingID := ""
	if mode == "live_api_relay_static" {
		candidate := configString(profile.Config, "relay_binding_id")
		if validYouTubeRelayStaticBindingID(candidate) {
			relayBindingID = candidate
		}
	}
	return youtubeOutputResponse{
		ID:                     profile.ID,
		Name:                   profile.Name,
		Mode:                   mode,
		RTMPURL:                configString(profile.Config, "rtmp_url"),
		StreamKeyConfigured:    status.Configured,
		StreamKeyFingerprint:   status.Fingerprint,
		WatchURL:               configString(profile.Config, "watch_url"),
		OAuthAccountID:         configString(profile.Config, "oauth_account_id"),
		UseConfiguredStreamKey: configBool(profile.Config, "use_configured_stream_key"),
		RelayBindingID:         relayBindingID,
		ReusableLiveStreamID:   configString(profile.Config, "reusable_live_stream_id"),
		BroadcastTitleTemplate: firstNonEmpty(configString(profile.Config, "broadcast_title_template"), configString(profile.Config, "broadcast_title")),
		BroadcastDescription:   configString(profile.Config, "broadcast_description"),
		PrivacyStatus:          configString(profile.Config, "privacy_status"),
		LatencyPreference:      configString(profile.Config, "latency_preference"),
		EnableAutoStart:        youtubeOutputAutoStartEnabled(profile.Config),
		EnableAutoStop:         configBool(profile.Config, "enable_auto_stop"),
		CompleteOnStop:         youtubeCompleteOnStop(profile.Config),
		CreatedAt:              profile.CreatedAt,
		UpdatedAt:              profile.UpdatedAt,
	}
}

func (s *Server) youtubeRelayBindingOutputClaimActive(ctx context.Context, outputID string) (bool, error) {
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return false, nil
	}
	return claimStore.HasStreamYouTubeRelayBindingClaimForOutput(ctx, strings.TrimSpace(outputID))
}

func (s *Server) youtubeRelayBindingStreamClaimActive(ctx context.Context, streamID string) (bool, error) {
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return false, nil
	}
	_, err := claimStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, strings.TrimSpace(streamID))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
