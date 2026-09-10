package servicecall

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

// Kept identical to the Encoder handler fixture in archive_wire_request_test.go.
// This is the full canonical body captured from Client.Start, including the
// desired visual epoch and non-secret YouTube lifecycle metadata.
const canonicalArchiveStartWire = `{
  "stream_id":"stream-01","name":"Morning",
  "input_url":"srt://input.example.com:9000","rtmp_url":"rtmps://youtube.example.com/live2",
  "stream_key_secret_name":"youtube_stream_key:stream-01",
  "encoder_profile_id":"enc-profile-01","overlay_profile_id":"overlay-profile-01",
  "encoder_audio_gain_db":-3.5,"archive_profile_id":"archive-profile-01",
  "archive_run_id":"run-01","started_at":"2026-08-18T05:06:29.123456789Z",
  "youtube_runtime":{"mode":"live_api","output_id":"output-01","oauth_account_id":"account-01",
    "broadcast_id":"broadcast01","live_stream_id":"live01","rtmp_url":"rtmps://youtube.example.com/live2",
    "stream_key_secret_name":"youtube_stream_key:stream-01","watch_url":"https://www.youtube.com/watch?v=broadcast01",
    "dry_run":false,"complete_on_stop":true},
  "video_cover_start":{"job_generation":17,"revision":3,"active":false,"idempotency_key":"cover-start-17"}
}`

const canonicalArchivePackageWire = `{
  "stream_id":"stream-01","name":"Morning","archive_run_id":"run-01",
  "started_at":"2026-08-18T05:06:29.123456789Z","dry_run":false
}`

func TestArchiveWireActualProducerPreservesCanonicalBodies(t *testing.T) {
	var received []struct {
		path string
		body map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("producer sent invalid JSON")
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Error("producer lost service authentication")
		}
		received = append(received, struct {
			path string
			body map[string]any
		}{r.URL.Path, body})
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 123456789, time.UTC)
	stream := store.Stream{ID: "stream-01", Name: "Morning", ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt}
	services := []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL,
		ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}}
	internalConfig := map[string]any{
		"archive_profile_id": "archive-profile-01", "drive_destination_id": "dest-01",
		"auth_mode": "oauth2", "oauth_account_id": "account-01", "oauth_provider_id": "provider-01",
		"folder_id_secret_name":     "drive_destination:dest-01:folder_id",
		"client_secret_secret_name": "oauth_provider:provider-01:client_secret",
		"refresh_token_secret_name": "oauth_account:account-01:refresh_token",
		"archive_file_name":         "Recording.mp4", "retention_days": 45, "shared_drive": true,
	}
	before, err := json.Marshal(internalConfig)
	if err != nil {
		t.Fatal(err)
	}
	var expectedStart, expectedPackage map[string]any
	if err := json.Unmarshal([]byte(canonicalArchiveStartWire), &expectedStart); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(canonicalArchivePackageWire), &expectedPackage); err != nil {
		t.Fatal(err)
	}
	client := testClient()
	results := client.Start(t.Context(), stream, services, StartRequest{
		EncoderInputURL: "srt://input.example.com:9000", EncoderRTMPURL: "rtmps://youtube.example.com/live2",
		EncoderStreamKeySecretName: "youtube_stream_key:stream-01", EncoderProfileID: "enc-profile-01",
		OverlayProfileID: "overlay-profile-01", EncoderAudioGainDB: -3.5, ArchiveProfileID: "archive-profile-01",
		ArchiveRunID: stream.ArchiveRunID, ArchiveStartedAt: startedAt, ArchiveConfig: internalConfig,
		YouTubeRuntime:  expectedStart["youtube_runtime"].(map[string]any),
		VideoCoverStart: &VideoCoverStartSnapshot{JobGeneration: 17, Revision: 3, Active: false, IdempotencyKey: "cover-start-17"},
	})
	if len(results) != 1 || !results[0].Success {
		t.Fatal("canonical start dispatch failed")
	}
	results = client.RetryArchiveUpload(t.Context(), stream, services, internalConfig)
	if len(results) != 1 || !results[0].Success {
		t.Fatal("canonical package dispatch failed")
	}
	if len(received) != 2 {
		t.Fatalf("request count=%d, want 2", len(received))
	}
	for i, expected := range []map[string]any{expectedStart, expectedPackage} {
		if received[i].path != []string{"/streams/start", "/streams/package"}[i] || !reflect.DeepEqual(received[i].body, expected) {
			t.Errorf("request %d differs from the canonical Encoder fixture", i)
		}
		for _, key := range []string{"archive_config", "base_path", "stream_key"} {
			if _, exists := received[i].body[key]; exists {
				t.Errorf("request %d contains forbidden field %s", i, key)
			}
		}
	}
	after, err := json.Marshal(internalConfig)
	if err != nil || !bytes.Equal(before, after) {
		t.Error("wire dispatch changed internal archive configuration")
	}
}
