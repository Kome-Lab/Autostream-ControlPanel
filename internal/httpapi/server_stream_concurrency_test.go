package httpapi

import (
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
