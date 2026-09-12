package httpapi

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

type encoderOutputRelayMode string

const (
	encoderOutputRelayModeUnknown            encoderOutputRelayMode = ""
	encoderOutputRelayModeDirect             encoderOutputRelayMode = "direct"
	encoderOutputRelayModeLiveAPIRelayStatic encoderOutputRelayMode = "live_api_relay_static"
)

// normalizedEncoderOutputRelayMode accepts only the canonical v2 capabilities.
// Missing or retired values never authorize a stream-key compatibility fallback.
func normalizedEncoderOutputRelayMode(value string) encoderOutputRelayMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "direct":
		return encoderOutputRelayModeDirect
	case "live_api_relay_static":
		return encoderOutputRelayModeLiveAPIRelayStatic
	default:
		return encoderOutputRelayModeUnknown
	}
}

func primaryEncoderOutputRelayMode(service store.RegisteredService) (encoderOutputRelayMode, bool) {
	if service.ServiceType != "encoder_recorder" || normalizeAssignmentRole(service.AssignmentRole) != "primary" {
		return encoderOutputRelayModeUnknown, false
	}
	return normalizedEncoderOutputRelayMode(capabilityString(service.Capabilities["output_relay_mode"])), true
}

func (s *Server) validateYouTubeLiveAPIOutputRelay(ctx context.Context, assignments []store.RegisteredService, req *servicecall.StartRequest) error {
	if req == nil || strings.TrimSpace(req.YouTubeOutputID) == "" {
		for _, service := range assignments {
			relayMode, primary := primaryEncoderOutputRelayMode(service)
			if !primary || relayMode == encoderOutputRelayModeDirect {
				continue
			}
			if relayMode == encoderOutputRelayModeLiveAPIRelayStatic {
				return errYouTubeRelayStaticBindingUnavailable
			}
			return errYouTubeOutputInvalidConfig
		}
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, strings.TrimSpace(req.YouTubeOutputID))
	if errors.Is(err, store.ErrNotFound) {
		return errYouTubeOutputNotFound
	}
	if err != nil {
		return err
	}
	return validateYouTubeLiveAPIOutputRelayProfile(assignments, profile)
}

func validateYouTubeLiveAPIOutputRelayProfile(assignments []store.RegisteredService, profile store.Profile) error {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		return errYouTubeOutputInvalidConfig
	}
	for _, service := range assignments {
		relayMode, primary := primaryEncoderOutputRelayMode(service)
		if !primary {
			continue
		}
		switch relayMode {
		case encoderOutputRelayModeDirect:
			if mode == "live_api_relay_static" {
				return errYouTubeRelayStaticBindingUnavailable
			}
		case encoderOutputRelayModeLiveAPIRelayStatic:
			if mode != "live_api_relay_static" {
				return errYouTubeRelayStaticBindingUnavailable
			}
			relayBindingID := configString(profile.Config, "relay_binding_id")
			if !validYouTubeRelayStaticBindingID(relayBindingID) {
				return errYouTubeOutputInvalidConfig
			}
			if capabilityString(service.Capabilities["output_relay_binding_id"]) != relayBindingID {
				return errYouTubeRelayStaticBindingUnavailable
			}
		default:
			return youtubeOutputRelayModeUnsupportedError(mode)
		}
	}
	return nil
}

func youtubeOutputRelayModeUnsupportedError(mode string) error {
	switch mode {
	case "live_api", "live_api_dry_run":
		return errYouTubeLiveAPIRequiresManagedOutputRelay
	case "live_api_relay_static":
		return errYouTubeRelayStaticBindingUnavailable
	default:
		return errYouTubeOutputInvalidConfig
	}
}

type relayStaticYouTubeOutputStartFence struct {
	OutputID string
	Profile  store.Profile
}

func (s *Server) selectedYouTubeRelayStaticOutputStartFence(ctx context.Context, outputID string) (*relayStaticYouTubeOutputStartFence, error) {
	outputID = strings.TrimSpace(outputID)
	if outputID == "" {
		return nil, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, outputID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errYouTubeOutputNotFound
	}
	if err != nil {
		return nil, err
	}
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode != "live_api_relay_static" {
		return nil, nil
	}
	return &relayStaticYouTubeOutputStartFence{OutputID: outputID, Profile: profile}, nil
}

func (s *Server) validateYouTubeRelayStaticOutputStartFence(ctx context.Context, streamID string, fence relayStaticYouTubeOutputStartFence) error {
	stream, err := s.streams.GetStream(ctx, strings.TrimSpace(streamID))
	if err != nil || strings.TrimSpace(stream.YouTubeOutputID) != strings.TrimSpace(fence.OutputID) {
		return errYouTubeRelayStaticConfigChanged
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, strings.TrimSpace(fence.OutputID))
	if err != nil || profile.ID != fence.Profile.ID || profile.YouTubeRelayBindingRevision != fence.Profile.YouTubeRelayBindingRevision {
		return errYouTubeRelayStaticConfigChanged
	}
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode != "live_api_relay_static" {
		return errYouTubeRelayStaticConfigChanged
	}
	return nil
}

// convergeRelayStaticPreDispatchFailure is the claim-fenced convergence path
// for every failure after ownership acquisition and before/around dispatch.
// A newer lifecycle, assignment identity, or archive authority always wins.
func (s *Server) convergeRelayStaticPreDispatchFailure(ctx context.Context, ownership store.StreamStartOwnershipClaim) {
	claimStore, ok := s.streams.(store.StreamStartClaimStore)
	if !ok {
		log.Printf("stream pre-dispatch convergence unavailable: stream_id=%s", ownership.StreamID)
		return
	}
	if _, transitioned, err := claimStore.TransitionClaimedStreamStart(ctx, ownership, "failed"); err != nil {
		log.Printf("stream pre-dispatch start convergence failed: stream_id=%s error_type=%T", ownership.StreamID, err)
	} else if !transitioned {
		log.Printf("stream pre-dispatch start convergence superseded: stream_id=%s", ownership.StreamID)
	}
}

func (s *Server) applyYouTubeOutput(ctx context.Context, stream store.Stream, ownership store.StreamStartOwnershipClaim, req *servicecall.StartRequest) error {
	outputID := strings.TrimSpace(req.YouTubeOutputID)
	if outputID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, outputID)
	if errors.Is(err, store.ErrNotFound) {
		return errYouTubeOutputNotFound
	}
	if err != nil {
		return err
	}
	return s.applyYouTubeOutputProfile(ctx, stream, profile, ownership, req)
}

func (s *Server) applyYouTubeOutputProfile(ctx context.Context, stream store.Stream, profile store.Profile, ownership store.StreamStartOwnershipClaim, req *servicecall.StartRequest) error {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		return errYouTubeOutputInvalidConfig
	}
	if mode != "live_api_relay_static" {
		if value := strings.TrimSpace(configString(profile.Config, "rtmp_url")); value != "" {
			req.EncoderRTMPURL = value
		}
	}
	switch mode {
	case "stream_key":
		return s.applyYouTubeStreamKeyOutput(ctx, profile, req)
	case "live_api_dry_run":
		return s.applyYouTubeLiveAPIDryRunOutput(ctx, stream, profile, req)
	case "live_api":
		return s.applyYouTubeLiveAPIOutput(ctx, stream, profile, req)
	case "live_api_relay_static":
		return s.applyYouTubeRelayStaticOutput(ctx, stream, profile, ownership, req)
	default:
		return errYouTubeOutputInvalidConfig
	}
}

func youtubeOutputRTMPURL(profile store.Profile) string {
	return strings.TrimSpace(configString(profile.Config, "rtmp_url"))
}

func (s *Server) youtubeOutputReadinessIssues(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) []servicecall.ReadinessIssue {
	if err := s.validateYouTubeOutputReadiness(ctx, stream, req); err != nil {
		return []servicecall.ReadinessIssue{{
			ServiceType: "encoder_recorder",
			Code:        youtubeOutputCode(err),
			Message:     youtubeOutputReadinessMessage(err),
		}}
	}
	return nil
}

func (s *Server) validateYouTubeOutputReadiness(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) error {
	outputID := strings.TrimSpace(req.YouTubeOutputID)
	if outputID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, outputID)
	if errors.Is(err, store.ErrNotFound) {
		return errYouTubeOutputNotFound
	}
	if err != nil {
		return err
	}
	return s.validateYouTubeOutputReadinessProfile(ctx, stream, profile, req)
}

func (s *Server) validateYouTubeOutputReadinessProfile(ctx context.Context, stream store.Stream, profile store.Profile, req *servicecall.StartRequest) error {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		return errYouTubeOutputInvalidConfig
	}
	if mode != "live_api_relay_static" {
		if value := youtubeOutputRTMPURL(profile); value != "" {
			req.EncoderRTMPURL = value
		}
	}
	switch mode {
	case "stream_key":
		return s.validateYouTubeStreamKeyReadiness(ctx, profile, req)
	case "live_api_dry_run":
		if strings.TrimSpace(req.EncoderRTMPURL) == "" {
			req.EncoderRTMPURL = "rtmps://a.rtmps.youtube.com/live2"
		}
		if !isSecureRTMPSURL(req.EncoderRTMPURL) {
			return errYouTubeOutputInvalidConfig
		}
		return nil
	case "live_api":
		return s.validateYouTubeLiveAPIReadiness(ctx, stream, profile)
	case "live_api_relay_static":
		return s.validateYouTubeRelayStaticReadiness(ctx, stream, profile)
	default:
		return errYouTubeOutputInvalidConfig
	}
}

func (s *Server) validateYouTubeStreamKeyReadiness(ctx context.Context, profile store.Profile, req *servicecall.StartRequest) error {
	rtmpURL := youtubeOutputRTMPURL(profile)
	if rtmpURL == "" {
		return errYouTubeOutputInvalidConfig
	}
	req.EncoderRTMPURL = rtmpURL
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	secretName := firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))
	if strings.TrimSpace(secretName) == "" {
		return errYouTubeOutputInvalidConfig
	}
	statuses, err := s.secrets.ListSecretStatus(ctx)
	if err != nil {
		return err
	}
	if status := secretStatusByName(statuses, secretName); !status.Configured {
		return errYouTubeOutputStreamKeyUnavailable
	}
	if strings.TrimSpace(req.DiscordTextChannelID) != "" {
		if _, ok := normalizeYouTubeWatchURL(configString(profile.Config, "watch_url")); !ok {
			return errYouTubeOutputInvalidConfig
		}
	}
	return nil
}

func (s *Server) validateYouTubeLiveAPIReadiness(ctx context.Context, stream store.Stream, profile store.Profile) error {
	_ = stream
	if s.youtubeLive == nil {
		return errYouTubeLiveAPIUnavailable
	}
	oauthAccountID := firstNonEmpty(configString(profile.Config, "oauth_account_id"), configString(profile.Config, "youtube_oauth_account_id"))
	if strings.TrimSpace(oauthAccountID) == "" {
		return errYouTubeOAuthAccountUnavailable
	}
	account, err := s.integrations.GetOAuthAccount(ctx, oauthAccountID)
	if errors.Is(err, store.ErrNotFound) || !account.RefreshTokenConfigured {
		return errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	provider, err := s.integrations.GetOAuthProvider(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientID) == "" || !provider.ClientSecretConfigured {
		return errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(provider.ProviderType, "google") || !strings.EqualFold(account.ProviderType, "google") {
		return errYouTubeOAuthAccountUnavailable
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return errYouTubeOAuthAccountUnavailable
	}
	if configBool(profile.Config, "use_configured_stream_key") {
		secretName := firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))
		if strings.TrimSpace(secretName) == "" {
			return errYouTubeOutputInvalidConfig
		}
		statuses, err := s.secrets.ListSecretStatus(ctx)
		if err != nil {
			return err
		}
		if status := secretStatusByName(statuses, secretName); !status.Configured {
			return errYouTubeOutputStreamKeyUnavailable
		}
	}
	return nil
}

func (s *Server) validateYouTubeRelayStaticReadiness(ctx context.Context, stream store.Stream, profile store.Profile) error {
	if err := s.validateYouTubeLiveAPIReadiness(ctx, stream, profile); err != nil {
		return err
	}
	if _, ok := s.youtubeLive.(ytlive.RelayStaticLiveClient); !ok {
		return errYouTubeRelayStaticUnavailable
	}
	if _, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore); !ok {
		return errYouTubeRelayBindingStoreUnavailable
	}
	if strings.TrimSpace(configString(profile.Config, "rtmp_url")) != "" ||
		strings.TrimSpace(firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))) != "" ||
		strings.TrimSpace(configString(profile.Config, "watch_url")) != "" {
		return errYouTubeOutputInvalidConfig
	}
	if !validYouTubeRelayStaticBindingID(configString(profile.Config, "relay_binding_id")) ||
		!validYouTubeRelayStaticExternalID(strings.TrimSpace(configString(profile.Config, "reusable_live_stream_id")), 255) {
		return errYouTubeOutputInvalidConfig
	}
	return nil
}

func youtubeOutputReadinessMessage(err error) string {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound):
		return "selected YouTube output was not found."
	case errors.Is(err, errYouTubeOutputInvalidConfig):
		return "selected YouTube output is missing required RTMPS or mode settings."
	case errors.Is(err, errYouTubeOutputVideoFormatMismatch):
		return "selected YouTube output resolution or frame rate does not match the Encoder profile."
	case errors.Is(err, errYouTubeOutputStreamKeyUnavailable):
		return "selected YouTube output stream key is not configured."
	case errors.Is(err, errYouTubeLiveAPIUnavailable):
		return "YouTube Live API client is not available on the Control Panel."
	case errors.Is(err, errYouTubeOAuthAccountUnavailable):
		return "selected YouTube OAuth connected account is not ready."
	case errors.Is(err, errYouTubeLiveAPIRequiresManagedOutputRelay):
		return "The primary Encoder is not advertising direct YouTube output. A fixed Relay is not required: set AUTOSTREAM_OUTPUT_RELAY_MODE=direct, leave AUTOSTREAM_OUTPUT_RELAY_URL empty, disable AUTOSTREAM_REQUIRE_OUTPUT_RELAY, then restart the Encoder so its capability refreshes."
	case errors.Is(err, errYouTubeRelayStaticUnavailable):
		return "the Control Panel does not support the selected fixed relay YouTube output."
	case errors.Is(err, errYouTubeRelayStaticBindingUnavailable):
		return "the primary Encoder does not have the selected fixed relay binding."
	case errors.Is(err, errYouTubeRelayBindingInUse):
		return "the selected fixed relay binding is already reserved by another stream."
	case errors.Is(err, errYouTubeRelayStaticConfigChanged):
		return "the selected fixed relay output changed while starting; reload the stream settings and try again."
	case errors.Is(err, errYouTubeRelayStaticRecoveryRequired):
		return "the fixed relay YouTube binding requires recovery before it can be reused."
	default:
		return "selected YouTube output could not be validated."
	}
}

func youtubeLiveAPITitle(config map[string]any, streamName string) string {
	name := strings.TrimSpace(streamName)
	if name == "" {
		name = "AutoStream Broadcast"
	}
	template := strings.TrimSpace(configString(config, "broadcast_title_template"))
	if template == "" {
		template = strings.TrimSpace(configString(config, "broadcast_title"))
	}
	if template == "" {
		return name
	}
	title := strings.NewReplacer(
		"{{program_title}}", name,
		"{{stream_name}}", name,
	).Replace(template)
	title = strings.TrimSpace(title)
	// Do not create a public broadcast with an accidentally unexpanded
	// placeholder. Unknown template variables fall back to the stream name.
	if title == "" || strings.Contains(title, "{{") || strings.Contains(title, "}}") {
		return name
	}
	return title
}

func youtubeLiveAPIScheduledStart(stream store.Stream, config map[string]any) time.Time {
	if stream.ScheduledStartAt != nil {
		return stream.ScheduledStartAt.UTC()
	}
	return configTime(config, "scheduled_start_at")
}

func youtubeRuntimeScheduledStart(runtime map[string]any) (time.Time, bool) {
	value := strings.TrimSpace(mapString(runtime, "scheduled_start_at"))
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func (s *Server) applyYouTubeLiveAPIOutput(ctx context.Context, stream store.Stream, profile store.Profile, req *servicecall.StartRequest) error {
	if s.youtubeLive == nil {
		return errYouTubeLiveAPIUnavailable
	}
	resolution, frameRate, err := s.validatedYouTubeEncoderVideoFormat(ctx, profile, req)
	if err != nil {
		return err
	}
	oauthAccountID := strings.TrimSpace(configString(profile.Config, "oauth_account_id"))
	if oauthAccountID == "" {
		oauthAccountID = strings.TrimSpace(configString(profile.Config, "youtube_oauth_account_id"))
	}
	if oauthAccountID == "" {
		return errYouTubeOAuthAccountUnavailable
	}
	credentials, err := s.youtubeOAuthCredentials(ctx, oauthAccountID)
	if err != nil {
		return err
	}
	preferredStreamKey := ""
	ingestSelection := "fresh_stream"
	if configBool(profile.Config, "use_configured_stream_key") {
		preferredStreamKey, err = s.youtubeOutputConfiguredStreamKey(ctx, profile)
		if err != nil {
			return err
		}
		ingestSelection = "configured_reusable_stream"
	}
	scheduledStart := youtubeLiveAPIScheduledStart(stream, profile.Config)
	prepared, err := s.youtubeLive.Prepare(ctx, ytlive.PrepareRequest{
		Credentials:    credentials,
		StreamID:       stream.ID,
		StreamName:     stream.Name,
		OutputID:       profile.ID,
		Title:          youtubeLiveAPITitle(profile.Config, stream.Name),
		Description:    configString(profile.Config, "broadcast_description"),
		PrivacyStatus:  defaultConfigString(profile.Config, "privacy_status", "private"),
		ScheduledStart: youtubeLiveAPIScheduledStart(stream, profile.Config),
		// Bind each fresh LiveStream to the resolved Encoder profile. Provider
		// auto-detection can classify a valid 16:9 ingest as a square CDN canvas;
		// the explicit supported format keeps provider metadata and coded output
		// on the same contract.
		Resolution:         resolution,
		FrameRate:          frameRate,
		EnableAutoStart:    youtubeOutputAutoStartEnabled(profile.Config),
		EnableAutoStop:     configBool(profile.Config, "enable_auto_stop"),
		PreferredStreamKey: preferredStreamKey,
		// Keep provider ingest format state scoped to this broadcast. Reusing an
		// account-wide LiveStream can preserve stale dimensions (for example a
		// prior 4K or square ingest) even after the Encoder returns to 1080p.
		ReuseAccountStream: false,
	})
	if err != nil {
		return newYouTubeLiveAPIPrepareError("provider_prepare", err)
	}
	if !isSecureRTMPSURL(prepared.RTMPURL) || strings.TrimSpace(prepared.StreamKey) == "" || youtubeWatchURLForBroadcastID(prepared.BroadcastID) == "" {
		return newYouTubeLiveAPIPrepareError("prepared_output_validation", errYouTubeLiveAPIPrepareFailed)
	}
	streamKeySecretName := youtubeLiveAPIStreamKeySecretName(stream.ID, profile.ID, prepared.BroadcastID)
	if _, err := s.secrets.UpdateSecret(ctx, streamKeySecretName, prepared.StreamKey); err != nil {
		return newYouTubeLiveAPIPrepareError("stream_key_secret_store", err)
	}
	req.EncoderRTMPURL = prepared.RTMPURL
	req.EncoderStreamKeySecretName = streamKeySecretName
	req.YouTubeRuntime = map[string]any{
		"mode":                   "live_api",
		"output_id":              profile.ID,
		"oauth_account_id":       oauthAccountID,
		"broadcast_id":           prepared.BroadcastID,
		"watch_url":              youtubeWatchURLForBroadcastID(prepared.BroadcastID),
		"live_stream_id":         prepared.LiveStreamID,
		"rtmp_url":               prepared.RTMPURL,
		"stream_key_secret_name": streamKeySecretName,
		"ingest_selection":       ingestSelection,
		"dry_run":                false,
		"enable_auto_start":      youtubeOutputAutoStartEnabled(profile.Config),
		"complete_on_stop":       youtubeCompleteOnStop(profile.Config),
	}
	if !scheduledStart.IsZero() {
		req.YouTubeRuntime["scheduled_start_at"] = scheduledStart.UTC().Format(time.RFC3339Nano)
	}
	return nil
}

func (s *Server) applyYouTubeRelayStaticOutput(ctx context.Context, stream store.Stream, profile store.Profile, ownership store.StreamStartOwnershipClaim, req *servicecall.StartRequest) error {
	if err := s.validateYouTubeRelayStaticReadiness(ctx, stream, profile); err != nil {
		return err
	}
	resolution, frameRate, err := s.validatedYouTubeEncoderVideoFormat(ctx, profile, req)
	if err != nil {
		return err
	}
	relayStaticClient, ok := s.youtubeLive.(ytlive.RelayStaticLiveClient)
	if !ok {
		return errYouTubeRelayStaticUnavailable
	}
	claimStore, ok := s.streams.(store.StreamYouTubeRelayBindingClaimStore)
	if !ok {
		return errYouTubeRelayBindingStoreUnavailable
	}
	oauthAccountID := firstNonEmpty(configString(profile.Config, "oauth_account_id"), configString(profile.Config, "youtube_oauth_account_id"))
	credentials, err := s.youtubeOAuthCredentials(ctx, oauthAccountID)
	if err != nil {
		return err
	}
	expectedYouTubeOutputRevision := profile.YouTubeRelayBindingRevision
	claim, err := claimStore.ReserveStreamYouTubeRelayBindingClaim(ctx, store.YouTubeRelayBindingClaim{
		RelayBindingID:                configString(profile.Config, "relay_binding_id"),
		StreamID:                      stream.ID,
		YouTubeOutputID:               profile.ID,
		ExpectedYouTubeOutputRevision: &expectedYouTubeOutputRevision,
		OAuthAccountID:                oauthAccountID,
		ReusableLiveStreamID:          configString(profile.Config, "reusable_live_stream_id"),
	})
	if errors.Is(err, store.ErrYouTubeRelayBindingClaimProfileRevisionConflict) || errors.Is(err, store.ErrYouTubeRelayBindingClaimStreamOutputConflict) {
		return errYouTubeRelayStaticConfigChanged
	}
	if errors.Is(err, store.ErrYouTubeRelayBindingClaimConflict) {
		return errYouTubeRelayBindingInUse
	}
	if errors.Is(err, store.ErrInvalidYouTubeRelayBindingClaim) {
		return errYouTubeOutputInvalidConfig
	}
	if err != nil {
		return err
	}
	// Advance the durable provider-handoff fence immediately before the first
	// external PrepareRelayStatic call. Once this marker has committed, even a
	// documented validation error cannot prove that a prior process did not
	// reach YouTube; every later outcome is therefore recovered, never released.
	claim, err = s.markYouTubeRelayStaticPossiblyPrepared(ctx, claimStore, claim, ownership)
	if err != nil {
		return err
	}
	prepared, err := relayStaticClient.PrepareRelayStatic(ctx, ytlive.RelayStaticPrepareRequest{
		PrepareRequest: ytlive.PrepareRequest{
			Credentials:     credentials,
			StreamID:        stream.ID,
			StreamName:      stream.Name,
			OutputID:        profile.ID,
			Title:           youtubeLiveAPITitle(profile.Config, stream.Name),
			Description:     configString(profile.Config, "broadcast_description"),
			PrivacyStatus:   defaultConfigString(profile.Config, "privacy_status", "private"),
			ScheduledStart:  youtubeLiveAPIScheduledStart(stream, profile.Config),
			Resolution:      resolution,
			FrameRate:       frameRate,
			EnableAutoStart: youtubeOutputAutoStartEnabled(profile.Config),
			EnableAutoStop:  configBool(profile.Config, "enable_auto_stop"),
		},
		ReusableLiveStreamID: claim.ReusableLiveStreamID,
	})
	if err != nil {
		var bindErr *ytlive.RelayStaticBindError
		if errors.As(err, &bindErr) && bindErr != nil {
			s.markYouTubeRelayStaticRecovery(ctx, claimStore, claim, bindErr.BroadcastID, youtubeRelayStaticRecoveryErrorCode(err))
			return errYouTubeRelayStaticRecoveryRequired
		}
		// The provider handoff fence was already persisted. This includes known
		// pre-create validation errors: a previous process may have crossed the
		// same fence and lost its response, so releasing the reusable binding
		// would be unsafe.
		s.markYouTubeRelayStaticRecovery(ctx, claimStore, claim, "", youtubeRelayStaticRecoveryErrorCode(err))
		return errYouTubeRelayStaticRecoveryRequired
	}
	if strings.TrimSpace(prepared.RTMPURL) != "" || strings.TrimSpace(prepared.StreamKey) != "" ||
		!validYouTubeRelayStaticExternalID(strings.TrimSpace(prepared.BroadcastID), 255) ||
		strings.TrimSpace(prepared.LiveStreamID) != claim.ReusableLiveStreamID ||
		youtubeWatchURLForBroadcastID(prepared.BroadcastID) == "" {
		// A malformed successful response is also post-attempt uncertainty: the
		// provider may have created a Broadcast that cannot safely be reused.
		s.markYouTubeRelayStaticRecovery(ctx, claimStore, claim, prepared.BroadcastID, "youtube_relay_static_prepare_invalid")
		return errYouTubeRelayStaticRecoveryRequired
	}
	claim.BroadcastID = prepared.BroadcastID
	runtime := store.StreamYouTubeRuntime{
		StreamID:       stream.ID,
		YouTubeOutput:  profile.ID,
		OAuthAccountID: oauthAccountID,
		Mode:           "live_api_relay_static",
		BroadcastID:    prepared.BroadcastID,
		LiveStreamID:   claim.ReusableLiveStreamID,
		DryRun:         false,
		CompleteOnStop: true,
	}
	if err := claimStore.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, claim, runtime); err != nil {
		// A database response can be lost after the atomic finalizer committed.
		// Re-read the exact reservation and runtime before treating the error as a
		// failed start: when both durable records are present we can continue the
		// still-starting lifecycle deterministically, rather than either dispatching
		// without proof or trying to downgrade a prepared claim to recovery.
		if !s.relayStaticFinalizeCommitWasObserved(ctx, claimStore, claim, runtime) {
			s.markYouTubeRelayStaticRecovery(ctx, claimStore, claim, claim.BroadcastID, "youtube_relay_static_runtime_finalize_failed")
			return errYouTubeRelayStaticRecoveryRequired
		}
	}
	// The Encoder must keep the raw YouTube key out of its process and use the
	// configured local fixed relay. Never forward an RTMP endpoint or secret
	// reference in this mode.
	req.EncoderRTMPURL = ""
	req.EncoderStreamKeySecretName = ""
	req.YouTubeRuntime = map[string]any{
		"mode":                    "live_api_relay_static",
		"output_id":               profile.ID,
		"oauth_account_id":        oauthAccountID,
		"relay_binding_id":        claim.RelayBindingID,
		"reusable_live_stream_id": claim.ReusableLiveStreamID,
		"broadcast_id":            prepared.BroadcastID,
		"watch_url":               youtubeWatchURLForBroadcastID(prepared.BroadcastID),
		"live_stream_id":          claim.ReusableLiveStreamID,
		"dry_run":                 false,
		"complete_on_stop":        true,
	}
	return nil
}

func (s *Server) validatedYouTubeEncoderVideoFormat(ctx context.Context, profile store.Profile, req *servicecall.StartRequest) (string, string, error) {
	if req == nil {
		return "", "", errYouTubeOutputVideoFormatMismatch
	}
	if req.EncoderVideoWidth == 0 || req.EncoderVideoHeight == 0 || req.EncoderVideoFPS == 0 {
		if strings.TrimSpace(req.EncoderProfileID) == "" {
			// Streams created before Encoder profiles became mandatory have no
			// selected format to resolve. Preserve that compatibility path with a
			// fixed output hint, but never return to provider auto-detection.
			resolution := defaultConfigString(profile.Config, "resolution", "1080p")
			frameRate := defaultConfigString(profile.Config, "frame_rate", "60fps")
			if strings.EqualFold(strings.TrimSpace(resolution), "variable") {
				resolution = "1080p"
			}
			if strings.EqualFold(strings.TrimSpace(frameRate), "variable") {
				frameRate = "60fps"
			}
			return resolution, frameRate, nil
		}
		// Mixed-version assignments may not advertise Worker video capabilities,
		// so the normal start path may not have populated these internal fields.
		// Resolve the selected Encoder profile in the Panel instead of guessing a
		// provider default; an unresolved profile must fail before YouTube prepare.
		if err := s.applyEncoderVideoProfile(ctx, req); err != nil {
			return "", "", errYouTubeOutputVideoFormatMismatch
		}
	}
	if req.EncoderVideoFPS < 1 || req.EncoderVideoFPS > 60 {
		return "", "", errYouTubeOutputVideoFormatMismatch
	}
	var expectedResolution string
	switch {
	case req.EncoderVideoWidth == 1920 && req.EncoderVideoHeight == 1080:
		expectedResolution = "1080p"
	case req.EncoderVideoWidth == 1280 && req.EncoderVideoHeight == 720:
		expectedResolution = "720p"
	case req.EncoderVideoWidth == 854 && req.EncoderVideoHeight == 480:
		expectedResolution = "480p"
	default:
		return "", "", errYouTubeOutputVideoFormatMismatch
	}
	expectedFrameRate := "60fps"
	if req.EncoderVideoFPS <= 30 {
		expectedFrameRate = "30fps"
	}
	configuredResolution := strings.TrimSpace(configString(profile.Config, "resolution"))
	configuredFrameRate := strings.TrimSpace(configString(profile.Config, "frame_rate"))
	if configuredResolution != "" && !strings.EqualFold(configuredResolution, "variable") && !strings.EqualFold(configuredResolution, expectedResolution) {
		return "", "", errYouTubeOutputVideoFormatMismatch
	}
	if configuredFrameRate != "" && !strings.EqualFold(configuredFrameRate, "variable") && !strings.EqualFold(configuredFrameRate, expectedFrameRate) {
		return "", "", errYouTubeOutputVideoFormatMismatch
	}
	return expectedResolution, expectedFrameRate, nil
}

func (s *Server) relayStaticFinalizeCommitWasObserved(ctx context.Context, claimStore store.StreamYouTubeRelayBindingClaimStore, expectedClaim store.YouTubeRelayBindingClaim, expectedRuntime store.StreamYouTubeRuntime) bool {
	observedClaim, err := claimStore.GetStreamYouTubeRelayBindingClaim(ctx, expectedClaim.RelayBindingID)
	if err != nil || observedClaim.State != store.YouTubeRelayBindingClaimStatePrepared ||
		observedClaim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared ||
		observedClaim.DispatchState != store.YouTubeRelayBindingClaimDispatchStateNotDispatched ||
		observedClaim.ReservationToken != expectedClaim.ReservationToken ||
		observedClaim.StreamID != expectedClaim.StreamID ||
		observedClaim.YouTubeOutputID != expectedClaim.YouTubeOutputID ||
		observedClaim.YouTubeOutputRevision != expectedClaim.YouTubeOutputRevision ||
		observedClaim.OAuthAccountID != expectedClaim.OAuthAccountID ||
		observedClaim.ReusableLiveStreamID != expectedClaim.ReusableLiveStreamID ||
		observedClaim.BroadcastID != expectedClaim.BroadcastID {
		return false
	}
	runtimeStore, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return false
	}
	observedRuntime, err := runtimeStore.GetStreamYouTubeRuntime(ctx, expectedRuntime.StreamID)
	if err != nil || observedRuntime.Mode != "live_api_relay_static" ||
		observedRuntime.StreamID != expectedRuntime.StreamID ||
		observedRuntime.YouTubeOutput != expectedRuntime.YouTubeOutput ||
		observedRuntime.OAuthAccountID != expectedRuntime.OAuthAccountID ||
		observedRuntime.BroadcastID != expectedRuntime.BroadcastID ||
		observedRuntime.LiveStreamID != expectedRuntime.LiveStreamID ||
		!observedRuntime.CompleteOnStop {
		return false
	}
	stream, err := s.streams.GetStream(ctx, expectedRuntime.StreamID)
	return err == nil && strings.EqualFold(strings.TrimSpace(stream.Status), "starting")
}

// markYouTubeRelayStaticPossiblyPrepared is the durable YouTube Prepare
// handoff. A lost marker response must never be retried as a provider Prepare:
// when the marker is visible, recovery owns the possible external Broadcast.
// Only a provably unchanged reserved/not-attempted claim may be released,
// because the external client has not been called before that marker.
func (s *Server) markYouTubeRelayStaticPossiblyPrepared(ctx context.Context, claimStore store.StreamYouTubeRelayBindingClaimStore, claim store.YouTubeRelayBindingClaim, ownership store.StreamStartOwnershipClaim) (store.YouTubeRelayBindingClaim, error) {
	marked, err := claimStore.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, claim)
	if err == nil {
		return marked, nil
	}
	// The Prepare marker may have committed even when its response (or the
	// following read) was lost. First terminalize this local start attempt; the
	// Store reconciliation then reads the durable PrepareState under its own
	// fence and releases only a proven not-attempted reservation.
	startClaimStore, ok := s.streams.(store.StreamStartClaimStore)
	if !ok {
		return claim, errYouTubeRelayStaticRecoveryRequired
	}
	failed, transitioned, transitionErr := startClaimStore.TransitionClaimedStreamStart(ctx, ownership, "failed")
	if transitionErr != nil {
		// A status-CAS response may have been lost just like the marker response.
		// Re-read before deciding whether reconciliation is safe: only an inactive
		// stream can release a reservation that is proven not attempted.
		current, readErr := s.streams.GetStream(ctx, claim.StreamID)
		if readErr != nil || isActiveStreamStatus(current.Status) {
			log.Printf("youtube relay static prepare marker failed before inactive reconciliation: stream_id=%s marker_error_type=%T transition_error_type=%T", claim.StreamID, err, transitionErr)
			return claim, errYouTubeRelayStaticRecoveryRequired
		}
	} else if !transitioned && isActiveStreamStatus(failed.Status) {
		log.Printf("youtube relay static prepare marker failed before inactive reconciliation: stream_id=%s marker_error_type=%T transition_error_type=%T", claim.StreamID, err, transitionErr)
		return claim, errYouTubeRelayStaticRecoveryRequired
	}
	claim.BroadcastID = youtubeRelayStaticUnknownBroadcastID
	claim.LastError = "youtube_relay_static_prepare_marker_response_uncertain"
	resolution, reconcileErr := claimStore.ReconcileReservedStreamYouTubeRelayBindingClaimAfterPrepareFence(ctx, claim)
	if reconcileErr != nil {
		log.Printf("youtube relay static prepare marker reconciliation failed: stream_id=%s marker_error_type=%T reconcile_error_type=%T", claim.StreamID, err, reconcileErr)
		return claim, errYouTubeRelayStaticRecoveryRequired
	}
	if resolution.Released {
		return claim, errYouTubeLiveAPIPrepareFailed
	}
	return resolution.Claim, errYouTubeRelayStaticRecoveryRequired
}

func (s *Server) markYouTubeRelayStaticRecovery(ctx context.Context, claimStore store.StreamYouTubeRelayBindingClaimStore, claim store.YouTubeRelayBindingClaim, broadcastID, lastError string) {
	broadcastID = strings.TrimSpace(broadcastID)
	if !validYouTubeRelayStaticExternalID(broadcastID, 255) {
		broadcastID = youtubeRelayStaticUnknownBroadcastID
	}
	claim.BroadcastID = broadcastID
	claim.LastError = strings.TrimSpace(lastError)
	if _, recoveryErr := claimStore.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx, claim); recoveryErr != nil {
		log.Printf("youtube relay static recovery claim failed: stream_id=%s error_type=%T", claim.StreamID, recoveryErr)
	}
}

func youtubeRelayStaticRecoveryErrorCode(err error) string {
	switch {
	case errors.Is(err, ytlive.ErrRelayStaticBindCleanupUncertain):
		return ytlive.ErrRelayStaticBindCleanupUncertain.Error()
	case errors.Is(err, ytlive.ErrRelayStaticBindFailed):
		return ytlive.ErrRelayStaticBindFailed.Error()
	default:
		return "youtube_relay_static_prepare_uncertain"
	}
}

func (s *Server) youtubeOAuthCredentials(ctx context.Context, oauthAccountID string) (ytlive.OAuthCredentials, error) {
	account, err := s.integrations.GetOAuthAccountForDispatch(ctx, oauthAccountID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(account.RefreshToken) == "" {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return ytlive.OAuthCredentials{}, err
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(provider.ClientSecret) == "" || strings.TrimSpace(provider.ClientID) == "" || !provider.Enabled {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return ytlive.OAuthCredentials{}, err
	}
	if provider.ProviderType != "google" || account.ProviderType != "google" {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	return ytlive.OAuthCredentials{ClientID: provider.ClientID, ClientSecret: provider.ClientSecret, RefreshToken: account.RefreshToken}, nil
}

func (s *Server) applyYouTubeStreamKeyOutput(ctx context.Context, profile store.Profile, req *servicecall.StartRequest) error {
	rtmpURL := youtubeOutputRTMPURL(profile)
	if rtmpURL == "" {
		return errYouTubeOutputInvalidConfig
	}
	req.EncoderRTMPURL = rtmpURL
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	secretName := firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))
	if _, err := s.youtubeOutputConfiguredStreamKey(ctx, profile); err != nil {
		return err
	}
	req.EncoderStreamKeySecretName = secretName
	if watchURL, ok := normalizeYouTubeWatchURL(configString(profile.Config, "watch_url")); ok {
		req.YouTubeRuntime = map[string]any{
			"mode":             "stream_key",
			"output_id":        profile.ID,
			"watch_url":        watchURL,
			"dry_run":          false,
			"complete_on_stop": false,
		}
	}
	return nil
}

func (s *Server) youtubeOutputConfiguredStreamKey(ctx context.Context, profile store.Profile) (string, error) {
	secretName := firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))
	if strings.TrimSpace(secretName) == "" {
		return "", errYouTubeOutputInvalidConfig
	}
	streamKey, err := s.secrets.GetSecretValue(ctx, secretName)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnknownSecret) {
		return "", errYouTubeOutputStreamKeyUnavailable
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(streamKey) == "" {
		return "", errYouTubeOutputStreamKeyUnavailable
	}
	return streamKey, nil
}

func (s *Server) applyYouTubeLiveAPIDryRunOutput(ctx context.Context, stream store.Stream, profile store.Profile, req *servicecall.StartRequest) error {
	if strings.TrimSpace(req.EncoderRTMPURL) == "" {
		req.EncoderRTMPURL = "rtmps://a.rtmps.youtube.com/live2"
	}
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	seed := stream.ID + ":" + profile.ID + ":" + profile.UpdatedAt.UTC().Format(time.RFC3339Nano)
	fingerprint := security.SecretFingerprint(seed)
	streamKeySecretName := youtubeLiveAPIStreamKeySecretName(stream.ID, profile.ID, "dry-broadcast-"+fingerprint)
	if _, err := s.secrets.UpdateSecret(ctx, streamKeySecretName, "yt-dry-run-"+fingerprint); err != nil {
		return errYouTubeLiveAPIPrepareFailed
	}
	req.EncoderStreamKeySecretName = streamKeySecretName
	req.YouTubeRuntime = map[string]any{
		"mode":                   "live_api_dry_run",
		"output_id":              profile.ID,
		"broadcast_id":           "dry-broadcast-" + fingerprint,
		"live_stream_id":         "dry-live-stream-" + fingerprint,
		"rtmp_url":               req.EncoderRTMPURL,
		"stream_key_secret_name": streamKeySecretName,
		"dry_run":                true,
		"complete_on_stop":       youtubeCompleteOnStop(profile.Config),
	}
	return nil
}

func isSecureRTMPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.User == nil && parsed.Scheme == "rtmps" && parsed.Host != ""
}

func youtubeWatchURLForBroadcastID(broadcastID string) string {
	broadcastID = strings.TrimSpace(broadcastID)
	if !validYouTubeVideoID(broadcastID) {
		return ""
	}
	return "https://www.youtube.com/watch?" + url.Values{"v": []string{broadcastID}}.Encode()
}

func normalizeYouTubeWatchURL(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	videoID := ""
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if parsed.Path != "/watch" {
			return "", false
		}
		videoID = parsed.Query().Get("v")
	case "youtu.be":
		videoID = strings.Trim(parsed.Path, "/")
		if strings.Contains(videoID, "/") {
			return "", false
		}
	default:
		return "", false
	}
	if !validYouTubeVideoID(videoID) {
		return "", false
	}
	return youtubeWatchURLForBroadcastID(videoID), true
}

func validYouTubeVideoID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 6 || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
