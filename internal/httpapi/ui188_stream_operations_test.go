package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each positive create owns a fresh pair; existing runtime owners remain intact.
func ui188CreatePayload(t *testing.T, handler http.Handler, payload string) string {
	t.Helper()
	s, ok := handler.(*Server)
	if !ok {
		t.Fatal("UI188 fixture requires the real server")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["visual_settings"]; !ok {
		body["visual_settings"] = map[string]any{}
	}
	for field, kind := range map[string]string{"encoder_service_id": "encoder_recorder", "worker_service_id": "worker"} {
		if _, ok := body[field]; ok {
			continue
		}
		id := "ui188-" + kind + "-" + outputSecretRandom(t)[:12]
		token, err := s.services.CreateServiceToken(t.Context(), kind, []string{"service.register"})
		if err != nil {
			t.Fatal(err)
		}
		registration := store.ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: id, PublicURL: "https://" + id + ".example.com", Version: "ui188", Capabilities: map[string]any{"output_relay_mode": "direct"}}
		if _, err = s.services.PrecreateService(t.Context(), token, registration); err != nil {
			t.Fatal(err)
		}
		if _, err = s.services.RegisterService(t.Context(), token, registration); err != nil {
			t.Fatal(err)
		}
		body[field] = id
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUI188CreateMandatoryRolesPermissionAndAtomicNodeSelections(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	for user, permissions := range map[string][]string{"operator": {"streams.create", "services.assign", "workers.assign"}, "denied": {"streams.create"}} {
		if err := auth.AddUser(store.User{Username: user}, "correct horse battery", permissions); err != nil {
			t.Fatal(err)
		}
	}
	registerServiceInstance(t, auth, "ui188-encoder", "encoder_recorder")
	registerServiceInstance(t, auth, "ui188-worker", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	base := map[string]any{"name": "UI188", "visual_settings": map[string]any{}, "encoder_service_id": "ui188-encoder", "worker_service_id": "ui188-worker"}
	post := func(body map[string]any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return serveUserJSON(t, handler, http.MethodPost, "/streams", string(raw), cookie, csrf)
	}
	for _, field := range []string{"encoder_service_id", "worker_service_id"} {
		for _, value := range []any{nil, "", "   ", "missing-service", 123} {
			copy := map[string]any{}
			for k, v := range base {
				copy[k] = v
			}
			copy[field] = value
			res := post(copy, cookie, csrf)
			if res.Code < 400 {
				t.Fatalf("invalid %s persisted: %d", field, res.Code)
			}
			rows, _ := streams.ListStreams(t.Context())
			if len(rows) != 0 {
				t.Fatal("rejected create persisted partial stream")
			}
			for _, id := range []string{"ui188-encoder", "ui188-worker"} {
				service, _ := auth.GetService(t.Context(), id)
				if service.CurrentStreamID != "" {
					t.Fatal("rejected create reserved service")
				}
			}
		}
	}
	wrong := map[string]any{}
	for k, v := range base {
		wrong[k] = v
	}
	wrong["encoder_service_id"] = "ui188-worker"
	if res := post(wrong, cookie, csrf); res.Code < 400 {
		t.Fatal("wrong role accepted")
	}
	deniedCookie, deniedCSRF := loginForTest(t, handler, "denied", "correct horse battery")
	if res := post(base, deniedCookie, deniedCSRF); res.Code != http.StatusForbidden {
		t.Fatalf("assignment permission status %d", res.Code)
	}
	if res := post(base, cookie, ""); res.Code != http.StatusForbidden {
		t.Fatalf("CSRF status %d", res.Code)
	}
	res := post(base, cookie, csrf)
	if res.Code != http.StatusCreated {
		t.Fatalf("create %d %s", res.Code, res.Body.String())
	}
	var stream store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &stream); err != nil {
		t.Fatal(err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || stream.SelectedEncoderID != "ui188-encoder" || stream.SelectedWorkerID != "ui188-worker" || stream.AssignedEncoderID != "" || stream.AssignedWorkerID != "" || len(assignments) != 0 {
		t.Fatal("atomic two-role selections were not saved without claiming execution")
	}
	for _, id := range []string{"ui188-encoder", "ui188-worker"} {
		service, err := auth.GetService(t.Context(), id)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("saving selections claimed a runtime owner")
		}
		if _, err := auth.AssignServiceToStream(t.Context(), id, stream.ID, "runtime-fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	if res = post(base, cookie, csrf); res.Code != http.StatusCreated {
		t.Fatalf("shared Node selection create status %d: %s", res.Code, res.Body.String())
	}
	var shared store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &shared); err != nil {
		t.Fatal(err)
	}
	sharedAssignments, err := auth.ListStreamAssignments(t.Context(), shared.ID)
	if err != nil || shared.ID == stream.ID || shared.SelectedEncoderID != stream.SelectedEncoderID || shared.SelectedWorkerID != stream.SelectedWorkerID || shared.AssignedEncoderID != "" || shared.AssignedWorkerID != "" || len(sharedAssignments) != 0 {
		t.Fatal("second frame did not save shared selections independently of runtime ownership")
	}
	for _, id := range []string{"ui188-encoder", "ui188-worker"} {
		service, err := auth.GetService(t.Context(), id)
		if err != nil || service.CurrentStreamID != stream.ID {
			t.Fatal("saving another frame changed the active runtime owner")
		}
	}
	assignments, err = auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(assignments) != 2 || assignedServiceID(assignments, "encoder_recorder") != "ui188-encoder" || assignedServiceID(assignments, "worker") != "ui188-worker" {
		t.Fatal("saving another frame changed the active assignments")
	}
	rows, err := streams.ListStreams(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatal("shared selections did not persist exactly two complete frames")
	}
}

func TestUI188StopReceiptMatchesExactSourceVersion(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "reader"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "UI188 receipt")
	if err != nil {
		t.Fatal(err)
	}
	source := "2026-10-12T00:00:00Z"
	if err = streams.RecordNormalStopReceipt(t.Context(), stream.ID, source, stream.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth))
	cookie, _ := loginForTest(t, handler, "reader", "correct horse battery")
	for _, testCase := range []struct {
		status, version string
		confirmed       bool
	}{{"ready", source, true}, {"completed", source, true}, {"ready", "2026-10-12T00:00:01Z", false}, {"live", source, true}, {"stopping", source, true}, {"failed", source, true}} {
		_, _ = streams.UpdateStreamStatus(t.Context(), stream.ID, testCase.status)
		req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/stop-outcome?expected_updated_at="+testCase.version, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("read receipt %d", res.Code)
		}
		var value struct {
			Confirmed bool `json:"stop_confirmed"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &value)
		if value.Confirmed != testCase.confirmed {
			t.Fatalf("status=%s matched=%t", testCase.status, value.Confirmed)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/stop-outcome?expected_updated_at="+source, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), "unauthenticated") && res.Code < 400 {
		t.Fatal("unauthenticated receipt read accepted")
	}
}
