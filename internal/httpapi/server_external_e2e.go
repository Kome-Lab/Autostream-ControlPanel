package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

type externalE2EConfigResponse struct {
	SchemaVersion      int                           `json:"schema_version"`
	StreamID           string                        `json:"stream_id"`
	RuntimeConfig      externalE2ERuntimeConfig      `json:"runtime_config"`
	ServiceAssignments externalE2EServiceAssignments `json:"service_assignments"`
	Confirmations      externalE2EConfirmations      `json:"confirmations"`
	Readiness          externalE2EReadiness          `json:"readiness"`
}

type externalE2ERuntimeConfig struct {
	YouTubeOutputID    string `json:"youtube_output_id"`
	DriveDestinationID string `json:"drive_destination_id"`
	DiscordConfigID    string `json:"discord_config_id"`
	EncoderProfileID   string `json:"encoder_profile_id"`
	ArchiveProfileID   string `json:"archive_profile_id"`
}

type externalE2EServiceAssignments struct {
	DiscordBotServiceID             string `json:"discord_bot_service_id"`
	EncoderRecorderPrimaryServiceID string `json:"encoder_recorder_primary_service_id"`
	WorkerPrimaryServiceID          string `json:"worker_primary_service_id"`
	EncoderRecorderStandbyServiceID string `json:"encoder_recorder_standby_service_id"`
	WorkerStandbyServiceID          string `json:"worker_standby_service_id"`
}

type externalE2EConfirmations struct {
	YouTubeOutputSaved               bool `json:"youtube_output_saved"`
	DriveDestinationSaved            bool `json:"drive_destination_saved"`
	DiscordConfigSaved               bool `json:"discord_config_saved"`
	PrimaryAssignmentsSaved          bool `json:"primary_assignments_saved"`
	RuntimeConfigDistributionEnabled bool `json:"runtime_config_distribution_enabled"`
}

type externalE2EReadiness struct {
	Ready                            bool     `json:"ready"`
	MissingConfirmations             []string `json:"missing_confirmations"`
	MissingRuntimeIDs                []string `json:"missing_runtime_ids"`
	MissingPrimaryServices           []string `json:"missing_primary_services"`
	MissingRuntimeConfigCapabilities []string `json:"missing_runtime_config_capabilities"`
}

func (s *Server) externalE2EConfig(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	payload, err := s.externalE2EConfigForStream(r.Context(), stream)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "external_e2e_config_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) externalE2EConfigForStream(ctx context.Context, stream store.Stream) (externalE2EConfigResponse, error) {
	payload := externalE2EConfigResponse{
		SchemaVersion: 1,
		StreamID:      stream.ID,
		RuntimeConfig: externalE2ERuntimeConfig{
			YouTubeOutputID:  stream.YouTubeOutputID,
			DiscordConfigID:  stream.DiscordConfigID,
			EncoderProfileID: stream.EncoderProfileID,
			ArchiveProfileID: stream.ArchiveProfileID,
		},
	}
	if stream.YouTubeOutputID != "" {
		ok, err := s.profileExists(ctx, store.ProfileYouTubeOutput, stream.YouTubeOutputID)
		if err != nil {
			return payload, err
		}
		payload.Confirmations.YouTubeOutputSaved = ok
	}
	if stream.DiscordConfigID != "" {
		ok, err := s.profileExists(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
		if err != nil {
			return payload, err
		}
		payload.Confirmations.DiscordConfigSaved = ok
	}
	if stream.ArchiveProfileID != "" {
		profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, stream.ArchiveProfileID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return payload, err
		}
		if err == nil {
			payload.RuntimeConfig.DriveDestinationID = strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
			if payload.RuntimeConfig.DriveDestinationID != "" {
				destination, err := s.integrations.GetDriveDestination(ctx, payload.RuntimeConfig.DriveDestinationID)
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return payload, err
				}
				payload.Confirmations.DriveDestinationSaved = err == nil && destination.FolderIDConfigured
			}
		}
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return payload, err
	}
	payload.ServiceAssignments = externalE2EServiceAssignmentsFromServices(assignments)
	primaryAssignments := primaryStreamAssignments(assignments)
	payload.Confirmations.PrimaryAssignmentsSaved = len(missingServiceTypes(primaryAssignments, requiredStartServiceTypes)) == 0
	payload.Confirmations.RuntimeConfigDistributionEnabled = payload.Confirmations.PrimaryAssignmentsSaved && allServicesHaveCapability(primaryAssignments, "runtime_config")
	payload.Readiness = externalE2EReadinessFromPayload(payload, primaryAssignments)
	return payload, nil
}

func externalE2EReadinessFromPayload(payload externalE2EConfigResponse, primaryAssignments []store.RegisteredService) externalE2EReadiness {
	out := externalE2EReadiness{
		MissingConfirmations:             make([]string, 0),
		MissingRuntimeIDs:                make([]string, 0),
		MissingPrimaryServices:           make([]string, 0),
		MissingRuntimeConfigCapabilities: make([]string, 0),
	}
	for _, item := range []struct {
		name string
		ok   bool
	}{
		{name: "youtube_output_saved", ok: payload.Confirmations.YouTubeOutputSaved},
		{name: "drive_destination_saved", ok: payload.Confirmations.DriveDestinationSaved},
		{name: "discord_config_saved", ok: payload.Confirmations.DiscordConfigSaved},
		{name: "primary_assignments_saved", ok: payload.Confirmations.PrimaryAssignmentsSaved},
		{name: "runtime_config_distribution_enabled", ok: payload.Confirmations.RuntimeConfigDistributionEnabled},
	} {
		if !item.ok {
			out.MissingConfirmations = append(out.MissingConfirmations, item.name)
		}
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "youtube_output_id", value: payload.RuntimeConfig.YouTubeOutputID},
		{name: "drive_destination_id", value: payload.RuntimeConfig.DriveDestinationID},
		{name: "discord_config_id", value: payload.RuntimeConfig.DiscordConfigID},
		{name: "encoder_profile_id", value: payload.RuntimeConfig.EncoderProfileID},
		{name: "archive_profile_id", value: payload.RuntimeConfig.ArchiveProfileID},
	} {
		if strings.TrimSpace(item.value) == "" {
			out.MissingRuntimeIDs = append(out.MissingRuntimeIDs, item.name)
		}
	}
	for _, serviceType := range missingServiceTypes(primaryAssignments, requiredStartServiceTypes) {
		out.MissingPrimaryServices = append(out.MissingPrimaryServices, serviceType)
	}
	for _, service := range primaryAssignments {
		if !serviceCapabilityEnabled(service, "runtime_config") {
			out.MissingRuntimeConfigCapabilities = append(out.MissingRuntimeConfigCapabilities, service.ServiceType)
		}
	}
	out.Ready = len(out.MissingConfirmations) == 0 &&
		len(out.MissingRuntimeIDs) == 0 &&
		len(out.MissingPrimaryServices) == 0 &&
		len(out.MissingRuntimeConfigCapabilities) == 0
	return out
}

func (s *Server) profileExists(ctx context.Context, kind store.ProfileKind, id string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, nil
	}
	_, err := s.profiles.GetProfile(ctx, kind, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func externalE2EServiceAssignmentsFromServices(assignments []store.RegisteredService) externalE2EServiceAssignments {
	var out externalE2EServiceAssignments
	for _, service := range assignments {
		role := normalizeAssignmentRole(service.AssignmentRole)
		switch service.ServiceType {
		case "discord_bot":
			if role == "primary" && out.DiscordBotServiceID == "" {
				out.DiscordBotServiceID = service.ServiceID
			}
		case "encoder_recorder":
			if role == "primary" && out.EncoderRecorderPrimaryServiceID == "" {
				out.EncoderRecorderPrimaryServiceID = service.ServiceID
			}
			if role == "standby" && out.EncoderRecorderStandbyServiceID == "" {
				out.EncoderRecorderStandbyServiceID = service.ServiceID
			}
		case "worker":
			if role == "primary" && out.WorkerPrimaryServiceID == "" {
				out.WorkerPrimaryServiceID = service.ServiceID
			}
			if role == "standby" && out.WorkerStandbyServiceID == "" {
				out.WorkerStandbyServiceID = service.ServiceID
			}
		}
	}
	return out
}

func allServicesHaveCapability(services []store.RegisteredService, capability string) bool {
	if len(services) == 0 {
		return false
	}
	for _, service := range services {
		if !serviceCapabilityEnabled(service, capability) {
			return false
		}
	}
	return true
}

func serviceCapabilityEnabled(service store.RegisteredService, capability string) bool {
	value, ok := service.Capabilities[capability]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
