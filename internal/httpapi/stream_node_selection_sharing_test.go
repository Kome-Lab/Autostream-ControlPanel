package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestSharedNodeSelectionPreservesRunningFrame(t *testing.T) {
	heartbeatAt := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	auth := store.NewMemoryAuthStore(store.WithMemoryServiceHeartbeatClock(func() time.Time { return heartbeatAt }))
	if err := auth.AddUser(store.User{Username: "sharing-operator", Roles: []string{"stream_operator"}}, "synthetic-sharing-passphrase", []string{
		"streams.create", "streams.read", "streams.update", "streams.delete",
		"services.assign", "workers.assign", "service_health.read",
	}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "sharing-h264", map[string]any{"video_codec": "h264"})
	if err != nil {
		t.Fatal(err)
	}
	otherProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "sharing-hevc", map[string]any{"video_codec": "hevc"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := streams.CreateStream(t.Context(), "running-frame-A")
	if err != nil {
		t.Fatal(err)
	}
	active, err = streams.UpdateStreamSettings(t.Context(), active.ID, store.StreamSettings{EncoderProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	active, err = streams.UpdateStreamStatus(t.Context(), active.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	encoderToken := registerStreamIsolationService(t, auth, "sharing-encoder", "encoder_recorder", []string{
		"service.register", "service.heartbeat", "service.config.read", "encoder.status.write",
	})
	registerStreamIsolationService(t, auth, "sharing-worker", "worker", []string{
		"service.register", "service.heartbeat", "service.config.read",
	})
	for _, serviceID := range []string{"sharing-encoder", "sharing-worker"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, active.ID, "bootstrap"); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := &previewFakeDispatcher{result: servicecall.PreviewAssetResult{
		StatusCode: http.StatusOK,
		Success: true,
		Body: []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nsegment-000001.ts\n"),
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth),
		WithServiceRegistryStore(auth), WithProfileStore(profiles),
		WithServiceDispatcher(dispatcher), WithPreviewSigningKey("synthetic-sharing-preview-key"))
	cookie, csrf := loginForTest(t, handler, "sharing-operator", "synthetic-sharing-passphrase")
	before, err := streams.GetStream(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEncoder, _ := auth.GetService(t.Context(), "sharing-encoder")
	beforeWorker, _ := auth.GetService(t.Context(), "sharing-worker")
	beforeAssignments, _ := auth.ListStreamAssignments(t.Context(), active.ID)
	beforeRuntime := streamIsolationRuntimeConfig(t, handler, encoderToken, "sharing-encoder")

	created := createStreamForIsolation(t, handler, cookie, csrf,
		`{"name":"waiting-frame-B","encoder_profile_id":"`+otherProfile.ID+`","encoder_service_id":"sharing-encoder","worker_service_id":"sharing-worker"}`)
	if created.ID == active.ID || created.Status != "created" {
		t.Fatalf("waiting frame was not independently created: %#v", created)
	}
	checkSelected := func() {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/streams/"+created.ID, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("get waiting frame status=%d body=%s", res.Code, res.Body.String())
		}
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["selected_encoder_id"] != "sharing-encoder" || body["selected_worker_id"] != "sharing-worker" {
			t.Fatalf("saved selections were lost: %#v", body)
		}
		assignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
		if err != nil || len(assignments) != 0 {
			t.Fatalf("waiting selection acquired execution ownership: assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(assignments), err)
		}
	}
	checkSelected()
	edit := httptest.NewRequest(http.MethodPut, "/streams/"+created.ID+"/settings", strings.NewReader(
		`{"name":"edited-waiting-frame-B","encoder_service_id":"sharing-encoder","worker_service_id":"sharing-worker"}`))
	edit.AddCookie(cookie)
	edit.Header.Set("X-CSRF-Token", csrf)
	editRes := httptest.NewRecorder()
	handler.ServeHTTP(editRes, edit)
	if editRes.Code != http.StatusOK {
		t.Fatalf("shared selection edit status=%d body=%s", editRes.Code, editRes.Body.String())
	}
	checkSelected()
	remove := httptest.NewRequest(http.MethodDelete, "/streams/"+created.ID, nil)
	remove.AddCookie(cookie)
	remove.Header.Set("X-CSRF-Token", csrf)
	removeRes := httptest.NewRecorder()
	handler.ServeHTTP(removeRes, remove)
	if removeRes.Code != http.StatusOK {
		t.Fatalf("waiting frame delete status=%d body=%s", removeRes.Code, removeRes.Body.String())
	}

	after, _ := streams.GetStream(t.Context(), active.ID)
	afterEncoder, _ := auth.GetService(t.Context(), "sharing-encoder")
	afterWorker, _ := auth.GetService(t.Context(), "sharing-worker")
	afterAssignments, _ := auth.ListStreamAssignments(t.Context(), active.ID)
	afterRuntime := streamIsolationRuntimeConfig(t, handler, encoderToken, "sharing-encoder")
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeAssignments, afterAssignments) {
		t.Fatal("creating/editing/deleting B changed A or its execution assignments")
	}
	assertStreamIsolationServiceUnchanged(t, "encoder", beforeEncoder, afterEncoder)
	assertStreamIsolationServiceUnchanged(t, "worker", beforeWorker, afterWorker)
	if !reflect.DeepEqual(beforeRuntime, afterRuntime) {
		t.Fatal("B changed the runtime configuration delivered to A's Encoder")
	}
	streamIsolationPreview(t, handler, cookie, active.ID, http.StatusOK)
	streamIsolationPreviewLink(t, handler, cookie, csrf, active.ID, http.StatusCreated)
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 {
		t.Fatalf("saving B dispatched lifecycle requests: start=%d stop=%d retry=%d", dispatcher.startCalls, dispatcher.stopCalls, dispatcher.retryCalls)
	}
}

func TestSharedNodeSelectionExplicitClearDoesNotRestartPreviousAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "clear-operator"}, "synthetic-clear-passphrase", []string{
		"streams.read", "streams.update", "streams.start", "services.assign", "workers.assign",
	}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "old inactive assignment")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, kind string }{
		{"clear-encoder", "encoder_recorder"}, {"clear-worker", "worker"}, {"clear-bot", "discord_bot"},
	} {
		registerStreamIsolationService(t, auth, item.id, item.kind, []string{"service.register", "service.heartbeat"})
		if _, err := auth.AssignServiceToStream(t.Context(), item.id, stream.ID, "bootstrap"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &previewFakeDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth),
		WithServiceRegistryStore(auth), WithProfileStore(store.NewMemoryProfileStore()),
		WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "clear-operator", "synthetic-clear-passphrase")
	beforeAssignments, _ := auth.ListStreamAssignments(t.Context(), stream.ID)
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		method := http.MethodPost
		if strings.HasSuffix(path, "/settings") {
			method = http.MethodPut
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	cleared := request("/streams/"+stream.ID+"/settings",
		`{"encoder_service_id":"","worker_service_id":"","discord_config_id":""}`)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear preferences status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	var saved map[string]any
	if err := json.Unmarshal(cleared.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"selected_encoder_id", "selected_worker_id", "selected_discord_id"} {
		if value, present := saved[key]; !present || value != "" {
			t.Fatalf("explicit clear was lost: field=%s value=%v present=%v", key, value, present)
		}
	}
	readiness := request("/streams/"+stream.ID+"/start-readiness", "{}")
	var ready struct {
		Ready bool `json:"ready"`
		Missing []string `json:"missing_service_types"`
	}
	if err := json.Unmarshal(readiness.Body.Bytes(), &ready); err != nil || readiness.Code != http.StatusOK ||
		ready.Ready || len(ready.Missing) != 3 {
		t.Fatalf("cleared frame reused old execution owners: status=%d body=%s err=%v", readiness.Code, readiness.Body.String(), err)
	}
	started := request("/streams/"+stream.ID+"/start", "{}")
	if started.Code != http.StatusConflict || !strings.Contains(started.Body.String(), "missing_stream_assignments") {
		t.Fatalf("cleared choices started old Nodes: status=%d body=%s", started.Code, started.Body.String())
	}
	afterAssignments, _ := auth.ListStreamAssignments(t.Context(), stream.ID)
	if !reflect.DeepEqual(beforeAssignments, afterAssignments) || len(afterAssignments) != 3 {
		t.Fatal("clearing a preference changed execution assignment rows")
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 {
		t.Fatal("cleared choices dispatched a lifecycle operation")
	}
}
