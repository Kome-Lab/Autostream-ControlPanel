package servicecall

import (
	"context"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxPreviewPlaylistBytes     = 1 << 20
	maxPreviewSegmentBytes      = 32 << 20
	maxWorkerStartResponseBytes = 1 << 20
	encoderStopTimeout          = 15 * time.Second
	archiveTransferTimeout      = 6 * time.Hour
)

type Config struct {
	Timeout               time.Duration
	URLPolicy             netpolicy.ServiceURLPolicy
	IngestTokenSigningKey string
	IngestTokenTTL        time.Duration
	NodeTokenKey          string
}

type Client struct {
	Config               Config
	HTTP                 *http.Client
	RuntimeTokenResolver func(store.RegisteredService) (string, error)
}

type StartRequest struct {
	DiscordConfigID            string `json:"discord_config_id,omitempty"`
	DiscordGuildID             string `json:"-"`
	DiscordVoiceChannelID      string `json:"-"`
	DiscordTextChannelID       string `json:"-"`
	EncoderInputURL            string `json:"encoder_input_url,omitempty"`
	EncoderRTMPURL             string `json:"encoder_rtmp_url,omitempty"`
	EncoderStreamKeySecretName string `json:"-"`
	EncoderProfileID           string `json:"encoder_profile_id,omitempty"`
	// EncoderVideoWidth/Height/FPS are resolved from EncoderProfileID by the
	// Control Panel. They are internal dispatch inputs, never operator-supplied
	// fields, and let a negotiated Worker scene match the selected Encoder
	// output before the final Encoder pass.
	EncoderVideoWidth           int                      `json:"-"`
	EncoderVideoHeight          int                      `json:"-"`
	EncoderVideoFPS             int                      `json:"-"`
	CaptionProfileID            string                   `json:"caption_profile_id,omitempty"`
	WorkerJobGeneration         uint64                   `json:"-"`
	CaptionAudioFlushMS         int                      `json:"-"`
	CaptionAudioMaxBatchPackets int                      `json:"-"`
	UnresolvedSSRCBufferMS      int                      `json:"-"`
	DiscordTargetRevision       uint64                   `json:"-"`
	SceneAppearance             *SceneAppearance         `json:"-"`
	VideoCoverStart             *VideoCoverStartSnapshot `json:"-"`
	OverlayProfileID            string                   `json:"overlay_profile_id,omitempty"`
	EncoderAudioGainDB          float64                  `json:"encoder_audio_gain_db,omitempty"`
	ArchiveProfileID            string                   `json:"archive_profile_id,omitempty"`
	ArchiveRunID                string                   `json:"-"`
	ArchiveStartedAt            time.Time                `json:"-"`
	YouTubeOutputID             string                   `json:"youtube_output_id,omitempty"`
	YouTubeRuntime              map[string]any           `json:"-"`
	ArchiveConfig               map[string]any           `json:"-"`
}

func (c Client) UpdateEncoderRuntimeSettings(ctx context.Context, stream store.Stream, services []store.RegisteredService, audioGainDB float64, overlayProfileID string) DispatchResult {
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" {
			continue
		}
		if enabled, _ := service.Capabilities["live_encoder_runtime_settings"].(bool); !enabled {
			return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/streams/" + url.PathEscape(stream.ID) + "/runtime-settings", Code: "encoder_runtime_settings_not_supported", FailurePhase: "pre_dispatch", Error: "assigned Encoder does not support live runtime settings"}
		}
		return c.serviceJSONAction(ctx, service, http.MethodPut, "/streams/"+url.PathEscape(stream.ID)+"/runtime-settings", map[string]any{
			"encoder_audio_gain_db": audioGainDB,
			"overlay_profile_id":    strings.TrimSpace(overlayProfileID),
		})
	}
	return DispatchResult{ServiceType: "encoder_recorder", Code: "assigned_encoder_not_found", FailurePhase: "pre_dispatch", Error: "assigned Encoder service not found"}
}

func (c Client) UpdateWorkerCaptionRuntimeSettings(ctx context.Context, stream store.Stream, services []store.RegisteredService, captionProfileID string) DispatchResult {
	endpoint := "/jobs/" + url.PathEscape(strings.TrimSpace(stream.ID)) + "/caption-runtime-settings"
	captionProfileID = strings.TrimSpace(captionProfileID)
	if captionProfileID == "" {
		return DispatchResult{ServiceType: "worker", Endpoint: endpoint, Code: "caption_profile_id_required", FailurePhase: "pre_dispatch", Error: "caption profile is required for a live runtime refresh"}
	}
	for _, service := range services {
		if service.ServiceType != "worker" {
			continue
		}
		if enabled, _ := service.Capabilities["live_caption_runtime_settings"].(bool); !enabled {
			return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint, Code: "worker_caption_runtime_settings_not_supported", FailurePhase: "pre_dispatch", Error: "assigned Worker does not support live caption runtime settings"}
		}
		return c.serviceJSONAction(ctx, service, http.MethodPut, endpoint, map[string]any{
			"caption_profile_id": captionProfileID,
		})
	}
	return DispatchResult{ServiceType: "worker", Endpoint: endpoint, Code: "assigned_worker_not_found", FailurePhase: "pre_dispatch", Error: "assigned Worker service not found"}
}

type WorkerEventRequest struct {
	EventType     string              `json:"event_type"`
	Text          string              `json:"text,omitempty"`
	SpeakerUserID string              `json:"speaker_user_id,omitempty"`
	Participants  []WorkerParticipant `json:"participants,omitempty"`
	UserID        string              `json:"user_id,omitempty"`
	DisplayName   string              `json:"display_name,omitempty"`
	OverlayType   string              `json:"overlay_type,omitempty"`
	Payload       map[string]any      `json:"payload,omitempty"`
}

type WorkerParticipant struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	IsSpeaking  bool   `json:"is_speaking,omitempty"`
	IsMuted     bool   `json:"is_muted,omitempty"`
}

type DispatchResult struct {
	ServiceID    string `json:"service_id"`
	ServiceType  string `json:"service_type"`
	Endpoint     string `json:"endpoint"`
	StatusCode   int    `json:"status_code"`
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
	Code         string `json:"code,omitempty"`
	FailurePhase string `json:"failure_phase,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	// Retryable is set only when the service explicitly confirms that it did
	// not accept the notification. Transport errors and 5xx responses remain
	// ambiguous delivery outcomes at the durable outbox boundary.
	Retryable     bool   `json:"retryable,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	AlreadySent   bool   `json:"already_sent,omitempty"`
	JobGeneration uint64 `json:"-"`
	// VideoOverlayBurnInNegotiated is internal orchestration evidence. It is set
	// only after the Encoder route was accepted and the Worker accepted that
	// exact route; public/audit JSON must never infer this from advertisements.
	VideoOverlayBurnInNegotiated bool `json:"-"`
}

// workerVideoIngestRoute is deliberately not embedded in DispatchResult. Its
// credential is write-only orchestration state: it exists only between the
// Encoder acknowledgement and the following Worker start request.
type workerVideoIngestRoute struct {
	URL        string `json:"url"`
	Passphrase string `json:"passphrase"`
	Credential string `json:"credential"`
	PBKeyLen   int    `json:"pbkeylen"`
}

func (r workerVideoIngestRoute) secret() string {
	if value := strings.TrimSpace(r.Passphrase); value != "" {
		return value
	}
	return strings.TrimSpace(r.Credential)
}

type AudioStatusResult struct {
	ServiceID        string            `json:"service_id"`
	ServiceType      string            `json:"service_type"`
	Endpoint         string            `json:"endpoint"`
	StatusCode       int               `json:"status_code"`
	Success          bool              `json:"success"`
	Error            string            `json:"error,omitempty"`
	AudioBridgeState AudioBridgeStatus `json:"audio_bridge_status,omitempty"`
}

type WorkerEventsResult struct {
	ServiceID   string        `json:"service_id"`
	ServiceType string        `json:"service_type"`
	Endpoint    string        `json:"endpoint"`
	StatusCode  int           `json:"status_code"`
	Success     bool          `json:"success"`
	Error       string        `json:"error,omitempty"`
	Events      []WorkerEvent `json:"events,omitempty"`
}

type ServicePreflightCheck struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ServicePreflightResult struct {
	ServiceID   string                  `json:"service_id"`
	ServiceType string                  `json:"service_type"`
	Endpoint    string                  `json:"endpoint"`
	StatusCode  int                     `json:"status_code"`
	Success     bool                    `json:"success"`
	Error       string                  `json:"error,omitempty"`
	CheckedAt   time.Time               `json:"checked_at,omitempty"`
	Ready       bool                    `json:"ready"`
	Checks      []ServicePreflightCheck `json:"checks,omitempty"`
	Summary     map[string]any          `json:"summary,omitempty"`
}

type ArchiveArtifactDownloadResult struct {
	ServiceID    string        `json:"service_id"`
	ServiceType  string        `json:"service_type"`
	Endpoint     string        `json:"endpoint"`
	StatusCode   int           `json:"status_code"`
	Success      bool          `json:"success"`
	Error        string        `json:"error,omitempty"`
	Code         string        `json:"code,omitempty"`
	FileName     string        `json:"file_name,omitempty"`
	ContentType  string        `json:"content_type,omitempty"`
	ContentRange string        `json:"content_range,omitempty"`
	AcceptRanges string        `json:"accept_ranges,omitempty"`
	SizeBytes    int64         `json:"size_bytes,omitempty"`
	Body         io.ReadCloser `json:"-"`
}

type PreviewAssetResult struct {
	ServiceID    string `json:"service_id"`
	ServiceType  string `json:"service_type"`
	Endpoint     string `json:"endpoint"`
	StatusCode   int    `json:"status_code"`
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
	Code         string `json:"code,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	ContentRange string `json:"content_range,omitempty"`
	AcceptRanges string `json:"accept_ranges,omitempty"`
	Body         []byte `json:"-"`
}
