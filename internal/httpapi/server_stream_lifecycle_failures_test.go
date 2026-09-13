package httpapi

import (
	"bytes"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
