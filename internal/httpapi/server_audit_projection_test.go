package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestAdminAuditEventNotificationPolicy(t *testing.T) {
	cases := []struct {
		name  string
		event store.AuditEvent
		want  bool
	}{
		{name: "oauth account update", event: store.AuditEvent{Action: "oauth_accounts.update", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "notification channel create", event: store.AuditEvent{Action: "notification_channels.create", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "notification channel test is already delivered directly", event: store.AuditEvent{Action: "notification_channels.test", ActorUserID: "user-01", ActorUsername: "ops"}, want: false},
		{name: "stream start", event: store.AuditEvent{Action: "streams.start", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "authenticated login", event: store.AuditEvent{Action: "auth.login", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "mfa", event: store.AuditEvent{Action: "mfa.enroll", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "passkey", event: store.AuditEvent{Action: "passkeys.delete", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "archive", event: store.AuditEvent{Action: "archive.artifact.delete", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "remediation", event: store.AuditEvent{Action: "remediation.approve", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "profile", event: store.AuditEvent{Action: "encoder_profiles.update", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "api token", event: store.AuditEvent{Action: "api_tokens.rotate", ActorUserID: "user-01", ActorUsername: "ops"}, want: true},
		{name: "system actor", event: store.AuditEvent{Action: "youtube.complete", ActorUsername: "system"}, want: true},
		{name: "unauthenticated login failure", event: store.AuditEvent{Action: "auth.login", ResourceID: "attacker"}, want: false},
		{name: "username without authenticated id", event: store.AuditEvent{Action: "streams.start", ActorUsername: "ops"}, want: false},
		{name: "service actor stays out", event: store.AuditEvent{Action: "nodes.update", ActorUsername: "service:worker"}, want: false},
		{name: "service actor id stays out", event: store.AuditEvent{Action: "streams.start", ActorUserID: "service:encoder_recorder", ActorUsername: "encoder_recorder"}, want: false},
		{name: "updater success reaches channels", event: store.AuditEvent{Action: "system_updates.succeeded", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: true},
		{name: "updater rollback reaches channels", event: store.AuditEvent{Action: "system_updates.rolled_back", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: true},
		{name: "updater failure reaches channels", event: store.AuditEvent{Action: "system_updates.failed", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: true},
		{name: "bootstrap success reaches channels", event: store.AuditEvent{Action: "system_updates.bootstrap.succeeded", ActorUserID: "service:update_agent", ActorUsername: "update_agent"}, want: true},
		{name: "bootstrap failure reaches channels", event: store.AuditEvent{Action: "system_updates.bootstrap.failed", ActorUserID: "service:update_agent", ActorUsername: "update_agent"}, want: true},
		{name: "bootstrap progress stays out", event: store.AuditEvent{Action: "system_updates.bootstrap.report", ActorUserID: "service:update_agent", ActorUsername: "update_agent"}, want: false},
		{name: "updater claim stays out", event: store.AuditEvent{Action: "system_updates.claim", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: false},
		{name: "updater report stays out", event: store.AuditEvent{Action: "system_updates.report", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: false},
		{name: "updater authorize stays out", event: store.AuditEvent{Action: "system_updates.authorize", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: false},
		{name: "terminal prefix is not enough", event: store.AuditEvent{Action: "system_updates.succeeded.replay", ActorUserID: "service:updater-01", ActorUsername: "updater-01"}, want: false},
		{name: "blank action stays out", event: store.AuditEvent{ActorUserID: "user-01", ActorUsername: "ops"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adminAuditEventNotificationAllowed(tc.event); got != tc.want {
				t.Fatalf("adminAuditEventNotificationAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdminAuditNotificationSummaryUsesRedactedEvent(t *testing.T) {
	event := store.RedactAuditEvent(store.AuditEvent{
		Action:       "secrets.update",
		ResourceType: "secret",
		ResourceID:   "raw-secret-token",
		Result:       "success",
		Metadata:     map[string]any{"webhook_url": "https://discord.com/api/webhooks/id/raw-secret-token"},
	})
	summary := adminAuditNotificationSummary(event)
	metadata := toJSONForTest(t, event.Metadata)
	if strings.Contains(summary, "raw-secret-token") || strings.Contains(metadata, "raw-secret-token") {
		t.Fatalf("admin audit notification leaked raw secret: summary=%q metadata=%s", summary, metadata)
	}
	if summary != "管理イベント: secrets.update / success" {
		t.Fatalf("admin audit summary duplicated structured resource fields: %q", summary)
	}
	if severity := adminAuditNotificationSeverity(event); severity != "warning" {
		t.Fatalf("security-related admin audit severity = %q, want warning", severity)
	}
}

func TestAdminAuditNotificationServiceIDDoesNotTreatEveryTargetAsService(t *testing.T) {
	if got := adminAuditNotificationServiceID(store.AuditEvent{Action: "streams.start", Metadata: map[string]any{"service_id": "observability"}}); got != "control-panel" {
		t.Fatalf("receiver service was mislabeled as the source: %q", got)
	}
	if got := adminAuditNotificationServiceID(store.AuditEvent{Action: "streams.start", Metadata: map[string]any{"target_id": "stream-01"}}); got != "control-panel" {
		t.Fatalf("stream target was mislabeled as a service: %q", got)
	}
	if got := adminAuditNotificationServiceID(store.AuditEvent{Action: "system_updates.failed", Metadata: map[string]any{"target_id": "worker-01", "target_service_type": "worker"}}); got != "worker-01" {
		t.Fatalf("system update target service was lost: %q", got)
	}
	if got := adminAuditNotificationServiceID(store.AuditEvent{Action: "services.runtime_config.read", ResourceType: "service", ResourceID: "encoder-01"}); got != "encoder-01" {
		t.Fatalf("service resource was not used as notification source: %q", got)
	}
}

func TestAdminAuditNotificationUsesStrictRedactedPayload(t *testing.T) {
	type receivedRequest struct {
		body          []byte
		authorization string
		decodeErr     error
		payload       struct {
			EventType     string `json:"event_type"`
			Severity      string `json:"severity"`
			Status        string `json:"status"`
			Action        string `json:"action"`
			ServiceID     string `json:"service_id"`
			ResourceType  string `json:"resource_type"`
			ResourceID    string `json:"resource_id"`
			ActorUsername string `json:"actor_username"`
			Summary       string `json:"summary"`
			Details       string `json:"details"`
			Timestamp     string `json:"timestamp"`
		}
	}
	received := make(chan receivedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		got := receivedRequest{body: body, authorization: r.Header.Get("Authorization"), decodeErr: err}
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			got.decodeErr = decoder.Decode(&got.payload)
		}
		received <- got
		if got.decodeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
	}))
	defer upstream.Close()

	auth := store.NewMemoryAuthStore()
	token := registerObservabilityNodeForTest(t, auth, "audit-notification-token", upstream.URL)
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuditStore(auth), WithServiceRegistryStore(auth))
	request := httptest.NewRequest(http.MethodPost, "/test-audit", nil)
	server.writeAudit(request, store.AuditEvent{
		ActorUserID:   "user-01",
		ActorUsername: "ops",
		Action:        "secrets.update",
		ResourceType:  "secret",
		ResourceID:    "raw-secret-token",
		Result:        "success",
		Metadata: map[string]any{
			"webhook_url": "https://discord.com/api/webhooks/id/raw-secret-token",
			"password":    "raw-password",
		},
	})

	var got receivedRequest
	select {
	case got = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for admin audit notification")
	}
	if got.decodeErr != nil {
		t.Fatalf("strict notification payload decode failed: %v body=%s", got.decodeErr, got.body)
	}
	if got.authorization != "Bearer "+token.RawToken {
		t.Fatalf("notification authorization = %q", got.authorization)
	}
	if got.payload.EventType != "admin.audit" || got.payload.Action != "secrets.update" || got.payload.Status != "success" || got.payload.ActorUsername != "ops" || got.payload.ServiceID != "control-panel" {
		t.Fatalf("unexpected admin audit notification: %#v", got.payload)
	}
	if got.payload.ResourceID != "<redacted>" {
		t.Fatalf("secret resource id was not redacted: %q", got.payload.ResourceID)
	}
	if strings.Contains(string(got.body), "metadata") || strings.Contains(string(got.body), "raw-secret-token") || strings.Contains(string(got.body), "raw-password") {
		t.Fatalf("notification payload leaked unsupported metadata or a secret: %s", got.body)
	}
	var fields map[string]any
	if err := json.Unmarshal(got.body, &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := []string{"event_type", "severity", "status", "action", "service_id", "resource_type", "resource_id", "actor_username", "summary", "details", "timestamp"}
	if len(fields) != len(wantFields) {
		t.Fatalf("notification payload fields = %#v, want exactly %#v", fields, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Fatalf("notification payload is missing %q: %#v", field, fields)
		}
	}
}

func TestWriteAuditEnrichesOnlyAuthenticatedRequestActor(t *testing.T) {
	audit := store.NewMemoryAuthStore()
	server := NewServer(store.NewMemoryStreamStore(), WithAuditStore(audit))
	authenticated := httptest.NewRequest(http.MethodPost, "/streams/stream-01/start", nil)
	authenticated = authenticated.WithContext(context.WithValue(authenticated.Context(), currentUserKey{}, currentUser{
		User: store.User{ID: "user-01", Username: "ops"},
	}))
	server.writeAudit(authenticated, store.AuditEvent{Action: "streams.start", ResourceType: "stream", ResourceID: "stream-01", Result: "success"})

	public := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	server.writeAudit(public, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: "attacker", Result: "failure"})

	events := audit.AuditEvents()
	if len(events) != 2 {
		t.Fatalf("audit event count = %d, want 2", len(events))
	}
	if events[0].ActorUserID != "user-01" || events[0].ActorUsername != "ops" || !adminAuditEventNotificationAllowed(events[0]) {
		t.Fatalf("authenticated audit actor was not enriched: %#v", events[0])
	}
	if events[1].ActorUserID != "" || events[1].ActorUsername != "" || adminAuditEventNotificationAllowed(events[1]) {
		t.Fatalf("public audit unexpectedly acquired an authenticated actor: %#v", events[1])
	}
}

func TestWriteSystemAuditPersistsTimestampAndNotifies(t *testing.T) {
	received := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		received <- payload
		writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
	}))
	defer upstream.Close()

	auth := store.NewMemoryAuthStore()
	token := registerObservabilityNodeForTest(t, auth, "system-audit-token", upstream.URL)
	if _, err := auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuditStore(auth), WithServiceRegistryStore(auth))
	server.writeSystemAudit(t.Context(), store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: "stream-01", Result: "success"})

	select {
	case payload := <-received:
		if payload["event_type"] != "admin.audit" || payload["action"] != "youtube.complete" || payload["actor_username"] != "system" {
			t.Fatalf("unexpected system audit notification: %#v", payload)
		}
		if timestamp, _ := payload["timestamp"].(string); timestamp == "" {
			t.Fatalf("system audit notification timestamp is missing: %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for system audit notification")
	}

	events := auth.AuditEvents()
	if len(events) != 1 || events[0].ActorUsername != "system" || events[0].Timestamp.IsZero() {
		t.Fatalf("system audit was not persisted with actor and timestamp: %#v", events)
	}
}

func TestAuditStoreFailureDoesNotNotify(t *testing.T) {
	received := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
	}))
	defer upstream.Close()

	services := store.NewMemoryAuthStore()
	token := registerObservabilityNodeForTest(t, services, "failed-audit-token", upstream.URL)
	if _, err := services.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuditStore(failingAuditStore{}), WithServiceRegistryStore(services))
	server.writeAudit(httptest.NewRequest(http.MethodPost, "/test-audit", nil), store.AuditEvent{Action: "streams.start", ResourceType: "stream", ResourceID: "stream-01", Result: "success"})
	server.writeSystemAudit(t.Context(), store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: "stream-01", Result: "success"})

	select {
	case <-received:
		t.Fatal("notification must not be attempted when the audit record was not persisted")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSafeStreamLogMetadataPreservesTypedCollectionsAndRedactsSecrets(t *testing.T) {
	when := time.Date(2026, time.August, 21, 12, 0, 0, 123, time.FixedZone("JST", 9*60*60))
	got := safeStreamLogMetadata(map[string]any{
		"missing_service_types": []string{"worker", "encoder_recorder"},
		"labels": map[string]string{
			"node":         "worker-01",
			"secret_token": "must-not-leak",
			"callback_url": "https://secret.invalid",
		},
		"occurred_at":   when,
		"authorization": "must-not-leak",
	})

	missing, ok := got["missing_service_types"].([]string)
	if !ok || !reflect.DeepEqual(missing, []string{"worker", "encoder_recorder"}) {
		t.Fatalf("typed string slice was not preserved: %#v", got)
	}
	labels, ok := got["labels"].(map[string]any)
	if !ok || !reflect.DeepEqual(labels, map[string]any{"node": "worker-01"}) {
		t.Fatalf("typed map was not safely filtered: %#v", got)
	}
	if got["occurred_at"] != when.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("timestamp was not normalized: %#v", got)
	}
	if _, exists := got["authorization"]; exists {
		t.Fatalf("authorization leaked into stream log metadata: %#v", got)
	}
}

func TestStartStreamAuditsYouTubeRuntimeSaveFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "runtime save failure stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "runtime save failure discord", "discord_bot-01", "guild-youtube", "voice-youtube", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "runtime-save-failure-output", map[string]any{
		"mode":     "live_api_dry_run",
		"rtmp_url": "rtmps://youtube.example.com/live2",
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	dispatcher := &fakeServiceDispatcher{}
	failingStore := &failingYouTubeRuntimeStreamStore{
		MemoryStreamStore: streams,
		err:               errors.New("write tcp: connection reset by peer"),
	}
	handler := NewServer(failingStore, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+discord.ID+`","youtube_output_id":"`+youtube.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"detail_code":"database_connection_transient"`) {
		t.Fatalf("runtime save failure response = %d %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "connection reset by peer") {
		t.Fatalf("raw database error leaked in response: %s", res.Body.String())
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	if !strings.Contains(auditJSON, `"reason":"save_youtube_runtime_failed"`) || !strings.Contains(auditJSON, `"error_code":"database_connection_transient"`) || strings.Contains(auditJSON, "connection reset by peer") {
		t.Fatalf("runtime save failure audit missing or leaked raw error: %s", auditJSON)
	}
	updated, err := failingStore.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "failed" {
		t.Fatalf("claimed stream did not converge after runtime save failure: %s", updated.Status)
	}
}

func TestYouTubeOutputResponseRedactsNoncanonicalRelayBinding(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"youtube_outputs.read"}); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	const rawBindingID = "yt-stream-key-like-value"
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "legacy unsafe fixed relay", map[string]any{
		"mode":                    "live_api_relay_static",
		"relay_binding_id":        rawBindingID,
		"reusable_live_stream_id": "youtube-live-stream-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/youtube/outputs/"+profile.ID, nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("get unsafe fixed relay status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), rawBindingID) || strings.Contains(res.Body.String(), `"relay_binding_id"`) {
		t.Fatalf("noncanonical relay binding leaked in output response: %s", res.Body.String())
	}
}

func TestRuntimeYouTubeRelayStaticConfigRejectsAndRedactsNoncanonicalBinding(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "unsafe fixed relay runtime")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	const rawBindingID = "yt-stream-key-like-value"
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "unsafe fixed relay runtime", map[string]any{
		"mode":                    "live_api_relay_static",
		"relay_binding_id":        rawBindingID,
		"reusable_live_stream_id": "youtube-live-stream-primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID}); err != nil {
		t.Fatal(err)
	}
	server := &Server{streams: streams, profiles: profiles}
	assignment := store.StreamServiceAssignment{StreamID: stream.ID, ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", AssignmentRole: "primary"}
	service := store.RegisteredService{
		ServiceID:   assignment.ServiceID,
		ServiceType: assignment.ServiceType,
		Capabilities: map[string]any{
			"output_relay_mode":       "live_api_relay_static",
			"output_relay_binding_id": "relay-00000000-0000-4000-8000-000000000001",
		},
	}
	configs, err := server.runtimeYouTubeStreamConfigs(t.Context(), service, []store.StreamServiceAssignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Ready || configs[0].ReadinessCode != "youtube_output_invalid_config" {
		t.Fatalf("noncanonical relay binding must be not-ready: %#v", configs)
	}
	if _, present := configs[0].YouTubeConfig["relay_binding_id"]; present {
		t.Fatalf("noncanonical relay binding reached the Encoder runtime config: %#v", configs[0].YouTubeConfig)
	}
	runtimeResponse, err := json.Marshal(configs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runtimeResponse), rawBindingID) {
		t.Fatalf("noncanonical relay binding leaked in runtime response: %s", runtimeResponse)
	}
	profileResponse := sanitizeRuntimeProfileConfigForKind(store.ProfileYouTubeOutput, youtube.Config)
	if _, present := profileResponse["relay_binding_id"]; present {
		t.Fatalf("noncanonical relay binding leaked in runtime profile response: %#v", profileResponse)
	}
}

func TestHistoricalStreamLogsIncludeDeletedStreamsAndRedactedAuditContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "auditor"}, "correct horse battery", []string{"logs.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "historical stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	handler.writeSystemAudit(t.Context(), store.AuditEvent{
		Action:       "streams.stop",
		ResourceType: "stream",
		ResourceID:   stream.ID,
		Result:       "failure",
		Metadata:     map[string]any{"reason": "encoder_timeout", "authorization": "Bearer must-not-leak"},
	})
	if err := streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}
	cookie, _ := loginForTest(t, handler, "auditor", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/stream-logs", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("historical stream logs status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), stream.ID) || !strings.Contains(res.Body.String(), "historical stream") || !strings.Contains(res.Body.String(), "streams.stop") || !strings.Contains(res.Body.String(), "encoder_timeout") {
		t.Fatalf("historical stream log context missing: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "must-not-leak") || strings.Contains(strings.ToLower(res.Body.String()), "authorization") {
		t.Fatalf("historical stream log leaked sensitive audit metadata: %s", res.Body.String())
	}
}

func TestSendWorkerTestEventDispatchesAssignedWorkerAndAudits(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "audit_logs.read"}); err != nil {
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
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/worker-events/test", bytes.NewBufferString(`{"event_type":"caption","text":"hello","speaker_user_id":"user-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.workerEventSendCalls != 1 || dispatcher.workerEventRequest.Text != "hello" {
		t.Fatalf("worker event dispatcher was not called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"streams.worker_event_test"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Result != "success" || events[0].ResourceID != stream.ID {
		t.Fatalf("missing audit event: %#v", events)
	}
}

func TestClientIPOnlyTrustsForwardedForFromTrustedProxy(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "198.51.100.10:54321"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.99")
	if got := clientIP(untrusted); got != "198.51.100.10" {
		t.Fatalf("untrusted proxy X-Forwarded-For should be ignored, got %q", got)
	}

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.1.2.3:54321"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.99, 10.1.2.3")
	if got := clientIP(trusted); got != "203.0.113.99" {
		t.Fatalf("trusted proxy X-Forwarded-For should be used, got %q", got)
	}
}

func TestClientIPWalksTrustedProxyChainFromRight(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, 203.0.113.66, 10.0.0.20")
	if got := clientIP(req); got != "203.0.113.66" {
		t.Fatalf("expected first untrusted address from right, got %q", got)
	}
}

func TestClientIPRejectsMalformedForwardedChain(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, malformed")
	if got := clientIP(req); got != "10.0.0.10" {
		t.Fatalf("malformed chain must fall back to direct peer, got %q", got)
	}
}

func TestClientIPDoesNotTrustLoopbackImplicitly(t *testing.T) {
	t.Setenv("AUTOSTREAM_TRUSTED_PROXIES", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	if got := clientIP(req); got != "127.0.0.1" {
		t.Fatalf("loopback must require explicit trusted proxy configuration, got %q", got)
	}
}

func TestChangePasswordInvalidatesSessionAndAudits(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewBufferString(`{"current_password":"correct horse battery","new_password":"new correct battery"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("change password status = %d body = %s", res.Code, res.Body.String())
	}
	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.AddCookie(cookie)
	meRes := httptest.NewRecorder()
	handler.ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusUnauthorized {
		t.Fatalf("old session should be invalidated, status = %d body = %s", meRes.Code, meRes.Body.String())
	}
	_, newCSRF := loginForTest(t, handler, "operator", "new correct battery")
	if newCSRF == "" {
		t.Fatal("expected login with new password")
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "auth.login" {
		t.Fatalf("unexpected audit tail: %#v", events)
	}
	found := false
	for _, event := range events {
		if event.Action == "auth.change_password" && event.Result == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing successful change password audit event: %#v", events)
	}
}

func TestAuditLogsListAndExport(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "auditor"}, "correct horse battery", []string{"audit_logs.read", "audit_logs.export"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "auditor", "correct horse battery")
	listReq := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), "auth.login") {
		t.Fatalf("audit list status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "services.assign", ResourceType: "service", ResourceID: "enc-01", Result: "success", Metadata: map[string]any{"stream_id": "stream-01", "service_type": "encoder_recorder"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "services.runtime_config.read", ResourceType: "service", ResourceID: "enc-01", Result: "success", Metadata: map[string]any{"assignment_count": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "services.register", ResourceType: "service", ResourceID: "updater-01", Result: "success", Metadata: map[string]any{"service_type": "update_agent"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "observability.signals.ingest", ResourceType: "service", ResourceID: "worker-01", Result: "success", Metadata: map[string]any{"signal_count": 12}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "streams.start", ResourceType: "stream", ResourceID: "stream-01", Result: "failure", Metadata: map[string]any{"missing_service_types": []string{"worker"}}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "notification_channels.create", ResourceType: "notification_channel", ResourceID: "chn-01", Result: "success", Metadata: map[string]any{"has_webhook_url": true, "webhook_url": "https://discord.com/api/webhooks/id/raw-secret-token"}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Action: "secrets.update", ResourceType: "secret", ResourceID: "DISCORD_BOT_TOKEN", Result: "success", Metadata: map[string]any{"configured": true, "value": "super-raw-discord-token"}}); err != nil {
		t.Fatal(err)
	}
	redactedListReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=notifications", nil)
	redactedListReq.AddCookie(cookie)
	redactedListRes := httptest.NewRecorder()
	handler.ServeHTTP(redactedListRes, redactedListReq)
	if redactedListRes.Code != http.StatusOK || strings.Contains(redactedListRes.Body.String(), "raw-secret-token") || strings.Contains(redactedListRes.Body.String(), "discord.com/api/webhooks") {
		t.Fatalf("audit list leaked metadata secret: status=%d body=%s", redactedListRes.Code, redactedListRes.Body.String())
	}
	filterReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=service_assignment&result=success&q=enc-01", nil)
	filterReq.AddCookie(cookie)
	filterRes := httptest.NewRecorder()
	handler.ServeHTTP(filterRes, filterReq)
	if filterRes.Code != http.StatusOK {
		t.Fatalf("audit filter status = %d body = %s", filterRes.Code, filterRes.Body.String())
	}
	var filtered []store.AuditEvent
	if err := json.NewDecoder(filterRes.Body).Decode(&filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Action != "services.assign" || filtered[0].ResourceID != "enc-01" {
		t.Fatalf("unexpected filtered audit events: %#v", filtered)
	}
	runtimeReadReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=service_runtime_reads", nil)
	runtimeReadReq.AddCookie(cookie)
	runtimeReadRes := httptest.NewRecorder()
	handler.ServeHTTP(runtimeReadRes, runtimeReadReq)
	if runtimeReadRes.Code != http.StatusOK {
		t.Fatalf("runtime read audit status = %d body = %s", runtimeReadRes.Code, runtimeReadRes.Body.String())
	}
	var runtimeReads []store.AuditEvent
	if err := json.NewDecoder(runtimeReadRes.Body).Decode(&runtimeReads); err != nil {
		t.Fatal(err)
	}
	if len(runtimeReads) != 2 || runtimeReads[0].Action != "services.register" || runtimeReads[1].Action != "services.runtime_config.read" {
		t.Fatalf("unexpected runtime read audit events: %#v", runtimeReads)
	}
	nodeActivityReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=node_activity", nil)
	nodeActivityReq.AddCookie(cookie)
	nodeActivityRes := httptest.NewRecorder()
	handler.ServeHTTP(nodeActivityRes, nodeActivityReq)
	if nodeActivityRes.Code != http.StatusOK {
		t.Fatalf("node activity audit status = %d body = %s", nodeActivityRes.Code, nodeActivityRes.Body.String())
	}
	var nodeActivity []store.AuditEvent
	if err := json.NewDecoder(nodeActivityRes.Body).Decode(&nodeActivity); err != nil {
		t.Fatal(err)
	}
	if len(nodeActivity) != 3 || nodeActivity[0].Action != "observability.signals.ingest" || nodeActivity[1].Action != "services.register" || nodeActivity[2].Action != "services.runtime_config.read" {
		t.Fatalf("unexpected node activity audit events: %#v", nodeActivity)
	}
	operationsReq := httptest.NewRequest(http.MethodGet, "/audit-logs?exclude_action_group=node_activity", nil)
	operationsReq.AddCookie(cookie)
	operationsRes := httptest.NewRecorder()
	handler.ServeHTTP(operationsRes, operationsReq)
	if operationsRes.Code != http.StatusOK {
		t.Fatalf("operations audit status = %d body = %s", operationsRes.Code, operationsRes.Body.String())
	}
	var operations []store.AuditEvent
	if err := json.NewDecoder(operationsRes.Body).Decode(&operations); err != nil {
		t.Fatal(err)
	}
	if len(operations) == 0 {
		t.Fatal("operations audit unexpectedly returned no events")
	}
	for _, event := range operations {
		if event.Action == "services.runtime_config.read" || event.Action == "services.register" || event.Action == "observability.signals.ingest" {
			t.Fatalf("operations audit included runtime read event: %#v", operations)
		}
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Timestamp: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), Action: "streams.start", ResourceType: "stream", ResourceID: "dated-in", Result: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteAudit(t.Context(), store.AuditEvent{Timestamp: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), Action: "streams.start", ResourceType: "stream", ResourceID: "dated-out", Result: "success"}); err != nil {
		t.Fatal(err)
	}
	dateReq := httptest.NewRequest(http.MethodGet, "/audit-logs?from=2026-07-01&to=2026-07-01&q=dated", nil)
	dateReq.AddCookie(cookie)
	dateRes := httptest.NewRecorder()
	handler.ServeHTTP(dateRes, dateReq)
	if dateRes.Code != http.StatusOK {
		t.Fatalf("audit date filter status = %d body = %s", dateRes.Code, dateRes.Body.String())
	}
	var dated []store.AuditEvent
	if err := json.NewDecoder(dateRes.Body).Decode(&dated); err != nil {
		t.Fatal(err)
	}
	if len(dated) != 1 || dated[0].ResourceID != "dated-in" {
		t.Fatalf("unexpected date-filtered audit events: %#v", dated)
	}
	exportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export", nil)
	exportReq.AddCookie(cookie)
	exportRes := httptest.NewRecorder()
	handler.ServeHTTP(exportRes, exportReq)
	if exportRes.Code != http.StatusOK || !strings.Contains(exportRes.Body.String(), "auth.login") || !strings.Contains(exportRes.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("audit export status = %d headers = %#v body = %s", exportRes.Code, exportRes.Header(), exportRes.Body.String())
	}
	if !strings.Contains(exportRes.Body.String(), "user_agent") {
		t.Fatalf("audit export header missing user_agent: %s", exportRes.Body.String())
	}
	if strings.Contains(exportRes.Body.String(), "raw-secret-token") || strings.Contains(exportRes.Body.String(), "super-raw-discord-token") {
		t.Fatalf("audit export leaked metadata secret: %s", exportRes.Body.String())
	}
	operationsExportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export?exclude_action_group=node_activity", nil)
	operationsExportReq.AddCookie(cookie)
	operationsExportRes := httptest.NewRecorder()
	handler.ServeHTTP(operationsExportRes, operationsExportReq)
	if operationsExportRes.Code != http.StatusOK || strings.Contains(operationsExportRes.Body.String(), "services.runtime_config.read") || !strings.Contains(operationsExportRes.Body.String(), "services.assign") {
		t.Fatalf("operations audit export status = %d body = %s", operationsExportRes.Code, operationsExportRes.Body.String())
	}
	notificationExportReq := httptest.NewRequest(http.MethodGet, "/audit-logs/export?action_group=notifications", nil)
	notificationExportReq.AddCookie(cookie)
	notificationExportRes := httptest.NewRecorder()
	handler.ServeHTTP(notificationExportRes, notificationExportReq)
	if notificationExportRes.Code != http.StatusOK || !strings.Contains(notificationExportRes.Body.String(), "notification_channels.create") || strings.Contains(notificationExportRes.Body.String(), "raw-secret-token") {
		t.Fatalf("notification audit export status = %d body = %s", notificationExportRes.Code, notificationExportRes.Body.String())
	}
}

func TestLoginFailureAudited(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"operator","password":"wrong password"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	events := auth.AuditEvents()
	if len(events) != 1 || events[0].Action != "auth.login" || events[0].Result != "failure" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
}

func TestRedactRawJSONDoesNotEchoInvalidUpstreamBody(t *testing.T) {
	body := redactRawJSON(json.RawMessage(`raw upstream token https://discord.com/api/webhooks/id/upstream-secret-token`))
	if strings.Contains(string(body), "upstream-secret-token") || strings.Contains(string(body), "discord.com/api/webhooks") {
		t.Fatalf("invalid upstream body leaked raw content: %s", string(body))
	}
	if !strings.Contains(string(body), "invalid_upstream_json") {
		t.Fatalf("expected safe invalid JSON marker, got %s", string(body))
	}
}

func TestRedactRawJSONRedactsScalarSecretLikeString(t *testing.T) {
	for _, raw := range []string{
		`"Bearer upstream-secret-token"`,
		`"https://discord.com/api/webhooks/id/upstream-secret-token"`,
		`"https://example.com/callback?api_key=upstream-secret-token"`,
		`"ast_svc_upstream-secret-token"`,
		`"ast_ingest_v1.upstream-secret-token.signature"`,
		`"ya29.upstream-secret-token"`,
	} {
		body := redactRawJSON(json.RawMessage(raw))
		if strings.Contains(string(body), "upstream-secret-token") || strings.Contains(string(body), "discord.com/api/webhooks") || strings.Contains(string(body), "api_key=") || strings.Contains(string(body), "ast_svc_") || strings.Contains(string(body), "ast_ingest_v1.") || strings.Contains(string(body), "ya29.") {
			t.Fatalf("scalar upstream JSON leaked raw content: %s", string(body))
		}
		if !strings.Contains(string(body), "redacted") {
			t.Fatalf("expected scalar upstream JSON to be redacted, got %s", string(body))
		}
	}
}

func TestRedactRawJSONRedactsNestedServiceTokens(t *testing.T) {
	body := redactRawJSON(json.RawMessage(`{"message":"ast_svc_upstream-secret-token","items":[{"detail":"ast_ingest_v1.upstream-secret-token.signature"}]}`))
	out := string(body)
	if strings.Contains(out, "upstream-secret-token") || strings.Contains(out, "ast_svc_") || strings.Contains(out, "ast_ingest_v1.") {
		t.Fatalf("nested upstream JSON leaked token-like value: %s", out)
	}
	if strings.Count(out, "redacted") != 2 {
		t.Fatalf("expected nested token-like values to be redacted, got %s", out)
	}
}

func TestWriteOneTimeSecretJSONSetsStrictNoStoreHeaders(t *testing.T) {
	res := httptest.NewRecorder()
	writeOneTimeSecretJSON(res, http.StatusOK, map[string]string{"token": "one-time-value"})
	header := res.Result().Header
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if got := header.Get("Expires"); got != "0" {
		t.Fatalf("Expires = %q, want 0", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}
