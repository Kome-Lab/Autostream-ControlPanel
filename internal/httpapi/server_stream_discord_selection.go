package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

// The authentication profile names one registered Bot. Destination presets
// contain only channel IDs and cannot authorize a Gateway or an assignment.
func (s *Server) selectedDiscordServiceID(ctx context.Context, streamID, profileID string) (string, string, int) {
	if s.profiles == nil || s.services == nil {
		return "", "service_registry_not_configured", http.StatusServiceUnavailable
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, strings.TrimSpace(profileID))
	if errors.Is(err, store.ErrNotFound) {
		return "", "discord_config_not_found", http.StatusBadRequest
	}
	if err != nil {
		return "", "get_discord_config_failed", http.StatusInternalServerError
	}
	serviceID := strings.TrimSpace(configString(profile.Config, "service_id"))
	if serviceID == "" {
		return "", "discord_config_service_mismatch", http.StatusConflict
	}
	service, err := s.services.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) || err == nil && service.ServiceType != "discord_bot" {
		return "", "discord_config_service_mismatch", http.StatusConflict
	}
	if err != nil {
		return "", "get_service_failed", http.StatusInternalServerError
	}
	// Saving a profile selects this Bot; only a start claim may acquire it.
	return serviceID, "", 0
}

func (s *Server) prepareStreamDiscordAssignment(ctx context.Context, streamID, profileID string, body *streamSettingsRequest, permissions []string) (string, int) {
	desired := ""
	if strings.TrimSpace(profileID) != "" {
		var code string
		var status int
		desired, code, status = s.selectedDiscordServiceID(ctx, streamID, profileID)
		if code != "" {
			return code, status
		}
	}
	body.expectedDiscordServiceID = &desired
	if s.services == nil {
		return "service_registry_not_configured", http.StatusServiceUnavailable
	}
	selections, ok := s.streams.(store.StreamNodeSelectionStore)
	if !ok {
		return "stream_node_selection_unavailable", http.StatusServiceUnavailable
	}
	selected, err := selections.GetStreamNodeSelections(ctx, streamID)
	if err != nil {
		return "get_stream_failed", http.StatusInternalServerError
	}
	if desired == selected.DiscordServiceID {
		return "", 0
	}
	if !security.HasPermission(permissions, "services.assign") {
		return "permission_denied", http.StatusForbidden
	}
	body.discordServiceID = &desired
	return "", 0
}
