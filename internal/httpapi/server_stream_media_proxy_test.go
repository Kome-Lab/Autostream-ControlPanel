package httpapi

import (
	"bytes"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamAudioStatusRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/audio-status", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamAudioStatusProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{audioStatus: servicecall.AudioStatusResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/streams/" + stream.ID + "/audio-status",
		StatusCode:  http.StatusOK,
		Success:     true,
		AudioBridgeState: servicecall.AudioBridgeStatus{
			StreamID:         stream.ID,
			BridgeActive:     true,
			PacketsTotal:     2,
			RTPForwarded:     2,
			LastPacketAgeSec: 0,
		},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/audio-status", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.audioStatusCalls != 1 {
		t.Fatalf("audio status dispatcher was not called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if !strings.Contains(res.Body.String(), `"packets_total":2`) || strings.Contains(res.Body.String(), "service-token") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
}

func TestStreamEncoderPreflightRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/encoder-preflight", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamEncoderPreflightProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{encoderPreflight: servicecall.ServicePreflightResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/preflight",
		StatusCode:  http.StatusOK,
		Success:     true,
		Ready:       false,
		CheckedAt:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Checks: []servicecall.ServicePreflightCheck{{
			ID:       "ffmpeg_binary",
			Status:   "ok",
			Severity: "critical",
			Message:  "ffmpeg is available.",
		}, {
			ID:       "youtube_stream_key",
			Status:   "missing",
			Severity: "critical",
			Message:  "YOUTUBE_STREAM_KEY is not configured.",
		}, {
			ID:       "auth_check",
			Status:   "warning",
			Severity: "warning",
			Message:  "Authorization Bearer service-token",
		}},
		Summary: map[string]any{
			"ffmpeg_bin":             "ffmpeg",
			"archive_root":           "/var/lib/autostream/archives",
			"stream_key":             "super-secret-stream-key",
			"google_drive_folder_id": "drive-folder-secret-id",
			"credential_url":         "rtsp://user:password@camera.example.com/live",
			"nested": map[string]any{
				"webhook_url": "https://discord.com/api/webhooks/id/upstream-secret-token",
			},
			"messages": []any{"ok", "Bearer nested-secret-token"},
		},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/encoder-preflight", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.encoderPreflightCalls != 1 {
		t.Fatalf("encoder preflight dispatcher was not called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if !strings.Contains(res.Body.String(), `"id":"youtube_stream_key"`) || !strings.Contains(res.Body.String(), `"ffmpeg_bin":"ffmpeg"`) {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
	for _, raw := range []string{"service-token", "super-secret-stream-key", "drive-folder-secret-id", "password@camera", "upstream-secret-token", "nested-secret-token", "discord.com/api/webhooks"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("encoder preflight leaked upstream secret %q in response: %s", raw, res.Body.String())
		}
	}
}

func TestStreamEncoderPreflightRejectsLegacyRelayLiveAPIWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "legacy relay preflight")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy relay live api", map[string]any{"mode": "live_api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{"output_relay_mode": "legacy_stream_key"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder_recorder-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/encoder-preflight", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "live_api_requires_managed_output_relay") {
		t.Fatalf("legacy relay preflight status=%d body=%s", res.Code, res.Body.String())
	}
	if dispatcher.encoderPreflightCalls != 0 {
		t.Fatalf("legacy relay preflight must fail before encoder dispatch: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestStreamWorkerEventsRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/worker-events", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamWorkerEventsProxiesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{workerEvents: servicecall.WorkerEventsResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Endpoint:    "/streams/" + stream.ID + "/worker-events",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events: []servicecall.WorkerEvent{{
			ID:        "event-01",
			StreamID:  stream.ID,
			Type:      "caption.telop",
			Payload:   map[string]any{"text": "縺薙ｓ縺ｫ縺｡縺ｯ"},
			Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		}, {
			ID:       "event-02",
			StreamID: stream.ID,
			Type:     "overlay.custom",
			Payload: map[string]any{
				"text":        "safe",
				"target":      "https://example.com/callback?api_key=upstream-secret",
				"webhook_url": "https://discord.com/api/webhooks/id/upstream-secret-token",
				"nested":      map[string]any{"message": "Bearer upstream-secret-token"},
			},
			Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		}},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/worker-events", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.workerEventsCalls != 1 {
		t.Fatalf("worker events dispatcher was not called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	for _, raw := range []string{"service-token", "upstream-secret", "api_key=", "discord.com/api/webhooks", "Bearer"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("worker events leaked upstream secret-like payload %q in response: %s", raw, res.Body.String())
		}
	}
	if !strings.Contains(res.Body.String(), `"type":"caption.telop"`) || !strings.Contains(res.Body.String(), `"text":"safe"`) || !strings.Contains(res.Body.String(), "redacted") {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
}

func TestServiceStreamEventsRequireDedicatedScope(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "event stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))

	limitedToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, limitedToken, store.ServiceRegistration{ServiceID: "worker-limited", ServiceType: "worker", ServiceName: "Worker Limited", PublicURL: "https://worker-limited.example.com"})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-limited", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	reqBody := `{"service_id":"worker-limited","stream_id":"` + stream.ID + `","event_type":"worker.overlay","payload":{"ok":true}}`
	req := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+limitedToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "missing_service_scope") {
		t.Fatalf("limited token should be rejected, status = %d body = %s", res.Code, res.Body.String())
	}

	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "worker.events.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{ServiceID: "worker-ok", ServiceType: "worker", ServiceName: "Worker OK", PublicURL: "https://worker-ok.example.com"})
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-ok", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	reqBody = `{"service_id":"worker-ok","stream_id":"` + stream.ID + `","event_type":"worker.overlay","payload":{"ok":true}}`
	req = httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("worker.events.write token should be accepted, status = %d body = %s", res.Code, res.Body.String())
	}
}
