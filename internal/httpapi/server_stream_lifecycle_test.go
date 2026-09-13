package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamLifecycleEndpoints(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.create", "streams.start", "streams.stop", "streams.update", "streams.retry_upload", "logs.read", "archives.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "lifecycle discord", "discord_bot-01", "guild-life", "voice-life", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createBody := bytes.NewBufferString(`{"name":"morning stream","discord_config_id":"` + config.ID + `","visual_settings":{"expected_revision":0,"discord_target":{"mode":"manual","guild_id":"1001","text_channel_id":"1002","voice_channel_id":"1003"}}}`)
	createReq := httptest.NewRequest(http.MethodPost, "/streams", createBody)
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Stream
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "created" || created.ID == "" {
		t.Fatalf("bad created stream: %#v", created)
	}
	registerAssignedServices(t, auth, created.ID, requiredStartServiceTypes...)

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/start", nil)
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	var startBody struct {
		Stream   store.Stream                 `json:"stream"`
		Dispatch []servicecall.DispatchResult `json:"dispatch"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&startBody); err != nil {
		t.Fatal(err)
	}
	if startBody.Stream.Status != "live" || len(startBody.Dispatch) != 3 {
		t.Fatalf("expected live status and dispatch, got %#v", startBody)
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected start dispatch, got %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}

	failReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/mark-failed", nil)
	failReq.AddCookie(cookie)
	failReq.Header.Set("X-CSRF-Token", csrf)
	failRes := httptest.NewRecorder()
	handler.ServeHTTP(failRes, failReq)
	if failRes.Code != http.StatusOK {
		t.Fatalf("mark failed status = %d body = %s", failRes.Code, failRes.Body.String())
	}
	var failed store.Stream
	if err := json.NewDecoder(failRes.Body).Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed status, got %#v", failed)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID, nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get stream status = %d body = %s", getRes.Code, getRes.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+created.ID+"/retry-upload", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusAccepted {
		t.Fatalf("retry upload status = %d body = %s", retryRes.Code, retryRes.Body.String())
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID+"/logs", nil)
	logsReq.AddCookie(cookie)
	logsRes := httptest.NewRecorder()
	handler.ServeHTTP(logsRes, logsReq)
	if logsRes.Code != http.StatusOK || !strings.Contains(logsRes.Body.String(), "archive upload retry requested") {
		t.Fatalf("logs status = %d body = %s", logsRes.Code, logsRes.Body.String())
	}
	if strings.Contains(logsRes.Body.String(), `"message":"streams.retry_upload"`) {
		t.Fatalf("successful retry audit was duplicated in stream logs: %s", logsRes.Body.String())
	}

	archiveStartedAt := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)
	archiving, err := streams.PrepareStreamArchiveRun(t.Context(), created.ID, "run-lifecycle", archiveStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: created.ID, ArchiveRunID: archiving.ArchiveRunID, ArchiveStartedAt: archiving.ArchiveStartedAt, Kind: "archive", Name: "final.mp4", RelativePath: "final/" + created.ID + "/run-lifecycle/final.mp4", SizeBytes: 123}); err != nil {
		t.Fatal(err)
	}
	if err := streams.AddArtifact(t.Context(), store.StreamArtifact{StreamID: created.ID, Kind: "archive", Name: "bad", RelativePath: "../secret", SizeBytes: 1}); err == nil {
		t.Fatal("unsafe artifact path was accepted")
	}
	artifactsReq := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID+"/artifacts", nil)
	artifactsReq.AddCookie(cookie)
	artifactsRes := httptest.NewRecorder()
	handler.ServeHTTP(artifactsRes, artifactsReq)
	if artifactsRes.Code != http.StatusOK || !strings.Contains(artifactsRes.Body.String(), "final/"+created.ID+"/run-lifecycle/final.mp4") || strings.Contains(artifactsRes.Body.String(), "secret") {
		t.Fatalf("artifacts status = %d body = %s", artifactsRes.Code, artifactsRes.Body.String())
	}
}

func TestStreamStartPersistsNegotiatedWorkerVideoOverlayBurnIn(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "video discord", "discord_bot-01", "guild", "voice", "")
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "1080p60", map[string]any{"width": 1920, "height": 1080, "fps": 60})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "Worker scene")
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID: discord.ID, EncoderProfileID: encoderProfile.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, registration := range []store.ServiceRegistration{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", ServiceName: "encoder", PublicURL: "https://encoder.example.com", Capabilities: map[string]any{"output_relay_mode": "direct", "worker_frame_ingest_mjpeg_srt": true}},
		{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "worker", PublicURL: "https://worker.example.com", Capabilities: map[string]any{"scene_frames_mjpeg_srt": true}},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", ServiceName: "bot", PublicURL: "https://bot.example.com"},
	} {
		token, err := auth.CreateServiceToken(t.Context(), registration.ServiceType, []string{"service.register"})
		if err != nil {
			t.Fatal(err)
		}
		registerServiceWithTokenForTest(t, auth, token, registration)
		if _, err := auth.AssignServiceToStream(t.Context(), registration.ServiceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", res.Code, res.Body.String())
	}
	runtime, err := streams.GetStreamMediaRuntime(t.Context(), stream.ID)
	if err != nil || !runtime.VideoOverlayBurnIn {
		t.Fatalf("negotiated burn-in state was not persisted: runtime=%#v err=%v", runtime, err)
	}
	if dispatcher.startRequest.EncoderVideoWidth != 1920 || dispatcher.startRequest.EncoderVideoHeight != 1080 || dispatcher.startRequest.EncoderVideoFPS != 60 {
		t.Fatalf("Encoder video profile was not resolved for dispatch: %#v", dispatcher.startRequest)
	}
}

func TestApplyEncoderVideoProfileRejectsUnsupportedWorkerSceneGeometry(t *testing.T) {
	profiles := store.NewMemoryProfileStore()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "unsupported", map[string]any{"width": 1024, "height": 576, "fps": 61})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{profiles: profiles}
	req := servicecall.StartRequest{EncoderProfileID: profile.ID}
	if err := server.applyEncoderVideoProfile(t.Context(), &req); err == nil {
		t.Fatalf("unsupported Worker scene geometry was accepted: %#v", req)
	}
	for index, config := range []map[string]any{
		{"width": 1920, "height": 1080, "fps": 60},
		{"width": 1280, "height": 720, "fps": 30},
		{"width": 854, "height": 480, "fps": 1},
	} {
		valid, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, fmt.Sprintf("valid-%d", index), config)
		if err != nil {
			t.Fatal(err)
		}
		req = servicecall.StartRequest{EncoderProfileID: valid.ID}
		if err := server.applyEncoderVideoProfile(t.Context(), &req); err != nil {
			t.Fatalf("supported Worker scene geometry was rejected: config=%#v err=%v", config, err)
		}
	}
}

func TestStreamStartStopDispatchesAssignedServices(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "dispatch discord", "discord_bot-01", "guild-01", "voice-01", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	startReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","encoder_input_url":"srt://input.example.com:9000"}`))
	startReq.AddCookie(cookie)
	startReq.Header.Set("X-CSRF-Token", csrf)
	startRes := httptest.NewRecorder()
	handler.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", startRes.Code, startRes.Body.String())
	}
	if !strings.Contains(startRes.Body.String(), `"dispatch"`) || !strings.Contains(startRes.Body.String(), `"service_type":"encoder_recorder"`) {
		t.Fatalf("start response does not include dispatch results: %s", startRes.Body.String())
	}
	if dispatcher.startCalls != 1 || dispatcher.startedStream.ID != stream.ID || len(dispatcher.startedServices) != 3 {
		t.Fatalf("dispatcher was not called correctly: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	mediaRuntime, err := streams.GetStreamMediaRuntime(t.Context(), stream.ID)
	if err != nil || mediaRuntime.VideoOverlayBurnIn {
		t.Fatalf("legacy start did not persist false burn-in state: runtime=%#v err=%v", mediaRuntime, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	stopReq.AddCookie(cookie)
	stopReq.Header.Set("X-CSRF-Token", csrf)
	stopRes := httptest.NewRecorder()
	handler.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
	}
	if !strings.Contains(stopRes.Body.String(), `"dispatch"`) || !strings.Contains(stopRes.Body.String(), `"service_type":"worker"`) {
		t.Fatalf("stop response does not include dispatch results: %s", stopRes.Body.String())
	}
	if dispatcher.stopCalls != 1 || dispatcher.stoppedStream.ID != stream.ID || len(dispatcher.stoppedServices) != 3 {
		t.Fatalf("stop dispatcher was not called correctly: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestStartStreamCompensatesPartialDispatchFailure(t *testing.T) {
	tests := []struct {
		name    string
		results []servicecall.DispatchResult
	}{
		{
			name: "worker fails after encoder starts",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/start", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/start", StatusCode: http.StatusConflict, Code: "invalid_stream_state", Error: "service returned status 409: invalid_stream_state"},
			},
		},
		{
			name: "worker response framing fails after encoder starts",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/start", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/start", StatusCode: http.StatusAccepted, Code: "worker_job_generation_response_invalid", FailurePhase: "protocol", Error: "Worker start response did not contain exactly one JSON value"},
			},
		},
		{
			name: "oversized worker response fails after encoder starts",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/start", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/start", StatusCode: http.StatusAccepted, Code: "worker_job_generation_response_invalid", FailurePhase: "protocol", Error: "Worker start response exceeded size limit"},
			},
		},
		{
			name: "bot fails after encoder and worker start",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/start", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/start", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/start", StatusCode: http.StatusBadGateway, Error: "service returned status 502"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "partial start failure")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
			profiles := store.NewMemoryProfileStore()
			config := createDiscordConfigForTest(t, profiles, "partial failure discord", "discord_bot-01", "guild-01", "voice-01", "")

			requestContext, cancelRequest := context.WithCancel(t.Context())
			defer cancelRequest()
			dispatcher := &cancelingRequestStopDispatcher{
				fakeServiceDispatcher: fakeServiceDispatcher{startResultsOverride: tt.results},
				cancelRequest:         cancelRequest,
			}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","encoder_input_url":"srt://input.example.com:9000"}`)).WithContext(requestContext)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"service_dispatch_failed"`) || !strings.Contains(res.Body.String(), `"stop_dispatch"`) {
				t.Fatalf("partial failure response status=%d body=%s", res.Code, res.Body.String())
			}
			if dispatcher.startCalls != 1 || dispatcher.stopCalls != 1 || dispatcher.stopContextCancelled {
				t.Fatalf("partial failure did not use one detached compensating stop: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
			}
			if len(dispatcher.stoppedServices) != 3 || dispatcher.stoppedStream.ID != stream.ID || dispatcher.stoppedStream.Status != "failed" {
				t.Fatalf("compensating stop did not cover failed stream primary assignments: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
			}
			stored, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil || stored.Status != "failed" {
				t.Fatalf("partial failure did not terminalize stream: stream=%#v err=%v", stored, err)
			}
			auditJSON := toJSONForTest(t, auth.AuditEvents())
			if !strings.Contains(auditJSON, `"action":"streams.start"`) || !strings.Contains(auditJSON, `"stop_dispatch"`) {
				t.Fatalf("partial failure audit omitted compensating stop: %s", auditJSON)
			}
		})
	}
}
