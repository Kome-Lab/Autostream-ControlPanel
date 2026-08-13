package servicecall

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestStartDispatchesToAssignedServices(t *testing.T) {
	var paths []string
	var dispatchOrder []string
	var auth string
	payloads := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		auth = r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["stream_id"] != "stream-01" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		switch {
		case payload["encoder_profile_id"] != nil:
			dispatchOrder = append(dispatchOrder, "encoder_recorder")
			payloads["encoder_recorder"] = payload
		case payload["overlay_profile_id"] != nil:
			dispatchOrder = append(dispatchOrder, "worker")
			payloads["worker"] = payload
		case payload["guild_id"] != nil:
			dispatchOrder = append(dispatchOrder, "discord_bot")
			payloads["discord_bot"] = payload
		}
		if payload["overlay_profile_id"] != nil && payload["encoder_profile_id"] == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"job_generation": 17})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	client.Config.IngestTokenTTL = time.Hour
	services := []store.RegisteredService{
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	}
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, services, StartRequest{
		DiscordGuildID: "guild", DiscordVoiceChannelID: "voice", DiscordTextChannelID: "text", EncoderInputURL: "srt://input.example.com:9000",
		EncoderStreamKeySecretName: "youtube_stream_key_main", EncoderProfileID: "enc-prof-01", ArchiveProfileID: "archive-prof-01", OverlayProfileID: "overlay-prof-01", CaptionProfileID: "caption-prof-01",
		ArchiveConfig:  map[string]any{"folder_id": "drive-folder-id", "shared_drive": true},
		YouTubeRuntime: map[string]any{"mode": "live_api_dry_run", "broadcast_id": "dry-broadcast-01", "dry_run": true},
	})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %#v", results)
	}
	if got, want := strings.Join(dispatchOrder, ","), "encoder_recorder,worker,discord_bot"; got != want {
		t.Fatalf("start dispatch order = %q, want %q", got, want)
	}
	if auth != "Bearer service-token" {
		t.Fatalf("unexpected auth: %s", auth)
	}
	got := strings.Join(paths, ",")
	for _, want := range []string{"/streams/start", "/jobs/start"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing path %s in %s", want, got)
		}
	}
	if payloads["worker"]["overlay_profile_id"] != "overlay-prof-01" || payloads["worker"]["caption_profile_id"] != "caption-prof-01" {
		t.Fatalf("worker profile IDs were not dispatched: %#v", payloads["worker"])
	}
	if payloads["encoder_recorder"]["encoder_profile_id"] != "enc-prof-01" || payloads["encoder_recorder"]["overlay_profile_id"] != "overlay-prof-01" || payloads["encoder_recorder"]["archive_profile_id"] != "archive-prof-01" {
		t.Fatalf("encoder profile IDs were not dispatched: %#v", payloads["encoder_recorder"])
	}
	if payloads["encoder_recorder"]["stream_key"] != nil || payloads["encoder_recorder"]["stream_key_secret_name"] != "youtube_stream_key_main" {
		t.Fatalf("encoder stream key secret was not dispatched safely: %#v", payloads["encoder_recorder"])
	}
	youtubeRuntime, ok := payloads["encoder_recorder"]["youtube_runtime"].(map[string]any)
	if !ok || youtubeRuntime["broadcast_id"] != "dry-broadcast-01" || youtubeRuntime["dry_run"] != true {
		t.Fatalf("encoder youtube runtime was not dispatched: %#v", payloads["encoder_recorder"])
	}
	archiveConfig, ok := payloads["encoder_recorder"]["archive_config"].(map[string]any)
	if !ok || archiveConfig["folder_id"] != "drive-folder-id" || archiveConfig["shared_drive"] != true {
		t.Fatalf("encoder archive config was not dispatched: %#v", payloads["encoder_recorder"])
	}
	if payloads["discord_bot"]["encoder_audio_url"] != server.URL {
		t.Fatalf("discord bot did not receive encoder audio URL: %#v", payloads["discord_bot"])
	}
	if payloads["discord_bot"]["guild_id"] != "guild" || payloads["discord_bot"]["voice_channel_id"] != "voice" || payloads["discord_bot"]["text_channel_id"] != "text" {
		t.Fatalf("discord bot did not receive stream-specific Discord channel IDs: %#v", payloads["discord_bot"])
	}
	if payloads["discord_bot"]["worker_events_url"] != server.URL {
		t.Fatalf("discord bot did not receive assigned worker event URL: %#v", payloads["discord_bot"])
	}
	if payloads["discord_bot"]["job_generation"] != float64(17) {
		t.Fatalf("discord bot did not receive the Worker job generation: %#v", payloads["discord_bot"])
	}
	if payloads["discord_bot"]["caption_audio_url"] != server.URL {
		t.Fatalf("discord bot did not receive assigned worker caption audio URL: %#v", payloads["discord_bot"])
	}
	if token, ok := payloads["worker"]["stream_ingest_token"].(string); !ok || !strings.HasPrefix(token, "ast_ingest_v1.") {
		t.Fatalf("worker did not receive signed ingest token: %#v", payloads["worker"])
	}
	if token, ok := payloads["discord_bot"]["stream_ingest_token"].(string); !ok || !strings.HasPrefix(token, "ast_ingest_v1.") {
		t.Fatalf("discord bot did not receive signed ingest token: %#v", payloads["discord_bot"])
	}
	workerEventsToken, ok := payloads["discord_bot"]["worker_events_token"].(string)
	if !ok || !strings.HasPrefix(workerEventsToken, "ast_ingest_v1.") {
		t.Fatalf("discord bot did not receive signed worker event token: %#v", payloads["discord_bot"])
	}
	claims, err := ingesttoken.Verify("test-ingest-signing-key", workerEventsToken, ingesttoken.Expected{
		StreamID:    "stream-01",
		ServiceID:   "discord-01",
		ServiceType: "discord_bot",
		Purpose:     "worker_events",
		Audience:    "worker",
	})
	if err != nil || claims.StreamID != "stream-01" {
		t.Fatalf("discord worker event token claims mismatch: claims=%#v err=%v", claims, err)
	}
	captionAudioToken, ok := payloads["discord_bot"]["caption_audio_token"].(string)
	if !ok || !strings.HasPrefix(captionAudioToken, "ast_ingest_v1.") {
		t.Fatalf("discord bot did not receive signed caption audio token: %#v", payloads["discord_bot"])
	}
	captionClaims, err := ingesttoken.Verify("test-ingest-signing-key", captionAudioToken, ingesttoken.Expected{
		StreamID: "stream-01", ServiceID: "discord-01", ServiceType: "discord_bot", Purpose: "caption_audio", Audience: "worker",
	})
	if err != nil || captionClaims.StreamID != "stream-01" {
		t.Fatalf("discord caption audio token claims mismatch: claims=%#v err=%v", captionClaims, err)
	}
	if _, ok := payloads["encoder_recorder"]["stream_ingest_token"]; ok {
		t.Fatalf("encoder start payload must not receive ingest token: %#v", payloads["encoder_recorder"])
	}
	if _, ok := payloads["encoder_recorder"]["worker_video_ingest"]; ok {
		t.Fatalf("legacy start must not opt into Worker video ingest: %#v", payloads["encoder_recorder"])
	}
	if _, ok := payloads["worker"]["video_ingest_url"]; ok {
		t.Fatalf("legacy start must not dispatch a Worker video route: %#v", payloads["worker"])
	}
}

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
			w.WriteHeader(http.StatusAccepted)
		case payload["guild_id"] != nil:
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
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	}
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, services, StartRequest{
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
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	}, StartRequest{EncoderProfileID: "enc-prof-01", EncoderVideoWidth: 1920, EncoderVideoHeight: 1080, EncoderVideoFPS: 60})
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

func TestStartStopsAtFirstFailedDependency(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/streams/start" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		t.Fatalf("downstream start must not continue after encoder failure: %s", r.URL.Path)
	}))
	defer server.Close()

	client := testClient()
	results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	}, StartRequest{})
	if len(results) != 1 {
		t.Fatalf("expected only the failed encoder result, got %#v", results)
	}
	if results[0].ServiceType != "encoder_recorder" || results[0].Success {
		t.Fatalf("unexpected failed encoder result: %#v", results[0])
	}
	if got, want := strings.Join(paths, ","), "/streams/start"; got != want {
		t.Fatalf("start dispatch continued after failure: got %q want %q", got, want)
	}
}

func TestStartPayloadOmitsCaptionRouteWhenCaptionProfileIsNotSelected(t *testing.T) {
	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	_, payloadValue, ok := client.startPayload(
		store.Stream{ID: "stream-01"},
		store.RegisteredService{ServiceID: "discord-01", ServiceType: "discord_bot"},
		StartRequest{},
		"https://encoder.example.com",
		store.RegisteredService{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"},
		time.Now().UTC(),
	)
	if !ok {
		t.Fatal("discord start payload was not built")
	}
	payload := payloadValue.(map[string]any)
	if _, exists := payload["caption_audio_url"]; exists {
		t.Fatalf("caption route must be omitted without a caption profile: %#v", payload)
	}
	if _, exists := payload["caption_audio_token"]; exists {
		t.Fatalf("caption token must be omitted without a caption profile: %#v", payload)
	}
}

func TestStartUsesEncryptedNodeRuntimeTokenBeforeGlobalFallback(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	ciphertext, nonce, err := security.EncryptSecret("node-runtime-token", "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	client := testClient()
	client.Config.NodeTokenKey = "secret-key"
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{{
		ServiceID:           "enc-01",
		ServiceType:         "encoder_recorder",
		PublicURL:           server.URL,
		NodeTokenCiphertext: ciphertext,
		NodeTokenNonce:      nonce,
	}}, StartRequest{EncoderProfileID: "enc-prof-01"})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("dispatch failed: %#v", results)
	}
	if auth != "Bearer node-runtime-token" {
		t.Fatalf("unexpected auth: %q", auth)
	}
}

func TestDispatchErrorDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service-token", http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient()
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, StartRequest{})
	if len(results) != 1 || results[0].Success {
		t.Fatalf("expected failed result: %#v", results)
	}
	if strings.Contains(results[0].Error, "service-token") {
		t.Fatalf("token leaked in error: %#v", results[0])
	}
}

func TestRetryArchiveUploadDispatchesOnlyToEncoder(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		archiveConfig, _ := payload["archive_config"].(map[string]any)
		if payload["stream_id"] != "stream-01" || payload["name"] != "Morning" || archiveConfig["folder_id_secret_name"] != "drive_destination:dest-01:folder_id" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := testClient()
	results := client.RetryArchiveUpload(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
	}, map[string]any{"folder_id_secret_name": "drive_destination:dest-01:folder_id", "shared_drive": true})
	if len(results) != 1 || !results[0].Success || strings.Join(paths, ",") != "/streams/package" {
		t.Fatalf("unexpected retry dispatch: results=%#v paths=%#v", results, paths)
	}
}

func TestRetryArchiveUploadCapturesSafePackageFailureClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"package_failed","failure_phase":"upload","error_class":"archive_upload_failed","error":"service-token"}`))
	}))
	defer server.Close()
	client := testClient()
	results := client.RetryArchiveUpload(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	}, nil)
	if len(results) != 1 || results[0].Success {
		t.Fatalf("expected failed retry dispatch: %#v", results)
	}
	result := results[0]
	if result.Code != "package_failed" || result.FailurePhase != "upload" || result.ErrorClass != "archive_upload_failed" {
		t.Fatalf("expected package failure classification: %#v", result)
	}
	if strings.Contains(result.Error, "service-token") {
		t.Fatalf("token leaked in dispatch error: %#v", result)
	}
}

func TestAudioStatusFetchesAssignedEncoderStatus(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stream_id":"stream-01","bridge_active":true,"started_at":"2026-05-28T00:00:00Z","packets_total":3,"rtp_forwarded":3,"last_packet_age_sec":0}`))
	}))
	defer server.Close()

	client := testClient()
	result := client.AudioStatus(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	})
	if !result.Success || result.AudioBridgeState.PacketsTotal != 3 || result.AudioBridgeState.RTPForwarded != 3 {
		t.Fatalf("unexpected audio status result: %#v", result)
	}
	if gotPath != "/streams/stream-01/audio-status" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
}

func TestWorkerEventsFetchesAssignedEncoderEvents(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"id":"event-01","stream_id":"stream-01","type":"caption.telop","payload":{"text":"こんにちは"},"timestamp":"2026-06-01T00:00:00Z"}]}`))
	}))
	defer server.Close()

	client := testClient()
	result := client.WorkerEvents(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	})
	if !result.Success || len(result.Events) != 1 || result.Events[0].Type != "caption.telop" {
		t.Fatalf("unexpected worker events result: %#v", result)
	}
	if gotPath != "/streams/stream-01/worker-events" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
}

func TestWorkerEventsRedactsUpstreamSecretLikePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"id":"event-01","stream_id":"stream-01","type":"overlay.custom","payload":{"text":"safe","target":"https://example.com/callback?api_key=upstream-secret","nested":{"message":"Bearer upstream-secret-token"},"webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token"},"timestamp":"2026-06-01T00:00:00Z"}]}`))
	}))
	defer server.Close()

	client := testClient()
	result := client.WorkerEvents(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	})
	if !result.Success || len(result.Events) != 1 {
		t.Fatalf("unexpected worker events result: %#v", result)
	}
	text, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"upstream-secret", "api_key=", "discord.com/api/webhooks", "Bearer"} {
		if strings.Contains(string(text), raw) {
			t.Fatalf("worker event secret-like payload leaked: %s", text)
		}
	}
	if !strings.Contains(string(text), `"text":"safe"`) || !strings.Contains(string(text), "redacted") {
		t.Fatalf("safe worker event fields were not preserved with redaction: %s", text)
	}
}

func TestEncoderPreflightFetchesAssignedEncoderPreflight(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":false,"checked_at":"2026-06-05T00:00:00Z","checks":[{"id":"ffmpeg_binary","status":"ok","severity":"critical","message":"ffmpeg is available"},{"id":"youtube_stream_key","status":"missing","severity":"critical","message":"YOUTUBE_STREAM_KEY is not configured"}],"summary":{"ffmpeg_bin":"ffmpeg","archive_root":"C:\\archives"}}`))
	}))
	defer server.Close()

	client := testClient()
	result := client.EncoderPreflight(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	})
	if !result.Success || result.Ready || len(result.Checks) != 2 || result.Checks[1].ID != "youtube_stream_key" {
		t.Fatalf("unexpected preflight result: %#v", result)
	}
	if gotPath != "/preflight" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
}

func TestEncoderPreflightRedactsUpstreamSecretLikeFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ready":false,
			"checked_at":"2026-06-05T00:00:00Z",
			"checks":[
				{"id":"youtube_stream_key","status":"missing","severity":"critical","message":"YOUTUBE_STREAM_KEY is not configured"},
				{"id":"auth_check","status":"warning","severity":"warning","message":"Authorization Bearer service-token"}
			],
			"summary":{
				"ffmpeg_bin":"ffmpeg",
				"archive_root":"C:\\archives",
				"stream_key":"super-secret-stream-key",
				"google_drive_folder_id":"drive-folder-secret-id",
				"credential_url":"rtsp://user:password@camera.example.com/live",
				"nested":{"webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token"},
				"messages":["ok","Bearer nested-secret-token"]
			}
		}`))
	}))
	defer server.Close()

	client := testClient()
	result := client.EncoderPreflight(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	})
	if !result.Success || len(result.Checks) != 2 || result.Checks[0].ID != "youtube_stream_key" {
		t.Fatalf("unexpected preflight result: %#v", result)
	}
	text, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"service-token", "super-secret-stream-key", "drive-folder-secret-id", "password@camera", "upstream-secret-token", "nested-secret-token", "discord.com/api/webhooks"} {
		if strings.Contains(string(text), raw) {
			t.Fatalf("upstream secret leaked in preflight result: %s", text)
		}
	}
	if !strings.Contains(string(text), `"id":"youtube_stream_key"`) || !strings.Contains(string(text), `"ffmpeg_bin":"ffmpeg"`) {
		t.Fatalf("safe preflight fields were unexpectedly removed: %s", text)
	}
}

func TestSendWorkerEventDispatchesToAssignedWorker(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := testClient()
	result := client.SendWorkerEvent(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
	}, WorkerEventRequest{EventType: "caption", Text: "hello", SpeakerUserID: "user-01"})
	if !result.Success {
		t.Fatalf("unexpected dispatch result: %#v", result)
	}
	if gotPath != "/streams/stream-01/events/caption" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("unexpected auth: %s", gotAuth)
	}
	if gotPayload["text"] != "hello" || gotPayload["speaker_user_id"] != "user-01" {
		t.Fatalf("unexpected payload: %#v", gotPayload)
	}
}

func TestSendWorkerEventRejectsUnsupportedType(t *testing.T) {
	client := testClient()
	result := client.SendWorkerEvent(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"},
	}, WorkerEventRequest{EventType: "bad"})
	if result.Success || !strings.Contains(result.Error, "unsupported") {
		t.Fatalf("expected unsupported event type: %#v", result)
	}
}

func TestAudioStatusFailureDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service-token", http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient()
	result := client.AudioStatus(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}})
	if result.Success || !strings.Contains(result.Error, "403") {
		t.Fatalf("expected failed result: %#v", result)
	}
	if strings.Contains(result.Error, "service-token") {
		t.Fatalf("token leaked in error: %#v", result)
	}
}

func TestWorkerEventsFailureDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service-token", http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient()
	result := client.WorkerEvents(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}})
	if result.Success || !strings.Contains(result.Error, "403") {
		t.Fatalf("expected failed result: %#v", result)
	}
	if strings.Contains(result.Error, "service-token") {
		t.Fatalf("token leaked in error: %#v", result)
	}
}

func TestEncoderPreflightFailureDoesNotLeakToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service-token", http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient()
	result := client.EncoderPreflight(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}})
	if result.Success || !strings.Contains(result.Error, "403") {
		t.Fatalf("expected failed result: %#v", result)
	}
	if strings.Contains(result.Error, "service-token") {
		t.Fatalf("token leaked in error: %#v", result)
	}
}

func TestDispatchDoesNotFollowRedirectWithServiceToken(t *testing.T) {
	var redirectedAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/streams/start", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	client := testClient()
	results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: redirector.URL}}, StartRequest{})
	if len(results) != 1 {
		t.Fatalf("expected one dispatch result, got %#v", results)
	}
	if results[0].Success {
		t.Fatalf("redirect response must not be treated as a successful dispatch: %#v", results[0])
	}
	if results[0].StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("expected original redirect status, got %#v", results[0])
	}
	if redirectedAuth != "" {
		t.Fatalf("service token was forwarded to redirect target: %q", redirectedAuth)
	}
}

func TestDisabledClientReturnsFailureWithoutRequest(t *testing.T) {
	client := Client{}
	results := client.Stop(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"}})
	if len(results) != 1 || results[0].Success || !strings.Contains(results[0].Error, "SERVICE_CALL_TOKEN") {
		t.Fatalf("unexpected result: %#v", results)
	}
}

func TestStopExtendsOnlyEncoderTimeoutWithoutMutatingSuppliedHTTPClient(t *testing.T) {
	const normalTimeout = time.Second
	transport := &requestDeadlineTransport{}
	suppliedHTTPClient := &http.Client{Transport: transport, Timeout: normalTimeout}
	client := testClient()
	client.Config.Timeout = normalTimeout
	client.HTTP = suppliedHTTPClient
	stream := store.Stream{ID: "stream-01"}

	for _, service := range []store.RegisteredService{
		{ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"},
	} {
		results := client.Stop(t.Context(), stream, []store.RegisteredService{service})
		if len(results) != 1 || !results[0].Success {
			t.Fatalf("stop %s failed: %#v", service.ServiceType, results)
		}
	}
	startResults := client.Start(t.Context(), stream, []store.RegisteredService{{
		ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com",
	}}, StartRequest{})
	if len(startResults) != 1 || !startResults[0].Success {
		t.Fatalf("start failed: %#v", startResults)
	}

	if client.Config.Timeout != normalTimeout {
		t.Fatalf("client config timeout was mutated: got %s want %s", client.Config.Timeout, normalTimeout)
	}
	if suppliedHTTPClient.Timeout != normalTimeout {
		t.Fatalf("supplied HTTP client timeout was mutated: got %s want %s", suppliedHTTPClient.Timeout, normalTimeout)
	}
	if len(transport.requests) != 4 {
		t.Fatalf("request count = %d, want 4", len(transport.requests))
	}
	assertRequestTimeout(t, transport.requests[0], 15*time.Second)
	assertRequestTimeout(t, transport.requests[1], normalTimeout)
	assertRequestTimeout(t, transport.requests[2], normalTimeout)
	assertRequestTimeout(t, transport.requests[3], normalTimeout)
}

func TestStopEncoderUsesLongerConfiguredTimeout(t *testing.T) {
	const configuredTimeout = 20 * time.Second
	transport := &requestDeadlineTransport{}
	suppliedHTTPClient := &http.Client{Transport: transport, Timeout: time.Second}
	client := testClient()
	client.Config.Timeout = configuredTimeout
	client.HTTP = suppliedHTTPClient

	results := client.Stop(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{
		ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com",
	}})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("encoder stop failed: %#v", results)
	}
	if suppliedHTTPClient.Timeout != time.Second {
		t.Fatalf("supplied HTTP client timeout was mutated: got %s want %s", suppliedHTTPClient.Timeout, time.Second)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(transport.requests))
	}
	assertRequestTimeout(t, transport.requests[0], configuredTimeout)
}

func TestPreviewAssetUsesAssignedEncoderTokenAndBoundsResponse(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nsegment-000001.ts\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/index.m3u8" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Fatalf("unexpected preview authorization: %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("Accept"), "mpegurl") {
			t.Fatalf("unexpected preview accept header: %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(playlist))
	}))
	defer server.Close()

	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "index.m3u8", "")
	if !result.Success || string(result.Body) != playlist || result.ContentType != "application/vnd.apple.mpegurl" {
		t.Fatalf("unexpected preview result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetRejectsOversizedPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxPreviewPlaylistBytes+1)))
	}))
	defer server.Close()
	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "index.m3u8", "")
	if result.Success || result.Code != "preview_asset_too_large" || len(result.Body) != 0 {
		t.Fatalf("oversized preview was accepted: %#v", result)
	}
}

func TestPreviewAssetForwardsSingleRangeAndPreservesPartialResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/segment-000001.ts" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Fatalf("unexpected preview authorization: %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("Range"); got != "bytes=1-3" {
			t.Fatalf("range = %q, want bytes=1-3", got)
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 1-3/6")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("bcd"))
	}))
	defer server.Close()

	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=1-3")
	if !result.Success || result.StatusCode != http.StatusPartialContent || result.ContentRange != "bytes 1-3/6" || result.AcceptRanges != "bytes" || string(result.Body) != "bcd" {
		t.Fatalf("unexpected partial preview result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetPreservesUnsatisfiedRangeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/segment-000001.ts" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Range"); got != "bytes=99-" {
			t.Fatalf("range = %q, want bytes=99-", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes */6")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()

	result := testClient().PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=99-")
	if !result.Success || result.StatusCode != http.StatusRequestedRangeNotSatisfiable || result.ContentRange != "bytes */6" || result.AcceptRanges != "bytes" || len(result.Body) != 0 {
		t.Fatalf("unexpected unsatisfied range result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetDoesNotForwardInvalidOrMultiRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "" {
			t.Fatalf("unexpected range forwarding: %q", got)
		}
		_, _ = w.Write([]byte("segment"))
	}))
	defer server.Close()

	result := testClient().PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=0-1,4-5")
	if !result.Success {
		t.Fatalf("invalid range request failed unexpectedly: %#v", result)
	}
}

func TestDownloadArchiveArtifactForwardsRangeAndKeepsStreamingBodyAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/artifacts/final.mp4" {
			t.Fatalf("unexpected archive path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer service-token" {
			t.Fatalf("unexpected archive authorization: %q", got)
		}
		if got := r.Header.Get("Range"); got != "bytes=0-3" {
			t.Fatalf("archive range = %q, want bytes=0-3", got)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.WriteHeader(http.StatusPartialContent)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()

	client := testClient()
	client.Config.Timeout = 10 * time.Millisecond
	client.HTTP = server.Client()
	result := client.DownloadArchiveArtifact(
		t.Context(),
		store.Stream{ID: "stream-01"},
		[]store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}},
		store.StreamArtifact{ID: "artifact-01", StreamID: "stream-01", Kind: "archive", Name: "final.mp4"},
		"bytes=0-3",
	)
	if !result.Success || result.StatusCode != http.StatusPartialContent || result.ContentRange != "bytes 0-3/8" || result.AcceptRanges != "bytes" || result.Body == nil {
		t.Fatalf("unexpected archive result: %#v", result)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("archive body was cancelled before streaming completed: %v", err)
	}
	if string(body) != "data" {
		t.Fatalf("archive body = %q, want data", string(body))
	}
}

func TestNotifyDiscordYouTubeLiveDoesNotRetryAmbiguousFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/streams/stream-01/notifications/youtube-live" {
			t.Fatalf("unexpected notification path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Fatalf("unexpected notification authorization: %q", r.Header.Get("Authorization"))
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["event_id"] != "youtube-live-event-01" || payload["watch_url"] != "https://www.youtube.com/watch?v=video_01" {
			t.Fatalf("unexpected notification payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "discord_unavailable"})
	}))
	defer server.Close()

	client := testClient()
	result := client.NotifyDiscordYouTubeLive(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL}}, "youtube-live-event-01", "https://www.youtube.com/watch?v=video_01")
	if result.Success || result.StatusCode != http.StatusServiceUnavailable || result.Code != "discord_unavailable" || attempts != 1 {
		t.Fatalf("ambiguous notification must make exactly one request: attempts=%d result=%#v", attempts, result)
	}
}

func TestNotifyDiscordYouTubeLivePreservesBareRateLimitForDurableRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// A proxy or a minimal Bot deployment can return a real 429 without a
		// JSON body. The durable outbox must still recognize this as an explicit
		// pre-send rate-limit rejection, rather than losing it as a generic error.
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	result := testClient().NotifyDiscordYouTubeLive(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL}}, "youtube-live-event-01", "https://www.youtube.com/watch?v=video_01")
	if result.Success || result.StatusCode != http.StatusTooManyRequests || result.Code != "" || attempts != 1 {
		t.Fatalf("bare rate limit classification was lost: attempts=%d result=%#v", attempts, result)
	}
}

func TestNotifyDiscordYouTubeLiveDoesNotRetryPermanentFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "stream_not_assigned_to_service"})
	}))
	defer server.Close()

	client := testClient()
	result := client.NotifyDiscordYouTubeLive(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL}}, "youtube-live-event-01", "https://www.youtube.com/watch?v=video_01")
	if result.Success || result.StatusCode != http.StatusForbidden || result.Code != "stream_not_assigned_to_service" || attempts != 1 {
		t.Fatalf("unexpected permanent failure result: attempts=%d result=%#v", attempts, result)
	}
}

func TestStartReadinessIssues(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Minute)
	client := Client{}
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com", Status: "online"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "ftp://worker.example.com", Status: "online"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com", Status: "online", LastHeartbeatAt: &stale, Capabilities: map[string]any{"audio_stream_forward": false}},
	}, StartRequest{}, now)
	for _, want := range []string{"service_call_token_missing", "stream_ingest_signing_key_missing", "service_public_url_invalid", "service_heartbeat_stale", "discord_audio_forward_unavailable"} {
		if !hasIssueCode(issues, want) {
			t.Fatalf("missing readiness issue %s in %#v", want, issues)
		}
	}
}

func TestStartReadinessAllowsUnknownAudioForwardCapability(t *testing.T) {
	now := time.Now().UTC()
	client := Client{Config: Config{Token: "service-token", IngestTokenSigningKey: "stream-ingest-signing-key"}}
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
	client := Client{Config: Config{Token: "service-token", IngestTokenSigningKey: "stream-ingest-signing-key"}}
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

func TestStartReadinessBlocksDisabledDiscordAudioCapture(t *testing.T) {
	now := time.Now().UTC()
	client := Client{Config: Config{Token: "service-token"}}
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
	client := Client{Config: Config{Token: "service-token"}}
	issues := client.StartReadinessIssues([]store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "http://169.254.169.254", Status: "online"},
	}, StartRequest{EncoderInputURL: "srt://input.example.com:9000"}, time.Now().UTC())
	if !hasIssueCode(issues, "service_public_url_blocked") {
		t.Fatalf("missing blocked URL readiness issue: %#v", issues)
	}
}

func testClient() Client {
	return Client{Config: Config{
		Token:   "service-token",
		Timeout: time.Second,
		URLPolicy: netpolicy.ServiceURLPolicy{
			AllowedHosts: map[string]struct{}{"127.0.0.1": {}},
		},
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
