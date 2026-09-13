package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"strings"
	"time"
)

const youtubeLiveTransitionTimeout = 20 * time.Second

const youtubeLiveTransitionAttempts = 5

func applyStreamSettingsDefaults(stream store.Stream, req *servicecall.StartRequest) {
	if strings.TrimSpace(req.DiscordConfigID) == "" {
		req.DiscordConfigID = stream.DiscordConfigID
	}
	if strings.TrimSpace(req.EncoderProfileID) == "" {
		req.EncoderProfileID = stream.EncoderProfileID
	}
	if strings.TrimSpace(req.CaptionProfileID) == "" {
		req.CaptionProfileID = stream.CaptionProfileID
	}
	if strings.TrimSpace(req.OverlayProfileID) == "" {
		req.OverlayProfileID = stream.OverlayProfileID
	}
	req.EncoderAudioGainDB = stream.EncoderAudioGainDB
	if strings.TrimSpace(req.ArchiveProfileID) == "" {
		req.ArchiveProfileID = stream.ArchiveProfileID
	}
	if strings.TrimSpace(req.YouTubeOutputID) == "" {
		req.YouTubeOutputID = stream.YouTubeOutputID
	}
	if strings.TrimSpace(req.EncoderInputURL) == "" {
		req.EncoderInputURL = stream.EncoderInputURL
	}
}

func (s *Server) applyEncoderVideoProfile(ctx context.Context, req *servicecall.StartRequest) error {
	if strings.TrimSpace(req.EncoderProfileID) == "" {
		return errEncoderProfileNotFound
	}
	if s.profiles == nil {
		return errEncoderProfileNotFound
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileEncoder, req.EncoderProfileID)
	if err != nil {
		return err
	}
	req.EncoderVideoWidth = configInt(profile.Config, "width")
	req.EncoderVideoHeight = configInt(profile.Config, "height")
	req.EncoderVideoFPS = configInt(profile.Config, "fps")
	validSize := (req.EncoderVideoWidth == 1920 && req.EncoderVideoHeight == 1080) ||
		(req.EncoderVideoWidth == 1280 && req.EncoderVideoHeight == 720) ||
		(req.EncoderVideoWidth == 854 && req.EncoderVideoHeight == 480)
	if !validSize || req.EncoderVideoFPS < 1 || req.EncoderVideoFPS > 60 {
		return errors.New("encoder profile video dimensions are invalid")
	}
	return nil
}

type streamStartMaterialization struct {
	Service     store.RegisteredService
	ActorUserID string
}
