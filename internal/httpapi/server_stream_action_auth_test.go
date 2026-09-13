package httpapi

import (
	"bytes"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConcurrentForceStopsClaimOnceBeforeDispatch(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	baseStreams := store.NewMemoryStreamStore()
	transitionGate := &gatedLifecycleTransitionStore{
		StreamStore: baseStreams,
		expected:    "live",
		status:      "failed",
		entered:     make(chan struct{}, 2),
		release:     make(chan struct{}),
	}
	dispatcher := &synchronizedServiceDispatcher{}
	first := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	second := NewServer(transitionGate, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, first, "operator", "correct horse battery")

	stream, err := baseStreams.CreateStream(t.Context(), "concurrent force stop")
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
			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/force-stop", nil)
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
	if codes[http.StatusOK] != 1 || codes[http.StatusAccepted] != 1 {
		t.Fatalf("concurrent force-stop response codes = %#v, want one 200 and one 202", codes)
	}
	if got := dispatcher.StopCalls(); got != 1 {
		t.Fatalf("concurrent force stops dispatched %d times, want 1", got)
	}
	final, err := baseStreams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" {
		t.Fatalf("concurrent force stop final status = %q, want completed after confirmed downstream stop", final.Status)
	}
}

func TestSendWorkerTestEventRequiresAssignedWorker(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "morning stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(&fakeServiceDispatcher{}))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/worker-events/test", bytes.NewBufferString(`{"event_type":"current_time"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body = %s", res.Code, res.Body.String())
	}
}

func TestStreamsRequireAuthentication(t *testing.T) {
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	req := httptest.NewRequest(http.MethodGet, "/streams", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestPermissionDenied(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCSRFFailure(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams", bytes.NewBufferString(`{"name":"blocked"}`))
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}
