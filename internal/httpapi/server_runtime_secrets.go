package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const runtimeSecretLeaseTTL = 60 * time.Second

func (s *Server) runtimeSecretAllowedForService(ctx context.Context, service store.RegisteredService, secretName, streamID, archiveProfileID string) (bool, error) {
	if allowed, err := s.runtimeYouTubeStreamSecretAllowed(ctx, service, secretName, streamID); err != nil || allowed {
		return allowed, err
	}
	kinds := runtimeProfileKindsForService(service.ServiceType)
	for _, kind := range kinds {
		items, err := s.profiles.ListProfiles(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if service.ServiceType == "encoder_recorder" && kind == store.ProfileArchive && runtimeProfileConfigReferencesSecret(item.Config, secretName) {
				if !runtimeProfileKindAllowsSecret(kind, secretName) {
					continue
				}
				return s.runtimeArchiveProfileSecretAllowed(ctx, service, item.ID, streamID, archiveProfileID)
			}
			if kind != store.ProfileCaption && !runtimeProfileMatchesService(item.Config, service.ServiceID) {
				continue
			}
			if runtimeProfileConfigReferencesSecret(item.Config, secretName) {
				if !runtimeProfileKindAllowsSecret(kind, secretName) {
					continue
				}
				if service.ServiceType == "encoder_recorder" || streamScopedGenericSecretName(secretName) {
					return s.runtimeGenericStreamSecretAllowedForProfile(ctx, service, kind, item.ID, streamID)
				}
				return true, nil
			}
		}
	}
	return s.runtimeIntegrationSecretAllowedForService(ctx, service, secretName, streamID, archiveProfileID)
}

func (s *Server) runtimeYouTubeStreamSecretAllowed(ctx context.Context, service store.RegisteredService, secretName, streamID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	secretName = strings.TrimSpace(secretName)
	if streamID == "" || secretName == "" || !strings.HasPrefix(secretName, "youtube_stream_key_runtime_") {
		return false, nil
	}
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return false, nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(runtime.StreamKeySecretName) != secretName || (runtime.Mode != "live_api" && runtime.Mode != "live_api_dry_run") {
		return false, nil
	}
	if strings.TrimSpace(runtime.YouTubeOutput) == "" {
		return false, nil
	}
	if _, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, runtime.YouTubeOutput); errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
}

func (s *Server) runtimeSecretValue(ctx context.Context, secretName string) (string, error) {
	if kind, id, field, ok := parseRuntimeIntegrationSecretName(secretName); ok {
		switch kind + ":" + field {
		case "drive_destination:folder_id":
			destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(destination.FolderID) == "" {
				return "", store.ErrNotFound
			}
			return destination.FolderID, nil
		case "oauth_provider:client_secret":
			provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(provider.ClientSecret) == "" {
				return "", store.ErrNotFound
			}
			return provider.ClientSecret, nil
		case "oauth_account:refresh_token":
			account, err := s.integrations.GetOAuthAccountForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(account.RefreshToken) == "" {
				return "", store.ErrNotFound
			}
			return account.RefreshToken, nil
		default:
			return "", store.ErrUnknownSecret
		}
	}
	return s.secrets.GetSecretValue(ctx, secretName)
}

func (s *Server) runtimeIntegrationSecretAllowedForService(ctx context.Context, service store.RegisteredService, secretName, streamID, archiveProfileID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	kind, id, field, ok := parseRuntimeIntegrationSecretName(secretName)
	if !ok {
		return false, nil
	}
	if strings.TrimSpace(streamID) == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	streamArchiveProfileID := strings.TrimSpace(stream.ArchiveProfileID)
	if strings.TrimSpace(archiveProfileID) == "" {
		archiveProfileID = streamArchiveProfileID
	} else if strings.TrimSpace(archiveProfileID) != streamArchiveProfileID {
		return false, nil
	}
	if strings.TrimSpace(archiveProfileID) == "" {
		return false, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, archiveProfileID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		return false, nil
	}
	return s.runtimeIntegrationSecretAllowedForDriveDestination(ctx, destinationID, kind, id, field)
}

func (s *Server) runtimeArchiveProfileSecretAllowed(ctx context.Context, service store.RegisteredService, profileID, streamID, requestedProfileID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(requestedProfileID) != "" && strings.TrimSpace(requestedProfileID) != strings.TrimSpace(stream.ArchiveProfileID) {
		return false, nil
	}
	return stream.ArchiveProfileID == profileID, nil
}

func (s *Server) runtimeGenericStreamSecretAllowedForProfile(ctx context.Context, service store.RegisteredService, kind store.ProfileKind, profileID, streamID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch kind {
	case store.ProfileYouTubeOutput:
		return stream.YouTubeOutputID == profileID, nil
	case store.ProfileArchive:
		return stream.ArchiveProfileID == profileID, nil
	case store.ProfileEncoder:
		return stream.EncoderProfileID == profileID, nil
	case store.ProfileOverlay:
		return stream.OverlayProfileID == profileID, nil
	case store.ProfileCaption:
		return service.ServiceType == "worker" && stream.CaptionProfileID == profileID, nil
	default:
		return false, nil
	}
}

func (s *Server) servicePrimaryAssignedToStream(ctx context.Context, serviceID, streamID string) (bool, error) {
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		role := strings.TrimSpace(assignment.AssignmentRole)
		if role == "" {
			role = "primary"
		}
		if assignment.StreamID == streamID && role == "primary" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) runtimeIntegrationSecretAllowedForDriveDestination(ctx context.Context, destinationID, kind, id, field string) (bool, error) {
	destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch kind + ":" + field {
	case "drive_destination:folder_id":
		return destination.ID == id, nil
	case "oauth_account:refresh_token":
		if destination.AuthMode != "oauth2" || destination.OAuthAccountID != id {
			return false, nil
		}
		account, err := s.integrations.GetOAuthAccountForDispatch(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive), nil
	case "oauth_provider:client_secret":
		if destination.AuthMode != "oauth2" || strings.TrimSpace(destination.OAuthAccountID) == "" {
			return false, nil
		}
		account, err := s.integrations.GetOAuthAccountForDispatch(ctx, destination.OAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return account.ProviderID == id && store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive), nil
	default:
		return false, nil
	}
}

func parseRuntimeIntegrationSecretName(secretName string) (kind string, id string, field string, ok bool) {
	parts := strings.Split(strings.TrimSpace(secretName), ":")
	if len(parts) != 3 {
		return "", "", "", false
	}
	kind = strings.TrimSpace(parts[0])
	id = strings.TrimSpace(parts[1])
	field = strings.TrimSpace(parts[2])
	if kind == "" || id == "" || field == "" {
		return "", "", "", false
	}
	switch kind + ":" + field {
	case "drive_destination:folder_id", "oauth_provider:client_secret", "oauth_account:refresh_token":
		return kind, id, field, true
	default:
		return "", "", "", false
	}
}

func runtimeProfileConfigReferencesSecret(config map[string]any, secretName string) bool {
	for key, value := range config {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasSuffix(normalizedKey, "_secret_name") && !strings.HasSuffix(normalizedKey, "_secret_ref") && !strings.HasSuffix(normalizedKey, "_secret_id") {
			continue
		}
		if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == secretName {
			return true
		}
	}
	return false
}

func runtimeProfileKindAllowsSecret(kind store.ProfileKind, secretName string) bool {
	secretName = strings.TrimSpace(secretName)
	if secretName == "" {
		return false
	}
	if integrationKind, _, field, ok := parseRuntimeIntegrationSecretName(secretName); ok {
		return kind == store.ProfileArchive && ((integrationKind == "drive_destination" && field == "folder_id") ||
			(integrationKind == "oauth_provider" && field == "client_secret") ||
			(integrationKind == "oauth_account" && field == "refresh_token"))
	}
	switch kind {
	case store.ProfileDiscordConfig:
		return secretName == "discord_bot_token" || strings.HasPrefix(secretName, "discord_bot_token_")
	case store.ProfileYouTubeOutput:
		return secretName == "youtube_stream_key" || strings.HasPrefix(secretName, "youtube_stream_key_")
	case store.ProfileArchive:
		return secretName == "google_drive_folder_id" ||
			strings.HasPrefix(secretName, "google_drive_folder_id_") ||
			strings.HasPrefix(secretName, "google_oauth_refresh_token_")
	case store.ProfileEncoder:
		return strings.HasPrefix(secretName, "encoder_runtime_secret_")
	case store.ProfileCaption:
		return secretName == "deepgram_api_key" || strings.HasPrefix(secretName, "deepgram_api_key_")
	default:
		return false
	}
}

func streamScopedGenericSecretName(secretName string) bool {
	secretName = strings.TrimSpace(secretName)
	for _, prefix := range []string{
		"youtube_stream_key_",
		"google_oauth_refresh_token_",
		"google_drive_folder_id_",
		"deepgram_api_key_",
	} {
		if strings.HasPrefix(secretName, prefix) {
			return true
		}
	}
	switch secretName {
	case "youtube_stream_key", "google_drive_folder_id", "deepgram_api_key":
		return true
	default:
		return false
	}
}

func runtimeProfileKindsForService(serviceType string) []store.ProfileKind {
	switch serviceType {
	case "discord_bot":
		return []store.ProfileKind{store.ProfileDiscordConfig, store.ProfileCaption}
	case "encoder_recorder":
		return []store.ProfileKind{store.ProfileEncoder, store.ProfileArchive, store.ProfileYouTubeOutput, store.ProfileOverlay}
	case "worker":
		return []store.ProfileKind{store.ProfileOverlay, store.ProfileCaption}
	default:
		return nil
	}
}

func runtimeProfileMatchesService(config map[string]any, serviceID string) bool {
	if value, ok := config["service_id"].(string); ok {
		return strings.TrimSpace(value) == serviceID
	}
	if values, ok := config["service_ids"].([]any); ok {
		for _, value := range values {
			if item, ok := value.(string); ok && strings.TrimSpace(item) == serviceID {
				return true
			}
		}
	}
	return false
}

func sanitizeRuntimeProfileConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" || runtimeSecretLikeKey(trimmedKey) {
			continue
		}
		out[trimmedKey] = sanitizeRuntimeProfileValue(value)
	}
	return out
}

func sanitizeRuntimeProfileConfigForKind(kind store.ProfileKind, config map[string]any) map[string]any {
	if kind == store.ProfileCaption {
		config = normalizeProfileConfig(kind, config)
	}
	out := sanitizeRuntimeProfileConfig(config)
	if kind == store.ProfileDiscordConfig {
		delete(out, "guild_id")
		delete(out, "voice_channel_id")
		delete(out, "text_channel_id")
	}
	if kind == store.ProfileYouTubeOutput {
		mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(out, "mode"), configString(out, "output_mode")))
		if mode == "live_api_relay_static" && !validYouTubeRelayStaticBindingID(configString(out, "relay_binding_id")) {
			delete(out, "relay_binding_id")
		}
	}
	return out
}

func sanitizeRuntimeProfileValue(value any) any {
	switch typed := value.(type) {
	case string:
		if runtimeSecretLikeValue(typed) {
			return "<redacted>"
		}
		return typed
	case bool, float64, int, int64, uint64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeRuntimeProfileValue(item))
		}
		return out
	case map[string]any:
		return sanitizeRuntimeProfileConfig(typed)
	default:
		return nil
	}
}

func runtimeSecretLikeKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, suffix := range []string{"_secret_name", "_secret_ref", "_secret_id", "_secret_status", "_configured", "_fingerprint"} {
		if strings.HasSuffix(normalized, suffix) {
			return false
		}
	}
	for _, token := range []string{"password", "passwd", "token", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "client_secret", "refresh_token", "access_token", "authorization", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func runtimeSecretLikeValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, pattern := range []string{"bearer ", "authorization:", "password=", "token=", "access_token=", "refresh_token=", "discord.com/api/webhooks/", "hooks.slack.com/services/", "-----begin private key-----"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return strings.Contains(lower, "://") && strings.Contains(lower, "@")
}
