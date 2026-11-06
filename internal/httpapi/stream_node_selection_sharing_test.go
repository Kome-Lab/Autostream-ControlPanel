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
