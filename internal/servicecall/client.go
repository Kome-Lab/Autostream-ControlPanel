package servicecall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

type Config struct {
	Token                 string
	Timeout               time.Duration
	URLPolicy             netpolicy.ServiceURLPolicy
	IngestTokenSigningKey string
	IngestTokenTTL        time.Duration
	NodeTokenKey          string
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

type StartRequest struct {
	DiscordConfigID            string         `json:"discord_config_id,omitempty"`
	DiscordGuildID             string         `json:"discord_guild_id,omitempty"`
	DiscordVoiceChannelID      string         `json:"discord_voice_channel_id,omitempty"`
	DiscordTextChannelID       string         `json:"discord_text_channel_id,omitempty"`
	EncoderInputURL            string         `json:"encoder_input_url,omitempty"`
	EncoderRTMPURL             string         `json:"encoder_rtmp_url,omitempty"`
	EncoderStreamKey           string         `json:"-"`
	EncoderStreamKeySecretName string         `json:"-"`
	EncoderProfileID           string         `json:"encoder_profile_id,omitempty"`
	CaptionProfileID           string         `json:"caption_profile_id,omitempty"`
	OverlayProfileID           string         `json:"overlay_profile_id,omitempty"`
	ArchiveProfileID           string         `json:"archive_profile_id,omitempty"`
	YouTubeOutputID            string         `json:"youtube_output_id,omitempty"`
	YouTubeRuntime             map[string]any `json:"-"`
	ArchiveConfig              map[string]any `json:"-"`
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
	ServiceID   string        `json:"service_id"`
	ServiceType string        `json:"service_type"`
	Endpoint    string        `json:"endpoint"`
	StatusCode  int           `json:"status_code"`
	Success     bool          `json:"success"`
	Error       string        `json:"error,omitempty"`
	Code        string        `json:"code,omitempty"`
	FileName    string        `json:"file_name,omitempty"`
	ContentType string        `json:"content_type,omitempty"`
	SizeBytes   int64         `json:"size_bytes,omitempty"`
	Body        io.ReadCloser `json:"-"`
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
		Token:                 os.Getenv("SERVICE_CALL_TOKEN"),
		Timeout:               envDuration("SERVICE_CALL_TIMEOUT_SEC", 5*time.Second),
		URLPolicy:             netpolicy.ServiceURLPolicyFromEnv(),
		IngestTokenSigningKey: os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"),
		IngestTokenTTL:        envMinutes("AUTOSTREAM_STREAM_INGEST_TOKEN_TTL_MIN", 12*time.Hour),
		NodeTokenKey:          os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"),
	}}
}

func (c Client) Enabled() bool {
	return strings.TrimSpace(c.Config.Token) != "" || strings.TrimSpace(c.Config.NodeTokenKey) != ""
}

func (c Client) StartReadinessIssues(services []store.RegisteredService, req StartRequest, now time.Time) []ReadinessIssue {
	var issues []ReadinessIssue
	if !c.Enabled() {
		issues = append(issues, ReadinessIssue{
			Code:    "service_call_token_missing",
			Message: "SERVICE_CALL_TOKEN is not configured on the Control Panel.",
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
	results := make([]DispatchResult, 0, len(services))
	encoderURL := firstServiceURL(services, "encoder_recorder")
	workerService := firstService(services, "worker")
	for _, service := range services {
		endpoint, payload, ok := c.startPayload(stream, service, req, encoderURL, workerService, time.Now().UTC())
		if !ok {
			continue
		}
		results = append(results, c.post(ctx, service, endpoint, payload))
	}
	return results
}

func (c Client) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []DispatchResult {
	results := make([]DispatchResult, 0, len(services))
	for _, service := range services {
		endpoint, payload, ok := stopPayload(stream, service)
		if !ok {
			continue
		}
		results = append(results, c.post(ctx, service, endpoint, payload))
	}
	return results
}

func (c Client) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []DispatchResult {
	results := make([]DispatchResult, 0, len(services))
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" {
			continue
		}
		payload := map[string]any{
			"stream_id":  stream.ID,
			"name":       stream.Name,
			"started_at": stream.CreatedAt,
			"dry_run":    false,
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

func (c Client) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) ArchiveArtifactDownloadResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.getArchiveArtifact(ctx, service, archiveArtifactEndpoint(stream.ID, artifact.Name), artifact.Name)
		}
	}
	return ArchiveArtifactDownloadResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.serviceJSONAction(ctx, service, http.MethodDelete, archiveArtifactEndpoint(stream.ID, artifact.Name), nil)
		}
	}
	return DispatchResult{ServiceType: "encoder_recorder", Error: "assigned encoder_recorder service not found"}
}

func (c Client) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) DispatchResult {
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			return c.serviceJSONAction(ctx, service, http.MethodPut, archiveArtifactEndpoint(stream.ID, artifact.Name), map[string]string{"name": name})
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
	if token := strings.TrimSpace(c.Config.Token); token != "" {
		return token, nil
	}
	return "", errors.New("node runtime token is not configured")
}

func (c Client) post(ctx context.Context, service store.RegisteredService, endpoint string, payload any) DispatchResult {
	return c.serviceJSONAction(ctx, service, http.MethodPost, endpoint, payload)
}

func (c Client) serviceJSONAction(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any) DispatchResult {
	result := DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "SERVICE_CALL_TOKEN is not configured"
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
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
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
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		result.Success = true
		return result
	}
	var errorBody struct {
		Code         string `json:"code"`
		FailurePhase string `json:"failure_phase"`
		ErrorClass   string `json:"error_class"`
	}
	if err := json.NewDecoder(response.Body).Decode(&errorBody); err == nil {
		result.Code = sanitizeServiceErrorValue(errorBody.Code)
		result.FailurePhase = sanitizeServiceErrorValue(errorBody.FailurePhase)
		result.ErrorClass = sanitizeServiceErrorValue(errorBody.ErrorClass)
	}
	result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
	if result.Code != "" {
		result.Error += ": " + result.Code
	}
	return result
}

func (c Client) getArchiveArtifact(ctx context.Context, service store.RegisteredService, endpoint, fallbackName string) ArchiveArtifactDownloadResult {
	result := ArchiveArtifactDownloadResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint, FileName: fallbackName}
	if !c.Enabled() {
		result.Error = "SERVICE_CALL_TOKEN is not configured"
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
	request.Header.Set("Accept", "application/octet-stream")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
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
	result.Body = response.Body
	return result
}

func (c Client) getWorkerEvents(ctx context.Context, service store.RegisteredService, endpoint string) WorkerEventsResult {
	result := WorkerEventsResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "SERVICE_CALL_TOKEN is not configured"
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
		result.Error = "SERVICE_CALL_TOKEN is not configured"
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
		result.Error = "SERVICE_CALL_TOKEN is not configured"
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
			"stream_id":          stream.ID,
			"name":               stream.Name,
			"input_url":          req.EncoderInputURL,
			"rtmp_url":           req.EncoderRTMPURL,
			"encoder_profile_id": req.EncoderProfileID,
			"archive_profile_id": req.ArchiveProfileID,
		}
		if req.EncoderStreamKey != "" {
			payload["stream_key"] = req.EncoderStreamKey
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
		return "/streams/start", payload, true
	case "discord_bot":
		payload := map[string]any{
			"stream_id":         stream.ID,
			"guild_id":          req.DiscordGuildID,
			"voice_channel_id":  req.DiscordVoiceChannelID,
			"text_channel_id":   req.DiscordTextChannelID,
			"encoder_audio_url": encoderURL,
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

func archiveArtifactEndpoint(streamID, name string) string {
	return "/streams/" + url.PathEscape(streamID) + "/artifacts/" + url.PathEscape(name)
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
