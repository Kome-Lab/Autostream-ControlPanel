package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

func (s *Server) registerServiceRuntimeRoutes() {
	s.mux.HandleFunc("POST /services/register", s.serviceRegister)
	s.mux.HandleFunc("GET /services/runtime-config", s.serviceRuntimeConfig)
	s.mux.HandleFunc("POST /services/runtime-secrets/resolve", s.serviceRuntimeSecretResolve)
	s.mux.HandleFunc("POST /services/heartbeat", s.serviceHeartbeat)
	s.mux.HandleFunc("POST /services/streams/{id}/start", s.serviceStartStream)
	s.mux.HandleFunc("POST /services/streams/{id}/stop", s.serviceStopStream)
	s.mux.HandleFunc("POST /services/observability/signals", s.serviceObservabilitySignal)
	s.mux.HandleFunc("POST /services/stream-events", s.serviceStreamEvent)
	s.mux.HandleFunc("POST /services/stream-artifacts", s.serviceStreamArtifacts)
	s.mux.HandleFunc("POST /services/notifications/email", s.serviceEmailNotification)
	s.mux.HandleFunc("GET /services/notifications/email/readiness", s.serviceEmailReadiness)
	s.mux.HandleFunc("POST /services/remediation-actions/execute", s.serviceRemediationExecute)
}

func (s *Server) serviceRegister(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.register")
	if !ok {
		return
	}
	var request struct {
		store.ServiceRegistration
		ExecutionHostID json.RawMessage `json:"execution_host_id"`
		OwnershipEpoch  json.RawMessage `json:"ownership_epoch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeServiceAudit(r, token, "services.register", "service", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body := request.ServiceRegistration
	if token.ServiceType == "update_agent" &&
		(len(request.ExecutionHostID) != 0 || len(request.OwnershipEpoch) != 0) {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{
			"reason":                 "server_owned_update_agent_binding",
			"requested_service_type": body.ServiceType,
		})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
		return
	}
	if len(request.ExecutionHostID) != 0 {
		if err := json.Unmarshal(request.ExecutionHostID, &body.ExecutionHostID); err != nil {
			s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "bad_request"})
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	if len(request.OwnershipEpoch) != 0 {
		if err := json.Unmarshal(request.OwnershipEpoch, &body.OwnershipEpoch); err != nil {
			s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "bad_request"})
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	var service store.RegisteredService
	var err error
	if token.ServiceType == "update_agent" {
		s.systemUpdateOperationMu.Lock()
		token, ok = s.authenticateService(w, r, "service.register")
		if !ok {
			s.systemUpdateOperationMu.Unlock()
			return
		}
		service, err = s.services.RegisterService(r.Context(), token, body)
		s.systemUpdateOperationMu.Unlock()
	} else {
		service, err = s.services.RegisterService(r.Context(), token, body)
	}
	if errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "service_token_scope_mismatch", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_scope_mismatch"})
		return
	}
	if errors.Is(err, store.ErrInvalidServiceRegistration) {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "invalid_service_registration", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
		return
	}
	if err != nil {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "register_service_failed", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "register_service_failed"})
		return
	}
	s.writeServiceAudit(r, token, "services.register", "service", service.ServiceID, "success", map[string]any{"service_type": service.ServiceType})
	writeJSON(w, http.StatusAccepted, service)
}

type serviceRuntimeConfigResponse struct {
	Service              serviceRuntimeConfigService         `json:"service"`
	Assignments          []store.StreamServiceAssignment     `json:"assignments"`
	Profiles             map[string][]store.Profile          `json:"profiles"`
	StreamDiscordConfigs []serviceRuntimeDiscordStreamConfig `json:"stream_discord_configs,omitempty"`
	StreamArchiveConfigs []serviceRuntimeArchiveStreamConfig `json:"stream_archive_configs,omitempty"`
	StreamYouTubeConfigs []serviceRuntimeYouTubeStreamConfig `json:"stream_youtube_configs,omitempty"`
}

type serviceRuntimeConfigService struct {
	ServiceID       string         `json:"service_id"`
	ServiceType     string         `json:"service_type"`
	ServiceName     string         `json:"service_name"`
	PublicURL       string         `json:"public_url"`
	Version         string         `json:"version"`
	Status          string         `json:"status"`
	AssignmentRole  string         `json:"assignment_role,omitempty"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	CurrentStreamID string         `json:"current_stream_id,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type serviceRuntimeDiscordStreamConfig struct {
	StreamID         string `json:"stream_id"`
	AssignmentRole   string `json:"assignment_role"`
	DiscordConfigID  string `json:"discord_config_id"`
	GuildID          string `json:"guild_id"`
	VoiceChannelID   string `json:"voice_channel_id"`
	TextChannelID    string `json:"text_channel_id,omitempty"`
	AutoStartTrigger string `json:"auto_start_trigger,omitempty"`
}

type serviceRuntimeArchiveStreamConfig struct {
	StreamID         string         `json:"stream_id"`
	AssignmentRole   string         `json:"assignment_role"`
	ArchiveProfileID string         `json:"archive_profile_id"`
	Ready            bool           `json:"ready"`
	ReadinessCode    string         `json:"readiness_code,omitempty"`
	ReadinessMessage string         `json:"readiness_message,omitempty"`
	ArchiveConfig    map[string]any `json:"archive_config,omitempty"`
}

type serviceRuntimeYouTubeStreamConfig struct {
	StreamID         string         `json:"stream_id"`
	AssignmentRole   string         `json:"assignment_role"`
	YouTubeOutputID  string         `json:"youtube_output_id"`
	Ready            bool           `json:"ready"`
	ReadinessCode    string         `json:"readiness_code,omitempty"`
	ReadinessMessage string         `json:"readiness_message,omitempty"`
	YouTubeConfig    map[string]any `json:"youtube_config,omitempty"`
	ActiveRuntime    map[string]any `json:"active_runtime,omitempty"`
}

func serviceRuntimeConfigServiceFromStore(service store.RegisteredService) serviceRuntimeConfigService {
	return serviceRuntimeConfigService{
		ServiceID:       service.ServiceID,
		ServiceType:     service.ServiceType,
		ServiceName:     service.ServiceName,
		PublicURL:       service.PublicURL,
		Version:         service.Version,
		Status:          service.Status,
		AssignmentRole:  service.AssignmentRole,
		LastHeartbeatAt: service.LastHeartbeatAt,
		CurrentStreamID: service.CurrentStreamID,
		Capabilities:    service.Capabilities,
		Metrics:         service.Metrics,
		CreatedAt:       service.CreatedAt,
		UpdatedAt:       service.UpdatedAt,
	}
}

type serviceRuntimeSecretResolveRequest struct {
	ServiceID        string `json:"service_id"`
	StreamID         string `json:"stream_id,omitempty"`
	ArchiveProfileID string `json:"archive_profile_id,omitempty"`
	SecretName       string `json:"secret_name"`
}

type serviceRuntimeSecretResolveResponse struct {
	SecretName   string `json:"secret_name"`
	Value        string `json:"value"`
	ExpiresInSec int    `json:"expires_in_sec"`
}

func (s *Server) serviceRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	if serviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if service.TokenID != token.ID || service.ServiceType != token.ServiceType {
		s.writeServiceAudit(r, token, "services.runtime_config.read", "service", serviceID, "failure", map[string]any{"reason": "service_not_assigned_to_token"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	payload, code, err := s.runtimeConfigForService(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": code})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_config.read", "service", serviceID, "success", map[string]any{"assignment_count": len(payload.Assignments)})
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) adminServiceRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("id"))
	if serviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	payload, code, err := s.runtimeConfigForService(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": code})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.runtime_config.preview", ResourceType: "service", ResourceID: serviceID, Result: "success", Metadata: map[string]any{"assignment_count": len(payload.Assignments), "service_type": service.ServiceType}})
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeConfigForService(ctx context.Context, service store.RegisteredService) (serviceRuntimeConfigResponse, string, error) {
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, service.ServiceID)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_service_assignments_failed", err
	}
	profiles, err := s.runtimeProfilesForService(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_profiles_failed", err
	}
	discordConfigs, err := s.runtimeDiscordStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_discord_configs_failed", err
	}
	archiveConfigs, err := s.runtimeArchiveStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_archive_configs_failed", err
	}
	youtubeConfigs, err := s.runtimeYouTubeStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_youtube_configs_failed", err
	}
	return serviceRuntimeConfigResponse{Service: serviceRuntimeConfigServiceFromStore(service), Assignments: assignments, Profiles: profiles, StreamDiscordConfigs: discordConfigs, StreamArchiveConfigs: archiveConfigs, StreamYouTubeConfigs: youtubeConfigs}, "", nil
}

func (s *Server) serviceRuntimeSecretResolve(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.secret.resolve")
	if !ok {
		return
	}
	var body serviceRuntimeSecretResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	serviceID := strings.TrimSpace(body.ServiceID)
	secretName := strings.TrimSpace(body.SecretName)
	if serviceID == "" || secretName == "" {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_id_or_secret_name_required", "secret_name": secretName})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_or_secret_name_required"})
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		w.Header().Set("Cache-Control", "no-store")
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_transport_insecure", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_secret_transport_insecure"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_not_registered", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if service.TokenID != token.ID || service.ServiceType != token.ServiceType {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_not_assigned_to_token", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	allowed, err := s.runtimeSecretAllowedForService(r.Context(), service, secretName, strings.TrimSpace(body.StreamID), strings.TrimSpace(body.ArchiveProfileID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_runtime_profiles_failed"})
		return
	}
	if !allowed {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_not_allowed", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_secret_not_allowed"})
		return
	}
	lease, err := s.runtimeLeases.ClaimRuntimeSecretLease(r.Context(), store.RuntimeSecretLease{
		ServiceID:        serviceID,
		TokenID:          token.ID,
		StreamID:         strings.TrimSpace(body.StreamID),
		ArchiveProfileID: strings.TrimSpace(body.ArchiveProfileID),
		SecretName:       secretName,
	}, runtimeSecretLeaseTTL)
	if errors.Is(err, store.ErrRuntimeSecretLeaseActive) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_lease_active", "secret_name": secretName})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_secret_lease_active"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "runtime_secret_lease_failed"})
		return
	}
	value, err := s.runtimeSecretValue(r.Context(), secretName)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_not_configured", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_secret_not_configured"})
		return
	}
	if errors.Is(err, store.ErrUnknownSecret) {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_unknown", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_secret_unknown"})
		return
	}
	if err != nil {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_runtime_secret_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "success", map[string]any{"secret_name": secretName, "lease_expires_at": lease.ExpiresAt.Format(time.RFC3339)})
	writeOneTimeSecretJSON(w, http.StatusOK, serviceRuntimeSecretResolveResponse{
		SecretName:   secretName,
		Value:        value,
		ExpiresInSec: int(runtimeSecretLeaseTTL / time.Second),
	})
}

func (s *Server) runtimeProfilesForService(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) (map[string][]store.Profile, error) {
	kinds := runtimeProfileKindsForService(service.ServiceType)
	profiles := make(map[string][]store.Profile, len(kinds))
	streamEncoderProfileIDs := make(map[string]struct{})
	streamOverlayProfileIDs := make(map[string]struct{})
	if service.ServiceType == "encoder_recorder" || service.ServiceType == "worker" {
		for _, assignment := range assignments {
			if assignment.ServiceID != service.ServiceID || assignment.ServiceType != service.ServiceType {
				continue
			}
			stream, err := s.streams.GetStream(ctx, assignment.StreamID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if service.ServiceType == "encoder_recorder" {
				if profileID := strings.TrimSpace(stream.EncoderProfileID); profileID != "" {
					streamEncoderProfileIDs[profileID] = struct{}{}
				}
			}
			if profileID := strings.TrimSpace(stream.OverlayProfileID); profileID != "" {
				streamOverlayProfileIDs[profileID] = struct{}{}
			}
		}
	}
	for _, kind := range kinds {
		items, err := s.profiles.ListProfiles(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if kind != store.ProfileCaption && !runtimeProfileMatchesService(item.Config, service.ServiceID) &&
				!(kind == store.ProfileEncoder && service.ServiceType == "encoder_recorder" &&
					runtimeUnscopedProfileMayFollowAssignedMediaService(item, streamEncoderProfileIDs)) &&
				!(kind == store.ProfileOverlay && (service.ServiceType == "encoder_recorder" || service.ServiceType == "worker") &&
					runtimeUnscopedProfileMayFollowAssignedMediaService(item, streamOverlayProfileIDs)) {
				continue
			}
			item.Config = sanitizeRuntimeProfileConfigForKind(kind, item.Config)
			profiles[string(kind)] = append(profiles[string(kind)], item)
		}
	}
	return profiles, nil
}

// Encoder and overlay profiles are selected by stream settings, so an
// unscoped profile referenced by an assigned stream follows the media service
// that must apply it. Explicit service bindings still win and are never
// overridden by the stream reference.
func runtimeUnscopedProfileMayFollowAssignedMediaService(profile store.Profile, selectedProfileIDs map[string]struct{}) bool {
	if _, referenced := selectedProfileIDs[profile.ID]; !referenced {
		return false
	}
	if _, configured := profile.Config["service_id"]; configured && strings.TrimSpace(configString(profile.Config, "service_id")) != "" {
		return false
	}
	if values, configured := profile.Config["service_ids"]; configured {
		if list, ok := values.([]any); ok && len(list) > 0 {
			return false
		}
		if list, ok := values.([]string); ok && len(list) > 0 {
			return false
		}
	}
	return true
}

func (s *Server) runtimeDiscordStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeDiscordStreamConfig, error) {
	if service.ServiceType != "discord_bot" {
		return nil, nil
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	assignmentRoles := assignmentRoleByStream(assignments, service.ServiceID, "discord_bot")
	type implicitCandidate struct {
		item serviceRuntimeDiscordStreamConfig
		key  string
	}
	explicitPrimaryByVoice := make(map[string]struct{})
	explicitItems := make([]serviceRuntimeDiscordStreamConfig, 0)
	implicitCandidates := make([]implicitCandidate, 0)
	for _, stream := range streams {
		// The Bot uses this payload as its waiting auto-start/defaults source. Do
		// not resend active, completed, or stopping streams: a completed source can
		// coexist briefly with its rearmed waiting successor and otherwise creates
		// an ambiguous primary candidate for the same VC.
		if !isRuntimeDiscordConfigStreamStatus(stream.Status) {
			continue
		}
		if strings.TrimSpace(stream.DiscordConfigID) == "" {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !runtimeProfileMatchesService(profile.Config, service.ServiceID) {
			continue
		}
		visual, err := s.streamVisual.Get(ctx, stream.ID)
		if errors.Is(err, streamvisual.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		guildID := strings.TrimSpace(firstNonEmpty(visual.DiscordGuildID, configString(profile.Config, "guild_id")))
		voiceChannelID := strings.TrimSpace(firstNonEmpty(visual.DiscordVoiceChannelID, configString(profile.Config, "voice_channel_id")))
		if guildID == "" || voiceChannelID == "" {
			continue
		}
		item := serviceRuntimeDiscordStreamConfig{
			StreamID:         stream.ID,
			DiscordConfigID:  profile.ID,
			GuildID:          guildID,
			VoiceChannelID:   voiceChannelID,
			TextChannelID:    strings.TrimSpace(firstNonEmpty(visual.DiscordTextChannelID, configString(profile.Config, "text_channel_id"))),
			AutoStartTrigger: strings.TrimSpace(stream.AutoStartTrigger),
		}
		voiceKey := runtimeDiscordVoiceKey(guildID, voiceChannelID)
		if assignmentRole, assigned := assignmentRoles[stream.ID]; assigned {
			// A standby Bot must not receive a start/default config. The explicit
			// primary assignment is authoritative even when another unassigned
			// waiting stream happens to use the same VC.
			if normalizeAssignmentRole(assignmentRole) != "primary" {
				continue
			}
			item.AssignmentRole = "primary"
			explicitPrimaryByVoice[voiceKey] = struct{}{}
			explicitItems = append(explicitItems, item)
			continue
		}
		// A waiting VC-auto stream deliberately has no assignment until the Bot
		// requests its start. It may be presented as implicit primary only when
		// it is the sole candidate for this Bot/VC; guessing between two saved
		// streams would start the wrong one.
		if isAutoStartableStreamStatus(stream.Status) && strings.EqualFold(item.AutoStartTrigger, autoStartTriggerDiscordVoiceJoin) {
			item.AssignmentRole = "primary"
			implicitCandidates = append(implicitCandidates, implicitCandidate{item: item, key: voiceKey})
		}
	}
	sort.Slice(explicitItems, func(i, j int) bool {
		return explicitItems[i].StreamID < explicitItems[j].StreamID
	})
	sort.Slice(implicitCandidates, func(i, j int) bool {
		if implicitCandidates[i].key == implicitCandidates[j].key {
			return implicitCandidates[i].item.StreamID < implicitCandidates[j].item.StreamID
		}
		return implicitCandidates[i].key < implicitCandidates[j].key
	})
	items := append([]serviceRuntimeDiscordStreamConfig(nil), explicitItems...)
	for index := 0; index < len(implicitCandidates); {
		next := index + 1
		for next < len(implicitCandidates) && implicitCandidates[next].key == implicitCandidates[index].key {
			next++
		}
		if _, explicit := explicitPrimaryByVoice[implicitCandidates[index].key]; !explicit && next-index == 1 {
			items = append(items, implicitCandidates[index].item)
		}
		index = next
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StreamID < items[j].StreamID
	})
	return items, nil
}

// isRuntimeDiscordConfigStreamStatus keeps the runtime payload scoped to
// waiting streams for which a Bot might need an auto-start config. Active and
// terminal streams are started/stopped through an explicit service dispatch,
// so they must not compete with a rearmed waiting stream for the same VC.
func isRuntimeDiscordConfigStreamStatus(status string) bool {
	return isAutoStartableStreamStatus(status)
}

func runtimeDiscordVoiceKey(guildID, voiceChannelID string) string {
	return strings.TrimSpace(guildID) + "\x00" + strings.TrimSpace(voiceChannelID)
}

func assignmentRoleByStream(assignments []store.StreamServiceAssignment, serviceID, serviceType string) map[string]string {
	roles := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.ServiceID) != serviceID || strings.TrimSpace(assignment.ServiceType) != serviceType {
			continue
		}
		role := normalizeAssignmentRole(assignment.AssignmentRole)
		if role == "" {
			role = "primary"
		}
		roles[assignment.StreamID] = role
	}
	return roles
}

func (s *Server) runtimeArchiveStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeArchiveStreamConfig, error) {
	if service.ServiceType != "encoder_recorder" {
		return nil, nil
	}
	items := make([]serviceRuntimeArchiveStreamConfig, 0)
	for _, assignment := range assignments {
		if assignment.ServiceID != service.ServiceID || assignment.ServiceType != "encoder_recorder" {
			continue
		}
		stream, err := s.streams.GetStream(ctx, assignment.StreamID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(stream.ArchiveProfileID) == "" {
			continue
		}
		req := servicecall.StartRequest{ArchiveProfileID: stream.ArchiveProfileID}
		item := serviceRuntimeArchiveStreamConfig{
			StreamID:         stream.ID,
			AssignmentRole:   assignment.AssignmentRole,
			ArchiveProfileID: stream.ArchiveProfileID,
			Ready:            true,
		}
		if err := s.applyArchiveConfig(ctx, &req); err != nil {
			item.Ready = false
			item.ReadinessCode = archiveConfigCode(err)
			item.ReadinessMessage = archiveConfigReadinessMessage(err)
		} else if len(req.ArchiveConfig) > 0 {
			item.ArchiveConfig = sanitizeRuntimeProfileConfig(req.ArchiveConfig)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) runtimeYouTubeStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeYouTubeStreamConfig, error) {
	if service.ServiceType != "encoder_recorder" {
		return nil, nil
	}
	items := make([]serviceRuntimeYouTubeStreamConfig, 0)
	for _, assignment := range assignments {
		if assignment.ServiceID != service.ServiceID || assignment.ServiceType != "encoder_recorder" {
			continue
		}
		stream, err := s.streams.GetStream(ctx, assignment.StreamID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(stream.YouTubeOutputID) == "" {
			continue
		}
		item := serviceRuntimeYouTubeStreamConfig{
			StreamID:        stream.ID,
			AssignmentRole:  assignment.AssignmentRole,
			YouTubeOutputID: stream.YouTubeOutputID,
			Ready:           true,
		}
		profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, stream.YouTubeOutputID)
		if errors.Is(err, store.ErrNotFound) {
			item.Ready = false
			item.ReadinessCode = errYouTubeOutputNotFound.Error()
			item.ReadinessMessage = youtubeOutputReadinessMessage(errYouTubeOutputNotFound)
			items = append(items, item)
			continue
		}
		if err != nil {
			return nil, err
		}
		item.YouTubeConfig = youtubeRuntimeConfigFromProfile(profile)
		req := servicecall.StartRequest{YouTubeOutputID: stream.YouTubeOutputID}
		// Resolve the Encoder capability first. A legacy or unknown Encoder must
		// report the relay-policy reason rather than an unrelated OAuth/secret
		// readiness detail for a dynamic output it is not allowed to receive.
		serviceForAssignment := service
		serviceForAssignment.AssignmentRole = assignment.AssignmentRole
		if err := s.validateYouTubeLiveAPIOutputRelay(ctx, []store.RegisteredService{serviceForAssignment}, &req); err != nil {
			item.Ready = false
			item.ReadinessCode = youtubeOutputCode(err)
			item.ReadinessMessage = youtubeOutputReadinessMessage(err)
		} else if err := s.validateYouTubeOutputReadiness(ctx, stream, &req); err != nil {
			item.Ready = false
			item.ReadinessCode = youtubeOutputCode(err)
			item.ReadinessMessage = youtubeOutputReadinessMessage(err)
		}
		if runtime := s.safeYouTubeRuntimeForStream(ctx, stream.ID); len(runtime) > 0 {
			item.ActiveRuntime = runtime
		}
		items = append(items, item)
	}
	return items, nil
}

func youtubeRuntimeConfigFromProfile(profile store.Profile) map[string]any {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		mode = "stream_key"
	}
	out := map[string]any{
		"mode":      mode,
		"output_id": profile.ID,
	}
	for key, value := range map[string]string{
		"oauth_account_id":         firstNonEmpty(configString(profile.Config, "oauth_account_id"), configString(profile.Config, "youtube_oauth_account_id")),
		"broadcast_title_template": firstNonEmpty(configString(profile.Config, "broadcast_title_template"), configString(profile.Config, "broadcast_title")),
		"broadcast_description":    configString(profile.Config, "broadcast_description"),
		"privacy_status":           configString(profile.Config, "privacy_status"),
		"latency_preference":       configString(profile.Config, "latency_preference"),
	} {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if mode == "live_api_relay_static" {
		relayBindingID := configString(profile.Config, "relay_binding_id")
		if validYouTubeRelayStaticBindingID(relayBindingID) {
			out["relay_binding_id"] = relayBindingID
			if reusableLiveStreamID := strings.TrimSpace(configString(profile.Config, "reusable_live_stream_id")); reusableLiveStreamID != "" {
				out["reusable_live_stream_id"] = reusableLiveStreamID
			}
		}
	} else {
		for key, value := range map[string]string{
			"rtmp_url":               configString(profile.Config, "rtmp_url"),
			"stream_key_secret_name": configString(profile.Config, "stream_key_secret_name"),
			"watch_url":              configString(profile.Config, "watch_url"),
		} {
			if strings.TrimSpace(value) != "" {
				out[key] = value
			}
		}
	}
	out["enable_auto_start"] = youtubeOutputAutoStartEnabled(profile.Config)
	out["enable_auto_stop"] = configBool(profile.Config, "enable_auto_stop")
	out["complete_on_stop"] = youtubeCompleteOnStop(profile.Config)
	return sanitizeRuntimeProfileConfig(out)
}

func (s *Server) safeYouTubeRuntimeForStream(ctx context.Context, streamID string) map[string]any {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if err != nil {
		return nil
	}
	out := map[string]any{
		"mode":                 runtime.Mode,
		"output_id":            runtime.YouTubeOutput,
		"dry_run":              runtime.DryRun,
		"complete_on_stop":     runtime.CompleteOnStop,
		"complete_retry_count": runtime.CompleteRetryCount,
	}
	if !runtime.CompleteNextRetryAt.IsZero() {
		out["complete_next_retry_at"] = runtime.CompleteNextRetryAt.UTC().Format(time.RFC3339)
	}
	for key, value := range map[string]string{
		"oauth_account_id":       runtime.OAuthAccountID,
		"broadcast_id":           runtime.BroadcastID,
		"live_stream_id":         runtime.LiveStreamID,
		"rtmp_url":               runtime.RTMPURL,
		"stream_key_secret_name": runtime.StreamKeySecretName,
		"complete_last_error":    runtime.CompleteLastError,
	} {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return sanitizeRuntimeProfileConfig(out)
}
