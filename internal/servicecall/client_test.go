package servicecall

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const expectedWorkerStartResponseLimitBytes = 1_048_576
const expectedWorkerStartResponseLimitPlusOneBytes = 1_048_577

func TestWorkerStartResponseLimitIsOneMiB(t *testing.T) {
	if maxWorkerStartResponseBytes != expectedWorkerStartResponseLimitBytes {
		t.Fatalf(
			"maxWorkerStartResponseBytes = %d, want %d",
			maxWorkerStartResponseBytes,
			expectedWorkerStartResponseLimitBytes,
		)
	}
}

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
		case payload["discord_target"] != nil:
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
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL},
	}
	results := client.Start(t.Context(), store.Stream{ID: "stream-01", Name: "Morning"}, services, StartRequest{
		DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002", EncoderInputURL: "srt://input.example.com:9000",
		EncoderStreamKeySecretName: "youtube_stream_key_main", EncoderProfileID: "enc-prof-01", ArchiveProfileID: "archive-prof-01", OverlayProfileID: "overlay-prof-01", CaptionProfileID: "caption-prof-01",
		ArchiveRunID: "run-01", ArchiveStartedAt: time.Date(2026, 8, 18, 5, 6, 29, 123456789, time.UTC),
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
	if _, ok := payloads["encoder_recorder"]["archive_config"]; ok {
		t.Fatal("encoder request must not contain internal archive config")
	}
	if payloads["discord_bot"]["encoder_audio_url"] != server.URL {
		t.Fatalf("discord bot did not receive encoder audio URL: %#v", payloads["discord_bot"])
	}
	discordTarget, ok := payloads["discord_bot"]["discord_target"].(map[string]any)
	resolvedTarget, resolvedOK := discordTarget["resolved"].(map[string]any)
	if !ok || !resolvedOK || discordTarget["revision"] != float64(1) || resolvedTarget["guild_id"] != "1001" || resolvedTarget["voice_channel_id"] != "1003" || resolvedTarget["text_channel_id"] != "1002" {
		t.Fatalf("discord bot did not receive resolved target v2: %#v", payloads["discord_bot"])
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
		t.Fatalf("audio-only start must not opt into Worker video ingest: %#v", payloads["encoder_recorder"])
	}
	if _, ok := payloads["worker"]["video_ingest_url"]; ok {
		t.Fatalf("audio-only start must not dispatch a Worker video route: %#v", payloads["worker"])
	}
}

func TestStartFailsClosedWithoutWorkerJobGeneration(t *testing.T) {
	for _, test := range []struct {
		name         string
		workerResult string
	}{
		{name: "missing generation", workerResult: `{"status":"accepted"}`},
		{name: "zero generation", workerResult: `{"job_generation":0}`},
		{name: "negative generation", workerResult: `{"job_generation":-1}`},
		{name: "fractional generation", workerResult: `{"job_generation":1.5}`},
		{name: "string generation", workerResult: `{"job_generation":"17"}`},
		{name: "uint64 overflow generation", workerResult: `{"job_generation":18446744073709551616}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				switch {
				case payload["overlay_profile_id"] != nil:
					requests["worker"]++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, test.workerResult)
				case payload["discord_target"] != nil:
					requests["discord_bot"]++
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Fatalf("unexpected start payload: %#v", payload)
				}
			}))
			defer server.Close()

			client := testClient()
			results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
				{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
				{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
			}, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002"})

			if requests["worker"] != 1 || requests["discord_bot"] != 0 {
				t.Fatalf("invalid Worker generation dispatch count = %#v, want worker=1 discord_bot=0", requests)
			}
			if len(results) != 1 || results[0].Success || results[0].ServiceType != "worker" || results[0].Code != "worker_job_generation_response_invalid" || results[0].FailurePhase != "protocol" {
				t.Fatalf("invalid Worker generation did not fail closed: %#v", results)
			}
		})
	}

	t.Run("discord bot assignment without worker", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()

		results := testClient().Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
			{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
		}, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002"})

		if requests != 0 {
			t.Fatalf("Discord Bot received %d requests without a Worker generation, want 0", requests)
		}
		if len(results) != 1 || results[0].Success || results[0].ServiceType != "discord_bot" || results[0].Code != "worker_job_generation_unavailable" || results[0].FailurePhase != "pre_dispatch" {
			t.Fatalf("missing Worker generation did not fail closed before Discord Bot dispatch: %#v", results)
		}
	})
}
