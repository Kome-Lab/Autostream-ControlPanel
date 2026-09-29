package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

// runtimeSelectedYouTubeOutputSecretAllowed handles ordinary HTTP-created
// Outputs, which have no service binding. The authenticated service still needs
// primary ownership of the requested stream and its currently selected Output.
func (s *Server) runtimeSelectedYouTubeOutputSecretAllowed(ctx context.Context, service store.RegisteredService, secretName, streamID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	secretName = strings.TrimSpace(secretName)
	if streamID == "" || !runtimeProfileKindAllowsSecret(store.ProfileYouTubeOutput, secretName) || strings.HasPrefix(secretName, "youtube_stream_key_runtime_") {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(stream.YouTubeOutputID) == "" {
		return false, nil
	}
	output, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, stream.YouTubeOutputID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Dynamic LiveAPI secrets retain their preceding runtime-owned path. Static
	// relay Outputs must not gain a secret delivery path from this fallback.
	if normalizedYouTubeOutputMode(firstNonEmpty(configString(output.Config, "mode"), configString(output.Config, "output_mode"))) != "stream_key" {
		return false, nil
	}
	if strings.TrimSpace(configString(output.Config, "stream_key_secret_name")) != secretName {
		return false, nil
	}
	_, singleBinding := output.Config["service_id"]
	_, multipleBindings := output.Config["service_ids"]
	if (singleBinding || multipleBindings) && !runtimeProfileMatchesService(output.Config, service.ServiceID) {
		return false, nil
	}
	return s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
}
