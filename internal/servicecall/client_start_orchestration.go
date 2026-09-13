package servicecall

import (
	"context"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
	"os"
	"sort"
	"strings"
	"time"
)

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
