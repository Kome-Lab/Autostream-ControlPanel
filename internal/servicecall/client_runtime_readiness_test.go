package servicecall

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartReadinessIssues(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Minute)
	client := Client{}
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com", Status: "online"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "ftp://worker.example.com", Status: "online"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com", Status: "online", LastHeartbeatAt: &stale, Capabilities: map[string]any{"audio_stream_forward": false}},
	}, StartRequest{}, now)
	for _, want := range []string{"node_runtime_token_key_missing", "node_runtime_token_missing", "stream_ingest_signing_key_missing", "service_public_url_invalid", "service_heartbeat_stale", "discord_audio_forward_unavailable"} {
		if !hasIssueCode(issues, want) {
			t.Fatalf("missing readiness issue %s in %#v", want, issues)
		}
	}
}

func TestStartReadinessAllowsUnknownAudioForwardCapability(t *testing.T) {
	now := time.Now().UTC()
	client := clientWithStaticTestToken(Config{IngestTokenSigningKey: "stream-ingest-signing-key"}, "service-token")
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com", Status: "online"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com", Status: "online"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com", Status: "online"},
	}, StartRequest{}, now)
	if len(issues) != 0 {
		t.Fatalf("unexpected readiness issues: %#v", issues)
	}
}

func TestStartReadinessBlocksUnavailableCaptionPipelineCapabilities(t *testing.T) {
	now := time.Now().UTC()
	client := clientWithStaticTestToken(Config{IngestTokenSigningKey: "stream-ingest-signing-key"}, "service-token")
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com", Status: "online"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com", Status: "online", Capabilities: map[string]any{"deepgram_transcription": false}},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com", Status: "online", Capabilities: map[string]any{"audio_stream_forward": true, "audio_capture": true, "caption_audio_forward": false}},
	}, StartRequest{CaptionProfileID: "caption-prof-01"}, now)
	for _, want := range []string{"discord_caption_audio_forward_unavailable", "worker_deepgram_transcription_unavailable"} {
		if !hasIssueCode(issues, want) {
			t.Fatalf("missing caption readiness issue %s in %#v", want, issues)
		}
	}
}

func TestUpdateWorkerCaptionRuntimeSettingsUsesCapabilityAndProfileReferenceOnly(t *testing.T) {
	var method, path, authorization string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		authorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"caption_session_generation": 2})
	}))
	defer server.Close()

	result := testClient().UpdateWorkerCaptionRuntimeSettings(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, Capabilities: map[string]any{"live_caption_runtime_settings": true}},
	}, "caption-profile-01")
	if !result.Success || result.StatusCode != http.StatusOK {
		t.Fatalf("caption runtime dispatch failed: %#v", result)
	}
	if method != http.MethodPut || path != "/jobs/stream-01/caption-runtime-settings" || authorization != "Bearer service-token" {
		t.Fatalf("unexpected request: method=%s path=%s authorization=%s", method, path, authorization)
	}
	if len(body) != 1 || body["caption_profile_id"] != "caption-profile-01" {
		t.Fatalf("caption runtime payload must contain only the profile reference: %#v", body)
	}
}

func TestUpdateWorkerCaptionRuntimeSettingsRejectsUnsupportedWorkerBeforeDispatch(t *testing.T) {
	result := testClient().UpdateWorkerCaptionRuntimeSettings(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "http://127.0.0.1:1", Capabilities: map[string]any{}},
	}, "caption-profile-01")
	if result.Success || result.Code != "worker_caption_runtime_settings_not_supported" || result.FailurePhase != "pre_dispatch" {
		t.Fatalf("unsupported worker was not rejected before dispatch: %#v", result)
	}
}

func TestStartReadinessBlocksDisabledDiscordAudioCapture(t *testing.T) {
	now := time.Now().UTC()
	client := clientWithStaticTestToken(Config{}, "service-token")
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com", Status: "online"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com", Status: "online"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com", Status: "online", Capabilities: map[string]any{"audio_stream_forward": true, "audio_capture": false}},
	}, StartRequest{}, now)
	if !hasIssueCode(issues, "discord_audio_capture_unavailable") {
		t.Fatalf("missing audio capture readiness issue: %#v", issues)
	}
}

func TestStartReadinessBlocksPrivateServiceURLByDefault(t *testing.T) {
	client := clientWithStaticTestToken(Config{}, "service-token")
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "http://169.254.169.254", Status: "online"},
	}, StartRequest{EncoderInputURL: "srt://input.example.com:9000"}, time.Now().UTC())
	if !hasIssueCode(issues, "service_public_url_blocked") {
		t.Fatalf("missing blocked URL readiness issue: %#v", issues)
	}
}

func testClient() Client {
	return clientWithStaticTestToken(Config{
		Timeout: time.Second,
		URLPolicy: netpolicy.ServiceURLPolicy{
			AllowedHosts: map[string]struct{}{"127.0.0.1": {}},
		},
	}, "service-token")
}

func clientWithStaticTestToken(config Config, token string) Client {
	return Client{Config: config, RuntimeTokenResolver: func(store.RegisteredService) (string, error) {
		return token, nil
	}}
}

type capturedRequestDeadline struct {
	path         string
	deadlineSet  bool
	timeToExpiry time.Duration
}

type requestDeadlineTransport struct {
	requests []capturedRequestDeadline
}

func (t *requestDeadlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	deadline, deadlineSet := request.Context().Deadline()
	captured := capturedRequestDeadline{path: request.URL.Path, deadlineSet: deadlineSet}
	if deadlineSet {
		captured.timeToExpiry = time.Until(deadline)
	}
	t.requests = append(t.requests, captured)
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    request,
	}, nil
}

func assertRequestTimeout(t *testing.T, request capturedRequestDeadline, want time.Duration) {
	t.Helper()
	if !request.deadlineSet {
		t.Fatalf("request %s had no deadline", request.path)
	}
	const tolerance = time.Second
	if request.timeToExpiry < want-tolerance || request.timeToExpiry > want+tolerance {
		t.Fatalf("request %s timeout = %s, want %s (+/-%s)", request.path, request.timeToExpiry, want, tolerance)
	}
}

func hasIssueCode(issues []ReadinessIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
