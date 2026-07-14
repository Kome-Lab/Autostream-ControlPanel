package servicecall

import (
	"encoding/json"
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
		case payload["overlay_profile_id"] != nil:
			payloads["worker"] = payload
		case payload["encoder_profile_id"] != nil:
			payloads["encoder_recorder"] = payload
		case payload["guild_id"] != nil:
			payloads["discord_bot"] = payload
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := testClient()
	client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
	client.Config.IngestTokenTTL = time.Hour
	services := []store.RegisteredService{
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL},
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
	if payloads["encoder_recorder"]["encoder_profile_id"] != "enc-prof-01" || payloads["encoder_recorder"]["archive_profile_id"] != "archive-prof-01" {
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

func hasIssueCode(issues []ReadinessIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
