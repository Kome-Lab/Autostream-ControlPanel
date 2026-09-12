package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestCurrentUserAvatarLifecycle(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "user-avatar", Username: "avatar-admin", Email: "avatar@example.jp", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	cookie, csrfToken := loginForTest(t, handler, "avatar-admin", "correct horse battery")
	avatarBody := testAvatarPNG(t, 96, 96)

	upload := httptest.NewRequest(http.MethodPut, "/auth/avatar", bytes.NewReader(avatarBody))
	upload.AddCookie(cookie)
	upload.Header.Set("Content-Type", "image/png")
	upload.Header.Set("X-CSRF-Token", csrfToken)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("avatar upload status = %d body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if strings.Contains(uploadResponse.Body.String(), "image_data") || strings.Contains(uploadResponse.Body.String(), "iVBOR") {
		t.Fatalf("avatar upload response leaked binary data: %s", uploadResponse.Body.String())
	}
	var uploaded struct {
		AvatarURL   string `json:"avatar_url"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uploaded.AvatarURL, "/auth/avatar?v=") || uploaded.ContentType != "image/png" || uploaded.SizeBytes != int64(len(avatarBody)) {
		t.Fatalf("unexpected avatar response: %#v", uploaded)
	}

	me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	me.AddCookie(cookie)
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK || !strings.Contains(meResponse.Body.String(), uploaded.AvatarURL) || !strings.Contains(meResponse.Body.String(), "avatar_updated_at") {
		t.Fatalf("auth me did not expose avatar metadata: status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}

	download := httptest.NewRequest(http.MethodGet, uploaded.AvatarURL, nil)
	download.AddCookie(cookie)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("avatar download status = %d body = %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if !bytes.Equal(downloadResponse.Body.Bytes(), avatarBody) {
		t.Fatal("avatar download bytes did not match upload")
	}
	if got := downloadResponse.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("avatar content type = %q", got)
	}
	if got := downloadResponse.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("avatar cache control = %q", got)
	}
	if got := downloadResponse.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("avatar nosniff header = %q", got)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/auth/avatar", nil)
	remove.AddCookie(cookie)
	remove.Header.Set("X-CSRF-Token", csrfToken)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("avatar delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}

	missing := httptest.NewRequest(http.MethodGet, "/auth/avatar", nil)
	missing.AddCookie(cookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted avatar status = %d body = %s", missingResponse.Code, missingResponse.Body.String())
	}

	events := auth.AuditEvents()
	if len(events) < 3 || !hasAuditAction(events, "auth.avatar.update") || !hasAuditAction(events, "auth.avatar.delete") {
		t.Fatalf("avatar audit actions missing: %#v", events)
	}
}

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

func TestStartDoesNotReviveStreamCompletedDuringDelayedDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "race discord", "discord_bot-01", "guild-race", "voice-race", "")
	dispatcher := &blockingStartDispatcher{
		startEntered: make(chan struct{}, 1),
		releaseStart: make(chan struct{}),
		stopEntered:  make(chan struct{}, 2),
	}
	stream, err := streams.CreateStream(t.Context(), "delayed start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	visualOption := withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003")
	startHandler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), visualOption, WithServiceDispatcher(dispatcher))
	// A second Server models another Control Panel process. Its local stream
	// lock is intentionally independent, so this test proves that the durable
	// CAS transitions, not just the in-process lock, protect the final state.
	stopHandler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), visualOption, WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, startHandler, "operator", "correct horse battery")

	startDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		startHandler.ServeHTTP(res, req)
		startDone <- res
	}()

	select {
	case <-dispatcher.startEntered:
	case <-time.After(time.Second):
		t.Fatal("start dispatch did not begin")
	}
	starting, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if starting.Status != "starting" {
		t.Fatalf("stream status before concurrent stop = %q, want starting", starting.Status)
	}

	stopDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		stopReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
		stopReq.AddCookie(cookie)
		stopReq.Header.Set("X-CSRF-Token", csrf)
		stopRes := httptest.NewRecorder()
		stopHandler.ServeHTTP(stopRes, stopReq)
		stopDone <- stopRes
	}()
	select {
	case stopRes := <-stopDone:
		if stopRes.Code != http.StatusOK {
			t.Fatalf("concurrent stop status = %d body = %s", stopRes.Code, stopRes.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent stop did not complete")
	}
	select {
	case <-dispatcher.stopEntered:
		// The normal stop has reached the services before the delayed start is
		// allowed to finish.
	case <-time.After(time.Second):
		t.Fatal("concurrent stop did not dispatch")
	}
	close(dispatcher.releaseStart)
	select {
	case startRes := <-startDone:
		if startRes.Code != http.StatusAccepted || !strings.Contains(startRes.Body.String(), "start_superseded") {
			t.Fatalf("delayed start status = %d body = %s", startRes.Code, startRes.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("delayed start did not complete")
	}
	select {
	case <-dispatcher.stopEntered:
		// A separate Panel can have issued the normal stop while this start was
		// blocked. Once the delayed remote start returns, the initiating Panel
		// must emit a compensating stop so the final service-side action is stop.
	case <-time.After(time.Second):
		t.Fatal("superseded delayed start did not issue compensating stop")
	}
	if dispatcher.stopCalls != 2 {
		t.Fatalf("stop dispatch count after delayed start = %d, want normal plus compensating stop", dispatcher.stopCalls)
	}
	final, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" {
		t.Fatalf("delayed start revived stream to %q, want completed", final.Status)
	}
}

func TestConcurrentStreamStartsClaimOnceBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	baseStreams := store.NewMemoryStreamStore()
	auth.BindStreamAssignmentGuard(baseStreams)
	transitionGate := &gatedLifecycleTransitionStore{
		StreamStore: baseStreams,
		startClaims: baseStreams,
		expected:    "created",
		status:      "starting",
		entered:     make(chan struct{}, 2),
		release:     make(chan struct{}),
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "concurrent start discord", "discord_bot-01", "", "", "")
	dispatcher := &synchronizedServiceDispatcher{}
	stream, err := baseStreams.CreateStream(t.Context(), "concurrent start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStreams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	visualOption := withManualDiscordTargetForTest(t, baseStreams, stream.ID, "1001", "1002", "1003")
	first := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), visualOption, WithServiceDispatcher(dispatcher))
	second := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), visualOption, WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, first, "operator", "correct horse battery")

	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, handler := range []http.Handler{first, second} {
		go func(handler http.Handler) {
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			responses <- res
		}(handler)
	}
	waitForLifecycleTransitionAttempts(t, transitionGate.entered, 2)
	close(transitionGate.release)

	codes := receiveLifecycleResponseCodes(t, responses, 2)
	if codes[http.StatusOK] != 1 || codes[http.StatusConflict] != 1 {
		t.Fatalf("concurrent start response codes = %#v, want one 200 and one 409", codes)
	}
	if got := dispatcher.StartCalls(); got != 1 {
		t.Fatalf("concurrent starts dispatched %d times, want 1", got)
	}
	final, err := baseStreams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "live" {
		t.Fatalf("concurrent start final status = %q, want live", final.Status)
	}
}

func TestConcurrentStreamStopsClaimOnceBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	baseStreams := store.NewMemoryStreamStore()
	transitionGate := &gatedLifecycleTransitionStore{
		StreamStore: baseStreams,
		expected:    "live",
		status:      "stopping",
		entered:     make(chan struct{}, 2),
		release:     make(chan struct{}),
	}
	dispatcher := &synchronizedServiceDispatcher{}
	first := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	second := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, first, "operator", "correct horse battery")

	stream, err := baseStreams.CreateStream(t.Context(), "concurrent stop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStreams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)

	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, handler := range []http.Handler{first, second} {
		go func(handler http.Handler) {
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			responses <- res
		}(handler)
	}
	waitForLifecycleTransitionAttempts(t, transitionGate.entered, 2)
	close(transitionGate.release)

	codes := receiveLifecycleResponseCodes(t, responses, 2)
	if codes[http.StatusOK] != 1 || codes[http.StatusConflict] != 1 {
		t.Fatalf("concurrent stop response codes = %#v, want one 200 and one 409", codes)
	}
	if got := dispatcher.StopCalls(); got != 1 {
		t.Fatalf("concurrent stops dispatched %d times, want 1", got)
	}
	final, err := baseStreams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" {
		t.Fatalf("concurrent stop final status = %q, want completed", final.Status)
	}
}

func TestStopStreamNormalizesOnlyExactAlreadyStoppedDownstreamReceipts(t *testing.T) {
	defaultResults := func() []servicecall.DispatchResult {
		return []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
		}
	}
	withFailure := func(result servicecall.DispatchResult) []servicecall.DispatchResult {
		results := defaultResults()
		for index := range results {
			if results[index].ServiceType == result.ServiceType {
				results[index] = result
				return results
			}
		}
		t.Fatalf("missing default result for service type %q", result.ServiceType)
		return nil
	}

	tests := []struct {
		name             string
		results          []servicecall.DispatchResult
		wantHTTPStatus   int
		wantStreamStatus string
		failure          servicecall.DispatchResult
	}{
		{
			name: "exact stopped receipts complete the dynamic stream",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "no_active_stream_job", Error: "service returned status 409: no_active_stream_job"},
				{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			},
			wantHTTPStatus:   http.StatusOK,
			wantStreamStatus: "completed",
		},
		{
			name:             "encoder starting remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_starting", Error: "service returned status 409: stream_starting"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_starting"},
		},
		{
			name:             "worker stream mismatch remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "stream_id_mismatch", Error: "service returned status 409: stream_id_mismatch"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "worker", StatusCode: http.StatusConflict, Code: "stream_id_mismatch"},
		},
		{
			name:             "encoder different active stream remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_already_running", Error: "service returned status 409: stream_already_running"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_already_running"},
		},
		{
			name:             "authentication failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusUnauthorized, Code: "missing_or_invalid_service_token", Error: "service returned status 401: missing_or_invalid_service_token"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "worker", StatusCode: http.StatusUnauthorized, Code: "missing_or_invalid_service_token"},
		},
		{
			name:             "server failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusInternalServerError, Code: "stop_stream_failed", Error: "service returned status 500: stop_stream_failed"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusInternalServerError, Code: "stop_stream_failed"},
		},
		{
			name:             "transport failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", Error: "service request failed"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", Error: "service request failed"},
		},
		{
			name:             "encoder no process with the wrong status remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_not_running", Error: "service returned status 409: stream_not_running"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_not_running"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			dispatcher := &fakeServiceDispatcher{stopResultsOverride: tt.results}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			stream, err := streams.CreateStream(t.Context(), "manual stop receipts")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
			if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.wantHTTPStatus {
				t.Fatalf("stop status=%d want=%d body=%s", response.Code, tt.wantHTTPStatus, response.Body.String())
			}

			stored, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil || stored.Status != tt.wantStreamStatus {
				t.Fatalf("stream status=%q err=%v want=%q", stored.Status, err, tt.wantStreamStatus)
			}

			responseBody := response.Body.String()
			var body struct {
				Dispatch []servicecall.DispatchResult `json:"dispatch"`
			}
			if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
				t.Fatalf("decode stop response: %v", err)
			}
			if tt.wantHTTPStatus == http.StatusOK {
				var encoderStopped, workerStopped bool
				for _, result := range body.Dispatch {
					switch {
					case result.ServiceType == "encoder_recorder" && result.StatusCode == http.StatusNotFound && result.Code == "stream_not_running":
						encoderStopped = result.Success && result.Error == ""
					case result.ServiceType == "worker" && result.StatusCode == http.StatusConflict && result.Code == "no_active_stream_job":
						workerStopped = result.Success && result.Error == ""
					}
				}
				if !encoderStopped || !workerStopped {
					t.Fatalf("exact already-stopped receipts were not normalized: %#v", body.Dispatch)
				}
				return
			}

			if !strings.Contains(responseBody, `"code":"service_dispatch_failed"`) {
				t.Fatalf("unsafe downstream result must preserve service dispatch failure: %s", responseBody)
			}
			for _, result := range body.Dispatch {
				if result.ServiceType == tt.failure.ServiceType && result.StatusCode == tt.failure.StatusCode && result.Code == tt.failure.Code {
					if result.Success {
						t.Fatalf("unsafe downstream result was normalized: %#v", result)
					}
					return
				}
			}
			t.Fatalf("missing unsafe downstream result %#v in %#v", tt.failure, body.Dispatch)
		})
	}
}

func TestStopStreamDoesNotNormalizeEncoderNoProcessWhenRelayClaimLookupFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := &failingRelayBindingClaimLookupStreamStore{MemoryStreamStore: store.NewMemoryStreamStore(), err: errors.New("relay binding lookup unavailable")}
	dispatcher := &fakeServiceDispatcher{stopResultsOverride: []servicecall.DispatchResult{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
		{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "no_active_stream_job", Error: "service returned status 409: no_active_stream_job"},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "manual stop claim lookup failure")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"service_dispatch_failed"`) {
		t.Fatalf("claim lookup failure must retain encoder dispatch failure: status=%d body=%s", response.Code, response.Body.String())
	}
	stored, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("claim lookup failure must leave stream failed: stream=%#v err=%v", stored, err)
	}
}

func TestStopStreamCompletesAfterDownstreamCancelsRequestContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	dispatcher := &cancelingRequestStopDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "auto stop detached context")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  "discord-config-auto-stop",
		AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
	}); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	dispatcher.cancelRequest = cancelRequest
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil).WithContext(requestCtx)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopContextCancelled {
		t.Fatal("downstream stop inherited the cancelled requester context")
	}
	completed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "ready" {
		t.Fatalf("stop after downstream request cancellation left status %q, want ready", completed.Status)
	}
	allStreams, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var waitingCount int
	for _, candidate := range allStreams {
		if candidate.ID != stream.ID && candidate.Status == "created" && candidate.AutoStartTrigger == autoStartTriggerDiscordVoiceJoin {
			waitingCount++
		}
	}
	if waitingCount != 0 || len(allStreams) != 1 || allStreams[0].ID != stream.ID || allStreams[0].Status != "ready" {
		t.Fatalf("stop after downstream request cancellation did not reuse the source stream: %#v", allStreams)
	}
}

func TestForceStopStreamCompletesAfterDownstreamCancelsRequestContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	dispatcher := &cancelingRequestStopDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "force stop detached context")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	dispatcher.cancelRequest = cancelRequest
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/force-stop", nil).WithContext(requestCtx)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("force-stop status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopContextCancelled {
		t.Fatal("force-stop downstream stop inherited the cancelled requester context")
	}
	failed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "completed" {
		t.Fatalf("force-stop after downstream request cancellation left status %q, want completed after confirmed downstream stop", failed.Status)
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

func TestStartStreamResolvesDiscordConfigForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord config stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "main discord", "discord_bot-01", "guild-from-config", "voice-from-config", "text-from-config")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "1001" || dispatcher.startRequest.DiscordVoiceChannelID != "1003" || dispatcher.startRequest.DiscordTextChannelID != "1002" {
		t.Fatalf("discord config was not resolved into start request: %#v", dispatcher.startRequest)
	}
}

func TestStartStreamMaterializesConfiguredDiscordBotAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "implicit discord assignment stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "configured discord", "discord_bot-01", "guild-01", "voice-01", "text-01")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if primaryServiceID(primaryStreamAssignments(assignments), "discord_bot") != "discord_bot-01" {
		t.Fatalf("configured Discord Bot was not materialized as primary assignment: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	if primaryServiceID(dispatcher.startedServices, "discord_bot") != "discord_bot-01" {
		t.Fatalf("dispatcher did not receive the materialized Discord Bot: %#v", dispatcher.startedServices)
	}
}

func TestStartStreamUsesSavedDiscordConfigSetting(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "saved discord config stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "saved discord", "discord_bot-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
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
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "1001" || dispatcher.startRequest.DiscordVoiceChannelID != "1003" || dispatcher.startRequest.DiscordTextChannelID != "1002" {
		t.Fatalf("saved discord config was not applied: %#v", dispatcher.startRequest)
	}
}

func TestServiceStartStreamUsesSavedSettingsForPrimaryDiscordBot(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service start stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected one start dispatch, got %d", dispatcher.startCalls)
	}
	if dispatcher.startRequest.DiscordConfigID != config.ID || dispatcher.startRequest.DiscordGuildID != "1001" || dispatcher.startRequest.DiscordVoiceChannelID != "1003" {
		t.Fatalf("service start must use saved settings: %#v", dispatcher.startRequest)
	}
	if dispatcher.startRequest.DiscordTextChannelID != "1002" {
		t.Fatalf("service start should use saved stream text channel: %#v", dispatcher.startRequest)
	}
	events := auth.AuditEvents()
	foundStartAudit := false
	for _, event := range events {
		if event.Action == "streams.start" && event.ResourceID == stream.ID && event.Result == "success" {
			foundStartAudit = event.ActorUserID == "service:discord-01" && event.ActorUsername == "discord-01"
		}
	}
	if !foundStartAudit {
		t.Fatalf("service start audit missing service actor: %#v", events)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	duplicateReq.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	duplicateRes := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusOK || !strings.Contains(duplicateRes.Body.String(), "already_active") {
		t.Fatalf("duplicate service start status = %d body = %s", duplicateRes.Code, duplicateRes.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("active stream must not be dispatched again, got %d calls", dispatcher.startCalls)
	}
}

func TestServiceStopStreamUsesPrimaryDiscordBotAndCanonicalLifecycle(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service stop stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.stop"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "service stop discord", "discord-01", "guild-stop", "voice-stop", "")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, AutoStartTrigger: autoStartTriggerDiscordVoiceJoin}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/stop", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service stop status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopCalls != 1 || dispatcher.stoppedStream.ID != stream.ID || len(dispatcher.stoppedServices) != 3 {
		t.Fatalf("service stop must use the canonical dispatch: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	stopped, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "ready" || stopped.ID != stream.ID {
		t.Fatalf("service stop must rearm the same stream, got %#v", stopped)
	}

	otherDiscordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.stop"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, otherDiscordToken, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	otherReq := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/stop", nil)
	otherReq.Header.Set("Authorization", "Bearer "+otherDiscordToken.RawToken)
	otherRes := httptest.NewRecorder()
	handler.ServeHTTP(otherRes, otherReq)
	if otherRes.Code != http.StatusForbidden || !strings.Contains(otherRes.Body.String(), "service_not_primary_assignment") {
		t.Fatalf("unassigned Discord Bot completed stop status = %d body = %s", otherRes.Code, otherRes.Body.String())
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/stop", nil)
	duplicateReq.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	duplicateRes := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusConflict || !strings.Contains(duplicateRes.Body.String(), "stream_status_not_stoppable") {
		t.Fatalf("rearmed service stop status = %d body = %s", duplicateRes.Code, duplicateRes.Body.String())
	}
	if dispatcher.stopCalls != 1 {
		t.Fatalf("rearmed stream must not be stopped twice, got %d calls", dispatcher.stopCalls)
	}
}

func TestStopStreamReportsWaitingStreamRearmFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := &failingRearmStreamStore{MemoryStreamStore: store.NewMemoryStreamStore()}
	stream, err := streams.CreateStream(t.Context(), "VC rearm failure")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.MemoryStreamStore.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{AutoStartTrigger: autoStartTriggerDiscordVoiceJoin}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	streams.failSettingsUpdate = true
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "waiting_stream_rearm_failed") {
		t.Fatalf("stop must surface rearm warning, status=%d body=%s", res.Code, res.Body.String())
	}
	completed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" {
		t.Fatalf("stream status after rearm failure = %q, want completed", completed.Status)
	}
	foundRearmFailure := false
	for _, event := range auth.AuditEvents() {
		if event.Action == "streams.rearm" && event.ResourceID == stream.ID && event.Result == "failure" && event.Metadata["reason"] == "waiting_stream_rearm_failed" {
			foundRearmFailure = true
		}
	}
	if !foundRearmFailure {
		t.Fatalf("waiting stream rearm failure was not audited: %#v", auth.AuditEvents())
	}
}

func TestServiceStartStreamAllowsConfiguredDiscordBotWithoutPriorAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord configured service start stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "configured service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected one start dispatch, got %d", dispatcher.startCalls)
	}
	foundDiscord := false
	for _, service := range dispatcher.startedServices {
		if service.ServiceID == "discord-01" && service.ServiceType == "discord_bot" && service.AssignmentRole == "primary" {
			foundDiscord = true
		}
	}
	if !foundDiscord {
		t.Fatalf("configured discord bot was not assigned before dispatch: %#v", dispatcher.startedServices)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, service := range assignments {
		if service.ServiceID == "discord-01" && service.ServiceType == "discord_bot" && service.AssignmentRole == "primary" {
			persisted = true
		}
	}
	if !persisted {
		t.Fatalf("configured discord bot assignment was not persisted: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
}

func TestServiceStartStreamRequiresAutoStartTrigger(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service start disabled")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "service start discord", "discord-01", "guild-saved", "voice-saved", "text-saved")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_auto_start_not_enabled") {
		t.Fatalf("service start without trigger status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("disabled auto-start must not dispatch, got %d calls", dispatcher.startCalls)
	}
}

func TestServiceStartStreamRequiresPrimaryDiscordBotToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord service forbidden stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "service_not_primary_assignment") {
		t.Fatalf("unassigned service start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("unassigned discord token must not dispatch start")
	}
}

func TestStartStreamRejectsSavedDiscordChannelOverridesWithoutConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "saved discord channel stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "discord_config_required") {
		t.Fatalf("expected discord_config_required: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called without a Discord Config")
	}
}

func TestStartStreamRejectsDiscordConfigForDifferentPrimaryBot(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "discord config mismatch stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "wrong discord", map[string]any{
		"service_id":       "discord-bot-other",
		"guild_id":         "guild-from-config",
		"voice_channel_id": "voice-from-config",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "discord_config_service_mismatch") {
		t.Fatalf("expected discord_config_service_mismatch: %s", res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher must not be called for mismatched discord config")
	}
}

func TestStartStreamQueuesDiscordNotificationOnlyAfterSuccessfulDispatch(t *testing.T) {
	tests := []struct {
		name                      string
		watchURL                  string
		failStart                 bool
		notificationResult        servicecall.DispatchResult
		snapshotTextChannel       string
		wantDispatchTextChannel   string
		wantHTTPStatus            int
		wantStreamStatus          string
		wantNotifyCalls           int
		wantResponseCode          string
		wantNotifiedURL           string
		wantNotificationState     string
		wantNotificationLastError string
	}{
		{name: "notification sent", watchURL: "https://youtu.be/video_01", snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_01", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "profile text channel fallback sends notification", watchURL: "https://youtu.be/video_profile_fallback", wantDispatchTextChannel: "3002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_profile_fallback", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "saved stream text channel override keeps priority", watchURL: "https://youtu.be/video_stream_override", snapshotTextChannel: "9002", wantDispatchTextChannel: "9002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_stream_override", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDelivered},
		{name: "ambiguous notification failure keeps live stream and fences recovery", watchURL: "https://www.youtube.com/watch?v=video_02", notificationResult: servicecall.DispatchResult{StatusCode: http.StatusBadGateway, Code: "discord_api_unavailable", Error: "service returned status 502"}, snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusOK, wantStreamStatus: "live", wantNotifyCalls: 1, wantNotifiedURL: "https://www.youtube.com/watch?v=video_02", wantNotificationState: store.DiscordYouTubeLiveNotificationStateDeliveryUnknown, wantNotificationLastError: "discord_api_unavailable"},
		{name: "dispatch failure suppresses notification", watchURL: "https://www.youtube.com/watch?v=video_03", failStart: true, snapshotTextChannel: "2002", wantDispatchTextChannel: "2002", wantHTTPStatus: http.StatusBadGateway, wantStreamStatus: "failed", wantNotifyCalls: 0, wantResponseCode: "service_dispatch_failed"},
		{name: "profile text channel fallback requires watch URL", wantHTTPStatus: http.StatusConflict, wantStreamStatus: "created", wantNotifyCalls: 0, wantResponseCode: "youtube_output_invalid_config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "notification stream")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
			secrets := store.NewMemorySecretStore()
			if _, err := secrets.UpdateSecret(t.Context(), "youtube_stream_key_notification", "runtime-secret-stream-key"); err != nil {
				t.Fatal(err)
			}
			profiles := store.NewMemoryProfileStore()
			discord, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "notification discord", map[string]any{
				"service_id":           "discord_bot-01",
				"bot_token_configured": true,
				"guild_id":             "3001",
				"voice_channel_id":     "3003",
				"text_channel_id":      "3002",
			})
			if err != nil {
				t.Fatal(err)
			}
			youtubeConfig := map[string]any{
				"mode":                   "stream_key",
				"rtmp_url":               "rtmps://youtube.example.com/live2",
				"stream_key_secret_name": "youtube_stream_key_notification",
			}
			if tt.watchURL != "" {
				youtubeConfig["watch_url"] = tt.watchURL
			}
			youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "notification-output", youtubeConfig)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := &notificationFakeDispatcher{fakeServiceDispatcher: fakeServiceDispatcher{failStart: tt.failStart}, result: tt.notificationResult}
			options := []ServerOption{WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher)}
			if tt.snapshotTextChannel != "" {
				options = append(options, withManualDiscordTargetForTest(t, streams, stream.ID, "2001", tt.snapshotTextChannel, "2003"))
			}
			handler := NewServer(streams, options...)
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			body := fmt.Sprintf(`{"discord_config_id":%q,"youtube_output_id":%q}`, discord.ID, youtube.ID)
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantHTTPStatus {
				t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
			}
			updated, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != tt.wantStreamStatus || dispatcher.notifyCalls != 0 {
				t.Fatalf("start must only enqueue notification: stream=%s notify_calls=%d body=%s", updated.Status, dispatcher.notifyCalls, res.Body.String())
			}
			if tt.wantResponseCode != "" && !strings.Contains(res.Body.String(), tt.wantResponseCode) {
				t.Fatalf("expected response code %q: %s", tt.wantResponseCode, res.Body.String())
			}
			if tt.wantDispatchTextChannel != "" && dispatcher.startRequest.DiscordTextChannelID != tt.wantDispatchTextChannel {
				t.Fatalf("discord text channel was not resolved for dispatch: got=%q want=%q request=%#v", dispatcher.startRequest.DiscordTextChannelID, tt.wantDispatchTextChannel, dispatcher.startRequest)
			}
			if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "youtube.com/watch") || strings.Contains(toJSONForTest(t, auth.AuditEvents()), "youtu.be/") {
				t.Fatalf("YouTube watch URL leaked into audit events: %#v", auth.AuditEvents())
			}
			if tt.wantNotificationState != "" {
				queued, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				if queued.State != store.DiscordYouTubeLiveNotificationStateDispatchPending || queued.LifecycleStatus != "legacy_unverified" {
					t.Fatalf("start did not enqueue legacy notification: %#v", queued)
				}
				if _, err := handler.DispatchDueDiscordYouTubeLiveNotifications(t.Context(), 25); err != nil {
					t.Fatal(err)
				}
				notification, err := streams.GetLatestDiscordYouTubeLiveNotification(t.Context(), stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				if notification.State != tt.wantNotificationState || notification.LastError != tt.wantNotificationLastError {
					t.Fatalf("notification state = %#v, want state=%q last_error=%q", notification, tt.wantNotificationState, tt.wantNotificationLastError)
				}
			}
			if dispatcher.notifyCalls != tt.wantNotifyCalls {
				t.Fatalf("notification calls = %d, want %d", dispatcher.notifyCalls, tt.wantNotifyCalls)
			}
			if dispatcher.notifyCalls == 1 {
				if dispatcher.notifiedStream.Status != "live" || dispatcher.notifiedURL != tt.wantNotifiedURL {
					t.Fatalf("unexpected notification request: stream=%#v url=%q", dispatcher.notifiedStream, dispatcher.notifiedURL)
				}
				if !strings.HasPrefix(dispatcher.notifiedEventID, "youtube-live-") {
					t.Fatalf("unexpected notification event id: %q", dispatcher.notifiedEventID)
				}
			}
		})
	}
}

func TestStartStreamResolvesArchiveDriveDestinationForDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "archive-main", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "raw-drive-folder-id") {
		t.Fatalf("drive folder ID leaked in response: %s", res.Body.String())
	}
	if dispatcher.startRequest.ArchiveConfig["folder_id"] == "raw-drive-folder-id" {
		t.Fatalf("archive config leaked raw folder ID to dispatch: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	secretName, _ := dispatcher.startRequest.ArchiveConfig["folder_id_secret_name"].(string)
	if secretName != driveDestinationFolderIDSecretName(destination.ID) || dispatcher.startRequest.ArchiveConfig["shared_drive"] != true {
		t.Fatalf("archive config did not include scoped secret reference: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	if dispatcher.startRequest.ArchiveConfig["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || dispatcher.startRequest.ArchiveConfig["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("archive config did not include OAuth secret references: %#v", dispatcher.startRequest.ArchiveConfig)
	}
	for _, leaked := range []string{"service_account_json", "service_account_credentials_secret_name", "client_secret", "refresh_token", "folder_id"} {
		if _, ok := dispatcher.startRequest.ArchiveConfig[leaked]; ok {
			t.Fatalf("archive config leaked raw or unsupported secret field %q: %#v", leaked, dispatcher.startRequest.ArchiveConfig)
		}
	}
}

func TestStartStreamResolvesOAuthDriveDestinationForDispatchWithoutResponseLeak(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account",
		RefreshToken: "raw-google-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth shared drive archive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive discord", "discord_bot-01", "guild-archive", "voice-archive", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive", map[string]any{"drive_destination_id": destination.ID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-google-refresh-token", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("raw OAuth/Drive secret leaked in response: %s", res.Body.String())
		}
	}
	cfg := dispatcher.startRequest.ArchiveConfig
	if cfg["auth_mode"] != "oauth2" || cfg["client_secret"] == "raw-google-client-secret" || cfg["refresh_token"] == "raw-google-refresh-token" || cfg["folder_id"] == "raw-oauth-drive-folder-id" {
		t.Fatalf("OAuth archive config leaked raw secret values to dispatch: %#v", cfg)
	}
	if cfg["folder_id_secret_name"] != driveDestinationFolderIDSecretName(destination.ID) || cfg["client_secret_secret_name"] != oauthProviderClientSecretSecretName(provider.ID) || cfg["refresh_token_secret_name"] != oauthAccountRefreshTokenSecretName(account.ID) {
		t.Fatalf("OAuth archive config did not include scoped secret references: %#v", cfg)
	}
}

func TestStreamStartChecksReadinessBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code   string                       `json:"code"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "stream_start_not_ready" || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher should not be called: %#v", dispatcher.fakeServiceDispatcher)
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness failure: %#v", unchanged)
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "streams.start" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected readiness failure audit event, got %#v", events)
	}
}

func TestStreamStartReadinessEndpointReportsMissingAssignmentsWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready               bool     `json:"ready"`
		MissingServiceTypes []string `json:"missing_service_types"`
		AssignedCount       int      `json:"assigned_service_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "encoder_recorder") || body.AssignedCount != 1 {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on readiness check: %#v", unchanged)
	}
}

func TestStreamStartReadinessEndpointReportsServerReadinessIssues(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "readiness endpoint discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &readinessBlockDispatcher{issues: []servicecall.ReadinessIssue{{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		Code:        "service_public_url_invalid",
		Message:     "service public_url must be absolute",
	}}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`","encoder_input_url":"srt://source.example.com:9000"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "service_public_url_invalid" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %#v", dispatcher.fakeServiceDispatcher)
	}
}

func TestStreamStartReadinessRejectsUnsupportedNegotiatedWorkerVideoProfile(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "unsupported scene")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "scene discord", "discord_bot-01", "guild", "voice", "")
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "unsupported", map[string]any{"width": 1024, "height": 576, "fps": 30})
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
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	body := fmt.Sprintf(`{"discord_config_id":%q,"encoder_profile_id":%q}`, discord.ID, encoderProfile.ID)
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "worker_video_encoder_profile_unsupported") || !strings.Contains(res.Body.String(), `"ready":false`) {
		t.Fatalf("readiness did not reject unsupported negotiated scene: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestStreamStartReadinessEndpointReportsMissingDriveDestination(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "missing-destination-archive", map[string]any{
		"drive_destination_id": "missing-drive-destination",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(store.NewMemoryIntegrationStore()), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_destination_not_found" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestStreamStartReadinessEndpointReportsOAuthDriveAccountIssueWithoutRawSecret(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "oauth archive readiness stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive Readiness",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "archive account without token",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "oauth archive readiness destination",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-oauth-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "oauth archive readiness discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "oauth-archive-readiness", map[string]any{
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithIntegrationStore(integrations), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","archive_profile_id":"`+archiveProfile.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Ready  bool                         `json:"ready"`
		Issues []servicecall.ReadinessIssue `json:"issues"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || len(body.Issues) != 1 || body.Issues[0].Code != "drive_oauth_account_unavailable" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("readiness endpoint must not dispatch start: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	for _, raw := range []string{"raw-google-client-secret", "raw-oauth-drive-folder-id"} {
		if strings.Contains(res.Body.String(), raw) {
			t.Fatalf("readiness response leaked raw archive secret %q: %s", raw, res.Body.String())
		}
	}
}

func TestStreamStartRejectsActiveStatusWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "already live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_status_not_startable") {
		t.Fatalf("expected start state conflict, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("start must not be dispatched for an active stream: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "live" {
		t.Fatalf("active stream status changed: %#v", unchanged)
	}
	assertAuditFailureReason(t, auth.AuditEvents(), "streams.start", stream.ID, "stream_status_not_startable")
}

func TestStreamStopRejectsInactiveStatusWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "not started")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "stream_status_not_stoppable") {
		t.Fatalf("expected stop state conflict, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopCalls != 0 {
		t.Fatalf("stop must not be dispatched for an inactive stream: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("inactive stream status changed: %#v", unchanged)
	}
	assertAuditFailureReason(t, auth.AuditEvents(), "streams.stop", stream.ID, "stream_status_not_stoppable")
}

func TestStreamDispatchFailureMarksFailed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "dispatch failure discord", "discord_bot-01", "guild-dispatch", "voice-dispatch", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(&fakeServiceDispatcher{failStart: true}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	failed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed stream, got %#v", failed)
	}
}

func TestStreamDispatchFailureDoesNotLeakSecretError(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	dispatcher := &fakeServiceDispatcher{failStart: true, dispatchFailureError: `Post "https://encoder.example.com/jobs/start?token=secret-token": Authorization Bearer secret-token`}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "secret failure discord", "discord_bot-01", "guild-dispatch", "voice-dispatch", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") || strings.Contains(res.Body.String(), "encoder.example.com") || !strings.Contains(res.Body.String(), "service dispatch failed") {
		t.Fatalf("dispatch secret leaked or sanitized error missing: %s", res.Body.String())
	}
	if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "secret-token") {
		t.Fatalf("dispatch secret leaked in audit events: %#v", auth.AuditEvents())
	}
}

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

func TestDriveDestinationDeleteRejectsStreamAndArchiveProfileReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "archive stream")
	if err != nil {
		t.Fatal(err)
	}
	integrations := store.NewMemoryIntegrationStore()
	profiles := store.NewMemoryProfileStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google Drive",
		Enabled:      true,
		ClientID:     "google-client-id",
		ClientSecret: "raw-google-client-secret",
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "Archive Account",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := integrations.CreateDriveDestination(t.Context(), store.DriveDestination{
		Name:           "Shared Drive",
		AuthMode:       "oauth2",
		OAuthAccountID: account.ID,
		FolderID:       "raw-drive-folder-id",
		SharedDrive:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{ArchiveDriveDestinationID: destination.ID, ArchiveOAuthAccountID: account.ID}); err != nil {
		t.Fatal(err)
	}
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "Shared Drive Archive", map[string]any{
		"format":               "mp4",
		"upload_enabled":       true,
		"drive_destination_id": destination.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(integrations), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	body := deleteRes.Body.String()
	if deleteRes.Code != http.StatusConflict || !strings.Contains(body, "drive_destination_in_use") {
		t.Fatalf("expected destination in-use conflict, status=%d body=%s", deleteRes.Code, body)
	}
	for _, expected := range []string{"stream_archive_settings", "archive_profiles"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("delete conflict missing reference count %q: %s", expected, body)
		}
	}
	for _, raw := range []string{"raw-refresh-token", "raw-google-client-secret", "raw-drive-folder-id"} {
		if strings.Contains(body, raw) {
			t.Fatalf("delete conflict leaked secret material %q: %s", raw, body)
		}
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); err != nil {
		t.Fatalf("destination was deleted despite references: %v", err)
	}

	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.DeleteProfile(t.Context(), store.ProfileArchive, archiveProfile.ID); err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodDelete, "/archive/destinations/"+destination.ID, nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	handler.ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("destination delete after reference removal failed: %d %s", retryRes.Code, retryRes.Body.String())
	}
	if _, err := integrations.GetDriveDestination(t.Context(), destination.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected destination deletion, err=%v", err)
	}
}
