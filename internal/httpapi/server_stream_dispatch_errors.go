package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

var (
	errYouTubeOutputNotFound                         = errors.New("youtube_output_not_found")
	errYouTubeOutputInvalidConfig                    = errors.New("youtube_output_invalid_config")
	errYouTubeOutputVideoFormatMismatch              = errors.New("youtube_output_video_format_mismatch")
	errYouTubeOutputStreamKeyUnavailable             = errors.New("youtube_stream_key_unavailable")
	errYouTubeLiveAPIUnavailable                     = errors.New("youtube_live_api_unavailable")
	errYouTubeOAuthAccountUnavailable                = errors.New("youtube_oauth_account_unavailable")
	errYouTubeLiveAPIPrepareFailed                   = errors.New("youtube_live_api_prepare_failed")
	errYouTubeLiveAPIStartFailed                     = errors.New("youtube_live_api_start_failed")
	errYouTubeLiveAPICompleteFailed                  = errors.New("youtube_live_api_complete_failed")
	errYouTubeLiveAPIRequiresManagedOutputRelay      = errors.New("live_api_requires_managed_output_relay")
	errYouTubeRelayStaticUnavailable                 = errors.New("youtube_relay_static_unavailable")
	errYouTubeRelayStaticBindingUnavailable          = errors.New("youtube_relay_static_binding_unavailable")
	errYouTubeRelayBindingStoreUnavailable           = errors.New("youtube_relay_binding_store_unavailable")
	errYouTubeRelayBindingInUse                      = errors.New("youtube_relay_binding_in_use")
	errYouTubeRelayStaticConfigChanged               = errors.New("youtube_relay_static_config_changed_reload")
	errYouTubeRelayStaticCompletionRequiresCompleted = errors.New("youtube_relay_static_completion_requires_completed_stream")
	errYouTubeRelayStaticRecoveryRequired            = errors.New("youtube_relay_static_recovery_required")
	errArchiveProfileNotFound                        = errors.New("archive_profile_not_found")
	errArchiveProfileInvalidConfig                   = errors.New("archive_profile_invalid_config")
	errDriveDestinationNotFound                      = errors.New("drive_destination_not_found")
	errDriveDestinationUnavailable                   = errors.New("drive_destination_unavailable")
	errDriveOAuthAccountUnavailable                  = errors.New("drive_oauth_account_unavailable")
	errArchiveOAuthAccountRequired                   = errors.New("archive_oauth_account_required")
	errArchiveFolderIDRequired                       = errors.New("archive_folder_id_required")
	errArchiveSharedDriveIDRequired                  = errors.New("archive_shared_drive_id_required")
	errArchiveSettingsStoreUnavailable               = errors.New("archive_settings_store_unavailable")
	errAutoStartTriggerInvalid                       = errors.New("auto_start_trigger_invalid")
	errAutoStartDiscordRequired                      = errors.New("auto_start_discord_required")
	errDiscordConfigRequired                         = errors.New("discord_config_required")
	errDiscordConfigNotFound                         = errors.New("discord_config_not_found")
	errDiscordConfigInvalid                          = errors.New("discord_config_invalid")
	errDiscordConfigServiceMismatch                  = errors.New("discord_config_service_mismatch")
	errEncoderProfileNotFound                        = errors.New("encoder_profile_not_found")
	errCaptionProfileNotFound                        = errors.New("caption_profile_not_found")
	errOverlayProfileNotFound                        = errors.New("overlay_profile_not_found")
	errEncoderInputURLBlocked                        = errors.New("encoder_input_url_blocked")
)

// youtubeLiveAPIPrepareError preserves the public error code while retaining
// only safe, structured context for audit/log diagnostics. The underlying
// provider error is never rendered directly because it may contain request
// details that are not appropriate for durable records.
type youtubeLiveAPIPrepareError struct {
	stage string
	cause error
}

func (e *youtubeLiveAPIPrepareError) Error() string {
	return errYouTubeLiveAPIPrepareFailed.Error()
}

func (e *youtubeLiveAPIPrepareError) Unwrap() error {
	return errYouTubeLiveAPIPrepareFailed
}

func newYouTubeLiveAPIPrepareError(stage string, cause error) error {
	return &youtubeLiveAPIPrepareError{stage: stage, cause: cause}
}

func youtubeLiveAPIPrepareFailureMetadata(err error) map[string]any {
	var prepareErr *youtubeLiveAPIPrepareError
	if !errors.As(err, &prepareErr) || prepareErr == nil {
		return nil
	}
	metadata := map[string]any{
		"prepare_stage": prepareErr.stage,
		"error_type":    fmt.Sprintf("%T", prepareErr.cause),
		"error_class":   youtubeLiveAPIPrepareErrorClass(prepareErr.cause),
	}
	if operation := youtubeLiveAPIPrepareTransportOperation(prepareErr.cause); operation != "" {
		metadata["transport_operation"] = operation
	}
	var apiErr *googleapi.Error
	if errors.As(prepareErr.cause, &apiErr) && apiErr != nil {
		metadata["provider_status_code"] = apiErr.Code
		if len(apiErr.Errors) > 0 {
			if reason := safeYouTubeProviderReason(apiErr.Errors[0].Reason); reason != "" {
				metadata["provider_reason"] = reason
			}
		}
	}
	var retrieveErr *oauth2.RetrieveError
	if errors.As(prepareErr.cause, &retrieveErr) && retrieveErr != nil {
		if retrieveErr.Response != nil && retrieveErr.Response.StatusCode > 0 {
			metadata["provider_status_code"] = retrieveErr.Response.StatusCode
		}
		if reason := safeYouTubeProviderReason(retrieveErr.ErrorCode); reason != "" {
			metadata["provider_reason"] = reason
		}
	}
	return metadata
}

func youtubeLiveAPIPrepareErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		return "oauth_token_exchange"
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return "provider_http"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return "timeout"
		}
		return "network"
	}
	var transportErr *url.Error
	if errors.As(err, &transportErr) {
		return "transport"
	}
	return "unknown"
}

func youtubeLiveAPIPrepareTransportOperation(err error) string {
	var transportErr *url.Error
	if !errors.As(err, &transportErr) || transportErr == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(transportErr.Op)) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return strings.ToLower(strings.TrimSpace(transportErr.Op))
	default:
		return "other"
	}
}

func safeYouTubeProviderReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return ""
	}
	return value
}

func archiveConfigStatus(err error) int {
	switch {
	case errors.Is(err, errArchiveProfileNotFound), errors.Is(err, errDriveDestinationNotFound):
		return http.StatusNotFound
	case errors.Is(err, errArchiveProfileInvalidConfig), errors.Is(err, errDriveDestinationUnavailable), errors.Is(err, errDriveOAuthAccountUnavailable), errors.Is(err, store.ErrSecretKeyRequired):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func archiveConfigCode(err error) string {
	switch {
	case errors.Is(err, errArchiveProfileNotFound), errors.Is(err, errArchiveProfileInvalidConfig), errors.Is(err, errDriveDestinationNotFound), errors.Is(err, errDriveDestinationUnavailable), errors.Is(err, errDriveOAuthAccountUnavailable):
		return err.Error()
	case errors.Is(err, store.ErrSecretKeyRequired):
		return errDriveDestinationUnavailable.Error()
	default:
		return "archive_config_resolution_failed"
	}
}

func youtubeOutputStatus(err error) int {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound):
		return http.StatusNotFound
	case errors.Is(err, errYouTubeRelayBindingStoreUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errYouTubeOutputInvalidConfig), errors.Is(err, errYouTubeOutputVideoFormatMismatch), errors.Is(err, errYouTubeOutputStreamKeyUnavailable), errors.Is(err, errYouTubeLiveAPIUnavailable), errors.Is(err, errYouTubeOAuthAccountUnavailable), errors.Is(err, errYouTubeLiveAPIPrepareFailed), errors.Is(err, errYouTubeLiveAPIRequiresManagedOutputRelay), errors.Is(err, errYouTubeRelayStaticUnavailable), errors.Is(err, errYouTubeRelayStaticBindingUnavailable), errors.Is(err, errYouTubeRelayBindingInUse), errors.Is(err, errYouTubeRelayStaticConfigChanged), errors.Is(err, errYouTubeRelayStaticRecoveryRequired), errors.Is(err, store.ErrSecretKeyRequired):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func youtubeOutputCode(err error) string {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound), errors.Is(err, errYouTubeOutputInvalidConfig), errors.Is(err, errYouTubeOutputVideoFormatMismatch), errors.Is(err, errYouTubeOutputStreamKeyUnavailable), errors.Is(err, errYouTubeLiveAPIUnavailable), errors.Is(err, errYouTubeOAuthAccountUnavailable), errors.Is(err, errYouTubeLiveAPIPrepareFailed), errors.Is(err, errYouTubeLiveAPIRequiresManagedOutputRelay), errors.Is(err, errYouTubeRelayStaticUnavailable), errors.Is(err, errYouTubeRelayStaticBindingUnavailable), errors.Is(err, errYouTubeRelayBindingStoreUnavailable), errors.Is(err, errYouTubeRelayBindingInUse), errors.Is(err, errYouTubeRelayStaticConfigChanged), errors.Is(err, errYouTubeRelayStaticRecoveryRequired):
		return err.Error()
	case errors.Is(err, store.ErrSecretKeyRequired):
		return errYouTubeOutputStreamKeyUnavailable.Error()
	default:
		return "youtube_output_resolution_failed"
	}
}

func discordConfigStatus(err error) int {
	switch {
	case errors.Is(err, errDiscordConfigNotFound):
		return http.StatusNotFound
	case errors.Is(err, errDiscordConfigRequired), errors.Is(err, errDiscordConfigInvalid), errors.Is(err, errDiscordConfigServiceMismatch):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func discordConfigCode(err error) string {
	switch {
	case errors.Is(err, errDiscordConfigRequired), errors.Is(err, errDiscordConfigNotFound), errors.Is(err, errDiscordConfigInvalid), errors.Is(err, errDiscordConfigServiceMismatch):
		return err.Error()
	default:
		return "discord_config_resolution_failed"
	}
}
