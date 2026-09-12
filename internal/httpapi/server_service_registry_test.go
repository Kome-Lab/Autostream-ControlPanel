package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamStartRequiresRequiredServiceAssignments(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "encoder_recorder") || hasString(body.MissingServiceTypes, "worker") {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("dispatcher should not be called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "created" {
		t.Fatalf("stream status changed on missing assignment: %#v", unchanged)
	}
}

func TestStreamStopRequiresRequiredServiceAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code                string   `json:"code"`
		MissingServiceTypes []string `json:"missing_service_types"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "missing_stream_assignments" || !hasString(body.MissingServiceTypes, "discord_bot") || !hasString(body.MissingServiceTypes, "worker") || hasString(body.MissingServiceTypes, "encoder_recorder") {
		t.Fatalf("unexpected missing assignment response: %#v", body)
	}
	if dispatcher.stopCalls != 0 {
		t.Fatalf("dispatcher should not be called: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	unchanged, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "live" {
		t.Fatalf("stream status changed on missing stop assignment: %#v", unchanged)
	}
}

func TestAssignServiceEndpointAllowsNonWorkerServices(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/enc-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceType != "encoder_recorder" {
		t.Fatalf("unexpected assignments: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
}

func TestGenericServiceAssignRequiresServicePermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "restricted stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/services/enc-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("generic service assign with workers.assign should be forbidden: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAssignServiceEndpointReplacesSameTypeAndMovesService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	streamA, err := streams.CreateStream(t.Context(), "stream a")
	if err != nil {
		t.Fatal(err)
	}
	streamB, err := streams.CreateStream(t.Context(), "stream b")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	registerServiceInstance(t, auth, "enc-02", "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	assignServiceForTest(t, handler, cookie, csrf, "enc-01", streamA.ID)
	assignServiceForTest(t, handler, cookie, csrf, "enc-01", streamB.ID)
	assignmentsA, err := auth.ListStreamAssignments(t.Context(), streamA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignmentsA) != 0 {
		t.Fatalf("moved service should be removed from previous stream: %s", formatSafeHTTPSensitiveDiagnostic(assignmentsA))
	}
	enc01, err := auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if enc01.CurrentStreamID != streamB.ID || enc01.Status != "assigned" {
		t.Fatalf("moved service has wrong state: %s", formatSafeHTTPSensitiveDiagnostic(enc01))
	}

	assignServiceForTest(t, handler, cookie, csrf, "enc-02", streamB.ID)
	assignmentsB, err := auth.ListStreamAssignments(t.Context(), streamB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignmentsB) != 1 || assignmentsB[0].ServiceID != "enc-02" {
		t.Fatalf("same type replacement did not keep a single assignment: %s", formatSafeHTTPSensitiveDiagnostic(assignmentsB))
	}
	enc01, err = auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if enc01.CurrentStreamID != "" || enc01.Status == "assigned" {
		t.Fatalf("replaced service should be cleared: %s", formatSafeHTTPSensitiveDiagnostic(enc01))
	}
}

func TestServiceAssignmentRoleAllowsStandbyWithoutDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.assign", "streams.start", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "standby stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "enc-primary", "encoder_recorder")
	registerServiceInstance(t, auth, "enc-standby", "encoder_recorder")
	dispatcher := &fakeServiceDispatcher{}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "standby discord", "discord-01", "guild-standby", "voice-standby", "")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	assignServiceForTest(t, handler, cookie, csrf, "discord-01", stream.ID)
	assignServiceForTest(t, handler, cookie, csrf, "worker-01", stream.ID)
	assignServiceForTest(t, handler, cookie, csrf, "enc-primary", stream.ID)
	assignServiceWithRoleForTest(t, handler, cookie, csrf, "enc-standby", stream.ID, "standby")

	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 4 {
		t.Fatalf("expected primary services plus standby encoder, got %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	var primaryEncoder, standbyEncoder bool
	for _, assignment := range assignments {
		if assignment.ServiceID == "enc-primary" && assignment.AssignmentRole == "primary" {
			primaryEncoder = true
		}
		if assignment.ServiceID == "enc-standby" && assignment.AssignmentRole == "standby" {
			standbyEncoder = true
		}
	}
	if !primaryEncoder || !standbyEncoder {
		t.Fatalf("missing expected encoder assignment roles: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("service health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	var health []store.RegisteredService
	if err := json.NewDecoder(healthRes.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, service := range health {
		roles[service.ServiceID] = service.AssignmentRole
	}
	if roles["discord-01"] != "primary" || roles["enc-primary"] != "primary" || roles["enc-standby"] != "standby" {
		t.Fatalf("service health did not expose assignment roles: %#v", roles)
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.startCalls != 1 {
		t.Fatalf("expected start dispatch, got %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	for _, service := range dispatcher.startedServices {
		if service.ServiceID == "enc-standby" {
			t.Fatalf("standby encoder must not receive start dispatch: %#v", dispatcher.startedServices)
		}
		if service.AssignmentRole != "primary" {
			t.Fatalf("dispatch should only include primary assignments: %#v", dispatcher.startedServices)
		}
	}
}

func TestUnassignServiceEndpointClearsAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "enc-01", "encoder_recorder")
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/services/enc-01/assignment", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unassign status = %d body = %s", res.Code, res.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected no assignments after unassign: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	service, err := auth.GetService(t.Context(), "enc-01")
	if err != nil {
		t.Fatal(err)
	}
	if service.CurrentStreamID != "" || service.Status == "assigned" {
		t.Fatalf("service was not cleared: %s", formatSafeHTTPSensitiveDiagnostic(service))
	}
	var auditEvents []store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.unassign" {
			auditEvents = append(auditEvents, event)
		}
	}
	if len(auditEvents) != 1 || auditEvents[0].Metadata["previous_stream_id"] != stream.ID {
		t.Fatalf("unassign audit not recorded correctly: %#v", auditEvents)
	}
}

func TestUnassignWorkerRequiresWorkerUnassignPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "assigner"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "unassigner"}, "correct horse battery", []string{"workers.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "worker stream")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "worker-01", "worker")
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))

	assignerCookie, assignerCSRF := loginForTest(t, handler, "assigner", "correct horse battery")
	forbiddenReq := httptest.NewRequest(http.MethodDelete, "/workers/worker-01/assignment", nil)
	forbiddenReq.AddCookie(assignerCookie)
	forbiddenReq.Header.Set("X-CSRF-Token", assignerCSRF)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("workers.assign-only unassign status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].ServiceID != "worker-01" {
		t.Fatalf("assignment changed after forbidden unassign: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}

	unassignerCookie, unassignerCSRF := loginForTest(t, handler, "unassigner", "correct horse battery")
	allowedReq := httptest.NewRequest(http.MethodDelete, "/workers/worker-01/assignment", nil)
	allowedReq.AddCookie(unassignerCookie)
	allowedReq.Header.Set("X-CSRF-Token", unassignerCSRF)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("workers.unassign status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	assignments, err = auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected no assignments after allowed worker unassign: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
}

func TestAssignWorkerEndpointRejectsMissingStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/workers/worker-01/assign", bytes.NewBufferString(`{"stream_id":"missing-stream"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("assign worker to missing stream status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "stream_not_found" {
		t.Fatalf("unexpected response code: %#v", body)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), "missing-stream")
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("missing stream should not receive assignments: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	worker, err := auth.GetService(t.Context(), "worker-01")
	if err != nil {
		t.Fatal(err)
	}
	if worker.CurrentStreamID != "" || worker.Status == "assigned" {
		t.Fatalf("worker state changed after missing stream assignment: %s", formatSafeHTTPSensitiveDiagnostic(worker))
	}
}

func TestDeleteServiceEndpointRemovesRegistryAssignmentAndRevokesToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"services.disable", "service_health.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "dry-run stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "enc-01", ServiceType: "encoder_recorder", ServiceName: "Encoder", PublicURL: "https://encoder.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "enc-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/services/enc-01", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete service status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := auth.GetService(t.Context(), "enc-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("service should be removed, err = %v", err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("assignment should be removed: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"enc-01","status":"online"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusUnauthorized {
		t.Fatalf("deleted service token should be revoked, heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}
	healthReq := httptest.NewRequest(http.MethodGet, "/service-health", nil)
	healthReq.AddCookie(cookie)
	healthRes := httptest.NewRecorder()
	handler.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRes.Code, healthRes.Body.String())
	}
	if strings.Contains(healthRes.Body.String(), "enc-01") || strings.Contains(healthRes.Body.String(), token.RawToken) {
		t.Fatalf("deleted service or raw token leaked in health response: %s", healthRes.Body.String())
	}
	var auditEvents []store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.delete" {
			auditEvents = append(auditEvents, event)
		}
	}
	if len(auditEvents) != 1 || auditEvents[0].ResourceID != "enc-01" || auditEvents[0].Metadata["service_type"] != "encoder_recorder" {
		t.Fatalf("delete audit not recorded correctly: %#v", auditEvents)
	}
}

func TestWorkerAssignmentAndStreamEventAuthorization(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "workers.read", "workers.assign", "workers.restart"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "worker event stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	token := createServiceTokenForTest(t, handler, cookie, csrf, "worker", []string{"service.register", "service.heartbeat", "service.status.write", "worker.events.write"})
	registerServiceForTest(t, handler, token.RawToken, "worker-01", "worker")

	listReq := httptest.NewRequest(http.MethodGet, "/workers", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list workers status = %d body = %s", listRes.Code, listRes.Body.String())
	}

	unassignedEventReq := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(`{"service_id":"worker-01","stream_id":"`+stream.ID+`","event_type":"worker.overlay","payload":{"ok":true}}`))
	unassignedEventReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedEventRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedEventRes, unassignedEventReq)
	if unassignedEventRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned event status = %d body = %s", unassignedEventRes.Code, unassignedEventRes.Body.String())
	}

	unassignedHeartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","current_stream_id":"`+stream.ID+`"}`))
	unassignedHeartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	unassignedHeartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignedHeartbeatRes, unassignedHeartbeatReq)
	if unassignedHeartbeatRes.Code != http.StatusForbidden {
		t.Fatalf("unassigned heartbeat status = %d body = %s", unassignedHeartbeatRes.Code, unassignedHeartbeatRes.Body.String())
	}
	var heartbeatFailureAudit *store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "services.heartbeat" && event.Result == "failure" && event.ResourceID == "worker-01" {
			heartbeatFailureAudit = &event
		}
	}
	if heartbeatFailureAudit == nil || heartbeatFailureAudit.ActorUsername != "worker" || heartbeatFailureAudit.Metadata["reason"] != "service_not_assigned_to_token" {
		t.Fatalf("heartbeat failure audit missing: %#v", auth.AuditEvents())
	}

	assignReq := httptest.NewRequest(http.MethodPost, "/workers/worker-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("assign worker status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewBufferString(`{"service_id":"worker-01","status":"online","current_stream_id":"`+stream.ID+`"}`))
	heartbeatReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	heartbeatRes := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusAccepted {
		t.Fatalf("assigned heartbeat status = %d body = %s", heartbeatRes.Code, heartbeatRes.Body.String())
	}

	eventReq := httptest.NewRequest(http.MethodPost, "/services/stream-events", bytes.NewBufferString(`{"service_id":"worker-01","stream_id":"`+stream.ID+`","event_type":"worker.overlay","payload":{"ok":true}}`))
	eventReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	eventRes := httptest.NewRecorder()
	handler.ServeHTTP(eventRes, eventReq)
	if eventRes.Code != http.StatusAccepted {
		t.Fatalf("assigned event status = %d body = %s", eventRes.Code, eventRes.Body.String())
	}

	restartReq := httptest.NewRequest(http.MethodPost, "/workers/worker-01/restart", nil)
	restartReq.AddCookie(cookie)
	restartReq.Header.Set("X-CSRF-Token", csrf)
	restartRes := httptest.NewRecorder()
	handler.ServeHTTP(restartRes, restartReq)
	if restartRes.Code != http.StatusAccepted {
		t.Fatalf("restart worker status = %d body = %s", restartRes.Code, restartRes.Body.String())
	}
}

func TestWorkerAssignRejectsNonWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"api_tokens.create", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "non-worker assignment stream")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	token := createServiceTokenForTest(t, handler, cookie, csrf, "discord_bot", []string{"service.register"})
	registerServiceForTest(t, handler, token.RawToken, "discord-01", "discord_bot")

	assignReq := httptest.NewRequest(http.MethodPost, "/workers/discord-01/assign", bytes.NewBufferString(`{"stream_id":"`+stream.ID+`"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusBadRequest {
		t.Fatalf("assign non-worker status = %d body = %s", assignRes.Code, assignRes.Body.String())
	}
}
