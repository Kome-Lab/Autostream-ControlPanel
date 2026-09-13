package servicecall

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRetryArchiveUploadDispatchesOnlyToEncoder(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["archive_config"]; ok {
			t.Error("package request must not contain internal archive config")
		}
		if payload["stream_id"] != "stream-01" || payload["name"] != "Morning" || payload["archive_run_id"] != "run-01" || payload["started_at"] != "2026-08-18T05:06:29Z" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := testClient()
	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 0, time.UTC)
	results := client.RetryArchiveUpload(t.Context(), store.Stream{ID: "stream-01", Name: "Morning", ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt}, []store.RegisteredService{
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
	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 0, time.UTC)
	results := client.RetryArchiveUpload(t.Context(), store.Stream{ID: "stream-01", Name: "Morning", ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt}, []store.RegisteredService{
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
	if len(results) != 1 || results[0].Success || !strings.Contains(results[0].Error, "node runtime token encryption key") {
		t.Fatalf("unexpected result: %#v", results)
	}
}
