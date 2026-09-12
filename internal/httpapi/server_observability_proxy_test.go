package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestServiceRemediationExecuteDispatchesAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat", "observability.ingest", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	obsClient, closeObs := remediationValidationClient(t, obsToken.RawToken, map[string]observability.RemediationDispatchContext{
		"action-01": {ActionID: "action-01", Action: "retry_package_remux", IncidentID: "inc-01", StreamID: stream.ID, Executable: true},
	})
	defer closeObs()
	registerObservabilityNodeWithTokenForTest(t, auth, obsToken, obsClient.BaseURL)
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 || len(dispatcher.retriedServices) != 1 || dispatcher.retriedServices[0].ServiceType != "encoder_recorder" {
		t.Fatalf("expected encoder retry dispatch, got %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "remediation.execute" || events[len(events)-1].Result != "success" {
		t.Fatalf("expected remediation audit event, got %#v", events)
	}
	if events[len(events)-1].Metadata["action_id"] != "action-01" || events[len(events)-1].Metadata["incident_id"] != "inc-01" {
		t.Fatalf("expected remediation context in audit metadata, got %#v", events[len(events)-1])
	}
	if strings.Contains(res.Body.String(), obsToken.RawToken) {
		t.Fatalf("service token leaked in response: %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "remediation_action_replayed") {
		t.Fatalf("expected replay rejection, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 1 {
		t.Fatalf("replayed action should not dispatch again: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestServiceRemediationExecuteRejectsUnverifiedObservabilityContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "service.heartbeat", "observability.ingest", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	obsClient, closeObs := remediationValidationClient(t, obsToken.RawToken, map[string]observability.RemediationDispatchContext{
		"action-01": {ActionID: "action-01", Action: "retry_package_remux", IncidentID: "different-incident", StreamID: stream.ID, Executable: true},
	})
	defer closeObs()
	registerObservabilityNodeWithTokenForTest(t, auth, obsToken, obsClient.BaseURL)
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-01","action":"retry_package_remux","incident_id":"inc-01","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "remediation_context_not_verified") {
		t.Fatalf("expected context verification failure, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called for unverified context: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestServiceRemediationExecuteRequiresActionAndIncidentContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, obsToken, store.ServiceRegistration{ServiceID: "observability-01", ServiceType: "observability", ServiceName: "Observability", PublicURL: "https://observability.example.com"})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	for name, body := range map[string]string{
		"missing_action_id":   `{"action":"retry_package_remux","incident_id":"inc-01","stream_id":"` + stream.ID + `"}`,
		"missing_incident_id": `{"action_id":"action-missing-incident","action":"retry_package_remux","stream_id":"` + stream.ID + `"}`,
		"missing_stream_id":   `{"action_id":"action-missing-stream","action":"retry_package_remux","incident_id":"inc-01"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "remediation_context_required") {
				t.Fatalf("expected remediation context rejection, got %d body = %s", res.Code, res.Body.String())
			}
		})
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called without complete context: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
}

func TestServiceRemediationExecuteRejectsInvalidServiceToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-worker","action":"retry_gdrive_upload","incident_id":"inc-worker","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-observability token, got %d body = %s", res.Code, res.Body.String())
	}

	noScopeToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-noscope","action":"retry_gdrive_upload","incident_id":"inc-noscope","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+noScopeToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing scope, got %d body = %s", res.Code, res.Body.String())
	}

	pendingToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"service.register", "remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), pendingToken, store.ServiceRegistration{ServiceID: "observability-pending", ServiceType: "observability", ServiceName: "Observability Pending", PublicURL: "https://observability-pending.example.com"}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-pending","action":"retry_gdrive_upload","incident_id":"inc-pending","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+pendingToken.RawToken)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "service_token_not_registered") {
		t.Fatalf("expected pending observability token to be rejected, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestServiceRemediationExecuteRequiresAssignedEncoder(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "worker")
	obsToken, err := auth.CreateServiceToken(t.Context(), "observability", []string{"remediation.execute"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, obsToken, store.ServiceRegistration{ServiceID: "observability-01", ServiceType: "observability", ServiceName: "Observability", PublicURL: "https://observability.example.com"})
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	req := httptest.NewRequest(http.MethodPost, "/services/remediation-actions/execute", bytes.NewBufferString(`{"action_id":"action-missing-assignment","action":"retry_gdrive_upload","incident_id":"inc-missing-assignment","stream_id":"`+stream.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+obsToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.retryCalls != 0 {
		t.Fatalf("dispatcher should not be called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	events := auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "remediation.execute" || events[len(events)-1].Result != "failure" {
		t.Fatalf("expected failed remediation audit event, got %#v", events)
	}
}

func TestNodeRegistrationScopesGrantEmailRelayOnlyToObservability(t *testing.T) {
	observabilityScopes := nodeRegistrationScopes("observability", false, false)
	if !stringSliceContains(observabilityScopes, "observability.ingest") || !stringSliceContains(observabilityScopes, "notifications.email.send") {
		t.Fatalf("observability scopes should include dedicated email relay access: %#v", observabilityScopes)
	}
	for _, serviceType := range []string{"worker", "encoder_recorder", "discord_bot"} {
		if scopes := nodeRegistrationScopes(serviceType, false, false); stringSliceContains(scopes, "notifications.email.send") {
			t.Fatalf("%s unexpectedly received email relay access: %#v", serviceType, scopes)
		}
	}
}

func TestCreateWorkerNodeRegistrationTokenIncludesObservabilityIngest(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY", "test-stream-ingest-signing-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "secrets.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/nodes/registration-tokens", bytes.NewBufferString(`{"node_type":"worker","node_id":"worker-01","name":"Worker 01","host":"worker.example.com","port":8443,"ssl_enabled":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("worker node status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(body.Scopes, "observability.ingest") || !stringSliceContains(body.Scopes, "worker.events.write") {
		t.Fatalf("worker node scopes missing observability ingest: %#v", body.Scopes)
	}
}

func TestServiceObservabilitySignalProxiesWithRegisteredNodeIdentity(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	var gotAuth string
	var gotPayload map[string]any
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notification-events" {
			writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
			return
		}
		if r.URL.Path != "/signals" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"signal":{"id":"sig-01"}}`))
	}))
	defer obs.Close()
	observabilityToken := registerObservabilityNodeForTest(t, auth, "admin-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-01", []string{"service.register", "service.heartbeat", "observability.ingest"})
	registerServiceForTest(t, handler, token.RawToken, "worker-01", "worker")

	req := httptest.NewRequest(http.MethodPost, "/services/observability/signals", bytes.NewBufferString(`{"type":"metric","name":"worker.event_send_failures_total","service_id":"attacker","service_type":"encoder_recorder","stream_id":"stream-01","value":1}`))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("proxy signal status = %d body = %s", res.Code, res.Body.String())
	}
	if gotAuth != "Bearer "+observabilityToken.RawToken {
		t.Fatalf("observability proxy used wrong auth: %q", gotAuth)
	}
	if gotPayload["service_id"] != "worker-01" || gotPayload["service_type"] != "worker" {
		t.Fatalf("service identity was not enforced by proxy: %#v", gotPayload)
	}
}

func TestServiceObservabilityCriticalSignalIsObservabilityOnly(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	observabilityCalls := 0
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notification-events" {
			writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
			return
		}
		if r.URL.Path != "/signals" {
			http.NotFound(w, r)
			return
		}
		observabilityCalls++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"signal":{"id":"sig-critical"}}`))
	}))
	defer obs.Close()
	registerObservabilityNodeForTest(t, auth, "admin-token", obs.URL)
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "critical Worker failure")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	workerToken := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-critical-01", []string{"service.register", "service.heartbeat", "observability.ingest"})
	registerServiceForTest(t, handler, workerToken.RawToken, "worker-critical-01", "worker")
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-critical-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "discord_bot")
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/services/observability/signals", bytes.NewBufferString(`{"type":"event","name":"worker.video.output_failed","stream_id":"`+stream.ID+`","status":"failed","attributes":{"secret":"must-not-enter-audit"}}`))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("critical signal status=%d body=%s", res.Code, res.Body.String())
	}
	stored, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || stored.Status != "live" {
		t.Fatalf("observability signal changed stream lifecycle: stream=%#v err=%v", stored, err)
	}
	if dispatcher.stopCalls != 0 {
		t.Fatalf("observability signal dispatched a lifecycle stop: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if observabilityCalls != 1 {
		t.Fatalf("critical signal was not proxied to Observability: calls=%d", observabilityCalls)
	}
	auditJSON := toJSONForTest(t, auth.AuditEvents())
	for _, expected := range []string{`"action":"observability.signals.ingest"`, `"signal_name":"worker.video.output_failed"`} {
		if !strings.Contains(auditJSON, expected) {
			t.Fatalf("critical lifecycle audit missing %q: %s", expected, auditJSON)
		}
	}
	if strings.Contains(auditJSON, "must-not-enter-audit") {
		t.Fatalf("critical signal attributes leaked into audit: %s", auditJSON)
	}
}

func TestServiceObservabilityCriticalSignalDoesNotAuthorizeLifecycleForUnassignedStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create"}); err != nil {
		t.Fatal(err)
	}
	observabilityCalls := 0
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notification-events" {
			writeJSON(w, http.StatusAccepted, []map[string]string{{"status": "success"}})
			return
		}
		observabilityCalls++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer obs.Close()
	registerObservabilityNodeForTest(t, auth, "admin-token", obs.URL)
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "unassigned critical target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	workerToken := createBoundServiceTokenForTest(t, handler, cookie, csrf, "worker", "worker-unassigned-01", []string{"service.register", "service.heartbeat", "observability.ingest"})
	registerServiceForTest(t, handler, workerToken.RawToken, "worker-unassigned-01", "worker")

	req := httptest.NewRequest(http.MethodPost, "/services/observability/signals", bytes.NewBufferString(`{"type":"event","name":"worker.video.output_failed","stream_id":"`+stream.ID+`","status":"failed","attributes":{"secret":"unassigned-secret"}}`))
	req.Header.Set("Authorization", "Bearer "+workerToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("unassigned critical signal status=%d body=%s", res.Code, res.Body.String())
	}
	stored, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || stored.Status != "live" || dispatcher.stopCalls != 0 || observabilityCalls != 1 {
		t.Fatalf("unassigned critical signal changed lifecycle: stream=%#v dispatcher=%s obs_calls=%d err=%v", stored, formatSafeHTTPSensitiveDiagnostic(dispatcher), observabilityCalls, err)
	}
	if auditJSON := toJSONForTest(t, auth.AuditEvents()); strings.Contains(auditJSON, "unassigned-secret") {
		t.Fatalf("unassigned critical signal attributes leaked into audit: %s", auditJSON)
	}
}

func TestHeartbeatMismatchEligibleGraceBoundary(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		stream store.Stream
		want   bool
	}{
		{name: "before grace", stream: store.Stream{Status: "live", UpdatedAt: now.Add(-criticalHeartbeatMismatchGrace + time.Nanosecond)}},
		{name: "at grace", stream: store.Stream{Status: "live", UpdatedAt: now.Add(-criticalHeartbeatMismatchGrace)}, want: true},
		{name: "after grace", stream: store.Stream{Status: "live", UpdatedAt: now.Add(-criticalHeartbeatMismatchGrace - time.Second)}, want: true},
		{name: "starting is never eligible", stream: store.Stream{Status: "starting", UpdatedAt: now.Add(-2 * criticalHeartbeatMismatchGrace)}},
		{name: "future update", stream: store.Stream{Status: "live", UpdatedAt: now.Add(time.Second)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := heartbeatMismatchEligible(test.stream, now); got != test.want {
				t.Fatalf("heartbeat mismatch eligible=%t want=%t stream=%#v", got, test.want, test.stream)
			}
		})
	}
}

func TestPreferredObservabilityServiceUsesHealthHeartbeatAndNameOrder(t *testing.T) {
	now := time.Date(2026, time.July, 18, 2, 0, 0, 0, time.UTC)
	offlineHeartbeat := now.Add(-4 * time.Minute)
	healthyOlderHeartbeat := now.Add(-30 * time.Second)
	healthyNewerHeartbeat := now.Add(-10 * time.Second)

	tests := []struct {
		name     string
		services []store.RegisteredService
		wantID   string
	}{
		{
			name: "healthy beats alphabetically first offline node",
			services: []store.RegisteredService{
				{ServiceID: "obs-offline", ServiceType: "observability", ServiceName: "A Offline", PublicURL: "https://offline.example.com", Status: "online", LastHeartbeatAt: &offlineHeartbeat},
				{ServiceID: "obs-healthy", ServiceType: "observability", ServiceName: "Z Healthy", PublicURL: "https://healthy.example.com", Status: "online", LastHeartbeatAt: &healthyOlderHeartbeat},
			},
			wantID: "obs-healthy",
		},
		{
			name: "newest heartbeat wins within the same health rank",
			services: []store.RegisteredService{
				{ServiceID: "obs-older", ServiceType: "observability", ServiceName: "A Older", PublicURL: "https://older.example.com", Status: "online", LastHeartbeatAt: &healthyOlderHeartbeat},
				{ServiceID: "obs-newer", ServiceType: "observability", ServiceName: "Z Newer", PublicURL: "https://newer.example.com", Status: "online", LastHeartbeatAt: &healthyNewerHeartbeat},
			},
			wantID: "obs-newer",
		},
		{
			name: "name order breaks an equal heartbeat tie",
			services: []store.RegisteredService{
				{ServiceID: "obs-zulu", ServiceType: "observability", ServiceName: "Zulu", PublicURL: "https://zulu.example.com", Status: "online", LastHeartbeatAt: &healthyNewerHeartbeat},
				{ServiceID: "obs-alpha", ServiceType: "observability", ServiceName: "Alpha", PublicURL: "https://alpha.example.com", Status: "online", LastHeartbeatAt: &healthyNewerHeartbeat},
			},
			wantID: "obs-alpha",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := preferredObservabilityService(tc.services, now)
			if !ok || got.ServiceID != tc.wantID {
				t.Fatalf("preferred observability service = %#v, ok=%v, want %q", got, ok, tc.wantID)
			}
		})
	}
}

func TestObservabilityProxyEndpoints(t *testing.T) {
	var capturedMu sync.Mutex
	var gotAuth string
	var gotMetricsRange string
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/metrics" {
			gotMetricsRange = r.URL.Query().Get("range_sec")
		}
		capturedMu.Unlock()
		switch r.URL.Path {
		case "/incidents":
			_, _ = w.Write([]byte(`[{"id":"inc-1","severity":"critical","google_drive_folder_id":"drive-folder-secret-id","target":"https://example.com/callback?api_key=upstream-secret"}]`))
		case "/diagnostics":
			_, _ = w.Write([]byte(`[{"incident_id":"inc-1","rule":"encoder_process_exited","diagnostic_report":{"summary":"Encoder stopped"}}]`))
		case "/metrics":
			_, _ = w.Write([]byte(`[{"name":"encoder.output_fps","service_id":"enc-1","value":60},{"name":"discord.audio_receiving","service_id":"enc-1","value":1},{"name":"encoder.audio_silence_sec","service_id":"enc-1","value":0},{"name":"encoder.audio_clipping_total","service_id":"enc-1","value":0}]`))
		case "/remediation-actions":
			_, _ = w.Write([]byte(`[{"id":"rem-1","status":"suggested"}]`))
		case "/notification-deliveries":
			_, _ = w.Write([]byte(`[{"id":"ntf-1","event_type":"admin.audit","status":"success","target":"https://discord.com/api/webhooks/id/upstream-secret-token","message":"bearer upstream-secret-token","metadata":{"rule":"secrets.update","summary":"シークレットを更新\n実行者: ops"},"created_at":"2026-07-18T01:32:00Z"}]`))
		case "/notification-channels":
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"chn-1","name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/upstream-slack-token","masked_webhook_url":"https://hooks.slack.com/<WEBHOOK_PATH>","smtp_password":"raw-smtp-password","uses_global_smtp":true,"masked_email_target":"o***s@example.com","smtp_server":"smtp-bypass.example.com","recipient_list":["bypass@example.com"]}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"chn-1","name":"discord","webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token","masked_webhook_url":"https://example.com/<WEBHOOK_PATH>"},{"id":"slack-1","name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/upstream-slack-token","masked_webhook_url":"https://hooks.slack.com/<WEBHOOK_PATH>"},{"id":"email-1","name":"email","type":"email","email_recipients":["ops@example.com"],"smtp_host":"smtp.example.com","smtp_port":587,"smtp_tls":true,"smtp_from":"autostream@example.com","smtp_username":"autostream","smtp_password":"raw-smtp-password","uses_global_smtp":true,"masked_email_target":"o***s@example.com","smtp_server":"smtp-bypass.example.com","recipient_list":["bypass@example.com"]}]`))
		case "/notification-channels/chn-1":
			if r.Method == http.MethodDelete {
				_, _ = w.Write([]byte(`{"status":"deleted"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"chn-1","name":"discord","webhook_url":"https://discord.com/api/webhooks/id/upstream-secret-token","masked_webhook_url":"https://example.com/<WEBHOOK_PATH>","recipient_list":["bypass@example.com"]}`))
		case "/notification-channels/chn-1/test":
			_, _ = w.Write([]byte(`[{"status":"success","target":"https://example.com/<WEBHOOK_PATH>"}]`))
		case "/notification-events":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`[{"event_type":"admin.audit","status":"success","channel":"generic","target":"https://example.com/<WEBHOOK_PATH>"}]`))
		case "/incidents/inc-1/acknowledge":
			_, _ = w.Write([]byte(`{"id":"inc-1","status":"acknowledged"}`))
		case "/incidents/inc-1/resolve":
			_, _ = w.Write([]byte(`{"id":"inc-1","status":"resolved"}`))
		case "/incidents/inc-1/diagnostics/rerun":
			_, _ = w.Write([]byte(`{"incident":{"id":"inc-1","status":"acknowledged"},"outcome":"evaluated"}`))
		case "/remediation-actions/rem-1/approve":
			_, _ = w.Write([]byte(`{"id":"rem-1","status":"approved"}`))
		default:
			t.Fatalf("unexpected observability path: %s", r.URL.Path)
		}
	}))
	defer obs.Close()

	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"incidents.read", "incidents.acknowledge", "incidents.resolve", "diagnostics.read", "diagnostics.run", "metrics.read", "remediation.read", "remediation.approve", "notification_channels.read", "notification_channels.create", "notification_channels.update", "notification_channels.delete", "notification_channels.test", "audit_logs.read"}); err != nil {
		t.Fatal(err)
	}
	observabilityToken := registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	if _, err := auth.Heartbeat(t.Context(), observabilityToken, store.ServiceHeartbeat{ServiceID: "observability-01", Status: "online", Metrics: map[string]any{"observability.uptime_seconds": 42}}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	})
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Metrics: map[string]any{"worker.active_jobs": 2, "worker.last_error": "do not persist"}}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	for _, path := range []string{"/observability/incidents", "/observability/diagnostics", "/observability/metrics", "/observability/remediation-actions", "/observability/notification-deliveries", "/observability/notification-channels"} {
		requestPath := path
		if path == "/observability/metrics" {
			requestPath += "?range_sec=900"
		}
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, res.Code, res.Body.String())
		}
		if path == "/observability/metrics" && (!strings.Contains(res.Body.String(), "discord.audio_receiving") || !strings.Contains(res.Body.String(), "encoder.audio_clipping_total")) {
			t.Fatalf("audio metrics were not proxied: %s", res.Body.String())
		}
		if path == "/observability/metrics" && (!strings.Contains(res.Body.String(), "observability.uptime_seconds") || !strings.Contains(res.Body.String(), "worker.active_jobs") || strings.Contains(res.Body.String(), "do not persist")) {
			t.Fatalf("node heartbeat metrics were not safely merged: %s", res.Body.String())
		}
		if strings.Contains(res.Body.String(), "upstream-secret-token") || strings.Contains(res.Body.String(), "upstream-slack-token") || strings.Contains(res.Body.String(), "upstream-secret") || strings.Contains(res.Body.String(), "api_key=") || strings.Contains(res.Body.String(), "drive-folder-secret-id") || strings.Contains(res.Body.String(), `"webhook_url":"https://discord.com`) || strings.Contains(res.Body.String(), `"webhook_url":"https://hooks.slack.com`) || strings.Contains(res.Body.String(), "hooks.slack.com/services") || strings.Contains(res.Body.String(), "raw-smtp-password") || strings.Contains(res.Body.String(), "ops@example.com") || strings.Contains(res.Body.String(), "smtp.example.com") || strings.Contains(res.Body.String(), "autostream@example.com") || strings.Contains(res.Body.String(), "smtp-bypass.example.com") || strings.Contains(res.Body.String(), "bypass@example.com") || strings.Contains(res.Body.String(), `"smtp_host"`) || strings.Contains(res.Body.String(), `"email_recipients"`) || strings.Contains(res.Body.String(), `"smtp_from"`) || strings.Contains(res.Body.String(), `"smtp_username"`) || strings.Contains(res.Body.String(), `"smtp_server"`) || strings.Contains(res.Body.String(), `"recipient_list"`) {
			t.Fatalf("observability proxy leaked upstream notification secret: %s", res.Body.String())
		}
		if path == "/observability/notification-channels" && (!strings.Contains(res.Body.String(), `"uses_global_smtp":true`) || !strings.Contains(res.Body.String(), `"masked_email_target":"o***s@example.com"`)) {
			t.Fatalf("email notification public status was not preserved: %s", res.Body.String())
		}
		if path == "/observability/notification-channels" && !strings.Contains(res.Body.String(), `"masked_webhook_url":"https://hooks.slack.com/\u003cWEBHOOK_PATH\u003e"`) {
			t.Fatalf("slack notification public masked URL was not preserved: %s", res.Body.String())
		}
		if path == "/observability/notification-deliveries" && (!strings.Contains(res.Body.String(), `"rule":"secrets.update"`) || !strings.Contains(res.Body.String(), `"summary":"シークレットを更新\n実行者: ops"`) || !strings.Contains(res.Body.String(), `"created_at":"2026-07-18T01:32:00Z"`)) {
			t.Fatalf("notification delivery operation context was not preserved: %s", res.Body.String())
		}
	}
	capturedMu.Lock()
	gotMetricsRangeSnapshot := gotMetricsRange
	capturedMu.Unlock()
	if gotMetricsRangeSnapshot != "900" {
		t.Fatalf("metrics range was not forwarded to observability: %q", gotMetricsRangeSnapshot)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/observability/notification-channels", bytes.NewBufferString(`{"name":"slack","type":"slack","webhook_url":"https://hooks.slack.com/services/T000/B000/slack-secret-token","enabled":true}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated || strings.Contains(createRes.Body.String(), "slack-secret-token") || strings.Contains(createRes.Body.String(), "upstream-slack-token") || strings.Contains(createRes.Body.String(), `"webhook_url":"https://hooks.slack.com`) || strings.Contains(createRes.Body.String(), "hooks.slack.com/services") || strings.Contains(createRes.Body.String(), "raw-smtp-password") {
		t.Fatalf("create channel status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), `"type":"slack"`) || !strings.Contains(createRes.Body.String(), `"masked_webhook_url":"https://hooks.slack.com/\u003cWEBHOOK_PATH\u003e"`) {
		t.Fatalf("create channel response lost public slack status: %s", createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), `"uses_global_smtp":true`) || !strings.Contains(createRes.Body.String(), `"masked_email_target":"o***s@example.com"`) {
		t.Fatalf("create channel response lost public email status: %s", createRes.Body.String())
	}
	events := auth.AuditEvents()
	var createAudit *store.AuditEvent
	for i := range events {
		if events[i].Action == "notification_channels.create" {
			createAudit = &events[i]
			break
		}
	}
	if createAudit == nil {
		t.Fatalf("expected notification channel create audit event, got %#v", events)
	}
	metadata, _ := json.Marshal(createAudit.Metadata)
	if strings.Contains(string(metadata), "slack-secret-token") || strings.Contains(string(metadata), "upstream-slack-token") || strings.Contains(string(metadata), "hooks.slack.com/services") {
		t.Fatalf("raw webhook leaked in audit metadata: %s", string(metadata))
	}
	if createAudit.Metadata["has_webhook_url"] != true {
		t.Fatalf("expected has_webhook_url audit metadata, got %#v", createAudit.Metadata)
	}
	auditReq := httptest.NewRequest(http.MethodGet, "/audit-logs?action_group=notifications", nil)
	auditReq.AddCookie(cookie)
	auditRes := httptest.NewRecorder()
	handler.ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK || !strings.Contains(auditRes.Body.String(), "notification_channels.create") || strings.Contains(auditRes.Body.String(), "slack-secret-token") || strings.Contains(auditRes.Body.String(), "hooks.slack.com/services") {
		t.Fatalf("notification audit status = %d body = %s", auditRes.Code, auditRes.Body.String())
	}
	testReq := httptest.NewRequest(http.MethodPost, "/observability/notification-channels/chn-1/test", nil)
	testReq.AddCookie(cookie)
	testReq.Header.Set("X-CSRF-Token", csrf)
	testRes := httptest.NewRecorder()
	handler.ServeHTTP(testRes, testReq)
	if testRes.Code != http.StatusAccepted || !strings.Contains(testRes.Body.String(), "success") {
		t.Fatalf("test channel status = %d body = %s", testRes.Code, testRes.Body.String())
	}
	ackReq := httptest.NewRequest(http.MethodPost, "/observability/incidents/inc-1/acknowledge", nil)
	ackReq.AddCookie(cookie)
	ackReq.Header.Set("X-CSRF-Token", csrf)
	ackRes := httptest.NewRecorder()
	handler.ServeHTTP(ackRes, ackReq)
	if ackRes.Code != http.StatusOK || !strings.Contains(ackRes.Body.String(), "acknowledged") {
		t.Fatalf("ack incident status = %d body = %s", ackRes.Code, ackRes.Body.String())
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/observability/remediation-actions/rem-1/approve", nil)
	approveReq.AddCookie(cookie)
	approveReq.Header.Set("X-CSRF-Token", csrf)
	approveRes := httptest.NewRecorder()
	handler.ServeHTTP(approveRes, approveReq)
	if approveRes.Code != http.StatusOK || !strings.Contains(approveRes.Body.String(), "approved") {
		t.Fatalf("approve status = %d body = %s", approveRes.Code, approveRes.Body.String())
	}
	rerunReq := httptest.NewRequest(http.MethodPost, "/observability/incidents/inc-1/diagnostics/rerun", nil)
	rerunReq.AddCookie(cookie)
	rerunReq.Header.Set("X-CSRF-Token", csrf)
	rerunRes := httptest.NewRecorder()
	handler.ServeHTTP(rerunRes, rerunReq)
	if rerunRes.Code != http.StatusOK || !strings.Contains(rerunRes.Body.String(), `"outcome":"evaluated"`) {
		t.Fatalf("diagnostic rerun status = %d body = %s", rerunRes.Code, rerunRes.Body.String())
	}
	capturedMu.Lock()
	gotAuthSnapshot := gotAuth
	capturedMu.Unlock()
	if gotAuthSnapshot != "Bearer "+observabilityToken.RawToken {
		t.Fatal("unexpected upstream authorization header")
	}
	events = auth.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "diagnostics.run" || events[len(events)-1].ResourceType != "incident" || events[len(events)-1].ResourceID != "inc-1" {
		t.Fatalf("expected diagnostic rerun audit event, got %#v", events)
	}
}

func TestObservabilityProxyDoesNotLeakTokenOnUpstreamError(t *testing.T) {
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-token", http.StatusForbidden)
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"incidents.read"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/incidents", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") {
		t.Fatalf("token leaked in response: %s", res.Body.String())
	}
}

func TestObservabilityIncidentProxyForwardsOnlySupportedHistoryQuery(t *testing.T) {
	upstreamQueries := make(chan url.Values, 1)
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/incidents" {
			upstreamQueries <- r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"incidents.read"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "query-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	before := "2026-08-21T12:00:00.123456789Z"
	req := httptest.NewRequest(http.MethodGet, "/observability/incidents?limit=25&before="+url.QueryEscape(before)+"&before_id=inc-25&status=resolved&token=must-not-forward", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	var upstreamQuery url.Values
	select {
	case upstreamQuery = <-upstreamQueries:
	default:
		t.Fatal("incidents request did not reach observability")
	}
	if upstreamQuery.Get("limit") != "25" || upstreamQuery.Get("before") != before || upstreamQuery.Get("before_id") != "inc-25" || upstreamQuery.Get("status") != "resolved" {
		t.Fatalf("history query was not forwarded: %#v", upstreamQuery)
	}
	if upstreamQuery.Has("token") {
		t.Fatalf("unsupported query parameter reached observability: %#v", upstreamQuery)
	}
}

func TestObservabilityDiagnosticRerunAuditsSafeUpstreamFailure(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
		wantStatus     int
		wantCode       string
	}{
		{
			name:           "upstream unavailable",
			upstreamStatus: http.StatusServiceUnavailable,
			upstreamBody:   `{"code":"private_upstream_failure","detail":"private-token"}`,
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "observability_unavailable",
		},
		{
			name:           "upstream rate limited",
			upstreamStatus: http.StatusTooManyRequests,
			upstreamBody:   `{"code":"private_upstream_failure","detail":"private-token"}`,
			wantStatus:     http.StatusTooManyRequests,
			wantCode:       "observability_rate_limited",
		},
		{
			name:           "upstream gateway failure",
			upstreamStatus: http.StatusBadGateway,
			upstreamBody:   `{"code":"private_upstream_failure","detail":"private-token"}`,
			wantStatus:     http.StatusBadGateway,
			wantCode:       "observability_request_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/notification-events" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/incidents/inc-1/diagnostics/rerun" {
					t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.upstreamBody))
			}))
			defer obs.Close()

			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"diagnostics.run"}); err != nil {
				t.Fatal(err)
			}
			registerObservabilityNodeForTest(t, auth, "unused", obs.URL)
			handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
			cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

			req := httptest.NewRequest(http.MethodPost, "/observability/incidents/inc-1/diagnostics/rerun", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", res.Code, res.Body.String(), tt.wantStatus)
			}
			if !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) || strings.Contains(res.Body.String(), "private-token") {
				t.Fatalf("unsafe response body = %s", res.Body.String())
			}

			events := auth.AuditEvents()
			var failureAudit *store.AuditEvent
			for i := range events {
				event := events[i]
				if event.Action == "diagnostics.run" && event.ResourceType == "incident" && event.ResourceID == "inc-1" && event.Result == "failure" {
					failureAudit = &event
					break
				}
			}
			if failureAudit == nil {
				t.Fatalf("diagnostic rerun failure was not audited: %#v", events)
			}
			if failureAudit.ActorUsername != "admin" || failureAudit.Metadata["code"] != tt.wantCode || failureAudit.Metadata["reason"] != tt.wantCode || failureAudit.Metadata["status"] != tt.wantStatus {
				t.Fatalf("unsafe or incomplete failure audit: %#v", failureAudit)
			}
			if auditJSON := toJSONForTest(t, failureAudit); strings.Contains(auditJSON, "private-token") || strings.Contains(auditJSON, "private_upstream_failure") {
				t.Fatalf("upstream body leaked into audit: %s", auditJSON)
			}
		})
	}
}

func TestObservabilityNotificationProxyPreservesSafeUpstreamErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid webhook", status: http.StatusBadRequest, body: `{"code":"invalid_webhook_url","detail":"https://hooks.example/private-token"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_webhook_url"},
		{name: "missing encryption key", status: http.StatusServiceUnavailable, body: `{"code":"secret_encryption_key_required","detail":"private-key-material"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "secret_encryption_key_required"},
		{name: "runtime token mismatch", status: http.StatusUnauthorized, body: `{"code":"invalid_service_token","detail":"Bearer private-token"}`, wantStatus: http.StatusBadGateway, wantCode: "observability_auth_failed"},
		{name: "untrusted error code", status: http.StatusBadRequest, body: `{"code":"private_token","detail":"private-token"}`, wantStatus: http.StatusBadRequest, wantCode: "observability_request_rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer obs.Close()

			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"notification_channels.create"}); err != nil {
				t.Fatal(err)
			}
			registerObservabilityNodeForTest(t, auth, "node-runtime-token", obs.URL)
			handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
			cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
			req := httptest.NewRequest(http.MethodPost, "/observability/notification-channels", bytes.NewBufferString(`{"name":"ops","type":"slack","webhook_url":"https://hooks.slack.com/services/T/B/private-token","enabled":true}`))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.wantStatus || !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			for _, secret := range []string{"private-token", "private-key-material", "hooks.slack.com/services"} {
				if strings.Contains(res.Body.String(), secret) {
					t.Fatalf("upstream or request secret leaked: %s", res.Body.String())
				}
			}
			events := auth.AuditEvents()
			if len(events) == 0 || events[len(events)-1].Result != "failure" || events[len(events)-1].Metadata["reason"] != tt.wantCode {
				t.Fatalf("missing safe failure audit: %#v", events)
			}
			metadata, _ := json.Marshal(events[len(events)-1].Metadata)
			if strings.Contains(string(metadata), "private-token") || strings.Contains(string(metadata), "hooks.slack.com/services") {
				t.Fatalf("notification secret leaked in audit: %s", metadata)
			}
		})
	}
}

func TestObservabilityMetricsFallsBackToNodeHeartbeatMetrics(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"metrics.read"}); err != nil {
		t.Fatal(err)
	}
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	})
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-01", Status: "online", Metrics: map[string]any{"worker.active_jobs": 3}}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/observability/metrics", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("metrics fallback status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"name":"worker.active_jobs"`) || !strings.Contains(res.Body.String(), `"service_id":"worker-01"`) {
		t.Fatalf("node heartbeat metrics were not returned without observability upstream: %s", res.Body.String())
	}
}

func TestObservabilityProxyRejectsEncodedSlashID(t *testing.T) {
	upstreamCalled := false
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		t.Fatalf("upstream should not be called for invalid path id: %s", r.URL.Path)
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"notification_channels.read"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "secret-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/notification-channels/..%2Fmetrics", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if upstreamCalled {
		t.Fatalf("invalid observability id reached upstream")
	}
}

func TestObservabilityProxyRequiresPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/observability/incidents", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}
