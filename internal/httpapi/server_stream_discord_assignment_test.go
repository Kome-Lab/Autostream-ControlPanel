package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

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

func TestCreateStreamSavesSharedDiscordBotWithoutChangingActiveOwnership(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	active, err := streams.CreateStream(t.Context(), "active Discord owner")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, active.ID, "encoder_recorder", "worker", "discord_bot")
	if _, err := streams.UpdateStreamStatus(t.Context(), active.ID, "live"); err != nil {
		t.Fatal(err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoderID := assignedServiceID(assignments, "encoder_recorder")
	workerID := assignedServiceID(assignments, "worker")
	botID := assignedServiceID(assignments, "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "shared Discord authentication", map[string]any{"service_id": botID})
	if err != nil {
		t.Fatal(err)
	}
	registry := &streamIsolationServiceRegistry{ServiceRegistryStore: auth}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeActive, err := streams.GetStream(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeBot, err := auth.GetService(t.Context(), botID)
	if err != nil {
		t.Fatal(err)
	}
	payload := ui188CreatePayload(t, handler, `{"name":"second Discord frame","encoder_service_id":"`+encoderID+`","worker_service_id":"`+workerID+`","discord_config_id":"`+config.ID+`","auto_start_trigger":"discord_voice_join","visual_settings":{"expected_revision":0,"discord_target":{"mode":"manual","guild_id":"1001","text_channel_id":"1002","voice_channel_id":"1003"}}}`)
	res := serveUserJSON(t, handler, http.MethodPost, "/streams", payload, cookie, csrf)
	if res.Code != http.StatusCreated {
		t.Fatalf("shared Discord configuration save status=%d body=%s", res.Code, res.Body.String())
	}
	var created store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.DiscordConfigID != config.ID || created.SelectedDiscordID != botID || created.SelectedEncoderID != encoderID || created.SelectedWorkerID != workerID || created.AssignedEncoderID != "" || created.AssignedWorkerID != "" {
		t.Fatalf("shared Discord configuration did not persist separate selections: %#v", created)
	}
	createdAssignments, err := auth.ListStreamAssignments(t.Context(), created.ID)
	if err != nil || len(createdAssignments) != 0 {
		t.Fatal("saving shared Discord settings materialized runtime assignments")
	}
	afterActive, err := streams.GetStream(t.Context(), active.ID)
	if err != nil || !reflect.DeepEqual(beforeActive, afterActive) {
		t.Fatal("saving a second Discord frame changed the active stream")
	}
	afterBot, err := auth.GetService(t.Context(), botID)
	if err != nil || !reflect.DeepEqual(beforeBot, afterBot) {
		t.Fatal("saving a second Discord frame changed the active Bot")
	}
	activeAssignments, err := auth.ListStreamAssignments(t.Context(), active.ID)
	if err != nil || len(activeAssignments) != 3 || assignedServiceID(activeAssignments, "discord_bot") != botID || assignedServiceID(activeAssignments, "encoder_recorder") != encoderID || assignedServiceID(activeAssignments, "worker") != workerID {
		t.Fatal("saving a second Discord frame changed active runtime assignments")
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 {
		t.Fatal("saving shared Discord settings dispatched a lifecycle operation")
	}
	assertNoStreamIsolationAssignmentWrites(t, registry)
}
