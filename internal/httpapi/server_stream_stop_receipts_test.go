package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStopStreamNormalizesOnlyExactAlreadyStoppedDownstreamReceipts(t *testing.T) {
	defaultResults := func() []servicecall.DispatchResult {
		return []servicecall.DispatchResult{
			{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
		}
	}
	withFailure := func(result servicecall.DispatchResult) []servicecall.DispatchResult {
		results := defaultResults()
		for index := range results {
			if results[index].ServiceType == result.ServiceType {
				results[index] = result
				return results
			}
		}
		t.Fatalf("missing default result for service type %q", result.ServiceType)
		return nil
	}

	tests := []struct {
		name             string
		results          []servicecall.DispatchResult
		wantHTTPStatus   int
		wantStreamStatus string
		failure          servicecall.DispatchResult
	}{
		{
			name: "exact stopped receipts complete the dynamic stream",
			results: []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "no_active_stream_job", Error: "service returned status 409: no_active_stream_job"},
				{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
			},
			wantHTTPStatus:   http.StatusOK,
			wantStreamStatus: "completed",
		},
		{
			name:             "encoder starting remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_starting", Error: "service returned status 409: stream_starting"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_starting"},
		},
		{
			name:             "worker stream mismatch remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "stream_id_mismatch", Error: "service returned status 409: stream_id_mismatch"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "worker", StatusCode: http.StatusConflict, Code: "stream_id_mismatch"},
		},
		{
			name:             "encoder different active stream remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_already_running", Error: "service returned status 409: stream_already_running"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_already_running"},
		},
		{
			name:             "authentication failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusUnauthorized, Code: "missing_or_invalid_service_token", Error: "service returned status 401: missing_or_invalid_service_token"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "worker", StatusCode: http.StatusUnauthorized, Code: "missing_or_invalid_service_token"},
		},
		{
			name:             "server failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusInternalServerError, Code: "stop_stream_failed", Error: "service returned status 500: stop_stream_failed"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusInternalServerError, Code: "stop_stream_failed"},
		},
		{
			name:             "transport failure remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", Error: "service request failed"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", Error: "service request failed"},
		},
		{
			name:             "encoder no process with the wrong status remains a failure",
			results:          withFailure(servicecall.DispatchResult{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusConflict, Code: "stream_not_running", Error: "service returned status 409: stream_not_running"}),
			wantHTTPStatus:   http.StatusBadGateway,
			wantStreamStatus: "failed",
			failure:          servicecall.DispatchResult{ServiceType: "encoder_recorder", StatusCode: http.StatusConflict, Code: "stream_not_running"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			dispatcher := &fakeServiceDispatcher{stopResultsOverride: tt.results}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			stream, err := streams.CreateStream(t.Context(), "manual stop receipts")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
			if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.wantHTTPStatus {
				t.Fatalf("stop status=%d want=%d body=%s", response.Code, tt.wantHTTPStatus, response.Body.String())
			}

			stored, err := streams.GetStream(t.Context(), stream.ID)
			if err != nil || stored.Status != tt.wantStreamStatus {
				t.Fatalf("stream status=%q err=%v want=%q", stored.Status, err, tt.wantStreamStatus)
			}

			responseBody := response.Body.String()
			var body struct {
				Dispatch []servicecall.DispatchResult `json:"dispatch"`
			}
			if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
				t.Fatalf("decode stop response: %v", err)
			}
			if tt.wantHTTPStatus == http.StatusOK {
				var encoderStopped, workerStopped bool
				for _, result := range body.Dispatch {
					switch {
					case result.ServiceType == "encoder_recorder" && result.StatusCode == http.StatusNotFound && result.Code == "stream_not_running":
						encoderStopped = result.Success && result.Error == ""
					case result.ServiceType == "worker" && result.StatusCode == http.StatusConflict && result.Code == "no_active_stream_job":
						workerStopped = result.Success && result.Error == ""
					}
				}
				if !encoderStopped || !workerStopped {
					t.Fatalf("exact already-stopped receipts were not normalized: %#v", body.Dispatch)
				}
				return
			}

			if !strings.Contains(responseBody, `"code":"service_dispatch_failed"`) {
				t.Fatalf("unsafe downstream result must preserve service dispatch failure: %s", responseBody)
			}
			for _, result := range body.Dispatch {
				if result.ServiceType == tt.failure.ServiceType && result.StatusCode == tt.failure.StatusCode && result.Code == tt.failure.Code {
					if result.Success {
						t.Fatalf("unsafe downstream result was normalized: %#v", result)
					}
					return
				}
			}
			t.Fatalf("missing unsafe downstream result %#v in %#v", tt.failure, body.Dispatch)
		})
	}
}

func TestStopStreamDoesNotNormalizeEncoderNoProcessWhenRelayClaimLookupFails(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := &failingRelayBindingClaimLookupStreamStore{MemoryStreamStore: store.NewMemoryStreamStore(), err: errors.New("relay binding lookup unavailable")}
	dispatcher := &fakeServiceDispatcher{stopResultsOverride: []servicecall.DispatchResult{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/streams/stream/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
		{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusConflict, Code: "no_active_stream_job", Error: "service returned status 409: no_active_stream_job"},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/jobs/stream/stop", StatusCode: http.StatusAccepted, Success: true},
	}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "manual stop claim lookup failure")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"service_dispatch_failed"`) {
		t.Fatalf("claim lookup failure must retain encoder dispatch failure: status=%d body=%s", response.Code, response.Body.String())
	}
	stored, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("claim lookup failure must leave stream failed: stream=%#v err=%v", stored, err)
	}
}

func TestStopStreamCompletesAfterDownstreamCancelsRequestContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	dispatcher := &cancelingRequestStopDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "auto stop detached context")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  "discord-config-auto-stop",
		AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
	}); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	dispatcher.cancelRequest = cancelRequest
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/stop", nil).WithContext(requestCtx)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("stop status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopContextCancelled {
		t.Fatal("downstream stop inherited the cancelled requester context")
	}
	completed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "ready" {
		t.Fatalf("stop after downstream request cancellation left status %q, want ready", completed.Status)
	}
	allStreams, err := streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var waitingCount int
	for _, candidate := range allStreams {
		if candidate.ID != stream.ID && candidate.Status == "created" && candidate.AutoStartTrigger == autoStartTriggerDiscordVoiceJoin {
			waitingCount++
		}
	}
	if waitingCount != 0 || len(allStreams) != 1 || allStreams[0].ID != stream.ID || allStreams[0].Status != "ready" {
		t.Fatalf("stop after downstream request cancellation did not reuse the source stream: %#v", allStreams)
	}
}

func TestForceStopStreamCompletesAfterDownstreamCancelsRequestContext(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	dispatcher := &cancelingRequestStopDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	stream, err := streams.CreateStream(t.Context(), "force stop detached context")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStopServiceTypes...)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	dispatcher.cancelRequest = cancelRequest
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/force-stop", nil).WithContext(requestCtx)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("force-stop status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.stopContextCancelled {
		t.Fatal("force-stop downstream stop inherited the cancelled requester context")
	}
	failed, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "completed" {
		t.Fatalf("force-stop after downstream request cancellation left status %q, want completed after confirmed downstream stop", failed.Status)
	}
}
