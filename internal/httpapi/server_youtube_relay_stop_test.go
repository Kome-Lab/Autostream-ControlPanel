package httpapi

import (
	"context"
	"errors"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartStreamRelayStaticDispatchFailureRequiresRecoveryWithoutProviderCleanup(t *testing.T) {
	youtubeLive := &fakeYouTubeLiveClient{
		relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: "youtube-live-stream-dispatch"},
	}
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStart: true}, youtubeLive)

	res := fixture.start(t)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "service_dispatch_failed") {
		t.Fatalf("dispatch failure status=%d body=%s", res.Code, res.Body.String())
	}
	if fixture.youtubeLive.relayStaticPrepareCalls != 1 || fixture.dispatcher.startCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("unconfirmed static dispatch must never clean up or complete provider state: youtube=%#v dispatcher=%#v", fixture.youtubeLive, fixture.dispatcher)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("static dispatch failure must remove the automatic completion runtime, err=%v", err)
	}
	failed, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("static dispatch failure must terminalize before recovery fencing: stream=%#v err=%v", failed, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("static dispatch failure must leave no due completion retry: due=%#v err=%v", due, err)
	}
	result, err := fixture.server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil || result["attempted"] != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("completion retry must not touch an unconfirmed static dispatch: result=%#v complete_calls=%d err=%v", result, fixture.youtubeLive.completeCalls, err)
	}

	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.PrepareState != store.YouTubeRelayBindingClaimPrepareStatePossiblyPrepared || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_start_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed static start must retain recovery fence: claim=%#v err=%v", claim, err)
	}
}

func TestForceStopRelayStaticDispatchFailureRetainsRecoveryFenceWithoutProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStop: true}, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/force-stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("force stop static dispatch failure status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed force stop must not complete or delete provider state: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed force stop must remain failed: stream=%#v err=%v", stream, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unconfirmed force stop must remove automatic completion runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_force_stop_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed force stop must retain recovery claim: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("unconfirmed force stop must not leave a completion retry: due=%#v err=%v", due, err)
	}
}

func TestStopRelayStaticDispatchFailureRetainsRecoveryFenceWithoutProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, &fakeServiceDispatcher{failStop: true}, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("normal stop static dispatch failure status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.dispatcher.stopCalls != 1 || fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed normal stop must not complete or delete provider state: dispatcher=%#v youtube=%#v", fixture.dispatcher, fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed normal stop must remain failed: stream=%#v err=%v", stream, err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unconfirmed normal stop must remove automatic completion runtime: %v", err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() || claim.LastError != "youtube_relay_static_stop_dispatch_unconfirmed" {
		t.Fatalf("unconfirmed normal stop must retain possible-dispatch recovery claim: claim=%#v err=%v", claim, err)
	}
	due, err := fixture.streams.ListDueStreamYouTubeRuntimes(t.Context(), time.Now().UTC(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("unconfirmed normal stop must not leave a completion retry: due=%#v err=%v", due, err)
	}
}

func TestStopRelayStaticDoesNotNormalizeEncoderNoProcessAfterDispatch(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}
	fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
		{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusNotFound, Code: "stream_not_running", Error: "service returned status 404: stream_not_running"},
		{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
		{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
	}

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
		t.Fatalf("static no-process stop status=%d body=%s", response.Code, response.Body.String())
	}
	if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("unconfirmed static encoder stop must not complete or delete provider state: youtube=%#v", fixture.youtubeLive)
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "failed" {
		t.Fatalf("unconfirmed static encoder stop must remain failed: stream=%#v err=%v", stream, err)
	}
	claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
	if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || !claim.EncoderStopConfirmedAt.IsZero() {
		t.Fatalf("static no-process reply must retain the unconfirmed recovery claim: claim=%#v err=%v", claim, err)
	}
}

func TestPartialRelayStaticStopPersistsEncoderReceiptBeforeAbandon(t *testing.T) {
	for _, tt := range []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "normal stop", path: "stop", wantStatus: http.StatusBadGateway},
		{name: "force stop", path: "force-stop", wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
			if response := fixture.start(t); response.Code != http.StatusOK {
				t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
			}
			fixture.dispatcher.stopResultsOverride = []servicecall.DispatchResult{
				{ServiceID: "encoder_recorder-01", ServiceType: "encoder_recorder", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
				{ServiceID: "worker-01", ServiceType: "worker", Endpoint: "/stop", StatusCode: http.StatusBadGateway, Code: "worker_stop_failed", Success: false},
				{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true},
			}
			req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/"+tt.path, nil)
			req.AddCookie(fixture.cookie)
			req.Header.Set("X-CSRF-Token", fixture.csrf)
			response := httptest.NewRecorder()
			fixture.server.ServeHTTP(response, req)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), `"recovery_required":true`) {
				t.Fatalf("partial %s status=%d body=%s", tt.path, response.Code, response.Body.String())
			}
			claim, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID)
			if err != nil || claim.State != store.YouTubeRelayBindingClaimStateRecoveryRequired || claim.DispatchState != store.YouTubeRelayBindingClaimDispatchStatePossiblyDispatched || claim.EncoderStopConfirmedAt.IsZero() {
				t.Fatalf("partial %s must persist Encoder receipt before abandon: claim=%#v err=%v", tt.path, claim, err)
			}
			if fixture.youtubeLive.completeCalls != 0 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
				t.Fatalf("partial %s must not complete/delete before recovery: youtube=%#v", tt.path, fixture.youtubeLive)
			}
		})
	}
}

func TestForceStopRelayStaticAfterConfirmedStopsCompletesAndReleasesBinding(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	receipts := &relayStaticEncoderStopReceiptRecordingStreamStore{MemoryStreamStore: fixture.streams}
	fixture.server.streams = receipts
	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/force-stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("confirmed force stop status=%d body=%s", response.Code, response.Body.String())
	}
	stream, err := fixture.streams.GetStream(t.Context(), fixture.stream.ID)
	if err != nil || stream.Status != "completed" {
		t.Fatalf("confirmed force stop must transition to completed before static completion: stream=%#v err=%v", stream, err)
	}
	if fixture.youtubeLive.completeCalls != 1 || fixture.youtubeLive.relayStaticCleanupCalls != 0 {
		t.Fatalf("confirmed force stop must use YouTube Complete exactly once: youtube=%#v", fixture.youtubeLive)
	}
	if receipts.encoderStopReceiptCalls != 1 || receipts.lastEncoderStopClaim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("confirmed force stop must persist Encoder Stop receipt before completion: receipts=%#v", receipts)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed force stop must release static runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed force stop must release static claim: %v", err)
	}
}

func TestStopRelayStaticTransitionsCompletedBeforeProviderCompletion(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}
	var completionObservedStatus string
	fixture.youtubeLive.onComplete = func(ctx context.Context, _ ytlive.CompleteRequest) {
		stream, err := fixture.streams.GetStream(ctx, fixture.stream.ID)
		if err != nil {
			t.Fatalf("read static stream during completion: %v", err)
		}
		completionObservedStatus = stream.Status
	}
	receipts := &relayStaticEncoderStopReceiptRecordingStreamStore{MemoryStreamStore: fixture.streams}
	fixture.server.streams = receipts

	req := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/stop", nil)
	req.AddCookie(fixture.cookie)
	req.Header.Set("X-CSRF-Token", fixture.csrf)
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("static stop status=%d body=%s", response.Code, response.Body.String())
	}
	if completionObservedStatus != "completed" || fixture.youtubeLive.completeCalls != 1 {
		t.Fatalf("static stop must mark completed before provider completion: observed=%q youtube=%#v", completionObservedStatus, fixture.youtubeLive)
	}
	if receipts.encoderStopReceiptCalls != 1 || receipts.lastEncoderStopClaim.BroadcastID != "broadcast-static-dispatch" {
		t.Fatalf("normal stop must persist Encoder Stop receipt before completion: receipts=%#v", receipts)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirmed normal stop must release static claim: %v", err)
	}
}

func TestRelayStaticManualAndDueCompletionRequireCompletedStream(t *testing.T) {
	fixture := newRelayStaticStartFixtureForTest(t, nil, nil)
	if response := fixture.start(t); response.Code != http.StatusOK {
		t.Fatalf("start static stream: status=%d body=%s", response.Code, response.Body.String())
	}

	manual := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.stream.ID+"/youtube/complete", nil)
	manual.AddCookie(fixture.cookie)
	manual.Header.Set("X-CSRF-Token", fixture.csrf)
	manualResponse := httptest.NewRecorder()
	fixture.server.ServeHTTP(manualResponse, manual)
	if manualResponse.Code != http.StatusConflict || !strings.Contains(manualResponse.Body.String(), errYouTubeRelayStaticCompletionRequiresCompleted.Error()) {
		t.Fatalf("manual active static completion must be rejected: status=%d body=%s", manualResponse.Code, manualResponse.Body.String())
	}
	if fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("manual active static completion called provider: %#v", fixture.youtubeLive.completeRequest)
	}
	if _, err := fixture.streams.RecordStreamYouTubeRuntimeCompleteFailure(t.Context(), fixture.stream.ID, "youtube_live_api_complete_failed", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.server.CompleteDueYouTubeRuntimes(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["attempted"] != 0 || result["skipped"] != 1 || result["completed"] != 0 || fixture.youtubeLive.completeCalls != 0 {
		t.Fatalf("due active static completion must skip provider and binding release: result=%#v youtube=%#v", result, fixture.youtubeLive)
	}
	if _, err := fixture.streams.GetStreamYouTubeRuntime(t.Context(), fixture.stream.ID); err != nil {
		t.Fatalf("active static completion must retain runtime: %v", err)
	}
	if _, err := fixture.streams.GetStreamYouTubeRelayBindingClaim(t.Context(), fixture.relayBindingID); err != nil {
		t.Fatalf("active static completion must retain claim: %v", err)
	}
}
