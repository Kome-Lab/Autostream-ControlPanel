package servicecall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
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

func RedactServicePreflightResult(result ServicePreflightResult) ServicePreflightResult {
	result.Error = redactPreflightString(result.Error)
	if len(result.Checks) > 0 {
		checks := make([]ServicePreflightCheck, 0, len(result.Checks))
		for _, check := range result.Checks {
			check.ID = redactPreflightString(check.ID)
			check.Status = redactPreflightString(check.Status)
			check.Severity = redactPreflightString(check.Severity)
			check.Message = redactPreflightString(check.Message)
			checks = append(checks, check)
		}
		result.Checks = checks
	}
	if result.Summary != nil {
		if redacted, ok := redactPreflightValue(result.Summary).(map[string]any); ok {
			result.Summary = redacted
		} else {
			result.Summary = nil
		}
	}
	return result
}

func RedactWorkerEventsResult(result WorkerEventsResult) WorkerEventsResult {
	result.Error = redactPreflightString(result.Error)
	for i := range result.Events {
		if result.Events[i].Payload == nil {
			continue
		}
		if redacted, ok := redactPreflightValue(result.Events[i].Payload).(map[string]any); ok {
			result.Events[i].Payload = redacted
		} else {
			result.Events[i].Payload = nil
		}
	}
	return result
}

func redactPreflightValue(value any) any {
	switch typed := value.(type) {
	case string:
		return redactPreflightString(typed)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, nil:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactPreflightValue(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if preflightSecretKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactPreflightValue(nested)
		}
		return out
	default:
		return nil
	}
}

func redactPreflightString(value string) string {
	if preflightSecretValue(value) {
		return "<redacted>"
	}
	return value
}

func preflightSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for _, token := range []string{
		"webhook_url",
		"token",
		"secret",
		"password",
		"private_key",
		"credential",
		"authorization",
		"stream_key",
		"refresh_token",
		"access_token",
		"folder_id",
		"drive_folder_id",
		"google_drive_folder_id",
		"gdrive_folder_id",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func preflightSecretValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "<redacted>") || strings.Contains(trimmed, "<WEBHOOK_PATH>") || strings.Contains(trimmed, "****") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, pattern := range []string{
		"discord.com/api/webhooks/",
		"hooks.slack.com/services/",
		"token=",
		"api_key=",
		"apikey=",
		"client_secret=",
		"stream_key=",
		"passphrase=",
		"password=",
		"secret=",
		"access_token",
		"refresh_token",
		"authorization",
		"bearer ",
		"private_key",
		"credential",
		"-----begin private key-----",
		"ast_svc_",
		"ast_ingest_v1.",
		"ya29.",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.User != nil {
		return true
	}
	return false
}

type AudioBridgeStatus struct {
	StreamID         string    `json:"stream_id"`
	BridgeActive     bool      `json:"bridge_active"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	LastPacketAt     time.Time `json:"last_packet_at,omitempty"`
	PacketsTotal     int64     `json:"packets_total"`
	RTPForwarded     int64     `json:"rtp_forwarded"`
	LastPacketAgeSec float64   `json:"last_packet_age_sec"`
}

type WorkerEvent struct {
	ID        string         `json:"id"`
	StreamID  string         `json:"stream_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

type ReadinessIssue struct {
	ServiceID   string `json:"service_id,omitempty"`
	ServiceType string `json:"service_type,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

func FromEnv() Client {
	return Client{Config: Config{
		Timeout:               envDuration("SERVICE_CALL_TIMEOUT_SEC", 5*time.Second),
		URLPolicy:             netpolicy.ServiceURLPolicyFromEnv(),
		IngestTokenSigningKey: os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"),
		IngestTokenTTL:        envMinutes("AUTOSTREAM_STREAM_INGEST_TOKEN_TTL_MIN", 12*time.Hour),
		NodeTokenKey:          os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"),
	}}
}

func (c Client) Enabled() bool {
	return c.RuntimeTokenResolver != nil || strings.TrimSpace(c.Config.NodeTokenKey) != ""
}

func (c Client) StartReadinessIssues(services []store.RegisteredService, req StartRequest, now time.Time) []ReadinessIssue {
	var issues []ReadinessIssue
	if !c.Enabled() {
		issues = append(issues, ReadinessIssue{
			Code:    "node_runtime_token_key_missing",
			Message: "The node runtime token encryption key is not configured on the Control Panel.",
		})
	}
	if strings.TrimSpace(c.Config.IngestTokenSigningKey) == "" {
		issues = append(issues, ReadinessIssue{
			Code:    "stream_ingest_signing_key_missing",
			Message: "AUTOSTREAM_STREAM_INGEST_SIGNING_KEY is not configured on the Control Panel.",
		})
	}
	encoderURL := firstServiceURL(services, "encoder_recorder")
	workerService := firstService(services, "worker")
	if issue, mismatch := workerVideoCapabilityMismatch(services); mismatch {
		issues = append(issues, issue)
	}
	for _, service := range services {
		if _, _, ok := c.startPayload(store.Stream{}, service, req, encoderURL, workerService, now); !ok {
			continue
		}
		if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
			issues = append(issues, ReadinessIssue{
				ServiceID:   service.ServiceID,
				ServiceType: service.ServiceType,
				Code:        serviceURLIssueCode(err),
				Message:     serviceURLMessage(err),
			})
		}
		if _, err := c.authToken(service); err != nil {
			issues = append(issues, ReadinessIssue{
				ServiceID:   service.ServiceID,
				ServiceType: service.ServiceType,
				Code:        "node_runtime_token_missing",
				Message:     "node runtime token is not available for dispatch.",
			})
		}
		if service.Status == "offline" {
			issues = append(issues, ReadinessIssue{
				ServiceID:   service.ServiceID,
				ServiceType: service.ServiceType,
				Code:        "service_offline",
				Message:     "assigned service is offline.",
			})
		}
		if service.LastHeartbeatAt != nil && now.Sub(*service.LastHeartbeatAt) > 90*time.Second {
			issues = append(issues, ReadinessIssue{
				ServiceID:   service.ServiceID,
				ServiceType: service.ServiceType,
				Code:        "service_heartbeat_stale",
				Message:     "assigned service heartbeat is stale.",
			})
		}
	}
	if req.EncoderInputURL == "" {
		for _, service := range services {
			if service.ServiceType != "discord_bot" {
				continue
			}
			if enabled, ok := capabilityBool(service.Capabilities, "audio_stream_forward"); ok && !enabled {
				issues = append(issues, ReadinessIssue{
					ServiceID:   service.ServiceID,
					ServiceType: service.ServiceType,
					Code:        "discord_audio_forward_unavailable",
					Message:     "discord_bot reports audio_stream_forward=false while encoder_input_url is blank.",
				})
			}
			if enabled, ok := capabilityBool(service.Capabilities, "audio_capture"); ok && !enabled {
				issues = append(issues, ReadinessIssue{
					ServiceID:   service.ServiceID,
					ServiceType: service.ServiceType,
					Code:        "discord_audio_capture_unavailable",
					Message:     "discord_bot reports audio_capture=false while encoder_input_url is blank.",
				})
			}
			break
		}
	}
	if strings.TrimSpace(req.CaptionProfileID) != "" {
		for _, service := range services {
			switch service.ServiceType {
			case "discord_bot":
				if enabled, ok := capabilityBool(service.Capabilities, "caption_audio_forward"); ok && !enabled {
					issues = append(issues, ReadinessIssue{
						ServiceID: service.ServiceID, ServiceType: service.ServiceType,
						Code: "discord_caption_audio_forward_unavailable", Message: "discord_bot reports caption_audio_forward=false while a caption profile is selected.",
					})
				}
			case "worker":
				if enabled, ok := capabilityBool(service.Capabilities, "deepgram_transcription"); ok && !enabled {
					issues = append(issues, ReadinessIssue{
						ServiceID: service.ServiceID, ServiceType: service.ServiceType,
						Code: "worker_deepgram_transcription_unavailable", Message: "worker reports deepgram_transcription=false while a caption profile is selected.",
					})
				}
			}
		}
	}
	if encoderURL == "" {
		issues = append(issues, ReadinessIssue{
			ServiceType: "encoder_recorder",
			Code:        "encoder_public_url_missing",
			Message:     "encoder_recorder public_url is required for Discord Bot and Worker dispatch.",
		})
	} else if err := c.Config.URLPolicy.ValidateURL(encoderURL); err != nil {
		issues = append(issues, ReadinessIssue{
			ServiceType: "encoder_recorder",
			Code:        encoderURLIssueCode(err),
			Message:     serviceURLMessage(err),
		})
	}
	return issues
}

func (c Client) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req StartRequest) []DispatchResult {
	ordered := orderStartServices(services)
	if issue, mismatch := workerVideoCapabilityMismatch(ordered); mismatch {
		return []DispatchResult{{
			ServiceID: issue.ServiceID, ServiceType: issue.ServiceType,
			Code: issue.Code, FailurePhase: "pre_dispatch", Error: issue.Message,
		}}
	}
	if req.VideoCoverStart != nil {
		encoder := firstService(ordered, "encoder_recorder")
		if !reportedCapabilityTrue(encoder.ReportedCapabilities, CapabilityLiveVideoCoverV1) {
			return []DispatchResult{{ServiceID: encoder.ServiceID, ServiceType: "encoder_recorder", Code: "video_cover_capability_unavailable", FailurePhase: "pre_dispatch", Error: "Encoder does not advertise video cover runtime support"}}
		}
	}
	if req.SceneAppearance != nil {
		worker := firstService(ordered, "worker")
		if !reportedCapabilityTrue(worker.ReportedCapabilities, CapabilitySceneAppearanceV1) {
			return []DispatchResult{{ServiceID: worker.ServiceID, ServiceType: "worker", Code: "scene_appearance_capability_unavailable", FailurePhase: "pre_dispatch", Error: "Worker does not advertise scene appearance runtime support"}}
		}
	}
	discordService := firstService(ordered, "discord_bot")
	if strings.TrimSpace(discordService.ServiceID) != "" && !reportedCapabilityTrue(discordService.ReportedCapabilities, CapabilityDiscordResolvedTargetV2) {
		return []DispatchResult{{ServiceID: discordService.ServiceID, ServiceType: "discord_bot", Code: "discord_resolved_target_v2_capability_unavailable", FailurePhase: "pre_dispatch", Error: "Discord Bot does not advertise resolved target v2 support"}}
	}
	if strings.TrimSpace(discordService.ServiceID) != "" &&
		(req.DiscordTargetRevision == 0 || !validDiscordTargetID(req.DiscordGuildID) || !validDiscordTargetID(req.DiscordTextChannelID) || !validDiscordTargetID(req.DiscordVoiceChannelID)) {
		return []DispatchResult{{ServiceID: discordService.ServiceID, ServiceType: "discord_bot", Code: "discord_target_invalid", FailurePhase: "pre_dispatch", Error: "resolved Discord target snapshot is invalid"}}
	}
	if strings.TrimSpace(req.ArchiveProfileID) != "" && (strings.TrimSpace(req.ArchiveRunID) == "" || req.ArchiveStartedAt.IsZero()) {
		encoder := firstService(ordered, "encoder_recorder")
		return []DispatchResult{{ServiceID: encoder.ServiceID, ServiceType: "encoder_recorder", Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id and start time are required"}}
	}
	results := make([]DispatchResult, 0, len(ordered))
	encoderURL := firstServiceURL(ordered, "encoder_recorder")
	workerService := firstService(ordered, "worker")
	workerVideoEnabled := workerVideoCapabilitiesEnabled(ordered)
	now := time.Now().UTC()
	workerVideoToken := ""
	if workerVideoEnabled {
		workerVideoToken = c.issueIngestTokenForAudience(stream.ID, workerService, "worker_video", "encoder_recorder", now)
		if workerVideoToken == "" {
			return []DispatchResult{{
				ServiceID: workerService.ServiceID, ServiceType: workerService.ServiceType,
				Code: "worker_video_ingest_token_unavailable", FailurePhase: "pre_dispatch",
				Error: "job-scoped Worker video ingest credential is unavailable",
			}}
		}
	}
	var workerVideoRoute workerVideoIngestRoute
	for _, service := range ordered {
		if service.ServiceType == "discord_bot" && req.WorkerJobGeneration == 0 {
			results = append(results, DispatchResult{
				ServiceID: service.ServiceID, ServiceType: service.ServiceType,
				Code: "worker_job_generation_unavailable", FailurePhase: "pre_dispatch",
				Error: "positive Worker job generation is required before Discord Bot start",
			})
			break
		}
		endpoint, payload, ok := c.startPayload(stream, service, req, encoderURL, workerService, now)
		if !ok {
			continue
		}
		payloadMap, payloadMapOK := payload.(map[string]any)
		if workerVideoEnabled && !payloadMapOK {
			return append(results, DispatchResult{
				ServiceID: service.ServiceID, ServiceType: service.ServiceType,
				Code: "worker_video_start_payload_invalid", FailurePhase: "pre_dispatch",
				Error: "Worker video start payload is invalid",
			})
		}
		if workerVideoEnabled {
			switch service.ServiceType {
			case "encoder_recorder":
				payloadMap["worker_video_ingest"] = true
				delete(payloadMap, "input_url")
				delete(payloadMap, "input_mode")
				payloadMap["worker_video_ingest_token"] = workerVideoToken
			case "worker":
				payloadMap["video_ingest_url"] = workerVideoRoute.URL
				payloadMap["video_ingest_passphrase"] = workerVideoRoute.secret()
				payloadMap["video_ingest_pbkeylen"] = workerVideoRoute.PBKeyLen
				payloadMap["encoder_profile_id"] = req.EncoderProfileID
				payloadMap["video_width"] = req.EncoderVideoWidth
				payloadMap["video_height"] = req.EncoderVideoHeight
				payloadMap["video_fps"] = req.EncoderVideoFPS
			}
		}
		var result DispatchResult
		if workerVideoEnabled && service.ServiceType == "encoder_recorder" {
			result = c.postCapturingWorkerVideoIngest(ctx, service, endpoint, payload, &workerVideoRoute)
			if result.Success {
				if err := validateWorkerVideoIngestRoute(workerVideoRoute); err != nil {
					result.Success = false
					result.Code = "worker_video_ingest_response_invalid"
					result.FailurePhase = "protocol"
					result.Error = "encoder returned an invalid Worker video ingest route"
				}
			}
		} else {
			result = c.post(ctx, service, endpoint, payload)
		}
		if service.ServiceType == "worker" && result.Success {
			if result.JobGeneration == 0 {
				result.Success = false
				result.Code = "worker_job_generation_response_invalid"
				result.FailurePhase = "protocol"
				result.Error = "Worker start response did not include a positive job generation"
			} else {
				req.WorkerJobGeneration = result.JobGeneration
				if workerVideoEnabled {
					result.VideoOverlayBurnInNegotiated = true
				}
			}
		}
		results = append(results, result)
		// Start dependencies are ordered encoder -> worker -> Discord Bot.
		// Once a dependency rejects the start, continuing would create a
		// misleading partial start (for example, the Bot joins Discord while
		// the Encoder never accepted the media process).  The caller will
		// terminalize the stream from the first failure and can retry the whole
		// dependency chain after the underlying issue is fixed.
		if !result.Success {
			break
		}
	}
	return results
}

func (c Client) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []DispatchResult {
	ordered := orderStopServices(services)
	results := make([]DispatchResult, 0, len(ordered))
	for _, service := range ordered {
		endpoint, payload, ok := stopPayload(stream, service)
		if !ok {
			continue
		}
		if service.ServiceType == "encoder_recorder" {
			results = append(results, c.postWithTimeout(ctx, service, endpoint, payload, encoderStopRequestTimeout(c.Config.Timeout)))
			continue
		}
		results = append(results, c.post(ctx, service, endpoint, payload))
	}
	return results
}

// Start order is part of the downstream lifecycle contract. The Encoder must
// have accepted the media process before the Discord Bot joins and begins
// forwarding audio; Worker starts between those two so its event route is
// available before the Bot sends participant/caption events. Keep this order
// deterministic even when the database returns assignments by service type.
func orderStartServices(services []store.RegisteredService) []store.RegisteredService {
	ordered := append([]store.RegisteredService(nil), services...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return startServiceRank(ordered[i].ServiceType) < startServiceRank(ordered[j].ServiceType)
	})
	return ordered
}

func startServiceRank(serviceType string) int {
	switch strings.TrimSpace(serviceType) {
	case "encoder_recorder":
		return 0
	case "worker":
		return 1
	case "discord_bot":
		return 2
	default:
		return 3
	}
}

// Stop in the reverse dependency order: stop the Bot's audio/event producer,
// then Worker processing, and only then terminate the Encoder process.
func orderStopServices(services []store.RegisteredService) []store.RegisteredService {
	ordered := append([]store.RegisteredService(nil), services...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return stopServiceRank(ordered[i].ServiceType) < stopServiceRank(ordered[j].ServiceType)
	})
	return ordered
}

func stopServiceRank(serviceType string) int {
	switch strings.TrimSpace(serviceType) {
	case "discord_bot":
		return 0
	case "worker":
		return 1
	case "encoder_recorder":
		return 2
	default:
		return 3
	}
}

func (c Client) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []DispatchResult {
	results := make([]DispatchResult, 0, len(services))
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" {
			continue
		}
		if strings.TrimSpace(stream.ArchiveRunID) == "" || stream.ArchiveStartedAt == nil || stream.ArchiveStartedAt.IsZero() {
			results = append(results, DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id and start time are required"})
			continue
		}
		payload := map[string]any{
			"stream_id":      stream.ID,
			"name":           stream.Name,
			"archive_run_id": stream.ArchiveRunID,
			"started_at":     stream.ArchiveStartedAt.UTC(),
			"dry_run":        false,
		}
		if len(archiveConfig) > 0 {
			payload["archive_config"] = archiveConfig
		}
		results = append(results, c.post(ctx, service, "/streams/package", payload))
	}
	return results
}

func (c Client) AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) AudioStatusResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getAudioStatus(ctx, service, "/streams/"+url.PathEscape(stream.ID)+"/audio-status")
		}
	}
	return AudioStatusResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) WorkerEventsResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getWorkerEvents(ctx, service, "/streams/"+url.PathEscape(stream.ID)+"/worker-events")
		}
	}
	return WorkerEventsResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) ServicePreflightResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getEncoderPreflight(ctx, service, "/preflight")
		}
	}
	return ServicePreflightResult{ServiceType: "encoder_recorder", Endpoint: "/preflight", Error: "assigned encoder_recorder service not found"}
}

func (c Client) PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name, byteRange string) PreviewAssetResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			endpoint := "/streams/" + url.PathEscape(stream.ID) + "/preview/" + url.PathEscape(name)
			return c.getPreviewAsset(ctx, service, endpoint, name, byteRange)
		}
	}
	return PreviewAssetResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) DispatchResult {
	for _, service := range services {
		if service.ServiceType != "discord_bot" {
			continue
		}
		endpoint := "/streams/" + url.PathEscape(stream.ID) + "/notifications/youtube-live"
		payload := map[string]string{"event_id": strings.TrimSpace(eventID), "watch_url": strings.TrimSpace(watchURL)}
		// This is intentionally one attempt. A response loss (and many 5xx
		// outcomes) may occur after Discord accepted the message. The durable
		// Control Panel outbox decides whether an explicit service receipt makes
		// a retry safe; this transport client must not create a hidden retry loop.
		return c.post(ctx, service, endpoint, payload)
	}
	return DispatchResult{ServiceType: "discord_bot", Error: "assigned discord_bot service not found"}
}

func (c Client) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, byteRange string) ArchiveArtifactDownloadResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return ArchiveArtifactDownloadResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", Error: "archive run id is required"}
			}
			return c.getArchiveArtifact(ctx, service, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), artifact.Name, byteRange)
		}
	}
	return ArchiveArtifactDownloadResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id is required"}
			}
			return c.serviceJSONAction(ctx, service, http.MethodDelete, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), nil)
		}
	}
	return DispatchResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			if strings.TrimSpace(artifact.ArchiveRunID) == "" {
				return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Code: "archive_run_authority_unavailable", FailurePhase: "pre_dispatch", Error: "archive run id is required"}
			}
			return c.serviceJSONAction(ctx, service, http.MethodPut, archiveArtifactEndpoint(stream.ID, artifact.ArchiveRunID, artifact.Name), map[string]string{"name": name})
		}
	}
	return DispatchResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req WorkerEventRequest) DispatchResult {
	for _, service := range services {
		if service.ServiceType != "worker" {
			continue
		}
		endpoint, payload, ok := workerEventPayload(stream, req)
		if !ok {
			return DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Error: "unsupported worker event type"}
		}
		return c.post(ctx, service, endpoint, payload)
	}
	return DispatchResult{ServiceType: "worker", Error: "assigned worker service not found"}
}

func (c Client) authToken(service store.RegisteredService) (string, error) {
	if c.RuntimeTokenResolver != nil {
		token, err := c.RuntimeTokenResolver(service)
		if err != nil || strings.TrimSpace(token) == "" {
			return "", errors.New("node runtime token could not be resolved")
		}
		return strings.TrimSpace(token), nil
	}
	if strings.TrimSpace(service.NodeTokenCiphertext) != "" && strings.TrimSpace(service.NodeTokenNonce) != "" {
		key := strings.TrimSpace(c.Config.NodeTokenKey)
		if key == "" {
			return "", errors.New("node runtime token encryption key is not configured")
		}
		token, err := security.DecryptSecret(service.NodeTokenCiphertext, service.NodeTokenNonce, key)
		if err != nil || strings.TrimSpace(token) == "" {
			return "", errors.New("node runtime token could not be decrypted")
		}
		return token, nil
	}
	return "", errors.New("node runtime token is not configured")
}

func (c Client) post(ctx context.Context, service store.RegisteredService, endpoint string, payload any) DispatchResult {
	return c.serviceJSONAction(ctx, service, http.MethodPost, endpoint, payload)
}

func (c Client) postCapturingWorkerVideoIngest(ctx context.Context, service store.RegisteredService, endpoint string, payload any, route *workerVideoIngestRoute) DispatchResult {
	return c.serviceJSONActionInternal(ctx, service, http.MethodPost, endpoint, payload, route)
}

// postWithTimeout uses a copied Client so the Encoder's bounded graceful stop
// window cannot shorten other service calls or mutate a caller-supplied HTTP
// client shared by them.
func (c Client) postWithTimeout(ctx context.Context, service store.RegisteredService, endpoint string, payload any, timeout time.Duration) DispatchResult {
	c.Config.Timeout = timeout
	if c.HTTP != nil {
		client := *c.HTTP
		client.Timeout = timeout
		c.HTTP = &client
	}
	return c.post(ctx, service, endpoint, payload)
}

func encoderStopRequestTimeout(configured time.Duration) time.Duration {
	if configured > encoderStopTimeout {
		return configured
	}
	return encoderStopTimeout
}

func (c Client) serviceJSONAction(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any) DispatchResult {
	return c.serviceJSONActionInternal(ctx, service, method, endpoint, payload, nil)
}

func (c Client) serviceJSONActionInternal(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any, workerVideoRoute *workerVideoIngestRoute) DispatchResult {
	result := DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.FailurePhase = "pre_dispatch"
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Code = serviceURLIssueCode(err)
		result.Error = serviceURLMessage(err)
		return result
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			result.FailurePhase = "pre_dispatch"
			result.Error = "marshal payload failed"
			return result
		}
		body = bytes.NewReader(encoded)
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, method, joinURL(service.PublicURL, endpoint), body)
	if err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.FailurePhase = "transport"
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var successBody struct {
			MessageID     string                 `json:"message_id"`
			AlreadySent   bool                   `json:"already_sent"`
			JobGeneration uint64                 `json:"job_generation"`
			VideoIngest   workerVideoIngestRoute `json:"video_ingest"`
		}
		workerStartResponse := service.ServiceType == "worker" && endpoint == "/jobs/start"
		var decoder *json.Decoder
		if workerStartResponse {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxWorkerStartResponseBytes+1))
			if readErr != nil {
				result.Code = "worker_job_generation_response_invalid"
				result.FailurePhase = "protocol"
				result.Error = "Worker start response could not be read"
				return result
			}
			if len(body) > maxWorkerStartResponseBytes {
				result.Code = "worker_job_generation_response_invalid"
				result.FailurePhase = "protocol"
				result.Error = "Worker start response exceeded size limit"
				return result
			}
			decoder = json.NewDecoder(bytes.NewReader(body))
		} else {
			decoder = json.NewDecoder(io.LimitReader(response.Body, 1<<20))
		}
		if err := decoder.Decode(&successBody); err == nil {
			if workerStartResponse {
				var trailing any
				if err := decoder.Decode(&trailing); err != io.EOF {
					result.Code = "worker_job_generation_response_invalid"
					result.FailurePhase = "protocol"
					result.Error = "Worker start response did not contain exactly one JSON value"
					return result
				}
			}
			result.MessageID = sanitizeServiceErrorValue(successBody.MessageID)
			result.AlreadySent = successBody.AlreadySent
			result.JobGeneration = successBody.JobGeneration
			if workerVideoRoute != nil {
				*workerVideoRoute = successBody.VideoIngest
			}
		}
		result.Success = true
		return result
	}
	var errorBody struct {
		Code         string `json:"code"`
		FailurePhase string `json:"failure_phase"`
		ErrorClass   string `json:"error_class"`
		Retryable    bool   `json:"retryable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&errorBody); err == nil {
		result.Code = sanitizeServiceErrorValue(errorBody.Code)
		result.FailurePhase = sanitizeServiceErrorValue(errorBody.FailurePhase)
		result.ErrorClass = sanitizeServiceErrorValue(errorBody.ErrorClass)
		result.Retryable = errorBody.Retryable
	}
	result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
	if result.Code != "" {
		result.Error += ": " + result.Code
	}
	return result
}

func (c Client) getArchiveArtifact(ctx context.Context, service store.RegisteredService, endpoint, fallbackName, byteRange string) ArchiveArtifactDownloadResult {
	result := ArchiveArtifactDownloadResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint, FileName: fallbackName}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Code = serviceURLIssueCode(err)
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx, cancel := context.WithTimeout(ctx, archiveTransferTimeout)
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		cancel()
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/octet-stream")
	if value := normalizedPreviewByteRange(byteRange); value != "" {
		request.Header.Set("Range", value)
	}
	client := c.archiveHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		cancel()
		result.Error = "service request failed"
		return result
	}
	result.StatusCode = response.StatusCode
	result.ContentRange = response.Header.Get("Content-Range")
	result.AcceptRanges = response.Header.Get("Accept-Ranges")
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		defer cancel()
		defer response.Body.Close()
		result.Success = true
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer cancel()
		defer response.Body.Close()
		var errorBody struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&errorBody); err == nil {
			result.Code = sanitizeServiceErrorValue(errorBody.Code)
		}
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		if result.Code != "" {
			result.Error += ": " + result.Code
		}
		return result
	}
	result.Success = true
	result.ContentType = response.Header.Get("Content-Type")
	result.SizeBytes = response.ContentLength
	result.Body = &cancelOnCloseReadCloser{ReadCloser: response.Body, cancel: cancel}
	return result
}

func (c Client) archiveHTTPClient() *http.Client {
	client := c.httpClient()
	clone := *client
	clone.Timeout = archiveTransferTimeout
	return &clone
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return err
}

func (c Client) getPreviewAsset(ctx context.Context, service store.RegisteredService, endpoint, name, byteRange string) PreviewAssetResult {
	result := PreviewAssetResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Code = serviceURLIssueCode(err)
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", previewAcceptHeader(name))
	if name != "index.m3u8" {
		if rangeValue := normalizedPreviewByteRange(byteRange); rangeValue != "" {
			request.Header.Set("Range", rangeValue)
		}
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")
	result.ContentRange = response.Header.Get("Content-Range")
	result.AcceptRanges = response.Header.Get("Accept-Ranges")
	// A valid RFC 7233 unsatisfied range is not an upstream availability
	// failure. The Control Panel validates the only safe form of
	// Content-Range before returning it to the browser.
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		result.Success = true
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var errorBody struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&errorBody); err == nil {
			result.Code = sanitizeServiceErrorValue(errorBody.Code)
		}
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	limit := int64(maxPreviewSegmentBytes)
	if name == "index.m3u8" {
		limit = maxPreviewPlaylistBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		result.Error = "read preview asset failed"
		return result
	}
	if int64(len(body)) > limit {
		result.Code = "preview_asset_too_large"
		result.Error = "preview asset exceeded size limit"
		return result
	}
	result.Success = true
	result.Body = body
	return result
}

// normalizedPreviewByteRange accepts exactly one RFC 7233 byte range. Preview
// is a bounded proxy, so forwarding arbitrary or multi-range headers would
// create avoidable request amplification at the Encoder.
func normalizedPreviewByteRange(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes=") {
		return ""
	}
	spec := strings.TrimSpace(strings.TrimPrefix(value, "bytes="))
	if spec == "" || strings.Contains(spec, ",") {
		return ""
	}
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return ""
	}
	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	if start == "" && end == "" {
		return ""
	}
	if start != "" && !decimalPreviewRangeValue(start) {
		return ""
	}
	if end != "" && !decimalPreviewRangeValue(end) {
		return ""
	}
	if start != "" && end != "" {
		startValue, startErr := strconv.ParseUint(start, 10, 64)
		endValue, endErr := strconv.ParseUint(end, 10, 64)
		if startErr != nil || endErr != nil || endValue < startValue {
			return ""
		}
	}
	return "bytes=" + start + "-" + end
}

func decimalPreviewRangeValue(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func previewAcceptHeader(name string) string {
	if name == "index.m3u8" {
		return "application/vnd.apple.mpegurl, application/x-mpegURL"
	}
	return "video/mp2t"
}

func (c Client) getWorkerEvents(ctx context.Context, service store.RegisteredService, endpoint string) WorkerEventsResult {
	result := WorkerEventsResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	var body struct {
		Events []WorkerEvent `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Events = body.Events
	result.Success = true
	return RedactWorkerEventsResult(result)
}

func (c Client) getEncoderPreflight(ctx context.Context, service store.RegisteredService, endpoint string) ServicePreflightResult {
	result := ServicePreflightResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	var body struct {
		Ready     bool                    `json:"ready"`
		CheckedAt time.Time               `json:"checked_at"`
		Checks    []ServicePreflightCheck `json:"checks"`
		Summary   map[string]any          `json:"summary"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Ready = body.Ready
	result.CheckedAt = body.CheckedAt
	result.Checks = body.Checks
	result.Summary = body.Summary
	result.Success = true
	return RedactServicePreflightResult(result)
}

func (c Client) getAudioStatus(ctx context.Context, service store.RegisteredService, endpoint string) AudioStatusResult {
	result := AudioStatusResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	if err := json.NewDecoder(response.Body).Decode(&result.AudioBridgeState); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Success = true
	return result
}

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
		if len(req.ArchiveConfig) > 0 {
			payload["archive_config"] = req.ArchiveConfig
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

func firstServiceURL(services []store.RegisteredService, serviceType string) string {
	return firstService(services, serviceType).PublicURL
}

func firstService(services []store.RegisteredService, serviceType string) store.RegisteredService {
	for _, service := range services {
		if service.ServiceType == serviceType {
			return service
		}
	}
	return store.RegisteredService{}
}

func workerVideoCapabilitiesEnabled(services []store.RegisteredService) bool {
	worker := firstService(services, "worker")
	encoder := firstService(services, "encoder_recorder")
	workerEnabled := reportedCapabilityTrue(worker.ReportedCapabilities, "scene_frames_mjpeg_srt")
	encoderEnabled := reportedCapabilityTrue(encoder.ReportedCapabilities, "worker_frame_ingest_mjpeg_srt")
	return workerEnabled && encoderEnabled
}

// WorkerVideoCapabilitiesEnabled reports whether the exact primary services
// advertise the compatible ends of the new media path. A successful Worker
// dispatch is still required before callers may persist an active contract.
func WorkerVideoCapabilitiesEnabled(services []store.RegisteredService) bool {
	return workerVideoCapabilitiesEnabled(services)
}

func workerVideoCapabilityMismatch(services []store.RegisteredService) (ReadinessIssue, bool) {
	worker := firstService(services, "worker")
	encoder := firstService(services, "encoder_recorder")
	workerEnabled := reportedCapabilityTrue(worker.ReportedCapabilities, "scene_frames_mjpeg_srt")
	encoderEnabled := reportedCapabilityTrue(encoder.ReportedCapabilities, "worker_frame_ingest_mjpeg_srt")
	if workerEnabled == encoderEnabled {
		return ReadinessIssue{}, false
	}
	missing := worker
	if workerEnabled {
		missing = encoder
	}
	return ReadinessIssue{
		ServiceID: missing.ServiceID, ServiceType: missing.ServiceType,
		Code:    "worker_video_capability_mismatch",
		Message: "Worker scene-frame output and Encoder MJPEG frame ingest capabilities must be upgraded together.",
	}, true
}

func reportedCapabilityTrue(capabilities map[string]any, name string) bool {
	value, ok := capabilities[name].(bool)
	return ok && value
}

func validDiscordTargetID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateWorkerVideoIngestRoute(route workerVideoIngestRoute) error {
	rawURL := route.URL
	if rawURL == "" || rawURL != strings.TrimSpace(rawURL) {
		return errors.New("invalid SRT ingest URL")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "srt") || parsed.Opaque != "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("invalid SRT ingest URL")
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || strings.TrimSpace(host) == "" || port < 1 || port > 65535 {
		return errors.New("invalid SRT ingest URL")
	}
	secret := route.secret()
	if len(secret) < 32 || len(secret) > 79 || !isBase64URLCredential(secret) || route.PBKeyLen != 32 {
		return errors.New("invalid SRT ingest credential")
	}
	return nil
}

func isBase64URLCredential(value string) bool {
	for _, character := range []byte(value) {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func capabilityBool(capabilities map[string]any, name string) (bool, bool) {
	if capabilities == nil {
		return false, false
	}
	value, ok := capabilities[name]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		if normalized == "true" || normalized == "1" || normalized == "yes" {
			return true, true
		}
		if normalized == "false" || normalized == "0" || normalized == "no" {
			return false, true
		}
	}
	return false, false
}

func sanitizeServiceErrorValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 80 {
		value = value[:80]
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "invalid_error_value"
		}
	}
	return value
}

func joinURL(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return c.Config.URLPolicy.HTTPClient(c.Config.Timeout)
}

func serviceURLIssueCode(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "service_public_url_blocked"
	}
	return "service_public_url_invalid"
}

func encoderURLIssueCode(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "encoder_public_url_blocked"
	}
	return "encoder_public_url_invalid"
}

func serviceURLMessage(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "service public_url is blocked by outbound policy"
	}
	return "service public_url must be an absolute http or https URL without credentials"
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value + "s")
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func envMinutes(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value + "m")
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
