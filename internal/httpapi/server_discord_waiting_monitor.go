package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

const discordWaitingMonitorCapability = "discord_waiting_monitor_v1"

type serviceRuntimeDiscordWaitingMonitor struct {
	Version            int                                   `json:"version"`
	ExecutionActive    bool                                  `json:"execution_active"`
	RequiredCapability string                                `json:"required_capability"`
	AvailabilityCode   string                                `json:"availability_code,omitempty"`
	Bindings           []serviceRuntimeDiscordWaitingBinding `json:"bindings"`
}

type serviceRuntimeDiscordWaitingBinding struct {
	store.DiscordWaitingCredentialBinding
	GuildID          string `json:"guild_id"`
	VoiceChannelID   string `json:"voice_channel_id"`
	TextChannelID    string `json:"text_channel_id,omitempty"`
	TargetRevision   uint64 `json:"target_revision"`
	AutoStartTrigger string `json:"auto_start_trigger"`
}

func (s *Server) runtimeDiscordWaitingMonitor(ctx context.Context, service store.RegisteredService, current *serviceRuntimeCurrentDiscordBindings) (*serviceRuntimeDiscordWaitingMonitor, error) {
	if service.ServiceType != "discord_bot" {
		return nil, nil
	}
	monitor := &serviceRuntimeDiscordWaitingMonitor{
		Version: 1, RequiredCapability: discordWaitingMonitorCapability,
		Bindings: []serviceRuntimeDiscordWaitingBinding{},
	}
	if current != nil && current.AvailabilityCode != "" {
		monitor.AvailabilityCode = current.AvailabilityCode
		return monitor, nil
	}
	snapshot, err := s.currentDiscordCredentialStores().WaitingDiscordSnapshot(ctx, service.ServiceID, service.TokenID)
	if errors.Is(err, store.ErrDiscordWaitingBindingUnavailable) {
		monitor.AvailabilityCode = "discord_waiting_monitor_unavailable"
		return monitor, nil
	}
	if err != nil {
		return nil, err
	}
	monitor.ExecutionActive = snapshot.ExecutionActive
	profiles := map[string]store.Profile{}
	for _, profile := range snapshot.Profiles {
		profiles[profile.ID] = profile
	}
	for _, credential := range snapshot.Bindings {
		binding := serviceRuntimeDiscordWaitingBinding{
			DiscordWaitingCredentialBinding: credential,
			AutoStartTrigger:                autoStartTriggerDiscordVoiceJoin,
		}
		if credential.Ready {
			if s.streamVisual == nil {
				binding.Ready, binding.ReadinessCode = false, "discord_waiting_target_unavailable"
			} else if visual, visualErr := s.streamVisual.Get(ctx, credential.StreamID); visualErr != nil {
				binding.Ready, binding.ReadinessCode = false, "discord_waiting_target_unavailable"
			} else {
				profile := profiles[credential.ProfileID]
				binding.GuildID = strings.TrimSpace(firstNonEmpty(visual.DiscordGuildID, configString(profile.Config, "guild_id")))
				binding.VoiceChannelID = strings.TrimSpace(firstNonEmpty(visual.DiscordVoiceChannelID, configString(profile.Config, "voice_channel_id")))
				binding.TextChannelID = strings.TrimSpace(firstNonEmpty(visual.DiscordTextChannelID, configString(profile.Config, "text_channel_id")))
				binding.TargetRevision = visual.DiscordSnapshotRevision
				if binding.GuildID == "" || binding.VoiceChannelID == "" || binding.TargetRevision == 0 {
					binding.Ready, binding.ReadinessCode = false, "discord_waiting_target_unavailable"
				}
			}
		}
		monitor.Bindings = append(monitor.Bindings, binding)
	}
	classifyDiscordWaitingMonitor(monitor, current)
	if supported, _ := service.ReportedCapabilities[discordWaitingMonitorCapability].(bool); !supported && len(monitor.Bindings) != 0 && monitor.AvailabilityCode == "" {
		monitor.AvailabilityCode = "discord_waiting_monitor_capability_unavailable"
	}
	return monitor, nil
}

func classifyDiscordWaitingMonitor(monitor *serviceRuntimeDiscordWaitingMonitor, current *serviceRuntimeCurrentDiscordBindings) {
	activeCredential := ""
	if current != nil && monitor.ExecutionActive {
		for _, binding := range current.Bindings {
			if binding.Ready {
				activeCredential = binding.SecretName + "\x00" + binding.Fingerprint
				break
			}
		}
	}
	credentials := map[string]bool{}
	for i := range monitor.Bindings {
		binding := &monitor.Bindings[i]
		if !binding.Ready {
			continue
		}
		credential := binding.SecretName + "\x00" + binding.Fingerprint
		if activeCredential != "" && credential != activeCredential {
			binding.Ready, binding.ReadinessCode = false, "discord_waiting_credential_conflict"
			continue
		}
		credentials[credential] = true
	}
	if activeCredential == "" && len(credentials) > 1 {
		monitor.AvailabilityCode = "discord_waiting_credentials_ambiguous"
		for i := range monitor.Bindings {
			if monitor.Bindings[i].Ready {
				monitor.Bindings[i].Ready, monitor.Bindings[i].ReadinessCode = false, monitor.AvailabilityCode
			}
		}
		return
	}
	targetCounts := map[string]int{}
	for _, binding := range monitor.Bindings {
		if binding.Ready {
			targetCounts[runtimeDiscordVoiceKey(binding.GuildID, binding.VoiceChannelID)]++
		}
	}
	for i := range monitor.Bindings {
		binding := &monitor.Bindings[i]
		if binding.Ready && targetCounts[runtimeDiscordVoiceKey(binding.GuildID, binding.VoiceChannelID)] > 1 {
			binding.Ready, binding.ReadinessCode = false, "discord_waiting_target_ambiguous"
		}
	}
}

func (s *Server) resolveDiscordWaitingBindingSecret(w http.ResponseWriter, r *http.Request, token store.ServiceToken, body serviceRuntimeSecretResolveRequest) {
	serviceID, secretName := strings.TrimSpace(body.ServiceID), strings.TrimSpace(body.SecretName)
	fail := func(status int, code string) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": code, "secret_name": secretName})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, map[string]string{"code": code})
	}
	if body.DiscordWaitingBindingVersion != 1 || len(body.DiscordWaitingBinding) == 0 ||
		bytes.Equal(bytes.TrimSpace(body.DiscordWaitingBinding), []byte("null")) ||
		len(body.CurrentDiscordBinding) != 0 || body.CurrentDiscordBindingVersion != 0 ||
		strings.TrimSpace(body.StreamID) != "" || strings.TrimSpace(body.ArchiveProfileID) != "" {
		fail(http.StatusBadRequest, "invalid_discord_waiting_binding")
		return
	}
	var expected store.DiscordWaitingCredentialBinding
	decoder := json.NewDecoder(bytes.NewReader(body.DiscordWaitingBinding))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err != nil || expected.ServiceID != serviceID || expected.SecretName != secretName {
		fail(http.StatusBadRequest, "invalid_discord_waiting_binding")
		return
	}
	value, lease, err := s.currentDiscordCredentialStores().ResolveWaitingDiscordBinding(r.Context(), serviceID, token.ID, expected, runtimeSecretLeaseTTL)
	switch {
	case errors.Is(err, store.ErrRuntimeSecretLeaseActive):
		fail(http.StatusConflict, "runtime_secret_lease_active")
		return
	case errors.Is(err, store.ErrServiceAssignmentConflict):
		fail(http.StatusConflict, "discord_waiting_binding_changed")
		return
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrUnauthorized), errors.Is(err, store.ErrNotFound):
		fail(http.StatusForbidden, "discord_waiting_binding_unavailable")
		return
	case errors.Is(err, store.ErrDiscordWaitingBindingUnavailable):
		fail(http.StatusServiceUnavailable, "discord_waiting_binding_unavailable")
		return
	case err != nil:
		fail(http.StatusInternalServerError, "resolve_runtime_secret_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "success", map[string]any{"secret_name": secretName, "waiting_monitor": true, "lease_expires_at": lease.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")})
	writeOneTimeSecretJSON(w, http.StatusOK, serviceRuntimeSecretResolveResponse{SecretName: secretName, Value: value, ExpiresInSec: int(runtimeSecretLeaseTTL.Seconds())})
}
