package servicecall

import (
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartNegotiatesWorkerVideoIngestWithoutLeakingCredential(t *testing.T) {
	const (
		videoURL        = "srt://encoder-media.example.com:19000"
		videoPassphrase = "job-scoped-video-passphrase-32bytes"
	)
	var dispatchOrder []string
	var encoderPayload, workerPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch {
		case r.URL.Path == "/streams/start":
			dispatchOrder = append(dispatchOrder, "encoder_recorder")
			encoderPayload = payload
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"video_ingest": map[string]any{
				"url": videoURL, "passphrase": videoPassphrase, "pbkeylen": 32,
			}})
		case payload["video_ingest_url"] != nil:
			dispatchOrder = append(dispatchOrder, "worker")
			workerPayload = payload
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"job_generation": 17})
		case payload["discord_target"] != nil:
			dispatchOrder = append(dispatchOrder, "discord_bot")
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected start request: path=%s payload=%#v", r.URL.Path, payload)
		}
	}))
	defer server.Close()

	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	services := []store.RegisteredService{
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	}
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, services, StartRequest{
		DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002",
		EncoderProfileID: "enc-prof-01", EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60,
	})
	if got, want := strings.Join(dispatchOrder, ","), "encoder_recorder,worker,discord_bot"; got != want {
		t.Fatalf("start dispatch order = %q, want %q", got, want)
	}
	if len(results) != 3 || hasFailedDispatchResult(results) {
		t.Fatalf("unexpected dispatch results: %#v", results)
	}
	if !results[1].VideoOverlayBurnInNegotiated {
		t.Fatalf("Worker acceptance did not record internal negotiation evidence: %#v", results)
	}
	if encoderPayload["worker_video_ingest"] != true {
		t.Fatalf("encoder was not opted into Worker video ingest: %#v", encoderPayload)
	}
	if _, exists := encoderPayload["input_url"]; exists {
		t.Fatalf("negotiated Worker video start must not retain an ambiguous input_url: %#v", encoderPayload)
	}
	workerVideoToken, ok := encoderPayload["worker_video_ingest_token"].(string)
	if !ok || !strings.HasPrefix(workerVideoToken, "ast_ingest_v1.") {
		t.Fatalf("encoder did not receive a job-scoped Worker video token: %#v", encoderPayload)
	}
	if _, err := ingesttoken.Verify("test-ingest-signing-key", workerVideoToken, ingesttoken.Expected{
		StreamID: "stream-01", ServiceID: "worker-01", ServiceType: "worker", Purpose: "worker_video", Audience: "encoder_recorder",
	}); err != nil {
		t.Fatalf("worker video token claims mismatch: %v", err)
	}
	if workerPayload["video_ingest_url"] != videoURL || workerPayload["video_ingest_passphrase"] != videoPassphrase || workerPayload["video_ingest_pbkeylen"] != float64(32) {
		t.Fatalf("Worker video route was not dispatched: %#v", workerPayload)
	}
	if workerPayload["encoder_profile_id"] != "enc-prof-01" || workerPayload["video_width"] != float64(1920) || workerPayload["video_height"] != float64(1080) || workerPayload["video_fps"] != float64(60) {
		t.Fatalf("Worker video profile was not dispatched: %#v", workerPayload)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "VideoOverlayBurnInNegotiated") || strings.Contains(string(encoded), "video_overlay_burn_in_negotiated") {
		t.Fatalf("internal negotiation evidence leaked through DispatchResult JSON: %s", encoded)
	}
	for _, secret := range []string{videoPassphrase, workerVideoToken} {
		if strings.Contains(string(encoded), secret) || strings.Contains(fmt.Sprintf("%#v", results), secret) {
			t.Fatalf("Worker video credential leaked through DispatchResult: %s", encoded)
		}
	}
}

func TestStartWorkerVideoCapabilityMismatchFailsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workerCaps  map[string]any
		encoderCaps map[string]any
	}{
		{name: "Worker only", workerCaps: map[string]any{"scene_frames_mjpeg_srt": true}},
		{name: "Encoder only", encoderCaps: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			services := []store.RegisteredService{
				{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: tc.workerCaps},
				{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: tc.encoderCaps},
			}
			client := testClient()
			client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
			results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, services, StartRequest{})
			if requests != 0 || len(results) != 1 || results[0].Success || results[0].Code != "worker_video_capability_mismatch" || results[0].FailurePhase != "pre_dispatch" {
				t.Fatalf("capability mismatch did not fail before dispatch: requests=%d results=%#v", requests, results)
			}
			issues := client.StartReadinessIssues(services, StartRequest{}, time.Now().UTC())
			if !hasIssueCode(issues, "worker_video_capability_mismatch") {
				t.Fatalf("readiness did not report capability mismatch: %#v", issues)
			}
		})
	}
}

func TestWorkerVideoCapabilitiesRequireExactReportedBooleans(t *testing.T) {
	services := []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": "true"}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": "true"}},
	}
	if WorkerVideoCapabilitiesEnabled(services) {
		t.Fatalf("string capability values must not negotiate Worker video: %#v", services)
	}
	services[0].ReportedCapabilities["scene_frames_mjpeg_srt"] = true
	if _, mismatch := workerVideoCapabilityMismatch(services); !mismatch {
		t.Fatalf("one exact capability and one non-boolean capability must fail closed: %#v", services)
	}
}

func TestStartNegotiatedWorkerVideoStopsBeforeBotWhenWorkerRejectsRoute(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/streams/start" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"video_ingest": map[string]any{
				"url": "srt://encoder.example.com:19000", "passphrase": "worker-video-passphrase-32-bytes-ok", "pbkeylen": 32,
			}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "video_ingest_unavailable"})
	}))
	defer server.Close()
	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	}, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002", EncoderProfileID: "enc-prof-01", EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60})
	if got, want := strings.Join(paths, ","), "/streams/start,/jobs/start"; got != want {
		t.Fatalf("Bot was dispatched after Worker rejected its video route: got=%q want=%q", got, want)
	}
	if len(results) != 2 || !results[0].Success || results[1].Success || results[1].Code != "video_ingest_unavailable" || results[1].VideoOverlayBurnInNegotiated {
		t.Fatalf("unexpected Worker route failure results: %#v", results)
	}
}

func TestStartRejectsSecretBearingWorkerVideoURLWithoutLeakingCredential(t *testing.T) {
	const passphrase = "must-never-reach-dispatch-results-32"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"video_ingest": map[string]any{
			"url":        "srt://encoder.example.com:19000?mode=caller&passphrase=" + passphrase,
			"passphrase": passphrase,
			"pbkeylen":   32,
		}})
	}))
	defer server.Close()
	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	}, StartRequest{})
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Success || results[0].Code != "worker_video_ingest_response_invalid" || strings.Contains(string(encoded), passphrase) || strings.Contains(fmt.Sprintf("%#v", results), passphrase) {
		t.Fatalf("secret-bearing ingest response was not safely rejected: %#v", results)
	}
}

func hasFailedDispatchResult(results []DispatchResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}
