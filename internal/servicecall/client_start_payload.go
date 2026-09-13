package servicecall

import (
	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/store"
	"net/url"
	"strings"
	"time"
)

func (c Client) startPayload(stream store.Stream, service store.RegisteredService, req StartRequest, encoderURL string, workerService store.RegisteredService, now time.Time) (string, any, bool) {
	switch service.ServiceType {
	case "encoder_recorder":
		payload := map[string]any{
			"stream_id":             stream.ID,
			"name":                  stream.Name,
			"input_url":             req.EncoderInputURL,
			"rtmp_url":              req.EncoderRTMPURL,
			"encoder_profile_id":    req.EncoderProfileID,
			"overlay_profile_id":    req.OverlayProfileID,
			"encoder_audio_gain_db": req.EncoderAudioGainDB,
			"archive_profile_id":    req.ArchiveProfileID,
		}
		if req.EncoderStreamKeySecretName != "" {
			payload["stream_key_secret_name"] = req.EncoderStreamKeySecretName
		}
		if len(req.YouTubeRuntime) > 0 {
			payload["youtube_runtime"] = req.YouTubeRuntime
		}
		if req.VideoCoverStart != nil && reportedCapabilityTrue(service.ReportedCapabilities, CapabilityLiveVideoCoverV1) {
			payload["video_cover_start"] = req.VideoCoverStart
		}
		if strings.TrimSpace(req.ArchiveRunID) != "" && !req.ArchiveStartedAt.IsZero() {
			payload["archive_run_id"] = req.ArchiveRunID
			payload["started_at"] = req.ArchiveStartedAt.UTC()
		}
		return "/streams/start", payload, true
	case "discord_bot":
		payload := map[string]any{
			"stream_id":         stream.ID,
			"job_generation":    req.WorkerJobGeneration,
			"encoder_audio_url": encoderURL,
			"schema_version":    2,
			"discord_target": DiscordTargetSnapshot{
				Revision: req.DiscordTargetRevision,
				Resolved: ResolvedDiscordTarget{
					GuildID: req.DiscordGuildID, TextChannelID: req.DiscordTextChannelID, VoiceChannelID: req.DiscordVoiceChannelID,
				},
			},
		}
		if token := c.issueIngestToken(stream.ID, service, "discord_audio", now); token != "" {
			payload["stream_ingest_token"] = token
		}
		if strings.TrimSpace(workerService.PublicURL) != "" {
			payload["worker_events_url"] = workerService.PublicURL
			if token := c.issueIngestTokenForAudience(stream.ID, service, "worker_events", "worker", now); token != "" {
				payload["worker_events_token"] = token
			}
			if strings.TrimSpace(req.CaptionProfileID) != "" {
				payload["caption_audio_url"] = workerService.PublicURL
				payload["caption_audio_flush_ms"] = req.CaptionAudioFlushMS
				payload["caption_audio_max_batch_packets"] = req.CaptionAudioMaxBatchPackets
				payload["unresolved_ssrc_buffer_ms"] = req.UnresolvedSSRCBufferMS
				if token := c.issueIngestTokenForAudience(stream.ID, service, "caption_audio", "worker", now); token != "" {
					payload["caption_audio_token"] = token
				}
			}
		}
		return "/jobs/start", payload, true
	case "worker":
		payload := map[string]any{
			"stream_id":            stream.ID,
			"stream_name":          stream.Name,
			"encoder_recorder_url": encoderURL,
			"overlay_profile_id":   req.OverlayProfileID,
			"caption_profile_id":   req.CaptionProfileID,
		}
		if token := c.issueIngestToken(stream.ID, service, "worker_events", now); token != "" {
			payload["stream_ingest_token"] = token
		}
		if req.SceneAppearance != nil && reportedCapabilityTrue(service.ReportedCapabilities, CapabilitySceneAppearanceV1) {
			payload["scene_appearance"] = req.SceneAppearance
		}
		return "/jobs/start", payload, true
	default:
		return "", nil, false
	}
}

func (c Client) issueIngestToken(streamID string, service store.RegisteredService, purpose string, now time.Time) string {
	return c.issueIngestTokenForAudience(streamID, service, purpose, "encoder_recorder", now)
}

func (c Client) issueIngestTokenForAudience(streamID string, service store.RegisteredService, purpose, audience string, now time.Time) string {
	if strings.TrimSpace(c.Config.IngestTokenSigningKey) == "" || strings.TrimSpace(streamID) == "" {
		return ""
	}
	ttl := c.Config.IngestTokenTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	token, err := ingesttoken.Issue(c.Config.IngestTokenSigningKey, ingesttoken.Claims{
		StreamID:    streamID,
		ServiceID:   service.ServiceID,
		ServiceType: service.ServiceType,
		Purpose:     purpose,
		Audience:    audience,
		ExpiresAt:   ingesttoken.Expiry(now, ttl),
	})
	if err != nil {
		return ""
	}
	return token
}

func stopPayload(stream store.Stream, service store.RegisteredService) (string, any, bool) {
	switch service.ServiceType {
	case "encoder_recorder":
		return "/streams/" + url.PathEscape(stream.ID) + "/stop", map[string]any{}, true
	case "discord_bot", "worker":
		return "/jobs/" + url.PathEscape(stream.ID) + "/stop", map[string]any{}, true
	default:
		return "", nil, false
	}
}

func archiveArtifactEndpoint(streamID, archiveRunID, name string) string {
	return "/streams/" + url.PathEscape(streamID) + "/archive-runs/" + url.PathEscape(archiveRunID) + "/artifacts/" + url.PathEscape(name)
}

func workerEventPayload(stream store.Stream, req WorkerEventRequest) (string, any, bool) {
	base := "/streams/" + url.PathEscape(stream.ID) + "/events/"
	switch strings.TrimSpace(req.EventType) {
	case "current_time":
		return base + "current-time", map[string]any{}, true
	case "caption":
		return base + "caption", map[string]any{"text": req.Text, "speaker_user_id": req.SpeakerUserID}, true
	case "participants":
		return base + "participants", map[string]any{"participants": req.Participants}, true
	case "active_speaker":
		return base + "active-speaker", map[string]any{"user_id": req.UserID, "display_name": req.DisplayName}, true
	case "overlay":
		return base + "overlay", map[string]any{"type": req.OverlayType, "payload": req.Payload}, true
	default:
		return "", nil, false
	}
}
