package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type activationFixture struct{ ready, failed bool }

func (a *activationFixture) Ready() bool           { return a.ready }
func (a *activationFixture) ProbeReady() bool      { return !a.failed }
func (a *activationFixture) UpdateJobID() string   { return "pending-job" }
func (a *activationFixture) UpdateAgentID() string { return "host-agent" }

func TestNodeSelectionActivationBlocksApplicationBeforeSuccess(t *testing.T) {
	gate := &activationFixture{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := withNodeSelectionActivation(next, gate)
	for _, route := range []struct{ method, path string }{
		{"GET", "/admin"}, {"GET", "/services/runtime-config"},
		{"POST", "/streams"}, {"PATCH", "/streams/frame/settings"},
		{"POST", "/services/stream-events"}, {"POST", "/services/streams/frame/start"},
		{"POST", "/system-updates"}, {"POST", "/services/update-jobs/another-job/report"},
		{"POST", "/services/host-agent/runtime-identity/activate"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("pending activation accepted application request: got %d", response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") == "" {
				t.Fatal("hold response must be non-cacheable and retryable")
			}
		})
	}
	gate.ready = true
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/streams", nil))
	if response.Code != http.StatusNoContent {
		t.Fatal("activated application was not released")
	}
}

func TestNodeSelectionActivationKeepsExactRecoveryProtocol(t *testing.T) {
	gate := &activationFixture{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != r.Header.Get("Expected-Body") {
				t.Fatal("recovery request body changed")
			}
		}
		if r.URL.Path != "/health" && r.URL.Path != "/updater/version" && r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Fatal("existing authentication was not preserved")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := withNodeSelectionActivation(next, gate)
	for _, request := range []struct {
		path, body string
		want       int
	}{
		{"/services/update-jobs/pending-job/report", `{}`, 204},
		{"/services/update-jobs/pending-job/mutation-grants", `{}`, 204},
		{"/services/update-jobs/pending-job/mutation-grants/consume", `{}`, 204},
		{"/services/update-jobs/claim", `{"active_job_id":"pending-job"}`, 204},
		{"/services/update-jobs/claim", `{"active_job_id":"another-job"}`, 503},
		{"/services/update-jobs/claim", `{}`, 503},
		{"/services/update-jobs/claim", `{"active_job_id":"pending-job"} {}`, 503},
		{"/services/register", `{"service_id":"host-agent","service_type":"update_agent"}`, 204},
		{"/services/register", `{"service_id":"host-agent","service_type":"encoder_recorder"}`, 503},
		{"/services/heartbeat", `{"service_id":"host-agent"}`, 204},
		{"/services/heartbeat", `{"service_id":"encoder"}`, 503},
		{"/services/host-agent/policy", `{}`, 204},
	} {
		t.Run(request.path+request.body, func(t *testing.T) {
			req := httptest.NewRequest("POST", request.path, strings.NewReader(request.body))
			req.Header.Set("Authorization", "Bearer synthetic")
			req.Header.Set("Expected-Body", request.body)
			req.Header.Set("X-AutoStream-Contract-Major", "2")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != request.want {
				t.Fatalf("recovery gate returned %d; want %d", res.Code, request.want)
			}
			if res.Code == 503 && strings.HasPrefix(request.path, "/services/update-jobs/") && (res.Header().Get("X-AutoStream-Contract-Major") != "2" || res.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("recovery rejection lost v2 confirmation")
			}
		})
	}
	for _, path := range []string{"/health", "/updater/version"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("GET", path, nil))
		if res.Code != 204 {
			t.Fatal("pending target cannot be probed")
		}
		gate.failed = true
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("GET", path, nil))
		if res.Code != 503 {
			t.Fatal("failed or unknown update remained healthy")
		}
		gate.failed = false
	}
}

func TestNodeSelectionActivationDefersFirstBackgroundWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	started := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAfterNodeSelectionActivation(ctx, ready, func(context.Context) { started <- struct{}{} })
	}()
	select {
	case <-started:
		t.Fatal("background work ran before activation")
	case <-time.After(10 * time.Millisecond):
	}
	close(ready)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("activation did not release background work")
	}
	<-done
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	runAfterNodeSelectionActivation(cancelled, make(chan struct{}), func(context.Context) { t.Fatal("shutdown started a held worker") })
}
