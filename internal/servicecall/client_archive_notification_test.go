package servicecall

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDownloadArchiveArtifactForwardsRangeAndKeepsStreamingBodyAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/archive-runs/run-01/artifacts/final.mp4" {
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

	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 0, time.UTC)
	client := testClient()
	client.Config.Timeout = 10 * time.Millisecond
	client.HTTP = server.Client()
	result := client.DownloadArchiveArtifact(
		t.Context(),
		store.Stream{ID: "stream-01", ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt},
		[]store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}},
		store.StreamArtifact{ID: "artifact-01", StreamID: "stream-01", ArchiveRunID: "run-01", ArchiveStartedAt: &startedAt, Kind: "archive", Name: "final.mp4"},
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

func TestDownloadArchiveArtifactUsesRunScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/archive-runs/run-01/artifacts/final.mp4" {
			t.Fatalf("unexpected archive path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()
	result := testClient().DownloadArchiveArtifact(
		t.Context(), store.Stream{ID: "stream-01"},
		[]store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}},
		store.StreamArtifact{ID: "artifact-01", StreamID: "stream-01", ArchiveRunID: "run-01", Kind: "archive", Name: "final.mp4"}, "",
	)
	if !result.Success || result.Body == nil {
		t.Fatalf("run-scoped archive result = %#v", result)
	}
	defer result.Body.Close()
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
